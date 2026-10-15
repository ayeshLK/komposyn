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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCollectorPollsBothProviderShapesAndReportsHealth(t *testing.T) {
	store := openTestStore(t)
	collector, err := NewCollector(store, []string{
		filepath.Join("testdata", "codex", "normal.jsonl"),
		filepath.Join("testdata", "claude-code", "normal.jsonl"),
	}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.PollOnce()
	if err != nil || result.Receipt.Imported != 4 {
		t.Fatalf("poll result=%+v err=%v", result, err)
	}
	if health := collector.Health(); health.Status != "healthy" || health.Sources != 2 || health.Imported != 4 {
		t.Fatalf("unexpected health: %+v", health)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := collector.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if health := collector.Health(); health.Status != "healthy" || health.Lifecycle != "stopped" || health.Imported != 4 || health.Duplicates != 0 {
		t.Fatalf("collector did not stop: %+v", collector.Health())
	}
}

func TestCollectorLargeRecordsUseOneUnlimitedSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		largeLine  string
		importFile func(string, *Store) (ImportResult, error)
	}{
		{"claude", filepath.Join("testdata", "claude-code", "normal.jsonl"), `{"type":"user","message":{"content":"` + strings.Repeat("x", 70_000) + `"}}`, (ClaudeAdapter{}).Import},
		{"codex", filepath.Join("testdata", "codex", "normal.jsonl"), `{"type":"response_item","payload":{"type":"message","content":"` + strings.Repeat("x", 70_000) + `"}}`, (CodexAdapter{}).Import},
	}
	for _, test := range tests {
		for _, position := range []string{"before-detection", "after-detection"} {
			t.Run(test.name+"/"+position, func(t *testing.T) {
				fixture, err := os.ReadFile(test.fixture)
				if err != nil {
					t.Fatal(err)
				}
				contents := []byte(test.largeLine + "\n" + string(fixture))
				if position == "after-detection" {
					firstBreak := strings.IndexByte(string(fixture), '\n') + 1
					contents = append(append(append([]byte{}, fixture[:firstBreak]...), []byte(test.largeLine+"\n")...), fixture[firstBreak:]...)
				}
				contents = append(contents, []byte(`{"type":`)...)
				path := filepath.Join(t.TempDir(), test.name+".jsonl")
				if err := os.WriteFile(path, contents, 0o600); err != nil {
					t.Fatal(err)
				}
				directStore := openTestStore(t)
				direct, err := test.importFile(path, directStore)
				if err != nil || direct.Receipt.Imported != 2 || direct.Outcomes.DeferredIncompleteTail != 1 {
					t.Fatalf("direct result=%+v err=%v", direct, err)
				}
				collectorStore := openTestStore(t)
				collector, err := NewCollector(collectorStore, []string{path}, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				first, err := collector.PollOnce()
				if err != nil || first.Receipt.Imported != direct.Receipt.Imported || first.Outcomes.DeferredIncompleteTail != 1 {
					t.Fatalf("collector first=%+v err=%v direct=%+v", first, err, direct)
				}
				second, err := collector.PollOnce()
				if err != nil || second.Receipt != (Receipt{}) || second.Outcomes.UnchangedSnapshot != 1 || second.Outcomes.DeferredIncompleteTail != 1 {
					t.Fatalf("collector retry=%+v err=%v", second, err)
				}
			})
		}
	}
}

func TestCollectorIsolatesDiscoveryFailuresAndRecovers(t *testing.T) {
	fixture := filepath.Join("testdata", "claude-code", "normal.jsonl")
	badParent := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(badParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(badParent, "child")
	for _, roots := range [][]string{{badRoot, fixture}, {fixture, badRoot}} {
		store := openTestStore(t)
		collector, err := NewCollector(store, roots, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result, pollErr := collector.PollOnce()
		if pollErr == nil || result.Receipt.Imported != 2 || result.Outcomes.FailedSource != 1 {
			t.Fatalf("isolated discovery result=%+v err=%v roots=%v", result, pollErr, roots)
		}
		if health := collector.Health(); health.Status != "degraded" || health.Sources != 1 || health.DiscoveryFailures != 1 || health.Outcomes.FailedSource != 1 {
			t.Fatalf("degraded health=%+v", health)
		}
	}

	store := openTestStore(t)
	recoveryRoot := filepath.Join(t.TempDir(), "later.jsonl")
	collector, err := NewCollector(store, []string{fixture, recoveryRoot}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, firstErr := collector.PollOnce()
	if firstErr == nil || first.Receipt.Imported != 2 || first.Outcomes.FailedSource != 1 {
		t.Fatalf("pre-recovery result=%+v err=%v", first, firstErr)
	}
	contents, err := os.ReadFile(filepath.Join("testdata", "codex", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryRoot, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	second, secondErr := collector.PollOnce()
	if secondErr != nil || second.Receipt.Imported != 2 || second.Outcomes.FailedSource != 0 {
		t.Fatalf("recovered result=%+v err=%v", second, secondErr)
	}
	if health := collector.Health(); health.Status != "healthy" || health.DiscoveryFailures != 0 || health.LastSuccessAt.IsZero() || health.Outcomes.FailedSource != 1 {
		t.Fatalf("recovered health=%+v", health)
	}
	if err := os.Remove(recoveryRoot); err != nil {
		t.Fatal(err)
	}
	third, thirdErr := collector.PollOnce()
	if thirdErr == nil || third.Outcomes.FailedSource != 1 || collector.Health().Status != "degraded" || collector.Health().Outcomes.FailedSource != 2 {
		t.Fatalf("post-recovery removal result=%+v err=%v health=%+v", third, thirdErr, collector.Health())
	}
}

func TestCollectorIsolatesUnreadableSubtree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not define Windows directory readability")
	}
	root := t.TempDir()
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "healthy.jsonl"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "hidden.jsonl"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })

	store := openTestStore(t)
	collector, err := NewCollector(store, []string{root}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr := collector.PollOnce()
	if pollErr == nil {
		t.Skip("host privileges bypass unreadable-directory permission bits")
	}
	if result.Receipt.Imported != 2 || result.Outcomes.FailedSource != 1 || collector.Health().Sources != 1 {
		t.Fatalf("unreadable subtree blocked healthy source: result=%+v err=%v health=%+v", result, pollErr, collector.Health())
	}
}

func TestCollectorRequiredOptionalAndDuplicateRoots(t *testing.T) {
	store := openTestStore(t)
	missing := filepath.Join(t.TempDir(), "missing")
	collector, err := NewCollector(store, []string{missing}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr := collector.PollOnce()
	if pollErr == nil || result.Outcomes.FailedSource != 1 || collector.Health().Status != "degraded" {
		t.Fatalf("required missing root result=%+v err=%v health=%+v", result, pollErr, collector.Health())
	}

	optional, err := NewCollectorWithRoots(store, []CollectorRoot{{Path: missing}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr = optional.PollOnce()
	if pollErr != nil || result.Outcomes.FailedSource != 0 || optional.Health().Status != "healthy" || optional.Health().Sources != 0 {
		t.Fatalf("optional missing root result=%+v err=%v health=%+v", result, pollErr, optional.Health())
	}

	empty, err := NewCollector(store, []string{t.TempDir()}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr = empty.PollOnce()
	if pollErr != nil || result != (ImportResult{}) || empty.Health().Status != "healthy" || empty.Health().Sources != 0 {
		t.Fatalf("empty required directory result=%+v err=%v health=%+v", result, pollErr, empty.Health())
	}

	root := t.TempDir()
	path := filepath.Join(root, "normal.jsonl")
	contents, err := os.ReadFile(filepath.Join("testdata", "claude-code", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := []string{root, path, filepath.Join(root, ".", "normal.jsonl")}
	link := filepath.Join(t.TempDir(), "source-link.jsonl")
	if err := os.Symlink(path, link); err == nil {
		roots = append(roots, link)
	} else if runtime.GOOS != "windows" {
		t.Fatalf("create source symlink: %v", err)
	}
	deduplicated, err := NewCollector(store, roots, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, pollErr = deduplicated.PollOnce()
	if pollErr != nil || result.Receipt.Imported != 2 || deduplicated.Health().Sources != 1 {
		t.Fatalf("deduplicated roots result=%+v err=%v health=%+v", result, pollErr, deduplicated.Health())
	}
	if _, err := os.Lstat(link); err == nil {
		identityStore := openTestStore(t)
		identityCollector, err := NewCollector(identityStore, []string{link, path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := identityCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 || identityCollector.Health().Sources != 1 {
			t.Fatalf("symlink-first deduplication result=%+v err=%v health=%+v", result, err, identityCollector.Health())
		}
		absoluteLink, err := filepath.Abs(link)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := identityStore.Cursor("claude-file-" + digest([]byte(absoluteLink))); err != nil {
			t.Fatalf("configured symlink source identity was not preserved: %v", err)
		}
		reorderedCollector, err := NewCollector(identityStore, []string{path, link}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := reorderedCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
			t.Fatalf("reordered aliases changed source identity: result=%+v err=%v", result, err)
		}

		directStore := openTestStore(t)
		directCollector, err := NewCollector(directStore, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := directCollector.PollOnce(); err != nil || result.Receipt.Imported != 2 {
			t.Fatalf("direct source setup result=%+v err=%v", result, err)
		}
		aliasedCollector, err := NewCollector(directStore, []string{link, path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := aliasedCollector.PollOnce(); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
			t.Fatalf("new alias changed established source identity: result=%+v err=%v", result, err)
		}
		if count, err := directStore.EventCount(); err != nil || count != 2 {
			t.Fatalf("alias transition event count=%d err=%v", count, err)
		}
	}
}

func TestCollectorCancellationJoinsWithoutPartialPersistence(t *testing.T) {
	store := openTestStore(t)
	collector, err := NewCollector(store, []string{filepath.Join("testdata", "claude-code", "normal.jsonl")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	collector.beforeSource = func(string) {
		close(started)
		<-release
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.RunImmediate(ctx, nil) }()
	<-started
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if health := collector.Health(); health.Lifecycle != "stopped" || health.Status != "cancelled" || !health.Interrupted || health.Outcomes.FailedSource != 0 {
		t.Fatalf("cancelled collector health=%+v", health)
	}
	if count, err := store.EventCount(); err != nil || count != 0 {
		t.Fatalf("cancelled collector persisted events=%d err=%v", count, err)
	}

	preCancelled, err := NewCollector(store, []string{filepath.Join("testdata", "codex", "normal.jsonl")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	preCtx, preCancel := context.WithCancel(context.Background())
	preCancel()
	if err := preCancelled.RunImmediate(preCtx, nil); err != nil {
		t.Fatal(err)
	}
	if health := preCancelled.Health(); health.Lifecycle != "stopped" || !health.Interrupted || health.Outcomes.FailedSource != 0 {
		t.Fatalf("pre-cancelled collector health=%+v", health)
	}
}

func TestCollectorJoinsAfterPersistenceFailure(t *testing.T) {
	store := openTestStore(t)
	collector, err := NewCollector(store, []string{filepath.Join("testdata", "claude-code", "normal.jsonl")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- collector.RunImmediate(ctx, func(_ ImportResult, pollErr error) {
			observed <- pollErr
			cancel()
		})
	}()
	if pollErr := <-observed; pollErr == nil {
		t.Fatal("closed Store persistence failure was not reported")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if health := collector.Health(); health.Lifecycle != "stopped" || health.Status != "degraded" || health.Outcomes.FailedSource != 1 {
		t.Fatalf("persistence-failed collector health=%+v", health)
	}
}

func TestCollectorOutcomeHealthAndDiagnosticDeduplication(t *testing.T) {
	t.Run("warning degrades and deduplicates", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "unsupported.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"token_usage_record","input_tokens":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		collector, err := NewCollector(store, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			result, err := collector.PollOnce()
			if err != nil || result.Outcomes.UnsupportedUsage != 1 {
				t.Fatalf("poll result=%+v err=%v", result, err)
			}
		}
		health := collector.Health()
		if health.Status != "degraded" || health.Pending {
			t.Fatalf("unexpected warning health: %+v", health)
		}
		diagnostics, err := store.PendingDiagnostics()
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 1 || diagnostics[0].OccurrenceCount != 2 || diagnostics[0].Message != "adapter=codex reason=unsupported_usage" {
			t.Fatalf("unexpected diagnostics: %+v", diagnostics)
		}
	})

	t.Run("partial tail is visible but healthy", func(t *testing.T) {
		store := openTestStore(t)
		path := filepath.Join(t.TempDir(), "partial.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"token_count","payload":`), 0o600); err != nil {
			t.Fatal(err)
		}
		collector, err := NewCollector(store, []string{path}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result, err := collector.PollOnce()
		if err != nil || result.Outcomes.DeferredIncompleteTail != 1 {
			t.Fatalf("poll result=%+v err=%v", result, err)
		}
		if health := collector.Health(); health.Status != "healthy" || !health.Pending {
			t.Fatalf("unexpected pending health: %+v", health)
		}
	})
}

func TestCollectorRunReportsSubsequentWarningPolls(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "changing.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"session","model":"gpt-test"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{path}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var results []ImportResult
	err = collector.RunWithResults(ctx, func(result ImportResult, pollErr error) {
		if pollErr != nil {
			t.Errorf("poll error: %v", pollErr)
			cancel()
			return
		}
		results = append(results, result)
		if len(results) == 1 {
			if err := os.WriteFile(path, []byte(`{"type":"token_usage_record","input_tokens":1}`+"\n"), 0o600); err != nil {
				t.Errorf("replace source: %v", err)
				cancel()
			}
		} else if result.Outcomes.UnsupportedUsage == 1 {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 || results[len(results)-1].Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("subsequent warning was not observed: %+v", results)
	}
	if health := collector.Health(); health.Outcomes.UnsupportedUsage == 0 {
		t.Fatalf("final health omitted warning outcome: %+v", health)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := DefaultSettings("/tmp/token-logger")
	want.OnboardingComplete = true
	want.CollectorEnabled = true
	want.AutoStart = false
	if err := SaveSettings(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(path, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("settings=%+v want=%+v", got, want)
	}
}

func TestSaveSettingsReplacesPermissiveFilePrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"collector_enabled":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := DefaultSettings("/tmp/token-logger")
	want.CollectorEnabled = true
	if err := SaveSettings(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("settings mode = %o, want 600", mode)
		}
	}
	got, err := LoadSettings(path, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("settings=%+v want=%+v", got, want)
	}
}

func TestSaveSettingsPreservesPreviousFileWhenPreparationFails(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "settings.json")
	previous := []byte("previous settings\n")
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(filepath.Join(path, "nested.json"), DefaultSettings("/tmp/token-logger")); err == nil {
		t.Fatal("SaveSettings succeeded with a file as its parent")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(previous) {
		t.Fatalf("previous settings changed to %q", got)
	}
}
