// Package observe adapts provider token counts to Token Logger Core. It never
// accepts conversation content, tool arguments, instructions, or credentials.
package observe

import (
	"errors"
	"strings"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/model"
	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

type UsageSink interface {
	AppendWithReferences([]tokenlogger.UsageEvent, []tokenlogger.Project, []tokenlogger.Session) (tokenlogger.Receipt, error)
}
type TokenLogger struct{ Store UsageSink }

func (o TokenLogger) Record(eventID, projectID, sessionID string, config model.ModelConfig, response model.Response) error {
	if o.Store == nil {
		return errors.New("token logger store is not configured")
	}
	if strings.TrimSpace(eventID) == "" {
		return errors.New("stable usage event identity is required")
	}
	if !response.Usage.Known {
		return nil
	}
	started, completed := response.StartedAt.UTC(), response.CompletedAt.UTC()
	occurred := completed
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	if started.IsZero() {
		started = occurred
	}
	event := tokenlogger.UsageEvent{EventID: eventID, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurred, StartedAt: &started, CompletedAt: &completed, ProjectID: projectID, SessionID: sessionID, RequestID: response.RequestID, Operation: "model.request", Client: tokenlogger.Client{Name: "komposyn-harness", Version: "0.1.0"}, Provider: string(config.Provider), RequestedModel: config.ModelID, ResolvedModel: response.ResolvedModel, Usage: tokenlogger.TokenUsage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, CacheReadTokens: response.Usage.CacheReadTokens, CacheWriteTokens: response.Usage.CacheWriteTokens, CacheWrite5mTokens: response.Usage.CacheWrite5mTokens, CacheWrite1hTokens: response.Usage.CacheWrite1hTokens, ReasoningTokens: response.Usage.ReasoningTokens}, Cost: tokenlogger.Cost{Status: "unknown"}, Provenance: tokenlogger.Provenance{Adapter: "komposyn-harness", SourceID: "harness", SourceRecord: response.RequestID}, IngestionKey: eventID}
	projects := []tokenlogger.Project{}
	if projectID != "" {
		projects = append(projects, tokenlogger.Project{ID: projectID, DisplayName: projectID})
	}
	sessions := []tokenlogger.Session{}
	if sessionID != "" {
		sessions = append(sessions, tokenlogger.Session{ID: sessionID, ProjectID: projectID, Client: tokenlogger.Client{Name: "komposyn-harness", Version: "0.1.0"}})
	}
	_, err := o.Store.AppendWithReferences([]tokenlogger.UsageEvent{event}, projects, sessions)
	return err
}
