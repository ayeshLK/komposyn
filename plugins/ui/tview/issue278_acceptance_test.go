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
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	harnessruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"github.com/gdamore/tcell/v2"
)

const (
	issue278LaunchSecret   = "issue278-launch-secret"
	issue278OutsideSecret  = "issue278-outside-secret"
	issue278RedirectPrompt = "Compare names only."
)

func TestIssue278OutsideReadApprovalUsesCurrentWorkingDirectoryThroughTerminal(t *testing.T) {
	base := canonicalReadFixtureDirectory(t, t.TempDir())
	launchDirectory := filepath.Join(base, "launch")
	nestedDirectory := filepath.Join(launchDirectory, "nested")
	outsideDirectory := filepath.Join(base, "outside")
	for _, path := range []string{launchDirectory, nestedDirectory, outsideDirectory} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	launchDirectory = canonicalReadFixtureDirectory(t, launchDirectory)
	nestedDirectory = canonicalReadFixtureDirectory(t, nestedDirectory)
	outsideDirectory = canonicalReadFixtureDirectory(t, outsideDirectory)
	launchTarget := filepath.Join(launchDirectory, "launch-secret.txt")
	outsideTarget := filepath.Join(outsideDirectory, "README.md")
	if err := os.WriteFile(launchTarget, []byte(issue278LaunchSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideTarget, []byte(issue278OutsideSecret), 0o600); err != nil {
		t.Fatal(err)
	}

	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	if err := store.AddAPIKey("fixture-profile", "openai", "Fixture", "synthetic-key", nil); err != nil {
		t.Fatal(err)
	}
	provider := &issue278Provider{outsideSecret: issue278OutsideSecret, launchTarget: launchTarget, outsideRelative: "../../outside/README.md"}
	providers, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	host := &issue276ReadHost{allowOutside: true}
	approvalBridge := harnessui.NewApprovalBridge()
	recorder := &issue278ApprovalRecorder{bridge: approvalBridge}
	clarification := harnessruntime.ToolClarificationFunc(func(context.Context, harnessruntime.ClarificationRequest) (harnessruntime.ClarificationResponse, error) {
		return harnessruntime.ClarificationResponse{}, errors.New("unexpected clarification")
	})
	toolRuntime := harnessruntime.BundledConversationToolRuntimeConfigWithFilesystemRead(recorder, clarification, host)
	chat := harnessruntime.NewChatWithToolRuntime(providers, store, nil, toolRuntime)
	if err := chat.InitializeWorkingDirectory(context.Background(), launchDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chat.Close)
	client := harnessui.Client{Chat: chat, ProfileStore: store, Approval: approvalBridge}
	simulation := tcell.NewSimulationScreen("UTF-8")
	simulation.SetSize(320, 40)
	screen := &synchronizedSimulation{SimulationScreen: simulation}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- (Plugin{screen: screen}).Run(ctx, client) }()
	press := func(key tcell.Key, r rune) {
		simulation.InjectKey(key, r, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	typeText := func(text string) {
		for _, r := range text {
			press(tcell.KeyRune, r)
		}
	}
	stop := func() {
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
	waitForScreen(t, screen, "No model selected", "")
	for _, r := range "/model" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Outside Read Fixture Model", "")
	press(tcell.KeyTab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory:", "")
	if got := chat.Snapshot().WorkingDirectory; got != launchDirectory {
		t.Fatalf("launch working directory=%q, want %q", got, launchDirectory)
	}
	typeText("/cd " + nestedDirectory)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory changed to:", "")

	typeText("Read both exact files and compare them")
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Tool approval", "")
	assertOutsideNotice(t, recorder, true)
	waitForScreen(t, screen, "Provider destination: openai", "")
	if targets := host.targetsSnapshot(); len(targets) != 1 || targets[0] != launchTarget {
		t.Fatalf("first approval target=%q, want exact absolute target %q", targets, launchTarget)
	}
	if host.readCount() != 0 || strings.Contains(provider.requestText(0), issue278LaunchSecret) || strings.Contains(provider.requestText(0), issue278OutsideSecret) {
		t.Fatal("file bytes were read or disclosed before explicit approval")
	}
	allowOnce := func() {
		press(tcell.KeyEnd, 0) // The first key is guarded against stale composer input.
		press(tcell.KeyEnd, 0)
		press(tcell.KeyTab, 0)
		press(tcell.KeyUp, 0)
		press(tcell.KeyUp, 0) // Deliberately move from Cancel turn to Allow once.
		press(tcell.KeyEnter, 0)
	}
	allowOnce()

	waitForScreen(t, screen, "Tool approval", "")
	assertOutsideNotice(t, recorder, true)
	if targets := host.targetsSnapshot(); len(targets) != 2 || targets[1] != outsideTarget {
		t.Fatalf("second ..-resolved approval target=%q, want %q", targets, outsideTarget)
	}
	if host.readCount() != 1 || strings.Contains(provider.requestText(0), issue278LaunchSecret) || strings.Contains(provider.requestText(0), issue278OutsideSecret) {
		t.Fatal("second file was read early or file bytes were disclosed before batch completion")
	}
	press(tcell.KeyEnd, 0)
	press(tcell.KeyEnd, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyUp, 0) // Deny this file, separately from the first Allow once.
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "comparison answer", "")
	if host.readCount() != 1 || strings.Contains(provider.requestText(1), issue278OutsideSecret) {
		t.Fatal("denied second file was read or disclosed")
	}

	typeText("/cd " + outsideDirectory)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory changed to:", "")
	if got := chat.Snapshot().WorkingDirectory; got != outsideDirectory {
		t.Fatalf("working directory after /cd=%q, want %q", got, outsideDirectory)
	}
	typeText("Read README.md from this directory")
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Tool approval", "")
	assertOutsideNotice(t, recorder, false)
	if targets := host.targetsSnapshot(); len(targets) != 3 || targets[2] != outsideTarget {
		t.Fatalf("read after /cd target=%q, want exact target %q", targets, outsideTarget)
	}
	if host.readCount() != 1 || strings.Contains(provider.requestText(2), issue278OutsideSecret) {
		t.Fatal("outside file was read or disclosed before fresh approval")
	}
	allowOnce()
	waitForScreen(t, screen, "outside-file answer", "")
	if host.readCount() != 2 || !strings.Contains(provider.requestText(3), issue278OutsideSecret) {
		t.Fatal("approved file outside the launch directory did not reach the provider continuation")
	}
	if got := chat.Snapshot().WorkingDirectory; got != outsideDirectory {
		t.Fatalf("reading outside the launch directory changed working directory to %q", got)
	}
	typeText("Read the outside README again")
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Tool approval", "")
	assertOutsideNotice(t, recorder, false)
	if targets := host.targetsSnapshot(); len(targets) != 4 || targets[3] != outsideTarget {
		t.Fatalf("redirected read target=%q, want exact target %q", targets, outsideTarget)
	}
	press(tcell.KeyEnd, 0)
	press(tcell.KeyEnd, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyDown, 0) // Select redirect from the safe Cancel turn action.
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Original tool call will be rejected", "")
	typeText(issue278RedirectPrompt)
	press(tcell.KeyEscape, 0)
	waitForScreen(t, screen, "Tool approval", "")
	press(tcell.KeyTab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, issue278RedirectPrompt, "")
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Request interrupted:", "")
	if host.readCount() != 2 || strings.Contains(provider.requestText(5), issue278OutsideSecret) {
		t.Fatal("redirected outside read was executed or disclosed")
	}
	stop()
}

type issue278ApprovalRecorder struct {
	bridge   *harnessui.ApprovalBridge
	mu       sync.Mutex
	requests []permissions.Request
}

func (r *issue278ApprovalRecorder) Review(ctx context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
	r.mu.Lock()
	r.requests = append(r.requests, pending.Request)
	r.mu.Unlock()
	return r.bridge.Review(ctx, pending)
}

func assertOutsideNotice(t *testing.T, recorder *issue278ApprovalRecorder, want bool) {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.requests) == 0 {
		t.Fatal("no approval request was recorded")
	}
	request := recorder.requests[len(recorder.requests)-1]
	got := false
	for _, requirement := range request.Requirements {
		got = got || strings.Contains(requirement.Description, "outside the working directory")
	}
	if got != want {
		t.Fatalf("outside-working-directory approval notice=%t, want %t: %+v", got, want, request.Requirements)
	}
}

type issue278Provider struct {
	mu              sync.Mutex
	requests        []modelprovider.ChatRequest
	outsideSecret   string
	launchTarget    string
	outsideRelative string
}

func (*issue278Provider) ID() modelprovider.ID { return modelprovider.OpenAI }
func (*issue278Provider) AuthMethods() []modelprovider.AuthMethod {
	return []modelprovider.AuthMethod{modelprovider.AuthAPIKey}
}
func (*issue278Provider) Authenticate(context.Context, modelprovider.Credential) (modelprovider.AuthResult, error) {
	return modelprovider.AuthResult{Provider: modelprovider.OpenAI}, nil
}
func (*issue278Provider) RouteCapabilities(method modelprovider.AuthMethod) modelprovider.RouteCapabilities {
	if method == modelprovider.AuthAPIKey {
		return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportSupported}
	}
	return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportUnsupported}
}
func (*issue278Provider) ListModels(_ context.Context, credential modelprovider.Credential) ([]modelprovider.Model, error) {
	if credential.APIKey == "" {
		return nil, errors.New("fixture credential is unavailable")
	}
	return []modelprovider.Model{{ID: "outside-read-fixture", DisplayName: "Outside Read Fixture Model", Compatibility: modelprovider.CompatibilitySupported}}, nil
}
func (p *issue278Provider) StreamChat(_ context.Context, request modelprovider.ChatRequest, delta func(string)) (modelprovider.Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	requestNumber := len(p.requests)
	p.mu.Unlock()
	switch requestNumber {
	case 1:
		return issue278ReadCalls(request, []issue278ReadCall{{id: "absolute-launch", path: p.launchTarget}, {id: "outside-dotdot", path: p.outsideRelative}}), nil
	case 2:
		return issue278Answer(request, "comparison answer", delta), nil
	case 3:
		return issue278ReadCalls(request, []issue278ReadCall{{id: "outside-working-directory", path: "README.md"}}), nil
	case 4:
		if !strings.Contains(providerRequestText(request), p.outsideSecret) {
			return modelprovider.Result{}, errors.New("outside-file continuation omitted approved bytes")
		}
		return issue278Answer(request, "outside-file answer", delta), nil
	case 5:
		return issue278ReadCalls(request, []issue278ReadCall{{id: "redirect-outside", path: "README.md"}}), nil
	case 6:
		if strings.Contains(providerRequestText(request), p.outsideSecret) {
			return modelprovider.Result{}, errors.New("redirect continuation disclosed file bytes")
		}
		return issue278Answer(request, "redirected answer", delta), nil
	default:
		return modelprovider.Result{}, errors.New("unexpected provider request")
	}
}

type issue278ReadCall struct {
	id   string
	path string
}

func issue278ReadCalls(request modelprovider.ChatRequest, calls []issue278ReadCall) modelprovider.Result {
	toolCalls := make([]modelprovider.ToolCall, 0, len(calls))
	native := make([]json.RawMessage, 0, len(calls))
	for _, call := range calls {
		arguments, _ := json.Marshal(struct {
			Path string `json:"path"`
		}{call.path})
		encodedArguments, _ := json.Marshal(string(arguments))
		native = append(native, json.RawMessage(`{"type":"function_call","id":"item-`+call.id+`","call_id":"`+call.id+`","name":"read_file","arguments":`+string(encodedArguments)+`,"status":"completed"}`))
		toolCalls = append(toolCalls, modelprovider.ToolCall{CallID: call.id, ToolID: tools.ReadFileID, Arguments: arguments})
	}
	return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionToolCalls, Text: "I will read the requested files.", Native: native, ToolCalls: toolCalls}
}

func issue278Answer(request modelprovider.ChatRequest, answer string, delta func(string)) modelprovider.Result {
	if delta != nil {
		delta(answer)
	}
	return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: answer, Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"` + answer + `"}`)}}
}

func (p *issue278Provider) requestText(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.requests) {
		return ""
	}
	return providerRequestText(p.requests[index])
}

var _ modelprovider.Provider = (*issue278Provider)(nil)
