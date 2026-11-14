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

// Package sandboxstudy contains disposable experiments for issue #215. It is
// not used by the production harness and does not enable filesystem tools.
package sandboxstudy

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	workerRuntimeLimit = 5_000_000_000
	workerOutputLimit  = 64 * 1024
	scratchSize        = 16 * 1024 * 1024
)

// Profile returns the exact bwrap argument vector for a typed fixture worker.
// The worker is mounted read-only in an empty root and only the disposable
// fixture root is writable. protected is a fixture-relative path hidden by an
// empty tmpfs; it is intentionally only a path-based protected-file probe.
func Profile(bwrapPath, workerPath, fixtureRoot, protected string) ([]string, *os.File, error) {
	return profile(bwrapPath, workerPath, fixtureRoot, protected, false)
}

// ProfileWithPrivateProc adds a procfs mount inside the already unshared PID
// namespace. Only the O_TMPFILE study candidate uses this separate profile.
func ProfileWithPrivateProc(bwrapPath, workerPath, fixtureRoot, protected string) ([]string, *os.File, error) {
	return profile(bwrapPath, workerPath, fixtureRoot, protected, true)
}

func profile(bwrapPath, workerPath, fixtureRoot, protected string, privateProc bool) ([]string, *os.File, error) {
	if !filepath.IsAbs(bwrapPath) || !filepath.IsAbs(workerPath) || !filepath.IsAbs(fixtureRoot) {
		return nil, nil, errors.New("backend, worker, and fixture paths must be absolute")
	}
	if protected != "" && (!filepath.IsLocal(protected) || protected == ".") {
		return nil, nil, errors.New("protected fixture path must be relative and local")
	}
	if err := ValidateBackend(bwrapPath); err != nil {
		return nil, nil, err
	}
	worker, err := os.Open(workerPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open worker: %w", err)
	}
	image, err := elf.NewFile(worker)
	if err != nil {
		_ = worker.Close()
		return nil, nil, fmt.Errorf("read worker ELF: %w", err)
	}
	if len(image.Progs) == 0 || hasInterpreter(image) {
		_ = image.Close()
		return nil, nil, errors.New("worker must be a statically linked ELF executable (build with CGO_ENABLED=0)")
	}
	_ = image.Close()
	_ = worker.Close()
	root, err := os.Stat(fixtureRoot)
	if err != nil || !root.IsDir() {
		return nil, nil, errors.New("fixture root must be an existing directory")
	}
	filter, err := seccompFilter()
	if err != nil {
		return nil, nil, err
	}
	args := []string{
		"--die-with-parent", "--new-session",
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--hostname", "komposyn-study", "--unshare-net",
		"--disable-userns", "--cap-drop", "ALL", "--clearenv",
		"--setenv", "HOME", "/tmp/home", "--setenv", "TMPDIR", "/tmp",
		"--setenv", "PATH", "/nonexistent", "--setenv", "LANG", "C.UTF-8",
		"--size", fmt.Sprint(32 * 1024 * 1024), "--tmpfs", "/",
		"--dir", "/tmp", "--size", fmt.Sprint(scratchSize), "--perms", "0700", "--tmpfs", "/tmp",
		"--dir", "/tmp/home", "--dir", "/scope",
		"--ro-bind", workerPath, "/worker",
		"--bind", fixtureRoot, "/scope",
	}
	args = appendPrivateProc(args, privateProc)
	if protected != "" {
		args = append(args, "--size", fmt.Sprint(1024*1024), "--perms", "0700", "--tmpfs", "/scope/"+filepath.ToSlash(protected))
	}
	args = append(args, "--seccomp", "3", "--chdir", "/scope", "--", "/worker", "--sandbox-study-worker")
	return args, filter, nil
}

func appendPrivateProc(args []string, enabled bool) []string {
	if !enabled {
		return args
	}
	return append(args, "--proc", "/proc")
}

// ValidateBackend checks a trusted absolute system path before either reading
// or executing it. Every component must be a root-owned, non-writable real
// directory; the executable must be a root-owned regular executable.
func ValidateBackend(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("backend path must be absolute and clean")
	}
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return errors.New("trusted backend path must name an executable")
	}
	current := string(filepath.Separator)
	for index, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("trusted backend path component %q is missing or a symlink", current)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("trusted backend path component %q must be root-owned and not group/world writable", current)
		}
		isFile := index == len(parts)-1
		if isFile {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return errors.New("trusted backend must be an executable regular file")
			}
		} else if !info.IsDir() {
			return fmt.Errorf("trusted backend parent %q is not a directory", current)
		}
	}
	return nil
}

func hasInterpreter(file *elf.File) bool {
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return true
		}
	}
	return false
}

// seccompFilter denies networking/IPC sockets, namespace changes, mounts,
// process inspection and privilege-changing operations. It is architecture
// checked and allows other syscalls; it is a bounded feasibility profile, not
// a syscall-minimized production policy.
func seccompFilter() (*os.File, error) {
	arch, err := auditArch(runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: arch, Jt: 1, Jf: 0},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
	}
	for _, number := range deniedSyscalls() {
		filters = append(filters,
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: uint32(number), Jt: 0, Jf: 1},
			unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		)
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	bytes := make([]byte, len(filters)*unix.SizeofSockFilter)
	for i, filter := range filters {
		offset := i * unix.SizeofSockFilter
		binary.NativeEndian.PutUint16(bytes[offset:offset+2], filter.Code)
		bytes[offset+2] = filter.Jt
		bytes[offset+3] = filter.Jf
		binary.NativeEndian.PutUint32(bytes[offset+4:offset+8], filter.K)
	}
	file, err := os.CreateTemp("", "komposyn-seccomp-*.bpf")
	if err != nil {
		return nil, fmt.Errorf("create seccomp fixture: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	if _, err := file.Write(bytes); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}

func auditArch(goArch string) (uint32, error) {
	switch goArch {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	default:
		return 0, fmt.Errorf("seccomp architecture is not reviewed for %q", goArch)
	}
}

func deniedSyscalls() []int {
	denied := []int{
		unix.SYS_SOCKET, unix.SYS_SOCKETPAIR, unix.SYS_CONNECT, unix.SYS_BIND, unix.SYS_LISTEN,
		unix.SYS_ACCEPT, unix.SYS_ACCEPT4, unix.SYS_SENDTO, unix.SYS_RECVFROM, unix.SYS_SENDMSG,
		unix.SYS_RECVMSG, unix.SYS_SETSOCKOPT, unix.SYS_GETSOCKOPT, unix.SYS_SHUTDOWN,
		unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT,
		unix.SYS_UNSHARE, unix.SYS_SETNS, unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV,
		unix.SYS_PROCESS_VM_WRITEV, unix.SYS_PROCESS_MADVISE, unix.SYS_PROCESS_MRELEASE,
		unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_PIDFD_GETFD, unix.SYS_PIDFD_OPEN, unix.SYS_PIDFD_SEND_SIGNAL, unix.SYS_KCMP,
		unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT,
		unix.SYS_KEXEC_LOAD, unix.SYS_KEXEC_FILE_LOAD,
		unix.SYS_ACCT, unix.SYS_SWAPON, unix.SYS_SWAPOFF, unix.SYS_KEYCTL,
		unix.SYS_PERF_EVENT_OPEN, unix.SYS_BPF,
	}
	return append(denied, extraDeniedSyscalls()...)
}
