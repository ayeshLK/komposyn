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
	"runtime"
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
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	waitForPTYText(t, master, "> ")

	// Type "abc", delete the final character, insert "Z", and submit. The
	// resulting command proves that editor-side editing is applied before
	// command dispatch.
	if _, err := master.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	waitForPTYText(t, master, "> abc")
	if _, err := master.Write([]byte("\x7fZ\r")); err != nil {
		t.Fatal(err)
	}
	waitForPTYText(t, master, "RESULT:abZ\n")
	if _, err := master.Write([]byte("\x1b[A\r")); err != nil {
		t.Fatal(err)
	}
	waitForPTYText(t, master, "RESULT:abZ\n")
	if err := command.Wait(); err != nil {
		t.Fatalf("PTY helper failed: %v", err)
	}
}

func TestTerminalLineReaderRestoresAfterInterrupt(t *testing.T) {
	runPTYExitCase(t, "interrupt", "RESULT_ERR:Interrupt\r\n", "\x03")
}

func TestTerminalLineReaderRestoresAfterEOF(t *testing.T) {
	runPTYExitCase(t, "eof", "RESULT_ERR:EOF\r\n", "\x04")
}

func runPTYExitCase(t *testing.T, mode, result, input string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=TestTerminalLineReaderUsesCursorEditingAndSessionHistory", "--")
	command.Env = append(os.Environ(), "KOMPOSYN_PTY_HELPER=1", "KOMPOSYN_PTY_EXIT_MODE="+mode)
	master, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = master.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	waitForPTYText(t, master, "> ")
	if _, err := master.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	waitForPTYText(t, master, result)
	if err := command.Wait(); err != nil {
		t.Fatalf("PTY helper failed: %v", err)
	}
}

func terminalEditorPTYHelper(t *testing.T) {
	reader, ok := newTerminalLineReader(os.Stdin, os.Stdout, os.Stderr)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	defer reader.Close()
	for range 2 {
		line, err := reader.ReadLine("> ")
		if err != nil {
			t.Fatalf("read terminal line: %v", err)
		}
		if _, err := io.WriteString(os.Stdout, "RESULT:"+line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func terminalEditorPTYExitHelper(t *testing.T, mode string) {
	initial, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := newTerminalLineReader(os.Stdin, os.Stdout, os.Stderr)
	if !ok {
		t.Fatal("pseudo-terminal was not recognized as an interactive terminal")
	}
	_, readErr := reader.ReadLine("> ")
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := term.GetState(int(os.Stdin.Fd()))
	// macOS may add the IUTF8 termios flag while entering raw mode. The
	// flag is harmless and is normalized by the platform when the saved
	// state is restored, so exact State equality is not portable there.
	if err != nil || (!reflect.DeepEqual(initial, restored) && runtime.GOOS != "darwin") {
		t.Fatalf("terminal state was not restored after %s: err=%v initial=%#v restored=%#v", mode, err, initial, restored)
	}
	if mode == "interrupt" && !errors.Is(readErr, readline.ErrInterrupt) {
		t.Fatalf("interrupt error = %v", readErr)
	}
	if mode == "eof" && !errors.Is(readErr, io.EOF) {
		t.Fatalf("EOF error = %v", readErr)
	}
	if _, err := io.WriteString(os.Stdout, "RESULT_ERR:"+errorName(readErr)+"\n"); err != nil {
		t.Fatal(err)
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

func waitForPTYText(t *testing.T, master *os.File, expected string) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		var output bytes.Buffer
		chunk := make([]byte, 256)
		for !strings.Contains(output.String(), expected) {
			count, err := master.Read(chunk)
			output.Write(chunk[:count])
			if strings.Contains(output.String(), expected) {
				result <- nil
				return
			}
			if err != nil {
				result <- fmt.Errorf("read terminal output: %w (output=%q)", err, output.String())
				return
			}
		}
		result <- nil
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for terminal output %q", expected)
	}
}
