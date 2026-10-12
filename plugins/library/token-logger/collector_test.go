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
	if health := collector.Health(); health.Status != "stopped" || health.Imported != 4 || health.Duplicates != 0 {
		t.Fatalf("collector did not stop: %+v", collector.Health())
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
