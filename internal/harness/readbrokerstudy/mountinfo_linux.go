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
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	maxDescriptorInfoBytes = 16 << 10
	maxMountInfoBytes      = 4 << 20
)

var errMountFilesystemUnavailable = errors.New("descriptor mount filesystem is unavailable")

const extFilesystemMagic = 0xef53

func validateExt4SearchDirectory(fd int) error {
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(fd, &filesystem); err != nil {
		return ErrUnsupportedFilesystem
	}
	filesystemType, err := mountFilesystemTypeForFD(fd)
	if err != nil || !isSupportedSearchFilesystem(uint64(filesystem.Type), filesystemType) {
		return ErrUnsupportedFilesystem
	}
	return nil
}

// ValidateCreationDirectoryDescriptor restricts the disposable creation
// prototype to Linux amd64 on a descriptor whose mountinfo entry identifies
// ext4. The statfs magic alone is ambiguous across ext filesystems.
func ValidateCreationDirectoryDescriptor(fd int) error {
	if runtime.GOARCH != "amd64" || validateExt4SearchDirectory(fd) != nil {
		return ErrUnsupportedFilesystem
	}
	return nil
}

func isSupportedSearchFilesystem(statfsType uint64, mountFilesystemType string) bool {
	return statfsType == extFilesystemMagic && mountFilesystemType == "ext4"
}

func mountFilesystemTypeForFD(fd int) (string, error) {
	if fd < 0 {
		return "", errMountFilesystemUnavailable
	}
	fdInfo, err := readBoundedMountInfoProcFile(fmt.Sprintf("/proc/self/fdinfo/%d", fd), maxDescriptorInfoBytes)
	if err != nil {
		return "", errMountFilesystemUnavailable
	}
	mountID, err := parseDescriptorMountID(fdInfo)
	if err != nil {
		return "", errMountFilesystemUnavailable
	}
	mountInfo, err := readBoundedMountInfoProcFile("/proc/self/mountinfo", maxMountInfoBytes)
	if err != nil {
		return "", errMountFilesystemUnavailable
	}
	filesystem, err := parseMountFilesystemType(mountInfo, mountID)
	if err != nil {
		return "", errMountFilesystemUnavailable
	}
	return filesystem, nil
}

func readBoundedMountInfoProcFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errMountFilesystemUnavailable
	}
	return data, nil
}

func parseDescriptorMountID(data []byte) (uint64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "mnt_id:" {
			continue
		}
		mountID, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || mountID == 0 {
			return 0, errMountFilesystemUnavailable
		}
		return mountID, nil
	}
	return 0, errMountFilesystemUnavailable
}

func parseMountFilesystemType(data []byte, mountID uint64) (string, error) {
	if mountID == 0 {
		return "", errMountFilesystemUnavailable
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || id != mountID {
			continue
		}
		separator := -1
		for index, field := range fields {
			if field == "-" {
				separator = index
				break
			}
		}
		if separator < 6 || len(fields) <= separator+1 || fields[separator+1] == "" {
			return "", errMountFilesystemUnavailable
		}
		return fields[separator+1], nil
	}
	if scanner.Err() != nil {
		return "", errMountFilesystemUnavailable
	}
	return "", errMountFilesystemUnavailable
}
