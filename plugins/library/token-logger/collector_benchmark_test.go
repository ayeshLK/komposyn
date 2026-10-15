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
	"strconv"
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
			b.ReportAllocs()
			b.SetBytes(int64(len(contents.Bytes())))
			var historical int
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				result, err := collector.PollOnce()
				if err != nil {
					b.Fatal(err)
				}
				historical += result.Receipt.Duplicates
			}
			b.StopTimer()
			if b.N > 0 {
				b.ReportMetric(float64(historical)/float64(b.N), "historical_events/op")
			}
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
			var historical int
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
				historical += result.Receipt.Duplicates
				if result.Receipt.Imported != 1 {
					store.Close()
					b.Fatalf("append imported %d events, want 1", result.Receipt.Imported)
				}
				if err := store.Close(); err != nil {
					b.Fatal(err)
				}
			}
			if b.N > 0 {
				b.ReportMetric(float64(historical)/float64(b.N), "historical_events/op")
			}
		})
	}
}
