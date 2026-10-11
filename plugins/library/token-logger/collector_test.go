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
	"path/filepath"
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
	if collector.Health().Status != "stopped" {
		t.Fatalf("collector did not stop: %+v", collector.Health())
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
