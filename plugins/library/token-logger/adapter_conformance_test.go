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
	"os"
	"path/filepath"
	"testing"
)

type conformanceAdapter struct {
	name        string
	importFile  func(string, *Store) (ImportResult, error)
	header      string
	usage       func(string, int) string
	unsupported string
	missing     string
}

func TestAdapterOutcomeConformance(t *testing.T) {
	adapters := []conformanceAdapter{
		{
			name: "codex",
			importFile: func(path string, store *Store) (ImportResult, error) {
				return (CodexAdapter{}).Import(path, store)
			},
			header: `{"type":"session_meta","payload":{"id":"session","project_id":"project","project_name":"Project","model":"gpt-test"}}` + "\n",
			usage: func(id string, input int) string {
				return `{"type":"event_msg","id":"` + id + `","timestamp":"2026-10-11T00:00:00Z","payload":{"type":"token_count","session_id":"session","info":{"total_token_usage":{"input_tokens":` + decimal(input) + `,"output_tokens":1}}}}` + "\n"
			},
			unsupported: `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","input_tokens":1}` + "\n",
			missing:     `{"type":"token_count","timestamp":"2026-10-11T00:00:00Z","payload":{"total_token_usage":{"input_tokens":1}}}` + "\n",
		},
		{
			name: "claude",
			importFile: func(path string, store *Store) (ImportResult, error) {
				return (ClaudeAdapter{}).Import(path, store)
			},
			header: `{"type":"user","sessionId":"session","cwd":"/sanitized/project","project_id":"project","project_name":"Project","timestamp":"2026-10-11T00:00:00Z"}` + "\n",
			usage: func(id string, input int) string {
				return `{"type":"assistant","uuid":"` + id + `","sessionId":"session","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"input_tokens":` + decimal(input) + `,"output_tokens":1}}}` + "\n"
			},
			unsupported: `{"type":"assistant","uuid":"future-usage","sessionId":"session","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"future_token_units":1}}}` + "\n",
			missing:     `{"type":"assistant","uuid":"missing-model","sessionId":"session","timestamp":"2026-10-11T00:00:00Z","message":{"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n",
		},
	}

	for _, adapter := range adapters {
		adapter := adapter
		t.Run(adapter.name, func(t *testing.T) {
			runAdapterConformance(t, adapter)
		})
	}
}

func runAdapterConformance(t *testing.T, adapter conformanceAdapter) {
	t.Helper()

	t.Run("initial reimport append replacement and immutable source", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "source.jsonl")
		initial := []byte(adapter.header + adapter.usage("one", 10))
		writeFixture(t, path, initial)
		first, err := adapter.importFile(path, store)
		if err != nil || first.Receipt.Imported != 1 {
			t.Fatalf("initial result=%+v err=%v", first, err)
		}
		assertSourceUnchanged(t, path, initial)
		repeat, err := adapter.importFile(path, store)
		if err != nil || repeat.Receipt.Duplicates != 1 || repeat.Receipt.Imported != 0 {
			t.Fatalf("repeat result=%+v err=%v", repeat, err)
		}
		appended := []byte(adapter.header + adapter.usage("one", 10) + adapter.usage("two", 20))
		writeFixture(t, path, appended)
		appendResult, err := adapter.importFile(path, store)
		if err != nil || appendResult.Receipt.Imported != 1 || appendResult.Receipt.Duplicates != 1 {
			t.Fatalf("append result=%+v err=%v", appendResult, err)
		}
		reset := []byte(adapter.header + adapter.usage("one", 10) + adapter.usage("two", 20) + adapter.usage("reset", 5))
		writeFixture(t, path, reset)
		resetResult, err := adapter.importFile(path, store)
		if err != nil || resetResult.Receipt.Imported != 1 || resetResult.Receipt.Duplicates != 2 {
			t.Fatalf("reset result=%+v err=%v", resetResult, err)
		}
		replacement := []byte(adapter.header + adapter.usage("replacement", 30))
		writeFixture(t, path, replacement)
		replaced, err := adapter.importFile(path, store)
		if err != nil || replaced.Receipt.Imported != 1 || replaced.Receipt.Duplicates != 0 {
			t.Fatalf("replacement result=%+v err=%v", replaced, err)
		}
		assertSourceUnchanged(t, path, replacement)
	})

	t.Run("malformed interior differs from retryable tail", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "source.jsonl")
		contents := []byte(adapter.header + "{malformed}\n" + adapter.usage("valid", 10))
		writeFixture(t, path, contents)
		result, err := adapter.importFile(path, store)
		if err != nil || result.Receipt.Imported != 1 || result.Outcomes.MalformedRecord != 1 || result.Outcomes.DeferredIncompleteTail != 0 {
			t.Fatalf("interior malformed result=%+v err=%v", result, err)
		}

		partialPath := filepath.Join(t.TempDir(), "partial.jsonl")
		complete := adapter.header + adapter.usage("tail", 10)
		partial := complete[:len(complete)-4]
		writeFixture(t, partialPath, []byte(partial))
		pending, err := adapter.importFile(partialPath, store)
		if err != nil || pending.Outcomes.DeferredIncompleteTail != 1 || pending.Outcomes.MalformedRecord != 0 {
			t.Fatalf("partial result=%+v err=%v", pending, err)
		}
		writeFixture(t, partialPath, []byte(complete))
		completed, err := adapter.importFile(partialPath, store)
		if err != nil || completed.Receipt.Imported != 1 || completed.Outcomes.DeferredIncompleteTail != 0 {
			t.Fatalf("completed tail result=%+v err=%v", completed, err)
		}
	})

	t.Run("unsupported usage and missing attribution are warnings", func(t *testing.T) {
		unsupportedStore := openTestStore(t)
		unsupportedPath := filepath.Join(t.TempDir(), "unsupported.jsonl")
		unsupportedContents := []byte(adapter.header + adapter.unsupported)
		writeFixture(t, unsupportedPath, unsupportedContents)
		unsupported, err := adapter.importFile(unsupportedPath, unsupportedStore)
		if err != nil || unsupported.Outcomes.UnsupportedUsage != 1 || !unsupported.Outcomes.Incomplete() {
			t.Fatalf("unsupported result=%+v err=%v", unsupported, err)
		}
		assertSourceUnchanged(t, unsupportedPath, unsupportedContents)

		missingStore := openTestStore(t)
		missingPath := filepath.Join(t.TempDir(), "missing.jsonl")
		missingContents := []byte(adapter.missing)
		writeFixture(t, missingPath, missingContents)
		missing, err := adapter.importFile(missingPath, missingStore)
		if err != nil || missing.Outcomes.MalformedRecord != 1 || !missing.Outcomes.Incomplete() {
			t.Fatalf("missing attribution result=%+v err=%v", missing, err)
		}
		assertSourceUnchanged(t, missingPath, missingContents)
	})

	t.Run("storage failure does not mutate source", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "source.jsonl")
		contents := []byte(adapter.header + adapter.usage("one", 10))
		writeFixture(t, path, contents)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.importFile(path, store); err == nil {
			t.Fatal("import unexpectedly succeeded with closed storage")
		}
		assertSourceUnchanged(t, path, contents)
	})
}

func decimal(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

func writeFixture(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSourceUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("adapter modified its provider-owned source")
	}
}
