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

package tokenlogger

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageEventValidation(t *testing.T) {
	event := testEvent("ingestion-1")
	if err := event.Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	event.Usage.CacheReadTokens = event.Usage.InputTokens + 1
	if err := event.Validate(); err == nil {
		t.Fatal("invalid cache subset accepted")
	}
}

func TestAppendIsIdempotentAndCursorIsCommitted(t *testing.T) {
	store := openTestStore(t)
	event := testEvent("ingestion-1")
	cursor := Cursor{SourceID: "codex-source", FileFingerprint: "fingerprint", ByteOffset: 42, LastProviderRecordID: "record-1"}

	first, err := store.AppendWithCursor([]UsageEvent{event}, cursor)
	if err != nil {
		t.Fatalf("first append failed: %v", err)
	}
	if first.Imported != 1 || first.Duplicates != 0 {
		t.Fatalf("unexpected first receipt: %+v", first)
	}
	second, err := store.AppendWithCursor([]UsageEvent{event}, cursor)
	if err != nil {
		t.Fatalf("second append failed: %v", err)
	}
	if second.Imported != 0 || second.Duplicates != 1 {
		t.Fatalf("unexpected duplicate receipt: %+v", second)
	}
	count, err := store.EventCount()
	if err != nil || count != 1 {
		t.Fatalf("event count = %d, err = %v", count, err)
	}
	gotCursor, err := store.Cursor(cursor.SourceID)
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if gotCursor != cursor {
		t.Fatalf("cursor = %+v, want %+v", gotCursor, cursor)
	}
}

func TestAppendRejectsInvalidEventBeforeTransaction(t *testing.T) {
	store := openTestStore(t)
	event := testEvent("ingestion-1")
	event.EventID = ""
	_, err := store.AppendWithCursor([]UsageEvent{event}, Cursor{SourceID: "source", ByteOffset: 1})
	if err == nil {
		t.Fatal("invalid event was accepted")
	}
	if count, countErr := store.EventCount(); countErr != nil || count != 0 {
		t.Fatalf("invalid append changed event count: %d, err = %v", count, countErr)
	}
	if _, cursorErr := store.Cursor("source"); !errors.Is(cursorErr, sql.ErrNoRows) {
		t.Fatalf("invalid append advanced cursor: %v", cursorErr)
	}
}

func TestAppendRollsBackEventAndCursorOnPersistenceFailure(t *testing.T) {
	store := openTestStore(t)
	event := testEvent("ingestion-1")
	event.ProjectID = "missing-project"
	cursor := Cursor{SourceID: "source", ByteOffset: 1}
	if _, err := store.AppendWithCursor([]UsageEvent{event}, cursor); err == nil {
		t.Fatal("append with missing project was accepted")
	}
	if count, err := store.EventCount(); err != nil || count != 0 {
		t.Fatalf("failed append changed event count: %d, err = %v", count, err)
	}
	if _, err := store.Cursor(cursor.SourceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed append advanced cursor: %v", err)
	}
}

func TestAppendWithReferencesRollsBackReferencesOnEventFailure(t *testing.T) {
	store := openTestStore(t)
	event := testEvent("ingestion-1")
	event.ProjectID = "project-1"
	event.SessionID = "missing-session"
	project := Project{ID: "project-1", Root: "/redacted/project", DisplayName: "Example"}

	if _, err := store.AppendWithReferences([]UsageEvent{event}, []Project{project}, nil); err == nil {
		t.Fatal("append with missing session was accepted")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM projects WHERE id = ?", project.ID).Scan(&count); err != nil {
		t.Fatalf("query rolled-back project: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed append left %d project rows", count)
	}
}

func TestAppendWithReferencesAndCursorRollsBackAllPersistenceOnEventFailure(t *testing.T) {
	store := openTestStore(t)
	conflict := testEvent("existing-ingestion")
	if _, err := store.Append([]UsageEvent{conflict}); err != nil {
		t.Fatalf("seed conflicting event: %v", err)
	}
	event := testEvent("ingestion-1")
	event.ProjectID = "project-1"
	event.EventID = conflict.EventID
	event.SessionID = "session-1"
	project := Project{ID: "project-1", Root: "/redacted/project", DisplayName: "Example", SourceAdapter: "codex", ProviderAlias: "project-1"}
	session := Session{ID: event.SessionID, ProjectID: project.ID, Client: Client{Name: "codex-cli"}, SourceAdapter: "codex", ProviderAlias: event.SessionID}
	cursor := Cursor{SourceID: "source", FileFingerprint: "fingerprint", ByteOffset: 42}

	if _, err := store.AppendWithReferencesAndCursor([]UsageEvent{event}, []Project{project}, []Session{session}, cursor); err == nil {
		t.Fatal("append with conflicting event was accepted")
	}
	for _, check := range []struct {
		name  string
		query string
		args  []any
	}{
		{name: "project", query: "SELECT COUNT(*) FROM projects WHERE id = ?", args: []any{project.ID}},
		{name: "session", query: "SELECT COUNT(*) FROM sessions WHERE id = ?", args: []any{session.ID}},
		{name: "event", query: "SELECT COUNT(*) FROM usage_events WHERE ingestion_key = ?", args: []any{event.IngestionKey}},
		{name: "cursor", query: "SELECT COUNT(*) FROM source_cursors WHERE source_id = ?", args: []any{cursor.SourceID}},
		{name: "project alias", query: "SELECT COUNT(*) FROM project_aliases WHERE scope = 'codex' AND alias = ?", args: []any{project.ProviderAlias}},
		{name: "project location", query: "SELECT COUNT(*) FROM project_locations WHERE project_id LIKE 'project-%'", args: nil},
		{name: "session alias", query: "SELECT COUNT(*) FROM session_aliases WHERE scope = 'codex:codex-cli' AND alias = ?", args: []any{session.ProviderAlias}},
	} {
		var count int
		if err := store.db.QueryRow(check.query, check.args...).Scan(&count); err != nil {
			t.Fatalf("query rolled-back %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("failed append left %d %s rows", count, check.name)
		}
	}
}

func TestProjectsAndSessionsCanBeUpsertedAndReferenced(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertProject(Project{ID: "project-1", Root: "/redacted/project", DisplayName: "Example"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := store.UpsertSession(Session{ID: "session-1", ProjectID: "project-1", Client: Client{Name: "codex-cli"}, SourceMetadata: []byte(`{"fixture":true}`)}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	event := testEvent("ingestion-1")
	event.ProjectID = "project-1"
	event.SessionID = "session-1"
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatalf("append referenced event: %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "token-logger.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testEvent(ingestionKey string) UsageEvent {
	return UsageEvent{
		EventID: "event-1", SchemaVersion: 1, EventType: "model.request",
		OccurredAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		Client:     Client{Name: "codex-cli"}, Provider: "openai", RequestedModel: "gpt-test",
		Usage:        TokenUsage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 25, ReasoningTokens: 5},
		Provenance:   Provenance{Adapter: "codex", SourceID: "source", SourceRecord: "record-1"},
		IngestionKey: ingestionKey,
	}
}
