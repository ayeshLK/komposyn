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
	"path/filepath"
	"strings"
	"testing"
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
