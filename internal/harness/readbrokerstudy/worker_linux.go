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
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	frameReady        byte = 'R'
	frameHandoff      byte = 'H'
	frameStarted      byte = 'S'
	frameRefused      byte = 'X'
	frameData         byte = 'D'
	frameFinish       byte = 'F'
	workerMode             = "--readbrokerstudy-worker"
	workerStderrLimit      = 4 << 10
	workerWaitDelay        = 500 * time.Millisecond
)

type workerLauncher struct {
	bwrapPath  string
	workerFile *os.File
	closeOnce  sync.Once
	closeErr   error
}

// NewWorkerLauncher constructs the only launch path in this feasibility
// package. It requires an absolute root-owned Bubblewrap executable and a
// statically linked fixed-purpose worker; it never chooses a weaker fallback.
func NewWorkerLauncher(bwrapPath, workerPath string) (WorkerLauncher, error) {
	if err := validateTrustedExecutable(bwrapPath, true); err != nil {
		return nil, fmt.Errorf("%w: trusted Bubblewrap validation failed", ErrUnavailable)
	}
	if err := requireBindFDOption(bwrapPath); err != nil {
		return nil, fmt.Errorf("%w: trusted Bubblewrap lacks required descriptor-bind support", ErrUnavailable)
	}
	if err := validateTrustedExecutable(workerPath, false); err != nil {
		return nil, fmt.Errorf("%w: fixed worker validation failed", ErrUnavailable)
	}
	workerFile, err := os.Open(workerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: fixed worker could not be pinned", ErrUnavailable)
	}
	if err := validateStaticWorkerFile(workerFile); err != nil {
		_ = workerFile.Close()
		return nil, fmt.Errorf("%w: fixed worker validation failed", ErrUnavailable)
	}
	return &workerLauncher{bwrapPath: bwrapPath, workerFile: workerFile}, nil
}

func requireBindFDOption(path string) error {
	output, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ErrUnavailable
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 || fields[0] != "bubblewrap" {
		return ErrUnavailable
	}
	version := strings.Split(fields[1], ".")
	if len(version) < 2 {
		return ErrUnavailable
	}
	major, majorErr := strconv.Atoi(version[0])
	minor, minorErr := strconv.Atoi(version[1])
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return ErrUnavailable
	}
	if major == 0 && minor < 10 {
		return ErrUnavailable
	}
	return nil
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
	workerCtx, cancel := context.WithTimeout(ctx, time.Duration(workerDeadline))
	args := []string{
		"--die-with-parent", "--new-session",
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--hostname", "komposyn-readbroker", "--unshare-net",
		"--disable-userns", "--cap-drop", "ALL", "--clearenv",
		"--size", "8388608", "--tmpfs", "/",
		"--ro-bind-fd", "5", "/worker",
		"--seccomp", fmt.Sprint(seccompInputFD),
		"--chdir", "/", "--", "/worker", workerMode,
		"--control-fd", fmt.Sprint(controlFD),
	}
	cmd := exec.CommandContext(workerCtx, l.bwrapPath, args...)
	cmd.Dir = "/"
	cmd.Env = []string{"KOMPOSYN_READBROKER_SENTINEL=synthetic-only"}
	cmd.ExtraFiles = []*os.File{filter, childSocket, l.workerFile}
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
		if w.cmd != nil {
			w.waitErr = w.cmd.Wait()
		}
		if w.control != nil {
			_ = w.control.Close()
		}
		if w.cancel != nil {
			w.cancel()
		}
	})
	return w.waitErr
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

// WorkerMain is selected only by the study command's private worker mode. It
// reads no path and accepts no shell or arbitrary payload. The sole protocol
// descriptor is fd 4; an approved read-only file descriptor arrives later via
// SCM_RIGHTS.
func WorkerMain() int {
	if len(os.Args) != 4 || os.Args[1] != workerMode || os.Args[2] != "--control-fd" || os.Args[3] != "4" {
		return 70
	}
	return workerMainFD(controlFD)
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
	probe := WorkerProbe{Details: []string{"required namespaces requested: user,pid,ipc,uts,net (no try/fallback flags)", "bubblewrap mount namespace created with empty tmpfs root"}}
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
