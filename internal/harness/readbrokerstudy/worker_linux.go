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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	frameReady              byte = 'R'
	frameHandoff            byte = 'H'
	frameStarted            byte = 'S'
	frameRefused            byte = 'X'
	frameData               byte = 'D'
	frameFinish             byte = 'F'
	workerBootstrapMode          = "--readbrokerstudy-bootstrap"
	workerStderrLimit            = 4 << 10
	workerWaitDelay              = 500 * time.Millisecond
	backendProbeTimeout          = 2 * time.Second
	backendHashLimit             = 512 << 20
	backendProbeOutputLimit      = 64 << 10
	maxWorkerSnapshotBytes       = 7 << 20
)

type BindMode string

const (
	BindModeDescriptor   BindMode = "ro-bind-fd"
	BindModeFileSnapshot BindMode = "file-snapshot"
)

// BubblewrapCapabilities records options reported by the trusted executable's
// bounded --help response. These are feature checks, not version assumptions.
type BubblewrapCapabilities struct {
	Options  []string
	BindMode BindMode
}

var requiredBubblewrapOptions = []string{
	"--as-pid-1", "--cap-drop", "--chdir", "--clearenv", "--die-with-parent",
	"--disable-userns", "--hostname", "--new-session", "--size",
	"--sync-fd", "--tmpfs", "--unshare-ipc", "--unshare-net", "--unshare-pid",
	"--unshare-user", "--unshare-uts",
}

// BackendInspection contains bounded, non-secret metadata about the selected
// Bubblewrap candidate. Version execution occurs only after the complete
// executable path passes the same root-owned, non-writable trust check used
// by NewWorkerLauncher.
type BackendInspection struct {
	Path         string
	UID          uint32
	Mode         uint32
	SHA256       string
	Version      string
	Capabilities []string
	BindMode     BindMode
	Trusted      bool
	Error        string
}

func InspectBubblewrap(path string) BackendInspection {
	result := BackendInspection{Path: path}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		result.Error = "configured backend is missing or not a regular file"
		return result
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		result.UID = stat.Uid
		result.Mode = stat.Mode & 0o7777
	}
	if err := validateTrustedExecutable(path, true); err != nil {
		result.Error = "backend trust validation failed; harness skipped backend probes and sandbox launch"
		return result
	}
	result.Trusted = true
	if info.Size() <= backendHashLimit {
		if data, readErr := os.ReadFile(path); readErr == nil {
			sum := sha256.Sum256(data)
			result.SHA256 = hex.EncodeToString(sum[:])
		}
	}
	version, capabilities, err := probeBubblewrap(path)
	result.Version = version
	result.Capabilities = capabilities.Options
	result.BindMode = capabilities.BindMode
	if err != nil {
		result.Error = boundedText("trusted backend capability preflight failed: "+err.Error(), 512)
		return result
	}
	return result
}

func probeBubblewrap(path string) (string, BubblewrapCapabilities, error) {
	return probeBubblewrapWith(func(argument string) (string, error) {
		return runBoundedProbe(path, argument)
	})
}

func probeBubblewrapWith(run func(argument string) (string, error)) (string, BubblewrapCapabilities, error) {
	versionOutput, err := run("--version")
	if err != nil {
		return "", BubblewrapCapabilities{}, err
	}
	help, err := run("--help")
	if err != nil {
		version := strings.TrimSpace(versionOutput)
		fields := strings.Fields(version)
		if len(fields) == 2 && fields[0] == "bubblewrap" && validVersion(fields[1]) {
			return version, BubblewrapCapabilities{}, err
		}
		return "", BubblewrapCapabilities{}, err
	}
	return bubblewrapCapabilities(versionOutput, help)
}

func bubblewrapCapabilities(versionOutput, help string) (string, BubblewrapCapabilities, error) {
	version := strings.TrimSpace(versionOutput)
	fields := strings.Fields(version)
	if len(fields) != 2 || fields[0] != "bubblewrap" || !validVersion(fields[1]) {
		return "", BubblewrapCapabilities{}, ErrUnavailable
	}
	options := parseBubblewrapOptions(help)
	ordered := make([]string, 0, len(options))
	for option := range options {
		ordered = append(ordered, option)
	}
	slices.Sort(ordered)
	missing := make([]string, 0)
	for _, option := range requiredBubblewrapOptions {
		if _, ok := options[option]; !ok {
			missing = append(missing, option)
		}
	}
	if len(missing) != 0 {
		return version, BubblewrapCapabilities{Options: ordered}, fmt.Errorf("%w: missing required Bubblewrap features: %s", ErrUnavailable, strings.Join(missing, ", "))
	}
	if _, ok := options["--ro-bind-fd"]; ok {
		return version, BubblewrapCapabilities{Options: ordered, BindMode: BindModeDescriptor}, nil
	}
	if _, hasFile := options["--ro-bind-data"]; hasFile {
		if _, hasPerms := options["--perms"]; hasPerms {
			return version, BubblewrapCapabilities{Options: ordered, BindMode: BindModeFileSnapshot}, nil
		}
	}
	return version, BubblewrapCapabilities{Options: ordered},
		fmt.Errorf("%w: Bubblewrap supports neither descriptor binds nor the secure --perms/--ro-bind-data snapshot path", ErrUnavailable)
}

// EffectiveBubblewrapArguments returns the exact reviewed launch argument
// sequence for the selected descriptor mount compatibility path.
func EffectiveBubblewrapArguments(mode BindMode) []string {
	args := []string{
		"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--hostname", "komposyn-readbroker", "--unshare-net", "--as-pid-1", "--disable-userns", "--cap-drop", "ALL", "--clearenv",
		"--size", "8388608", "--tmpfs", "/",
	}
	switch mode {
	case BindModeDescriptor:
		args = append(args, "--ro-bind-fd", "5", "/worker")
	case BindModeFileSnapshot:
		args = append(args, "--perms", "0500", "--ro-bind-data", "5", "/worker")
	default:
		return nil
	}
	return append(args, "--sync-fd", "6", "--chdir", "/", "--", "/worker", workerBootstrapMode,
		"--control-fd", fmt.Sprint(controlFD), "--worker-mode", workerVerificationMode(mode),
		"--worker-fd", fmt.Sprint(workerVerificationFD), "--seccomp-fd", fmt.Sprint(seccompInputFD))
}

func workerVerificationMode(mode BindMode) string {
	if mode == BindModeFileSnapshot {
		return "snapshot"
	}
	return "descriptor"
}

func runBoundedProbe(path, argument string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), backendProbeTimeout)
	defer cancel()
	output := &boundedProbeOutput{}
	cmd := exec.CommandContext(ctx, path, argument)
	cmd.Dir = "/"
	cmd.Env = []string{}
	cmd.WaitDelay = workerWaitDelay
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil || ctx.Err() != nil || output.overflow {
		return "", ErrUnavailable
	}
	return output.String(), nil
}

type boundedProbeOutput struct {
	mu       sync.Mutex
	bytes    []byte
	overflow bool
}

func (o *boundedProbeOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if len(o.bytes)+len(p) > backendProbeOutputLimit {
		o.overflow = true
		p = p[:backendProbeOutputLimit-len(o.bytes)]
	}
	o.bytes = append(o.bytes, p...)
	return n, nil
}

func (o *boundedProbeOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.bytes)
}

func parseBubblewrapOptions(help string) map[string]struct{} {
	options := make(map[string]struct{})
	for _, line := range strings.Split(help, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
			options[fields[0]] = struct{}{}
		}
	}
	return options
}

func validVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

type workerLauncher struct {
	bwrapPath  string
	capability BubblewrapCapabilities
	workerFile *os.File
	closeOnce  sync.Once
	closeErr   error
}

// NewWorkerLauncher constructs the only launch path in this feasibility
// package. It requires an absolute root-owned Bubblewrap executable and uses
// either descriptor bind or a sealed byte snapshot; it never falls back to a
// pathname-based worker source.
func NewWorkerLauncher(bwrapPath, workerPath string) (WorkerLauncher, error) {
	if err := validateTrustedExecutable(bwrapPath, true); err != nil {
		return nil, fmt.Errorf("%w: trusted Bubblewrap validation failed", ErrUnavailable)
	}
	_, capability, err := probeBubblewrap(bwrapPath)
	if err != nil {
		return nil, fmt.Errorf("%w: Bubblewrap compatibility preflight failed: %v", ErrUnavailable, err)
	}
	workerFile, err := snapshotCurrentExecutable(workerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: running fixed worker could not be snapshotted: %v", ErrUnavailable, err)
	}
	return &workerLauncher{bwrapPath: bwrapPath, capability: capability, workerFile: workerFile}, nil
}

// snapshotCurrentExecutable binds the worker bytes to the already-running
// study image, not to a mutable pathname. The supplied path is only checked as
// an identity hint; the snapshot source is the kernel's /proc/self/exe handle.
func snapshotCurrentExecutable(workerPath string) (*os.File, error) {
	if workerPath == "" {
		return nil, errors.New("worker path identity hint is empty")
	}
	pathFile, err := os.Open(workerPath)
	if err != nil {
		return nil, errors.New("worker path could not be opened for identity comparison")
	}
	defer pathFile.Close()
	runningFile, err := os.Open("/proc/self/exe")
	if err != nil {
		return nil, errors.New("the running executable handle is unavailable")
	}
	defer runningFile.Close()
	var pathStat, runningStat unix.Stat_t
	if err := unix.Fstat(int(pathFile.Fd()), &pathStat); err != nil {
		return nil, errors.New("worker path identity could not be checked")
	}
	if err := unix.Fstat(int(runningFile.Fd()), &runningStat); err != nil ||
		pathStat.Dev != runningStat.Dev || pathStat.Ino != runningStat.Ino {
		return nil, errors.New("worker path no longer names the running executable")
	}
	if err := validateStaticWorkerFile(runningFile); err != nil {
		return nil, err
	}
	snapshot, err := createSealedWorkerSnapshot(runningFile)
	if err != nil {
		return nil, err
	}
	if err := validateStaticWorkerFile(snapshot); err != nil {
		_ = snapshot.Close()
		return nil, errors.New("sealed worker snapshot is not a static ELF executable")
	}
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		_ = snapshot.Close()
		return nil, errors.New("sealed worker snapshot could not be rewound")
	}
	return snapshot, nil
}

// openSealedWorkerSnapshot returns a new read-only open-file description at
// offset zero. A dup would share the source offset, which Bubblewrap advances
// while consuming --file/--ro-bind-data input.
func openSealedWorkerSnapshot(snapshot *os.File) (*os.File, error) {
	if snapshot == nil {
		return nil, errors.New("sealed worker snapshot is missing")
	}
	var expected unix.Stat_t
	if err := unix.Fstat(int(snapshot.Fd()), &expected); err != nil || expected.Mode&unix.S_IFMT != unix.S_IFREG ||
		expected.Size <= 0 || expected.Size > maxWorkerSnapshotBytes {
		return nil, errors.New("sealed worker snapshot identity is invalid")
	}
	originalFlags, err := unix.FcntlInt(snapshot.Fd(), unix.F_GETFL, 0)
	if err != nil || originalFlags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, errors.New("sealed worker snapshot is not held read-only")
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	gotSeals, err := unix.FcntlInt(snapshot.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || gotSeals&seals != seals {
		return nil, errors.New("worker snapshot is not fully sealed")
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", snapshot.Fd()), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("fresh read-only worker descriptor could not be opened")
	}
	file := os.NewFile(uintptr(fd), "readbroker-worker-launch-snapshot")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("fresh worker snapshot descriptor could not be wrapped")
	}
	var actual unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err := unix.Fstat(fd, &actual); err != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino ||
		actual.Size != expected.Size || flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		_ = file.Close()
		return nil, errors.New("fresh worker snapshot descriptor failed validation")
	}
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		_ = file.Close()
		return nil, errors.New("fresh worker snapshot descriptor is not at offset zero")
	}
	return file, nil
}

// createSealedWorkerSnapshot copies a bounded open file into anonymous memory
// and seals the resulting memfd before returning a read-only descriptor. It
// never reopens the source pathname and never trusts source mode bits as an
// integrity check.
func createSealedWorkerSnapshot(source *os.File) (_ *os.File, resultErr error) {
	if source == nil {
		return nil, errors.New("snapshot source is missing")
	}
	var before unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &before); err != nil || before.Mode&unix.S_IFMT != unix.S_IFREG ||
		before.Size <= 0 || before.Size > maxWorkerSnapshotBytes {
		return nil, errors.New("snapshot source is not a bounded regular file")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New("snapshot source cannot be rewound")
	}
	fd, err := unix.MemfdCreate("komposyn-readbroker-worker", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("sealed worker snapshot is unavailable: %w", err)
	}
	writable := os.NewFile(uintptr(fd), "readbroker-worker-snapshot-writable")
	if writable == nil {
		_ = unix.Close(fd)
		return nil, errors.New("sealed worker snapshot descriptor could not be wrapped")
	}
	defer func() {
		if writable != nil {
			_ = writable.Close()
		}
	}()
	count, err := io.CopyN(writable, source, before.Size)
	if err != nil || count != before.Size {
		return nil, errors.New("worker snapshot copy was incomplete")
	}
	var after unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &after); err != nil || after.Dev != before.Dev || after.Ino != before.Ino ||
		after.Size != before.Size || after.Mtim != before.Mtim || after.Ctim != before.Ctim {
		return nil, errors.New("worker snapshot source changed during copy")
	}
	var copied unix.Stat_t
	if err := unix.Fstat(fd, &copied); err != nil || copied.Size != before.Size {
		return nil, errors.New("worker snapshot size does not match the source")
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return nil, fmt.Errorf("worker snapshot could not be sealed: %w", err)
	}
	gotSeals, err := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
	if err != nil || gotSeals&seals != seals {
		return nil, errors.New("worker snapshot seals could not be verified")
	}
	readOnlyFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", fd), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("read-only worker snapshot descriptor could not be opened")
	}
	readOnly := os.NewFile(uintptr(readOnlyFD), "readbroker-worker-snapshot")
	if readOnly == nil {
		_ = unix.Close(readOnlyFD)
		return nil, errors.New("read-only worker snapshot descriptor could not be wrapped")
	}
	var reopened unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(readOnlyFD), unix.F_GETFL, 0)
	if err := unix.Fstat(readOnlyFD, &reopened); err != nil || reopened.Dev != copied.Dev || reopened.Ino != copied.Ino ||
		flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		_ = readOnly.Close()
		return nil, errors.New("read-only worker snapshot descriptor failed identity validation")
	}
	readOnlySeals, err := unix.FcntlInt(uintptr(readOnlyFD), unix.F_GET_SEALS, 0)
	if err != nil || readOnlySeals&seals != seals {
		_ = readOnly.Close()
		return nil, errors.New("read-only worker snapshot is not fully sealed")
	}
	if _, err := readOnly.Seek(0, io.SeekStart); err != nil {
		_ = readOnly.Close()
		return nil, errors.New("read-only worker snapshot could not be rewound")
	}
	if err := writable.Close(); err != nil {
		_ = readOnly.Close()
		return nil, errors.New("writable worker snapshot descriptor could not be closed")
	}
	writable = nil
	return readOnly, nil
}

func (l *workerLauncher) Close() error {
	l.closeOnce.Do(func() {
		if l.workerFile != nil {
			l.closeErr = l.workerFile.Close()
		}
	})
	return l.closeErr
}

type sandboxWorker struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	control  *os.File
	ctx      context.Context
	cancel   context.CancelFunc
	stderr   *limitedOutput
	ready    bool
	sent     bool
	started  bool
	finished bool
	expected FileIdentity
	probe    WorkerProbe
	waitOnce sync.Once
	waitErr  error
	joined   bool
}

type limitedOutput struct {
	mu sync.Mutex
	b  []byte
	n  int
}

func (l *limitedOutput) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	originalLen := len(p)
	l.n += originalLen
	remaining := workerStderrLimit - len(l.b)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		l.b = append(l.b, p...)
	}
	return originalLen, nil
}

func (l *limitedOutput) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	value := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, string(l.b))
	return value
}

func (l *workerLauncher) Start(ctx context.Context) (SandboxWorker, error) {
	if ctx == nil {
		return nil, errors.New("worker context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateTrustedExecutable(l.bwrapPath, true); err != nil {
		return nil, fmt.Errorf("%w: trusted Bubblewrap changed or is unavailable", ErrUnavailable)
	}
	if l.workerFile == nil {
		return nil, fmt.Errorf("%w: pinned fixed worker is unavailable", ErrUnavailable)
	}
	filter, err := workerSeccompFilter()
	if err != nil {
		return nil, fmt.Errorf("%w: seccomp profile could not be prepared", ErrUnavailable)
	}
	defer func() {
		_ = filter.Close()
		_ = os.Remove(filter.Name())
	}()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: local control socket could not be created", ErrUnavailable)
	}
	parentSocket := os.NewFile(uintptr(pair[0]), "readbroker-control-parent")
	childSocket := os.NewFile(uintptr(pair[1]), "readbroker-control-worker")
	if parentSocket == nil || childSocket == nil {
		_ = unix.Close(pair[0])
		_ = unix.Close(pair[1])
		return nil, ErrUnavailable
	}
	if err := unix.SetNonblock(pair[0], true); err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: control socket setup failed", ErrUnavailable)
	}
	if err := unix.SetNonblock(pair[1], true); err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: control socket setup failed", ErrUnavailable)
	}
	mountFile, err := openSealedWorkerSnapshot(l.workerFile)
	if err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: worker mount snapshot could not be reopened", ErrUnavailable)
	}
	defer mountFile.Close()
	verificationFile, err := openSealedWorkerSnapshot(l.workerFile)
	if err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: worker verification snapshot could not be reopened", ErrUnavailable)
	}
	defer verificationFile.Close()
	workerCtx, cancel := context.WithTimeout(ctx, time.Duration(workerDeadline))
	args := EffectiveBubblewrapArguments(l.capability.BindMode)
	if args == nil {
		cancel()
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: no verified worker bind mode is available", ErrUnavailable)
	}
	cmd := exec.CommandContext(workerCtx, l.bwrapPath, args...)
	cmd.Dir = "/"
	cmd.Env = []string{"KOMPOSYN_READBROKER_SENTINEL=synthetic-only"}
	cmd.ExtraFiles = []*os.File{filter, childSocket, mountFile, verificationFile}
	cmd.Stdout = io.Discard
	stderr := &limitedOutput{}
	cmd.Stderr = stderr
	cmd.WaitDelay = workerWaitDelay
	if err := cmd.Start(); err != nil {
		cancel()
		_ = parentSocket.Close()
		_ = childSocket.Close()
		message := strings.ReplaceAll(err.Error(), l.bwrapPath, "<BWRAP>")
		return nil, fmt.Errorf("%w: Bubblewrap process could not start: %s", ErrUnavailable, boundedText(message, 512))
	}
	_ = childSocket.Close()
	return &sandboxWorker{cmd: cmd, control: parentSocket, ctx: workerCtx, cancel: cancel, stderr: stderr}, nil
}

func (w *sandboxWorker) Ready(ctx context.Context) error {
	w.mu.Lock()
	if w.ready || w.cmd == nil {
		w.mu.Unlock()
		return ErrProtocol
	}
	w.mu.Unlock()
	packet, err := recvPacket(ctx, int(w.control.Fd()), 4096)
	if err != nil {
		return fmt.Errorf("%w: worker startup acknowledgement unavailable: %s", ErrUnavailable, w.startupDiagnostic())
	}
	if len(packet) < 2 || packet[0] != frameReady {
		return ErrProtocol
	}
	var probe WorkerProbe
	if err := json.Unmarshal(packet[1:], &probe); err != nil {
		return ErrProtocol
	}
	w.mu.Lock()
	w.probe = probe
	valid := probe.SeccompMode == 2 && probe.NoNewPrivs && probe.SocketCreationDenied &&
		probe.PathOpenDenied && probe.ReadCapabilityInitiallyAbsent && probe.OnlyProtocolDescriptor &&
		probe.EnvironmentCleared
	if valid {
		w.ready = true
	}
	w.mu.Unlock()
	if !valid {
		_ = w.Terminate()
		_ = w.Wait()
		return fmt.Errorf("%w: required worker isolation probes failed", ErrUnavailable)
	}
	return nil
}

// Probe returns the bounded in-worker observations captured before any file
// descriptor handoff. It contains no path or file content.
func (w *sandboxWorker) Probe() WorkerProbe {
	w.mu.Lock()
	defer w.mu.Unlock()
	probe := w.probe
	probe.Details = append([]string(nil), probe.Details...)
	return probe
}

func (w *sandboxWorker) startupDiagnostic() string {
	if w.stderr == nil {
		return ""
	}
	message := w.stderr.String()
	if w.cmd != nil {
		message = strings.ReplaceAll(message, w.cmd.Path, "<BWRAP>")
		for _, arg := range w.cmd.Args {
			if len(arg) > 1 && strings.HasPrefix(arg, "/") {
				message = strings.ReplaceAll(message, arg, "<PATH>")
			}
		}
	}
	return boundedText(message, 512)
}

// FailureDetail is only surfaced by the study's sanitized JSON evidence. It
// includes bounded stderr and, after Wait, the candidate process exit code.
func (w *sandboxWorker) FailureDetail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	message := w.startupDiagnostic()
	if w.cmd != nil && w.cmd.ProcessState != nil {
		message = strings.TrimSpace(message + " candidate_exit_code=" + strconv.Itoa(w.cmd.ProcessState.ExitCode()))
	}
	return boundedText(message, 768)
}

func boundedText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (w *sandboxWorker) TransferCapability(ctx context.Context, file *os.File, expected FileIdentity) error {
	if file == nil || ctx == nil {
		return ErrProtocol
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ready || w.sent || w.finished {
		return ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil || identityFromStat(&stat) != expected || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return ErrStaleReceipt
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return ErrStaleReceipt
	}
	message := identityFrame(frameHandoff, expected)
	oob := unix.UnixRights(int(file.Fd()))
	n, err := unix.SendmsgN(int(w.control.Fd()), message, oob, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	if err != nil || n != len(message) {
		return fmt.Errorf("%w: capability descriptor handoff failed", ErrUnavailable)
	}
	w.expected = expected
	w.sent = true
	return nil
}

func (w *sandboxWorker) AwaitReadStarted(ctx context.Context) error {
	w.mu.Lock()
	if !w.sent || w.started || w.finished {
		w.mu.Unlock()
		return ErrProtocol
	}
	expected := w.expected
	w.mu.Unlock()
	packet, err := recvPacket(ctx, int(w.control.Fd()), 64)
	if err != nil {
		return fmt.Errorf("%w: read-start acknowledgement unavailable", ErrUnavailable)
	}
	if len(packet) != 1+20 || packet[0] != frameStarted || !sameIdentity(packet[1:], expected) {
		return ErrStaleReceipt
	}
	w.mu.Lock()
	w.started = true
	w.mu.Unlock()
	return nil
}

func (w *sandboxWorker) ReadResult(ctx context.Context) (WorkerResult, error) {
	w.mu.Lock()
	if !w.started || w.finished {
		w.mu.Unlock()
		return WorkerResult{}, ErrProtocol
	}
	w.finished = true
	w.mu.Unlock()
	result := WorkerResult{Outcome: ReadUnknown}
	for {
		packet, err := recvPacket(ctx, int(w.control.Fd()), dataChunkBytes+16)
		if err != nil {
			return result, fmt.Errorf("%w: worker result was incomplete", ErrUnavailable)
		}
		if len(packet) == 0 {
			return result, io.EOF
		}
		switch packet[0] {
		case frameData:
			if len(packet) == 1 || len(result.Bytes)+len(packet)-1 > MaxReadBytes {
				return WorkerResult{Outcome: ReadUnknown}, ErrResultTooLarge
			}
			result.Bytes = append(result.Bytes, packet[1:]...)
		case frameFinish:
			if len(packet) != 7 {
				return WorkerResult{Outcome: ReadUnknown}, ErrProtocol
			}
			outcome, ok := decodeOutcome(packet[1])
			if !ok || uint32(len(result.Bytes)) != binary.BigEndian.Uint32(packet[3:7]) {
				return WorkerResult{Outcome: ReadUnknown}, ErrProtocol
			}
			result.Outcome = outcome
			if packet[2] != 0 {
				result.Detail = "truncated=true"
			}
			return result, nil
		default:
			return WorkerResult{Outcome: ReadUnknown}, ErrProtocol
		}
	}
}

func decodeOutcome(value byte) (ReadOutcome, bool) {
	switch value {
	case 'S':
		return ReadSucceeded, true
	case 'F':
		return ReadFailed, true
	case 'P':
		return ReadPartial, true
	default:
		return ReadUnknown, false
	}
}

func (w *sandboxWorker) Terminate() error {
	w.mu.Lock()
	cmd := w.cmd
	w.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (w *sandboxWorker) Wait() error {
	w.waitOnce.Do(func() {
		var waitErr error
		if w.cmd != nil {
			waitErr = w.cmd.Wait()
		}
		if w.control != nil {
			_ = w.control.Close()
		}
		if w.cancel != nil {
			w.cancel()
		}
		w.mu.Lock()
		w.waitErr = waitErr
		w.joined = w.cmd == nil || w.cmd.ProcessState != nil
		w.mu.Unlock()
	})
	return w.waitErr
}

func (w *sandboxWorker) Joined() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.joined
}

func identityFrame(kind byte, identity FileIdentity) []byte {
	frame := make([]byte, 1+20)
	frame[0] = kind
	binary.BigEndian.PutUint64(frame[1:9], identity.Device)
	binary.BigEndian.PutUint64(frame[9:17], identity.Inode)
	binary.BigEndian.PutUint32(frame[17:21], identity.Type)
	return frame
}

func sameIdentity(frame []byte, identity FileIdentity) bool {
	return len(frame) == 20 && binary.BigEndian.Uint64(frame[:8]) == identity.Device &&
		binary.BigEndian.Uint64(frame[8:16]) == identity.Inode && binary.BigEndian.Uint32(frame[16:20]) == identity.Type
}

func recvPacket(ctx context.Context, fd, max int) ([]byte, error) {
	if max <= 0 || max > maxPacketBytes {
		return nil, ErrProtocol
	}
	for {
		if err := waitFD(ctx, fd, unix.POLLIN); err != nil {
			return nil, err
		}
		buffer := make([]byte, max)
		n, oobn, flags, _, err := unix.Recvmsg(fd, buffer, nil, unix.MSG_DONTWAIT|unix.MSG_CMSG_CLOEXEC)
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err != nil {
			return nil, err
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || oobn != 0 || n > max {
			return nil, ErrProtocol
		}
		return buffer[:n], nil
	}
}

func waitFD(ctx context.Context, fd int, events int16) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pollFD := []unix.PollFd{{Fd: int32(fd), Events: events}}
		ready, err := unix.Poll(pollFD, 50)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if ready != 0 {
			if pollFD[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
				return ErrUnavailable
			}
			if pollFD[0].Revents&(events|unix.POLLHUP) != 0 {
				return nil
			}
		}
	}
}

func sendPacket(fd int, packet []byte) error {
	if len(packet) == 0 || len(packet) > maxPacketBytes {
		return ErrProtocol
	}
	n, err := unix.SendmsgN(fd, packet, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	if err != nil || n != len(packet) {
		return ErrUnavailable
	}
	return nil
}

// WorkerMain is selected only by the study command's private bootstrap mode.
// It authenticates the mounted executable and installs seccomp before the
// worker reports readiness. The sole protocol descriptor is fd 4; an approved
// read-only file descriptor arrives later via SCM_RIGHTS.
func WorkerMain() int {
	if len(os.Args) != 10 || os.Args[1] != workerBootstrapMode || os.Args[2] != "--control-fd" || os.Args[3] != "4" ||
		os.Args[4] != "--worker-mode" || os.Args[6] != "--worker-fd" || os.Args[7] != "6" ||
		os.Args[8] != "--seccomp-fd" || os.Args[9] != "3" {
		return 70
	}
	var verifyErr error
	switch os.Args[5] {
	case "descriptor":
		verifyErr = verifyDescriptorMounts([]DescriptorMount{{FD: workerVerificationFD, Destination: "/worker"}})
	case "snapshot":
		verifyErr = verifySnapshotExecutable(workerVerificationFD, "/worker")
	default:
		return 70
	}
	if verifyErr != nil {
		return 70
	}
	if err := installWorkerSeccomp(seccompInputFD); err != nil {
		return 70
	}
	return workerMainFD(controlFD)
}

const workerMountFD = 5
const workerVerificationFD = 6

type DescriptorMount struct {
	FD          int
	Destination string
}

// verifySnapshotExecutable checks that Bubblewrap's private-root file contains
// the exact bytes from the sealed host snapshot. The trust anchor is the
// already-running parent image plus the sealed descriptor and trusted
// Bubblewrap file-copy operation, not this post-exec comparison by itself.
func verifySnapshotExecutable(fd int, destination string) (result error) {
	if fd <= 2 || destination == "" || destination[0] != '/' {
		return ErrProtocol
	}
	defer func() {
		if err := unix.Close(fd); err != nil && result == nil {
			result = err
		}
	}()
	var source unix.Stat_t
	if err := unix.Fstat(fd, &source); err != nil || source.Mode&unix.S_IFMT != unix.S_IFREG ||
		source.Size <= 0 || source.Size > maxWorkerSnapshotBytes {
		return ErrProtocol
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return ErrProtocol
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	gotSeals, err := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
	if err != nil || gotSeals&seals != seals {
		return ErrProtocol
	}
	var entry unix.Stat_t
	if err := unix.Lstat(destination, &entry); err != nil || entry.Mode&unix.S_IFMT != unix.S_IFREG || entry.Mode&0o111 == 0 || entry.Size != source.Size {
		return ErrStaleReceipt
	}
	workerFD, err := unix.Open(destination, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrStaleReceipt
	}
	defer func() {
		if err := unix.Close(workerFD); err != nil && result == nil {
			result = err
		}
	}()
	var mounted unix.Stat_t
	if err := unix.Fstat(workerFD, &mounted); err != nil || mounted.Mode&unix.S_IFMT != unix.S_IFREG ||
		mounted.Dev != entry.Dev || mounted.Ino != entry.Ino || mounted.Size != source.Size {
		return ErrStaleReceipt
	}
	sourceDigest, err := digestDescriptor(fd, source.Size)
	if err != nil {
		return ErrProtocol
	}
	workerDigest, err := digestDescriptor(workerFD, mounted.Size)
	if err != nil || sourceDigest != workerDigest {
		return ErrStaleReceipt
	}
	return nil
}

func digestDescriptor(fd int, size int64) ([sha256.Size]byte, error) {
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	for offset := int64(0); offset < size; {
		want := int64(len(buffer))
		if remaining := size - offset; want > remaining {
			want = remaining
		}
		count, err := unix.Pread(fd, buffer[:int(want)], offset)
		if err == unix.EINTR {
			continue
		}
		if err != nil || count == 0 {
			return [sha256.Size]byte{}, ErrProtocol
		}
		_, _ = hash.Write(buffer[:count])
		offset += int64(count)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

// verifyDescriptorMounts consumes every distinct non-standard verification
// descriptor and compares the mounted entry with the retained object before
// seccomp denies pathname inspection. It is deliberately limited to startup
// verification; no verification descriptor is retained by the worker.
func verifyDescriptorMounts(mounts []DescriptorMount) (result error) {
	seen := make(map[int]struct{}, len(mounts))
	defer func() {
		for fd := range seen {
			if err := unix.Close(fd); err != nil && result == nil {
				result = err
			}
		}
	}()
	for _, mount := range mounts {
		if mount.FD > 2 {
			if _, duplicate := seen[mount.FD]; duplicate {
				return ErrProtocol
			}
			seen[mount.FD] = struct{}{}
		}
		if mount.FD <= 2 || mount.Destination == "" || mount.Destination[0] != '/' {
			return ErrProtocol
		}
	}
	for _, mount := range mounts {
		var source unix.Stat_t
		if err := unix.Fstat(mount.FD, &source); err != nil || source.Mode&unix.S_IFMT != unix.S_IFREG {
			return ErrProtocol
		}
		flags, err := unix.FcntlInt(uintptr(mount.FD), unix.F_GETFL, 0)
		if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
			return ErrProtocol
		}
		var mounted unix.Stat_t
		if err := unix.Lstat(mount.Destination, &mounted); err != nil || mounted.Mode&unix.S_IFMT != unix.S_IFREG ||
			mounted.Dev != source.Dev || mounted.Ino != source.Ino {
			return ErrStaleReceipt
		}
	}
	return nil
}

func installWorkerSeccomp(fd int) error {
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size <= 0 || stat.Size > 64<<10 || stat.Size%unix.SizeofSockFilter != 0 {
		return ErrProtocol
	}
	data := make([]byte, stat.Size)
	for offset := 0; offset < len(data); {
		count, err := unix.Pread(fd, data[offset:], int64(offset))
		if err == unix.EINTR {
			continue
		}
		if err != nil || count == 0 {
			return ErrProtocol
		}
		offset += count
	}
	expected, err := workerSeccompFilterBytes()
	if err != nil || !bytes.Equal(data, expected) {
		return ErrProtocol
	}
	filters := make([]unix.SockFilter, len(data)/unix.SizeofSockFilter)
	for index := range filters {
		offset := index * unix.SizeofSockFilter
		filters[index] = unix.SockFilter{
			Code: binary.NativeEndian.Uint16(data[offset : offset+2]),
			Jt:   data[offset+2], Jf: data[offset+3],
			K: binary.NativeEndian.Uint32(data[offset+4 : offset+8]),
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	result, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filters)
	runtime.KeepAlive(program)
	if errno != 0 || result != 0 {
		if errno == 0 {
			return fmt.Errorf("seccomp thread synchronization failed for thread %d", result)
		}
		return errno
	}
	return nil
}

func workerMainFD(fd int) int {
	probe := collectWorkerProbe(fd)
	encoded, err := json.Marshal(probe)
	if err != nil || sendPacket(fd, append([]byte{frameReady}, encoded...)) != nil {
		return 70
	}
	if probe.SeccompMode != 2 || !probe.NoNewPrivs || !probe.SocketCreationDenied ||
		!probe.PathOpenDenied || !probe.ReadCapabilityInitiallyAbsent || !probe.OnlyProtocolDescriptor ||
		!probe.EnvironmentCleared {
		return 71
	}
	return receiveAndRead(fd)
}

func collectWorkerProbe(control int) WorkerProbe {
	probe := WorkerProbe{Details: []string{"required namespaces requested: user,pid,ipc,uts,net (no try/fallback flags)", "bubblewrap mount namespace created with empty tmpfs root", "worker mount identity verified before seccomp; verification descriptors closed", "worker installed seccomp with thread synchronization before readiness"}}
	if mode, _, errno := unix.Syscall6(unix.SYS_PRCTL, unix.PR_GET_SECCOMP, 0, 0, 0, 0, 0); errno == 0 {
		probe.SeccompMode = int(mode)
	}
	if value, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err == nil && value == 1 {
		probe.NoNewPrivs = true
	}
	openFDs := make([]int, 0, 4)
	for candidate := 3; candidate < 1024; candidate++ {
		if _, err := unix.FcntlInt(uintptr(candidate), unix.F_GETFD, 0); err == nil {
			openFDs = append(openFDs, candidate)
		}
	}
	probe.ReadCapabilityInitiallyAbsent = len(openFDs) == 1 && openFDs[0] == control
	probe.OnlyProtocolDescriptor = probe.ReadCapabilityInitiallyAbsent
	probe.Details = append(probe.Details, "startup descriptor inventory checked: stdio plus protocol fd 4 only")
	if !probe.OnlyProtocolDescriptor {
		probe.Details = append(probe.Details, "worker inherited an unexpected non-stdio descriptor")
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err == unix.EPERM {
		probe.SocketCreationDenied = true
	} else if err == nil {
		_ = unix.Close(fd)
	}
	probe.PathOpenDenied = false
	fd, err = unix.Openat(unix.AT_FDCWD, "/worker", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err == unix.EPERM {
		probe.PathOpenDenied = true
	} else if err == nil {
		_ = unix.Close(fd)
	}
	probe.EnvironmentCleared = os.Getenv("KOMPOSYN_READBROKER_SENTINEL") == ""
	for _, value := range os.Environ() {
		if value != "PWD=/" {
			probe.EnvironmentCleared = false
		}
	}
	if !probe.EnvironmentCleared {
		probe.Details = append(probe.Details, "synthetic inherited environment marker remained visible")
	}
	if err := errors.Join(
		probeError(probe.NoNewPrivs, "no_new_privs was not set"),
		probeError(probe.SocketCreationDenied, "socket creation was not denied"),
		probeError(probe.PathOpenDenied, "path open was not denied"),
		probeError(probe.OnlyProtocolDescriptor, "unexpected inherited descriptors"),
		probeError(probe.EnvironmentCleared, "environment marker was not cleared"),
	); err != nil {
		probe.Details = append(probe.Details, err.Error())
	}
	return probe
}

func probeError(ok bool, message string) error {
	if ok {
		return nil
	}
	return errors.New(message)
}

func receiveAndRead(control int) int {
	packet := make([]byte, 64)
	oob := make([]byte, unix.CmsgSpace(4*4))
	for {
		if waitFD(context.Background(), control, unix.POLLIN) != nil {
			return 72
		}
		n, oobn, flags, _, err := unix.Recvmsg(control, packet, oob, unix.MSG_DONTWAIT|unix.MSG_CMSG_CLOEXEC)
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err != nil || n != 21 || oobn == 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || packet[0] != frameHandoff {
			return sendRefusal(control)
		}
		files, parseErr := parseExactlyOneReceivedFD(oob[:oobn])
		if parseErr != nil {
			return sendRefusal(control)
		}
		capability := files[0]
		result := readTransferredFD(control, capability, packet[1:])
		_ = unix.Close(capability)
		return result
	}
}

func parseExactlyOneReceivedFD(oob []byte) ([]int, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, ErrProtocol
	}
	var received []int
	valid := len(messages) == 1
	for i := range messages {
		fds, parseErr := unix.ParseUnixRights(&messages[i])
		if parseErr != nil {
			valid = false
			continue
		}
		received = append(received, fds...)
	}
	if !valid || len(received) != 1 {
		for _, fd := range received {
			_ = unix.Close(fd)
		}
		return nil, ErrProtocol
	}
	return received, nil
}

func sendRefusal(control int) int {
	_ = sendPacket(control, []byte{frameRefused})
	return 73
}

func readTransferredFD(control, fd int, expected []byte) int {
	unix.CloseOnExec(fd)
	var stat unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || unix.Fstat(fd, &stat) != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || !sameIdentity(expected, identityFromStat(&stat)) {
		_ = sendPacket(control, []byte{frameRefused})
		return 74
	}
	if err := sendPacket(control, identityFrame(frameStarted, identityFromStat(&stat))); err != nil {
		return 75
	}
	result := preadBounded(fd, stat.Size)
	for offset := 0; offset < len(result.Bytes); {
		end := offset + dataChunkBytes
		if end > len(result.Bytes) {
			end = len(result.Bytes)
		}
		if err := sendPacket(control, append([]byte{frameData}, result.Bytes[offset:end]...)); err != nil {
			return 76
		}
		offset = end
	}
	truncated := byte(0)
	if result.Detail == "truncated=true" {
		truncated = 1
	}
	finish := []byte{frameFinish, encodeOutcome(result.Outcome), truncated, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(finish[3:7], uint32(len(result.Bytes)))
	if err := sendPacket(control, finish); err != nil {
		return 77
	}
	return 0
}

func preadBounded(fd int, fileSize int64) WorkerResult {
	limit := MaxReadBytes
	truncated := fileSize > int64(limit)
	if fileSize >= 0 && fileSize < int64(limit) {
		limit = int(fileSize)
	}
	data := make([]byte, limit)
	read := 0
	for read < len(data) {
		n, err := unix.Pread(fd, data[read:], int64(read))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			outcome := ReadFailed
			if read > 0 {
				outcome = ReadPartial
			}
			return WorkerResult{Outcome: outcome, Bytes: data[:read]}
		}
		if n == 0 {
			break
		}
		read += n
	}
	result := WorkerResult{Outcome: ReadSucceeded, Bytes: data[:read]}
	if truncated {
		result.Detail = "truncated=true"
	}
	return result
}

func encodeOutcome(outcome ReadOutcome) byte {
	switch outcome {
	case ReadSucceeded:
		return 'S'
	case ReadPartial:
		return 'P'
	default:
		return 'F'
	}
}

func RunWorkerCommand(ctx context.Context, bwrapPath, workerPath string) (SandboxWorker, error) {
	launcher, err := NewWorkerLauncher(bwrapPath, workerPath)
	if err != nil {
		return nil, err
	}
	worker, startErr := launcher.Start(ctx)
	closeErr := launcher.Close()
	if startErr != nil || closeErr != nil {
		if worker != nil {
			cleanupErr := errors.Join(worker.Terminate(), worker.Wait())
			return nil, errors.Join(startErr, closeErr, cleanupErr)
		}
		return nil, errors.Join(startErr, closeErr)
	}
	return worker, nil
}

var _ SandboxWorker = (*sandboxWorker)(nil)
var _ WorkerLauncher = (*workerLauncher)(nil)
