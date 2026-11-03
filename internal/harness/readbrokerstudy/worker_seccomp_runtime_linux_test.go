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

//go:build linux && !race

// This kernel-level subprocess test runs against the production worker's
// CGO_ENABLED=0 runtime. The race-instrumented test binary uses runtime/cgo
// pthread creation with a different clone profile, outside this worker policy.

package readbrokerstudy

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWorkerSeccompAllowsRuntimeThreadsAndDeniesProcessClone(t *testing.T) {
	const childEnv = "KOMPOSYN_READBROKER_SECCOMP_CHILD=1"
	for _, value := range os.Environ() {
		if value == childEnv {
			testWorkerSeccompRuntimeAndProcessRules(t)
			return
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWorkerSeccompAllowsRuntimeThreadsAndDeniesProcessClone$")
	command.Env = append(os.Environ(), childEnv)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("filtered subprocess failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "THREADS_OK_PROCESS_CLONE_DENIED") {
		t.Fatalf("filtered subprocess did not complete both checks: %s", output)
	}
}

func testWorkerSeccompRuntimeAndProcessRules(t *testing.T) {
	runtime.GOMAXPROCS(32)
	filter, err := workerSeccompFilter()
	if err != nil {
		t.Fatalf("create worker seccomp fixture: %v", err)
	}
	filterPath := filter.Name()
	if err := filter.Close(); err != nil {
		t.Fatalf("close seccomp fixture writer: %v", err)
	}
	fd, err := unix.Open(filterPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open seccomp fixture: %v", err)
	}
	if err := os.Remove(filterPath); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("remove seccomp fixture pathname: %v", err)
	}
	if err := installWorkerSeccomp(fd); err != nil {
		t.Fatalf("install exact worker filter with TSYNC: %v", err)
	}

	// Locking goroutines to threads while they block on a channel forces the Go
	// scheduler to create additional Ms after filter installation.
	const workers = 16
	start := make(chan struct{})
	tids := make(chan int, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			tids <- unix.Gettid()
			<-start
			request := unix.NsecToTimespec(50_000_000)
			if err := unix.Nanosleep(&request, nil); err != nil && err != unix.EINTR {
				panic(err)
			}
		}()
	}
	uniqueTIDs := make(map[int]struct{}, workers)
	for range workers {
		uniqueTIDs[<-tids] = struct{}{}
	}
	if len(uniqueTIDs) < 2 {
		t.Fatalf("test did not exercise runtime creation of additional OS threads: got %d thread IDs", len(uniqueTIDs))
	}
	close(start)
	runtime.GC()
	group.Wait()
	runtime.GC()

	_, _, errno := unix.RawSyscall(unix.SYS_CLONE, uintptr(unix.SIGCHLD), 0, 0)
	if errno != unix.EPERM {
		t.Fatalf("process-like clone returned %v; want EPERM", errno)
	}
	_, _, errno = unix.RawSyscall(unix.SYS_CLONE, uintptr(goRuntimeThreadCloneFlags)|(uintptr(1)<<32), 0, 0)
	if errno != unix.EPERM {
		t.Fatalf("clone with unsupported high flag bits returned %v; want EPERM", errno)
	}
	_, _, errno = unix.RawSyscall(unix.SYS_CLONE3, 0, 0, 0)
	if errno != unix.EPERM {
		t.Fatalf("clone3 returned %v; want EPERM", errno)
	}
	for _, number := range workerArchDeniedSyscalls() {
		_, _, errno := unix.RawSyscall(uintptr(number), 0, 0, 0)
		if errno != unix.EPERM {
			t.Fatalf("architecture-specific denied syscall %d returned %v; want EPERM", number, errno)
		}
	}
	_, _ = unix.Write(1, []byte("THREADS_OK_PROCESS_CLONE_DENIED\n"))
}
