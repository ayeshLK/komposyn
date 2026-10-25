// Package tviewui provides the terminal client for the runtime APIs.
package tviewui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/model"
	rt "github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/gdamore/tcell/v2"
	tv "github.com/rivo/tview"
)

type UI struct {
	app        *tv.Application
	ctx        context.Context
	cancel     context.CancelFunc
	runtime    *rt.Runtime
	root       string
	output     *tv.TextView
	input      *tv.InputField
	layout     *tv.Flex
	session    *rt.Session
	activeTask string
}

func Run(runtime *rt.Runtime, root string) error {
	ctx, cancel := context.WithCancel(context.Background())
	u := &UI{app: tv.NewApplication(), runtime: runtime, root: root, ctx: ctx, cancel: cancel}
	u.output = tv.NewTextView().SetDynamicColors(true).SetScrollable(true)
	u.output.SetBorder(true).SetTitle(" KompoSyn Harness ")
	u.input = tv.NewInputField().SetLabel("> ").SetFieldWidth(0)
	u.layout = tv.NewFlex().SetDirection(tv.FlexRow).AddItem(u.output, 0, 1, false).AddItem(u.input, 1, 0, true)
	u.input.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}
		text := strings.TrimSpace(u.input.GetText())
		u.input.SetText("")
		if text != "" {
			u.handle(text)
		}
	})
	u.write("Harness ready. Use /help for commands. No model is selected until you add or select one.\n")
	e := u.app.SetRoot(u.layout, true).EnableMouse(true).Run()
	cancel()
	if e != nil {
		return fmt.Errorf("run terminal UI: %w", e)
	}
	return nil
}
func (u *UI) write(s string) { fmt.Fprint(u.output, s); u.output.ScrollToEnd() }
func (u *UI) writeAsync(s string) {
	if u.ctx.Err() != nil {
		return
	}
	u.app.QueueUpdateDraw(func() { fmt.Fprint(u.output, s); u.output.ScrollToEnd() })
}
func (u *UI) handle(line string) {
	if strings.HasPrefix(line, "/") {
		parts := strings.Fields(line)
		switch parts[0] {
		case "/help":
			u.write("Commands: /help /status /usage /project /sessions /task NAME /resume ID /complete /model [add|list|select ID] /instructions /permissions /exit\n")
		case "/status":
			m, ok := u.runtime.SelectedModel()
			if ok {
				u.write(fmt.Sprintf("Selected model: %s (%s / %s)\n", m.Alias, m.Provider, m.ModelID))
			} else {
				u.write("No model selected. Run /model add.\n")
			}
			if u.session != nil {
				u.write(fmt.Sprintf("Session %s · task %q · complete=%v\n", u.session.ID, u.activeTask, u.session.TaskComplete))
			}
		case "/usage":
			if u.session == nil {
				u.write("No active session.\n")
			} else {
				var input, output, read, write int64
				var count int
				for _, m := range u.session.Messages {
					if m.Usage != nil && m.Usage.Known {
						count++
						input += m.Usage.InputTokens
						output += m.Usage.OutputTokens
						read += m.Usage.CacheReadTokens
						write += m.Usage.CacheWriteTokens
					}
				}
				u.write(fmt.Sprintf("Session usage: requests=%d input=%d output=%d cache_read=%d cache_write=%d\n", count, input, output, read, write))
			}
		case "/project":
			u.write("Workspace: " + u.root + " (read-only MCP tools)\n")
		case "/resume":
			if len(parts) < 2 {
				u.write("Usage: /resume SESSION_ID\n")
			} else if s, ok := u.runtime.Session(parts[1]); !ok {
				u.write("Session not found.\n")
			} else if s.Root != u.root {
				u.write("Session belongs to a different workspace.\n")
			} else {
				u.session = &s
				u.activeTask = s.Task
				if s.NeedsReconciliation {
					u.write("Session loaded with interrupted work requiring reconciliation. Inspect history/operations before proceeding; turns will not be replayed.\n")
				} else {
					u.write(fmt.Sprintf("Resumed session %s for task %q.\n", s.ID, s.Task))
				}
			}
		case "/sessions":
			for _, s := range u.runtime.Sessions() {
				u.write(fmt.Sprintf("%s · project %s · model %s · task %q · complete=%v\n", s.ID, s.ProjectID, s.ModelID, s.Task, s.TaskComplete))
			}
		case "/instructions":
			if u.session == nil {
				u.write("Start a task to load workspace instructions.\n")
			} else if len(u.session.InstructionSources) == 0 {
				u.write("No AGENTS.md files found at workspace root.\n")
			} else {
				for _, s := range u.session.InstructionSources {
					u.write(fmt.Sprintf("%s scope=%s sha256=%s bytes=%d modified=%s\n", s.Path, s.Scope, s.SHA256, s.Bytes, s.Modified.Format(time.RFC3339)))
				}
			}
		case "/permissions":
			u.write("Workspace tools use Ask for every exact read operation. No write or shell tools are registered.\n")
		case "/model":
			if len(parts) == 1 {
				u.showModels()
				return
			}
			switch parts[1] {
			case "add":
				u.addModel()
			case "list":
				u.showModels()
			case "select":
				if len(parts) < 3 {
					u.write("Usage: /model select ID\n")
				} else if e := u.runtime.SelectModel(parts[2]); e != nil {
					u.write("Model error: " + e.Error() + "\n")
				} else {
					u.write("Selected model " + parts[2] + ".\n")
				}
			default:
				u.write("Usage: /model [add|list|select ID]\n")
			}
		case "/complete":
			if u.session == nil {
				u.write("No active task.\n")
			} else if e := u.runtime.CompleteTask(u.session.ID); e != nil {
				u.write("Task error: " + e.Error() + "\n")
			} else {
				u.session.TaskComplete = true
				u.write("Task marked complete.\n")
			}
		case "/task":
			if len(parts) < 2 {
				u.write("Usage: /task NAME\n")
				return
			}
			u.startTask(strings.Join(parts[1:], " "))
		case "/exit":
			u.cancel()
			u.app.Stop()
		default:
			u.write("Unknown command. Use /help.\n")
		}
		return
	}
	if u.session == nil {
		u.write("Start a task with /task NAME before sending a prompt.\n")
		return
	}
	u.runTurn(line)
}
func (u *UI) showModels() {
	models := u.runtime.Models()
	if len(models) == 0 {
		u.write("No configured models. Use /model add.\n")
		return
	}
	selected, _ := u.runtime.SelectedModel()
	for _, m := range models {
		mark := " "
		if m.ID == selected.ID {
			mark = "*"
		}
		u.write(fmt.Sprintf("%s %s · %s · %s · id=%s\n", mark, m.Alias, m.Provider, m.ModelID, m.ID))
	}
}
func (u *UI) addModel() {
	provider := "openai"
	modelID, alias, credential := tv.NewInputField(), tv.NewInputField(), tv.NewInputField()
	form := tv.NewForm().SetFieldBackgroundColor(tv.Styles.PrimitiveBackgroundColor)
	form.SetBorder(true).SetTitle(" Add model ").SetTitleAlign(tv.AlignLeft)
	form.AddDropDown("Provider", []string{"openai", "anthropic"}, 0, func(option string, _ int) { provider = option })
	modelID.SetLabel("Model ID").SetFieldWidth(36)
	alias.SetLabel("Local alias").SetFieldWidth(36)
	credential.SetLabel("Credential ref (env:NAME)").SetFieldWidth(42)
	form.AddFormItem(modelID)
	form.AddFormItem(alias)
	form.AddFormItem(credential)
	form.AddButton("Save and select", func() {
		id := fmt.Sprintf("model-%d", time.Now().UTC().UnixNano())
		cfg := model.ModelConfig{ID: id, Provider: model.Provider(provider), ModelID: strings.TrimSpace(modelID.GetText()), Alias: strings.TrimSpace(alias.GetText()), CredentialRef: strings.TrimSpace(credential.GetText())}
		if e := u.runtime.AddModel(cfg); e != nil {
			u.write("Model error: " + e.Error() + "\n")
			return
		}
		if e := u.runtime.SelectModel(id); e != nil {
			u.write("Model error: " + e.Error() + "\n")
			return
		}
		u.app.SetRoot(u.layout, true).SetFocus(u.input)
		u.write(fmt.Sprintf("Added and selected %s (%s). No provider request was sent.\n", cfg.Alias, cfg.Provider))
	})
	form.AddButton("Cancel", func() { u.app.SetRoot(u.layout, true).SetFocus(u.input) })
	u.app.SetRoot(form, true).SetFocus(modelID)
}
func (u *UI) startTask(name string) {
	if u.session != nil && !u.session.TaskComplete {
		u.write("Complete the current task with /complete before starting another.\n")
		return
	}
	sum := sha256.Sum256([]byte(u.root))
	project := "project-" + hex.EncodeToString(sum[:])
	s, e := u.runtime.NewSession(u.root, project)
	if e != nil {
		u.write("Session error: " + e.Error() + "\n")
		return
	}
	u.session = &s
	u.activeTask = name
	u.write(fmt.Sprintf("Started task %q in session %s. Enter a prompt.\n", name, s.ID))
}
func (u *UI) runTurn(prompt string) {
	s := u.session
	task := u.activeTask
	u.write("\nYou: " + prompt + "\nAssistant: ")
	go func() {
		e := u.runtime.Run(u.ctx, s.ID, task, prompt, u.approve, func(ev rt.Event) {
			switch ev.Kind {
			case "text":
				u.writeAsync(ev.Text)
			case "tool_result":
				u.writeAsync("\nTool result: " + ev.Text + "\nAssistant: ")
			case "response":
				u.writeAsync("\n")
			}
		})
		if e != nil {
			u.writeAsync("\nTurn ended: " + e.Error() + "\n")
		}
		if updated, ok := u.runtime.Session(s.ID); ok {
			if u.ctx.Err() == nil {
				u.app.QueueUpdateDraw(func() { u.session = &updated })
			}
		}
	}()
}
func (u *UI) approve(ctx context.Context, a rt.Approval) (bool, error) {
	answer := make(chan bool, 1)
	modal := tv.NewModal().SetText(fmt.Sprintf("Allow read operation?\n\nTool: %s\nPath: %s", a.Tool, a.Path)).AddButtons([]string{"Approve", "Deny"}).SetDoneFunc(func(_ int, label string) {
		answer <- label == "Approve"
		u.app.SetRoot(u.layout, true).SetFocus(u.input)
	})
	u.app.QueueUpdateDraw(func() { u.app.SetRoot(modal, false) })
	select {
	case allowed := <-answer:
		return allowed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
