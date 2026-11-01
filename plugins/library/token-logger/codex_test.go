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
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCodexImportNormalizesCumulativeSnapshots(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join("testdata", "codex", "normal.jsonl")
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	if result.Receipt.Imported != 2 || result.Receipt.Duplicates != 0 ||
		result.Outcomes.IgnoredNonUsage != 3 || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	count, err := store.EventCount()
	if err != nil || count != 2 {
		t.Fatalf("event count = %d, err = %v", count, err)
	}
	rows, err := store.db.Query(`SELECT input_tokens, output_tokens, cache_read_tokens, reasoning_tokens, provider_name, client_name FROM usage_events ORDER BY occurred_at`)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	want := [][6]any{{int64(100), int64(30), int64(20), int64(10), "openai", "codex-cli"}, {int64(50), int64(20), int64(10), int64(5), "openai", "codex-cli"}}
	for index := range want {
		if !rows.Next() {
			t.Fatalf("missing event %d", index)
		}
		var input, output, cached, reasoning int64
		var provider, client string
		if err := rows.Scan(&input, &output, &cached, &reasoning, &provider, &client); err != nil {
			t.Fatalf("scan event %d: %v", index, err)
		}
		got := [6]any{input, output, cached, reasoning, provider, client}
		if got != want[index] {
			t.Fatalf("event %d = %v, want %v", index, got, want[index])
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra event")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cursor("codex-file-" + digest([]byte(canonicalPath))); err != nil {
		t.Fatalf("cursor was not committed: %v", err)
	}
}

func TestCodexLastUsageIncludesReasoningIsOptionalMetadata(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "absent"},
		{name: "true", value: `,"last_usage_includes_reasoning":true`},
		{name: "false", value: `,"last_usage_includes_reasoning":false`},
		{name: "null", value: `,"last_usage_includes_reasoning":null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "reasoning-metadata.jsonl")
			metadata := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"session-reasoning","project_id":"project-reasoning","model":"gpt-test"}}` + "\n"
			usageObject := func(input, cached, writes, output, reasoning int) string {
				return `{"input_tokens":` + decimal(input) + `,"cached_input_tokens":` + decimal(cached) +
					`,"cache_write_input_tokens":` + decimal(writes) + `,"output_tokens":` + decimal(output) +
					`,"reasoning_output_tokens":` + decimal(reasoning) + `,"total_tokens":` + decimal(input+output) + `}`
			}
			usage := func(timestamp string, input, cached, writes, output, reasoning, lastInput, lastCached, lastWrites, lastOutput, lastReasoning int) string {
				return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","session_id":"session-reasoning","info":{"total_token_usage":` + usageObject(input, cached, writes, output, reasoning) +
					`,"last_token_usage":` + usageObject(lastInput, lastCached, lastWrites, lastOutput, lastReasoning) + `,"model_context_window":258400` + test.value + `}}}` + "\n"
			}
			first := usage("2026-10-11T00:00:01Z", 100, 20, 5, 10, 4, 100, 20, 5, 10, 4)
			second := usage("2026-10-11T00:00:02Z", 150, 30, 6, 15, 6, 50, 10, 1, 5, 2)
			if err := os.WriteFile(path, []byte(metadata+first+second), 0o600); err != nil {
				t.Fatal(err)
			}

			result, err := (CodexAdapter{}).Import(path, store)
			if err != nil || result.Receipt != (Receipt{Imported: 2}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
				t.Fatalf("cumulative import = %+v, err = %v", result, err)
			}
			var count, input, output, cached, writes, reasoning int64
			readTotals := func() [6]int64 {
				t.Helper()
				if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_write_tokens), 0), COALESCE(SUM(reasoning_tokens), 0) FROM usage_events`).Scan(&count, &input, &output, &cached, &writes, &reasoning); err != nil {
					t.Fatal(err)
				}
				return [6]int64{count, input, output, cached, writes, reasoning}
			}
			want := [6]int64{2, 150, 15, 30, 6, 6}
			if totals := readTotals(); totals != want {
				t.Fatalf("exact cumulative totals = %v; want %v", totals, want)
			}

			unchanged, err := (CodexAdapter{}).Import(path, store)
			if err != nil || unchanged.Receipt != (Receipt{}) || unchanged.Outcomes != (OutcomeCounts{UnchangedSnapshot: 1}) {
				t.Fatalf("unchanged import = %+v, err = %v", unchanged, err)
			}
			third := usage("2026-10-11T00:00:03Z", 170, 35, 7, 17, 7, 20, 5, 1, 2, 1)
			if err := os.WriteFile(path, []byte(metadata+first+second+third), 0o600); err != nil {
				t.Fatal(err)
			}
			appended, err := (CodexAdapter{}).Import(path, store)
			if err != nil || appended.Receipt != (Receipt{Imported: 1, Duplicates: 2}) || appended.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
				t.Fatalf("appended import = %+v, err = %v", appended, err)
			}
			if totals := readTotals(); totals != [6]int64{3, 170, 17, 35, 7, 7} {
				t.Fatalf("exact appended totals = %v; want [3 170 17 35 7 7]", totals)
			}
		})
	}
}

func TestCodexGPT53UsageWithoutObservedTierRemainsUnknownInReport(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "gpt-5.3-codex.jsonl")
	contents := `{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"session-1","project_id":"project-1","model":"gpt-5.3-codex"}}
{"type":"token_usage_record","timestamp":"2026-10-16T12:00:01Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":100,"output_tokens":20,"reasoning_output_tokens":10}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("Codex import = %+v, err = %v", result, err)
	}
	var billingContext string
	if err := store.db.QueryRow("SELECT COALESCE(billing_context_json, '') FROM usage_events").Scan(&billingContext); err != nil {
		t.Fatal(err)
	}
	if billingContext != "" && billingContext != "{}" {
		t.Fatalf("adapter synthesized billing context %q without source evidence", billingContext)
	}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	report, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.UnknownCostRequests != 1 || report.Summary.CostUnknownReason != "billing_context_missing" || report.Summary.CalculatedCostMicros != 0 {
		t.Fatalf("tierless Codex report = %+v, want unknown billing context", report.Summary)
	}
}

func TestCodexImportIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join("testdata", "codex", "normal.jsonl")
	if _, err := (CodexAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("re-import fixture: %v", err)
	}
	if result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("unexpected re-import result: %+v", result)
	}
	if _, err := store.Cursor("missing"); err != sql.ErrNoRows {
		t.Fatalf("unexpected cursor error: %v", err)
	}
}

func TestCodexImportDetectsReplacementAndPreservesAppendIdempotency(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	session := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}` + "\n"
	token := func(input int) string {
		return `{"type":"token_count","timestamp":"2026-10-11T00:00:00Z","payload":{"session_id":"session-1","total_token_usage":{"input_tokens":` + string(rune('0'+input)) + `,"output_tokens":1}}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(session+token(1)), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (CodexAdapter{}).Import(path, store)
	if err != nil || first.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", first, err)
	}
	if err := os.WriteFile(path, []byte(session+token(1)+token(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	appendResult, err := (CodexAdapter{}).Import(path, store)
	if err != nil || appendResult.Receipt.Imported != 1 || appendResult.Receipt.Duplicates != 1 {
		t.Fatalf("append import = %+v, err = %v; want one new and one duplicate", appendResult, err)
	}
	if err := os.WriteFile(path, []byte(session+token(3)), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement, err := (CodexAdapter{}).Import(path, store)
	if err != nil || replacement.Receipt.Imported != 1 || replacement.Receipt.Duplicates != 0 {
		t.Fatalf("replacement import = %+v, err = %v; want one new event", replacement, err)
	}
	repeat, err := (CodexAdapter{}).Import(path, store)
	if err != nil || repeat.Receipt != (Receipt{}) || repeat.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("replacement re-import = %+v, err = %v; want unchanged snapshot", repeat, err)
	}
	if count, err := store.EventCount(); err != nil || count != 3 {
		t.Fatalf("event count = %d, err = %v; want 3", count, err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Cursor("codex-file-" + digest([]byte(canonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.SourceGeneration != 1 {
		t.Fatalf("source generation = %d, want 1", cursor.SourceGeneration)
	}
}

func TestCodexImportDetectsSameSizeSameMtimeReplacement(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}` + "\n"
	token := func(input int) string {
		return `{"type":"event_msg","id":"usage-1","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-1","info":{"total_token_usage":{"input_tokens":` + string(rune('0'+input)) + `,"output_tokens":1,"total_tokens":` + string(rune('1'+input)) + `}}}}` + "\n"
	}
	initial := []byte(metadata + token(1))
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (CodexAdapter{}).Import(path, store)
	if err != nil || first.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", first, err)
	}
	initialInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte(metadata + token(2))
	if len(replacement) != len(initial) {
		t.Fatalf("fixture replacement size=%d, initial size=%d", len(replacement), len(initial))
	}
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, initialInfo.ModTime(), initialInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if replacementInfo.Size() != initialInfo.Size() || !replacementInfo.ModTime().Equal(initialInfo.ModTime()) {
		t.Fatalf("replacement metadata size=%d mtime=%s; initial size=%d mtime=%s", replacementInfo.Size(), replacementInfo.ModTime(), initialInfo.Size(), initialInfo.ModTime())
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt != (Receipt{Imported: 1}) {
		t.Fatalf("same-size replacement = %+v, err = %v", result, err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Cursor("codex-file-" + digest([]byte(canonicalPath)))
	if err != nil || cursor.SourceGeneration != 1 || cursor.FileFingerprint != digest(replacement) {
		t.Fatalf("replacement cursor=%+v err=%v", cursor, err)
	}
	if count, err := store.EventCount(); err != nil || count != 2 {
		t.Fatalf("replacement event count=%d err=%v; want 2", count, err)
	}
}

func TestCodexImportUsesTokenUsageRecordsOnceAndIgnoresMirroredSnapshots(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "usage-records.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-1","info":{"total_token_usage":{"input_tokens":100,"output_tokens":10}}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":20,"cached_input_tokens":3,"cache_write_input_tokens":4,"output_tokens":2,"reasoning_output_tokens":1,"total_tokens":22},"turn_token_usage":{"input_tokens":20,"cached_input_tokens":3,"cache_write_input_tokens":4,"output_tokens":2,"reasoning_output_tokens":1},"thread_token_usage":{"input_tokens":120,"cached_input_tokens":3,"cache_write_input_tokens":4,"output_tokens":12,"reasoning_output_tokens":1}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-2","usage":{"input_tokens":30,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":33},"turn_token_usage":{"input_tokens":50,"cached_input_tokens":3,"cache_write_input_tokens":4,"output_tokens":5,"reasoning_output_tokens":2},"thread_token_usage":{"input_tokens":150,"cached_input_tokens":3,"cache_write_input_tokens":4,"output_tokens":15,"reasoning_output_tokens":2}}}
{"type":"event_msg","timestamp":"2026-10-11T00:00:03Z","payload":{"type":"token_count","session_id":"session-1","info":{"total_token_usage":{"input_tokens":150,"output_tokens":15}}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Receipt.Imported != 3 || result.Receipt.Duplicates != 0 || result.Outcomes.IgnoredNonUsage != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	var count, input, cached, writes, output, reasoning int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_write_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(reasoning_tokens), 0) FROM usage_events`).Scan(&count, &input, &cached, &writes, &output, &reasoning); err != nil {
		t.Fatal(err)
	}
	if [6]int64{count, input, cached, writes, output, reasoning} != [6]int64{3, 150, 3, 4, 15, 2} {
		t.Fatalf("normalized totals = %v", [6]int64{count, input, cached, writes, output, reasoning})
	}
	repeat, err := (CodexAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("repeat import: %v", err)
	}
	if repeat.Receipt != (Receipt{}) || repeat.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("repeat result: %+v", repeat)
	}
}

func TestCodexTokenUsageRecordIdentitySurvivesRewriteAndSeparatesSessions(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "identity.jsonl")
	header := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
`
	record := func(session, response string, input int) string {
		return `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","session_id":"` + session + `","response_id":"` + response + `","usage":{"input_tokens":` + decimal(input) + `,"output_tokens":1}}}
`
	}
	if err := os.WriteFile(path, []byte(header+record("session-1", "response-1", 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (CodexAdapter{}).Import(path, store)
	if err != nil || first.Receipt.Imported != 1 {
		t.Fatalf("first import = %+v, err = %v", first, err)
	}
	if err := os.WriteFile(path, []byte(header+record("session-1", "response-1", 99)), 0o600); err != nil {
		t.Fatal(err)
	}
	rewrite, err := (CodexAdapter{}).Import(path, store)
	if err != nil || rewrite.Receipt.Imported != 0 || rewrite.Receipt.Duplicates != 1 {
		t.Fatalf("rewrite import = %+v, err = %v", rewrite, err)
	}
	other := `{"type":"session_meta","payload":{"id":"session-2","project_id":"project-2","project_name":"Other","cwd":"/tmp/other","model":"gpt-test"}}
` + record("session-2", "response-1", 7)
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	separate, err := (CodexAdapter{}).Import(path, store)
	if err != nil || separate.Receipt.Imported != 1 {
		t.Fatalf("separate session import = %+v, err = %v", separate, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events e JOIN session_aliases a ON a.session_id = e.session_id
		WHERE a.scope = 'codex:codex-cli' AND a.alias = 'session-2'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("session-2 event count = %d, want 1", count)
	}
}

func TestCodexTokenUsageRecordRejectsCumulativeRollback(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "rollback.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":10,"output_tokens":1}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-2","usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":9,"output_tokens":1}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("rollback result = %+v, err = %v", result, err)
	}
}

func TestCodexTokenUsageRecordDuplicateResponseReachesStorageDeduplication(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "duplicate.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Receipt.Duplicates != 1 || result.Outcomes.UnsupportedUsage != 0 {
		t.Fatalf("duplicate result = %+v, err = %v", result, err)
	}
}

func TestCodexTokenUsageRecordConflictingResponseRemainsRejectedWithReasoningMetadata(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "conflicting-response.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-conflict","project_id":"project-conflict","model":"gpt-test"}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-conflict","session_id":"session-conflict","response_id":"response-conflict","usage":{"input_tokens":10,"output_tokens":1}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-conflict","session_id":"session-conflict","response_id":"response-conflict","usage":{"input_tokens":11,"output_tokens":1}}}
{"type":"event_msg","timestamp":"2026-10-11T00:00:03Z","payload":{"type":"token_count","session_id":"session-conflict","info":{"total_token_usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11},"last_usage_includes_reasoning":true}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt != (Receipt{Imported: 1}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2, UnsupportedUsage: 1}) {
		t.Fatalf("conflicting response import = %+v, err = %v; want conflict rejected with one response and legacy mirror ignored", result, err)
	}
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("conflicting response event count = %d, err = %v; want only the first response persisted", count, err)
	}
}

func TestCodexTokenUsageRecordRejectsAmbiguousCacheReadAliases(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "cache-alias.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"cached_input_tokens":5,"cache_read_input_tokens":5,"output_tokens":1}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 0 || result.Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("ambiguous cache result = %+v, err = %v", result, err)
	}
}

func TestCodexTokenUsageRecordResetCanResume(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "reset-resume.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":10,"output_tokens":1}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-2","usage":{"input_tokens":9,"output_tokens":1},"turn_token_usage":{"input_tokens":9,"output_tokens":1},"thread_token_usage":{"input_tokens":9,"output_tokens":1}}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:03Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-3","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":19,"output_tokens":2},"thread_token_usage":{"input_tokens":19,"output_tokens":2}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 2 || result.Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("reset/resume result = %+v, err = %v", result, err)
	}
}

func TestCodexTokenUsageRecordValidationMatrix(t *testing.T) {
	tests := map[string]struct {
		payload  string
		outcomes OutcomeCounts
	}{
		"missing response": {payload: `{"thread_id":"thread-1","session_id":"session-1","usage":{"input_tokens":1}}`, outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
		"negative input":   {payload: `{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":-1}}`, outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
		"unknown field":    {payload: `{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1,"future_tokens":2}}`, outcomes: OutcomeCounts{IgnoredNonUsage: 1, UnsupportedUsage: 1}},
		"wrong total":      {payload: `{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":9}}`, outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "invalid.jsonl")
			contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
` + `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":` + test.payload + "}\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (CodexAdapter{}).Import(path, store)
			if err != nil || result.Receipt != (Receipt{}) || result.Outcomes != test.outcomes || !result.Outcomes.Incomplete() || result.Outcomes.Pending() {
				t.Fatalf("validation result = %+v, err = %v", result, err)
			}
		})
	}
}

func TestCodexTokenUsageRecordAppendReportsExactReceiptAndTotals(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "append.jsonl")
	header := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
`
	firstRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":10,"output_tokens":1}}}
`
	secondRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-2","session_id":"session-1","response_id":"response-2","usage":{"input_tokens":5,"output_tokens":1},"turn_token_usage":{"input_tokens":5,"output_tokens":1},"thread_token_usage":{"input_tokens":15,"output_tokens":2}}}
`
	mirror := `{"type":"event_msg","timestamp":"2026-10-11T00:00:03Z","payload":{"type":"token_count","session_id":"session-1","info":{"total_token_usage":{"input_tokens":15,"output_tokens":2}}}}
`
	if err := os.WriteFile(path, []byte(header+firstRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (CodexAdapter{}).Import(path, store)
	if err != nil || first.Receipt.Imported != 1 || first.Receipt.Duplicates != 0 || first.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("first append result = %+v, err = %v", first, err)
	}
	if err := os.WriteFile(path, []byte(header+firstRecord+secondRecord+mirror), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := (CodexAdapter{}).Import(path, store)
	if err != nil || second.Receipt.Imported != 1 || second.Receipt.Duplicates != 1 || second.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2}) {
		t.Fatalf("append result = %+v, err = %v", second, err)
	}
	var count, input, output int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&count, &input, &output); err != nil {
		t.Fatal(err)
	}
	if [3]int64{count, input, output} != [3]int64{2, 15, 2} {
		t.Fatalf("append totals = %v", [3]int64{count, input, output})
	}
	assertCodexAppendRowsAndReport(t, store)
}

type codexCanonicalProjection struct {
	EventID, IngestionKey, OccurredAt string
	ProjectID, SessionID              string
	Client, Provider                  string
	RequestedModel, ResolvedModel     string
	Input, Output, CacheRead          int64
	CacheWrite, Reasoning             int64
	SourceRecord                      string
}

func queryCodexCanonicalRows(t *testing.T, store *Store) []codexCanonicalProjection {
	t.Helper()
	rows, err := store.db.Query(`SELECT event_id, ingestion_key, occurred_at, project_id, session_id,
		client_name, provider_name, requested_model, resolved_model, input_tokens, output_tokens,
		cache_read_tokens, cache_write_tokens, reasoning_tokens, source_record_id
		FROM usage_events ORDER BY occurred_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var projections []codexCanonicalProjection
	for rows.Next() {
		var projection codexCanonicalProjection
		if err := rows.Scan(&projection.EventID, &projection.IngestionKey, &projection.OccurredAt,
			&projection.ProjectID, &projection.SessionID, &projection.Client, &projection.Provider,
			&projection.RequestedModel, &projection.ResolvedModel, &projection.Input, &projection.Output,
			&projection.CacheRead, &projection.CacheWrite, &projection.Reasoning, &projection.SourceRecord); err != nil {
			t.Fatal(err)
		}
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return projections
}

func assertCodexAppendRowsAndReport(t *testing.T, store *Store) {
	t.Helper()
	rows, err := store.db.Query(`SELECT event_id, ingestion_key, occurred_at, project_id, session_id,
		client_name, provider_name, requested_model, resolved_model, input_tokens, output_tokens,
		cache_read_tokens, cache_write_tokens, reasoning_tokens, source_record_id
		FROM usage_events ORDER BY occurred_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []codexCanonicalProjection
	for rows.Next() {
		var projection codexCanonicalProjection
		if err := rows.Scan(&projection.EventID, &projection.IngestionKey, &projection.OccurredAt,
			&projection.ProjectID, &projection.SessionID, &projection.Client, &projection.Provider,
			&projection.RequestedModel, &projection.ResolvedModel, &projection.Input, &projection.Output,
			&projection.CacheRead, &projection.CacheWrite, &projection.Reasoning, &projection.SourceRecord); err != nil {
			t.Fatal(err)
		}
		got = append(got, projection)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	identity := func(response string) string {
		return digest([]byte("codex-response\x00session-1\x00thread-1\x00" + response))
	}
	var localProjectID, localSessionID string
	if err := store.db.QueryRow("SELECT project_id FROM project_aliases WHERE scope = 'codex' AND alias = 'project-1'").Scan(&localProjectID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT session_id FROM session_aliases WHERE scope = 'codex:codex-cli' AND alias = 'session-1'").Scan(&localSessionID); err != nil {
		t.Fatal(err)
	}
	want := []codexCanonicalProjection{
		{EventID: "codex-response-event-" + identity("response-1"), IngestionKey: "codex:response:" + identity("response-1"), OccurredAt: "2026-10-11T00:00:01.000000000Z", ProjectID: localProjectID, SessionID: localSessionID, Client: "codex-cli", Provider: "openai", RequestedModel: "gpt-test", ResolvedModel: "gpt-test", Input: 10, Output: 1, SourceRecord: "response-1"},
		{EventID: "codex-response-event-" + identity("response-2"), IngestionKey: "codex:response:" + identity("response-2"), OccurredAt: "2026-10-11T00:00:02.000000000Z", ProjectID: localProjectID, SessionID: localSessionID, Client: "codex-cli", Provider: "openai", RequestedModel: "gpt-test", ResolvedModel: "gpt-test", Input: 5, Output: 1, SourceRecord: "response-2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical rows = %+v, want %+v", got, want)
	}
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	report, err := store.Query(UsageFilter{From: from, To: from.AddDate(0, 0, 1), GroupBy: []string{GroupProject, GroupSession, GroupClient, GroupProvider, GroupModel}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RequestCount != 2 || report.Summary.SessionCount != 1 || report.Summary.InputTokens != 15 ||
		report.Summary.OutputTokens != 2 || report.Summary.TotalTokens != 17 || report.Summary.UnknownCostRequests != 2 {
		t.Fatalf("report summary = %+v", report.Summary)
	}
	if len(report.Rows) != 1 || !reflect.DeepEqual(report.Rows[0].Dimensions, map[string]string{
		GroupProject: localProjectID, GroupSession: localSessionID, GroupClient: "codex-cli", GroupProvider: "openai", GroupModel: "gpt-test",
	}) || report.Rows[0].UsageMeasures != report.Summary {
		t.Fatalf("report rows = %+v, summary = %+v", report.Rows, report.Summary)
	}
}

func TestCodexTokenUsageRecordAttributionFailuresHaveExactOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		record     string
		wantResult ImportResult
	}{
		{
			name:       "invalid timestamp",
			header:     `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}` + "\n",
			record:     `{"type":"token_usage_record","timestamp":"not-rfc3339","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n",
			wantResult: ImportResult{Outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
		},
		{
			name:       "missing model context",
			header:     `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project"}}` + "\n",
			record:     `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n",
			wantResult: ImportResult{Outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
		},
		{
			name:       "missing explicit project metadata",
			header:     `{"type":"session_meta","payload":{"id":"session-1","model":"gpt-test"}}` + "\n",
			record:     `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n",
			wantResult: ImportResult{Outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
		},
		{
			name:       "unknown session context",
			header:     `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}` + "\n",
			record:     `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","payload":{"thread_id":"thread-1","session_id":"missing-session","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n",
			wantResult: ImportResult{Outcomes: OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "attribution.jsonl")
			if err := os.WriteFile(path, []byte(test.header+test.record), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (CodexAdapter{}).Import(path, store)
			if err != nil || result.Receipt != test.wantResult.Receipt || result.Outcomes != test.wantResult.Outcomes || result.Outcomes.Incomplete() != true || result.Outcomes.Pending() {
				t.Fatalf("attribution result = %+v, err = %v", result, err)
			}
			var eventCount int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&eventCount); err != nil {
				t.Fatal(err)
			}
			if eventCount != 0 {
				t.Fatalf("imported %d events without trustworthy attribution", eventCount)
			}
		})
	}
}

func TestCodexTokenUsageRecordRejectsLegacyOnlyAttribution(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "legacy-context.jsonl")
	legacy := `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-1","project_id":"project-1","model":"gpt-test","info":{"total_token_usage":{"input_tokens":10}}}}` + "\n"
	usageRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n"
	if err := os.WriteFile(path, []byte(legacy+usageRecord), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Receipt.Duplicates != 0 ||
		result.Outcomes != (OutcomeCounts{MalformedRecord: 1}) {
		t.Fatalf("legacy-only attribution result = %+v, err = %v", result, err)
	}
	var eventCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("event count = %d, want only the legacy event", eventCount)
	}
}

func TestCodexLegacyTokenCountAcceptsCacheWriteInputTokensAlias(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "legacy-cache-write-alias.jsonl")
	legacy := `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-1","project_id":"project-1","model":"gpt-test","info":{"total_token_usage":{"input_tokens":10,"cache_write_input_tokens":4}}}}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes != (OutcomeCounts{}) {
		t.Fatalf("legacy cache-write alias result = %+v, err = %v", result, err)
	}
	var input, writes int64
	if err := store.db.QueryRow(`SELECT input_tokens, cache_write_tokens FROM usage_events`).Scan(&input, &writes); err != nil {
		t.Fatal(err)
	}
	if input != 10 || writes != 4 {
		t.Fatalf("legacy cache-write alias usage = input %d, cache_write %d; want 10, 4", input, writes)
	}
}

func TestCodexTokenUsageRecordDerivesProjectFromCWDWhenProjectIDAbsent(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "cwd-project.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-1","cwd":"/tmp/project","model":"gpt-test"}}` + "\n"
	usageRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n"
	if err := os.WriteFile(path, []byte(metadata+usageRecord), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("cwd-derived project result = %+v, err = %v", result, err)
	}
	var projectID string
	if err := store.db.QueryRow(`SELECT project_id FROM usage_events WHERE event_id LIKE 'codex-response-event-%'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if projectID == "" || strings.Contains(projectID, "-project") {
		t.Fatalf("project id = %q, want a stable identity derived from cwd", projectID)
	}

	otherStore := openTestStore(t)
	otherPath := filepath.Join(t.TempDir(), "cwd-project-other-source.jsonl")
	if err := os.WriteFile(otherPath, []byte(metadata+usageRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(otherPath, otherStore); err != nil {
		t.Fatal(err)
	}
	var otherProjectID string
	if err := otherStore.db.QueryRow(`SELECT project_id FROM usage_events WHERE event_id LIKE 'codex-response-event-%'`).Scan(&otherProjectID); err != nil {
		t.Fatal(err)
	}
	if otherProjectID != projectID {
		t.Fatalf("project id differs across source files for the same cwd: %q vs %q", projectID, otherProjectID)
	}
}

func TestCodexTokenUsageRecordAttributionUsesMetadataAfterLegacyRecords(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "metadata-context.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-1","project_id":"metadata-project","model":"metadata-model"}}` + "\n"
	legacy := `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-1","project_id":"metadata-project","model":"legacy-model","info":{"total_token_usage":{"input_tokens":10}}}}` + "\n"
	usageRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n"
	if err := os.WriteFile(path, []byte(metadata+legacy+usageRecord), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 2 || result.Receipt.Duplicates != 0 ||
		result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("metadata attribution result = %+v, err = %v", result, err)
	}
	var projectID, model string
	if err := store.db.QueryRow(`SELECT project_id, requested_model FROM usage_events WHERE event_id LIKE 'codex-response-event-%'`).Scan(&projectID, &model); err != nil {
		t.Fatal(err)
	}
	var expectedProjectID string
	if err := store.db.QueryRow("SELECT project_id FROM project_aliases WHERE scope = 'codex' AND alias = 'metadata-project'").Scan(&expectedProjectID); err != nil {
		t.Fatal(err)
	}
	if projectID != expectedProjectID || model != "metadata-model" {
		t.Fatalf("response attribution = project %q, model %q; want metadata values", projectID, model)
	}
}

func TestCodexTokenUsageRecordWithoutSessionUsesActiveSession(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "active-session.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","model":"gpt-test"}}` + "\n"
	legacy := `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-2","info":{"total_token_usage":{"input_tokens":10}}}}` + "\n"
	usageRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","response_id":"response-1","usage":{"input_tokens":1}}}` + "\n"
	if err := os.WriteFile(path, []byte(metadata+legacy+usageRecord), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1, MalformedRecord: 1}) {
		t.Fatalf("active-session attribution result = %+v, err = %v", result, err)
	}
	var eventCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("event count = %d, want only the legacy event", eventCount)
	}
}

func TestCodexLegacyTokenCountSessionSwitchPreservesContext(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "legacy-session-switch.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","model":"gpt-test"}}` + "\n"
	legacy := `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session-2","info":{"total_token_usage":{"input_tokens":10}}}}` + "\n"
	if err := os.WriteFile(path, []byte(metadata+legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("legacy session-switch result = %+v, err = %v", result, err)
	}
	var projectID, model string
	if err := store.db.QueryRow(`SELECT e.project_id, e.requested_model FROM usage_events e JOIN session_aliases a ON a.session_id = e.session_id
		WHERE a.scope = 'codex:codex-cli' AND a.alias = 'session-2'`).Scan(&projectID, &model); err != nil {
		t.Fatal(err)
	}
	var expectedProjectID string
	if err := store.db.QueryRow("SELECT project_id FROM project_aliases WHERE scope = 'codex' AND alias = 'project-1'").Scan(&expectedProjectID); err != nil {
		t.Fatal(err)
	}
	if projectID != expectedProjectID || model != "gpt-test" {
		t.Fatalf("legacy session-switch attribution = project %q, model %q; want inherited context", projectID, model)
	}
}

func TestCodexTokenUsageRecordIncompleteTailCompletesWithoutChangingSource(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "incomplete.jsonl")
	header := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
`
	record := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":4,"output_tokens":1}}}
`
	partial := []byte(header + strings.TrimSuffix(record, "}\n"))
	if err := os.WriteFile(path, partial, 0o600); err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), partial...)
	pending, err := (CodexAdapter{}).Import(path, store)
	if err != nil || pending.Receipt.Imported != 0 || pending.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1, DeferredIncompleteTail: 1}) || !bytes.Equal(original, mustReadFile(t, path)) {
		t.Fatalf("pending result = %+v, err = %v", pending, err)
	}
	if err := os.WriteFile(path, []byte(header+record), 0o600); err != nil {
		t.Fatal(err)
	}
	completed, err := (CodexAdapter{}).Import(path, store)
	if err != nil || completed.Receipt.Imported != 1 || completed.Receipt.Duplicates != 0 || completed.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("completed result = %+v, err = %v", completed, err)
	}
}

func TestCodexTokenUsageRecordPersistenceFailureRollsBackReferencesAndCursor(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "rollback.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}
{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","payload":{"thread_id":"thread-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":4,"output_tokens":1}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := testEvent("seed")
	conflict.EventID = "codex-response-event-" + digest([]byte("codex-response\x00session-1\x00thread-1\x00response-1"))
	if _, err := store.Append([]UsageEvent{conflict}); err != nil {
		t.Fatal(err)
	}
	original := mustReadFile(t, path)
	if _, err := (CodexAdapter{}).Import(path, store); err == nil {
		t.Fatal("conflicting new-format event unexpectedly imported")
	}
	if !bytes.Equal(original, mustReadFile(t, path)) {
		t.Fatal("source changed after failed import")
	}
	for name, query := range map[string]string{
		"project": `SELECT COUNT(*) FROM projects WHERE id = 'project-1'`,
		"session": `SELECT COUNT(*) FROM sessions WHERE id = 'session-1'`,
		"cursor":  `SELECT COUNT(*) FROM source_cursors WHERE source_id LIKE 'codex-file-%'`,
	} {
		var count int
		if err := store.db.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("failed import left %d %s rows", count, name)
		}
	}
}

func TestCodexTokenUsageRecordMirrorsDoNotChangeEventsOrTotals(t *testing.T) {
	withMirror := mustReadFile(t, filepath.Join("testdata", "codex", "token-usage-records.jsonl"))
	normalized := strings.ReplaceAll(string(withMirror), "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) < 2 {
		t.Fatal("mirror fixture has no removable final record")
	}
	var finalEnvelope codexEnvelope
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &finalEnvelope); err != nil {
		t.Fatalf("parse final mirror record: %v", err)
	}
	if finalEnvelope.Type != "event_msg" || !strings.Contains(string(finalEnvelope.Payload), `"token_count"`) {
		t.Fatalf("final fixture record is not the expected legacy mirror: %s", lines[len(lines)-1])
	}
	withoutMirrorText := strings.Join(lines[:len(lines)-1], "\n") + "\n"
	if len(withoutMirrorText) >= len(normalized)+1 {
		t.Fatal("mirror fixture final record was not removed")
	}
	withoutMirror := []byte(withoutMirrorText)
	path := filepath.Join(t.TempDir(), "mirror.jsonl")
	type observation struct {
		rows   []codexCanonicalProjection
		report UsageReport
		totals [6]int64
	}
	var observations map[string]observation
	for name, contents := range map[string][]byte{"with mirror": withMirror, "without mirror": withoutMirror} {
		t.Run(name, func(t *testing.T) {
			store := openTestStore(t)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (CodexAdapter{}).Import(path, store)
			if err != nil || result.Receipt.Imported != 3 || result.Outcomes.UnsupportedUsage != 0 || result.Outcomes.MalformedRecord != 0 {
				t.Fatalf("mirror result = %+v, err = %v", result, err)
			}
			var count, input, output, cached, writes, reasoning int64
			if err := store.db.QueryRow(`SELECT COUNT(*), SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens) FROM usage_events`).Scan(&count, &input, &output, &cached, &writes, &reasoning); err != nil {
				t.Fatal(err)
			}
			totals := [6]int64{count, input, output, cached, writes, reasoning}
			if totals != [6]int64{3, 150, 15, 3, 4, 2} {
				t.Fatalf("mirror totals = %v", [6]int64{count, input, output, cached, writes, reasoning})
			}
			from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
			report, err := store.Query(UsageFilter{From: from, To: from.AddDate(0, 0, 1), GroupBy: []string{GroupProject, GroupSession, GroupClient, GroupProvider, GroupModel}})
			if err != nil {
				t.Fatal(err)
			}
			if observations == nil {
				observations = make(map[string]observation)
			}
			observations[name] = observation{rows: queryCodexCanonicalRows(t, store), report: report, totals: totals}
		})
	}
	if !reflect.DeepEqual(observations["with mirror"], observations["without mirror"]) {
		t.Fatalf("mirrored and unmirrored canonical results differ: with=%+v without=%+v", observations["with mirror"], observations["without mirror"])
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestCodexTokenCountWithoutUsageIsIgnoredAndReused(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{
			name:    "null info with rate limits and no accounting context",
			payload: `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":null,"rate_limits":{"primary":null}}}`,
		},
		{
			name:    "omitted info with rate limits",
			payload: `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","rate_limits":{"primary":null}}}`,
		},
		{
			name:    "legacy token_count envelope without info",
			payload: `{"type":"token_count","timestamp":"2026-10-11T00:00:00Z","payload":{"rate_limits":{"primary":null}}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "informational.jsonl")
			metadata := `{"type":"session_meta","payload":{"id":"session-no-model"}}` + "\n"
			if err := os.WriteFile(path, []byte(metadata+test.payload+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			first, err := (CodexAdapter{}).Import(path, store)
			if err != nil || first.Receipt != (Receipt{}) || first.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2}) {
				t.Fatalf("initial result = %+v, err = %v; want metadata and informational records ignored", first, err)
			}
			count, err := store.EventCount()
			if err != nil || count != 0 {
				t.Fatalf("event count = %d, err = %v; want 0", count, err)
			}
			canonicalPath, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			cursor, err := store.Cursor("codex-file-" + digest([]byte(canonicalPath)))
			if err != nil || !cursor.SnapshotReusable || cursor.PendingTail {
				t.Fatalf("cursor = %+v, err = %v; want reusable complete snapshot", cursor, err)
			}
			repeat, err := (CodexAdapter{}).Import(path, store)
			if err != nil || repeat.Receipt != (Receipt{}) || repeat.Outcomes != (OutcomeCounts{UnchangedSnapshot: 1}) {
				t.Fatalf("repeat result = %+v, err = %v; want unchanged fast path", repeat, err)
			}
		})
	}
}

func TestCodexTokenCountWithoutUsagePreservesContextAndStrictUsageValidation(t *testing.T) {
	database := filepath.Join(t.TempDir(), "mixed.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	path := filepath.Join(t.TempDir(), "mixed.jsonl")
	metadata := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"session-classifier-only"}}` + "\n"
	usage := func(timestamp string, input, output int) string {
		return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` + decimal(input) + `,"output_tokens":` + decimal(output) + `}}}}` + "\n"
	}
	informational := `{"type":"event_msg","timestamp":"2026-10-11T00:00:01Z","payload":{"type":"token_count","session_id":"session-199","project_id":"project-199","model":"gpt-test","info":null,"rate_limits":{"primary":null}}}` + "\n"
	between := `{"type":"event_msg","timestamp":"2026-10-11T00:00:03Z","payload":{"type":"token_count","session_id":"session-199","rate_limits":{"primary":null}}}` + "\n"
	after := `{"type":"event_msg","timestamp":"2026-10-11T00:00:05Z","payload":{"type":"token_count","session_id":"session-199","info":null}}` + "\n"
	initial := metadata + informational + usage("2026-10-11T00:00:02Z", 10, 2) + between + usage("2026-10-11T00:00:04Z", 15, 3) + after
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (CodexAdapter{}).Import(path, store)
	if err != nil || first.Receipt != (Receipt{Imported: 2}) || first.Outcomes != (OutcomeCounts{IgnoredNonUsage: 4}) {
		t.Fatalf("mixed import = %+v, err = %v; want 2 imports and 4 ignored records including session metadata", first, err)
	}
	var count, input, output int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&count, &input, &output); err != nil {
		t.Fatal(err)
	}
	if [3]int64{count, input, output} != [3]int64{2, 15, 3} {
		t.Fatalf("mixed accounting = count %d input %d output %d; want 2, 15, 3", count, input, output)
	}
	var projectCount, sessionCount int
	var minModel, maxModel string
	if err := store.db.QueryRow(`SELECT COUNT(DISTINCT project_id), COUNT(DISTINCT session_id), MIN(requested_model), MAX(requested_model) FROM usage_events`).Scan(&projectCount, &sessionCount, &minModel, &maxModel); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 || sessionCount != 1 || minModel != "gpt-test" || maxModel != "gpt-test" {
		t.Fatalf("usage after metadata-only token count attributed to projects=%d sessions=%d models=%q..%q", projectCount, sessionCount, minModel, maxModel)
	}

	// An append after informational records must import its cumulative delta
	// exactly once, and a restart must retain the committed identities/cursor.
	appended := initial + usage("2026-10-11T00:00:06Z", 18, 4)
	if err := os.WriteFile(path, []byte(appended), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := (CodexAdapter{}).Import(path, store)
	if err != nil || next.Receipt != (Receipt{Imported: 1, Duplicates: 2}) || next.Outcomes != (OutcomeCounts{IgnoredNonUsage: 4}) {
		t.Fatalf("appended import = %+v, err = %v; want one new event", next, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := (CodexAdapter{}).Import(path, store)
	if err != nil || repeat.Receipt != (Receipt{}) || repeat.Outcomes != (OutcomeCounts{UnchangedSnapshot: 1}) {
		t.Fatalf("appended repeat after restart = %+v, err = %v; want unchanged snapshot", repeat, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&count, &input, &output); err != nil {
		t.Fatal(err)
	}
	if [3]int64{count, input, output} != [3]int64{3, 18, 4} {
		t.Fatalf("appended accounting = count %d input %d output %d; want 3, 18, 4", count, input, output)
	}
}

func TestCodexTokenCountPresentInvalidUsageRemainsRejected(t *testing.T) {
	for _, test := range []struct {
		name        string
		payload     string
		wantOutcome OutcomeCounts
	}{
		{
			name:        "empty info object is malformed usage",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{}}}`,
			wantOutcome: OutcomeCounts{MalformedRecord: 1},
		},
		{
			name:        "info with wrong JSON type is malformed usage",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":"not-an-object"}}`,
			wantOutcome: OutcomeCounts{MalformedRecord: 1},
		},
		{
			name:        "invalid counter type is malformed usage",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":"invalid"}}}}`,
			wantOutcome: OutcomeCounts{MalformedRecord: 1},
		},
		{
			name:        "unknown counter remains unsupported",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"future_token_units":4}}}}`,
			wantOutcome: OutcomeCounts{UnsupportedUsage: 1},
		},
		{
			name:        "unknown info field remains unsupported",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{"future_usage":{}}}}`,
			wantOutcome: OutcomeCounts{UnsupportedUsage: 1},
		},
		{
			name:        "reasoning flag at the wrong level remains unsupported",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","last_usage_includes_reasoning":true,"info":{"total_token_usage":{"input_tokens":1,"output_tokens":1}}}}`,
			wantOutcome: OutcomeCounts{UnsupportedUsage: 1},
		},
		{
			name:        "reasoning flag string is malformed metadata",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{"last_usage_includes_reasoning":"true","total_token_usage":{"input_tokens":1,"output_tokens":1}}}}`,
			wantOutcome: OutcomeCounts{MalformedRecord: 1},
		},
		{
			name:        "reasoning flag number is malformed metadata",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{"last_usage_includes_reasoning":1,"total_token_usage":{"input_tokens":1,"output_tokens":1}}}}`,
			wantOutcome: OutcomeCounts{MalformedRecord: 1},
		},
		{
			name:        "reasoning flag object is malformed metadata",
			payload:     `{"type":"event_msg","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","info":{"last_usage_includes_reasoning":{},"total_token_usage":{"input_tokens":1,"output_tokens":1}}}}`,
			wantOutcome: OutcomeCounts{MalformedRecord: 1},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "invalid.jsonl")
			metadata := `{"type":"session_meta","payload":{"id":"session-invalid","project_id":"project-invalid","model":"gpt-test"}}` + "\n"
			if err := os.WriteFile(path, []byte(metadata+test.payload+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := (CodexAdapter{}).Import(path, store)
			if err != nil || got.Outcomes != func() OutcomeCounts { want := test.wantOutcome; want.IgnoredNonUsage++; return want }() {
				t.Fatalf("import = %+v, err = %v; want parser outcome %+v plus metadata", got, err, test.wantOutcome)
			}
		})
	}
}

func TestCodexLegacyDirectTokenCountWithoutInfoStillImportsUsage(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "direct-usage.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-direct","project_id":"project-direct","model":"gpt-test"}}` + "\n"
	direct := `{"type":"token_count","timestamp":"2026-10-11T00:00:01Z","payload":{"session_id":"session-direct","project_id":"project-direct","model":"gpt-test","input_tokens":9,"output_tokens":2}}` + "\n"
	if err := os.WriteFile(path, []byte(metadata+direct), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt != (Receipt{Imported: 1}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("direct legacy import = %+v, err = %v; want one imported event and metadata ignored", result, err)
	}
	var count, input, output int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&count, &input, &output); err != nil {
		t.Fatal(err)
	}
	if [3]int64{count, input, output} != [3]int64{1, 9, 2} {
		t.Fatalf("direct usage accounting = %v; want [1 9 2]", [3]int64{count, input, output})
	}
}

func TestCodexInformationalTokenCountAndIncompleteTailCompleteOnAppend(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "pending-info.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-tail","project_id":"project-tail","model":"gpt-test"}}` + "\n"
	firstUsage := `{"type":"event_msg","timestamp":"2026-10-11T00:00:01Z","payload":{"type":"token_count","session_id":"session-tail","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}` + "\n"
	informational := `{"type":"event_msg","timestamp":"2026-10-11T00:00:02Z","payload":{"type":"token_count","session_id":"session-tail","info":null,"rate_limits":{"primary":null}}}` + "\n"
	partial := `{"type":"event_msg","timestamp":"2026-10-11T00:00:03Z","payload":{"type":"token_count","session_id":"session-tail","info":{"total_token_usage":{"input_tokens":15,"output_tokens":3`
	if err := os.WriteFile(path, []byte(metadata+firstUsage+informational+partial), 0o600); err != nil {
		t.Fatal(err)
	}
	pending, err := (CodexAdapter{}).Import(path, store)
	if err != nil || pending.Receipt != (Receipt{Imported: 1}) || pending.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2, DeferredIncompleteTail: 1}) {
		t.Fatalf("pending import = %+v, err = %v", pending, err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Cursor("codex-file-" + digest([]byte(canonicalPath)))
	if err != nil || !cursor.PendingTail {
		t.Fatalf("pending cursor = %+v, err = %v; want pending tail", cursor, err)
	}
	completion := `,"total_tokens":18},"last_usage_includes_reasoning":false}}}` + "\n"
	if err := os.WriteFile(path, []byte(metadata+firstUsage+informational+partial+completion), 0o600); err != nil {
		t.Fatal(err)
	}
	completed, err := (CodexAdapter{}).Import(path, store)
	if err != nil || completed.Receipt.Imported != 1 || completed.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2}) {
		t.Fatalf("completed import = %+v, err = %v; want exactly one new event and no deferred tail", completed, err)
	}
	var count, input, output int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&count, &input, &output); err != nil {
		t.Fatal(err)
	}
	if [3]int64{count, input, output} != [3]int64{2, 15, 3} {
		t.Fatalf("completed accounting = %v; want [2 15 3]", [3]int64{count, input, output})
	}
}

func TestCodexInformationalReimportRecoversPreFixDatabaseState(t *testing.T) {
	database := filepath.Join(t.TempDir(), "pre-fix.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	path := filepath.Join(t.TempDir(), "affected.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"session-recovery","project_id":"project-recovery","model":"gpt-test"}}` + "\n"
	usage := `{"type":"event_msg","timestamp":"2026-10-11T00:00:01Z","payload":{"type":"token_count","session_id":"session-recovery","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}` + "\n"
	info := `{"type":"event_msg","timestamp":"2026-10-11T00:00:02Z","payload":{"type":"token_count","session_id":"session-recovery","info":null,"rate_limits":{"primary":null}}}` + "\n"
	prefix := metadata + usage
	if err := os.WriteFile(path, []byte(prefix), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := collector.PollOnce()
	if err != nil || initial.Receipt != (Receipt{Imported: 1}) {
		t.Fatalf("initial pre-existing usage = %+v, err = %v", initial, err)
	}
	fullSource := prefix + info
	if err := os.WriteFile(path, []byte(fullSource), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "codex-file-" + digest([]byte(canonicalPath))
	// Reconstruct the cursor and finding written by the pre-fix parser: the
	// source was observed as incomplete after the informational line, while
	// the already committed event remained in storage.
	order, err := store.reserveDiagnosticEvidenceOrder()
	if err != nil {
		t.Fatal(err)
	}
	finding := testDetailedFinding(sourceID, order, 1, uint64(len(prefix)))
	finding.Classification = makeDiagnosticClassification(DiagnosticComponentParser, DiagnosticOperationParse,
		DiagnosticReasonInvalidTokenCount, DiagnosticAdapterCodex)
	finding.SubjectAvailability = DiagnosticAvailabilityAvailable
	finding.Generation = DiagnosticOptionalUint64{Value: 0, Availability: DiagnosticAvailabilityAvailable}
	finding.CoverageOffset = DiagnosticOptionalUint64{Value: uint64(len(fullSource)), Availability: DiagnosticAvailabilityAvailable}
	finding.Position.ByteOffset = DiagnosticOptionalUint64{Value: uint64(len(prefix)), Availability: DiagnosticAvailabilityAvailable}
	finding.Coverage = DiagnosticCoverageFullSnapshot
	persistTestFinding(t, store, finding)
	if _, err := store.db.Exec(`UPDATE source_cursors SET file_fingerprint = ?, byte_offset = ?, snapshot_reusable = 0, pending_tail = 0 WHERE source_id = ?`,
		digest([]byte(fullSource)), len(fullSource), sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM parser_checkpoints WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{Reason: DiagnosticReasonInvalidTokenCount})
	if err != nil || len(page.Items) != 1 || page.Items[0].Condition.State != DiagnosticConditionActive {
		t.Fatalf("seeded pre-fix finding = %+v, err = %v", page, err)
	}
	problemID := page.Items[0].ProblemID

	recovered, err := collector.PollOnce()
	if err != nil || recovered.Receipt != (Receipt{Duplicates: 1}) || recovered.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2}) {
		t.Fatalf("candidate recovery = %+v, err = %v; want existing event duplicate plus complete informational parse", recovered, err)
	}
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("existing event count = %d, err = %v; want 1", count, err)
	}
	detail, err := store.InspectDiagnosticDetail(problemID)
	if err != nil || detail.Condition.State != DiagnosticConditionResolved || detail.Condition.Coverage != DiagnosticCoverageFullSnapshot {
		t.Fatalf("full candidate snapshot did not resolve prior finding: condition=%+v err=%v", detail.Condition, err)
	}
	resolvedAt := detail.Condition.AsOf
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	collector, err = NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := collector.PollOnce()
	unchangedDetail, inspectErr := store.InspectDiagnosticDetail(problemID)
	if err != nil || unchanged.Outcomes != (OutcomeCounts{UnchangedSnapshot: 1}) || inspectErr != nil ||
		unchangedDetail.Condition.State != DiagnosticConditionResolved || !unchangedDetail.Condition.AsOf.Equal(resolvedAt) {
		t.Fatalf("restart unchanged check = %+v detail=%+v pollErr=%v inspectErr=%v", unchanged, unchangedDetail.Condition, err, inspectErr)
	}
}

func TestCodexReasoningMetadataAppendReplaysRevisionThreeCheckpoint(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "reasoning-append.jsonl")
	metadata := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"session-revision","project_id":"project-revision","model":"gpt-test"}}` + "\n"
	snapshot := func(timestamp string, input, output, lastInput, lastOutput int, flag string) string {
		metadata := ""
		if flag != "" {
			metadata = `,"last_usage_includes_reasoning":` + flag
		}
		return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","session_id":"session-revision","info":{"total_token_usage":{"input_tokens":` + decimal(input) + `,"output_tokens":` + decimal(output) + `,"total_tokens":` + decimal(input+output) + `},"last_token_usage":{"input_tokens":` + decimal(lastInput) + `,"output_tokens":` + decimal(lastOutput) + `,"total_tokens":` + decimal(lastInput+lastOutput) + `},"model_context_window":258400` + metadata + `}}}` + "\n"
	}
	first := snapshot("2026-10-11T00:00:01Z", 10, 2, 10, 2, "")
	if err := os.WriteFile(path, []byte(metadata+first), 0o600); err != nil {
		t.Fatal(err)
	}
	initial, err := (CodexAdapter{}).Import(path, store)
	if err != nil || initial.Receipt != (Receipt{Imported: 1}) {
		t.Fatalf("initial import = %+v, err = %v", initial, err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "codex-file-" + digest([]byte(canonicalPath))
	checkpoint, err := parserCheckpointExpectation(store, sourceID)
	if err != nil || !checkpoint.exists {
		t.Fatalf("initial checkpoint = %+v, err = %v", checkpoint, err)
	}
	if checkpoint.row.ParserRevision != codexParserRevision {
		t.Fatalf("initial parser revision = %d; want %d", checkpoint.row.ParserRevision, codexParserRevision)
	}
	// Revision 3 is the checkpoint format produced by merged PR #201. It did
	// not accept the newly supported metadata, so appending such a record must
	// invalidate this state and replay the complete source.
	if _, err := store.db.Exec(`UPDATE parser_checkpoints SET parser_revision = 3 WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	second := snapshot("2026-10-11T00:00:02Z", 15, 3, 5, 1, "true")
	fullSource := metadata + first + second
	if err := os.WriteFile(path, []byte(fullSource), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := (CodexAdapter{}).Import(path, store)
	if err != nil || recovered.Receipt != (Receipt{Imported: 1, Duplicates: 1}) || recovered.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("revision-mismatch append = %+v, err = %v; want full replay, one new event and one duplicate", recovered, err)
	}
	var count, input, output int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&count, &input, &output); err != nil {
		t.Fatal(err)
	}
	if [3]int64{count, input, output} != [3]int64{2, 15, 3} {
		t.Fatalf("revision-mismatch accounting = %v; want [2 15 3]", [3]int64{count, input, output})
	}
	checkpoint, err = parserCheckpointExpectation(store, sourceID)
	if err != nil || !checkpoint.exists || checkpoint.row.ParserRevision != codexParserRevision || checkpoint.row.BoundaryOffset != int64(len(fullSource)) {
		t.Fatalf("rebuilt checkpoint = %+v, err = %v; want current revision and full boundary", checkpoint.row, err)
	}
}

func TestCodexReasoningMetadataReimportRecoversPreviouslyUnsupportedSnapshot(t *testing.T) {
	database := filepath.Join(t.TempDir(), "reasoning-recovery.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	path := filepath.Join(t.TempDir(), "affected-reasoning.jsonl")
	metadata := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"session-recovery-200","project_id":"project-recovery-200","model":"gpt-test"}}` + "\n"
	snapshot := func(timestamp string, input, cached, writes, output, reasoning, lastInput, lastCached, lastWrites, lastOutput, lastReasoning int, flag string) string {
		lastUsageMetadata := ""
		if flag != "" {
			lastUsageMetadata = `,"last_usage_includes_reasoning":` + flag
		}
		return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","session_id":"session-recovery-200","info":{"total_token_usage":{"input_tokens":` + decimal(input) + `,"cached_input_tokens":` + decimal(cached) + `,"cache_write_input_tokens":` + decimal(writes) + `,"output_tokens":` + decimal(output) + `,"reasoning_output_tokens":` + decimal(reasoning) + `,"total_tokens":` + decimal(input+output) + `},"last_token_usage":{"input_tokens":` + decimal(lastInput) + `,"cached_input_tokens":` + decimal(lastCached) + `,"cache_write_input_tokens":` + decimal(lastWrites) + `,"output_tokens":` + decimal(lastOutput) + `,"reasoning_output_tokens":` + decimal(lastReasoning) + `,"total_tokens":` + decimal(lastInput+lastOutput) + `},"model_context_window":258400` + lastUsageMetadata + `}}}` + "\n"
	}
	first := snapshot("2026-10-11T00:00:01Z", 100, 20, 5, 10, 4, 100, 20, 5, 10, 4, "")
	prefix := metadata + first
	if err := os.WriteFile(path, []byte(prefix), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := collector.PollOnce()
	if err != nil || initial.Receipt != (Receipt{Imported: 1}) {
		t.Fatalf("initial imported event = %+v, err = %v", initial, err)
	}
	var originalEventID string
	var originalInput, originalOutput int64
	if err := store.db.QueryRow(`SELECT event_id, input_tokens, output_tokens FROM usage_events`).Scan(&originalEventID, &originalInput, &originalOutput); err != nil {
		t.Fatal(err)
	}
	// Model the source cursor and finding left by the pre-fix parser after it
	// rejected the second, otherwise valid snapshot as unsupported usage.
	second := snapshot("2026-10-11T00:00:02Z", 150, 30, 6, 15, 6, 50, 10, 1, 5, 2, "false")
	fullSource := prefix + second
	if err := os.WriteFile(path, []byte(fullSource), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "codex-file-" + digest([]byte(canonicalPath))
	order, err := store.reserveDiagnosticEvidenceOrder()
	if err != nil {
		t.Fatal(err)
	}
	finding := testDetailedFinding(sourceID, order, 1, uint64(len(prefix)))
	finding.Classification = makeDiagnosticClassification(DiagnosticComponentParser, DiagnosticOperationParse,
		DiagnosticReasonUnsupportedUsage, DiagnosticAdapterCodex)
	finding.SubjectAvailability = DiagnosticAvailabilityAvailable
	finding.Generation = DiagnosticOptionalUint64{Value: 0, Availability: DiagnosticAvailabilityAvailable}
	finding.CoverageOffset = DiagnosticOptionalUint64{Value: uint64(len(fullSource)), Availability: DiagnosticAvailabilityAvailable}
	finding.Position.ByteOffset = DiagnosticOptionalUint64{Value: uint64(len(prefix)), Availability: DiagnosticAvailabilityAvailable}
	finding.Coverage = DiagnosticCoverageFullSnapshot
	persistTestFinding(t, store, finding)
	if _, err := store.db.Exec(`UPDATE source_cursors SET file_fingerprint = ?, byte_offset = ?, snapshot_reusable = 0, pending_tail = 0 WHERE source_id = ?`,
		digest([]byte(fullSource)), len(fullSource), sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM parser_checkpoints WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{Reason: DiagnosticReasonUnsupportedUsage})
	if err != nil || len(page.Items) != 1 || page.Items[0].Condition.State != DiagnosticConditionActive {
		t.Fatalf("seeded prior finding = %+v, err = %v", page, err)
	}
	problemID := page.Items[0].ProblemID

	recovered, err := collector.PollOnce()
	if err != nil || recovered.Receipt != (Receipt{Imported: 1, Duplicates: 1}) || recovered.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
		t.Fatalf("candidate recovery = %+v, err = %v", recovered, err)
	}
	var count, input, output, cached, writes, reasoning int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_write_tokens), 0), COALESCE(SUM(reasoning_tokens), 0) FROM usage_events`).Scan(&count, &input, &output, &cached, &writes, &reasoning); err != nil {
		t.Fatal(err)
	}
	if [6]int64{count, input, output, cached, writes, reasoning} != [6]int64{2, 150, 15, 30, 6, 6} {
		t.Fatalf("recovered exact accounting = %v; want [2 150 15 30 6 6]", [6]int64{count, input, output, cached, writes, reasoning})
	}
	var recoveredEventID string
	var recoveredInput, recoveredOutput int64
	if err := store.db.QueryRow(`SELECT event_id, input_tokens, output_tokens FROM usage_events WHERE input_tokens = 100`).Scan(&recoveredEventID, &recoveredInput, &recoveredOutput); err != nil {
		t.Fatal(err)
	}
	if recoveredEventID != originalEventID || recoveredInput != originalInput || recoveredOutput != originalOutput {
		t.Fatalf("previous event changed: before=(%s,%d,%d), after=(%s,%d,%d)", originalEventID, originalInput, originalOutput, recoveredEventID, recoveredInput, recoveredOutput)
	}
	detail, err := store.InspectDiagnosticDetail(problemID)
	if err != nil || detail.Condition.State != DiagnosticConditionResolved || detail.Condition.Coverage != DiagnosticCoverageFullSnapshot {
		t.Fatalf("full candidate snapshot did not resolve prior finding: condition=%+v err=%v", detail.Condition, err)
	}
	resolvedAt := detail.Condition.AsOf
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	collector, err = NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := collector.PollOnce()
	unchangedDetail, inspectErr := store.InspectDiagnosticDetail(problemID)
	if err != nil || unchanged.Outcomes != (OutcomeCounts{UnchangedSnapshot: 1}) || inspectErr != nil ||
		unchangedDetail.Condition.State != DiagnosticConditionResolved || !unchangedDetail.Condition.AsOf.Equal(resolvedAt) {
		t.Fatalf("restart unchanged check = %+v detail=%+v pollErr=%v inspectErr=%v", unchanged, unchangedDetail.Condition, err, inspectErr)
	}
}

func TestCodexReasoningMetadataRecoveryDoesNotDoubleCountLaterPersistedSnapshot(t *testing.T) {
	database := filepath.Join(t.TempDir(), "reasoning-recovery-later-event.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	path := filepath.Join(t.TempDir(), "accepted-rejected-accepted.jsonl")
	metadata := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"session-recovery-later","project_id":"project-recovery-later","model":"gpt-test"}}` + "\n"
	snapshot := func(timestamp string, input, output int, reasoningField string) string {
		return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","session_id":"session-recovery-later","info":{"total_token_usage":{"input_tokens":` + decimal(input) + `,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":` + decimal(output) + `,"reasoning_output_tokens":` + decimal(input/10) + `,"total_tokens":` + decimal(input+output) + `},"last_token_usage":{"input_tokens":` + decimal(input) + `,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":` + decimal(output) + `,"reasoning_output_tokens":` + decimal(input/10) + `,"total_tokens":` + decimal(input+output) + `},"model_context_window":258400` + reasoningField + `}}}` + "\n"
	}
	first := snapshot("2026-10-11T00:00:01Z", 100, 10, "")
	second := snapshot("2026-10-11T00:00:02Z", 150, 15, `,"last_usage_includes_reasoning":false`)
	third := snapshot("2026-10-11T00:00:03Z", 200, 20, "")
	prefix := metadata + first
	if err := os.WriteFile(path, []byte(prefix), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := collector.PollOnce()
	if err != nil || initial.Receipt != (Receipt{Imported: 1}) {
		t.Fatalf("initial historical event = %+v, err = %v", initial, err)
	}

	// Seed the durable state produced by the pre-fix parser: the middle
	// snapshot was rejected, while the later stable record stored its full
	// 100/10 delta from the previously accepted 100/10 baseline.
	fullSource := prefix + second + third
	thirdOffset := int64(len(prefix + second))
	sourceID := "codex-file-" + digest([]byte(mustAbsPath(t, path)))
	var persistedProjectID, persistedSessionID string
	if err := store.db.QueryRow(`SELECT project_id, session_id FROM usage_events LIMIT 1`).Scan(&persistedProjectID, &persistedSessionID); err != nil {
		t.Fatalf("read persisted references: %v", err)
	}
	thirdLine := strings.TrimSuffix(third, "\n")
	thirdEvent := codexEventObserved(sourceID, digest([]byte(fullSource)), thirdOffset,
		"offset-"+decimal(int(thirdOffset)), codexSession{ID: persistedSessionID, ProjectID: persistedProjectID, Model: "gpt-test"},
		codexUsage{InputTokens: 100, OutputTokens: 10, ReasoningOutputTokens: 10},
		time.Date(2026, 10, 11, 0, 0, 3, 0, time.UTC), []byte(thirdLine), nil)
	if _, err := store.Append([]UsageEvent{thirdEvent}); err != nil {
		t.Fatalf("seed pre-fix later event: %v", err)
	}
	if err := os.WriteFile(path, []byte(fullSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE source_cursors SET file_fingerprint = ?, byte_offset = ?, snapshot_reusable = 0, pending_tail = 0 WHERE source_id = ?`,
		digest([]byte(fullSource)), len(fullSource), sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM parser_checkpoints WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}

	recovered, err := collector.PollOnce()
	if err != nil || recovered.Receipt != (Receipt{Duplicates: 3}) ||
		recovered.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1, UnchangedSnapshot: 1}) {
		t.Fatalf("candidate upgrade replay = %+v, err = %v; want no new events and replay-equivalent duplicate accounting", recovered, err)
	}
	assertCodexRecoveryTotals(t, store, 2, 200, 20)

	unchanged, err := collector.PollOnce()
	if err != nil || unchanged.Receipt != (Receipt{}) || unchanged.Outcomes != (OutcomeCounts{UnchangedSnapshot: 1}) {
		t.Fatalf("unchanged post-recovery poll = %+v, err = %v", unchanged, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	collector, err = NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := collector.PollOnce(); err != nil || result.Receipt != (Receipt{}) {
		t.Fatalf("recreated collector poll = %+v, err = %v", result, err)
	}

	fourth := snapshot("2026-10-11T00:00:04Z", 250, 25, "")
	if err := os.WriteFile(path, []byte(fullSource+fourth), 0o600); err != nil {
		t.Fatal(err)
	}
	appended, err := collector.PollOnce()
	if err != nil || appended.Receipt != (Receipt{Imported: 1, Duplicates: 3}) {
		t.Fatalf("append after recovered upgrade = %+v, err = %v", appended, err)
	}
	assertCodexRecoveryTotals(t, store, 3, 250, 25)
}

func TestCodexReasoningMetadataRecoveryLeavesAmbiguousResetUncommitted(t *testing.T) {
	database := filepath.Join(t.TempDir(), "reasoning-recovery-reset.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	path := filepath.Join(t.TempDir(), "accepted-reset-accepted.jsonl")
	metadata := `{"type":"session_meta","timestamp":"2026-10-11T00:00:00Z","payload":{"id":"session-recovery-reset","project_id":"project-recovery-reset","model":"gpt-test"}}` + "\n"
	snapshot := func(timestamp string, input, cached, writes, output, reasoning int, flag string) string {
		lastUsageMetadata := ""
		if flag != "" {
			lastUsageMetadata = `,"last_usage_includes_reasoning":` + flag
		}
		usage := `"input_tokens":` + decimal(input) + `,"cached_input_tokens":` + decimal(cached) +
			`,"cache_write_input_tokens":` + decimal(writes) + `,"output_tokens":` + decimal(output) +
			`,"reasoning_output_tokens":` + decimal(reasoning) + `,"total_tokens":` + decimal(input+output)
		return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","session_id":"session-recovery-reset","info":{"total_token_usage":{` + usage + `},"last_token_usage":{` + usage + `},"model_context_window":258400` + lastUsageMetadata + `}}}` + "\n"
	}
	first := snapshot("2026-10-11T00:00:01Z", 100, 20, 4, 10, 2, "")
	second := snapshot("2026-10-11T00:00:02Z", 50, 10, 2, 5, 1, "false")
	third := snapshot("2026-10-11T00:00:03Z", 150, 30, 6, 15, 3, "")
	prefix := metadata + first
	if err := os.WriteFile(path, []byte(prefix), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := collector.PollOnce(); err != nil || result.Receipt != (Receipt{Imported: 1}) {
		t.Fatalf("initial event = %+v, err = %v", result, err)
	}
	fullSource := prefix + second + third
	thirdOffset := int64(len(prefix + second))
	sourceID := "codex-file-" + digest([]byte(mustAbsPath(t, path)))
	var projectID, sessionID string
	if err := store.db.QueryRow(`SELECT project_id, session_id FROM usage_events LIMIT 1`).Scan(&projectID, &sessionID); err != nil {
		t.Fatalf("read persisted references: %v", err)
	}
	thirdEvent := codexEventObserved(sourceID, digest([]byte(fullSource)), thirdOffset,
		"offset-"+decimal(int(thirdOffset)), codexSession{ID: sessionID, ProjectID: projectID, Model: "gpt-test"},
		codexUsage{InputTokens: 50, CachedInputTokens: 10, CacheWriteTokens: 2, OutputTokens: 5, ReasoningOutputTokens: 1},
		time.Date(2026, 10, 11, 0, 0, 3, 0, time.UTC), []byte(strings.TrimSuffix(third, "\n")), nil)
	if _, err := store.Append([]UsageEvent{thirdEvent}); err != nil {
		t.Fatalf("seed pre-fix later event: %v", err)
	}
	if err := os.WriteFile(path, []byte(fullSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE source_cursors SET file_fingerprint = ?, byte_offset = ?, snapshot_reusable = 0, pending_tail = 0 WHERE source_id = ?`,
		digest([]byte(fullSource)), len(fullSource), sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM parser_checkpoints WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	beforeCursor, err := store.Cursor(sourceID)
	if err != nil {
		t.Fatal(err)
	}

	assertBlocked := func(label string) {
		t.Helper()
		result, pollErr := collector.PollOnce()
		if pollErr == nil || !strings.Contains(pollErr.Error(), errCodexLegacyRecoveryAmbiguous.Error()) || result.Outcomes.FailedSource != 1 {
			t.Fatalf("%s ambiguous reset replay = %+v, err = %v; want explicit failed-source recovery", label, result, pollErr)
		}
		assertCodexDetailedTotals(t, store, [6]int64{2, 150, 30, 6, 15, 3})
		var input, output, cached, writes, write5m, write1h, reasoning int64
		if err := store.db.QueryRow(`SELECT input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			cache_write_5m_tokens, cache_write_1h_tokens, reasoning_tokens FROM usage_events WHERE ingestion_key = ?`,
			thirdEvent.IngestionKey).Scan(&input, &output, &cached, &writes, &write5m, &write1h, &reasoning); err != nil {
			t.Fatalf("%s read preserved later event: %v", label, err)
		}
		if got := [7]int64{input, output, cached, writes, write5m, write1h, reasoning}; got != [7]int64{50, 5, 10, 2, 0, 0, 1} {
			t.Fatalf("%s ambiguous recovery changed the existing event vector: got %v", label, got)
		}
		afterCursor, err := store.Cursor(sourceID)
		if err != nil || afterCursor != beforeCursor {
			t.Fatalf("%s ambiguous recovery advanced cursor: before=%+v after=%+v err=%v", label, beforeCursor, afterCursor, err)
		}
	}
	assertBlocked("first poll")
	assertBlocked("repeat poll")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	collector, err = NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked("recreated collector")
	fourth := snapshot("2026-10-11T00:00:04Z", 200, 40, 8, 20, 4, "")
	if err := os.WriteFile(path, []byte(fullSource+fourth), 0o600); err != nil {
		t.Fatal(err)
	}
	assertBlocked("later append")
}

func assertCodexDetailedTotals(t *testing.T, store *Store, want [6]int64) {
	t.Helper()
	var count, input, cached, writes, output, reasoning int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_write_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(reasoning_tokens), 0) FROM usage_events`).Scan(&count, &input, &cached, &writes, &output, &reasoning); err != nil {
		t.Fatal(err)
	}
	got := [6]int64{count, input, cached, writes, output, reasoning}
	if got != want {
		t.Fatalf("Codex exact totals = %v, want %v", got, want)
	}
}

func assertCodexRecoveryTotals(t *testing.T, store *Store, count, input, output int64) {
	t.Helper()
	var gotCount, gotInput, gotOutput int64
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM usage_events`).Scan(&gotCount, &gotInput, &gotOutput); err != nil {
		t.Fatal(err)
	}
	if [3]int64{gotCount, gotInput, gotOutput} != [3]int64{count, input, output} {
		t.Fatalf("Codex recovery totals = [%d %d %d], want [%d %d %d]", gotCount, gotInput, gotOutput, count, input, output)
	}
}

func mustAbsPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}
