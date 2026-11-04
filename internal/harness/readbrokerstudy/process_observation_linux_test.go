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
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseHostProcessStartTimeHandlesParenthesesInCommandName(t *testing.T) {
	fields := make([]string, 20)
	fields[0] = "S"
	for index := 1; index < len(fields); index++ {
		fields[index] = "0"
	}
	fields[19] = "987654"
	got, err := parseHostProcessStartTime([]byte("123 (name with ) embedded paren) " + strings.Join(fields, " ")))
	if err != nil {
		t.Fatal(err)
	}
	if got != 987654 {
		t.Fatalf("start time=%d, want 987654", got)
	}
}

func TestReadHostProcessStartTimeAndVerifiedPIDFD(t *testing.T) {
	start, err := ReadHostProcessStartTime(os.Getpid())
	if err != nil || start == 0 {
		t.Fatalf("self process start time=%d err=%v", start, err)
	}
	fd, err := OpenVerifiedPIDFD(HostProcessIdentity{PID: os.Getpid(), StartTimeTicks: start})
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
			t.Skipf("pidfd support unavailable: %v", err)
		}
		t.Fatal(err)
	}
	defer unix.Close(fd)
	var poll [1]unix.PollFd
	poll[0] = unix.PollFd{Fd: int32(fd), Events: unix.POLLIN}
	n, err := unix.Poll(poll[:], 0)
	if err != nil || n != 0 {
		t.Fatalf("live process pidfd poll=(%d,%v), want no exit event", n, err)
	}
}

func TestParseHostProcessStartTimeRejectsMalformedInput(t *testing.T) {
	for _, input := range [][]byte{nil, []byte("no close parenthesis"), []byte("42 (short) S 0"), []byte("9 (bad-number) S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 nope")} {
		if _, err := parseHostProcessStartTime(input); err == nil {
			t.Errorf("parseHostProcessStartTime(%q) unexpectedly succeeded", input)
		}
	}
}
