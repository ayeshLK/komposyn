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

// Package tview provides the initial experimental terminal UI. It owns input,
// filtering, focus, rendering, and scroll state; chat semantics stay in Client.
package tview

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/ui"
	"github.com/gdamore/tcell/v2"
	tv "github.com/rivo/tview"
)

type Plugin struct{ screen tcell.Screen }

func (Plugin) ID() string          { return "tview" }
func (Plugin) DisplayName() string { return "tview" }
func New() Plugin                  { return Plugin{} }

type entry struct {
	label  string
	action string
	model  *ui.Model
}

type command struct{ name, description, action string }

type outputPart struct {
	text      string
	markup    bool
	assistant bool
}

const usageWideThreshold = 100

type credentialOperation struct {
	ctx     context.Context
	cancel  context.CancelFunc
	loginID string
}

type terminal struct {
	app               *tv.Application
	done              chan struct{}
	client            ui.Client
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	lifecycleMu       sync.Mutex
	stopping          bool
	stopOnce          sync.Once
	closing           atomic.Bool
	runReturned       atomic.Bool
	output            *tv.TextView
	transcript        []outputPart
	header            *tv.TextView
	input             *tv.InputField
	layout            *tv.Flex
	body              *tv.Flex
	usagePane         *tv.TextView
	footer            *tv.TextView
	composer          *tv.Flex
	commandList       *tv.List
	usageCompact      *tv.TextView
	commands          []command
	commandItems      []command
	usageVisible      bool
	wide              bool
	layoutReady       bool
	compactUsageShown bool
	usageExact        bool
	usageCursor       int
	terminalWidth     int
	pickerQuery       string
	picker            *tv.List
	modelList         *tv.List
	pickerInput       *tv.InputField
	pickerStatus      *tv.TextView
	pickerLayout      *tv.Flex
	entries           []entry
	modelEntries      []entry
	discovery         ui.Discovery
	credentialOp      *credentialOperation
}

func (u *terminal) beginCredentialOperation() *credentialOperation {
	u.cancelCredentialOperation(u.credentialOp)
	ctx, cancel := context.WithCancel(u.ctx)
	op := &credentialOperation{ctx: ctx, cancel: cancel}
	u.credentialOp = op
	return op
}

func (u *terminal) cancelCredentialOperation(op *credentialOperation) {
	if op == nil {
		return
	}
	if u.credentialOp == op {
		u.credentialOp = nil
	}
	op.cancel()
	if op.loginID != "" {
		u.client.CancelLogin(op.loginID)
	}
}

func (p Plugin) Run(parent context.Context, client ui.Client) error {
	ctx, cancel := context.WithCancel(parent)
	u := &terminal{app: tv.NewApplication(), client: client, ctx: ctx, cancel: cancel, usageVisible: true}
	u.output = tv.NewTextView().SetDynamicColors(true).SetScrollable(true)
	u.usagePane = tv.NewTextView().SetScrollable(true)
	u.usagePane.SetBorder(true).SetTitle(" Usage · this run ")
	u.body = tv.NewFlex().SetDirection(tv.FlexColumn).AddItem(u.output, 0, 1, false).AddItem(u.usagePane, 0, 0, false)
	u.header = tv.NewTextView().SetDynamicColors(true).SetTextAlign(tv.AlignLeft)
	u.footer = tv.NewTextView().SetText("Tab focus · Ctrl+U usage · Ctrl+N/P models · Ctrl+E exact · PgUp/Dn scroll · Ctrl+L clear · Ctrl+C cancel")
	u.commandList = tv.NewList().ShowSecondaryText(false)
	u.usageCompact = tv.NewTextView().SetScrollable(true)
	u.commands = supportedCommands()
	u.input = tv.NewInputField().SetLabel("> ").SetFieldWidth(0).SetChangedFunc(func(text string) { u.refreshCommandSuggestions(text) })
	u.composer = tv.NewFlex().SetDirection(tv.FlexRow).AddItem(u.commandList, 0, 0, false).AddItem(u.usageCompact, 0, 0, false).AddItem(u.input, 1, 0, true)
	u.layout = tv.NewFlex().SetDirection(tv.FlexRow).AddItem(u.header, 1, 0, false).AddItem(u.body, 0, 1, false).AddItem(u.composer, 0, 1, true).AddItem(u.footer, 1, 0, false)
	u.layout.SetBorder(true).SetTitle(" KompoSyn · Temporary Chat ")
	u.updateBanner()
	u.updateUsage()
	u.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		width, _ := screen.Size()
		u.setTerminalWidth(width)
		return false
	})
	u.input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			if u.suggestionsActive() {
				u.selectCommandSuggestion()
			} else {
				u.submit()
			}
		case tcell.KeyEscape:
			u.dismissCommandSuggestions()
		}
	})
	u.input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if u.suggestionsActive() {
			switch event.Key() {
			case tcell.KeyUp:
				u.moveCommandSuggestion(-1)
				return nil
			case tcell.KeyDown:
				u.moveCommandSuggestion(1)
				return nil
			case tcell.KeyTab:
				u.completeCommandSuggestion()
				return nil
			case tcell.KeyEscape:
				u.dismissCommandSuggestions()
				return nil
			}
		}
		return event
	})
	u.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			if u.client.Snapshot().Active {
				u.client.Cancel()
				u.write("\nCancelling the active request…\n")
			} else {
				u.stop()
			}
			return nil
		}
		if event.Key() == tcell.KeyCtrlU {
			u.toggleUsage()
			return nil
		}
		if event.Key() == tcell.KeyCtrlL {
			u.clearTranscript()
			return nil
		}
		if event.Key() == tcell.KeyCtrlE {
			u.usageExact = !u.usageExact
			u.updateUsage()
			return nil
		}
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			direction := 1
			if event.Key() == tcell.KeyCtrlP {
				direction = -1
			}
			u.moveUsageCursor(direction)
			return nil
		}
		if event.Key() == tcell.KeyPgDn || event.Key() == tcell.KeyPgUp {
			if !((u.wide && u.usageVisible) || (!u.wide && u.compactUsageShown)) {
				return event
			}
			direction := 1
			if event.Key() == tcell.KeyPgUp {
				direction = -1
			}
			u.scrollUsage(direction)
			return nil
		}
		return event
	})
	u.writeMarkup("[gray]KompoSyn is ready. Chat state is temporary and will be cleared on exit. Run /model to connect a provider.[-]\n")
	u.done = make(chan struct{})
	ready := make(chan struct{})
	var readyOnce sync.Once
	u.app.SetAfterDrawFunc(func(tcell.Screen) { readyOnce.Do(func() { close(ready) }) })
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-ready:
				u.stop()
			case <-u.done:
			}
		case <-u.done:
		}
	}()
	if p.screen != nil {
		u.app.SetScreen(p.screen)
		width, _ := p.screen.Size()
		u.setTerminalWidth(width)
	}
	err := u.app.SetRoot(u.layout, true).EnableMouse(true).SetFocus(u.input).Run()
	u.runReturned.Store(true)
	close(u.done)
	u.stop()
	u.wg.Wait()
	if err != nil {
		return fmt.Errorf("run terminal UI: %w", err)
	}
	return nil
}

func (u *terminal) write(s string)          { u.appendOutput(s, false, false) }
func (u *terminal) writeMarkup(s string)    { u.appendOutput(s, true, false) }
func (u *terminal) writeAssistant(s string) { u.appendOutput(s, false, true) }

func (u *terminal) appendOutput(text string, markup, assistant bool) {
	if text == "" || u.output == nil {
		return
	}
	if !markup && len(u.transcript) > 0 && !u.transcript[len(u.transcript)-1].markup && u.transcript[len(u.transcript)-1].assistant == assistant {
		u.transcript[len(u.transcript)-1].text += text
	} else {
		u.transcript = append(u.transcript, outputPart{text: text, markup: markup, assistant: assistant})
	}
	u.output.Clear()
	for _, part := range u.transcript {
		if part.markup {
			fmt.Fprint(u.output, part.text)
		} else if part.assistant {
			fmt.Fprint(u.output, "[gray]"+escapeLiteralText(part.text)+"[-]")
		} else {
			fmt.Fprint(u.output, escapeLiteralText(part.text))
		}
	}
	u.output.ScrollToEnd()
}

func escapeLiteralText(text string) string {
	return tv.Escape(text)
}
func (u *terminal) queue(update func()) {
	if u.closing.Load() || u.ctx.Err() != nil {
		return
	}
	u.app.QueueUpdateDraw(func() {
		if !u.closing.Load() {
			update()
		}
	})
}
func (u *terminal) writeAsync(s string) {
	u.queue(func() { u.appendOutput(s, false, false) })
}

func (u *terminal) writeAssistantAsync(s string) {
	u.queue(func() { u.appendOutput(s, false, true) })
}

func (u *terminal) clearTranscript() {
	u.transcript = nil
	if u.output != nil {
		u.output.Clear()
		u.output.ScrollToBeginning()
	}
}

func supportedCommands() []command {
	return []command{
		{name: "/model", description: "open provider and model setup", action: "picker"},
		{name: "/help", description: "show available commands", action: "help"},
		{name: "/cancel", description: "stop the active reply", action: "cancel"},
		{name: "/exit", description: "close KompoSyn", action: "exit"},
	}
}

func (u *terminal) suggestionsActive() bool {
	return u.commandList != nil && u.commandList.GetItemCount() > 0
}

func (u *terminal) refreshCommandSuggestions(text string) {
	if u.commandList == nil || u.composer == nil {
		return
	}
	query := strings.TrimSpace(text)
	if !strings.HasPrefix(query, "/") || strings.ContainsAny(query, " \t\n") {
		u.commandItems = nil
		u.commandList.Clear()
		u.composer.ResizeItem(u.commandList, 0, 0)
		return
	}
	prefix := strings.ToLower(query)
	u.commandItems = u.commandItems[:0]
	u.commandList.Clear()
	for _, item := range u.commands {
		if strings.HasPrefix(item.name, prefix) {
			u.commandItems = append(u.commandItems, item)
			u.commandList.AddItem(item.name+"  ·  "+item.description, "", 0, nil)
		}
	}
	if len(u.commandItems) == 0 {
		u.composer.ResizeItem(u.commandList, 0, 0)
		return
	}
	u.commandList.SetCurrentItem(0)
	u.composer.ResizeItem(u.commandList, min(4, len(u.commandItems)), 0)
}

func (u *terminal) dismissCommandSuggestions() {
	if u.commandList != nil {
		u.commandItems = nil
		u.commandList.Clear()
		u.composer.ResizeItem(u.commandList, 0, 0)
	}
}

func (u *terminal) moveCommandSuggestion(delta int) {
	if !u.suggestionsActive() {
		return
	}
	index := u.commandList.GetCurrentItem() + delta
	if index < 0 {
		index = len(u.commandItems) - 1
	} else if index >= len(u.commandItems) {
		index = 0
	}
	u.commandList.SetCurrentItem(index)
}

func (u *terminal) completeCommandSuggestion() {
	if !u.suggestionsActive() {
		return
	}
	index := u.commandList.GetCurrentItem()
	if index < 0 || index >= len(u.commandItems) {
		index = 0
	}
	u.input.SetText(u.commandItems[index].name + " ")
	u.dismissCommandSuggestions()
}

func (u *terminal) selectCommandSuggestion() {
	if !u.suggestionsActive() {
		return
	}
	index := u.commandList.GetCurrentItem()
	if index < 0 || index >= len(u.commandItems) {
		index = 0
	}
	selected := u.commandItems[index].name
	u.dismissCommandSuggestions()
	u.input.SetText(selected)
	u.runCommand(selected)
}

func (u *terminal) updateBanner() {
	if u.header == nil || u.client == nil {
		return
	}
	snapshot := u.client.Snapshot()
	modelName := "No model selected"
	if snapshot.Selected != nil {
		modelName = snapshot.Selected.DisplayName
		if modelName == "" {
			modelName = snapshot.Selected.ID
		}
	}
	version := snapshot.Version
	if version == "" {
		version = "dev"
	}
	u.header.SetText(fmt.Sprintf("[white::b]KompoSyn[-:-:-]  [gray]v%s[-]  ·  [white]%s[-]", tv.Escape(version), tv.Escape(modelName)))
}

func (u *terminal) updateUsage() {
	if u.usagePane == nil || u.client == nil {
		return
	}
	usage := u.client.Snapshot().Usage
	// Page one model at a time in both layouts. A fixed column width does not
	// imply enough height for every wrapped model row.
	selected := u.usageCursor
	text := renderUsage(usage, u.usageExact, selected)
	if !u.wide && u.compactUsageShown {
		text = renderUsage(usage, u.usageExact, u.usageCursor)
	}
	u.usagePane.SetText(text)
	u.usagePane.ScrollToBeginning()
	if u.usageCompact != nil {
		u.usageCompact.SetText(text)
		u.usageCompact.ScrollToBeginning()
		if u.compactUsageShown {
			u.composer.ResizeItem(u.usageCompact, compactUsageVisibleRows(text, u.terminalWidth), 0)
		}
	}
}

func (u *terminal) scrollUsage(direction int) {
	view := u.usagePane
	if !u.wide && u.compactUsageShown {
		view = u.usageCompact
	}
	if view == nil {
		return
	}
	row, _ := view.GetScrollOffset()
	_, _, _, height := view.GetRect()
	step := max(1, height-3)
	view.ScrollTo(max(0, row+direction*step), 0)
}

func (u *terminal) moveUsageCursor(delta int) {
	count := len(u.client.Snapshot().Usage)
	if count == 0 {
		return
	}
	u.usageCursor = (u.usageCursor + delta + count) % count
	u.updateUsage()
	if !u.wide && u.compactUsageShown {
		u.composer.ResizeItem(u.usageCompact, compactUsageVisibleRows(u.usageCompact.GetText(true), u.terminalWidth), 0)
	}
}

func renderUsage(items []ui.ModelUsage, exact bool, selected int) string {
	if len(items) == 0 {
		return "No model usage yet.\nCtrl+U: show usage"
	}
	start, end := 0, len(items)
	if selected >= 0 {
		selected %= len(items)
		start, end = selected, selected+1
	}
	var b strings.Builder
	if selected >= 0 {
		fmt.Fprintf(&b, "Model %d/%d · Ctrl+N/P\n", start+1, len(items))
	}
	b.WriteString("Ctrl+E: exact/compact\n")
	for i := start; i < end; i++ {
		item := items[i]
		fmt.Fprintf(&b, "%s / %s\nInput: %s\nOutput: %s\nCached read: %s\nCache write: %s\n",
			tv.Escape(item.Provider), tv.Escape(item.ModelID), formatMetric(item.Input, exact), formatMetric(item.Output, exact), formatMetric(item.CachedRead, exact), formatMetric(item.CacheWrite, exact))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func formatMetric(metric ui.MetricUsage, exact bool) string {
	if !metric.Known {
		return "unknown"
	}
	value := fmt.Sprintf("%d", metric.Tokens)
	if !exact {
		value = compactCount(metric.Tokens)
	}
	if metric.Partial {
		value += " (partial)"
	}
	return value
}

func compactCount(value int64) string {
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	unit, suffix := int64(1000), "K"
	if value >= 999_950 {
		unit, suffix = 1_000_000, "M"
	}
	if value >= 999_950_000 {
		unit, suffix = 1_000_000_000, "B"
	}
	whole, remainder := value/unit, value%unit
	tenth := (remainder*10 + unit/2) / unit
	if tenth == 10 {
		whole++
		tenth = 0
	}
	if tenth == 0 {
		return fmt.Sprintf("%d%s", whole, suffix)
	}
	return fmt.Sprintf("%d.%d%s", whole, tenth, suffix)
}

func compactUsageRows(text string, width int) int {
	innerWidth := max(1, width-2)
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		rows += max(1, (len(line)+innerWidth-1)/innerWidth)
	}
	return max(2, rows)
}

func compactUsageVisibleRows(text string, width int) int {
	return min(5, compactUsageRows(text, width))
}

func (u *terminal) toggleUsage() {
	if !u.wide {
		u.compactUsageShown = !u.compactUsageShown
		u.updateUsage()
		if u.compactUsageShown {
			u.composer.ResizeItem(u.usageCompact, compactUsageVisibleRows(u.usageCompact.GetText(true), u.terminalWidth), 0)
		} else {
			u.composer.ResizeItem(u.usageCompact, 0, 0)
		}
		return
	}
	u.usageVisible = !u.usageVisible
	if u.usageVisible {
		u.body.ResizeItem(u.usagePane, 28, 0)
	} else {
		u.body.ResizeItem(u.usagePane, 0, 0)
	}
}

func (u *terminal) setTerminalWidth(width int) {
	wide := width >= usageWideThreshold
	u.terminalWidth = width
	if u.body == nil || u.usagePane == nil {
		return
	}
	if u.layoutReady && wide == u.wide {
		if !wide && u.compactUsageShown {
			u.composer.ResizeItem(u.usageCompact, compactUsageVisibleRows(u.usageCompact.GetText(true), width), 0)
		}
		return
	}
	u.layoutReady = true
	u.wide = wide
	if wide {
		u.compactUsageShown = false
		u.composer.ResizeItem(u.usageCompact, 0, 0)
		if u.usageVisible {
			u.body.ResizeItem(u.usagePane, 28, 0)
		} else {
			u.body.ResizeItem(u.usagePane, 0, 0)
		}
	} else {
		u.body.ResizeItem(u.usagePane, 0, 0)
		u.compactUsageShown = false
		u.composer.ResizeItem(u.usageCompact, 0, 0)
	}
}
func (u *terminal) launch(work func()) {
	u.lifecycleMu.Lock()
	if u.stopping {
		u.lifecycleMu.Unlock()
		return
	}
	u.wg.Add(1)
	u.lifecycleMu.Unlock()
	go func() { defer u.wg.Done(); work() }()
}
func (u *terminal) stop() {
	u.stopOnce.Do(func() {
		u.lifecycleMu.Lock()
		u.stopping = true
		u.lifecycleMu.Unlock()
		u.closing.Store(true)
		u.cancel()
		u.client.Cancel()
		go func() {
			u.wg.Wait()
			if !u.runReturned.Load() {
				u.app.Stop()
			}
		}()
	})
}

func (u *terminal) submit() {
	text := u.input.GetText()
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if strings.HasPrefix(trimmed, "/") {
		u.runCommand(trimmed)
		return
	}
	snapshot := u.client.Snapshot()
	if snapshot.Selected == nil {
		u.write("Choose a model first with /model. Your draft is preserved.\n")
		return
	}
	if snapshot.Active {
		u.write("A reply is active. Your draft is preserved; press Ctrl+C to cancel.\n")
		return
	}
	u.input.SetText("")
	u.writeMarkup("\n[white::b]You:[-:-:-] " + tv.Escape(trimmed) + "\n")
	u.launch(func() {
		turn, err := u.client.Send(u.ctx, trimmed, func(delta string) { u.writeAssistantAsync(delta) })
		u.writeAsync("\n")
		if turn.Outcome == modelprovider.OutcomeRefused {
			if turn.Refusal != "" {
				u.writeAsync("Provider refusal: " + turn.Refusal + "\n")
			} else {
				u.writeAsync("The provider refused this request.\n")
			}
		}
		u.writeAsync(outcomeStatus(turn, err))
		u.queue(func() {
			u.updateUsage()
			u.updateBanner()
		})
	})
}

func (u *terminal) runCommand(text string) {
	trimmed := strings.TrimSpace(text)
	name := strings.Fields(trimmed)[0]
	if name == "/model" && trimmed != "/model" {
		u.input.SetText("")
		u.write("Use /model to open the provider and model picker. No command arguments were used.\n")
		return
	}
	var selected *command
	for i := range u.commands {
		if u.commands[i].name == name {
			selected = &u.commands[i]
			break
		}
	}
	if selected == nil {
		u.write("Unknown command. Use /help.\n")
		u.input.SetText("")
		return
	}
	switch selected.action {
	case "picker":
		u.openPicker()
	case "help":
		var descriptions []string
		for _, item := range u.commands {
			descriptions = append(descriptions, item.name+" "+item.description)
		}
		u.write("Commands: " + strings.Join(descriptions, "; ") + ". Keyboard: Ctrl+L clears the display; Ctrl+U shows usage; Ctrl+E toggles exact counts; Ctrl+N/P changes model rows; PageUp/PageDown scroll usage details.\n")
	case "cancel":
		u.client.Cancel()
		u.write("Cancellation requested.\n")
	case "exit":
		u.stop()
	}
	u.input.SetText("")
}

func (u *terminal) openPicker() {
	u.pickerInput = tv.NewInputField().SetLabel("Filter models and actions: ").SetFieldWidth(0).SetText(u.pickerQuery).SetChangedFunc(func(v string) { u.pickerQuery = v; u.rebuildPicker() })
	u.picker = tv.NewList().ShowSecondaryText(true)
	u.modelList = tv.NewList().ShowSecondaryText(true)
	for _, list := range []*tv.List{u.picker, u.modelList} {
		list.SetSelectedFocusOnly(true)
	}
	u.pickerStatus = tv.NewTextView()
	actionHeading := tv.NewTextView().SetText("PROVIDER ACTIONS")
	modelHeading := tv.NewTextView().SetText("AVAILABLE MODELS")
	u.pickerLayout = tv.NewFlex().SetDirection(tv.FlexRow).AddItem(u.pickerInput, 1, 0, true).AddItem(actionHeading, 1, 0, false).AddItem(u.picker, 6, 0, false).AddItem(modelHeading, 1, 0, false).AddItem(u.modelList, 0, 1, false).AddItem(u.pickerStatus, 2, 0, false)
	u.pickerLayout.SetBorder(true).SetTitle(" /model · Select a model or manage providers ")
	u.pickerInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			u.app.SetFocus(u.picker)
		} else if key == tcell.KeyEscape {
			u.returnToChat()
		}
	})
	u.pickerInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			u.app.SetFocus(u.picker)
			return nil
		case tcell.KeyBacktab:
			u.app.SetFocus(u.modelList)
			return nil
		}
		return event
	})
	u.picker.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			u.returnToChat()
			return nil
		}
		if event.Key() == tcell.KeyTab {
			u.app.SetFocus(u.modelList)
			return nil
		}
		if event.Key() == tcell.KeyBacktab {
			u.app.SetFocus(u.pickerInput)
			return nil
		}
		return event
	})
	u.modelList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			u.returnToChat()
			return nil
		}
		if event.Key() == tcell.KeyTab {
			u.app.SetFocus(u.pickerInput)
			return nil
		}
		if event.Key() == tcell.KeyBacktab {
			u.app.SetFocus(u.picker)
			return nil
		}
		return event
	})
	u.rebuildPicker()
	u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
	u.refreshModels()
}

func (u *terminal) rebuildPicker() {
	if u.picker == nil || u.modelList == nil {
		return
	}
	selected := u.client.Snapshot().Selected
	query := strings.ToLower(strings.TrimSpace(u.pickerQuery))
	u.entries = nil
	u.modelEntries = nil
	u.picker.Clear()
	u.modelList.Clear()
	addAction := func(e entry, secondary string, shortcut rune) {
		u.entries = append(u.entries, e)
		u.picker.AddItem(e.label, secondary, shortcut, func() { u.activate(e) })
	}
	addAction(entry{label: "＋ Add New Provider", action: "add"}, "Choose ChatGPT sign-in or an API key", 'a')
	addAction(entry{label: "↻ Refresh Models", action: "refresh"}, "Fetch the current provider catalogs", 'r')
	addAction(entry{label: "⚙ Manage Providers", action: "manage"}, "Review profile metadata, replace keys, or remove profiles", 'p')
	addModel := func(e entry, secondary string) {
		u.modelEntries = append(u.modelEntries, e)
		u.modelList.AddItem(e.label, secondary, 0, func() { u.activate(e) })
	}
	for _, m := range u.discovery.Models {
		mark := ""
		if selected != nil && selected.Key == m.Key {
			mark = " ✓ current"
		}
		compat := string(m.Compatibility)
		if compat == "" {
			compat = "unknown"
		}
		label := fmt.Sprintf("%s · %s · %s%s", m.Provider, m.ProfileLabel, m.DisplayName, mark)
		secondary := fmt.Sprintf("%s · %s · %s", m.ProfileID, m.ID, compat)
		e := entry{label: label, action: "select", model: &m}
		if query == "" || strings.Contains(strings.ToLower(label+" "+secondary), query) {
			addModel(e, secondary)
		}
	}
	if len(u.discovery.Models) == 0 {
		u.pickerStatus.SetText("No models discovered yet. Add a provider or refresh after configuring one.")
	} else {
		u.pickerStatus.SetText("Models with unknown capability are unverified; model lists do not promise text-chat access. Refresh checks live credentials.")
	}
	for _, f := range u.discovery.Failures {
		message := f.Message
		if message == "" {
			message = failureText(f.Outcome)
		}
		u.pickerStatus.SetText(u.pickerStatus.GetText(true) + fmt.Sprintf("\n%s (%s): %s", f.Label, f.Provider, message))
	}
}

func (u *terminal) activate(e entry) {
	switch e.action {
	case "add":
		u.addProvider()
	case "refresh":
		u.refreshModels()
	case "manage":
		u.manageProviders()
	case "select":
		if e.model != nil {
			u.selectModel(*e.model)
		}
	}
}
func (u *terminal) refreshModels() {
	u.pickerStatus.SetText("Refreshing model catalogs…")
	u.launch(func() {
		d, err := u.client.Discover(u.ctx)
		u.queue(func() {
			if err != nil {
				u.pickerStatus.SetText("Model discovery failed: " + err.Error())
				return
			}
			u.discovery = d
			u.rebuildPicker()
			u.updateBanner()
			u.updateUsage()
			if len(d.Models) > 0 && len(d.Failures) == 0 {
				u.pickerStatus.SetText("Live discovery completed. Unknown capability means text-chat support is unverified.")
			}
		})
	})
}

func (u *terminal) addProvider() {
	providerID := "openai"
	form := tv.NewForm()
	form.SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true))
	form.SetBorder(true).SetTitle(" Add New Provider ")
	form.AddDropDown("Provider", []string{"openai", "anthropic"}, 0, func(value string, _ int) { providerID = value })
	form.AddButton("Continue", func() {
		methods := u.client.AuthMethods(providerID)
		if len(methods) > 1 {
			options := make([]string, 0, len(methods))
			for _, method := range methods {
				if method == "oauth" {
					options = append(options, "Continue with ChatGPT")
				} else if method == "api_key" {
					options = append(options, "API key")
				}
			}
			modal := tv.NewModal().SetText("Choose an authentication method for " + providerID + ".").AddButtons(options).SetDoneFunc(func(_ int, label string) {
				if label == "Continue with ChatGPT" {
					u.startOpenAILogin("")
					return
				}
				u.addAPIKeyProvider(providerID)
			})
			u.app.SetRoot(modal, true)
			return
		}
		u.addAPIKeyProvider(providerID)
	})
	form.AddButton("Cancel", func() { u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput) })
	u.app.SetRoot(form, true).SetFocus(form.GetFormItem(0))
}

func (u *terminal) addAPIKeyProvider(providerID string) {
	label := tv.NewInputField().SetLabel("Profile label: ").SetFieldWidth(32)
	key := tv.NewInputField().SetLabel("API key: ").SetFieldWidth(40).SetMaskCharacter('•')
	org := tv.NewInputField().SetLabel("OpenAI organization (optional): ").SetFieldWidth(36)
	project := tv.NewInputField().SetLabel("OpenAI project (optional): ").SetFieldWidth(36)
	form := tv.NewForm()
	form.SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true))
	form.SetBorder(true)
	form.SetTitle(" Add provider · API key is stored locally as plaintext ")
	form.AddFormItem(label)
	form.AddFormItem(key)
	form.AddFormItem(org)
	form.AddFormItem(project)
	form.AddButton("Validate and save", func() {
		op := u.beginCredentialOperation()
		candidate := key.GetText()
		key.SetText("")
		name := strings.TrimSpace(label.GetText())
		route := map[string]string{}
		if providerID == "openai" {
			if v := strings.TrimSpace(org.GetText()); v != "" {
				route["organization"] = v
			}
			if v := strings.TrimSpace(project.GetText()); v != "" {
				route["project"] = v
			}
		}
		u.launch(func() {
			profiles, err := u.client.Profiles(op.ctx)
			id := profileID(providerID, name, profiles)
			if err == nil && op.ctx.Err() == nil {
				err = u.client.AddProfile(op.ctx, id, providerID, name, candidate, route)
			} else if err == nil {
				err = op.ctx.Err()
			}
			candidate = ""
			u.queue(func() {
				if u.credentialOp != op {
					return
				}
				u.cancelCredentialOperation(op)
				if err != nil {
					u.showMessage("Provider was not saved", err.Error(), func() { u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput) })
					return
				}
				u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
				u.writePickerNotice("Saved profile " + id + ". Choose a discovered model explicitly.")
				u.refreshModels()
			})
		})
	})
	form.AddButton("Cancel", func() {
		u.cancelCredentialOperation(u.credentialOp)
		key.SetText("")
		u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
	})
	u.app.SetRoot(form, true).SetFocus(label)
}

func (u *terminal) startOpenAILogin(profileID string) {
	op := u.beginCredentialOperation()
	attempt, err := u.client.StartLogin(op.ctx, "openai", "oauth", profileID)
	if err != nil {
		u.cancelCredentialOperation(op)
		u.showMessage("Could not start ChatGPT sign-in", err.Error(), func() { u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput) })
		return
	}
	op.loginID = attempt.ID
	baseText := "OpenAI will ask you to authorize eligible inference to use your ChatGPT plan. Sign-in alone does not guarantee eligibility. KompoSyn will return here after sign-in; no chat prompt is sent during setup."
	modal := tv.NewModal().SetText(baseText).AddButtons([]string{"Cancel sign-in"}).SetDoneFunc(func(_ int, _ string) {
		u.cancelCredentialOperation(op)
		u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
		u.writePickerNotice("ChatGPT sign-in canceled.")
	})
	u.app.SetRoot(modal, true)
	u.launch(func() {
		for event := range attempt.Events {
			message := event.Message
			u.queue(func() {
				if u.credentialOp == op && message != "" {
					modal.SetText(baseText + "\n" + message)
				}
			})
		}
	})
	u.launch(func() {
		result := <-attempt.Done
		u.queue(func() {
			if u.credentialOp != op {
				return
			}
			u.cancelCredentialOperation(op)
			if result.Err != nil {
				u.showMessage("ChatGPT sign-in did not complete", result.Err.Error(), func() { u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput) })
				return
			}
			u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
			u.writePickerNotice("Sign-in completed for " + result.Account + ". Review the profile's connection and permission status, then choose a model explicitly.")
			u.refreshModels()
		})
	})
}

func (u *terminal) manageProviders() {
	u.launch(func() {
		profiles, err := u.client.Profiles(u.ctx)
		u.queue(func() {
			if err != nil {
				u.writePickerNotice("Could not read provider profiles: " + err.Error())
				return
			}
			if len(profiles) == 0 {
				u.showMessage("No providers configured", "Use Add New Provider to connect an account.", func() { u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput) })
				return
			}
			list := tv.NewList().ShowSecondaryText(true)
			list.SetBorder(true).SetTitle(" Manage Providers · no credentials are shown ")
			for _, p := range profiles {
				p := p
				secondary := p.Provider + " · " + p.ID + " · " + p.AuthType
				if p.AuthType == "oauth" {
					secondary = p.Account + " · " + p.Status
				}
				list.AddItem(p.Label, secondary, 0, func() { u.manageProfile(p) })
			}
			list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
				if event.Key() == tcell.KeyEscape {
					u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
					return nil
				}
				return event
			})
			u.app.SetRoot(list, true)
		})
	})
}
func (u *terminal) manageProfile(p ui.Profile) {
	if p.AuthType == "oauth" {
		u.manageOAuthProfile(p)
		return
	}
	modal := tv.NewModal().SetText(p.Label + "\n" + p.Provider + " · " + p.ID + "\nRemoving this local profile does not revoke the provider key.").AddButtons([]string{"Replace API key", "Remove profile", "Back"}).SetDoneFunc(func(_ int, label string) {
		switch label {
		case "Replace API key":
			u.replaceProfile(p)
		case "Remove profile":
			u.confirmRemove(p)
		default:
			u.manageProviders()
		}
	})
	u.app.SetRoot(modal, true)
}

func (u *terminal) manageOAuthProfile(p ui.Profile) {
	status := "Connection: " + p.Status
	for _, permission := range p.Permissions {
		status += "\n" + permission.Name + ": " + permission.State
	}
	modal := tv.NewModal().SetText(p.Account + "\n" + status + "\nEligible requests use your ChatGPT plan. Manage usage in ChatGPT Settings.").AddButtons([]string{"Reconnect / enable plan usage", "Manage usage", "Disconnect and keep registration", "Remove registration", "Back"}).SetDoneFunc(func(_ int, label string) {
		switch label {
		case "Reconnect / enable plan usage":
			u.startOpenAILogin(p.ID)
		case "Manage usage":
			u.launch(func() {
				err := u.client.OpenUsage(u.ctx)
				if err != nil {
					u.queue(func() { u.showMessage("Could not open usage settings", err.Error(), func() { u.manageProviders() }) })
				}
			})
		case "Disconnect and keep registration":
			u.disconnectOAuth(p, false)
		case "Remove registration":
			u.disconnectOAuth(p, true)
		default:
			u.manageProviders()
		}
	})
	u.app.SetRoot(modal, true)
}

func (u *terminal) disconnectOAuth(p ui.Profile, remove bool) {
	op := u.beginCredentialOperation()
	u.launch(func() {
		result, err := u.client.Disconnect(op.ctx, p.Provider, p.AuthType, p.ID, remove, false)
		u.queue(func() {
			if u.credentialOp != op {
				return
			}
			if errors.Is(err, ui.ErrConversationReset) {
				question := "Disconnecting this selected account clears the current temporary chat. Continue?"
				if remove {
					question = "Removing this selected account clears the current temporary chat. Continue?"
				}
				modal := tv.NewModal().SetText(question).AddButtons([]string{"Continue", "Cancel"}).SetDoneFunc(func(index int, _ string) {
					if index != 0 {
						u.cancelCredentialOperation(op)
						u.manageProviders()
						return
					}
					u.launch(func() {
						confirmedResult, confirmedErr := u.client.Disconnect(op.ctx, p.Provider, p.AuthType, p.ID, remove, true)
						u.queue(func() { u.finishOAuthDisconnect(op, p, confirmedResult, confirmedErr, remove) })
					})
				})
				u.app.SetRoot(modal, true)
				return
			}
			u.finishOAuthDisconnect(op, p, result, err, remove)
		})
	})
}

func (u *terminal) finishOAuthDisconnect(op *credentialOperation, p ui.Profile, result ui.DisconnectResult, err error, remove bool) {
	if u.credentialOp != op {
		return
	}
	u.cancelCredentialOperation(op)
	if result.LocalTokensRemoved {
		u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
		message := "ChatGPT tokens were removed locally."
		if remove {
			message = "The local ChatGPT registration and tokens were removed."
		}
		if !result.RemoteRevocationConfirmed {
			message += " Remote revocation was not confirmed; disconnect the app in ChatGPT Settings if needed."
		}
		u.writePickerNotice(message)
		u.refreshModels()
		return
	}
	if err != nil {
		u.showMessage("ChatGPT profile was not disconnected", err.Error(), func() { u.manageProfile(p) })
		return
	}
	u.manageProviders()
}
func (u *terminal) replaceProfile(p ui.Profile) {
	label := tv.NewInputField().SetLabel("Profile label: ").SetText(p.Label)
	key := tv.NewInputField().SetLabel("New API key: ").SetMaskCharacter('•')
	form := tv.NewForm()
	form.SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true))
	form.SetBorder(true)
	form.SetTitle(" Replace API key ")
	form.AddFormItem(label)
	form.AddFormItem(key)
	form.AddButton("Validate and replace", func() {
		op := u.beginCredentialOperation()
		value := key.GetText()
		labelValue := label.GetText()
		key.SetText("")
		u.launch(func() {
			err := u.client.ReplaceProfile(op.ctx, p.ID, labelValue, value, nil, false)
			u.queue(func() {
				if u.credentialOp != op {
					return
				}
				if errors.Is(err, ui.ErrConversationReset) {
					u.confirmReplace(p, labelValue, value, op)
					return
				}
				value = ""
				u.cancelCredentialOperation(op)
				if err != nil {
					u.showMessage("Credential was not replaced", err.Error(), func() { u.manageProviders() })
					return
				}
				u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
				u.writePickerNotice("Credential replaced. Select a model again.")
				u.refreshModels()
			})
		})
	})
	form.AddButton("Cancel", func() {
		u.cancelCredentialOperation(u.credentialOp)
		key.SetText("")
		u.manageProviders()
	})
	u.app.SetRoot(form, true)
}
func (u *terminal) confirmReplace(p ui.Profile, label, key string, op *credentialOperation) {
	modal := tv.NewModal().SetText("This profile is used by the current conversation. Replace it and clear chat history?").AddButtons([]string{"Replace and start fresh", "Cancel"}).SetDoneFunc(func(i int, _ string) {
		if i != 0 {
			key = ""
			u.cancelCredentialOperation(op)
			u.manageProviders()
			return
		}
		u.launch(func() {
			err := u.client.ReplaceProfile(op.ctx, p.ID, label, key, nil, true)
			key = ""
			u.queue(func() {
				if u.credentialOp != op {
					return
				}
				u.cancelCredentialOperation(op)
				if err != nil {
					u.showMessage("Credential was not replaced", err.Error(), func() { u.manageProviders() })
					return
				}
				u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
				u.writePickerNotice("Credential replaced. Choose a model for the new chat.")
				u.refreshModels()
			})
		})
	})
	u.app.SetRoot(modal, true)
}
func (u *terminal) confirmRemove(p ui.Profile) {
	modal := tv.NewModal().SetText("Remove local profile " + p.Label + "? The upstream API key remains active.").AddButtons([]string{"Remove", "Cancel"}).SetDoneFunc(func(i int, _ string) {
		if i != 0 {
			u.manageProviders()
			return
		}
		u.launch(func() {
			err := u.client.RemoveProfile(p.ID, false)
			u.queue(func() {
				if errors.Is(err, ui.ErrConversationReset) {
					u.confirmRemoveAndReset(p)
					return
				}
				if err != nil {
					u.showMessage("Profile was not removed", err.Error(), func() { u.manageProviders() })
					return
				}
				u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
				u.writePickerNotice("Profile removed locally.")
				u.refreshModels()
			})
		})
	})
	u.app.SetRoot(modal, true)
}
func (u *terminal) confirmRemoveAndReset(p ui.Profile) {
	modal := tv.NewModal().SetText("Removing this selected profile will also clear the current chat. Continue?").AddButtons([]string{"Remove and start fresh", "Cancel"}).SetDoneFunc(func(i int, _ string) {
		if i != 0 {
			u.manageProviders()
			return
		}
		u.launch(func() {
			err := u.client.RemoveProfile(p.ID, true)
			u.queue(func() {
				if err != nil {
					u.showMessage("Profile was not removed", err.Error(), func() { u.manageProviders() })
					return
				}
				u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
				u.writePickerNotice("Profile removed. Choose a model for a new chat.")
				u.refreshModels()
			})
		})
	})
	u.app.SetRoot(modal, true)
}

func (u *terminal) selectModel(m ui.Model) {
	u.launch(func() {
		err := u.client.Select(u.ctx, m.Key, false)
		if errors.Is(err, ui.ErrConversationReset) {
			u.queue(func() { u.confirmSelection(m) })
			return
		}
		u.queue(func() {
			if err != nil {
				u.writePickerNotice("Model selection failed: " + err.Error())
				return
			}
			u.returnToChat()
			u.updateBanner()
			u.updateUsage()
			u.write(fmt.Sprintf("Selected %s · %s · %s (%s capability).\n", m.Provider, m.ProfileLabel, m.ID, m.Compatibility))
		})
	})
}
func (u *terminal) confirmSelection(m ui.Model) {
	modal := tv.NewModal().SetText("Changing provider/model/profile starts a fresh in-memory conversation. Continue?").AddButtons([]string{"Start fresh", "Cancel"}).SetDoneFunc(func(i int, _ string) {
		if i != 0 {
			u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
			return
		}
		u.launch(func() {
			err := u.client.Select(u.ctx, m.Key, true)
			u.queue(func() {
				if err != nil {
					u.writePickerNotice("Model selection failed: " + err.Error())
					return
				}
				u.returnToChat()
				u.updateBanner()
				u.updateUsage()
				u.write("Previous chat cleared. Selected " + m.Provider + " · " + m.ProfileLabel + " · " + m.ID + ".\n")
			})
		})
	})
	u.app.SetRoot(modal, true)
}

func (u *terminal) returnToChat() { u.app.SetRoot(u.layout, true).SetFocus(u.input) }
func (u *terminal) writePickerNotice(s string) {
	if u.pickerStatus != nil {
		u.pickerStatus.SetText(s)
	}
}
func (u *terminal) showMessage(title, message string, done func()) {
	modal := tv.NewModal().SetText(title + "\n" + message).AddButtons([]string{"OK"}).SetDoneFunc(func(int, string) { done() })
	u.app.SetRoot(modal, true)
}
func profileID(providerID, label string, profiles []ui.Profile) string {
	slug := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(label), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "profile"
	}
	base := providerID + "-" + slug
	maxSlug := 64 - len(providerID) - 1
	if len(slug) > maxSlug {
		slug = strings.Trim(slug[:maxSlug], "-")
	}
	base = providerID + "-" + slug
	used := map[string]bool{}
	for _, p := range profiles {
		used[p.ID] = true
	}
	id := base
	for i := 2; used[id]; i++ {
		suffix := fmt.Sprintf("-%d", i)
		prefix := base
		if len(prefix)+len(suffix) > 64 {
			prefix = prefix[:64-len(suffix)]
		}
		id = prefix + suffix
	}
	return id
}

func outcomeStatus(turn ui.Turn, err error) string {
	diagnostic := ""
	if turn.Outcome == modelprovider.OutcomeUnsupported && modelprovider.SafeDiagnosticCode(turn.DiagnosticCode) {
		diagnostic = " (diagnostic: " + string(turn.DiagnosticCode) + ")"
	}
	if turn.Message != "" {
		return fmt.Sprintf("Request %s%s: %s. This response was not added to follow-up context.\n", turn.Outcome, diagnostic, turn.Message)
	}
	if err != nil {
		message := turn.Message
		if message == "" {
			message = "request failed"
		}
		return fmt.Sprintf("Request %s: %s. This response was not added to follow-up context.\n", turn.Outcome, message)
	}
	switch turn.Outcome {
	case modelprovider.OutcomeCompleted:
		return "Response completed and added to follow-up context.\n"
	case modelprovider.OutcomeRefused:
		return "Provider refusal; response added to follow-up context.\n"
	case modelprovider.OutcomeTruncated:
		return "Response truncated; it was not added to follow-up context.\n"
	case modelprovider.OutcomeInterrupted:
		return "Response interrupted; it was not added to follow-up context.\n"
	case modelprovider.OutcomeUnsupported:
		return "Provider response unsupported" + diagnostic + "; it was not added to follow-up context.\n"
	case modelprovider.OutcomeAuthentication:
		return "Provider rejected the credential; response was not added to follow-up context.\n"
	case modelprovider.OutcomePermission:
		return "Provider denied permission; response was not added to follow-up context.\n"
	case modelprovider.OutcomeRateLimited:
		return "Provider rate limited the request; response was not added to follow-up context.\n"
	case modelprovider.OutcomeNetwork:
		return "Provider network request failed; response was not added to follow-up context.\n"
	case modelprovider.OutcomeService:
		return "Provider service failed; response was not added to follow-up context.\n"
	default:
		return "Provider returned no recognized outcome; response was not added to follow-up context.\n"
	}
}

func failureText(out modelprovider.Outcome) string {
	switch out {
	case modelprovider.OutcomeAuthentication:
		return "credential rejected"
	case modelprovider.OutcomePermission:
		return "insufficient permission"
	case modelprovider.OutcomeRateLimited:
		return "rate limited"
	case modelprovider.OutcomeNetwork:
		return "network failure"
	default:
		return "discovery failed"
	}
}

var _ ui.Plugin = Plugin{}
