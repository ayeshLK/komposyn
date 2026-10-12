//go:build !windows

package main

import (
	"bytes"
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
	defer master.Close()
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
	if err := master.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer master.SetReadDeadline(time.Time{})
	var output bytes.Buffer
	chunk := make([]byte, 256)
	for !strings.Contains(output.String(), expected) {
		count, err := master.Read(chunk)
		output.Write(chunk[:count])
		if err != nil {
			t.Fatalf("read terminal prompt: %v (output=%q)", err, output.String())
		}
	}
}
