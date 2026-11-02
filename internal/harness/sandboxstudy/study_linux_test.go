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
	"errors"
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
	if _, err := readLineBounded(bufio.NewReader(strings.NewReader(line))); err == nil {
		t.Fatal("oversized protocol line accepted")
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
