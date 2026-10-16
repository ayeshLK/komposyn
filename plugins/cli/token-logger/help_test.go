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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicCommandHelpDoesNotTouchDataDirectory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("TOKEN_LOGGER_DATA_DIR", dataDir)
	cases := [][]string{
		{"--help"}, {"-h"}, {"help"}, {"help", "import"}, {"help", "watch", "add"},
		{"version", "--help"}, {"import", "--help"}, {"usage", "--help"}, {"cost", "-h"},
		{"projects", "--help"}, {"projects", "list", "--help"}, {"projects", "show", "project-id", "--help"},
		{"sessions", "--help"}, {"sessions", "list", "--help"}, {"sessions", "show", "session-id", "--help"},
		{"watch", "--help"}, {"watch", "add", "--help"}, {"watch", "remove", "--help"},
		{"watch", "list", "--help"}, {"watch", "start", "--help"}, {"watch", "status", "--help"},
		{"watch", "stop", "--help"}, {"watch", "foreground", "--help"},
		{"settings", "--help"}, {"report-issue", "--help"}, {"harness", "--help"},
		{"completion", "--help"},
	}
	for _, command := range publicCLICommands {
		cases = append(cases, []string{"help", command})
	}
	for _, subcommand := range publicWatchCommands {
		cases = append(cases, []string{"help", "watch", subcommand})
	}
	for _, resourceCommand := range []string{"projects list", "projects show", "sessions list", "sessions show"} {
		cases = append(cases, append([]string{"help"}, strings.Split(resourceCommand, " ")...))
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runWithInput(args, strings.NewReader("unused\n"), &stdout, &stderr); code != 0 {
				t.Fatalf("help exit=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
			}
			if stdout.Len() == 0 || stderr.Len() != 0 {
				t.Fatalf("help stream contract: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("help output has no usage syntax: %q", stdout.String())
			}
			if args[0] == "help" && len(args) > 1 {
				topic := strings.Join(args[1:], " ")
				if !strings.Contains(stdout.String(), "komposyn-lg "+topic) {
					t.Fatalf("help topic %q returned unrelated help: %q", topic, stdout.String())
				}
			}
			if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
				t.Fatalf("help created/accessed data directory: stat err=%v", err)
			}
		})
	}
}

func TestInvalidHelpTopicsRemainUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"help", "unknown"}, {"unknown", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown help topic") {
			t.Errorf("args=%v exit/output=(%d,%q,%q), want stderr usage error", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestInternalWatchChildIsNotAdvertisedByHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"watch", "serve", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("watch parent help exit=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "serve") {
		t.Fatalf("public watch help advertised the internal child command: %s", stdout.String())
	}
}

func TestShellCompletionIsDeterministicAndPublicOnly(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			var first, second, stderr bytes.Buffer
			for _, output := range []*bytes.Buffer{&first, &second} {
				if code := run([]string{"completion", shell}, output, &stderr); code != 0 {
					t.Fatalf("completion exit=%d stderr=%q", code, stderr.String())
				}
			}
			if first.String() != second.String() || stderr.Len() != 0 {
				t.Fatalf("completion is not deterministic or wrote diagnostics: first=%q second=%q stderr=%q", first.String(), second.String(), stderr.String())
			}
			for _, command := range publicCLICommands {
				if !strings.Contains(first.String(), command) {
					t.Errorf("completion omits public command %q", command)
				}
			}
			for _, subcommand := range append(append([]string{}, publicWatchCommands...), "list", "show") {
				if !strings.Contains(first.String(), subcommand) {
					t.Errorf("completion omits public subcommand %q", subcommand)
				}
			}
			options := []string{"--help", "--version", "--data-dir", "--format", "--from", "--to", "--project", "--session", "--client", "--provider", "--model", "--group-by", "--timezone", "--interval", "--timeout", "--defaults", "--collector", "--auto-start", "--output"}
			for _, option := range options {
				completionForm := option
				if shell == "fish" {
					completionForm = "-l " + strings.TrimPrefix(option, "--")
				}
				if !strings.Contains(first.String(), completionForm) {
					t.Errorf("completion omits public option %q (expected %q)", option, completionForm)
				}
			}
			for _, internal := range []string{"serve", "_watch-lifecycle"} {
				if strings.Contains(first.String(), internal) {
					t.Errorf("completion advertises internal command %q", internal)
				}
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"completion", "unknown"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("invalid shell exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIReferenceMatchesPublicCommandsAndCurrentReleaseState(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		contents, err := os.ReadFile(filepath.Join("..", "..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}
	cliReference := read(filepath.Join("docs", "cli-reference.md"))
	for _, command := range publicCLICommands {
		if !strings.Contains(cliReference, "komposyn-lg "+command) {
			t.Errorf("CLI reference omits public command %q", command)
		}
	}
	for _, subcommand := range publicWatchCommands {
		if !strings.Contains(cliReference, "komposyn-lg watch "+subcommand) {
			t.Errorf("CLI reference omits public watch subcommand %q", subcommand)
		}
	}
	for _, stale := range []string{"issue #68", "tracked in [issue", "Detached registration and lifecycle controls are tracked"} {
		if strings.Contains(cliReference, stale) {
			t.Errorf("CLI reference retains completed watch roadmap statement %q", stale)
		}
	}
	readme, gettingStarted := read("README.md"), read(filepath.Join("docs", "getting-started.md"))
	for _, stale := range []string{"currently pre-release", "does not yet contain a published signed release"} {
		if strings.Contains(readme, stale) || strings.Contains(gettingStarted, stale) {
			t.Errorf("release-facing docs retain stale statement %q", stale)
		}
	}
	if !strings.Contains(readme, "Signed stable releases are published") || !strings.Contains(gettingStarted, "Signed stable GitHub releases are available") {
		t.Fatal("release-facing docs do not state that stable signed releases are available")
	}
}
