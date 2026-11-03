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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

func TestBubblewrapArgumentsSelectModernMountAndSeparateVerifierFD(t *testing.T) {
	modern := strings.Join(EffectiveBubblewrapArguments(BindModeDescriptor), " ")
	if !strings.Contains(modern, "--ro-bind-fd 5 /worker") || strings.Contains(modern, "/proc/self/fd") {
		t.Fatalf("unexpected modern mount arguments: %s", modern)
	}
	for _, value := range []string{"--as-pid-1", "--sync-fd 6", "--readbrokerstudy-bootstrap", "--worker-fd 6", "--seccomp-fd 3"} {
		if !strings.Contains(modern, value) {
			t.Fatalf("modern arguments missing %q: %s", value, modern)
		}
	}
	if workerMountFD == workerVerificationFD {
		t.Fatal("mount source and retained verifier descriptors must be distinct")
	}
	snapshot := strings.Join(EffectiveBubblewrapArguments(BindModeFileSnapshot), " ")
	for _, value := range []string{"--perms 0500 --ro-bind-data 5 /worker", "--sync-fd 6", "--worker-mode snapshot"} {
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
	if err := verifySnapshotExecutable(verifierFD, workerPath); err != nil {
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
	if err := verifySnapshotExecutable(verifierFD, privateWorker); !errors.Is(err, ErrStaleReceipt) {
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
