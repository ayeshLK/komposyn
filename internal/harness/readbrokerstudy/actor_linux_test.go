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

//go:build linux

package readbrokerstudy

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	actorEnv        = "KOMPOSYN_READ_BROKER_ACTOR"
	actorRootEnv    = "KOMPOSYN_READ_BROKER_ACTOR_ROOT"
	actorActionEnv  = "KOMPOSYN_READ_BROKER_ACTOR_ACTION"
	actorSourceEnv  = "KOMPOSYN_READ_BROKER_ACTOR_SOURCE"
	actorTargetEnv  = "KOMPOSYN_READ_BROKER_ACTOR_TARGET"
	actorDeadline   = 3 * time.Second
	actorReadyToken = "READY\n"
	actorDoneToken  = "DONE\n"
)

// TestNamespaceActorProcess is re-executed as a separate same-user process by
// namespaceActor. Its allowlisted operations are intentionally limited to
// disposable paths beneath one fresh fixture root; no shell is involved.
func TestNamespaceActorProcess(t *testing.T) {
	if os.Getenv(actorEnv) != "1" {
		return
	}
	root := os.Getenv(actorRootEnv)
	if !filepath.IsAbs(root) {
		os.Exit(20)
	}
	source, err := fixturePath(root, os.Getenv(actorSourceEnv))
	if err != nil {
		os.Exit(21)
	}
	target, err := fixturePath(root, os.Getenv(actorTargetEnv))
	if err != nil {
		os.Exit(22)
	}
	writer := bufio.NewWriter(os.Stdout)
	if _, err := writer.WriteString(actorReadyToken); err != nil || writer.Flush() != nil {
		os.Exit(23)
	}
	command, err := bufio.NewReaderSize(os.Stdin, 16).ReadString('\n')
	if err != nil || command != "RUN\n" {
		os.Exit(24)
	}
	switch os.Getenv(actorActionEnv) {
	case "rename":
		err = os.Rename(source, target)
	case "link":
		err = os.Link(source, target)
	case "replace":
		if err = os.Remove(source); err == nil {
			err = os.Rename(target, source)
		}
	case "write":
		err = os.WriteFile(source, []byte("actor-synthetic"), 0o600)
	case "remove":
		err = os.Remove(source)
	default:
		err = fmt.Errorf("actor operation is not allowlisted")
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "actor fixture operation failed: %s", err.Error())
		os.Exit(25)
	}
	if _, err := writer.WriteString(actorDoneToken); err != nil || writer.Flush() != nil {
		os.Exit(26)
	}
	os.Exit(0)
}

type namespaceActor struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	done    chan struct{}
	waitErr error
}

// startNamespaceActor starts a distinct process and waits for its explicit
// READY handshake. The child has only an allowlisted operation and fixture
// paths beneath root; the caller decides when to release it with Run.
func startNamespaceActor(t *testing.T, root, action, sourceRelative, targetRelative string) *namespaceActor {
	t.Helper()
	for _, relative := range []string{sourceRelative, targetRelative} {
		if relative == "" {
			continue
		}
		if _, err := fixturePath(root, relative); err != nil {
			t.Fatalf("unsafe actor fixture path %q: %v", relative, err)
		}
	}
	switch action {
	case "rename", "link", "replace", "write", "remove":
	default:
		t.Fatalf("actor action %q is not allowlisted", action)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestNamespaceActorProcess$")
	command.Env = []string{
		actorEnv + "=1",
		actorRootEnv + "=" + root,
		actorActionEnv + "=" + action,
		actorSourceEnv + "=" + sourceRelative,
		actorTargetEnv + "=" + targetRelative,
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	actor := &namespaceActor{
		command: command,
		stdin:   stdin,
		stdout:  bufio.NewReaderSize(stdout, 32),
		done:    make(chan struct{}),
	}
	go func() {
		actor.waitErr = command.Wait()
		close(actor.done)
	}()
	t.Cleanup(func() { actor.stop() })
	if got := actor.readLine(t); got != actorReadyToken {
		t.Fatalf("actor startup handshake = %q, want READY", got)
	}
	return actor
}

func (a *namespaceActor) Run(t *testing.T) {
	t.Helper()
	if err := a.run(); err != nil {
		t.Fatalf("actor fixture operation: %v", err)
	}
}

func (a *namespaceActor) run() error {
	if _, err := io.WriteString(a.stdin, "RUN\n"); err != nil {
		return fmt.Errorf("release actor: %w", err)
	}
	result := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		line, err := a.stdout.ReadString('\n')
		if err != nil {
			readErr <- err
			return
		}
		result <- line
	}()
	select {
	case got := <-result:
		if got != actorDoneToken {
			return fmt.Errorf("actor completion handshake = %q, want DONE", got)
		}
	case err := <-readErr:
		return fmt.Errorf("read actor completion handshake: %w", err)
	case <-time.After(actorDeadline):
		return fmt.Errorf("actor completion handshake timed out")
	}
	if err := a.wait(); err != nil {
		return fmt.Errorf("actor process failed: %w", err)
	}
	return nil
}

func (a *namespaceActor) readLine(t *testing.T) string {
	t.Helper()
	type lineResult struct {
		line string
		err  error
	}
	read := make(chan lineResult, 1)
	go func() {
		line, err := a.stdout.ReadString('\n')
		read <- lineResult{line: line, err: err}
	}()
	select {
	case result := <-read:
		if result.err != nil {
			t.Fatalf("read actor handshake: %v", result.err)
		}
		return result.line
	case <-time.After(actorDeadline):
		t.Fatal("actor handshake timed out")
		return ""
	}
}

func (a *namespaceActor) wait() error {
	select {
	case <-a.done:
		return a.waitErr
	case <-time.After(actorDeadline):
		return fmt.Errorf("actor did not exit within %s", actorDeadline)
	}
}

func (a *namespaceActor) stop() {
	_ = a.stdin.Close()
	if a.command.Process != nil {
		select {
		case <-a.done:
		default:
			_ = a.command.Process.Kill()
		}
	}
	select {
	case <-a.done:
	case <-time.After(actorDeadline):
	}
}

func fixturePath(root, relative string) (string, error) {
	if !filepath.IsAbs(root) || relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, '\x00') {
		return "", fmt.Errorf("path must be relative to the absolute fixture root")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes fixture root")
	}
	joined := filepath.Join(root, clean)
	if _, err := filepath.Rel(root, joined); err != nil {
		return "", err
	}
	return joined, nil
}

func TestNamespaceActorUsesDeterministicSeparateProcess(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "approved"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "moved"), 0o700); err != nil {
		t.Fatal(err)
	}
	actor := startNamespaceActor(t, root, "rename", "approved", "moved/approved")
	if _, err := os.Stat(filepath.Join(root, "approved")); err != nil {
		t.Fatalf("actor changed fixture before release: %v", err)
	}
	actor.Run(t)
	if _, err := os.Stat(filepath.Join(root, "approved")); !os.IsNotExist(err) {
		t.Fatalf("old name remains after actor rename: err=%v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "moved", "approved")); err != nil || !info.IsDir() {
		t.Fatalf("moved fixture directory missing: info=%v err=%v", info, err)
	}
}
