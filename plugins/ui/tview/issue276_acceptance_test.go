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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	harnessruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"github.com/gdamore/tcell/v2"
)

const issue276PrivateFixture = "issue276-private-read-fixture"

func TestIssue276LaunchRelativeReadJourneyThroughTerminalRuntimeAndProvider(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(issue276PrivateFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	if err := store.AddAPIKey("fixture-profile", "openai", "Fixture", "synthetic-key", nil); err != nil {
		t.Fatal(err)
	}
	provider := &issue276Provider{}
	providers, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	host := &issue276ReadHost{}
	approvalBridge := harnessui.NewApprovalBridge()
	clarification := harnessruntime.ToolClarificationFunc(func(context.Context, harnessruntime.ClarificationRequest) (harnessruntime.ClarificationResponse, error) {
		return harnessruntime.ClarificationResponse{}, errors.New("unexpected clarification")
	})
	toolRuntime := harnessruntime.BundledConversationToolRuntimeConfigWithFilesystemRead(approvalBridge, clarification, host)
	chat := harnessruntime.NewChatWithToolRuntime(providers, store, nil, toolRuntime)
	if err := chat.InitializeWorkingDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if chat.Snapshot().WorkingDirectory != root || len(chat.ToolGrants()) != 0 {
		t.Fatalf("launch directory setup granted access or was not retained: %+v", chat.Snapshot())
	}
	t.Cleanup(chat.Close)
	client := harnessui.Client{Chat: chat, ProfileStore: store, Approval: approvalBridge}
	simulation := tcell.NewSimulationScreen("UTF-8")
	simulation.SetSize(240, 40)
	screen := &synchronizedSimulation{SimulationScreen: simulation}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- (Plugin{screen: screen}).Run(ctx, client) }()
	press := func(key tcell.Key, r rune) {
		simulation.InjectKey(key, r, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	waitForScreen(t, screen, "No model selected", "")
	simulation.SetSize(240, 40)
	for _, r := range "/model" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Relative Fixture Model", "")
	press(tcell.KeyTab, 0) // provider actions
	press(tcell.KeyTab, 0) // discovered models
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory: "+root, "")
	for _, r := range "Read README.md and explain it" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	target := filepath.Join(root, "README.md")
	waitForScreen(t, screen, target, "")
	if provider.requestCount() != 1 {
		t.Fatalf("provider requests before approval = %d, want only the initial request", provider.requestCount())
	}
	if strings.Contains(provider.requestText(0), issue276PrivateFixture) {
		t.Fatal("private file bytes reached the provider before approval")
	}
	if host.readCount() != 0 {
		t.Fatal("private file was read before the explicit approval")
	}
	press(tcell.KeyTab, 0) // first key is intentionally ignored by the stale-view guard
	press(tcell.KeyTab, 0) // focus Allow once
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "derived answer", "")
	if host.readCount() != 1 {
		t.Fatalf("approved file read count = %d, want 1", host.readCount())
	}
	if provider.requestCount() != 2 || !strings.Contains(provider.requestText(1), issue276PrivateFixture) {
		t.Fatalf("provider did not receive the approved file in continuation: requests=%d second=%q", provider.requestCount(), provider.requestText(1))
	}
	for _, r := range "Follow up" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "follow-up answer", "")
	if got := len(chat.Snapshot().Turns); got != 2 {
		t.Fatalf("completed conversation turns = %d, want 2", got)
	}
	if chat.Snapshot().WorkingDirectory != root {
		t.Fatal("file read or model interaction changed the working directory")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("acceptance terminal did not stop")
	}
}

type issue276Provider struct {
	mu       sync.Mutex
	requests []modelprovider.ChatRequest
}

func (*issue276Provider) ID() modelprovider.ID { return modelprovider.OpenAI }
func (*issue276Provider) AuthMethods() []modelprovider.AuthMethod {
	return []modelprovider.AuthMethod{modelprovider.AuthAPIKey}
}
func (*issue276Provider) Authenticate(context.Context, modelprovider.Credential) (modelprovider.AuthResult, error) {
	return modelprovider.AuthResult{Provider: modelprovider.OpenAI}, nil
}
func (*issue276Provider) RouteCapabilities(method modelprovider.AuthMethod) modelprovider.RouteCapabilities {
	if method == modelprovider.AuthAPIKey {
		return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportSupported}
	}
	return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportUnsupported}
}
func (*issue276Provider) ListModels(_ context.Context, credential modelprovider.Credential) ([]modelprovider.Model, error) {
	if credential.APIKey == "" {
		return nil, errors.New("fixture credential is unavailable")
	}
	return []modelprovider.Model{{ID: "relative-fixture", DisplayName: "Relative Fixture Model", Compatibility: modelprovider.CompatibilitySupported}}, nil
}
func (p *issue276Provider) StreamChat(_ context.Context, request modelprovider.ChatRequest, delta func(string)) (modelprovider.Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	requestNumber := len(p.requests)
	p.mu.Unlock()
	if requestNumber == 1 {
		args := json.RawMessage(`{"path":"README.md"}`)
		encodedArgs, _ := json.Marshal(string(args))
		native := json.RawMessage(`{"type":"function_call","id":"item-read-1","call_id":"read-1","name":"read_file","arguments":` + string(encodedArgs) + `,"status":"completed"}`)
		return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionToolCalls, Text: "I will read that file.", Native: []json.RawMessage{native}, ToolCalls: []modelprovider.ToolCall{{CallID: "read-1", ToolID: tools.ReadFileID, Arguments: args}}}, nil
	}
	if requestNumber == 2 {
		if !strings.Contains(providerRequestText(request), issue276PrivateFixture) {
			return modelprovider.Result{}, errors.New("approved file contents are missing from continuation")
		}
		if delta != nil {
			delta("derived answer")
		}
		return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: "derived answer", Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"derived answer"}`)}}, nil
	}
	if delta != nil {
		delta("follow-up answer")
	}
	return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: "follow-up answer", Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"follow-up answer"}`)}}, nil
}
func (p *issue276Provider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}
func (p *issue276Provider) requestText(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.requests) {
		return ""
	}
	return providerRequestText(p.requests[index])
}
func providerRequestText(request modelprovider.ChatRequest) string {
	encoded, _ := json.Marshal(request.Messages)
	return string(encoded)
}

type issue276ReadHost struct {
	mu           sync.Mutex
	root         string
	conversation string
	rootGen      uint64
	reads        int
}

type issue276Preparation struct {
	admission harnessruntime.FilesystemReadAdmission
}

func (p *issue276Preparation) Metadata() harnessruntime.FilesystemReadAdmission { return p.admission }
func (p *issue276Preparation) WithAuthorityCommit(ctx context.Context, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit()
}
func (*issue276Preparation) Close(context.Context) error { return nil }

func (h *issue276ReadHost) BackendStatus() (bool, string) { return true, "fixture exact-read backend" }
func (h *issue276ReadHost) ValidateFilesystemRoot(ctx context.Context, root string) (string, error) {
	return harnessruntime.CanonicalFilesystemRoot(ctx, root)
}
func (h *issue276ReadHost) InvalidateFilesystemRoot(_ context.Context, transition harnessruntime.FilesystemRootTransition) error {
	h.mu.Lock()
	h.root, h.rootGen, h.conversation = transition.Root, transition.Generation, transition.ConversationID
	h.mu.Unlock()
	return nil
}
func (h *issue276ReadHost) AdmitFilesystemRead(_ context.Context, invocation tools.Invocation, destination string) (harnessruntime.FilesystemReadAdmission, error) {
	var arguments struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(invocation.Arguments, &arguments); err != nil {
		return harnessruntime.FilesystemReadAdmission{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	relative, err := filepath.Rel(h.root, arguments.Path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || arguments.Path != filepath.Join(h.root, "README.md") || invocation.ConversationID != h.conversation {
		return harnessruntime.FilesystemReadAdmission{}, errors.New("fixture read target is outside the launch directory")
	}
	return harnessruntime.FilesystemReadAdmission{Reference: new(struct{}), ServiceID: "issue276-fixture", ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, CallID: invocation.ProviderCallID, Root: h.root, Deadline: time.Now().Add(time.Minute), AdmissionBinding: "issue276-admission", Target: arguments.Path, Class: "ordinary", Identity: "fixture-object", RootGeneration: h.rootGen, PolicyRevision: 1, CoverageRevision: 1, Destination: destination}, nil
}
func (h *issue276ReadHost) PrepareAdmittedFilesystemRead(_ context.Context, admission harnessruntime.FilesystemReadAdmission, _ uint64) (harnessruntime.FilesystemReadPreparation, error) {
	return &issue276Preparation{admission: admission}, nil
}
func (*issue276ReadHost) CloseFilesystemReadAdmission(context.Context, harnessruntime.FilesystemReadAdmission) error {
	return nil
}
func (h *issue276ReadHost) ExecuteAdmittedFilesystemRead(ctx context.Context, admission harnessruntime.FilesystemReadAdmission, _ harnessruntime.FilesystemReadPreparation, _ *harnessruntime.ReadExecutionReference, authority harnessruntime.FilesystemReadExecutionAuthority) (*harnessruntime.PrivateReadCapture, error) {
	if err := authority.Claim(); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(admission.Target)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.reads++
	h.mu.Unlock()
	var capture *harnessruntime.PrivateReadCapture
	err = authority.WithCurrent(func() error {
		var stageErr error
		capture, stageErr = authority.Stage(admission, harnessruntime.FilesystemReadExecutionResult{Outcome: tools.OutcomeSucceeded, Bytes: content, ServiceID: admission.ServiceID, AdmissionBinding: admission.AdmissionBinding, AuthorizationBinding: authority.AuthorizationBinding(), Identity: admission.Identity, RootGeneration: admission.RootGeneration, PolicyRevision: admission.PolicyRevision, CoverageRevision: admission.CoverageRevision, Destination: admission.Destination, ReadStarted: true, WorkerJoined: true, CapabilitiesRetired: true})
		return stageErr
	})
	if err != nil {
		return nil, err
	}
	return capture, nil
}
func (h *issue276ReadHost) WithFilesystemReadPublicationCurrent(ctx context.Context, conversationID, root string, generation, policy, coverage uint64, operation func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if conversationID != h.conversation || root != h.root || generation != h.rootGen || policy != 1 || coverage != 1 {
		return harnessruntime.ErrFilesystemRootStale
	}
	return operation()
}
func (*issue276ReadHost) InvalidateFilesystemReadExecutions(string) error { return nil }
func (*issue276ReadHost) SetFilesystemReadInvalidator(func(string))       {}
func (h *issue276ReadHost) readCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}

var _ harnessruntime.FilesystemReadCompositionHost = (*issue276ReadHost)(nil)
var _ modelprovider.Provider = (*issue276Provider)(nil)
