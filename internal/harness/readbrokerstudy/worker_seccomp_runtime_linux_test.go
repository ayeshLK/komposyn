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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestWorkerListingSeccompAllowsOnlyItsRequiredDirectorySyscalls(t *testing.T) {
	const childEnv = "KOMPOSYN_READBROKER_LISTING_SECCOMP_CHILD=1"
	for _, value := range os.Environ() {
		if value == childEnv {
			testWorkerListingSeccompDirectorySyscalls(t)
			return
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.tmp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWorkerListingSeccompAllowsOnlyItsRequiredDirectorySyscalls$")
	command.Env = append(os.Environ(), childEnv, "KOMPOSYN_READBROKER_LISTING_SECCOMP_ROOT="+root)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("listing-filtered subprocess failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "LISTING_DIRECTORY_SYSCALLS_OK") {
		t.Fatalf("listing-filtered subprocess did not complete its checks: %s", output)
	}
}

func TestSearchContentSeccompAllowsOnlyTransferredSnapshotReads(t *testing.T) {
	const childEnv = "KOMPOSYN_READBROKER_SEARCH_CONTENT_SECCOMP_CHILD=1"
	for _, value := range os.Environ() {
		if value == childEnv {
			testSearchContentSeccompSnapshotOnly(t)
			return
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestSearchContentSeccompAllowsOnlyTransferredSnapshotReads$")
	command.Env = append(os.Environ(), childEnv)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("search-content filtered subprocess failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "SEARCH_CONTENT_SNAPSHOT_ONLY") {
		t.Fatalf("search-content filtered subprocess did not complete its checks: %s", output)
	}
}

func testSearchContentSeccompSnapshotOnly(t *testing.T) {
	file, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	filter, err := workerSeccompFilterForOperation(AdmissionSearchContent)
	if err != nil {
		t.Fatalf("create search-content profile: %v", err)
	}
	filterPath := filter.Name()
	if err := filter.Close(); err != nil {
		t.Fatal(err)
	}
	filterFD, err := unix.Open(filterPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filterPath); err != nil {
		_ = unix.Close(filterFD)
		t.Fatal(err)
	}
	operation, err := installWorkerSeccompProfile(filterFD)
	if err != nil || operation != AdmissionSearchContent {
		t.Fatalf("installed profile operation=%q err=%v", operation, err)
	}
	if _, err := unix.Openat(unix.AT_FDCWD, "/etc/passwd", unix.O_RDONLY|unix.O_CLOEXEC, 0); err != unix.EPERM {
		t.Fatalf("search content worker pathname lookup err=%v; want EPERM", err)
	}
	data := make([]byte, 16)
	if count, err := unix.Pread(int(file.Fd()), data, 0); err != nil || count == 0 {
		t.Fatalf("search content worker could not read already-open descriptor: bytes=%d err=%v", count, err)
	}
	if _, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0); err != unix.EPERM {
		t.Fatalf("search content worker socket creation err=%v; want EPERM", err)
	}
	fmt.Println("SEARCH_CONTENT_SNAPSHOT_ONLY")
}

func testWorkerListingSeccompDirectorySyscalls(t *testing.T) {
	root := os.Getenv("KOMPOSYN_READBROKER_LISTING_SECCOMP_ROOT")
	if root == "" {
		t.Fatal("listing seccomp fixture root is missing")
	}
	rootFile, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFile.Close()
	filter, err := workerSeccompFilterForOperation(AdmissionListDirectory)
	if err != nil {
		t.Fatalf("create listing worker seccomp fixture: %v", err)
	}
	filterPath := filter.Name()
	if err := filter.Close(); err != nil {
		t.Fatal(err)
	}
	filterFD, err := unix.Open(filterPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filterPath); err != nil {
		_ = unix.Close(filterFD)
		t.Fatal(err)
	}
	operation, err := installWorkerSeccompProfile(filterFD)
	if err != nil {
		t.Fatalf("install listing worker seccomp profile: %v", err)
	}
	if operation != AdmissionListDirectory {
		t.Fatalf("installed worker profile operation=%q, want directory listing", operation)
	}

	var rootStat unix.Statx_t
	if err := unix.Statx(int(rootFile.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_MNT_ID, &rootStat); err != nil {
		t.Fatalf("listing profile denied mount identity lookup on the retained directory: %v", err)
	}
	var ignoreStat unix.Stat_t
	if err := unix.Fstatat(int(rootFile.Fd()), ".gitignore", &ignoreStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatalf("listing profile denied no-follow ignore-file metadata lookup: %v", err)
	}
	childFD, err := unix.Openat2(int(rootFile.Fd()), "child", &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NONBLOCK,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		t.Fatalf("listing profile denied beneath-only child-directory traversal: %v", err)
	}
	_ = unix.Close(childFD)
	if _, err := unix.Openat(int(rootFile.Fd()), "child", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0); err != unix.EPERM {
		t.Fatalf("listing profile openat error=%v, want EPERM", err)
	}
	_, _ = unix.Write(1, []byte("LISTING_DIRECTORY_SYSCALLS_OK\n"))
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

func TestRewritePrototypeSeccompWorker(t *testing.T) {
	const childEnv = "KOMPOSYN_REWRITE_PROTOTYPE_SECCOMP_CHILD=1"
	for _, item := range os.Environ() {
		if item == childEnv {
			operation, err := installWorkerSeccompProfile(3)
			if err != nil || operation != AdmissionRewritePrototype {
				t.Fatalf("rewrite filter not installed: %q, %v", operation, err)
			}
			how := &unix.OpenHow{Flags: unix.O_RDONLY}
			if _, err := unix.Openat2(unix.AT_FDCWD, "/", how); err != unix.EPERM {
				t.Fatalf("worker path open error = %v", err)
			}
			os.Exit(receiveAndRead(4, false, operation))
		}
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("long original"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	identity, err := identityFromFile(file)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := workerSeccompFilterForOperation(AdmissionRewritePrototype)
	if err != nil {
		t.Fatal(err)
	}
	defer filter.Close()
	defer os.Remove(filter.Name())
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	host := os.NewFile(uintptr(pair[0]), "rewrite-test-host")
	defer host.Close()
	child := os.NewFile(uintptr(pair[1]), "rewrite-test-child")
	defer child.Close()
	ctx, cancel := context.WithTimeout(context.Background(), workerWaitDelay*10)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRewritePrototypeSeccompWorker$")
	cmd.Env = append(os.Environ(), childEnv)
	cmd.ExtraFiles = []*os.File{filter, child}
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	_ = child.Close()
	worker := &sandboxWorker{control: host, operation: AdmissionRewritePrototype, ready: true}
	if err := worker.HoldRewrite(ctx, file, identity, []byte("short")); err != nil {
		t.Fatal(err)
	}
	if err := worker.ReleaseRewrite(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := worker.WaitRewrite(ctx); err != nil || got != RewriteComplete {
		t.Fatalf("rewrite outcome %v, %v", got, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("filtered worker exited %v: %s", err, output.String())
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "short" {
		t.Fatalf("final bytes %q, %v", got, err)
	}
}
