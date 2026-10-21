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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestInspectProjectSeparatesSelectedAndStoredHistory(t *testing.T) {
	store, err := Open(t.TempDir() + "/inspection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	projectID := "local-project"
	if err := store.UpsertProject(Project{ID: projectID, DisplayName: "Unattributed", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{
		{ID: "usage-session", ProjectID: projectID, Client: Client{Name: "codex-cli"}},
		{ID: "metadata-session", ProjectID: projectID, Client: Client{Name: "claude-code"}},
	} {
		if err := store.UpsertSession(session); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2025, 12, 15, 12, 0, 0, 0, time.UTC)
	sourceID := t.TempDir() + "/unread-source.jsonl"
	event := UsageEvent{
		EventID: "event-1", SchemaVersion: 1, EventType: "model.request", OccurredAt: old,
		ProjectID: projectID, SessionID: "usage-session", Client: Client{Name: "codex-cli"},
		Provider: "openai", RequestedModel: "gpt-5", Usage: TokenUsage{InputTokens: 20, OutputTokens: 10},
		Provenance:   Provenance{Adapter: "codex", SourceID: sourceID, SourceRecord: "record-1"},
		IngestionKey: "inspection-event-1",
	}
	if receipt, err := store.AppendWithCursor([]UsageEvent{event}, Cursor{
		SourceID: sourceID, FileFingerprint: strings.Repeat("a", 64), ByteOffset: 128,
		SourceGeneration: 2, PendingTail: true, SnapshotReusable: false,
	}); err != nil || receipt.Imported != 1 {
		t.Fatalf("append = %+v, %v", receipt, err)
	}
	for _, alias := range []string{"private-alias-one", "private-alias-two"} {
		if _, err := store.db.Exec(`INSERT INTO project_aliases(scope, alias, project_id, provenance, first_seen_at, last_seen_at)
			VALUES ('codex', ?, ?, 'provider metadata', ?, ?)`, alias, projectID, formatStoredTime(old), formatStoredTime(old)); err != nil {
			t.Fatal(err)
		}
	}
	for _, location := range []string{"/private/work-a", "/private/work-b"} {
		if _, err := store.db.Exec(`INSERT INTO project_locations(project_id, canonical_path, display_path, is_preferred, first_seen_at, last_seen_at, provenance)
			VALUES (?, ?, ?, 0, ?, ?, 'test evidence')`, projectID, location, location, formatStoredTime(old), formatStoredTime(old)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO reconciliation_warnings(kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at, occurrence_count)
		VALUES ('alias-location-conflict', 'codex', 'digest', ?, '', 'established alias retained; location belongs to another local project', ?, ?, 2)`,
		projectID, formatStoredTime(old), formatStoredTime(old)); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	var beforeWrites int64
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&beforeWrites); err != nil {
		t.Fatal(err)
	}
	result, err := store.InspectProject(ProjectInspectionRequest{ProjectID: projectID, From: from, To: to, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.FormatVersion != 1 || !result.Exists || result.Synthetic {
		t.Fatalf("identity = %+v", result)
	}
	if result.NameOrigin != InspectionEvidenceUnavailable {
		t.Fatalf("name origin = %q; must not infer from the label", result.NameOrigin)
	}
	if result.ClassificationEvidence.Status != InspectionEvidenceUnavailable {
		t.Fatalf("classification evidence = %+v; adapter labels cannot establish the original decision", result.ClassificationEvidence)
	}
	if result.StoredSessionCount != 2 || result.SelectedUsageSessionCount != 0 || result.HistoricalUsageSessionCount != 1 {
		t.Fatalf("session counts stored/selected/history = %d/%d/%d",
			result.StoredSessionCount, result.SelectedUsageSessionCount, result.HistoricalUsageSessionCount)
	}
	if result.SelectedActivity.HasMatchingEvents || result.SelectedUsage.RequestCount != 0 {
		t.Fatalf("selected activity/usage = %+v / %+v", result.SelectedActivity, result.SelectedUsage)
	}
	if result.HistoricalCoverage != "stored_events_only" || result.HistoricalActivity.EventCount != 1 ||
		result.HistoricalActivity.FirstActive == nil || !result.HistoricalActivity.FirstActive.Equal(old) {
		t.Fatalf("historical activity = %+v coverage=%q", result.HistoricalActivity, result.HistoricalCoverage)
	}
	if result.WarningsPage.Total != 1 || result.RecoveryClassification != InspectionRecoveryReviewReconciliation {
		t.Fatalf("warnings/recovery = %+v / %q", result.WarningsPage, result.RecoveryClassification)
	}
	if result.AliasesPage.Total != 2 || len(result.Aliases) != 1 || !result.AliasesPage.HasMore {
		t.Fatalf("alias page = %+v values=%+v", result.AliasesPage, result.Aliases)
	}
	if result.LocationsPage.Total != 3 || len(result.Locations) != 1 || !result.LocationsPage.HasMore {
		t.Fatalf("location page = %+v values=%+v", result.LocationsPage, result.Locations)
	}
	if result.SourcesPage.Total != 1 || result.SourceEvidence.Status != InspectionEvidenceAvailable || len(result.Sources) != 1 || result.Sources[0].Adapter != "codex" ||
		result.Sources[0].CursorStatus != InspectionEvidenceAvailable || result.Sources[0].CursorGeneration == nil ||
		*result.Sources[0].CursorGeneration != 2 {
		t.Fatalf("source evidence = %+v", result.Sources)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"/private/work-a", "/private/work-b", "private-alias-one", "private-alias-two", sourceID} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("inspection leaked %q: %s", private, encoded)
		}
	}
	var afterWrites int64
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&afterWrites); err != nil {
		t.Fatal(err)
	}
	if afterWrites != beforeWrites {
		t.Fatalf("inspection wrote to SQLite: total_changes %d -> %d", beforeWrites, afterWrites)
	}

	next, err := store.InspectProject(ProjectInspectionRequest{ProjectID: projectID, From: from, To: to, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Aliases) != 1 || next.AliasesPage.HasMore || next.Aliases[0].Digest == result.Aliases[0].Digest {
		t.Fatalf("next alias page = %+v / %+v", next.AliasesPage, next.Aliases)
	}
	if len(next.Locations) != 1 || !next.LocationsPage.HasMore || next.Locations[0].Digest == result.Locations[0].Digest {
		t.Fatalf("next location page = %+v / %+v", next.LocationsPage, next.Locations)
	}
	last, err := store.InspectProject(ProjectInspectionRequest{ProjectID: projectID, From: from, To: to, Limit: 1, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Locations) != 1 || last.LocationsPage.HasMore || last.Locations[0].Digest == next.Locations[0].Digest {
		t.Fatalf("last location page = %+v / %+v", last.LocationsPage, last.Locations)
	}
}

func TestInspectProjectMissingAndSyntheticUnattributed(t *testing.T) {
	store, err := Open(t.TempDir() + "/inspection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	missing, err := store.InspectProject(ProjectInspectionRequest{ProjectID: "missing", From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Exists || missing.RecoveryClassification != InspectionRecoveryNotFound ||
		missing.LocationEvidence.Status != InspectionEvidenceNotRecorded ||
		missing.SourceEvidence.Status != InspectionEvidenceNotRecorded {
		t.Fatalf("missing project = %+v", missing)
	}

	if err := store.UpsertSession(Session{ID: "unattributed-session", Client: Client{Name: "codex-cli"}}); err != nil {
		t.Fatal(err)
	}
	synthetic, err := store.InspectProject(ProjectInspectionRequest{ProjectID: UnattributedProjectID, From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if !synthetic.Exists || !synthetic.Synthetic || synthetic.StoredSessionCount != 1 ||
		synthetic.DisplayName != "Unattributed" || synthetic.HistoricalCoverage != "stored_events_only" {
		t.Fatalf("synthetic project = %+v", synthetic)
	}
	if synthetic.RecoveryClassification != InspectionRecoveryRetainMetadataOnly {
		t.Fatalf("synthetic metadata-only recovery = %q", synthetic.RecoveryClassification)
	}
}

func TestInspectProjectRejectsInvalidBounds(t *testing.T) {
	store, err := Open(t.TempDir() + "/inspection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, request := range []ProjectInspectionRequest{
		{ProjectID: "p", From: from, To: to, Limit: MaxInspectionPageSize + 1},
		{ProjectID: "p", From: from, To: to, Offset: -1},
		{ProjectID: "p", From: from, To: to, Offset: MaxInspectionOffset + 1},
		{ProjectID: "p", From: from, To: from},
		{ProjectID: " ", From: from, To: to},
	} {
		if _, err := store.InspectProject(request); err == nil {
			t.Fatalf("InspectProject(%+v) succeeded; want validation error", request)
		}
	}
}

func TestInspectionPageReportsContinuationAndHardCap(t *testing.T) {
	page := inspectionPage(0, 20, 45, 20)
	if !page.HasMore || page.Truncated || page.NextOffset == nil || *page.NextOffset != 20 {
		t.Fatalf("continuation page = %+v", page)
	}
	page = inspectionPage(MaxInspectionOffset-10, 50, int64(MaxInspectionOffset+2), 10)
	if !page.HasMore || !page.Truncated || page.NextOffset != nil {
		t.Fatalf("capped page = %+v", page)
	}
}

func TestInspectProjectUsesPersistedNameOriginWhenPresent(t *testing.T) {
	store, err := Open(t.TempDir() + "/inspection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertProject(Project{ID: "project", DisplayName: "Unattributed", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var hasColumn int
	if err := store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('projects') WHERE name = 'display_name_source')").Scan(&hasColumn); err != nil {
		t.Fatal(err)
	}
	if hasColumn == 0 {
		if _, err := store.db.Exec("ALTER TABLE projects ADD COLUMN display_name_source TEXT NOT NULL DEFAULT 'unknown'"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("UPDATE projects SET display_name_source = 'fallback' WHERE id = 'project'"); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	result, err := store.InspectProject(ProjectInspectionRequest{ProjectID: "project", From: from, To: from.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if result.NameOrigin != "fallback" || result.RecoveryClassification != InspectionRecoveryCandidateNameReview {
		t.Fatalf("persisted fallback origin = %q / recovery %q", result.NameOrigin, result.RecoveryClassification)
	}
	if _, err := store.db.Exec("UPDATE projects SET display_name_source = 'unknown' WHERE id = 'project'"); err != nil {
		t.Fatal(err)
	}
	result, err = store.InspectProject(ProjectInspectionRequest{ProjectID: "project", From: from, To: from.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if result.NameOrigin != InspectionEvidenceUnavailable {
		t.Fatalf("unknown origin = %q; want unavailable without inference", result.NameOrigin)
	}
}

func TestInspectProjectCoexistsWithConcurrentStoreWrites(t *testing.T) {
	store, err := Open(t.TempDir() + "/inspection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertProject(Project{ID: "project", DisplayName: "Project"}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	errs := make(chan error, 2)
	go func() {
		for i := 0; i < 12; i++ {
			err := store.UpsertSession(Session{ID: fmt.Sprintf("writer-session-%02d", i), ProjectID: "project", Client: Client{Name: "writer"}})
			if err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	go func() {
		for i := 0; i < 12; i++ {
			_, err := store.InspectProject(ProjectInspectionRequest{ProjectID: "project", From: from, To: to, Limit: 5})
			if err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
