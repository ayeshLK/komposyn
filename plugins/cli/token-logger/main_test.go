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

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
	"github.com/chzyer/readline"
	_ "modernc.org/sqlite"
)

type cancelOnWriteBuffer struct {
	bytes.Buffer
	marker string
	cancel context.CancelFunc
}

type blockingWatchPoller struct {
	started      chan struct{}
	laterStarted bool
}

func (poller *blockingWatchPoller) PollOnceContext(ctx context.Context) (tokenlogger.ImportResult, error) {
	close(poller.started)
	select {
	case <-ctx.Done():
		return tokenlogger.ImportResult{}, ctx.Err()
	case <-time.After(5 * time.Second):
		poller.laterStarted = true
		return tokenlogger.ImportResult{}, nil
	}
}

func (buffer *cancelOnWriteBuffer) Write(value []byte) (int, error) {
	written, err := buffer.Buffer.Write(value)
	if strings.Contains(string(value), buffer.marker) {
		buffer.cancel()
	}
	return written, err
}

func TestImportCommand(t *testing.T) {
	dataDir := t.TempDir()
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "codex", "normal.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"import", fixture, "--data-dir", dataDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Imported events: 2") {
		t.Fatalf("unexpected import output: %q", stdout.String())
	}
}

func TestClaudeFallbackCreditImportIsVisibleInReportsAndInteractiveMode(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "fallback-credit.jsonl")
	record := `{"type":"assistant","uuid":"fallback-cli","sessionId":"fallback-session","cwd":"/sanitized/fallback-project","timestamp":"2026-10-11T12:00:00Z","message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":1,"fallback_credit":{"status":{"type":"redeemed"}},"server_tool_use":{"web_search_requests":1}}}}` + "\n"
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}

	var importOutput, importErr bytes.Buffer
	if code := run([]string{"import", path, "--format", "json", "--data-dir", dataDir}, &importOutput, &importErr); code != 0 {
		t.Fatalf("import exit=%d stderr=%q output=%q", code, importErr.String(), importOutput.String())
	}
	for _, expected := range []string{`"imported_events":1`, `"unsupported_usage":0`, `"complete":true`} {
		if !strings.Contains(importOutput.String(), expected) {
			t.Fatalf("import output missing %q: %s", expected, importOutput.String())
		}
	}

	var reportOutput, reportErr bytes.Buffer
	if code := run([]string{"usage", "--data-dir", dataDir, "--format", "json", "--group-by", "model", "--from", "2026-10-11", "--to", "2026-10-12"}, &reportOutput, &reportErr); code != 0 {
		t.Fatalf("usage exit=%d stderr=%q output=%q", code, reportErr.String(), reportOutput.String())
	}
	var report tokenlogger.UsageReport
	if err := json.Unmarshal(reportOutput.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Dimensions[tokenlogger.GroupModel] != "claude-sonnet-5" {
		t.Fatalf("unexpected report rows: %+v", report.Rows)
	}
	row := report.Rows[0]
	if row.RequestCount != 1 || row.InputTokens != 13 || row.CachedInputTokens != 1 || row.CacheWriteInputTokens != 2 || row.OutputTokens != 5 || row.TotalTokens != 18 {
		t.Fatalf("unexpected report totals: %+v", row.UsageMeasures)
	}
	if row.UnknownCostRequests != 1 || row.EstimatedCostRequests != 0 || row.CalculationBasis != "unavailable" {
		t.Fatalf("fallback credit changed cost semantics: %+v", row.UsageMeasures)
	}

	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	commands := "/range 2026-10-11 2026-10-12\n/projects\n/project 1\n/sessions\n/session 1\n/models\n/quit\n"
	reader := &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(commands)), output: io.Discard}
	var interactiveOutput, interactiveErr bytes.Buffer
	if code := runInteractiveLoopWithClock(reader, &interactiveOutput, &interactiveErr, store, dataDir, defaultSettings(dataDir), false, func() time.Time {
		return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	}); code != 0 {
		t.Fatalf("interactive exit=%d stderr=%q", code, interactiveErr.String())
	}
	for _, expected := range []string{"fallback-project", "session:", "claude-sonnet-5", "13", "5", "18", "unknown"} {
		if !strings.Contains(interactiveOutput.String(), expected) {
			t.Fatalf("interactive output missing %q: %s", expected, interactiveOutput.String())
		}
	}
}

func TestClaudeImportCommand(t *testing.T) {
	dataDir := t.TempDir()
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"import", fixture, "--data-dir", dataDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Claude import exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Imported events: 2") {
		t.Fatalf("unexpected Claude import output: %q", stdout.String())
	}
}

func TestWatchRegistryCommandsAndMultiRootForeground(t *testing.T) {
	dataDir := t.TempDir()
	codex := filepath.Join("..", "..", "library", "token-logger", "testdata", "codex")
	claude := filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"watch", "add", codex, claude, "--data-dir", dataDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("watch add code=%d stderr=%q", code, stderr.String())
	}
	if strings.Count(stdout.String(), "registered source") != 2 {
		t.Fatalf("watch add output=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"watch", "list", "--format", "json", "--data-dir", dataDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("watch list code=%d stderr=%q", code, stderr.String())
	}
	var listed sourceListOutput
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil || listed.FormatVersion != 1 || len(listed.Sources) != 2 {
		t.Fatalf("listed=%+v err=%v output=%q", listed, err, stdout.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	output := &cancelOnWriteBuffer{marker: "poll status=", cancel: cancel}
	stderr.Reset()
	code := runWatchWithContext([]string{"foreground", codex, claude, "--interval", "1ms", "--data-dir", dataDir}, output, &stderr, ctx)
	if code != 0 {
		t.Fatalf("foreground code=%d stderr=%q output=%q", code, stderr.String(), output.String())
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Query(tokenlogger.UsageFilter{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil || report.Summary.RequestCount == 0 {
		t.Fatalf("combined report=%+v err=%v", report.Summary, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"watch", "remove", listed.Sources[0].ID, "--data-dir", dataDir}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "disabled source") {
		t.Fatalf("remove code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
}

func TestWatchIntervalPersistsExactly(t *testing.T) {
	dataDir := t.TempDir()
	if err := persistWatchInterval(dataDir, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettings(filepath.Join(dataDir, "settings.json"), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.collectorPollInterval(); got != 1500*time.Millisecond {
		t.Fatalf("poll interval=%s, want 1.5s", got)
	}
}

func TestWatchSubcommandsRejectIrrelevantOptions(t *testing.T) {
	for _, args := range [][]string{
		{"watch", "status", "--interval", "1s", "--data-dir", t.TempDir()},
		{"watch", "stop", "--format", "json", "--data-dir", t.TempDir()},
		{"watch", "list", "--defaults", "--data-dir", t.TempDir()},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "is not valid") {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestForegroundWatchRejectsActiveCollectorLease(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireCollectorLease("background", 1, tokenlogger.Version, "background", time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCollectorReady("background"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl")
	var stdout, stderr bytes.Buffer
	code := runWatchWithContext([]string{"foreground", fixture, "--data-dir", dataDir}, &stdout, &stderr, context.Background())
	if code != 2 || !strings.Contains(stderr.String(), "another collector is already active") {
		t.Fatalf("foreground code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	store, err = tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Query(tokenlogger.UsageFilter{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil || report.Summary.RequestCount != 0 {
		t.Fatalf("foreground imported alongside active collector: summary=%+v err=%v", report.Summary, err)
	}
}

func TestPollWithLeaseHeartbeatHonorsStopRequest(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.AcquireCollectorLease("poll-stop", 1, tokenlogger.Version, "background", time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCollectorReady("poll-stop"); err != nil {
		t.Fatal(err)
	}
	poller := &blockingWatchPoller{started: make(chan struct{})}
	type pollResult struct {
		pollErr      error
		heartbeatErr error
		stop         bool
	}
	done := make(chan pollResult, 1)
	go func() {
		_, pollErr, heartbeatErr, stop := pollWithLeaseHeartbeat(context.Background(), store, "poll-stop", poller, 15*time.Second)
		done <- pollResult{pollErr: pollErr, heartbeatErr: heartbeatErr, stop: stop}
	}()
	<-poller.started
	if requested, err := store.RequestCollectorStop(); err != nil || !requested {
		t.Fatalf("request stop=%t err=%v", requested, err)
	}
	select {
	case result := <-done:
		if !result.stop || result.heartbeatErr != nil || !errors.Is(result.pollErr, context.Canceled) || poller.laterStarted {
			t.Fatalf("poll result=%+v later_started=%t", result, poller.laterStarted)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop request did not cancel the active poll")
	}
}

func TestFastForegroundWatchHonorsCooperativeStop(t *testing.T) {
	dataDir := t.TempDir()
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		done <- runWatchWithContext([]string{"foreground", fixture, "--interval", "10ms", "--data-dir", dataDir}, &stdout, &stderr, ctx)
	}()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, statusErr := store.CollectorRuntimeStatus(time.Second)
		if statusErr == nil && status.LastCompletedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("foreground did not complete an initial poll: status=%+v err=%v stderr=%q", status, statusErr, stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requested, err := store.RequestCollectorStop(); err != nil || !requested {
		t.Fatalf("request stop=%t err=%v", requested, err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("foreground code=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fast foreground collector ignored cooperative stop")
	}
	status, err := store.CollectorRuntimeStatus(time.Second)
	if err != nil || status.State != "stopped" {
		t.Fatalf("stopped status=%+v err=%v", status, err)
	}
}

func TestRegistryForegroundReloadsSourcesBeforeEachPoll(t *testing.T) {
	dataDir := t.TempDir()
	claude, err := filepath.Abs(filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code"))
	if err != nil {
		t.Fatal(err)
	}
	codex, err := filepath.Abs(filepath.Join("..", "..", "library", "token-logger", "testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	claudeSource, err := store.RegisterCollectorSource(claude, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		done <- runWatchWithContext([]string{"foreground", "--interval", "20ms", "--data-dir", dataDir}, &stdout, &stderr, ctx)
	}()
	store, err = tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	waitForSourceStatus := func(path, description string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			sources, listErr := store.ListCollectorSources(false)
			if listErr == nil {
				for _, source := range sources {
					if source.Path == path && source.Status != "unknown" {
						return
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s; stderr=%q", description, stderr.String())
	}
	waitForSourceStatus(claude, "initial Claude poll")
	if _, changed, err := store.RemoveCollectorSource(claudeSource.Source.ID); err != nil || !changed {
		t.Fatalf("disable Claude changed=%t err=%v", changed, err)
	}
	if _, err := store.RegisterCollectorSource(codex, true); err != nil {
		t.Fatal(err)
	}
	waitForSourceStatus(codex, "reloaded Codex poll")
	if requested, err := store.RequestCollectorStop(); err != nil || !requested {
		t.Fatalf("request stop=%t err=%v", requested, err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("foreground code=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("registry-backed foreground did not stop")
	}
	status, err := store.CollectorRuntimeStatus(time.Second)
	if err != nil || status.SourceCount != 1 || status.State != "stopped" {
		t.Fatalf("final status=%+v err=%v", status, err)
	}
}

func TestRegistryForegroundSummaryAccumulatesAcrossReloadedCollectors(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "registered")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterCollectorSource(root, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		done <- runWatchWithContext([]string{"foreground", "--interval", "20ms", "--data-dir", dataDir}, &stdout, &stderr, ctx)
	}()
	store, err = tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	waitForRuntime := func(description string, condition func(tokenlogger.CollectorRuntimeStatus) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			status, statusErr := store.CollectorRuntimeStatus(time.Second)
			if statusErr == nil && condition(status) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		t.Fatalf("timed out waiting for %s; stderr=%q output=%q", description, stderr.String(), stdout.String())
	}
	failedSourcesBeforeRecovery := 0
	waitForRuntime("missing-root failure", func(status tokenlogger.CollectorRuntimeStatus) bool {
		if status.Outcomes.FailedSource > 0 {
			failedSourcesBeforeRecovery = status.Outcomes.FailedSource
			return true
		}
		return false
	})
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "normal.jsonl"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForRuntime("successful import and unchanged follow-up", func(status tokenlogger.CollectorRuntimeStatus) bool {
		return status.Imported == 2 && status.Outcomes.FailedSource >= failedSourcesBeforeRecovery && status.Outcomes.UnchangedSnapshot > 0
	})
	if requested, err := store.RequestCollectorStop(); err != nil || !requested {
		t.Fatalf("request stop=%t err=%v", requested, err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("foreground code=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("registry-backed foreground did not stop")
	}
	finalStatus, err := store.CollectorRuntimeStatus(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if finalStatus.Outcomes.FailedSource < failedSourcesBeforeRecovery {
		t.Fatalf("failed-source count was lost across collector reloads: before=%d final=%d", failedSourcesBeforeRecovery, finalStatus.Outcomes.FailedSource)
	}
	output := stdout.String()
	index := strings.LastIndex(output, "collector stopped:")
	if index < 0 {
		t.Fatalf("missing stopped summary: %q", output)
	}
	summary := output[index:]
	for _, expected := range []string{" imported=2 ", fmt.Sprintf(" failed_source=%d ", finalStatus.Outcomes.FailedSource), " run_complete=false\n"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("stopped summary omitted %q: %q", expected, summary)
		}
	}
}

func TestWatchServeReloadsRegistryAndStopsCooperatively(t *testing.T) {
	dataDir := t.TempDir()
	codex, err := filepath.Abs(filepath.Join("..", "..", "library", "token-logger", "testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	claude, err := filepath.Abs(filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	codexSource, err := store.RegisterCollectorSource(codex, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- runWatchServe([]string{"--data-dir", dataDir, "--interval", "20ms", "--instance", "test-instance"}, &stderr, ctx)
	}()

	store, err = tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer store.Close()
	waitFor := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		t.Fatalf("timed out waiting for %s; stderr=%q", description, stderr.String())
	}
	waitFor("initial registered root", func() bool {
		status, statusErr := store.CollectorRuntimeStatus(time.Second)
		return statusErr == nil && (status.State == "running" || status.State == "degraded") && status.SourceCount == 1 && status.LastCompletedAt != nil
	})
	if _, err := store.RegisterCollectorSource(claude, true); err != nil {
		cancel()
		t.Fatal(err)
	}
	waitFor("added root on next poll", func() bool {
		status, statusErr := store.CollectorRuntimeStatus(time.Second)
		return statusErr == nil && status.SourceCount == 2
	})
	if _, changed, err := store.RemoveCollectorSource(codexSource.Source.ID); err != nil || !changed {
		cancel()
		t.Fatalf("remove changed=%t err=%v", changed, err)
	}
	waitFor("removed root on next poll", func() bool {
		status, statusErr := store.CollectorRuntimeStatus(time.Second)
		return statusErr == nil && status.SourceCount == 1
	})
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve code=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after cancellation")
	}
	status, err := store.CollectorRuntimeStatus(time.Second)
	if err != nil || status.State != "stopped" {
		t.Fatalf("stopped status=%+v err=%v", status, err)
	}
}

func TestRegisteredSourceHealthUsesCollectorOutcomes(t *testing.T) {
	dataDir := t.TempDir()
	healthyRoot := t.TempDir()
	degradedRoot := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(healthyRoot, "normal.jsonl"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := append(append([]byte(nil), fixture...), []byte("{not-json}\n")...)
	if err := os.WriteFile(filepath.Join(degradedRoot, "malformed.jsonl"), malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, root := range []string{healthyRoot, degradedRoot} {
		if _, err := store.RegisterCollectorSource(root, true); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := store.ListCollectorSources(true)
	if err != nil {
		t.Fatal(err)
	}
	collector, err := tokenlogger.NewCollector(store, []string{healthyRoot, degradedRoot}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	if err := updateRegisteredSourceHealth(store, sources, collector.Health()); err != nil {
		t.Fatal(err)
	}
	sources, err = store.ListCollectorSources(true)
	if err != nil {
		t.Fatal(err)
	}
	statusByPath := map[string]tokenlogger.RegisteredSource{}
	for _, source := range sources {
		statusByPath[source.Path] = source
	}
	if source := statusByPath[healthyRoot]; source.Status != "healthy" || source.LastError != "" {
		t.Fatalf("healthy source=%+v", source)
	}
	if source := statusByPath[degradedRoot]; source.Status != "degraded" || !strings.Contains(source.LastError, "malformed_record=1") {
		t.Fatalf("degraded source=%+v", source)
	}
}

func TestInteractiveCollectorDoesNotDuplicateBackgroundLease(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.AcquireCollectorLease("background-instance", 99, tokenlogger.Version, "background", time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCollectorReady("background-instance"); err != nil {
		t.Fatal(err)
	}
	defer store.ReleaseCollectorLease("background-instance", "stopped", "")
	settings := defaultSettings(dataDir)
	settings.OnboardingComplete, settings.CollectorEnabled, settings.AutoStart = true, true, true
	var stdout, stderr bytes.Buffer
	code := runInteractiveScanner(bufio.NewScanner(strings.NewReader("/quit\n")), &stdout, &stderr, store, dataDir, settings, false)
	if code != 0 {
		t.Fatalf("interactive code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "background collector already active") || !strings.Contains(stdout.String(), "mode=background") {
		t.Fatalf("interactive output did not reuse background status: %q", stdout.String())
	}
	status, err := store.CollectorRuntimeStatus(time.Minute)
	if err != nil || status.InstanceID != "background-instance" || status.State != "running" {
		t.Fatalf("background lease changed: %+v err=%v", status, err)
	}
}

func TestImportJSONOutcomeContract(t *testing.T) {
	dataDir := t.TempDir()
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "codex", "normal.jsonl")
	contents, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = "pr74-private-project-sentinel"
	contents = bytes.Replace(contents, []byte("Sanitized Fixture Project"), []byte(sentinel), 1)
	sourcePath := filepath.Join(t.TempDir(), "private-codex-transcript.jsonl")
	if err := os.WriteFile(sourcePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	outputs := make([]string, 0, 2)
	for runIndex := 0; runIndex < 2; runIndex++ {
		var stdout, stderr bytes.Buffer
		code := run([]string{"import", sourcePath, "--format", "json", "--data-dir", dataDir}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("import run %d exit code = %d, stderr = %q", runIndex+1, code, stderr.String())
		}
		outputs = append(outputs, stdout.String())
		if strings.Contains(stdout.String(), sourcePath) || strings.Contains(stdout.String(), filepath.Base(sourcePath)) || strings.Contains(stdout.String(), sentinel) {
			t.Fatalf("machine output disclosed source path or transcript content: %q", stdout.String())
		}
	}

	const firstImport = `{"format_version":1,"sources_discovered":1,"sources_processed":1,"imported_events":2,"duplicate_events":0,"outcomes":{"ignored_non_usage":3,"unchanged_snapshot":1,"deferred_incomplete_tail":0,"unsupported_usage":0,"malformed_record":0,"failed_source":0},"failed_sources":0,"complete":true,"pending":false}` + "\n"
	const repeatedImport = `{"format_version":1,"sources_discovered":1,"sources_processed":1,"imported_events":0,"duplicate_events":0,"outcomes":{"ignored_non_usage":0,"unchanged_snapshot":1,"deferred_incomplete_tail":0,"unsupported_usage":0,"malformed_record":0,"failed_source":0},"failed_sources":0,"complete":true,"pending":false}` + "\n"
	if outputs[0] != firstImport {
		t.Errorf("first import JSON contract mismatch:\n got: %q\nwant: %q", outputs[0], firstImport)
	}
	if outputs[1] != repeatedImport {
		t.Errorf("repeated import JSON contract mismatch:\n got: %q\nwant: %q", outputs[1], repeatedImport)
	}
}

func TestImportWarningAndPendingExitBehavior(t *testing.T) {
	t.Run("unsupported usage warns", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "unsupported.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"token_usage_record","timestamp":"2026-10-11T00:00:00Z","input_tokens":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"import", path, "--format", "json", "--data-dir", t.TempDir()}, &stdout, &stderr); code != 3 {
			t.Fatalf("warning exit code = %d, stderr=%q output=%q", code, stderr.String(), stdout.String())
		}
		var result importCommandOutput
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Outcomes.UnsupportedUsage != 1 || result.Complete || result.Pending {
			t.Fatalf("unexpected warning result: %+v", result)
		}
	})

	t.Run("incomplete final line remains retryable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "partial.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"token_count","payload":`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"import", path, "--format", "json", "--data-dir", t.TempDir()}, &stdout, &stderr); code != 0 {
			t.Fatalf("pending exit code = %d, stderr=%q", code, stderr.String())
		}
		var result importCommandOutput
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Outcomes.DeferredIncompleteTail != 1 || !result.Pending || result.Complete {
			t.Fatalf("unexpected pending result: %+v", result)
		}
	})

	t.Run("warning and pending remain independent in text", func(t *testing.T) {
		sourceDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(sourceDir, "unsupported.jsonl"), []byte(`{"type":"token_usage_record","input_tokens":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, "partial.jsonl"), []byte(`{"type":"token_count","payload":`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"import", sourceDir, "--data-dir", t.TempDir()}, &stdout, &stderr); code != 3 {
			t.Fatalf("combined exit code = %d, stderr=%q output=%q", code, stderr.String(), stdout.String())
		}
		for _, expected := range []string{"Pending:", "Warning:", "Status: incomplete."} {
			if !strings.Contains(stdout.String(), expected) {
				t.Fatalf("combined output omitted %q: %q", expected, stdout.String())
			}
		}
	})
}

func TestWatchOutputIncludesSubsequentWarningAndFinalOutcomes(t *testing.T) {
	result := tokenlogger.ImportResult{Outcomes: tokenlogger.OutcomeCounts{UnsupportedUsage: 1}}
	var output bytes.Buffer
	writeWatchPoll(&output, result, tokenlogger.CollectorHealth{Status: "degraded", Sources: 1})
	writeWatchSummary(&output, tokenlogger.CollectorHealth{
		Status: "degraded", Lifecycle: "stopped", Sources: 1, Outcomes: result.Outcomes,
	})
	for _, expected := range []string{
		"poll status=degraded sources=1", "imported=0", "unsupported_usage=1", "complete=false",
		"collector stopped:", "pending=false",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("watch output omitted %q: %q", expected, output.String())
		}
	}
}

func TestWatchDiscoveryFailureStopNeverClaimsComplete(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := cancelOnWriteBuffer{marker: "poll status=", cancel: cancel}
	var stderr bytes.Buffer
	code := runWatchWithContext([]string{filepath.Join(parent, "child"), "--data-dir", t.TempDir()}, &stdout, &stderr, ctx)
	if code != 0 {
		t.Fatalf("cancelled watch exit=%d stderr=%q output=%q", code, stderr.String(), stdout.String())
	}
	for _, expected := range []string{"poll status=degraded sources=0 discovery_failures=1", "failed_source=1", "collector stopped: lifecycle=stopped latest_status=degraded", "run_complete=false"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("failed watch output omitted %q: %q", expected, stdout.String())
		}
	}
}

func TestWatchInitialPollHonorsCancellation(t *testing.T) {
	dataDir := t.TempDir()
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := runWatchWithContext([]string{fixture, "--data-dir", dataDir}, &stdout, &stderr, ctx)
	if code != 0 {
		t.Fatalf("cancelled initial watch exit=%d stderr=%q output=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "interrupted=true run_complete=false") {
		t.Fatalf("cancelled initial watch claimed completion: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "poll status=cancelled") || !strings.Contains(stdout.String(), "interrupted=true complete=false") {
		t.Fatalf("cancelled initial poll claimed completion: %q", stdout.String())
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if count, err := store.EventCount(); err != nil || count != 0 {
		t.Fatalf("cancelled initial watch persisted events=%d err=%v", count, err)
	}
}

func TestWatchInterruptedStopNeverClaimsComplete(t *testing.T) {
	var output bytes.Buffer
	writeWatchSummary(&output, tokenlogger.CollectorHealth{
		Status: "cancelled", Lifecycle: "stopped", Sources: 1, Interrupted: true,
	})
	if !strings.Contains(output.String(), "interrupted=true run_complete=false") {
		t.Fatalf("interrupted watch claimed completion: %q", output.String())
	}
}

func TestAccumulateWatchHealthPreservesRunTotalsAndInterruptedState(t *testing.T) {
	run := tokenlogger.CollectorHealth{
		Imported:    2,
		Duplicates:  1,
		Outcomes:    tokenlogger.OutcomeCounts{FailedSource: 1},
		Interrupted: true,
		Sources:     3,
	}
	result := tokenlogger.ImportResult{
		Receipt:  tokenlogger.Receipt{Imported: 4, Duplicates: 2},
		Outcomes: tokenlogger.OutcomeCounts{UnchangedSnapshot: 1},
	}
	latest := tokenlogger.CollectorHealth{Status: "healthy", Sources: 1}

	got := accumulateWatchHealth(run, result, latest)
	if got.Imported != 6 || got.Duplicates != 3 || got.Outcomes.FailedSource != 1 || got.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("cumulative health=%+v", got)
	}
	if !got.Interrupted || got.LatestInterrupted || got.Status != "healthy" || got.Sources != 1 {
		t.Fatalf("sticky/latest health=%+v", got)
	}
}

func TestCollectorStatusShowsFailureRecoveryAndFreshness(t *testing.T) {
	store, err := tokenlogger.Open(filepath.Join(t.TempDir(), "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(t.TempDir(), "later.jsonl")
	collector, err := tokenlogger.NewCollector(store, []string{path}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err == nil {
		t.Fatal("missing required root was reported as successful")
	}
	degraded := collectorStatusLine(collector)
	for _, expected := range []string{"health=degraded", "sources=0", "discovery_failures=1", "last_completed=", "last_success=unavailable", "failed_source=1"} {
		if !strings.Contains(degraded, expected) {
			t.Fatalf("degraded status omitted %q: %s", expected, degraded)
		}
	}
	contents, err := os.ReadFile(filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	recovered := collectorStatusLine(collector)
	for _, expected := range []string{"health=healthy", "sources=1", "discovery_failures=0", "last_success="} {
		if !strings.Contains(recovered, expected) {
			t.Fatalf("recovered status omitted %q: %s", expected, recovered)
		}
	}
	if strings.Contains(recovered, "last_success=unavailable") {
		t.Fatalf("recovered status omitted successful scan time: %s", recovered)
	}
}

func TestDefaultCollectorRootsAugmentDefaultsAndMarkConfiguredRootsRequired(t *testing.T) {
	t.Setenv("TOKEN_LOGGER_CODEX_DIR", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("TOKEN_LOGGER_CLAUDE_DIR", filepath.Join(t.TempDir(), "claude"))
	roots := defaultCollectorRoots()
	if len(roots) < 4 {
		t.Fatalf("collector roots = %+v, want configured roots plus two defaults", roots)
	}
	if !roots[0].Required || !roots[1].Required || roots[2].Required || roots[3].Required {
		t.Fatalf("collector root requirements = %+v", roots)
	}
}

func TestImportFixtureMatchesHarnessCanonicalUsageProjection(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		events  []tokenlogger.UsageEvent
	}{
		{
			name:    "codex",
			fixture: filepath.Join("..", "..", "library", "token-logger", "testdata", "codex", "normal.jsonl"),
			events: []tokenlogger.UsageEvent{
				canonicalDifferentialEvent("token-count-1", time.Date(2026, 10, 11, 10, 0, 2, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 100, OutputTokens: 30, CacheReadTokens: 20, ReasoningTokens: 10}, "project-fixture", "codex-session-fixture", "codex-cli", "openai", "gpt-5-codex"),
				canonicalDifferentialEvent("token-count-2", time.Date(2026, 10, 11, 10, 0, 3, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 50, OutputTokens: 20, CacheReadTokens: 10, ReasoningTokens: 5}, "project-fixture", "codex-session-fixture", "codex-cli", "openai", "gpt-5-codex"),
			},
		},
		{
			name:    "codex token usage records",
			fixture: filepath.Join("..", "..", "library", "token-logger", "testdata", "codex", "token-usage-records.jsonl"),
			events: []tokenlogger.UsageEvent{
				canonicalDifferentialEvent("legacy-prefix", time.Date(2026, 10, 11, 10, 0, 1, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 100, OutputTokens: 10}, "record-project", "codex-record-session", "codex-cli", "openai", "gpt-5-codex"),
				canonicalDifferentialEvent("record-response-1", time.Date(2026, 10, 11, 10, 0, 3, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 20, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, ReasoningTokens: 1}, "record-project", "codex-record-session", "codex-cli", "openai", "gpt-5-codex"),
				canonicalDifferentialEvent("record-response-2", time.Date(2026, 10, 11, 10, 0, 5, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 30, OutputTokens: 3, ReasoningTokens: 1}, "record-project", "codex-record-session", "codex-cli", "openai", "gpt-5-codex-mini"),
			},
		},
		{
			name:    "claude",
			fixture: filepath.Join("..", "..", "library", "token-logger", "testdata", "claude-code", "normal.jsonl"),
			events: []tokenlogger.UsageEvent{
				canonicalDifferentialEvent("claude-message-1", time.Date(2026, 10, 11, 11, 0, 1, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 110, OutputTokens: 15, CacheReadTokens: 20, CacheWriteTokens: 10}, "claude-project-fixture", "claude-session-fixture", "claude-code", "anthropic", "claude-sonnet-fixture"),
				canonicalDifferentialEvent("claude-message-2", time.Date(2026, 10, 11, 11, 0, 2, 0, time.UTC), tokenlogger.TokenUsage{InputTokens: 45, OutputTokens: 8, CacheReadTokens: 5}, "claude-project-fixture", "claude-session-fixture", "claude-code", "anthropic", "claude-sonnet-fixture"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertImportMatchesHarness(t, test.fixture, test.events)
		})
	}
}

func assertImportMatchesHarness(t *testing.T, fixture string, events []tokenlogger.UsageEvent) {
	t.Helper()
	importDataDir := t.TempDir()
	var importOutput, importErr bytes.Buffer
	if code := run([]string{"import", fixture, "--data-dir", importDataDir}, &importOutput, &importErr); code != 0 {
		t.Fatalf("fixture import exit=%d stderr=%q", code, importErr.String())
	}

	harnessDataDir := t.TempDir()
	requests := []map[string]any{
		{"protocol": protocolName, "version": protocolVersion, "id": "1", "method": "initialize", "params": map[string]any{"data_dir": harnessDataDir}},
	}
	for index, event := range events {
		requests = append(requests, map[string]any{
			"protocol": protocolName, "version": protocolVersion, "id": decimalString(index + 2),
			"method": "record", "params": canonicalDifferentialRecord(event),
		})
	}
	requests = append(requests, map[string]any{
		"protocol": protocolName, "version": protocolVersion, "id": decimalString(len(events) + 2), "method": "shutdown",
	})
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var harnessOutput, harnessErr bytes.Buffer
	if code := runProtocol(&input, &harnessOutput, &harnessErr); code != 0 {
		t.Fatalf("harness exit=%d stderr=%q output=%q", code, harnessErr.String(), harnessOutput.String())
	}
	importedBefore := eventProjections(t, importDataDir)
	harnessedBefore := eventProjections(t, harnessDataDir)
	if len(importedBefore) != len(events) || len(harnessedBefore) != len(events) {
		t.Fatalf("unexpected initial event projections: import=%d harness=%d", len(importedBefore), len(harnessedBefore))
	}
	for index := range importedBefore {
		if !reflect.DeepEqual(canonicalProjection(importedBefore[index]), canonicalProjection(harnessedBefore[index])) {
			t.Fatalf("event projection %d differs: import=%+v harness=%+v", index, importedBefore[index], harnessedBefore[index])
		}
	}

	filter := tokenlogger.UsageFilter{
		From:    time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		GroupBy: []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel},
	}
	imported := queryReport(t, importDataDir, filter)
	harnessed := queryReport(t, harnessDataDir, filter)
	if !reflect.DeepEqual(imported.Summary, harnessed.Summary) || !reflect.DeepEqual(imported.Rows, harnessed.Rows) {
		t.Fatalf("canonical usage differs: import=%+v harness=%+v", imported, harnessed)
	}

	var replayOutput, replayErr bytes.Buffer
	if code := run([]string{"import", fixture, "--data-dir", importDataDir}, &replayOutput, &replayErr); code != 0 || !strings.Contains(replayOutput.String(), "unchanged_snapshot=1") || !strings.Contains(replayOutput.String(), "storage duplicates: 0") {
		t.Fatalf("fixture replay code=%d stderr=%q output=%q", code, replayErr.String(), replayOutput.String())
	}
	assertProjectionStable(t, importedBefore, eventProjections(t, importDataDir), "fixture replay")

	replayRequests := []map[string]any{
		{"protocol": protocolName, "version": protocolVersion, "id": "replay-1", "method": "initialize", "params": map[string]any{"data_dir": harnessDataDir}},
	}
	for index, event := range events {
		replayRequests = append(replayRequests, map[string]any{
			"protocol": protocolName, "version": protocolVersion, "id": "replay-" + decimalString(index+2),
			"method": "record", "params": canonicalDifferentialRecord(event),
		})
	}
	replayRequests = append(replayRequests, map[string]any{
		"protocol": protocolName, "version": protocolVersion, "id": "replay-shutdown", "method": "shutdown",
	})
	var replayInput, replayHarnessOutput, replayHarnessErr bytes.Buffer
	for _, request := range replayRequests {
		if err := json.NewEncoder(&replayInput).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	if code := runProtocol(&replayInput, &replayHarnessOutput, &replayHarnessErr); code != 0 {
		t.Fatalf("harness replay code=%d stderr=%q output=%q", code, replayHarnessErr.String(), replayHarnessOutput.String())
	}
	var responses []protocolResponse
	decoder := json.NewDecoder(&replayHarnessOutput)
	for {
		var response protocolResponse
		if err := decoder.Decode(&response); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	for _, response := range responses[1 : 1+len(events)] {
		result, ok := response.Result.(map[string]any)
		if !ok || result["imported"] != float64(0) || result["duplicates"] != float64(1) {
			t.Fatalf("unexpected harness replay response: %+v", response)
		}
	}
	assertProjectionStable(t, harnessedBefore, eventProjections(t, harnessDataDir), "harness replay")
}

func canonicalDifferentialEvent(id string, occurredAt time.Time, usage tokenlogger.TokenUsage, projectID, sessionID, client, provider, model string) tokenlogger.UsageEvent {
	return tokenlogger.UsageEvent{
		EventID: "differential-" + id, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		ProjectID: projectID, SessionID: sessionID,
		Client: tokenlogger.Client{Name: client}, Provider: provider, RequestedModel: model, ResolvedModel: model,
		Usage: usage, Provenance: tokenlogger.Provenance{Adapter: "fixture", SourceID: "fixture", SourceRecord: id},
		IngestionKey: "differential:" + id,
	}
}

func canonicalDifferentialRecord(event tokenlogger.UsageEvent) map[string]any {
	return map[string]any{
		"event":   event,
		"project": tokenlogger.Project{ID: event.ProjectID, Root: "/sanitized/project", DisplayName: "Sanitized Fixture Project"},
		"session": tokenlogger.Session{ID: event.SessionID, ProjectID: event.ProjectID, Client: event.Client},
	}
}

func queryReport(t *testing.T, dataDir string, filter tokenlogger.UsageFilter) tokenlogger.UsageReport {
	t.Helper()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

type testEventProjection struct {
	EventID        string
	IngestionKey   string
	OccurredAt     time.Time
	ProjectID      string
	SessionID      string
	ClientName     string
	Provider       string
	RequestedModel string
	ResolvedModel  string
	Usage          tokenlogger.TokenUsage
	SourceID       string
	SourceRecord   string
}

func eventProjections(t *testing.T, dataDir string) []testEventProjection {
	t.Helper()
	// This is intentionally test-only SQL access. The product library does not
	// expose an unbounded event enumeration API just for this differential test.
	database, err := sql.Open("sqlite", filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`SELECT event_id, ingestion_key, occurred_at,
		COALESCE(project_id, ''), COALESCE(session_id, ''), client_name, provider_name,
		requested_model, resolved_model, input_tokens, output_tokens, cache_read_tokens,
		cache_write_tokens, reasoning_tokens, source_id, source_record_id
		FROM usage_events ORDER BY occurred_at, event_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var projections []testEventProjection
	for rows.Next() {
		var projection testEventProjection
		var occurredAt string
		if err := rows.Scan(&projection.EventID, &projection.IngestionKey, &occurredAt,
			&projection.ProjectID, &projection.SessionID, &projection.ClientName, &projection.Provider,
			&projection.RequestedModel, &projection.ResolvedModel, &projection.Usage.InputTokens,
			&projection.Usage.OutputTokens, &projection.Usage.CacheReadTokens, &projection.Usage.CacheWriteTokens,
			&projection.Usage.ReasoningTokens, &projection.SourceID, &projection.SourceRecord); err != nil {
			t.Fatal(err)
		}
		projection.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			t.Fatal(err)
		}
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return projections
}

func canonicalProjection(event testEventProjection) testEventProjection {
	event.EventID = ""
	event.IngestionKey = ""
	event.SourceID = ""
	// Provider imports reconcile aliases to KompoSyn-owned IDs, while harness
	// callers supply local IDs explicitly. Identity is tested separately from
	// normalized usage equivalence.
	event.ProjectID = ""
	event.SessionID = ""
	return event
}

func assertProjectionStable(t *testing.T, before, after []testEventProjection, label string) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s changed persisted event projections: before=%+v after=%+v", label, before, after)
	}
}

func decimalString(value int) string {
	return fmt.Sprint(value)
}

func TestCLIUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unknown"}, &stdout, &stderr); code != 1 {
		t.Fatalf("unknown command exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "unknown"`) || !strings.Contains(stderr.String(), "--help") {
		t.Fatalf("missing unknown-command help guidance: %q", stderr.String())
	}
}

func TestProjectAndSessionResourceCommands(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	event := cliReportEvent("resource-command", time.Now().UTC().Add(-time.Hour))
	event.ProjectID, event.SessionID = "local-project", "local-session"
	event.Usage = tokenlogger.TokenUsage{InputTokens: 600_000, CacheReadTokens: 120_000, CacheWriteTokens: 30_000, OutputTokens: 400_000, ReasoningTokens: 250_000}
	project := tokenlogger.Project{ID: event.ProjectID, Root: filepath.Join(t.TempDir(), "workspace"), DisplayName: "Readable Project"}
	session := tokenlogger.Session{ID: event.SessionID, ProjectID: event.ProjectID, Client: event.Client}
	if _, err := store.AppendWithReferences([]tokenlogger.UsageEvent{event}, []tokenlogger.Project{project}, []tokenlogger.Session{session}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	var projectsOut, projectsErr bytes.Buffer
	if code := run([]string{"projects", "list", "--data-dir", dataDir, "--format", "json"}, &projectsOut, &projectsErr); code != 0 {
		t.Fatalf("projects list exit=%d stderr=%q", code, projectsErr.String())
	}
	if !strings.Contains(projectsOut.String(), `"format_version": 1`) || !strings.Contains(projectsOut.String(), `"display_name": "Readable Project"`) || !strings.Contains(projectsOut.String(), `"request_count": 1`) || !strings.Contains(projectsOut.String(), `"total_tokens": 1000000`) {
		t.Fatalf("unexpected projects JSON: %s", projectsOut.String())
	}
	var projectsCSVOut, projectsCSVErr bytes.Buffer
	if code := run([]string{"projects", "list", "--data-dir", dataDir, "--format", "csv"}, &projectsCSVOut, &projectsCSVErr); code != 0 {
		t.Fatalf("projects list CSV exit=%d stderr=%q", code, projectsCSVErr.String())
	}
	projectRecords, err := csv.NewReader(strings.NewReader(projectsCSVOut.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(projectRecords) != 2 || projectRecords[0][0] != "format_version" || projectRecords[0][1] != "project_id" || projectRecords[1][0] != "1" || projectRecords[1][1] != "local-project" || !reflect.DeepEqual(projectRecords[1][10:16], []string{"600000", "120000", "30000", "400000", "250000", "1000000"}) {
		t.Fatalf("unexpected project-list CSV: %#v", projectRecords)
	}

	var sessionsOut, sessionsErr bytes.Buffer
	if code := run([]string{"sessions", "list", "--project", "local-project", "--data-dir", dataDir, "--format", "json"}, &sessionsOut, &sessionsErr); code != 0 {
		t.Fatalf("sessions list exit=%d stderr=%q", code, sessionsErr.String())
	}
	if !strings.Contains(sessionsOut.String(), `"format_version": 1`) || !strings.Contains(sessionsOut.String(), `"id": "local-session"`) || !strings.Contains(sessionsOut.String(), event.ResolvedModel) || !strings.Contains(sessionsOut.String(), `"total_tokens": 1000000`) {
		t.Fatalf("unexpected sessions JSON: %s", sessionsOut.String())
	}
	var sessionsCSVOut, sessionsCSVErr bytes.Buffer
	if code := run([]string{"sessions", "list", "--project", "local-project", "--data-dir", dataDir, "--format", "csv"}, &sessionsCSVOut, &sessionsCSVErr); code != 0 {
		t.Fatalf("sessions list CSV exit=%d stderr=%q", code, sessionsCSVErr.String())
	}
	sessionRecords, err := csv.NewReader(strings.NewReader(sessionsCSVOut.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionRecords) != 2 || sessionRecords[0][0] != "format_version" || sessionRecords[0][1] != "session_id" || sessionRecords[1][0] != "1" || sessionRecords[1][1] != "local-session" || !reflect.DeepEqual(sessionRecords[1][10:16], []string{"600000", "120000", "30000", "400000", "250000", "1000000"}) {
		t.Fatalf("unexpected session-list CSV: %#v", sessionRecords)
	}

	var detailOut, detailErr bytes.Buffer
	if code := run([]string{"sessions", "show", "local-session", "--data-dir", dataDir, "--format", "csv", "--group-by", "model"}, &detailOut, &detailErr); code != 0 {
		t.Fatalf("sessions show CSV exit=%d stderr=%q", code, detailErr.String())
	}
	reader := csv.NewReader(strings.NewReader(detailOut.String()))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantModel := event.ResolvedModel
	if wantModel == "" {
		wantModel = event.RequestedModel
	}
	if len(records) != 2 || records[0][0] != "format_version" || records[0][1] != "session_id" || records[1][0] != "1" || records[1][1] != "local-session" || records[1][4] != wantModel || !reflect.DeepEqual(records[1][8:14], []string{"600000", "120000", "30000", "400000", "250000", "1000000"}) {
		t.Fatalf("unexpected session detail CSV: %#v", records)
	}
}

func TestInteractiveHierarchicalNavigationAndRangePersistence(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	event := cliReportEvent("navigation", now.Add(-time.Hour))
	event.ProjectID, event.SessionID, event.ResolvedModel = "nav-project", "nav-session", "nav model"
	event.Usage = tokenlogger.TokenUsage{InputTokens: 600_000, CacheReadTokens: 120_000, CacheWriteTokens: 30_000, OutputTokens: 400_000, ReasoningTokens: 250_000}
	if _, err := store.AppendWithReferences([]tokenlogger.UsageEvent{event},
		[]tokenlogger.Project{{ID: event.ProjectID, Root: "/workspace/navigation", DisplayName: "Navigation Project"}},
		[]tokenlogger.Session{{ID: event.SessionID, ProjectID: event.ProjectID, Client: event.Client}}); err != nil {
		t.Fatal(err)
	}
	project, err := store.ProjectResource(event.ProjectID, tokenlogger.ResourceFilter{From: now.AddDate(0, 0, -1), To: now.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	expectedLocation := "LOCATION: " + humanLocation(project.Location)
	commands := "/range 2026-10-14 2026-10-16\n/projects\n/project Navigation Project\n/sessions\n/session nav-session\n/models\n/model nav model\n/back\n/home\n/range\n/quit\n"
	var stdout, stderr bytes.Buffer
	reader := &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(commands)), output: io.Discard}
	if code := runInteractiveLoopWithClock(reader, &stdout, &stderr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return now }); code != 0 {
		t.Fatalf("interactive exit=%d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"Navigation Project", expectedLocation, "nav-session", "nav model", "komposyn / project:Navigation Project", "session:nav-session", "model:nav model", "600K", "120K", "30K", "400K", "250K", "1M", "active range (pinned): 2026-10-14T00:00:00Z to 2026-10-16T00:00:00Z"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("interactive navigation missing %q: %s", expected, output)
		}
	}
	for _, raw := range []string{"600000", "120000", "30000", "400000", "250000", "1000000"} {
		if strings.Contains(output, raw) {
			t.Fatalf("interactive navigation exposed unformatted token count %q: %s", raw, output)
		}
	}
}

func TestInteractiveProjectHeaderUsesUnavailableLocationFallback(t *testing.T) {
	var output bytes.Buffer
	writeInteractiveProjectHeader(&output, &interactiveState{projectName: "No Location"})
	if got := output.String(); !strings.Contains(got, "PROJECT: No Location") || !strings.Contains(got, "LOCATION: location unavailable") {
		t.Fatalf("missing-location project header = %q", got)
	}
}

func TestInteractiveSelectorsUseDisplayedSnapshots(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	filter := tokenlogger.ResourceFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	state := interactiveState{view: "projects", projectRows: []tokenlogger.ProjectResource{{ID: "displayed-b", DisplayName: "B"}}}
	if err := store.UpsertProject(tokenlogger.Project{ID: "new-a", DisplayName: "A"}); err != nil {
		t.Fatal(err)
	}
	selected, err := selectInteractiveProject(store, &state, filter, "1")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "displayed-b" {
		t.Fatalf("row selection used a refreshed query: got %q", selected.ID)
	}
	state.view = "sessions"
	state.sessionRows = []tokenlogger.SessionResource{{ID: "displayed-session", ProjectID: "displayed-b"}}
	session, err := selectInteractiveSession(store, &state, filter, "1")
	if err != nil || session.ID != "displayed-session" {
		t.Fatalf("session snapshot selection = %+v, %v", session, err)
	}
}

func TestUnattributedModelSelectionHasExplicitState(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := tokenlogger.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	event := cliReportEvent("missing-model", from.Add(time.Hour))
	event.ProjectID, event.SessionID = "project", "session"
	if _, err := store.AppendWithReferences([]tokenlogger.UsageEvent{event}, []tokenlogger.Project{{ID: "project", DisplayName: "Project"}}, []tokenlogger.Session{{ID: "session", ProjectID: "project", Client: event.Client}}); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("UPDATE usage_events SET requested_model = '', resolved_model = '' WHERE event_id = ?", event.EventID); err != nil {
		t.Fatal(err)
	}
	state := interactiveState{from: from, to: from.AddDate(0, 0, 1), view: "models", projectID: "project", projectName: "Project", sessionID: "session", modelRows: []interactiveModelSelection{{Model: "", Provider: event.Provider}}}
	selection, err := selectInteractiveModel(&state, "Unattributed")
	if err != nil {
		t.Fatal(err)
	}
	state.model, state.modelProvider, state.modelMissing, state.modelSelected = selection.Model, selection.Provider, selection.Model == "", true
	report, err := store.Query(interactiveUsageFilter(&state))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RequestCount != 1 || !strings.Contains(interactiveBreadcrumb(&state), "model:Unattributed") {
		t.Fatalf("missing-model state report=%+v breadcrumb=%q", report.Summary, interactiveBreadcrumb(&state))
	}
	state.clearModelSelection()
	if strings.Contains(interactiveBreadcrumb(&state), "model:") {
		t.Fatalf("cleared model remained in breadcrumb: %q", interactiveBreadcrumb(&state))
	}
}

func TestModelsCommandClearsDescendantSelection(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := tokenlogger.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	events := []tokenlogger.UsageEvent{
		cliReportEvent("provider-a", now.Add(-3*time.Hour)),
		cliReportEvent("provider-b", now.Add(-2*time.Hour)),
		cliReportEvent("missing-model", now.Add(-time.Hour)),
	}
	for index := range events {
		events[index].ProjectID, events[index].SessionID = "project", "session"
		events[index].ResolvedModel = "shared model"
		events[index].Provider = fmt.Sprintf("provider-%c", 'a'+index)
	}
	if _, err := store.AppendWithReferences(events,
		[]tokenlogger.Project{{ID: "project", DisplayName: "Project"}},
		[]tokenlogger.Session{{ID: "session", ProjectID: "project", Client: events[0].Client}}); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("UPDATE usage_events SET requested_model = '', resolved_model = '' WHERE event_id = ?", events[2].EventID); err != nil {
		t.Fatal(err)
	}
	commands := "/range 2026-10-14 2026-10-16\n/projects\n/project 1\n/sessions\n/session 1\n/models\n/model 1\n/models\n/quit\n"
	var stdout, stderr bytes.Buffer
	reader := &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(commands)), output: io.Discard}
	if code := runInteractiveLoopWithClock(reader, &stdout, &stderr, store, t.TempDir(), defaultSettings(t.TempDir()), false, func() time.Time { return now }); code != 0 {
		t.Fatalf("interactive exit=%d stderr=%q", code, stderr.String())
	}
	marker := "komposyn / project:Project / session:session\n"
	index := strings.LastIndex(stdout.String(), marker)
	if index < 0 {
		t.Fatalf("final models breadcrumb missing: %s", stdout.String())
	}
	finalModels := stdout.String()[index:]
	if strings.Contains(strings.SplitN(finalModels, "\n", 2)[0], "model:") || strings.Count(finalModels, "shared model") < 2 || !strings.Contains(finalModels, "(unattributed)") {
		t.Fatalf("/models retained descendant selection or lost model rows: %s", finalModels)
	}
}

func TestResourceHumanOutputEscapesUntrustedMetadataAndPreservesUnicode(t *testing.T) {
	unsafe := "name\nwith\ttab\x1b[31m\u202E界"
	project := tokenlogger.ProjectResource{ID: "id\n\x1b", DisplayName: unsafe, Location: "/tmp/" + unsafe}
	var projectOut bytes.Buffer
	if err := writeProjectTable(&projectOut, []tokenlogger.ProjectResource{project}); err != nil {
		t.Fatal(err)
	}
	output := projectOut.String()
	if strings.ContainsAny(output, "\t\r\x1b") || strings.Contains(output, "\u202E") || !strings.Contains(output, `\n`) || !strings.Contains(output, `\t`) || !strings.Contains(output, `\x1B`) || !strings.Contains(output, `\u202E`) || !strings.Contains(output, "界") {
		t.Fatalf("unsafe or incorrectly escaped project table: %q", output)
	}
	state := interactiveState{projectID: "id", projectName: unsafe, sessionID: "session\n", model: unsafe, modelSelected: true}
	breadcrumb := interactiveBreadcrumb(&state)
	if strings.ContainsAny(breadcrumb, "\n\t\r\x1b") || !strings.Contains(breadcrumb, `\n`) || !strings.Contains(breadcrumb, `\x1B`) {
		t.Fatalf("unsafe breadcrumb: %q", breadcrumb)
	}
}

func TestResourceTablesUseCompactTokenCounts(t *testing.T) {
	var projectOutput bytes.Buffer
	project := tokenlogger.ProjectResource{ID: "project", DisplayName: "Project", Location: "/workspace", Summary: tokenlogger.UsageMeasures{TotalTokens: 1_000_000}}
	if err := writeProjectTable(&projectOutput, []tokenlogger.ProjectResource{project}); err != nil {
		t.Fatal(err)
	}
	projectFields := strings.Fields(strings.Split(projectOutput.String(), "\n")[1])
	if len(projectFields) < 7 || projectFields[6] != "1M" {
		t.Fatalf("project token cell was not compact: %q", projectOutput.String())
	}

	var sessionOutput bytes.Buffer
	session := tokenlogger.SessionResource{ID: "session", Client: "client", Summary: tokenlogger.UsageMeasures{TotalTokens: 1_000_000_000}}
	if err := writeSessionTable(&sessionOutput, []tokenlogger.SessionResource{session}); err != nil {
		t.Fatal(err)
	}
	sessionFields := strings.Fields(strings.Split(sessionOutput.String(), "\n")[1])
	if len(sessionFields) < 8 || sessionFields[7] != "1B" {
		t.Fatalf("session token cell was not compact: %q", sessionOutput.String())
	}

	row := tokenlogger.UsageRow{Dimensions: map[string]string{tokenlogger.GroupModel: "model", tokenlogger.GroupProvider: "provider"}, UsageMeasures: tokenlogger.UsageMeasures{
		RequestCount: 7, InputTokens: 999, CachedInputTokens: 1_000, CacheWriteInputTokens: 1_050,
		OutputTokens: 999_950, ReasoningOutputTokens: 1_000_000_000, TotalTokens: 1_000_000_000_000,
	}}
	var modelOutput bytes.Buffer
	if err := writeInteractiveModelTable(&modelOutput, tokenlogger.UsageReport{Rows: []tokenlogger.UsageRow{row}}); err != nil {
		t.Fatal(err)
	}
	modelFields := strings.Fields(strings.Split(modelOutput.String(), "\n")[1])
	want := []string{"7", "999", "1K", "1.1K", "1M", "1B", "1T"}
	if len(modelFields) < 10 || !reflect.DeepEqual(modelFields[3:10], want) {
		t.Fatalf("granular token cells = %v, want %v; table=%q", modelFields[3:10], want, modelOutput.String())
	}
}

func TestInteractiveModelTablePreservesCostSemantics(t *testing.T) {
	unsafeModel := "reported\n\x1b[31m\u202E界"
	unsafeProvider := "provider\t\x1b[2J"
	reported := tokenlogger.UsageRow{Dimensions: map[string]string{tokenlogger.GroupModel: unsafeModel, tokenlogger.GroupProvider: unsafeProvider}, UsageMeasures: tokenlogger.UsageMeasures{RequestCount: 1, UnknownCostRequests: 1, ProviderReportedCostMicros: 2_000_000, ProviderReportedCurrency: "USD", CostUnknownReason: "no_matching_rate"}}
	partial := tokenlogger.UsageRow{Dimensions: map[string]string{tokenlogger.GroupModel: "partial", tokenlogger.GroupProvider: "provider"}, UsageMeasures: tokenlogger.UsageMeasures{RequestCount: 2, EstimatedCostRequests: 1, UnknownCostRequests: 1, CalculatedCostMicros: 1_000_000, CalculatedCurrency: "USD", CostUnknownReason: "no_matching_rate"}}
	var output bytes.Buffer
	if err := writeInteractiveModelTable(&output, tokenlogger.UsageReport{Rows: []tokenlogger.UsageRow{reported, partial}}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ESTIMATE", "REPORTED", "STATUS", "2 USD", "reported only", "1 USD", "partial; 1 no public catalog rate"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("model table missing %q: %s", expected, output.String())
		}
	}
	if strings.ContainsAny(output.String(), "\t\r\x1b") || strings.Contains(output.String(), "\u202E") || !strings.Contains(output.String(), `\n`) || !strings.Contains(output.String(), `\t`) || !strings.Contains(output.String(), `\x1B`) || !strings.Contains(output.String(), `\u202E`) || !strings.Contains(output.String(), "界") {
		t.Fatalf("unsafe or incorrectly escaped model table: %q", output.String())
	}
}

func TestInteractiveModelTableKeepsPricingDetailsOnDemand(t *testing.T) {
	report := tokenlogger.UsageReport{
		Rows: []tokenlogger.UsageRow{{Dimensions: map[string]string{tokenlogger.GroupModel: "model", tokenlogger.GroupProvider: "provider"}, UsageMeasures: tokenlogger.UsageMeasures{RequestCount: 3, UnknownCostRequests: 3}}},
		PricingCoverage: []tokenlogger.PricingCoverageRow{{
			Provider: "provider",
			Model:    "model",
			Requests: 3,
			Reasons: map[string]int64{
				"no_matching_rate":        1,
				"outside_effective_range": 1,
				"billing_context_missing": 1,
			},
		}},
	}
	var output bytes.Buffer
	if err := writeInteractiveModelTable(&output, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "PRICING COVERAGE GAPS") || strings.Contains(output.String(), "Review exact-model catalog coverage") {
		t.Fatalf("interactive model table included opt-in pricing details: %s", output.String())
	}
	var pricing bytes.Buffer
	if err := tokenlogger.WriteHumanPricingCoverage(&pricing, report.PricingCoverage, 80); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Review exact-model catalog coverage", "Do not apply current prices retroactively", "Check for a verified rate for the affected token category"} {
		if !strings.Contains(pricing.String(), expected) {
			t.Errorf("pricing details missing %q: %s", expected, pricing.String())
		}
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), versionOutput(); got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--version exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), versionOutput(); got != want {
		t.Fatalf("--version output = %q, want %q", got, want)
	}
}

func TestUnequalCLICoreVersionsRemainSeparateAcrossSurfaces(t *testing.T) {
	oldCLI, oldCore := cliVersion, tokenlogger.Version
	cliVersion, tokenlogger.Version = "0.7.0", "0.6.0"
	defer func() { cliVersion, tokenlogger.Version = oldCLI, oldCore }()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version command exit=%d stderr=%q", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), "komposyn-lg 0.7.0 (core 0.6.0)"; got != want {
		t.Fatalf("version output=%q want=%q", got, want)
	}

	dataDir := t.TempDir()
	params, err := json.Marshal(initializeParams{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	server := &protocolServer{}
	response, _ := server.initialize(protocolRequest{ID: "version-check", Params: params}, io.Discard)
	if !response.OK {
		t.Fatalf("initialize failed: %+v", response.Error)
	}
	defer server.store.Close()
	manifest := response.Result.(map[string]any)
	if manifest["plugin_version"] != "0.6.0" || manifest["cli_version"] != "0.7.0" {
		t.Fatalf("protocol versions = core %v, cli %v", manifest["plugin_version"], manifest["cli_version"])
	}
	diagnostic, err := recordDiagnostic(server.store, "test report", tokenlogger.DiagnosticCategoryReporting)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.CoreVersion != "0.6.0" || diagnostic.CLIVersion != "0.7.0" || diagnostic.ProtocolVersion != protocolVersion {
		t.Fatalf("diagnostic version tuple = %+v", diagnostic)
	}
}

func TestParseReportTimezone(t *testing.T) {
	filter, _, _, err := parseReportArgs("usage", []string{"--timezone", "Asia/Kathmandu"})
	if err != nil {
		t.Fatalf("parse report timezone: %v", err)
	}
	if got, want := filter.Timezone.String(), "Asia/Kathmandu"; got != want {
		t.Fatalf("timezone = %q, want %q", got, want)
	}

	if _, _, _, err := parseReportArgs("usage", []string{"--timezone", "not/a-zone"}); err == nil {
		t.Fatal("invalid timezone unexpectedly accepted")
	}
}

func TestStandaloneBinaryHasIANATimezoneData(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load embedded IANA timezone data: %v", err)
	}
	if location.String() != "America/New_York" {
		t.Fatalf("timezone = %q, want America/New_York", location)
	}
}

func TestReportTimezoneAcrossOutputFormats(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	events := []tokenlogger.UsageEvent{
		cliReportEvent("dst-first", time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)),
		cliReportEvent("dst-second", time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)),
	}
	if _, err := store.Append(events); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	args := []string{"usage", "--data-dir", dataDir, "--format", "json", "--group-by", "hour", "--timezone", "America/New_York", "--from", "2026-10-31T00:00:00Z", "--to", "2026-11-02T00:00:00Z"}
	var jsonOutput, stderr bytes.Buffer
	if code := run(args, &jsonOutput, &stderr); code != 0 {
		t.Fatalf("JSON report exit code = %d, stderr = %q", code, stderr.String())
	}
	var report tokenlogger.UsageReport
	if err := json.Unmarshal(jsonOutput.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Timezone != "America/New_York" || len(report.Rows) != 2 {
		t.Fatalf("unexpected JSON report timezone/rows: %+v", report)
	}
	if report.Rows[0].Bucket != "2026-11-01T01:00:00-04:00" || report.Rows[1].Bucket != "2026-11-01T01:00:00-05:00" {
		t.Fatalf("unexpected DST fold buckets: %+v", report.Rows)
	}

	for _, format := range []string{"csv", "table"} {
		args[4] = format
		var output, formatStderr bytes.Buffer
		if code := run(args, &output, &formatStderr); code != 0 {
			t.Fatalf("%s report exit code = %d, stderr = %q", format, code, formatStderr.String())
		}
		if !strings.Contains(output.String(), "2026-11-01T01:00:00-04:00") || !strings.Contains(output.String(), "2026-11-01T01:00:00-05:00") {
			t.Fatalf("%s report omitted DST fold buckets: %q", format, output.String())
		}
	}
}

func TestReportTimezoneFractionalOffset(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append([]tokenlogger.UsageEvent{cliReportEvent("fractional", time.Date(2026, 10, 11, 0, 15, 0, 0, time.UTC))}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{"usage", "--data-dir", dataDir, "--format", "json", "--group-by", "hour", "--timezone", "Asia/Kathmandu", "--from", "2026-10-10T00:00:00Z", "--to", "2026-10-12T00:00:00Z"}
	var output, stderr bytes.Buffer
	if code := run(args, &output, &stderr); code != 0 {
		t.Fatalf("report exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(output.String(), "2026-10-11T06:00:00+05:45") {
		t.Fatalf("fractional-offset bucket missing: %q", output.String())
	}
}

func cliReportEvent(id string, occurredAt time.Time) tokenlogger.UsageEvent {
	return tokenlogger.UsageEvent{
		EventID: id, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		Client: tokenlogger.Client{Name: "test-client"}, Provider: "test-provider", RequestedModel: "test-model",
		Usage:        tokenlogger.TokenUsage{InputTokens: 1, OutputTokens: 1},
		Provenance:   tokenlogger.Provenance{Adapter: "test", SourceID: "test-source", SourceRecord: id},
		IngestionKey: "test-source:" + id,
	}
}

func TestInteractiveReportsUseHumanReadableTable(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := cliReportEvent("interactive-human-table", time.Now().UTC().Add(-time.Hour))
	event.Usage.InputTokens = 1_050
	event.Usage.OutputTokens = 0
	if _, err := store.Append([]tokenlogger.UsageEvent{event}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runInteractive(strings.NewReader("/by-client\n/refresh\n/refresh\n/quit\n"), &stdout, &stderr, store, dataDir); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, expected := range []string{"CLIENT", "PROVIDER", "MODEL", "REQUESTS", "SESSIONS", "1.1K", "no public catalog rate"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("interactive output does not contain %q: %s", expected, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "PRICING COVERAGE GAPS") || strings.Contains(stdout.String(), "Next:") || strings.Count(stdout.String(), "no public catalog rate") < 3 {
		t.Fatalf("interactive output included on-demand pricing details: %s", stdout.String())
	}
}

func TestInteractiveClearIsNonTTYNoOp(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var stdout, stderr bytes.Buffer
	if code := runInteractive(strings.NewReader("/clear\n/quit\n"), &stdout, &stderr, store, dataDir); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "clear is available only in an interactive terminal") {
		t.Fatalf("missing non-TTY clear message: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "\x1b[") || strings.Contains(stderr.String(), "\x1b[") {
		t.Fatalf("non-TTY clear emitted terminal control bytes: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestInteractiveHelpIncludesClear(t *testing.T) {
	store, err := tokenlogger.Open(filepath.Join(t.TempDir(), "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var output, stderr bytes.Buffer
	if code := runInteractive(strings.NewReader("/help\n/quit\n"), &output, &stderr, store, t.TempDir()); code != 0 {
		t.Fatalf("interactive exit code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(output.String(), "/clear") {
		t.Fatalf("interactive help does not advertise /clear: %q", output.String())
	}
}

func TestCtrlLClearsAndDoesNotReachCommandInput(t *testing.T) {
	var output bytes.Buffer
	filter := terminalInputRuneFilter(&output)
	if got, keep := filter(readline.CharCtrlL); !keep || got != readline.CharCtrlL {
		t.Fatalf("Ctrl-L filter = (%q, %t), want readline to handle Ctrl-L", got, keep)
	}
	if runtime.GOOS == "windows" {
		// Native Windows clearing uses the active console buffer APIs instead
		// of writing ANSI bytes to the supplied writer.
		return
	}
	if output.String() != "\033[2J\033[H" {
		t.Fatalf("Ctrl-L clear sequence = %q", output.String())
	}
}

func TestPasteGuardFoldsBracketedMultilinePaste(t *testing.T) {
	input := strings.NewReader("\033[200~/help\r\n/quit\033[201~\r")
	guard := &pasteGuardReader{source: input}
	output, err := io.ReadAll(guard)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "/help /quit\r" {
		t.Fatalf("guarded paste = %q", output)
	}
}

func TestPasteGuardPreservesCursorKeysAndUnicode(t *testing.T) {
	input := "abc\x1b[D\x1b[C\x1b[3~界\r"
	guard := &pasteGuardReader{source: strings.NewReader(input)}
	output, err := io.ReadAll(guard)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != input {
		t.Fatalf("guarded editor input = %q, want %q", output, input)
	}
}

func TestInteractiveCommandLimitUsesUTF8Bytes(t *testing.T) {
	line := []rune(strings.Repeat("界", maxInteractiveCommandBytes))
	bounded := truncateInteractiveCommand(line)
	if len(string(bounded)) > maxInteractiveCommandBytes {
		t.Fatalf("bounded command is %d bytes", len(string(bounded)))
	}
	if len(bounded) != maxInteractiveCommandRunes {
		t.Fatalf("bounded rune count = %d", len(bounded))
	}
	if ascii := truncateInteractiveCommand([]rune(strings.Repeat("a", maxInteractiveCommandBytes+1))); len(ascii) != maxInteractiveCommandRunes {
		t.Fatalf("bounded ASCII rune count = %d", len(ascii))
	}
}

type cancelledTerminalReader struct{}

func (cancelledTerminalReader) ReadLine(string) (string, error) { return "", readline.ErrInterrupt }
func (cancelledTerminalReader) DisableHistory()                 {}
func (cancelledTerminalReader) EnableHistory()                  {}
func (cancelledTerminalReader) IsTerminal() bool                { return true }

func TestTerminalCancellationDoesNotCompleteOnboarding(t *testing.T) {
	settings := defaultSettings(t.TempDir())
	updated, startNow, err := runOnboarding(cancelledTerminalReader{}, settings.DataDir, settings)
	if !errors.Is(err, readline.ErrInterrupt) {
		t.Fatalf("onboarding error = %v", err)
	}
	if startNow || updated.OnboardingComplete || updated.CollectorEnabled != settings.CollectorEnabled {
		t.Fatalf("cancellation changed onboarding settings: before=%+v after=%+v", settings, updated)
	}
}

type historyTrackingReader struct {
	answers       []string
	history       bool
	readWithState []bool
}

func (reader *historyTrackingReader) ReadLine(string) (string, error) {
	reader.readWithState = append(reader.readWithState, reader.history)
	answer := reader.answers[0]
	reader.answers = reader.answers[1:]
	return answer, nil
}
func (reader *historyTrackingReader) DisableHistory()  { reader.history = false }
func (reader *historyTrackingReader) EnableHistory()   { reader.history = true }
func (reader *historyTrackingReader) IsTerminal() bool { return true }

func TestOnboardingAnswersAreReadWithHistoryDisabled(t *testing.T) {
	reader := &historyTrackingReader{answers: []string{"n", "y"}, history: true}
	settings := defaultSettings(t.TempDir())
	if _, _, err := runOnboardingWithoutHistory(reader, settings.DataDir, settings); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader.readWithState, []bool{false, false}) || !reader.history {
		t.Fatalf("history state during onboarding=%v after=%t; want disabled for both answers and restored", reader.readWithState, reader.history)
	}
}

func TestDiagnosticConsentIsReadWithHistoryDisabled(t *testing.T) {
	store, err := tokenlogger.Open(filepath.Join(t.TempDir(), "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	diagnostic, err := store.RecordDiagnostic("test diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	reader := &historyTrackingReader{answers: []string{"view", "n"}, history: true}
	if err := offerDiagnostic(reader, store, diagnostic); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader.readWithState, []bool{false, false}) || !reader.history {
		t.Fatalf("history state during diagnostic prompts=%v after=%t; want disabled for both answers then restored", reader.readWithState, reader.history)
	}
}

func TestHarnessProtocolFixture(t *testing.T) {
	dataDir := t.TempDir()
	event := tokenlogger.UsageEvent{
		EventID: "event-1", SchemaVersion: 1, EventType: "model.request",
		OccurredAt: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
		Client:     tokenlogger.Client{Name: "fixture-harness"}, Provider: "openai", RequestedModel: "gpt-test",
		Usage:        tokenlogger.TokenUsage{InputTokens: 10, OutputTokens: 5},
		Provenance:   tokenlogger.Provenance{Adapter: "fixture", SourceID: "fixture-source", SourceRecord: "record-1"},
		IngestionKey: "fixture-source:record-1",
	}
	requests := []map[string]any{
		{"protocol": protocolName, "version": protocolVersion, "id": "1", "method": "initialize", "params": map[string]any{"data_dir": dataDir}},
		{"protocol": protocolName, "version": protocolVersion, "id": "2", "method": "record", "params": map[string]any{"event": event}},
		{"protocol": protocolName, "version": protocolVersion, "id": "3", "method": "flush"},
		{"protocol": protocolName, "version": protocolVersion, "id": "4", "method": "health"},
		{"protocol": protocolName, "version": protocolVersion, "id": "5", "method": "shutdown"},
	}
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInput([]string{"harness"}, &input, &stdout, &stderr); code != 0 {
		t.Fatalf("harness exit code = %d, stderr = %q", code, stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	var responses []protocolResponse
	for {
		var response protocolResponse
		if err := decoder.Decode(&response); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 5 {
		t.Fatalf("response count = %d, want 5", len(responses))
	}
	for _, response := range responses {
		if !response.OK {
			t.Fatalf("protocol response %q failed: %#v", response.ID, response.Error)
		}
		if response.Protocol != protocolName || response.Version != protocolVersion {
			t.Fatalf("response %q has unexpected protocol metadata: %#v", response.ID, response)
		}
	}
	manifest, ok := responses[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("initialize result = %#v, want plugin manifest", responses[0].Result)
	}
	versions, ok := manifest["schema_versions"].([]any)
	if !ok || len(versions) != 4 {
		t.Fatalf("schema versions = %#v, want four supported versions", manifest["schema_versions"])
	}
	if manifest["plugin_version"] != tokenlogger.CoreVersion() || manifest["cli_version"] != cliVersion {
		t.Fatalf("initialize version tuple = core %v, cli %v; want core %s, cli %s", manifest["plugin_version"], manifest["cli_version"], tokenlogger.CoreVersion(), cliVersion)
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("durable event count = %d, err = %v; want 1", count, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected protocol diagnostics: %q", stderr.String())
	}
}

func TestHarnessRecordReferenceMetadata(t *testing.T) {
	dataDir := t.TempDir()
	occurredAt := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	baseEvent := tokenlogger.UsageEvent{
		SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		Client: tokenlogger.Client{Name: "reference-harness"}, Provider: "openai", RequestedModel: "gpt-test",
		Usage:      tokenlogger.TokenUsage{InputTokens: 10, OutputTokens: 5},
		Provenance: tokenlogger.Provenance{Adapter: "fixture", SourceID: "reference-source"},
	}
	project := tokenlogger.Project{ID: "project-1", Root: "/work/project", DisplayName: "Project One"}
	session := tokenlogger.Session{ID: "session-1", ProjectID: project.ID, Client: baseEvent.Client, StartedAt: &occurredAt}
	projectEvent := baseEvent
	projectEvent.EventID, projectEvent.IngestionKey = "project-event", "reference-source:project"
	projectEvent.Provenance.SourceRecord, projectEvent.ProjectID = "project-record", project.ID
	sessionEvent := baseEvent
	sessionEvent.EventID, sessionEvent.IngestionKey = "session-event", "reference-source:session"
	sessionEvent.Provenance.SourceRecord, sessionEvent.SessionID = "session-record", session.ID
	bothEvent := baseEvent
	bothEvent.EventID, bothEvent.IngestionKey = "both-event", "reference-source:both"
	bothEvent.Provenance.SourceRecord, bothEvent.ProjectID, bothEvent.SessionID = "both-record", project.ID, session.ID

	requests := []map[string]any{
		{"protocol": protocolName, "version": protocolVersion, "id": "1", "method": "initialize", "params": map[string]any{"data_dir": dataDir}},
		{"protocol": protocolName, "version": protocolVersion, "id": "2", "method": "record", "params": map[string]any{"event": projectEvent, "project": project}},
		{"protocol": protocolName, "version": protocolVersion, "id": "3", "method": "record", "params": map[string]any{"event": sessionEvent, "project": project, "session": session}},
		{"protocol": protocolName, "version": protocolVersion, "id": "4", "method": "record", "params": map[string]any{"event": bothEvent, "project": project, "session": session}},
		{"protocol": protocolName, "version": protocolVersion, "id": "5", "method": "record", "params": map[string]any{"event": bothEvent, "project": project, "session": session}},
		{"protocol": protocolName, "version": protocolVersion, "id": "6", "method": "record", "params": map[string]any{"event": func() tokenlogger.UsageEvent {
			event := baseEvent
			event.EventID, event.IngestionKey, event.Provenance.SourceRecord, event.ProjectID = "missing-project", "reference-source:missing-project", "missing-project-record", "missing-project"
			return event
		}()}},
		{"protocol": protocolName, "version": protocolVersion, "id": "7", "method": "record", "params": map[string]any{"event": func() tokenlogger.UsageEvent {
			event := baseEvent
			event.EventID, event.IngestionKey, event.Provenance.SourceRecord, event.SessionID = "missing-session", "reference-source:missing-session", "missing-session-record", "missing-session"
			return event
		}()}},
		{"protocol": protocolName, "version": protocolVersion, "id": "8", "method": "shutdown"},
	}
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInput([]string{"harness"}, &input, &stdout, &stderr); code != 0 {
		t.Fatalf("harness exit code = %d, stderr = %q", code, stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	var responses []protocolResponse
	for {
		var response protocolResponse
		if err := decoder.Decode(&response); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != len(requests) {
		t.Fatalf("response count = %d, want %d", len(responses), len(requests))
	}
	for _, index := range []int{0, 1, 2, 3, 4, 7} {
		if !responses[index].OK {
			t.Fatalf("response %q failed: %#v", responses[index].ID, responses[index].Error)
		}
	}
	if responses[4].Result.(map[string]any)["duplicates"] != float64(1) {
		t.Fatalf("repeated record result = %#v, want one duplicate", responses[4].Result)
	}
	for index, code := range map[int]string{5: "missing_project_metadata", 6: "missing_session_metadata"} {
		if responses[index].OK || responses[index].Error == nil || responses[index].Error.Code != code {
			t.Fatalf("response %q = %#v, want error %q", responses[index].ID, responses[index], code)
		}
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if count, err := store.EventCount(); err != nil || count != 3 {
		t.Fatalf("durable event count = %d, err = %v; want 3", count, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected protocol diagnostics: %q", stderr.String())
	}
}

func TestHarnessPersistsOptionalPricingMetadataAndKeepsV1Errors(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	serviceTier := &tokenlogger.BillingContextValue{Value: "standard", Evidence: tokenlogger.BillingEvidenceProviderResponse}
	region := &tokenlogger.BillingContextValue{Value: "global", Evidence: tokenlogger.BillingEvidenceProviderResponse}
	speed := &tokenlogger.BillingContextValue{Value: "standard", Evidence: tokenlogger.BillingEvidenceProviderResponse}
	valid := tokenlogger.UsageEvent{EventID: "anthropic-priced", SchemaVersion: 1, EventType: "model.request", OccurredAt: at,
		Client: tokenlogger.Client{Name: "fixture"}, Provider: "anthropic", RequestedModel: "claude-opus-5-5", ResolvedModel: "claude-opus-5-5",
		Usage:          tokenlogger.TokenUsage{InputTokens: 1_300_000, OutputTokens: 300_000, CacheReadTokens: 200_000, CacheWriteTokens: 100_000, CacheWrite5mTokens: 60_000, CacheWrite1hTokens: 40_000},
		BillingContext: tokenlogger.BillingContext{ServiceTier: serviceTier, Region: region, Speed: speed},
		Provenance:     tokenlogger.Provenance{Adapter: "fixture", SourceID: "pricing-source", SourceRecord: "anthropic-priced"}, IngestionKey: "pricing-source:anthropic-priced"}
	invalidEvidence := valid
	invalidEvidence.EventID, invalidEvidence.IngestionKey, invalidEvidence.Provenance.SourceRecord = "invalid-evidence", "pricing-source:invalid-evidence", "invalid-evidence"
	invalidEvidence.BillingContext.Speed = &tokenlogger.BillingContextValue{Value: "standard", Evidence: "inferred"}
	invalidSplit := valid
	invalidSplit.EventID, invalidSplit.IngestionKey, invalidSplit.Provenance.SourceRecord = "invalid-split", "pricing-source:invalid-split", "invalid-split"
	invalidSplit.Usage.CacheWrite5mTokens = 80_000
	invalidJSON := `{"event":{"event_id":"decode-error","schema_version":1,"event_type":"model.request","occurred_at":"2026-10-16T12:00:00Z","client":{"name":"fixture"},"provider":"openai","requested_model":"gpt-5.3-codex","usage":{"input_tokens":"not-an-integer"},"provenance":{"adapter":"fixture","source_id":"s","source_record":"r"},"ingestion_key":"s:r"}}`
	requests := []map[string]any{
		{"protocol": protocolName, "version": protocolVersion, "id": "1", "method": "initialize", "params": map[string]any{"data_dir": dataDir}},
		{"protocol": protocolName, "version": protocolVersion, "id": "2", "method": "record", "params": map[string]any{"event": valid}},
		{"protocol": protocolName, "version": protocolVersion, "id": "3", "method": "record", "params": map[string]any{"event": valid}},
		{"protocol": protocolName, "version": protocolVersion, "id": "4", "method": "record", "params": map[string]any{"event": invalidEvidence}},
		{"protocol": protocolName, "version": protocolVersion, "id": "5", "method": "record", "params": map[string]any{"event": invalidSplit}},
		{"protocol": protocolName, "version": protocolVersion, "id": "6", "method": "record", "params": json.RawMessage(invalidJSON)},
		{"protocol": protocolName, "version": protocolVersion, "id": "7", "method": "shutdown"},
	}
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInput([]string{"harness"}, &input, &stdout, &stderr); code != 0 {
		t.Fatalf("harness exit = %d, stderr=%q", code, stderr.String())
	}
	var responses []protocolResponse
	decoder := json.NewDecoder(&stdout)
	for {
		var response protocolResponse
		if err := decoder.Decode(&response); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != len(requests) || !responses[1].OK || !responses[2].OK {
		t.Fatalf("unexpected harness responses: %+v", responses)
	}
	if responses[2].Result.(map[string]any)["duplicates"] != float64(1) {
		t.Fatalf("duplicate response = %#v", responses[2].Result)
	}
	for index, code := range map[int]string{3: "record_failed", 4: "record_failed", 5: "invalid_params"} {
		if responses[index].OK || responses[index].Error == nil || responses[index].Error.Code != code {
			t.Fatalf("response %d = %+v; want %s", index, responses[index], code)
		}
	}
	for scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes())); scanner.Scan(); {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil || len(envelope["protocol"]) == 0 {
			t.Fatalf("stdout contains non-protocol output %q, err=%v", scanner.Text(), err)
		}
	}
	if !strings.Contains(stderr.String(), "protocol diagnostic: validate event") || strings.Contains(stderr.String(), "inferred") == false {
		t.Fatalf("validation diagnostics were not sent to stderr: %q", stderr.String())
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("persisted event count = %d, err=%v", count, err)
	}
	report, err := store.Query(tokenlogger.UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil || report.Summary.CalculatedCostMicros != 10_660_000 || report.Summary.UnknownCostRequests != 0 || report.Summary.PricingVersion != "anthropic-api-2026-10-16-v1" {
		t.Fatalf("persisted optional metadata report = %+v, err=%v", report.Summary, err)
	}
}

func TestClaudePricingAcceptanceAcrossCLIReportsAndREPL(t *testing.T) {
	dataDir := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "claude-priced.jsonl")
	priced := claudePricingAcceptanceRecord("priced-claude", "2026-10-16T12:00:00Z", true)
	if err := os.WriteFile(sourcePath, []byte(priced+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	importSource := func() string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run([]string{"import", sourcePath, "--format", "json", "--data-dir", dataDir}, &stdout, &stderr); code != 0 {
			t.Fatalf("Claude import exit=%d stderr=%q output=%q", code, stderr.String(), stdout.String())
		}
		return stdout.String()
	}
	if result := importSource(); !strings.Contains(result, `"imported_events":1`) {
		t.Fatalf("first Claude import = %s", result)
	}
	queryJSON := func() tokenlogger.UsageReport {
		t.Helper()
		var stdout, stderr bytes.Buffer
		args := []string{"cost", "--data-dir", dataDir, "--format", "json", "--from", "2026-10-16", "--to", "2026-10-17"}
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("cost JSON exit=%d stderr=%q output=%q", code, stderr.String(), stdout.String())
		}
		var report tokenlogger.UsageReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode cost JSON %q: %v", stdout.String(), err)
		}
		return report
	}
	first := queryJSON()
	if first.Summary.InputTokens != 1_300_000 || first.Summary.OutputTokens != 300_000 || first.Summary.CalculatedCostMicros != 10_660_000 || first.Summary.UnknownCostRequests != 0 || first.Summary.PricingVersion != "anthropic-api-2026-10-16-v1" {
		t.Fatalf("priced Claude CLI report = %+v", first.Summary)
	}
	var csvOut, csvErr bytes.Buffer
	if code := run([]string{"cost", "--data-dir", dataDir, "--format", "csv", "--from", "2026-10-16", "--to", "2026-10-17"}, &csvOut, &csvErr); code != 0 {
		t.Fatalf("cost CSV exit=%d stderr=%q", code, csvErr.String())
	}
	csvRows, err := csv.NewReader(strings.NewReader(csvOut.String())).ReadAll()
	if err != nil || len(csvRows) < 2 || !strings.Contains(csvOut.String(), "1300000") || !strings.Contains(csvOut.String(), "10660000") || !strings.Contains(csvOut.String(), "anthropic-api-2026-10-16-v1") {
		t.Fatalf("cost CSV lacks exact usage, estimate, or provenance: rows=%d err=%v output=%s", len(csvRows), err, csvOut.String())
	}
	var humanOut, humanErr bytes.Buffer
	if code := run([]string{"cost", "--data-dir", dataDir, "--format", "table", "--from", "2026-10-16", "--to", "2026-10-17"}, &humanOut, &humanErr); code != 0 {
		t.Fatalf("human cost exit=%d stderr=%q", code, humanErr.String())
	}
	if !strings.Contains(humanOut.String(), "claude-opus-5-5") || !strings.Contains(humanOut.String(), "10.66") {
		t.Fatalf("human cost report missing model or estimate: %s", humanOut.String())
	}
	if repeated := importSource(); !strings.Contains(repeated, `"unchanged_snapshot":1`) {
		t.Fatalf("repeated Claude import = %s", repeated)
	}
	if repeated := queryJSON(); repeated.Summary.CalculatedCostMicros != 10_660_000 || repeated.Summary.RequestCount != 1 {
		t.Fatalf("report changed after unchanged import: %+v", repeated.Summary)
	}

	// Add a second event with matching tier/geography/speed but aggregate-only
	// cache writes. Its verified subtotal is retained while its write charge is
	// unknown, so the combined report exercises mixed priced/unpriced coverage.
	partial := claudePricingAcceptanceRecord("partial-claude", "2026-10-16T13:00:00Z", false)
	if err := os.WriteFile(sourcePath, []byte(priced+"\n"+partial+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := importSource(); !strings.Contains(result, `"imported_events":1`) || !strings.Contains(result, `"duplicate_events":1`) {
		t.Fatalf("append Claude import = %s", result)
	}
	mixed := queryJSON()
	if mixed.Summary.RequestCount != 2 || mixed.Summary.UnknownCostRequests != 1 || mixed.Summary.CalculatedCostMicros != 20_700_000 || mixed.Summary.InputTokens != 2_600_000 {
		t.Fatalf("mixed Claude report = %+v; want one unknown and verified subtotal", mixed.Summary)
	}
	if string(mixed.Summary.CostUnknownReasons) != `{"billing_context_missing":1}` {
		t.Fatalf("mixed report omitted cache-write unknown reason: %s", mixed.Summary.CostUnknownReasons)
	}
	var mixedCSV, mixedCSVErr bytes.Buffer
	if code := run([]string{"cost", "--data-dir", dataDir, "--format", "csv", "--from", "2026-10-16", "--to", "2026-10-17"}, &mixedCSV, &mixedCSVErr); code != 0 {
		t.Fatalf("mixed CSV exit=%d stderr=%q", code, mixedCSVErr.String())
	}
	mixedCSVRows, err := csv.NewReader(strings.NewReader(mixedCSV.String())).ReadAll()
	if err != nil || len(mixedCSVRows) < 2 {
		t.Fatalf("decode mixed CSV rows=%d err=%v output=%s", len(mixedCSVRows), err, mixedCSV.String())
	}
	columns := make(map[string]int, len(mixedCSVRows[0]))
	for index, name := range mixedCSVRows[0] {
		columns[name] = index
	}
	firstRow := mixedCSVRows[1]
	if firstRow[columns["input_tokens"]] != "2600000" || firstRow[columns["calculated_cost_micros"]] != "20700000" || firstRow[columns["unknown_cost_requests"]] != "1" || firstRow[columns["unknown_cost_reasons"]] != `{"billing_context_missing":1}` {
		t.Fatalf("mixed CSV row lacks exact tokens/cost/unknown counts: %#v", firstRow)
	}
	var mixedHuman bytes.Buffer
	if code := run([]string{"cost", "--data-dir", dataDir, "--format", "table", "--from", "2026-10-16", "--to", "2026-10-17"}, &mixedHuman, &humanErr); code != 0 {
		t.Fatalf("mixed human cost exit=%d stderr=%q", code, humanErr.String())
	}
	if !strings.Contains(mixedHuman.String(), "billing context or rate unavailable") || !strings.Contains(mixedHuman.String(), "cache-write") || !strings.Contains(mixedHuman.String(), "anthropic") {
		t.Fatalf("mixed human report lacks actionable coverage text: %s", mixedHuman.String())
	}

	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	activeRange := tokenlogger.ResourceFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	projectList, err := store.ListProjects(activeRange)
	if err != nil {
		t.Fatal(err)
	}
	var pricingProject tokenlogger.ProjectResource
	for _, project := range projectList.Projects {
		if project.DisplayName == "Pricing Project" {
			pricingProject = project
			break
		}
	}
	if pricingProject.ID == "" {
		t.Fatalf("could not find imported pricing project: %+v", projectList.Projects)
	}
	sessionList, err := store.ListSessions(pricingProject.ID, activeRange)
	if err != nil {
		t.Fatal(err)
	}
	var pricingSession tokenlogger.SessionResource
	for _, session := range sessionList.Sessions {
		for _, model := range session.Models {
			if model == "claude-opus-5-5" {
				pricingSession = session
				break
			}
		}
	}
	if pricingSession.ID == "" {
		t.Fatalf("could not find Claude pricing session: %+v", sessionList.Sessions)
	}
	otherProjectID, otherProjectSessionID, otherSessionID := "scope-other-project", "scope-other-project-session", "scope-other-session"
	makeScopeEvent := func(id string, at time.Time, projectID, sessionID, model string) tokenlogger.UsageEvent {
		event := cliReportEvent(id, at)
		event.Provider, event.RequestedModel, event.ResolvedModel = "anthropic", model, model
		event.ProjectID, event.SessionID = projectID, sessionID
		event.Usage = tokenlogger.TokenUsage{InputTokens: 1, CacheWriteTokens: 1}
		return event
	}
	decoys := []tokenlogger.UsageEvent{
		makeScopeEvent("outside-project", time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC), otherProjectID, otherProjectSessionID, "claude-opus-5-5"),
		makeScopeEvent("outside-session", time.Date(2026, 10, 16, 14, 1, 0, 0, time.UTC), pricingProject.ID, otherSessionID, "claude-opus-5-5"),
		makeScopeEvent("outside-model", time.Date(2026, 10, 16, 14, 2, 0, 0, time.UTC), pricingProject.ID, pricingSession.ID, "claude-unlisted-scope-test"),
		makeScopeEvent("outside-date", time.Date(2026, 10, 8, 14, 3, 0, 0, time.UTC), pricingProject.ID, pricingSession.ID, "claude-opus-5-5"),
	}
	if _, err := store.AppendWithReferences(decoys,
		[]tokenlogger.Project{{ID: otherProjectID, DisplayName: "Other Project"}},
		[]tokenlogger.Session{
			{ID: otherProjectSessionID, ProjectID: otherProjectID, Client: decoys[0].Client},
			{ID: otherSessionID, ProjectID: pricingProject.ID, Client: decoys[1].Client},
		}); err != nil {
		t.Fatalf("append pricing scope decoys: %v", err)
	}
	commands := "/range 2026-10-16 2026-10-17\n/projects\n/project Pricing Project\n/pricing\n/sessions\n/session " + pricingSession.ID + "\n/pricing\n/models\n/model claude-opus-5-5\n/pricing\n/range 2026-10-07 2026-10-09\n/pricing\n/range 2026-10-16 2026-10-17\n/refresh\n/quit\n"
	reader := &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(commands)), output: io.Discard}
	var replOut, replErr bytes.Buffer
	if code := runInteractiveLoopWithClock(reader, &replOut, &replErr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }); code != 0 {
		t.Fatalf("pricing REPL exit=%d stderr=%q output=%q", code, replErr.String(), replOut.String())
	}
	output := replOut.String()
	pricingIndex := strings.Index(output, "pricing coverage for active scope")
	if pricingIndex < 0 {
		t.Fatalf("/pricing did not render coverage for the active scope: %s", output)
	}
	pricingBlocks := make([]string, 0, 4)
	for offset := pricingIndex; offset < len(output); {
		nextHeader := strings.Index(output[offset:], "pricing coverage for active scope")
		if nextHeader < 0 {
			break
		}
		start := offset + nextHeader
		nextReport := strings.Index(output[start:], "report queried_at:")
		if nextReport < 0 {
			t.Fatalf("pricing output has no following view boundary: %s", output[start:])
		}
		pricingBlocks = append(pricingBlocks, output[start:start+nextReport])
		offset = start + nextReport
	}
	if len(pricingBlocks) != 4 {
		t.Fatalf("got %d /pricing output blocks, want project/session/model/date scopes: %s", len(pricingBlocks), output[pricingIndex:])
	}
	for _, expected := range []string{"anthropic / claude-opus-5-5: 2 requests", "billing context or rate unavailable (2)", "Affected category: cache-write (2)", "claude-unlisted-scope-test"} {
		if !strings.Contains(pricingBlocks[0], expected) {
			t.Errorf("project-scope /pricing missing %q: %s", expected, pricingBlocks[0])
		}
	}
	for _, excluded := range []string{"outside-date"} {
		if strings.Contains(pricingBlocks[0], excluded) {
			t.Errorf("project-scope /pricing included %q: %s", excluded, pricingBlocks[0])
		}
	}
	for _, expected := range []string{"anthropic / claude-opus-5-5: 1 requests", "billing context or rate unavailable (1)", "Affected category: cache-write (1)", "claude-unlisted-scope-test"} {
		if !strings.Contains(pricingBlocks[1], expected) {
			t.Errorf("session-scope /pricing missing %q: %s", expected, pricingBlocks[1])
		}
	}
	if strings.Contains(pricingBlocks[1], "outside-date") {
		t.Errorf("session-scope /pricing included out-of-range event: %s", pricingBlocks[1])
	}
	for _, expected := range []string{"anthropic / claude-opus-5-5: 1 requests", "billing context or rate unavailable (1)", "Affected category: cache-write (1)"} {
		if !strings.Contains(pricingBlocks[2], expected) {
			t.Errorf("model-scope /pricing missing %q: %s", expected, pricingBlocks[2])
		}
	}
	if strings.Contains(pricingBlocks[2], "claude-unlisted-scope-test") {
		t.Errorf("model-scope /pricing included other model: %s", pricingBlocks[2])
	}
	for _, expected := range []string{"anthropic / claude-opus-5-5: 1 requests", "billing context or rate unavailable (1)", "Affected category: cache-write (1)"} {
		if !strings.Contains(pricingBlocks[3], expected) {
			t.Errorf("date-scope /pricing missing %q: %s", expected, pricingBlocks[3])
		}
	}
	if strings.Contains(pricingBlocks[3], "2 requests") {
		t.Errorf("date-scope /pricing did not isolate the out-of-range event: %s", pricingBlocks[3])
	}
	refreshBreadcrumbOffset := strings.LastIndex(output, "komposyn / project:")
	postPricingOutput := output[refreshBreadcrumbOffset:]
	if !strings.Contains(output[:pricingIndex], "partial; 1 billing context or rate unavailable") {
		t.Fatalf("default model view missing concise partial status: %s", output[:pricingIndex])
	}
	if !strings.Contains(postPricingOutput, "model:claude-opus-5-5") || !strings.Contains(postPricingOutput, "20.7 USD") {
		t.Fatalf("/pricing changed the navigation selection used by the next refresh: %s", postPricingOutput)
	}
	if strings.Contains(output[:pricingIndex], "PRICING COVERAGE GAPS") || strings.Contains(output[:pricingIndex], "Next:") {
		t.Fatalf("default REPL views included opt-in pricing explanations: %s", output[:pricingIndex])
	}
	pending, err := store.PendingDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 || strings.Contains(output, "Pending diagnostic") {
		t.Fatalf("pricing unknowns created or prompted for diagnostics: pending=%v output=%s", pending, output)
	}
}

func claudePricingAcceptanceRecord(id, timestamp string, includeSplit bool) string {
	cacheCreation := `,"cache_creation":{"ephemeral_5m_input_tokens":60000,"ephemeral_1h_input_tokens":40000}`
	if !includeSplit {
		cacheCreation = ""
	}
	return `{"type":"assistant","uuid":"` + id + `","sessionId":"pricing-session","project_id":"pricing-project","project_name":"Pricing Project","timestamp":"` + timestamp + `","cwd":"/sanitized/pricing-project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":1000000,"cache_creation_input_tokens":100000,"cache_read_input_tokens":200000,"output_tokens":300000` + cacheCreation + `,"service_tier":"standard","inference_geo":"global","speed":"standard"}}}`
}

func TestOpenAIPricingAcceptanceThroughHarnessAndCLI(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	makeEvent := func(id, tier string, hour int) tokenlogger.UsageEvent {
		value := &tokenlogger.BillingContextValue{Value: tier, Evidence: tokenlogger.BillingEvidenceHarness}
		return tokenlogger.UsageEvent{EventID: id, SchemaVersion: 1, EventType: "model.request", OccurredAt: at.Add(time.Duration(hour-12) * time.Hour),
			Client: tokenlogger.Client{Name: "fixture"}, Provider: "openai", RequestedModel: "gpt-5.3-codex", ResolvedModel: "gpt-5.3-codex",
			Usage:          tokenlogger.TokenUsage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, ReasoningTokens: 100_000},
			BillingContext: tokenlogger.BillingContext{ServiceTier: value},
			Provenance:     tokenlogger.Provenance{Adapter: "fixture", SourceID: "openai-harness", SourceRecord: id}, IngestionKey: "openai-harness:" + id}
	}
	requests := []map[string]any{
		{"protocol": protocolName, "version": protocolVersion, "id": "1", "method": "initialize", "params": map[string]any{"data_dir": dataDir}},
		{"protocol": protocolName, "version": protocolVersion, "id": "2", "method": "record", "params": map[string]any{"event": makeEvent("default-tier", "default", 12)}},
		{"protocol": protocolName, "version": protocolVersion, "id": "3", "method": "record", "params": map[string]any{"event": makeEvent("priority-tier", "priority", 13)}},
		{"protocol": protocolName, "version": protocolVersion, "id": "4", "method": "shutdown"},
	}
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var protocolOut, protocolErr bytes.Buffer
	if code := runWithInput([]string{"harness"}, &input, &protocolOut, &protocolErr); code != 0 {
		t.Fatalf("OpenAI harness exit=%d stderr=%q", code, protocolErr.String())
	}
	if protocolErr.Len() != 0 {
		t.Fatalf("OpenAI harness stderr=%q", protocolErr.String())
	}
	for _, test := range []struct {
		from, to string
		want     int64
	}{{"2026-10-16T12:00:00Z", "2026-10-16T12:30:00Z", 15_925_000}, {"2026-10-16T13:00:00Z", "2026-10-16T13:30:00Z", 31_850_000}} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"cost", "--data-dir", dataDir, "--format", "json", "--from", test.from, "--to", test.to}, &stdout, &stderr); code != 0 {
			t.Fatalf("OpenAI cost CLI exit=%d stderr=%q output=%q", code, stderr.String(), stdout.String())
		}
		var report tokenlogger.UsageReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Summary.CalculatedCostMicros != test.want || report.Summary.UnknownCostRequests != 0 || report.Summary.OutputTokens != 1_000_000 {
			t.Fatalf("OpenAI %s report = %+v; want %d, reasoning included once", test.from, report.Summary, test.want)
		}
	}
}

func TestInteractiveMode(t *testing.T) {
	t.Setenv("TOKEN_LOGGER_DATA_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runWithInput(nil, strings.NewReader("n\nn\n/help\n/range 2026-10-11 2026-10-12\n/quit\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, expected := range []string{"interactive", "/projects", "range: 2026-10-11T00:00:00Z"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("interactive output missing %q: %q", expected, stdout.String())
		}
	}
}

func TestInteractiveRollingRangeRefreshSeesCommittedEventFromSecondStore(t *testing.T) {
	dataDir := t.TempDir()
	databasePath := filepath.Join(dataDir, "token-logger.db")
	absolutePath, err := filepath.Abs(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	readStore, err := tokenlogger.Open(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	defer readStore.Close()
	writeStore, err := tokenlogger.Open(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	defer writeStore.Close()

	t1 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	current := t1
	reader := &beforeReadLineReader{
		scannerLineReader: &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader("/refresh\n/quit\n")), output: io.Discard},
		before: func(command string) {
			if command != "/refresh" {
				return
			}
			event := cliReportEvent("watched-after-start", t1.Add(time.Minute))
			event.ResolvedModel = "newly-watched-model"
			if _, err := writeStore.Append([]tokenlogger.UsageEvent{event}); err != nil {
				t.Errorf("append event through writer store: %v", err)
			}
			current = t1.Add(2 * time.Minute)
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runInteractiveLoopWithClock(reader, &stdout, &stderr, readStore, dataDir, defaultSettings(dataDir), false, func() time.Time { return current }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "range: 2026-10-07T10:00:00Z to 2026-10-14T10:00:00Z (UTC)") {
		t.Fatalf("initial render did not use T1 rolling bounds: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "range: 2026-10-07T10:02:00Z to 2026-10-14T10:02:00Z (UTC)") {
		t.Fatalf("refresh did not use T2 rolling bounds: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "newly-watched-model") {
		t.Fatalf("refresh did not see committed event from second Store: %q", stdout.String())
	}
}

func TestInteractiveRollingRangeDisplaysFractionalUpperBoundaryExactly(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	to := time.Date(2026, 10, 14, 10, 0, 0, 500_000_000, time.UTC)
	inside := cliReportEvent("fractional-before-upper", to.Add(-time.Nanosecond))
	inside.ResolvedModel = "inside-upper"
	atUpper := cliReportEvent("fractional-at-upper", to)
	atUpper.ResolvedModel = "at-upper"
	if _, err := store.Append([]tokenlogger.UsageEvent{inside, atUpper}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runInteractiveLoopWithClock(&scannerLineReader{scanner: bufio.NewScanner(strings.NewReader("/quit\n")), output: io.Discard}, &stdout, &stderr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return to }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	wantRange := "range: 2026-10-07T10:00:00.5Z to 2026-10-14T10:00:00.5Z (UTC)"
	if !strings.Contains(stdout.String(), wantRange) {
		t.Fatalf("rendered range lost fractional precision: want %q in %q", wantRange, stdout.String())
	}
	if !strings.Contains(stdout.String(), "inside-upper") || strings.Contains(stdout.String(), "at-upper") {
		t.Fatalf("report did not honor the exact displayed half-open range: %q", stdout.String())
	}
}

func TestInteractiveReportCommandsResolveFreshRollingRanges(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	commands := []string{"/projects", "/sessions", "/total-usage", "/by-client", "/by-model", "/cost", "/refresh", "/quit"}
	var stdout, stderr bytes.Buffer
	base := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	current := base
	var lines strings.Builder
	for _, command := range commands {
		lines.WriteString(command)
		lines.WriteByte('\n')
	}
	reader := &beforeReadLineReader{
		scannerLineReader: &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(lines.String())), output: io.Discard},
		before: func(command string) {
			for index, candidate := range commands[:len(commands)-1] {
				if command == candidate {
					current = base.Add(time.Duration(index+1) * time.Minute)
					return
				}
			}
		},
	}
	if code := runInteractiveLoopWithClock(reader, &stdout, &stderr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return current }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	for minute := 0; minute <= len(commands)-1; minute++ {
		// /sessions is now scoped to an explicitly selected project and does not
		// render a report when invoked at home.
		if minute == 2 {
			continue
		}
		to := base.Add(time.Duration(minute) * time.Minute)
		from := to.AddDate(0, 0, -7)
		want := fmt.Sprintf("range: %s to %s (UTC)", from.Format(time.RFC3339), to.Format(time.RFC3339))
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("rolling report range %q missing from output: %s", want, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "select a project first with /project") {
		t.Fatalf("project-scoped /sessions did not explain the missing selection: %q", stdout.String())
	}
}

func TestInteractivePinnedRangeAndResetUseHalfOpenBounds(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	resetWriter, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer resetWriter.Close()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	fromEvent := cliReportEvent("pinned-from-boundary", from)
	fromEvent.ResolvedModel = "included-at-from"
	toEvent := cliReportEvent("pinned-to-boundary", to)
	toEvent.ResolvedModel = "excluded-at-to"
	if _, err := store.Append([]tokenlogger.UsageEvent{fromEvent, toEvent}); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	current := base
	commands := "/range 2026-10-01 2026-10-02\n/projects\n/range\n/range reset\n/by-model\n/range\n/quit\n"
	reader := &beforeReadLineReader{
		scannerLineReader: &scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(commands)), output: io.Discard},
		before: func(command string) {
			switch command {
			case "/projects":
				current = base.Add(time.Hour)
			case "/range reset":
				current = base.Add(2 * time.Hour)
				event := cliReportEvent("committed-before-reset", current.Add(-time.Minute))
				event.ResolvedModel = "new-event-visible-after-reset"
				if _, err := resetWriter.Append([]tokenlogger.UsageEvent{event}); err != nil {
					t.Errorf("append reset-visible event: %v", err)
				}
			case "/range":
				current = current.Add(time.Hour)
			}
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runInteractiveLoopWithClock(reader, &stdout, &stderr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return current }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	pinnedRange := "range: 2026-10-01T00:00:00Z to 2026-10-02T00:00:00Z (UTC)"
	if strings.Count(out, pinnedRange) != 2 {
		t.Fatalf("pinned range should remain unchanged across report views: %q", out)
	}
	if !strings.Contains(out, "active range (pinned): 2026-10-01T00:00:00Z to 2026-10-02T00:00:00Z (UTC)") {
		t.Fatalf("/range did not report pinned mode and bounds: %q", out)
	}
	if !strings.Contains(out, "included-at-from") || strings.Contains(out, "excluded-at-to") {
		t.Fatalf("pinned query violated [from,to) boundary: %q", out)
	}
	if !strings.Contains(out, "new-event-visible-after-reset") {
		t.Fatalf("reset rolling render did not include newly committed event: %q", out)
	}
	resetRange := "range: 2026-10-07T12:00:00Z to 2026-10-14T12:00:00Z (UTC)"
	if !strings.Contains(out, resetRange) || !strings.Contains(out, "active range (rolling): 2026-10-07T13:00:00Z to 2026-10-14T13:00:00Z (UTC)") {
		t.Fatalf("/range reset did not immediately restore rolling mode: %q", out)
	}
}

func TestInteractiveRangeRejectsUnsupportedArguments(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	commands := "/range nonsense\n/range reset extra\n/range\n/quit\n"
	var stdout, stderr bytes.Buffer
	current := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	if code := runInteractiveLoopWithClock(&scannerLineReader{scanner: bufio.NewScanner(strings.NewReader(commands)), output: io.Discard}, &stdout, &stderr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return current }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Count(stdout.String(), "usage: /range [reset | <from> <to>]") != 2 {
		t.Fatalf("unsupported range arguments did not get actionable usage: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "active range (rolling): 2026-10-07T10:00:00Z to 2026-10-14T10:00:00Z (UTC)") {
		t.Fatalf("invalid range arguments changed the active mode: %q", stdout.String())
	}
}

func TestInteractiveDoesNotSearchOtherDataDirectories(t *testing.T) {
	readDir, otherDir := t.TempDir(), t.TempDir()
	readStore, err := tokenlogger.Open(filepath.Join(readDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer readStore.Close()
	otherStore, err := tokenlogger.Open(filepath.Join(otherDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Close()
	event := cliReportEvent("isolated-event", time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	event.ResolvedModel = "other-data-directory-only"
	if _, err := otherStore.Append([]tokenlogger.UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	current := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	if code := runInteractiveLoopWithClock(&scannerLineReader{scanner: bufio.NewScanner(strings.NewReader("/refresh\n/quit\n")), output: io.Discard}, &stdout, &stderr, readStore, readDir, defaultSettings(readDir), false, func() time.Time { return current }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "other-data-directory-only") {
		t.Fatalf("interactive report leaked data from another directory: %q", stdout.String())
	}
}

func TestInteractiveReportQueryFailureSavesAndOffersSanitizedDiagnostic(t *testing.T) {
	dataDir := t.TempDir()
	databasePath := filepath.Join(dataDir, "token-logger.db")
	store, err := tokenlogger.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP TABLE usage_events"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	input := strings.NewReader("view\nn\n/quit\n")
	current := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	if code := runInteractiveLoopWithClock(&scannerLineReader{scanner: bufio.NewScanner(input), output: &stdout}, &stdout, &stderr, store, dataDir, defaultSettings(dataDir), false, func() time.Time { return current }); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage query failed") || !strings.Contains(stdout.String(), "Sanitized diagnostic saved locally:") {
		t.Fatalf("query failure did not use local diagnostic flow: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Open a prefilled issue draft? [y/N]") || !strings.Contains(stdout.String(), "skipped") {
		t.Fatalf("diagnostic was not offered for review: %q", stdout.String())
	}
	const fingerprintMarker = "Sanitized diagnostic saved locally: "
	marker := strings.Index(stdout.String(), fingerprintMarker)
	if marker < 0 {
		t.Fatalf("diagnostic fingerprint not found in output: %q", stdout.String())
	}
	fingerprint := strings.Fields(strings.SplitN(stdout.String()[marker+len(fingerprintMarker):], "\n", 2)[0])[0]
	diagnostic, err := store.Diagnostic(fingerprint)
	if err != nil {
		t.Fatalf("load saved diagnostic: %v", err)
	}
	if diagnostic.Message == "" || diagnostic.Message != tokenlogger.SanitizeDiagnostic(diagnostic.Message) {
		t.Fatalf("saved diagnostic is empty or not sanitized: %+v", diagnostic)
	}
	if diagnostic.Status != "skipped" {
		t.Fatalf("diagnostic review status = %q, want skipped", diagnostic.Status)
	}
}

type beforeReadLineReader struct {
	*scannerLineReader
	before func(string)
}

func (reader *beforeReadLineReader) ReadLine(prompt string) (string, error) {
	line, err := reader.scannerLineReader.ReadLine(prompt)
	if err == nil && reader.before != nil {
		reader.before(line)
	}
	return line, err
}

func TestSettingsCommand(t *testing.T) {
	dataDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"settings", "--data-dir", dataDir, "--collector", "on", "--auto-start", "off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("settings exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "collector=true") {
		t.Fatalf("unexpected settings output: %q", stdout.String())
	}
}

func TestReportIssueExportsPendingSanitizedDiagnostics(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDiagnostic("failed /home/private/project token=secret-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(t.TempDir(), "report.zip")
	if err := os.WriteFile(outputPath, []byte("keep this file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var collisionStdout, collisionStderr bytes.Buffer
	if code := run([]string{"report-issue", "--data-dir", dataDir, "--output", outputPath}, &collisionStdout, &collisionStderr); code == 0 {
		t.Fatalf("report-issue unexpectedly overwrote an existing output: stdout=%q stderr=%q", collisionStdout.String(), collisionStderr.String())
	}
	existingContents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(existingContents) != "keep this file" {
		t.Fatalf("existing output changed after rejected export: %q", existingContents)
	}
	if err := os.Remove(outputPath); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"report-issue", "--data-dir", dataDir, "--output", outputPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("report-issue exit code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	archive, err := zip.OpenReader(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 1 || archive.File[0].Name != "diagnostics.json" {
		t.Fatalf("archive entries = %#v, want diagnostics.json", archive.File)
	}
	contents, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	var report diagnosticReport
	if err := json.NewDecoder(contents).Decode(&report); err != nil {
		contents.Close()
		t.Fatal(err)
	}
	contents.Close()
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %d, want 1", len(report.Diagnostics))
	}
	message := report.Diagnostics[0].Message
	if strings.Contains(message, "secret-value") || strings.Contains(message, "/home/private") {
		t.Fatalf("report leaked sensitive diagnostic content: %q", message)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("report permissions = %o, want user-only", info.Mode().Perm())
	}

	store, err = tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pending, err := store.PendingDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending diagnostics after export = %d, want 1", len(pending))
	}
}
