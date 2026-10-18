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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCollectorPollsBothProviderShapesAndReportsHealth(t *testing.T) {
	store := openTestStore(t)
	collector, err := NewCollector(store, []string{
		filepath.Join("testdata", "codex", "normal.jsonl"),
		filepath.Join("testdata", "claude-code", "normal.jsonl"),
	}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.PollOnce()
	if err != nil || result.Receipt.Imported != 4 {
		t.Fatalf("poll result=%+v err=%v", result, err)
	}
	if health := collector.Health(); health.Status != "healthy" || health.Sources != 2 || health.Imported != 4 {
		t.Fatalf("unexpected health: %+v", health)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := collector.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if health := collector.Health(); health.Status != "healthy" || health.Lifecycle != "stopped" || health.Imported != 4 || health.Duplicates != 0 {
		t.Fatalf("collector did not stop: %+v", collector.Health())
	}
}

func TestGeneratedCollectorMatchesDirectImportOracle(t *testing.T) {
	for _, provider := range []struct {
		name     string
		importer func(string, *Store) (ImportResult, error)
	}{
		{name: "claude", importer: (ClaudeAdapter{}).Import},
		{name: "codex", importer: (CodexAdapter{}).Import},
	} {
		t.Run(provider.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "oracle.jsonl")
			contents := benchmarkGeneratedHistory(provider.name, "oracle-session", 3)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			directStore := openTestStore(t)
			collectorStore := openTestStore(t)
			direct, err := provider.importer(path, directStore)
			if err != nil {
				t.Fatal(err)
			}
			canonicalProvider, canonicalModel := "anthropic", "claude-sonnet-5"
			if provider.name == "codex" {
				canonicalProvider, canonicalModel = "openai", "gpt-5-codex"
			}
			wantEvents := [][]string{
				{"2026-10-11T00:00:01.000000000Z", canonicalProvider, canonicalModel, "1", "1"},
				{"2026-10-11T00:00:01.000000000Z", canonicalProvider, canonicalModel, "1", "1"},
				{"2026-10-11T00:00:01.000000000Z", canonicalProvider, canonicalModel, "1", "1"},
			}
			gotEvents := benchmarkOracleRows(t, directStore, "SELECT occurred_at, provider_name, requested_model, input_tokens, output_tokens FROM usage_events ORDER BY occurred_at, event_id")
			if !reflect.DeepEqual(gotEvents, wantEvents) {
				t.Fatalf("generated canonical event accounting=%v, want %v", gotEvents, wantEvents)
			}
			collector, err := NewCollector(collectorStore, []string{path}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			collected, err := collector.PollOnce()
			if err != nil {
				t.Fatal(err)
			}
			if direct.Receipt != (Receipt{Imported: 3}) || direct.Receipt != collected.Receipt || direct.Outcomes != collected.Outcomes {
				t.Fatalf("direct=%+v; collector=%+v; want same three-event import", direct, collected)
			}
			for _, query := range []string{
				"SELECT * FROM usage_events ORDER BY event_id",
				"SELECT id, root, display_name FROM projects ORDER BY id",
				"SELECT * FROM sessions ORDER BY id",
				"SELECT scope, alias, project_id, provenance FROM project_aliases ORDER BY scope, alias",
				"SELECT scope, alias, session_id, project_id, provenance FROM session_aliases ORDER BY scope, alias",
			} {
				if left, right := benchmarkOracleRows(t, directStore, query), benchmarkOracleRows(t, collectorStore, query); !reflect.DeepEqual(left, right) {
					t.Errorf("direct and collector differ for %s:\n%v\n%v", query, left, right)
				}
			}
			canonicalPath, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			adapter := provider.name
			if adapter == "claude" {
				adapter = "claude-file"
			} else {
				adapter = "codex-file"
			}
			directCursor, directErr := directStore.Cursor(adapter + "-" + digest([]byte(canonicalPath)))
			collectorCursor, collectorErr := collectorStore.Cursor(adapter + "-" + digest([]byte(canonicalPath)))
			if directErr != nil || collectorErr != nil || directCursor != collectorCursor || directCursor.FileFingerprint != digest(contents) || directCursor.SourceGeneration != 0 || directCursor.ByteOffset != int64(len(contents)) {
				t.Fatalf("direct cursor=%+v err=%v; collector cursor=%+v err=%v", directCursor, directErr, collectorCursor, collectorErr)
			}
			health := collector.Health()
			if health.Status != "healthy" || health.Sources != 1 || health.Imported != 3 || health.Pending {
				t.Fatalf("collector health=%+v", health)
			}
			if diagnostics, err := collectorStore.PendingDiagnostics(); err != nil || len(diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics=%+v err=%v", diagnostics, err)
			}

			filter := UsageFilter{From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
			directReport, err := directStore.Query(filter)
			if err != nil {
				t.Fatal(err)
			}
			collectorReport, err := collectorStore.Query(filter)
			if err != nil {
				t.Fatal(err)
			}
			var directJSON, collectorJSON, directCSV, collectorCSV bytes.Buffer
			if err := WriteJSON(&directJSON, directReport); err != nil {
				t.Fatal(err)
			}
			if err := WriteJSON(&collectorJSON, collectorReport); err != nil {
				t.Fatal(err)
			}
			if err := WriteCSV(&directCSV, directReport); err != nil {
				t.Fatal(err)
			}
			if err := WriteCSV(&collectorCSV, collectorReport); err != nil {
				t.Fatal(err)
			}
			if directJSON.String() != collectorJSON.String() || directCSV.String() != collectorCSV.String() {
				t.Fatalf("direct and collector machine reports differ:\nJSON %s / %s\nCSV %s / %s", directJSON.String(), collectorJSON.String(), directCSV.String(), collectorCSV.String())
			}
			unknownReason := "no_matching_rate"
			if provider.name == "codex" {
				unknownReason = "outside_effective_range"
			}
			wantJSON := fmt.Sprintf(`{
  "from": "2026-10-11T00:00:00Z",
  "to": "2026-10-12T00:00:00Z",
  "timezone": "UTC",
  "filters": {
    "from": "2026-10-11T00:00:00Z",
    "to": "2026-10-12T00:00:00Z"
  },
  "summary": {
    "request_count": 3,
    "session_count": 1,
    "input_tokens": 3,
    "cached_input_tokens": 0,
    "cache_write_input_tokens": 0,
    "output_tokens": 3,
    "reasoning_output_tokens": 0,
    "total_tokens": 6,
    "calculated_cost_micros": 0,
    "provider_reported_cost_micros": 0,
    "unknown_cost_requests": 3,
    "estimated_cost_requests": 0,
    "known_zero_cost_requests": 0,
    "cost_unknown_reason": %q,
    "cost_unknown_reasons": {
      %q: 3
    },
    "pricing_provider": %q,
    "calculation_basis": "unavailable"
  },
  "rows": []
}
`, unknownReason, unknownReason, canonicalProvider)
			if directJSON.String() != wantJSON {
				t.Fatalf("generated JSON differs from frozen accounting/provenance/shape:\n%s\nwant:\n%s", directJSON.String(), wantJSON)
			}
			wantCSV := fmt.Sprintf(`group,bucket,request_count,session_count,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_output_tokens,total_tokens,calculated_cost_micros,calculated_currency,provider_reported_cost_micros,unknown_cost_requests,estimated_cost_requests,known_zero_cost_requests,unknown_cost_reason,unknown_cost_reasons,pricing_provider,calculation_basis,pricing_version,pricing_source,pricing_model_selector,pricing_effective_from,pricing_effective_to,provider_reported_currency
,,3,1,3,0,0,3,0,6,0,,0,3,0,0,%s,"{""%s"":3}",%s,unavailable,,,,,,
`, unknownReason, unknownReason, canonicalProvider)
			if directCSV.String() != wantCSV {
				t.Fatalf("generated CSV differs from frozen accounting/provenance/shape:\n%s\nwant:\n%s", directCSV.String(), wantCSV)
			}

			directRepeat, err := provider.importer(path, directStore)
			if err != nil {
				t.Fatal(err)
			}
			collectorRepeat, err := collector.PollOnce()
			if err != nil {
				t.Fatal(err)
			}
			if directRepeat.Receipt != (Receipt{}) || collectorRepeat.Receipt != directRepeat.Receipt || directRepeat.Outcomes != collectorRepeat.Outcomes || directRepeat.Outcomes.UnchangedSnapshot != 1 {
				t.Fatalf("direct unchanged=%+v; collector unchanged=%+v", directRepeat, collectorRepeat)
			}
		})
	}
}

func TestUnchangedCollectorCountsBothAdapterDetectionPasses(t *testing.T) {
	for _, provider := range []struct {
		name        string
		wantRecords int64
	}{
		{name: "claude", wantRecords: 4}, // detection and authoritative detection each decode the first two lines
		{name: "codex", wantRecords: 2},  // session_meta is recognized on the first line in each pass
	} {
		t.Run(provider.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.jsonl")
			contents := benchmarkGeneratedHistory(provider.name, "observer-session", 3)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			store := openTestStore(t)
			collector, err := NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.PollOnce(); err != nil {
				t.Fatal(err)
			}
			observer := &collectorBenchmarkObserver{}
			store.benchmarkObserver = observer
			if _, err := collector.PollOnce(); err != nil {
				t.Fatal(err)
			}
			if observer.recordsDecoded != provider.wantRecords {
				t.Fatalf("decoded records=%d, want %d across both adapter detection passes", observer.recordsDecoded, provider.wantRecords)
			}
		})
	}
}

func benchmarkOracleRows(t *testing.T, store *Store, query string) [][]string {
	t.Helper()
	rows, err := store.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := make([][]string, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			if value == nil {
				row[index] = "<null>"
			} else if bytes, ok := value.([]byte); ok {
				row[index] = string(bytes)
			} else {
				row[index] = fmt.Sprint(value)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCollectorReportsHealthForEachConfiguredRoot(t *testing.T) {
	store := openTestStore(t)
	healthyRoot := t.TempDir()
	degradedRoot := t.TempDir()
	claudeFixture, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(healthyRoot, "normal.jsonl"), claudeFixture, 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := append(append([]byte(nil), claudeFixture...), []byte("{not-json}\n")...)
	if err := os.WriteFile(filepath.Join(degradedRoot, "malformed.jsonl"), malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{healthyRoot, degradedRoot}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.PollOnce()
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcomes.MalformedRecord != 1 {
		t.Fatalf("poll outcomes=%+v", result.Outcomes)
	}
	diagnostics, err := store.PendingDiagnostics()
	if err != nil || len(diagnostics) != 1 {
		t.Fatalf("collector diagnostics = %+v, err=%v; want one diagnostic", diagnostics, err)
	}
	if diagnostics[0].Category != DiagnosticCategoryIngestion || diagnostics[0].CoreVersion != CoreVersion() || diagnostics[0].SchemaVersion != SchemaVersion() {
		t.Fatalf("collector diagnostic metadata = %+v", diagnostics[0])
	}
	health := collector.Health()
	if root := health.Roots[healthyRoot]; root.Status != "healthy" || root.LastError != "" || root.Sources != 1 {
		t.Fatalf("healthy root=%+v", root)
	}
	if root := health.Roots[degradedRoot]; root.Status != "degraded" || root.Outcomes.MalformedRecord != 1 || !strings.Contains(root.LastError, "malformed_record=1") {
		t.Fatalf("degraded root=%+v", root)
	}
}

func TestCollectorImportsClaudeFallbackCreditWithoutMaskingOtherFailures(t *testing.T) {
	store := openTestStore(t)
	healthyRoot := t.TempDir()
	affectedRoot := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(healthyRoot, "normal.jsonl"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	affected := append(
		claudeFallbackCreditRecord("fallback-valid", "claude-sonnet-5", `,"fallback_credit":{"status":{"type":"redeemed"}}`, ""),
		claudeFallbackCreditRecord("future-unknown", "claude-sonnet-5", `,"fallback_credit":null`, `,"future_token_units":1`)...,
	)
	if err := os.WriteFile(filepath.Join(affectedRoot, "affected.jsonl"), affected, 0o600); err != nil {
		t.Fatal(err)
	}

	collector, err := NewCollector(store, []string{healthyRoot, affectedRoot}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.PollOnce()
	if err != nil || result.Receipt.Imported != 3 || result.Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("poll result=%+v err=%v", result, err)
	}
	health := collector.Health()
	if root := health.Roots[healthyRoot]; root.Status != "healthy" || root.Sources != 1 || root.Outcomes.IgnoredNonUsage != 2 || root.Outcomes.Incomplete() {
		t.Fatalf("healthy root=%+v", root)
	}
	if root := health.Roots[affectedRoot]; root.Status != "degraded" || root.Sources != 1 || root.Outcomes.UnsupportedUsage != 1 || !strings.Contains(root.LastError, "unsupported_usage=1") {
		t.Fatalf("affected root=%+v", root)
	}
	var affectedEvents int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events WHERE resolved_model = 'claude-sonnet-5'`).Scan(&affectedEvents); err != nil {
		t.Fatal(err)
	}
	if affectedEvents != 1 {
		t.Fatalf("affected events = %d, want 1", affectedEvents)
	}
}

func TestCollectorDetectsAdapterFromAuthoritativeSnapshot(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "growing.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"summary","summary":"preview without usage"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	appended := false
	store.beforeSnapshotRead = func() {
		if appended {
			return
		}
		appended = true
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if openErr != nil {
			t.Fatalf("open growing source: %v", openErr)
		}
		if _, writeErr := file.Write(fixture); writeErr != nil {
			file.Close()
			t.Fatalf("append Claude usage: %v", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			t.Fatalf("close growing source: %v", closeErr)
		}
	}
	result, err := collector.PollOnce()
	if err != nil || result.Receipt.Imported != 2 || result.Outcomes.Incomplete() {
		t.Fatalf("authoritative poll result=%+v err=%v", result, err)
	}
	if count, err := store.EventCount(); err != nil || count != 2 {
		t.Fatalf("event count=%d err=%v; want 2", count, err)
	}
	var claudeCursors, codexCursors int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM source_cursors WHERE source_id LIKE 'claude-file-%'`).Scan(&claudeCursors); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM source_cursors WHERE source_id LIKE 'codex-file-%'`).Scan(&codexCursors); err != nil {
		t.Fatal(err)
	}
	if claudeCursors != 1 || codexCursors != 0 {
		t.Fatalf("source cursors: claude=%d codex=%d; want 1, 0", claudeCursors, codexCursors)
	}
	if health := collector.Health(); health.Status != "healthy" || health.Imported != 2 {
		t.Fatalf("collector health=%+v", health)
	}
}

func TestCollectorLargeRecordsUseOneUnlimitedSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		largeLine  string
		importFile func(string, *Store) (ImportResult, error)
	}{
		{"claude", filepath.Join("testdata", "claude-code", "normal.jsonl"), `{"type":"user","message":{"content":"` + strings.Repeat("x", 70_000) + `"}}`, (ClaudeAdapter{}).Import},
		{"codex", filepath.Join("testdata", "codex", "normal.jsonl"), `{"type":"response_item","payload":{"type":"message","content":"` + strings.Repeat("x", 70_000) + `"}}`, (CodexAdapter{}).Import},
	}
	for _, test := range tests {
		for _, position := range []string{"before-detection", "after-detection"} {
			t.Run(test.name+"/"+position, func(t *testing.T) {
				fixture, err := os.ReadFile(test.fixture)
				if err != nil {
					t.Fatal(err)
				}
				contents := []byte(test.largeLine + "\n" + string(fixture))
				if position == "after-detection" {
					firstBreak := strings.IndexByte(string(fixture), '\n') + 1
					contents = append(append(append([]byte{}, fixture[:firstBreak]...), []byte(test.largeLine+"\n")...), fixture[firstBreak:]...)
				}
				contents = append(contents, []byte(`{"type":`)...)
				path := filepath.Join(t.TempDir(), test.name+".jsonl")
				if err := os.WriteFile(path, contents, 0o600); err != nil {
					t.Fatal(err)
				}
				directStore := openTestStore(t)
				direct, err := test.importFile(path, directStore)
				if err != nil || direct.Receipt.Imported != 2 || direct.Outcomes.DeferredIncompleteTail != 1 {
					t.Fatalf("direct result=%+v err=%v", direct, err)
				}
				collectorStore := openTestStore(t)
				collector, err := NewCollector(collectorStore, []string{path}, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				first, err := collector.PollOnce()
				if err != nil || first.Receipt.Imported != direct.Receipt.Imported || first.Outcomes.DeferredIncompleteTail != 1 {
					t.Fatalf("collector first=%+v err=%v direct=%+v", first, err, direct)
				}
				second, err := collector.PollOnce()
				if err != nil || second.Receipt != (Receipt{}) || second.Outcomes.UnchangedSnapshot != 1 || second.Outcomes.DeferredIncompleteTail != 1 {
					t.Fatalf("collector retry=%+v err=%v", second, err)
				}
			})
		}
	}
}

func TestCollectorIsolatesDiscoveryFailuresAndRecovers(t *testing.T) {
	fixture := filepath.Join("testdata", "claude-code", "normal.jsonl")
	badParent := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(badParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(badParent, "child")
	for _, roots := range [][]string{{badRoot, fixture}, {fixture, badRoot}} {
		store := openTestStore(t)
		collector, err := NewCollector(store, roots, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result, pollErr := collector.PollOnce()
		if pollErr == nil || result.Receipt.Imported != 2 || result.Outcomes.FailedSource != 1 {
			t.Fatalf("isolated discovery result=%+v err=%v roots=%v", result, pollErr, roots)
		}
		if health := collector.Health(); health.Status != "degraded" || health.Sources != 1 || health.DiscoveryFailures != 1 || health.Outcomes.FailedSource != 1 {
			t.Fatalf("degraded health=%+v", health)
		}
	}

	store := openTestStore(t)
	recoveryRoot := filepath.Join(t.TempDir(), "later.jsonl")
	collector, err := NewCollector(store, []string{fixture, recoveryRoot}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, firstErr := collector.PollOnce()
	if firstErr == nil || first.Receipt.Imported != 2 || first.Outcomes.FailedSource != 1 {
		t.Fatalf("pre-recovery result=%+v err=%v", first, firstErr)
	}
	contents, err := os.ReadFile(filepath.Join("testdata", "codex", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryRoot, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	second, secondErr := collector.PollOnce()
	if secondErr != nil || second.Receipt.Imported != 2 || second.Outcomes.FailedSource != 0 {
		t.Fatalf("recovered result=%+v err=%v", second, secondErr)
	}
	if health := collector.Health(); health.Status != "healthy" || health.DiscoveryFailures != 0 || health.LastSuccessAt.IsZero() || health.Outcomes.FailedSource != 1 {
		t.Fatalf("recovered health=%+v", health)
	}
	if err := os.Remove(recoveryRoot); err != nil {
		t.Fatal(err)
	}
	third, thirdErr := collector.PollOnce()
	if thirdErr == nil || third.Outcomes.FailedSource != 1 || collector.Health().Status != "degraded" || collector.Health().Outcomes.FailedSource != 2 {
		t.Fatalf("post-recovery removal result=%+v err=%v health=%+v", third, thirdErr, collector.Health())
	}
}

func TestCollectorIsolatesUnreadableSubtree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not define Windows directory readability")
	}
	root := t.TempDir()
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "healthy.jsonl"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "hidden.jsonl"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })

	store := openTestStore(t)
	collector, err := NewCollector(store, []string{root}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr := collector.PollOnce()
	if pollErr == nil {
		t.Skip("host privileges bypass unreadable-directory permission bits")
	}
	if result.Receipt.Imported != 2 || result.Outcomes.FailedSource != 1 || collector.Health().Sources != 1 {
		t.Fatalf("unreadable subtree blocked healthy source: result=%+v err=%v health=%+v", result, pollErr, collector.Health())
	}
}

func TestCollectorRequiredOptionalAndDuplicateRoots(t *testing.T) {
	store := openTestStore(t)
	missing := filepath.Join(t.TempDir(), "missing")
	collector, err := NewCollector(store, []string{missing}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr := collector.PollOnce()
	if pollErr == nil || result.Outcomes.FailedSource != 1 || collector.Health().Status != "degraded" {
		t.Fatalf("required missing root result=%+v err=%v health=%+v", result, pollErr, collector.Health())
	}

	optional, err := NewCollectorWithRoots(store, []CollectorRoot{{Path: missing}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr = optional.PollOnce()
	if pollErr != nil || result.Outcomes.FailedSource != 0 || optional.Health().Status != "healthy" || optional.Health().Sources != 0 {
		t.Fatalf("optional missing root result=%+v err=%v health=%+v", result, pollErr, optional.Health())
	}

	empty, err := NewCollector(store, []string{t.TempDir()}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr = empty.PollOnce()
	if pollErr != nil || result != (ImportResult{}) || empty.Health().Status != "healthy" || empty.Health().Sources != 0 {
		t.Fatalf("empty required directory result=%+v err=%v health=%+v", result, pollErr, empty.Health())
	}

	root := t.TempDir()
	path := filepath.Join(root, "normal.jsonl")
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := []string{root, path, filepath.Join(root, ".", "normal.jsonl")}
	link := filepath.Join(t.TempDir(), "source-link.jsonl")
	if err := os.Symlink(path, link); err == nil {
		roots = append(roots, link)
	} else if runtime.GOOS != "windows" {
		t.Fatalf("create source symlink: %v", err)
	}
	deduplicated, err := NewCollector(store, roots, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr = deduplicated.PollOnce()
	if pollErr != nil || result.Receipt.Imported != 2 || deduplicated.Health().Sources != 1 {
		t.Fatalf("deduplicated roots result=%+v err=%v health=%+v", result, pollErr, deduplicated.Health())
	}
	if _, err := os.Lstat(link); err == nil {
		identityStore := openTestStore(t)
		identityCollector, err := NewCollector(identityStore, []string{link, path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := identityCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 || identityCollector.Health().Sources != 1 {
			t.Fatalf("symlink-first deduplication result=%+v err=%v health=%+v", result, err, identityCollector.Health())
		}
		absoluteLink, err := filepath.Abs(link)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := identityStore.Cursor("claude-file-" + digest([]byte(absoluteLink))); err != nil {
			t.Fatalf("configured symlink source identity was not preserved: %v", err)
		}
		reorderedCollector, err := NewCollector(identityStore, []string{path, link}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := reorderedCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
			t.Fatalf("reordered aliases changed source identity: result=%+v err=%v", result, err)
		}
		realOnlyCollector, err := NewCollector(identityStore, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := realOnlyCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
			t.Fatalf("removed alias changed established source identity: result=%+v err=%v", result, err)
		}
		canonicalPath, err := canonicalCollectorPath(path)
		if err != nil {
			t.Fatal(err)
		}
		var storedCanonicalKey, storedSourceID string
		if err := identityStore.db.QueryRow(`SELECT canonical_key, source_id FROM collector_source_identities
			WHERE adapter = 'claude'`).Scan(&storedCanonicalKey, &storedSourceID); err != nil {
			t.Fatal(err)
		}
		if storedCanonicalKey != digest([]byte(canonicalPath)) || storedSourceID != "claude-file-"+digest([]byte(absoluteLink)) {
			t.Fatalf("persisted source identity = %q/%q", storedCanonicalKey, storedSourceID)
		}

		directStore := openTestStore(t)
		directCollector, err := NewCollector(directStore, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := directCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 {
			t.Fatalf("direct source setup result=%+v err=%v", result, err)
		}
		aliasedCollector, err := NewCollector(directStore, []string{link, path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := aliasedCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
			t.Fatalf("new alias changed established source identity: result=%+v err=%v", result, err)
		}
		if count, err := directStore.EventCount(); err != nil || count != 2 {
			t.Fatalf("alias transition event count=%d err=%v", count, err)
		}

		directoryStore := openTestStore(t)
		directoryRoot := t.TempDir()
		directoryFile := filepath.Join(directoryRoot, "normal.jsonl")
		if err := os.WriteFile(directoryFile, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		directoryLink := filepath.Join(t.TempDir(), "source-directory")
		if err := os.Symlink(directoryRoot, directoryLink); err != nil {
			t.Fatalf("create directory symlink: %v", err)
		}
		if result, err := (ClaudeAdapter{}).Import(filepath.Join(directoryLink, "normal.jsonl"), directoryStore); err != nil || result.Receipt.Imported != 2 {
			t.Fatalf("directory alias import result=%+v err=%v", result, err)
		}
		directoryCollector, err := NewCollector(directoryStore, []string{directoryLink}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := directoryCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
			t.Fatalf("directory alias collection changed source identity: result=%+v err=%v", result, err)
		}
	}
}

func TestCollectorCancellationJoinsWithoutPartialPersistence(t *testing.T) {
	store := openTestStore(t)
	collector, err := NewCollector(store, []string{filepath.Join("testdata", "claude-code", "normal.jsonl")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	collector.beforeSource = func(string) {
		close(started)
		<-release
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.RunImmediate(ctx, nil) }()
	<-started
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if health := collector.Health(); health.Lifecycle != "stopped" || health.Status != "cancelled" || !health.Interrupted || health.Outcomes.FailedSource != 0 {
		t.Fatalf("cancelled collector health=%+v", health)
	}
	if count, err := store.EventCount(); err != nil || count != 0 {
		t.Fatalf("cancelled collector persisted events=%d err=%v", count, err)
	}

	preCancelled, err := NewCollector(store, []string{filepath.Join("testdata", "codex", "normal.jsonl")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	preCtx, preCancel := context.WithCancel(context.Background())
	preCancel()
	if err := preCancelled.RunImmediate(preCtx, nil); err != nil {
		t.Fatal(err)
	}
	if health := preCancelled.Health(); health.Lifecycle != "stopped" || !health.Interrupted || health.Outcomes.FailedSource != 0 {
		t.Fatalf("pre-cancelled collector health=%+v", health)
	}
}

func TestCollectorRetargetedAliasKeepsCanonicalSourcesDistinct(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.jsonl")
	secondPath := filepath.Join(root, "second.jsonl")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "current.jsonl")
	if err := os.Symlink(firstPath, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("source symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	store := openTestStore(t)
	firstCollector, err := NewCollector(store, []string{link, firstPath}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := firstCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 {
		t.Fatalf("first canonical source result=%+v err=%v", result, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secondPath, link); err != nil {
		t.Fatal(err)
	}
	retargetedCollector, err := NewCollector(store, []string{link, firstPath}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for poll := 0; poll < 3; poll++ {
		result, err := retargetedCollector.PollOnce()
		if err != nil {
			t.Fatalf("retargeted poll %d: %v", poll+1, err)
		}
		if poll == 0 && result.Receipt.Imported != 2 {
			t.Fatalf("retargeted first result=%+v", result)
		}
		if poll > 0 && (result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 2) {
			t.Fatalf("retargeted repeated result=%+v", result)
		}
		if count, err := store.EventCount(); err != nil || count != 4 {
			t.Fatalf("retargeted poll %d event count=%d err=%v", poll+1, count, err)
		}
	}
	var identities, distinctSourceIDs int
	if err := store.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT source_id)
		FROM collector_source_identities WHERE adapter = 'claude'`).Scan(&identities, &distinctSourceIDs); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || distinctSourceIDs != 2 {
		t.Fatalf("retargeted identities=%d distinct_source_ids=%d", identities, distinctSourceIDs)
	}
}

func TestExplicitImportsReuseCollectorSourceIdentity(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		importer func(string, *Store) (ImportResult, error)
	}{
		{"claude", filepath.Join("testdata", "claude-code", "normal.jsonl"), (ClaudeAdapter{}).Import},
		{"codex", filepath.Join("testdata", "codex", "normal.jsonl"), (CodexAdapter{}).Import},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents, err := os.ReadFile(test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "normal.jsonl")
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.Symlink(path, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skipf("source symlinks unavailable: %v", err)
				}
				t.Fatal(err)
			}
			store := openTestStore(t)
			collector, err := NewCollector(store, []string{link, path}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := collector.PollOnce(); err != nil || result.Receipt.Imported != 2 {
				t.Fatalf("collector setup result=%+v err=%v", result, err)
			}
			realOnly, err := NewCollector(store, []string{path}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := realOnly.PollOnce(); err != nil || result.Outcomes.UnchangedSnapshot != 1 {
				t.Fatalf("real-only collector result=%+v err=%v", result, err)
			}
			if result, err := test.importer(path, store); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
				t.Fatalf("explicit import changed identity: result=%+v err=%v", result, err)
			}
			if count, err := store.EventCount(); err != nil || count != 2 {
				t.Fatalf("explicit import event count=%d err=%v", count, err)
			}
		})
	}
}

func TestCollectorAllocatesIdentityWhenSymlinkBecomesRegularFile(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "original.jsonl")
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "current.jsonl")
	if err := os.Symlink(target, current); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("source symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	store := openTestStore(t)
	originalCollector, err := NewCollector(store, []string{current}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := originalCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 {
		t.Fatalf("symlink source result=%+v err=%v", result, err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	replacementCollector, err := NewCollector(store, []string{current}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := replacementCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 {
		t.Fatalf("regular replacement result=%+v err=%v", result, err)
	}
	if result, err := replacementCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("regular replacement retry result=%+v err=%v", result, err)
	}
	if result, err := (ClaudeAdapter{}).Import(current, store); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("regular replacement explicit import result=%+v err=%v", result, err)
	}
	if count, err := store.EventCount(); err != nil || count != 4 {
		t.Fatalf("regular replacement event count=%d err=%v", count, err)
	}
	var identities, distinctSourceIDs int
	if err := store.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT source_id)
		FROM collector_source_identities WHERE adapter = 'claude'`).Scan(&identities, &distinctSourceIDs); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || distinctSourceIDs != 2 {
		t.Fatalf("regular replacement identities=%d distinct_source_ids=%d", identities, distinctSourceIDs)
	}
}

func TestCollectorJoinsAfterPersistenceFailure(t *testing.T) {
	store := openTestStore(t)
	collector, err := NewCollector(store, []string{filepath.Join("testdata", "claude-code", "normal.jsonl")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- collector.RunImmediate(ctx, func(_ ImportResult, pollErr error) {
			observed <- pollErr
			cancel()
		})
	}()
	if pollErr := <-observed; pollErr == nil {
		t.Fatal("closed Store persistence failure was not reported")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if health := collector.Health(); health.Lifecycle != "stopped" || health.Status != "degraded" || health.Outcomes.FailedSource != 1 {
		t.Fatalf("persistence-failed collector health=%+v", health)
	}
}

func TestCollectorOutcomeHealthAndDiagnosticDeduplication(t *testing.T) {
	t.Run("warning degrades and deduplicates", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "unsupported.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"token_usage_record","input_tokens":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		collector, err := NewCollector(store, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			result, err := collector.PollOnce()
			if err != nil || result.Outcomes.UnsupportedUsage != 1 {
				t.Fatalf("poll result=%+v err=%v", result, err)
			}
		}
		health := collector.Health()
		if health.Status != "degraded" || health.Pending {
			t.Fatalf("unexpected warning health: %+v", health)
		}
		diagnostics, err := store.PendingDiagnostics()
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 1 || diagnostics[0].OccurrenceCount != 2 || diagnostics[0].Message != "adapter=codex reason=unsupported_usage" {
			t.Fatalf("unexpected diagnostics: %+v", diagnostics)
		}
	})

	t.Run("partial tail is visible but healthy", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "partial.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"token_count","payload":`), 0o600); err != nil {
			t.Fatal(err)
		}
		collector, err := NewCollector(store, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result, err := collector.PollOnce()
		if err != nil || result.Outcomes.DeferredIncompleteTail != 1 {
			t.Fatalf("poll result=%+v err=%v", result, err)
		}
		if health := collector.Health(); health.Status != "healthy" || !health.Pending {
			t.Fatalf("unexpected pending health: %+v", health)
		}
	})
}

func TestCollectorRunReportsSubsequentWarningPolls(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "changing.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"session","model":"gpt-test"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var results []ImportResult
	err = collector.RunWithResults(ctx, func(result ImportResult, pollErr error) {
		if pollErr != nil {
			t.Errorf("poll error: %v", pollErr)
			cancel()
			return
		}
		results = append(results, result)
		if len(results) == 1 {
			if err := os.WriteFile(path, []byte(`{"type":"token_usage_record","input_tokens":1}`+"\n"), 0o600); err != nil {
				t.Errorf("replace source: %v", err)
				cancel()
			}
		} else if result.Outcomes.UnsupportedUsage == 1 {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 || results[len(results)-1].Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("subsequent warning was not observed: %+v", results)
	}
	if health := collector.Health(); health.Outcomes.UnsupportedUsage == 0 {
		t.Fatalf("final health omitted warning outcome: %+v", health)
	}
}
