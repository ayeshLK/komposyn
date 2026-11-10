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

//go:build linux && komposyn_stage6_study

package readbrokerstudy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// InspectBubblewrapForStage6 resolves an explicitly requested, already
// feature-detected worker bind mode for the disposable Stage 6 study. An empty
// mode preserves automatic selection. Backend trust validation and bounded
// capability probes always run before the requested mode is considered.
func InspectBubblewrapForStage6(path string, preferred BindMode) BackendInspection {
	result := InspectBubblewrap(path)
	if preferred == "" || !result.Trusted || result.Error != "" {
		return result
	}
	workerPath, err := os.Executable()
	if err != nil {
		result.Error = "running worker source is unavailable"
		return result
	}
	source, err := openCurrentExecutable(workerPath)
	if err != nil {
		result.Error = "running worker source could not be opened"
		return result
	}
	mode, modeErr := selectWorkerBindModePreferred(
		BubblewrapCapabilities{Options: result.Capabilities, BindMode: result.BindMode}, source, preferred,
	)
	closeErr := source.Close()
	if modeErr != nil || closeErr != nil {
		result.BindMode = ""
		result.Error = "trusted Bubblewrap does not support the requested Stage 6 bind mode"
		return result
	}
	result.BindMode = mode
	return result
}

// NewWorkerLauncherForStage6 selects one supported mode for the disposable
// Stage 6 study. It is absent from non-study builds and does not alter normal
// launcher selection.
func NewWorkerLauncherForStage6(bwrapPath, workerPath string, mode BindMode, holdBeforeRead bool) (WorkerLauncher, error) {
	return newWorkerLauncherWithBindMode(bwrapPath, workerPath, holdBeforeRead, mode)
}

// NewWorkerLauncherForStage6Image launches a separately built, source-pinned
// cmd/harness image as the Stage 6 worker. The expected digest comes from the
// bounded runner immediately after it builds the image. The image is copied
// into a sealed snapshot and checked again there before Bubblewrap receives
// it, so a path replacement or concurrent in-place write cannot change the
// executed bytes. This study-only API supports snapshot mode only.
func NewWorkerLauncherForStage6Image(bwrapPath, imagePath, expectedSHA256 string, holdBeforeRead bool) (WorkerLauncher, error) {
	if err := validateTrustedExecutable(bwrapPath, true); err != nil {
		return nil, errors.New("trusted Bubblewrap validation failed")
	}
	_, capability, err := probeBubblewrap(bwrapPath)
	if err != nil {
		return nil, errors.New("Bubblewrap compatibility preflight failed")
	}
	if !filepath.IsAbs(imagePath) || filepath.Clean(imagePath) != imagePath {
		return nil, errors.New("cmd/harness worker image path is not canonical absolute path")
	}
	snapshot, err := sealedExpectedWorkerImage(imagePath, expectedSHA256)
	if err != nil {
		return nil, err
	}
	mode, err := selectWorkerBindModePreferred(capability, snapshot, BindModeFileSnapshot)
	if err != nil {
		_ = snapshot.Close()
		return nil, errors.New("trusted Bubblewrap cannot launch the sealed cmd/harness image")
	}
	capability.BindMode = mode
	return &workerLauncher{bwrapPath: bwrapPath, capability: capability, workerFile: snapshot, holdBeforeRead: holdBeforeRead}, nil
}

func sealedExpectedWorkerImage(path, expectedSHA256 string) (*os.File, error) {
	expected, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(expected) != sha256.Size || strings.ToLower(expectedSHA256) != expectedSHA256 {
		return nil, errors.New("cmd/harness worker image digest is invalid")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("cmd/harness worker image could not be opened without following links")
	}
	source := os.NewFile(uintptr(fd), "stage6-cmd-harness-worker-source")
	if source == nil {
		_ = unix.Close(fd)
		return nil, errors.New("cmd/harness worker image descriptor is invalid")
	}
	defer source.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size <= 0 || stat.Size > maxWorkerSnapshotBytes {
		return nil, errors.New("cmd/harness worker image is not a bounded regular file")
	}
	if err := validateStaticWorkerFile(source); err != nil {
		return nil, errors.New("cmd/harness worker image is not a supported static ELF executable")
	}
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		return nil, errors.New("cmd/harness worker image could not be sealed")
	}
	if err := validateStaticWorkerFile(snapshot); err != nil {
		_ = snapshot.Close()
		return nil, errors.New("sealed cmd/harness worker image is invalid")
	}
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		_ = snapshot.Close()
		return nil, errors.New("sealed cmd/harness worker image could not be rewound")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, snapshot); err != nil || subtle.ConstantTimeCompare(digest.Sum(nil), expected) != 1 {
		_ = snapshot.Close()
		return nil, errors.New("sealed cmd/harness worker image does not match its recorded digest")
	}
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		_ = snapshot.Close()
		return nil, errors.New("sealed cmd/harness worker image could not be rewound")
	}
	return snapshot, nil
}
