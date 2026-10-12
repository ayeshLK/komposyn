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
	"testing"
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
	if result.Receipt.Imported != 0 || result.Receipt.Duplicates != 2 {
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
	if err != nil || repeat.Receipt.Imported != 0 || repeat.Receipt.Duplicates != 1 {
		t.Fatalf("replacement re-import = %+v, err = %v; want one duplicate", repeat, err)
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
	if repeat.Receipt.Imported != 0 || repeat.Receipt.Duplicates != 3 {
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
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events WHERE session_id = 'session-2'`).Scan(&count); err != nil {
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
