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
	"fmt"
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

func TestPersistenceRejectsSyntheticNavigationIDs(t *testing.T) {
	store := openTestStore(t)
	for _, id := range []string{UnattributedProjectID, UnattributedSessionID} {
		if err := store.UpsertProject(Project{ID: id, DisplayName: "Collision"}); err == nil {
			t.Fatalf("reserved project ID %q was accepted", id)
		}
		if err := store.UpsertSession(Session{ID: id, Client: Client{Name: "client"}}); err == nil {
			t.Fatalf("reserved session ID %q was accepted", id)
		}
	}
	for index, id := range []string{UnattributedProjectID, UnattributedSessionID} {
		event := testEvent(fmt.Sprintf("reserved-project-%d", index))
		event.EventID, event.ProjectID = fmt.Sprintf("reserved-project-event-%d", index), id
		if _, err := store.Append([]UsageEvent{event}); err == nil {
			t.Fatalf("event with reserved project ID %q was accepted", id)
		}
		event = testEvent(fmt.Sprintf("reserved-session-%d", index))
		event.EventID, event.SessionID = fmt.Sprintf("reserved-session-event-%d", index), id
		if _, err := store.Append([]UsageEvent{event}); err == nil {
			t.Fatalf("event with reserved session ID %q was accepted", id)
		}
	}
	imported := testEvent("reserved-provider-alias")
	imported.EventID, imported.Provenance.SourceRecord = "provider-event", "provider-record"
	imported.ProjectID = UnattributedProjectID
	project := Project{ID: UnattributedProjectID, DisplayName: "Provider alias", SourceAdapter: "codex", ProviderAlias: UnattributedProjectID}
	if _, err := store.AppendWithReferences([]UsageEvent{imported}, []Project{project}, nil); err != nil {
		t.Fatalf("provider alias equal to reserved local ID was not reconciled: %v", err)
	}
	var persistedProjectID string
	if err := store.db.QueryRow("SELECT project_id FROM usage_events WHERE event_id = ?", imported.EventID).Scan(&persistedProjectID); err != nil {
		t.Fatal(err)
	}
	if persistedProjectID == UnattributedProjectID || persistedProjectID == "" {
		t.Fatalf("provider alias was persisted without local reconciliation: %q", persistedProjectID)
	}
	crossImported := testEvent("reserved-cross-provider-alias")
	crossImported.EventID, crossImported.Provenance.SourceRecord = "cross-provider-event", "cross-provider-record"
	crossImported.ProjectID, crossImported.SessionID = UnattributedSessionID, UnattributedProjectID
	crossProject := Project{ID: UnattributedSessionID, DisplayName: "Cross provider alias", SourceAdapter: "codex", ProviderAlias: UnattributedSessionID}
	crossSession := Session{ID: UnattributedProjectID, ProjectID: UnattributedSessionID, Client: crossImported.Client, SourceAdapter: "codex", ProviderAlias: UnattributedProjectID}
	if _, err := store.AppendWithReferences([]UsageEvent{crossImported}, []Project{crossProject}, []Session{crossSession}); err != nil {
		t.Fatalf("cross-namespace provider aliases were not reconciled: %v", err)
	}
	var crossProjectID, crossSessionID string
	if err := store.db.QueryRow("SELECT project_id, session_id FROM usage_events WHERE event_id = ?", crossImported.EventID).Scan(&crossProjectID, &crossSessionID); err != nil {
		t.Fatal(err)
	}
	if isReservedProjectID(crossProjectID) || isReservedSessionID(crossSessionID) || crossProjectID == "" || crossSessionID == "" {
		t.Fatalf("cross-namespace provider aliases persisted reserved IDs: project=%q session=%q", crossProjectID, crossSessionID)
	}
}

func TestMigrationEightRemapsCrossNamespaceSyntheticIDCollisions(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent("cross-collision")
	event.EventID, event.ProjectID, event.SessionID = "cross-collision-event", "legacy-project", "legacy-session"
	if _, err := store.AppendWithReferences([]UsageEvent{event},
		[]Project{{ID: event.ProjectID, DisplayName: "Cross collision"}},
		[]Session{{ID: event.SessionID, ProjectID: event.ProjectID, Client: event.Client}}); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO projects(id, root, display_name, created_at) SELECT '` + UnattributedSessionID + `', root, display_name, created_at FROM projects WHERE id = 'legacy-project'`,
		`UPDATE sessions SET project_id = '` + UnattributedSessionID + `' WHERE project_id = 'legacy-project'`,
		`UPDATE usage_events SET project_id = '` + UnattributedSessionID + `' WHERE project_id = 'legacy-project'`,
		`DELETE FROM projects WHERE id = 'legacy-project'`,
		`INSERT INTO sessions(id, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata)
		 SELECT '` + UnattributedProjectID + `', project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata
		 FROM sessions WHERE id = 'legacy-session'`,
		`UPDATE usage_events SET session_id = '` + UnattributedProjectID + `' WHERE session_id = 'legacy-session'`,
		`DELETE FROM sessions WHERE id = 'legacy-session'`,
		`INSERT INTO project_aliases(scope, alias, project_id, first_seen_at, last_seen_at)
		 VALUES ('legacy', 'cross-project', '` + UnattributedSessionID + `', '2026-01-01', '2026-01-02')`,
		`INSERT INTO session_aliases(scope, alias, session_id, project_id, first_seen_at, last_seen_at)
		 VALUES ('legacy', 'cross-session', '` + UnattributedProjectID + `', '` + UnattributedSessionID + `', '2026-01-01', '2026-01-02')`,
		`INSERT INTO reconciliation_warnings(kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at, occurrence_count)
		 VALUES ('cross-collision', 'legacy', 'digest', '` + UnattributedSessionID + `', '` + UnattributedProjectID + `', 'collision', '2026-01-01', '2026-01-02', 2)`,
		`ALTER TABLE source_cursors DROP COLUMN snapshot_reusable`,
		`ALTER TABLE source_cursors DROP COLUMN pending_tail`,
		`DROP TABLE collector_source_identities`,
		`DROP TABLE collector_runs`,
		`DROP TABLE collector_runtime`,
		`DROP TABLE collector_sources`,
		`DELETE FROM schema_migrations WHERE version IN (8, 9, 10, 11)`,
	}
	for _, statement := range statements {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var projectID, sessionID string
	if err := store.db.QueryRow("SELECT project_id, session_id FROM usage_events WHERE event_id = ?", event.EventID).Scan(&projectID, &sessionID); err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{projectID, sessionID} {
		if isReservedProjectID(got) || isReservedSessionID(got) || got == "" {
			t.Fatalf("migration left cross-namespace collision: project=%q session=%q", projectID, sessionID)
		}
	}
	checks := []struct {
		query string
		args  []any
	}{
		{"SELECT COUNT(*) FROM project_aliases WHERE project_id = ?", []any{projectID}},
		{"SELECT COUNT(*) FROM session_aliases WHERE project_id = ? AND session_id = ?", []any{projectID, sessionID}},
		{"SELECT COUNT(*) FROM reconciliation_warnings WHERE project_id = ? AND session_id = ? AND occurrence_count = 2", []any{projectID, sessionID}},
	}
	for _, check := range checks {
		var count int
		if err := store.db.QueryRow(check.query, check.args...).Scan(&count); err != nil || count != 1 {
			t.Fatalf("cross-collision reference count=%d err=%v query=%q", count, err, check.query)
		}
	}
	filter := ResourceFilter{From: event.OccurredAt.Add(-time.Hour), To: event.OccurredAt.Add(time.Hour)}
	project, err := store.ProjectResource(projectID, filter)
	if err != nil || project.Summary.RequestCount != 1 {
		t.Fatalf("migrated cross-collision project=%+v err=%v", project, err)
	}
	session, err := store.SessionResource(sessionID, projectID, filter)
	if err != nil || session.Summary.RequestCount != 1 {
		t.Fatalf("migrated cross-collision session=%+v err=%v", session, err)
	}
}

func TestMigrationEightRemapsSyntheticIDCollisions(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	attributed := testEvent("collision-attributed")
	attributed.ProjectID, attributed.SessionID = "legacy-project", "legacy-session"
	if _, err := store.AppendWithReferences([]UsageEvent{attributed},
		[]Project{{ID: attributed.ProjectID, DisplayName: "Persisted collision"}},
		[]Session{{ID: attributed.SessionID, ProjectID: attributed.ProjectID, Client: attributed.Client}}); err != nil {
		t.Fatal(err)
	}
	unattributed := testEvent("collision-unattributed")
	unattributed.EventID, unattributed.Provenance.SourceRecord = "event-2", "record-2"
	if _, err := store.Append([]UsageEvent{unattributed}); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO projects(id, root, display_name, created_at) SELECT '` + UnattributedProjectID + `', root, display_name, created_at FROM projects WHERE id = 'legacy-project'`,
		`UPDATE sessions SET project_id = '` + UnattributedProjectID + `' WHERE project_id = 'legacy-project'`,
		`UPDATE usage_events SET project_id = '` + UnattributedProjectID + `' WHERE project_id = 'legacy-project'`,
		`DELETE FROM projects WHERE id = 'legacy-project'`,
		`INSERT INTO sessions(id, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata)
		 SELECT '` + UnattributedSessionID + `', project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata
		 FROM sessions WHERE id = 'legacy-session'`,
		`UPDATE usage_events SET session_id = '` + UnattributedSessionID + `' WHERE session_id = 'legacy-session'`,
		`DELETE FROM sessions WHERE id = 'legacy-session'`,
		`INSERT INTO project_locations(project_id, canonical_path, display_path, first_seen_at, last_seen_at)
		 VALUES ('` + UnattributedProjectID + `', '/collision', '/collision', '2026-01-01', '2026-01-02')`,
		`INSERT INTO project_aliases(scope, alias, project_id, first_seen_at, last_seen_at)
		 VALUES ('legacy', 'project-alias', '` + UnattributedProjectID + `', '2026-01-01', '2026-01-02')`,
		`INSERT INTO session_aliases(scope, alias, session_id, project_id, first_seen_at, last_seen_at)
		 VALUES ('legacy', 'session-alias', '` + UnattributedSessionID + `', '` + UnattributedProjectID + `', '2026-01-01', '2026-01-02')`,
		`INSERT INTO reconciliation_warnings(kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at, occurrence_count)
		 VALUES ('collision', 'legacy', 'digest', '` + UnattributedProjectID + `', '` + UnattributedSessionID + `', 'collision', '2026-01-01', '2026-01-02', 3)`,
		`ALTER TABLE source_cursors DROP COLUMN snapshot_reusable`,
		`ALTER TABLE source_cursors DROP COLUMN pending_tail`,
		`DROP TABLE collector_source_identities`,
		`DROP TABLE collector_runs`,
		`DROP TABLE collector_runtime`,
		`DROP TABLE collector_sources`,
		`DELETE FROM schema_migrations WHERE version IN (8, 9, 10, 11)`,
	}
	for _, statement := range statements {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var projectID, sessionID string
	if err := store.db.QueryRow("SELECT project_id, session_id FROM usage_events WHERE event_id = ?", attributed.EventID).Scan(&projectID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if projectID == UnattributedProjectID || sessionID == UnattributedSessionID || projectID == "" || sessionID == "" {
		t.Fatalf("migration left colliding references: project=%q session=%q", projectID, sessionID)
	}
	checks := []struct {
		query string
		args  []any
	}{
		{"SELECT COUNT(*) FROM project_locations WHERE project_id = ?", []any{projectID}},
		{"SELECT COUNT(*) FROM project_aliases WHERE project_id = ?", []any{projectID}},
		{"SELECT COUNT(*) FROM session_aliases WHERE project_id = ? AND session_id = ?", []any{projectID, sessionID}},
		{"SELECT COUNT(*) FROM reconciliation_warnings WHERE project_id = ? AND session_id = ? AND occurrence_count = 3", []any{projectID, sessionID}},
	}
	for _, check := range checks {
		var count int
		if err := store.db.QueryRow(check.query, check.args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("migration did not preserve dependent reference for %q", check.query)
		}
	}
	filter := ResourceFilter{From: attributed.OccurredAt.Add(-time.Hour), To: attributed.OccurredAt.Add(time.Hour)}
	projects, err := store.ListProjects(filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects) != 2 {
		t.Fatalf("projects after collision migration = %+v, want persisted and synthetic resources", projects.Projects)
	}
	var persisted, synthetic int
	for _, project := range projects.Projects {
		if project.ID == projectID && project.Summary.RequestCount == 1 {
			persisted++
		}
		if project.ID == UnattributedProjectID && project.Summary.RequestCount == 1 {
			synthetic++
		}
	}
	if persisted != 1 || synthetic != 1 {
		t.Fatalf("collision migration navigation persisted=%d synthetic=%d projects=%+v", persisted, synthetic, projects.Projects)
	}
	sessions, err := store.ListSessions(projectID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != sessionID || sessions.Sessions[0].Summary.RequestCount != 1 {
		t.Fatalf("migrated session navigation = %+v", sessions.Sessions)
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

func TestMultipleStoresWaitBoundedlyAndPreserveExactlyOnceAccounting(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	lock, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec("INSERT INTO projects(id, display_name, created_at) VALUES ('lock-holder', 'Lock holder', ?)", formatStoredTime(time.Now())); err != nil {
		lock.Rollback()
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		event := testEvent("concurrent-writer")
		event.EventID, event.Provenance.SourceRecord = "concurrent-event", "concurrent-record"
		_, appendErr := second.Append([]UsageEvent{event})
		done <- appendErr
	}()
	<-started
	if err := lock.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("bounded concurrent append failed: %v", err)
	}
	if count, err := first.EventCount(); err != nil || count != 1 {
		t.Fatalf("shared-store event count=%d err=%v", count, err)
	}
	if _, err := first.Append([]UsageEvent{testEvent("concurrent-writer")}); err != nil {
		t.Fatal(err)
	}
	if count, err := second.EventCount(); err != nil || count != 1 {
		t.Fatalf("idempotent shared-store event count=%d err=%v", count, err)
	}
}

func TestSimultaneousOpenCoordinatesConfigurationAndMigrations(t *testing.T) {
	for attempt := 0; attempt < 10; attempt++ {
		databasePath := filepath.Join(t.TempDir(), "shared.db")
		start := make(chan struct{})
		type openResult struct {
			store *Store
			err   error
		}
		results := make(chan openResult, 2)
		for range 2 {
			go func() {
				<-start
				store, err := Open(databasePath)
				results <- openResult{store: store, err: err}
			}()
		}
		close(start)
		opened := make([]*Store, 0, 2)
		for range 2 {
			result := <-results
			if result.err != nil {
				for _, store := range opened {
					_ = store.Close()
				}
				t.Fatalf("simultaneous open attempt %d failed: %v", attempt+1, result.err)
			}
			opened = append(opened, result.store)
		}
		var version int
		if err := opened[0].db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil || version != currentSchemaVersion {
			t.Fatalf("simultaneous open schema version=%d err=%v", version, err)
		}
		for _, store := range opened {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMigrationTenAddsCollectorIdentityMapWithoutChangingCursors(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	cursor := Cursor{SourceID: "claude-file-established", FileFingerprint: "fingerprint", ByteOffset: 42, SnapshotReusable: true}
	if _, err := store.AppendWithCursor(nil, cursor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DROP TABLE collector_source_identities"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version = 10"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version = 11"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"collector_runs", "collector_runtime", "collector_sources"} {
		if _, err := store.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got, err := store.Cursor(cursor.SourceID); err != nil || got != cursor {
		t.Fatalf("migration changed cursor: got=%+v err=%v", got, err)
	}
	if sourceID, claimed, err := store.rememberCollectorSourceIdentity("canonical-digest", "claude", cursor.SourceID); err != nil || !claimed || sourceID != cursor.SourceID {
		t.Fatalf("migrated identity map source=%q claimed=%t err=%v", sourceID, claimed, err)
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
