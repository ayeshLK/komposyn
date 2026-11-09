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
	"path/filepath"
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
	frameCapabilityHeld     byte = 'C'
	frameReleaseRead        byte = 'L'
	workerBootstrapMode          = "--readbrokerstudy-bootstrap"
	workerHoldBootstrapArg       = "--hold-capability-before-read"
	workerStderrLimit            = 4 << 10
	workerWaitDelay              = 500 * time.Millisecond
	backendProbeTimeout          = 2 * time.Second
	backendHashLimit             = 512 << 20
	backendProbeOutputLimit      = 64 << 10
	maxWorkerSnapshotBytes       = 7 << 20
	maxSCMRightsFDs              = 253
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
	if err != nil {
		result.Error = boundedText("trusted backend capability preflight failed: "+err.Error(), 512)
		return result
	}
	workerPath, err := os.Executable()
	if err != nil {
		result.Error = "running worker source is unavailable"
		return result
	}
	workerFile, err := openCurrentExecutable(workerPath)
	if err != nil {
		result.Error = "running worker source could not be opened"
		return result
	}
	mode, modeErr := selectWorkerBindMode(capabilities, workerFile)
	closeErr := workerFile.Close()
	if modeErr != nil || closeErr != nil {
		result.Error = "trusted Bubblewrap has no compatible executable source mechanism"
		return result
	}
	result.BindMode = mode
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
	return effectiveBubblewrapArguments(mode, false)
}

// EffectiveBubblewrapArgumentsWithCapabilityHold reports the fixed research
// launch arguments used only by owner-lifecycle scenarios.
func EffectiveBubblewrapArgumentsWithCapabilityHold(mode BindMode) []string {
	return effectiveBubblewrapArguments(mode, true)
}

func effectiveBubblewrapArguments(mode BindMode, holdCapability bool) []string {
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
	args = append(args, "--sync-fd", "6", "--chdir", "/", "--", "/worker", workerBootstrapMode,
		"--control-fd", fmt.Sprint(controlFD), "--worker-mode", workerVerificationMode(mode),
		"--worker-fd", fmt.Sprint(workerVerificationFD), "--seccomp-fd", fmt.Sprint(seccompInputFD))
	if holdCapability {
		args = append(args, workerHoldBootstrapArg)
	}
	return args
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
	bwrapPath      string
	capability     BubblewrapCapabilities
	workerFile     *os.File
	holdBeforeRead bool
	closeOnce      sync.Once
	closeErr       error
}

// NewWorkerLauncher constructs the only launch path in this feasibility
// package. Modern Bubblewrap receives the held descriptor for the currently
// executing image through its /proc/self/fd interface and verifies the
// mounted inode before exec. The 0.9 compatibility path uses an immutable byte
// snapshot and descriptor-fed file construction. No user-controlled pathname
// is passed as an executable source.
func NewWorkerLauncher(bwrapPath, workerPath string) (WorkerLauncher, error) {
	return newWorkerLauncher(bwrapPath, workerPath, false)
}

// NewWorkerLauncherWithCapabilityHold enables the fixed bounded hold used by
// owner-lifecycle scenarios. The worker validates and holds one read-only
// descriptor until the coordinator explicitly releases the read or kills it.
func NewWorkerLauncherWithCapabilityHold(bwrapPath, workerPath string) (WorkerLauncher, error) {
	return newWorkerLauncher(bwrapPath, workerPath, true)
}

func newWorkerLauncher(bwrapPath, workerPath string, holdBeforeRead bool) (WorkerLauncher, error) {
	return newWorkerLauncherWithBindMode(bwrapPath, workerPath, holdBeforeRead, "")
}

func newWorkerLauncherWithBindMode(bwrapPath, workerPath string, holdBeforeRead bool, preferredMode BindMode) (WorkerLauncher, error) {
	if err := validateTrustedExecutable(bwrapPath, true); err != nil {
		return nil, fmt.Errorf("%w: trusted Bubblewrap validation failed", ErrUnavailable)
	}
	_, capability, err := probeBubblewrap(bwrapPath)
	if err != nil {
		return nil, fmt.Errorf("%w: Bubblewrap compatibility preflight failed: %v", ErrUnavailable, err)
	}
	workerFile, err := openCurrentExecutable(workerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: running fixed worker could not be opened: %v", ErrUnavailable, err)
	}
	mode, err := selectWorkerBindModePreferred(capability, workerFile, preferredMode)
	if err != nil {
		_ = workerFile.Close()
		return nil, fmt.Errorf("%w: no safe mount source is available: %v", ErrUnavailable, err)
	}
	capability.BindMode = mode
	if mode == BindModeFileSnapshot {
		snapshot, snapshotErr := createSealedWorkerSnapshot(workerFile)
		closeErr := workerFile.Close()
		if snapshotErr != nil || closeErr != nil {
			if snapshot != nil {
				_ = snapshot.Close()
			}
			return nil, fmt.Errorf("%w: running fixed worker could not be snapshotted", ErrUnavailable)
		}
		if err := validateStaticWorkerFile(snapshot); err != nil {
			_ = snapshot.Close()
			return nil, fmt.Errorf("%w: sealed worker snapshot is not static ELF", ErrUnavailable)
		}
		if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
			_ = snapshot.Close()
			return nil, fmt.Errorf("%w: sealed worker snapshot could not be rewound", ErrUnavailable)
		}
		workerFile = snapshot
	}
	return &workerLauncher{bwrapPath: bwrapPath, capability: capability, workerFile: workerFile, holdBeforeRead: holdBeforeRead}, nil
}

// openCurrentExecutable binds the worker source to the already-running study
// image, not a later lookup of the supplied pathname. The pathname is checked
// only as an identity hint; the opened source is /proc/self/exe.
func openCurrentExecutable(workerPath string) (*os.File, error) {
	if workerPath == "" {
		return nil, errors.New("worker path identity hint is empty")
	}
	pathFD, err := unix.Open(workerPath, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("worker path could not be opened for identity comparison")
	}
	pathFile := os.NewFile(uintptr(pathFD), "readbroker-worker-identity-hint")
	if pathFile == nil {
		_ = unix.Close(pathFD)
		return nil, errors.New("worker path identity handle could not be wrapped")
	}
	defer pathFile.Close()
	runningFile, err := os.Open("/proc/self/exe")
	if err != nil {
		return nil, errors.New("the running executable handle is unavailable")
	}
	var pathStat, runningStat unix.Stat_t
	if err := unix.Fstat(int(pathFile.Fd()), &pathStat); err != nil || pathStat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = runningFile.Close()
		return nil, errors.New("worker path identity could not be checked")
	}
	if err := unix.Fstat(int(runningFile.Fd()), &runningStat); err != nil ||
		pathStat.Dev != runningStat.Dev || pathStat.Ino != runningStat.Ino {
		_ = runningFile.Close()
		return nil, errors.New("worker path no longer names the running executable")
	}
	if err := validateStaticWorkerFile(runningFile); err != nil {
		_ = runningFile.Close()
		return nil, err
	}
	return runningFile, nil
}

func snapshotCurrentExecutable(workerPath string) (*os.File, error) {
	runningFile, err := openCurrentExecutable(workerPath)
	if err != nil {
		return nil, err
	}
	defer runningFile.Close()
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

func selectWorkerBindMode(capability BubblewrapCapabilities, source *os.File) (BindMode, error) {
	return selectWorkerBindModePreferred(capability, source, "")
}

func selectWorkerBindModePreferred(capability BubblewrapCapabilities, source *os.File, preferred BindMode) (BindMode, error) {
	if preferred != "" {
		switch preferred {
		case BindModeDescriptor:
			if !slices.Contains(capability.Options, "--ro-bind-fd") {
				return "", errors.New("requested Bubblewrap descriptor-bind mode is not available")
			}
			if !descriptorBindSourcePathCompatible(source) {
				return "", errors.New("requested Bubblewrap descriptor-bind mode cannot resolve the fixed worker source")
			}
			return BindModeDescriptor, nil
		case BindModeFileSnapshot:
			if !supportsFileSnapshot(capability.Options) {
				return "", errors.New("requested Bubblewrap file-snapshot mode is not available")
			}
			return BindModeFileSnapshot, nil
		default:
			return "", errors.New("requested Bubblewrap bind mode is invalid")
		}
	}
	if capability.BindMode == BindModeDescriptor && descriptorBindSourcePathCompatible(source) {
		return BindModeDescriptor, nil
	}
	if supportsFileSnapshot(capability.Options) {
		return BindModeFileSnapshot, nil
	}
	if capability.BindMode == BindModeDescriptor {
		return "", errors.New("Bubblewrap descriptor bind cannot resolve this executable source and descriptor-fed file construction is unavailable")
	}
	if capability.BindMode == BindModeFileSnapshot {
		return BindModeFileSnapshot, nil
	}
	return "", ErrUnavailable
}

func supportsFileSnapshot(options []string) bool {
	return slices.Contains(options, "--ro-bind-data") && slices.Contains(options, "--perms")
}

// Bubblewrap's --ro-bind-fd resolves /proc/self/fd/N to a host pathname,
// mounts that path, then compares the mounted object's device/inode with the
// retained descriptor before exec. Require that the trusted input can resolve
// to its existing inode; anonymous memfds need the descriptor-fed copy mode.
func descriptorBindSourcePathCompatible(source *os.File) bool {
	if source == nil {
		return false
	}
	fdPath := fmt.Sprintf("/proc/self/fd/%d", source.Fd())
	resolved, err := filepath.EvalSymlinks(fdPath)
	if err != nil {
		return false
	}
	pathInfo, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	var fdInfo unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &fdInfo); err != nil {
		return false
	}
	pathStat, ok := pathInfo.Sys().(*syscall.Stat_t)
	return ok && uint64(pathStat.Dev) == uint64(fdInfo.Dev) && pathStat.Ino == fdInfo.Ino
}

func openWorkerMountDescriptor(source *os.File, mode BindMode) (*os.File, error) {
	if mode == BindModeFileSnapshot {
		return openSealedWorkerSnapshot(source)
	}
	if mode != BindModeDescriptor {
		return nil, errors.New("worker descriptor bind mode is unavailable")
	}
	if !descriptorBindSourcePathCompatible(source) {
		return nil, errors.New("descriptor-bind worker source has no stable host pathname")
	}
	var expected unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &expected); err != nil || expected.Mode&unix.S_IFMT != unix.S_IFREG ||
		expected.Size <= 0 || expected.Size > maxWorkerSnapshotBytes {
		return nil, errors.New("descriptor-bind worker source identity is invalid")
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", source.Fd()), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("fresh descriptor-bind worker source could not be opened (%s)", safeSystemError(err))
	}
	file := os.NewFile(uintptr(fd), "readbroker-worker-mount-source")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("descriptor-bind worker source could not be wrapped")
	}
	var actual unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err := unix.Fstat(fd, &actual); err != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino ||
		actual.Size != expected.Size || flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		_ = file.Close()
		return nil, errors.New("fresh descriptor-bind source failed identity validation")
	}
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		_ = file.Close()
		return nil, errors.New("fresh descriptor-bind source is not at offset zero")
	}
	return file, nil
}

// safeSystemError reports only a fixed errno description, never the path text
// carried by os.PathError or another host-specific wrapper.
func safeSystemError(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	return "system call failed"
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
	holdMode bool
	held     bool
	released bool
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
	mountFile, err := openWorkerMountDescriptor(l.workerFile, l.capability.BindMode)
	if err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: worker mount snapshot could not be reopened: %s", ErrUnavailable, err.Error())
	}
	defer mountFile.Close()
	verificationFile, err := openWorkerMountDescriptor(l.workerFile, l.capability.BindMode)
	if err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		return nil, fmt.Errorf("%w: worker verification snapshot could not be reopened: %s", ErrUnavailable, err.Error())
	}
	defer verificationFile.Close()
	watchdog := time.Duration(workerDeadline)
	if l.holdBeforeRead {
		watchdog = time.Duration(capabilityHoldWorkerDeadline)
	}
	workerCtx, cancel := context.WithTimeout(ctx, watchdog)
	args := effectiveBubblewrapArguments(l.capability.BindMode, l.holdBeforeRead)
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
	worker := &sandboxWorker{cmd: cmd, control: parentSocket, ctx: workerCtx, cancel: cancel, stderr: stderr, holdMode: l.holdBeforeRead}
	if l.holdBeforeRead {
		return &heldSandboxWorker{sandboxWorker: worker}, nil
	}
	return worker, nil
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

// heldSandboxWorker exposes the optional hold protocol only for launchers
// explicitly configured for the owner-lifecycle experiment. Ordinary workers
// therefore retain the established SandboxWorker contract.
type heldSandboxWorker struct{ *sandboxWorker }

func (w *heldSandboxWorker) AwaitCapabilityHeld(ctx context.Context) error {
	w.mu.Lock()
	if !w.holdMode || !w.sent || w.held || w.started || w.finished {
		w.mu.Unlock()
		return ErrProtocol
	}
	expected := w.expected
	w.mu.Unlock()
	packet, err := recvPacket(ctx, int(w.control.Fd()), 64)
	if err != nil {
		return fmt.Errorf("%w: held-capability acknowledgement unavailable", ErrUnavailable)
	}
	if len(packet) != 1+20 || packet[0] != frameCapabilityHeld || !sameIdentity(packet[1:], expected) {
		return ErrStaleReceipt
	}
	w.mu.Lock()
	w.held = true
	w.mu.Unlock()
	return nil
}

func (w *heldSandboxWorker) HeldCapabilityIdentity() (FileIdentity, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.expected, w.holdMode && w.held
}

func (w *heldSandboxWorker) ReleaseHeldRead(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.holdMode || !w.held || w.released || w.finished || ctx == nil {
		return ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sendPacket(int(w.control.Fd()), identityFrame(frameReleaseRead, w.expected)); err != nil {
		return err
	}
	w.released = true
	return nil
}

func (w *sandboxWorker) AwaitReadStarted(ctx context.Context) error {
	w.mu.Lock()
	if !w.sent || w.started || w.finished || (w.holdMode && (!w.held || !w.released)) {
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
				result.Truncated = true
				result.Detail = "result truncated at the fixed read limit"
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

// ObserveHostProcesses is used by the independent research supervisor while
// this worker is alive. It identifies Bubblewrap and its PID-namespace init
// through host procfs without exposing procfs to the sandboxed worker.
func (w *sandboxWorker) ObserveHostProcesses() (HostProcessIdentity, HostProcessIdentity, error) {
	w.mu.Lock()
	if w.cmd == nil || w.cmd.Process == nil {
		w.mu.Unlock()
		return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("Bubblewrap process is unavailable")
	}
	pid := w.cmd.Process.Pid
	w.mu.Unlock()
	return observeBubblewrapProcesses(pid)
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
	holdCapability := false
	validArgs := len(os.Args) == 10 || (len(os.Args) == 11 && os.Args[10] == workerHoldBootstrapArg)
	if !validArgs || os.Args[1] != workerBootstrapMode || os.Args[2] != "--control-fd" || os.Args[3] != "4" ||
		os.Args[4] != "--worker-mode" || os.Args[6] != "--worker-fd" || os.Args[7] != "6" ||
		os.Args[8] != "--seccomp-fd" || os.Args[9] != "3" {
		return 70
	}
	holdCapability = len(os.Args) == 11
	var verifyErr error
	switch os.Args[5] {
	case "descriptor":
		verifyErr = verifyDescriptorMounts([]DescriptorMount{{FD: workerVerificationFD, Destination: "/worker"}})
	case "snapshot":
		verifier := os.NewFile(workerVerificationFD, "readbroker-worker-snapshot-verifier")
		if verifier == nil {
			_ = unix.Close(workerVerificationFD)
			return 70
		}
		verifyErr = verifySnapshotExecutable(verifier, "/worker")
	default:
		return 70
	}
	if verifyErr != nil {
		return 70
	}
	if err := installWorkerSeccomp(seccompInputFD); err != nil {
		return 70
	}
	return workerMainFD(controlFD, holdCapability)
}

const workerMountFD = 5
const workerVerificationFD = 6

type DescriptorMount struct {
	FD          int
	Destination string
}

// verifySnapshotExecutable consumes verifier and checks that Bubblewrap's
// private-root file contains the exact bytes from the sealed host snapshot.
// Taking the *os.File makes the ownership transfer explicit so a finalizer
// cannot later close a descriptor number already closed and reused by raw
// unix.Close. The trust anchor is the already-running parent image plus the
// sealed descriptor and trusted Bubblewrap file-copy operation, not this
// post-exec comparison by itself.
func verifySnapshotExecutable(verifier *os.File, destination string) (result error) {
	if verifier == nil {
		return ErrProtocol
	}
	defer func() {
		if err := verifier.Close(); err != nil && result == nil {
			result = err
		}
	}()
	fd := int(verifier.Fd())
	if fd <= 2 || destination == "" || destination[0] != '/' {
		return ErrProtocol
	}
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

func workerMainFD(fd int, holdCapability bool) int {
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
	return receiveAndRead(fd, holdCapability)
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

func receiveAndRead(control int, holdCapability bool) int {
	packet := make([]byte, 64)
	// Linux limits one SCM_RIGHTS message to SCM_MAX_FD (253). Receive enough
	// ancillary space to account for every descriptor so even an over-counted
	// frame can be rejected after closing all descriptors the kernel installed.
	oob := make([]byte, unix.CmsgSpace(maxSCMRightsFDs*4))
	for {
		if waitFD(context.Background(), control, unix.POLLIN) != nil {
			return 72
		}
		n, oobn, flags, _, err := unix.Recvmsg(control, packet, oob, unix.MSG_DONTWAIT|unix.MSG_CMSG_CLOEXEC)
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		oobBytes := oob[:min(max(oobn, 0), len(oob))]
		files, parseErr := parseExactlyOneReceivedFD(oobBytes)
		if parseErr != nil {
			return sendRefusal(control)
		}
		capability := files[0]
		if n != 1+20 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || packet[0] != frameHandoff {
			_ = unix.Close(capability)
			return sendRefusal(control)
		}
		// A SOCK_SEQPACKET frame is exactly 21 bytes after validation. Slice the
		// identity to the received length, not the reusable packet capacity.
		result := readTransferredFD(control, capability, packet[1:n], holdCapability)
		_ = unix.Close(capability)
		return result
	}
}

func parseExactlyOneReceivedFD(oob []byte) ([]int, error) {
	messages, parseErr := unix.ParseSocketControlMessage(oob)
	var received []int
	valid := parseErr == nil && len(messages) == 1
	for i := range messages {
		if messages[i].Header.Level != unix.SOL_SOCKET || messages[i].Header.Type != unix.SCM_RIGHTS {
			valid = false
			continue
		}
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

func readTransferredFD(control, fd int, expected []byte, holdCapability bool) int {
	unix.CloseOnExec(fd)
	var stat unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || unix.Fstat(fd, &stat) != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || !sameIdentity(expected, identityFromStat(&stat)) {
		_ = sendPacket(control, []byte{frameRefused})
		return 74
	}
	identity := identityFromStat(&stat)
	if holdCapability {
		if err := sendPacket(control, identityFrame(frameCapabilityHeld, identity)); err != nil {
			return 75
		}
		packet, err := recvPacket(context.Background(), control, 64)
		if err != nil || len(packet) != 1+20 || packet[0] != frameReleaseRead || !sameIdentity(packet[1:], identity) {
			return sendRefusal(control)
		}
	}
	if err := sendPacket(control, identityFrame(frameStarted, identity)); err != nil {
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
