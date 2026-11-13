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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/model-provider/anthropic"
	"github.com/ayeshLK/komposyn/plugins/model-provider/openai"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

type publicationHost struct {
	mu         sync.Mutex
	content    []byte
	reads      int
	rootGen    uint64
	root       string
	target     string
	reference  *ReadExecutionReference
	rejectGate atomic.Bool
	gateCalls  atomic.Int32
}
type publicationAdmission struct {
	metadata FilesystemReadAdmission
	host     *publicationHost
}
type publicationPreparation struct {
	metadata FilesystemReadAdmission
	closed   bool
}

func (h *publicationHost) AdmitFilesystemRead(_ context.Context, invocation tools.Invocation, destination string) (FilesystemReadAdmission, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(invocation.Arguments, &args); err != nil || args.Path == "" {
		return FilesystemReadAdmission{}, errors.New("invalid fixture target")
	}
	return FilesystemReadAdmission{Reference: new(struct{}), ServiceID: "fixture-service", ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, CallID: invocation.ProviderCallID, Root: h.root, Deadline: time.Now().Add(time.Minute), AdmissionBinding: "synthetic-admission", Target: args.Path, Class: "ordinary", Identity: "fixture-object-1", RootGeneration: h.rootGen, PolicyRevision: 1, CoverageRevision: 1, Destination: destination}, nil
}
func (h *publicationHost) PrepareAdmittedFilesystemRead(_ context.Context, admission FilesystemReadAdmission, _ uint64) (FilesystemReadPreparation, error) {
	return &publicationPreparation{metadata: admission}, nil
}
func (h *publicationHost) CloseFilesystemReadAdmission(context.Context, FilesystemReadAdmission) error {
	return nil
}
func (h *publicationHost) ValidateFilesystemRoot(ctx context.Context, root string) (string, error) {
	return CanonicalFilesystemRoot(ctx, root)
}
func (h *publicationHost) InvalidateFilesystemRoot(_ context.Context, transition FilesystemRootTransition) error {
	h.root, h.rootGen = transition.Root, transition.Generation
	return nil
}
func (h *publicationHost) BackendStatus() (bool, string) { return true, "synthetic fixture" }

func (h *publicationHost) WithFilesystemReadPublicationCurrent(ctx context.Context, conversationID, root string, generation, policy, coverage uint64, operation func() error) error {
	h.gateCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.rejectGate.Load() {
		return ErrFilesystemRootStale
	}
	if conversationID == "" || root != h.root || generation != h.rootGen || policy != 1 || coverage != 1 {
		return ErrFilesystemRootStale
	}
	return operation()
}

type blockingPublicationOAuthResolver struct {
	store   auth.Store
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	block   bool
}

func (*blockingPublicationOAuthResolver) Provider() string    { return "openai" }
func (*blockingPublicationOAuthResolver) Method() auth.Method { return auth.MethodOAuth }
func (r *blockingPublicationOAuthResolver) Resolve(ctx context.Context, id string) (auth.Profile, string, error) {
	r.mu.Lock()
	block := r.block
	r.block = false
	r.mu.Unlock()
	if block {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return auth.Profile{}, "", ctx.Err()
		}
	}
	profile, err := r.store.Get(id)
	return profile, "publication-fixture-token", err
}

func newPublicationOAuthChat(t *testing.T, fp *publicationProvider, host *publicationHost, resolver *blockingPublicationOAuthResolver) *Chat {
	t.Helper()
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	registry := auth.NewRegistry()
	if err := registry.Register("openai", auth.MethodOAuth, resolver); err != nil {
		t.Fatal(err)
	}
	config := publicationReadRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), host)
	chat := NewChatWithToolRuntime(providers, resolver.store, registry, config)
	profile, err := resolver.store.Get("profile")
	if err != nil {
		t.Fatal(err)
	}
	selected := model.Model{Provider: provider.OpenAI, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "chat-model", DisplayName: "Chat Model", Compatibility: provider.CompatibilitySupported, ProfileVersion: model.ProfileVersion(profile, "publication-fixture-token")}
	chat.selected = &selected
	host.root = filepath.Dir(fp.target)
	if err := chat.SelectFilesystemRoot(context.Background(), filepath.Dir(fp.target)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chat.Close)
	return chat
}

func TestStage5LaterTurnSensitiveHistoryStillUsesHostPublicationGate(t *testing.T) {
	const sentinel = "later-turn-sensitive-history-sentinel"
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt")}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: []byte(sentinel), root: filepath.Dir(fp.target), target: fp.target}
	config := publicationReadRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), host)
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
	t.Cleanup(chat.Close)
	if turn, err := chat.Send(context.Background(), "read the fixture", nil); err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("initial sensitive turn=%+v err=%v", turn, err)
	}
	if !chat.historySensitive || !strings.Contains(providerMessagesText(chat.history), sentinel) {
		t.Fatal("fixture did not establish sensitive continuation history")
	}
	host.rejectGate.Store(true)
	turn, err := chat.Send(context.Background(), "continue discussing the file", nil)
	if turn.Outcome != provider.OutcomeInterrupted || err == nil {
		t.Fatalf("blocked continuation turn=%+v err=%v; want an interrupted turn with a gate refusal", turn, err)
	}
	if got := len(fp.captured()); got != 2 {
		t.Fatalf("provider accepted %d requests; blocked later turn must not dispatch", got)
	}
	if host.gateCalls.Load() < 3 {
		t.Fatalf("publication gate calls=%d; expected both initial disclosures and later-turn replay", host.gateCalls.Load())
	}
}

func TestStage5InvalidationAfterHistorySnapshotCannotReclassifyDraft(t *testing.T) {
	const sentinel = "retired-snapshot-must-not-be-redisclosed"
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "profile", Provider: "openai", Label: "Fixture", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "fixture-client", Issuer: "https://auth.openai.com", Subject: "fixture-subject", HostID: hostID, IDToken: "synthetic-id", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt")}
	root := filepath.Dir(fp.target)
	host := &publicationHost{content: []byte(sentinel), root: root, target: fp.target}
	resolver := &blockingPublicationOAuthResolver{store: store, started: make(chan struct{}), release: make(chan struct{})}
	chat := newPublicationOAuthChat(t, fp, host, resolver)
	if turn, err := chat.Send(context.Background(), "read the fixture", nil); err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("initial sensitive turn=%+v err=%v", turn, err)
	}
	if !chat.historySensitive || !strings.Contains(providerMessagesText(chat.history), sentinel) {
		t.Fatal("fixture did not establish sensitive continuation history")
	}
	resolver.mu.Lock()
	resolver.block = true
	resolver.mu.Unlock()
	done := make(chan Turn, 1)
	go func() {
		turn, _ := chat.Send(context.Background(), "continue after the snapshot", nil)
		done <- turn
	}()
	select {
	case <-resolver.started:
	case <-time.After(2 * time.Second):
		t.Fatal("send did not pause in credential resolution after taking its history snapshot")
	}
	if _, err := chat.ClearToolGrants(); err != nil {
		t.Fatalf("clear during credential resolution: %v", err)
	}
	if chat.historySensitive || len(chat.history) != 0 {
		t.Fatal("invalidation did not retire committed sensitive history")
	}
	close(resolver.release)
	select {
	case turn := <-done:
		if turn.Outcome != provider.OutcomeInterrupted {
			t.Fatalf("retired draft outcome=%s, want interrupted", turn.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send remained blocked after credential resolution was released")
	}
	requests := fp.captured()
	if len(requests) != 2 {
		t.Fatalf("provider accepted %d requests after invalidation; want only the initial disclosure cycle", len(requests))
	}
	for _, request := range requests {
		if strings.Contains(providerMessagesText(request.Messages), "continue after the snapshot") {
			t.Fatal("stale user draft was dispatched after its sensitive history was retired")
		}
	}
}
func (p *publicationPreparation) Metadata() FilesystemReadAdmission { return p.metadata }
func (p *publicationPreparation) WithAuthorityCommit(ctx context.Context, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit()
}
func (p *publicationPreparation) Close(context.Context) error { p.closed = true; return nil }
func (h *publicationHost) ExecuteAdmittedFilesystemRead(ctx context.Context, admission FilesystemReadAdmission, preparation FilesystemReadPreparation, reference *ReadExecutionReference, authority FilesystemReadExecutionAuthority) (*PrivateReadCapture, error) {
	h.reference = reference
	if err := authority.Claim(); err != nil {
		return nil, err
	}
	var capture *PrivateReadCapture
	err := authority.WithCurrent(func() error {
		h.mu.Lock()
		h.reads++
		content := append([]byte(nil), h.content...)
		h.mu.Unlock()
		var err error
		capture, err = authority.Stage(admission, FilesystemReadExecutionResult{Outcome: tools.OutcomeSucceeded, Bytes: content, ServiceID: admission.ServiceID, AdmissionBinding: admission.AdmissionBinding, AuthorizationBinding: authority.AuthorizationBinding(), Identity: admission.Identity, RootGeneration: admission.RootGeneration, PolicyRevision: admission.PolicyRevision, CoverageRevision: admission.CoverageRevision, Destination: admission.Destination, ReadStarted: true, WorkerJoined: true, CapabilitiesRetired: true})
		return err
	})
	closeErr := preparation.Close(ctx)
	return capture, errors.Join(err, closeErr)
}

func selectPublicationRoot(t *testing.T, chat *Chat, host *publicationHost, root string) {
	t.Helper()
	host.root = root
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatalf("select fixture root: %v", err)
	}
}

func publicationTarget(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "synthetic")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, name)
}

func registerPublicationReadTool(t *testing.T, chat *Chat) {
	t.Helper()
	if chat == nil || chat.toolRuntime == nil {
		t.Fatal("publication fixture has no tool runtime")
	}
	if err := chat.toolRuntime.registry.Register(readFileTool{}); err != nil {
		t.Fatal(err)
	}
}

func publicationReadRuntimeConfig(approval ToolApprovalInteraction, host *publicationHost) ToolRuntimeConfig {
	clarification := ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, errors.New("clarification is not part of this fixture")
	})
	return BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, host)
}

type publicationProvider struct {
	*fakeProvider
	mu2             sync.Mutex
	calls           int
	seen            []string
	blockResponse   bool
	target          string
	responseStarted chan struct{}
	releaseResponse chan struct{}
}

func (p *publicationProvider) RouteCapabilities(method provider.AuthMethod) provider.RouteCapabilities {
	if method == provider.AuthOAuth || method == provider.AuthAPIKey {
		return provider.RouteCapabilities{ClientTools: provider.SupportSupported}
	}
	return provider.RouteCapabilities{ClientTools: provider.SupportUnknown}
}

func (*publicationHost) InvalidateFilesystemReadExecutions(string) error { return nil }
func (*publicationHost) SetFilesystemReadInvalidator(func(string))       {}

func (p *publicationProvider) StreamChat(ctx context.Context, request provider.ChatRequest, delta func(string)) (provider.Result, error) {
	p.fakeProvider.mu.Lock()
	p.fakeProvider.requests = append(p.fakeProvider.requests, request)
	p.fakeProvider.mu.Unlock()
	p.mu2.Lock()
	p.calls++
	call := p.calls
	p.seen = append(p.seen, providerMessagesText(request.Messages))
	p.mu2.Unlock()
	if call == 1 {
		args, _ := json.Marshal(map[string]string{"path": p.target})
		nativeArgs, _ := json.Marshal(string(args))
		raw := json.RawMessage(`{"type":"function_call","id":"item-read-1","call_id":"read-1","name":"read_file","arguments":` + string(nativeArgs) + `,"status":"completed"}`)
		return provider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted, Completion: provider.CompletionToolCalls, Text: "Reading.", Native: []json.RawMessage{raw}, ToolCalls: []provider.ToolCall{{CallID: "read-1", ToolID: tools.ID("read_file"), Arguments: args}}}, nil
	}
	if p.blockResponse {
		close(p.responseStarted)
		<-p.releaseResponse
	}
	if delta != nil {
		delta("derived answer")
	}
	return provider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted, Completion: provider.CompletionFinal, Text: "derived answer", Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"derived answer"}`)}, Usage: provider.Usage{Known: true, InputTokens: 4, OutputTokens: 2}}, nil
}
func providerMessagesText(messages []provider.Message) string {
	b, _ := json.Marshal(messages)
	return string(b)
}

func TestStage5ReadPublicationAndSameContextReplay(t *testing.T) {
	const sentinel = "stage5-private-synthetic-sentinel"
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt")}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	host := &publicationHost{content: []byte(sentinel), root: filepath.Dir(fp.target), target: fp.target}
	var pendingApproval permissions.PendingApproval
	config := publicationReadRuntimeConfig(ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		pendingApproval = pending
		return permissions.ApprovalOnce, "", nil
	}), host)
	chat := NewChatWithToolRuntime(providers, store, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
	t.Cleanup(chat.Close)
	turn, err := chat.Send(context.Background(), "summarize the file", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("first turn=%+v err=%v", turn, err)
	}
	requests := fp.captured()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	if strings.Contains(providerMessagesText(requests[0].Messages), sentinel) {
		t.Fatal("private bytes appeared before the read completed")
	}
	if !strings.Contains(providerMessagesText(requests[1].Messages), sentinel) {
		t.Fatal("approved capture was not disclosed at provider handoff")
	}
	if len(turn.ToolActivities) == 0 || !strings.Contains(turn.ToolActivities[0].Message, "disclosed at provider handoff") {
		t.Fatalf("activity did not distinguish disclosure commitment: %+v", turn.ToolActivities)
	}
	host.mu.Lock()
	reads := host.reads
	host.mu.Unlock()
	if reads != 1 {
		t.Fatalf("read executions=%d", reads)
	}
	if len(pendingApproval.Request.Requirements) != 2 || pendingApproval.Request.Requirements[1].Resource == nil || pendingApproval.Request.Requirements[1].Resource.Target != fp.target || !strings.Contains(pendingApproval.Request.Requirements[0].Description, "send its contents to openai/") || !strings.Contains(pendingApproval.Request.Requirements[0].Description, "using auth profile profile") || !strings.Contains(pendingApproval.Request.Requirements[1].Description, "may include credentials or sensitive data") || !strings.Contains(pendingApproval.Request.Requirements[1].Description, "may recur") || !strings.Contains(pendingApproval.Request.Requirements[1].Description, "one read only") {
		t.Fatalf("approval omitted exact target/destination/disclosure: %+v", pendingApproval.Request)
	}
	if !chat.historySensitive || !strings.Contains(providerMessagesText(chat.history), sentinel) {
		t.Fatal("committed sensitive continuation missing from in-memory history")
	}
	if turn, err = chat.Send(context.Background(), "explain further", nil); err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("replay turn=%+v err=%v", turn, err)
	}
	requests = fp.captured()
	if len(requests) != 3 || !strings.Contains(providerMessagesText(requests[2].Messages), sentinel) {
		t.Fatal("same-context replay did not use committed bytes")
	}
	host.mu.Lock()
	reads = host.reads
	host.mu.Unlock()
	if reads != 1 {
		t.Fatalf("context replay repeated read: %d", reads)
	}
	if err := chat.RevokeReadExecution(host.reference); err != nil {
		t.Fatalf("revoke consumed read: %v", err)
	}
	if chat.historySensitive || len(chat.history) != 0 {
		t.Fatal("consumed-operation revocation did not retire continuation")
	}
	for _, turn := range chat.Snapshot().Turns {
		if strings.Contains(turn.Text, sentinel) {
			t.Fatal("retired derived text remained in snapshot")
		}
	}
}

func TestStage5EmptyReadPublishesAndReplaysInSameContext(t *testing.T) {
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "empty.txt")}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{root: filepath.Dir(fp.target), target: fp.target}
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	registerPublicationReadTool(t, chat)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
	t.Cleanup(chat.Close)
	turn, err := chat.Send(context.Background(), "summarize the empty file", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("first turn=%+v err=%v", turn, err)
	}
	if !chat.historySensitive || len(chat.toolRuntime.readContinuations) != 1 {
		t.Fatal("empty disclosure provenance was rejected")
	}
	requests := fp.captured()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	body := providerMessagesText(requests[1].Messages)
	if !strings.Contains(body, `"CallID":"read-1"`) || !strings.Contains(body, `\"outcome\":\"succeeded\"`) || !strings.Contains(body, `\"content\":\"\"`) {
		t.Fatalf("empty result envelope missing or malformed: %s", body)
	}
	if turn, err = chat.Send(context.Background(), "explain the empty result", nil); err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("replay turn=%+v err=%v", turn, err)
	}
	requests = fp.captured()
	if len(requests) != 3 || !strings.Contains(providerMessagesText(requests[2].Messages), `"CallID":"read-1"`) {
		t.Fatal("empty read continuation did not replay in its original context")
	}
	host.mu.Lock()
	reads := host.reads
	host.mu.Unlock()
	if reads != 1 {
		t.Fatalf("empty read was re-executed: %d", reads)
	}
}

func TestProductionCatalogDoesNotAdvertiseReadFile(t *testing.T) {
	fp := &fakeProvider{id: provider.OpenAI}
	chat, _ := makeChat(t, fp)
	selectFirst(t, chat)
	t.Cleanup(chat.Close)
	selection := *chat.selected
	if chat.toolRuntime != nil {
		if _, ok := chat.toolRuntime.registry.Lookup(tools.ReadFileID); ok {
			t.Fatal("text-only chat unexpectedly registered read_file")
		}
	}
	if selection.ID == "" {
		t.Fatal("model selection missing")
	}
}

func TestFilesystemReadAvailabilityTracksModelRouteBackendAndRoot(t *testing.T) {
	t.Run("no model selected", func(t *testing.T) {
		chat, _ := makeChat(t, &fakeProvider{id: provider.OpenAI})
		defer chat.Close()
		if got := chat.Snapshot().FilesystemReadAvailability.State; got != "no_model" {
			t.Fatalf("availability state=%q, want no_model", got)
		}
		if got := chat.Snapshot().FilesystemReadAvailability.Reason; !strings.Contains(got, "/model") {
			t.Fatalf("no-model recovery guidance=%q", got)
		}
	})

	t.Run("unsupported provider route", func(t *testing.T) {
		fp := &unsupportedPublicationProvider{publicationProvider: &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}}}
		chat := newAvailabilityChat(t, fp, &publicationHost{root: t.TempDir()})
		defer chat.Close()
		if got := chat.Snapshot().FilesystemReadAvailability.State; got != "unsupported_route" {
			t.Fatalf("availability state=%q, want unsupported_route", got)
		}
		if got := chat.Snapshot().FilesystemReadAvailability.Reason; !strings.Contains(got, "route") {
			t.Fatalf("route recovery guidance=%q", got)
		}
	})

	t.Run("backend unavailable", func(t *testing.T) {
		host := &unavailablePublicationHost{publicationHost: &publicationHost{root: t.TempDir()}}
		chat := newAvailabilityChat(t, &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}}, host)
		defer chat.Close()
		if got := chat.Snapshot().FilesystemReadAvailability.State; got != "backend_unavailable" {
			t.Fatalf("availability state=%q, want backend_unavailable", got)
		}
		if got := chat.Snapshot().FilesystemReadAvailability.Reason; !strings.Contains(got, "backend is unavailable") || strings.Contains(got, host.root) {
			t.Fatalf("safe backend recovery reason=%q", got)
		}
	})

	t.Run("root needed then available", func(t *testing.T) {
		fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}}
		root := t.TempDir()
		host := &publicationHost{root: root}
		chat := newAvailabilityChat(t, fp, host)
		defer chat.Close()
		if got := chat.Snapshot().FilesystemReadAvailability.State; got != "working_directory_unavailable" {
			t.Fatalf("availability state=%q, want working_directory_unavailable", got)
		}
		host.root = root
		if err := chat.InitializeWorkingDirectory(context.Background(), root); err != nil {
			t.Fatalf("initialize working directory: %v", err)
		}
		status := chat.Snapshot().FilesystemReadAvailability
		if status.State != "available" || !status.Available {
			t.Fatalf("availability after selected root=%+v", status)
		}
	})
}

func TestCapabilityAvailabilityExplainsRealProviderRoutes(t *testing.T) {
	for _, test := range []struct {
		name       string
		auth       provider.AuthMethod
		plug       provider.Provider
		modelID    string
		wantState  string
		wantReason string
	}{
		{"OpenAI API key", provider.AuthAPIKey, openai.Plugin{}, "fixture-model", "available", "authorization and approval"},
		{"OpenAI ChatGPT OAuth route", provider.AuthOAuth, openai.Plugin{}, "gpt-6-astra", "available", "authorization and approval"},
		{"OpenAI ChatGPT OAuth tested model", provider.AuthOAuth, openai.Plugin{}, "gpt-6-sol", "available", "authorization and approval"},
		{"Anthropic API key", provider.AuthAPIKey, anthropic.Plugin{}, "fixture-model", "available", "authorization and approval"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry, err := model.NewRegistry(test.plug)
			if err != nil {
				t.Fatal(err)
			}
			config := BundledConversationToolRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return permissions.ApprovalOnce, "", nil
			}), ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
				return ClarificationResponse{}, nil
			}))
			chat := NewChatWithToolRuntime(registry, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
			defer chat.Close()
			chat.mu.Lock()
			chat.selected = &model.Model{Provider: test.plug.ID(), AuthMethod: test.auth, ID: test.modelID}
			chat.mu.Unlock()
			snapshot := chat.Snapshot()
			for name, got := range map[string]CapabilityAvailability{"update_plan": snapshot.UpdatePlanAvailability, "request_user_input": snapshot.RequestInputAvailability} {
				if got.State != test.wantState || !strings.Contains(got.Reason, test.wantReason) {
					t.Errorf("%s availability=%+v; want state %q and reason containing %q", name, got, test.wantState, test.wantReason)
				}
			}
			definitions := chat.toolRuntime.definitions(test.plug, test.auth, test.modelID)
			if test.wantState == "available" && len(definitions) != 2 {
				t.Fatalf("verified route registered %d tools; want plan and input", len(definitions))
			}
			if test.wantState != "available" && len(definitions) != 0 {
				t.Fatalf("unverified route advertised tools: %+v", definitions)
			}
			if test.auth == provider.AuthOAuth && test.modelID == "gpt-6-sol" && snapshot.FilesystemReadAvailability.State != "backend_unavailable" {
				t.Fatalf("read capability should remain conditional on the trusted host backend: %+v", snapshot.FilesystemReadAvailability)
			}
		})
	}
}

type unsupportedPublicationProvider struct{ *publicationProvider }

func (*unsupportedPublicationProvider) RouteCapabilities(provider.AuthMethod) provider.RouteCapabilities {
	return provider.RouteCapabilities{ClientTools: provider.SupportUnsupported}
}

func newAvailabilityChat(t *testing.T, plug provider.Provider, host FilesystemReadCompositionHost) *Chat {
	t.Helper()
	providers, err := model.NewRegistry(plug)
	if err != nil {
		t.Fatal(err)
	}
	approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	})
	clarification := ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, nil
	})
	config := BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, host)
	if config.RootHost == nil {
		config.RootHost = host
	}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	if err := chat.AddProfile(context.Background(), "availability-profile", string(plug.ID()), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	discovery := chat.Discover(context.Background())
	if len(discovery.Models) == 0 {
		t.Fatalf("discovered models=%+v", discovery.Models)
	}
	if err := chat.Select(context.Background(), discovery.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	return chat
}

func TestFilesystemReadCatalogRequiresCompleteInspectedHost(t *testing.T) {
	approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	})
	clarification := ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, nil
	})
	host := &publicationHost{root: t.TempDir()}
	config := BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, host)
	if config.Registry == nil || len(config.Registry.Definitions()) != 3 {
		t.Fatalf("complete inspected host registry=%+v", config.Registry)
	}
	definitions := config.Registry.Definitions()
	if definitions[0].ID != tools.ReadFileID {
		t.Fatalf("definitions=%+v; read_file should be sorted into the catalog", definitions)
	}
	toolRuntime := newToolRuntime(config)
	providerRoute := &toolLoopProvider{}
	if got := toolRuntime.definitions(providerRoute, provider.AuthAPIKey, "fixture-model"); len(got) != 3 {
		t.Fatalf("supported API-key route definitions=%+v", got)
	}
	if got := toolRuntime.definitions(providerRoute, provider.AuthOAuth, "fixture-model"); len(got) != 0 {
		t.Fatalf("unknown OAuth route exposed read tool: %+v", got)
	}

	unsupported := &unavailablePublicationHost{publicationHost: host}
	config = BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, unsupported)
	if config.Registry == nil || len(config.Registry.Definitions()) != 2 {
		t.Fatalf("unavailable backend retained read_file: %+v", config.Registry)
	}
}

func TestReadFileAdvertisementRequiresSelectedRootAndApprovalIsExplicit(t *testing.T) {
	for _, test := range []struct {
		name          string
		choice        permissions.ApprovalChoice
		wantProvider  int
		wantOutcome   tools.Outcome
		wantReadCount int
	}{
		{name: "without root", choice: permissions.ApprovalOnce, wantProvider: 1, wantOutcome: tools.OutcomeNotExecuted},
		{name: "denied", choice: permissions.ApprovalDeny, wantProvider: 2, wantOutcome: tools.OutcomeDeclined},
		{name: "cancelled", choice: permissions.ApprovalCancel, wantProvider: 1, wantOutcome: tools.OutcomeCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt")}
			providers, err := model.NewRegistry(fp)
			if err != nil {
				t.Fatal(err)
			}
			host := &publicationHost{content: []byte("synthetic fixture"), root: filepath.Dir(fp.target), target: fp.target}
			approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return test.choice, "", nil
			})
			chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, publicationReadRuntimeConfig(approval, host))
			t.Cleanup(chat.Close)
			if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
				t.Fatal(err)
			}
			selectFirst(t, chat)
			if test.name != "without root" {
				selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
			}
			turn, _ := chat.Send(context.Background(), "read the fixture", nil)
			if test.name != "without root" && (len(turn.ToolActivities) == 0 || turn.ToolActivities[0].Outcome != test.wantOutcome) {
				t.Fatalf("activity=%+v want outcome %s", turn.ToolActivities, test.wantOutcome)
			}
			if test.name == "denied" && !strings.Contains(turn.ToolActivities[0].Message, "refused") {
				t.Fatalf("explicit denial was not described as a refusal: %+v", turn.ToolActivities[0])
			}
			requests := fp.captured()
			if len(requests) != test.wantProvider {
				t.Fatalf("provider requests=%d want=%d", len(requests), test.wantProvider)
			}
			_, advertised := definitionByID(requests[0].Tools, tools.ReadFileID)
			if advertised != (test.name != "without root") {
				t.Fatalf("read_file advertised=%t in first request tools=%+v", advertised, requests[0].Tools)
			}
			host.mu.Lock()
			reads := host.reads
			host.mu.Unlock()
			if reads != test.wantReadCount {
				t.Fatalf("read executions=%d want=%d", reads, test.wantReadCount)
			}
		})
	}
}

func TestReadFileApprovalRedirectReachesProviderWithoutReading(t *testing.T) {
	const instruction = "Please summarize the request without opening a file."
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt")}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: []byte("must-not-be-read"), root: filepath.Dir(fp.target), target: fp.target}
	approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalRedirect, instruction, nil
	})
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, publicationReadRuntimeConfig(approval, host))
	t.Cleanup(chat.Close)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
	turn, err := chat.Send(context.Background(), "read this file", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("redirected turn=%+v err=%v", turn, err)
	}
	requests := fp.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want original call plus replanning request", len(requests))
	}
	last := requests[1].Messages
	if len(last) == 0 || last[len(last)-1].Role != provider.RoleUser || last[len(last)-1].Text != instruction {
		t.Fatalf("redirect instruction was not appended as a user message: %#v", last)
	}
	if strings.Contains(providerMessagesText(last), "must-not-be-read") {
		t.Fatal("redirect path disclosed file content")
	}
	if len(turn.ToolActivities) == 0 || turn.ToolActivities[0].Outcome != tools.OutcomeNotExecuted {
		t.Fatalf("redirect activity does not report not-executed: %+v", turn.ToolActivities)
	}
	host.mu.Lock()
	reads := host.reads
	host.mu.Unlock()
	if reads != 0 {
		t.Fatalf("redirect executed a read %d times", reads)
	}
	revision, grants := chat.toolRuntime.grants.Snapshot(chat.conversationID)
	if revision != 0 || len(grants) != 0 || chat.toolRuntime.readAuthorityCount() != 0 {
		t.Fatalf("redirect changed permission/read authority: revision=%d grants=%d authorities=%d", revision, len(grants), chat.toolRuntime.readAuthorityCount())
	}
}

type unavailablePublicationHost struct{ *publicationHost }

func (*unavailablePublicationHost) BackendStatus() (bool, string) {
	return false, "backend unavailable"
}

type twoReadProvider struct {
	*fakeProvider
	muCalls  sync.Mutex
	calls    int
	requests []provider.ChatRequest
	root     string
}

func (p *twoReadProvider) RouteCapabilities(method provider.AuthMethod) provider.RouteCapabilities {
	if method == provider.AuthOAuth || method == provider.AuthAPIKey {
		return provider.RouteCapabilities{ClientTools: provider.SupportSupported}
	}
	return provider.RouteCapabilities{ClientTools: provider.SupportUnknown}
}

func (p *twoReadProvider) StreamChat(ctx context.Context, request provider.ChatRequest, delta func(string)) (provider.Result, error) {
	p.muCalls.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, request)
	p.muCalls.Unlock()
	if call == 1 {
		mk := func(id string) provider.ToolCall {
			args, _ := json.Marshal(map[string]string{"path": filepath.Join(p.root, id+".txt")})
			return provider.ToolCall{CallID: id, ToolID: tools.ID("read_file"), Arguments: args}
		}
		calls := []provider.ToolCall{mk("read-a"), mk("read-b")}
		native := make([]json.RawMessage, 0, len(calls))
		for index, call := range calls {
			encodedArgs, _ := json.Marshal(string(call.Arguments))
			item, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-read-" + string(rune('a'+index)), "call_id": call.CallID, "name": string(call.ToolID), "arguments": json.RawMessage(encodedArgs), "status": "completed"})
			native = append(native, item)
		}
		return provider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted, Completion: provider.CompletionToolCalls, Text: "Reading two files.", Native: native, ToolCalls: calls}, nil
	}
	return provider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted, Completion: provider.CompletionFinal, Text: "safe answer", Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"safe answer"}`)}}, nil
}

func TestStage5InvalidationWhileSiblingApprovalWaitsWithholdsWholeBatch(t *testing.T) {
	const sentinel = "must-never-publish-after-sibling-invalidation"
	root := filepath.Join(t.TempDir(), "synthetic")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fp := &twoReadProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, root: root}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: []byte(sentinel), root: root}
	waitingB := make(chan struct{})
	releaseB := make(chan struct{})
	var approvals atomic.Int32
	approval := ToolApprovalFunc(func(ctx context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		if approvals.Add(1) == 2 {
			close(waitingB)
			select {
			case <-releaseB:
				return permissions.ApprovalOnce, "", nil
			case <-ctx.Done():
				return "", "", ctx.Err()
			}
		}
		return permissions.ApprovalOnce, "", nil
	})
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: approval, ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	registerPublicationReadTool(t, chat)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, root)
	t.Cleanup(chat.Close)
	type sendResult struct {
		turn Turn
		err  error
	}
	done := make(chan sendResult, 1)
	go func() {
		turn, err := chat.Send(context.Background(), "inspect both", nil)
		done <- sendResult{turn: turn, err: err}
	}()
	select {
	case <-waitingB:
	case <-time.After(3 * time.Second):
		t.Fatal("second approval did not wait")
	}
	if got := len(fp.requests); got != 1 || strings.Contains(providerMessagesText(fp.requests[0].Messages), sentinel) || strings.Contains(providerMessagesText(chat.history), sentinel) {
		t.Fatalf("private A data escaped while B awaited: requests=%d history=%s", got, providerMessagesText(chat.history))
	}
	if _, err := chat.ClearToolGrants(); err != nil {
		t.Fatal(err)
	}
	close(releaseB)
	var result sendResult
	select {
	case result = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("invalidated batch did not stop")
	}
	if result.err != nil || result.turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("revoked read batch outcome=%s err=%v; want interrupted without a service error", result.turn.Outcome, result.err)
	}
	if got := len(fp.requests); got != 1 {
		t.Fatalf("stale batch dispatched provider request: %d", got)
	}
	snapshotJSON, _ := json.Marshal(chat.Snapshot().Turns)
	if strings.Contains(providerMessagesText(chat.history), sentinel) || strings.Contains(string(snapshotJSON), sentinel) {
		t.Fatal("invalidated capture entered transcript or snapshot")
	}
	host.mu.Lock()
	reads := host.reads
	host.mu.Unlock()
	if reads != 1 {
		t.Fatalf("read executions=%d", reads)
	}
	if _, err := chat.Send(context.Background(), "fresh prompt", nil); err != nil {
		t.Fatal(err)
	}
	fp.muCalls.Lock()
	lastRequest := fp.requests[len(fp.requests)-1]
	fp.muCalls.Unlock()
	if strings.Contains(providerMessagesText(lastRequest.Messages), sentinel) {
		t.Fatal("invalidated bytes resurrected in a later request")
	}
}

func TestStage5LateProviderResponseCannotReinstallRetiredContext(t *testing.T) {
	const sentinel = "late-response-sensitive-synthetic-sentinel"
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt"), blockResponse: true, responseStarted: make(chan struct{}), releaseResponse: make(chan struct{})}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: []byte(sentinel), root: filepath.Dir(fp.target), target: fp.target}
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	registerPublicationReadTool(t, chat)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
	t.Cleanup(chat.Close)
	deltas := make(chan string, 1)
	done := make(chan Turn, 1)
	go func() {
		turn, _ := chat.Send(context.Background(), "read this", func(text string) { deltas <- text })
		done <- turn
	}()
	select {
	case <-fp.responseStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("fake provider did not reach held response")
	}
	if _, err := chat.ClearToolGrants(); err != nil {
		t.Fatal(err)
	}
	close(fp.releaseResponse)
	var turn Turn
	select {
	case turn = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("late provider response did not finish")
	}
	if turn.Outcome != provider.OutcomeInterrupted || turn.Text != "" {
		t.Fatalf("late response was not suppressed: %+v", turn)
	}
	select {
	case got := <-deltas:
		t.Fatalf("late delta reached UI: %q", got)
	default:
	}
	if len(chat.history) != 0 || chat.historySensitive {
		t.Fatal("retired continuation was reinstalled")
	}
	for _, item := range chat.Snapshot().Turns {
		if item.Text != "" || strings.Contains(item.Message, sentinel) {
			t.Fatalf("retired output remained in snapshot: %+v", item)
		}
	}
	if turn.Usage.InputTokens != 4 || turn.Usage.OutputTokens != 2 || turn.Usage.Known {
		t.Fatalf("provider usage was lost or marked complete during retirement: %+v", turn.Usage)
	}
}

func TestStage5SiblingReadTransactionsCommitAsOneBatch(t *testing.T) {
	const sentinel = "two-read-batch-content"
	root := filepath.Join(t.TempDir(), "synthetic")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fp := &twoReadProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, root: root}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: []byte(sentinel), root: root}
	approvalCount := 0
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvalCount++
		return permissions.ApprovalOnce, "", nil
	}), ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	registerPublicationReadTool(t, chat)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, root)
	t.Cleanup(chat.Close)
	turn, err := chat.Send(context.Background(), "read both", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	fp.muCalls.Lock()
	requests := append([]provider.ChatRequest(nil), fp.requests...)
	calls := fp.calls
	fp.muCalls.Unlock()
	if calls != 2 || len(requests) != 2 {
		t.Fatalf("provider calls=%d requests=%d", calls, len(requests))
	}
	body := providerMessagesText(requests[1].Messages)
	if strings.Count(body, sentinel) != 2 || !strings.Contains(body, "read-a") || !strings.Contains(body, "read-b") {
		t.Fatalf("not all correlated sibling results were committed: %s", body)
	}
	host.mu.Lock()
	reads := host.reads
	host.mu.Unlock()
	if reads != 2 {
		t.Fatalf("read executions=%d", reads)
	}
	if approvalCount != 2 {
		t.Fatalf("distinct reads received %d approvals; want one explicit once approval per target", approvalCount)
	}
	for _, activity := range turn.ToolActivities {
		if strings.Contains(activity.Message, sentinel) {
			t.Fatal("raw file contents appeared in activity metadata")
		}
	}
	if chat.toolRuntime.readAuthorityCount() != 0 {
		t.Fatal("per-execution authority survived tool-loop retirement")
	}
	if len(chat.toolRuntime.readContinuations) != 1 {
		t.Fatal("committed continuation provenance missing")
	}
}

func TestStage5OversizedEncodedReadIsWithheldWithoutPartialDisclosure(t *testing.T) {
	fp := &publicationProvider{fakeProvider: &fakeProvider{id: provider.OpenAI}, target: publicationTarget(t, "notes.txt")}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: bytes.Repeat([]byte{0}, 50<<10), root: filepath.Dir(fp.target), target: fp.target}
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	registerPublicationReadTool(t, chat)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, filepath.Dir(fp.target))
	t.Cleanup(chat.Close)
	turn, err := chat.Send(context.Background(), "read an oversized encoded file", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	requests := fp.captured()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	body := providerMessagesText(requests[1].Messages)
	if strings.Contains(body, "\\u0000\\u0000\\u0000\\u0000") || !strings.Contains(body, "content was withheld because the provider result budget was exhausted") {
		t.Fatalf("oversized content was partially disclosed or result was not truthful: %s", body)
	}
	if len(turn.ToolActivities) == 0 || turn.ToolActivities[0].Outcome != tools.OutcomeSucceeded || !strings.Contains(turn.ToolActivities[0].Message, "withheld") {
		t.Fatalf("execution outcome/disclosure status lost: %+v", turn.ToolActivities)
	}
	if chat.historySensitive || len(chat.toolRuntime.readContinuations) != 0 {
		t.Fatal("withheld oversized content created replay authority")
	}
}
