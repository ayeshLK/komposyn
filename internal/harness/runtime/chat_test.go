// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	openaioauth "github.com/ayeshLK/komposyn/internal/harness/auth/openai"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	p "github.com/ayeshLK/komposyn/plugins/model-provider"
)

type fakeProvider struct {
	id           p.ID
	mu           sync.Mutex
	requests     []p.ChatRequest
	stream       func(context.Context, p.ChatRequest, func(string)) (p.Result, error)
	authenticate func(context.Context, p.Credential) (p.AuthResult, error)
	started      chan struct{}
}

func (f *fakeProvider) ID() p.ID                    { return f.id }
func (f *fakeProvider) AuthMethods() []p.AuthMethod { return []p.AuthMethod{p.AuthAPIKey} }

func (f *fakeProvider) Authenticate(ctx context.Context, credential p.Credential) (p.AuthResult, error) {
	if f.authenticate != nil {
		return f.authenticate(ctx, credential)
	}
	return p.AuthResult{Provider: f.id}, nil
}
func (f *fakeProvider) ListModels(context.Context, p.Credential) ([]p.Model, error) {
	return []p.Model{{ID: "model-a", DisplayName: "Model A", Compatibility: p.CompatibilitySupported}, {ID: "model-b", DisplayName: "Model B", Compatibility: p.CompatibilitySupported}}, nil
}
func (f *fakeProvider) StreamChat(ctx context.Context, r p.ChatRequest, delta func(string)) (p.Result, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.stream != nil {
		return f.stream(ctx, r, delta)
	}
	if delta != nil {
		delta("answer")
	}
	return p.Result{RequestID: r.RequestID, ModelID: r.ModelID, Outcome: p.OutcomeCompleted, Text: "answer", Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"answer"}`)}, Usage: p.Usage{Known: true, InputTokens: 2, OutputTokens: 1}}, nil
}
func (f *fakeProvider) captured() []p.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]p.ChatRequest(nil), f.requests...)
}

func makeChat(t *testing.T, fp *fakeProvider) (*Chat, auth.Store) {
	t.Helper()
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	registry, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChat(registry, store)
	if err := chat.AddProfile(context.Background(), "profile", string(fp.id), "Test profile", "secret-key", nil); err != nil {
		t.Fatal(err)
	}
	return chat, store
}
func selectFirst(t *testing.T, c *Chat) {
	t.Helper()
	d := c.Discover(context.Background())
	if len(d.Models) < 1 {
		t.Fatalf("discovery=%+v", d)
	}
	if err := c.Select(context.Background(), d.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialOperationBlocksChatAndModelChanges(t *testing.T) {
	fp := &fakeProvider{id: p.OpenAI}
	c, _ := makeChat(t, fp)
	selectFirst(t, c)
	c.mu.Lock()
	c.credentialBusy = true
	c.mu.Unlock()
	if _, err := c.Send(context.Background(), "must wait", nil); !errors.Is(err, ErrRequestActive) {
		t.Fatalf("send during credential operation error=%v", err)
	}
	selected := c.Snapshot().Selected
	if err := c.Select(context.Background(), selected.Key(), false); !errors.Is(err, ErrRequestActive) {
		t.Fatalf("selection during credential operation error=%v", err)
	}
}

func TestTemporaryMultiMessageChatAndConfirmedModelReset(t *testing.T) {
	fp := &fakeProvider{id: p.OpenAI}
	c, _ := makeChat(t, fp)
	selectFirst(t, c)
	if _, err := c.Send(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
	requests := fp.captured()
	if len(requests) != 2 || len(requests[0].Messages) != 1 || len(requests[1].Messages) != 3 {
		t.Fatalf("request histories: %+v", requests)
	}
	snap := c.Snapshot()
	if len(snap.Turns) != 2 || snap.Selected == nil {
		t.Fatalf("snapshot=%+v", snap)
	}
	d := c.Discover(context.Background())
	var second string
	for _, m := range d.Models {
		if m.ID == "model-b" {
			second = m.Key()
		}
	}
	if err := c.Select(context.Background(), second, false); !errors.Is(err, ErrConversationReset) {
		t.Fatalf("unconfirmed change err=%v", err)
	}
	if len(c.Snapshot().Turns) != 2 {
		t.Fatal("canceling model change should preserve chat")
	}
	if err := c.Select(context.Background(), second, true); err != nil {
		t.Fatal(err)
	}
	snap = c.Snapshot()
	if len(snap.Turns) != 0 || snap.Selected.ID != "model-b" {
		t.Fatalf("confirmed change did not start fresh: %+v", snap)
	}
}

func TestCanceledCredentialValidationDoesNotPersist(t *testing.T) {
	for _, operation := range []string{"add", "replace"} {
		t.Run(operation, func(t *testing.T) {
			fp := &fakeProvider{id: p.OpenAI}
			c, store := makeChat(t, fp)
			entered := make(chan struct{})
			release := make(chan struct{})
			fp.authenticate = func(ctx context.Context, _ p.Credential) (p.AuthResult, error) {
				close(entered)
				<-release
				return p.AuthResult{Provider: fp.id}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				if operation == "add" {
					result <- c.AddProfile(ctx, "pending", string(fp.id), "Pending", "new-key", nil)
					return
				}
				result <- c.ReplaceProfile(ctx, "profile", "Test profile", "new-key", nil, false)
			}()
			<-entered
			cancel()
			close(release)
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context canceled", err)
			}
			if operation == "add" {
				if _, _, err := store.Resolve("pending"); err == nil {
					t.Fatal("canceled add persisted a profile")
				}
			} else {
				_, key, err := store.Resolve("profile")
				if err != nil || key != "secret-key" {
					t.Fatalf("canceled replacement changed the credential: key=%q err=%v", key, err)
				}
			}
		})
	}
}

type rotatingOAuthResolver struct {
	store auth.Store
	mu    sync.Mutex
	count int
}

func (r *rotatingOAuthResolver) Provider() string    { return "openai" }
func (r *rotatingOAuthResolver) Method() auth.Method { return auth.MethodOAuth }

func (r *rotatingOAuthResolver) Resolve(_ context.Context, id string) (auth.Profile, string, error) {
	p, err := r.store.Get(id)
	if err != nil {
		return auth.Profile{}, "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	return p, fmt.Sprintf("access-token-%d", r.count), nil
}

func TestOAuthAccessTokenRefreshKeepsSelectedModelAndChatContext(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "openai-chatgpt-rotation", Provider: "openai", Label: "ChatGPT", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "stable-subject", HostID: hostID, IDToken: "stable-id", AccessToken: "persisted-access", RefreshToken: "persisted-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	plug := &fakeProvider{id: p.OpenAI}
	registry, err := model.NewRegistry(plug)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChat(registry, store)
	if err := chat.authRegistry.Register("openai", auth.MethodOAuth, &rotatingOAuthResolver{store: store}); err != nil {
		t.Fatal(err)
	}
	selected := model.Model{Provider: p.OpenAI, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "chat-model", DisplayName: "Chat Model", Compatibility: p.CompatibilitySupported, ProfileVersion: model.ProfileVersion(profile, "")}
	chat.selected = &selected
	for _, prompt := range []string{"first message", "follow-up after token rotation"} {
		turn, err := chat.Send(context.Background(), prompt, nil)
		if err != nil || turn.Outcome != p.OutcomeCompleted {
			t.Fatalf("send turn=%+v err=%v", turn, err)
		}
	}
	requests := plug.captured()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	if requests[0].Credential.Method != p.AuthOAuth || requests[0].Credential.AccessToken != "access-token-1" || requests[1].Credential.AccessToken != "access-token-2" {
		t.Fatalf("OAuth credentials=%+v %+v", requests[0].Credential, requests[1].Credential)
	}
	if len(requests[1].Messages) != 3 || requests[1].Messages[0].Text != "first message" || len(requests[1].Messages[1].Native) == 0 || requests[1].Messages[2].Text != "follow-up after token rotation" {
		t.Fatalf("conversation context reset across refresh: %+v", requests[1].Messages)
	}
	snapshot := chat.Snapshot()
	if snapshot.Selected == nil || snapshot.Selected.Key() != selected.Key() || len(snapshot.Turns) != 2 {
		t.Fatalf("selection/chat was reset: %+v", snapshot)
	}
}

func TestOAuthIdentityWithoutInferencePermissionCannotStartChat(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "openai-chatgpt-no-permission", Provider: "openai", Label: "ChatGPT", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "stable-subject", HostID: hostID, AccessToken: "identity-only-access", RefreshToken: "identity-only-refresh", TokenType: "Bearer", Scopes: []string{"openid", "email"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{id: p.OpenAI}
	registry, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChat(registry, store)
	oauthManager := openaioauth.New(store)
	defer func() {
		if err := oauthManager.Close(context.Background()); err != nil {
			t.Errorf("close auth manager: %v", err)
		}
	}()
	if err := chat.authRegistry.Register("openai", auth.MethodOAuth, oauthManager); err != nil {
		t.Fatal(err)
	}
	status, err := chat.AuthStatus(context.Background(), "openai", auth.MethodOAuth, profile.ID)
	if err != nil || status.Connection != "permission_required" || len(status.Permissions) != 1 || status.Permissions[0].State != auth.PermissionMissing {
		t.Fatalf("permission status=%+v err=%v", status, err)
	}
	selected := model.Model{Provider: p.OpenAI, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "chat-model", DisplayName: "Chat Model", Compatibility: p.CompatibilitySupported, ProfileVersion: model.ProfileVersion(profile, "")}
	chat.selected = &selected
	turn, err := chat.Send(context.Background(), "must not be sent", nil)
	if err == nil || turn.Outcome != p.OutcomePermission {
		t.Fatalf("no-permission chat turn=%+v err=%v", turn, err)
	}
	if got := len(provider.captured()); got != 0 {
		t.Fatalf("identity-only account started inference %d times", got)
	}
}

type waitingOAuthResolver struct{ started chan struct{} }

func (waitingOAuthResolver) Provider() string    { return "openai" }
func (waitingOAuthResolver) Method() auth.Method { return auth.MethodOAuth }

func (r waitingOAuthResolver) Resolve(ctx context.Context, _ string) (auth.Profile, string, error) {
	close(r.started)
	<-ctx.Done()
	return auth.Profile{}, "", ctx.Err()
}

func TestCanceledChatRefreshDoesNotStartInferenceOrAppendTurn(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "openai-chatgpt-cancel-refresh", Provider: "openai", Label: "ChatGPT", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "stable-subject", HostID: hostID, IDToken: "validated-id", AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	plug := &fakeProvider{id: p.OpenAI}
	registry, err := model.NewRegistry(plug)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChat(registry, store)
	started := make(chan struct{})
	if err := chat.authRegistry.Register("openai", auth.MethodOAuth, waitingOAuthResolver{started: started}); err != nil {
		t.Fatal(err)
	}
	selected := model.Model{Provider: p.OpenAI, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "chat-model", DisplayName: "Chat Model", Compatibility: p.CompatibilitySupported, ProfileVersion: model.ProfileVersion(profile, "")}
	chat.selected = &selected
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sendDone := make(chan error, 1)
	go func() {
		_, sendErr := chat.Send(ctx, "prompt", nil)
		sendDone <- sendErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("OAuth resolution did not start")
	}
	cancel()
	select {
	case err := <-sendDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled chat error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled chat kept waiting for refresh completion")
	}
	if got := len(plug.captured()); got != 0 {
		t.Fatalf("canceled request started inference %d times", got)
	}
	snapshot := chat.Snapshot()
	if snapshot.Active || len(snapshot.Turns) != 0 {
		t.Fatalf("canceled OAuth wait produced stale chat state: %+v", snapshot)
	}
}

type blockingStoreResolver struct {
	store   auth.Store
	started chan struct{}
	release chan struct{}
}

func (blockingStoreResolver) Provider() string    { return "openai" }
func (blockingStoreResolver) Method() auth.Method { return auth.MethodOAuth }

func (r blockingStoreResolver) Resolve(ctx context.Context, id string) (auth.Profile, string, error) {
	profile, err := r.store.RefreshOAuthContext(ctx, id, time.Now, func(auth.OAuthCredential) (auth.OAuthTokens, error) {
		close(r.started)
		<-r.release
		return auth.OAuthTokens{AccessToken: "rotated-access", RefreshToken: "rotated-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	if err != nil {
		return auth.Profile{}, "", err
	}
	return profile, profile.Auth.OAuth.AccessToken, nil
}

func TestSnapshotAndCancelStayResponsiveDuringAuthStoreLockWait(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "openai-chatgpt-blocked-store", Provider: "openai", Label: "ChatGPT", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "stable-subject", HostID: hostID, IDToken: "validated-id", AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(-time.Minute)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	plug := &fakeProvider{id: p.OpenAI}
	registry, err := model.NewRegistry(plug)
	if err != nil {
		t.Fatal(err)
	}
	resolver := blockingStoreResolver{store: store, started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	releaseRefresh := func() { releaseOnce.Do(func() { close(resolver.release) }) }
	t.Cleanup(releaseRefresh)
	chat := NewChat(registry, store)
	if err := chat.authRegistry.Register("openai", auth.MethodOAuth, resolver); err != nil {
		t.Fatal(err)
	}
	selected := model.Model{Provider: p.OpenAI, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "chat-model", DisplayName: "Chat Model", Compatibility: p.CompatibilitySupported, ProfileVersion: model.ProfileVersion(profile, "")}
	chat.selected = &selected
	refreshDone := make(chan error, 1)
	go func() {
		_, _, refreshErr := resolver.Resolve(context.Background(), profile.ID)
		refreshDone <- refreshErr
	}()
	select {
	case <-resolver.started:
	case <-time.After(2 * time.Second):
		releaseRefresh()
		t.Fatal("background refresh did not acquire the auth-file lock")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sendDone := make(chan error, 1)
	go func() {
		_, sendErr := chat.Send(ctx, "prompt", nil)
		sendDone <- sendErr
	}()
	deadline := time.Now().Add(time.Second)
	for !chat.Snapshot().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !chat.Snapshot().Active {
		cancel()
		releaseRefresh()
		t.Fatal("chat request did not register before waiting on credential storage")
	}
	snapshotDone := make(chan struct{})
	go func() {
		_ = chat.Snapshot()
		close(snapshotDone)
	}()
	select {
	case <-snapshotDone:
	case <-time.After(100 * time.Millisecond):
		cancel()
		releaseRefresh()
		t.Fatal("Snapshot blocked behind the auth-file lock")
	}
	chat.Cancel()
	select {
	case err := <-sendDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled lock waiter error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel did not stop the auth-file lock wait")
	}
	releaseRefresh()
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if got := len(plug.captured()); got != 0 {
		t.Fatalf("canceled lock waiter started inference %d times", got)
	}
	if snapshot := chat.Snapshot(); snapshot.Active || len(snapshot.Turns) != 0 {
		t.Fatalf("canceled lock waiter changed chat state: %+v", snapshot)
	}
}

func TestCancellationBlocksLateDeltasAndPreservesSafeHistory(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	fp := &fakeProvider{id: p.OpenAI, started: started}
	fp.stream = func(ctx context.Context, r p.ChatRequest, delta func(string)) (p.Result, error) {
		if r.RequestID != "chat-1" {
			t.Fatalf("request ID=%s", r.RequestID)
		}
		started <- struct{}{}
		<-ctx.Done()
		if delta != nil {
			delta("late")
		}
		<-release
		return p.Result{}, &p.Error{Outcome: p.OutcomeInterrupted}
	}
	c, _ := makeChat(t, fp)
	selectFirst(t, c)
	done := make(chan Turn, 1)
	go func() {
		turn, _ := c.Send(context.Background(), "unfinished", func(string) { t.Error("late delta reached UI") })
		done <- turn
	}()
	<-started
	if err := c.Select(context.Background(), "anything", false); !errors.Is(err, ErrRequestActive) {
		t.Fatalf("active model change err=%v", err)
	}
	c.Cancel()
	close(release)
	turn := <-done
	if turn.Outcome != p.OutcomeInterrupted {
		t.Fatalf("turn=%+v", turn)
	}
	snap := c.Snapshot()
	if snap.Active || len(snap.Turns) != 1 || len(c.history) != 0 {
		t.Fatalf("unsafe canceled state: %+v history=%+v", snap, c.history)
	}
	fp.stream = nil
	if _, err := c.Send(context.Background(), "next", nil); err != nil {
		t.Fatal(err)
	}
	requests := fp.captured()
	last := requests[len(requests)-1]
	if len(last.Messages) != 1 || last.Messages[0].Text != "next" {
		t.Fatalf("canceled turn contaminated next request: %+v", last.Messages)
	}
}

func TestRequestFailureIsSanitizedAndNotReplayed(t *testing.T) {
	fp := &fakeProvider{id: p.Anthropic}
	fp.stream = func(context.Context, p.ChatRequest, func(string)) (p.Result, error) {
		return p.Result{}, &p.Error{Outcome: p.OutcomeAuthentication}
	}
	c, _ := makeChat(t, fp)
	selectFirst(t, c)
	turn, err := c.Send(context.Background(), "private prompt", nil)
	if err == nil || turn.Outcome != p.OutcomeAuthentication || turn.Message != "provider rejected the credential" {
		t.Fatalf("failure=%+v %v", turn, err)
	}
	if len(c.history) != 0 {
		t.Fatal("failed turn appended to provider history")
	}
	if got := c.Snapshot().Turns[0].Message; got == "" || got == "secret-key" || got == "private prompt" {
		t.Fatalf("unsafe user-visible failure: %q", got)
	}
}

func TestProfileReplacementDuringActiveRequestIsRejected(t *testing.T) {
	started := make(chan struct{}, 1)
	fp := &fakeProvider{id: p.OpenAI, started: started}
	wait := make(chan struct{})
	fp.stream = func(ctx context.Context, r p.ChatRequest, d func(string)) (p.Result, error) {
		started <- struct{}{}
		<-wait
		return p.Result{Outcome: p.OutcomeInterrupted}, nil
	}
	c, _ := makeChat(t, fp)
	selectFirst(t, c)
	done := make(chan struct{})
	go func() { _, _ = c.Send(context.Background(), "working", nil); close(done) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := c.ReplaceProfile(ctx, "profile", "Test", "other-secret", nil, true)
	if !errors.Is(err, ErrRequestActive) {
		t.Fatalf("replace error=%v", err)
	}
	close(wait)
	<-done
}
