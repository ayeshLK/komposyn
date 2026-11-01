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
	"strings"
	"sync"
	"testing"
	"time"

	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/ui"
	"github.com/gdamore/tcell/v2"
	tv "github.com/rivo/tview"
)

type clientStub struct {
	mu                     sync.Mutex
	selected               *ui.Model
	turns                  []ui.Turn
	usage                  []ui.ModelUsage
	version                string
	selectedCh             chan struct{}
	selectCalls            int
	profiles               []ui.Profile
	addHook                func(context.Context, string, string, string, string, map[string]string) error
	replaceHook            func(context.Context, string, string, string, map[string]string, bool) error
	snapshotHook           func() ui.Snapshot
	sendHook               func(context.Context, string, func(string)) (ui.Turn, error)
	methods                map[string][]string
	loginHook              func(context.Context, string) (ui.LoginAttempt, error)
	loginCanceled          []string
	approvalEvents         chan ui.ApprovalEvent
	approvalResponses      chan ui.ApprovalResponse
	clarificationEvents    chan ui.ClarificationEvent
	clarificationResponses chan ui.ClarificationResponse
	toolGrants             []ui.ToolGrant
	plan                   []ui.PlanStep
	revoked                []string
	cleared                int
}

// synchronizedSimulation protects the returned cell slice from tview's render
// goroutine while the test inspects a snapshot of the simulated screen.
type synchronizedSimulation struct {
	tcell.SimulationScreen
	mu sync.Mutex
}

func (s *synchronizedSimulation) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SimulationScreen.Init()
}

func (s *synchronizedSimulation) Show() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SimulationScreen.Show()
}

func (s *synchronizedSimulation) GetContents() ([]tcell.SimCell, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cells, width, height := s.SimulationScreen.GetContents()
	snapshot := make([]tcell.SimCell, len(cells))
	copy(snapshot, cells)
	for i := range snapshot {
		snapshot[i].Runes = append([]rune(nil), cells[i].Runes...)
	}
	return snapshot, width, height
}

func (c *clientStub) Profiles(context.Context) ([]ui.Profile, error) { return c.profiles, nil }
func (c *clientStub) AuthMethods(provider string) []string {
	if methods := c.methods[provider]; methods != nil {
		return append([]string(nil), methods...)
	}
	return []string{"api_key"}
}
func (c *clientStub) Discover(context.Context) (ui.Discovery, error) {
	return ui.Discovery{Models: []ui.Model{{Key: "openai\x00personal\x00model-x", Provider: "openai", ProfileID: "personal", ProfileLabel: "Personal", ID: "model-x", DisplayName: "Text Model", Compatibility: modelprovider.CompatibilityUnknown}}}, nil
}

func (c *clientStub) AddProfile(ctx context.Context, id, provider, label, key string, route map[string]string) error {
	if c.addHook != nil {
		return c.addHook(ctx, id, provider, label, key, route)
	}
	return nil
}
func (c *clientStub) ReplaceProfile(ctx context.Context, id, label, key string, route map[string]string, reset bool) error {
	if c.replaceHook != nil {
		return c.replaceHook(ctx, id, label, key, route, reset)
	}
	return nil
}
func (c *clientStub) RemoveProfile(string, bool) error { return nil }
func (c *clientStub) StartLogin(ctx context.Context, providerID, method, profileID string) (ui.LoginAttempt, error) {
	if c.loginHook != nil {
		return c.loginHook(ctx, profileID)
	}
	done := make(chan ui.LoginResult, 1)
	done <- ui.LoginResult{Err: context.Canceled}
	close(done)
	return ui.LoginAttempt{ID: "test-attempt", Done: done}, nil
}
func (c *clientStub) CancelLogin(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loginCanceled = append(c.loginCanceled, id)
	return true
}
func (c *clientStub) Disconnect(context.Context, string, string, string, bool, bool) (ui.DisconnectResult, error) {
	return ui.DisconnectResult{LocalTokensRemoved: true}, nil
}
func (c *clientStub) OpenUsage(context.Context) error { return nil }
func (c *clientStub) Select(_ context.Context, key string, _ bool) error {
	d, _ := c.Discover(context.Background())
	for _, m := range d.Models {
		if m.Key == key {
			c.mu.Lock()
			copy := m
			c.selected = &copy
			c.selectCalls++
			c.mu.Unlock()
			select {
			case c.selectedCh <- struct{}{}:
			default:
			}
			return nil
		}
	}
	return nil
}
func (c *clientStub) Snapshot() ui.Snapshot {
	c.mu.Lock()
	hook := c.snapshotHook
	c.snapshotHook = nil
	snapshot := ui.Snapshot{Selected: c.selected, Turns: append([]ui.Turn(nil), c.turns...), Usage: append([]ui.ModelUsage(nil), c.usage...), Version: c.version, Plan: append([]ui.PlanStep(nil), c.plan...)}
	c.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return snapshot
}
func (c *clientStub) Send(ctx context.Context, text string, delta func(string)) (ui.Turn, error) {
	c.mu.Lock()
	hook := c.sendHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, text, delta)
	}
	return ui.Turn{}, nil
}
func (c *clientStub) Cancel()                                 {}
func (c *clientStub) ApprovalEvents() <-chan ui.ApprovalEvent { return c.approvalEvents }
func (c *clientStub) RespondApproval(response ui.ApprovalResponse) error {
	if c.approvalResponses != nil {
		c.approvalResponses <- response
	}
	return nil
}
func (c *clientStub) ClarificationEvents() <-chan ui.ClarificationEvent { return c.clarificationEvents }
func (c *clientStub) RespondClarification(response ui.ClarificationResponse) error {
	if c.clarificationResponses != nil {
		c.clarificationResponses <- response
	}
	return nil
}
func (c *clientStub) Permissions(context.Context) ([]ui.ToolGrant, error) {
	return append([]ui.ToolGrant(nil), c.toolGrants...), nil
}
func (c *clientStub) RevokePermission(_ context.Context, id string) error {
	c.revoked = append(c.revoked, id)
	return nil
}
func (c *clientStub) ClearPermissions(context.Context) (int, error) {
	count := len(c.toolGrants)
	c.toolGrants = nil
	c.cleared += count
	return count, nil
}

func TestApprovalDialogRequiresExplicitChoiceAndEscapeCancelsTurn(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 30)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	responses := make(chan ui.ApprovalResponse, 2)
	events := make(chan ui.ApprovalEvent, 2)
	client := &clientStub{approvalEvents: events, approvalResponses: responses}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	waitForScreen(t, safe, "No model selected", "")
	events <- ui.ApprovalEvent{Request: &ui.ApprovalRequest{ID: "pending-1", ToolID: "fixture", ToolDescription: "fixture requested access", AllowConversation: true, Requirements: []ui.ApprovalRequirement{{Kind: "resource", ToolID: "fixture", Operation: "read", Description: "read file", ResourceKind: "filesystem", ResourceScope: "/tmp/exact"}}}}
	waitForScreen(t, safe, "/tmp/exact", "")
	select {
	case response := <-responses:
		t.Fatalf("approval happened without a choice: %+v", response)
	case <-time.After(80 * time.Millisecond):
	}
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(35 * time.Millisecond)
	}
	press(tcell.KeyRune, 'j')
	select {
	case response := <-responses:
		t.Fatalf("unrelated key accepted approval: %+v", response)
	default:
	}
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	select {
	case response := <-responses:
		if response.ID != "pending-1" || response.Choice != ui.ApprovalOnce {
			t.Fatalf("explicit choice = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit Allow once choice was not delivered")
	}
	events <- ui.ApprovalEvent{Request: &ui.ApprovalRequest{ID: "pending-2", ToolID: "fixture", Requirements: []ui.ApprovalRequirement{{Kind: "action", Operation: "write"}}}}
	waitForScreen(t, safe, "write", "")
	press(tcell.KeyEscape, 0)
	select {
	case response := <-responses:
		if response.ID != "pending-2" || response.Choice != ui.ApprovalCancel {
			t.Fatalf("Escape response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("Escape did not cancel the turn")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestClarificationRequiresExplicitAnswerDeclineOrCancel(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(105, 32)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	events := make(chan ui.ClarificationEvent, 4)
	responses := make(chan ui.ClarificationResponse, 4)
	client := &clientStub{clarificationEvents: events, clarificationResponses: responses}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	waitForScreen(t, safe, "No model selected", "")
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	events <- ui.ClarificationEvent{Request: &ui.ClarificationRequest{ID: "request-choice", ConversationID: "conversation-a", Question: "Choose [red] a format", MaxAnswerBytes: 4096, Options: []ui.SuggestedAnswer{{Label: "JSON [blue]", Description: "structured"}}}}
	waitForScreen(t, safe, "Choose [red] a format", "")
	waitForScreen(t, safe, "Your answer will be sent to the selected model.", "")
	waitForScreen(t, safe, "JSON [blue]", "")
	select {
	case response := <-responses:
		t.Fatalf("silence or suggestion focus submitted an answer: %+v", response)
	case <-time.After(80 * time.Millisecond):
	}
	press(tcell.KeyTab, 0) // Focus the suggestion list; no option is accepted merely by focus.
	press(tcell.KeyEnter, 0)
	select {
	case response := <-responses:
		if response.ID != "request-choice" || response.ConversationID != "conversation-a" || response.Choice != ui.ClarificationAnswer || response.Answer != "JSON [blue]" {
			t.Fatalf("suggested answer response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit suggested answer was not submitted")
	}
	waitForScreen(t, safe, "KompoSyn is ready", "")

	events <- ui.ClarificationEvent{Request: &ui.ClarificationRequest{ID: "request-free-text", ConversationID: "conversation-a", Question: "What should I know?", MaxAnswerBytes: 4096}}
	waitForScreen(t, safe, "What should I know?", "")
	select {
	case response := <-responses:
		t.Fatalf("free-text prompt submitted without input: %+v", response)
	case <-time.After(60 * time.Millisecond):
	}
	answer := "  literal [green] 雪  "
	for _, r := range answer {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyTab, 0) // Submit is a separate explicit action; Enter in the editor adds a newline.
	press(tcell.KeyEnter, 0)
	select {
	case response := <-responses:
		if response.ID != "request-free-text" || response.Choice != ui.ClarificationAnswer || response.Answer != answer {
			t.Fatalf("free-text response changed: %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit free-text answer was not submitted")
	}
	waitForScreen(t, safe, "KompoSyn is ready", "")

	events <- ui.ClarificationEvent{Request: &ui.ClarificationRequest{ID: "request-decline", ConversationID: "conversation-a", Question: "Decline this?", MaxAnswerBytes: 4096}}
	waitForScreen(t, safe, "Decline this?", "")
	press(tcell.KeyTab, 0) // Input -> action form.
	press(tcell.KeyTab, 0) // Submit -> Decline.
	press(tcell.KeyEnter, 0)
	select {
	case response := <-responses:
		if response.ID != "request-decline" || response.Choice != ui.ClarificationDecline || response.Answer != "" {
			t.Fatalf("decline response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit decline was not submitted")
	}
	waitForScreen(t, safe, "KompoSyn is ready", "")

	events <- ui.ClarificationEvent{Request: &ui.ClarificationRequest{ID: "request-cancel", ConversationID: "conversation-a", Question: "Cancel this turn?", MaxAnswerBytes: 4096}}
	waitForScreen(t, safe, "Cancel this turn?", "")
	press(tcell.KeyEscape, 0)
	select {
	case response := <-responses:
		if response.ID != "request-cancel" || response.Choice != ui.ClarificationCancel {
			t.Fatalf("Escape response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("Escape did not cancel the clarification turn")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestCurrentConversationPlanIsVisibleAndRenderedLiterally(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 28)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{plan: []ui.PlanStep{{Step: "Inspect [red] input", Status: "in_progress"}, {Step: "Finish", Status: "pending"}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	waitForScreen(t, safe, "[in_progress] Inspect [red] input", "")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestPermissionsRemainReachableDuringClarificationWait(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 30)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	events := make(chan ui.ClarificationEvent, 2)
	responses := make(chan ui.ClarificationResponse, 2)
	client := &clientStub{clarificationEvents: events, clarificationResponses: responses}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	waitForScreen(t, safe, "No model selected", "")
	events <- ui.ClarificationEvent{Request: &ui.ClarificationRequest{ID: "pending", ConversationID: "conversation", Question: "Still waiting?", MaxAnswerBytes: 4096}}
	waitForScreen(t, safe, "Still waiting?", "")
	press := func(key tcell.Key) {
		screen.InjectKey(key, 0, tcell.ModNone)
		time.Sleep(30 * time.Millisecond)
	}
	press(tcell.KeyTab) // Input -> action form.
	press(tcell.KeyTab) // Submit -> Decline.
	press(tcell.KeyTab) // Decline -> Cancel.
	press(tcell.KeyTab) // Cancel -> /permissions.
	press(tcell.KeyEnter)
	waitForScreen(t, safe, "No tool grants", "")
	press(tcell.KeyEscape)
	waitForScreen(t, safe, "Still waiting?", "")
	select {
	case response := <-responses:
		t.Fatalf("opening permissions answered clarification: %+v", response)
	default:
	}
	press(tcell.KeyEscape)
	select {
	case response := <-responses:
		if response.ID != "pending" || response.Choice != ui.ClarificationCancel {
			t.Fatalf("Escape after returning from permissions = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("Escape did not cancel the pending clarification")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestClarificationAnswerLimitDoesNotDismissPendingPrompt(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(90, 26)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	events := make(chan ui.ClarificationEvent, 1)
	responses := make(chan ui.ClarificationResponse, 1)
	client := &clientStub{clarificationEvents: events, clarificationResponses: responses}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	waitForScreen(t, safe, "No model selected", "")
	events <- ui.ClarificationEvent{Request: &ui.ClarificationRequest{ID: "limited", ConversationID: "conversation", Question: "Short answer?", MaxAnswerBytes: 3}}
	waitForScreen(t, safe, "Short answer?", "")
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(20 * time.Millisecond)
	}
	press(tcell.KeyRune, 'x') // Initial-key guard.
	for _, r := range "long" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	waitForScreen(t, safe, "limit is 3 UTF-8 bytes", "")
	select {
	case response := <-responses:
		t.Fatalf("oversized answer submitted: %+v", response)
	case <-time.After(50 * time.Millisecond):
	}
	press(tcell.KeyEscape, 0)
	select {
	case response := <-responses:
		if response.ID != "limited" || response.Choice != ui.ClarificationCancel {
			t.Fatalf("cancel after oversized answer = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("pending prompt could not be cancelled")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestPermissionsPanelKeyboardAndLiteralGrantRendering(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(110, 32)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{toolGrants: []ui.ToolGrant{{ID: "grant-1", ToolID: "file_read", Action: "Read [red] path", ResourceKind: "filesystem", ResourceScope: "/safe/[red]report[-].txt", Operation: "read", Lifetime: "conversation", Origin: strings.Repeat("origin line\n", 40) + "trusted [blue] origin MARKER"}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	waitForScreen(t, safe, "No model selected", "")
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	for _, r := range "/permissions" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	waitForScreen(t, safe, "/safe/[red]report[-].txt", "")
	waitForScreen(t, safe, "Read [red] path", "")
	press(tcell.KeyTab, 0) // List -> full details.
	press(tcell.KeyEnd, 0) // Scroll to the end of the full grant details.
	waitForScreen(t, safe, "MARKER", "")
	press(tcell.KeyTab, 0) // Details -> actions.
	press(tcell.KeyEnter, 0)
	waitForScreen(t, safe, "No tool grants", "")
	press(tcell.KeyTab, 0) // List -> details.
	press(tcell.KeyTab, 0) // Details -> Clear all.
	press(tcell.KeyTab, 0) // Clear all to Back.
	press(tcell.KeyEnter, 0)
	waitForScreen(t, safe, "KompoSyn is ready", "revoking prevents future dispatch only")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop after leaving permissions")
	}
}

func TestDismissedApprovalReturnsToChatAfterLeavingPermissions(t *testing.T) {
	app := tv.NewApplication()
	input := newComposerInput()
	client := &clientStub{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chatRoot := tv.NewFlex().SetDirection(tv.FlexRow).AddItem(tv.NewTextView().SetText("CHAT ROOT"), 0, 1, false).AddItem(input, 1, 0, true)
	u := &terminal{app: app, client: client, ctx: ctx, cancel: cancel, input: input, layout: chatRoot}
	u.showApproval(&ui.ApprovalRequest{ID: "pending", ToolID: "fixture", Requirements: []ui.ApprovalRequirement{{Kind: "action", Operation: "read"}}})
	u.permissionsFromApproval = true
	u.permissionGrants = []ui.ToolGrant{}
	u.showPermissions()
	u.returnPermissionView()
	if u.permissionsFromApproval || u.permissionsVisible || u.approvalRequest == nil {
		t.Fatalf("return state before dismissal: approval=%+v fromApproval=%t permissionsVisible=%t", u.approvalRequest, u.permissionsFromApproval, u.permissionsVisible)
	}
	u.dismissApproval("pending")
	if u.approvalRequest != nil || !u.chatRootActive.Load() || app.GetFocus() != input {
		t.Fatalf("dismissal left stale approval root: approval=%+v chatActive=%t focus=%T", u.approvalRequest, u.chatRootActive.Load(), app.GetFocus())
	}
}

func TestPickerInteractionOnSimulationScreen(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{selectedCh: make(chan struct{}, 2)}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	for _, r := range "/model" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	deadline := time.Now().Add(3 * time.Second)
	rendered := ""
	for time.Now().Before(deadline) {
		cells, _, _ := safeScreen.GetContents()
		var b strings.Builder
		for _, cell := range cells {
			b.WriteString(string(cell.Runes))
		}
		rendered = b.String()
		if strings.Contains(rendered, "Refresh Models") && strings.Contains(rendered, "Text Model") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(rendered, "Add New Provider") || !strings.Contains(rendered, "Refresh Models") || !strings.Contains(rendered, "Manage Providers") || !strings.Contains(rendered, "Text Model") {
		cancel()
		<-finished
		t.Fatalf("picker actions/models not rendered: %q", rendered)
	}
	// Tab moves from the action group to models; the grouped headings are not
	// selectable entries and therefore do not disturb row-to-model mapping.
	press(tcell.KeyEnter, 0)
	press(tcell.KeyBacktab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	select {
	case <-client.selectedCh:
	case <-time.After(2 * time.Second):
		cancel()
		<-finished
		t.Fatal("selecting the rendered model did not reach the client")
	}
	time.Sleep(75 * time.Millisecond)
	client.mu.Lock()
	selectCalls := client.selectCalls
	client.mu.Unlock()
	if selectCalls != 1 {
		t.Fatalf("one activation called Select %d times", selectCalls)
	}
	press(tcell.KeyCtrlC, 0)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("terminal did not stop")
	}
}

func TestPickerFilteringKeepsActionsAndModelRowsMapped(t *testing.T) {
	first := ui.Model{Key: "first", Provider: "openai", ProfileID: "p1", ProfileLabel: "One", ID: "model-one", DisplayName: "Alpha"}
	second := ui.Model{Key: "second", Provider: "anthropic", ProfileID: "p2", ProfileLabel: "Two", ID: "model-two", DisplayName: "Beta"}
	client := &clientStub{selectedCh: make(chan struct{}, 1)}
	u := &terminal{client: client, discovery: ui.Discovery{Models: []ui.Model{first, second}}, picker: tv.NewList(), modelList: tv.NewList(), pickerStatus: tv.NewTextView(), pickerQuery: "beta"}
	u.rebuildPicker()
	if u.picker.GetItemCount() != 3 {
		t.Fatalf("provider actions disappeared while filtering: %d", u.picker.GetItemCount())
	}
	if u.modelList.GetItemCount() != 1 || len(u.modelEntries) != 1 || u.modelEntries[0].model == nil || u.modelEntries[0].model.Key != second.Key {
		t.Fatalf("filtered model row lost its identity: items=%d entries=%+v", u.modelList.GetItemCount(), u.modelEntries)
	}
}

func TestProviderFormTraversesForwardAndBackward(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(90, 24)
	form := tv.NewForm()
	form.AddInputField("Profile label", "", 20, nil, nil)
	form.AddInputField("API key", "", 20, nil, nil)
	form.AddButton("Save", func() {})
	first, second := form.GetFormItem(0), form.GetFormItem(1)
	app := tv.NewApplication().SetScreen(screen).SetRoot(form, true).SetFocus(first)
	done := make(chan error, 1)
	go func() { done <- app.Run() }()
	time.Sleep(100 * time.Millisecond)
	if app.GetFocus() != first {
		t.Fatalf("initial provider form focus = %T", app.GetFocus())
	}
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	deadline := time.Now().Add(time.Second)
	for app.GetFocus() != second && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if app.GetFocus() != second {
		t.Fatalf("Tab did not move to second provider form field: %T", app.GetFocus())
	}
	screen.InjectKey(tcell.KeyBacktab, 0, tcell.ModNone)
	deadline = time.Now().Add(time.Second)
	for app.GetFocus() != first && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if app.GetFocus() != first {
		t.Fatalf("Shift+Tab did not restore first provider form field: %T", app.GetFocus())
	}
	app.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("form simulation did not stop")
	}
}

func TestSlashSuggestionsKeyboardWorkflowDoesNotSubmitCommands(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(110, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	model := ui.Model{Key: "openai\x00personal\x00model-x", Provider: "openai", ProfileID: "personal", ProfileLabel: "Personal", ID: "model-x", DisplayName: "Text Model"}
	client := &clientStub{selectedCh: make(chan struct{}, 1), selected: &model,
		turns: []ui.Turn{{Prompt: "earlier context"}},
		usage: []ui.ModelUsage{{Provider: "openai", ModelID: "model-x", Input: ui.MetricUsage{Tokens: 4, Known: true}}}}
	sent := make(chan string, 1)
	continueReply := make(chan struct{})
	client.sendHook = func(_ context.Context, text string, delta func(string)) (ui.Turn, error) {
		sent <- text
		delta("reply [re")
		<-continueReply
		delta("d]literal")
		return ui.Turn{Outcome: modelprovider.OutcomeCompleted}, nil
	}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(120 * time.Millisecond)
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	press(tcell.KeyRune, '/')
	waitForScreen(t, safeScreen, "/model  ·  open provider", "")
	press(tcell.KeyDown, 0) // highlight /help
	press(tcell.KeyTab, 0)  // complete, do not submit
	press(tcell.KeyEnter, 0)
	waitForScreen(t, safeScreen, "Commands: /model", "")
	select {
	case got := <-sent:
		t.Fatalf("slash command was sent to provider as %q", got)
	default:
	}
	for _, r := range "ordinary draft" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	select {
	case got := <-sent:
		if got != "ordinary draft" {
			t.Fatalf("submitted text = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary composer text was not submitted")
	}
	waitForScreen(t, safeScreen, "reply [re", "")
	for _, r := range "keep draft" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyCtrlL, 0)
	waitForScreen(t, safeScreen, "> keep draft", "reply [re")
	if got := client.Snapshot(); got.Selected == nil {
		t.Fatal("display clear changed model selection")
	}
	if got := client.Snapshot(); len(got.Turns) != 1 || len(got.Usage) != 1 || got.Usage[0].Input.Tokens != 4 {
		t.Fatalf("display clear changed continuation or usage state: %+v", got)
	}
	if !strings.Contains(renderedScreen(safeScreen), "> keep draft") {
		t.Fatalf("clear discarded composer draft: %q", renderedScreen(safeScreen))
	}
	close(continueReply)
	waitForScreen(t, safeScreen, "d]literal", "reply [re")
	if strings.Contains(renderedScreen(safeScreen), "Assistant:") {
		t.Fatal("assistant label remains visible")
	}
	press(tcell.KeyCtrlC, 0)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestUsagePaneResizesAndCompactControlKeepsComposerVisible(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{selectedCh: make(chan struct{}, 1), usage: []ui.ModelUsage{{Provider: "openai", ModelID: "model-x",
		Input:  ui.MetricUsage{Tokens: 1234567890123456789, Known: true},
		Output: ui.MetricUsage{Tokens: 2345678901234567890, Known: true, Partial: true}}}}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	waitForScreen(t, safeScreen, "> x", "Usage · this run")
	screen.SetSize(110, 30)
	screen.InjectKey(tcell.KeyRune, 'y', tcell.ModNone)
	waitForScreen(t, safeScreen, "Usage · this run", "")
	if got := renderedScreen(safeScreen); !strings.Contains(got, "> xy") {
		t.Fatalf("composer did not remain available after resizing wide: %q", got)
	}
	screen.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone)
	waitForScreen(t, safeScreen, "KompoSyn", "Usage · this run")
	screen.SetSize(30, 24)
	screen.InjectKey(tcell.KeyRune, 'z', tcell.ModNone)
	waitForScreen(t, safeScreen, "> xyz", "Usage · this run")
	screen.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	waitForScreen(t, safeScreen, "partial", "Usage · this run")
	if got := renderedScreen(safeScreen); !strings.Contains(got, "> xyz") {
		t.Fatalf("composer disappeared while compact usage was open: %q", got)
	}
	screen.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone)
	waitForScreen(t, safeScreen, "> xyz", "partial")
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not stop")
	}
}

func renderedScreen(screen *synchronizedSimulation) string {
	cells, _, _ := screen.GetContents()
	var b strings.Builder
	for _, cell := range cells {
		b.WriteString(string(cell.Runes))
	}
	return b.String()
}

func TestTranscriptDragCopiesSelectionOnRelease(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := tv.NewApplication().SetScreen(safe).EnableMouse(true)
	view := newTranscriptView()
	view.SetText("selectable reply", true)
	copied := make(chan string, 1)
	clipboard := clipboardFunc(func(_ context.Context, text string) error { copied <- text; return nil })
	u := &terminal{app: app, output: view, ctx: ctx, clipboard: newClipboardQueue(ctx, clipboard, nil, nil)}
	u.chatRootActive.Store(true)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey { return event })
	app.SetMouseCapture(u.captureTranscriptMouse)
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC && u.transcriptFocused() && view.HasSelection() {
			u.copySelection()
			return nil
		}
		return event
	})
	done := make(chan error, 1)
	go func() { done <- app.SetRoot(view, true).SetFocus(view).Run() }()
	defer func() {
		app.Stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("terminal did not stop")
		}
	}()
	waitForScreen(t, safe, "selectable reply", "")
	screen.InjectMouse(0, 0, tcell.ButtonPrimary, tcell.ModNone)
	screen.InjectMouse(10, 0, tcell.ButtonPrimary, tcell.ModNone)
	screen.InjectMouse(10, 0, tcell.ButtonNone, tcell.ModNone)
	select {
	case got := <-copied:
		if got != "selectable" {
			t.Fatalf("drag copied %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mouse release did not copy the transcript selection")
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case got := <-copied:
		if got != "selectable" {
			t.Fatalf("Ctrl+C copied %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ctrl+C did not copy the active transcript selection")
	}
}

func TestEscapeFromModelFilterReturnsToChatAndPreservesSelection(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	model := ui.Model{Key: "openai\x00personal\x00model-x", Provider: "openai", ProfileID: "personal", ProfileLabel: "Personal", ID: "model-x", DisplayName: "Text Model", Compatibility: modelprovider.CompatibilityUnknown}
	client := &clientStub{selectedCh: make(chan struct{}, 2), selected: &model}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	for _, r := range "/model" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	press(tcell.KeyEscape, 0)
	waitForScreen(t, safeScreen, "KompoSyn · Temporary Chat", "/model · Select a model")
	if got := client.Snapshot().Selected; got == nil || got.Key != model.Key {
		t.Fatalf("selection changed after escape: %+v", got)
	}
	press(tcell.KeyCtrlC, 0)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestStopDrainsQueuedWorkerUpdates(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	model := ui.Model{Key: "openai\x00personal\x00model-x", Provider: "openai", ProfileID: "personal", ProfileLabel: "Personal", ID: "model-x", DisplayName: "Text Model", Compatibility: modelprovider.CompatibilityUnknown}
	client := &clientStub{selectedCh: make(chan struct{}, 2), selected: &model}
	sendStarted := make(chan struct{})
	allowDelta := make(chan struct{})
	deltaCalling := make(chan struct{})
	client.sendHook = func(_ context.Context, _ string, delta func(string)) (ui.Turn, error) {
		close(sendStarted)
		<-allowDelta
		close(deltaCalling)
		delta("pending worker update")
		return ui.Turn{Outcome: modelprovider.OutcomeCompleted}, nil
	}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	for _, r := range "hello" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	select {
	case <-sendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("send did not start")
	}
	blocked := make(chan struct{})
	release := make(chan struct{})
	client.mu.Lock()
	client.snapshotHook = func() ui.Snapshot {
		close(blocked)
		<-release
		return ui.Snapshot{Selected: &model, Active: false}
	}
	client.mu.Unlock()
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("input loop did not reach the stop callback")
	}
	close(allowDelta)
	select {
	case <-deltaCalling:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not try to queue its update")
	}
	time.Sleep(40 * time.Millisecond)
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal waited forever for an update after shutdown")
	}
}

func TestCancelButtonCancelsPendingAddOperation(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	started := make(chan context.Context, 1)
	client := &clientStub{selectedCh: make(chan struct{}, 2)}
	client.addHook = func(ctx context.Context, _, _, _, _ string, _ map[string]string) error {
		started <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := &terminal{app: tv.NewApplication(), client: client, ctx: parent, cancel: cancel}
	u.pickerInput = tv.NewInputField()
	u.pickerLayout = tv.NewFlex().AddItem(tv.NewTextView().SetText("Provider picker"), 0, 1, false)
	u.addProvider()
	u.app.SetScreen(safeScreen)
	runDone := make(chan error, 1)
	go func() { runDone <- u.app.Run() }()
	t.Cleanup(func() { stopSimulationTerminal(t, u, runDone) })
	time.Sleep(80 * time.Millisecond)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForScreen(t, safeScreen, "Profile label", "")
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	for _, r := range "test" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyTab, 0)
	for _, r := range "secret" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyTab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	var operation context.Context
	select {
	case operation = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("add operation did not start")
	}
	press(tcell.KeyTab, 0)
	press(tcell.KeyEnter, 0)
	select {
	case <-operation.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel button left the add operation active")
	}
}

func TestProviderAdvertisedOAuthMethodCanBeCanceled(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	started := make(chan string, 1)
	client := &clientStub{selectedCh: make(chan struct{}, 1), methods: map[string][]string{"openai": {"api_key", "oauth"}}}
	client.loginHook = func(ctx context.Context, _ string) (ui.LoginAttempt, error) {
		done := make(chan ui.LoginResult, 1)
		events := make(chan ui.LoginEvent, 1)
		events <- ui.LoginEvent{Kind: "authorization_pending", Message: "Signing in..."}
		attemptID := "oauth-attempt-42"
		started <- attemptID
		go func() { <-ctx.Done(); done <- ui.LoginResult{Err: context.Canceled}; close(done); close(events) }()
		return ui.LoginAttempt{ID: attemptID, Events: events, Done: done}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := &terminal{app: tv.NewApplication(), client: client, ctx: ctx, cancel: cancel}
	u.pickerInput = tv.NewInputField()
	u.pickerLayout = tv.NewFlex().AddItem(tv.NewTextView().SetText("Provider picker"), 0, 1, false)
	u.addProvider()
	u.app.SetScreen(safeScreen)
	finished := make(chan error, 1)
	go func() { finished <- u.app.Run() }()
	press := func(key tcell.Key) { screen.InjectKey(key, 0, tcell.ModNone); time.Sleep(40 * time.Millisecond) }
	waitForScreen(t, safeScreen, "Add New Provider", "")
	press(tcell.KeyTab)
	press(tcell.KeyEnter)
	waitForScreen(t, safeScreen, "Choose an authentication method", "")
	press(tcell.KeyRight)
	press(tcell.KeyEnter)
	select {
	case id := <-started:
		if id != "oauth-attempt-42" {
			t.Fatalf("login attempt ID=%q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider-advertised OAuth choice did not start login")
	}
	waitForScreen(t, safeScreen, "Cancel sign-in", "")
	waitForScreen(t, safeScreen, "Signing in...", "")
	press(tcell.KeyEnter)
	deadline := time.After(2 * time.Second)
	for {
		client.mu.Lock()
		canceled := append([]string(nil), client.loginCanceled...)
		client.mu.Unlock()
		if len(canceled) > 0 {
			if canceled[0] != "oauth-attempt-42" {
				t.Fatalf("canceled attempt=%q", canceled[0])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("Cancel sign-in did not cancel the matching OAuth attempt")
		case <-time.After(10 * time.Millisecond):
		}
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not stop")
	}
}

func TestCancelButtonCancelsPendingReplaceOperation(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	started := make(chan context.Context, 1)
	profile := ui.Profile{ID: "openai-personal", Provider: "openai", Label: "Personal", AuthType: "api_key"}
	client := &clientStub{selectedCh: make(chan struct{}, 2), profiles: []ui.Profile{profile}}
	client.replaceHook = func(ctx context.Context, _, _, _ string, _ map[string]string, _ bool) error {
		started <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := &terminal{app: tv.NewApplication(), client: client, ctx: parent, cancel: cancel}
	u.replaceProfile(profile)
	u.app.SetScreen(safeScreen)
	runDone := make(chan error, 1)
	go func() { runDone <- u.app.Run() }()
	t.Cleanup(func() { stopSimulationTerminal(t, u, runDone) })
	waitForScreen(t, safeScreen, "Replace API key", "")
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	press(tcell.KeyTab, 0) // New API key field.
	for _, r := range "secret" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyTab, 0) // Validate and replace.
	press(tcell.KeyEnter, 0)
	var operation context.Context
	select {
	case operation = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("replace operation did not start")
	}
	press(tcell.KeyTab, 0) // Cancel.
	press(tcell.KeyEnter, 0)
	select {
	case <-operation.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel button left the replace operation active")
	}
	waitForScreen(t, safeScreen, "Personal", "Replace API key")
}

func stopSimulationTerminal(t *testing.T, u *terminal, runDone <-chan error) {
	t.Helper()
	u.stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("simulation UI stopped with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("simulation UI did not stop")
	}
	waitDone := make(chan struct{})
	go func() { u.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Error("simulation UI workers did not stop")
	}
}

func TestSlashCommandSuggestionsFilterCompleteSelectAndDismiss(t *testing.T) {
	client := &clientStub{version: "0.11.0"}
	u := &terminal{client: client, commands: supportedCommands(), input: newComposerInput(), output: newTranscriptView(), commandList: tv.NewList(), composer: tv.NewFlex()}
	u.composer.AddItem(u.commandList, 0, 0, false).AddItem(u.input, 1, 0, true)
	u.input.SetChangedFunc(u.refreshCommandSuggestions)
	u.refreshCommandSuggestions("/m")
	if len(u.commandItems) != 1 || u.commandItems[0].name != "/model" {
		t.Fatalf("/m suggestions = %+v", u.commandItems)
	}
	u.refreshCommandSuggestions("/")
	if len(u.commandItems) != 7 {
		t.Fatalf("root slash suggestions = %+v", u.commandItems)
	}
	u.commandList.SetCurrentItem(1)
	u.completeCommandSuggestion()
	if got := u.input.GetText(); got != "/help " || u.suggestionsActive() {
		t.Fatalf("completion text=%q active=%t", got, u.suggestionsActive())
	}
	u.refreshCommandSuggestions("/he")
	u.selectCommandSuggestion()
	if strings.Contains(u.output.GetText(true), "Commands: /model") == false {
		t.Fatalf("selected /help did not follow command behavior: %q", u.output.GetText(true))
	}
	if u.input.GetText() != "" {
		t.Fatalf("selected command left composer text %q", u.input.GetText())
	}
	u.refreshCommandSuggestions("/not-a-command")
	if u.suggestionsActive() {
		t.Fatal("no-match prefix left command suggestions active")
	}
	u.refreshCommandSuggestions("ordinary draft")
	if u.suggestionsActive() {
		t.Fatal("ordinary draft opened command suggestions")
	}
}

func TestUsageRenderingPreservesUnknownPartialAndCompactCounts(t *testing.T) {
	items := []ui.ModelUsage{{Provider: "openai", ModelID: "model-a",
		Input:  ui.MetricUsage{Tokens: 1234, Known: true, Partial: true},
		Output: ui.MetricUsage{Tokens: 0, Known: true}, CachedRead: ui.MetricUsage{}}}
	compact := renderUsage(items, -1)
	if !strings.Contains(compact, "1.2K (partial)") || !strings.Contains(compact, "Output: 0") || !strings.Contains(compact, "Cached read: unknown") {
		t.Fatalf("compact usage = %q", compact)
	}

}

func TestCompactCountUsesIntegerRounding(t *testing.T) {
	for value, want := range map[int64]string{999: "999", 1000: "1K", 1499: "1.5K", 1200: "1.2K", 999999: "1M", 2500000: "2.5M", 999950000: "1B", 2500000000: "2.5B", 9007199254740993: "9007199.3B"} {
		if got := compactCount(value); got != want {
			t.Errorf("compactCount(%d)=%q, want %q", value, got, want)
		}
	}
}

func TestUsageRowsCanBePagedOnNarrowLayout(t *testing.T) {
	client := &clientStub{usage: []ui.ModelUsage{{Provider: "openai", ModelID: "a"}, {Provider: "anthropic", ModelID: "b"}}}
	u := &terminal{client: client, usagePane: tv.NewTextView(), usageCompact: tv.NewTextView(), composer: tv.NewFlex(), terminalWidth: 70}
	u.composer.AddItem(u.usageCompact, 0, 0, false)
	u.updateUsage()
	u.compactUsageShown = true
	u.updateUsage()
	if got := u.usageCompact.GetText(true); !strings.Contains(got, "openai / a") {
		t.Fatalf("first row: %q", got)
	}
	u.moveUsageCursor(1)
	if got := u.usageCompact.GetText(true); !strings.Contains(got, "anthropic / b") {
		t.Fatalf("second row: %q", got)
	}
}

func TestWideUsageRowsCanBePagedOnOrdinaryHeight(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{usage: []ui.ModelUsage{
		{Provider: "openai", ModelID: "model-one"},
		{Provider: "openai", ModelID: "model-two"},
		{Provider: "anthropic", ModelID: "model-three"},
		{Provider: "anthropic", ModelID: "model-last"},
	}}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	screen.SetSize(110, 24)
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	waitForScreen(t, safeScreen, "Usage · this run", "")
	waitForScreen(t, safeScreen, "model-one", "")
	for range 3 {
		screen.InjectKey(tcell.KeyCtrlN, 0, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	waitForScreen(t, safeScreen, "model-last", "")
	if got := renderedScreen(safeScreen); !strings.Contains(got, "Model 4/4") {
		t.Fatalf("wide usage paging did not reach the last model: %q", got)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wide usage terminal did not stop")
	}
}

func TestWideUsageMetricsScrollWithLongNamesAndPartialValues(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{usage: []ui.ModelUsage{{Provider: "anthropic", ModelID: "model-example-long-name-that-wraps-over-multiple-lines",
		Input:      ui.MetricUsage{Tokens: 1234567, Known: true, Partial: true},
		Output:     ui.MetricUsage{Tokens: 2345678, Known: true},
		CachedRead: ui.MetricUsage{Tokens: 3456789, Known: true, Partial: true},
		CacheWrite: ui.MetricUsage{Tokens: 4567890, Known: true}}}}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	screen.SetSize(110, 24)
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	waitForScreen(t, safeScreen, "Usage · this run", "")
	waitForScreen(t, safeScreen, "Input: 1.2M (partial)", "")
	if strings.Contains(renderedScreen(safeScreen), "Cache write: 4.6M") {
		t.Fatal("regression fixture should require scrolling to reveal cache write")
	}
	var observed strings.Builder
	observed.WriteString(renderedScreen(safeScreen))
	for range 5 {
		screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
		observed.WriteString(renderedScreen(safeScreen))
		if strings.Contains(observed.String(), "Cache write: 4.6M") {
			break
		}
	}
	if !strings.Contains(observed.String(), "Cache write: 4.6M") {
		t.Fatalf("PageDown could not reveal the compact cache-write metric: %q", renderedScreen(safeScreen))
	}
	for _, metric := range []string{"Output: 2.3M", "Cached read: 3.5M"} {
		if !strings.Contains(observed.String(), metric) {
			t.Errorf("scrolling did not expose %q; observed %q", metric, observed.String())
		}
	}
	screen.InjectKey(tcell.KeyPgUp, 0, tcell.ModNone)
	waitForScreen(t, safeScreen, "Model 1/1", "")
	if got := renderedScreen(safeScreen); !strings.Contains(got, "model-example-") || !strings.Contains(got, "long-name") {
		t.Fatalf("scrolling did not return to the model heading: %q", got)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scrollable usage terminal did not stop")
	}
}

func TestClearTranscriptDoesNotClearDraftOrResurrectBufferedText(t *testing.T) {
	u := &terminal{output: newTranscriptView(), input: newComposerInput()}
	u.writeAssistant("before clear")
	u.input.SetText("keep my draft")
	u.clearTranscript()
	u.writeAssistant("after clear")
	if got := u.input.GetText(); got != "keep my draft" {
		t.Fatalf("draft=%q", got)
	}
	if strings.Contains(u.output.GetText(true), "before clear") || !strings.Contains(u.output.GetText(true), "after clear") {
		t.Fatalf("transcript after clear=%q", u.output.GetText(true))
	}
	if len(u.transcript) != 1 || u.transcript[0].text != "after clear" {
		t.Fatalf("render buffer=%+v", u.transcript)
	}
	if got := u.output.GetText(false); strings.Contains(got, "[gray]") || strings.Contains(got, "Assistant:") || !u.transcript[0].assistant {
		t.Fatalf("assistant styling/label = text %q parts %+v", got, u.transcript)
	}
}

func TestBannerVersionSelectionAndResponsiveUsage(t *testing.T) {
	client := &clientStub{version: "0.11.0"}
	model := ui.Model{Key: "selected", ID: "model-y", DisplayName: "Model Y"}
	u := &terminal{client: client, header: tv.NewTextView(), body: tv.NewFlex(), usagePane: tv.NewTextView(), usageCompact: tv.NewTextView(), composer: tv.NewFlex(), input: newComposerInput()}
	u.body.AddItem(tv.NewTextView(), 0, 1, false).AddItem(u.usagePane, 0, 0, false)
	u.composer.AddItem(u.usageCompact, 0, 0, false).AddItem(u.input, 1, 0, true)
	u.updateBanner()
	if got := u.header.GetText(true); !strings.Contains(got, "KompoSyn") || !strings.Contains(got, "v0.11.0") || !strings.Contains(got, "No model selected") {
		t.Fatalf("initial banner = %q", got)
	}
	client.selected = &model
	u.updateBanner()
	if got := u.header.GetText(true); !strings.Contains(got, "Model Y") {
		t.Fatalf("selected model missing from banner: %q", got)
	}
	u.setTerminalWidth(usageWideThreshold)
	if !u.wide {
		t.Fatal("usage pane did not expand at documented threshold")
	}
	u.setTerminalWidth(usageWideThreshold - 1)
	if u.wide {
		t.Fatal("usage pane did not collapse below threshold")
	}
	u.toggleUsage()
	if !u.compactUsageShown {
		t.Fatal("narrow usage control did not expand the compact pane")
	}
	u.toggleUsage()
	if u.compactUsageShown {
		t.Fatal("narrow usage control did not collapse the compact pane")
	}
}

func TestNonSuccessOutcomesAreRendered(t *testing.T) {
	cases := []struct {
		name string
		out  modelprovider.Outcome
		want string
	}{
		{name: "truncated", out: modelprovider.OutcomeTruncated, want: "Response truncated; it was not added to follow-up context."},
		{name: "unsupported", out: modelprovider.OutcomeUnsupported, want: "Provider response unsupported; it was not added to follow-up context."},
		{name: "service failure", out: modelprovider.OutcomeService, want: "Provider service failed; response was not added to follow-up context."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			screen.SetSize(100, 32)
			safeScreen := &synchronizedSimulation{SimulationScreen: screen}
			model := ui.Model{Key: "openai\x00personal\x00model-x", Provider: "openai", ProfileID: "personal", ProfileLabel: "Personal", ID: "model-x", DisplayName: "Text Model", Compatibility: modelprovider.CompatibilityUnknown}
			client := &clientStub{selectedCh: make(chan struct{}, 2), selected: &model}
			client.sendHook = func(_ context.Context, _ string, delta func(string)) (ui.Turn, error) {
				delta("partial reply")
				return ui.Turn{Outcome: tc.out, Text: "partial reply"}, nil
			}
			finished := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
			time.Sleep(150 * time.Millisecond)
			press := func(key tcell.Key, r rune) {
				screen.InjectKey(key, r, tcell.ModNone)
				time.Sleep(10 * time.Millisecond)
			}
			for _, r := range "hello" {
				press(tcell.KeyRune, r)
			}
			press(tcell.KeyEnter, 0)
			waitForScreen(t, safeScreen, tc.want, "")
			press(tcell.KeyCtrlC, 0)
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal did not stop")
			}
		})
	}
}

func TestUnsupportedDiagnosticStatusIsBounded(t *testing.T) {
	got := outcomeStatus(ui.Turn{Outcome: modelprovider.OutcomeUnsupported, DiagnosticCode: modelprovider.DiagnosticOpenAICompletedStatusInProgress}, nil)
	if !strings.Contains(got, string(modelprovider.DiagnosticOpenAICompletedStatusInProgress)) {
		t.Fatalf("safe diagnostic was not shown: %q", got)
	}
	unsafe := outcomeStatus(ui.Turn{Outcome: modelprovider.OutcomeUnsupported, DiagnosticCode: "private provider payload"}, nil)
	if strings.Contains(unsafe, "private") || strings.Contains(unsafe, "payload") || unsafe != "Provider response unsupported; it was not added to follow-up context.\n" {
		t.Fatalf("unrecognized diagnostic was exposed: %q", unsafe)
	}
}

func waitForScreen(t *testing.T, screen *synchronizedSimulation, contains, absent string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cells, _, _ := screen.GetContents()
		var b strings.Builder
		for _, cell := range cells {
			b.WriteString(string(cell.Runes))
		}
		text := b.String()
		if strings.Contains(text, contains) && (absent == "" || !strings.Contains(text, absent)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cells, _, _ := screen.GetContents()
	var b strings.Builder
	for _, cell := range cells {
		b.WriteString(string(cell.Runes))
	}
	t.Fatalf("screen did not contain %q (or still contained %q): %q", contains, absent, b.String())
}

func TestTranscriptSeparatesTurnsAndKeepsThemeForeground(t *testing.T) {
	for _, theme := range []struct {
		name                   string
		foreground, background tcell.Color
	}{
		{"dark", tcell.ColorWhite, tcell.ColorBlack},
		{"light", tcell.ColorBlack, tcell.ColorWhite},
	} {
		t.Run(theme.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			defer screen.Fini()
			screen.SetSize(80, 24)
			view := newTranscriptView()
			if view.GetBackgroundColor() != tcell.ColorDefault {
				t.Fatal("transcript overrides terminal palette")
			}
			view.SetTextStyle(tcell.StyleDefault.Foreground(theme.foreground).Background(theme.background))
			u := &terminal{output: view}
			u.writeUser("Question [red]literal")
			u.writeAssistant("Answer [re")
			u.writeAssistant("d]literal")
			u.write("\n")
			u.writeUser("Follow-up")
			u.writeAssistant("Second answer")
			want := "\nYou: Question [red]literal\n\nAnswer [red]literal\n\nYou: Follow-up\n\nSecond answer"
			if got := view.GetText(true); got != want {
				t.Fatalf("turn spacing or literal output: got %q, want %q", got, want)
			}
			view.SetRect(0, 0, 80, 24)
			view.Draw(screen)
			screen.Show()
			// First reply starts after the prompt and one visibly blank row.
			ch, _, style, _ := screen.GetContent(0, 3)
			fg, bg, attrs := style.Decompose()
			if ch != 'A' || fg != theme.foreground || bg != theme.background || attrs&tcell.AttrDim != 0 {
				t.Fatalf("reply style: char=%q fg=%v bg=%v attrs=%v", ch, fg, bg, attrs)
			}
			for x := 0; x < 80; x++ {
				ch, _, _, _ := screen.GetContent(x, 2)
				if ch != ' ' && ch != 0 {
					t.Fatalf("no blank line before answer at column %d", x)
				}
			}
		})
	}
}

func TestPromptRecallBoundariesDraftAndEditedHistory(t *testing.T) {
	u := &terminal{input: newComposerInput()}
	u.input.SetText("unsent draft")
	u.recallPrompt(-1)
	if u.input.GetText() != "unsent draft" {
		t.Fatal("empty history changed draft")
	}
	u.rememberPrompt("first question")
	u.rememberPrompt("second question")
	u.recallPrompt(1)
	if u.input.GetText() != "unsent draft" {
		t.Fatal("Down outside history changed draft")
	}
	u.recallPrompt(-1)
	if u.input.GetText() != "second question" {
		t.Fatal("Up did not recall most recent prompt")
	}
	u.recallPrompt(-1)
	u.recallPrompt(-1)
	if u.input.GetText() != "first question" {
		t.Fatal("oldest history boundary wrapped")
	}
	u.input.SetText("edited recalled question")
	u.recallPrompt(1)
	u.recallPrompt(1)
	if u.input.GetText() != "unsent draft" {
		t.Fatal("Down did not restore original draft")
	}
	if u.promptHistory[0] != "first question" {
		t.Fatal("editing recall mutated history")
	}
	u.clearTranscript()
	u.recallPrompt(-1)
	if u.input.GetText() != "second question" {
		t.Fatal("display clear removed prompt history")
	}
	fresh := &terminal{input: newComposerInput()}
	fresh.recallPrompt(-1)
	if fresh.input.GetText() != "" {
		t.Fatal("history escaped its terminal instance")
	}
}

func TestPromptHistorySkipsUnsubmittedQuestions(t *testing.T) {
	client := &clientStub{}
	u := &terminal{input: newComposerInput(), output: newTranscriptView(), client: client, commands: supportedCommands()}
	u.input.SetText("  ")
	u.submit()
	u.input.SetText("not sent without a model")
	u.submit()
	client.snapshotHook = func() ui.Snapshot { return ui.Snapshot{Selected: &ui.Model{ID: "model"}, Active: true} }
	u.input.SetText("not sent while busy")
	u.submit()
	u.input.SetText("/help")
	u.submit()
	if len(u.promptHistory) != 0 {
		t.Fatalf("non-submitted input entered history: %q", u.promptHistory)
	}
}

func TestComposerArrowHistoryAndSlashSuggestionsOnSimulation(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	safe := &synchronizedSimulation{SimulationScreen: screen}
	sent := make(chan string, 4)
	client := &clientStub{selected: &ui.Model{Key: "m", ID: "model"}}
	client.sendHook = func(_ context.Context, text string, _ func(string)) (ui.Turn, error) {
		sent <- text
		return ui.Turn{Outcome: modelprovider.OutcomeCompleted}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("terminal did not stop")
		}
	}()
	waitForScreen(t, safe, "KompoSyn", "")
	typeText := func(text string) {
		for _, r := range text {
			screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
	}
	press := func(key tcell.Key) { screen.InjectKey(key, 0, tcell.ModNone) }
	for _, question := range []string{"first question", "second question"} {
		typeText(question)
		press(tcell.KeyEnter)
		select {
		case got := <-sent:
			if got != question {
				t.Fatalf("sent %q, want %q", got, question)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("question not submitted")
		}
	}
	typeText("keep draft")
	press(tcell.KeyUp)
	waitForScreen(t, safe, "> second question", "")
	press(tcell.KeyUp)
	waitForScreen(t, safe, "> first question", "")
	press(tcell.KeyDown)
	waitForScreen(t, safe, "> second question", "")
	press(tcell.KeyDown)
	waitForScreen(t, safe, "> keep draft", "")
	select {
	case got := <-sent:
		t.Fatalf("navigation submitted %q", got)
	default:
	}
	press(tcell.KeyUp)
	press(tcell.KeyEnter)
	select {
	case got := <-sent:
		if got != "second question" {
			t.Fatalf("resubmitted %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recalled question not submitted on Enter")
	}
	typeText("/")
	press(tcell.KeyUp)
	press(tcell.KeyTab)
	waitForScreen(t, safe, "> /copy", "") // Up navigates slash suggestions, not prompt history.
	select {
	case got := <-sent:
		t.Fatalf("completion submitted %q", got)
	default:
	}
}

func TestMultilinePromptHistoryTraversalRestoresDraftOnSimulation(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	sent := make(chan string, 2)
	client := &clientStub{selected: &ui.Model{Key: "fixture", ID: "fixture-model"}}
	client.sendHook = func(_ context.Context, prompt string, _ func(string)) (ui.Turn, error) {
		sent <- prompt
		return ui.Turn{Text: "done", Outcome: modelprovider.OutcomeCompleted}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("terminal did not stop")
		}
	}()
	waitForScreen(t, safe, "KompoSyn", "")
	typeText := func(text string) {
		for _, r := range text {
			screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
	}
	press := func(key tcell.Key) { screen.InjectKey(key, 0, tcell.ModNone) }
	paste := func(text string) {
		screen.PostEventWait(tcell.NewEventPaste(true))
		for _, r := range text {
			if r == '\n' {
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
			} else {
				screen.PostEventWait(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
			}
		}
		screen.PostEventWait(tcell.NewEventPaste(false))
	}
	submitPasted := func(text string) {
		paste(text)
		press(tcell.KeyEnter)
		select {
		case got := <-sent:
			if got != text {
				t.Fatalf("submitted prompt=%q, want %q", got, text)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("pasted prompt was not submitted")
		}
	}
	submitPasted("older prompt")
	submitPasted("first\nsecond")
	typeText("keep draft")
	waitForScreen(t, safe, "> keep draft", "")
	press(tcell.KeyUp)
	waitForScreen(t, safe, "> first", "")
	waitForScreen(t, safe, "second", "")
	press(tcell.KeyUp)
	waitForScreen(t, safe, "> older prompt", "")
	press(tcell.KeyDown)
	waitForScreen(t, safe, "> first", "")
	press(tcell.KeyDown)
	waitForScreen(t, safe, "> keep draft", "")
	select {
	case got := <-sent:
		t.Fatalf("history navigation submitted %q", got)
	default:
	}
}

func TestBracketedTrailingNewlineSlashPasteStaysLiteralUntilEnter(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safe := &synchronizedSimulation{SimulationScreen: screen}
	sent := make(chan string, 1)
	client := &clientStub{selected: &ui.Model{Key: "fixture", ID: "fixture-model"}}
	client.sendHook = func(_ context.Context, prompt string, _ func(string)) (ui.Turn, error) {
		sent <- prompt
		return ui.Turn{Text: "done", Outcome: modelprovider.OutcomeCompleted}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("terminal did not stop")
		}
	}()
	waitForScreen(t, safe, "KompoSyn", "")
	if err := screen.PostEvent(tcell.NewEventPaste(true)); err != nil {
		t.Fatal(err)
	}
	for _, r := range "/exit\n" {
		if r == '\n' {
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
		} else {
			screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
	}
	if err := screen.PostEvent(tcell.NewEventPaste(false)); err != nil {
		t.Fatal(err)
	}
	waitForScreen(t, safe, "> /exit", "")
	select {
	case got := <-sent:
		t.Fatalf("paste submitted before an explicit Enter: %q", got)
	case <-time.After(40 * time.Millisecond):
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case got := <-sent:
		if got != "/exit\n" {
			t.Fatalf("pasted prompt=%q, want %q", got, "/exit\n")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("explicit Enter did not submit the pasted slash text as a prompt")
	}
}

func TestExplicitClipboardCopyUsesPlainReplyAndTranscript(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	written := make(chan string, 4)
	u := &terminal{screen: screen, output: newTranscriptView(), input: newComposerInput(), commands: supportedCommands()}
	u.clipboard = newClipboardQueue(ctx, clipboardFunc(func(_ context.Context, text string) error {
		screen.SetClipboard([]byte(text))
		written <- text
		return nil
	}), nil, nil)
	u.input.SetText("keep draft")
	u.writeUser("Question")
	u.writeAssistant("literal [re")
	u.writeAssistant("d] answer")
	if got := screen.GetClipboardData(); len(got) != 0 {
		t.Fatal("output wrote clipboard without user action")
	}
	u.latestCompletedReply = "literal [red] answer"
	u.copyTranscript(false)
	if got := <-written; got != "literal [red] answer" {
		t.Fatalf("written reply=%q", got)
	}
	if got := string(screen.GetClipboardData()); got != "literal [red] answer" {
		t.Fatalf("clipboard reply=%q", got)
	}
	if u.input.GetText() != "keep draft" {
		t.Fatal("copy discarded draft")
	}
	u.clearTranscript()
	u.writeAssistant(" tail")
	u.copyTranscript(false)
	if got := <-written; got != "literal [red] answer" {
		t.Fatalf("written reply after clear=%q", got)
	}
	if got := string(screen.GetClipboardData()); got != "literal [red] answer" {
		t.Fatalf("clear or streaming text changed completed reply copy=%q", got)
	}
	u.runCommand("/copy all")
	want := " tail"
	if got := <-written; got != want {
		t.Fatalf("logical transcript=%q, want %q", got, want)
	}
	if got := string(screen.GetClipboardData()); got != want {
		t.Fatalf("clipboard transcript=%q, want %q", got, want)
	}
	u.latestCompletedReply = ""
	screen.SetClipboard([]byte("existing"))
	u.copyTranscript(false)
	if string(screen.GetClipboardData()) != "existing" {
		t.Fatal("empty reply cleared clipboard")
	}
}

type clipboardFunc func(context.Context, string) error

func (f clipboardFunc) Write(ctx context.Context, text string) error { return f(ctx, text) }

func TestCopyShortcutAndTerminalSelectionToggle(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	safe := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{selected: &ui.Model{Key: "m", ID: "fixture"}}
	client.sendHook = func(_ context.Context, _ string, delta func(string)) (ui.Turn, error) {
		delta("copy me [red]literal")
		return ui.Turn{Text: "copy me [red]literal", Outcome: modelprovider.OutcomeCompleted}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Plugin{screen: safe, clipboard: clipboardFunc(func(_ context.Context, text string) error {
			screen.SetClipboard([]byte(text))
			return nil
		})}).Run(ctx, client)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("terminal did not stop")
		}
	}()
	waitForScreen(t, safe, "KompoSyn", "")
	for _, r := range "question" {
		screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForScreen(t, safe, "copy me [red]literal", "")
	for _, r := range "/copy" {
		screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForScreen(t, safe, "Copied using the native clipboard helper", "")
	if got := string(screen.GetClipboardData()); got != "copy me [red]literal" {
		t.Fatalf("/copy copied %q", got)
	}
	screen.InjectKey(tcell.KeyCtrlT, 0, tcell.ModNone)
	waitForScreen(t, safe, "Mouse capture released", "")
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	waitForScreen(t, safe, "> x", "")
	screen.InjectKey(tcell.KeyCtrlT, 0, tcell.ModNone)
	waitForScreen(t, safe, "Mouse capture enabled", "")
}
