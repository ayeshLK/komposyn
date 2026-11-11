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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fakeWorker is deliberately a coordinator unit-test double. It reads a
// duplicated capability in this process and MUST NOT be counted as sandbox
// enforcement evidence. Native cases use the real Bubblewrap WorkerLauncher.
type fakeWorker struct {
	mu               sync.Mutex
	capability       *os.File
	identity         FileIdentity
	result           WorkerResult
	started          bool
	transferred      bool
	readStarted      bool
	terminated       bool
	joined           bool
	transferErr      error
	readStartedErr   error
	resultErr        error
	resultOverride   *WorkerResult
	terminateErr     error
	terminateEntered chan struct{}
	terminateRelease chan struct{}
	terminateOnce    sync.Once
	waitErr          error
	joinUnconfirmed  bool
	waitEntered      chan struct{}
	waitRelease      chan struct{}
	waitEnteredOnce  sync.Once
	waitCalls        int
	ackReturnEntered chan struct{}
	ackReturnRelease chan struct{}
	ackReturnOnce    sync.Once
	resultEntered    chan struct{}
	resultRelease    chan struct{}
	resultOnce       sync.Once
	joinedEntered    chan struct{}
	joinedRelease    chan struct{}
	joinedOnce       sync.Once
}

type startupDiagnosticWorker struct {
	*fakeWorker
	readyErr error
	detail   string
}

func (w *startupDiagnosticWorker) Ready(context.Context) error { return w.readyErr }

func (w *startupDiagnosticWorker) FailureDetail() string { return w.detail }

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
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.transferErr != nil {
		return w.transferErr
	}
	if w.transferred {
		return errors.New("capability transferred twice")
	}
	actual, err := identityFromFile(file)
	if err != nil {
		return err
	}
	if actual != identity {
		return ErrStaleReceipt
	}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return err
	}
	w.capability = os.NewFile(uintptr(fd), "fake-worker-read-capability")
	if w.capability == nil {
		_ = unix.Close(fd)
		return ErrProtocol
	}
	w.identity = identity
	w.transferred = true
	return nil
}

func (w *fakeWorker) AwaitReadStarted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	if w.readStartedErr != nil {
		defer w.mu.Unlock()
		return w.readStartedErr
	}
	if w.capability == nil || !w.transferred {
		defer w.mu.Unlock()
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
	entered, release := w.ackReturnEntered, w.ackReturnRelease
	w.mu.Unlock()
	if entered != nil {
		w.ackReturnOnce.Do(func() { close(entered) })
	}
	if release != nil {
		// The controlled fake reports a successful acknowledgement even if
		// cancellation races the return, matching the coordinator edge under
		// review. The next coordinator state check must observe invalidation.
		<-release
	}
	return nil
}

func (w *fakeWorker) ReadResult(ctx context.Context) (WorkerResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkerResult{}, err
	}
	if w.resultEntered != nil {
		w.resultOnce.Do(func() { close(w.resultEntered) })
	}
	if w.resultRelease != nil {
		<-w.resultRelease
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
	w.terminated = true
	entered, release, err := w.terminateEntered, w.terminateRelease, w.terminateErr
	w.mu.Unlock()
	if entered != nil {
		w.terminateOnce.Do(func() { close(entered) })
	}
	if release != nil {
		<-release
	}
	return err
}

func (w *fakeWorker) Wait() error {
	w.mu.Lock()
	w.waitCalls++
	entered, release := w.waitEntered, w.waitRelease
	w.mu.Unlock()
	if entered != nil {
		w.waitEnteredOnce.Do(func() { close(entered) })
	}
	if release != nil {
		<-release
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.joinUnconfirmed {
		w.joined = true
	}
	if w.joined && w.capability != nil {
		_ = w.capability.Close()
		w.capability = nil
	}
	return w.waitErr
}

func (w *fakeWorker) Joined() bool {
	if w.joinedEntered != nil {
		w.joinedOnce.Do(func() { close(w.joinedEntered) })
	}
	if w.joinedRelease != nil {
		<-w.joinedRelease
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.joined
}

type blockingFixtureAuthorizer struct {
	delegate *OnceGrantFixture
	blockID  string
	entered  chan struct{}
	release  chan struct{}
}

func (a *blockingFixtureAuthorizer) AuthorizeAndConsume(ctx context.Context, request AuthorizationRequest) (AuthorizationReceipt, error) {
	if request.InvocationID == a.blockID {
		close(a.entered)
		select {
		case <-a.release:
		case <-ctx.Done():
			return AuthorizationReceipt{}, ctx.Err()
		}
	}
	return a.delegate.AuthorizeAndConsume(ctx, request)
}

func cloneWorkerResult(result WorkerResult) WorkerResult {
	result.Bytes = append([]byte(nil), result.Bytes...)
	return result
}

type fakeLauncher struct {
	mu       sync.Mutex
	worker   SandboxWorker
	workers  []*fakeWorker
	starts   int
	startErr error
	closed   bool
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

func (l *fakeLauncher) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	return nil
}

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
	session := newCoveredSession(t, registry, root, launcher, authorizer)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = session.Close(ctx)
		_ = registry.Close()
	})
	return &sessionFixture{session: session, registry: registry, authorizer: authorizer, worker: worker, launcher: launcher, root: root}
}

func newCoveredSession(t *testing.T, registry *Registry, root string, launcher WorkerLauncher, authorizer FixtureAuthorizer) FixtureSession {
	t.Helper()
	coverage := NewSyntheticStudyInventory()
	session, err := NewHostCoordinator(registry, NewBroker(registry), launcher, authorizer, coverage)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SelectRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := installTestOwnerCoverage(session, root); err != nil {
		t.Fatal(err)
	}
	return session
}

func testOwnerRequirements() []OwnerRequirement {
	return SyntheticStudyRequirements()
}

func installTestOwnerCoverage(session FixtureSession, root string) error {
	for _, requirement := range testOwnerRequirements() {
		if err := session.DeclareCoverageOwner(requirement); err != nil {
			return err
		}
		path := filepath.Join(root, ".coverage-"+requirement.Owner)
		if err := session.Register(OwnerRegistration{Owner: requirement.Owner, Class: requirement.Class, Path: path}); err != nil {
			return err
		}
	}
	return nil
}

func TestUnknownAndPartialOwnerInventoryRefuseBeforeAuthorizationAndWorker(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("bounded inventory fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		partial    bool
		wantReason string
	}{
		{name: "unknown inventory", wantReason: ErrInventoryIncomplete.Error()},
		{name: "auth and settings are not the whole profile", partial: true, wantReason: "fixture-token-db-inventory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry, err := NewRegistry()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = registry.Close() })
			authorizer := NewOnceGrantFixture()
			if err := authorizer.ApproveOnce("coverage-read"); err != nil {
				t.Fatal(err)
			}
			launcher := &fakeLauncher{worker: &fakeWorker{}}
			var session FixtureSession
			if test.partial {
				inventory := NewSyntheticStudyInventory()
				session, err = NewFixtureSession(registry, NewBroker(registry), launcher, authorizer, inventory)
			} else {
				session, err = NewFixtureSession(registry, NewBroker(registry), launcher, authorizer)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = session.Close(ctx)
			})
			if err := session.SelectRoot(root); err != nil {
				t.Fatal(err)
			}
			if test.partial {
				unsupported := testOwnerRequirements()[0]
				unsupported.Lifecycle = LifecycleReadOnlySnapshot
				if err := session.DeclareCoverageOwner(unsupported); err == nil {
					t.Fatal("unsupported lifecycle declaration was accepted")
				}
				for _, requirement := range testOwnerRequirements()[:2] {
					if err := session.DeclareCoverageOwner(requirement); err != nil {
						t.Fatal(err)
					}
					reserved := filepath.Join(root, ".absent-"+requirement.Owner)
					if err := session.Register(OwnerRegistration{Owner: requirement.Owner, Class: requirement.Class, Path: reserved}); err != nil {
						t.Fatal(err)
					}
				}
				if err := session.Register(OwnerRegistration{Owner: "uncovered-provider-source", Class: ClassProvider, Path: target}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := session.Read(context.Background(), root, target, "coverage-read")
			if err != nil || result.Outcome != ReadNotExecuted || !strings.Contains(result.Detail, test.wantReason) {
				t.Fatalf("incomplete inventory read=(%q,%q,%v), want fail-closed reason %q", result.Outcome, result.Detail, err, test.wantReason)
			}
			if launcher.starts != 0 || authorizer.ConsumptionCount("coverage-read") != 0 || len(session.PublishedResults()) != 0 {
				t.Fatalf("incomplete inventory crossed admission: starts=%d once=%d published=%d", launcher.starts, authorizer.ConsumptionCount("coverage-read"), len(session.PublishedResults()))
			}
		})
	}
}

func TestCompleteBoundedSyntheticInventoryAdmitsAndPartialRegistrationRecovers(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	want := []byte("bounded inventory fixture")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	authorizer := NewOnceGrantFixture()
	if err := authorizer.ApproveOnce("coverage-read"); err != nil {
		t.Fatal(err)
	}
	launcher := &fakeLauncher{worker: &fakeWorker{}}
	inventory := NewSyntheticStudyInventory()
	session, err := NewFixtureSession(registry, NewBroker(registry), launcher, authorizer, inventory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = session.Close(ctx)
	}()
	if err := session.SelectRoot(root); err != nil {
		t.Fatal(err)
	}
	for _, requirement := range testOwnerRequirements()[:2] {
		if err := session.DeclareCoverageOwner(requirement); err != nil {
			t.Fatal(err)
		}
		if err := session.Register(OwnerRegistration{Owner: requirement.Owner, Class: requirement.Class, Path: filepath.Join(root, ".absent-"+requirement.Owner)}); err != nil {
			t.Fatal(err)
		}
	}
	partial := session.CoverageSnapshot()
	if partial.Complete || partial.AdmissionError() == nil {
		t.Fatalf("auth/settings-only inventory was incorrectly complete: %+v", partial)
	}
	if partial.Requirements[0].RegisteredRoutes != 1 || partial.Requirements[0].PinnedIdentities != 0 {
		t.Fatalf("absent reserved route was not represented distinctly: %+v", partial.Requirements[0])
	}
	if result, err := session.Read(context.Background(), root, target, "coverage-read"); err != nil || result.Outcome != ReadNotExecuted {
		t.Fatalf("partial read=(%q,%v), must refuse before consuming its approval", result.Outcome, err)
	}
	for _, requirement := range testOwnerRequirements()[2:] {
		if err := session.DeclareCoverageOwner(requirement); err != nil {
			t.Fatal(err)
		}
		if err := session.Register(OwnerRegistration{Owner: requirement.Owner, Class: requirement.Class, Path: filepath.Join(root, ".absent-"+requirement.Owner)}); err != nil {
			t.Fatal(err)
		}
	}
	complete := session.CoverageSnapshot()
	if err := complete.AdmissionError(); err != nil {
		t.Fatalf("completed bounded fixture inventory refused: %v (%+v)", err, complete)
	}
	result, err := session.Read(context.Background(), root, target, "coverage-read")
	if err != nil || result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, want) {
		t.Fatalf("read after coverage recovery=(%q,%q,%v)", result.Outcome, result.Bytes, err)
	}
	if launcher.starts != 1 || authorizer.ConsumptionCount("coverage-read") != 1 {
		t.Fatalf("recovered read starts=%d once=%d, want one of each", launcher.starts, authorizer.ConsumptionCount("coverage-read"))
	}
}

func TestInventoryChangeAfterClassificationDoesNotConsumeOnceGrant(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("inventory change fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("inventory-stale-after-classify"); err != nil {
		t.Fatal(err)
	}
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterClassify {
			return fixture.session.Register(OwnerRegistration{
				Owner: "fixture-auth-inventory", Class: ClassCredential,
				Path: filepath.Join(root, ".auth-after-classify"),
			})
		}
		return nil
	})
	result, err := fixture.session.Read(context.Background(), root, target, "inventory-stale-after-classify")
	if err != nil || result.Outcome != ReadNotExecuted {
		t.Fatalf("read after inventory revision changed=(%q,%v), want not executed", result.Outcome, err)
	}
	if got := fixture.authorizer.ConsumptionCount("inventory-stale-after-classify"); got != 0 {
		t.Fatalf("stale prepared inventory consumed once grant %d times, want zero", got)
	}
	if fixture.launcher.starts != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatalf("stale inventory crossed authorization gate: workers=%d published=%d", fixture.launcher.starts, len(fixture.session.PublishedResults()))
	}
}

func TestOverlappingOwnerUpdatesCannotClearNewerFailedUpdate(t *testing.T) {
	for _, firstKind := range []string{"register", "rotation"} {
		for _, secondKind := range []string{"register", "rotation"} {
			t.Run(firstKind+"_then_"+secondKind, func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, "ordinary.fixture")
				if err := os.WriteFile(target, []byte("overlapping owner update"), 0o600); err != nil {
					t.Fatal(err)
				}
				fixture := newSessionFixture(t, root)
				fixture.worker.resultEntered = make(chan struct{})
				fixture.worker.resultRelease = make(chan struct{})
				fixture.worker.terminateEntered = make(chan struct{})
				fixture.worker.terminateRelease = make(chan struct{})
				var releaseResultOnce, releaseTerminateOnce sync.Once
				releaseResult := func() { releaseResultOnce.Do(func() { close(fixture.worker.resultRelease) }) }
				releaseTerminate := func() { releaseTerminateOnce.Do(func() { close(fixture.worker.terminateRelease) }) }
				defer releaseResult()
				defer releaseTerminate()

				const invocationID = "overlapping-owner-update"
				if err := fixture.authorizer.ApproveOnce(invocationID); err != nil {
					t.Fatal(err)
				}
				type readResult struct {
					result WorkerResult
					err    error
				}
				readDone := make(chan readResult, 1)
				go func() {
					result, err := fixture.session.Read(context.Background(), root, target, invocationID)
					readDone <- readResult{result: result, err: err}
				}()
				select {
				case <-fixture.worker.resultEntered:
				case <-time.After(2 * time.Second):
					t.Fatal("read did not reach the held-result checkpoint")
				}

				firstDone := make(chan error, 1)
				go func() { firstDone <- runInventoryUpdate(t, fixture.session, root, firstKind, ".first-update") }()
				select {
				case <-fixture.worker.terminateEntered:
				case <-time.After(2 * time.Second):
					t.Fatal("first update did not reach the controlled termination checkpoint")
				}

				// Let the revoked read finish while the first update remains paused
				// after its registry mutation. The second update then fails before
				// the first update attempts to clear inventory pending state.
				releaseResult()
				select {
				case <-readDone:
				case <-time.After(2 * time.Second):
					t.Fatal("revoked read did not finish while update cleanup was held")
				}

				secondErr := runInvalidInventoryUpdate(fixture.session, secondKind)
				if secondErr == nil {
					t.Fatal("invalid overlapping owner update unexpectedly succeeded")
				}
				releaseTerminate()
				select {
				case firstErr := <-firstDone:
					if !errors.Is(firstErr, ErrInventoryIncomplete) {
						t.Fatalf("older update result=%v, want stale completion refusal", firstErr)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("first owner update did not finish after termination release")
				}

				coverage := fixture.session.CoverageSnapshot()
				if coverage.Complete || !coverage.Requirements[0].PendingUpdate {
					t.Fatalf("older successful update cleared newer failed update: %+v", coverage)
				}
				const retryID = "read-after-overlapping-owner-failure"
				if err := fixture.authorizer.ApproveOnce(retryID); err != nil {
					t.Fatal(err)
				}
				blocked, err := fixture.session.Read(context.Background(), root, target, retryID)
				if err != nil || blocked.Outcome != ReadNotExecuted || fixture.authorizer.ConsumptionCount(retryID) != 0 || fixture.launcher.starts != 1 {
					t.Fatalf("read after overlapping failed update=(%q,%v) once=%d workers=%d", blocked.Outcome, err, fixture.authorizer.ConsumptionCount(retryID), fixture.launcher.starts)
				}
			})
		}
	}
}

func runInventoryUpdate(t *testing.T, session FixtureSession, root, kind, name string) error {
	t.Helper()
	registration := OwnerRegistration{
		Owner: "fixture-auth-inventory", Class: ClassCredential, Path: filepath.Join(root, name),
	}
	if kind == "rotation" {
		return session.RegisterRotation(registration.Owner, []OwnerRegistration{registration})
	}
	return session.Register(registration)
}

func runInvalidInventoryUpdate(session FixtureSession, kind string) error {
	registration := OwnerRegistration{
		Owner: "fixture-auth-inventory", Class: ClassCredential, Path: "relative-invalid-owner-path",
	}
	if kind == "rotation" {
		return session.RegisterRotation(registration.Owner, []OwnerRegistration{registration})
	}
	return session.Register(registration)
}

func TestInventoryRevisionChangeSuppressesCapturedReadAndInvalidatesGeneration(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("inventory revision fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("inventory-change"); err != nil {
		t.Fatal(err)
	}
	before := fixture.session.GenerationSnapshot()
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterCapture {
			return fixture.session.Register(OwnerRegistration{Owner: "fixture-auth-inventory", Class: ClassCredential, Path: filepath.Join(root, ".auth-second-route")})
		}
		return nil
	})
	result, err := fixture.session.Read(context.Background(), root, target, "inventory-change")
	fixture.session.SetCheckpointHook(nil)
	after := fixture.session.GenerationSnapshot()
	if err != nil || result.Outcome == ReadSucceeded || len(fixture.session.PublishedResults()) != 0 {
		t.Fatalf("stale captured result escaped after owner inventory changed: result=%+v err=%v published=%d", result, err, len(fixture.session.PublishedResults()))
	}
	if after.Inventory <= before.Inventory || after.Policy <= before.Policy || fixture.authorizer.ConsumptionCount("inventory-change") != 1 {
		t.Fatalf("inventory mutation revisions before=%+v after=%+v once=%d", before, after, fixture.authorizer.ConsumptionCount("inventory-change"))
	}
}

func TestRetiredRequiredOwnerInvalidatesCoverageWithoutDroppingPins(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("retired owner fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("retired-owner-read"); err != nil {
		t.Fatal(err)
	}
	ownerPath := filepath.Join(root, ".coverage-fixture-token-db-inventory")
	if err := os.WriteFile(ownerPath, []byte("synthetic protected owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.session.Register(OwnerRegistration{Owner: "fixture-token-db-inventory", Class: ClassTokenDatabase, Path: ownerPath}); err != nil {
		t.Fatal(err)
	}
	beforePins := fixture.registry.PinnedObjectCount()
	if err := fixture.session.RetireCoverageOwner("fixture-token-db-inventory"); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.session.CoverageSnapshot()
	if snapshot.Complete || snapshot.AdmissionError() == nil {
		t.Fatalf("retired required owner kept coverage complete: %+v", snapshot)
	}
	var retired OwnerCoverage
	for _, owner := range snapshot.Requirements {
		if owner.Owner == "fixture-token-db-inventory" {
			retired = owner
		}
	}
	if !retired.Retired || retired.Complete || retired.PinnedIdentities != 1 {
		t.Fatalf("retired coverage/pin state=%+v", retired)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "retired-owner-read")
	if err != nil || result.Outcome != ReadNotExecuted || fixture.launcher.starts != 0 || fixture.authorizer.ConsumptionCount("retired-owner-read") != 0 {
		t.Fatalf("read after owner retirement=(%q,%v) starts=%d once=%d", result.Outcome, err, fixture.launcher.starts, fixture.authorizer.ConsumptionCount("retired-owner-read"))
	}
	if fixture.registry.PinnedObjectCount() < beforePins {
		t.Fatalf("owner retirement released registry pins early: before=%d after=%d", beforePins, fixture.registry.PinnedObjectCount())
	}
}

func TestFailedOwnerRegistrationLeavesCoveragePendingUntilRetry(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("owner retry fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.authorizer.ApproveOnce("owner-retry-read"); err != nil {
		t.Fatal(err)
	}
	failed := fixture.session.Register(OwnerRegistration{Owner: "fixture-auth-inventory", Class: ClassCredential, Path: "relative-owner-path"})
	if failed == nil {
		t.Fatal("invalid owner route registration unexpectedly succeeded")
	}
	coverage := fixture.session.CoverageSnapshot()
	if coverage.Complete || !coverage.Requirements[0].PendingUpdate {
		t.Fatalf("failed update left stale complete coverage: %+v", coverage)
	}
	blocked, err := fixture.session.Read(context.Background(), root, target, "owner-retry-read")
	if err != nil || blocked.Outcome != ReadNotExecuted || fixture.authorizer.ConsumptionCount("owner-retry-read") != 0 || fixture.launcher.starts != 0 {
		t.Fatalf("read during failed owner update=(%q,%v) once=%d starts=%d", blocked.Outcome, err, fixture.authorizer.ConsumptionCount("owner-retry-read"), fixture.launcher.starts)
	}
	if err := fixture.session.Register(OwnerRegistration{Owner: "fixture-auth-inventory", Class: ClassCredential, Path: filepath.Join(root, ".auth-owner-retry")}); err != nil {
		t.Fatalf("owner registration retry: %v", err)
	}
	coverage = fixture.session.CoverageSnapshot()
	if err := coverage.AdmissionError(); err != nil {
		t.Fatalf("successful retry did not restore bounded fixture admission: %v (%+v)", err, coverage)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "owner-retry-read")
	if err != nil || result.Outcome != ReadSucceeded || fixture.authorizer.ConsumptionCount("owner-retry-read") != 1 || fixture.launcher.starts != 1 {
		t.Fatalf("read after owner retry=(%q,%v) once=%d starts=%d", result.Outcome, err, fixture.authorizer.ConsumptionCount("owner-retry-read"), fixture.launcher.starts)
	}
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

func TestLateAliasDoesNotReclassifyExistingOrdinaryRouteButProtectedRouteRefuses(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("synthetic ordinary bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if err := registry.Register(OwnerRegistration{Owner: "credential-fixture", Class: ClassCredential, Path: filepath.Join(root, "reserved-auth.fixture")}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "reserved-auth.fixture")
	if _, err := os.Lstat(alias); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reserved alias existed before actor transition: %v", err)
	}
	actor := startNamespaceActor(t, root, "link", filepath.Base(target), filepath.Base(alias))
	actor.Run(t)
	ordinaryInfo, ordinaryErr := os.Stat(target)
	aliasInfo, aliasErr := os.Stat(alias)
	if ordinaryErr != nil || aliasErr != nil || !os.SameFile(ordinaryInfo, aliasInfo) {
		t.Fatalf("independent actor alias effect ordinaryErr=%v aliasErr=%v same=%t", ordinaryErr, aliasErr, ordinaryErr == nil && aliasErr == nil && os.SameFile(ordinaryInfo, aliasInfo))
	}
	authorizer := NewOnceGrantFixture()
	if err := authorizer.ApproveOnce("mutable-location"); err != nil {
		t.Fatal(err)
	}
	worker := &fakeWorker{}
	launcher := &fakeLauncher{worker: worker}
	session := newCoveredSession(t, registry, root, launcher, authorizer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := session.Read(ctx, root, target, "mutable-location")
	if err != nil || result.Outcome != ReadSucceeded || string(result.Bytes) != "synthetic ordinary bytes" {
		t.Fatalf("ordinary-route read=(%q,%q,%v), want approved read of captured object", result.Outcome, result.Bytes, err)
	}
	if authorizer.ConsumptionCount("mutable-location") != 1 || launcher.starts != 1 || !worker.transferred || !worker.readStarted || len(session.PublishedResults()) != 1 {
		t.Fatalf("once=%d starts=%d transferred=%t read=%t published=%d", authorizer.ConsumptionCount("mutable-location"), launcher.starts, worker.transferred, worker.readStarted, len(session.PublishedResults()))
	}
	if err := authorizer.ApproveOnce("direct-protected-route"); err != nil {
		t.Fatal(err)
	}
	direct, err := session.Read(ctx, root, alias, "direct-protected-route")
	if err != nil || direct.Outcome != ReadRefused || direct.Detail != ErrProtected.Error() {
		t.Fatalf("direct protected-route read=(%q,%q,%v), want protected refusal", direct.Outcome, direct.Detail, err)
	}
	if authorizer.ConsumptionCount("direct-protected-route") != 0 || launcher.starts != 1 {
		t.Fatalf("protected route consumed once=%d or launched worker=%d", authorizer.ConsumptionCount("direct-protected-route"), launcher.starts)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := session.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestReservedProtectedRouteRefusesBeforeOpeningAbsentOrNonRegularTarget(t *testing.T) {
	root := t.TempDir()
	reserved := filepath.Join(root, "reserved-settings.fixture")
	fixture := newSessionFixture(t, root)
	if err := fixture.registry.Register(OwnerRegistration{Owner: "settings-fixture", Class: ClassSettings, Path: reserved}); err != nil {
		t.Fatal(err)
	}

	if err := fixture.authorizer.ApproveOnce("reserved-absent"); err != nil {
		t.Fatal(err)
	}
	absent, err := fixture.session.Read(context.Background(), root, reserved, "reserved-absent")
	if err != nil || absent.Outcome != ReadRefused || absent.Detail != ErrProtected.Error() {
		t.Fatalf("absent reserved route result=(%q,%q,%v), want route-policy refusal", absent.Outcome, absent.Detail, err)
	}
	if err := os.Mkdir(reserved, 0o700); err != nil {
		t.Fatalf("create non-regular replacement at reserved route: %v", err)
	}
	if err := fixture.authorizer.ApproveOnce("reserved-nonregular"); err != nil {
		t.Fatal(err)
	}
	nonregular, err := fixture.session.Read(context.Background(), root, reserved, "reserved-nonregular")
	if err != nil || nonregular.Outcome != ReadRefused || nonregular.Detail != ErrProtected.Error() {
		t.Fatalf("non-regular reserved route result=(%q,%q,%v), want route-policy refusal", nonregular.Outcome, nonregular.Detail, err)
	}
	if fixture.launcher.starts != 0 || fixture.authorizer.ConsumptionCount("reserved-absent") != 0 || fixture.authorizer.ConsumptionCount("reserved-nonregular") != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatalf("protected route caused activity: worker starts=%d once counts=(%d,%d) published=%d", fixture.launcher.starts, fixture.authorizer.ConsumptionCount("reserved-absent"), fixture.authorizer.ConsumptionCount("reserved-nonregular"), len(fixture.session.PublishedResults()))
	}
}

func TestUnconfirmedWorkerJoinBlocksFurtherReadsAndResourceRelease(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "cleanup.fixture")
	if err := os.WriteFile(target, []byte("synthetic cleanup fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.joinUnconfirmed = true
	if err := fixture.authorizer.ApproveOnce("cleanup-first"); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.session.Read(context.Background(), root, target, "cleanup-first")
	if err != nil || first.Outcome != ReadUnknown {
		t.Fatalf("first read=(%q,%v), want unknown without confirmed join", first.Outcome, err)
	}
	if got := fixture.session.Evidence(); len(got) != 1 || got[0].Outcome != ReadUnknown {
		t.Fatalf("unresolved cleanup evidence=%+v, want one unknown record", got)
	}
	if err := fixture.authorizer.ApproveOnce("cleanup-second"); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.session.Read(context.Background(), root, target, "cleanup-second")
	if err != nil || second.Outcome != ReadNotExecuted || second.Detail != ErrCleanupPending.Error() {
		t.Fatalf("second read=(%q,%q,%v), want cleanup-pending not-executed", second.Outcome, second.Detail, err)
	}
	if fixture.launcher.starts != 1 || fixture.authorizer.ConsumptionCount("cleanup-second") != 0 {
		t.Fatal("unresolved worker did not block dispatch and once-grant consumption")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = fixture.session.Close(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("close with unresolved join=%v, want bounded cleanup refusal", err)
	}
	fixture.registry.mu.RLock()
	registryClosed := fixture.registry.closed
	fixture.registry.mu.RUnlock()
	if registryClosed {
		t.Fatal("registry resources were released while worker join remained unconfirmed")
	}

	// A later positive join observation lets Close finish cleanup. Until then,
	// the record remains active and continues to block resource release.
	fixture.worker.mu.Lock()
	fixture.worker.joinUnconfirmed = false
	fixture.worker.joined = true
	if fixture.worker.capability != nil {
		_ = fixture.worker.capability.Close()
		fixture.worker.capability = nil
	}
	fixture.worker.mu.Unlock()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fixture.session.Close(ctx); err != nil {
		t.Fatalf("close after join confirmation: %v", err)
	}
	fixture.registry.mu.RLock()
	registryClosed = fixture.registry.closed
	fixture.registry.mu.RUnlock()
	if !registryClosed {
		t.Fatal("registry resources were not released after join confirmation")
	}
}

func TestNonzeroWorkerExitIsReapedAndDoesNotMasqueradeAsPendingCleanup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nonzero-exit.fixture")
	if err := os.WriteFile(target, []byte("synthetic worker exit fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.waitErr = errors.New("synthetic nonzero worker exit")
	if err := fixture.authorizer.ApproveOnce("nonzero-exit"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "nonzero-exit")
	if err != nil || result.Outcome != ReadUnknown {
		t.Fatalf("read=(%q,%v), want unknown result after nonzero process exit", result.Outcome, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fixture.session.Close(ctx); err != nil {
		t.Fatalf("close after confirmed process reaping: %v", err)
	}
}

func TestCancellationBeforeHandoffIsCancelledWhenKilledWorkerWasJoined(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "cancel-before-release.fixture")
	if err := os.WriteFile(target, []byte("synthetic cancellation fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.waitErr = errors.New("signal: killed")
	followup := &fakeWorker{}
	fixture.launcher.workers = []*fakeWorker{fixture.worker, followup}
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointBeforeHandoff {
			return context.Canceled
		}
		return nil
	})
	if err := fixture.authorizer.ApproveOnce("cancelled-killed-joined"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "cancelled-killed-joined")
	if err != nil || result.Outcome != ReadCancelled || !strings.Contains(result.Detail, "no target-data descriptor was released") {
		t.Fatalf("pre-release cancellation=(%q,%q,%v), want cancelled with no target capability release", result.Outcome, result.Detail, err)
	}
	if !fixture.worker.Joined() || fixture.worker.transferred || fixture.authorizer.ConsumptionCount("cancelled-killed-joined") != 1 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatalf("joined=%t transferred=%t once=%d published=%d", fixture.worker.Joined(), fixture.worker.transferred, fixture.authorizer.ConsumptionCount("cancelled-killed-joined"), len(fixture.session.PublishedResults()))
	}
	if !strings.Contains(result.Detail, "exit error after the direct process was reaped") {
		t.Fatalf("joined process exit diagnostic missing: %q", result.Detail)
	}
	fixture.session.SetCheckpointHook(nil)
	if err := fixture.authorizer.ApproveOnce("read-after-confirmed-cancel"); err != nil {
		t.Fatal(err)
	}
	followupResult, err := fixture.session.Read(context.Background(), root, target, "read-after-confirmed-cancel")
	if err != nil || followupResult.Outcome != ReadSucceeded || fixture.launcher.starts != 2 {
		t.Fatalf("read after confirmed pre-release cleanup=(%q,%v), starts=%d", followupResult.Outcome, err, fixture.launcher.starts)
	}
}

func TestCancellationBeforeHandoffRemainsUnknownWhenWorkerIsNotJoined(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "cancel-unjoined.fixture")
	if err := os.WriteFile(target, []byte("synthetic cancellation fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.joinUnconfirmed = true
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointBeforeHandoff {
			return context.Canceled
		}
		return nil
	})
	if err := fixture.authorizer.ApproveOnce("cancelled-unjoined"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "cancelled-unjoined")
	if err != nil || result.Outcome != ReadUnknown || !strings.Contains(result.Detail, "direct-process join is unconfirmed") {
		t.Fatalf("pre-release cancellation with unjoined worker=(%q,%q,%v), want unknown cleanup", result.Outcome, result.Detail, err)
	}
	if fixture.worker.transferred || fixture.authorizer.ConsumptionCount("cancelled-unjoined") != 1 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("unjoined pre-release cancellation transferred data, changed consumption, or published")
	}
	if err := fixture.authorizer.ApproveOnce("blocked-after-unjoined-cancel"); err != nil {
		t.Fatal(err)
	}
	blocked, err := fixture.session.Read(context.Background(), root, target, "blocked-after-unjoined-cancel")
	if err != nil || blocked.Outcome != ReadNotExecuted || blocked.Detail != ErrCleanupPending.Error() || fixture.launcher.starts != 1 {
		t.Fatalf("read after unjoined cancellation=(%q,%q,%v), starts=%d", blocked.Outcome, blocked.Detail, err, fixture.launcher.starts)
	}
	fixture.worker.mu.Lock()
	fixture.worker.joinUnconfirmed = false
	fixture.worker.joined = true
	fixture.worker.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fixture.session.Close(ctx); err != nil {
		t.Fatalf("close after explicit join confirmation: %v", err)
	}
}

func TestFinalRevalidationCheckpointPrecedesProtectedAliasHandoff(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary-before-actor.fixture")
	protectedAlias := filepath.Join(root, "registered-protected-alias.fixture")
	if err := os.WriteFile(target, []byte("synthetic ordinary bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "race-protected-file", Class: ClassCredential, Path: protectedAlias}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("final-scan-protected-alias"); err != nil {
		t.Fatal(err)
	}
	var checkpoints []Checkpoint
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		checkpoints = append(checkpoints, checkpoint)
		if checkpoint == CheckpointAfterFinalRevalidation {
			return os.Link(target, protectedAlias)
		}
		return nil
	})
	result, err := fixture.session.Read(context.Background(), root, target, "final-scan-protected-alias")
	if err != nil || result.Outcome != ReadSucceeded || string(result.Bytes) != "synthetic ordinary bytes\n" {
		t.Fatalf("read=(%q,%q,%v), want captured ordinary object to publish despite alias-only change", result.Outcome, result.Bytes, err)
	}
	wantOrder := []Checkpoint{CheckpointBeforeOpen, CheckpointAfterOpen, CheckpointAfterClassify, CheckpointBeforeHandoff, CheckpointAfterFinalRevalidation, CheckpointAfterCapture, CheckpointBeforePublish}
	if !reflect.DeepEqual(checkpoints, wantOrder) {
		t.Fatalf("checkpoints=%v, want ordering %v", checkpoints, wantOrder)
	}
	evidence := fixture.session.Evidence()
	if len(evidence) != 1 || !evidence[0].WorkerStarted || !evidence[0].WorkerReady || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || evidence[0].WorkerJoined != true || !evidence[0].Published {
		t.Fatalf("protected-alias race evidence=%+v, want capability/read/publication and confirmed join", evidence)
	}
	if fixture.authorizer.ConsumptionCount("final-scan-protected-alias") != 1 {
		t.Fatal("once grant was not consumed exactly once before capability handoff")
	}
	ordinaryInfo, ordinaryErr := os.Stat(target)
	aliasInfo, aliasErr := os.Stat(protectedAlias)
	if ordinaryErr != nil || aliasErr != nil || !os.SameFile(ordinaryInfo, aliasInfo) {
		t.Fatalf("actor alias fixture state ordinary=%v alias=%v err=(%v,%v)", ordinaryInfo, aliasInfo, ordinaryErr, aliasErr)
	}
}

func TestRevocationAfterReadStartRemainsUnknownWhenResultWasNotEstablished(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "revoked-after-read-start.fixture")
	if err := os.WriteFile(target, []byte("synthetic post-release fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.waitErr = errors.New("signal: killed")
	fixture.worker.ackReturnEntered = make(chan struct{})
	fixture.worker.ackReturnRelease = make(chan struct{})
	if err := fixture.authorizer.ApproveOnce("revoked-after-read-start"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	done := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, target, "revoked-after-read-start")
		done <- readResult{result: result, err: err}
	}()
	select {
	case <-fixture.worker.ackReturnEntered:
	case <-time.After(time.Second):
		t.Fatal("read did not reach the controlled post-acknowledgement point")
	}
	if err := fixture.session.Revoke("revoked-after-read-start"); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("revocation cleanup result=%v, want pending while worker has not joined", err)
	}
	close(fixture.worker.ackReturnRelease)
	select {
	case got := <-done:
		if got.err != nil || got.result.Outcome != ReadUnknown {
			t.Fatalf("read after release/revocation=(%q,%q,%v), want unknown", got.result.Outcome, got.result.Detail, got.err)
		}
		if !strings.Contains(got.result.Detail, "read result was not established") || !strings.Contains(got.result.Detail, "exit error after the direct process was reaped") {
			t.Fatalf("post-release uncertainty diagnostics missing: %q", got.result.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked read did not finish after the direct worker joined")
	}
	evidence := fixture.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != ReadUnknown || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || evidence[0].Published {
		t.Fatalf("post-release revocation evidence=%+v, want unknown after transfer/start and before publication", evidence)
	}
	if fixture.authorizer.ConsumptionCount("revoked-after-read-start") != 1 || len(fixture.session.PublishedResults()) != 0 || !fixture.worker.Joined() {
		t.Fatal("revoked post-release read replayed, published data, or failed to confirm direct-process join")
	}
}

func TestConcurrentReadAndCloseSynchronizeJoinCompletion(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "overlapping-close.fixture")
	if err := os.WriteFile(target, []byte("synthetic overlapping close fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.resultEntered = make(chan struct{})
	fixture.worker.resultRelease = make(chan struct{})
	fixture.worker.joinedEntered = make(chan struct{})
	fixture.worker.joinedRelease = make(chan struct{})
	if err := fixture.authorizer.ApproveOnce("overlapping-close"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	readDone := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, target, "overlapping-close")
		readDone <- readResult{result: result, err: err}
	}()
	select {
	case <-fixture.worker.resultEntered:
	case <-time.After(time.Second):
		t.Fatal("read did not reach the controlled result boundary")
	}

	// Model a process whose direct-child reap becomes observable while Read is
	// still consuming its protocol result. Close's independent join observation
	// and Read's waiter snapshot then overlap on the operation's join state.
	fixture.worker.mu.Lock()
	fixture.worker.joined = true
	fixture.worker.mu.Unlock()
	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closeDone <- fixture.session.Close(ctx)
	}()
	select {
	case <-fixture.worker.joinedEntered:
	case <-time.After(time.Second):
		t.Fatal("Close did not reach the independent join observation")
	}

	// Release both controlled edges together so the waiter completion and
	// Close's positive join update contend in the same Read/Close window.
	close(fixture.worker.resultRelease)
	close(fixture.worker.joinedRelease)
	select {
	case got := <-readDone:
		if got.err != nil || (got.result.Outcome != ReadCancelled && got.result.Outcome != ReadUnknown) {
			t.Fatalf("overlapping read=(%q,%v), want suppressed cancellation/uncertain result", got.result.Outcome, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("overlapping read did not finish")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close during joined read: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after join confirmation")
	}
}

func TestDirectoryCoverageIsSpecificPreAuthorizationRefusal(t *testing.T) {
	result := refusalFor(fmt.Errorf("wrapped policy check: %w", ErrDirectoryCoverage))
	if result.Outcome != ReadRefused || result.Detail != ErrDirectoryCoverage.Error() || len(result.Bytes) != 0 {
		t.Fatalf("directory coverage mapping=%+v, want specific refused result", result)
	}
	unrelated := refusalFor(errors.New("unrelated preparation failure"))
	if unrelated.Outcome != ReadNotExecuted || unrelated.Detail == ErrDirectoryCoverage.Error() {
		t.Fatalf("unrelated error mapping=%+v, must remain distinct from coverage refusal", unrelated)
	}
}

func TestBrokerLookupRefusalsKeepSanitizedReasons(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		detail string
	}{
		{name: "symlink", err: errSymlinkLookupRefused, detail: LookupRefusalSymlink},
		{name: "mount boundary", err: errMountBoundaryRefused, detail: LookupRefusalMountBoundary},
		{name: "non-regular file", err: errReadNotRegular, detail: LookupRefusalNonRegular},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := refusalFor(test.err)
			if result.Outcome != ReadNotExecuted || result.Detail != test.detail || len(result.Bytes) != 0 {
				t.Fatalf("lookup refusal=%+v, want sanitized reason %q", result, test.detail)
			}
		})
	}
}

func TestUnresolvedCleanupInvalidatesReadAlreadyWaitingForAuthorization(t *testing.T) {
	root := t.TempDir()
	targetA := filepath.Join(root, "read-a.fixture")
	targetB := filepath.Join(root, "read-b.fixture")
	for _, path := range []string{targetA, targetB} {
		if err := os.WriteFile(path, []byte("overlap fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	baseAuthorizer := NewOnceGrantFixture()
	blocked := &blockingFixtureAuthorizer{delegate: baseAuthorizer, blockID: "read-b", entered: make(chan struct{}), release: make(chan struct{})}
	workerA := &fakeWorker{joinUnconfirmed: true}
	launcher := &fakeLauncher{workers: []*fakeWorker{workerA, &fakeWorker{}}}
	session := newCoveredSession(t, registry, root, launcher, blocked)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = session.Close(ctx)
		_ = registry.Close()
	})
	if err := baseAuthorizer.ApproveOnce("read-a"); err != nil {
		t.Fatal(err)
	}
	if err := baseAuthorizer.ApproveOnce("read-b"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	readBDone := make(chan readResult, 1)
	go func() {
		result, readErr := session.Read(context.Background(), root, targetB, "read-b")
		readBDone <- readResult{result: result, err: readErr}
	}()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("read B did not reach the controlled authorization wait")
	}
	readA, err := session.Read(context.Background(), root, targetA, "read-a")
	if err != nil || readA.Outcome != ReadUnknown {
		t.Fatalf("read A=(%q,%v), want unknown with unconfirmed cleanup", readA.Outcome, err)
	}
	close(blocked.release)
	select {
	case got := <-readBDone:
		if got.err != nil || got.result.Outcome != ReadNotExecuted {
			t.Fatalf("read B=(%q,%v), want stale not-executed result", got.result.Outcome, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("read B did not resume after authorization release")
	}
	if launcher.starts != 1 || baseAuthorizer.ConsumptionCount("read-b") != 1 || len(session.PublishedResults()) != 0 {
		t.Fatal("read B escaped the unresolved-cleanup admission barrier")
	}
	workerA.mu.Lock()
	workerA.joined = true
	if workerA.capability != nil {
		_ = workerA.capability.Close()
		workerA.capability = nil
	}
	workerA.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close after test confirms worker join: %v", err)
	}
}

func TestCloseDeadlineDoesNotBlockOnWorkerJoinOrReleaseResources(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "blocking-wait.fixture")
	if err := os.WriteFile(target, []byte("synthetic blocking wait"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.worker.waitEntered = make(chan struct{})
	fixture.worker.waitRelease = make(chan struct{})
	defer func() {
		select {
		case <-fixture.worker.waitRelease:
		default:
			close(fixture.worker.waitRelease)
		}
	}()
	if err := fixture.authorizer.ApproveOnce("blocking-wait"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	readDone := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, target, "blocking-wait")
		readDone <- readResult{result: result, err: err}
	}()
	select {
	case <-fixture.worker.waitEntered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter its controlled blocking Wait")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	closeStarted := time.Now()
	err := fixture.session.Close(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close with blocked Wait=%v, want context deadline", err)
	}
	if elapsed := time.Since(closeStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("Close ignored its 20ms deadline for %s", elapsed)
	}
	fixture.registry.mu.RLock()
	registryClosed := fixture.registry.closed
	fixture.registry.mu.RUnlock()
	fixture.launcher.mu.Lock()
	launcherClosed := fixture.launcher.closed
	fixture.launcher.mu.Unlock()
	fixture.worker.mu.Lock()
	waitCalls := fixture.worker.waitCalls
	fixture.worker.mu.Unlock()
	if registryClosed || launcherClosed || waitCalls != 1 {
		t.Fatalf("incomplete close released resources or duplicated waiter: registry=%t launcher=%t waitCalls=%d", registryClosed, launcherClosed, waitCalls)
	}
	close(fixture.worker.waitRelease)
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("read did not finish after controlled worker join")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fixture.session.Close(ctx); err != nil {
		t.Fatalf("Close after worker join: %v", err)
	}
}

func TestRootChangeBlocksAdmissionUntilInvalidatedWorkerJoins(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	targetA := filepath.Join(rootA, "old-root.fixture")
	targetB := filepath.Join(rootB, "new-root.fixture")
	for _, path := range []string{targetA, targetB} {
		if err := os.WriteFile(path, []byte("root transition fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newSessionFixture(t, rootA)
	workerA := fixture.worker
	workerA.waitEntered = make(chan struct{})
	workerA.waitRelease = make(chan struct{})
	workerB := &fakeWorker{}
	fixture.launcher.mu.Lock()
	fixture.launcher.workers = []*fakeWorker{workerA, workerB}
	fixture.launcher.mu.Unlock()
	if err := fixture.authorizer.ApproveOnce("old-root-read"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	oldReadDone := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), rootA, targetA, "old-root-read")
		oldReadDone <- readResult{result: result, err: err}
	}()
	select {
	case <-workerA.waitEntered:
	case <-time.After(time.Second):
		t.Fatal("old-root worker did not reach blocked join")
	}
	if err := fixture.session.SelectRoot(rootB); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("root change error=%v, want cleanup pending", err)
	}
	if err := fixture.authorizer.ApproveOnce("new-root-blocked"); err != nil {
		t.Fatal(err)
	}
	blocked, err := fixture.session.Read(context.Background(), rootB, targetB, "new-root-blocked")
	if err != nil || blocked.Outcome != ReadNotExecuted {
		t.Fatalf("new-root read while old worker is unjoined=(%q,%v), want not-executed", blocked.Outcome, err)
	}
	if fixture.launcher.starts != 1 || fixture.authorizer.ConsumptionCount("new-root-blocked") != 0 {
		t.Fatal("new-root read started or consumed approval while invalidated worker was unjoined")
	}
	close(workerA.waitRelease)
	select {
	case <-oldReadDone:
	case <-time.After(time.Second):
		t.Fatal("old-root read did not finish after worker join")
	}
	if err := fixture.authorizer.ApproveOnce("new-root-after-join"); err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.session.Read(context.Background(), rootB, targetB, "new-root-after-join")
	if err != nil || accepted.Outcome != ReadSucceeded {
		t.Fatalf("new-root read after confirmed join=(%q,%v), want success", accepted.Outcome, err)
	}
	if fixture.launcher.starts != 2 || len(fixture.session.PublishedResults()) != 1 {
		t.Fatal("admission did not resume exactly once after cleanup confirmation")
	}
}

func TestCleanupBarrierSurvivesInvalidatedWorkerStateTransition(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	paths := []string{
		filepath.Join(rootA, "read-a.fixture"),
		filepath.Join(rootA, "read-b.fixture"),
		filepath.Join(rootB, "read-c.fixture"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("state transition fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newSessionFixture(t, rootA)
	workerA := fixture.worker
	workerA.resultOverride = &WorkerResult{Outcome: ReadFailed}
	workerA.ackReturnEntered = make(chan struct{})
	workerA.ackReturnRelease = make(chan struct{})
	workerA.waitEntered = make(chan struct{})
	workerA.waitRelease = make(chan struct{})
	workerB := &fakeWorker{}
	workerC := &fakeWorker{}
	fixture.launcher.mu.Lock()
	fixture.launcher.workers = []*fakeWorker{workerA, workerB, workerC}
	fixture.launcher.mu.Unlock()
	hookEntered := make(chan struct{})
	hookRelease := make(chan struct{})
	fixture.session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterCapture {
			close(hookEntered)
			<-hookRelease
		}
		return nil
	})
	if err := fixture.authorizer.ApproveOnce("state-read-a"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("state-read-b"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	readADone := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), rootA, paths[0], "state-read-a")
		readADone <- readResult{result: result, err: err}
	}()
	select {
	case <-workerA.ackReturnEntered:
	case <-time.After(time.Second):
		t.Fatal("read A did not reach the successful acknowledgement return gate")
	}
	readBDone := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), rootA, paths[1], "state-read-b")
		readBDone <- readResult{result: result, err: err}
	}()
	select {
	case <-hookEntered:
	case <-time.After(time.Second):
		t.Fatal("read B did not reach the post-capture gate")
	}
	if err := fixture.session.SelectRoot(rootB); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("root change error=%v, want pending cleanup", err)
	}
	close(workerA.ackReturnRelease)
	select {
	case <-workerA.waitEntered:
	case <-time.After(time.Second):
		t.Fatal("read A did not reach its blocked Wait after acknowledgement")
	}
	close(hookRelease)
	select {
	case got := <-readBDone:
		if got.err != nil || got.result.Outcome != ReadCancelled {
			t.Fatalf("read B=(%q,%v), want cancellation after root change", got.result.Outcome, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("read B did not finish after release")
	}
	if err := fixture.authorizer.ApproveOnce("state-read-blocked"); err != nil {
		t.Fatal(err)
	}
	blocked, err := fixture.session.Read(context.Background(), rootB, paths[2], "state-read-blocked")
	if err != nil || blocked.Outcome != ReadNotExecuted || fixture.launcher.starts != 2 {
		t.Fatalf("new-root read while A is still unjoined=(%q,%v), starts=%d", blocked.Outcome, err, fixture.launcher.starts)
	}
	close(workerA.waitRelease)
	select {
	case <-readADone:
	case <-time.After(time.Second):
		t.Fatal("read A did not finish after join")
	}
	fixture.session.SetCheckpointHook(nil)
	if err := fixture.authorizer.ApproveOnce("state-read-after-join"); err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.session.Read(context.Background(), rootB, paths[2], "state-read-after-join")
	if err != nil || accepted.Outcome != ReadSucceeded || fixture.launcher.starts != 3 {
		t.Fatalf("new-root read after A joins=(%q,%v), starts=%d", accepted.Outcome, err, fixture.launcher.starts)
	}
}

func TestProviderPinCannotOverrideMovedChildOfProtectedSettingsDirectory(t *testing.T) {
	root := t.TempDir()
	protectedDir := filepath.Join(root, "settings")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(protectedDir, "provider-source.fixture")
	if err := os.WriteFile(target, []byte("synthetic protected provider source"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	if err := fixture.session.Register(OwnerRegistration{Owner: "settings-fixture", Class: ClassSettings, Path: protectedDir, Directory: true}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.session.Register(OwnerRegistration{Owner: "provider-fixture", Class: ClassProvider, Path: target}); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "provider-source-moved.fixture")
	if err := os.Rename(target, moved); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authorizer.ApproveOnce("provider-under-settings"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, moved, "provider-under-settings")
	if err != nil || result.Outcome != ReadRefused || result.Detail != ErrDirectoryCoverage.Error() {
		t.Fatalf("moved child read=(%q,%q,%v), want directory-coverage refusal", result.Outcome, result.Detail, err)
	}
	if fixture.launcher.starts != 0 || fixture.authorizer.ConsumptionCount("provider-under-settings") != 0 || len(fixture.session.PublishedResults()) != 0 {
		t.Fatal("provider invocation approval overrode protected-directory policy")
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

func TestMutableProviderExactRegistrationAllowsOrdinaryAndExplicitProviderReads(t *testing.T) {
	root := t.TempDir()
	ordinary := filepath.Join(root, "ordinary.fixture")
	provider := filepath.Join(root, "provider-source.fixture")
	if err := os.WriteFile(ordinary, []byte("ordinary synthetic bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider, []byte("provider synthetic bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if err := registry.Register(OwnerRegistration{Owner: "provider-fixture", Class: ClassProvider, Path: provider}); err != nil {
		t.Fatal(err)
	}
	authorizer := NewOnceGrantFixture()
	ordinaryWorker, providerWorker := &fakeWorker{}, &fakeWorker{}
	launcher := &fakeLauncher{workers: []*fakeWorker{ordinaryWorker, providerWorker}}
	session := newCoveredSession(t, registry, root, launcher, authorizer)
	if err := authorizer.ApproveOnce("ordinary-with-provider-location"); err != nil {
		t.Fatal(err)
	}
	finalCheckpointReached := false
	session.SetCheckpointHook(func(checkpoint Checkpoint) error {
		if checkpoint == CheckpointAfterFinalRevalidation {
			finalCheckpointReached = true
		}
		return nil
	})
	ordinaryResult, err := session.Read(context.Background(), root, ordinary, "ordinary-with-provider-location")
	session.SetCheckpointHook(nil)
	if err != nil || ordinaryResult.Outcome != ReadSucceeded || string(ordinaryResult.Bytes) != "ordinary synthetic bytes" {
		t.Fatalf("ordinary read=(%q,%q,%v), want read despite mutable exact provider registration", ordinaryResult.Outcome, ordinaryResult.Bytes, err)
	}
	if !finalCheckpointReached || launcher.starts != 1 || authorizer.ConsumptionCount("ordinary-with-provider-location") != 1 {
		t.Fatalf("ordinary request reached final checkpoint=%t workers=%d once=%d", finalCheckpointReached, launcher.starts, authorizer.ConsumptionCount("ordinary-with-provider-location"))
	}

	if err := authorizer.ApproveOnce("approved-provider-source"); err != nil {
		t.Fatal(err)
	}
	providerResult, err := session.Read(context.Background(), root, provider, "approved-provider-source")
	if err != nil || providerResult.Outcome != ReadSucceeded || string(providerResult.Bytes) != "provider synthetic bytes" {
		t.Fatalf("explicit provider read=(%q,%q,%v)", providerResult.Outcome, providerResult.Bytes, err)
	}
	if authorizer.ConsumptionCount("approved-provider-source") != 1 || launcher.starts != 2 {
		t.Fatalf("provider once=%d worker starts=%d, want one provider grant and second worker", authorizer.ConsumptionCount("approved-provider-source"), launcher.starts)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := session.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerRotationWaitsForWorkerJoinBeforeWritingRegisteredObject(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "auth-old.fixture")
	newPath := filepath.Join(root, "auth-next.fixture")
	ordinaryPath := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(oldPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ordinaryPath, []byte("ordinary fixture read"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	coverageBefore := fixture.session.GenerationSnapshot()
	if err := fixture.session.Register(OwnerRegistration{Owner: "auth-owner", Class: ClassCredential, Path: oldPath}); err != nil {
		t.Fatal(err)
	}
	newObject, err := os.OpenFile(newPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer newObject.Close()
	newIdentity, err := identityFromFile(newObject)
	if err != nil {
		t.Fatal(err)
	}
	fixture.worker.waitEntered = make(chan struct{})
	fixture.worker.waitRelease = make(chan struct{})
	if err := fixture.authorizer.ApproveOnce("read-during-owner-rotation"); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		result WorkerResult
		err    error
	}
	readDone := make(chan readResult, 1)
	go func() {
		result, err := fixture.session.Read(context.Background(), root, ordinaryPath, "read-during-owner-rotation")
		readDone <- readResult{result: result, err: err}
	}()
	select {
	case <-fixture.worker.waitEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach the deterministic join barrier")
	}
	coverageRegistration := OwnerRegistration{Owner: "fixture-auth-inventory", Class: ClassCredential, Path: filepath.Join(root, ".auth-coverage-while-held")}
	if err := fixture.session.Register(coverageRegistration); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("inventory registration=%v, want cleanup pending while capability worker is held", err)
	}
	if generation := fixture.session.GenerationSnapshot(); generation.Inventory <= coverageBefore.Inventory {
		t.Fatalf("owner coverage registration did not advance inventory revision: before=%+v after=%+v", coverageBefore, generation)
	}

	rotation := []OwnerRegistration{{Owner: "auth-owner", Class: ClassCredential, Path: newPath}}
	if err := fixture.session.RegisterRotation("auth-owner", rotation); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("rotation registration=%v, want cleanup pending while worker is unjoined", err)
	}
	if stat, err := newObject.Stat(); err != nil || stat.Size() != 0 {
		t.Fatalf("owner wrote sensitive fixture bytes before worker join: size/error=%v/%v", stat, err)
	}
	view := fixture.registry.View()
	oldPinned, newPinned := false, false
	for _, entry := range view.Entries {
		if entry.Owner == "auth-owner" && entry.Identity == newIdentity {
			newPinned = true
		}
		if entry.Owner == "auth-owner" && entry.Path == oldPath && entry.Identity == identityAt(t, oldPath) {
			oldPinned = true
		}
	}
	if !oldPinned || newPinned {
		t.Fatalf("rotation must retain the old pin and withhold the replacement pin until cleanup: old=%t new=%t entries=%+v", oldPinned, newPinned, view.Entries)
	}

	close(fixture.worker.waitRelease)
	select {
	case result := <-readDone:
		if result.err != nil {
			t.Fatalf("active read returned coordinator error: %v", result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("invalidated read did not finish after the worker joined")
	}
	if err := fixture.session.RegisterRotation("auth-owner", rotation); err != nil {
		t.Fatalf("idempotent rotation retry after confirmed join: %v", err)
	}
	if _, err := newObject.WriteAt([]byte("synthetic-sensitive-fixture"), 0); err != nil {
		t.Fatalf("write through the already-registered object after join: %v", err)
	}
	if got := identityAt(t, newPath); got != newIdentity {
		t.Fatalf("registered pathname now identifies %v; expected pinned object %v", got, newIdentity)
	}
	alias := filepath.Join(root, "auth-next-alias.fixture")
	if err := os.Link(newPath, alias); err != nil {
		t.Fatal(err)
	}
	assertClassified(t, fixture.registry, alias, newIdentity, ClassCredential)
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

func TestFixtureSessionRecordsBoundedWorkerStartupDiagnostic(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	fixture.launcher.worker = &startupDiagnosticWorker{
		fakeWorker: fixture.worker,
		readyErr:   errors.New("startup handshake failed"),
		detail:     "bubblewrap: synthetic startup diagnostic " + strings.Repeat("x", 900),
	}
	if err := fixture.authorizer.ApproveOnce("startup-diagnostic"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.session.Read(context.Background(), root, target, "startup-diagnostic")
	if err != nil || result.Outcome != ReadNotExecuted {
		t.Fatalf("startup failure result=(%s,%v), want not executed", result.Outcome, err)
	}
	evidence := fixture.session.Evidence()
	if len(evidence) != 1 || !strings.Contains(evidence[0].FailureDetail, "bubblewrap: synthetic startup diagnostic") || len(evidence[0].FailureDetail) != 768 {
		t.Fatalf("startup evidence=%+v, want bounded startup diagnostic", evidence)
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
	if err != nil || result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, want) {
		t.Fatalf("before-handoff alias read=(%q,%q,%v), want approved ordinary-route read", result.Outcome, result.Bytes, err)
	}
	if result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, want) || len(fixture.session.PublishedResults()) != 1 {
		t.Fatal("ordinary approved bytes were not published after alias-only route change")
	}
	fixture.worker.mu.Lock()
	transferred, readStarted := fixture.worker.transferred, fixture.worker.readStarted
	fixture.worker.mu.Unlock()
	if !transferred || !readStarted {
		t.Fatal("alias-only change unexpectedly blocked the authorized object read")
	}
	if got, err := os.ReadFile(protectedAlias); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("actor fixture alias state: bytes=%q err=%v", got, err)
	}
}

func TestIndependentActorHardlinkIntoProtectedLocationAfterCaptureDoesNotSuppressBytes(t *testing.T) {
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
	if err != nil || result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, want) {
		t.Fatalf("after-capture alias read=(%q,%q,%v), want original captured result published", result.Outcome, result.Bytes, err)
	}
	if result.Outcome != ReadSucceeded || !bytes.Equal(result.Bytes, want) || len(fixture.session.PublishedResults()) != 1 {
		t.Fatal("captured bytes were not published after alias-only namespace change")
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
