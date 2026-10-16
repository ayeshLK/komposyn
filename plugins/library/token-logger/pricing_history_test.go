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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchemaTwelveClaudeHistoryKeepsExistingEventsAndCursors(t *testing.T) {
	for _, reusable := range []bool{true, false} {
		name := "non-reusable snapshot reparses as duplicate"
		if reusable {
			name = "reusable snapshot skips parsing"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			databasePath := filepath.Join(dir, "token-logger.db")
			sourcePath := filepath.Join(dir, "claude.jsonl")
			oldLine := historyClaudeRecord("legacy-response", "2026-10-16T12:00:00Z")
			originalBytes := []byte(oldLine + "\n")
			if err := os.WriteFile(sourcePath, originalBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			absoluteSource, err := filepath.Abs(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			sourceID := "claude-file-" + digest([]byte(absoluteSource))
			fingerprint := digest(originalBytes)
			db, err := sql.Open("sqlite", databasePath)
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			if err := createHistoricalSchemaEleven(db); err != nil {
				db.Close()
				t.Fatalf("create historical schema 11: %v", err)
			}
			historical := &Store{db: db}
			if _, err := db.Exec(`INSERT INTO pricing_rules(provider_name, model_selector, token_category, rate_micros, currency,
				effective_from, pricing_version, rate_numerator_micros, rate_denominator_tokens)
				VALUES ('history-custom', 'custom-model', 'input', 9, 'USD', '2026-01-01T00:00:00.000000000Z', 'legacy-custom-v1', 9, 1)`); err != nil {
				db.Close()
				t.Fatal(err)
			}
			diagnostic, err := historical.RecordDiagnostic("legacy sanitized diagnostic")
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			occurredAt := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			old := claudeEvent(sourceID, fingerprint, 0, "legacy-response",
				claudeSession{ID: "stable-session", ProjectID: "stable-project", ProjectName: "Legacy Claude Project", Model: "claude-opus-5-5"},
				claudeUsage{InputTokens: 1_000_000, CacheCreationInputTokens: 100_000, CacheReadInputTokens: 200_000, OutputTokens: 300_000},
				occurredAt, []byte(oldLine))
			old.BillingContext = BillingContext{}
			old.Usage.CacheWrite5mTokens, old.Usage.CacheWrite1hTokens = 0, 0
			old.ProjectID, old.SessionID = "stable-project", "stable-session"
			cursor := Cursor{SourceID: sourceID, FileFingerprint: fingerprint, ByteOffset: int64(len(originalBytes)),
				LastProviderRecordID: "legacy-response", SourceGeneration: 0, SnapshotReusable: reusable}
			projects := []Project{{ID: "stable-project", DisplayName: "Legacy Claude Project", SourceAdapter: "claude-code", ProviderAlias: "history-project"}}
			sessions := []Session{{ID: "stable-session", ProjectID: "stable-project", Client: Client{Name: "claude-code"},
				SourceAdapter: "claude-code", ProviderAlias: "history-session"}}
			if receipt, err := historical.AppendWithReferencesAndCursor([]UsageEvent{old}, projects, sessions, cursor); err != nil || receipt.Imported != 1 {
				db.Close()
				t.Fatalf("populate schema 11 event = %+v, err = %v", receipt, err)
			}
			if err := db.QueryRow("SELECT project_id, session_id FROM usage_events WHERE event_id = ?", old.EventID).Scan(&old.ProjectID, &old.SessionID); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO collector_source_identities(canonical_key, adapter, source_id, created_at)
				VALUES (?, 'claude', ?, '2026-10-16T12:00:00.000000000Z')`, digest([]byte(absoluteSource)), sourceID); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := applyHistoricalMigrationTwelve(db); err != nil {
				db.Close()
				t.Fatalf("apply migration 12 to historical fixture: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			store, err := Open(databasePath)
			if err != nil {
				t.Fatalf("open schema 12 database: %v", err)
			}
			defer store.Close()
			var latest int
			if err := store.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&latest); err != nil || latest != 14 {
				t.Fatalf("upgraded schema version = %d, err = %v; want 14", latest, err)
			}
			if got, err := store.Diagnostic(diagnostic.Fingerprint); err != nil || got.Message != diagnostic.Message || got.OccurrenceCount != 1 {
				t.Fatalf("legacy diagnostic after upgrade = %+v, err = %v", got, err)
			}
			if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE provider_name = 'history-custom' AND pricing_version = 'legacy-custom-v1'").Scan(&latest); err != nil || latest != 1 {
				t.Fatalf("custom pricing rule count = %d, err = %v; want 1", latest, err)
			}
			var identitySourceID string
			if err := store.db.QueryRow("SELECT source_id FROM collector_source_identities WHERE canonical_key = ? AND adapter = 'claude'", digest([]byte(absoluteSource))).Scan(&identitySourceID); err != nil || identitySourceID != sourceID {
				t.Fatalf("historical source identity = %q, err = %v; want %q", identitySourceID, err, sourceID)
			}
			assertHistoricalClaudeEvent(t, store, old.EventID, old.IngestionKey, old.ProjectID, old.SessionID)
			at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
			oldOnly, err := store.Query(UsageFilter{From: at, To: at.AddDate(0, 0, 1)})
			if err != nil || oldOnly.Summary.RequestCount != 1 || oldOnly.Summary.UnknownCostRequests != 1 {
				t.Fatalf("old in-range event estimate = %+v, err = %v; want unknown for missing historical evidence", oldOnly.Summary, err)
			}
			var eventCount int
			if eventCount, err = store.EventCount(); err != nil || eventCount != 1 {
				t.Fatalf("event count after schema upgrade = %d, err = %v; want 1", eventCount, err)
			}
			if got, err := store.Cursor(sourceID); err != nil || got.ByteOffset != cursor.ByteOffset || got.FileFingerprint != fingerprint || got.SourceGeneration != 0 || got.LastProviderRecordID != "legacy-response" || got.SnapshotReusable != reusable {
				t.Fatalf("source cursor after schema upgrade = %+v, err = %v", got, err)
			}
			dataBefore, err := os.ReadFile(sourcePath)
			if err != nil || string(dataBefore) != string(originalBytes) {
				t.Fatalf("provider source changed during upgrade: err=%v", err)
			}
			first, err := (ClaudeAdapter{}).Import(sourcePath, store)
			if err != nil {
				t.Fatal(err)
			}
			if reusable {
				if first.Outcomes.UnchangedSnapshot != 1 || first.Receipt != (Receipt{}) {
					t.Fatalf("reusable source result = %+v; want unchanged snapshot", first)
				}
			} else if first.Receipt.Duplicates != 1 || first.Receipt.Imported != 0 {
				t.Fatalf("non-reusable source result = %+v; want one storage duplicate", first)
			}
			assertHistoricalClaudeEvent(t, store, old.EventID, old.IngestionKey, old.ProjectID, old.SessionID)
			if got, err := store.Cursor(sourceID); err != nil || got.SourceID != cursor.SourceID || got.SourceGeneration != 0 || got.ByteOffset != cursor.ByteOffset || got.FileFingerprint != fingerprint {
				t.Fatalf("cursor after unchanged/reparsed source = %+v, err = %v", got, err)
			}
			unchangedSource, err := os.ReadFile(sourcePath)
			if err != nil || string(unchangedSource) != string(originalBytes) {
				t.Fatalf("provider source changed during duplicate reparse: err=%v", err)
			}

			newLine := historyClaudeRecord("new-response", "2026-10-16T12:01:00Z")
			appended := append(append([]byte(nil), originalBytes...), []byte(newLine+"\n")...)
			if err := os.WriteFile(sourcePath, appended, 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (ClaudeAdapter{}).Import(sourcePath, store)
			if err != nil || result.Receipt.Imported != 1 || result.Receipt.Duplicates != 1 {
				t.Fatalf("append import = %+v, err = %v; want one new and one duplicate event", result, err)
			}
			appendedSource, err := os.ReadFile(sourcePath)
			if err != nil || string(appendedSource) != string(appended) {
				t.Fatalf("provider source changed during append import: err=%v", err)
			}
			if got, err := store.Cursor(sourceID); err != nil || got.SourceGeneration != 0 || got.ByteOffset != int64(len(appended)) || got.FileFingerprint != digest(appended) {
				t.Fatalf("cursor after append = %+v, err = %v; want same generation and appended snapshot", got, err)
			}
			if eventCount, err = store.EventCount(); err != nil || eventCount != 2 {
				t.Fatalf("event count after append = %d, err = %v; want 2", eventCount, err)
			}
			report, err := store.Query(UsageFilter{From: at, To: at.AddDate(0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			if report.Summary.RequestCount != 2 || report.Summary.UnknownCostRequests != 1 || report.Summary.CalculatedCostMicros != 10_660_000 || report.Summary.InputTokens != 2_600_000 {
				t.Fatalf("old plus new Claude report = %+v; want only new event priced", report.Summary)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			second, err := (ClaudeAdapter{}).Import(sourcePath, store)
			if err != nil || second.Outcomes.UnchangedSnapshot != 1 || second.Receipt != (Receipt{}) {
				t.Fatalf("reopened unchanged import = %+v, err = %v", second, err)
			}
			report, err = store.Query(UsageFilter{From: at, To: at.AddDate(0, 0, 1)})
			if err != nil || report.Summary.RequestCount != 2 || report.Summary.CalculatedCostMicros != 10_660_000 {
				t.Fatalf("report after reopen/reimport = %+v, err = %v", report.Summary, err)
			}
		})
	}
}

func TestExistingGPT53CodexEventBecomesPricedAfterCatalogSync(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	event := testEvent("historical-gpt53")
	event.EventID, event.OccurredAt = "historical-gpt53", at
	event.Provider, event.RequestedModel, event.ResolvedModel = "openai", "gpt-5.3-codex", "gpt-5.3-codex"
	event.Usage = TokenUsage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, ReasoningTokens: 100_000}
	event.BillingContext.ServiceTier = &BillingContextValue{Value: "default", Evidence: BillingEvidenceProviderResponse}
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM pricing_rules WHERE provider_name = 'openai' AND model_selector = 'gpt-5.3-codex'"); err != nil {
		t.Fatal(err)
	}
	before, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil || before.Summary.UnknownCostRequests != 1 {
		t.Fatalf("event without synchronized catalog = %+v, err = %v", before.Summary, err)
	}
	if err := store.syncBundledPricingCatalog(); err != nil {
		t.Fatal(err)
	}
	after, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil || after.Summary.CalculatedCostMicros != 15_925_000 || after.Summary.UnknownCostRequests != 0 {
		t.Fatalf("existing event after catalog sync = %+v, err = %v", after.Summary, err)
	}
	var eventID, ingestionKey, contextJSON string
	var inputTokens, cacheRead int64
	if err := store.db.QueryRow("SELECT event_id, ingestion_key, billing_context_json, input_tokens, cache_read_tokens FROM usage_events").Scan(&eventID, &ingestionKey, &contextJSON, &inputTokens, &cacheRead); err != nil {
		t.Fatal(err)
	}
	if eventID != event.EventID || ingestionKey != event.IngestionKey || inputTokens != 2_000_000 || cacheRead != 1_000_000 || !strings.Contains(contextJSON, "default") {
		t.Fatalf("catalog sync rewrote event metadata: %s/%s %d/%d %s", eventID, ingestionKey, inputTokens, cacheRead, contextJSON)
	}
	if err := store.syncBundledPricingCatalog(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	final, err := reopened.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil || final.Summary.CalculatedCostMicros != 15_925_000 || final.Summary.RequestCount != 1 {
		t.Fatalf("event after reopen/catalog resync = %+v, err = %v", final.Summary, err)
	}
	var afterID, afterKey string
	if err := reopened.db.QueryRow("SELECT event_id, ingestion_key FROM usage_events").Scan(&afterID, &afterKey); err != nil || afterID != event.EventID || afterKey != event.IngestionKey {
		t.Fatalf("event identity after reopen = %s/%s, err = %v", afterID, afterKey, err)
	}
}

func historyClaudeRecord(id, timestamp string) string {
	return `{"type":"assistant","uuid":"` + id + `","sessionId":"history-session","project_id":"history-project","project_name":"Historical Claude Project","timestamp":"` + timestamp + `","cwd":"/sanitized/project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":1000000,"cache_creation_input_tokens":100000,"cache_read_input_tokens":200000,"output_tokens":300000,"cache_creation":{"ephemeral_5m_input_tokens":60000,"ephemeral_1h_input_tokens":40000},"service_tier":"standard","inference_geo":"global","speed":"standard"}}}`
}

func createHistoricalSchemaEleven(db *sql.DB) error {
	if _, err := db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		return err
	}
	apply := func(version int, migrate func(*sql.Tx) error) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := migrate(tx); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", version, "2026-01-01T00:00:00.000000000Z"); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	migrations := []func(*sql.Tx) error{
		func(tx *sql.Tx) error { _, err := tx.Exec(schemaMigrationOne); return err },
		func(tx *sql.Tx) error { _, err := tx.Exec(schemaMigrationTwo); return err },
		normalizeStoredTimestamps,
		applySourceGenerationMigration,
		applyPricingRationalMigration,
		applyProjectIdentityMigration,
		func(tx *sql.Tx) error { _, err := tx.Exec(schemaMigrationSeven); return err },
		migrateReservedResourceIDs,
		func(tx *sql.Tx) error { _, err := tx.Exec(schemaMigrationNine); return err },
		func(tx *sql.Tx) error { _, err := tx.Exec(schemaMigrationTen); return err },
		func(tx *sql.Tx) error { _, err := tx.Exec(schemaMigrationEleven); return err },
	}
	for index, migration := range migrations {
		if err := apply(index+1, migration); err != nil {
			return err
		}
	}
	return nil
}

func applyHistoricalMigrationTwelve(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := applyBillingContextMigration(tx); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (12, ?)", "2026-01-01T00:00:00.000000000Z"); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func assertHistoricalClaudeEvent(t *testing.T, store *Store, eventID, ingestionKey, projectID, sessionID string) {
	t.Helper()
	var gotID, gotKey, gotProject, gotSession string
	var input, write5m, write1h int64
	var billing sql.NullString
	if err := store.db.QueryRow(`SELECT event_id, ingestion_key, project_id, session_id, input_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, billing_context_json FROM usage_events WHERE event_id = ?`, eventID).
		Scan(&gotID, &gotKey, &gotProject, &gotSession, &input, &write5m, &write1h, &billing); err != nil {
		t.Fatal(err)
	}
	var output, cacheRead, cacheWrite int64
	if err := store.db.QueryRow("SELECT output_tokens, cache_read_tokens, cache_write_tokens FROM usage_events WHERE event_id = ?", eventID).Scan(&output, &cacheRead, &cacheWrite); err != nil {
		t.Fatal(err)
	}
	if gotID != eventID || gotKey != ingestionKey || gotProject != projectID || gotSession != sessionID || input != 1_300_000 || output != 300_000 || cacheRead != 200_000 || cacheWrite != 100_000 || write5m != 0 || write1h != 0 || billing.Valid {
		t.Fatalf("historical event changed: id/key=%s/%s refs=%s/%s usage=%d/%d/%d/%d splits=%d/%d context=%v", gotID, gotKey, gotProject, gotSession, input, cacheRead, cacheWrite, output, write5m, write1h, billing)
	}
}
