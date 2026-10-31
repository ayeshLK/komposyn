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

package tview

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	harnessruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	pluginui "github.com/ayeshLK/komposyn/plugins/ui"
	"github.com/gdamore/tcell/v2"
	tv "github.com/rivo/tview"
)

func TestIssue164LoginSuccessAndDenialThroughRuntimeAdapterAndScreen(t *testing.T) {
	for _, mode := range []string{"success", "denied", "no-permission"} {
		t.Run(mode, func(t *testing.T) {
			store := auth.Store{Path: t.TempDir() + "/auth.json"}
			provider := &acceptanceProvider{}
			coordinator := &acceptanceCoordinator{store: store, mode: mode}
			client := newAcceptanceClient(t, store, provider, coordinator)
			u, screen, _, finished := startAcceptanceTerminal(t, client)
			dispatch := make(chan struct{})
			u.app.QueueUpdateDraw(func() { u.startOpenAILogin(""); close(dispatch) })
			select {
			case <-dispatch:
			case <-time.After(2 * time.Second):
				t.Fatal("login was not dispatched through the screen event loop")
			}
			if mode == "success" {
				waitForScreen(t, screen, "Acceptance Model", "")
				profiles, err := store.ListMetadata()
				if err != nil || len(profiles) != 1 || profiles[0].ID != "acceptance-account" {
					t.Fatalf("successful login profiles=%+v err=%v", profiles, err)
				}
				if selected := client.Snapshot().Selected; selected != nil {
					t.Fatalf("login automatically selected a model: %+v", selected)
				}
				if provider.chatCalls.Load() != 0 {
					t.Fatalf("login sent %d inference requests", provider.chatCalls.Load())
				}
			} else if mode == "denied" {
				waitForScreen(t, screen, "Sign-in was denied", "")
				profiles, err := store.ListMetadata()
				if err != nil || len(profiles) != 0 {
					t.Fatalf("denied login persisted a candidate: %+v err=%v", profiles, err)
				}
				if provider.chatCalls.Load() != 0 {
					t.Fatalf("denied login sent %d inference requests", provider.chatCalls.Load())
				}
			} else {
				manage := make(chan struct{})
				u.app.QueueUpdateDraw(func() { u.manageProviders(); close(manage) })
				select {
				case <-manage:
				case <-time.After(2 * time.Second):
					t.Fatal("manage providers did not open")
				}
				waitForScreen(t, screen, "permission_required", "")
				profiles, err := client.Profiles(context.Background())
				if err != nil || len(profiles) != 1 || profiles[0].Status != "permission_required" || len(profiles[0].Permissions) != 1 || profiles[0].Permissions[0].State != string(auth.PermissionMissing) {
					t.Fatalf("identity-only profile status=%+v err=%v", profiles, err)
				}
				if selected := client.Snapshot().Selected; selected != nil || provider.chatCalls.Load() != 0 {
					t.Fatalf("identity-only login selected a model or inferred: selected=%+v calls=%d", selected, provider.chatCalls.Load())
				}
			}
			u.stop()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("acceptance terminal did not stop")
			}
		})
	}
}

func TestIssue164AdapterForwardsLoginCompletion(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	client := newAcceptanceClient(t, store, &acceptanceProvider{}, &acceptanceCoordinator{store: store, mode: "success"})
	attempt, err := client.StartLogin(context.Background(), "openai", "oauth", "")
	if err != nil {
		t.Fatal(err)
	}
	for range attempt.Events {
	}
	select {
	case result := <-attempt.Done:
		if result.Err != nil || result.ProfileID != "acceptance-account" || result.Account != "acceptance@example.com" {
			t.Fatalf("adapter login result=%+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UI adapter did not forward completed login")
	}
}

func TestIssue164ReturningLoginAndStaleCompletionKeepTheRightView(t *testing.T) {
	t.Run("reconnect preserves identity and chat", func(t *testing.T) {
		store := auth.Store{Path: t.TempDir() + "/auth.json"}
		profile := addAcceptanceOAuthProfile(t, store)
		provider := &acceptanceProvider{}
		coordinator := &acceptanceCoordinator{store: store, mode: "success"}
		client := newAcceptanceClient(t, store, provider, coordinator)
		discovery, err := client.Discover(context.Background())
		if err != nil || len(discovery.Models) != 1 {
			t.Fatalf("discovery=%+v err=%v", discovery, err)
		}
		if err := client.Select(context.Background(), discovery.Models[0].Key, false); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Send(context.Background(), "keep this context", nil); err != nil {
			t.Fatal(err)
		}
		u, screen, _, finished := startAcceptanceTerminal(t, client)
		dispatch := make(chan struct{})
		u.app.QueueUpdateDraw(func() { u.startOpenAILogin(profile.ID); close(dispatch) })
		select {
		case <-dispatch:
		case <-time.After(2 * time.Second):
			t.Fatal("reconnect was not dispatched")
		}
		waitForScreen(t, screen, "Acceptance Model", "")
		stored, err := store.Get(profile.ID)
		if err != nil || stored.Auth.OAuth.Subject != profile.Auth.OAuth.Subject || stored.Auth.OAuth.ClientID != profile.Auth.OAuth.ClientID || stored.Auth.OAuth.AccessToken != "rotated-access" {
			t.Fatalf("reconnect identity=%+v err=%v", stored.Auth.OAuth, err)
		}
		snapshot := client.Snapshot()
		if snapshot.Selected == nil || snapshot.Selected.ProfileID != profile.ID || len(snapshot.Turns) != 1 || snapshot.Turns[0].Prompt != "keep this context" {
			t.Fatalf("reconnect changed selected context: %+v", snapshot)
		}
		u.stop()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("reconnect terminal did not stop")
		}
	})

	t.Run("late completion after cancel does not hijack picker", func(t *testing.T) {
		store := auth.Store{Path: t.TempDir() + "/auth.json"}
		provider := &acceptanceProvider{}
		coordinator := &acceptanceCoordinator{store: store, mode: "pending", pending: make(chan chan auth.LoginResult, 1)}
		client := newAcceptanceClient(t, store, provider, coordinator)
		u, screen, press, finished := startAcceptanceTerminal(t, client)
		dispatch := make(chan struct{})
		u.app.QueueUpdateDraw(func() { u.startOpenAILogin(""); close(dispatch) })
		select {
		case <-dispatch:
		case <-time.After(2 * time.Second):
			t.Fatal("pending login was not dispatched")
		}
		waitForScreen(t, screen, "Cancel sign-in", "")
		pending := <-coordinator.pending
		press(tcell.KeyEnter, 0)
		deadline := time.Now().Add(2 * time.Second)
		for !coordinator.canceled.Load() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !coordinator.canceled.Load() {
			t.Fatal("cancel action did not reach the registered coordinator")
		}
		pending <- auth.LoginResult{ProfileID: "late-profile", Status: auth.PublicStatus{Connection: "connected", Account: "late@example.com"}}
		close(pending)
		waitForScreen(t, screen, "ChatGPT sign-in canceled.", "")
		time.Sleep(80 * time.Millisecond)
		if got := readScreen(screen); strings.Contains(got, "Sign-in completed for late@example.com") {
			t.Fatalf("stale completion replaced the current view: %q", got)
		}
		if _, err := store.Get("late-profile"); err == nil {
			t.Fatal("stale UI completion persisted a profile")
		}
		u.stop()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("canceled login terminal did not stop")
		}
	})
}

func TestIssue164SelectedCredentialFailureDoesNotFallback(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	if err := store.AddAPIKey("fallback-key", "openai", "Fallback key", "api-key-secret", nil); err != nil {
		t.Fatal(err)
	}
	profile := addAcceptanceOAuthProfile(t, store)
	provider := &acceptanceProvider{}
	coordinator := &acceptanceCoordinator{store: store, mode: "success"}
	client := newAcceptanceClient(t, store, provider, coordinator)
	discovery, err := client.Discover(context.Background())
	if err != nil || len(discovery.Models) != 2 {
		t.Fatalf("initial discovery=%+v err=%v", discovery, err)
	}
	var oauthModel pluginui.Model
	for _, item := range discovery.Models {
		if item.ProfileID == profile.ID {
			oauthModel = item
		}
	}
	if oauthModel.Key == "" {
		t.Fatalf("OAuth model missing from discovery: %+v", discovery)
	}
	if err := client.Select(context.Background(), oauthModel.Key, false); err != nil {
		t.Fatal(err)
	}
	coordinator.failResolution.Store(true)
	turn, err := client.Send(context.Background(), "do not fallback", nil)
	if err == nil || turn.Outcome != modelprovider.OutcomeAuthentication {
		t.Fatalf("failed selected credential turn=%+v err=%v", turn, err)
	}
	if calls := provider.chatCalls.Load(); calls != 0 {
		t.Fatalf("failed OAuth profile silently used another credential for %d requests", calls)
	}
	if selected := client.Snapshot().Selected; selected == nil || selected.ProfileID != profile.ID {
		t.Fatalf("failed operation changed selected profile: %+v", selected)
	}

	coordinator.failResolution.Store(true)
	failedDiscovery, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failedDiscovery.Models) != 1 || failedDiscovery.Models[0].ProfileID != "fallback-key" || len(failedDiscovery.Failures) != 1 || failedDiscovery.Failures[0].ProfileID != profile.ID {
		t.Fatalf("discovery fell back instead of reporting selected profile failure: %+v", failedDiscovery)
	}
}

func TestIssue164ConfirmedDisconnectAndRemoveThroughUIAdapter(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	profile := addAcceptanceOAuthProfile(t, store)
	provider := &acceptanceProvider{}
	coordinator := &acceptanceCoordinator{store: store, mode: "success"}
	client := newAcceptanceClient(t, store, provider, coordinator)
	discovery, err := client.Discover(context.Background())
	if err != nil || len(discovery.Models) != 1 {
		t.Fatalf("discovery=%+v err=%v", discovery, err)
	}
	if err := client.Select(context.Background(), discovery.Models[0].Key, false); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Send(context.Background(), "temporary context", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Disconnect(context.Background(), "openai", "oauth", profile.ID, false, false); !errors.Is(err, pluginui.ErrConversationReset) {
		t.Fatalf("unconfirmed selected-profile disconnect error=%v", err)
	}
	disconnected, err := client.Disconnect(context.Background(), "openai", "oauth", profile.ID, false, true)
	if err != nil || !disconnected.LocalTokensRemoved || disconnected.LocalRegistrationRemoved || disconnected.RemoteRevocationConfirmed {
		t.Fatalf("disconnect result=%+v err=%v", disconnected, err)
	}
	stored, err := store.Get(profile.ID)
	if err != nil || stored.Auth.OAuth.AccessToken != "" || !stored.Auth.OAuth.ReconnectRequired {
		t.Fatalf("disconnect did not retain registration and clear local tokens: %+v err=%v", stored.Auth.OAuth, err)
	}
	if snapshot := client.Snapshot(); snapshot.Selected != nil || len(snapshot.Turns) != 0 {
		t.Fatalf("confirmed disconnect did not clear selected context: %+v", snapshot)
	}

	removalProfile, err := addAcceptanceOAuthProfileValue(store, "remove-account")
	if err != nil {
		t.Fatal(err)
	}
	removed, err := client.Disconnect(context.Background(), "openai", "oauth", removalProfile.ID, true, true)
	if err != nil || !removed.LocalRegistrationRemoved || !removed.LocalTokensRemoved || removed.RemoteRevocationConfirmed {
		t.Fatalf("remove result=%+v err=%v", removed, err)
	}
	if _, err := store.Get(removalProfile.ID); err == nil {
		t.Fatal("confirmed remove retained the local registration")
	}
}

type acceptanceProvider struct{ chatCalls atomic.Int32 }

func (*acceptanceProvider) ID() modelprovider.ID { return modelprovider.OpenAI }
func (*acceptanceProvider) AuthMethods() []modelprovider.AuthMethod {
	return []modelprovider.AuthMethod{modelprovider.AuthAPIKey}
}
func (*acceptanceProvider) Authenticate(context.Context, modelprovider.Credential) (modelprovider.AuthResult, error) {
	return modelprovider.AuthResult{Provider: modelprovider.OpenAI}, nil
}
func (*acceptanceProvider) ListModels(_ context.Context, credential modelprovider.Credential) ([]modelprovider.Model, error) {
	if credential.APIKey == "" && credential.AccessToken == "" {
		return nil, errors.New("credential missing")
	}
	return []modelprovider.Model{{ID: "acceptance-model", DisplayName: "Acceptance Model", Compatibility: modelprovider.CompatibilitySupported}}, nil
}
func (p *acceptanceProvider) StreamChat(_ context.Context, request modelprovider.ChatRequest, _ func(string)) (modelprovider.Result, error) {
	p.chatCalls.Add(1)
	return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Text: "response"}, nil
}

type acceptanceCoordinator struct {
	store          auth.Store
	mode           string
	pending        chan chan auth.LoginResult
	canceled       atomic.Bool
	failResolution atomic.Bool
}

func (*acceptanceCoordinator) Provider() string    { return "openai" }
func (*acceptanceCoordinator) Method() auth.Method { return auth.MethodOAuth }
func (c *acceptanceCoordinator) Resolve(_ context.Context, profileID string) (auth.Profile, string, error) {
	if c.failResolution.Load() {
		return auth.Profile{}, "", auth.ErrOAuthReconnectRequired
	}
	profile, err := c.store.Get(profileID)
	if err != nil || profile.Auth.OAuth == nil {
		return auth.Profile{}, "", errors.New("OAuth profile unavailable")
	}
	if !containsAcceptanceScope(profile.Auth.OAuth.Scopes, "chatgpt.tokens.use.direct") {
		return auth.Profile{}, "", auth.ErrOAuthPermissionRequired
	}
	return profile, profile.Auth.OAuth.AccessToken, nil
}
func (c *acceptanceCoordinator) PublicStatus(ctx context.Context, profileID string) (auth.PublicStatus, error) {
	profile, err := c.store.GetContext(ctx, profileID)
	if err != nil || profile.Auth.OAuth == nil {
		return auth.PublicStatus{}, errors.New("OAuth profile unavailable")
	}
	status := acceptanceStatus(profile)
	if profile.Auth.OAuth.ReconnectRequired {
		status.Connection = "reconnect_required"
	}
	return status, nil
}
func (c *acceptanceCoordinator) StartLogin(_ context.Context, profileID string) (auth.LoginAttempt, error) {
	events := make(chan auth.LoginEvent, 3)
	events <- auth.LoginEvent{Kind: auth.LoginStarted, Message: "Sign-in started"}
	events <- auth.LoginEvent{Kind: auth.LoginBrowser, Message: "Waiting for browser authorization"}
	done := make(chan auth.LoginResult, 1)
	if c.mode == "pending" {
		c.pending <- done
		close(events)
		return auth.LoginAttempt{ID: "acceptance-login", Events: events, Done: done}, nil
	}
	if c.mode == "denied" {
		done <- auth.LoginResult{Err: errors.New("Sign-in was denied")}
		close(done)
		close(events)
		return auth.LoginAttempt{ID: "acceptance-login", Events: events, Done: done}, nil
	}
	var profile auth.Profile
	if profileID == "" {
		var err error
		profile, err = addAcceptanceOAuthProfileValue(c.store, "acceptance-account")
		if err != nil {
			return auth.LoginAttempt{}, err
		}
		if c.mode == "no-permission" {
			profile.Auth.OAuth.Scopes = []string{"openid", "email"}
			if err := c.store.ReplaceOAuth(profile.ID, profile.Revision, profile); err != nil {
				return auth.LoginAttempt{}, err
			}
			profile, err = c.store.Get(profile.ID)
			if err != nil {
				return auth.LoginAttempt{}, err
			}
		}
	} else {
		current, err := c.store.Get(profileID)
		if err != nil {
			return auth.LoginAttempt{}, err
		}
		candidate := current
		credential := *current.Auth.OAuth
		credential.AccessToken = "rotated-access"
		credential.RefreshToken = "rotated-refresh"
		credential.ExpiresAt = time.Now().Add(time.Hour)
		candidate.Auth.OAuth = &credential
		if err := c.store.ReplaceOAuth(profileID, current.Revision, candidate); err != nil {
			return auth.LoginAttempt{}, err
		}
		profile, err = c.store.Get(profileID)
		if err != nil {
			return auth.LoginAttempt{}, err
		}
	}
	status := acceptanceStatus(profile)
	events <- auth.LoginEvent{Kind: auth.LoginCompleted, Message: "Sign-in finished", Succeeded: true}
	done <- auth.LoginResult{Profile: profile, ProfileID: profile.ID, Status: status}
	close(done)
	close(events)
	return auth.LoginAttempt{ID: "acceptance-login", Events: events, Done: done}, nil
}
func (c *acceptanceCoordinator) CancelLogin(string) bool {
	c.canceled.Store(true)
	return true
}
func (c *acceptanceCoordinator) Disconnect(_ context.Context, profileID string, remove bool) (auth.DisconnectResult, error) {
	if remove {
		if err := c.store.Remove(profileID); err != nil {
			return auth.DisconnectResult{}, err
		}
		return auth.DisconnectResult{LocalTokensRemoved: true, LocalRegistrationRemoved: true}, nil
	}
	if err := c.store.ClearOAuthTokens(profileID); err != nil {
		return auth.DisconnectResult{}, err
	}
	return auth.DisconnectResult{LocalTokensRemoved: true}, nil
}
func (*acceptanceCoordinator) OpenUsage(context.Context) error { return nil }

func acceptanceStatus(profile auth.Profile) auth.PublicStatus {
	permission := auth.Permission{ID: "inference.plan", Name: "ChatGPT plan usage", State: auth.PermissionMissing}
	status := auth.PublicStatus{Connection: "permission_required", Account: profile.Auth.OAuth.Email, Permissions: []auth.Permission{permission}}
	if containsAcceptanceScope(profile.Auth.OAuth.Scopes, "chatgpt.tokens.use.direct") {
		status.Connection = "connected"
		status.Permissions[0].State = auth.PermissionGranted
	}
	if profile.Auth.OAuth.ReconnectRequired {
		status.Connection = "reconnect_required"
	}
	return status
}

func containsAcceptanceScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func addAcceptanceOAuthProfile(t *testing.T, store auth.Store) auth.Profile {
	t.Helper()
	profile, err := addAcceptanceOAuthProfileValue(store, "acceptance-account")
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func addAcceptanceOAuthProfileValue(store auth.Store, id string) (auth.Profile, error) {
	hostID, err := store.EnsureHostID()
	if err != nil {
		return auth.Profile{}, err
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		return auth.Profile{}, err
	}
	profile := auth.Profile{ID: id, Provider: "openai", Label: "Acceptance account", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "issued-acceptance-client", Issuer: "https://auth.openai.com", Subject: "acceptance-subject", Email: "acceptance@example.com", HostID: hostID, IDToken: "fake-id-token", AccessToken: "fake-access-token", RefreshToken: "fake-refresh-token", TokenType: "Bearer", Scopes: []string{"openid", "chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		return auth.Profile{}, err
	}
	return profile, nil
}

func newAcceptanceClient(t *testing.T, store auth.Store, provider *acceptanceProvider, coordinator *acceptanceCoordinator) harnessui.Client {
	t.Helper()
	registry, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	chat := harnessruntime.NewChatWithOAuth(registry, store, coordinator)
	return harnessui.Client{Chat: chat, ProfileStore: store}
}

func startAcceptanceTerminal(t *testing.T, client pluginui.Client) (*terminal, *synchronizedSimulation, func(tcell.Key, rune), <-chan error) {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	ctx, cancel := context.WithCancel(context.Background())
	u := &terminal{app: tv.NewApplication(), client: client, ctx: ctx, cancel: cancel, output: newTranscriptView(), input: newComposerInput()}
	u.app.SetScreen(safeScreen)
	u.openPicker()
	finished := make(chan error, 1)
	go func() {
		err := u.app.Run()
		u.runReturned.Store(true)
		finished <- err
		close(finished)
	}()
	waitForScreen(t, safeScreen, "Refresh Models", "")
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		u.stop()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			cancel()
			t.Error("acceptance terminal cleanup timed out")
		}
		cancel()
	})
	return u, safeScreen, press, finished
}

func readScreen(screen *synchronizedSimulation) string {
	cells, _, _ := screen.GetContents()
	var text strings.Builder
	for _, cell := range cells {
		text.WriteString(string(cell.Runes))
	}
	return text.String()
}

var _ modelprovider.Provider = (*acceptanceProvider)(nil)
var _ auth.OAuthCoordinator = (*acceptanceCoordinator)(nil)
