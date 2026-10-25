package observe

import (
	"github.com/ayeshLK/komposyn/internal/harness/model"
	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
	"testing"
	"time"
)

type capture struct {
	events   []tokenlogger.UsageEvent
	projects []tokenlogger.Project
	sessions []tokenlogger.Session
}

func (c *capture) AppendWithReferences(e []tokenlogger.UsageEvent, p []tokenlogger.Project, s []tokenlogger.Session) (tokenlogger.Receipt, error) {
	c.events = e
	c.projects = p
	c.sessions = s
	return tokenlogger.Receipt{}, nil
}
func TestUsageOnlyEvent(t *testing.T) {
	c := &capture{}
	now := time.Now().UTC()
	resp := model.Response{RequestID: "req-1", Text: "private response", StartedAt: now, CompletedAt: now, Usage: model.Usage{Known: true, InputTokens: 5, OutputTokens: 2, CacheReadTokens: 1}}
	e := (TokenLogger{Store: c}).Record("event-1", "project-1", "session-1", model.ModelConfig{Provider: model.OpenAI, ModelID: "model-x"}, resp)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.events) != 1 || c.events[0].Usage.InputTokens != 5 || c.events[0].Cost.Status != "unknown" || len(c.projects) != 1 || len(c.sessions) != 1 {
		t.Fatalf("capture=%+v", c)
	}
	if c.events[0].Attributes != nil {
		t.Fatal("content attributes unexpectedly captured")
	}
}

func TestRecordAgainstReleasedCoreStore(t *testing.T) {
	path := t.TempDir() + "/usage.db"
	db, e := tokenlogger.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now().UTC()
	resp := model.Response{RequestID: "provider-request-1", StartedAt: now, CompletedAt: now, ResolvedModel: "resolved-x", Usage: model.Usage{Known: true, InputTokens: 4, OutputTokens: 2}}
	if e = (TokenLogger{Store: db}).Record("openai-event-1", "project-abc", "session-abc", model.ModelConfig{Provider: model.OpenAI, ModelID: "requested-x"}, resp); e != nil {
		t.Fatal(e)
	}
}
