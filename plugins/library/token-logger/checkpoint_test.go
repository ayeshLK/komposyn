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
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParserCheckpointContinuationSkipsHistory(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			initial := benchmarkGeneratedHistory(provider, "checkpoint-session", 12)
			suffix := benchmarkGeneratedUsage(provider, "checkpoint-session", 13)
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			store := openTestStore(t)
			collector, err := NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			first, err := collector.PollOnce()
			if err != nil || first.Receipt.Imported != 12 {
				t.Fatalf("initial import=%+v err=%v", first, err)
			}
			var checkpointCount int
			if err := store.db.QueryRow("SELECT COUNT(*) FROM parser_checkpoints").Scan(&checkpointCount); err != nil || checkpointCount != 1 {
				t.Fatalf("checkpoint count=%d err=%v", checkpointCount, err)
			}
			ids := checkpointTestEventIDs(t, store)
			observer := &collectorBenchmarkObserver{historicalEventIDs: ids}
			store.benchmarkObserver = observer
			if err := os.WriteFile(path, append(append([]byte(nil), initial...), suffix...), 0o600); err != nil {
				t.Fatal(err)
			}
			// Recreate the collector to prove the state belongs to the database,
			// not to a particular process object.
			collector, err = NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			got, err := collector.PollOnce()
			if err != nil {
				t.Fatal(err)
			}
			if got.Receipt != (Receipt{Imported: 1, Duplicates: 12}) {
				t.Fatalf("append receipt=%+v, want one new and twelve replay-equivalent duplicates", got.Receipt)
			}
			if observer.parserRecordsDecoded != 1 || observer.historicalInsertAttempts != 0 || observer.eventInsertAttempts != 1 {
				t.Fatalf("append work parser=%d historical inserts=%d inserts=%d", observer.parserRecordsDecoded, observer.historicalInsertAttempts, observer.eventInsertAttempts)
			}
		})
	}
}

func TestParserCheckpointExcludesRawSourceContent(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			contents := append(benchmarkGeneratedHistory(provider, "privacy-session", 1), benchmarkGeneratedNonUsage(provider, "privacy-session")...)
			contents = append(contents, []byte("\n")...)
			const marker = "generated benchmark data"
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			store := openTestStore(t)
			defer store.Close()
			collector, err := NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.PollOnce(); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := store.db.QueryRow("SELECT CAST(state_json AS TEXT) FROM parser_checkpoints LIMIT 1").Scan(&state); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(state, marker) {
				t.Fatalf("checkpoint retained provider source content: %q", marker)
			}
		})
	}
}

func TestParserCheckpointRereadsUnterminatedRecord(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			initial := benchmarkGeneratedHistory(provider, "tail-session", 2)
			unterminated := benchmarkGeneratedUsage(provider, "tail-session", 3)
			unterminated = unterminated[:len(unterminated)-1]
			firstBytes := append(append([]byte(nil), initial...), unterminated...)
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, firstBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			store := openTestStore(t)
			defer store.Close()
			collector, err := NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			first, err := collector.PollOnce()
			if err != nil || first.Receipt.Imported != 3 {
				t.Fatalf("unterminated import=%+v err=%v", first, err)
			}
			var boundary, cursorOffset int64
			if err := store.db.QueryRow("SELECT boundary_offset FROM parser_checkpoints").Scan(&boundary); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow("SELECT byte_offset FROM source_cursors LIMIT 1").Scan(&cursorOffset); err != nil {
				t.Fatal(err)
			}
			if boundary >= cursorOffset {
				t.Fatalf("checkpoint boundary=%d must precede unterminated tail at cursor=%d", boundary, cursorOffset)
			}
			observer := &collectorBenchmarkObserver{historicalEventIDs: checkpointTestEventIDs(t, store)}
			store.benchmarkObserver = observer
			secondRecord := benchmarkGeneratedUsage(provider, "tail-session", 4)
			completed := append(append(append([]byte(nil), firstBytes...), '\n'), secondRecord...)
			if err := os.WriteFile(path, completed, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := collector.PollOnce()
			if err != nil {
				t.Fatal(err)
			}
			if got.Receipt != (Receipt{Imported: 1, Duplicates: 3}) {
				t.Fatalf("completed-tail receipt=%+v", got.Receipt)
			}
			if observer.historicalInsertAttempts != 1 || observer.eventInsertAttempts != 2 {
				t.Fatalf("tail continuation historical=%d inserts=%d; completed tail is deliberately replayed once", observer.historicalInsertAttempts, observer.eventInsertAttempts)
			}
		})
	}
}

func TestParserCheckpointFallbackCases(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		for _, fallback := range []string{"corrupt-state", "oversized-state", "old-parser", "old-format", "prefix-mismatch", "interior-boundary", "missing"} {
			t.Run(provider+"/"+fallback, func(t *testing.T) {
				initial := benchmarkGeneratedHistory(provider, "corrupt-session", 8)
				path := filepath.Join(t.TempDir(), "source.jsonl")
				if err := os.WriteFile(path, initial, 0o600); err != nil {
					t.Fatal(err)
				}
				store := openTestStore(t)
				defer store.Close()
				collector, err := NewCollector(store, []string{path}, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := collector.PollOnce(); err != nil {
					t.Fatal(err)
				}
				switch fallback {
				case "corrupt-state":
					_, err = store.db.Exec("UPDATE parser_checkpoints SET state_json = ?", []byte("not-json"))
				case "oversized-state":
					_, err = store.db.Exec("UPDATE parser_checkpoints SET state_json = zeroblob(?)", maxCheckpointStateBytes+1)
				case "old-parser":
					_, err = store.db.Exec("UPDATE parser_checkpoints SET parser_revision = parser_revision + 1")
				case "old-format":
					_, err = store.db.Exec("UPDATE parser_checkpoints SET format_version = format_version + 1")
				case "prefix-mismatch":
					_, err = store.db.Exec("UPDATE parser_checkpoints SET prefix_fingerprint = ?", strings.Repeat("0", 64))
				case "interior-boundary":
					state := parserCheckpointState{}
					if provider == "claude" {
						state.Claude = &claudeCheckpointState{}
					} else {
						state.Codex = &codexCheckpointState{}
					}
					encoded, ok := encodeParserCheckpoint(parserCheckpoint{Adapter: provider, State: state})
					if !ok {
						t.Fatal("could not encode empty checkpoint state")
					}
					_, err = store.db.Exec("UPDATE parser_checkpoints SET boundary_offset = 1, prefix_fingerprint = ?, state_json = ?, state_digest = ?",
						digest(initial[:1]), encoded.StateJSON, encoded.StateDigest)
				case "missing":
					_, err = store.db.Exec("DELETE FROM parser_checkpoints")
				}
				if err != nil {
					t.Fatal(err)
				}
				observer := &collectorBenchmarkObserver{historicalEventIDs: checkpointTestEventIDs(t, store)}
				store.benchmarkObserver = observer
				if err := os.WriteFile(path, append(append([]byte(nil), initial...), benchmarkGeneratedUsage(provider, "corrupt-session", 9)...), 0o600); err != nil {
					t.Fatal(err)
				}
				got, err := collector.PollOnce()
				if err != nil || got.Receipt != (Receipt{Imported: 1, Duplicates: 8}) {
					t.Fatalf("fallback import=%+v err=%v", got, err)
				}
				if observer.historicalInsertAttempts != 8 || observer.parserRecordsDecoded <= 8 {
					t.Fatalf("fallback work historical=%d parser=%d", observer.historicalInsertAttempts, observer.parserRecordsDecoded)
				}
			})
		}
	}
}

func TestCodexCheckpointPreservesResponseAndCumulativeState(t *testing.T) {
	for _, scenario := range []string{"duplicate-response", "conflicting-response", "cumulative-reset"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex.jsonl")
			header := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}` + "\n"
			firstRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":10,"output_tokens":1}}}` + "\n"
			if err := os.WriteFile(path, []byte(header+firstRecord), 0o600); err != nil {
				t.Fatal(err)
			}
			store := openTestStore(t)
			defer store.Close()
			if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 1 {
				t.Fatalf("initial import=%+v err=%v", result, err)
			}
			var suffix string
			switch scenario {
			case "duplicate-response":
				suffix = firstRecord
			case "conflicting-response":
				suffix = `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":11,"output_tokens":1},"turn_token_usage":{"input_tokens":11,"output_tokens":1},"thread_token_usage":{"input_tokens":11,"output_tokens":1}}}` + "\n"
			case "cumulative-reset":
				suffix = `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-2","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":9,"output_tokens":1},"thread_token_usage":{"input_tokens":9,"output_tokens":1}}}` + "\n" +
					`{"type":"token_usage_record","timestamp":"2026-10-11T00:00:03Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-3","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":19,"output_tokens":2},"thread_token_usage":{"input_tokens":19,"output_tokens":2}}}` + "\n"
			}
			observer := &collectorBenchmarkObserver{historicalEventIDs: checkpointTestEventIDs(t, store)}
			store.benchmarkObserver = observer
			if err := os.WriteFile(path, []byte(header+firstRecord+suffix), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (CodexAdapter{}).Import(path, store)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "duplicate-response":
				if result.Receipt != (Receipt{Duplicates: 2}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1}) {
					t.Fatalf("duplicate result=%+v", result)
				}
			case "conflicting-response":
				if result.Receipt != (Receipt{Duplicates: 1}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1, UnsupportedUsage: 1}) {
					t.Fatalf("conflict result=%+v", result)
				}
			case "cumulative-reset":
				if result.Receipt != (Receipt{Imported: 1, Duplicates: 1}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 1, UnsupportedUsage: 1}) {
					t.Fatalf("reset result=%+v", result)
				}
			}
			wantHistoricalAttempts := 0
			if scenario == "duplicate-response" {
				// The suffix itself repeats a response already imported in the
				// prefix, so its ordinary dedup insert is expected.
				wantHistoricalAttempts = 1
			}
			if observer.historicalInsertAttempts != int64(wantHistoricalAttempts) || observer.parserRecordsDecoded != int64(strings.Count(suffix, "\n")) || observer.eventInsertAttempts > 2 {
				t.Fatalf("continuation work historical=%d parser=%d insert=%d", observer.historicalInsertAttempts, observer.parserRecordsDecoded, observer.eventInsertAttempts)
			}
		})
	}
}

func TestCodexCheckpointCapturesEveryProcessedBoundaryBeforeTail(t *testing.T) {
	for _, scenario := range []string{"duplicate-response", "conflicting-response", "cumulative-reset"} {
		for _, tailKind := range []string{"valid-unterminated", "incomplete"} {
			t.Run(scenario+"/"+tailKind, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "codex.jsonl")
				header := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project","cwd":"/tmp/project","model":"gpt-test"}}` + "\n"
				firstRecord := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":10,"output_tokens":1}}}` + "\n"
				base := []byte(header + firstRecord)
				if err := os.WriteFile(path, base, 0o600); err != nil {
					t.Fatal(err)
				}
				store := openTestStore(t)
				defer store.Close()
				collector, err := NewCollector(store, []string{path}, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if result, err := collector.PollOnce(); err != nil || result.Receipt.Imported != 1 {
					t.Fatalf("initial import=%+v err=%v", result, err)
				}
				var initialCheckpointCount int
				if err := store.db.QueryRow("SELECT COUNT(*) FROM parser_checkpoints").Scan(&initialCheckpointCount); err != nil || initialCheckpointCount != 1 {
					t.Fatalf("initial checkpoint count=%d err=%v", initialCheckpointCount, err)
				}

				var boundaryRecord string
				switch scenario {
				case "duplicate-response":
					boundaryRecord = firstRecord
				case "conflicting-response":
					boundaryRecord = `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-1","usage":{"input_tokens":11,"output_tokens":1},"turn_token_usage":{"input_tokens":11,"output_tokens":1},"thread_token_usage":{"input_tokens":11,"output_tokens":1}}}` + "\n"
				case "cumulative-reset":
					boundaryRecord = `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-2","usage":{"input_tokens":1,"output_tokens":1},"turn_token_usage":{"input_tokens":9,"output_tokens":1},"thread_token_usage":{"input_tokens":9,"output_tokens":1}}}` + "\n"
				}
				tailTokens := `"input_tokens":20,"output_tokens":2`
				tailUsage := `"input_tokens":10,"output_tokens":1`
				if scenario == "cumulative-reset" {
					tailTokens = `"input_tokens":10,"output_tokens":2`
					tailUsage = `"input_tokens":1,"output_tokens":1`
				}
				tail := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:03Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"session-1","response_id":"response-3","usage":{` + tailUsage + `},"turn_token_usage":{` + tailTokens + `},"thread_token_usage":{` + tailTokens + `}}}`
				tailRemainder := ""
				if tailKind == "incomplete" {
					tailRemainder = tail[len(tail)-7:]
					tail = tail[:len(tail)-7]
				}
				appended := append(append(append([]byte(nil), base...), []byte(boundaryRecord)...), []byte(tail)...)
				if err := os.WriteFile(path, appended, 0o600); err != nil {
					t.Fatal(err)
				}
				result, err := collector.PollOnce()
				if err != nil {
					t.Fatalf("poll with trailing %s: result=%+v err=%v", tailKind, result, err)
				}
				var boundary int64
				var stateJSON []byte
				if scenario == "duplicate-response" {
					if err := store.db.QueryRow("SELECT boundary_offset, state_json FROM parser_checkpoints").Scan(&boundary, &stateJSON); err != nil {
						t.Fatal(err)
					}
					wantBoundary := int64(len(base) + len(boundaryRecord))
					if boundary != wantBoundary {
						t.Fatalf("checkpoint boundary=%d want=%d", boundary, wantBoundary)
					}
					var state parserCheckpointState
					if err := json.Unmarshal(stateJSON, &state); err != nil {
						t.Fatal(err)
					}
					wantAttempts := 1
					if scenario == "duplicate-response" {
						wantAttempts++
					}
					if state.HistoricalEventAttempts != wantAttempts {
						t.Fatalf("checkpoint historical attempts=%d want=%d state=%+v", state.HistoricalEventAttempts, wantAttempts, state)
					}
					if state.Outcomes.DeferredIncompleteTail != 0 {
						t.Fatalf("checkpoint carried EOF tail outcome into prefix: %+v", state.Outcomes)
					}
				} else {
					var count int
					if err := store.db.QueryRow("SELECT COUNT(*) FROM parser_checkpoints").Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != 0 {
						t.Fatalf("unsafe %s/%s result retained a checkpoint", scenario, tailKind)
					}
				}

				// Complete the pending tail and append another record. The next
				// poll must resume from the state captured at the exact prior LF.
				completion := []byte(tailRemainder + "\n" + `{"type":"event_msg","timestamp":"2026-10-11T00:00:04Z","payload":{"type":"token_count","session_id":"session-1","info":{"total_token_usage":{"input_tokens":21,"output_tokens":3}}}}` + "\n")
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write(completion); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				if result, err := collector.PollOnce(); err != nil {
					t.Fatalf("tail completion and later append: result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestCodexCheckpointReplaysNewSessionScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	first := `{"type":"session_meta","payload":{"id":"session-1","project_id":"project-1","project_name":"Project 1","cwd":"/tmp/project-1","model":"gpt-test"}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-10-11T00:00:01Z","payload":{"type":"token_count","session_id":"session-1","info":{"total_token_usage":{"input_tokens":10,"output_tokens":1}}}}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial import=%+v err=%v", result, err)
	}
	suffix := `{"type":"session_meta","payload":{"id":"session-2","project_id":"project-2","project_name":"Project 2","cwd":"/tmp/project-2","model":"gpt-test"}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-10-11T00:00:02Z","payload":{"type":"token_count","session_id":"session-2","info":{"total_token_usage":{"input_tokens":5,"output_tokens":1}}}}` + "\n"
	observer := &collectorBenchmarkObserver{historicalEventIDs: checkpointTestEventIDs(t, store)}
	store.benchmarkObserver = observer
	if err := os.WriteFile(path, []byte(first+suffix), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (CodexAdapter{}).Import(path, store)
	if err != nil || result.Receipt != (Receipt{Imported: 1, Duplicates: 1}) || result.Outcomes != (OutcomeCounts{IgnoredNonUsage: 2}) {
		t.Fatalf("session transition result=%+v err=%v", result, err)
	}
	if observer.parserRecordsDecoded <= 2 || observer.historicalInsertAttempts != 1 {
		t.Fatalf("session transition should replay parser=%d historical=%d", observer.parserRecordsDecoded, observer.historicalInsertAttempts)
	}
}

func TestParserCheckpointTransactionRollsBackIdentityEventsAndCursor(t *testing.T) {
	contents := benchmarkGeneratedHistory("claude", "rollback-session", 2)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	if _, err := store.db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE INSERT ON parser_checkpoints
		BEGIN SELECT RAISE(ABORT, 'injected checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err == nil {
		t.Fatal("injected checkpoint failure did not fail import")
	}
	var events, cursors, checkpoints, identities, projects, sessions int
	for query, destination := range map[string]*int{
		"SELECT COUNT(*) FROM usage_events":                &events,
		"SELECT COUNT(*) FROM source_cursors":              &cursors,
		"SELECT COUNT(*) FROM parser_checkpoints":          &checkpoints,
		"SELECT COUNT(*) FROM collector_source_identities": &identities,
		"SELECT COUNT(*) FROM projects":                    &projects,
		"SELECT COUNT(*) FROM sessions":                    &sessions,
	} {
		if err := store.db.QueryRow(query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if events != 0 || cursors != 0 || checkpoints != 0 || identities != 0 || projects != 0 || sessions != 0 {
		t.Fatalf("rolled-back state events=%d cursors=%d checkpoints=%d identities=%d projects=%d sessions=%d", events, cursors, checkpoints, identities, projects, sessions)
	}
}

func TestParserCheckpointCancellationLeavesCoupledStateUnchanged(t *testing.T) {
	contents := benchmarkGeneratedHistory("claude", "cancel-session", 2)
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	store.beforeAppend = cancel
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnceContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled poll error=%v; want context.Canceled", err)
	}
	for query, want := range map[string]int{"usage_events": 0, "projects": 0, "sessions": 0, "source_cursors": 0,
		"parser_checkpoints": 0, "collector_source_identities": 0} {
		var got int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s rows=%d, want %d", query, got, want)
		}
	}
}

func checkpointTestEventIDs(t *testing.T, store *Store) map[string]struct{} {
	t.Helper()
	rows, err := store.db.Query("SELECT event_id FROM usage_events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}
