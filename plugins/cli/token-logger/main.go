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
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithInput(args, os.Stdin, stdout, stderr)
}

func runWithInput(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
		if dataDir == "" {
			dataDir = defaultDataDir()
		}
		store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
		if err != nil {
			fmt.Fprintf(stderr, "open token logger database: %v\n", err)
			return 2
		}
		defer store.Close()
		return runInteractive(input, stdout, stderr, store, dataDir)
	}
	switch args[0] {
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "usage", "cost", "sessions":
		return runReport(args[0], args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage: komposyn-lg {import|usage|cost|sessions} ...")
		return 1
	}
}

type interactiveState struct {
	from    time.Time
	to      time.Time
	groupBy []string
}

func runInteractive(input io.Reader, stdout, stderr io.Writer, store *tokenlogger.Store, dataDir string) int {
	now := time.Now().UTC()
	state := interactiveState{from: now.AddDate(0, 0, -7), to: now, groupBy: []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}}
	fmt.Fprintf(stdout, "komposyn-lg %s interactive\n", tokenlogger.Version)
	fmt.Fprintln(stdout, "Type /help for commands; /quit to exit.")
	if pending, err := store.PendingDiagnostics(); err == nil && len(pending) > 0 {
		fmt.Fprintf(stdout, "There are %d pending local diagnostic(s). Use /settings to review them.\n", len(pending))
	}
	scanner := bufio.NewScanner(input)
	renderInteractive(stdout, stderr, store, state, scanner)
	for {
		fmt.Fprint(stdout, "> ")
		if !scanner.Scan() {
			return 0
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		command := fields[0]
		switch command {
		case "/quit", "/exit":
			return 0
		case "/help":
			printInteractiveHelp(stdout)
		case "/projects":
			state.groupBy = []string{tokenlogger.GroupProject}
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/sessions":
			state.groupBy = []string{tokenlogger.GroupSession}
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/total-usage":
			state.groupBy = []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/by-client":
			state.groupBy = []string{tokenlogger.GroupClient}
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/by-model":
			state.groupBy = []string{tokenlogger.GroupModel}
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/cost":
			state.groupBy = []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/refresh":
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/range":
			if len(fields) == 1 {
				fmt.Fprintf(stdout, "active range: %s to %s (UTC)\n", state.from.Format(time.RFC3339), state.to.Format(time.RFC3339))
				continue
			}
			if len(fields) != 3 {
				fmt.Fprintln(stdout, "usage: /range <from> <to>")
				continue
			}
			from, fromErr := parseTime(fields[1])
			to, toErr := parseTime(fields[2])
			if fromErr != nil || toErr != nil || !to.After(from) {
				fmt.Fprintln(stdout, "range must be two increasing ISO-8601 timestamps or dates")
				continue
			}
			state.from, state.to = from, to
			renderInteractive(stdout, stderr, store, state, scanner)
		case "/settings":
			fmt.Fprintf(stdout, "data directory: %s\n", dataDir)
			fmt.Fprintln(stdout, "diagnostic reporting: ask before any external action")
			fmt.Fprintf(stdout, "active range: %s to %s (UTC)\n", state.from.Format(time.RFC3339), state.to.Format(time.RFC3339))
		case "/report-issue":
			printPendingDiagnostics(stdout, store)
		default:
			fmt.Fprintf(stdout, "unknown command %q; use /help\n", command)
		}
	}
}

func renderInteractive(stdout, stderr io.Writer, store *tokenlogger.Store, state interactiveState, scanner *bufio.Scanner) {
	report, err := store.Query(tokenlogger.UsageFilter{From: state.from, To: state.to, GroupBy: state.groupBy})
	if err != nil {
		fmt.Fprintf(stderr, "usage query failed: %v\n", err)
		if diagnostic, diagnosticErr := store.RecordDiagnostic(err.Error()); diagnosticErr == nil {
			fmt.Fprintf(stdout, "Sanitized diagnostic saved locally: %s\n", diagnostic.Fingerprint)
			offerDiagnostic(stdout, scanner, diagnostic)
		}
		return
	}
	fmt.Fprintf(stdout, "range: %s to %s (UTC)\n", state.from.Format(time.RFC3339), state.to.Format(time.RFC3339))
	if err := tokenlogger.WriteTable(stdout, report); err != nil {
		fmt.Fprintf(stderr, "render report failed: %v\n", err)
	}
}

func offerDiagnostic(stdout io.Writer, scanner *bufio.Scanner, diagnostic tokenlogger.Diagnostic) {
	fmt.Fprint(stdout, "Send sanitized diagnostic report? [y/N/view] ")
	if scanner == nil || !scanner.Scan() {
		fmt.Fprintln(stdout, "skipped")
		return
	}
	choice := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if choice == "view" {
		fmt.Fprintf(stdout, "fingerprint: %s\nmessage: %s\n", diagnostic.Fingerprint, diagnostic.Message)
		fmt.Fprint(stdout, "Open a prefilled issue draft? [y/N] ")
		if scanner.Scan() && strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
			fmt.Fprintf(stdout, "draft (not sent): %s\n", tokenlogger.GitHubIssueDraftURL(diagnostic))
		} else {
			fmt.Fprintln(stdout, "skipped")
		}
		return
	}
	if choice == "y" {
		fmt.Fprintf(stdout, "draft (not sent): %s\n", tokenlogger.GitHubIssueDraftURL(diagnostic))
		return
	}
	fmt.Fprintln(stdout, "skipped")
}

func printInteractiveHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "/projects /sessions /total-usage /by-client /by-model /cost")
	fmt.Fprintln(stdout, "/range [from to] /refresh /settings /report-issue /help /quit")
}

func printPendingDiagnostics(stdout io.Writer, store *tokenlogger.Store) {
	diagnostics, err := store.PendingDiagnostics()
	if err != nil {
		fmt.Fprintf(stdout, "could not load diagnostics: %v\n", err)
		return
	}
	if len(diagnostics) == 0 {
		fmt.Fprintln(stdout, "no pending diagnostics")
		return
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(stdout, "%s (%d occurrence(s)): %s\n", diagnostic.Fingerprint, diagnostic.OccurrenceCount, diagnostic.Message)
	}
}

func runImport(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: komposyn-lg import <file-or-directory> [--data-dir <path>]")
		return 1
	}
	dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	path := args[0]
	for index := 1; index < len(args); index++ {
		if args[index] != "--data-dir" || index+1 >= len(args) {
			fmt.Fprintln(stderr, "usage: komposyn-lg import <file-or-directory> [--data-dir <path>]")
			return 1
		}
		dataDir = args[index+1]
		index++
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		fmt.Fprintf(stderr, "open token logger database: %v\n", err)
		return 2
	}
	defer store.Close()
	paths, err := codexPaths(path)
	if err != nil {
		fmt.Fprintf(stderr, "find Codex sources: %v\n", err)
		return 2
	}
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "no Codex JSONL sources found")
		return 2
	}
	total := tokenlogger.ImportResult{}
	for _, sourcePath := range paths {
		adapter, detectErr := tokenlogger.DetectAdapter(sourcePath)
		if detectErr != nil {
			fmt.Fprintf(stderr, "%s: detect source: %v\n", sourcePath, detectErr)
			total.Failed++
			continue
		}
		var result tokenlogger.ImportResult
		var importErr error
		if adapter == "claude" {
			result, importErr = (tokenlogger.ClaudeAdapter{}).Import(sourcePath, store)
		} else {
			result, importErr = (tokenlogger.CodexAdapter{}).Import(sourcePath, store)
		}
		if importErr != nil {
			fmt.Fprintf(stderr, "%s: %v\n", sourcePath, importErr)
			total.Failed++
			continue
		}
		total.Receipt.Imported += result.Receipt.Imported
		total.Receipt.Duplicates += result.Receipt.Duplicates
		total.Skipped += result.Skipped
	}
	fmt.Fprintf(stdout, "imported=%d duplicates=%d skipped=%d failed=%d\n", total.Receipt.Imported, total.Receipt.Duplicates, total.Skipped, total.Failed)
	if total.Failed > 0 {
		return 3
	}
	return 0
}

func runReport(command string, args []string, stdout, stderr io.Writer) int {
	filter, format, dataDir, err := parseReportArgs(command, args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		fmt.Fprintf(stderr, "open token logger database: %v\n", err)
		return 2
	}
	defer store.Close()
	report, err := store.Query(filter)
	if err != nil {
		fmt.Fprintf(stderr, "query usage: %v\n", err)
		return 2
	}
	switch format {
	case "json":
		err = tokenlogger.WriteJSON(stdout, report)
	case "csv":
		err = tokenlogger.WriteCSV(stdout, report)
	default:
		err = tokenlogger.WriteTable(stdout, report)
	}
	if err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 2
	}
	return 0
}

func parseReportArgs(command string, args []string) (tokenlogger.UsageFilter, string, string, error) {
	now := time.Now().UTC()
	filter := tokenlogger.UsageFilter{From: now.AddDate(0, 0, -7), To: now, GroupBy: []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}}
	if command == "sessions" {
		filter.GroupBy = []string{tokenlogger.GroupSession}
	}
	format := "table"
	dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	for index := 0; index < len(args); index++ {
		if index+1 >= len(args) {
			return filter, format, dataDir, fmt.Errorf("missing value for %s", args[index])
		}
		value := args[index+1]
		switch args[index] {
		case "--data-dir":
			dataDir = value
		case "--format":
			if value != "table" && value != "json" && value != "csv" {
				return filter, format, dataDir, fmt.Errorf("unsupported format %q", value)
			}
			format = value
		case "--group-by":
			filter.GroupBy = nil
			for _, group := range strings.Split(value, ",") {
				if group != "" {
					filter.GroupBy = append(filter.GroupBy, group)
				}
			}
		case "--project":
			filter.ProjectID = value
		case "--session":
			filter.SessionID = value
		case "--client":
			filter.Client = value
		case "--provider":
			filter.Provider = value
		case "--model":
			filter.Model = value
		case "--from":
			parsed, err := parseTime(value)
			if err != nil {
				return filter, format, dataDir, fmt.Errorf("invalid --from: %w", err)
			}
			filter.From = parsed
		case "--to":
			parsed, err := parseTime(value)
			if err != nil {
				return filter, format, dataDir, fmt.Errorf("invalid --to: %w", err)
			}
			filter.To = parsed
		default:
			return filter, format, dataDir, fmt.Errorf("unknown option %q", args[index])
		}
		index++
	}
	return filter, format, dataDir, nil
}

func parseTime(value string) (time.Time, error) {
	if len(value) == len("2006-01-02") {
		parsed, err := time.Parse("2006-01-02", value)
		return parsed.UTC(), err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func codexPaths(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var paths []string
	err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && (filepath.Ext(candidate) == ".jsonl" || filepath.Ext(candidate) == ".json") {
			paths = append(paths, candidate)
		}
		return nil
	})
	return paths, err
}

func defaultDataDir() string {
	if configured := os.Getenv("XDG_DATA_HOME"); configured != "" {
		return filepath.Join(configured, "token-logger")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "token-logger")
	}
	return filepath.Join(".", ".token-logger")
}
