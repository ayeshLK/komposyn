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
)

func TestClaudeImportAttributesAndNormalizesUsage(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join("testdata", "claude-code", "normal.jsonl")
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("import Claude fixture: %v", err)
	}
	if result.Receipt.Imported != 2 || result.Outcomes.IgnoredNonUsage != 2 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	var input, cached, cacheWrite, output int64
	var client, provider, project, session string
	err = store.db.QueryRow(`SELECT input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, client_name, provider_name, project_id, session_id FROM usage_events ORDER BY occurred_at`).Scan(&input, &cached, &cacheWrite, &output, &client, &provider, &project, &session)
	if err != nil {
		t.Fatal(err)
	}
	var aliasedProject, aliasedSession string
	if err := store.db.QueryRow("SELECT project_id FROM project_aliases WHERE scope = 'claude-code' AND alias = 'claude-project-fixture'").Scan(&aliasedProject); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT session_id FROM session_aliases WHERE scope = 'claude-code:claude-code' AND alias = 'claude-session-fixture'").Scan(&aliasedSession); err != nil {
		t.Fatal(err)
	}
	if input != 110 || cached != 20 || cacheWrite != 10 || output != 15 || client != "claude-code" || provider != "anthropic" || project != aliasedProject || session != aliasedSession {
		t.Fatalf("unexpected Claude event: input=%d cached=%d cacheWrite=%d output=%d client=%s provider=%s project=%s session=%s", input, cached, cacheWrite, output, client, provider, project, session)
	}
	second, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatal(err)
	}
	if second.Receipt != (Receipt{}) || second.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("unexpected re-import result: %+v", second)
	}
}

func TestClaudeImportAcceptsNewerUsageFields(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "newer-usage-fields.jsonl")
	record := `{"type":"assistant","uuid":"u1","sessionId":"s1","timestamp":"2026-10-11T00:00:00Z","message":{"role":"assistant","model":"claude-x","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":2},"server_tool_use":{"web_search_requests":1},"output_tokens_details":{"reasoning_tokens":3},"iterations":1,"inference_geo":"us","speed":"fast"}}}` + "\n"
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes != (OutcomeCounts{}) {
		t.Fatalf("newer usage fields result = %+v, err = %v", result, err)
	}
	var input, output int64
	if err := store.db.QueryRow(`SELECT input_tokens, output_tokens FROM usage_events`).Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if input != 13 || output != 5 {
		t.Fatalf("usage = input %d, output %d; want 13, 5", input, output)
	}
}

func TestDetectClaudeAdapter(t *testing.T) {
	path := filepath.Join("testdata", "claude-code", "normal.jsonl")
	adapter, err := DetectAdapter(path)
	if err != nil || adapter != "claude" {
		t.Fatalf("adapter=%q err=%v", adapter, err)
	}
}

func TestClaudeImportDetectsReplacementWithProviderID(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	message := func(input int) string {
		return `{"type":"assistant","uuid":"provider-record-1","sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"input_tokens":` + string(rune('0'+input)) + `,"output_tokens":1}}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(message(1)), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || first.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", first, err)
	}
	if err := os.WriteFile(path, []byte(message(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || replacement.Receipt.Imported != 1 || replacement.Receipt.Duplicates != 0 {
		t.Fatalf("replacement import = %+v, err = %v; want one new event", replacement, err)
	}
	repeat, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || repeat.Receipt != (Receipt{}) || repeat.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("replacement re-import = %+v, err = %v; want unchanged snapshot", repeat, err)
	}
	if count, err := store.EventCount(); err != nil || count != 2 {
		t.Fatalf("event count = %d, err = %v; want 2", count, err)
	}
}

func TestConcurrentClaudeReplacementsRevalidateCursorBeforeCommit(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	stores := make([]*Store, 3)
	for index := range stores {
		store, err := Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
		defer store.Close()
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	message := func(input int) []byte {
		return []byte(fmt.Sprintf(`{"type":"assistant","uuid":"provider-record-1","sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", input))
	}
	if err := os.WriteFile(path, message(110), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", result, err)
	}

	type importResult struct {
		result ImportResult
		err    error
	}
	installPause := func(store *Store) (<-chan struct{}, chan<- struct{}) {
		reached := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		store.beforeAppend = func() {
			once.Do(func() {
				close(reached)
				<-release
			})
		}
		return reached, release
	}
	bReached, releaseB := installPause(stores[1])
	cReached, releaseC := installPause(stores[2])

	if err := os.WriteFile(path, message(111), 0o600); err != nil {
		t.Fatal(err)
	}
	bDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[1])
		bDone <- importResult{result: result, err: err}
	}()
	<-bReached

	if err := os.WriteFile(path, message(112), 0o600); err != nil {
		t.Fatal(err)
	}
	cDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[2])
		cDone <- importResult{result: result, err: err}
	}()
	<-cReached

	close(releaseB)
	b := <-bDone
	if b.err != nil || b.result.Receipt.Imported != 1 || b.result.Receipt.Duplicates != 0 {
		t.Fatalf("first replacement = %+v, err = %v", b.result, b.err)
	}
	close(releaseC)
	c := <-cDone
	if c.err != nil || c.result.Receipt.Imported != 1 || c.result.Receipt.Duplicates != 0 {
		t.Fatalf("stale replacement retry = %+v, err = %v", c.result, c.err)
	}

	if count, err := stores[0].EventCount(); err != nil || count != 3 {
		t.Fatalf("event count = %d, err = %v; want 3", count, err)
	}
	source, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	baseSourceID, err := establishedCollectorSourceID(stores[0], source, "claude")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := stores[0].Cursor(baseSourceID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.SourceGeneration != 2 || cursor.FileFingerprint != digest(message(112)) || !cursor.SnapshotReusable {
		t.Fatalf("latest cursor = %+v; want reusable generation 2 for final replacement", cursor)
	}
	var latestInput int64
	if err := stores[0].db.QueryRow(`SELECT input_tokens FROM usage_events WHERE source_id = ?`, generationSourceID(baseSourceID, 2)).Scan(&latestInput); err != nil {
		t.Fatal(err)
	}
	if latestInput != 112 {
		t.Fatalf("latest replacement input = %d, want 112", latestInput)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("final retry = %+v, err = %v", result, err)
	}
}
