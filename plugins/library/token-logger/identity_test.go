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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReconciliationWarningMigrationCoalescesNullableKeys(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE reconciliation_warnings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL, scope TEXT NOT NULL DEFAULT '', alias_digest TEXT NOT NULL DEFAULT '',
		project_id TEXT, session_id TEXT, message TEXT NOT NULL,
		first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL,
		occurrence_count INTEGER NOT NULL DEFAULT 1,
		UNIQUE(kind, scope, alias_digest, project_id, session_id)
	)`); err != nil {
		t.Fatal(err)
	}
	for _, occurrences := range []int{2, 3} {
		if _, err := db.Exec(`INSERT INTO reconciliation_warnings
			(kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at, occurrence_count)
			VALUES ('conflict', 'scope', 'digest', NULL, NULL, 'message', '2026-01-01', '2026-01-02', ?)`, occurrences); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(schemaMigrationSeven); err != nil {
		t.Fatal(err)
	}
	var rows, occurrences int
	var projectID, sessionID string
	if err := db.QueryRow(`SELECT COUNT(*), SUM(occurrence_count), project_id, session_id
		FROM reconciliation_warnings`).Scan(&rows, &occurrences, &projectID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || occurrences != 5 || projectID != "" || sessionID != "" {
		t.Fatalf("migrated warnings = rows %d occurrences %d project %q session %q, want 1/5/empty/empty", rows, occurrences, projectID, sessionID)
	}
	if _, err := db.Exec(`INSERT INTO reconciliation_warnings
		(kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at)
		VALUES ('conflict', 'scope', 'other', NULL, '', 'message', '2026-01-01', '2026-01-02')`); err == nil {
		t.Fatal("migrated project_id unexpectedly accepted NULL")
	}
}

func TestCrossClientImportsAtSameLocationShareLocalProject(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	codexProject, codexSession := appendImportedFixture(t, store, "codex", "codex-cli", "codex-project", "shared", root, "one")
	claudeProject, claudeSession := appendImportedFixture(t, store, "claude-code", "claude-code", "claude-project", "shared", root, "two")

	if codexProject != claudeProject {
		t.Fatalf("cross-client projects = %q and %q, want one local identity", codexProject, claudeProject)
	}
	if codexSession == claudeSession {
		t.Fatalf("scoped sessions collided at %q", codexSession)
	}
	list, err := store.ListSessions(codexProject, ResourceFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 2 {
		t.Fatalf("session count = %d, want Codex and Claude sessions", len(list.Sessions))
	}
	projects, err := store.ListProjects(ResourceFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects) != 1 || projects.Projects[0].Summary.RequestCount != 2 {
		t.Fatalf("project aggregate = %+v, want two cross-client requests", projects.Projects)
	}
	var sessionRequests int64
	for _, session := range list.Sessions {
		sessionRequests += session.Summary.RequestCount
	}
	if sessionRequests != projects.Projects[0].Summary.RequestCount {
		t.Fatalf("session requests %d do not reconcile to project requests %d", sessionRequests, projects.Projects[0].Summary.RequestCount)
	}
}

func TestProjectReconciliationUsesLocationNotDisplayName(t *testing.T) {
	store := openTestStore(t)
	first, _ := appendImportedFixture(t, store, "codex", "codex-cli", "one", "same-name", t.TempDir(), "one")
	second, _ := appendImportedFixture(t, store, "claude-code", "claude-code", "two", "same-name", t.TempDir(), "two")
	if first == second {
		t.Fatal("equal display names at different locations were merged")
	}
}

func TestStableAliasRecordsMovedLocationWithoutReassigningProject(t *testing.T) {
	store := openTestStore(t)
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first, sessionID := appendImportedFixture(t, store, "codex", "codex-cli", "provider-project", "project", firstRoot, "one")
	second, secondSession := appendImportedFixture(t, store, "codex", "codex-cli", "provider-project", "project", secondRoot, "two")
	if first != second {
		t.Fatalf("moved stable alias changed project from %q to %q", first, second)
	}
	if sessionID != secondSession {
		t.Fatalf("stable session alias changed from %q to %q", sessionID, secondSession)
	}
	var locations int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM project_locations WHERE project_id = ?", first).Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if locations != 2 {
		t.Fatalf("location count = %d, want old and moved locations", locations)
	}
	var preferred string
	if err := store.db.QueryRow("SELECT canonical_path FROM project_locations WHERE project_id = ? AND is_preferred = 1", first).Scan(&preferred); err != nil {
		t.Fatal(err)
	}
	wantPreferred, err := canonicalProjectLocation(secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if preferred != wantPreferred {
		t.Fatalf("preferred moved location = %q, want %q", preferred, wantPreferred)
	}
}

func TestConflictingAliasAndLocationDoesNotSilentlyMerge(t *testing.T) {
	store := openTestStore(t)
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first, _ := appendImportedFixture(t, store, "codex", "codex-cli", "stable-a", "first", firstRoot, "first")
	second, _ := appendImportedFixture(t, store, "claude-code", "claude-code", "stable-b", "second", secondRoot, "second")
	conflicted, _ := appendImportedFixture(t, store, "codex", "codex-cli", "stable-a", "first", secondRoot, "conflict")
	appendImportedFixture(t, store, "codex", "codex-cli", "stable-a", "first", secondRoot, "conflict-repeat")
	if conflicted != first || conflicted == second {
		t.Fatalf("conflict reassigned alias: first=%q second=%q conflicted=%q", first, second, conflicted)
	}
	var claimed, warnings, occurrences int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM project_locations WHERE project_id = ? AND canonical_path = ?", first, secondRoot).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM reconciliation_warnings WHERE kind = 'alias-location-conflict'").Scan(&warnings); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT occurrence_count FROM reconciliation_warnings WHERE kind = 'alias-location-conflict'").Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if claimed != 0 || warnings != 1 || occurrences != 2 {
		t.Fatalf("conflicting location claimed=%d warnings=%d occurrences=%d, want 0/1/2", claimed, warnings, occurrences)
	}
}

func TestCanonicalLocationResolvesSymlink(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	first, _ := appendImportedFixture(t, store, "codex", "codex-cli", "one", "project", target, "one")
	second, _ := appendImportedFixture(t, store, "claude-code", "claude-code", "two", "project", link, "two")
	if first != second {
		t.Fatalf("symlinked paths resolved to different projects: %q, %q", first, second)
	}
}

func TestConcurrentImportAndProjectBrowsing(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	filter := ResourceFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 16)
	for index := 0; index < 8; index++ {
		index := index
		wait.Add(2)
		go func() {
			defer wait.Done()
			event := testEvent(fmt.Sprintf("concurrent-%d", index))
			event.EventID = fmt.Sprintf("concurrent-event-%d", index)
			event.ProjectID, event.SessionID = "shared-project", fmt.Sprintf("session-%d", index)
			event.Provenance.SourceID, event.Provenance.SourceRecord = fmt.Sprintf("source-%d", index), fmt.Sprintf("record-%d", index)
			_, err := store.AppendWithReferences([]UsageEvent{event},
				[]Project{{ID: event.ProjectID, Root: root, DisplayName: "Shared", SourceAdapter: "codex", ProviderAlias: event.ProjectID}},
				[]Session{{ID: event.SessionID, ProjectID: event.ProjectID, Client: event.Client, SourceAdapter: "codex", ProviderAlias: event.SessionID}})
			if err != nil {
				errorsFound <- err
			}
		}()
		go func() {
			defer wait.Done()
			if _, err := store.ListProjects(filter); err != nil {
				errorsFound <- err
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent operation: %v", err)
	}
}

func TestUnattributedResourcesReconcileWithGlobalTotals(t *testing.T) {
	store := openTestStore(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	attributed := testEvent("attributed")
	attributed.EventID, attributed.ProjectID, attributed.SessionID = "attributed-event", "project", "session"
	unattributed := testEvent("unattributed")
	unattributed.EventID, unattributed.ProjectID, unattributed.SessionID = "unattributed-event", "", ""
	projectScoped := testEvent("project-scoped-unattributed-session")
	projectScoped.EventID, projectScoped.ProjectID, projectScoped.SessionID = "project-scoped-event", "project", ""
	projectScoped.Provenance.SourceRecord = "project-scoped-record"
	if _, err := store.AppendWithReferences([]UsageEvent{attributed}, []Project{{ID: "project", DisplayName: "Project"}}, []Session{{ID: "session", ProjectID: "project", Client: attributed.Client}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append([]UsageEvent{unattributed}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append([]UsageEvent{projectScoped}); err != nil {
		t.Fatal(err)
	}
	filter := ResourceFilter{From: from, To: to}
	projects, err := store.ListProjects(filter)
	if err != nil {
		t.Fatal(err)
	}
	global, err := store.Query(UsageFilter{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	var projectRequests int64
	foundUnattributed := false
	for _, project := range projects.Projects {
		projectRequests += project.Summary.RequestCount
		if project.ID == UnattributedProjectID {
			foundUnattributed = project.DisplayName == "Unattributed" && project.Summary.RequestCount == 1
		}
	}
	if !foundUnattributed || projectRequests != global.Summary.RequestCount {
		t.Fatalf("project resources = %+v requests=%d, global=%d", projects.Projects, projectRequests, global.Summary.RequestCount)
	}
	projectSessions, err := store.ListSessions("project", filter)
	if err != nil {
		t.Fatal(err)
	}
	var projectSessionRequests int64
	var projectSessionCount int64
	foundMissingSession := false
	for _, session := range projectSessions.Sessions {
		projectSessionRequests += session.Summary.RequestCount
		projectSessionCount += session.Summary.SessionCount
		if session.ID == UnattributedSessionID {
			foundMissingSession = session.ProjectID == "project" && session.Summary.RequestCount == 1
		}
	}
	project, err := store.ProjectResource("project", filter)
	if err != nil {
		t.Fatal(err)
	}
	if !foundMissingSession || len(projectSessions.Sessions) != 2 || projectSessionRequests != project.Summary.RequestCount || projectSessionCount != project.Summary.SessionCount || project.Summary.SessionCount != 2 {
		t.Fatalf("project sessions = %+v request sum=%d session count=%d project=%+v", projectSessions.Sessions, projectSessionRequests, projectSessionCount, project.Summary)
	}
	missingSession, err := store.SessionResource(UnattributedSessionID, "project", filter)
	if err != nil || missingSession.Summary.RequestCount != 1 || missingSession.Summary.SessionCount != 1 {
		t.Fatalf("project-scoped Unattributed session = %+v, %v", missingSession, err)
	}
	if _, err := store.SessionResource(UnattributedSessionID, "", filter); err == nil {
		t.Fatal("ambiguous Unattributed session lookup without project was accepted")
	}
	sessions, err := store.ListSessions(UnattributedProjectID, filter)
	if err != nil {
		t.Fatal(err)
	}
	var sessionRequests int64
	for _, session := range sessions.Sessions {
		sessionRequests += session.Summary.RequestCount
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != UnattributedSessionID || sessionRequests != 1 {
		t.Fatalf("unattributed sessions = %+v, request sum=%d", sessions.Sessions, sessionRequests)
	}
}

func appendImportedFixture(t *testing.T, store *Store, adapter, client, projectAlias, projectName, root, suffix string) (string, string) {
	t.Helper()
	event := testEvent("identity-" + suffix)
	event.EventID = "identity-event-" + suffix
	event.ProjectID = projectAlias
	event.SessionID = "same-provider-session"
	event.Client.Name = client
	event.Provenance.Adapter = adapter
	event.Provenance.SourceID = "source-" + suffix
	event.Provenance.SourceRecord = "record-" + suffix
	project := Project{ID: projectAlias, Root: root, DisplayName: projectName, SourceAdapter: adapter, ProviderAlias: projectAlias, AliasProvenance: "test metadata"}
	session := Session{ID: event.SessionID, ProjectID: projectAlias, Client: Client{Name: client}, SourceAdapter: adapter, ProviderAlias: event.SessionID}
	if _, err := store.AppendWithReferences([]UsageEvent{event}, []Project{project}, []Session{session}); err != nil {
		t.Fatal(err)
	}
	var projectID, sessionID string
	if err := store.db.QueryRow("SELECT project_id, session_id FROM usage_events WHERE event_id = ?", event.EventID).Scan(&projectID, &sessionID); err != nil {
		t.Fatal(err)
	}
	return projectID, sessionID
}
