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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
)

func TestInspectEnvironmentDoesNotRunUntrustedBubblewrapCandidate(t *testing.T) {
	base := t.TempDir()
	marker := filepath.Join(base, "executed")
	candidate := filepath.Join(base, "bwrap-fixture")
	script := "#!/bin/sh\nprintf invoked > " + marker + "\nprintf 'bubblewrap 0.99\\n'\n"
	if err := os.WriteFile(candidate, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	observed := inspectEnvironment(candidate)
	if observed.BubblewrapTrusted {
		t.Fatal("temporary writable-path backend was unexpectedly trusted")
	}
	if observed.BubblewrapVersion != "" {
		t.Fatalf("untrusted backend version was reported: %q", observed.BubblewrapVersion)
	}
	if !strings.Contains(observed.BubblewrapError, "skipped backend probes and sandbox launch") {
		t.Fatalf("trust diagnostic=%q, want explicit skipped-probe and launch status", observed.BubblewrapError)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("untrusted candidate executed and created marker: %v", err)
	}
	if !strings.Contains(observed.NamespaceProbeStatus, "not independently probed") {
		t.Fatalf("namespace probe status=%q, want a non-claiming status", observed.NamespaceProbeStatus)
	}
	encoded, err := json.Marshal(report{Environment: observed})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Environment environment `json:"environment"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Environment.BubblewrapTrusted || decoded.Environment.BubblewrapVersion != "" {
		t.Fatalf("serialized report inconsistently claims trust/version: %+v", decoded.Environment)
	}
}

func TestWorkerPolicyReportsPathBackedAndSnapshotModesAccurately(t *testing.T) {
	modernMount, modernVerification := workerMountPolicy(readbrokerstudy.BindModeDescriptor)
	if !strings.Contains(modernMount, "path-backed running-executable") ||
		!strings.Contains(modernMount, "mounted device/inode") || !strings.Contains(modernVerification, "/worker device/inode") {
		t.Fatalf("modern descriptor mode report is inaccurate: mount=%q verify=%q", modernMount, modernVerification)
	}
	modernSource := workerSnapshotPolicy(readbrokerstudy.BindModeDescriptor)
	if !strings.Contains(modernSource, "independently reopened read-only") || strings.Contains(modernSource, "sealed memfd") {
		t.Fatalf("modern worker source report is inaccurate: %q", modernSource)
	}
	legacyMount, legacyVerification := workerMountPolicy(readbrokerstudy.BindModeFileSnapshot)
	legacySource := workerSnapshotPolicy(readbrokerstudy.BindModeFileSnapshot)
	if !strings.Contains(legacyMount, "--ro-bind-data") || !strings.Contains(legacyVerification, "sealed verifier") ||
		!strings.Contains(legacySource, "sealed memfd") {
		t.Fatalf("snapshot mode report is inaccurate: mount=%q verify=%q", legacyMount, legacyVerification)
	}
}
