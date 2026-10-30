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
	mu            sync.Mutex
	selected      *ui.Model
	turns         []ui.Turn
	usage         []ui.ModelUsage
	version       string
	selectedCh    chan struct{}
	selectCalls   int
	profiles      []ui.Profile
	addHook       func(context.Context, string, string, string, string, map[string]string) error
	replaceHook   func(context.Context, string, string, string, map[string]string, bool) error
	snapshotHook  func() ui.Snapshot
	sendHook      func(context.Context, string, func(string)) (ui.Turn, error)
	methods       map[string][]string
	loginHook     func(context.Context, string) (ui.LoginAttempt, error)
	loginCanceled []string
}

// synchronizedSimulation protects the returned cell slice from tview's render
// goroutine while the test inspects a snapshot of the simulated screen.
type synchronizedSimulation struct {
	tcell.SimulationScreen
	mu sync.Mutex
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
	snapshot := ui.Snapshot{Selected: c.selected, Turns: append([]ui.Turn(nil), c.turns...), Usage: append([]ui.ModelUsage(nil), c.usage...), Version: c.version}
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
func (c *clientStub) Cancel() {}

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
	u := &terminal{client: client, commands: supportedCommands(), input: tv.NewInputField(), output: tv.NewTextView(), commandList: tv.NewList(), composer: tv.NewFlex()}
	u.composer.AddItem(u.commandList, 0, 0, false).AddItem(u.input, 1, 0, true)
	u.input.SetChangedFunc(u.refreshCommandSuggestions)
	u.refreshCommandSuggestions("/m")
	if len(u.commandItems) != 1 || u.commandItems[0].name != "/model" {
		t.Fatalf("/m suggestions = %+v", u.commandItems)
	}
	u.refreshCommandSuggestions("/")
	if len(u.commandItems) != 4 {
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

func TestUsageRenderingPreservesUnknownPartialAndExactCounts(t *testing.T) {
	items := []ui.ModelUsage{{Provider: "openai", ModelID: "model-a",
		Input:  ui.MetricUsage{Tokens: 1234, Known: true, Partial: true},
		Output: ui.MetricUsage{Tokens: 0, Known: true}, CachedRead: ui.MetricUsage{}}}
	compact := renderUsage(items, false, -1)
	if !strings.Contains(compact, "1.2K (partial)") || !strings.Contains(compact, "Output: 0") || !strings.Contains(compact, "Cached read: unknown") {
		t.Fatalf("compact usage = %q", compact)
	}
	exact := renderUsage(items, true, -1)
	if !strings.Contains(exact, "1234 (partial)") || !strings.Contains(exact, "Output: 0") {
		t.Fatalf("exact usage = %q", exact)
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

func TestWideUsageMetricsScrollWithExactLongAndPartialValues(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(80, 24)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{usage: []ui.ModelUsage{{Provider: "anthropic", ModelID: "model-example-long-name",
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
	screen.InjectKey(tcell.KeyCtrlE, 0, tcell.ModNone)
	waitForScreen(t, safeScreen, "Input: 1234567 (partial)", "")
	if strings.Contains(renderedScreen(safeScreen), "Cache write: 4567890") {
		t.Fatal("regression fixture should require scrolling to reveal cache write")
	}
	var observed strings.Builder
	observed.WriteString(renderedScreen(safeScreen))
	for range 5 {
		screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
		observed.WriteString(renderedScreen(safeScreen))
		if strings.Contains(observed.String(), "Cache write: 4567890") {
			break
		}
	}
	if !strings.Contains(observed.String(), "Cache write: 4567890") {
		t.Fatalf("PageDown could not reveal the exact cache-write metric: %q", renderedScreen(safeScreen))
	}
	for _, metric := range []string{"Output: 2345678", "Cached read: 3456789"} {
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
	u := &terminal{output: tv.NewTextView().SetDynamicColors(true), input: tv.NewInputField()}
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
	if got := u.output.GetText(false); !strings.Contains(got, "[gray]") || strings.Contains(got, "Assistant:") || !u.transcript[0].assistant {
		t.Fatalf("assistant styling/label = text %q parts %+v", got, u.transcript)
	}
}

func TestBannerVersionSelectionAndResponsiveUsage(t *testing.T) {
	client := &clientStub{version: "0.11.0"}
	model := ui.Model{Key: "selected", ID: "model-y", DisplayName: "Model Y"}
	u := &terminal{client: client, header: tv.NewTextView(), body: tv.NewFlex(), usagePane: tv.NewTextView(), usageCompact: tv.NewTextView(), composer: tv.NewFlex(), input: tv.NewInputField()}
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
