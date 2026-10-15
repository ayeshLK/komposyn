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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
	if conflicted != first || conflicted == second {
		t.Fatalf("conflict reassigned alias: first=%q second=%q conflicted=%q", first, second, conflicted)
	}
	var claimed, warnings int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM project_locations WHERE project_id = ? AND canonical_path = ?", first, secondRoot).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM reconciliation_warnings WHERE kind = 'alias-location-conflict'").Scan(&warnings); err != nil {
		t.Fatal(err)
	}
	if claimed != 0 || warnings != 1 {
		t.Fatalf("conflicting location claimed=%d warnings=%d, want 0/1", claimed, warnings)
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
