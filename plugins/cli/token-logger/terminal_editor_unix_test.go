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
	output := newPTYOutputReader(master)
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	output.waitForText(t, "> ")

	// Type "abc", delete the final character, insert "Z", and submit. The
	// resulting command proves that editor-side editing is applied before
	// command dispatch.
	if _, err := master.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "> abc")
	if _, err := master.Write([]byte("\x7fZ\r")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "RESULT:abZ\n")
	if _, err := master.Write([]byte("\x1b[A\r")); err != nil {
		t.Fatal(err)
	}
	output.waitForText(t, "RESULT:abZ\n")
	if err := command.Wait(); err != nil {
		t.Fatalf("PTY helper failed: %v", err)
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
	if err := command.Wait(); err != nil {
		t.Fatalf("PTY helper failed: %v", err)
	}
}

func terminalEditorPTYHelper(t *testing.T) {
	expectedRestored := terminalStateAfterRawModeRoundTrip(t)
	reader, ok := newTerminalLineReader(os.Stdin, os.Stdout, os.Stderr)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	for range 2 {
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

func terminalEditorPTYExitHelper(t *testing.T, mode string) {
	expectedRestored := terminalStateAfterRawModeRoundTrip(t)
	if mode == "init-error" {
		_, ok := newTerminalLineReaderWithFactory(os.Stdin, os.Stdout, os.Stderr, func(*readline.Config) (*readline.Instance, error) {
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
	reader, ok := newTerminalLineReaderWithFactory(os.Stdin, os.Stdout, os.Stderr, factory)
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
	results chan ptyReadResult
	output  bytes.Buffer
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
