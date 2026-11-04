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
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const maxProcObservationBytes = 32 << 10

// ReadHostProcessStartTime reads only the kernel start-time field needed to
// disambiguate a PID before the trusted supervisor opens its pidfd. It does
// not read command lines or environment data.
func ReadHostProcessStartTime(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, errors.New("process ID must be positive")
	}
	data, err := readBoundedProcFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	return parseHostProcessStartTime(data)
}

func parseHostProcessStartTime(data []byte) (uint64, error) {
	// comm is parenthesized and may itself contain spaces or ')'; the final
	// close parenthesis is the only safe delimiter before field 3 (state).
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 || end+1 >= len(data) {
		return 0, errors.New("process stat record is malformed")
	}
	fields := strings.Fields(string(data[end+1:]))
	// The tail starts at field 3; starttime is field 22 (index 19).
	if len(fields) <= 19 {
		return 0, errors.New("process stat record is incomplete")
	}
	value, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, errors.New("process start time is invalid")
	}
	return value, nil
}

func readBoundedProcFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxProcObservationBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProcObservationBytes {
		return nil, errors.New("process observation exceeds its bound")
	}
	return data, nil
}

// observeBubblewrapProcesses identifies the trusted Bubblewrap monitor and
// exactly one direct child that is PID 1 in its private PID namespace. The
// host procfs reads remain in this trusted process; the worker sees no procfs.
func observeBubblewrapProcesses(bwrapPID int) (HostProcessIdentity, HostProcessIdentity, error) {
	bwrapStart, err := ReadHostProcessStartTime(bwrapPID)
	if err != nil {
		return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("Bubblewrap process identity is unavailable")
	}
	childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", bwrapPID, bwrapPID)
	children, err := readBoundedProcFile(childrenPath)
	if err != nil {
		return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("Bubblewrap child process list is unavailable")
	}
	var initPID int
	for _, field := range strings.Fields(string(children)) {
		pid, parseErr := strconv.Atoi(field)
		if parseErr != nil || pid <= 0 {
			return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("Bubblewrap child process list is malformed")
		}
		if !isPIDNamespaceInit(pid) {
			continue
		}
		if initPID != 0 {
			return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("multiple PID namespace init candidates were observed")
		}
		initPID = pid
	}
	if initPID == 0 {
		return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("Bubblewrap PID namespace init was not observed")
	}
	initStart, err := ReadHostProcessStartTime(initPID)
	if err != nil || !isPIDNamespaceInit(initPID) {
		return HostProcessIdentity{}, HostProcessIdentity{}, errors.New("PID namespace init identity changed during observation")
	}
	return HostProcessIdentity{PID: bwrapPID, StartTimeTicks: bwrapStart},
		HostProcessIdentity{PID: initPID, StartTimeTicks: initStart}, nil
}

func isPIDNamespaceInit(pid int) bool {
	data, err := readBoundedProcFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "NSpid:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "NSpid:"))
		if len(fields) == 0 {
			return false
		}
		value, err := strconv.Atoi(fields[len(fields)-1])
		return err == nil && value == 1
	}
	return false
}

// OpenVerifiedPIDFD opens a race-resistant handle to a process identity already
// observed by the trusted coordinator/supervisor. Callers must close the fd.
func OpenVerifiedPIDFD(identity HostProcessIdentity) (int, error) {
	if identity.PID <= 0 || identity.StartTimeTicks == 0 {
		return -1, errors.New("process identity is incomplete")
	}
	before, err := ReadHostProcessStartTime(identity.PID)
	if err != nil || before != identity.StartTimeTicks {
		return -1, errors.New("process identity changed before pidfd open")
	}
	fd, err := unix.PidfdOpen(identity.PID, 0)
	if err != nil {
		return -1, err
	}
	after, err := ReadHostProcessStartTime(identity.PID)
	if err != nil || after != identity.StartTimeTicks {
		_ = unix.Close(fd)
		return -1, errors.New("process identity changed while opening pidfd")
	}
	return fd, nil
}
