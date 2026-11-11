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
	"unicode"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	harnessruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"github.com/gdamore/tcell/v2"
)

const (
	issue277FirstFixture  = "issue277-first-private-context"
	issue277SecondFixture = "issue277-second-private-context"
)

func TestIssue277ChangeDirectoryKeepsChatContextAndResolvesFutureRead(t *testing.T) {
	launchDirectory := t.TempDir()
	nestedDirectory := filepath.Join(launchDirectory, "資料 with spaces")
	if err := os.Mkdir(nestedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launchDirectory, "README.md"), []byte(issue277FirstFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDirectory, "README.md"), []byte(issue277SecondFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	if err := store.AddAPIKey("fixture-profile", "openai", "Fixture", "synthetic-key", nil); err != nil {
		t.Fatal(err)
	}
	provider := &issue277Provider{busyStarted: make(chan struct{}, 1)}
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
	waitForScreen(t, screen, "No model selected", "")
	for _, r := range "/model" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Navigation Fixture Model", "")
	press(tcell.KeyTab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory: ", "")
	typeText("/cd ")
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Usage: /cd <path>", "")
	waitForVisiblePath(t, screen, "> /cd")
	if got := chat.Snapshot().WorkingDirectory; got != launchDirectory {
		t.Fatalf("missing /cd argument changed directory to %q", got)
	}
	for range "/cd" {
		press(tcell.KeyBackspace, 0)
	}

	typeText("Read README.md and explain it")
	press(tcell.KeyEnter, 0)
	firstTarget := filepath.Join(launchDirectory, "README.md")
	waitForScreen(t, screen, "Tool approval", "")
	press(tcell.KeyEnd, 0) // The first key is ignored by the stale-view guard.
	press(tcell.KeyEnd, 0)
	waitForScreen(t, screen, "README.md", "")
	if targets := host.targetsSnapshot(); len(targets) != 1 || targets[0] != firstTarget {
		t.Fatalf("first approved target binding=%q, want %q", targets, firstTarget)
	}
	if host.readCount() != 0 || strings.Contains(provider.requestText(0), issue277FirstFixture) {
		t.Fatal("first file was read or disclosed before approval")
	}
	approveRead := func() {
		press(tcell.KeyTab, 0) // Focus the action list; cancellation is selected by default.
		press(tcell.KeyUp, 0)
		press(tcell.KeyUp, 0) // Move deliberately to Allow once.
		press(tcell.KeyEnter, 0)
	}
	approveRead()
	waitForScreen(t, screen, "first derived answer", "")
	if host.readCount() != 1 || !strings.Contains(provider.requestText(1), issue277FirstFixture) {
		t.Fatal("first approved read was not disclosed to its provider continuation")
	}

	typeText("/cd 資料 with spaces")
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory changed to:", "")
	if !strings.Contains(renderedScreen(screen), "with spaces") {
		t.Fatal("directory change confirmation omitted the path details")
	}
	if snapshot := chat.Snapshot(); snapshot.WorkingDirectory != nestedDirectory || snapshot.FilesystemRoot.Path != launchDirectory || len(snapshot.Turns) != 1 || !strings.Contains(snapshot.Turns[0].Text, "first derived answer") {
		t.Fatalf("directory transition changed eligible-target boundary or conversation: %+v", snapshot)
	}

	typeText("Read README.md from this directory")
	press(tcell.KeyEnter, 0)
	secondTarget := filepath.Join(nestedDirectory, "README.md")
	waitForScreen(t, screen, "Tool approval", "")
	press(tcell.KeyEnd, 0)
	press(tcell.KeyEnd, 0)
	waitForScreen(t, screen, "README.md", "")
	if targets := host.targetsSnapshot(); len(targets) != 2 || targets[1] != secondTarget {
		t.Fatalf("relative target after /cd=%q, want %q", targets, secondTarget)
	}
	if host.readCount() != 1 {
		t.Fatal("new read was captured before its own once approval")
	}
	if !strings.Contains(provider.requestText(2), issue277FirstFixture) || strings.Contains(provider.requestText(2), issue277SecondFixture) {
		t.Fatalf("previous approved context was not reused independently of the new read: %q", provider.requestText(2))
	}
	approveRead()
	waitForScreen(t, screen, "second derived answer", "")
	if host.readCount() != 2 || !strings.Contains(provider.requestText(3), issue277SecondFixture) {
		t.Fatal("new relative target was not captured and disclosed after its own approval")
	}

	invalidDraft := "/cd ../directory-that-does-not-exist"
	typeText(invalidDraft)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory unchanged:", "")
	waitForVisiblePath(t, screen, "> "+invalidDraft)
	if got := chat.Snapshot().WorkingDirectory; got != nestedDirectory {
		t.Fatalf("invalid directory changed working directory to %q, want %q", got, nestedDirectory)
	}
	for range invalidDraft {
		press(tcell.KeyBackspace, 0)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	home, err = harnessruntime.CanonicalFilesystemRoot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	typeText("/cd ~")
	press(tcell.KeyEnter, 0)
	waitForVisiblePath(t, screen, "Working directory changed to: "+home)
	if got := chat.Snapshot().WorkingDirectory; got != home {
		t.Fatalf("home shorthand resolved to %q, want %q", got, home)
	}
	typeText("/cd " + nestedDirectory)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory changed to:", "")
	if !strings.Contains(renderedScreen(screen), "with spaces") {
		t.Fatal("absolute directory change confirmation omitted the path details")
	}

	typeText("Keep this provider request active")
	press(tcell.KeyEnter, 0)
	select {
	case <-provider.busyStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("fake provider did not start the active-turn fixture")
	}
	if !chat.Snapshot().Active {
		t.Fatal("chat did not report the provider turn as active")
	}
	busyDraft := "/cd " + launchDirectory
	typeText(busyDraft)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Working directory unchanged:", "")
	waitForVisiblePath(t, screen, "> "+busyDraft)
	if got := chat.Snapshot().WorkingDirectory; got != nestedDirectory {
		t.Fatalf("busy-turn directory change committed %q, want %q", got, nestedDirectory)
	}
	press(tcell.KeyCtrlC, 0)
	waitForCondition(t, func() bool { return !chat.Snapshot().Active })
	for range busyDraft {
		press(tcell.KeyBackspace, 0)
	}
	typeText("/cd " + launchDirectory)
	press(tcell.KeyEnter, 0)
	waitForVisiblePath(t, screen, "Working directory changed to: "+launchDirectory)
	if got := chat.Snapshot().WorkingDirectory; got != launchDirectory {
		t.Fatalf("directory did not recover after active turn: %q", got)
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

type issue277Provider struct {
	mu          sync.Mutex
	requests    []modelprovider.ChatRequest
	busyStarted chan struct{}
}

func (*issue277Provider) ID() modelprovider.ID { return modelprovider.OpenAI }
func (*issue277Provider) AuthMethods() []modelprovider.AuthMethod {
	return []modelprovider.AuthMethod{modelprovider.AuthAPIKey}
}
func (*issue277Provider) Authenticate(context.Context, modelprovider.Credential) (modelprovider.AuthResult, error) {
	return modelprovider.AuthResult{Provider: modelprovider.OpenAI}, nil
}
func (*issue277Provider) RouteCapabilities(method modelprovider.AuthMethod) modelprovider.RouteCapabilities {
	if method == modelprovider.AuthAPIKey {
		return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportSupported}
	}
	return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportUnsupported}
}
func (*issue277Provider) ListModels(_ context.Context, credential modelprovider.Credential) ([]modelprovider.Model, error) {
	if credential.APIKey == "" {
		return nil, errors.New("fixture credential is unavailable")
	}
	return []modelprovider.Model{{ID: "navigation-fixture", DisplayName: "Navigation Fixture Model", Compatibility: modelprovider.CompatibilitySupported}}, nil
}
func (p *issue277Provider) StreamChat(ctx context.Context, request modelprovider.ChatRequest, delta func(string)) (modelprovider.Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	requestNumber := len(p.requests)
	p.mu.Unlock()
	switch requestNumber {
	case 1:
		return issue277ReadRequest(request, "read-launch-readme"), nil
	case 2:
		if !strings.Contains(providerRequestText(request), issue277FirstFixture) {
			return modelprovider.Result{}, errors.New("launch-file contents are missing from the authorized continuation")
		}
		if delta != nil {
			delta("first derived answer")
		}
		return issue277Final(request, "first derived answer"), nil
	case 3:
		if !strings.Contains(providerRequestText(request), issue277FirstFixture) {
			return modelprovider.Result{}, errors.New("prior authorized file context was not retained after directory change")
		}
		return issue277ReadRequest(request, "read-nested-readme"), nil
	case 4:
		if !strings.Contains(providerRequestText(request), issue277FirstFixture) || !strings.Contains(providerRequestText(request), issue277SecondFixture) {
			return modelprovider.Result{}, errors.New("approved nested-file context is missing from the continuation")
		}
		if delta != nil {
			delta("second derived answer")
		}
		return issue277Final(request, "second derived answer"), nil
	default:
		select {
		case p.busyStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return modelprovider.Result{}, ctx.Err()
	}
}

func (p *issue277Provider) requestText(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.requests) {
		return ""
	}
	return providerRequestText(p.requests[index])
}

func issue277ReadRequest(request modelprovider.ChatRequest, callID string) modelprovider.Result {
	arguments := json.RawMessage(`{"path":"README.md"}`)
	encodedArguments, _ := json.Marshal(string(arguments))
	native := json.RawMessage(`{"type":"function_call","id":"item-` + callID + `","call_id":"` + callID + `","name":"read_file","arguments":` + string(encodedArguments) + `,"status":"completed"}`)
	return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionToolCalls, Text: "I will read that file.", Native: []json.RawMessage{native}, ToolCalls: []modelprovider.ToolCall{{CallID: callID, ToolID: tools.ReadFileID, Arguments: arguments}}}
}

func issue277Final(request modelprovider.ChatRequest, answer string) modelprovider.Result {
	return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: answer, Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"` + answer + `"}`)}}
}

var _ modelprovider.Provider = (*issue277Provider)(nil)

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition did not become true before timeout")
}

func waitForVisiblePath(t *testing.T, screen *synchronizedSimulation, path string) {
	t.Helper()
	compact := func(value string) string {
		return strings.Map(func(r rune) rune {
			if r == '║' || unicode.IsSpace(r) {
				return -1
			}
			return r
		}, value)
	}
	want := compact(path)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		visible := compact(renderedScreen(screen))
		if strings.Contains(visible, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("terminal did not visibly contain path %q (normalized %q): %q", path, compact(renderedScreen(screen)), renderedScreen(screen))
}
