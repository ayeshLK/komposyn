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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/ayeshLK/komposyn/plugins/library/token-logger"
	"github.com/chzyer/readline"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

const maxInteractiveCommandBytes = 64 * 1024
const maxInteractiveCommandRunes = maxInteractiveCommandBytes / 4 // UTF-8's maximum width.

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
		settingsPath := filepath.Join(dataDir, "settings.json")
		settings, err := tokenlogger.LoadSettings(settingsPath, dataDir)
		if err != nil {
			fmt.Fprintf(stderr, "load settings: %v\n", err)
			return 2
		}
		startNow := false
		if terminalInput, ok := newTerminalLineReader(input, stdout, stderr); ok {
			defer terminalInput.Close()
			if !settings.OnboardingComplete {
				var onboardingErr error
				settings, startNow, onboardingErr = runOnboardingWithoutHistory(terminalInput, dataDir, settings)
				if onboardingErr != nil {
					return 0
				}
				if err := tokenlogger.SaveSettings(settingsPath, settings); err != nil {
					fmt.Fprintf(stderr, "save settings: %v\n", err)
					return 2
				}
			}
			return runInteractiveLoop(terminalInput, stdout, stderr, store, dataDir, settings, startNow)
		}
		scanner := bufio.NewScanner(input)
		if !settings.OnboardingComplete {
			var onboardingErr error
			settings, startNow, onboardingErr = runOnboarding(&scannerLineReader{scanner: scanner, output: stdout}, dataDir, settings)
			if onboardingErr != nil {
				return 0
			}
			if err := tokenlogger.SaveSettings(settingsPath, settings); err != nil {
				fmt.Fprintf(stderr, "save settings: %v\n", err)
				return 2
			}
		}
		return runInteractiveLoop(&scannerLineReader{scanner: scanner, output: stdout}, stdout, stderr, store, dataDir, settings, startNow)
	}
	switch args[0] {
	case "version", "--version":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: komposyn-lg version")
			return 1
		}
		fmt.Fprintf(stdout, "komposyn-lg %s\n", tokenlogger.Version)
		return 0
	case "harness":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: komposyn-lg harness")
			return 1
		}
		return runProtocol(input, stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "usage", "cost", "sessions":
		return runReport(args[0], args[1:], stdout, stderr)
	case "watch":
		return runWatch(args[1:], stdout, stderr)
	case "settings":
		return runSettings(args[1:], stdout, stderr)
	case "report-issue":
		return runReportIssue(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage: komposyn-lg {version|harness|import|usage|cost|sessions|watch|settings|report-issue} ...")
		return 1
	}
}

type interactiveState struct {
	from    time.Time
	to      time.Time
	groupBy []string
}

func runInteractive(input io.Reader, stdout, stderr io.Writer, store *tokenlogger.Store, dataDir string) int {
	settings := tokenlogger.DefaultSettings(dataDir)
	settings.OnboardingComplete = true
	return runInteractiveScanner(bufio.NewScanner(input), stdout, stderr, store, dataDir, settings, false)
}

func runInteractiveScanner(scanner *bufio.Scanner, stdout, stderr io.Writer, store *tokenlogger.Store, dataDir string, settings tokenlogger.Settings, startNow bool) int {
	return runInteractiveLoop(&scannerLineReader{scanner: scanner, output: stdout}, stdout, stderr, store, dataDir, settings, startNow)
}

type interactiveLineReader interface {
	ReadLine(prompt string) (string, error)
	DisableHistory()
	EnableHistory()
	IsTerminal() bool
}

type scannerLineReader struct {
	scanner *bufio.Scanner
	output  io.Writer
}

func (reader *scannerLineReader) ReadLine(prompt string) (string, error) {
	fmt.Fprint(reader.output, prompt)
	if !reader.scanner.Scan() {
		return "", io.EOF
	}
	return reader.scanner.Text(), nil
}

func (reader *scannerLineReader) DisableHistory()  {}
func (reader *scannerLineReader) EnableHistory()   {}
func (reader *scannerLineReader) IsTerminal() bool { return false }

type terminalLineReader struct {
	instance       *readline.Instance
	output         io.Writer
	input          *os.File
	rawState       *term.State
	setCommandMode func(bool)
}

func (reader *terminalLineReader) ReadLine(prompt string) (string, error) {
	if reader.setCommandMode != nil {
		reader.setCommandMode(prompt == "> ")
	}
	reader.instance.SetPrompt(prompt)
	return reader.instance.Readline()
}

func (reader *terminalLineReader) DisableHistory()  { reader.instance.HistoryDisable() }
func (reader *terminalLineReader) EnableHistory()   { reader.instance.HistoryEnable() }
func (reader *terminalLineReader) IsTerminal() bool { return true }
func (reader *terminalLineReader) Close() error {
	disableTerminalFeatures(reader.output)
	err := reader.instance.Close()
	if reader.rawState != nil {
		if restoreErr := term.Restore(int(reader.input.Fd()), reader.rawState); err == nil {
			err = restoreErr
		}
		reader.rawState = nil
	}
	return err
}

func newTerminalLineReader(input io.Reader, stdout, stderr io.Writer) (*terminalLineReader, bool) {
	return newTerminalLineReaderWithFactory(input, stdout, stderr, readline.NewEx)
}

func newTerminalLineReaderWithFactory(input io.Reader, stdout, stderr io.Writer, create func(*readline.Config) (*readline.Instance, error)) (*terminalLineReader, bool) {
	in, inputOK := input.(*os.File)
	out, outputOK := stdout.(*os.File)
	if !inputOK || !outputOK || !(isatty.IsTerminal(in.Fd()) || isatty.IsCygwinTerminal(in.Fd())) || !(isatty.IsTerminal(out.Fd()) || isatty.IsCygwinTerminal(out.Fd())) {
		return nil, false
	}
	preflightState, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		fmt.Fprintf(stderr, "interactive terminal raw-mode setup failed; using line input: %v\n", err)
		return nil, false
	}

	config := &readline.Config{
		Prompt:                 "> ",
		HistoryLimit:           500,
		HistoryFile:            "",
		DisableAutoSaveHistory: false,
		UniqueEditLine:         true,
		InterruptPrompt:        "^C\n",
		EOFPrompt:              "^D\n",
		ForceUseInteractive:    true,
		FuncIsTerminal:         func() bool { return true },
		// Fold Unix bracketed-paste line feeds into the current command. The
		// Windows console adapter maps plain Enter to a space and Ctrl+Enter to
		// submission, so a multiline paste cannot execute a command batch.
		FuncFilterInputRune: terminalInputRuneFilter(stdout),
	}
	setCommandMode := configureTerminalStreams(config, in, stdout, stderr)
	config.FuncMakeRaw = func() error {
		return nil
	}
	config.FuncExitRaw = func() error {
		return nil
	}
	config.SetListener(func(line []rune, pos int, key rune) ([]rune, int, bool) {
		bounded := truncateInteractiveCommand(line)
		if len(bounded) != len(line) && pos > len(bounded) {
			pos = len(bounded)
		}
		return bounded, pos, true
	})
	instance, err := create(config)
	if err != nil {
		_ = term.Restore(int(in.Fd()), preflightState)
		fmt.Fprintf(stderr, "interactive terminal setup failed; using line input: %v\n", err)
		return nil, false
	}
	enableTerminalFeatures(stdout)
	return &terminalLineReader{instance: instance, output: stdout, input: in, rawState: preflightState, setCommandMode: setCommandMode}, true
}

func terminalInputRuneFilter(stdout io.Writer) func(rune) (rune, bool) {
	return func(input rune) (rune, bool) {
		if input == readline.CharCtrlL {
			_ = clearTerminal(stdout)
			// Let readline handle Ctrl-L itself so it redraws the current line.
			// Filtering it here would make readline issue an extra KickRead;
			// depending on timing, that kick can leave the terminal reader
			// blocked in a fresh input read while the editor is being closed.
			return input, true
		}
		if input == '\n' {
			return '\r', true
		}
		return input, true
	}
}

func truncateInteractiveCommand(line []rune) []rune {
	if len(line) > maxInteractiveCommandRunes {
		return line[:maxInteractiveCommandRunes]
	}
	return line
}

type pasteGuardReader struct {
	source  io.Reader
	input   []byte
	output  []byte
	inPaste bool
}

var (
	pasteStart = []byte("\033[200~")
	pasteEnd   = []byte("\033[201~")
)

func (reader *pasteGuardReader) Read(target []byte) (int, error) {
	var readErr error
	for len(reader.output) == 0 {
		chunk := make([]byte, 4096)
		count, err := reader.source.Read(chunk)
		if count > 0 {
			reader.input = append(reader.input, chunk[:count]...)
			reader.process()
		}
		if len(reader.output) > 0 {
			break
		}
		if err != nil {
			readErr = err
			break
		}
	}
	if len(reader.output) == 0 {
		return 0, readErr
	}
	count := copy(target, reader.output)
	reader.output = reader.output[count:]
	return count, nil
}

func (reader *pasteGuardReader) Close() error { return nil }

func (reader *pasteGuardReader) process() {
	for len(reader.input) > 0 {
		if reader.inPaste {
			if end := bytes.Index(reader.input, pasteEnd); end >= 0 {
				reader.output = append(reader.output, normalizePaste(reader.input[:end])...)
				reader.input = reader.input[end+len(pasteEnd):]
				reader.inPaste = false
				continue
			}
			keep := suffixPrefixLength(reader.input, pasteEnd)
			if len(reader.input) > keep {
				reader.output = append(reader.output, normalizePaste(reader.input[:len(reader.input)-keep])...)
				reader.input = reader.input[len(reader.input)-keep:]
			}
			return
		}
		if start := bytes.Index(reader.input, pasteStart); start >= 0 {
			reader.output = append(reader.output, reader.input[:start]...)
			reader.input = reader.input[start+len(pasteStart):]
			reader.inPaste = true
			continue
		}
		keep := suffixPrefixLength(reader.input, pasteStart)
		if len(reader.input) > keep {
			reader.output = append(reader.output, reader.input[:len(reader.input)-keep]...)
			reader.input = reader.input[len(reader.input)-keep:]
		}
		return
	}
}

func suffixPrefixLength(input, prefix []byte) int {
	maximum := len(input)
	if maximum >= len(prefix) {
		maximum = len(prefix) - 1
	}
	for length := maximum; length > 0; length-- {
		if bytes.Equal(input[len(input)-length:], prefix[:length]) {
			return length
		}
	}
	return 0
}

func normalizePaste(input []byte) []byte {
	output := make([]byte, 0, len(input))
	for index := 0; index < len(input); index++ {
		if input[index] == '\r' || input[index] == '\n' {
			if input[index] == '\r' && index+1 < len(input) && input[index+1] == '\n' {
				index++
			}
			output = append(output, ' ')
			continue
		}
		output = append(output, input[index])
	}
	return output
}

func runInteractiveLoop(reader interactiveLineReader, stdout, stderr io.Writer, store *tokenlogger.Store, dataDir string, settings tokenlogger.Settings, startNow bool) int {
	now := time.Now().UTC()
	state := interactiveState{from: now.AddDate(0, 0, -7), to: now, groupBy: []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}}
	collectorContext, stopCollector := context.WithCancel(context.Background())
	defer stopCollector()
	if startNow || (settings.CollectorEnabled && settings.AutoStart) {
		collector, err := tokenlogger.NewCollector(store, defaultCollectorRoots(), time.Duration(settings.PollIntervalSeconds)*time.Second)
		if err != nil {
			fmt.Fprintf(stderr, "collector setup failed: %v\n", err)
		} else {
			go func() { _, _ = collector.PollOnce(); _ = collector.Run(collectorContext) }()
			fmt.Fprintln(stdout, "local collector running for this session")
		}
	}
	fmt.Fprintf(stdout, "komposyn-lg %s interactive\n", tokenlogger.Version)
	fmt.Fprintln(stdout, "Type /help for commands; /quit to exit.")
	if hint := interactiveInputHint(); hint != "" && reader.IsTerminal() {
		fmt.Fprintln(stdout, hint)
	}
	if pending, err := store.PendingDiagnostics(); err == nil && len(pending) > 0 {
		fmt.Fprintf(stdout, "There are %d pending local diagnostic(s).\n", len(pending))
		for _, diagnostic := range pending {
			fmt.Fprintf(stdout, "Pending diagnostic %s (%d occurrence(s)).\n", diagnostic.Fingerprint, diagnostic.OccurrenceCount)
			if offerDiagnostic(reader, store, diagnostic) != nil {
				return 0
			}
		}
	}
	if interactiveRender(stdout, stderr, store, state, reader) {
		return 0
	}
	for {
		line, err := reader.ReadLine("> ")
		if errors.Is(err, readline.ErrInterrupt) || errors.Is(err, io.EOF) {
			return 0
		}
		if err != nil {
			fmt.Fprintf(stderr, "read command failed: %v\n", err)
			return 2
		}
		line = strings.TrimSpace(line)
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
		case "/clear":
			if terminalReader, ok := reader.(*terminalLineReader); ok {
				if err := clearTerminal(terminalReader.instance.Stdout()); err != nil {
					fmt.Fprintf(stderr, "clear terminal failed: %v\n", err)
				}
			} else {
				fmt.Fprintln(stdout, "clear is available only in an interactive terminal")
			}
		case "/projects":
			state.groupBy = []string{tokenlogger.GroupProject}
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/sessions":
			state.groupBy = []string{tokenlogger.GroupSession}
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/total-usage":
			state.groupBy = []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/by-client":
			state.groupBy = []string{tokenlogger.GroupClient}
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/by-model":
			state.groupBy = []string{tokenlogger.GroupModel}
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/cost":
			state.groupBy = []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/refresh":
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
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
			if interactiveRender(stdout, stderr, store, state, reader) {
				return 0
			}
		case "/settings":
			fmt.Fprintf(stdout, "data directory: %s\n", dataDir)
			fmt.Fprintf(stdout, "collector enabled: %t\n", settings.CollectorEnabled)
			fmt.Fprintf(stdout, "collector auto-start: %t\n", settings.AutoStart)
			fmt.Fprintf(stdout, "collector poll interval: %ds\n", settings.PollIntervalSeconds)
			fmt.Fprintln(stdout, "diagnostic reporting: ask before any external action")
			fmt.Fprintf(stdout, "active range: %s to %s (UTC)\n", state.from.Format(time.RFC3339), state.to.Format(time.RFC3339))
		case "/report-issue":
			printPendingDiagnostics(stdout, store)
		default:
			fmt.Fprintf(stdout, "unknown command %q; use /help\n", command)
		}
	}
}

func renderInteractive(stdout, stderr io.Writer, store *tokenlogger.Store, state interactiveState, reader interactiveLineReader) error {
	report, err := store.Query(tokenlogger.UsageFilter{From: state.from, To: state.to, GroupBy: state.groupBy})
	if err != nil {
		fmt.Fprintf(stderr, "usage query failed: %v\n", err)
		if diagnostic, diagnosticErr := store.RecordDiagnostic(err.Error()); diagnosticErr == nil {
			fmt.Fprintf(stdout, "Sanitized diagnostic saved locally: %s\n", diagnostic.Fingerprint)
			return offerDiagnostic(reader, store, diagnostic)
		}
		return nil
	}
	fmt.Fprintf(stdout, "range: %s to %s (UTC)\n", state.from.Format(time.RFC3339), state.to.Format(time.RFC3339))
	if err := tokenlogger.WriteTable(stdout, report); err != nil {
		fmt.Fprintf(stderr, "render report failed: %v\n", err)
	}
	return nil
}

func interactiveRender(stdout, stderr io.Writer, store *tokenlogger.Store, state interactiveState, reader interactiveLineReader) bool {
	return renderInteractive(stdout, stderr, store, state, reader) != nil
}

func offerDiagnostic(reader interactiveLineReader, store *tokenlogger.Store, diagnostic tokenlogger.Diagnostic) error {
	reader.DisableHistory()
	defer reader.EnableHistory()
	choiceLine, err := reader.ReadLine("Send sanitized diagnostic report? [y/N/view] ")
	if err != nil {
		if reader.IsTerminal() {
			return err
		}
		fmt.Fprintln(readerOutput(reader), "skipped")
		_ = store.MarkDiagnosticReviewed(diagnostic.Fingerprint, "skipped")
		return nil
	}
	choice := strings.ToLower(strings.TrimSpace(choiceLine))
	if choice == "view" {
		output := readerOutput(reader)
		fmt.Fprintf(output, "fingerprint: %s\nmessage: %s\n", diagnostic.Fingerprint, diagnostic.Message)
		answer, answerErr := reader.ReadLine("Open a prefilled issue draft? [y/N] ")
		if answerErr != nil {
			if reader.IsTerminal() {
				return answerErr
			}
		}
		if answerErr == nil && strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintf(output, "draft (not sent): %s\n", tokenlogger.GitHubIssueDraftURL(diagnostic))
			_ = store.MarkDiagnosticReviewed(diagnostic.Fingerprint, "reviewed")
		} else {
			fmt.Fprintln(output, "skipped")
			_ = store.MarkDiagnosticReviewed(diagnostic.Fingerprint, "skipped")
		}
		return nil
	}
	if choice == "y" {
		fmt.Fprintf(readerOutput(reader), "draft (not sent): %s\n", tokenlogger.GitHubIssueDraftURL(diagnostic))
		_ = store.MarkDiagnosticReviewed(diagnostic.Fingerprint, "reviewed")
		return nil
	}
	fmt.Fprintln(readerOutput(reader), "skipped")
	_ = store.MarkDiagnosticReviewed(diagnostic.Fingerprint, "skipped")
	return nil
}

func readerOutput(reader interactiveLineReader) io.Writer {
	if scanner, ok := reader.(*scannerLineReader); ok {
		return scanner.output
	}
	if terminal, ok := reader.(*terminalLineReader); ok {
		return terminal.output
	}
	return io.Discard
}

func printInteractiveHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "/projects /sessions /total-usage /by-client /by-model /cost")
	fmt.Fprintln(stdout, "/range [from to] /refresh /clear /settings /report-issue /help /quit")
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

type diagnosticReport struct {
	FormatVersion int                      `json:"format_version"`
	CreatedAt     time.Time                `json:"created_at"`
	Diagnostics   []tokenlogger.Diagnostic `json:"diagnostics"`
}

func runReportIssue(args []string, stdout, stderr io.Writer) int {
	dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	var outputPath string
	for index := 0; index < len(args); index++ {
		if index+1 >= len(args) {
			fmt.Fprintln(stderr, "usage: komposyn-lg report-issue --output <path> [--data-dir <path>]")
			return 1
		}
		value := args[index+1]
		switch args[index] {
		case "--output":
			outputPath = value
		case "--data-dir":
			dataDir = value
		default:
			fmt.Fprintf(stderr, "unknown report-issue option %q\n", args[index])
			return 1
		}
		index++
	}
	if strings.TrimSpace(outputPath) == "" {
		fmt.Fprintln(stderr, "report-issue requires --output <path>")
		return 1
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		fmt.Fprintf(stderr, "open token logger database: %v\n", err)
		return 2
	}
	defer store.Close()
	diagnostics, err := store.PendingDiagnostics()
	if err != nil {
		fmt.Fprintf(stderr, "load diagnostics: %v\n", err)
		return 2
	}
	report := diagnosticReport{FormatVersion: 1, CreatedAt: time.Now().UTC(), Diagnostics: diagnostics}
	if err := writeDiagnosticReport(outputPath, report); err != nil {
		fmt.Fprintf(stderr, "write diagnostic report: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "wrote %d pending diagnostic(s) to %s\n", len(diagnostics), outputPath)
	return 0
}

func writeDiagnosticReport(outputPath string, report diagnosticReport) error {
	outputPath = filepath.Clean(outputPath)
	archiveFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(outputPath)
		}
	}()
	archive := zip.NewWriter(archiveFile)
	entry, err := archive.Create("diagnostics.json")
	if err == nil {
		encoder := json.NewEncoder(entry)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(report)
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := archiveFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	committed = true
	return nil
}

func runOnboarding(reader interactiveLineReader, dataDir string, settings tokenlogger.Settings) (tokenlogger.Settings, bool, error) {
	fmt.Fprintln(readerOutput(reader), "First-run setup")
	startNow, err := askYesNo(reader, "Start the local collector now?", true)
	if err != nil {
		return settings, false, err
	}
	settings.CollectorEnabled = startNow
	settings.AutoStart, err = askYesNo(reader, "Start the collector automatically when I sign in?", false)
	if err != nil {
		return settings, false, err
	}
	settings.OnboardingComplete = true
	settings.DataDir = dataDir
	return settings, startNow, nil
}

func runOnboardingWithoutHistory(reader interactiveLineReader, dataDir string, settings tokenlogger.Settings) (tokenlogger.Settings, bool, error) {
	reader.DisableHistory()
	defer reader.EnableHistory()
	return runOnboarding(reader, dataDir, settings)
}

func askYesNo(reader interactiveLineReader, question string, defaultYes bool) (bool, error) {
	defaultLabel := "y/N"
	if defaultYes {
		defaultLabel = "Y/n"
	}
	answerLine, err := reader.ReadLine(fmt.Sprintf("%s [%s] ", question, defaultLabel))
	if err != nil {
		if reader.IsTerminal() {
			return false, err
		}
		return defaultYes, nil
	}
	answer := strings.ToLower(strings.TrimSpace(answerLine))
	if answer == "" {
		return defaultYes, nil
	}
	return answer == "y" || answer == "yes", nil
}

func defaultCollectorRoots() []string {
	roots := []string{}
	if configured := os.Getenv("TOKEN_LOGGER_CODEX_DIR"); configured != "" {
		roots = append(roots, configured)
	}
	if configured := os.Getenv("TOKEN_LOGGER_CLAUDE_DIR"); configured != "" {
		roots = append(roots, configured)
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".codex", "sessions"), filepath.Join(home, ".claude", "projects"))
	}
	return roots
}

func runWatch(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: komposyn-lg watch <file-or-directory> [--interval <duration>] [--data-dir <path>]")
		return 1
	}
	dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	interval := 5 * time.Second
	root := args[0]
	for index := 1; index < len(args); index++ {
		if index+1 >= len(args) {
			fmt.Fprintln(stderr, "missing watch option value")
			return 1
		}
		switch args[index] {
		case "--data-dir":
			dataDir = args[index+1]
		case "--interval":
			parsed, err := time.ParseDuration(args[index+1])
			if err != nil || parsed <= 0 {
				fmt.Fprintln(stderr, "interval must be a positive duration")
				return 1
			}
			interval = parsed
		default:
			fmt.Fprintf(stderr, "unknown watch option %q\n", args[index])
			return 1
		}
		index++
	}
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		fmt.Fprintf(stderr, "open token logger database: %v\n", err)
		return 2
	}
	defer store.Close()
	collector, err := tokenlogger.NewCollector(store, []string{root}, interval)
	if err != nil {
		fmt.Fprintf(stderr, "create collector: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "watching %s every %s; press Ctrl-C to stop\n", root, interval)
	if result, pollErr := collector.PollOnce(); pollErr != nil {
		fmt.Fprintf(stderr, "initial collector poll: %v\n", pollErr)
	} else {
		writeWatchPoll(stdout, result)
	}
	if err := collector.RunWithResults(ctx, func(result tokenlogger.ImportResult, pollErr error) {
		if pollErr != nil {
			fmt.Fprintf(stderr, "collector poll: %v\n", pollErr)
			return
		}
		writeWatchPoll(stdout, result)
	}); err != nil {
		fmt.Fprintf(stderr, "collector stopped: %v\n", err)
		return 2
	}
	health := collector.Health()
	writeWatchSummary(stdout, health)
	return 0
}

func writeWatchSummary(output io.Writer, health tokenlogger.CollectorHealth) {
	fmt.Fprintf(output, "collector stopped: status=%s sources=%d imported=%d duplicates=%d ignored_non_usage=%d unchanged_snapshot=%d deferred_incomplete_tail=%d unsupported_usage=%d malformed_record=%d failed_source=%d pending=%t complete=%t\n",
		health.Status, health.Sources, health.Imported, health.Duplicates,
		health.Outcomes.IgnoredNonUsage, health.Outcomes.UnchangedSnapshot,
		health.Outcomes.DeferredIncompleteTail, health.Outcomes.UnsupportedUsage,
		health.Outcomes.MalformedRecord, health.Outcomes.FailedSource,
		health.Pending, !health.Outcomes.Incomplete() && !health.Pending)
}

func writeWatchPoll(output io.Writer, result tokenlogger.ImportResult) {
	fmt.Fprintf(output, "poll imported=%d duplicates=%d ignored_non_usage=%d unchanged_snapshot=%d deferred_incomplete_tail=%d unsupported_usage=%d malformed_record=%d failed_source=%d pending=%t complete=%t\n",
		result.Receipt.Imported, result.Receipt.Duplicates, result.Outcomes.IgnoredNonUsage,
		result.Outcomes.UnchangedSnapshot, result.Outcomes.DeferredIncompleteTail,
		result.Outcomes.UnsupportedUsage, result.Outcomes.MalformedRecord,
		result.Outcomes.FailedSource, result.Outcomes.Pending(), !result.Outcomes.Incomplete() && !result.Outcomes.Pending())
}

func runSettings(args []string, stdout, stderr io.Writer) int {
	dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	var collectorValue, autoStartValue string
	for index := 0; index < len(args); index++ {
		if index+1 >= len(args) {
			fmt.Fprintln(stderr, "usage: komposyn-lg settings [--data-dir <path>] [--collector on|off] [--auto-start on|off]")
			return 1
		}
		switch args[index] {
		case "--data-dir":
			dataDir = args[index+1]
		case "--collector":
			collectorValue = args[index+1]
		case "--auto-start":
			autoStartValue = args[index+1]
		default:
			fmt.Fprintf(stderr, "unknown settings option %q\n", args[index])
			return 1
		}
		index++
	}
	path := filepath.Join(dataDir, "settings.json")
	settings, err := tokenlogger.LoadSettings(path, dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "load settings: %v\n", err)
		return 2
	}
	if collectorValue != "" {
		value, ok := settingBool(collectorValue)
		if !ok {
			fmt.Fprintln(stderr, "--collector expects on or off")
			return 1
		}
		settings.CollectorEnabled = value
	}
	if autoStartValue != "" {
		value, ok := settingBool(autoStartValue)
		if !ok {
			fmt.Fprintln(stderr, "--auto-start expects on or off")
			return 1
		}
		settings.AutoStart = value
	}
	settings.OnboardingComplete = true
	if err := tokenlogger.SaveSettings(path, settings); err != nil {
		fmt.Fprintf(stderr, "save settings: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "data_dir=%s collector=%t auto_start=%t interval=%ds\n", dataDir, settings.CollectorEnabled, settings.AutoStart, settings.PollIntervalSeconds)
	return 0
}

func settingBool(value string) (bool, bool) {
	switch strings.ToLower(value) {
	case "on", "true", "yes", "y":
		return true, true
	case "off", "false", "no", "n":
		return false, true
	default:
		return false, false
	}
}

func runImport(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: komposyn-lg import <file-or-directory> [--data-dir <path>] [--format text|json]")
		return 1
	}
	dataDir := os.Getenv("TOKEN_LOGGER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	path := args[0]
	format := "text"
	for index := 1; index < len(args); index++ {
		if index+1 >= len(args) {
			fmt.Fprintln(stderr, "usage: komposyn-lg import <file-or-directory> [--data-dir <path>] [--format text|json]")
			return 1
		}
		switch args[index] {
		case "--data-dir":
			dataDir = args[index+1]
		case "--format":
			format = args[index+1]
			if format != "text" && format != "json" {
				fmt.Fprintln(stderr, "--format expects text or json")
				return 1
			}
		default:
			fmt.Fprintf(stderr, "unknown import option %q\n", args[index])
			return 1
		}
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
	processed := 0
	for _, sourcePath := range paths {
		adapter, detectErr := tokenlogger.DetectAdapter(sourcePath)
		if detectErr != nil {
			fmt.Fprintf(stderr, "detect source: %v\n", detectErr)
			total.Outcomes.Add(tokenlogger.OutcomeFailedSource)
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
			fmt.Fprintf(stderr, "import %s source: %v\n", adapter, importErr)
			total.Outcomes.Add(tokenlogger.OutcomeFailedSource)
			continue
		}
		processed++
		total.Merge(result)
	}
	output := importCommandOutput{
		FormatVersion: 1, SourcesDiscovered: len(paths), SourcesProcessed: processed,
		ImportedEvents: total.Receipt.Imported, DuplicateEvents: total.Receipt.Duplicates,
		Outcomes: total.Outcomes, FailedSources: total.Outcomes.FailedSource,
		Complete: !total.Outcomes.Incomplete() && !total.Outcomes.Pending(), Pending: total.Outcomes.Pending(),
	}
	if format == "json" {
		if err := json.NewEncoder(stdout).Encode(output); err != nil {
			fmt.Fprintf(stderr, "write import result: %v\n", err)
			return 2
		}
	} else {
		writeImportText(stdout, output)
	}
	if total.Outcomes.Incomplete() {
		return 3
	}
	return 0
}

type importCommandOutput struct {
	FormatVersion     int                       `json:"format_version"`
	SourcesDiscovered int                       `json:"sources_discovered"`
	SourcesProcessed  int                       `json:"sources_processed"`
	ImportedEvents    int                       `json:"imported_events"`
	DuplicateEvents   int                       `json:"duplicate_events"`
	Outcomes          tokenlogger.OutcomeCounts `json:"outcomes"`
	FailedSources     int                       `json:"failed_sources"`
	Complete          bool                      `json:"complete"`
	Pending           bool                      `json:"pending"`
}

func writeImportText(output io.Writer, result importCommandOutput) {
	fmt.Fprintf(output, "Imported events: %d; storage duplicates: %d; sources processed: %d/%d.\n",
		result.ImportedEvents, result.DuplicateEvents, result.SourcesProcessed, result.SourcesDiscovered,
	)
	fmt.Fprintf(output, "Record outcomes: ignored_non_usage=%d unchanged_snapshot=%d deferred_incomplete_tail=%d unsupported_usage=%d malformed_record=%d failed_source=%d.\n",
		result.Outcomes.IgnoredNonUsage, result.Outcomes.UnchangedSnapshot,
		result.Outcomes.DeferredIncompleteTail, result.Outcomes.UnsupportedUsage,
		result.Outcomes.MalformedRecord, result.Outcomes.FailedSource)
	if result.Pending {
		fmt.Fprintln(output, "Pending: the incomplete final record will be retried after the source grows.")
	}
	if result.Outcomes.Incomplete() {
		fmt.Fprintln(output, "Warning: usage accounting may be incomplete; review the warning outcomes and local diagnostics.")
	}
	status := "incomplete"
	if result.Complete {
		status = "complete"
	} else if result.Pending && !result.Outcomes.Incomplete() {
		status = "pending"
	}
	fmt.Fprintf(output, "Status: %s.\n", status)
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
		case "--timezone":
			location, err := time.LoadLocation(value)
			if err != nil {
				return filter, format, dataDir, fmt.Errorf("invalid --timezone %q: %w", value, err)
			}
			filter.Timezone = location
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
