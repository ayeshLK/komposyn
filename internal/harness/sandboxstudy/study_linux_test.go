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

package sandboxstudy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSeccompFilterIsArchitectureCheckedAndBounded(t *testing.T) {
	filter, err := seccompFilter()
	if err != nil {
		t.Fatal(err)
	}
	path := filter.Name()
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data)%8 != 0 || len(data) > 64*1024 {
		t.Fatalf("unexpected BPF length %d", len(data))
	}
	if info, err := filter.Stat(); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("seccomp input mode info=%v err=%v", info, err)
	}
}

func TestProfileRejectsRelativeBackendAndWorker(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Profile("bwrap", "/worker", root, ""); err == nil {
		t.Fatal("relative backend was accepted")
	}
	if _, _, err := Profile("/usr/bin/bwrap", "worker", root, ""); err == nil {
		t.Fatal("relative worker was accepted")
	}
}

func TestProfileRejectsUnsafeBackendAndProtectedTraversal(t *testing.T) {
	dir := t.TempDir()
	backend := filepath.Join(dir, "backend")
	if err := os.WriteFile(backend, []byte("fixture"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Profile(backend, "/worker", dir, ""); err == nil {
		t.Fatal("writable backend path was accepted")
	}
	if _, _, err := Profile("/usr/bin/bwrap", "/worker", dir, "../outside"); err == nil {
		t.Fatal("protected traversal was accepted")
	}
}

func TestPrivateProcProfileIsExplicitAndOptIn(t *testing.T) {
	base := []string{"--tmpfs", "/", "--unshare-pid"}
	if got := appendPrivateProc(append([]string(nil), base...), false); strings.Contains(strings.Join(got, " "), "--proc") {
		t.Fatalf("default profile unexpectedly mounts proc: %v", got)
	}
	got := appendPrivateProc(append([]string(nil), base...), true)
	if strings.Join(got, " ") != "--tmpfs / --unshare-pid --proc /proc" {
		t.Fatalf("private proc profile args=%v", got)
	}
}

type protocolInput struct {
	strings.Builder
	closed bool
}

func (p *protocolInput) Close() error {
	p.closed = true
	return nil
}

func TestCreateCheckpointKeepsControlPipeOpenUntilContinue(t *testing.T) {
	input := &protocolInput{}
	if err := releaseWorker(input, true); err != nil {
		t.Fatal(err)
	}
	if input.String() != "RELEASE\n" || input.closed {
		t.Fatalf("release protocol=%q closed=%t; checkpoint worker must still be able to await CONTINUE", input.String(), input.closed)
	}
	if err := continueWorker(input); err != nil {
		t.Fatal(err)
	}
	if input.String() != "RELEASE\nCONTINUE\n" || !input.closed {
		t.Fatalf("continued protocol=%q closed=%t", input.String(), input.closed)
	}
	standard := &protocolInput{}
	if err := releaseWorker(standard, false); err != nil {
		t.Fatal(err)
	}
	if standard.String() != "RELEASE\n" || !standard.closed {
		t.Fatalf("standard release protocol=%q closed=%t", standard.String(), standard.closed)
	}
}

func TestBoundedOutputAndLiteralSanitization(t *testing.T) {
	buffer := boundedBuffer{limit: 8}
	if n, err := buffer.Write([]byte("0123456789")); err != nil || n != 10 {
		t.Fatalf("Write=(%d,%v)", n, err)
	}
	if got := buffer.String(); got != "01234567" {
		t.Fatalf("bounded output=%q", got)
	}
	if got := sanitize("literal\x1b[31m\x00text"); strings.ContainsAny(got, "\x00\x1b") {
		t.Fatalf("control character remained: %q", got)
	}
}

func TestReadLineBoundedRejectsOversizeFixture(t *testing.T) {
	line := strings.Repeat("x", maxLineBytes+1) + "\n"
	if _, err := readLineBounded(bufio.NewReader(strings.NewReader(line))); !errors.Is(err, errOutputLimit) {
		t.Fatal("oversized protocol line accepted")
	}
}

func TestOutputLimitRequiresExactLimitAndConfirmedReap(t *testing.T) {
	if got := classifyOutputBound("flood", errOutputLimit, true); got != "measured_pass" {
		t.Fatalf("confirmed oversized line classified as %q", got)
	}
	for _, readErr := range []error{io.EOF, context.DeadlineExceeded, errors.New("worker failed")} {
		if got := classifyOutputBound("flood", readErr, true); got != "unavailable" {
			t.Errorf("read error %v classified as %q", readErr, got)
		}
	}
	if got := classifyOutputBound("flood", errOutputLimit, false); got != "unavailable" {
		t.Fatalf("unreaped worker classified as %q", got)
	}
	actual, status := verifyObserved("bounded-output", t.TempDir(), Observation{WorkerOutcome: "failed"})
	if status != "unavailable" || actual != "" {
		t.Fatalf("failed worker became output pass: actual=%q status=%q", actual, status)
	}
	actual, status = verifyObserved("bounded-output", t.TempDir(), Observation{ActualEffect: "premature EOF", WorkerOutcome: "failed", Status: "unknown"})
	if status != "unavailable" || actual != "premature EOF" {
		t.Fatalf("premature EOF classification: actual=%q status=%q", actual, status)
	}
	if _, err := parseWorkerResult("not-json"); err == nil {
		t.Fatal("malformed worker result was accepted")
	}
	malformed := unknownAfterRelease("flood", "fixture", "malformed worker result")
	actual, status = verifyObserved("bounded-output", t.TempDir(), malformed)
	if status != "unavailable" || !strings.Contains(malformed.Limitation, "malformed worker result") {
		t.Fatalf("malformed worker result classification: actual=%q status=%q limitation=%q", actual, status, malformed.Limitation)
	}
	actual, status = verifyObserved("bounded-output", t.TempDir(), Observation{ActualEffect: "worker exited early", WorkerOutcome: "failed", Status: "measured"})
	if status != "unavailable" || actual != "worker exited early" {
		t.Fatalf("failed worker result classification: actual=%q status=%q", actual, status)
	}
}

func TestScenarioFixtureDirectoriesAreIsolatedSequentially(t *testing.T) {
	suite := t.TempDir()
	for _, test := range scenarios() {
		t.Run(test.name, func(t *testing.T) {
			caseRoot := filepath.Join(suite, test.name)
			scope := filepath.Join(caseRoot, "scope")
			outside := filepath.Join(caseRoot, "outside")
			if err := os.MkdirAll(scope, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(outside, 0o700); err != nil {
				t.Fatal(err)
			}
			actor := func(action, actorScope, actorOutside string) error {
				return applyFixtureActor(action, actorScope, actorOutside)
			}
			afterReady, err := test.setup(scope, outside)
			if test.actorSetup != nil {
				afterReady, err = test.actorSetup(scope, outside, actor)
			}
			if err != nil {
				t.Fatalf("scenario setup: %v", err)
			}
			if _, err := afterReady(); err != nil && !errors.Is(err, errCancelBeforeRelease) {
				t.Fatalf("outside actor setup: %v", err)
			}
			if test.name == "selected-root-relocation" {
				if err := os.WriteFile(filepath.Join(outside, "moved-root", "root-effect"), []byte("synthetic worker effect"), 0o600); err != nil {
					t.Fatalf("simulate first worker effect: %v", err)
				}
			}
		})
	}
}

func TestActorRejectsPathsOutsideDisposableFixture(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "scope")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(scope, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := ActorMain([]string{"insert-collision", scope, outside}); code == 0 {
		t.Fatal("actor accepted non-study temp paths")
	}
}

func TestUnknownCreateDescriptionRetainsUnknownAndNeverRetries(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "scope")
	parent := filepath.Join(scope, "parent")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "unknown.txt"), []byte("synthetic create fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual := verifyUnknownCreate(scope, Observation{Status: "unknown"})
	if !strings.Contains(actual, "did not retry") || !strings.Contains(actual, "remains unknown") {
		t.Fatalf("unknown create was not honestly described: %q", actual)
	}
	if data, err := os.ReadFile(filepath.Join(parent, "unknown.txt")); err != nil || string(data) != "synthetic create fixture\n" {
		t.Fatalf("fixture changed during no-replay classification: %q, %v", data, err)
	}
}

func TestCreateOutcomeClassificationKeepsPartialUnknownAndUnavailableDistinct(t *testing.T) {
	scope := t.TempDir()
	if err := os.Mkdir(filepath.Join(scope, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scope, "parent", "partial.txt"), []byte("synthetic prefix"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, status := verifyObserved("create-one-partial-outcome", scope, Observation{WorkerOutcome: "partial"})
	if status != "measured_pass" || !strings.Contains(actual, "incomplete") {
		t.Fatalf("partial outcome classification: actual=%q status=%q", actual, status)
	}
	actual, status = verifyObserved("create-one-otmpfile-publication", scope, Observation{WorkerOutcome: "unavailable", ActualEffect: "no proc mount"})
	if status != "unavailable" || !strings.Contains(actual, "no proc mount") {
		t.Fatalf("unavailable O_TMPFILE classification: actual=%q status=%q", actual, status)
	}
	actual, status = verifyObserved("create-one-late-hardlink", scope, Observation{WorkerOutcome: "succeeded"})
	if status != "unavailable" {
		t.Fatalf("missing late-alias observation classified as %q: %s", status, actual)
	}
}

func TestPrivateHomeProbeProductionAndClassification(t *testing.T) {
	empty := t.TempDir()
	if got := probeHome(empty); got != "home_config_not_present" {
		t.Fatalf("empty private HOME marker = %q", got)
	}
	t.Setenv("HOME", empty)
	if probe := sandboxProbe("0"); !strings.Contains(probe, "home_config_not_present") {
		t.Fatalf("production probe omitted empty HOME marker: %s", probe)
	}
	if got := classifyProbe("environment_clear fd3_closed socket_denied no_new_privs=1 seccomp_mode=2 host_root_not_mounted proc_not_mounted home_not_mounted " + probeHome(empty) + " shell_lookup_unavailable host_pid_unreachable"); got != "measured_pass" {
		t.Fatalf("clean private HOME did not classify as pass: %q", got)
	}
	populated := t.TempDir()
	if err := os.WriteFile(filepath.Join(populated, "fixture-config"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probeHome(populated); got != "home_config_present" {
		t.Fatalf("populated private HOME marker = %q", got)
	}
	t.Setenv("HOME", populated)
	if probe := sandboxProbe("0"); !strings.Contains(probe, "home_config_present") {
		t.Fatalf("production probe omitted populated HOME marker: %s", probe)
	}
	if got := classifyProbe("home_config_present"); got != "observed_violation" {
		t.Fatalf("populated private HOME classified as %q", got)
	}
	if got := probeHome(filepath.Join(t.TempDir(), "missing")); got != "home_dir_missing" {
		t.Fatalf("missing private HOME marker = %q", got)
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probeHome(filepath.Join(file, "child")); got != "home_read_error" {
		t.Fatalf("unreadable private HOME marker = %q", got)
	}
	t.Setenv("HOME", filepath.Join(file, "child"))
	if probe := sandboxProbe("0"); !strings.Contains(probe, "home_read_error") {
		t.Fatalf("production probe omitted HOME read-error marker: %s", probe)
	}
}

func TestScratchPassRequiresSuccessfulPrivateWriteAndNoHostFixture(t *testing.T) {
	root := t.TempDir()
	if actual, status := verifyScratch(root, Observation{WorkerOutcome: "succeeded"}); status != "measured_pass" || !strings.Contains(actual, "private /tmp") {
		t.Fatalf("successful private scratch result: actual=%q status=%q", actual, status)
	}
	if actual, status := verifyScratch(root, Observation{WorkerOutcome: "failed"}); status != "unavailable" || !strings.Contains(actual, "did not succeed") {
		t.Fatalf("failed private scratch result: actual=%q status=%q", actual, status)
	}
	if err := os.WriteFile(filepath.Join(root, "scratch.bin"), []byte("host leak fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if actual, status := verifyScratch(root, Observation{WorkerOutcome: "succeeded"}); status != "observed_violation" || !strings.Contains(actual, "host-visible") {
		t.Fatalf("host-visible scratch fixture: actual=%q status=%q", actual, status)
	}
}

func TestEvidenceClassificationNeverTreatsAnObservedEffectAsAPass(t *testing.T) {
	if got := classifyEffect(true); got != "observed_violation" {
		t.Fatalf("outside effect classified as %q", got)
	}
	if got := classifyEffect(false); got != "observed_prevented" {
		t.Fatalf("no effect classified as %q", got)
	}
	if got := classifyDenied(false); got != "observed_violation" {
		t.Fatalf("allowed denied-operation classified as %q", got)
	}
	if got := classifyProbe("environment_clear fd3_closed socket_denied no_new_privs=1 seccomp_mode=2 host_root_not_mounted proc_not_mounted home_not_mounted home_config_not_present shell_lookup_unavailable host_pid_unreachable"); got != "measured_pass" {
		t.Fatalf("complete isolation fixture classified as %q", got)
	}
	if got := classifyProbe("environment_clear fd3_closed socket_allowed"); got != "observed_violation" {
		t.Fatalf("allowed network socket classified as %q", got)
	}
}

func TestOperationGateRequiresOrderingAndSingleConsumptionOwner(t *testing.T) {
	gate := newOperationGate(4, true)
	if err := gate.AcknowledgeStartup("child"); !errors.Is(err, ErrGateOrder) {
		t.Fatalf("startup acknowledgement before registration: %v", err)
	}
	if err := gate.RegisterChild("child"); err != nil {
		t.Fatal(err)
	}
	if err := gate.AcknowledgeStartup("other"); !errors.Is(err, ErrGateOrder) {
		t.Fatalf("wrong child acknowledgement: %v", err)
	}
	if err := gate.AcknowledgeStartup("child"); err != nil {
		t.Fatal(err)
	}
	if err := gate.Release("child"); err != nil {
		t.Fatal(err)
	}
	if !gate.Released() {
		t.Fatal("operation was not released")
	}

	missingConsumption := newOperationGate(1, false)
	_ = missingConsumption.RegisterChild("child")
	_ = missingConsumption.AcknowledgeStartup("child")
	if err := missingConsumption.Release("child"); !errors.Is(err, ErrOnceNotConsumed) {
		t.Fatalf("unconsumed once grant released: %v", err)
	}
}

func TestOperationGateRevocationBeforeAndAfterRelease(t *testing.T) {
	before := newOperationGate(7, true)
	_ = before.RegisterChild("child")
	_ = before.AcknowledgeStartup("child")
	if stop := before.Revoke(8); stop {
		t.Fatal("pre-release revocation was reported as running work")
	}
	if err := before.Release("child"); !errors.Is(err, ErrGateCancelled) {
		t.Fatalf("revoked child released: %v", err)
	}

	after := newOperationGate(7, true)
	_ = after.RegisterChild("child")
	_ = after.AcknowledgeStartup("child")
	if err := after.Release("child"); err != nil {
		t.Fatal(err)
	}
	if stop := after.Revoke(8); !stop {
		t.Fatal("post-release revocation did not request termination")
	}
	if err := after.Release("child"); err == nil {
		t.Fatal("duplicate release accepted")
	}
}

func TestOperationGateCancellationBeforeAndAfterRelease(t *testing.T) {
	before := newOperationGate(3, true)
	_ = before.RegisterChild("child")
	_ = before.AcknowledgeStartup("child")
	if stop := before.Cancel(); stop {
		t.Fatal("pre-release cancellation reported a running effect")
	}
	if err := before.Release("child"); !errors.Is(err, ErrGateCancelled) {
		t.Fatalf("cancelled child released: %v", err)
	}

	after := newOperationGate(3, true)
	_ = after.RegisterChild("child")
	_ = after.AcknowledgeStartup("child")
	if err := after.Release("child"); err != nil {
		t.Fatal(err)
	}
	if stop := after.Cancel(); !stop {
		t.Fatal("post-release cancellation did not request termination")
	}
}

func TestOperationGateConcurrentReleaseAndRevocationHasOneLinearizedOutcome(t *testing.T) {
	for i := 0; i < 100; i++ {
		gate := newOperationGate(uint64(i+1), true)
		if err := gate.RegisterChild("child"); err != nil {
			t.Fatal(err)
		}
		if err := gate.AcknowledgeStartup("child"); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var releaseErr error
		var stop bool
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			releaseErr = gate.Release("child")
		}()
		go func() {
			defer wait.Done()
			<-start
			stop = gate.Revoke(uint64(i + 2))
		}()
		close(start)
		wait.Wait()
		if releaseErr == nil && !stop {
			t.Fatalf("iteration %d released without observing revocation or stop request", i)
		}
		if releaseErr != nil && stop {
			t.Fatalf("iteration %d both rejected release and claimed running child", i)
		}
	}
}
