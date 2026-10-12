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
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

func TestImportCommand(t *testing.T) {
	dataDir := t.TempDir()
	fixture := filepath.Join("..", "..", "library", "token-logger", "testdata", "codex", "normal.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"import", fixture, "--data-dir", dataDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("import exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "imported=2") {
		t.Fatalf("unexpected import output: %q", stdout.String())
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
	if !strings.Contains(stdout.String(), "imported=2") {
		t.Fatalf("unexpected Claude import output: %q", stdout.String())
	}
}

func TestCLIUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unknown"}, &stdout, &stderr); code != 1 {
		t.Fatalf("unknown command exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("missing usage output: %q", stderr.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), "komposyn-lg "+tokenlogger.Version; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--version exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), "komposyn-lg "+tokenlogger.Version; got != want {
		t.Fatalf("--version output = %q, want %q", got, want)
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
	if code := runInteractive(strings.NewReader("/by-client\n/quit\n"), &stdout, &stderr, store, dataDir); code != 0 {
		t.Fatalf("interactive exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, expected := range []string{"CLIENT", "PROVIDER", "MODEL", "REQUESTS", "SESSIONS", "1.1K", "pricing unavailable"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("interactive output does not contain %q: %s", expected, stdout.String())
		}
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
