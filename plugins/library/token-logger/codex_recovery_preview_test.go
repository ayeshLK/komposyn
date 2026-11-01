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
	if _, _, err := parseCodexRecoverySnapshot(context.Background(), path, store, snapshot, &parsedEvents); err != nil {
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
