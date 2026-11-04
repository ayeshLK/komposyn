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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
)

type directoryCoverageSequenceSession struct {
	selectedRoot       string
	rootAtRegistration string
	readRoots          []string
	registrations      []readbrokerstudy.OwnerRegistration
}

func (s *directoryCoverageSequenceSession) SelectRoot(root string) error {
	s.selectedRoot = root
	return nil
}

func (s *directoryCoverageSequenceSession) Register(registration readbrokerstudy.OwnerRegistration) error {
	s.rootAtRegistration = s.selectedRoot
	s.registrations = append(s.registrations, registration)
	return nil
}

func (*directoryCoverageSequenceSession) RegisterRotation(string, []readbrokerstudy.OwnerRegistration) error {
	return nil
}

func (*directoryCoverageSequenceSession) Revoke(string) error                              { return nil }
func (*directoryCoverageSequenceSession) SetCheckpointHook(readbrokerstudy.CheckpointHook) {}
func (*directoryCoverageSequenceSession) PublishedResults() [][]byte                       { return nil }
func (*directoryCoverageSequenceSession) Evidence() []readbrokerstudy.ReadEvidence         { return nil }
func (*directoryCoverageSequenceSession) GenerationSnapshot() readbrokerstudy.GenerationSet {
	return readbrokerstudy.GenerationSet{}
}
func (*directoryCoverageSequenceSession) Close(context.Context) error { return nil }

func (s *directoryCoverageSequenceSession) Read(_ context.Context, root, target, _ string) (readbrokerstudy.WorkerResult, error) {
	s.readRoots = append(s.readRoots, s.selectedRoot)
	if root != s.selectedRoot {
		return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadRefused, Detail: readbrokerstudy.ErrOutOfScope.Error()}, nil
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadRefused, Detail: readbrokerstudy.ErrOutOfScope.Error()}, nil
	}
	if relative == filepath.Join("registered-directory", "descendant.fixture") {
		return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadRefused, Detail: readbrokerstudy.ErrProtected.Error()}, nil
	}
	if relative == "moved-directory-descendant.fixture" {
		return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadRefused, Detail: readbrokerstudy.ErrDirectoryCoverage.Error()}, nil
	}
	return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadUnknown}, nil
}

func TestDirectoryCoverageCasesRestoreRootAndRequireProtectedRefusals(t *testing.T) {
	root := t.TempDir()
	session := &directoryCoverageSequenceSession{selectedRoot: filepath.Join(root, "previous-actor-root")}
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	results, err := runDirectoryCoverageCases(session, authorizer, root)
	if err != nil {
		t.Fatalf("run directory coverage cases: %v", err)
	}
	if session.selectedRoot != root {
		t.Fatalf("selected root=%q, want restored fixture root %q", session.selectedRoot, root)
	}
	if len(results) != 2 {
		t.Fatalf("directory cases=%d, want two", len(results))
	}
	want := []struct {
		name   string
		detail string
	}{
		{name: "registered_directory_descendant_inside", detail: readbrokerstudy.ErrProtected.Error()},
		{name: "registered_directory_descendant_moved_out", detail: readbrokerstudy.ErrDirectoryCoverage.Error()},
	}
	for i, expected := range want {
		result := results[i]
		if result.Name != expected.name || result.Status != "measured_pass" || result.Observed != string(readbrokerstudy.ReadRefused) || result.Detail != expected.detail {
			t.Errorf("directory case %d=%+v, want protected refusal %q", i, result, expected.detail)
		}
	}
	if len(session.registrations) != 1 || !session.registrations[0].Directory {
		t.Fatalf("directory registrations=%+v, want one registered directory", session.registrations)
	}
	if session.rootAtRegistration != root || len(session.readRoots) != 2 || session.readRoots[0] != root || session.readRoots[1] != root {
		t.Fatalf("directory sequence root=%q reads=%v, want root restored before registration and both reads", session.rootAtRegistration, session.readRoots)
	}
}

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

func TestUnavailableReportPreservesBaselineAndNativeActorCaseInventory(t *testing.T) {
	planned := []string{
		"ordinary_positive", "protected_direct", "provider_source_explicit_once",
		"protected_existing_hardlink", "protected_late_hardlink",
		"protected_object_renamed_into_ordinary_tree", "protected_location_replaced_before_open",
		"ordinary_object_moved_into_protected_location", "cancel_before_release",
		"revoke_after_capture_before_publish", "registered_directory_descendant_inside",
		"registered_directory_descendant_moved_out", "actor_protected_alias_scan_to_handoff",
		"actor_selected_root_relocated_after_scan", "actor_parent_relocated_outside_root_after_scan",
		"actor_target_replaced_after_classification", "actor_unregistered_protected_descendant_moved_out",
		"actor_protected_ancestor_relocation", "study_session_cleanup",
	}
	cases := unavailableCases("trusted Bubblewrap unavailable")
	if len(cases) != len(planned) {
		t.Fatalf("unavailable cases=%d, want all %d planned scenarios", len(cases), len(planned))
	}
	seen := make(map[string]bool, len(cases))
	for _, result := range cases {
		if seen[result.Name] {
			t.Fatalf("duplicate unavailable case %q", result.Name)
		}
		seen[result.Name] = true
		if result.Status != "unavailable" || result.Classification != "unavailable" || result.WorkerStarted || result.CapabilitySent || result.ReadStarted || result.Published || result.ActorStarted || result.ActorTransition {
			t.Fatalf("unavailable case claims activity: %+v", result)
		}
	}
	for _, name := range planned {
		if !seen[name] {
			t.Errorf("unavailable report omitted planned case %q", name)
		}
	}
	encoded, err := json.Marshal(cases[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["generations_before"]; present {
		t.Fatal("unavailable scenario serialized an unmeasured generation snapshot")
	}
}
