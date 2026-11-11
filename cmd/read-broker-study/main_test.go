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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"golang.org/x/sys/unix"
)

type ownerLifecycleTestSession struct {
	registry      *readbrokerstudy.Registry
	policy        uint64
	firstRegister error
	registerCalls int
	closeErr      error
	evidence      []readbrokerstudy.ReadEvidence
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

func TestChildSubreaperGetterSetterAndRestore(t *testing.T) {
	before, err := childSubreaperSetting()
	if err != nil {
		t.Fatalf("read prior child-subreaper setting: %v", err)
	}
	restore, err := enableChildSubreaper()
	if err != nil {
		t.Fatalf("enable child subreaper: %v", err)
	}
	t.Cleanup(restore)
	during, err := childSubreaperSetting()
	if err != nil {
		t.Fatalf("read enabled child-subreaper setting: %v", err)
	}
	if during != 1 {
		t.Fatalf("child-subreaper setting while enabled=%d, want 1", during)
	}
	restore()
	after, err := childSubreaperSetting()
	if err != nil {
		t.Fatalf("read restored child-subreaper setting: %v", err)
	}
	if after != before {
		t.Fatalf("restored child-subreaper setting=%d, prior value was %d", after, before)
	}
}

func TestLifecycleGateMediatesContextBoundChildCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	launcher := &observedWorkerLauncher{inner: contextBoundTestLauncher{}, gate: newTerminationRequestGate()}
	worker, err := launcher.Start(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	observed := worker.(*observedSandboxWorker)
	processWorker := observed.SandboxWorker.(*contextBoundTestWorker)
	pidfd, err := unix.PidfdOpen(processWorker.cmd.Process.Pid, 0)
	if err != nil {
		cancel()
		_ = launcher.gate.release()
		_ = processWorker.Wait()
		t.Skipf("pidfd unavailable for context-bound child: %v", err)
	}
	defer unix.Close(pidfd)
	t.Cleanup(func() {
		cancel()
		_ = launcher.gate.release()
		_ = processWorker.Wait()
	})
	cancel()
	time.Sleep(50 * time.Millisecond)
	alive, err := pidfdIsAlive(pidfd)
	if err != nil {
		t.Fatalf("observe context-bound child after cancellation: %v", err)
	}
	if !alive {
		t.Fatal("operation context cancellation bypassed the fixture termination gate")
	}
	if err := observed.Terminate(); err != nil {
		t.Fatalf("request gated termination: %v", err)
	}
	select {
	case <-launcher.gate.requestedSignal():
	default:
		t.Fatal("cancellation-triggered termination request was not recorded")
	}
	alive, err = pidfdIsAlive(pidfd)
	if err != nil {
		t.Fatalf("observe context-bound child before gate release: %v", err)
	}
	if !alive {
		t.Fatal("child exited before the gate was released")
	}
	if err := launcher.gate.release(); err != nil {
		t.Fatalf("release real child termination: %v", err)
	}
	joined := make(chan struct{})
	go func() {
		_ = processWorker.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("context-bound child did not exit after the gate released termination")
	}
	if !processWorker.Joined() {
		t.Fatal("direct child was not waited after termination")
	}
	alive, err = pidfdIsAlive(pidfd)
	if err != nil || alive {
		t.Fatalf("child remains alive after gated termination: alive=%t err=%v", alive, err)
	}
}

func TestLifecycleGateChildHelper(t *testing.T) {
	if os.Getenv("KOMPOSYN_LIFECYCLE_GATE_CHILD") != "1" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}

type contextBoundTestLauncher struct{}

func (contextBoundTestLauncher) Close() error { return nil }

func (contextBoundTestLauncher) Start(ctx context.Context) (readbrokerstudy.SandboxWorker, error) {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifecycleGateChildHelper$")
	cmd.Env = append(os.Environ(), "KOMPOSYN_LIFECYCLE_GATE_CHILD=1")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &contextBoundTestWorker{cmd: cmd}, nil
}

type contextBoundTestWorker struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	mu       sync.Mutex
	waitErr  error
	joined   bool
}

func (*contextBoundTestWorker) Ready(context.Context) error { return nil }
func (*contextBoundTestWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*contextBoundTestWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return nil
}
func (*contextBoundTestWorker) AwaitReadStarted(context.Context) error { return nil }
func (*contextBoundTestWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, nil
}
func (w *contextBoundTestWorker) Terminate() error {
	err := w.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
func (w *contextBoundTestWorker) Wait() error {
	w.waitOnce.Do(func() {
		err := w.cmd.Wait()
		w.mu.Lock()
		w.waitErr = err
		w.joined = true
		w.mu.Unlock()
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.waitErr
}
func (w *contextBoundTestWorker) Joined() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.joined
}

func TestCoordinatorDeathRejectsStaleHeldProcessObservation(t *testing.T) {
	selfPIDFD, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Skipf("pidfd unavailable: %v", err)
	}
	defer unix.Close(selfPIDFD)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLifecycleGateChildHelper$")
	cmd.Env = append(os.Environ(), "KOMPOSYN_LIFECYCLE_GATE_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadPIDFD, err := unix.PidfdOpen(cmd.Process.Pid, 0)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Skipf("pidfd for fixture subprocess unavailable: %v", err)
	}
	defer unix.Close(deadPIDFD)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("fixture subprocess unexpectedly exited without a kill")
	}
	initAlive, workerAlive, err := heldWorkerProcessesLive(selfPIDFD, deadPIDFD)
	if err != nil {
		t.Fatalf("check held-event process liveness: %v", err)
	}
	if !initAlive || workerAlive {
		t.Fatalf("stale held event liveness=(init %t, worker %t), want (true, false)", initAlive, workerAlive)
	}
}

func TestCoordinatorCheckpointHookFollowsFixtureCheckpointOrder(t *testing.T) {
	// This is an in-process fixture. Its process identities and probe are
	// injected values, so it tests callback/protocol ordering, not Bubblewrap
	// enforcement or native sandbox evidence.
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	contents := []byte("synthetic coordinator checkpoint bytes\n")
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := identityAt(target)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	const invocation = "checkpoint-test-once"
	if err := authorizer.ApproveOnce(invocation); err != nil {
		t.Fatal(err)
	}
	native := &checkpointTestLauncher{worker: &checkpointTestWorker{}}
	observed := &observedWorkerLauncher{inner: native}
	coverage := readbrokerstudy.NewSyntheticStudyInventory()
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), observed, authorizer, coverage)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeStudySession(session) })
	if err := session.SelectRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := installSyntheticStudyInventory(session, root); err != nil {
		t.Fatal(err)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := os.NewFile(uintptr(pair[0]), "checkpoint-test-supervisor")
	coordinator := os.NewFile(uintptr(pair[1]), "checkpoint-test-coordinator")
	if supervisor == nil || coordinator == nil {
		_ = unix.Close(pair[0])
		_ = unix.Close(pair[1])
		t.Fatal("could not wrap checkpoint-test sockets")
	}
	defer supervisor.Close()
	defer coordinator.Close()
	hook := coordinatorCheckpointHook(observed, coordinator, identity, func() int {
		return authorizer.ConsumptionCount(invocation)
	})
	var checkpointMu sync.Mutex
	var checkpoints []readbrokerstudy.Checkpoint
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		checkpointMu.Lock()
		checkpoints = append(checkpoints, checkpoint)
		checkpointMu.Unlock()
		if checkpoint == readbrokerstudy.CheckpointBeforeOpen || checkpoint == readbrokerstudy.CheckpointAfterOpen || checkpoint == readbrokerstudy.CheckpointAfterClassify {
			if native.starts != 0 || observed.latest() != nil {
				return errors.New("worker started before pre-worker checkpoint completed")
			}
		}
		return hook(checkpoint)
	})
	type readDone struct {
		result readbrokerstudy.WorkerResult
		err    error
	}
	done := make(chan readDone, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, readErr := session.Read(ctx, root, target, invocation)
		done <- readDone{result: result, err: readErr}
	}()
	ready, err := readLifecycleHandshake(int(supervisor.Fd()), time.Second)
	if err != nil {
		t.Fatalf("read actual coordinator ready handshake: %v", err)
	}
	if ready.Phase != "ready" || ready.TargetIdentity != identity || ready.Probe.SeccompMode != 2 || ready.Bubblewrap.PID != 101 || ready.NamespaceInit.PID != 102 {
		t.Fatalf("unexpected injected ready handshake: %+v", ready)
	}
	if err := writeLifecycleCommand(int(supervisor.Fd()), "continue"); err != nil {
		t.Fatal(err)
	}
	held, err := readLifecycleHandshake(int(supervisor.Fd()), time.Second)
	if err != nil {
		t.Fatalf("read actual coordinator held handshake: %v", err)
	}
	if held.Phase != "held" || held.TargetIdentity != identity || held.CapabilityIdentity != identity || held.OnceConsumptions != 1 {
		t.Fatalf("unexpected injected held handshake: %+v", held)
	}
	if err := writeLifecycleCommand(int(supervisor.Fd()), "continue"); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-done:
		if completed.err != nil || completed.result.Outcome != readbrokerstudy.ReadSucceeded || string(completed.result.Bytes) != string(contents) {
			t.Fatalf("fixture read result=%+v err=%v", completed.result, completed.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not complete after both coordinator checkpoints")
	}
	want := []readbrokerstudy.Checkpoint{
		readbrokerstudy.CheckpointBeforeOpen, readbrokerstudy.CheckpointAfterOpen,
		readbrokerstudy.CheckpointAfterClassify, readbrokerstudy.CheckpointBeforeHandoff,
		readbrokerstudy.CheckpointAfterFinalRevalidation, readbrokerstudy.CheckpointAfterCapabilityHeld,
		readbrokerstudy.CheckpointAfterCapture, readbrokerstudy.CheckpointBeforePublish,
	}
	checkpointMu.Lock()
	got := append([]readbrokerstudy.Checkpoint(nil), checkpoints...)
	checkpointMu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("checkpoint sequence=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("checkpoint sequence=%v, want %v", got, want)
		}
	}
	if native.starts != 1 {
		t.Fatalf("fixture worker starts=%d, want exactly one after pre-worker checkpoints", native.starts)
	}
}

func TestCoordinatorCheckpointHookRequiresWorkerOnlyAtObservationPoints(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	control := os.NewFile(uintptr(pair[0]), "missing-worker-control")
	peer := os.NewFile(uintptr(pair[1]), "missing-worker-peer")
	if control == nil || peer == nil {
		_ = unix.Close(pair[0])
		_ = unix.Close(pair[1])
		t.Fatal("could not wrap missing-worker sockets")
	}
	defer control.Close()
	defer peer.Close()
	hook := coordinatorCheckpointHook(&observedWorkerLauncher{}, control, readbrokerstudy.FileIdentity{}, nil)
	for _, checkpoint := range []readbrokerstudy.Checkpoint{
		readbrokerstudy.CheckpointBeforeOpen,
		readbrokerstudy.CheckpointAfterOpen,
		readbrokerstudy.CheckpointAfterClassify,
		readbrokerstudy.CheckpointAfterFinalRevalidation,
	} {
		if err := hook(checkpoint); err != nil {
			t.Fatalf("non-observation checkpoint %q failed without a worker: %v", checkpoint, err)
		}
	}
	for _, checkpoint := range []readbrokerstudy.Checkpoint{
		readbrokerstudy.CheckpointBeforeHandoff,
		readbrokerstudy.CheckpointAfterCapabilityHeld,
	} {
		if err := hook(checkpoint); err == nil {
			t.Fatalf("worker observation checkpoint %q succeeded without a worker", checkpoint)
		}
	}
	if _, err := readLifecycleHandshake(int(peer.Fd()), 25*time.Millisecond); err == nil {
		t.Fatal("missing-worker failure unexpectedly emitted a readiness/held handshake")
	}
}

func TestCoordinatorOutcomeDiagnosticsAreBoundedAndSanitized(t *testing.T) {
	valid := coordinatorOutcomeHandshake(readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadCancelled}, readbrokerstudy.ReadEvidence{})
	if valid.Phase != "outcome" || valid.Stage != "before_worker_start" || valid.Outcome != readbrokerstudy.ReadCancelled {
		t.Fatalf("early cancellation handshake=%+v", valid)
	}
	recorded := &abnormalLifecycleEvidence{}
	if got := recordCoordinatorOutcome(recorded, valid); got != "phase=before_worker_start outcome=cancelled" {
		t.Fatalf("sanitized outcome=%q", got)
	}
	if recorded.CoordinatorOutcomePhase != valid.Stage || recorded.CoordinatorReadOutcome != string(valid.Outcome) {
		t.Fatalf("machine-readable coordinator outcome was not recorded: %+v", recorded)
	}
	for _, invalid := range []lifecycleHandshake{
		{Phase: "outcome", Stage: "/private/path", Outcome: readbrokerstudy.ReadCancelled},
		{Phase: "outcome", Stage: "worker_startup", Outcome: "private diagnostic"},
	} {
		got := recordCoordinatorOutcome(recorded, invalid)
		if strings.Contains(got, "/private/path") || strings.Contains(got, "private diagnostic") {
			t.Fatalf("unsafe coordinator detail escaped validation: %q", got)
		}
		if !strings.Contains(got, "invalid bounded outcome") {
			t.Fatalf("invalid coordinator detail was not replaced: %q", got)
		}
	}
}

type checkpointTestLauncher struct {
	worker *checkpointTestWorker
	starts int
}

func (l *checkpointTestLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	l.starts++
	return l.worker, nil
}
func (*checkpointTestLauncher) Close() error { return nil }

type checkpointTestWorker struct {
	mu       sync.Mutex
	file     *os.File
	identity readbrokerstudy.FileIdentity
	contents []byte
	joined   bool
}

func (*checkpointTestWorker) Ready(context.Context) error { return nil }
func (*checkpointTestWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{NoNewPrivs: true, SeccompMode: 2, SocketCreationDenied: true, PathOpenDenied: true, ReadCapabilityInitiallyAbsent: true, OnlyProtocolDescriptor: true, EnvironmentCleared: true}
}
func (w *checkpointTestWorker) TransferCapability(_ context.Context, file *os.File, identity readbrokerstudy.FileIdentity) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("fixture file identity is unavailable")
	}
	w.identity = readbrokerstudy.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Type: uint32(stat.Mode) & uint32(unix.S_IFMT)}
	if w.identity != identity {
		return readbrokerstudy.ErrStaleReceipt
	}
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.file = os.NewFile(uintptr(fd), "fixture-read-capability")
	w.mu.Unlock()
	return nil
}
func (*checkpointTestWorker) AwaitReadStarted(context.Context) error { return nil }
func (w *checkpointTestWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadSucceeded, Bytes: append([]byte(nil), w.contents...)}, nil
}
func (w *checkpointTestWorker) Terminate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.joined = true
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	return nil
}
func (w *checkpointTestWorker) Wait() error { return w.Terminate() }
func (w *checkpointTestWorker) Joined() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.joined
}
func (*checkpointTestWorker) ObserveHostProcesses() (readbrokerstudy.HostProcessIdentity, readbrokerstudy.HostProcessIdentity, error) {
	return readbrokerstudy.HostProcessIdentity{PID: 101, StartTimeTicks: 1}, readbrokerstudy.HostProcessIdentity{PID: 102, StartTimeTicks: 2}, nil
}
func (w *checkpointTestWorker) HeldCapabilityIdentity() (readbrokerstudy.FileIdentity, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.identity, w.identity.Inode != 0
}
func (*checkpointTestWorker) AwaitCapabilityHeld(context.Context) error { return nil }
func (w *checkpointTestWorker) ReleaseHeldRead(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return readbrokerstudy.ErrProtocol
	}
	data, err := io.ReadAll(w.file)
	_ = w.file.Close()
	w.file = nil
	w.contents = data
	return err
}

func (s *ownerLifecycleTestSession) Read(context.Context, string, string, string) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, errors.New("unexpected read")
}
func (s *ownerLifecycleTestSession) Register(registration readbrokerstudy.OwnerRegistration) error {
	return s.RegisterRotation(registration.Owner, []readbrokerstudy.OwnerRegistration{registration})
}
func (*ownerLifecycleTestSession) DeclareCoverageOwner(readbrokerstudy.OwnerRequirement) error {
	return nil
}
func (*ownerLifecycleTestSession) RetireCoverageOwner(string) error { return nil }
func (*ownerLifecycleTestSession) CoverageSnapshot() readbrokerstudy.CoverageSnapshot {
	return readbrokerstudy.CoverageSnapshot{Complete: true}
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
func (s *ownerLifecycleTestSession) Evidence() []readbrokerstudy.ReadEvidence       { return s.evidence }
func (*ownerLifecycleTestSession) ActiveOperationCount() int                        { return 0 }
func (s *ownerLifecycleTestSession) GenerationSnapshot() readbrokerstudy.GenerationSet {
	return readbrokerstudy.GenerationSet{Policy: s.policy}
}
func (s *ownerLifecycleTestSession) Close(context.Context) error { return s.closeErr }

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
	err    error
}

func (l *injectedProviderLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	l.starts++
	if l.err != nil {
		return nil, l.err
	}
	return l.worker, nil
}
func (*injectedProviderLauncher) Close() error { return nil }

type injectedProviderWorker struct {
	result      []byte
	override    bool
	started     bool
	joined      bool
	readyErr    error
	transferErr error
	startErr    error
	waitErr     error
}

func (w *injectedProviderWorker) Ready(context.Context) error { return w.readyErr }
func (*injectedProviderWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{
		NoNewPrivs: true, SeccompMode: 2, SocketCreationDenied: true, PathOpenDenied: true,
		ReadCapabilityInitiallyAbsent: true, OnlyProtocolDescriptor: true, EnvironmentCleared: true,
	}
}
func (w *injectedProviderWorker) TransferCapability(_ context.Context, file *os.File, _ readbrokerstudy.FileIdentity) error {
	if w.transferErr != nil {
		return w.transferErr
	}
	content, err := io.ReadAll(file)
	if err == nil && !w.override {
		w.result = content
	}
	return err
}
func (w *injectedProviderWorker) AwaitReadStarted(context.Context) error {
	if w.startErr != nil {
		return w.startErr
	}
	w.started = true
	return nil
}
func (w *injectedProviderWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadSucceeded, Bytes: append([]byte(nil), w.result...)}, nil
}
func (w *injectedProviderWorker) Terminate() error { w.joined = true; return nil }
func (w *injectedProviderWorker) Wait() error      { w.joined = w.waitErr == nil; return w.waitErr }
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
	coverage := readbrokerstudy.NewSyntheticStudyInventory()
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), launcher, mutating, coverage)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SelectRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := installSyntheticStudyInventory(session, root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeStudySession(session) })
	return &providerFixture{root: root, provider: provider, providerContents: providerContents, alias: alias, ordinary: ordinary, registry: registry, authorizer: authorizer, mutating: mutating, session: session}, launcher
}

func TestProviderAuthorizationControlsUseActualFixtureBytes(t *testing.T) {
	t.Run("ordinary positive control", func(t *testing.T) {
		fixture, _ := newInjectedProviderFixture(t, nil)
		row := executeCase(fixture.session, fixture.authorizer, fixture.root, "provider_only_ordinary_positive_control", fixture.ordinary, "ordinary-positive-control", "ordinary provider control fixture reads under its own once authorization", readbrokerstudy.ReadSucceeded)
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

func TestRealStoreOrdinaryReadControlsUsePositiveExpectation(t *testing.T) {
	for _, test := range []struct {
		name        string
		invocation  string
		description string
	}{
		{name: "real_auth_unrelated_ordinary_read_control", invocation: "auth-control", description: "unrelated ordinary object remains readable with explicit once approval"},
		{name: "real_settings_ordinary_read_control", invocation: "settings-control", description: "an explicit once approval permits an unrelated ordinary fixture read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, _ := newInjectedProviderFixture(t, nil)
			row := executeCase(fixture.session, fixture.authorizer, fixture.root, test.name, fixture.ordinary, test.invocation, test.description, readbrokerstudy.ReadSucceeded)
			if row.Status != "measured_pass" || row.Classification != "supports_b" || row.WorkerOutcome != readbrokerstudy.ReadSucceeded ||
				!row.WorkerStarted || !row.WorkerReady || !row.WorkerJoinConfirmed || !row.CapabilitySent || !row.ReadStarted ||
				!row.Published || !row.BytesMatchApproved || row.OnceGrantConsumptions != 1 {
				t.Fatalf("ordinary positive control did not satisfy the successful-read contract: %+v", row)
			}
		})
	}
}

func TestPositiveReadExpectationRequiresCompleteEvidence(t *testing.T) {
	t.Run("wrong bytes", func(t *testing.T) {
		fixture, _ := newInjectedProviderFixture(t, []byte("wrong synthetic bytes\n"))
		row := executeCase(fixture.session, fixture.authorizer, fixture.root, "real_auth_unrelated_ordinary_read_control", fixture.ordinary, "wrong-bytes", "ordinary read must match approved fixture bytes", readbrokerstudy.ReadSucceeded)
		if row.Status == "measured_pass" || row.BytesMatchApproved {
			t.Fatalf("wrong bytes satisfied a positive-read expectation: %+v", row)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(*injectedProviderWorker, *injectedProviderLauncher)
	}{
		{name: "worker startup missing", setup: func(_ *injectedProviderWorker, launcher *injectedProviderLauncher) {
			launcher.err = errors.New("injected launch refusal")
		}},
		{name: "startup acknowledgement missing", setup: func(worker *injectedProviderWorker, _ *injectedProviderLauncher) {
			worker.readyErr = errors.New("injected startup refusal")
		}},
		{name: "capability handoff missing", setup: func(worker *injectedProviderWorker, _ *injectedProviderLauncher) {
			worker.transferErr = errors.New("injected handoff refusal")
		}},
		{name: "read start missing", setup: func(worker *injectedProviderWorker, _ *injectedProviderLauncher) {
			worker.startErr = errors.New("injected start refusal")
		}},
		{name: "worker join missing", setup: func(worker *injectedProviderWorker, _ *injectedProviderLauncher) {
			worker.waitErr = errors.New("injected wait failure")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, launcher := newInjectedProviderFixture(t, nil)
			test.setup(launcher.worker, launcher)
			row := executeCase(fixture.session, fixture.authorizer, fixture.root, "real_settings_ordinary_read_control", fixture.ordinary, "missing-evidence", "ordinary read requires complete worker evidence", readbrokerstudy.ReadSucceeded)
			if row.Status == "measured_pass" {
				t.Fatalf("incomplete positive evidence satisfied expectation: %+v", row)
			}
		})
	}

	t.Run("publication suppressed", func(t *testing.T) {
		fixture, _ := newInjectedProviderFixture(t, nil)
		row := executeCaseWithHook(fixture.session, fixture.authorizer, fixture.root, "real_auth_unrelated_ordinary_read_control", fixture.ordinary, "publication-suppressed", "ordinary read requires publication", readbrokerstudy.ReadSucceeded, func(checkpoint readbrokerstudy.Checkpoint) error {
			if checkpoint == readbrokerstudy.CheckpointAfterCapture {
				return errors.New("injected publication suppression")
			}
			return nil
		})
		if row.Status == "measured_pass" || row.Published || fixture.authorizer.ConsumptionCount("publication-suppressed") != 1 {
			t.Fatalf("suppressed publication satisfied positive expectation: row=%+v once=%d", row, fixture.authorizer.ConsumptionCount("publication-suppressed"))
		}
	})

	t.Run("unexpected success under denial expectation", func(t *testing.T) {
		fixture, _ := newInjectedProviderFixture(t, nil)
		row := executeCase(fixture.session, fixture.authorizer, fixture.root, "unexpected-positive-as-denial", fixture.ordinary, "unexpected-positive", "this fixture is deliberately expected to refuse", readbrokerstudy.ReadRefused)
		if row.Status == "measured_pass" || row.Observed != string(readbrokerstudy.ReadSucceeded) {
			t.Fatalf("successful access was not reported as unexpected under denial expectation: %+v", row)
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
func (*directoryCoverageSequenceSession) DeclareCoverageOwner(readbrokerstudy.OwnerRequirement) error {
	return nil
}
func (*directoryCoverageSequenceSession) RetireCoverageOwner(string) error { return nil }
func (*directoryCoverageSequenceSession) CoverageSnapshot() readbrokerstudy.CoverageSnapshot {
	return readbrokerstudy.CoverageSnapshot{Complete: true}
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
		"ordinary_positive", "outside_working_directory_positive", "separate_mount_target_positive",
		"unsupported_symlink_lookup", "unsupported_fifo_lookup", "oversized_target_not_published",
		"protected_direct", "provider_source_explicit_once",
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
		"real_auth_startup_registration_and_successive_rotations", "real_auth_retired_alias_denied",
		"real_auth_renamed_retired_object_denied", "real_auth_intermediate_alias_denied",
		"real_auth_current_route_denied", "real_auth_current_alias_denied",
		"real_auth_unrelated_ordinary_read_control", "real_auth_held_worker_cleanup_pending_blocks_write_then_retry",
		"real_auth_injected_registration_failure_preserves_destination", "real_auth_failure_retry_after_registration",
		"real_settings_startup_absent_route_then_first_save", "real_settings_startup_existing_route_and_rotations",
		"real_settings_retired_alias_denied", "real_settings_intermediate_alias_denied",
		"real_settings_current_alias_denied", "real_settings_effective_route_denied",
		"real_settings_ordinary_read_control", "real_settings_cleanup_pending_refuses_then_retries",
		"real_auth_publication_invalidation_after_capture", "real_settings_publication_invalidation_after_capture",
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
		if result.Status != "unavailable" || result.Classification != "unavailable" || result.WorkerStarted || result.CapabilitySent || result.CapabilityHeld || result.CapabilityIdentity != nil || result.ReadStarted || result.Published || result.ActorStarted || result.ActorTransition || result.AuthorizationAttempted || result.AuthorizationReceiptAccepted || result.OwnerLifecycle != nil || result.RealStore != nil || result.Lifecycle != nil {
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

func TestRealStoreReportEvidenceIsExplicitAndSanitized(t *testing.T) {
	identity := readbrokerstudy.FileIdentity{Device: 1, Inode: 2, Type: 3}
	row := caseResult{
		Name: "real_auth_cleanup_pending_refuses_then_retries",
		RealStore: &realStoreEvidence{
			StoreType: "auth.Store", Operation: "AddAPIKey", EffectiveOverride: true,
			ReplacementIdentity: &identity, ReplacementEmptyAtRegistration: true,
			StoreWriteNotStartedBeforeJoin: true, SensitiveContentPresentInReport: false,
		},
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(fields["real_store"], &evidence); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"store_type", "operation", "effective_override", "replacement_empty_at_registration", "store_write_not_started_before_join", "sensitive_content_present_in_report"} {
		if _, ok := evidence[key]; !ok {
			t.Errorf("real-store report omitted %q: %s", key, encoded)
		}
	}
	if strings.Contains(string(encoded), "synthetic-secret") || strings.Contains(string(encoded), "auth.json") || strings.Contains(string(encoded), "settings.json") {
		t.Fatalf("report contains a sensitive value or host path: %s", encoded)
	}
}

func TestFixtureContentBeforeJoinFieldClarifiesLegacyMeaning(t *testing.T) {
	evidence := abnormalLifecycleEvidence{
		SensitiveBytesPresentBeforeJoin:   true,
		FixtureContentUnchangedBeforeJoin: true,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["sensitive_bytes_present_before_join"]) != "true" || string(fields["fixture_content_unchanged_before_join"]) != "true" {
		t.Fatalf("legacy/new pending-state evidence fields not preserved: %s", encoded)
	}
}
