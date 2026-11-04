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
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	nativeActorMode    = "--readbrokerstudy-actor"
	nativeActorReady   = "READY\n"
	nativeActorRun     = "RUN\n"
	nativeActorDone    = "DONE\n"
	nativeActorFailed  = "FAIL\n"
	nativeActorMaxLine = 16
	nativeActorMaxLife = 12 * time.Second
	nativeActorMaxWait = 3 * time.Second
)

var (
	ErrNativeActorProtocol = errors.New("native fixture actor protocol failed")
	ErrNativeActorCleanup  = errors.New("native fixture actor process cleanup was not confirmed")
)

// NativeActorAction is intentionally limited to deterministic fixture
// namespace transitions. It is not a general filesystem command interface.
type NativeActorAction string

const (
	NativeActorRename   NativeActorAction = "rename"
	NativeActorHardlink NativeActorAction = "hardlink"
	NativeActorReplace  NativeActorAction = "replace"
)

// NativeActor is a separate same-user fixture process waiting for one RUN
// message. It has no shell, provider credentials, open worker capability, or
// arbitrary command support.
type NativeActor struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	stdoutPipe   *os.File
	done         chan struct{}
	doneReadGate <-chan struct{}
	cancel       context.CancelFunc
	waitErr      error
	run          bool
	closed       bool
	ready        bool
	changed      bool
	joined       bool
}

// NewNativeActor starts the study executable in its fixed actor mode and
// returns only after it sends READY. All paths are synthetic fixture paths:
// source and target must be clean relative paths beneath fixtureRoot.
func NewNativeActor(ctx context.Context, executable, fixtureRoot string, action NativeActorAction, sourceRelative, targetRelative string) (*NativeActor, error) {
	args, err := nativeActorArgs(executable, fixtureRoot, action, sourceRelative, targetRelative)
	if err != nil {
		return nil, err
	}
	return startNativeActor(ctx, executable, args, nil)
}

func nativeActorArgs(executable, fixtureRoot string, action NativeActorAction, sourceRelative, targetRelative string) ([]string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return nil, errors.New("native fixture actor executable path must be absolute and clean")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("native fixture actor executable is unavailable")
	}
	if !filepath.IsAbs(fixtureRoot) || filepath.Clean(fixtureRoot) != fixtureRoot {
		return nil, errors.New("native fixture actor root must be absolute and clean")
	}
	if err := validateNativeActorRoot(fixtureRoot); err != nil {
		return nil, err
	}
	if action != NativeActorRename && action != NativeActorHardlink && action != NativeActorReplace {
		return nil, errors.New("native fixture actor action is not allowlisted")
	}
	if _, err := nativeActorFixturePath(fixtureRoot, sourceRelative); err != nil {
		return nil, err
	}
	if _, err := nativeActorFixturePath(fixtureRoot, targetRelative); err != nil {
		return nil, err
	}
	return []string{nativeActorMode, fixtureRoot, string(action), sourceRelative, targetRelative}, nil
}

func startNativeActor(ctx context.Context, executable string, args, environment []string) (*NativeActor, error) {
	if ctx == nil {
		return nil, errors.New("native fixture actor requires a context")
	}
	lifeCtx, cancel := context.WithTimeout(ctx, nativeActorMaxLife)
	cmd := exec.CommandContext(lifeCtx, executable, args...)
	// A non-nil empty slice is intentional: nil would make os/exec inherit the
	// coordinator's environment, including unrelated credentials and paths.
	cmd.Env = append([]string{}, environment...)
	cmd.WaitDelay = 250 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, errors.New("native fixture actor input pipe is unavailable")
	}
	stdoutPipe, stdoutChild, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, errors.New("native fixture actor output pipe is unavailable")
	}
	// Use an explicitly owned pipe rather than StdoutPipe: Cmd.Wait closes a
	// StdoutPipe, which can discard the final DONE token if the child exits
	// before the protocol reader consumes it.
	cmd.Stdout = stdoutChild
	// Actor diagnostics are deliberately discarded. Fixture paths and host
	// errors are not copied into the machine-readable study report.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdoutPipe.Close()
		_ = stdoutChild.Close()
		cancel()
		return nil, errors.New("native fixture actor process did not start")
	}
	actor := &NativeActor{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdoutPipe, nativeActorMaxLine), stdoutPipe: stdoutPipe, done: make(chan struct{}), cancel: cancel}
	go func() {
		waitErr := cmd.Wait()
		actor.mu.Lock()
		actor.waitErr = waitErr
		actor.joined = true
		actor.mu.Unlock()
		close(actor.done)
	}()
	if err := stdoutChild.Close(); err != nil {
		_ = actor.Close()
		_ = stdoutChild.Close()
		return nil, errors.New("native fixture actor output pipe setup failed")
	}
	if err := actor.readToken(ctx, nativeActorReady); err != nil {
		_ = actor.Close()
		return nil, err
	}
	actor.mu.Lock()
	actor.ready = true
	actor.mu.Unlock()
	return actor, nil
}

func (a *NativeActor) Ready() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ready
}

func (a *NativeActor) TransitionCompleted() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.changed
}

func (a *NativeActor) Joined() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.joined
}

// Run releases the prepared actor exactly once and waits for its bounded DONE
// acknowledgement and process exit.
func (a *NativeActor) Run(ctx context.Context) error {
	if a == nil || ctx == nil {
		return ErrNativeActorProtocol
	}
	a.mu.Lock()
	if a.closed || a.run {
		a.mu.Unlock()
		return ErrNativeActorProtocol
	}
	a.run = true
	stdin := a.stdin
	a.mu.Unlock()
	if _, err := io.WriteString(stdin, nativeActorRun); err != nil {
		_ = a.Close()
		return ErrNativeActorProtocol
	}
	a.mu.Lock()
	doneReadGate := a.doneReadGate
	a.mu.Unlock()
	if doneReadGate != nil {
		timer := time.NewTimer(nativeActorMaxWait)
		defer timer.Stop()
		select {
		case <-doneReadGate:
		case <-ctx.Done():
			_ = a.Close()
			return ErrNativeActorProtocol
		case <-timer.C:
			_ = a.Close()
			return ErrNativeActorProtocol
		}
	}
	if err := a.readToken(ctx, nativeActorDone); err != nil {
		_ = a.Close()
		return err
	}
	a.mu.Lock()
	a.changed = true
	a.mu.Unlock()
	if err := a.wait(ctx); err != nil {
		return err
	}
	return nil
}

func (a *NativeActor) readToken(ctx context.Context, want string) error {
	if ctx == nil {
		return ErrNativeActorProtocol
	}
	type tokenResult struct {
		line string
		err  error
	}
	result := make(chan tokenResult, 1)
	go func() {
		line, err := readNativeActorLine(a.stdout)
		result <- tokenResult{line: line, err: err}
	}()
	timer := time.NewTimer(nativeActorMaxWait)
	defer timer.Stop()
	select {
	case got := <-result:
		if got.err != nil || got.line != want {
			return ErrNativeActorProtocol
		}
		return nil
	case <-ctx.Done():
		return ErrNativeActorProtocol
	case <-timer.C:
		return ErrNativeActorProtocol
	}
}

func readNativeActorLine(reader *bufio.Reader) (string, error) {
	var line strings.Builder
	for line.Len() < nativeActorMaxLine {
		value, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		line.WriteByte(value)
		if value == '\n' {
			return line.String(), nil
		}
	}
	return "", ErrNativeActorProtocol
}

func (a *NativeActor) wait(ctx context.Context) error {
	if a == nil || ctx == nil {
		return ErrNativeActorProtocol
	}
	timer := time.NewTimer(nativeActorMaxWait)
	defer timer.Stop()
	select {
	case <-a.done:
		a.mu.Lock()
		err := a.waitErr
		a.mu.Unlock()
		if err != nil {
			return ErrNativeActorProtocol
		}
		return nil
	case <-ctx.Done():
		_ = a.Close()
		return ErrNativeActorCleanup
	case <-timer.C:
		_ = a.Close()
		return ErrNativeActorCleanup
	}
}

// Close kills an actor that has not exited and confirms its direct-process
// Wait. The actor never starts descendants, so a successful Wait is the full
// cleanup boundary for this fixed helper.
func (a *NativeActor) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	stdin := a.stdin
	cmd := a.cmd
	done := a.done
	cancel := a.cancel
	stdoutPipe := a.stdoutPipe
	a.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
		}
	}
	if stdoutPipe != nil {
		_ = stdoutPipe.Close()
	}
	timer := time.NewTimer(nativeActorMaxWait)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return ErrNativeActorCleanup
	}
}

// RunNativeActorMode executes one fixed fixture transition when invoked by
// the study executable's private actor mode. args includes the mode flag.
func RunNativeActorMode(args []string) (bool, error) {
	if len(args) == 0 || args[0] != nativeActorMode {
		return false, nil
	}
	if len(args) != 5 {
		return true, ErrNativeActorProtocol
	}
	root, action, sourceRelative, targetRelative := args[1], NativeActorAction(args[2]), args[3], args[4]
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return true, ErrNativeActorProtocol
	}
	if err := validateNativeActorRoot(root); err != nil {
		return true, ErrNativeActorProtocol
	}
	if _, err := nativeActorFixturePath(root, sourceRelative); err != nil {
		return true, ErrNativeActorProtocol
	}
	if _, err := nativeActorFixturePath(root, targetRelative); err != nil {
		return true, ErrNativeActorProtocol
	}
	if action != NativeActorRename && action != NativeActorHardlink && action != NativeActorReplace {
		return true, ErrNativeActorProtocol
	}
	rootFD, err := openNativeActorRoot(root)
	if err != nil {
		return true, ErrNativeActorProtocol
	}
	defer unix.Close(rootFD)
	writer := bufio.NewWriterSize(os.Stdout, nativeActorMaxLine)
	if _, err := writer.WriteString(nativeActorReady); err != nil || writer.Flush() != nil {
		return true, ErrNativeActorProtocol
	}
	line, err := readNativeActorLine(bufio.NewReaderSize(os.Stdin, nativeActorMaxLine))
	if err != nil || line != nativeActorRun {
		return true, ErrNativeActorProtocol
	}
	if err := runNativeActorActionAtRoot(rootFD, sourceRelative, targetRelative, action); err != nil {
		_, _ = writer.WriteString(nativeActorFailed)
		_ = writer.Flush()
		return true, ErrNativeActorProtocol
	}
	if _, err := writer.WriteString(nativeActorDone); err != nil || writer.Flush() != nil {
		return true, ErrNativeActorProtocol
	}
	return true, nil
}

func runNativeActorActionAtRoot(rootFD int, sourceRelative, targetRelative string, action NativeActorAction) error {
	sourceParent, sourceLeaf, err := openNativeActorParent(rootFD, sourceRelative)
	if err != nil {
		return ErrNativeActorProtocol
	}
	defer unix.Close(sourceParent)
	targetParent, targetLeaf, err := openNativeActorParent(rootFD, targetRelative)
	if err != nil {
		return ErrNativeActorProtocol
	}
	defer unix.Close(targetParent)
	if _, err := nativeActorEntry(targetParent, targetLeaf); err == nil {
		return ErrNativeActorProtocol
	} else if !errors.Is(err, unix.ENOENT) {
		return ErrNativeActorProtocol
	}
	sourceStat, err := nativeActorEntry(sourceParent, sourceLeaf)
	if err != nil || sourceStat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return ErrNativeActorProtocol
	}
	sourceIdentity := identityFromStat(&sourceStat)
	switch action {
	case NativeActorRename:
		if sourceStat.Mode&unix.S_IFMT != unix.S_IFREG && sourceStat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return ErrNativeActorProtocol
		}
		if err := unix.Renameat2(sourceParent, sourceLeaf, targetParent, targetLeaf, unix.RENAME_NOREPLACE); err != nil {
			return ErrNativeActorProtocol
		}
		moved, err := nativeActorEntry(targetParent, targetLeaf)
		if err != nil || identityFromStat(&moved) != sourceIdentity {
			return ErrNativeActorProtocol
		}
		return nil
	case NativeActorHardlink:
		if sourceStat.Mode&unix.S_IFMT != unix.S_IFREG {
			return ErrNativeActorProtocol
		}
		if err := unix.Linkat(sourceParent, sourceLeaf, targetParent, targetLeaf, 0); err != nil {
			return ErrNativeActorProtocol
		}
		linked, err := nativeActorEntry(targetParent, targetLeaf)
		if err != nil || identityFromStat(&linked) != sourceIdentity || linked.Mode&unix.S_IFMT != unix.S_IFREG {
			_ = unix.Unlinkat(targetParent, targetLeaf, 0)
			return ErrNativeActorProtocol
		}
		return nil
	case NativeActorReplace:
		if sourceStat.Mode&unix.S_IFMT != unix.S_IFREG {
			return ErrNativeActorProtocol
		}
		if err := unix.Renameat2(sourceParent, sourceLeaf, targetParent, targetLeaf, unix.RENAME_NOREPLACE); err != nil {
			return ErrNativeActorProtocol
		}
		archived, err := nativeActorEntry(targetParent, targetLeaf)
		if err != nil || identityFromStat(&archived) != sourceIdentity {
			_ = unix.Renameat2(targetParent, targetLeaf, sourceParent, sourceLeaf, unix.RENAME_NOREPLACE)
			return ErrNativeActorProtocol
		}
		fd, err := unix.Openat(sourceParent, sourceLeaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			_ = unix.Renameat2(targetParent, targetLeaf, sourceParent, sourceLeaf, unix.RENAME_NOREPLACE)
			return ErrNativeActorProtocol
		}
		replacement := []byte("actor-substitute\n")
		written, writeErr := unix.Write(fd, replacement)
		closeErr := unix.Close(fd)
		if writeErr != nil || written != len(replacement) || closeErr != nil {
			_ = unix.Unlinkat(sourceParent, sourceLeaf, 0)
			_ = unix.Renameat2(targetParent, targetLeaf, sourceParent, sourceLeaf, unix.RENAME_NOREPLACE)
			return ErrNativeActorProtocol
		}
		return nil
	default:
		return ErrNativeActorProtocol
	}
}

func validateNativeActorRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return errors.New("native fixture actor root must be absolute, clean, and non-root")
	}
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return errors.New("native fixture actor temp root is unavailable")
	}
	relative, err := filepath.Rel(tempRoot, root)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("native fixture actor root must be a private temporary directory")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("native fixture actor root must be a private real directory")
	}
	return nil
}

func openNativeActorRoot(root string) (int, error) {
	rootFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	relative := strings.TrimPrefix(root, string(filepath.Separator))
	fd, err := unix.Openat2(rootFD, relative, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	_ = unix.Close(rootFD)
	return fd, err
}

func openNativeActorParent(rootFD int, relative string) (int, string, error) {
	if relative == "" || filepath.Clean(relative) != relative || !filepath.IsLocal(relative) {
		return -1, "", ErrNativeActorProtocol
	}
	parent := filepath.Dir(relative)
	if parent == "." {
		fd, err := unix.FcntlInt(uintptr(rootFD), unix.F_DUPFD_CLOEXEC, 0)
		return fd, filepath.Base(relative), err
	}
	fd, err := unix.Openat2(rootFD, parent, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	return fd, filepath.Base(relative), err
}

func nativeActorEntry(parentFD int, name string) (unix.Stat_t, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return unix.Stat_t{}, err
	}
	var stat unix.Stat_t
	err = unix.Fstat(fd, &stat)
	_ = unix.Close(fd)
	return stat, err
}

func nativeActorFixturePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.ContainsRune(relative, '\x00') || !filepath.IsLocal(relative) {
		return "", ErrNativeActorProtocol
	}
	joined := filepath.Join(root, relative)
	check, err := filepath.Rel(root, joined)
	if err != nil || check == "." || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", ErrNativeActorProtocol
	}
	return joined, nil
}
