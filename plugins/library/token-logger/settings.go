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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings stores explicit local collector preferences. It contains no
// provider credentials.
type Settings struct {
	Version             int    `json:"version"`
	OnboardingComplete  bool   `json:"onboarding_complete"`
	CollectorEnabled    bool   `json:"collector_enabled"`
	AutoStart           bool   `json:"auto_start"`
	DataDir             string `json:"data_dir"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	FailureMode         string `json:"failure_mode"`
}

// DefaultSettings returns conservative first-run settings. Auto-start is
// deliberately disabled until separately approved by the user.
func DefaultSettings(dataDir string) Settings {
	return Settings{Version: 1, DataDir: dataDir, PollIntervalSeconds: 5, FailureMode: "non-blocking"}
}

// LoadSettings loads settings or returns defaults when the file is absent.
func LoadSettings(path, dataDir string) (Settings, error) {
	settings := DefaultSettings(dataDir)
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(contents, &settings); err != nil {
		return Settings{}, fmt.Errorf("parse settings: %w", err)
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
	if settings.FailureMode == "" {
		settings.FailureMode = "non-blocking"
	}
	return settings, nil
}

// SaveSettings writes settings with user-only permissions where supported.
func SaveSettings(path string, settings Settings) error {
	if settings.Version == 0 {
		settings.Version = 1
	}
	if settings.PollIntervalSeconds <= 0 {
		settings.PollIntervalSeconds = 5
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
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
