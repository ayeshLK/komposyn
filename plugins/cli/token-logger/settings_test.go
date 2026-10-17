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
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCLIOwnedSettingsRoundTripPreservesLegacyPollingInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"poll_interval_seconds":60,"onboarding_complete":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettings(path, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if settings.PollIntervalMillis != 60000 || settings.collectorPollInterval() != time.Minute || !settings.OnboardingComplete {
		t.Fatalf("legacy settings not preserved: %+v", settings)
	}
	settings.CollectorEnabled = true
	if err := saveSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path, "fallback")
	if err != nil || got != settings {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, settings)
	}
}

func TestCLIOwnedSettingsReplacementKeepsPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"collector_enabled":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings(t.TempDir())
	settings.CollectorEnabled = true
	if err := saveSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o, want 600", info.Mode().Perm())
	}
}
