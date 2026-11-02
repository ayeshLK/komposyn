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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fakeWorker is deliberately a coordinator unit-test double. It reads a
// duplicated capability in this process and MUST NOT be counted as sandbox
// enforcement evidence. Native cases use the real Bubblewrap WorkerLauncher.
type fakeWorker struct {
	mu             sync.Mutex
	capability     *os.File
	identity       FileIdentity
	result         WorkerResult
	started        bool
	transferred    bool
	readStarted    bool
	terminated     bool
	joined         bool
	transferErr    error
	readStartedErr error
	resultErr      error
	resultOverride *WorkerResult
	terminateErr   error
	waitErr        error
}

func (w *fakeWorker) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	w.started = true
	w.mu.Unlock()
	return nil
}

// Probe returns the interface's expected claims only to keep coordinator unit
// tests focused; it is a fake and is never included as native evidence.
func (w *fakeWorker) Probe() WorkerProbe {
	return WorkerProbe{
		NoNewPrivs: true, SeccompMode: 2, SocketCreationDenied: true,
		PathOpenDenied: true, ReadCapabilityInitiallyAbsent: true,
		OnlyProtocolDescriptor: true, EnvironmentCleared: true,
	}
}

func (w *fakeWorker) TransferCapability(ctx context.Context, file *os.File, identity FileIdentity) error {
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.transferErr != nil {
		_ = file.Close()
		return w.transferErr
	}
	if w.transferred {
		_ = file.Close()
		return errors.New("capability transferred twice")
	}
	actual, err := identityFromFile(file)
	if err != nil {
		_ = file.Close()
		return err
	}
	if actual != identity {
		_ = file.Close()
		return ErrStaleReceipt
	}
	w.capability = file
	w.identity = identity
	w.transferred = true
	return nil
}

func (w *fakeWorker) AwaitReadStarted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.readStartedErr != nil {
		return w.readStartedErr
	}
	if w.capability == nil || !w.transferred {
		return ErrProtocol
	}
	data := make([]byte, MaxReadBytes+1)
	n, err := unix.Pread(int(w.capability.Fd()), data, 0)
	if err != nil {
		return err
	}
	if n > MaxReadBytes {
		w.result = WorkerResult{Outcome: ReadPartial, Bytes: append([]byte(nil), data[:MaxReadBytes]...), Detail: "test fake bounded read"}
	} else {
		w.result = WorkerResult{Outcome: ReadSucceeded, Bytes: append([]byte(nil), data[:n]...)}
	}
	if w.resultOverride != nil {
		w.result = cloneWorkerResult(*w.resultOverride)
	}
	w.readStarted = true
	return nil
}

func (w *fakeWorker) ReadResult(ctx context.Context) (WorkerResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkerResult{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.resultErr != nil {
		return WorkerResult{}, w.resultErr
	}
	if !w.readStarted {
		return WorkerResult{}, ErrProtocol
	}
	return cloneWorkerResult(w.result), nil
}

func (w *fakeWorker) Terminate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.terminated = true
	return w.terminateErr
}

func (w *fakeWorker) Wait() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.joined = true
	if w.capability != nil {
		_ = w.capability.Close()
		w.capability = nil
	}
	return w.waitErr
}

func cloneWorkerResult(result WorkerResult) WorkerResult {
	result.Bytes = append([]byte(nil), result.Bytes...)
	return result
}

type fakeLauncher struct {
	mu       sync.Mutex
	worker   *fakeWorker
	workers  []*fakeWorker
	starts   int
	startErr error
}

func (l *fakeLauncher) Start(ctx context.Context) (SandboxWorker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.starts++
	if l.startErr != nil {
		return nil, l.startErr
	}
	if len(l.workers) > 0 {
		index := l.starts - 1
		if index >= len(l.workers) {
			return nil, ErrUnavailable
		}
		return l.workers[index], nil
	}
	return l.worker, nil
}

func (l *fakeLauncher) Close() error { return nil }

type sessionFixture struct {
	session    FixtureSession
	registry   *Registry
	authorizer *OnceGrantFixture
	worker     *fakeWorker
	launcher   *fakeLauncher
	root       string
}

func newSessionFixture(t *testing.T, root string) *sessionFixture {
	t.Helper()
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	authorizer := NewOnceGrantFixture()
	worker := &fakeWorker{}
	launcher := &fakeLauncher{worker: worker}
	session, err := NewFixtureSession(registry, NewBroker(registry), launcher, authorizer)
	if err != nil {
		_ = registry.Close()
		t.Fatal(err)
	}
	if err := session.SelectRoot(root); err != nil {
		_ = registry.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = session.Close(ctx)
		_ = registry.Close()
	})
	return &sessionFixture{session: session, registry: registry, authorizer: authorizer, worker: worker, launcher: launcher, root: root}
}

func TestFixtureSessionRequiresExplicitOnceApprovalAndPublishesExactObject(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	want := []byte("ordinary-read-synthetic")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	unapproved, err := fixture.session.Read(context.Background(), root, target, "call-no-approval")
	if err != nil || unapproved.Outcome != ReadRefused || unapproved.Detail != "explicit once approval is unavailable" {
		t.Fatalf("read without explicit approval=(%q,%q,%v), want refusal", unapproved.Outcome, unapproved.Detail, err)
	}
	if fixture.launcher.starts != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("unapproved read started a worker or published bytes")
	}

	if err := fixture.authorizer.ApproveOnce("call-1"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, want) {
		t.Fatalf("fixture result outcome=%q bytes=%q, want succeeded/exact target bytes", result.Outcome, result.Bytes)
	}
	if fixture.authorizer.ConsumptionCount("call-1") != 1 || fixture.launcher.starts != 1 {
		t.Fatalf("once consumption=%d worker starts=%d, want exactly one each", fixture.authorizer.ConsumptionCount("call-1"), fixture.launcher.starts)
	}
	if len(fixture.session.PublishedResults()) != 1 || !bytes.Equal(fixture.session.PublishedResults()[0], want) {
		t.Fatal("fixture result sink did not receive the exact approved bytes")
	}
	replay, err := fixture.session.Read(context.Background(), root, target, "call-1")
	if err != nil || replay.Outcome != ReadRefused || fixture.launcher.starts != 1 || fixture.authorizer.ConsumptionCount("call-1") != 1 {
		t.Fatalf("duplicate call replay=(%q,%v), workerStarts=%d grantConsumes=%d", replay.Outcome, err, fixture.launcher.starts, fixture.authorizer.ConsumptionCount("call-1"))
	}
	fixture.worker.mu.Lock()
	defer fixture.worker.mu.Unlock()
	if !fixture.worker.started || !fixture.worker.transferred || !fixture.worker.readStarted || !fixture.worker.joined {
		t.Fatalf("fake worker lifecycle flags: %+v", fixture.worker)
	}
}

func TestFixtureSessionProviderSourceNeedsInvocationApproval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provider-source.fixture")
	want := []byte("provider-source-synthetic")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "provider-fixture", Class: ClassProvider, Path: target}); err != nil {
		t.Fatal(err)
	}
	unapproved, err := fixture.session.Read(context.Background(), root, target, "call-source-denied")
	if err != nil || unapproved.Outcome != ReadRefused || fixture.launcher.starts != 0 {
		t.Fatalf("provider source without approval=(%q,%v), workerStarts=%d", unapproved.Outcome, err, fixture.launcher.starts)
	}
	if err := fixture.authorizer.ApproveOnce("call-source-approved"); err != nil {
		t.Fatal(err)
	}
	approved, err := fixture.session.Read(context.Background(), root, target, "call-source-approved")
	if err != nil || approved.Outcome != ReadSucceeded || !bytes.Equal(approved.Bytes, want) {
		t.Fatalf("approved provider source read=(%q,%q,%v)", approved.Outcome, approved.Bytes, err)
	}
	if fixture.authorizer.ConsumptionCount("call-source-approved") != 1 {
		t.Fatal("provider source once approval was not consumed exactly once")
	}
}

func TestFixtureSessionRefusesRegisteredCredentialWithoutWorker(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "credential.fixture")
	if err := os.WriteFile(target, []byte("credential-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: target}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("call-secret"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "call-secret")
	if err != nil || result.Outcome != ReadRefused || result.Detail != ErrProtected.Error() {
		t.Fatalf("credential read=(%q,%q,%v), want protected refusal", result.Outcome, result.Detail, err)
	}
	if fixture.authorizer.ConsumptionCount("call-secret") != 0 || fixture.launcher.starts != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("protected credential consumed approval, started worker, or published bytes")
	}
}

func TestFixtureSessionCancellationBeforeHandoffDoesNotReplay(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("cancel-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("call-cancel"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointBeforeHandoff {
			cancel()
		}
		return nil
	})
	result, err := fixture.session.Read(ctx, root, target, "call-cancel")
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error=%v", err)
	}
	if result.Outcome != ReadCancelled && result.Outcome != ReadNotExecuted {
		t.Fatalf("pre-handoff cancellation outcome=%q", result.Outcome)
	}
	fixture.worker.mu.Lock()
	transferred, joined, terminated := fixture.worker.transferred, fixture.worker.joined, fixture.worker.terminated
	fixture.worker.mu.Unlock()
	if transferred || !joined || !terminated || len(fixture.session.PublishedResults()) != 0 {
		t.Fatalf("pre-handoff cancellation transferred=%t joined=%t terminated=%t published=%d", transferred, joined, terminated, len(fixture.session.PublishedResults()))
	}
	if fixture.authorizer.ConsumptionCount("call-cancel") != 1 || fixture.launcher.starts != 1 {
		t.Fatalf("consumption=%d workerStarts=%d; cancellation must not automatically replay/reconsume", fixture.authorizer.ConsumptionCount("call-cancel"), fixture.launcher.starts)
	}
}

func TestFixtureSessionRevocationBeforeHandoffPreventsCapabilityTransfer(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("revocation-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("call-revoke-before-handoff"); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	continueRead := make(chan struct{})
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointBeforeHandoff {
			close(ready)
			<-continueRead
		}
		return nil
	})
	readDone := make(chan WorkerResult, 1)
	go func() {
		result, _ := fixture.session.Read(context.Background(), root, target, "call-revoke-before-handoff")
		readDone <- result
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("read never reached before-handoff checkpoint")
	}
	if err := fixture.session.Revoke("call-revoke-before-handoff"); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("revoke before handoff must report pending cleanup: %v", err)
	}
	close(continueRead)
	select {
	case result := <-readDone:
		if result.Outcome != ReadNotExecuted && result.Outcome != ReadCancelled {
			t.Fatalf("pre-handoff revocation outcome=%q", result.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoked read did not terminate within bound")
	}
	fixture.worker.mu.Lock()
	transferred, readStarted := fixture.worker.transferred, fixture.worker.readStarted
	joined, terminated := fixture.worker.joined, fixture.worker.terminated
	fixture.worker.mu.Unlock()
	if transferred || readStarted || !joined || !terminated || len(fixture.session.PublishedResults()) != 0 {
		t.Fatalf("pre-handoff revoke transferred=%t read=%t joined=%t terminated=%t published=%d", transferred, readStarted, joined, terminated, len(fixture.session.PublishedResults()))
	}
	if fixture.authorizer.ConsumptionCount("call-revoke-before-handoff") != 1 || fixture.launcher.starts != 1 {
		t.Fatal("revocation caused once-approval reuse or worker replay")
	}
}

func TestFixtureSessionGlobalRevocationStopsEveryPriorGenerationWorker(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.fixture")
	secondPath := filepath.Join(root, "second.fixture")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("synthetic revocation fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newSessionFixture(t, root)
	secondWorker := &fakeWorker{}
	fixture.launcher.workers = []*fakeWorker{fixture.worker, secondWorker}
	for _, id := range []string{"global-revoke-1", "global-revoke-2"} {
		if err := fixture.authorizer.ApproveOnce(id); err != nil {
			t.Fatal(err)
		}
	}
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointBeforeHandoff {
			arrived <- struct{}{}
			<-release
		}
		return nil
	})
	type readResult struct {
		outcome ReadOutcome
		err     error
	}
	done := make(chan readResult, 2)
	for _, item := range []struct{ path, id string }{{firstPath, "global-revoke-1"}, {secondPath, "global-revoke-2"}} {
		go func(path, id string) {
			result, err := fixture.session.Read(context.Background(), root, path, id)
			done <- readResult{outcome: result.Outcome, err: err}
		}(item.path, item.id)
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("both reads did not reach the pre-handoff gate")
		}
	}
	if err := fixture.session.Revoke(""); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("global revoke should report asynchronous cleanup pending: %v", err)
	}
	close(release)
	for range 2 {
		select {
		case result := <-done:
			if result.err != nil || (result.outcome != ReadNotExecuted && result.outcome != ReadCancelled) {
				t.Fatalf("revoked read result=%+v", result)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("revoked read did not finish")
		}
	}
	for index, worker := range fixture.launcher.workers {
		worker.mu.Lock()
		transferred, started, joined, terminated := worker.transferred, worker.readStarted, worker.joined, worker.terminated
		worker.mu.Unlock()
		if transferred || started || !joined || !terminated {
			t.Fatalf("worker %d transfer=%t read=%t joined=%t terminated=%t", index, transferred, started, joined, terminated)
		}
	}
}

func TestFixtureSessionRevocationAfterCaptureSuppressesStaleBytes(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	want := []byte("captured-but-revoked-synthetic")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("call-revoke"); err != nil {
		t.Fatal(err)
	}
	captured := make(chan struct{})
	continuePublish := make(chan struct{})
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterCapture {
			close(captured)
			<-continuePublish
		}
		return nil
	})
	readDone := make(chan struct {
		result WorkerResult
		err    error
	}, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, target, "call-revoke")
		readDone <- struct {
			result WorkerResult
			err    error
		}{result: result, err: err}
	}()
	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("read never reached post-capture checkpoint")
	}
	if err := fixture.session.Revoke("call-revoke"); err != nil {
		t.Fatalf("revoke after capture: %v", err)
	}
	close(continuePublish)
	select {
	case got := <-readDone:
		if got.err != nil && !errors.Is(got.err, ErrStaleReceipt) && !errors.Is(got.err, context.Canceled) {
			t.Fatalf("revoked read error=%v", got.err)
		}
		if got.result.Outcome == ReadSucceeded || len(got.result.Bytes) != 0 {
			t.Fatalf("revoked captured output was returned: outcome=%q bytes=%q", got.result.Outcome, got.result.Bytes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoked read did not finish within bound")
	}
	if len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("revoked generation entered result sink")
	}
	if fixture.authorizer.ConsumptionCount("call-revoke") != 1 || fixture.launcher.starts != 1 {
		t.Fatal("revoked operation was replayed or once authorization consumed more than once")
	}
}

func TestFixtureSessionPolicyChangeDuringReadSuppressesCapturedResult(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("policy-change-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	newProtected := filepath.Join(root, "new-credential.fixture")
	if err := os.WriteFile(newProtected, []byte("new-credential-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("call-policy"); err != nil {
		t.Fatal(err)
	}
	captured := make(chan struct{})
	continuePublish := make(chan struct{})
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterCapture {
			close(captured)
			<-continuePublish
		}
		return nil
	})
	readDone := make(chan WorkerResult, 1)
	readErr := make(chan error, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, target, "call-policy")
		readDone <- result
		readErr <- err
	}()
	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("read never reached post-capture checkpoint")
	}
	if err := fixture.session.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: newProtected}); err != nil {
		t.Fatalf("concurrent policy registration: %v", err)
	}
	close(continuePublish)
	select {
	case result := <-readDone:
		err := <-readErr
		if err != nil && !errors.Is(err, ErrPolicyChanged) && !errors.Is(err, ErrStaleReceipt) && !errors.Is(err, context.Canceled) {
			t.Fatalf("policy-changed read error=%v", err)
		}
		if result.Outcome == ReadSucceeded || len(result.Bytes) != 0 {
			t.Fatalf("policy-stale bytes returned: outcome=%q bytes=%q", result.Outcome, result.Bytes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("policy-changed read did not finish within bound")
	}
	if len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("policy-stale bytes entered result sink")
	}
	if fixture.authorizer.ConsumptionCount("call-policy") != 1 || fixture.launcher.starts != 1 {
		t.Fatal("policy change caused authorization reuse or worker replay")
	}
}

func TestFixtureSessionRootChangeDuringReadSuppressesCapturedResult(t *testing.T) {
	root := t.TempDir()
	newRoot := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("root-change-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("call-root-change"); err != nil {
		t.Fatal(err)
	}
	captured := make(chan struct{})
	continuePublish := make(chan struct{})
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterCapture {
			close(captured)
			<-continuePublish
		}
		return nil
	})
	type readCompletion struct {
		result WorkerResult
		err    error
	}
	readDone := make(chan readCompletion, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, target, "call-root-change")
		readDone <- readCompletion{result: result, err: err}
	}()
	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("read never reached post-capture checkpoint")
	}
	if err := fixture.session.SelectRoot(newRoot); err != nil {
		t.Fatalf("change selected root: %v", err)
	}
	close(continuePublish)
	select {
	case got := <-readDone:
		if got.err != nil && !errors.Is(got.err, context.Canceled) {
			t.Fatalf("root-changed read error=%v", got.err)
		}
		if got.result.Outcome == ReadSucceeded || len(got.result.Bytes) != 0 {
			t.Fatalf("old-root bytes escaped after root generation changed: %+v", got.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root-changed read did not finish within bound")
	}
	if len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("old-root bytes entered result sink")
	}
}

func TestFixtureSessionRejectsOversizedWorkerResult(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("bounded-input"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.resultOverride = &WorkerResult{Outcome: ReadSucceeded, Bytes: bytes.Repeat([]byte("x"), MaxReadBytes+1)}
	if err := fixture.authorizer.ApproveOnce("call-large"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "call-large")
	if err != nil || result.Outcome != ReadUnknown || result.Detail != ErrResultTooLarge.Error() {
		t.Fatalf("oversized worker output=(%q,%q,%v), want unknown bounded refusal", result.Outcome, result.Detail, err)
	}
	if result.Outcome == ReadSucceeded || len(result.Bytes) != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("oversized result was returned or published")
	}
}

func TestFixtureSessionPreservesPartialAndUnknownOutcomesWithoutReplay(t *testing.T) {
	for _, outcome := range []ReadOutcome{ReadPartial, ReadUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "ordinary.fixture")
			if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := newSessionFixture(t, root)
			fixture.worker.resultOverride = &WorkerResult{Outcome: outcome, Detail: "deterministic fixture outcome"}
			callID := "call-" + string(outcome)
			if err := fixture.authorizer.ApproveOnce(callID); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.session.Read(context.Background(), root, target, callID)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != outcome || fixture.launcher.starts != 1 || fixture.authorizer.ConsumptionCount(callID) != 1 {
				t.Fatalf("outcome=%q workerStarts=%d grantConsumes=%d", result.Outcome, fixture.launcher.starts, fixture.authorizer.ConsumptionCount(callID))
			}
			if outcome == ReadUnknown && len(fixture.session.PublishedResults()) != 0 {
				t.Fatal("unknown outcome published bytes")
			}
		})
	}
}

func TestIndependentActorHardlinkIntoProtectedLocationBeforeHandoffIsRefused(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "ordinary.fixture")
	want := []byte("ordinary-data-now-protected-synthetic")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	protectedAlias := filepath.Join(privateDir, "credential-alias.fixture")
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: protectedAlias}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("call-alias-before-handoff"); err != nil {
		t.Fatal(err)
	}
	actor := startNamespaceActor(t, root, "link", "ordinary.fixture", "private/credential-alias.fixture")
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterClassify {
			return actor.run()
		}
		return nil
	})
	result, err := fixture.session.Read(context.Background(), root, target, "call-alias-before-handoff")
	if err != nil || result.Outcome != ReadRefused || result.Detail != ErrProtected.Error() {
		t.Fatalf("before-handoff alias read=(%q,%q,%v), want protected refusal", result.Outcome, result.Detail, err)
	}
	if result.Outcome == ReadSucceeded || len(result.Bytes) != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("independent actor's protected alias was read or published")
	}
	fixture.worker.mu.Lock()
	transferred, readStarted := fixture.worker.transferred, fixture.worker.readStarted
	fixture.worker.mu.Unlock()
	if transferred || readStarted {
		t.Fatal("protected alias was discovered only after capability handoff")
	}
	if got, err := os.ReadFile(protectedAlias); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("actor fixture alias state: bytes=%q err=%v", got, err)
	}
}

func TestIndependentActorHardlinkIntoProtectedLocationAfterCaptureSuppressesBytes(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "ordinary.fixture")
	want := []byte("captured-before-protected-alias-synthetic")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	protectedAlias := filepath.Join(privateDir, "credential-alias.fixture")
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: protectedAlias}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("call-alias-after-capture"); err != nil {
		t.Fatal(err)
	}
	actor := startNamespaceActor(t, root, "link", "ordinary.fixture", "private/credential-alias.fixture")
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterCapture {
			return actor.run()
		}
		return nil
	})
	result, err := fixture.session.Read(context.Background(), root, target, "call-alias-after-capture")
	if err != nil || result.Outcome != ReadCancelled {
		t.Fatalf("after-capture alias read=(%q,%q,%v), want captured result suppressed as cancelled", result.Outcome, result.Detail, err)
	}
	if result.Outcome == ReadSucceeded || len(result.Bytes) != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("captured bytes escaped after independent actor created a protected alias")
	}
	fixture.worker.mu.Lock()
	transferred, readStarted, joined := fixture.worker.transferred, fixture.worker.readStarted, fixture.worker.joined
	fixture.worker.mu.Unlock()
	if !transferred || !readStarted || !joined {
		t.Fatalf("expected read-and-join before publication gate, transferred=%t read=%t joined=%t", transferred, readStarted, joined)
	}
}

func TestIndependentActorReplacesTargetAfterClassificationWorkerUsesOpenedObject(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.fixture")
	credential := filepath.Join(privateDir, "credential.fixture")
	openedBytes := []byte("approved-opened-object-synthetic")
	protectedBytes := []byte("protected-replacement-synthetic")
	if err := os.WriteFile(target, openedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, protectedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: credential}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("call-target-replaced"); err != nil {
		t.Fatal(err)
	}
	actor := startNamespaceActor(t, root, "replace", "target.fixture", "private/credential.fixture")
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterClassify {
			return actor.run()
		}
		return nil
	})
	result, err := fixture.session.Read(context.Background(), root, target, "call-target-replaced")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, openedBytes) {
		t.Fatalf("read did not remain bound to approved opened object: outcome=%q bytes=%q", result.Outcome, result.Bytes)
	}
	if bytes.Equal(result.Bytes, protectedBytes) {
		t.Fatal("worker followed replaced target name into protected object")
	}
	if len(fixture.session.PublishedResults()) != 1 || !bytes.Equal(fixture.session.PublishedResults()[0], openedBytes) {
		t.Fatal("published bytes did not match the originally prepared object")
	}
}
