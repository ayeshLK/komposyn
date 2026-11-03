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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexRecoveryHistoryListsAndInspectsCommittedEvidence(t *testing.T) {
	store := openTestStore(t)
	databasePath := store.databasePath
	sourcePath := filepath.Join(t.TempDir(), "synthetic-private-path-marker.jsonl")
	receipt, expectedInserted, expectedOverride := applySyntheticHistoryRecovery(t, store, sourcePath, "synthetic-session-marker", "synthetic-project-marker")
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}

	reportFilter := UsageFilter{From: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
	beforeReport, err := store.Query(reportFilter)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(beforeReport)
	if err != nil {
		t.Fatal(err)
	}
	var beforeCSV bytes.Buffer
	if err := WriteCSV(&beforeCSV, beforeReport); err != nil {
		t.Fatal(err)
	}
	beforeChanges := recoveryTotalChanges(t, store)

	listed, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{})
	if err != nil {
		t.Fatalf("ListCodexRecoveries after deleting provider source: %v", err)
	}
	if listed.ContractVersion != CodexRecoveryHistoryContractVersion || len(listed.Runs) != 1 || listed.NextToken != "" {
		t.Fatalf("history page = %+v", listed)
	}
	run := listed.Runs[0]
	if run.RunReference != receipt.RunReference || len(run.SourceReference) != 64 || run.SourceGeneration < 0 ||
		run.SnapshotBytes <= 0 || run.FormatVersion != 1 || run.InsertedEventCount != 1 || run.OverriddenEventCount != 1 || run.CommittedAt != receipt.CommittedAt {
		t.Fatalf("run summary = %+v; receipt=%+v", run, receipt)
	}

	inspection, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: run.RunReference})
	if err != nil {
		t.Fatalf("InspectCodexRecovery after deleting provider source: %v", err)
	}
	if inspection.ContractVersion != CodexRecoveryHistoryContractVersion || inspection.Run != run || len(inspection.Allocations) != 2 || inspection.NextToken != "" || inspection.EvidenceAsOf.IsZero() {
		t.Fatalf("inspection = %+v", inspection)
	}
	var inserted, overridden *CodexRecoveryAllocationDetail
	for i := range inspection.Allocations {
		allocation := &inspection.Allocations[i]
		switch allocation.Kind {
		case CodexRecoveryAllocationInserted:
			inserted = allocation
		case CodexRecoveryAllocationOverride:
			overridden = allocation
		default:
			t.Fatalf("unexpected allocation kind %q", allocation.Kind)
		}
	}
	if inserted == nil || overridden == nil {
		t.Fatalf("allocation kinds missing: %+v", inspection.Allocations)
	}
	if inserted.Before != nil || inserted.PreviousAllocationReference != "" || inserted.Original != expectedInserted ||
		inserted.After != expectedInserted || inserted.CurrentEffective != expectedInserted || inserted.IsCurrentAllocation ||
		inserted.CurrentAllocationReference != "" {
		t.Fatalf("inserted allocation semantics = %+v; expected=%+v", *inserted, expectedInserted)
	}
	if overridden.Before == nil || *overridden.Before != expectedOverride.before || overridden.Original != expectedOverride.before ||
		overridden.After != expectedOverride.after || overridden.CurrentEffective != expectedOverride.after ||
		overridden.PreviousAllocationReference != "" || overridden.CurrentAllocationReference != overridden.AllocationReference || !overridden.IsCurrentAllocation {
		t.Fatalf("override allocation semantics = %+v; expected before=%+v after=%+v", *overridden, expectedOverride.before, expectedOverride.after)
	}
	if inserted.EventReference == overridden.EventReference || len(inserted.EventReference) != 64 || len(overridden.AllocationReference) != 64 {
		t.Fatalf("opaque relationships invalid: inserted=%+v override=%+v", inserted, overridden)
	}

	encoded, err := json.Marshal(struct {
		List       CodexRecoveryHistoryPage `json:"list"`
		Inspection CodexRecoveryInspection  `json:"inspection"`
	}{listed, inspection})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{sourcePath, "synthetic-private-path-marker", "synthetic-session-marker", "synthetic-project-marker", "gpt-test", "codex-file-"} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatalf("history response leaked synthetic marker %q: %s", private, encoded)
		}
	}
	if len(encoded) > MaxCodexRecoveryHistoryResponseBytes*2 {
		t.Fatalf("combined test serialization unexpectedly large: %d", len(encoded))
	}
	if got := recoveryTotalChanges(t, store); got != beforeChanges {
		t.Fatalf("read-only history API changed SQLite total_changes: %d -> %d", beforeChanges, got)
	}
	afterReport, err := store.Query(reportFilter)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(afterReport)
	if err != nil || !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatalf("history inspection changed report: marshal err=%v before=%s after=%s", err, beforeJSON, afterJSON)
	}
	var afterCSV bytes.Buffer
	if err := WriteCSV(&afterCSV, afterReport); err != nil || !bytes.Equal(beforeCSV.Bytes(), afterCSV.Bytes()) {
		t.Fatalf("history inspection changed CSV report: write err=%v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	repeated, err := reopened.ListCodexRecoveries(CodexRecoveryHistoryQuery{})
	if err != nil || len(repeated.Runs) != 1 || repeated.Runs[0].RunReference != run.RunReference || repeated.Runs[0].SourceReference != run.SourceReference {
		t.Fatalf("stable run references after reopen = %+v, err=%v", repeated, err)
	}
	repeatedInspection, err := reopened.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: run.RunReference})
	if err != nil || len(repeatedInspection.Allocations) != 2 {
		t.Fatalf("inspection after reopen = %+v, err=%v", repeatedInspection, err)
	}
	refs := map[string]bool{}
	for _, allocation := range repeatedInspection.Allocations {
		refs[allocation.AllocationReference] = true
		refs[allocation.EventReference] = true
	}
	if !refs[inserted.AllocationReference] || !refs[inserted.EventReference] || !refs[overridden.AllocationReference] || !refs[overridden.EventReference] {
		t.Fatalf("opaque references changed after reopen: %+v", refs)
	}
}

func TestCodexRecoveryHistoryPaginationIsBoundedAndScoped(t *testing.T) {
	store := openTestStore(t)
	firstReceipt, _, _ := applySyntheticHistoryRecovery(t, store, filepath.Join(t.TempDir(), "first.jsonl"), "session-one", "project-one")
	secondReceipt, _, _ := applySyntheticHistoryRecovery(t, store, filepath.Join(t.TempDir(), "second.jsonl"), "session-two", "project-two")
	firstPage, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: 1})
	if err != nil || len(firstPage.Runs) != 1 || firstPage.Runs[0].RunReference != secondReceipt.RunReference || firstPage.NextToken == "" {
		t.Fatalf("first run page = %+v, err=%v", firstPage, err)
	}
	applySyntheticHistoryRecovery(t, store, filepath.Join(t.TempDir(), "concurrent-append.jsonl"), "session-three", "project-three")
	secondPage, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: 1, ContinuationToken: firstPage.NextToken})
	if err != nil || len(secondPage.Runs) != 1 || secondPage.Runs[0].RunReference != firstReceipt.RunReference || secondPage.NextToken != "" {
		t.Fatalf("second run page after concurrent append = %+v, err=%v", secondPage, err)
	}
	if _, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: 2, ContinuationToken: firstPage.NextToken}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("page-size-mismatched token error = %v", err)
	}

	firstInspection, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: firstReceipt.RunReference, PageSize: 1})
	if err != nil || len(firstInspection.Allocations) != 1 || firstInspection.NextToken == "" {
		t.Fatalf("first allocation page = %+v, err=%v", firstInspection, err)
	}
	secondInspection, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: firstReceipt.RunReference, PageSize: 1,
		ContinuationToken: firstInspection.NextToken})
	if err != nil || len(secondInspection.Allocations) != 1 || secondInspection.NextToken != "" {
		t.Fatalf("second allocation page = %+v, err=%v", secondInspection, err)
	}
	if firstInspection.Allocations[0].EventReference == secondInspection.Allocations[0].EventReference {
		t.Fatalf("allocation pagination repeated item %q", firstInspection.Allocations[0].EventReference)
	}
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: secondReceipt.RunReference, PageSize: 1,
		ContinuationToken: firstInspection.NextToken}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("cross-run continuation error = %v", err)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(firstPage.NextToken)
	if err != nil {
		t.Fatalf("decode continuation token for tampering: %v", err)
	}
	if len(sealed) == 0 {
		t.Fatal("continuation token decoded to an empty payload")
	}
	sealed[len(sealed)-1] ^= 1 // Flip an authenticated byte while keeping valid base64url encoding.
	tampered := base64.RawURLEncoding.EncodeToString(sealed)
	if tampered == firstPage.NextToken {
		t.Fatal("tampering did not change the continuation token")
	}
	if _, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: 1, ContinuationToken: tampered}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("tampered continuation error = %v", err)
	}
	if _, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: MaxCodexRecoveryHistoryPageSize + 1}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("oversized page error = %v", err)
	}
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: strings.Repeat("a", 64), PageSize: 1,
		ContinuationToken: firstInspection.NextToken}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("cross-run token with nonexistent run error = %v", err)
	}
	malformedReference := "../../synthetic-private-path-marker"
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: malformedReference}); !errors.Is(err, ErrCodexRecoveryRunNotFound) {
		t.Fatalf("malformed run reference error = %v", err)
	} else if strings.Contains(err.Error(), malformedReference) {
		t.Fatalf("safe error echoed malformed private reference: %v", err)
	}
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: strings.Repeat("0", 64)}); !errors.Is(err, ErrCodexRecoveryRunNotFound) {
		t.Fatalf("missing run reference error = %v", err)
	}
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: strings.Repeat("a", 65)}); !errors.Is(err, ErrCodexRecoveryRunNotFound) {
		t.Fatalf("oversized run reference error = %v", err)
	}
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: firstReceipt.RunReference, PageSize: MaxCodexRecoveryHistoryPageSize + 1}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("oversized inspection page error = %v", err)
	}
	if _, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{ContinuationToken: strings.Repeat("x", MaxCodexRecoveryHistoryTokenBytes+1)}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("oversized token error = %v", err)
	}
	otherStore, err := Open(filepath.Join(t.TempDir(), "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Close()
	if _, err := otherStore.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: 1, ContinuationToken: firstPage.NextToken}); !errors.Is(err, ErrInvalidCodexRecoveryHistoryQuery) {
		t.Fatalf("cross-store continuation error = %v", err)
	}
}

func TestCodexRecoveryHistorySeparatesHistoricalAndCurrentAllocations(t *testing.T) {
	store := openTestStore(t)
	receipt, _, expected := applySyntheticHistoryRecovery(t, store, filepath.Join(t.TempDir(), "source.jsonl"), "session-current", "project-current")
	var key []byte
	if err := store.db.QueryRow("SELECT namespace_key FROM recovery_meta WHERE singleton=1").Scan(&key); err != nil {
		t.Fatal(err)
	}
	var eventID, priorID, sourceID, snapshotDigest string
	var generation, snapshotSize int64
	if err := store.db.QueryRow(`SELECT a.event_id,a.allocation_id,r.source_id,r.source_generation,r.snapshot_size,r.snapshot_digest
		FROM recovery_allocations a JOIN recovery_runs r ON r.run_id=a.run_id
		WHERE r.run_id=? AND a.entry_kind='override'`, receipt.RunReference).Scan(&eventID, &priorID, &sourceID, &generation, &snapshotSize, &snapshotDigest); err != nil {
		t.Fatal(err)
	}
	laterPlan := strings.Repeat("b", 64)
	laterRun := recoveryOpaque(key, "run", laterPlan)
	committedAt := formatStoredTime(receipt.CommittedAt.Add(time.Second))
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO recovery_runs(run_id,plan_digest,format_version,source_id,source_generation,
		snapshot_digest,snapshot_size,inserted_event_count,overridden_event_count,audit_json,committed_at)
		VALUES(?,?,1,?,?,?,?,0,1,?,?)`, laterRun, laterPlan, sourceID, generation, snapshotDigest, snapshotSize, []byte(`{}`), committedAt); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert synthetic later run: %v", err)
	}
	newEffective := expected.after
	newEffective.InputTokens += 7
	newEffective.OutputTokens += 2
	previous := priorID
	newAllocation, err := insertRecoveryAllocation(context.Background(), tx, key, laterRun, eventID,
		string(CodexRecoveryAllocationOverride), &previous, &expected.after, newEffective, committedAt)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert synthetic later allocation: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO current_event_allocations(event_id,allocation_id) VALUES(?,?)
		ON CONFLICT(event_id) DO UPDATE SET allocation_id=excluded.allocation_id`, eventID, newAllocation); err != nil {
		_ = tx.Rollback()
		t.Fatalf("advance synthetic current allocation: %v", err)
	}
	if _, err := tx.Exec("UPDATE source_allocation_revisions SET revision=revision+1 WHERE source_id=?", sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	firstRun, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: receipt.RunReference})
	if err != nil {
		t.Fatal(err)
	}
	var historical *CodexRecoveryAllocationDetail
	for i := range firstRun.Allocations {
		if firstRun.Allocations[i].Kind == CodexRecoveryAllocationOverride {
			historical = &firstRun.Allocations[i]
		}
	}
	if historical == nil || historical.Before == nil || *historical.Before != expected.before || historical.Original != expected.before ||
		historical.After != expected.after || historical.CurrentEffective != newEffective || historical.IsCurrentAllocation ||
		historical.CurrentAllocationReference != recoveryOpaque(key, "allocation", newAllocation) {
		t.Fatalf("historical versus current projection = %+v", historical)
	}
	secondRun, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: laterRun})
	if err != nil || len(secondRun.Allocations) != 1 {
		t.Fatalf("later run inspection = %+v, err=%v", secondRun, err)
	}
	entry := secondRun.Allocations[0]
	if entry.PreviousAllocationReference != recoveryOpaque(key, "allocation", priorID) || entry.Before == nil || *entry.Before != expected.after ||
		entry.After != newEffective || entry.CurrentEffective != newEffective || !entry.IsCurrentAllocation {
		t.Fatalf("later allocation chain = %+v", entry)
	}
}

func TestCodexRecoveryHistoryDoesNotExposeOrLoadAuditJSON(t *testing.T) {
	store := openTestStore(t)
	sourcePath := filepath.Join(t.TempDir(), "private-audit.jsonl")
	receipt, _, _ := applySyntheticHistoryRecovery(t, store, sourcePath, "private-session", "private-project")
	if _, err := store.db.Exec("DROP TRIGGER recovery_runs_no_update"); err != nil {
		t.Fatal(err)
	}
	privateMarkers := []string{"synthetic-audit-private-marker", "synthetic-source-id-marker", "synthetic-model-marker", "synthetic-pricing-url-marker", "synthetic-parser-error-marker", "/synthetic/private/path"}
	private := strings.Join(privateMarkers, " ")
	largeAudit := []byte(`{"private":"` + private + `","padding":"` + strings.Repeat("x", (15<<20)-256) + `"}`)
	if len(largeAudit) > 16<<20 {
		t.Fatalf("fixture audit too large: %d", len(largeAudit))
	}
	if _, err := store.db.Exec("UPDATE recovery_runs SET source_id=?,audit_json=? WHERE run_id=?", privateMarkers[1], largeAudit, receipt.RunReference); err != nil {
		t.Fatalf("install bounded synthetic audit blob: %v", err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET requested_model=?,pricing_version=?,cost_currency=? WHERE event_id IN (SELECT event_id FROM recovery_allocations WHERE run_id=?)",
		privateMarkers[2], privateMarkers[3], "USD", receipt.RunReference); err != nil {
		t.Fatalf("install synthetic private event metadata: %v", err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListCodexRecoveries(CodexRecoveryHistoryQuery{PageSize: 1})
	if err != nil || len(listed.Runs) != 1 {
		t.Fatalf("list with large audit blob = %+v, err=%v", listed, err)
	}
	inspected, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: receipt.RunReference, PageSize: 1})
	if err != nil || len(inspected.Allocations) != 1 || inspected.NextToken == "" {
		t.Fatalf("inspect with large audit blob = %+v, err=%v", inspected, err)
	}
	encoded, err := json.Marshal(struct {
		List   CodexRecoveryHistoryPage `json:"list"`
		Detail CodexRecoveryInspection  `json:"detail"`
	}{listed, inspected})
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range privateMarkers {
		if bytes.Contains(encoded, []byte(marker)) {
			t.Fatalf("bounded response exposed audit marker %q", marker)
		}
	}
	if len(encoded) > MaxCodexRecoveryHistoryResponseBytes*2 {
		t.Fatalf("bounded response exceeded test bound: bytes=%d", len(encoded))
	}
	if plan := recoveryHistoryAllocationQueryPlan(t, store, receipt.RunReference); !strings.Contains(strings.ToUpper(plan), "INDEX") {
		t.Fatalf("allocation page did not use indexed access: %s", plan)
	}
	if plan := recoveryHistoryRunQueryPlan(t, store); strings.Contains(strings.ToUpper(plan), "TEMP B-TREE") {
		t.Fatalf("run page required a sort instead of row-order scan: %s", plan)
	}
}

func TestCodexRecoveryHistoryRefusesIncompleteAllocationEvidence(t *testing.T) {
	store := openTestStore(t)
	receipt, _, _ := applySyntheticHistoryRecovery(t, store, filepath.Join(t.TempDir(), "source.jsonl"), "session-incomplete", "project-incomplete")
	if _, err := store.db.Exec("DROP TRIGGER recovery_allocations_no_delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM recovery_allocations WHERE run_id=? AND entry_kind='inserted'", receipt.RunReference); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InspectCodexRecovery(CodexRecoveryInspectionRequest{RunReference: receipt.RunReference}); !errors.Is(err, ErrCodexRecoveryHistoryUnavailable) {
		t.Fatalf("incomplete allocation evidence error = %v", err)
	}
}

func TestCodexRecoveryHistoryBoundsLargeRunPages(t *testing.T) {
	store := openTestStore(t)
	var namespaceKey []byte
	if err := store.db.QueryRow("SELECT namespace_key FROM recovery_meta WHERE singleton=1").Scan(&namespaceKey); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	committedAt := formatStoredTime(time.Now().UTC())
	for i := 1; i <= 121; i++ {
		planDigest := fmt.Sprintf("%064x", i+1000)
		runID := recoveryOpaque(namespaceKey, "run", planDigest)
		if _, err := tx.Exec(`INSERT INTO recovery_runs(run_id,plan_digest,format_version,source_id,source_generation,
			snapshot_digest,snapshot_size,inserted_event_count,overridden_event_count,audit_json,committed_at)
			VALUES(?,?,1,?,0,?,0,0,0,?,?)`, runID, planDigest, fmt.Sprintf("synthetic-source-%d", i), planDigest, []byte(`{}`), committedAt); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert synthetic bounded-history row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	query := CodexRecoveryHistoryQuery{PageSize: MaxCodexRecoveryHistoryPageSize}
	pageCount := 0
	for {
		page, err := store.ListCodexRecoveries(query)
		if err != nil {
			t.Fatalf("large history page %d: %v", pageCount, err)
		}
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded) > MaxCodexRecoveryHistoryResponseBytes || len(page.Runs) > MaxCodexRecoveryHistoryPageSize {
			t.Fatalf("large history response bounds: items=%d bytes=%d err=%v", len(page.Runs), len(encoded), err)
		}
		if page.NextToken != "" && len(page.NextToken) > MaxCodexRecoveryHistoryTokenBytes {
			t.Fatalf("large history token length=%d", len(page.NextToken))
		}
		for _, run := range page.Runs {
			if seen[run.RunReference] {
				t.Fatalf("run repeated across pages: %s", run.RunReference)
			}
			seen[run.RunReference] = true
		}
		pageCount++
		if page.NextToken == "" {
			break
		}
		query.ContinuationToken = page.NextToken
	}
	if len(seen) != 121 || pageCount != 3 {
		t.Fatalf("large history coverage: runs=%d pages=%d, want 121/3", len(seen), pageCount)
	}
	if plan := recoveryHistoryRunQueryPlan(t, store); strings.Contains(strings.ToUpper(plan), "TEMP B-TREE") {
		t.Fatalf("run page required a sort instead of row-order scan: %s", plan)
	}
}

type syntheticExpectedOverride struct {
	before CodexRecoveryTokenVector
	after  CodexRecoveryTokenVector
}

func applySyntheticHistoryRecovery(t *testing.T, store *Store, path, session, project string) (CodexRecoveryReceipt, CodexRecoveryTokenVector, syntheticExpectedOverride) {
	t.Helper()
	contents := richLegacyRecoveryFixture(session, project)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 3 {
		t.Fatalf("synthetic initial import = %+v, %v", result, err)
	}
	sourceID := "codex-file-" + digest([]byte(path))
	rows, err := store.db.Query("SELECT event_id FROM usage_events WHERE source_id=? AND source_record_id IN ('A','B','C') ORDER BY source_offset", sourceID)
	if err != nil {
		t.Fatal(err)
	}
	var eventIDs []string
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(eventIDs) != 3 {
		t.Fatalf("synthetic source event count=%d, want 3", len(eventIDs))
	}
	if _, err := store.db.Exec("DELETE FROM usage_events WHERE event_id=?", eventIDs[1]); err != nil {
		t.Fatal(err)
	}
	oldC := CodexRecoveryTokenVector{InputTokens: 50, OutputTokens: 5, CacheReadTokens: 10, CacheWriteTokens: 0, ReasoningTokens: 2}
	if _, err := store.db.Exec(`UPDATE usage_events SET input_tokens=?,output_tokens=?,cache_read_tokens=?,cache_write_tokens=?,
		cache_write_5m_tokens=?,cache_write_1h_tokens=?,reasoning_tokens=? WHERE event_id=?`, oldC.InputTokens, oldC.OutputTokens,
		oldC.CacheReadTokens, oldC.CacheWriteTokens, oldC.CacheWrite5mTokens, oldC.CacheWrite1hTokens, oldC.ReasoningTokens, eventIDs[2]); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PrepareCodexRecovery(context.Background(), path)
	if err != nil {
		t.Fatalf("synthetic PrepareCodexRecovery = %v", err)
	}
	receipt, err := store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken)
	if err != nil {
		t.Fatalf("synthetic ApplyCodexRecovery = %v", err)
	}
	inserted := CodexRecoveryTokenVector{InputTokens: 50, OutputTokens: 5, CacheReadTokens: 5, CacheWriteTokens: 1, ReasoningTokens: 1}
	after := CodexRecoveryTokenVector{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 15, CacheWriteTokens: 2, ReasoningTokens: 3}
	return receipt, inserted, syntheticExpectedOverride{before: oldC, after: after}
}

func richLegacyRecoveryFixture(session, project string) []byte {
	meta := fmt.Sprintf(`{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":%q,"project_id":%q,"model":"gpt-test"}}`+"\n", session, project)
	record := func(id, timestamp string, input, cached, read, write, output, reasoning int, reasoningFlag string) string {
		return fmt.Sprintf(`{"type":"event_msg","id":%q,"timestamp":%q,"payload":{"type":"token_count","session_id":%q,"info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_read_input_tokens":%d,"cache_write_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d},"last_usage_includes_reasoning":%s}}}`+"\n",
			id, timestamp, session, input, cached, read, write, output, reasoning, reasoningFlag)
	}
	// A is the first accepted cumulative snapshot. B resets the counter, and C
	// includes B plus the next delta. The reconstructed old state stores A and
	// C's former A-to-C delta, just like the accepted recovery example.
	return []byte(meta +
		record("A", "2026-10-11T00:00:01Z", 100, 4, 6, 3, 10, 2, "null") +
		record("B", "2026-10-11T00:00:02Z", 50, 2, 3, 1, 5, 1, "true") +
		record("C", "2026-10-11T00:00:03Z", 150, 8, 12, 3, 15, 4, "null"))
}

func recoveryHistoryAllocationQueryPlan(t *testing.T, store *Store, runReference string) string {
	t.Helper()
	rows, err := store.db.Query(`EXPLAIN QUERY PLAN SELECT a.event_id FROM recovery_allocations AS a
		WHERE a.run_id=? ORDER BY a.event_id LIMIT ?`, runReference, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	return plan
}

func recoveryHistoryRunQueryPlan(t *testing.T, store *Store) string {
	t.Helper()
	rows, err := store.db.Query("EXPLAIN QUERY PLAN SELECT rowid FROM recovery_runs WHERE rowid<=? ORDER BY rowid DESC LIMIT ?", int64(100), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	return plan
}
