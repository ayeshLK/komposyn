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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func BenchmarkCollectorUnchangedHistory(b *testing.B) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []struct {
		name     string
		nonUsage int
	}{
		{"small", 0},
		{"large", 10_000},
	} {
		b.Run(size.name, func(b *testing.B) {
			var contents bytes.Buffer
			for index := 0; index < size.nonUsage; index++ {
				contents.WriteString(`{"type":"user","message":{"content":"benchmark"}}` + "\n")
			}
			contents.Write(fixture)
			path := filepath.Join(b.TempDir(), "history.jsonl")
			if err := os.WriteFile(path, contents.Bytes(), 0o600); err != nil {
				b.Fatal(err)
			}
			store, err := Open(filepath.Join(b.TempDir(), "benchmark.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			collector, err := NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := collector.PollOnce(); err != nil {
				b.Fatal(err)
			}
			observer := &collectorBenchmarkObserver{}
			store.benchmarkObserver = observer
			b.ReportAllocs()
			b.SetBytes(int64(len(contents.Bytes())))
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				_, err := collector.PollOnce()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			reportBenchmarkWork(b, observer)
		})
	}
}

func BenchmarkCollectorAppendHistory(b *testing.B) {
	for _, size := range []int{10, 1_000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			var initial bytes.Buffer
			initial.WriteString(`{"type":"user","sessionId":"benchmark-session","cwd":"/benchmark","project_id":"benchmark-project","project_name":"Benchmark"}` + "\n")
			for index := 0; index < size; index++ {
				initial.WriteString(`{"type":"assistant","uuid":"history-` + strconv.Itoa(index) + `","sessionId":"benchmark-session","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-benchmark","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n")
			}
			appended := append(append([]byte(nil), initial.Bytes()...), []byte(`{"type":"assistant","uuid":"appended","sessionId":"benchmark-session","timestamp":"2026-10-11T00:00:01Z","message":{"model":"claude-benchmark","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n")...)
			path := filepath.Join(b.TempDir(), "history.jsonl")
			b.ReportAllocs()
			b.SetBytes(int64(len(appended)))
			var total collectorBenchmarkObserver
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				if err := os.WriteFile(path, initial.Bytes(), 0o600); err != nil {
					b.Fatal(err)
				}
				store, err := Open(":memory:")
				if err != nil {
					b.Fatal(err)
				}
				collector, err := NewCollector(store, []string{path}, time.Hour)
				if err != nil {
					store.Close()
					b.Fatal(err)
				}
				if _, err := collector.PollOnce(); err != nil {
					store.Close()
					b.Fatal(err)
				}
				observer := &collectorBenchmarkObserver{historicalEventIDs: benchmarkStoredEventIDs(b, store)}
				store.benchmarkObserver = observer
				if err := os.WriteFile(path, appended, 0o600); err != nil {
					store.Close()
					b.Fatal(err)
				}
				b.StartTimer()
				result, err := collector.PollOnce()
				b.StopTimer()
				if err != nil {
					store.Close()
					b.Fatal(err)
				}
				addBenchmarkWork(&total, observer)
				if result.Receipt.Imported != 1 {
					store.Close()
					b.Fatalf("append imported %d events, want 1", result.Receipt.Imported)
				}
				if err := store.Close(); err != nil {
					b.Fatal(err)
				}
			}
			reportBenchmarkWork(b, &total)
		})
	}
}

// BenchmarkCollectorGeneratedBaselines uses only generated, temporary JSONL
// records. Fixture creation, schema initialization, and the initial import are
// excluded from the timed operation in steady-state benchmarks.
func BenchmarkCollectorGeneratedBaselines(b *testing.B) {
	for _, provider := range []string{"claude", "codex"} {
		b.Run(provider, func(b *testing.B) {
			initial := benchmarkGeneratedHistory(provider, "session-0", 1_000)
			usageAppend := benchmarkGeneratedUsage(provider, "session-0", 1_001)
			nonUsageAppend := benchmarkGeneratedNonUsage(provider, "session-0")

			b.Run("cold-import", func(b *testing.B) { benchmarkGeneratedColdImport(b, initial, 1_000) })
			b.Run("unchanged-retained-collector", func(b *testing.B) { benchmarkGeneratedUnchanged(b, initial, false) })
			b.Run("unchanged-recreated-collector", func(b *testing.B) { benchmarkGeneratedUnchanged(b, initial, true) })
			b.Run("usage-append", func(b *testing.B) { benchmarkGeneratedAppend(b, initial, usageAppend, 1) })
			b.Run("non-usage-append", func(b *testing.B) { benchmarkGeneratedAppend(b, initial, nonUsageAppend, 0) })

			malformed := append(append([]byte(nil), benchmarkGeneratedHistory(provider, "session-0", 50)...), []byte("{malformed-json}\n")...)
			unsupported := append(append([]byte(nil), benchmarkGeneratedHistory(provider, "session-0", 50)...), benchmarkGeneratedUnsupported(provider)...)
			b.Run("malformed-unchanged", func(b *testing.B) { benchmarkGeneratedIncomplete(b, malformed) })
			b.Run("unsupported-unchanged", func(b *testing.B) { benchmarkGeneratedIncomplete(b, unsupported) })

			incomplete, completion := benchmarkGeneratedPendingTail(provider, 50)
			b.Run("pending-tail-completion", func(b *testing.B) { benchmarkGeneratedAppend(b, incomplete, completion, 1) })

			large := append(benchmarkGeneratedHistory(provider, "session-large", 10), benchmarkGeneratedLargeRecord(provider)...)
			b.Run("large-record", func(b *testing.B) { benchmarkGeneratedColdImport(b, large, 10) })
			b.Run("many-small-files", func(b *testing.B) {
				benchmarkGeneratedFileSet(b, provider, 100, 2)
			})
			b.Run("few-large-files", func(b *testing.B) {
				benchmarkGeneratedFileSet(b, provider, 2, 100)
			})
		})
	}
}

// BenchmarkCollectorGeneratedHistoryScale extends the common 1,000-event
// cases to 10,000 events so follow-up changes can compare growth behavior.
func BenchmarkCollectorGeneratedHistoryScale(b *testing.B) {
	for _, provider := range []string{"claude", "codex"} {
		for _, count := range []int{1_000, 10_000} {
			b.Run(fmt.Sprintf("%s/%d/unchanged", provider, count), func(b *testing.B) {
				benchmarkGeneratedUnchanged(b, benchmarkGeneratedHistory(provider, "scale-session", count), true)
			})
			b.Run(fmt.Sprintf("%s/%d/append", provider, count), func(b *testing.B) {
				initial := benchmarkGeneratedHistory(provider, "scale-session", count)
				benchmarkGeneratedAppend(b, initial, benchmarkGeneratedUsage(provider, "scale-session", count+1), 1)
			})
		}
	}
}

func benchmarkGeneratedHistory(provider, session string, count int) []byte {
	var contents bytes.Buffer
	if provider == "claude" {
		fmt.Fprintf(&contents, "{\"type\":\"user\",\"sessionId\":%q,\"cwd\":\"/benchmark/project\",\"project_id\":\"benchmark-project\",\"project_name\":\"Benchmark\"}\n", session)
	} else {
		fmt.Fprintf(&contents, "{\"type\":\"session_meta\",\"timestamp\":\"2026-10-11T00:00:00Z\",\"payload\":{\"id\":%q,\"project_id\":\"benchmark-project\",\"project_name\":\"Benchmark\",\"cwd\":\"/benchmark/project\",\"model\":\"gpt-5-codex\"}}\n", session)
	}
	for index := 1; index <= count; index++ {
		contents.Write(benchmarkGeneratedUsage(provider, session, index))
	}
	return contents.Bytes()
}

func benchmarkGeneratedUsage(provider, session string, index int) []byte {
	if provider == "claude" {
		return []byte(fmt.Sprintf("{\"type\":\"assistant\",\"uuid\":\"usage-%d\",\"sessionId\":%q,\"timestamp\":\"2026-10-11T00:00:01Z\",\"message\":{\"model\":\"claude-sonnet-5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n", index, session))
	}
	tokens := index
	return []byte(fmt.Sprintf("{\"type\":\"event_msg\",\"id\":\"usage-%d\",\"timestamp\":\"2026-10-11T00:00:01Z\",\"payload\":{\"type\":\"token_count\",\"session_id\":%q,\"info\":{\"total_token_usage\":{\"input_tokens\":%d,\"output_tokens\":%d,\"total_tokens\":%d}}}}\n", index, session, tokens, tokens, tokens*2))
}

func benchmarkGeneratedNonUsage(provider, session string) []byte {
	if provider == "claude" {
		return []byte(fmt.Sprintf("{\"type\":\"user\",\"sessionId\":%q,\"message\":{\"content\":\"generated benchmark data\"}}\n", session))
	}
	return []byte(fmt.Sprintf("{\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"session_id\":%q,\"text\":\"generated benchmark data\"}}\n", session))
}

func benchmarkGeneratedUnsupported(provider string) []byte {
	if provider == "claude" {
		return []byte("{\"type\":\"assistant\",\"uuid\":\"unsupported\",\"sessionId\":\"session-0\",\"timestamp\":\"2026-10-11T00:00:02Z\",\"message\":{\"model\":\"claude-sonnet-5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"future_tokens\":1}}}\n")
	}
	return []byte("{\"type\":\"token_usage_record\",\"timestamp\":\"2026-10-11T00:00:02Z\",\"payload\":{\"thread_id\":\"thread-0\",\"session_id\":\"session-0\",\"response_id\":\"unsupported\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"future_tokens\":1}}}\n")
}

func benchmarkGeneratedLargeRecord(provider string) []byte {
	if provider == "claude" {
		return []byte("{\"type\":\"user\",\"message\":{\"content\":\"" + strings.Repeat("x", 64*1024) + "\"}}\n")
	}
	return []byte("{\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"text\":\"" + strings.Repeat("x", 64*1024) + "\"}}\n")
}

func benchmarkGeneratedPendingTail(provider string, count int) ([]byte, []byte) {
	complete := benchmarkGeneratedHistory(provider, "session-0", count)
	lastStart := bytes.LastIndex(complete[:len(complete)-1], []byte("\n")) + 1
	line := complete[lastStart : len(complete)-1]
	incomplete := append(append([]byte(nil), complete[:lastStart]...), line[:len(line)-1]...)
	return incomplete, []byte("}\n")
}

func benchmarkGeneratedColdImport(b *testing.B, contents []byte, expectedEvents int) {
	b.Helper()
	directory := b.TempDir()
	path := filepath.Join(directory, "source.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		b.Fatal(err)
	}
	benchmarkGeneratedColdFiles(b, directory, int64(len(contents)), expectedEvents)
}

func benchmarkGeneratedFileSet(b *testing.B, provider string, files, eventsPerFile int) {
	b.Helper()
	directory := b.TempDir()
	var totalBytes int64
	for index := 0; index < files; index++ {
		name := fmt.Sprintf("source-%03d.jsonl", index)
		contents := benchmarkGeneratedHistory(provider, fmt.Sprintf("session-%d", index), eventsPerFile)
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			b.Fatal(err)
		}
		totalBytes += int64(len(contents))
	}
	benchmarkGeneratedColdFiles(b, directory, totalBytes, files*eventsPerFile)
}

func benchmarkGeneratedColdFiles(b *testing.B, directory string, totalBytes int64, expectedEvents int) {
	b.Helper()
	var observer *collectorBenchmarkObserver
	var total collectorBenchmarkObserver
	b.SetBytes(totalBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		store, err := Open(":memory:")
		if err != nil {
			b.Fatal(err)
		}
		observer = &collectorBenchmarkObserver{}
		store.benchmarkObserver = observer
		collector, err := NewCollector(store, []string{directory}, time.Hour)
		if err != nil {
			store.Close()
			b.Fatal(err)
		}
		b.StartTimer()
		result, pollErr := collector.PollOnce()
		b.StopTimer()
		if pollErr != nil {
			store.Close()
			b.Fatal(pollErr)
		}
		if result.Receipt.Imported != expectedEvents {
			store.Close()
			b.Fatalf("cold import inserted %d events; want %d", result.Receipt.Imported, expectedEvents)
		}
		addBenchmarkWork(&total, observer)
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.StopTimer()
	reportBenchmarkWork(b, &total)
}

func benchmarkGeneratedUnchanged(b *testing.B, contents []byte, recreateCollector bool) {
	b.Helper()
	directory := b.TempDir()
	path := filepath.Join(directory, "source.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		b.Fatal(err)
	}
	observer := &collectorBenchmarkObserver{}
	store.benchmarkObserver = observer
	b.SetBytes(int64(len(contents)))
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if recreateCollector {
			collector, err = NewCollector(store, []string{path}, time.Hour)
			if err != nil {
				b.Fatal(err)
			}
		}
		if _, err := collector.PollOnce(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	reportBenchmarkWork(b, observer)
}

func benchmarkGeneratedAppend(b *testing.B, initial, suffix []byte, expectedImported int) {
	b.Helper()
	directory := b.TempDir()
	path := filepath.Join(directory, "source.jsonl")
	var total collectorBenchmarkObserver
	b.SetBytes(int64(len(initial) + len(suffix)))
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		if err := os.WriteFile(path, initial, 0o600); err != nil {
			b.Fatal(err)
		}
		store, err := Open(":memory:")
		if err != nil {
			b.Fatal(err)
		}
		collector, err := NewCollector(store, []string{path}, time.Hour)
		if err != nil {
			store.Close()
			b.Fatal(err)
		}
		first, err := collector.PollOnce()
		if err != nil {
			store.Close()
			b.Fatal(err)
		}
		incomplete := first.Outcomes.DeferredIncompleteTail > 0
		observer := &collectorBenchmarkObserver{historicalEventIDs: benchmarkStoredEventIDs(b, store)}
		store.benchmarkObserver = observer
		updated := append(append([]byte(nil), initial...), suffix...)
		if err := os.WriteFile(path, updated, 0o600); err != nil {
			store.Close()
			b.Fatal(err)
		}
		b.StartTimer()
		result, err := collector.PollOnce()
		b.StopTimer()
		if err != nil {
			store.Close()
			b.Fatal(err)
		}
		if incomplete && result.Outcomes.DeferredIncompleteTail > 0 {
			store.Close()
			b.Fatal("completed tail remains pending")
		}
		if result.Receipt.Imported != expectedImported {
			store.Close()
			b.Fatalf("append imported %d events; want %d (outcomes=%+v)", result.Receipt.Imported, expectedImported, result.Outcomes)
		}
		addBenchmarkWork(&total, observer)
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.StopTimer()
	reportBenchmarkWork(b, &total)
}

func benchmarkGeneratedIncomplete(b *testing.B, contents []byte) {
	b.Helper()
	directory := b.TempDir()
	path := filepath.Join(directory, "source.jsonl")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		b.Fatal(err)
	}
	observer := &collectorBenchmarkObserver{historicalEventIDs: benchmarkStoredEventIDs(b, store)}
	store.benchmarkObserver = observer
	b.SetBytes(int64(len(contents)))
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		result, err := collector.PollOnce()
		if err != nil {
			b.Fatal(err)
		}
		if !result.Outcomes.Incomplete() || result.Receipt.Imported != 0 {
			b.Fatalf("unchanged incomplete source result=%+v", result)
		}
	}
	b.StopTimer()
	reportBenchmarkWork(b, observer)
}

func addBenchmarkWork(total, current *collectorBenchmarkObserver) {
	total.sourceBytesRead += current.sourceBytesRead
	total.sourceBytesHashed += current.sourceBytesHashed
	total.recordsDecoded += current.recordsDecoded
	total.parserRecordsDecoded += current.parserRecordsDecoded
	total.classificationRecordsDecoded += current.classificationRecordsDecoded
	total.eventInsertAttempts += current.eventInsertAttempts
	total.historicalInsertAttempts += current.historicalInsertAttempts
}

func reportBenchmarkWork(b *testing.B, work *collectorBenchmarkObserver) {
	b.Helper()
	if b.N == 0 {
		return
	}
	b.ReportMetric(float64(work.sourceBytesRead)/float64(b.N), "source_bytes/op")
	b.ReportMetric(float64(work.sourceBytesHashed)/float64(b.N), "source_bytes_hashed/op")
	b.ReportMetric(float64(work.parserRecordsDecoded)/float64(b.N), "parser_records/op")
	b.ReportMetric(float64(work.classificationRecordsDecoded)/float64(b.N), "classification_records/op")
	b.ReportMetric(float64(work.eventInsertAttempts)/float64(b.N), "event_insert_attempts/op")
	b.ReportMetric(float64(work.historicalInsertAttempts)/float64(b.N), "historical_insert_attempts/op")
}

func benchmarkStoredEventIDs(b *testing.B, store *Store) map[string]struct{} {
	b.Helper()
	rows, err := store.db.Query("SELECT event_id FROM usage_events")
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	ids := make(map[string]struct{})
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			b.Fatal(err)
		}
		ids[eventID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return ids
}
