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
	"errors"
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
	_, legacy, err := bubblewrapCapabilities("bubblewrap 0.9.0\n", bubblewrapHelp(""))
	if !errors.Is(err, ErrUnavailable) || legacy.BindMode != BindModeLegacyRefused || !strings.Contains(err.Error(), "pathname before mounting") {
		t.Fatalf("unsafe legacy compatibility path was not refused: mode=%q err=%v", legacy.BindMode, err)
	}
	if !slices.Contains(legacy.Options, "--as-pid-1") || !slices.Contains(legacy.Options, "--sync-fd") {
		t.Fatalf("legacy capabilities missing from report: %#v", legacy.Options)
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
	if len(EffectiveBubblewrapArguments(BindModeLegacyRefused)) != 0 {
		t.Fatal("refused legacy mount path must not produce launch arguments")
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
	for _, required := range []uint32{unix.SYS_OPENAT, unix.SYS_SOCKET, unix.SYS_CLONE, unix.SYS_CLONE3, unix.SYS_EXECVE} {
		if !slices.Contains(denied, required) {
			t.Errorf("seccomp policy does not deny syscall %d", required)
		}
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

func bubblewrapHelp(extra string) string {
	options := slices.Clone(requiredBubblewrapOptions)
	if extra != "" {
		options = append(options, extra)
	}
	var help strings.Builder
	for _, option := range options {
		help.WriteString("  ")
		help.WriteString(option)
		help.WriteString(" ARG\n")
	}
	return help.String()
}
