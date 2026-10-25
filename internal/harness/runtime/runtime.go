// Package runtime coordinates provider-neutral multi-round model/tool turns.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/instructions"
	"github.com/ayeshLK/komposyn/internal/harness/mcpworkspace"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/store"
)

const maxRounds = 8

type Approval struct{ ID, Tool, Path string }
type Event struct {
	Kind, ID, Text string
	Response       *model.Response
	Call           *model.ToolCall
}
type Approver func(context.Context, Approval) (bool, error)
type Sink interface {
	Record(eventID, projectID, sessionID string, config model.ModelConfig, response model.Response) error
}
type diskState struct {
	Models   []model.ModelConfig `json:"models"`
	Selected string              `json:"selected"`
	Sessions []Session           `json:"sessions"`
}
type Operation struct {
	ID             string          `json:"id"`
	ProviderCallID string          `json:"providerCallId"`
	ToolName       string          `json:"toolName"`
	Arguments      json.RawMessage `json:"arguments"`
	Status         string          `json:"status"`
}
type Session struct {
	ID                  string                `json:"id"`
	ProjectID           string                `json:"projectId"`
	Root                string                `json:"root"`
	ModelID             string                `json:"modelId"`
	Messages            []model.Message       `json:"messages"`
	Task                string                `json:"task"`
	TaskComplete        bool                  `json:"taskComplete"`
	InProgress          bool                  `json:"inProgress,omitempty"`
	NeedsReconciliation bool                  `json:"needsReconciliation,omitempty"`
	PendingOperation    *Operation            `json:"pendingOperation,omitempty"`
	PendingResults      []model.ToolResult    `json:"pendingResults,omitempty"`
	Instructions        string                `json:"instructions,omitempty"`
	InstructionSources  []instructions.Source `json:"instructionSources,omitempty"`
	UpdatedAt           time.Time             `json:"updatedAt"`
}
type Runtime struct {
	mu        sync.Mutex
	db        *store.Store
	state     diskState
	providers map[model.Provider]model.Plugin
	workspace *mcpworkspace.Client
	usage     Sink
	running   map[string]bool
}

func Open(db *store.Store, workspace *mcpworkspace.Client, usage Sink, plugins ...model.Plugin) (*Runtime, error) {
	if db == nil {
		return nil, errors.New("runtime store is required")
	}
	r := &Runtime{db: db, workspace: workspace, usage: usage, providers: map[model.Provider]model.Plugin{}, running: map[string]bool{}}
	for _, p := range plugins {
		if p == nil || !p.Provider().Valid() {
			return nil, errors.New("invalid model provider plugin")
		}
		if _, ok := r.providers[p.Provider()]; ok {
			return nil, errors.New("duplicate model provider plugin")
		}
		r.providers[p.Provider()] = p
	}
	data, e := db.Load()
	if e != nil {
		return nil, e
	}
	if len(data) > 0 {
		if e = json.Unmarshal(data, &r.state); e != nil {
			return nil, fmt.Errorf("decode runtime state: %w", e)
		}
	}
	recovered := false
	for i := range r.state.Sessions {
		if r.state.Sessions[i].InProgress || r.state.Sessions[i].PendingOperation != nil {
			r.state.Sessions[i].InProgress = false
			r.state.Sessions[i].NeedsReconciliation = true
			recovered = true
		}
	}
	if recovered {
		if e = r.persistLocked(); e != nil {
			return nil, e
		}
	}
	return r, nil
}
func (r *Runtime) persistLocked() error {
	b, e := json.Marshal(r.state)
	if e != nil {
		return e
	}
	return r.db.Save(b)
}
func (r *Runtime) AddModel(c model.ModelConfig) error {
	if e := c.Validate(); e != nil {
		return e
	}
	if e := validateCredential(c.CredentialRef); e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busyLocked() {
		return errors.New("model configuration is unavailable while a turn or approval is active")
	}
	for _, old := range r.state.Models {
		if old.ID == c.ID {
			return errors.New("configured model ID already exists")
		}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	c.UpdatedAt = time.Now().UTC()
	if c.ConfigVersion == 0 {
		c.ConfigVersion = 1
	}
	r.state.Models = append(r.state.Models, c)
	return r.persistLocked()
}
func validateCredential(ref string) error { return auth.ValidateReference(ref) }
func (r *Runtime) busyLocked() bool {
	for id := range r.running {
		if r.running[id] {
			return true
		}
	}
	for _, s := range r.state.Sessions {
		if s.InProgress {
			return true
		}
	}
	return false
}

func (r *Runtime) Models() []model.ModelConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.ModelConfig(nil), r.state.Models...)
}
func (r *Runtime) SelectModel(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busyLocked() {
		return errors.New("model selection is unavailable while a turn or approval is active")
	}
	found := false
	for _, m := range r.state.Models {
		if m.ID == id {
			found = true
			break
		}
	}
	if !found {
		return errors.New("configured model not found")
	}
	r.state.Selected = id
	return r.persistLocked()
}
func (r *Runtime) SelectedModel() (model.ModelConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.state.Models {
		if m.ID == r.state.Selected {
			return m, true
		}
	}
	return model.ModelConfig{}, false
}
func (r *Runtime) NewSession(root, projectID string) (Session, error) {
	if r.workspace == nil {
		return Session{}, errors.New("open an MCP workspace before creating a session")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Session{}, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return Session{}, err
	}
	if abs != r.workspace.RootPath() {
		return Session{}, errors.New("session root must match the approved MCP workspace")
	}
	root = abs
	if projectID == "" {
		return Session{}, errors.New("project identity is required")
	}
	if _, ok := r.SelectedModel(); !ok {
		return Session{}, errors.New("select a configured model first")
	}
	loaded, e := instructions.Load(root, root)
	if e != nil {
		return Session{}, e
	}
	id, e := newID()
	if e != nil {
		return Session{}, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Session{ID: id, ProjectID: projectID, Root: root, ModelID: r.state.Selected, Instructions: loaded.Text, InstructionSources: loaded.Sources, UpdatedAt: time.Now().UTC()}
	r.state.Sessions = append(r.state.Sessions, s)
	return s, r.persistLocked()
}
func (r *Runtime) Sessions() []Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Session, len(r.state.Sessions))
	copy(out, r.state.Sessions)
	for i := range out {
		out[i].Messages = append([]model.Message(nil), out[i].Messages...)
	}
	return out
}
func (r *Runtime) Run(ctx context.Context, sessionID, task, prompt string, approve Approver, emit func(Event)) (runErr error) {
	if task == "" || prompt == "" {
		return errors.New("task name and prompt are required")
	}
	r.mu.Lock()
	idx := -1
	for i := range r.state.Sessions {
		if r.state.Sessions[i].ID == sessionID {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return errors.New("session not found")
	}
	s := r.state.Sessions[idx]
	if r.workspace == nil {
		r.mu.Unlock()
		return errors.New("open the session workspace before running a turn")
	}
	if s.Root != r.workspace.RootPath() {
		r.mu.Unlock()
		return errors.New("session workspace differs from the opened MCP workspace")
	}
	if s.NeedsReconciliation {
		r.mu.Unlock()
		return errors.New("session has interrupted work; inspect history and start a new task/session before continuing")
	}
	if r.running[sessionID] {
		r.mu.Unlock()
		return errors.New("session already has a running turn")
	}
	r.running[sessionID] = true
	s.InProgress = true
	var cfg model.ModelConfig
	for _, m := range r.state.Models {
		if m.ID == s.ModelID {
			cfg = m
			break
		}
	}
	plug := r.providers[cfg.Provider]
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running[sessionID] = false
		for i := range r.state.Sessions {
			if r.state.Sessions[i].ID == sessionID {
				r.state.Sessions[i].InProgress = false
				if runErr != nil {
					r.state.Sessions[i].NeedsReconciliation = true
				}
				_ = r.persistLocked()
				break
			}
		}
		r.mu.Unlock()
	}()
	if plug == nil {
		return fmt.Errorf("provider %q is unavailable", cfg.Provider)
	}
	tools, lookup, e := r.toolSet(ctx)
	if e != nil {
		return e
	}
	s.Task = task
	s.TaskComplete = false
	s.Messages = append(s.Messages, model.Message{Role: model.RoleUser, Text: prompt, CreatedAt: time.Now().UTC()})
	if e = r.saveSession(s); e != nil {
		return e
	}
	if e = r.markInProgress(sessionID, true); e != nil {
		return e
	}
	for round := 0; round < maxRounds; round++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		resp, e := plug.Stream(ctx, model.Request{Config: cfg, Instructions: s.Instructions, History: s.Messages, Tools: tools, MaxTokens: 4096}, func(d model.Delta) {
			if emit != nil {
				emit(Event{Kind: "text", Text: d.Text})
			}
		})
		if e != nil {
			return e
		}
		s.Messages = append(s.Messages, model.Message{Role: model.RoleAssistant, Text: resp.Text, Native: resp.Native, Usage: &resp.Usage, CreatedAt: time.Now().UTC()})
		if r.usage != nil {
			eventID := resp.RequestID
			if eventID == "" {
				eventID, _ = newID()
			}
			if e := r.usage.Record("usage-"+eventID, s.ProjectID, s.ID, cfg, resp); e != nil {
				return e
			}
		}
		if e = r.saveSession(s); e != nil {
			return e
		}
		if emit != nil {
			emit(Event{Kind: "response", Response: &resp})
		}
		if resp.Outcome != model.OutcomeToolCalls {
			return outcomeError(resp)
		}
		results := make([]model.ToolResult, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			if e := ctx.Err(); e != nil {
				return e
			}
			tool, ok := lookup[call.ProviderName]
			if !ok {
				return fmt.Errorf("provider requested unsupported tool %q", call.ProviderName)
			}
			args, decodeErr := decodeToolPath(call.Arguments)
			if decodeErr != nil {
				return decodeErr
			}
			op, e := newID()
			if e != nil {
				return e
			}
			s.PendingOperation = &Operation{ID: op, ProviderCallID: call.ProviderCallID, ToolName: tool.Name, Arguments: append(json.RawMessage(nil), call.Arguments...), Status: "awaiting_approval"}
			if e = r.saveSession(s); e != nil {
				return e
			}
			ap := Approval{ID: op, Tool: tool.Name, Path: args.Path}
			if emit != nil {
				emit(Event{Kind: "approval_pending", ID: op, Call: &call})
			}
			allowed := false
			if approve != nil {
				allowed, e = approve(ctx, ap)
				if e != nil {
					return e
				}
			}
			if allowed {
				s.PendingOperation.Status = "approved"
			} else {
				s.PendingOperation.Status = "denied"
			}
			if e = r.saveSession(s); e != nil {
				return e
			}
			if emit != nil {
				emit(Event{Kind: "approval_resolved", ID: op})
			}
			tr := model.ToolResult{ProviderCallID: call.ProviderCallID, OperationID: op, ToolName: tool.Name}
			if !allowed {
				tr.Content = "Operation denied by user."
				tr.IsError = true
			} else {
				tr.Content, e = r.workspace.Call(ctx, tool.Name, args.Path)
				if e != nil {
					tr.Content = e.Error()
					tr.IsError = true
				}
			}
			results = append(results, tr)
			s.PendingResults = append(s.PendingResults, tr)
			s.PendingOperation = nil
			if e = r.saveSession(s); e != nil {
				return e
			}
			if emit != nil {
				emit(Event{Kind: "tool_result", ID: op, Text: tr.Content})
			}
		}
		s.Messages = append(s.Messages, model.Message{Role: model.RoleToolBatch, Results: results, CreatedAt: time.Now().UTC()})
		s.PendingResults = nil
		if e = r.saveSession(s); e != nil {
			return e
		}
	}
	return errors.New("model/tool round limit reached")
}
func (r *Runtime) Session(sessionID string) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.state.Sessions {
		if s.ID == sessionID {
			s.Messages = append([]model.Message(nil), s.Messages...)
			return s, true
		}
	}
	return Session{}, false
}
func (r *Runtime) markInProgress(id string, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.state.Sessions {
		if r.state.Sessions[i].ID == id {
			r.state.Sessions[i].InProgress = active
			return r.persistLocked()
		}
	}
	return errors.New("session not found")
}

func (r *Runtime) CompleteTask(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[sessionID] {
		return errors.New("cannot complete a task while its turn is active")
	}
	for i := range r.state.Sessions {
		if r.state.Sessions[i].ID == sessionID {
			r.state.Sessions[i].TaskComplete = true
			r.state.Sessions[i].UpdatedAt = time.Now().UTC()
			return r.persistLocked()
		}
	}
	return errors.New("session not found")
}
func (r *Runtime) saveSession(s Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.state.Sessions {
		if r.state.Sessions[i].ID == s.ID {
			r.state.Sessions[i] = s
			r.state.Sessions[i].UpdatedAt = time.Now().UTC()
			return r.persistLocked()
		}
	}
	return errors.New("session disappeared")
}
func (r *Runtime) toolSet(ctx context.Context) ([]model.Tool, map[string]model.Tool, error) {
	if r.workspace == nil {
		return nil, nil, nil
	}
	ts, e := r.workspace.ListTools(ctx)
	if e != nil {
		return nil, nil, e
	}
	out := make([]model.Tool, 0, len(ts))
	lookup := map[string]model.Tool{}
	for _, t := range ts {
		schema, e := json.Marshal(t.InputSchema)
		if e != nil {
			return nil, nil, e
		}
		pt := model.Tool{ProviderName: "workspace_" + t.Name, ServerName: "workspace", Name: t.Name, Description: t.Description, InputSchema: schema}
		out = append(out, pt)
		lookup[pt.ProviderName] = pt
	}
	return out, lookup, nil
}
func decodeToolPath(raw json.RawMessage) (struct {
	Path string `json:"path"`
}, error) {
	var args struct {
		Path string `json:"path"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&args); e != nil || args.Path == "" {
		return args, errors.New("tool call requires a valid path and no unsupported arguments")
	}
	if e := dec.Decode(&struct{}{}); e != io.EOF {
		return args, errors.New("tool call contains trailing or malformed arguments")
	}
	return args, nil
}

func outcomeError(resp model.Response) error {
	switch resp.Outcome {
	case model.OutcomeCompleted, model.OutcomeRefusal:
		return nil
	case model.OutcomeTruncated:
		return errors.New("model response reached its output limit")
	case model.OutcomeContextLimit:
		return errors.New("model context limit reached")
	case model.OutcomeUnsupported:
		return fmt.Errorf("unsupported model response stop: %s", resp.StopReason)
	default:
		return fmt.Errorf("model request ended with outcome %q", resp.Outcome)
	}
}
func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
