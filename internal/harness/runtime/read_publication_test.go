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
	"github.com/ayeshLK/komposyn/plugins/tools"
)

type publicationHost struct {
	mu        sync.Mutex
	content   []byte
	reads     int
	rootGen   uint64
	root      string
	target    string
	reference *ReadExecutionReference
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if conversationID == "" || root != h.root || generation != h.rootGen || policy != 1 || coverage != 1 {
		return ErrFilesystemRootStale
	}
	return operation()
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
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, store, nil, config)
	chat.toolRuntime.readPublicationTestMode = true
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
	chat.toolRuntime.readPublicationTestMode = true
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
	defs := chat.toolRuntime
	if defs != nil && defs.readPublicationTestMode {
		t.Fatal("normal chat unexpectedly enabled the test-only read publication mode")
	}
	if selection.ID == "" {
		t.Fatal("model selection missing")
	}
}

type twoReadProvider struct {
	*fakeProvider
	muCalls  sync.Mutex
	calls    int
	requests []provider.ChatRequest
	root     string
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
	chat.toolRuntime.readPublicationTestMode = true
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic-only", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	selectPublicationRoot(t, chat, host, root)
	t.Cleanup(chat.Close)
	done := make(chan Turn, 1)
	go func() { turn, _ := chat.Send(context.Background(), "inspect both", nil); done <- turn }()
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
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("invalidated batch did not stop")
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
	chat.toolRuntime.readPublicationTestMode = true
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
	config := ToolRuntimeConfig{Registry: tools.NewRegistry(), Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(context.Context, tools.Tool, tools.Invocation, permissions.Request) (tools.Result, error) {
		return tools.Result{}, nil
	}), Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), ReadAdmission: host, RootHost: host, Grants: permissions.NewMemoryStore()}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	chat.toolRuntime.readPublicationTestMode = true
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
	chat.toolRuntime.readPublicationTestMode = true
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
