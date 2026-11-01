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
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCodexRecoveryPreviewsLegacyCumulativeRepairWithoutWrites(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "private-recovery-source.jsonl")
	data := legacyRecoveryFixture("session-private", "project-private")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 3 {
		t.Fatalf("baseline import = %+v, %v", result, err)
	}
	var middleKey, finalKey string
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset LIMIT 1 OFFSET 1").Scan(&middleKey); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset DESC LIMIT 1").Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the pre-fix persisted accounting: B was absent and C held
	// only its old 50/5 delta. The source identity/cursor come from a real
	// public import through the candidate parser.
	if _, err := store.db.Exec("DELETE FROM usage_events WHERE ingestion_key=?", middleKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET input_tokens=50, output_tokens=5 WHERE ingestion_key=?", finalKey); err != nil {
		t.Fatal(err)
	}
	var beforeChanges int64
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&beforeChanges); err != nil {
		t.Fatal(err)
	}
	snapshot, err := acquireStreamingSourceSnapshot(context.Background(), path, store, "codex")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.close()
	var parsedEvents []UsageEvent
	var lastRecordID string
	if _, _, err := parseCodexRecoverySnapshot(context.Background(), path, store, snapshot, &parsedEvents, &lastRecordID); err != nil {
		t.Fatal(err)
	}
	readTx, err := store.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	storedEvents, err := recoveryReadSourceEvents(readTx, snapshot.baseSourceID, snapshot.generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoveryResolveExistingIdentities(readTx, parsedEvents); err != nil {
		t.Fatal(err)
	}
	for _, parsed := range parsedEvents {
		if stored, exists := storedEvents[parsed.IngestionKey]; exists && !storedEventMatches(stored, parsed) {
			t.Fatalf("synthetic original event mismatch in controlled field %q (stored project present=%t, parsed project present=%t, equal=%t)",
				storedEventMismatchField(stored, parsed), stored.projectID != "", parsed.ProjectID != "", stored.projectID == parsed.ProjectID)
		}
	}
	_ = readTx.Rollback()
	preview, err := store.PrepareCodexRecovery(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Eligible || preview.FormatVersion != 1 || preview.AlgorithmVersion != 1 || preview.PlanToken == "" ||
		preview.ExistingEventCount != 2 || preview.InsertedEventCount != 1 || preview.OverriddenEventCount != 1 ||
		preview.BeforeRequestCount != 2 || preview.AfterRequestCount != 3 {
		t.Fatalf("preview summary = %+v", preview)
	}
	if preview.Before.InputTokens != 150 || preview.Before.OutputTokens != 15 || preview.After.InputTokens != 250 || preview.After.OutputTokens != 25 {
		t.Fatalf("preview vectors = before %+v after %+v", preview.Before, preview.After)
	}
	if len(preview.Changes) != 2 || preview.Changes[0].EventReference == "" || preview.Changes[1].EventReference == "" {
		t.Fatalf("preview changes = %+v", preview.Changes)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{path, "session-private", "project-private", "gpt-test", "codex-file-"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("preview exposed private marker %q: %s", private, encoded)
		}
	}
	var afterChanges int64
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&afterChanges); err != nil || afterChanges != beforeChanges {
		t.Fatalf("Prepare wrote to Store: total_changes %d -> %d, err=%v", beforeChanges, afterChanges, err)
	}
	first, err := store.PrepareCodexRecovery(context.Background(), path)
	if err != nil || first.PlanToken != preview.PlanToken {
		t.Fatalf("repeat preview token changed: %q != %q, err=%v", first.PlanToken, preview.PlanToken, err)
	}
	databasePath := store.databasePath
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	acrossOpen, err := reopened.PrepareCodexRecovery(context.Background(), path)
	if err != nil || acrossOpen.PlanToken != preview.PlanToken {
		t.Fatalf("persistent recovery namespace changed token across reopen: err=%v", err)
	}
}

func TestApplyCodexRecoveryCommitsOrdinaryMissingEventAndEffectiveOverrideOnce(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 3 {
		t.Fatalf("initial import = %+v, %v", result, err)
	}
	var middleKey, finalKey string
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset LIMIT 1 OFFSET 1").Scan(&middleKey); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset DESC LIMIT 1").Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM usage_events WHERE ingestion_key=?", middleKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET input_tokens=50, output_tokens=5 WHERE ingestion_key=?", finalKey); err != nil {
		t.Fatal(err)
	}
	var originalAID, originalCID string
	var originalCInput, originalCOutput int64
	if err := store.db.QueryRow("SELECT event_id FROM usage_events WHERE source_offset=(SELECT MIN(source_offset) FROM usage_events)").Scan(&originalAID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT event_id,input_tokens,output_tokens FROM usage_events WHERE ingestion_key=?", finalKey).Scan(&originalCID, &originalCInput, &originalCOutput); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PrepareCodexRecovery(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if receipt.InsertedEventCount != 1 || receipt.OverriddenEventCount != 1 || receipt.PlanToken != preview.PlanToken {
		t.Fatalf("receipt = %+v", receipt)
	}
	var count, input, output, reasoning int64
	if err := store.db.QueryRow(`SELECT COUNT(*),SUM(input_tokens),SUM(output_tokens),SUM(reasoning_tokens) FROM effective_usage_events`).Scan(&count, &input, &output, &reasoning); err != nil {
		t.Fatal(err)
	}
	if count != 3 || input != 250 || output != 25 || reasoning != 0 {
		t.Fatalf("effective totals = count %d input %d output %d reasoning %d", count, input, output, reasoning)
	}
	var baseCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&baseCount); err != nil || baseCount != 3 {
		t.Fatalf("ordinary event count = %d, err=%v", baseCount, err)
	}
	var afterAID, afterCID string
	var afterCInput, afterCOutput int64
	if err := store.db.QueryRow("SELECT event_id FROM usage_events WHERE ingestion_key=(SELECT ingestion_key FROM usage_events WHERE event_id=?)", originalAID).Scan(&afterAID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT event_id,input_tokens,output_tokens FROM usage_events WHERE event_id=?", originalCID).Scan(&afterCID, &afterCInput, &afterCOutput); err != nil {
		t.Fatal(err)
	}
	if afterAID != originalAID || afterCID != originalCID || afterCInput != originalCInput || afterCOutput != originalCOutput {
		t.Fatalf("base evidence changed: A %q/%q C %q/%q %d/%d -> %q %d/%d", originalAID, afterAID, originalCID, finalKey, originalCInput, originalCOutput, afterCID, afterCInput, afterCOutput)
	}
	var runs, allocations int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM recovery_runs").Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM recovery_allocations").Scan(&allocations); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || allocations != 2 {
		t.Fatalf("audit/journal counts = %d/%d", runs, allocations)
	}
	if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 0 {
		t.Fatalf("post-recovery verification import = %+v, %v", result, err)
	}
	var reusable bool
	if err := store.db.QueryRow("SELECT snapshot_reusable FROM source_cursors LIMIT 1").Scan(&reusable); err != nil || !reusable {
		t.Fatalf("normal full import did not revalidate recovered snapshot: reusable=%t err=%v", reusable, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	repeated, err := store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken)
	if err != nil || repeated != receipt {
		t.Fatalf("idempotent Apply receipt = %+v, err=%v; want %+v", repeated, err, receipt)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&baseCount); err != nil || baseCount != 3 {
		t.Fatalf("repeated Apply changed events: count=%d err=%v", baseCount, err)
	}
}

func TestApplyCodexRecoveryAllocationFailureRollsBackAccountingAndCanRetry(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	var middleKey, finalKey string
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset LIMIT 1 OFFSET 1").Scan(&middleKey); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset DESC LIMIT 1").Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM usage_events WHERE ingestion_key=?", middleKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET input_tokens=50, output_tokens=5 WHERE ingestion_key=?", finalKey); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PrepareCodexRecovery(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_recovery_allocation BEFORE INSERT ON recovery_allocations
		BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken)
	assertRecoveryReason(t, err, CodexRecoveryReasonApplyFailed)
	var eventCount, runCount, allocationCount, revisionCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM recovery_runs").Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM recovery_allocations").Scan(&allocationCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM source_allocation_revisions").Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 || runCount != 0 || allocationCount != 0 || revisionCount != 0 {
		t.Fatalf("failed Apply left event/run/allocation/revision state %d/%d/%d/%d", eventCount, runCount, allocationCount, revisionCount)
	}
	if _, err := store.db.Exec("DROP TRIGGER fail_recovery_allocation"); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken)
	if err != nil || receipt.InsertedEventCount != 1 || receipt.OverriddenEventCount != 1 {
		t.Fatalf("retry after rolled back Apply = %+v, err=%v", receipt, err)
	}
}

func TestImporterAppendRejectsStaleAllocationRevision(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	var middleKey, finalKey string
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset LIMIT 1 OFFSET 1").Scan(&middleKey); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset DESC LIMIT 1").Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM usage_events WHERE ingestion_key=?", middleKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET input_tokens=50, output_tokens=5 WHERE ingestion_key=?", finalKey); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PrepareCodexRecovery(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var cursorID string
	if err := store.db.QueryRow("SELECT source_id FROM source_cursors LIMIT 1").Scan(&cursorID); err != nil {
		t.Fatal(err)
	}
	staleExpectation, err := currentSourceCursorExpectation(store, cursorID)
	if err != nil {
		t.Fatal(err)
	}
	if staleExpectation.allocationRevision != 0 {
		t.Fatalf("pre-recovery allocation revision = %d", staleExpectation.allocationRevision)
	}
	if _, err := store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken); err != nil {
		t.Fatal(err)
	}
	current, err := store.Cursor(cursorID)
	if err != nil {
		t.Fatal(err)
	}
	// Model a parser that captured revision zero, then refreshed only its
	// cursor expectation. The allocation-revision fence must still reject it.
	staleExpectation.cursor = current
	staleExpectation.exists = true
	_, err = store.appendWithReferencesAndCursorIfCurrent(nil, nil, nil, current, staleExpectation)
	if !errors.Is(err, errSourceCursorChanged) {
		t.Fatalf("stale allocation revision append error = %v", err)
	}
}

func TestApplyCodexRecoveryRollsBackAtEachTransactionalWriteBoundary(t *testing.T) {
	tests := []struct {
		name, table, event string
	}{
		{"receipt", "recovery_runs", "INSERT"},
		{"ordinary_event", "usage_events", "INSERT"},
		{"allocation_journal", "recovery_allocations", "INSERT"},
		{"allocation_head", "current_event_allocations", "INSERT"},
		{"source_revision", "source_allocation_revisions", "INSERT"},
		{"cursor", "source_cursors", "UPDATE"},
		{"checkpoint", "parser_checkpoints", "DELETE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (CodexAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			var middleKey, finalKey, sourceID string
			if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset LIMIT 1 OFFSET 1").Scan(&middleKey); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset DESC LIMIT 1").Scan(&finalKey); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow("SELECT source_id FROM source_cursors LIMIT 1").Scan(&sourceID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec("DELETE FROM usage_events WHERE ingestion_key=?", middleKey); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec("UPDATE usage_events SET input_tokens=50,output_tokens=5 WHERE ingestion_key=?", finalKey); err != nil {
				t.Fatal(err)
			}
			preview, err := store.PrepareCodexRecovery(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.table == "parser_checkpoints" {
				if _, err := store.db.Exec(`INSERT OR IGNORE INTO parser_checkpoints(source_id,adapter,parser_revision,format_version,source_generation,
					boundary_offset,prefix_fingerprint,state_json,state_digest,updated_at) VALUES(?,'codex',1,1,0,0,'x',x'', 'x','2026-10-11T00:00:00Z')`, sourceID); err != nil {
					t.Fatal(err)
				}
			}
			trigger := "fail_apply_" + tc.name
			query := "CREATE TRIGGER " + trigger + " BEFORE " + tc.event + " ON " + tc.table + " BEGIN SELECT RAISE(ABORT,'synthetic'); END"
			if _, err := store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			_, err = store.ApplyCodexRecovery(context.Background(), path, preview.PlanToken)
			assertRecoveryReason(t, err, CodexRecoveryReasonApplyFailed)
			var events, runs, journal, heads, revisions int
			for query, destination := range map[string]*int{
				"SELECT COUNT(*) FROM usage_events":                &events,
				"SELECT COUNT(*) FROM recovery_runs":               &runs,
				"SELECT COUNT(*) FROM recovery_allocations":        &journal,
				"SELECT COUNT(*) FROM current_event_allocations":   &heads,
				"SELECT COUNT(*) FROM source_allocation_revisions": &revisions,
			} {
				if err := store.db.QueryRow(query).Scan(destination); err != nil {
					t.Fatal(err)
				}
			}
			if events != 2 || runs != 0 || journal != 0 || heads != 0 || revisions != 0 {
				t.Fatalf("failed Apply changed events/runs/journal/heads/revisions: %d/%d/%d/%d/%d", events, runs, journal, heads, revisions)
			}
		})
	}
}

func TestPrepareCodexRecoveryRefusesIncompleteAndChangedGeneration(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	data := legacyRecoveryFixture("session", "project")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte(nil), data...), []byte(`{"type":"event_msg"`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonIncompleteSnapshot)
	changed := append([]byte(nil), data...)
	changed = []byte(strings.Replace(string(changed), `"input_tokens":100`, `"input_tokens":101`, 1))
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonGenerationChanged)
}

func TestPrepareCodexRecoveryRefusesNoChangesAndActiveCollector(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	_, err := store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonNoChanges)
	if _, err := store.db.Exec("UPDATE collector_runtime SET state='running' WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	_, err = store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonCollectorActive)
}

func TestPrepareCodexRecoveryRefusesMonetaryReconciliation(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	var middleKey, finalKey string
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset LIMIT 1 OFFSET 1").Scan(&middleKey); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT ingestion_key FROM usage_events ORDER BY source_offset DESC LIMIT 1").Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM usage_events WHERE ingestion_key=?", middleKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET input_tokens=50,output_tokens=5,cost_status='calculated',cost_amount_micros=1,cost_currency='USD' WHERE ingestion_key=?", finalKey); err != nil {
		t.Fatal(err)
	}
	_, err := store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonMonetaryReconciliation)
}

func TestPrepareCodexRecoveryRejectsOversizedAndOversizedLineBeforeReading(t *testing.T) {
	for _, name := range []string{"oversized_file", "oversized_line"} {
		t.Run(name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (CodexAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(path, os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if name == "oversized_file" {
				if _, err := file.WriteAt([]byte("{}\n"), 0); err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(CodexRecoveryMaxSnapshotBytes + 1); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := file.Truncate(0); err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write([]byte(strings.Repeat("x", 128))); err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(CodexRecoveryMaxSnapshotBytes + 1); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			var bytesRead int64
			store.sourceReadHook = func(pass int, count int64) {
				if pass == 1 && count > bytesRead {
					bytesRead = count
				}
			}
			beforeWrites := recoveryTotalChanges(t, store)
			_, err = store.PrepareCodexRecovery(context.Background(), path)
			assertRecoveryReason(t, err, CodexRecoveryReasonLimitExceeded)
			if bytesRead != 0 {
				t.Fatalf("oversized input was read before refusal: %d bytes", bytesRead)
			}
			if got := recoveryTotalChanges(t, store); got != beforeWrites {
				t.Fatalf("Prepare wrote to Store: total_changes %d -> %d", beforeWrites, got)
			}
		})
	}
}

func TestPrepareCodexRecoveryStopsAtHardByteBudgetWhenSourceGrows(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, legacyRecoveryFixture("session", "project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	var maximumRead int64
	grew := false
	store.sourceReadHook = func(pass int, count int64) {
		if pass != 1 {
			return
		}
		if count > maximumRead {
			maximumRead = count
		}
		if !grew && count > 0 {
			grew = true
			file, err := os.OpenFile(path, os.O_RDWR, 0o600)
			if err != nil {
				t.Errorf("open growing source: %v", err)
				return
			}
			if err := file.Truncate(CodexRecoveryMaxSnapshotBytes + 1024); err != nil {
				t.Errorf("grow source: %v", err)
			}
			_ = file.Close()
		}
	}
	beforeWrites := recoveryTotalChanges(t, store)
	_, err := store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonLimitExceeded)
	if maximumRead > CodexRecoveryMaxSnapshotBytes+1 || maximumRead <= CodexRecoveryMaxSnapshotBytes {
		t.Fatalf("read pass exceeded hard byte budget: %d bytes, limit %d", maximumRead, CodexRecoveryMaxSnapshotBytes)
	}
	if got := recoveryTotalChanges(t, store); got != beforeWrites {
		t.Fatalf("Prepare wrote to Store: total_changes %d -> %d", beforeWrites, got)
	}
}

func TestPrepareCodexRecoveryStopsCountingAfterRecordBudget(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	base := legacyRecoveryFixture("session", "project")
	if err := os.WriteFile(path, base, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	data := append(append([]byte(nil), base...), bytes.Repeat([]byte{'\n'}, CodexRecoveryMaxRecords*3)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var countedBytes int64
	store.sourceReadHook = func(pass int, count int64) {
		if pass == 2 && count > countedBytes {
			countedBytes = count
		}
	}
	_, err := store.PrepareCodexRecovery(context.Background(), path)
	assertRecoveryReason(t, err, CodexRecoveryReasonLimitExceeded)
	if countedBytes <= CodexRecoveryMaxRecords || countedBytes >= int64(len(data)) {
		t.Fatalf("record count pass was not bounded: read %d of %d bytes for %d records", countedBytes, len(data), CodexRecoveryMaxRecords)
	}
}

func recoveryTotalChanges(t *testing.T, store *Store) int64 {
	t.Helper()
	var changes int64
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

func legacyRecoveryFixture(session, project string) []byte {
	meta := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"` + session + `","project_id":"` + project + `","model":"gpt-test"}}` + "\n"
	record := func(id, timestamp string, input, output int, reasoningField string) string {
		return `{"type":"event_msg","id":"` + id + `","timestamp":"` + timestamp + `","payload":{"type":"token_count","session_id":"` + session + `","info":{"total_token_usage":{"input_tokens":` + decimal(input) + `,"output_tokens":` + decimal(output) + `},"last_usage_includes_reasoning":` + reasoningField + `}}}` + "\n"
	}
	return []byte(meta + record("A", "2026-10-11T00:00:01Z", 100, 10, "null") +
		record("B", "2026-10-11T00:00:02Z", 50, 5, "true") +
		record("C", "2026-10-11T00:00:03Z", 150, 15, "null"))
}

func assertRecoveryReason(t *testing.T, err error, reason string) {
	t.Helper()
	var recoveryErr *CodexRecoveryError
	if !errors.As(err, &recoveryErr) || recoveryErr.Reason != reason {
		t.Fatalf("recovery error=%v; want reason %q", err, reason)
	}
}
