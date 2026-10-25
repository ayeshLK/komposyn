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
type terminal struct {
	app          *tv.Application
	done         chan struct{}
	client       ui.Client
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	closing      atomic.Bool
	output       *tv.TextView
	input        *tv.InputField
	layout       *tv.Flex
	pickerQuery  string
	picker       *tv.List
	pickerInput  *tv.InputField
	pickerStatus *tv.TextView
	pickerLayout *tv.Flex
	entries      []entry
	discovery    ui.Discovery
}

func (p Plugin) Run(parent context.Context, client ui.Client) error {
	ctx, cancel := context.WithCancel(parent)
	u := &terminal{app: tv.NewApplication(), client: client, ctx: ctx, cancel: cancel}
	u.output = tv.NewTextView().SetDynamicColors(true).SetScrollable(true)
	u.output.SetBorder(true).SetTitle(" KompoSyn · Temporary Chat ")
	u.input = tv.NewInputField().SetLabel("> ").SetFieldWidth(0)
	u.layout = tv.NewFlex().SetDirection(tv.FlexRow).AddItem(u.output, 0, 1, false).AddItem(u.input, 1, 0, true)
	u.input.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}
		u.submit()
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
		return event
	})
	u.write("KompoSyn is ready. Chat state is temporary and will be cleared on exit. Run /model to connect a provider.\n")
	u.done = make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			u.app.Stop()
		case <-u.done:
		}
	}()
	if p.screen != nil {
		u.app.SetScreen(p.screen)
	}
	err := u.app.SetRoot(u.layout, true).EnableMouse(true).SetFocus(u.input).Run()
	close(u.done)
	u.closing.Store(true)
	client.Cancel()
	cancel()
	u.wg.Wait()
	if err != nil {
		return fmt.Errorf("run terminal UI: %w", err)
	}
	return nil
}

func (u *terminal) write(s string) { fmt.Fprint(u.output, s); u.output.ScrollToEnd() }
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
	u.queue(func() { fmt.Fprint(u.output, s); u.output.ScrollToEnd() })
}
func (u *terminal) launch(work func()) { u.wg.Add(1); go func() { defer u.wg.Done(); work() }() }
func (u *terminal) stop()              { u.client.Cancel(); u.app.Stop() }

func (u *terminal) submit() {
	text := u.input.GetText()
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if strings.HasPrefix(trimmed, "/") {
		if strings.HasPrefix(trimmed, "/model ") {
			u.input.SetText("")
			u.write("Use /model to open the provider and model picker. No command arguments were used.\n")
			return
		}
		switch strings.Fields(trimmed)[0] {
		case "/model":
			u.openPicker()
		case "/help":
			u.write("Commands: /model opens provider and model setup; /cancel stops a reply; /exit closes KompoSyn.\n")
		case "/cancel":
			u.client.Cancel()
			u.write("Cancellation requested.\n")
		case "/exit":
			u.stop()
		default:
			u.write("Unknown command. Use /help.\n")
		}
		u.input.SetText("")
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
	u.write("\nYou: " + trimmed + "\nAssistant: ")
	u.launch(func() {
		turn, err := u.client.Send(u.ctx, trimmed, func(delta string) { u.writeAsync(delta) })
		u.writeAsync("\n")
		if err != nil {
			u.writeAsync("Request " + string(turn.Outcome) + ": " + turn.Message + ". This response was not added to follow-up context.\n")
		} else if turn.Outcome == modelprovider.OutcomeRefused {
			if turn.Refusal != "" {
				u.writeAsync("Provider refusal: " + turn.Refusal + "\n")
			} else {
				u.writeAsync("The provider refused this request.\n")
			}
		}
		if turn.Usage.Known {
			u.writeAsync(fmt.Sprintf("Request usage: input=%d output=%d\n", turn.Usage.InputTokens, turn.Usage.OutputTokens))
		} else {
			u.writeAsync("Request usage: unknown\n")
		}
		u.writeAsync("\n")
	})
}

func (u *terminal) openPicker() {
	u.pickerInput = tv.NewInputField().SetLabel("Filter models and actions: ").SetFieldWidth(0).SetText(u.pickerQuery).SetChangedFunc(func(v string) { u.pickerQuery = v; u.rebuildPicker() })
	u.picker = tv.NewList().ShowSecondaryText(true)
	u.pickerStatus = tv.NewTextView().SetDynamicColors(true)
	u.pickerLayout = tv.NewFlex().SetDirection(tv.FlexRow).AddItem(u.pickerInput, 1, 0, true).AddItem(u.picker, 0, 1, false).AddItem(u.pickerStatus, 2, 0, false)
	u.picker.SetBorder(true).SetTitle(" /model · Select a model or manage providers ")
	u.pickerInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			u.app.SetFocus(u.picker)
		}
	})
	u.picker.SetSelectedFunc(func(index int, main, secondary string, shortcut rune) {
		if index < 0 || index >= len(u.entries) {
			return
		}
		u.activate(u.entries[index])
	})
	u.picker.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			u.returnToChat()
			return nil
		}
		if event.Key() == tcell.KeyTab {
			u.app.SetFocus(u.pickerInput)
			return nil
		}
		return event
	})
	u.rebuildPicker()
	u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput)
	u.refreshModels()
}

func (u *terminal) rebuildPicker() {
	if u.picker == nil {
		return
	}
	selected := u.client.Snapshot().Selected
	query := strings.ToLower(strings.TrimSpace(u.pickerQuery))
	u.entries = nil
	u.picker.Clear()
	add := func(e entry, secondary string, shortcut rune) {
		u.entries = append(u.entries, e)
		u.picker.AddItem(e.label, secondary, shortcut, func() { u.activate(e) })
	}
	add(entry{label: "＋ Add New Provider", action: "add"}, "Choose OpenAI or Anthropic; enter a masked API key", 'a')
	add(entry{label: "↻ Refresh Models", action: "refresh"}, "Fetch the current provider catalogs", 'r')
	add(entry{label: "⚙ Manage Providers", action: "manage"}, "Review profile metadata, replace keys, or remove profiles", 'p')
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
			add(e, secondary, 0)
		}
	}
	if query != "" { // Keep all actions visible while the model list is filtered.
		u.picker.Clear()
		old := u.entries
		u.entries = nil
		for _, e := range old[:min(3, len(old))] {
			secondary := map[string]string{"add": "Add or configure an API-key profile", "refresh": "Refresh model catalogs", "manage": "Inspect or edit provider profiles"}[e.action]
			add(e, secondary, 0)
		}
		for _, m := range u.discovery.Models {
			mark := ""
			if selected != nil && selected.Key == m.Key {
				mark = " ✓ current"
			}
			label := fmt.Sprintf("%s · %s · %s%s", m.Provider, m.ProfileLabel, m.DisplayName, mark)
			secondary := fmt.Sprintf("%s · %s · %s", m.ProfileID, m.ID, m.Compatibility)
			if strings.Contains(strings.ToLower(label+" "+secondary), query) {
				copy := m
				add(entry{label: label, action: "select", model: &copy}, secondary, 0)
			}
		}
	}
	if len(u.discovery.Models) == 0 {
		u.pickerStatus.SetText("No models discovered yet. Add a provider or refresh after configuring one.")
	} else {
		u.pickerStatus.SetText("Models with unknown capability are unverified; model lists do not promise text-chat access. Refresh checks live credentials.")
	}
	for _, f := range u.discovery.Failures {
		u.pickerStatus.SetText(u.pickerStatus.GetText(true) + fmt.Sprintf("\n%s (%s): %s", f.Label, f.Provider, failureText(f.Outcome)))
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
			if len(d.Models) > 0 && len(d.Failures) == 0 {
				u.pickerStatus.SetText("Live discovery completed. Unknown capability means text-chat support is unverified.")
			}
		})
	})
}

func (u *terminal) addProvider() {
	providerID := "openai"
	label := tv.NewInputField().SetLabel("Profile label: ").SetFieldWidth(32)
	key := tv.NewInputField().SetLabel("API key: ").SetFieldWidth(40).SetMaskCharacter('•')
	org := tv.NewInputField().SetLabel("OpenAI organization (optional): ").SetFieldWidth(36)
	project := tv.NewInputField().SetLabel("OpenAI project (optional): ").SetFieldWidth(36)
	form := tv.NewForm()
	form.SetBorder(true)
	form.SetTitle(" Add provider · API key is stored locally as plaintext ")
	form.AddDropDown("Provider", []string{"openai", "anthropic"}, 0, func(value string, _ int) { providerID = value })
	form.AddFormItem(label)
	form.AddFormItem(key)
	form.AddFormItem(org)
	form.AddFormItem(project)
	form.AddButton("Validate and save", func() {
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
			profiles, err := u.client.Profiles(u.ctx)
			id := profileID(providerID, name, profiles)
			if err == nil {
				err = u.client.AddProfile(u.ctx, id, providerID, name, candidate, route)
			}
			candidate = ""
			u.queue(func() {
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
	form.AddButton("Cancel", func() { key.SetText(""); u.app.SetRoot(u.pickerLayout, true).SetFocus(u.pickerInput) })
	u.app.SetRoot(form, true).SetFocus(label)
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
			list.SetBorder(true).SetTitle(" Manage Providers · no keys are shown ")
			for _, p := range profiles {
				p := p
				list.AddItem(p.Label, p.Provider+" · "+p.ID+" · "+p.AuthType, 0, func() { u.manageProfile(p) })
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
func (u *terminal) replaceProfile(p ui.Profile) {
	label := tv.NewInputField().SetLabel("Profile label: ").SetText(p.Label)
	key := tv.NewInputField().SetLabel("New API key: ").SetMaskCharacter('•')
	form := tv.NewForm()
	form.SetBorder(true)
	form.SetTitle(" Replace API key ")
	form.AddFormItem(label)
	form.AddFormItem(key)
	form.AddButton("Validate and replace", func() {
		value := key.GetText()
		labelValue := label.GetText()
		key.SetText("")
		u.launch(func() {
			err := u.client.ReplaceProfile(u.ctx, p.ID, labelValue, value, nil, false)
			u.queue(func() {
				if errors.Is(err, ui.ErrConversationReset) {
					u.confirmReplace(p, labelValue, value)
					return
				}
				value = ""
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
	form.AddButton("Cancel", func() { key.SetText(""); u.manageProviders() })
	u.app.SetRoot(form, true)
}
func (u *terminal) confirmReplace(p ui.Profile, label, key string) {
	modal := tv.NewModal().SetText("This profile is used by the current conversation. Replace it and clear chat history?").AddButtons([]string{"Replace and start fresh", "Cancel"}).SetDoneFunc(func(i int, _ string) {
		if i != 0 {
			u.manageProviders()
			return
		}
		u.launch(func() {
			err := u.client.ReplaceProfile(u.ctx, p.ID, label, key, nil, true)
			u.queue(func() {
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
