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
	controlFD      = 4
	seccompInputFD = 3
	workerDeadline = 5_000_000_000
	maxPacketBytes = MaxReadBytes + 128
	dataChunkBytes = 16 << 10
)

func validateTrustedExecutable(path string, requireRoot bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("executable path must be absolute and clean")
	}
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	for index, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("executable path component is missing or a symlink")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("executable ownership could not be verified")
		}
		if requireRoot && stat.Uid != 0 {
			return errors.New("sandbox backend must be root-owned")
		}
		if info.Mode().Perm()&0o022 != 0 {
			return errors.New("executable path is group/world writable")
		}
		last := index == len(parts)-1
		if last {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return errors.New("executable must be a regular executable file")
			}
		} else if !info.IsDir() {
			return errors.New("executable parent is not a directory")
		}
	}
	return nil
}

func validateStaticWorkerFile(f *os.File) error {
	if f == nil {
		return errors.New("worker file is missing")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return errors.New("worker file cannot be inspected")
	}
	image, err := elf.NewFile(f)
	if err != nil {
		return errors.New("worker is not a valid ELF executable")
	}
	defer image.Close()
	if image.Type != elf.ET_EXEC && image.Type != elf.ET_DYN {
		return errors.New("worker ELF type is unsupported")
	}
	for _, program := range image.Progs {
		if program.Type == elf.PT_INTERP {
			return errors.New("worker must be statically linked")
		}
	}
	return nil
}

// workerSeccompFilter builds an architecture-checked syscall filter for the
// fixed worker. It permits its inherited AF_UNIX control socket, but denies
// opening or resolving paths, creating sockets, namespace/mount operations,
// process inspection and selected privilege-changing syscalls. This is a
// feasibility filter around one trusted worker, not a general sandbox policy.
func workerSeccompFilter() (*os.File, error) {
	data, err := workerSeccompFilterBytes()
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "komposyn-readbroker-seccomp-*.bpf")
	if err != nil {
		return nil, err
	}
	cleanup := func(err error) (*os.File, error) {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return cleanup(err)
	}
	return f, nil
}

func workerSeccompFilterBytes() ([]byte, error) {
	arch, err := workerAuditArch(runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: arch, Jt: 1, Jf: 0},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		// The fixed Go worker needs clone for runtime threads. Permit only the
		// exact shared-thread flag sets used by the supported Go runtime; all
		// process-like clone calls are refused. clone3 remains denied below.
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.SYS_CLONE, Jt: 1, Jf: 0},
		{Code: unix.BPF_JMP | unix.BPF_JA, K: 8},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 20},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: 0, Jt: 1, Jf: 0},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: goRuntimeThreadCloneFlags, Jt: 2, Jf: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: goRuntimeThreadCloneFlags | unix.CLONE_SETTLS, Jt: 1, Jf: 0},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	for _, number := range workerDeniedSyscalls() {
		filters = append(filters,
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: number, Jt: 0, Jf: 1},
			unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		)
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	data := make([]byte, len(filters)*unix.SizeofSockFilter)
	for i, filter := range filters {
		offset := i * unix.SizeofSockFilter
		binary.NativeEndian.PutUint16(data[offset:offset+2], filter.Code)
		data[offset+2] = filter.Jt
		data[offset+3] = filter.Jf
		binary.NativeEndian.PutUint32(data[offset+4:offset+8], filter.K)
	}
	return data, nil
}

func workerAuditArch(goArch string) (uint32, error) {
	switch goArch {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	default:
		return 0, fmt.Errorf("worker seccomp architecture %q is unsupported", goArch)
	}
}

func workerDeniedSyscalls() []uint32 {
	denied := []uint32{
		unix.SYS_CLONE3, unix.SYS_EXECVE,
		unix.SYS_OPENAT, unix.SYS_OPENAT2, unix.SYS_NEWFSTATAT,
		unix.SYS_STATX, unix.SYS_READLINKAT, unix.SYS_GETDENTS64,
		unix.SYS_FACCESSAT2,
		unix.SYS_SOCKET, unix.SYS_SOCKETPAIR, unix.SYS_CONNECT, unix.SYS_BIND,
		unix.SYS_LISTEN, unix.SYS_ACCEPT, unix.SYS_ACCEPT4, unix.SYS_SENDTO,
		unix.SYS_RECVFROM, unix.SYS_SHUTDOWN, unix.SYS_SETSOCKOPT,
		unix.SYS_GETSOCKOPT, unix.SYS_MOUNT, unix.SYS_UMOUNT2,
		unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT, unix.SYS_UNSHARE, unix.SYS_SETNS,
		unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV,
		unix.SYS_PROCESS_MADVISE, unix.SYS_PROCESS_MRELEASE,
		unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_PIDFD_GETFD, unix.SYS_PIDFD_OPEN,
		unix.SYS_PIDFD_SEND_SIGNAL, unix.SYS_KCMP, unix.SYS_INIT_MODULE,
		unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT,
		unix.SYS_KEXEC_LOAD, unix.SYS_KEXEC_FILE_LOAD, unix.SYS_ACCT,
		unix.SYS_SWAPON, unix.SYS_SWAPOFF, unix.SYS_KEYCTL,
		unix.SYS_PERF_EVENT_OPEN, unix.SYS_BPF, unix.SYS_EXECVEAT,
	}
	return append(denied, workerArchDeniedSyscalls()...)
}

const goRuntimeThreadCloneFlags = unix.CLONE_VM | unix.CLONE_FS | unix.CLONE_FILES |
	unix.CLONE_SIGHAND | unix.CLONE_SYSVSEM | unix.CLONE_THREAD
