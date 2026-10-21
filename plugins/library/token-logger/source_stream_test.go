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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCollectorRetriesChangesBetweenVerificationAndParse(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	firstEnd := strings.IndexByte(string(original), '\n') + 1
	secondEnd := strings.IndexByte(string(original[firstEnd:]), '\n') + firstEnd + 1
	if firstEnd <= 0 || secondEnd <= firstEnd {
		t.Fatal("fixture does not have expected records")
	}
	cases := []struct {
		name             string
		prepare          func(t *testing.T, path string)
		mutate           func(t *testing.T, path string, info os.FileInfo)
		wantGeneration   int64
		wantFinalContent func([]byte) []byte
	}{
		{
			name: "append",
			prepare: func(t *testing.T, path string) {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write(original); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
			mutate: func(t *testing.T, path string, _ os.FileInfo) {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write(original); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
			wantGeneration: 0,
			wantFinalContent: func(contents []byte) []byte {
				return append(append(append([]byte(nil), contents...), original...), original...)
			},
		},
		{
			name: "truncate",
			prepare: func(t *testing.T, path string) {
				thirdEnd := strings.IndexByte(string(original[secondEnd:]), '\n') + secondEnd + 1
				if err := os.WriteFile(path, original[:thirdEnd], 0o600); err != nil {
					t.Fatal(err)
				}
			},
			mutate: func(t *testing.T, path string, _ os.FileInfo) {
				if err := os.WriteFile(path, original[:secondEnd], 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantGeneration:   1,
			wantFinalContent: func([]byte) []byte { return append([]byte(nil), original[:secondEnd]...) },
		},
		{
			name: "same-size-same-mtime-rewrite",
			mutate: func(t *testing.T, path string, info os.FileInfo) {
				contents, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				contents = []byte(strings.Replace(string(contents), `"input_tokens":81`, `"input_tokens":82`, 1))
				if err := os.WriteFile(path, contents, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			},
			prepare: func(t *testing.T, path string) {
				contents := strings.Replace(string(original), `"input_tokens":80`, `"input_tokens":81`, 1)
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantGeneration: 1,
			wantFinalContent: func([]byte) []byte {
				return []byte(strings.Replace(string(original), `"input_tokens":80`, `"input_tokens":82`, 1))
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			collector, err := NewCollector(store, []string{path}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.PollOnce(); err != nil {
				t.Fatal(err)
			}
			if test.prepare != nil {
				test.prepare(t, path)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			recordAppends := 0
			store.beforeSnapshotParse = func() {
				once.Do(func() { test.mutate(t, path, info) })
			}
			store.beforeAppend = func() { recordAppends++ }
			result, err := collector.PollOnce()
			if err != nil {
				t.Fatalf("poll after mutation: %v", err)
			}
			if recordAppends != 1 {
				t.Fatalf("persistence attempts=%d, want one after discarding mutated pass", recordAppends)
			}
			finalContents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(finalContents, test.wantFinalContent(original)) {
				t.Fatal("test mutation did not produce its expected final bytes")
			}
			baseID := "claude-file-" + digest([]byte(path))
			cursor, err := store.Cursor(baseID)
			if err != nil {
				t.Fatal(err)
			}
			if cursor.FileFingerprint != digest(finalContents) || cursor.ByteOffset != int64(len(finalContents)) || cursor.SourceGeneration != test.wantGeneration {
				t.Fatalf("cursor=%+v final size=%d; want fingerprint of final bytes, matching offset, generation %d", cursor, len(finalContents), test.wantGeneration)
			}
			if result.Receipt.Imported != 0 && test.name == "append" {
				t.Fatalf("duplicate append imported new events: %+v", result.Receipt)
			}
		})
	}
}

func TestCollectorStreamsLateClassificationAndRejectsUnknownFallback(t *testing.T) {
	claude, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.Repeat(`{"type":"summary","summary":"generated"}`+"\n", 2048)
	if len(prefix) <= sourceVerificationBufferSize {
		t.Fatal("classification prefix must exceed the verification buffer")
	}
	cases := []struct {
		name            string
		contents        []byte
		adapter         string
		wantEvents      int
		wantIgnore      int
		wantBad         int
		wantUnsupported int
	}{
		{name: "late-claude", contents: []byte(prefix + string(claude)), adapter: "claude", wantEvents: 2, wantIgnore: 2050},
		{name: "unrecognized-summary-and-malformed", contents: []byte(prefix + "not-json\n"), wantIgnore: 2048, wantBad: 1},
		{name: "codex-auxiliary-only", contents: []byte(`{"type":"event_msg","payload":{"type":"agent_message","text":"synthetic"}}` + "\n"), wantIgnore: 1},
		{name: "codex-invalid-usage-only", contents: []byte(`{"type":"token_usage_record","input_tokens":1}` + "\n"), wantUnsupported: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, test.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			collector, err := NewCollector(store, []string{path}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			result, err := collector.PollOnce()
			if err != nil {
				t.Fatal(err)
			}
			if result.Receipt.Imported != test.wantEvents || result.Outcomes.IgnoredNonUsage != test.wantIgnore ||
				result.Outcomes.MalformedRecord != test.wantBad || result.Outcomes.UnsupportedUsage != test.wantUnsupported {
				t.Fatalf("result=%+v; want events=%d ignored=%d malformed=%d unsupported=%d", result, test.wantEvents, test.wantIgnore, test.wantBad, test.wantUnsupported)
			}
			if test.adapter != "" {
				baseID := test.adapter + "-file-" + digest([]byte(path))
				cursor, err := store.Cursor(baseID)
				if err != nil {
					t.Fatal(err)
				}
				if cursor.ByteOffset != int64(len(test.contents)) || cursor.FileFingerprint != digest(test.contents) {
					t.Fatalf("cursor=%+v; want full source offset and fingerprint", cursor)
				}
			} else {
				var cursors, identities, projects, sessions, checkpoints int
				for query, target := range map[string]*int{
					"SELECT COUNT(*) FROM source_cursors":              &cursors,
					"SELECT COUNT(*) FROM collector_source_identities": &identities,
					"SELECT COUNT(*) FROM projects":                    &projects,
					"SELECT COUNT(*) FROM sessions":                    &sessions,
					"SELECT COUNT(*) FROM parser_checkpoints":          &checkpoints,
				} {
					if err := store.db.QueryRow(query).Scan(target); err != nil {
						t.Fatal(err)
					}
				}
				if cursors+identities+projects+sessions+checkpoints != 0 {
					t.Fatalf("unrecognized source created persistent state: cursors=%d identities=%d projects=%d sessions=%d checkpoints=%d", cursors, identities, projects, sessions, checkpoints)
				}
			}
		})
	}
}

func TestCollectorDoesNotReuseUnrecognizedFallbackAndReplaysLateCodexRecognition(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	path := filepath.Join(t.TempDir(), "source.jsonl")
	prefix := []byte(`{"type":"summary","summary":"synthetic"}` + "\n")
	if err := os.WriteFile(path, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	// Seed the exact persistent shape an older fallback classifier produced:
	// a Codex cursor/checkpoint despite no recognized source record.
	if result, err := (CodexAdapter{}).Import(path, store); err != nil || result.Receipt != (Receipt{}) {
		t.Fatalf("seed fallback import=%+v err=%v", result, err)
	}
	source, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := establishedCollectorSourceID(store, source, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE parser_checkpoints SET parser_revision = 1 WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	var beforeProjects, beforeSessions, beforeCursors int
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM projects":       &beforeProjects,
		"SELECT COUNT(*) FROM sessions":       &beforeSessions,
		"SELECT COUNT(*) FROM source_cursors": &beforeCursors,
	} {
		if err := store.db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	observer := &collectorBenchmarkObserver{}
	store.benchmarkObserver = observer
	for range 2 {
		unchanged, err := collector.PollOnce()
		if err != nil || unchanged.Outcomes.UnchangedSnapshot != 0 || unchanged.Outcomes.IgnoredNonUsage != 1 {
			t.Fatalf("unrecognized cached source result=%+v err=%v; want ignored, not validated unchanged Codex", unchanged, err)
		}
	}
	if observer.sourceBytesRead != int64(2*len(prefix)) {
		t.Fatalf("unrecognized repeated polls read %d source bytes; want one full classification read per poll (%d)", observer.sourceBytesRead, 2*len(prefix))
	}
	var afterProjects, afterSessions, afterCursors int
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM projects":       &afterProjects,
		"SELECT COUNT(*) FROM sessions":       &afterSessions,
		"SELECT COUNT(*) FROM source_cursors": &afterCursors,
	} {
		if err := store.db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if beforeProjects != afterProjects || beforeSessions != afterSessions || beforeCursors != afterCursors {
		t.Fatalf("re-poll changed persisted fallback state: projects %d->%d sessions %d->%d cursors %d->%d",
			beforeProjects, afterProjects, beforeSessions, afterSessions, beforeCursors, afterCursors)
	}

	appended := benchmarkGeneratedHistory("codex", "late-recognized-session", 2)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(appended); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	observer = &collectorBenchmarkObserver{}
	store.benchmarkObserver = observer
	result, err := collector.PollOnce()
	if err != nil || result.Receipt.Imported != 2 {
		t.Fatalf("late Codex recognition result=%+v err=%v", result, err)
	}
	wantRecords := strings.Count(string(prefix)+string(appended), "\n")
	if observer.parserRecordsDecoded != int64(wantRecords) {
		t.Fatalf("parser decoded %d records; want full replay of %d after old fallback checkpoint invalidation", observer.parserRecordsDecoded, wantRecords)
	}
	var revision int
	if err := store.db.QueryRow(`SELECT parser_revision FROM parser_checkpoints WHERE source_id = ?`, sourceID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != codexParserRevision {
		t.Fatalf("checkpoint revision=%d; want current Codex parser revision %d", revision, codexParserRevision)
	}
}

func TestDetectAdapterRequiresPositiveProviderEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "empty"},
		{name: "whitespace", data: " \n\t\n"},
		{name: "unknown object", data: `{"type":"summary","summary":"synthetic"}` + "\n"},
		{name: "generic message key", data: `{"message":{"text":"synthetic"}}` + "\n"},
		{name: "model-only assistant auxiliary", data: `{"type":"assistant","message":{"model":"claude-model"}}` + "\n"},
		{name: "auxiliary Codex event", data: `{"type":"event_msg","payload":{"type":"agent_message","text":"synthetic"}}` + "\n"},
		{name: "Codex location without session identity", data: `{"type":"session_meta","payload":{"project_id":"project","cwd":"/synthetic/project"}}` + "\n"},
		{name: "malformed", data: "not-json\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			adapter, err := DetectAdapter(path)
			if err != nil || adapter != "" {
				t.Fatalf("adapter=%q err=%v; want no adapter without positive evidence", adapter, err)
			}
		})
	}
}

func TestCollectorPreservesCodexMetadataOnlySession(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	path := filepath.Join(t.TempDir(), "metadata-only.jsonl")
	contents := []byte(`{"type":"session_meta","payload":{"id":"provider-session","project_id":"provider-project","cwd":"/synthetic/project"}}` + "\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.PollOnce()
	if err != nil || result.Receipt != (Receipt{}) || result.Outcomes.IgnoredNonUsage != 1 {
		t.Fatalf("metadata-only result=%+v err=%v", result, err)
	}
	var projects, sessions, events int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM project_aliases WHERE scope = 'codex' AND alias = 'provider-project'`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM session_aliases WHERE alias = 'provider-session' AND scope = 'codex:codex-cli'`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if projects != 1 || sessions != 1 || events != 0 {
		t.Fatalf("metadata-only evidence: projects=%d sessions=%d events=%d; want 1, 1, 0", projects, sessions, events)
	}
}

func TestCollectorRetriesMutationDuringParserRead(t *testing.T) {
	line := []byte(`{"type":"summary","summary":"generated"}` + "\n")
	contents := append([]byte(strings.Repeat(string(line), 4096)), benchmarkGeneratedHistory("codex", "mutation-session", 2)...)
	path := filepath.Join(t.TempDir(), "large-source.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	store.sourceReadHook = func(pass int, bytesRead int64) {
		if pass == 2 && bytesRead >= sourceVerificationBufferSize {
			once.Do(func() {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Errorf("open source for concurrent append: %v", err)
					return
				}
				if _, err := file.Write(line); err != nil {
					t.Errorf("append during parser pass: %v", err)
				}
				if err := file.Close(); err != nil {
					t.Errorf("close concurrent append: %v", err)
				}
			})
		}
	}
	result, err := collector.PollOnce()
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcomes.FailedSource != 0 {
		t.Fatalf("collector outcomes=%+v", result.Outcomes)
	}
	finalContents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	baseID := "codex-file-" + digest([]byte(path))
	cursor, err := store.Cursor(baseID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ByteOffset != int64(len(finalContents)) || cursor.FileFingerprint != digest(finalContents) {
		t.Fatalf("cursor offset/fingerprint do not match final source: %+v size=%d", cursor, len(finalContents))
	}
}

func TestCollectorDoesNotCommitAfterRepeatedSourceChanges(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	path := filepath.Join(t.TempDir(), "repeated-change.jsonl")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var before Cursor
	baseID := "claude-file-" + digest([]byte(path))
	before, err = store.Cursor(baseID)
	if err != nil {
		t.Fatal(err)
	}
	store.beforeSnapshotParse = func() {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Errorf("open source during parser handoff: %v", err)
			return
		}
		if _, err := file.Write(original); err != nil {
			t.Errorf("change source during parser handoff: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Errorf("close changed source: %v", err)
		}
	}
	result, err := collector.PollOnce()
	if !errors.Is(err, errSourceCursorRetryExhausted) {
		t.Fatalf("poll error=%v result=%+v; want bounded retry exhaustion", err, result)
	}
	after, err := store.Cursor(baseID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("cursor changed after discarded attempts: before=%+v after=%+v", before, after)
	}
	count, err := store.EventCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event count after exhausted retries=%d; want original 2", count)
	}
}

func TestCollectorRetriesAdapterChangeBetweenPasses(t *testing.T) {
	claude, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	codex, err := os.ReadFile(filepath.Join("testdata", "codex", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	path := filepath.Join(t.TempDir(), "switch.jsonl")
	if err := os.WriteFile(path, claude, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(claude); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	store.beforeSnapshotParse = func() {
		once.Do(func() {
			if err := os.WriteFile(path, codex, 0o600); err != nil {
				t.Errorf("switch adapter source: %v", err)
			}
		})
	}
	result, err := collector.PollOnce()
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Imported != 2 {
		count, _ := store.EventCount()
		t.Fatalf("switched Codex result=%+v event count=%d; want two new events", result, count)
	}
	cursor, err := store.Cursor("codex-file-" + digest([]byte(path)))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.FileFingerprint != digest(codex) || cursor.ByteOffset != int64(len(codex)) {
		t.Fatalf("Codex cursor=%+v after adapter switch", cursor)
	}
}

func TestCollectorRetriesPathReplacementBetweenPasses(t *testing.T) {
	claude, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	codex, err := os.ReadFile(filepath.Join("testdata", "codex", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	claudePath := filepath.Join(root, "claude.jsonl")
	codexPath := filepath.Join(root, "codex.jsonl")
	link := filepath.Join(root, "current.jsonl")
	if err := os.WriteFile(claudePath, claude, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, codex, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudePath, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("source symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	collector, err := NewCollector(store, []string{link}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(claudePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(claude); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	store.beforeSnapshotParse = func() {
		once.Do(func() {
			if err := os.Remove(link); err != nil {
				t.Errorf("remove source symlink: %v", err)
				return
			}
			if err := os.Symlink(codexPath, link); err != nil {
				t.Errorf("replace source symlink: %v", err)
			}
		})
	}
	result, err := collector.PollOnce()
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Imported != 2 {
		t.Fatalf("replacement result=%+v; want Codex events", result)
	}
	cursor, err := store.Cursor("codex-file-" + digest([]byte(link)))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ByteOffset != int64(len(codex)) || cursor.FileFingerprint != digest(codex) {
		t.Fatalf("replacement cursor=%+v", cursor)
	}
}

func TestStreamingVerificationChecksCancellationBetweenReads(t *testing.T) {
	line := []byte(`{"type":"summary","summary":"generated"}` + "\n")
	contents := []byte(strings.Repeat(string(line), 10000))
	path := filepath.Join(t.TempDir(), "cancel.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.sourceReadHook = func(pass int, bytesRead int64) {
		if pass == 1 && bytesRead >= sourceVerificationBufferSize {
			cancel()
		}
	}
	_, err = collector.PollOnceContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("poll error=%v; want cancellation", err)
	}
	if count, err := store.EventCount(); err != nil || count != 0 {
		t.Fatalf("events after cancelled verification=%d err=%v; want none", count, err)
	}
	if _, err := store.Cursor("codex-file-" + digest([]byte(path))); err == nil {
		t.Fatal("cancelled verification persisted a source cursor")
	}
}

func TestPublicImportStreamsSourceWithoutChangingPlatformContract(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t)
	defer store.Close()
	path := filepath.Join(t.TempDir(), "explicit.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 2 {
		t.Fatalf("explicit Claude import result=%+v err=%v", result, err)
	}
	if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Cursor("claude-file-" + digest([]byte(path)))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ByteOffset != int64(len(contents)) || cursor.FileFingerprint != digest(contents) {
		t.Fatalf("explicit import cursor=%+v", cursor)
	}
}
