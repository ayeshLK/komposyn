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
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type dataDirResolution struct {
	Path    string
	Warning string
}

func resolveDataDir(explicit string) (dataDirResolution, error) {
	return resolveDataDirWith(explicit, os.Getenv, os.UserHomeDir)
}

func resolveDataDirWith(explicit string, getenv func(string) string, homeDir func() (string, error)) (dataDirResolution, error) {
	configured := explicit
	if configured == "" {
		configured = getenv("TOKEN_LOGGER_DATA_DIR")
	}
	if configured != "" {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return dataDirResolution{}, fmt.Errorf("resolve data directory %q: %w", configured, err)
		}
		return dataDirResolution{Path: absolute}, nil
	}

	home, err := homeDir()
	if err != nil || home == "" {
		return dataDirResolution{}, fmt.Errorf("cannot determine the home directory; set TOKEN_LOGGER_DATA_DIR to choose a data directory")
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return dataDirResolution{}, fmt.Errorf("resolve home directory %q: %w", home, err)
	}
	current := filepath.Join(home, ".komposyn", "token-logger", "data")
	legacy := ""
	if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
		legacy = filepath.Join(xdg, "token-logger")
	} else {
		legacy = filepath.Join(home, ".local", "share", "token-logger")
	}
	legacy, err = filepath.Abs(legacy)
	if err != nil {
		return dataDirResolution{}, fmt.Errorf("resolve legacy data directory %q: %w", legacy, err)
	}
	if legacy == current {
		return dataDirResolution{Path: current}, nil
	}
	newState, err := hasOwnedData(current)
	if err != nil {
		return dataDirResolution{}, err
	}
	legacyState, err := hasOwnedData(legacy)
	if err != nil {
		return dataDirResolution{}, err
	}
	if newState && legacyState {
		return dataDirResolution{Path: current, Warning: fmt.Sprintf("both %s and legacy data directory %s contain Komposyn data; no data was merged. To use the legacy data, set TOKEN_LOGGER_DATA_DIR=%s", current, legacy, legacy)}, nil
	}
	if !newState && legacyState {
		return dataDirResolution{Path: legacy}, nil
	}
	return dataDirResolution{Path: current}, nil
}

func hasOwnedData(dir string) (bool, error) {
	for _, name := range []string{"token-logger.db", "token-logger.db-wal", "token-logger.db-shm", "settings.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil {
			if info.Mode().IsRegular() {
				return true, nil
			}
			continue
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect data directory %s: %w", dir, err)
		}
	}
	return false, nil
}

func reportDataDirResolution(stderr io.Writer, resolution dataDirResolution) {
	if resolution.Warning != "" {
		fmt.Fprintf(stderr, "warning: %s\n", resolution.Warning)
	}
}
