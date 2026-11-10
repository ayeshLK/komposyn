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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestBubblewrapCapabilitySelection(t *testing.T) {
	help := bubblewrapHelp("--ro-bind-fd")
	version, modern, err := bubblewrapCapabilities("bubblewrap 0.10.0\n", help)
	if err != nil || version != "bubblewrap 0.10.0" || modern.BindMode != BindModeDescriptor {
		t.Fatalf("modern capability selection: version=%q mode=%q err=%v", version, modern.BindMode, err)
	}
	_, legacy, err := bubblewrapCapabilities("bubblewrap 0.9.0\n", bubblewrapHelp("--ro-bind-data", "--perms"))
	if err != nil || legacy.BindMode != BindModeFileSnapshot {
		t.Fatalf("secure 0.9 file-snapshot selection: mode=%q err=%v", legacy.BindMode, err)
	}
	if !slices.Contains(legacy.Options, "--as-pid-1") || !slices.Contains(legacy.Options, "--sync-fd") {
		t.Fatalf("legacy capabilities missing from report: %#v", legacy.Options)
	}
	_, incomplete, err := bubblewrapCapabilities("bubblewrap 0.9.0\n", bubblewrapHelp("--ro-bind-data"))
	if !errors.Is(err, ErrUnavailable) || incomplete.BindMode != "" {
		t.Fatalf("read-only data snapshot without explicit permissions must be refused: mode=%q err=%v", incomplete.BindMode, err)
	}
}

func TestReceiveAndReadUsesExactReceivedFrameAndReadOnlyDescriptor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handoff.fixture")
	want := bytes.Repeat([]byte("bounded synthetic read\n"), MaxReadBytes/23+2)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	identity, err := identityFromFile(file)
	if err != nil {
		t.Fatal(err)
	}

	frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameHandoff, identity), []*os.File{file})
	if exitCode != 0 || len(frames) < 3 || frames[0][0] != frameStarted || !sameIdentity(frames[0][1:], identity) {
		t.Fatalf("worker startup/read frames=%q exit=%d, want read-start and bounded result", frameKinds(frames), exitCode)
	}
	var got []byte
	var finish []byte
	for _, frame := range frames[1:] {
		switch frame[0] {
		case frameData:
			got = append(got, frame[1:]...)
		case frameFinish:
			finish = frame
		default:
			t.Fatalf("unexpected worker result frame kind %q", frame[0])
		}
	}
	if len(finish) != 7 || finish[1] != 'S' || finish[2] != 1 || binary.BigEndian.Uint32(finish[3:7]) != MaxReadBytes {
		t.Fatalf("finish frame=%v, want succeeded/truncated with %d bytes", finish, MaxReadBytes)
	}
	if len(got) != MaxReadBytes || !bytes.Equal(got, want[:MaxReadBytes]) {
		t.Fatalf("read result bytes=%d match=%t, want first bounded %d bytes", len(got), bytes.Equal(got, want[:MaxReadBytes]), MaxReadBytes)
	}
	if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
		t.Fatalf("open descriptors for target after worker return=%d, want only original descriptor", gotFDs)
	}
}

func TestReceiveAndReadRefusesMalformedFramesAndClosesReceivedDescriptors(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func([]byte) []byte
		fdCopies int
		wantCode int
	}{
		{name: "short handoff", mutate: func(frame []byte) []byte { return frame[:len(frame)-1] }, fdCopies: 1, wantCode: 73},
		{name: "truncated handoff", mutate: func(frame []byte) []byte { return append(frame, bytes.Repeat([]byte{'x'}, 64)...) }, fdCopies: 1, wantCode: 73},
		{name: "wrong frame kind", mutate: func(frame []byte) []byte { frame[0] = 'Z'; return frame }, fdCopies: 1, wantCode: 73},
		{name: "mismatched identity", mutate: func(frame []byte) []byte { frame[1] ^= 1; return frame }, fdCopies: 1, wantCode: 74},
		{name: "multiple descriptors", mutate: func(frame []byte) []byte { return frame }, fdCopies: 2, wantCode: 73},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, identity := openSyntheticReadTarget(t)
			defer file.Close()
			frame := test.mutate(identityFrame(frameHandoff, identity))
			descriptors := make([]*os.File, test.fdCopies)
			for i := range descriptors {
				descriptors[i] = file
			}
			frames, exitCode := exchangeWorkerFrame(t, frame, descriptors)
			if exitCode != test.wantCode || len(frames) != 1 || len(frames[0]) != 1 || frames[0][0] != frameRefused {
				t.Fatalf("refusal frames=%q exit=%d, want X/%d", frameKinds(frames), exitCode, test.wantCode)
			}
			if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
				t.Fatalf("malformed-frame handling leaked target descriptors: open=%d", gotFDs)
			}
		})
	}
}

func TestReceiveAndReadRefusesWritableAndNonRegularDescriptors(t *testing.T) {
	t.Run("writable descriptor", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "writable.fixture")
		if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		identity, err := identityFromFile(file)
		if err != nil {
			t.Fatal(err)
		}
		frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameHandoff, identity), []*os.File{file})
		if exitCode != 74 || len(frames) != 1 || frames[0][0] != frameRefused {
			t.Fatalf("writable descriptor response=%q exit=%d, want refusal/74", frameKinds(frames), exitCode)
		}
		if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
			t.Fatalf("writable descriptor refusal leaked FD: open=%d", gotFDs)
		}
	})

	t.Run("directory descriptor", func(t *testing.T) {
		directory, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		identity, err := identityFromFile(directory)
		if err != nil {
			t.Fatal(err)
		}
		frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameHandoff, identity), []*os.File{directory})
		if exitCode != 74 || len(frames) != 1 || frames[0][0] != frameRefused {
			t.Fatalf("directory descriptor response=%q exit=%d, want refusal/74", frameKinds(frames), exitCode)
		}
		if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
			t.Fatalf("directory descriptor refusal leaked FD: open=%d", gotFDs)
		}
	})
}

func openSyntheticReadTarget(t *testing.T) (*os.File, FileIdentity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target.fixture")
	if err := os.WriteFile(path, []byte("synthetic SCM_RIGHTS fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identityFromFile(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return file, identity
}

func exchangeWorkerFrame(t *testing.T, frame []byte, files []*os.File) ([][]byte, int) {
	t.Helper()
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sockets[0])
	workerControl := os.NewFile(uintptr(sockets[1]), "worker-control")
	defer workerControl.Close()
	done := make(chan int, 1)
	go func() { done <- receiveAndRead(int(workerControl.Fd()), false) }()
	var rights []int
	for _, file := range files {
		rights = append(rights, int(file.Fd()))
	}
	var oob []byte
	if len(rights) > 0 {
		oob = unix.UnixRights(rights...)
	}
	n, err := unix.SendmsgN(sockets[0], frame, oob, nil, unix.MSG_NOSIGNAL)
	if err != nil || n != len(frame) {
		t.Fatalf("send handoff frame bytes=%d err=%v", n, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	frames := make([][]byte, 0, 4)
	first, err := recvPacket(ctx, sockets[0], 64)
	if err != nil {
		t.Fatalf("receive worker response: %v", err)
	}
	frames = append(frames, first)
	if len(first) == 0 || first[0] == frameRefused {
		return frames, waitWorkerExitCode(t, done)
	}
	if first[0] != frameStarted {
		t.Fatalf("first worker response=%q, want start/refusal", first)
	}
	for {
		packet, recvErr := recvPacket(ctx, sockets[0], dataChunkBytes+16)
		if recvErr != nil {
			t.Fatalf("receive worker data frame: %v", recvErr)
		}
		frames = append(frames, packet)
		if len(packet) > 0 && packet[0] == frameFinish {
			break
		}
	}
	return frames, waitWorkerExitCode(t, done)
}

func TestHeldCapabilityProtocolDoesNotReadBeforeRelease(t *testing.T) {
	file, identity := openSyntheticReadTarget(t)
	defer file.Close()
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sockets[0])
	defer unix.Close(sockets[1])
	workerFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(workerFD)
	done := make(chan int, 1)
	go func() {
		done <- readTransferredFD(sockets[1], workerFD, identityFrame(frameHandoff, identity)[1:], true)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	held, err := recvPacket(ctx, sockets[0], 64)
	if err != nil || len(held) != 21 || held[0] != frameCapabilityHeld || !sameIdentity(held[1:], identity) {
		t.Fatalf("held acknowledgement=%x err=%v", held, err)
	}
	var poll []unix.PollFd = []unix.PollFd{{Fd: int32(sockets[0]), Events: unix.POLLIN}}
	if ready, err := unix.Poll(poll, 0); err != nil || ready != 0 {
		t.Fatalf("worker produced read data before release: ready=%d revents=%#x err=%v", ready, poll[0].Revents, err)
	}
	if err := sendPacket(sockets[0], identityFrame(frameReleaseRead, identity)); err != nil {
		t.Fatal(err)
	}
	started, err := recvPacket(ctx, sockets[0], 64)
	if err != nil || len(started) != 21 || started[0] != frameStarted || !sameIdentity(started[1:], identity) {
		t.Fatalf("read-started acknowledgement=%x err=%v", started, err)
	}
	data, err := recvPacket(ctx, sockets[0], dataChunkBytes+16)
	if err != nil || len(data) < 2 || data[0] != frameData {
		t.Fatalf("read data frame=%x err=%v", data, err)
	}
	finish, err := recvPacket(ctx, sockets[0], 64)
	if err != nil || len(finish) != 7 || finish[0] != frameFinish || finish[1] != encodeOutcome(ReadSucceeded) {
		t.Fatalf("read finish frame=%x err=%v", finish, err)
	}
	select {
	case exit := <-done:
		if exit != 0 {
			t.Fatalf("held worker exit=%d, want 0", exit)
		}
	case <-ctx.Done():
		t.Fatal("held worker did not exit after bounded release")
	}
}

func waitWorkerExitCode(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(3 * time.Second):
		t.Fatal("receiveAndRead did not return")
		return -1
	}
}

func frameKinds(frames [][]byte) string {
	var kinds strings.Builder
	for _, frame := range frames {
		if len(frame) == 0 {
			kinds.WriteByte('?')
		} else {
			kinds.WriteByte(frame[0])
		}
	}
	return kinds.String()
}

func countOpenIdentityFDs(t *testing.T, identity FileIdentity) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		fd, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil {
			continue
		}
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) == nil && identityFromStat(&stat) == identity {
			count++
		}
	}
	return count
}

func TestModernDescriptorModeRequiresResolvableSourceAndFallsBackToDataCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker-source")
	if err := os.WriteFile(path, []byte("path-backed running image"), 0o500); err != nil {
		t.Fatal(err)
	}
	pathSource, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pathSource.Close()
	modern := BubblewrapCapabilities{
		Options: []string{"--ro-bind-fd", "--ro-bind-data", "--perms"}, BindMode: BindModeDescriptor,
	}
	if !descriptorBindSourcePathCompatible(pathSource) {
		t.Fatal("ordinary path-backed descriptor should resolve for Bubblewrap's modern mount path")
	}
	if mode, err := selectWorkerBindMode(modern, pathSource); err != nil || mode != BindModeDescriptor {
		t.Fatalf("modern descriptor path selection: mode=%q err=%v", mode, err)
	}

	snapshot, err := createSealedWorkerSnapshot(pathSource)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if descriptorBindSourcePathCompatible(snapshot) {
		t.Fatal("anonymous memfd must not be selected for Bubblewrap's realpath-based descriptor bind")
	}
	if descriptorSource, err := openWorkerMountDescriptor(snapshot, BindModeDescriptor); err == nil {
		_ = descriptorSource.Close()
		t.Fatal("anonymous memfd was accepted directly by the realpath-based descriptor mode")
	}
	if mode, err := selectWorkerBindMode(modern, snapshot); err != nil || mode != BindModeFileSnapshot {
		t.Fatalf("memfd source did not select descriptor-fed byte copy: mode=%q err=%v", mode, err)
	}
	modernWithoutCopy := BubblewrapCapabilities{
		Options: []string{"--ro-bind-fd"}, BindMode: BindModeDescriptor,
	}
	if mode, err := selectWorkerBindMode(modernWithoutCopy, snapshot); err == nil || mode != "" {
		t.Fatalf("unresolvable memfd source did not fail closed: mode=%q err=%v", mode, err)
	}
}

func TestStage6PreferredBindModeIsFeatureCheckedWithoutChangingAutomaticSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker-source")
	if err := os.WriteFile(path, []byte("synthetic worker"), 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	modern := BubblewrapCapabilities{
		Options: []string{"--ro-bind-fd", "--ro-bind-data", "--perms"}, BindMode: BindModeDescriptor,
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, ""); err != nil || mode != BindModeDescriptor {
		t.Fatalf("automatic mode changed: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, BindModeFileSnapshot); err != nil || mode != BindModeFileSnapshot {
		t.Fatalf("explicit snapshot mode not selected: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, BindModeDescriptor); err != nil || mode != BindModeDescriptor {
		t.Fatalf("explicit descriptor mode not selected: mode=%q err=%v", mode, err)
	}
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if mode, err := selectWorkerBindModePreferred(modern, snapshot, BindModeDescriptor); err == nil || mode != "" {
		t.Fatalf("descriptor mode accepted a source without a resolvable host path: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, snapshot, BindModeFileSnapshot); err != nil || mode != BindModeFileSnapshot {
		t.Fatalf("explicit snapshot mode rejected a sealed image: mode=%q err=%v", mode, err)
	}
	withoutPermissions := BubblewrapCapabilities{Options: []string{"--ro-bind-data"}}
	if mode, err := selectWorkerBindModePreferred(withoutPermissions, source, BindModeFileSnapshot); err == nil || mode != "" {
		t.Fatalf("snapshot mode without explicit permission support was accepted: mode=%q err=%v", mode, err)
	}
	withoutDescriptor := BubblewrapCapabilities{Options: []string{"--ro-bind-data", "--perms"}}
	if mode, err := selectWorkerBindModePreferred(withoutDescriptor, source, BindModeDescriptor); err == nil || mode != "" {
		t.Fatalf("descriptor mode without descriptor support was accepted: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, BindMode("unknown")); err == nil || mode != "" {
		t.Fatalf("unknown explicit mode was accepted: mode=%q err=%v", mode, err)
	}
}

func TestSafeSystemErrorOmitsPathText(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/tmp/synthetic-private-path", Err: syscall.EACCES}
	got := safeSystemError(err)
	if got != syscall.EACCES.Error() || strings.Contains(got, "/tmp/") {
		t.Fatalf("safe system error=%q, want errno only", got)
	}
	if got := safeSystemError(errors.New("host-specific path diagnostic")); got != "system call failed" {
		t.Fatalf("unknown system error=%q, want generic classification", got)
	}
}

func TestModernDescriptorBindUsesFreshPathBackedDescriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker")
	want := []byte("pinned path-backed worker")
	if err := os.WriteFile(path, want, 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	first, err := openWorkerMountDescriptor(source, BindModeDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openWorkerMountDescriptor(source, BindModeDescriptor)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	var prefix [4]byte
	if _, err := first.Read(prefix[:]); err != nil {
		t.Fatal(err)
	}
	if offset, err := second.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("descriptor source shared offsets: offset=%d err=%v", offset, err)
	}
	var got []byte
	got, err = io.ReadAll(second)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("fresh descriptor did not read pinned object: got=%q err=%v", got, err)
	}
	_ = first.Close()
	_ = second.Close()
}

func TestRunningExecutableCanBeReopenedForDescriptorBind(t *testing.T) {
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatalf("running test executable could not be opened: %v", err)
	}
	defer source.Close()
	opened, err := openWorkerMountDescriptor(source, BindModeDescriptor)
	if err != nil {
		t.Fatalf("pinned running executable could not be reopened for descriptor binding: %v", err)
	}
	defer opened.Close()
	var sourceStat, openedStat unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &sourceStat); err != nil {
		t.Fatal("pinned executable identity could not be read")
	}
	if err := unix.Fstat(int(opened.Fd()), &openedStat); err != nil {
		t.Fatal("reopened executable identity could not be read")
	}
	if sourceStat.Dev != openedStat.Dev || sourceStat.Ino != openedStat.Ino {
		t.Fatal("descriptor bind reopen did not preserve the running executable identity")
	}
}

func TestBubblewrapArgumentsSelectModernMountAndSeparateVerifierFD(t *testing.T) {
	modern := strings.Join(EffectiveBubblewrapArguments(BindModeDescriptor), " ")
	if !strings.Contains(modern, "--ro-bind-fd 5 /worker") || strings.Contains(modern, "/proc/self/fd") {
		t.Fatalf("unexpected modern mount arguments: %s", modern)
	}
	for _, value := range []string{"--as-pid-1", "--sync-fd 6", "--readbrokerstudy-bootstrap", "--worker-fd 6", "--seccomp-fd 3", "--size 16777216"} {
		if !strings.Contains(modern, value) {
			t.Fatalf("modern arguments missing %q: %s", value, modern)
		}
	}
	if workerMountFD == workerVerificationFD {
		t.Fatal("mount source and retained verifier descriptors must be distinct")
	}
	snapshot := strings.Join(EffectiveBubblewrapArguments(BindModeFileSnapshot), " ")
	for _, value := range []string{"--perms 0500 --ro-bind-data 5 /worker", "--sync-fd 6", "--worker-mode snapshot", "--size 16777216"} {
		if !strings.Contains(snapshot, value) {
			t.Fatalf("Bubblewrap 0.9 snapshot arguments missing %q: %s", value, snapshot)
		}
	}
	if strings.Contains(snapshot, "/proc/self/fd") {
		t.Fatalf("file snapshot mode must not resolve a source pathname: %s", snapshot)
	}
	if len(EffectiveBubblewrapArguments("unknown")) != 0 {
		t.Fatal("unknown bind mode must produce no launch arguments")
	}
}

func TestCapabilityHoldBootstrapArgumentIsOptIn(t *testing.T) {
	normal := strings.Join(effectiveBubblewrapArguments(BindModeDescriptor, false), " ")
	held := strings.Join(effectiveBubblewrapArguments(BindModeDescriptor, true), " ")
	if strings.Contains(normal, workerHoldBootstrapArg) {
		t.Fatalf("normal worker unexpectedly enables hold mode: %s", normal)
	}
	if !strings.HasSuffix(held, workerHoldBootstrapArg) {
		t.Fatalf("held worker lacks the exact fixed bootstrap option: %s", held)
	}
}

func TestCapabilityHoldInterfaceIsOptIn(t *testing.T) {
	ordinary := any(&sandboxWorker{})
	if _, ok := ordinary.(HeldCapabilityWorker); ok {
		t.Fatal("ordinary worker unexpectedly exposes the owner-lifecycle hold protocol")
	}
	held := any(&heldSandboxWorker{sandboxWorker: &sandboxWorker{holdMode: true}})
	if _, ok := held.(HeldCapabilityWorker); !ok {
		t.Fatal("explicit held worker does not expose its fixed hold protocol")
	}
}

func TestModernBindConsumptionLeavesIndependentVerifierDescriptor(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "worker")
	if err := os.WriteFile(path, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	mountFD, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	verificationFD, err := unix.Dup(mountFD)
	if err != nil {
		_ = unix.Close(mountFD)
		t.Fatal(err)
	}
	if mountFD == verificationFD {
		t.Fatal("descriptor duplication reused the mount FD")
	}
	// Bubblewrap's --ro-bind-fd consumes the mount source after setting up the
	// mount. The bootstrap must rely on the separately duplicated FD instead.
	if err := unix.Close(mountFD); err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: verificationFD, Destination: path}}); err != nil {
		t.Fatalf("modern bootstrap could not verify after mount FD consumption: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(verificationFD), unix.F_GETFD, 0); err == nil {
		t.Fatal("retained verification descriptor survived bootstrap verification")
	}
}

func TestSealedSnapshotSurvivesSourcePathAndAncestorReplacement(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "selected", "bin")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(parent, "worker")
	want := []byte("reviewed executable snapshot")
	if err := os.WriteFile(sourcePath, want, 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	seals, err := unix.FcntlInt(snapshot.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals&(unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL) !=
		unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL {
		t.Fatalf("snapshot is not immutable: seals=%#x err=%v", seals, err)
	}
	writeFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", snapshot.Fd()), unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open sealed memfd for rejected-write check: %v", err)
	}
	if _, err := unix.Pwrite(writeFD, []byte("x"), 0); !errors.Is(err, unix.EPERM) {
		_ = unix.Close(writeFD)
		t.Fatalf("sealed snapshot accepted a write: %v", err)
	}
	if err := unix.Close(writeFD); err != nil {
		t.Fatal(err)
	}

	movedParent := filepath.Join(base, "moved")
	if err := os.Rename(filepath.Join(base, "selected"), movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("replacement at original pathname"), 0o500); err != nil {
		t.Fatal(err)
	}
	movedPath := filepath.Join(movedParent, "bin", "worker")
	if err := os.Chmod(movedPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movedPath, []byte("same inode changed after snapshot"), 0o500); err != nil {
		t.Fatal(err)
	}

	// Simulate Bubblewrap's descriptor-fed copy after the source pathname and
	// its ancestor have both been replaced. Only the sealed descriptor is used.
	mountInput, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := t.TempDir()
	workerPath := filepath.Join(privateRoot, "worker")
	destination, err := os.Create(workerPath)
	if err != nil {
		_ = mountInput.Close()
		t.Fatal(err)
	}
	copied, copyErr := io.CopyN(destination, mountInput, int64(len(want)))
	closeInputErr := mountInput.Close()
	closeDestinationErr := destination.Close()
	if copyErr != nil || closeInputErr != nil || closeDestinationErr != nil || copied != int64(len(want)) {
		t.Fatalf("snapshot copy failed: copied=%d copy=%v input_close=%v destination_close=%v", copied, copyErr, closeInputErr, closeDestinationErr)
	}
	if err := os.Chmod(workerPath, 0o500); err != nil {
		t.Fatal(err)
	}
	verifier, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	verifierFD := int(verifier.Fd())
	if err := verifySnapshotExecutable(verifier, workerPath); err != nil {
		t.Fatalf("copied private worker did not match sealed source: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(verifierFD), unix.F_GETFD, 0); err == nil {
		t.Fatal("snapshot verifier descriptor was not closed")
	}
	if got, err := os.ReadFile(workerPath); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("path/ancestor replacement changed copied worker: %q err=%v", got, err)
	}
}

func TestSnapshotCurrentExecutableRejectsReplacementPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(path, []byte("replacement executable"), 0o500); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := snapshotCurrentExecutable(path); err == nil {
		_ = snapshot.Close()
		t.Fatal("replacement pathname was accepted as the running executable")
	}
}

func TestExecutableIdentityHintFIFOIsNonblocking(t *testing.T) {
	const childPath = "KOMPOSYN_READBROKER_FIFO_IDENTITY_HINT"
	if fifoPath := os.Getenv(childPath); fifoPath != "" {
		if _, err := openCurrentExecutable(fifoPath); err == nil {
			t.Fatal("FIFO was accepted as the running executable identity hint")
		}
		fmt.Print("FIFO_HINT_REFUSED_PROMPTLY")
		return
	}
	fifoPath := filepath.Join(t.TempDir(), "worker")
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutableIdentityHintFIFOIsNonblocking$")
	command.Env = append(os.Environ(), childPath+"="+fifoPath)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("identity-hint inspection blocked on FIFO until timeout: %s", output)
	}
	if err != nil || !bytes.Contains(output, []byte("FIFO_HINT_REFUSED_PROMPTLY")) {
		t.Fatalf("FIFO identity-hint subprocess did not refuse promptly: err=%v output=%s", err, output)
	}
}

func TestSealedSnapshotDescriptorsHaveIndependentOffsetsAndMismatchCloses(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, []byte("bounded executable fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	first, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	var prefix [4]byte
	if _, err := first.Read(prefix[:]); err != nil {
		t.Fatal(err)
	}
	if offset, err := second.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("fresh descriptor shares source offset: offset=%d err=%v", offset, err)
	}
	_ = first.Close()
	_ = second.Close()

	privateWorker := filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(privateWorker, []byte("altered fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	verifier, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	verifierFD := int(verifier.Fd())
	if err := verifySnapshotExecutable(verifier, privateWorker); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("integrity mismatch was accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(verifierFD), unix.F_GETFD, 0); err == nil {
		t.Fatal("mismatched snapshot verifier descriptor was not closed")
	}
}

func TestSealedSnapshotRejectsOversizedSource(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "oversized")
	if err := os.WriteFile(sourcePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.OpenFile(sourcePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Truncate(maxWorkerSnapshotBytes + 1); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := createSealedWorkerSnapshot(source); err == nil {
		_ = snapshot.Close()
		t.Fatal("oversized worker snapshot was accepted")
	}
}

func TestBubblewrapCapabilityProbeRejectsMissingAndMalformedFeatures(t *testing.T) {
	_, partial, err := bubblewrapCapabilities("bubblewrap 0.9.0", "--ro-bind --ro-bind-fd --tmpfs\n")
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "missing required Bubblewrap features") {
		t.Fatalf("missing required options were not actionable: %v", err)
	}
	if partial.BindMode != "" || !slices.Contains(partial.Options, "--ro-bind") {
		t.Fatalf("missing-feature report lost detected options or selected an unusable path: %#v", partial)
	}
	for _, version := range []string{"", "bubblewrap 0.9.0 extra", "bwrap 0.9.0"} {
		if _, _, err := bubblewrapCapabilities(version, bubblewrapHelp("")); !errors.Is(err, ErrUnavailable) {
			t.Errorf("malformed version %q was accepted: %v", version, err)
		}
	}
}

func TestBubblewrapProbeOutputIsBounded(t *testing.T) {
	var output boundedProbeOutput
	if n, err := output.Write(make([]byte, backendProbeOutputLimit+1)); err != nil || n != backendProbeOutputLimit+1 {
		t.Fatalf("bounded output write: n=%d err=%v", n, err)
	}
	if !output.overflow || len(output.bytes) != backendProbeOutputLimit {
		t.Fatalf("probe output was not bounded: len=%d overflow=%t", len(output.bytes), output.overflow)
	}
}

func TestBubblewrapProbeFailuresRefuseCompatibility(t *testing.T) {
	for _, failOn := range []string{"--version", "--help"} {
		calls := 0
		_, _, err := probeBubblewrapWith(func(argument string) (string, error) {
			calls++
			if argument == failOn {
				return "", ErrUnavailable
			}
			if argument == "--version" {
				return "bubblewrap 0.9.0", nil
			}
			return bubblewrapHelp(""), nil
		})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("failed %s probe was accepted: %v", failOn, err)
		}
		if failOn == "--version" && calls != 1 {
			t.Errorf("help probe ran after version failure: calls=%d", calls)
		}
	}
}

func TestUntrustedBubblewrapIsRejectedWithoutProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(path, []byte("synthetic untrusted executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	inspection := InspectBubblewrap(path)
	if inspection.Trusted || inspection.Version != "" || inspection.BindMode != "" || inspection.Error == "" {
		t.Fatalf("untrusted backend yielded probe data: %#v", inspection)
	}
}

func TestVerifyDescriptorMountConsumesAndChecksDescriptor(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "worker")
	if err := os.WriteFile(path, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: path}}); err != nil {
		t.Fatalf("matching mount rejected: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("successful verification descriptor remained open")
	}
}

func TestVerifyDescriptorMountRejectsMismatchAndClosesDescriptor(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(path), 0o500); err != nil {
			t.Fatal(err)
		}
	}
	fd, err := unix.Open(first, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: second}}); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("mismatched mount was accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("mismatched verification descriptor remained open")
	}
}

func TestVerifyDescriptorMountRejectsSymlinkDestination(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	alias := filepath.Join(directory, "alias")
	if err := os.WriteFile(target, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(target, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: alias}}); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("symlink destination was followed: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("symlink verification descriptor remained open")
	}
}

func TestVerifyDescriptorMountRejectsInvalidDescriptors(t *testing.T) {
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: 0, Destination: "/worker"}}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("standard descriptor accepted: %v", err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: 987654, Destination: "/worker"}}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("missing descriptor accepted: %v", err)
	}
	fd, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: "/worker"}, {FD: fd, Destination: "/worker"}}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("duplicate descriptor accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("duplicate verification descriptor was not closed")
	}
}

func TestWorkerSeccompProgramIncludesBootstrapAndProcessRestrictions(t *testing.T) {
	denied := workerDeniedSyscalls()
	for _, required := range []uint32{unix.SYS_OPENAT, unix.SYS_SOCKET, unix.SYS_CLONE3, unix.SYS_EXECVE} {
		if !slices.Contains(denied, required) {
			t.Errorf("seccomp policy does not deny syscall %d", required)
		}
	}
	if slices.Contains(denied, unix.SYS_CLONE) {
		t.Fatal("clone must use the narrow thread-only argument policy, not an unconditional syscall denial")
	}
	if _, err := workerSeccompFilterBytes(); err != nil {
		t.Fatalf("worker seccomp program could not be built: %v", err)
	}
}

func TestInstallWorkerSeccompRejectsInvalidProgramAndClosesFD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-filter")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := installWorkerSeccomp(fd); !errors.Is(err, ErrProtocol) {
		t.Fatalf("empty seccomp program was accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("rejected seccomp descriptor remained open")
	}
}

func bubblewrapHelp(extra ...string) string {
	options := slices.Clone(requiredBubblewrapOptions)
	options = append(options, extra...)
	var help strings.Builder
	for _, option := range options {
		help.WriteString("  ")
		help.WriteString(option)
		help.WriteString(" ARG\n")
	}
	return help.String()
}
