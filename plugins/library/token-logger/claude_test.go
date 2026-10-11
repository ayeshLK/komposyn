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
	"path/filepath"
	"testing"
)

func TestClaudeImportAttributesAndNormalizesUsage(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join("testdata", "claude-code", "normal.jsonl")
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("import Claude fixture: %v", err)
	}
	if result.Receipt.Imported != 2 || result.Skipped != 2 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	var input, cached, cacheWrite, output int64
	var client, provider, project, session string
	err = store.db.QueryRow(`SELECT input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, client_name, provider_name, project_id, session_id FROM usage_events ORDER BY occurred_at`).Scan(&input, &cached, &cacheWrite, &output, &client, &provider, &project, &session)
	if err != nil {
		t.Fatal(err)
	}
	if input != 110 || cached != 20 || cacheWrite != 10 || output != 15 || client != "claude-code" || provider != "anthropic" || project != "claude-project-fixture" || session != "claude-session-fixture" {
		t.Fatalf("unexpected Claude event: input=%d cached=%d cacheWrite=%d output=%d client=%s provider=%s project=%s session=%s", input, cached, cacheWrite, output, client, provider, project, session)
	}
	second, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatal(err)
	}
	if second.Receipt.Imported != 0 || second.Receipt.Duplicates != 2 {
		t.Fatalf("unexpected re-import result: %+v", second)
	}
}

func TestDetectClaudeAdapter(t *testing.T) {
	path := filepath.Join("testdata", "claude-code", "normal.jsonl")
	adapter, err := DetectAdapter(path)
	if err != nil || adapter != "claude" {
		t.Fatalf("adapter=%q err=%v", adapter, err)
	}
}
