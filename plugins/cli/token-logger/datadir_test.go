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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
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

func TestResolveDataDirPreservesReadableLegacyState(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "share", "token-logger")
	settingsPath := filepath.Join(legacy, "settings.json")
	databasePath := filepath.Join(legacy, "token-logger.db")

	store, err := tokenlogger.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	event, project, session, cursor := legacyStateFixture()
	if _, err := store.AppendWithReferencesAndCursor([]tokenlogger.UsageEvent{event}, []tokenlogger.Project{project}, []tokenlogger.Session{session}, cursor); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := store.RecordDiagnostic("preserved diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDiagnosticReviewed(diagnostic.Fingerprint, "skipped"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	wantSettings := tokenlogger.Settings{
		Version: 1, OnboardingComplete: true, CollectorEnabled: true,
		AutoStart: false, DataDir: legacy, PollIntervalSeconds: 17, FailureMode: "non-blocking",
	}
	if err := tokenlogger.SaveSettings(settingsPath, wantSettings); err != nil {
		t.Fatal(err)
	}

	resolution, err := resolverForTest(home, nil)("")
	if err != nil || resolution.Path != legacy {
		t.Fatalf("resolution = %+v, err = %v; want legacy %q", resolution, err, legacy)
	}
	gotSettings, err := tokenlogger.LoadSettings(settingsPath, resolution.Path)
	if err != nil || gotSettings != wantSettings {
		t.Fatalf("settings = %+v, err = %v; want %+v", gotSettings, err, wantSettings)
	}
	store, err = tokenlogger.Open(filepath.Join(resolution.Path, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("event count = %d, err = %v; want preserved event", count, err)
	}
	gotCursor, err := store.Cursor(cursor.SourceID)
	if err != nil || gotCursor != cursor {
		t.Fatalf("cursor = %+v, err = %v; want %+v", gotCursor, err, cursor)
	}
	gotDiagnostic, err := store.Diagnostic(diagnostic.Fingerprint)
	if err != nil || gotDiagnostic.Status != "skipped" || gotDiagnostic.Message != diagnostic.Message {
		t.Fatalf("diagnostic = %+v, err = %v; want preserved reviewed diagnostic", gotDiagnostic, err)
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

func TestStoredDataSurfacesUseLegacyResolution(t *testing.T) {
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "codex", "normal.jsonl")
	surfaces := []string{"interactive", "import", "watch", "usage", "cost", "sessions", "settings", "diagnostics", "harness"}
	for _, surface := range surfaces {
		t.Run(surface, func(t *testing.T) {
			home := t.TempDir()
			legacy := filepath.Join(home, ".local", "share", "token-logger")
			newDefault := filepath.Join(home, ".komposyn", "token-logger", "data")
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("TOKEN_LOGGER_DATA_DIR", "")
			t.Setenv("XDG_DATA_HOME", "")
			seedLegacyState(t, legacy)

			var stdout, stderr bytes.Buffer
			diagnosticOutputPath := filepath.Join(t.TempDir(), "diagnostics.zip")
			code := 0
			switch surface {
			case "interactive":
				code = runWithInput(nil, strings.NewReader("view\nn\n/settings\n/quit\n"), &stdout, &stderr)
			case "import":
				code = run([]string{"import", fixture}, &stdout, &stderr)
			case "watch":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				code = runWatchWithContext([]string{fixture, "--interval", "1h"}, &stdout, &stderr, ctx)
			case "usage", "cost", "sessions":
				code = runReport(surface, []string{"--format", "json"}, &stdout, &stderr)
			case "settings":
				code = runSettings(nil, &stdout, &stderr)
			case "diagnostics":
				code = runReportIssue([]string{"--output", diagnosticOutputPath}, &stdout, &stderr)
			case "harness":
				server := &protocolServer{}
				response, _ := server.initialize(protocolRequest{ID: "init", Params: json.RawMessage(`{}`)}, &stderr)
				if !response.OK {
					code = 2
				} else if result, ok := response.Result.(map[string]any); !ok || result["data_dir"] != legacy {
					t.Fatalf("harness selected unexpected data directory: %#v", response.Result)
				}
				server.close()
			}
			if code != 0 {
				t.Fatalf("%s exit code = %d; stdout=%q stderr=%q", surface, code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(newDefault, "token-logger.db")); !os.IsNotExist(err) {
				t.Fatalf("%s opened the new default instead of legacy data (stat err: %v)", surface, err)
			}
			if surface == "interactive" && !strings.Contains(stdout.String(), "data directory: "+legacy) {
				t.Fatalf("interactive mode did not report selected path: %q", stdout.String())
			}
			if surface == "settings" && !strings.Contains(stdout.String(), "data_dir="+legacy) {
				t.Fatalf("settings did not report selected path: %q", stdout.String())
			}
			if surface == "usage" || surface == "cost" || surface == "sessions" {
				marker := "legacy-model"
				if surface == "sessions" {
					marker = "legacy-session"
				}
				if !strings.Contains(stdout.String(), marker) {
					t.Fatalf("%s did not read legacy database contents: %q", surface, stdout.String())
				}
			}
			if surface == "diagnostics" {
				archive, err := zip.OpenReader(diagnosticOutputPath)
				if err != nil {
					t.Fatal(err)
				}
				defer archive.Close()
				file, err := archive.File[0].Open()
				if err != nil {
					t.Fatal(err)
				}
				contents, err := io.ReadAll(file)
				file.Close()
				if err != nil || !strings.Contains(string(contents), "legacy diagnostic marker") {
					t.Fatalf("diagnostic export did not read legacy diagnostic: %s, err=%v", contents, err)
				}
			}
		})
	}
}

func seedLegacyState(t *testing.T, dataDir string) {
	t.Helper()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	event, project, session, cursor := legacyStateFixture()
	if _, err := store.AppendWithReferencesAndCursor([]tokenlogger.UsageEvent{event}, []tokenlogger.Project{project}, []tokenlogger.Session{session}, cursor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordDiagnostic("legacy diagnostic marker"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	settings := tokenlogger.Settings{
		Version: 1, OnboardingComplete: true, CollectorEnabled: false,
		AutoStart: false, DataDir: dataDir, PollIntervalSeconds: 5, FailureMode: "non-blocking",
	}
	if err := tokenlogger.SaveSettings(filepath.Join(dataDir, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
}

func legacyStateFixture() (tokenlogger.UsageEvent, tokenlogger.Project, tokenlogger.Session, tokenlogger.Cursor) {
	now := time.Now().UTC().Truncate(time.Second)
	project := tokenlogger.Project{ID: "legacy-project", Root: "/legacy/project", DisplayName: "Legacy project"}
	session := tokenlogger.Session{ID: "legacy-session", ProjectID: project.ID, Client: tokenlogger.Client{Name: "codex-cli"}}
	event := tokenlogger.UsageEvent{
		EventID: "legacy-event", SchemaVersion: 1, EventType: "model.request", OccurredAt: now,
		ProjectID: project.ID, SessionID: session.ID, Client: tokenlogger.Client{Name: "codex-cli"},
		Provider: "openai", ResolvedModel: "legacy-model",
		Usage:        tokenlogger.TokenUsage{InputTokens: 12, OutputTokens: 3},
		Provenance:   tokenlogger.Provenance{Adapter: "codex", SourceID: "legacy-source", SourceRecord: "legacy-record"},
		IngestionKey: "legacy-ingestion-key",
	}
	cursor := tokenlogger.Cursor{SourceID: "legacy-cursor", FileFingerprint: "legacy-fingerprint", ByteOffset: 123, LastProviderRecordID: "legacy-record", SourceGeneration: 2}
	return event, project, session, cursor
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
