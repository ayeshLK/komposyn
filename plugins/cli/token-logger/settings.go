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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// appSettings stores explicit local collector preferences. It contains no
// provider credentials.
type appSettings struct {
	Version             int    `json:"version"`
	OnboardingComplete  bool   `json:"onboarding_complete"`
	CollectorEnabled    bool   `json:"collector_enabled"`
	AutoStart           bool   `json:"auto_start"`
	DataDir             string `json:"data_dir"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	PollIntervalMillis  int64  `json:"poll_interval_millis"`
	FailureMode         string `json:"failure_mode"`
}

// DefaultSettings returns conservative first-run settings. Auto-start is
// deliberately disabled until separately approved by the user.
func defaultSettings(dataDir string) appSettings {
	return appSettings{Version: 1, DataDir: dataDir, PollIntervalSeconds: 5, PollIntervalMillis: 5000, FailureMode: "non-blocking"}
}

// LoadSettings loads settings or returns defaults when the file is absent.
func loadSettings(path, dataDir string) (appSettings, error) {
	settings := defaultSettings(dataDir)
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return appSettings{}, fmt.Errorf("read settings: %w", err)
	}
	// Leave the newer field unset while decoding an existing file so a legacy
	// poll_interval_seconds value is not shadowed by the current default.
	settings.PollIntervalMillis = 0
	if err := json.Unmarshal(contents, &settings); err != nil {
		return appSettings{}, fmt.Errorf("parse settings: %w", err)
	}
	if settings.Version == 0 {
		settings.Version = 1
	}
	if settings.DataDir == "" {
		settings.DataDir = dataDir
	}
	if settings.PollIntervalSeconds <= 0 {
		settings.PollIntervalSeconds = 5
	}
	if settings.PollIntervalMillis <= 0 {
		settings.PollIntervalMillis = int64(settings.PollIntervalSeconds) * 1000
	}
	if settings.FailureMode == "" {
		settings.FailureMode = "non-blocking"
	}
	return settings, nil
}

// SaveSettings writes settings with user-only permissions where supported.
func saveSettings(path string, settings appSettings) error {
	if settings.Version == 0 {
		settings.Version = 1
	}
	if settings.PollIntervalSeconds <= 0 {
		settings.PollIntervalSeconds = int((settings.PollIntervalMillis + 999) / 1000)
		if settings.PollIntervalSeconds <= 0 {
			settings.PollIntervalSeconds = 5
		}
	}
	if settings.PollIntervalMillis <= 0 {
		settings.PollIntervalMillis = int64(settings.PollIntervalSeconds) * 1000
	}
	if settings.FailureMode == "" {
		settings.FailureMode = "non-blocking"
	}
	contents, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect temporary settings file: %w", err)
	}
	if _, err := temporary.Write(append(contents, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("flush settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary settings file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

// CollectorPollInterval returns the exact persisted polling duration while
// retaining compatibility with settings written before millisecond storage.
func (settings appSettings) collectorPollInterval() time.Duration {
	if settings.PollIntervalMillis > 0 {
		return time.Duration(settings.PollIntervalMillis) * time.Millisecond
	}
	if settings.PollIntervalSeconds > 0 {
		return time.Duration(settings.PollIntervalSeconds) * time.Second
	}
	return 5 * time.Second
}
