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
	"fmt"
	"os"
	"os/exec"
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

func TestBashAndZshCompletionResultsMatchCommandParsers(t *testing.T) {
	tests := []struct {
		name  string
		bash  string
		zsh   string
		check func(*testing.T, []string)
	}{
		{
			name: "help command targets",
			bash: `COMP_WORDS=(komposyn-lg help ""); COMP_CWORD=2; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg help ""); CURRENT=3; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				for _, candidate := range publicCLICommands {
					assertContains(t, candidates, candidate)
				}
			},
		},
		{
			name: "nested help watch targets",
			bash: `COMP_WORDS=(komposyn-lg help watch ""); COMP_CWORD=3; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg help watch ""); CURRENT=4; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				for _, candidate := range publicWatchCommands {
					assertContains(t, candidates, candidate)
				}
			},
		},
		{
			name: "watch subcommands",
			bash: `COMP_WORDS=(komposyn-lg watch ""); COMP_CWORD=2; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg watch ""); CURRENT=3; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				for _, candidate := range publicWatchCommands {
					assertContains(t, candidates, candidate)
				}
			},
		},
		{
			name: "completion shells",
			bash: `COMP_WORDS=(komposyn-lg completion ""); COMP_CWORD=2; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg completion ""); CURRENT=3; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				for _, candidate := range []string{"bash", "zsh", "fish", "powershell"} {
					assertContains(t, candidates, candidate)
				}
			},
		},
		{
			name: "usage formats",
			bash: `COMP_WORDS=(komposyn-lg usage --format ""); COMP_CWORD=3; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg usage --format ""); CURRENT=4; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				if len(candidates) == 0 {
					t.Fatal("no report format candidates")
				}
				for _, candidate := range candidates {
					if _, _, _, err := parseReportArgs("usage", []string{"--format", candidate}); err != nil {
						t.Errorf("completion suggested rejected usage format %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "watch status formats",
			bash: `COMP_WORDS=(komposyn-lg watch status --format ""); COMP_CWORD=4; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg watch status --format ""); CURRENT=5; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				if len(candidates) == 0 {
					t.Fatal("no watch format candidates")
				}
				for _, candidate := range candidates {
					_, options, err := parseWatchOptions([]string{"--format", candidate}, false)
					if err == nil {
						err = validateWatchOptions(options, "format")
					}
					if err != nil {
						t.Errorf("completion suggested rejected watch format %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "watch stop options",
			bash: `COMP_WORDS=(komposyn-lg watch stop --); COMP_CWORD=3; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg watch stop --); CURRENT=4; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				assertContains(t, candidates, "--timeout")
				assertContains(t, candidates, "--data-dir")
				assertNotContains(t, candidates, "--defaults")
				assertNotContains(t, candidates, "--collector")
				assertNotContains(t, candidates, "--format")
				for _, candidate := range candidates {
					if candidate == "--help" || candidate == "-h" {
						var out, stderr bytes.Buffer
						if code := runWithInput([]string{"watch", "stop", candidate}, strings.NewReader(""), &out, &stderr); code != 0 {
							t.Errorf("help candidate %q rejected: exit=%d stderr=%q", candidate, code, stderr.String())
						}
						continue
					}
					value := t.TempDir()
					if candidate == "--timeout" {
						value = "1s"
					}
					_, options, err := parseWatchOptions([]string{candidate, value}, false)
					if err == nil {
						err = validateWatchOptions(options, "timeout")
					}
					if err != nil {
						t.Errorf("completion suggested rejected watch stop option %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "watch shorthand options",
			bash: `COMP_WORDS=(komposyn-lg watch --); COMP_CWORD=2; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg watch --); CURRENT=3; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				assertContains(t, candidates, "--data-dir")
				assertContains(t, candidates, "--interval")
				assertNotContains(t, candidates, "--defaults")
				assertNotContains(t, candidates, "--format")
				for _, candidate := range candidates {
					if candidate == "--help" || candidate == "-h" {
						continue
					}
					value := t.TempDir()
					if candidate == "--interval" {
						value = "1s"
					}
					_, options, err := parseWatchOptions([]string{candidate, value}, false)
					if err == nil {
						err = validateWatchOptions(options, "interval")
					}
					if err != nil {
						t.Errorf("completion suggested rejected watch shorthand option %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "sessions report options",
			bash: `COMP_WORDS=(komposyn-lg sessions --); COMP_CWORD=2; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg sessions --); CURRENT=3; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				assertContains(t, candidates, "--format")
				assertContains(t, candidates, "--timezone")
				assertNotContains(t, candidates, "--timeout")
				assertNotContains(t, candidates, "--interval")
				for _, candidate := range candidates {
					if candidate == "--help" || candidate == "-h" {
						continue
					}
					value := "probe"
					switch candidate {
					case "--data-dir":
						value = t.TempDir()
					case "--format":
						value = "table"
					case "--from":
						value = "2026-01-01"
					case "--to":
						value = "2026-01-02"
					case "--group-by":
						value = "model"
					case "--timezone":
						value = "UTC"
					}
					args := []string{candidate, value}
					if candidate == "--from" {
						args = append(args, "--to", "2026-01-02")
					} else if candidate == "--to" {
						args = append(args, "--from", "2026-01-01")
					}
					if _, _, _, err := parseReportArgs("sessions", args); err != nil {
						t.Errorf("completion suggested rejected sessions report option %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "session list options",
			bash: `COMP_WORDS=(komposyn-lg sessions list --); COMP_CWORD=3; _komposyn_lg; printf '%s\n' "${COMPREPLY[@]}"`,
			zsh:  `words=(komposyn-lg sessions list --); CURRENT=4; _komposyn-lg`,
			check: func(t *testing.T, candidates []string) {
				assertNotContains(t, candidates, "--session")
				assertNotContains(t, candidates, "--timezone")
				assertNotContains(t, candidates, "--group-by")
				for _, candidate := range candidates {
					if candidate == "--help" || candidate == "-h" {
						continue
					}
					value := "probe"
					switch candidate {
					case "--data-dir":
						value = t.TempDir()
					case "--format":
						value = "csv"
					case "--from":
						value = "2026-01-01"
					case "--to":
						value = "2026-01-02"
					}
					args := []string{candidate, value}
					if candidate == "--from" {
						args = append(args, "--to", "2026-01-02")
					} else if candidate == "--to" {
						args = append(args, "--from", "2026-01-01")
					}
					_, err := parseResourceOptions(args, true)
					if err != nil {
						t.Errorf("completion suggested rejected session-list option %q: %v", candidate, err)
					}
				}
			},
		},
	}

	for _, shell := range []string{"bash", "zsh"} {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is not installed", shell)
			}
			for _, test := range tests {
				test := test
				t.Run(test.name, func(t *testing.T) {
					var script string
					if shell == "bash" {
						script = bashCompletion + "\n" + test.bash
					} else {
						script = zshCompletion + "\ncompadd() { printf '%s\\n' \"$@\"; }\n" + test.zsh
					}
					output, err := exec.Command(shell, "-c", script).CombinedOutput()
					if err != nil {
						t.Fatalf("run %s completion probe: %v: %s", shell, err, output)
					}
					candidates := strings.Fields(string(output))
					candidates = withoutCandidate(candidates, "--")
					if len(candidates) == 0 {
						t.Fatalf("%s returned no candidates", shell)
					}
					test.check(t, candidates)
				})
			}
		})
	}
}

func TestPowerShellCompletionResultsMatchCommandParsers(t *testing.T) {
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		shell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell is not installed")
	}
	tests := []struct {
		name     string
		word     string
		elements []string
		check    func(*testing.T, []string)
	}{
		{
			name: "usage formats", elements: []string{"komposyn-lg", "usage", "--format"},
			check: func(t *testing.T, candidates []string) {
				if len(candidates) == 0 {
					t.Fatal("no report format candidates")
				}
				for _, candidate := range candidates {
					if _, _, _, err := parseReportArgs("usage", []string{"--format", candidate}); err != nil {
						t.Errorf("completion suggested rejected usage format %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "watch status formats", elements: []string{"komposyn-lg", "watch", "status", "--format"},
			check: func(t *testing.T, candidates []string) {
				if len(candidates) == 0 {
					t.Fatal("no watch format candidates")
				}
				for _, candidate := range candidates {
					_, options, err := parseWatchOptions([]string{"--format", candidate}, false)
					if err == nil {
						err = validateWatchOptions(options, "format")
					}
					if err != nil {
						t.Errorf("completion suggested rejected watch format %q: %v", candidate, err)
					}
				}
			},
		},
		{
			name: "watch stop options", word: "--", elements: []string{"komposyn-lg", "watch", "stop"},
			check: func(t *testing.T, candidates []string) {
				assertContains(t, candidates, "--timeout")
				assertContains(t, candidates, "--data-dir")
				assertNotContains(t, candidates, "--defaults")
				assertNotContains(t, candidates, "--collector")
				assertNotContains(t, candidates, "--format")
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			psElements := "@('" + strings.Join(test.elements, "','") + "')"
			script := `function Register-ArgumentCompleter {
  param([switch]$Native, [string[]]$CommandName, [scriptblock]$ScriptBlock)
  $global:KomposynCompleter = $ScriptBlock
}
` + powershellCompletion + fmt.Sprintf(`
$ast = [pscustomobject]@{ CommandElements = %s }
& $global:KomposynCompleter '%s' $ast 0 | ForEach-Object { $_.CompletionText }
`, psElements, test.word)
			output, err := exec.Command(shell, "-NoProfile", "-Command", script).CombinedOutput()
			if err != nil {
				t.Fatalf("run PowerShell completion probe: %v: %s", err, output)
			}
			candidates := strings.Fields(string(output))
			if len(candidates) == 0 {
				t.Fatalf("PowerShell returned no candidates: %s", output)
			}
			test.check(t, candidates)
		})
	}
}

func assertContains(t *testing.T, values []string, target string) {
	t.Helper()
	for _, value := range values {
		if value == target {
			return
		}
	}
	t.Errorf("candidates %q omit %q", values, target)
}

func assertNotContains(t *testing.T, values []string, target string) {
	t.Helper()
	for _, value := range values {
		if value == target {
			t.Errorf("candidates %q include unsupported %q", values, target)
			return
		}
	}
}

func withoutCandidate(values []string, target string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value != target {
			filtered = append(filtered, value)
		}
	}
	return filtered
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
