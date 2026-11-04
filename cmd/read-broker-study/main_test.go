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
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
)

type ownerLifecycleTestSession struct {
	registry      *readbrokerstudy.Registry
	policy        uint64
	firstRegister error
	registerCalls int
}

type lifecycleGateTestWorker struct {
	terminated int
}

func (*lifecycleGateTestWorker) Ready(context.Context) error { return nil }
func (*lifecycleGateTestWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*lifecycleGateTestWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return nil
}
func (*lifecycleGateTestWorker) AwaitReadStarted(context.Context) error { return nil }
func (*lifecycleGateTestWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, nil
}
func (w *lifecycleGateTestWorker) Terminate() error {
	w.terminated++
	return nil
}
func (*lifecycleGateTestWorker) Wait() error  { return nil }
func (*lifecycleGateTestWorker) Joined() bool { return true }

func TestTerminationRequestGateDelaysOnlyRealTermination(t *testing.T) {
	gate := newTerminationRequestGate()
	worker := &lifecycleGateTestWorker{}
	gate.attach(worker)
	if err := gate.requestTermination(worker); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.requestedSignal():
	default:
		t.Fatal("termination request was not observable")
	}
	if worker.terminated != 0 {
		t.Fatal("termination gate acted on the worker before release")
	}
	if err := gate.release(); err != nil {
		t.Fatal(err)
	}
	if worker.terminated != 1 {
		t.Fatalf("termination calls=%d, want one actual worker termination", worker.terminated)
	}
}

func (s *ownerLifecycleTestSession) Read(context.Context, string, string, string) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, errors.New("unexpected read")
}
func (s *ownerLifecycleTestSession) Register(registration readbrokerstudy.OwnerRegistration) error {
	return s.RegisterRotation(registration.Owner, []readbrokerstudy.OwnerRegistration{registration})
}
func (s *ownerLifecycleTestSession) RegisterRotation(owner string, registrations []readbrokerstudy.OwnerRegistration) error {
	s.registerCalls++
	if err := s.registry.RegisterRotation(owner, registrations); err != nil {
		return err
	}
	s.policy = s.registry.View().Generation
	if s.registerCalls == 1 {
		return s.firstRegister
	}
	return nil
}
func (*ownerLifecycleTestSession) SelectRoot(string) error                          { return nil }
func (*ownerLifecycleTestSession) Revoke(string) error                              { return nil }
func (*ownerLifecycleTestSession) SetCheckpointHook(readbrokerstudy.CheckpointHook) {}
func (*ownerLifecycleTestSession) PublishedResults() [][]byte                       { return nil }
func (*ownerLifecycleTestSession) Evidence() []readbrokerstudy.ReadEvidence         { return nil }
func (*ownerLifecycleTestSession) ActiveOperationCount() int                        { return 0 }
func (s *ownerLifecycleTestSession) GenerationSnapshot() readbrokerstudy.GenerationSet {
	return readbrokerstudy.GenerationSet{Policy: s.policy}
}
func (*ownerLifecycleTestSession) Close(context.Context) error { return nil }

func TestFixtureOwnerWriterAllowsConfirmedJoinForBothRegistrationResults(t *testing.T) {
	for _, test := range []struct {
		name        string
		firstResult error
		wantCalls   int
	}{
		{name: "already joined registration", firstResult: nil, wantCalls: 1},
		{name: "cleanup pending then joined", firstResult: readbrokerstudy.ErrCleanupPending, wantCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owner-target.fixture")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			writeFile, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer writeFile.Close()
			registry, err := readbrokerstudy.NewRegistry()
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()
			session := &ownerLifecycleTestSession{registry: registry, policy: registry.View().Generation, firstRegister: test.firstResult}
			owner := &fixtureOwnerWriter{
				session: session, registry: registry, owner: "test-owner",
				registrations: []readbrokerstudy.OwnerRegistration{{Owner: "test-owner", Class: readbrokerstudy.ClassCredential, Path: path}},
				writeFile:     writeFile, contents: []byte("synthetic protected write"),
			}
			beginErr := owner.Begin()
			if !errors.Is(beginErr, test.firstResult) {
				if beginErr != nil || test.firstResult != nil {
					t.Fatalf("Begin error=%v, want %v", beginErr, test.firstResult)
				}
			}
			readDone := make(chan struct{})
			close(readDone)
			readEvidence := &readbrokerstudy.ReadEvidence{WorkerStarted: true, WorkerJoined: true}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			evidence := owner.Complete(ctx, readDone, readEvidence, true)
			actual, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !evidence.WorkerJoinConfirmed || !evidence.WriteAuthorized || !evidence.WriteStarted || !evidence.WriteCompleted || !evidence.WriteDescriptorMatchesPin {
				t.Fatalf("confirmed join did not authorize the pinned write: %+v", evidence)
			}
			if string(actual) != string(owner.contents) || session.registerCalls != test.wantCalls {
				t.Fatalf("write=%q, registrations=%d; want %q and %d", actual, session.registerCalls, owner.contents, test.wantCalls)
			}
		})
	}
}

type injectedProviderLauncher struct {
	worker *injectedProviderWorker
	starts int
}

func (l *injectedProviderLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	l.starts++
	return l.worker, nil
}
func (*injectedProviderLauncher) Close() error { return nil }

type injectedProviderWorker struct {
	result   []byte
	override bool
	started  bool
	joined   bool
}

func (*injectedProviderWorker) Ready(context.Context) error { return nil }
func (*injectedProviderWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{
		NoNewPrivs: true, SeccompMode: 2, SocketCreationDenied: true, PathOpenDenied: true,
		ReadCapabilityInitiallyAbsent: true, OnlyProtocolDescriptor: true, EnvironmentCleared: true,
	}
}
func (w *injectedProviderWorker) TransferCapability(_ context.Context, file *os.File, _ readbrokerstudy.FileIdentity) error {
	content, err := io.ReadAll(file)
	if err == nil && !w.override {
		w.result = content
	}
	return err
}
func (w *injectedProviderWorker) AwaitReadStarted(context.Context) error {
	w.started = true
	return nil
}
func (w *injectedProviderWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadSucceeded, Bytes: append([]byte(nil), w.result...)}, nil
}
func (w *injectedProviderWorker) Terminate() error { w.joined = true; return nil }
func (w *injectedProviderWorker) Wait() error      { w.joined = true; return nil }
func (w *injectedProviderWorker) Joined() bool     { return w.joined }

func newInjectedProviderFixture(t *testing.T, workerBytes []byte) (*providerFixture, *injectedProviderLauncher) {
	t.Helper()
	root := t.TempDir()
	provider := filepath.Join(root, "provider.fixture")
	alias := filepath.Join(root, "provider-alias.fixture")
	ordinary := filepath.Join(root, "ordinary.fixture")
	providerContents := []byte("fixture provider contents expected by the test\n")
	if err := os.WriteFile(provider, providerContents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ordinary, []byte("ordinary control fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(provider, alias); err != nil {
		t.Fatal(err)
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(readbrokerstudy.OwnerRegistration{Owner: "test-provider", Class: readbrokerstudy.ClassProvider, Path: provider}); err != nil {
		t.Fatal(err)
	}
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	mutating := &studyMutatingAuthorizer{
		delegate: authorizer, attempts: make(map[string]int), receipts: make(map[string]readbrokerstudy.AuthorizationReceipt),
		returned: make(map[string]readbrokerstudy.AuthorizationReceipt), reuse: make(map[string]string),
		mutate: make(map[string]func(*readbrokerstudy.AuthorizationReceipt)), after: make(map[string]func(readbrokerstudy.AuthorizationRequest)),
	}
	launcher := &injectedProviderLauncher{worker: &injectedProviderWorker{result: append([]byte(nil), workerBytes...), override: workerBytes != nil}}
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), launcher, mutating)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SelectRoot(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeStudySession(session) })
	return &providerFixture{root: root, provider: provider, providerContents: providerContents, alias: alias, ordinary: ordinary, registry: registry, authorizer: authorizer, mutating: mutating, session: session}, launcher
}

func TestProviderAuthorizationControlsUseActualFixtureBytes(t *testing.T) {
	t.Run("ordinary positive control", func(t *testing.T) {
		fixture, _ := newInjectedProviderFixture(t, nil)
		row := executeCase(fixture.session, fixture.authorizer, fixture.root, "provider_only_ordinary_positive_control", fixture.ordinary, "ordinary-positive-control", "ordinary provider control fixture reads under its own once authorization")
		row.AuthorizationReceiptAccepted = row.Status == "measured_pass"
		if row.Status != "measured_pass" || !row.BytesMatchApproved || !row.AuthorizationReceiptAccepted || row.OnceGrantConsumptions != 1 {
			t.Fatalf("ordinary positive control did not pass: %+v", row)
		}
	})

	t.Run("provider bytes match and replay is refused", func(t *testing.T) {
		fixture, launcher := newInjectedProviderFixture(t, []byte("fixture provider contents expected by the test\n"))
		row := runProviderReadCase(fixture, "provider_explicit_once_success_and_replay", "provider-once", fixture.provider, true, "provider result matches its captured fixture")
		if row.Status != "measured_pass" || !row.BytesMatchApproved || !row.ReplayPrevented || row.ReplayAdditionalWorkers != 0 || launcher.starts != 1 {
			t.Fatalf("correct provider content did not reach successful replay verification: row=%+v worker_starts=%d", row, launcher.starts)
		}
	})

	t.Run("wrong provider bytes fail content correlation", func(t *testing.T) {
		fixture, launcher := newInjectedProviderFixture(t, []byte("wrong synthetic bytes\n"))
		row := runProviderReadCase(fixture, "provider_explicit_once_success_and_replay", "provider-wrong", fixture.provider, true, "provider result must match its captured fixture")
		if row.Status == "measured_pass" || row.BytesMatchApproved || row.ReplayPrevented || row.ReplayOutcome != "" || launcher.starts != 1 {
			t.Fatalf("incorrect provider content was accepted or replayed: row=%+v worker_starts=%d", row, launcher.starts)
		}
	})
}

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
func (*directoryCoverageSequenceSession) ActiveOperationCount() int                        { return 0 }
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

func TestCaseReadEvidenceSerializationPreservesSuccessAndUnexpectedActivity(t *testing.T) {
	generations := readbrokerstudy.GenerationSet{Root: 3, Policy: 5, Grant: 7}
	capabilityIdentity := readbrokerstudy.FileIdentity{Device: 11, Inode: 13, Type: 15}
	cases := []struct {
		name        string
		result      readbrokerstudy.WorkerResult
		evidence    readbrokerstudy.ReadEvidence
		wantBytes   bool
		wantOutcome readbrokerstudy.ReadOutcome
	}{
		{
			name:      "successful read",
			result:    readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadSucceeded, Bytes: []byte("synthetic")},
			evidence:  readbrokerstudy.ReadEvidence{Case: "positive-1", Outcome: readbrokerstudy.ReadSucceeded, Generations: generations, WorkerStarted: true, WorkerReady: true, WorkerJoined: true, CapabilitySent: true, CapabilityIdentity: capabilityIdentity, ReadStarted: true, Published: true, Effect: "read and published synthetic fixture", Limitation: "fixture only"},
			wantBytes: true, wantOutcome: readbrokerstudy.ReadSucceeded,
		},
		{
			name:      "unexpected worker activity",
			result:    readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadRefused, Detail: readbrokerstudy.ErrProtected.Error()},
			evidence:  readbrokerstudy.ReadEvidence{Case: "unexpected-1", Outcome: readbrokerstudy.ReadSucceeded, Generations: generations, WorkerStarted: true, WorkerReady: true, WorkerJoined: true, CapabilitySent: true, CapabilityIdentity: capabilityIdentity, ReadStarted: true, Published: true, Effect: "unexpected read", Limitation: "policy mismatch"},
			wantBytes: false, wantOutcome: readbrokerstudy.ReadSucceeded,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := caseResult{Name: test.name}
			applyReadEvidence(&row, test.result, test.evidence, true)
			encoded, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if row.ResultBytesReturned != test.wantBytes || !row.ReadEvidencePresent || row.ReadEvidenceCase != test.evidence.Case || row.WorkerOutcome != test.wantOutcome || row.WorkerGenerations == nil || *row.WorkerGenerations != generations || row.WorkerEffect != test.evidence.Effect || row.WorkerLimitation != test.evidence.Limitation || row.CapabilityIdentity == nil || *row.CapabilityIdentity != capabilityIdentity {
				t.Fatalf("row lost read evidence: %+v", row)
			}
			if decoded["read_evidence_present"] != true || decoded["read_evidence_case"] != test.evidence.Case || decoded["worker_outcome"] != string(test.wantOutcome) || decoded["worker_effect"] != test.evidence.Effect || decoded["worker_limitation"] != test.evidence.Limitation || decoded["worker_generations"] == nil || decoded["capability_identity"] == nil {
				t.Fatalf("serialized row lost evidence fields: %s", encoded)
			}
		})
	}
}

func TestUnavailableReportPreservesBaselineAndNativeActorCaseInventory(t *testing.T) {
	planned := []string{
		"ordinary_positive", "protected_direct", "provider_source_explicit_once",
		"protected_existing_hardlink", "protected_late_hardlink",
		"protected_object_renamed_into_ordinary_tree", "protected_location_replaced_before_open",
		"ordinary_object_moved_into_protected_location", "cancel_before_release",
		"revoke_after_capture_before_publish", "registered_directory_descendant_inside",
		"registered_directory_descendant_moved_out", "actor_late_alias_existing_invocation",
		"late_alias_protected_route_refusal",
		"actor_selected_root_relocated_after_scan", "actor_parent_relocated_outside_root_after_scan",
		"actor_target_replaced_after_classification", "actor_unregistered_protected_descendant_moved_out",
		"actor_protected_ancestor_relocation", "study_session_cleanup",
		"owner_registration_before_release", "owner_registration_before_release_registered_identity_alias_denied",
		"owner_registration_while_capability_held", "owner_registration_while_capability_held_registered_identity_alias_denied",
		"owner_registration_after_capture_suppresses_stale_publication",
		"owner_rotation_retains_old_new_pins_and_publishes_registered_replacement",
		"owner_rotation_old_alias_denied", "owner_rotation_renamed_old_identity_denied", "owner_rotation_new_alias_denied",
		"owner_registration_failure_blocks_write", "owner_incomplete_cleanup_blocks_write",
		"coordinator_death_with_held_capability", "shutdown_deadline_retains_cleanup_obligation",
		"owner_incomplete_cleanup_blocks_write_native",
		"provider_missing_explicit_once", "provider_only_ordinary_positive_control",
		"provider_ordinary_receipt_rejected_for_alias", "provider_stale_policy_receipt_rejected",
		"provider_stale_root_receipt_rejected", "provider_stale_grant_receipt_rejected",
		"provider_explicit_once_success_and_replay",
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
		if result.Status != "unavailable" || result.Classification != "unavailable" || result.WorkerStarted || result.CapabilitySent || result.CapabilityHeld || result.CapabilityIdentity != nil || result.ReadStarted || result.Published || result.ActorStarted || result.ActorTransition || result.AuthorizationAttempted || result.AuthorizationReceiptAccepted || result.OwnerLifecycle != nil || result.Lifecycle != nil {
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
