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
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty/v2"
	"golang.org/x/term"
)

func TestTerminalLineReaderUsesCursorEditingAndSessionHistory(t *testing.T) {
	if os.Getenv("KOMPOSYN_PTY_HELPER") == "1" {
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

	// Submit one command, then use a real up-arrow escape sequence to recall it.
	if _, err := master.Write([]byte("abc\r")); err != nil {
		t.Fatal(err)
	}
	waitForPTYText(t, master, "RESULT:abc\n")
	if _, err := master.Write([]byte("\x1b[A\r")); err != nil {
		t.Fatal(err)
	}
	waitForPTYText(t, master, "RESULT:abc\n")
	if err := command.Wait(); err != nil {
		t.Fatalf("PTY helper failed: %v", err)
	}
}

func terminalEditorPTYHelper(t *testing.T) {
	rawState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer term.Restore(int(os.Stdin.Fd()), rawState)
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

func waitForPTYText(t *testing.T, master *os.File, expected string) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		var output bytes.Buffer
		chunk := make([]byte, 256)
		for !strings.Contains(output.String(), expected) {
			count, err := master.Read(chunk)
			output.Write(chunk[:count])
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
