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
