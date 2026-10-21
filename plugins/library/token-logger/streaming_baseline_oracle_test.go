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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var streamingBaselinePath string

type streamingOracleSnapshot struct {
	Scenarios map[string][]streamingOracleStep `json:"scenarios"`
}

type streamingOracleStep struct {
	Name        string          `json:"name"`
	Result      ImportResult    `json:"result"`
	Error       string          `json:"error,omitempty"`
	Events      [][]string      `json:"events"`
	Projects    [][]string      `json:"projects"`
	Sessions    [][]string      `json:"sessions"`
	ProjectRefs [][]string      `json:"project_refs"`
	SessionRefs [][]string      `json:"session_refs"`
	Cursors     [][]string      `json:"cursors"`
	Identities  [][]string      `json:"identities"`
	Diagnostics []Diagnostic    `json:"diagnostics"`
	Health      CollectorHealth `json:"health"`
	JSON        string          `json:"json"`
	CSV         string          `json:"csv"`
}

func TestStreamingBaselineOracle(t *testing.T) {
	outputPath := os.Getenv("KOMPOSYN_STREAMING_ORACLE_OUTPUT")
	if outputPath == "" {
		t.Skip("set KOMPOSYN_STREAMING_ORACLE_OUTPUT to generate differential snapshot")
	}
	streamingBaselinePath = os.Getenv("KOMPOSYN_STREAMING_ORACLE_ROOT")
	if streamingBaselinePath == "" {
		t.Fatal("KOMPOSYN_STREAMING_ORACLE_ROOT is required")
	}
	if _, err := os.Lstat(streamingBaselinePath); err == nil {
		t.Fatalf("oracle fixture path already exists: %s", streamingBaselinePath)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(streamingBaselinePath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(streamingBaselinePath) })

	snapshot := streamingOracleSnapshot{Scenarios: make(map[string][]streamingOracleStep)}
	for _, provider := range []string{"claude", "codex"} {
		snapshot.Scenarios[provider] = runStreamingOracleProvider(t, provider)
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runStreamingOracleProvider(t *testing.T, provider string) []streamingOracleStep {
	t.Helper()
	store := openTestStore(t)
	defer store.Close()
	root := filepath.Join(streamingBaselinePath, provider)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "history.jsonl")
	initial := benchmarkGeneratedHistory(provider, "baseline-session", 3)
	firstBreak := bytes.IndexByte(initial, '\n') + 1
	initial = append(append(append([]byte(nil), initial[:firstBreak]...), []byte("{malformed interior record}\n")...), initial[firstBreak:]...)
	initial = append(initial, benchmarkGeneratedNonUsage(provider, "baseline-session")...)
	if err := os.WriteFile(mainPath, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{mainPath}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	steps := make([]streamingOracleStep, 0, 12)
	poll := func(name string) {
		t.Helper()
		result, pollErr := collector.PollOnceContext(context.Background())
		steps = append(steps, captureStreamingOracleStep(t, name, store, collector, result, pollErr))
	}
	poll("initial-with-malformed-interior")
	poll("unchanged")
	appendStreamingOracleFile(t, mainPath, benchmarkGeneratedUsage(provider, "baseline-session", 4))
	poll("usage-append")

	tail := benchmarkGeneratedUsage(provider, "baseline-session", 5)
	partial := tail[:len(tail)-4]
	appendStreamingOracleFile(t, mainPath, partial)
	poll("pending-tail")
	appendStreamingOracleFile(t, mainPath, tail[len(partial):])
	poll("tail-completion")

	if err := os.WriteFile(mainPath, benchmarkGeneratedHistory(provider, "baseline-session", 2), 0o600); err != nil {
		t.Fatal(err)
	}
	poll("truncate-rewrite-generation")
	large := append(benchmarkGeneratedLargeRecord(provider), benchmarkGeneratedUsage(provider, "baseline-session", 3)...)
	appendStreamingOracleFile(t, mainPath, large)
	poll("large-nonusage-record-and-usage")

	latePath := filepath.Join(root, "late-classification.jsonl")
	late := []byte(strings.Repeat(`{"type":"summary","summary":"generated"}`+"\n", 2048))
	late = append(late, benchmarkGeneratedHistory(provider, "late-session", 2)...)
	if err := os.WriteFile(latePath, late, 0o600); err != nil {
		t.Fatal(err)
	}
	lateCollector, err := NewCollector(store, []string{latePath}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	lateResult, lateErr := lateCollector.PollOnce()
	steps = append(steps, captureStreamingOracleStep(t, "late-classification-over-64k", store, lateCollector, lateResult, lateErr))

	eofPath := filepath.Join(root, "metadata-only-incomplete-tail.jsonl")
	eof := append([]byte(`{"type":"session_meta","payload":{"id":"oracle-eof-session","project_id":"oracle-eof-project","cwd":"/oracle/eof"}}`+"\n"), []byte(`{"type":"summary"}`)...)
	if err := os.WriteFile(eofPath, eof, 0o600); err != nil {
		t.Fatal(err)
	}
	eofCollector, err := NewCollector(store, []string{eofPath}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	eofResult, eofErr := eofCollector.PollOnce()
	steps = append(steps, captureStreamingOracleStep(t, "codex-metadata-only-incomplete-tail", store, eofCollector, eofResult, eofErr))

	if provider == "claude" {
		lateAttributionPath := filepath.Join(root, "late-attribution.jsonl")
		initial := []byte(`{"type":"assistant","uuid":"oracle-late-1","timestamp":"2026-10-11T00:00:01Z","message":{"model":"claude-sonnet-5","usage":{"input_tokens":3,"output_tokens":1}}}` + "\n")
		if err := os.WriteFile(lateAttributionPath, initial, 0o600); err != nil {
			t.Fatal(err)
		}
		lateAttributionCollector, err := NewCollector(store, []string{lateAttributionPath}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		result, pollErr := lateAttributionCollector.PollOnce()
		steps = append(steps, captureStreamingOracleStep(t, "late-attribution-before", store, lateAttributionCollector, result, pollErr))
		appendStreamingOracleFile(t, lateAttributionPath, []byte(`{"type":"user","sessionId":"oracle-late-session","cwd":"/oracle/late","project_id":"oracle-late-project","project_name":"Oracle Late"}`+"\n"))
		result, pollErr = lateAttributionCollector.PollOnce()
		steps = append(steps, captureStreamingOracleStep(t, "late-attribution-after", store, lateAttributionCollector, result, pollErr))
	} else {
		transitionPath := filepath.Join(root, "session-transition.jsonl")
		initial := benchmarkGeneratedHistory("codex", "oracle-session-1", 1)
		if err := os.WriteFile(transitionPath, initial, 0o600); err != nil {
			t.Fatal(err)
		}
		transitionCollector, err := NewCollector(store, []string{transitionPath}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		result, pollErr := transitionCollector.PollOnce()
		steps = append(steps, captureStreamingOracleStep(t, "session-transition-before", store, transitionCollector, result, pollErr))
		appendStreamingOracleFile(t, transitionPath, benchmarkGeneratedHistory("codex", "oracle-session-2", 1))
		result, pollErr = transitionCollector.PollOnce()
		steps = append(steps, captureStreamingOracleStep(t, "session-transition-after", store, transitionCollector, result, pollErr))

		steps = append(steps, runCodexBoundaryOracleCases(t, store, root)...)
	}
	return steps
}

func runCodexBoundaryOracleCases(t *testing.T, store *Store, root string) []streamingOracleStep {
	t.Helper()
	var steps []streamingOracleStep
	for _, scenario := range []string{"duplicate", "conflict", "reset"} {
		for _, mode := range []string{"initial", "continuation"} {
			for _, tailKind := range []string{"valid-unterminated", "incomplete"} {
				name := scenario + "-" + mode + "-" + tailKind
				path := filepath.Join(root, "boundary-"+name+".jsonl")
				session := "oracle-boundary-" + name
				header := `{"type":"session_meta","payload":{"id":"` + session + `","project_id":"` + session + `-project","project_name":"Boundary","cwd":"/oracle/boundary","model":"gpt-test"}}` + "\n"
				first := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:01Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"` + session + `","response_id":"response-1","usage":{"input_tokens":10,"output_tokens":1},"turn_token_usage":{"input_tokens":10,"output_tokens":1},"thread_token_usage":{"input_tokens":10,"output_tokens":1}}}` + "\n"
				special := first
				if scenario == "conflict" {
					special = `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"` + session + `","response_id":"response-1","usage":{"input_tokens":11,"output_tokens":1},"turn_token_usage":{"input_tokens":11,"output_tokens":1},"thread_token_usage":{"input_tokens":11,"output_tokens":1}}}` + "\n"
				} else if scenario == "reset" {
					special = `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:02Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"` + session + `","response_id":"response-2","usage":{"input_tokens":1,"output_tokens":1},"turn_token_usage":{"input_tokens":9,"output_tokens":1},"thread_token_usage":{"input_tokens":9,"output_tokens":1}}}` + "\n"
				}
				cumulative := `"input_tokens":20,"output_tokens":2`
				usage := `"input_tokens":10,"output_tokens":1`
				if scenario == "reset" {
					cumulative, usage = `"input_tokens":10,"output_tokens":2`, `"input_tokens":1,"output_tokens":1`
				}
				tail := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:03Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"` + session + `","response_id":"response-3","usage":{` + usage + `},"turn_token_usage":{` + cumulative + `},"thread_token_usage":{` + cumulative + `}}}`
				tailRemainder := ""
				if tailKind == "incomplete" {
					tailRemainder = tail[len(tail)-7:]
					tail = tail[:len(tail)-7]
				}
				prefix := []byte(header + first)
				if mode == "initial" {
					if err := os.WriteFile(path, append(append(append([]byte(nil), prefix...), []byte(special)...), []byte(tail)...), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, prefix, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				collector, err := NewCollector(store, []string{path}, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "continuation" {
					result, pollErr := collector.PollOnce()
					steps = append(steps, captureStreamingOracleStep(t, name+"-prefix", store, collector, result, pollErr))
					appendStreamingOracleFile(t, path, append([]byte(special), []byte(tail)...))
				}
				result, pollErr := collector.PollOnce()
				steps = append(steps, captureStreamingOracleStep(t, name+"-boundary", store, collector, result, pollErr))
				laterUsage := `"input_tokens":10,"output_tokens":1`
				if scenario == "reset" {
					laterUsage = `"input_tokens":20,"output_tokens":1`
				}
				later := `{"type":"token_usage_record","timestamp":"2026-10-11T00:00:04Z","payload":{"thread_id":"thread-1","turn_id":"turn-1","session_id":"` + session + `","response_id":"response-4","usage":{` + laterUsage + `},"turn_token_usage":{"input_tokens":30,"output_tokens":3},"thread_token_usage":{"input_tokens":30,"output_tokens":3}}}` + "\n"
				appendStreamingOracleFile(t, path, []byte(tailRemainder+"\n"+later))
				result, pollErr = collector.PollOnce()
				steps = append(steps, captureStreamingOracleStep(t, name+"-completed-and-appended", store, collector, result, pollErr))
			}
		}
	}
	return steps
}

func appendStreamingOracleFile(t *testing.T, path string, content []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func captureStreamingOracleStep(t *testing.T, name string, store *Store, collector *Collector, result ImportResult, pollErr error) streamingOracleStep {
	t.Helper()
	step := streamingOracleStep{Name: name, Result: result, Health: collector.Health()}
	if pollErr != nil {
		step.Error = pollErr.Error()
	}
	step.Health.LastPollAt = time.Time{}
	step.Health.LastCompletedAt = time.Time{}
	step.Health.LastSuccessAt = time.Time{}
	for key, root := range step.Health.Roots {
		root.LastError = strings.ReplaceAll(root.LastError, streamingBaselinePath, "<oracle-root>")
		step.Health.Roots[key] = root
	}
	var err error
	step.Events = streamingOracleRows(t, store, "SELECT * FROM usage_events ORDER BY event_id")
	step.Projects = streamingOracleRows(t, store, "SELECT id, root, display_name FROM projects ORDER BY id")
	step.Sessions = streamingOracleRows(t, store, "SELECT * FROM sessions ORDER BY id")
	step.ProjectRefs = streamingOracleRows(t, store, "SELECT scope, alias, project_id, provenance FROM project_aliases ORDER BY scope, alias")
	step.SessionRefs = streamingOracleRows(t, store, "SELECT scope, alias, session_id, project_id, provenance FROM session_aliases ORDER BY scope, alias")
	step.Cursors = streamingOracleRows(t, store, "SELECT source_id, file_fingerprint, byte_offset, last_provider_record_id, '<time>' AS updated_at, source_generation, pending_tail, snapshot_reusable FROM source_cursors ORDER BY source_id")
	step.Identities = streamingOracleRows(t, store, "SELECT canonical_key, adapter, source_id FROM collector_source_identities ORDER BY canonical_key, adapter")
	step.Diagnostics, err = store.PendingDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	for index := range step.Diagnostics {
		step.Diagnostics[index].FirstSeenAt = time.Time{}
		step.Diagnostics[index].LastSeenAt = time.Time{}
		// The independent v0.7.1 oracle uses schema 15; candidate schema 16
		// changes only this metadata field. Compare all behavioral diagnostic
		// fields while accounting explicitly for that intentional difference.
		step.Diagnostics[index].SchemaVersion = 0
	}
	report, err := store.Query(UsageFilter{From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var jsonReport, csvReport bytes.Buffer
	if err := WriteJSON(&jsonReport, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteCSV(&csvReport, report); err != nil {
		t.Fatal(err)
	}
	step.JSON, step.CSV = jsonReport.String(), csvReport.String()
	return step
}

func streamingOracleRows(t *testing.T, store *Store, query string) [][]string {
	t.Helper()
	rows, err := store.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := make([][]string, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			switch typed := value.(type) {
			case nil:
				row[index] = "<null>"
			case []byte:
				row[index] = string(typed)
			default:
				row[index] = fmt.Sprint(typed)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
