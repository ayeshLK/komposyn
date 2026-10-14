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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resolverForTest(home string, env map[string]string) func(string) (dataDirResolution, error) {
	return func(explicit string) (dataDirResolution, error) {
		return resolveDataDirWith(explicit, func(key string) string { return env[key] }, func() (string, error) { return home, nil })
	}
}

func TestResolveDataDirUsesNewDefaultAndIgnoresXDG(t *testing.T) {
	home := t.TempDir()
	resolve := resolverForTest(home, map[string]string{"XDG_DATA_HOME": filepath.Join(t.TempDir(), "xdg")})
	got, err := resolve("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".komposyn", "token-logger", "data")
	if got.Path != want || got.Warning != "" {
		t.Fatalf("got %+v, want path %q without warning", got, want)
	}
}

func TestResolveDataDirExplicitAndEnvironmentOverridesSkipDiscovery(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "share", "token-logger")
	writeDataFile(t, filepath.Join(legacy, "settings.json"))
	envPath := filepath.Join(t.TempDir(), "environment-data")
	resolve := resolverForTest(home, map[string]string{"TOKEN_LOGGER_DATA_DIR": envPath})
	got, err := resolve("")
	if err != nil || got.Path != envPath || got.Warning != "" {
		t.Fatalf("environment override: got %+v, err %v", got, err)
	}
	explicit := filepath.Join(t.TempDir(), "explicit-data")
	got, err = resolveDataDirWith(explicit, func(key string) string { return envPath }, func() (string, error) {
		t.Fatal("override must not resolve home or discover legacy state")
		return "", nil
	})
	if err != nil || got.Path != explicit {
		t.Fatalf("explicit override: got %+v, err %v", got, err)
	}
}

func TestResolveDataDirMakesRelativeOverrideAbsolute(t *testing.T) {
	got, err := resolveDataDirWith(filepath.Join("relative", "data"), func(string) string { return "" }, func() (string, error) {
		t.Fatal("explicit override must not resolve home")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join("relative", "data"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Fatalf("got %q, want absolute path %q", got.Path, want)
	}
}

func TestResolveDataDirSelectsLegacyOnlyWhenItHasOwnedState(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "share", "token-logger")
	writeDataFile(t, filepath.Join(legacy, "token-logger.db-wal"))
	got, err := resolverForTest(home, nil)("")
	if err != nil || got.Path != legacy || got.Warning != "" {
		t.Fatalf("got %+v, err %v; want legacy %q", got, err, legacy)
	}
}

func TestResolveDataDirNewStateWinsConflictWithoutMerge(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".komposyn", "token-logger", "data")
	legacy := filepath.Join(home, ".local", "share", "token-logger")
	writeDataFile(t, filepath.Join(current, "token-logger.db"))
	writeDataFile(t, filepath.Join(legacy, "settings.json"))
	got, err := resolverForTest(home, nil)("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != current || !strings.Contains(got.Warning, "no data was merged") || !strings.Contains(got.Warning, "TOKEN_LOGGER_DATA_DIR="+legacy) {
		t.Fatalf("unexpected conflict result: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(current, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy settings unexpectedly copied into new directory: %v", err)
	}
}

func TestResolveDataDirRecognizesOnlyOwnedFiles(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "share", "token-logger")
	writeDataFile(t, filepath.Join(legacy, "unrelated.txt"))
	got, err := resolverForTest(home, nil)("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".komposyn", "token-logger", "data")
	if got.Path != want {
		t.Fatalf("unrelated file triggered compatibility selection: got %q want %q", got.Path, want)
	}
}

func TestResolveDataDirDoesNotTreatDirectoriesAsStateFiles(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "share", "token-logger")
	if err := os.MkdirAll(filepath.Join(legacy, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := resolverForTest(home, nil)("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".komposyn", "token-logger", "data")
	if got.Path != want {
		t.Fatalf("directory named like a state file triggered compatibility selection: got %q want %q", got.Path, want)
	}
}

func TestResolveDataDirErrorsWhenHomeCannotBeResolved(t *testing.T) {
	got, err := resolveDataDirWith("", func(string) string { return "" }, func() (string, error) { return "", os.ErrNotExist })
	if err == nil || !strings.Contains(err.Error(), "TOKEN_LOGGER_DATA_DIR") || got.Path != "" {
		t.Fatalf("got %+v, err %v; want actionable home error", got, err)
	}
}

func TestResolveDataDirDoesNotUseHistoricalWorkingDirectoryFallback(t *testing.T) {
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)
	writeDataFile(t, filepath.Join(workingDirectory, ".token-logger", "token-logger.db"))
	got, err := resolveDataDirWith("", func(string) string { return "" }, func() (string, error) { return "", os.ErrNotExist })
	if err == nil || got.Path != "" {
		t.Fatalf("got %+v, err %v; expected no current-directory fallback", got, err)
	}
}

func TestResolveDataDirUsesXDGOnlyForLegacyDiscovery(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(t.TempDir(), "xdg", "token-logger")
	writeDataFile(t, filepath.Join(legacy, "settings.json"))
	got, err := resolverForTest(home, map[string]string{"XDG_DATA_HOME": filepath.Dir(legacy)})("")
	if err != nil || got.Path != legacy {
		t.Fatalf("got %+v, err %v; want XDG legacy path %q", got, err, legacy)
	}
}

func TestHarnessInitializeUsesSharedDefaultResolver(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TOKEN_LOGGER_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	server := &protocolServer{}
	response, _ := server.initialize(protocolRequest{ID: "init", Params: json.RawMessage(`{}`)}, &strings.Builder{})
	if !response.OK {
		t.Fatalf("initialize failed: %+v", response.Error)
	}
	defer server.close()
	result, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("initialize result has unexpected type: %T", response.Result)
	}
	want := filepath.Join(home, ".komposyn", "token-logger", "data")
	if result["data_dir"] != want {
		t.Fatalf("harness data_dir = %v, want %q", result["data_dir"], want)
	}
}

func writeDataFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}
