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
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
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
