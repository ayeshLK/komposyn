package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/mcpworkspace"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/store"
)

type sequenceProvider struct{ n int }

func (p *sequenceProvider) Provider() model.Provider { return model.OpenAI }
func (p *sequenceProvider) Stream(_ context.Context, req model.Request, _ func(model.Delta)) (model.Response, error) {
	p.n++
	if p.n == 1 {
		return model.Response{RequestID: "request-1", Outcome: model.OutcomeToolCalls, Native: []json.RawMessage{json.RawMessage(`{"type":"message","role":"assistant","content":[]}`)}, ToolCalls: []model.ToolCall{{ProviderCallID: "call-1", ProviderName: "workspace_read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`)}}, Usage: model.Usage{Known: true}, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}, nil
	}
	if len(req.History) < 3 || len(req.History[2].Results) != 1 {
		return model.Response{}, errBadHistory
	}
	return model.Response{RequestID: "request-2", Outcome: model.OutcomeCompleted, Text: "done", Usage: model.Usage{Known: true}, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}, nil
}

var errBadHistory = os.ErrInvalid

func TestRunApprovedToolRoundAndPersistence(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	ws, e := mcpworkspace.New(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	mc, e := mcpworkspace.Connect(context.Background(), ws)
	if e != nil {
		t.Fatal(e)
	}
	defer mc.Close()
	dbDir := t.TempDir()
	if e := os.Chmod(dbDir, 0700); e != nil {
		t.Fatal(e)
	}
	dbFile := filepath.Join(dbDir, "harness.db")
	db, e := store.Open(dbFile)
	if e != nil {
		t.Fatal(e)
	}
	plugin := &sequenceProvider{}
	r, e := Open(db, mc, nil, plugin)
	if e != nil {
		t.Fatal(e)
	}
	cfg := model.ModelConfig{ID: "m1", Provider: model.OpenAI, ModelID: "test-model", Alias: "test", CredentialRef: "env:OPENAI_TEST"}
	if e = r.AddModel(cfg); e != nil {
		t.Fatal(e)
	}
	if e = r.SelectModel(cfg.ID); e != nil {
		t.Fatal(e)
	}
	session, e := r.NewSession(dir, "project-1")
	if e != nil {
		t.Fatal(e)
	}
	approved := false
	var kinds []string
	e = r.Run(context.Background(), session.ID, "inspect note", "Read note.txt", func(_ context.Context, a Approval) (bool, error) {
		approved = true
		if a.Tool != "read_file" || a.Path != "note.txt" {
			t.Fatalf("approval = %+v", a)
		}
		return true, nil
	}, func(ev Event) { kinds = append(kinds, ev.Kind) })
	if e != nil {
		t.Fatal(e)
	}
	if !approved || plugin.n != 2 {
		t.Fatalf("approved=%v calls=%d", approved, plugin.n)
	}
	if len(kinds) < 4 {
		t.Fatalf("events = %v", kinds)
	}
	if e = r.CompleteTask(session.ID); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = store.Open(dbFile)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	restored, e := Open(db, nil, nil, &sequenceProvider{})
	if e != nil {
		t.Fatal(e)
	}
	if len(restored.Models()) != 1 || len(restored.Sessions()) != 1 || !restored.Sessions()[0].TaskComplete {
		t.Fatalf("state not restored: models=%d sessions=%+v", len(restored.Models()), restored.Sessions())
	}
}

func TestInterruptedApprovalIsNotReplayed(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	ws, e := mcpworkspace.New(root)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	mc, e := mcpworkspace.Connect(context.Background(), ws)
	if e != nil {
		t.Fatal(e)
	}
	defer mc.Close()
	dbDir := t.TempDir()
	_ = os.Chmod(dbDir, 0700)
	dbFile := filepath.Join(dbDir, "harness.db")
	db, e := store.Open(dbFile)
	if e != nil {
		t.Fatal(e)
	}
	plugin := &sequenceProvider{}
	r, e := Open(db, mc, nil, plugin)
	if e != nil {
		t.Fatal(e)
	}
	cfg := model.ModelConfig{ID: "m1", Provider: model.OpenAI, ModelID: "test-model", Alias: "test", CredentialRef: "env:OPENAI_TEST"}
	if e = r.AddModel(cfg); e != nil {
		t.Fatal(e)
	}
	if e = r.SelectModel(cfg.ID); e != nil {
		t.Fatal(e)
	}
	session, e := r.NewSession(root, "project-1")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx, session.ID, "inspect", "read", func(ctx context.Context, _ Approval) (bool, error) {
			close(entered)
			<-ctx.Done()
			return false, ctx.Err()
		}, nil)
	}()
	<-entered
	cancel()
	if e = <-done; e == nil {
		t.Fatal("canceled run returned success")
	}
	saved, ok := r.Session(session.ID)
	if !ok || !saved.NeedsReconciliation || saved.PendingOperation == nil {
		t.Fatalf("interrupted operation not persisted: %+v", saved)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = store.Open(dbFile)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	restored, e := Open(db, mc, nil, &sequenceProvider{})
	if e != nil {
		t.Fatal(e)
	}
	recovered, _ := restored.Session(session.ID)
	if !recovered.NeedsReconciliation || recovered.PendingOperation == nil {
		t.Fatalf("recovery state=%+v", recovered)
	}
	if e = restored.Run(context.Background(), session.ID, "retry", "retry", nil, nil); e == nil {
		t.Fatal("interrupted session was replayed")
	}
}
