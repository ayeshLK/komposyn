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
	snapshot := ui.Snapshot{Selected: c.selected}
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
	// Enter leaves the filter field for the picker list; three Down presses move
	// from Add New Provider through the two persistent actions to the model.
	press(tcell.KeyEnter, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0)
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
