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

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostSnapshotDoesNotProbeUntrustedBackend(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "backend-invoked")
	backend := filepath.Join(dir, "fake-bwrap")
	script := "#!/bin/sh\nprintf invoked > " + marker + "\n"
	if err := os.WriteFile(backend, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	snapshot := hostSnapshot(backend, "test-commit")
	if snapshot.BubblewrapVersion != "" || snapshot.BubblewrapSHA256 != "" || snapshot.BubblewrapOwner != "" || snapshot.BubblewrapMode != "" {
		t.Fatalf("untrusted backend metadata should remain unavailable: %+v", snapshot)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("untrusted backend was executed before trust validation: stat marker: %v", err)
	}
}
