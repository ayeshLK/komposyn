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
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRewritePrototypeWorkerChangesOnlyHeldDescriptor(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
		move, cancel        bool
	}{
		{name: "grow", before: "old", after: "longer text"},
		{name: "shrink", before: "old long text", after: "new"},
		{name: "empty", before: "old", after: ""},
		{name: "unchanged", before: "same", after: "same"},
		{name: "moved after check", before: "old", after: "new", move: true},
		{name: "cancel before release", before: "old", after: "new", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "edit.txt")
			if err := os.WriteFile(path, []byte(test.before), 0o600); err != nil {
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
			if err := unix.Fsetxattr(int(file.Fd()), "user.komposyn_rewrite_fixture", []byte("fixture"), 0); err != nil {
				if !errors.Is(err, unix.ENOTSUP) {
					t.Fatal(err)
				}
				t.Skip("test filesystem lacks user xattr support")
			}
			worker, stop, done := startRewriteTestWorker(t)
			defer stop()
			if err := worker.HoldRewrite(ctx, file, identity, []byte(test.after)); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != test.before {
				t.Fatalf("before release got %q, %v", data, err)
			}
			effectPath := path
			if test.move {
				effectPath = path + ".moved"
				if err := os.Rename(path, effectPath); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("incumbent"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want := test.after
			if test.cancel {
				cancel()
				if err := worker.ReleaseRewrite(ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("release after cancellation: %v", err)
				}
				stop()
				want = test.before
			} else {
				if err := worker.ReleaseRewrite(ctx); err != nil {
					t.Fatal(err)
				}
				got, err := worker.WaitRewrite(ctx)
				wantOutcome := RewriteComplete
				if test.before == test.after {
					wantOutcome = RewriteNoEffect
				}
				if err != nil || got != wantOutcome {
					t.Fatalf("rewrite outcome %v, %v", got, err)
				}
				if err := worker.ReleaseRewrite(ctx); !errors.Is(err, ErrProtocol) {
					t.Fatalf("release reused: %v", err)
				}
			}
			joinCtx, joinCancel := context.WithTimeout(context.Background(), time.Second)
			defer joinCancel()
			select {
			case code := <-done:
				if !test.cancel && code != 0 {
					t.Fatalf("worker exited with %d", code)
				}
			case <-joinCtx.Done():
				t.Fatal("worker did not exit")
			}
			if data, err := os.ReadFile(effectPath); err != nil || string(data) != want {
				t.Fatalf("after attempt got %q, %v", data, err)
			}
			if test.move {
				if data, err := os.ReadFile(path); err != nil || string(data) != "incumbent" {
					t.Fatalf("new occupant got %q, %v", data, err)
				}
			}
			info, err := os.Stat(effectPath)
			before, statErr := file.Stat()
			if err != nil || statErr != nil || !os.SameFile(info, before) || info.Mode().Perm() != 0o600 {
				t.Fatalf("target identity or mode changed: %v, %v", err, statErr)
			}
			attr := make([]byte, len("fixture"))
			if n, err := unix.Fgetxattr(int(file.Fd()), "user.komposyn_rewrite_fixture", attr); err != nil || n != len(attr) || !bytes.Equal(attr, []byte("fixture")) {
				t.Fatalf("user attribute changed: %q, %v", attr, err)
			}
		})
	}
}

func startRewriteTestWorker(t *testing.T) (RewritePrototypeWorker, func(), <-chan int) {
	t.Helper()
	done := make(chan int, 1)
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	control := os.NewFile(uintptr(pair[0]), "rewrite-host")
	t.Cleanup(func() { _ = control.Close(); _ = unix.Close(pair[1]) })
	worker := &sandboxWorker{control: control, operation: AdmissionRewritePrototype, ready: true}
	go func() { done <- receiveAndRead(pair[1], false, AdmissionRewritePrototype) }()
	return worker, func() { _ = control.Close() }, done
}

func TestRewritePrototypeReportsPartialOnTruncateFailure(t *testing.T) {
	fd, err := unix.MemfdCreate("rewrite-truncate-fixture", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		t.Skipf("memfd unavailable: %v", err)
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, []byte("old suffix")); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		t.Fatal(err)
	}
	if got := rewriteContents(fd, int64(len("old suffix")), []byte("new")); got != RewritePartial {
		t.Fatalf("rewrite outcome after failed truncate = %v", got)
	}
	data := make([]byte, len("new suffix"))
	if err := readRewriteBytes(fd, data); err != nil || string(data) != "new suffix" {
		t.Fatalf("partial bytes = %q, %v", data, err)
	}
}
