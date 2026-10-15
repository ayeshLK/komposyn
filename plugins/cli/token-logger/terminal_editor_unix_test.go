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

//go:build !windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/plugins/library/token-logger"
	"github.com/chzyer/readline"
	"github.com/creack/pty/v2"
	"golang.org/x/term"
)

func TestTerminalLineReaderUsesCursorEditingAndSessionHistory(t *testing.T) {
	if os.Getenv("KOMPOSYN_PTY_HELPER") == "1" {
		if mode := os.Getenv("KOMPOSYN_PTY_EXIT_MODE"); mode != "" {
			terminalEditorPTYExitHelper(t, mode)
			return
		}
		terminalEditorPTYHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=TestTerminalLineReaderUsesCursorEditingAndSessionHistory", "--")
	command.Env = append(os.Environ(), "KOMPOSYN_PTY_HELPER=1")
	master, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	setPTYSize(t, master)
	output := newPTYOutputReader(master)
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	output.waitForText(t, "> ")
	if _, err := master.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "> partial")
	if _, err := master.Write([]byte("\x0c")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "\033[2J\033[H")
	// Ctrl-L must clear the visible screen and redraw the active prompt and
	// partially typed line, not merely emit the clear sequence.
	output.waitForText(t, "> partial")
	if _, err := master.Write([]byte("\x01\x0b")); err != nil {
		t.Fatal(err)
	}

	// Exercise cursor insertion, Right, Delete in the middle, Home/Right, and
	// Unicode through the real editor before command dispatch.
	if _, err := master.Write([]byte("abcz")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "> abcz")
	if _, err := master.Write([]byte("\x7f")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "> abc")
	if _, err := master.Write([]byte("\x1b[D\x1b[D界\x1b[C\x1b[3~\x01\x1b[Cx\x05\r")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "RESULT:ax界b\n")
	if _, err := master.Write([]byte("\x1b[A\r")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "RESULT:ax界b\n")

	// Moving away from history and back restores the partially typed draft.
	if _, err := master.Write([]byte("draft\x1b[A\x1b[B\r")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "RESULT:draft\n")

	// Up stops at the oldest item; Down returns through the newest item to
	// the draft typed before history navigation.
	if _, err := master.Write([]byte("new\x1b[A\x1b[A\x1b[A\x1b[B\x1b[B\r")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "RESULT:new\n")

	output.waitForCommand(t, command)
}

func TestTerminalLineReaderWritesTableRowsStartingAtColumnZero(t *testing.T) {
	if os.Getenv("KOMPOSYN_PTY_TABLE_HELPER") == "1" {
		terminalEditorPTYTableHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=TestTerminalLineReaderWritesTableRowsStartingAtColumnZero", "--")
	command.Env = append(os.Environ(), "KOMPOSYN_PTY_TABLE_HELPER=1")
	master, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	setPTYSize(t, master)
	output := newPTYOutputReader(master)
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	output.waitForText(t, "> ")
	if _, err := master.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}

	// term.MakeRaw disables OPOST on the controlling tty for the session, so
	// a bare "\n" between table rows would advance a row without returning
	// to column zero (a "staircase"). The fix must insert a carriage return
	// so each row boundary in the captured raw output is "\r\n".
	output.waitForText(t, "CLIENT")
	output.waitForText(t, "claude-code")
	output.waitForText(t, "\r\n")
	output.waitForText(t, "codex-cli")
	output.waitForCommand(t, command)
}

func TestTerminalLineReaderWritesStderrStartingAtColumnZero(t *testing.T) {
	if os.Getenv("KOMPOSYN_PTY_STDERR_HELPER") == "1" {
		terminalEditorPTYStderrHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=TestTerminalLineReaderWritesStderrStartingAtColumnZero", "--")
	command.Env = append(os.Environ(), "KOMPOSYN_PTY_STDERR_HELPER=1")
	master, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	setPTYSize(t, master)
	output := newPTYOutputReader(master)
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	output.waitForText(t, "> ")
	if _, err := master.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}

	// stdin, stdout, and stderr share the controlling tty in the usual
	// interactive case, so term.MakeRaw's OPOST-disabling also affects
	// application errors written to stderr: they must return to column zero
	// between lines too, not just stdout.
	output.waitForText(t, "ERRLINE1")
	output.waitForText(t, "\r\n")
	output.waitForText(t, "ERRLINE2")
	output.waitForCommand(t, command)
}

func TestConfigureTerminalStreamsLeavesRedirectedStderrUnchanged(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()

	redirected, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer redirected.Close()

	config := &readline.Config{}
	_, errOutput, _ := configureTerminalStreams(config, terminal, terminal, redirected)
	if _, err := io.WriteString(errOutput, "first\nsecond\n"); err != nil {
		t.Fatal(err)
	}
	if err := redirected.Sync(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(redirected.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(contents), "first\nsecond\n"; got != want {
		t.Fatalf("redirected stderr = %q, want %q", got, want)
	}
}

func TestClearTerminalErasesVisibleScreenAndHomesCursor(t *testing.T) {
	var output bytes.Buffer
	if err := clearTerminal(&output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "\033[2J\033[H" {
		t.Fatalf("clear sequence = %q", output.String())
	}
}

func TestTerminalLineReaderRestoresAfterInterrupt(t *testing.T) {
	runPTYExitCase(t, "interrupt", "RESULT_ERR:Interrupt\r\n", "\x03")
}

func TestTerminalLineReaderRestoresAfterEOF(t *testing.T) {
	runPTYExitCase(t, "eof", "RESULT_ERR:EOF\r\n", "\x04")
}

func TestTerminalLineReaderRestoresAfterInitializationFailure(t *testing.T) {
	runPTYExitCase(t, "init-error", "RESULT_INIT_ERROR\r\n", "")
}

func TestTerminalLineReaderRestoresAfterReadError(t *testing.T) {
	runPTYExitCase(t, "read-error", "RESULT_READ_ERROR\r\n", "")
}

func runPTYExitCase(t *testing.T, mode, result, input string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=TestTerminalLineReaderUsesCursorEditingAndSessionHistory", "--")
	command.Env = append(os.Environ(), "KOMPOSYN_PTY_HELPER=1", "KOMPOSYN_PTY_EXIT_MODE="+mode)
	master, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	setPTYSize(t, master)
	output := newPTYOutputReader(master)
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if mode != "init-error" {
		output.waitForText(t, "> ")
	}
	if _, err := master.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, result)
	output.waitForCommand(t, command)
}

func setPTYSize(t *testing.T, terminal *os.File) {
	t.Helper()
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
}

func terminalEditorPTYHelper(t *testing.T) {
	expectedRestored := terminalStateAfterRawModeRoundTrip(t)
	reader, _, _, ok := newTerminalLineReader(os.Stdin, os.Stdout, os.Stderr)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	for range 4 {
		line, err := reader.ReadLine("> ")
		if err != nil {
			t.Fatalf("read terminal line: %v", err)
		}
		if _, err := io.WriteString(os.Stdout, "RESULT:"+line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	assertTerminalStateRestored(t, expectedRestored, "normal exit")
}

func terminalEditorPTYTableHelper(t *testing.T) {
	reader, output, _, ok := newTerminalLineReader(os.Stdin, os.Stdout, os.Stderr)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	if _, err := reader.ReadLine("> "); err != nil {
		t.Fatalf("read terminal line: %v", err)
	}
	report := tokenlogger.UsageReport{
		Filters: tokenlogger.UsageFilter{GroupBy: []string{tokenlogger.GroupClient, tokenlogger.GroupProvider, tokenlogger.GroupModel}},
		Rows: []tokenlogger.UsageRow{
			{
				Dimensions:    map[string]string{tokenlogger.GroupClient: "claude-code", tokenlogger.GroupProvider: "anthropic", tokenlogger.GroupModel: "claude-sonnet-5"},
				UsageMeasures: tokenlogger.UsageMeasures{RequestCount: 107, SessionCount: 1},
			},
			{
				Dimensions:    map[string]string{tokenlogger.GroupClient: "codex-cli", tokenlogger.GroupProvider: "openai", tokenlogger.GroupModel: "codex-auto-review"},
				UsageMeasures: tokenlogger.UsageMeasures{RequestCount: 2690, SessionCount: 12},
			},
		},
	}
	if err := tokenlogger.WriteTable(output, report); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func terminalEditorPTYStderrHelper(t *testing.T) {
	reader, _, errOutput, ok := newTerminalLineReader(os.Stdin, os.Stdout, os.Stderr)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	if _, err := reader.ReadLine("> "); err != nil {
		t.Fatalf("read terminal line: %v", err)
	}
	fmt.Fprintln(errOutput, "ERRLINE1")
	fmt.Fprintln(errOutput, "ERRLINE2")
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func terminalEditorPTYExitHelper(t *testing.T, mode string) {
	expectedRestored := terminalStateAfterRawModeRoundTrip(t)
	if mode == "init-error" {
		_, _, _, ok := newTerminalLineReaderWithFactory(os.Stdin, os.Stdout, os.Stderr, func(*readline.Config) (*readline.Instance, error) {
			return nil, errors.New("injected readline initialization failure")
		})
		if ok {
			t.Fatal("terminal reader unexpectedly initialized")
		}
		assertTerminalStateRestored(t, expectedRestored, mode)
		if _, err := io.WriteString(os.Stdout, "RESULT_INIT_ERROR\n"); err != nil {
			t.Fatal(err)
		}
		return
	}
	initial, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	factory := readline.NewEx
	if mode == "read-error" {
		factory = func(config *readline.Config) (*readline.Instance, error) {
			config.Stdin = terminalEditorErrorReader{}
			return readline.NewEx(config)
		}
	}
	reader, _, _, ok := newTerminalLineReaderWithFactory(os.Stdin, os.Stdout, os.Stderr, factory)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	_, readErr := reader.ReadLine("> ")
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	assertTerminalStateRestored(t, expectedRestored, mode+" (initial="+fmt.Sprintf("%#v", initial)+")")
	if mode == "read-error" {
		if readErr == nil {
			t.Fatalf("read error = %v", readErr)
		}
	} else if mode == "interrupt" && !errors.Is(readErr, readline.ErrInterrupt) {
		t.Fatalf("interrupt error = %v", readErr)
	} else if mode == "eof" && !errors.Is(readErr, io.EOF) {
		t.Fatalf("EOF error = %v", readErr)
	}
	result := "RESULT_ERR:" + errorName(readErr) + "\n"
	if mode == "read-error" {
		result = "RESULT_READ_ERROR\n"
	}
	if _, err := io.WriteString(os.Stdout, result); err != nil {
		t.Fatal(err)
	}
}

type terminalEditorErrorReader struct{}

func (terminalEditorErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("injected terminal read failure")
}

func (terminalEditorErrorReader) Close() error { return nil }

func terminalStateAfterRawModeRoundTrip(t *testing.T) *term.State {
	t.Helper()
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := term.Restore(int(os.Stdin.Fd()), state); err != nil {
		t.Fatal(err)
	}
	state, err = term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertTerminalStateRestored(t *testing.T, expected *term.State, path string) {
	t.Helper()
	restored, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil || !reflect.DeepEqual(expected, restored) {
		t.Fatalf("terminal state was not restored after %s: err=%v expected=%#v restored=%#v", path, err, expected, restored)
	}
}

func errorName(err error) string {
	if errors.Is(err, readline.ErrInterrupt) {
		return "Interrupt"
	}
	if errors.Is(err, io.EOF) {
		return "EOF"
	}
	return err.Error()
}

type ptyReadResult struct {
	data []byte
	err  error
}

type ptyOutputReader struct {
	results  chan ptyReadResult
	output   bytes.Buffer
	searched int
}

func newPTYOutputReader(master *os.File) *ptyOutputReader {
	reader := &ptyOutputReader{results: make(chan ptyReadResult, 16)}
	go func() {
		chunk := make([]byte, 256)
		for {
			count, err := master.Read(chunk)
			result := ptyReadResult{data: append([]byte(nil), chunk[:count]...), err: err}
			reader.results <- result
			if err != nil {
				return
			}
		}
	}()
	return reader
}

func (reader *ptyOutputReader) waitForText(t *testing.T, expected string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		output := reader.output.String()
		if index := strings.Index(output[reader.searched:], expected); index >= 0 {
			reader.searched += index + len(expected)
			return
		}
		select {
		case result := <-reader.results:
			reader.output.Write(result.data)
			if result.err != nil {
				t.Fatalf("read terminal output: %v (output=%q)", result.err, reader.output.String())
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for terminal output %q (output=%q)", expected, reader.output.String())
		}
	}
}

func (reader *ptyOutputReader) waitForCommand(t *testing.T, command *exec.Cmd) {
	t.Helper()
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result := <-reader.results:
			reader.output.Write(result.data)
		case err := <-wait:
			if err != nil {
				t.Fatalf("PTY helper failed: %v (output=%q)", err, reader.output.String())
			}
			return
		case <-timer.C:
			_ = command.Process.Kill()
			err := <-wait
			t.Fatalf("timed out waiting for PTY helper exit: %v (output=%q)", err, reader.output.String())
		}
	}
}
