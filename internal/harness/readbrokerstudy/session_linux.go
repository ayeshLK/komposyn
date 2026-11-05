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
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrSessionClosed = errors.New("read fixture session is closed")
	ErrRootNotSet    = errors.New("read fixture root was not explicitly selected")
	ErrInvocationID  = errors.New("read invocation identity is invalid or already used")
)

type fixtureSession struct {
	mu             sync.Mutex
	registry       PolicyRegistry
	inventory      *CoverageInventory
	broker         ReadBroker
	launcher       WorkerLauncher
	authorizer     FixtureAuthorizer
	root           string
	gen            GenerationSet
	closed         bool
	cleanupBlocked bool
	claimed        map[string]struct{}
	active         map[string]*readOperation
	hook           CheckpointHook
	published      [][]byte
	evidence       []ReadEvidence
}

type readOperation struct {
	id                string
	gen               GenerationSet
	state             OperationState
	worker            SandboxWorker
	cancel            context.CancelFunc
	revoked           bool
	released          bool
	workerJoined      bool
	cleanupRequired   bool
	cleanupObligation bool
	evidenceRecorded  bool
	waitOnce          sync.Once
	waitDone          chan struct{}
	waitErr           error
	completed         bool
	done              chan struct{}
	probe             WorkerProbe
	evidence          ReadEvidence
}

// workerWaitResult keeps process exit status separate from join confirmation.
// A killed process normally makes Wait return an exit error; Joined reports
// whether the direct child was reaped. Neither field alone proves arbitrary
// descendant cleanup.
type workerWaitResult struct {
	ExitErr error
	Joined  bool
}

type workerTerminationResult struct {
	TerminationErr error
	Wait           workerWaitResult
}

func (r workerTerminationResult) cleanupConfirmed() bool { return r.Wait.Joined }

func (r workerTerminationResult) diagnostic() string {
	var details []string
	if r.TerminationErr != nil {
		details = append(details, "termination request returned an error")
	}
	if r.Wait.ExitErr != nil {
		if r.Wait.Joined {
			details = append(details, "worker Wait returned an exit error after the direct process was reaped")
		} else {
			details = append(details, "worker Wait returned an error and direct-process join is unconfirmed")
		}
	} else if !r.Wait.Joined {
		details = append(details, "direct-process join is unconfirmed")
	}
	return strings.Join(details, "; ")
}

func withTerminationDiagnostic(detail string, result workerTerminationResult) string {
	if diagnostic := result.diagnostic(); diagnostic != "" {
		detail += "; " + diagnostic
	}
	return boundedText(detail, 768)
}

var _ FixtureSession = (*fixtureSession)(nil)

func NewFixtureSession(registry PolicyRegistry, broker ReadBroker, launcher WorkerLauncher, authorizer FixtureAuthorizer, inventory ...*CoverageInventory) (FixtureSession, error) {
	if registry == nil || broker == nil || launcher == nil || authorizer == nil {
		return nil, ErrUnavailable
	}
	if len(inventory) > 1 {
		return nil, ErrInventoryIncomplete
	}
	var coverage *CoverageInventory
	if len(inventory) == 1 {
		coverage = inventory[0]
	}
	if coverage != nil {
		coverage.observe(registry.View())
	}
	generation := GenerationSet{Policy: registry.View().Generation, Grant: 1}
	if coverage != nil {
		generation.Inventory = coverage.Snapshot().Revision
	}
	return &fixtureSession{
		registry: registry, inventory: coverage, broker: broker, launcher: launcher, authorizer: authorizer,
		gen:     generation,
		claimed: make(map[string]struct{}), active: make(map[string]*readOperation),
	}, nil
}

func (s *fixtureSession) DeclareCoverageOwner(requirement OwnerRequirement) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if s.inventory == nil {
		s.mu.Unlock()
		return ErrInventoryIncomplete
	}
	if err := s.inventory.declareOwner(requirement); err != nil {
		s.mu.Unlock()
		return err
	}
	s.syncInventoryLocked()
	workers := s.invalidateLocked(func(op *readOperation) bool { return op.gen.Inventory != s.gen.Inventory })
	s.mu.Unlock()
	return s.terminateAndCheck(workers)
}

// RetireCoverageOwner permanently invalidates this owner attestation for the
// fixture session. Pinned registry identities remain retained; callers must
// establish a fresh session/inventory before read admission can resume.
func (s *fixtureSession) RetireCoverageOwner(owner string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if s.inventory == nil {
		s.mu.Unlock()
		return ErrInventoryIncomplete
	}
	if err := s.inventory.retireOwner(owner); err != nil {
		s.mu.Unlock()
		return err
	}
	s.gen.Inventory = s.inventory.Snapshot().Revision
	workers := s.invalidateLocked(func(op *readOperation) bool { return op.gen.Inventory != s.gen.Inventory })
	s.mu.Unlock()
	return s.terminateAndCheck(workers)
}

func (s *fixtureSession) CoverageSnapshot() CoverageSnapshot {
	if s.inventory == nil {
		return CoverageSnapshot{}.withUnknownRequirement()
	}
	return s.inventory.Snapshot()
}

func (s CoverageSnapshot) withUnknownRequirement() CoverageSnapshot {
	s.Complete = false
	s.Requirements = []OwnerCoverage{{UnavailableReason: "trusted inventory requirements were not supplied"}}
	return s
}

func (s *fixtureSession) SelectRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsRune(root, '\x00') {
		return ErrInvalidPath
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if s.root == root {
		s.mu.Unlock()
		return nil
	}
	s.root = root
	s.gen.Root++
	workers := s.invalidateLocked(func(op *readOperation) bool { return true })
	s.mu.Unlock()
	return s.terminateAndCheck(workers)
}

func (s *fixtureSession) Register(registration OwnerRegistration) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	updateToken, tracked := s.inventory.beginOwnerUpdate(registration.Owner)
	if tracked {
		s.gen.Inventory = s.inventory.Snapshot().Revision
	}
	workers := s.invalidateLocked(func(op *readOperation) bool { return tracked && op.gen.Inventory != s.gen.Inventory })
	registrationErr := s.registry.Register(registration)
	if registrationErr == nil {
		s.gen.Policy = s.registry.View().Generation
		s.syncInventoryLocked()
		workers = append(workers, s.invalidateLocked(func(op *readOperation) bool { return op.gen.Policy != s.gen.Policy })...)
	}
	s.mu.Unlock()
	cleanupErr := s.terminateAndCheck(workers)
	if registrationErr != nil {
		return errors.Join(registrationErr, cleanupErr)
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if tracked {
		s.mu.Lock()
		completed := s.inventory.completeOwnerUpdate(registration.Owner, updateToken)
		if completed {
			s.gen.Inventory = s.inventory.Snapshot().Revision
		}
		s.mu.Unlock()
		if !completed {
			return ErrInventoryIncomplete
		}
	}
	return nil
}

func (s *fixtureSession) RegisterRotation(owner string, next []OwnerRegistration) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	updateToken, tracked := s.inventory.beginOwnerUpdate(owner)
	if tracked {
		s.gen.Inventory = s.inventory.Snapshot().Revision
	}
	workers := s.invalidateLocked(func(op *readOperation) bool { return tracked && op.gen.Inventory != s.gen.Inventory })
	registrationErr := s.registry.RegisterRotation(owner, next)
	if registrationErr == nil {
		s.gen.Policy = s.registry.View().Generation
		s.syncInventoryLocked()
		workers = append(workers, s.invalidateLocked(func(op *readOperation) bool { return op.gen.Policy != s.gen.Policy })...)
	}
	s.mu.Unlock()
	cleanupErr := s.terminateAndCheck(workers)
	if registrationErr != nil {
		return errors.Join(registrationErr, cleanupErr)
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if tracked {
		s.mu.Lock()
		completed := s.inventory.completeOwnerUpdate(owner, updateToken)
		if completed {
			s.gen.Inventory = s.inventory.Snapshot().Revision
		}
		s.mu.Unlock()
		if !completed {
			return ErrInventoryIncomplete
		}
	}
	return nil
}

func (s *fixtureSession) Revoke(invocationID string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if invocationID != "" {
		if _, exists := s.claimed[invocationID]; !exists {
			s.mu.Unlock()
			return ErrInvocationID
		}
	}
	s.gen.Grant++
	// Grant is a session-wide generation. Advancing it invalidates every
	// receipt bound to the previous revision, so every active worker must stop
	// even when the initiating revoke named one invocation.
	workers := s.invalidateLocked(func(op *readOperation) bool { return true })
	s.mu.Unlock()
	return s.terminateAndCheck(workers)
}

func (s *fixtureSession) SetCheckpointHook(hook CheckpointHook) {
	s.mu.Lock()
	s.hook = hook
	s.mu.Unlock()
}

func (s *fixtureSession) PublishedResults() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([][]byte, len(s.published))
	for i := range s.published {
		results[i] = append([]byte(nil), s.published[i]...)
	}
	return results
}

func (s *fixtureSession) Evidence() []ReadEvidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]ReadEvidence, len(s.evidence))
	copy(results, s.evidence)
	for i := range results {
		results[i].Probe.Details = append([]string(nil), results[i].Probe.Details...)
	}
	return results
}

// GenerationSnapshot is test-harness evidence only. It exposes no authority
// and is used to record whether coordinator-owned revisions changed around a
// disposable native scenario.
func (s *fixtureSession) GenerationSnapshot() GenerationSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation := s.gen
	generation.Policy = s.registry.View().Generation
	if s.inventory != nil {
		generation.Inventory = s.inventory.Snapshot().Revision
	}
	return generation
}

func (s *fixtureSession) syncInventoryLocked() {
	if s.inventory == nil {
		s.gen.Inventory = 0
		return
	}
	s.inventory.observe(s.registry.View())
	s.gen.Inventory = s.inventory.Snapshot().Revision
}

func (s *fixtureSession) Read(ctx context.Context, root, target, invocationID string) (WorkerResult, error) {
	if ctx == nil || invocationID == "" {
		return WorkerResult{Outcome: ReadRefused, Detail: ErrInvocationID.Error()}, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadRefused, Detail: ErrSessionClosed.Error()}, nil
	}
	if s.cleanupBlocked {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadNotExecuted, Detail: ErrCleanupPending.Error()}, nil
	}
	if s.inventory == nil {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadNotExecuted, Detail: ErrInventoryIncomplete.Error()}, nil
	}
	coverage := s.inventory.Snapshot()
	if err := coverage.AdmissionError(); err != nil {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadNotExecuted, Detail: err.Error()}, nil
	}
	if coverage.Revision != s.gen.Inventory {
		s.gen.Inventory = coverage.Revision
	}
	if s.root == "" {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadRefused, Detail: ErrRootNotSet.Error()}, nil
	}
	if root != s.root {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadRefused, Detail: ErrOutOfScope.Error()}, nil
	}
	if _, exists := s.claimed[invocationID]; exists {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadRefused, Detail: ErrInvocationID.Error()}, nil
	}
	s.claimed[invocationID] = struct{}{}
	view := s.registry.View()
	if view.Generation != s.gen.Policy {
		s.gen.Policy = view.Generation
	}
	operationGen := s.gen
	hook := s.hook
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "cancelled before preparation"}, nil
	}
	if err := runCheckpoint(hook, CheckpointBeforeOpen); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "cancelled before open"}, nil
	}
	prepared, err := s.broker.PrepareRead(root, target, operationGen)
	if err != nil {
		return refusalFor(err), nil
	}
	defer prepared.Close()
	if err := runCheckpoint(hook, CheckpointAfterOpen); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "cancelled after open"}, nil
	}
	if err := runCheckpoint(hook, CheckpointAfterClassify); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "cancelled after classification"}, nil
	}
	s.mu.Lock()
	coverage = s.inventory.Snapshot()
	coverageErr := coverage.AdmissionError()
	if coverageErr == nil && (s.gen.Inventory != coverage.Revision || operationGen.Inventory != coverage.Revision || prepared.Generations().Inventory != coverage.Revision) {
		s.gen.Inventory = coverage.Revision
		coverageErr = ErrPolicyChanged
	}
	s.mu.Unlock()
	if coverageErr != nil {
		return WorkerResult{Outcome: ReadNotExecuted, Detail: coverageErr.Error()}, nil
	}
	if prepared.Class() == ClassCredential || prepared.Class() == ClassSettings || prepared.Class() == ClassTokenDatabase {
		return WorkerResult{Outcome: ReadRefused, Detail: ErrProtected.Error()}, nil
	}
	request := AuthorizationRequest{
		InvocationID: invocationID, Identity: prepared.Identity(), Class: prepared.Class(),
		Generations: prepared.Generations(), RequestDigest: prepared.RequestDigest(),
	}
	receipt, err := s.authorizer.AuthorizeAndConsume(ctx, request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return WorkerResult{Outcome: ReadCancelled, Detail: "cancelled before authorization"}, nil
		}
		return WorkerResult{Outcome: ReadRefused, Detail: "explicit once approval is unavailable"}, nil
	}
	if receipt.InvocationID != invocationID || !ReceiptMatches(prepared, receipt) {
		return WorkerResult{Outcome: ReadNotExecuted, Detail: ErrStaleReceipt.Error()}, nil
	}

	operationCtx, cancel := context.WithCancel(ctx)
	op := &readOperation{id: invocationID, gen: operationGen, state: StatePrepared, cancel: cancel, done: make(chan struct{}), evidence: ReadEvidence{Case: invocationID, Generations: operationGen}}
	s.mu.Lock()
	if !s.currentLocked(op) {
		s.mu.Unlock()
		cancel()
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "authorization became stale before worker launch"}, nil
	}
	s.active[invocationID] = op
	op.state = StateLaunching
	op.evidence.Outcome = ReadNotExecuted
	s.mu.Unlock()

	result := s.runWorker(operationCtx, op, prepared)
	cancel()
	s.finish(op, result)
	return result, nil
}

func (s *fixtureSession) runWorker(ctx context.Context, op *readOperation, prepared PreparedRead) WorkerResult {
	worker, err := s.launcher.Start(ctx)
	if err != nil {
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "sandbox setup refused before capability handoff: " + boundedText(err.Error(), 768)}
	}
	s.mu.Lock()
	op.worker = worker
	op.cleanupRequired = true
	op.evidence.WorkerStarted = true
	invalid := !s.currentLocked(op) || op.revoked
	s.mu.Unlock()
	if invalid {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("revoked worker cleanup could not be confirmed", cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("revoked before startup acknowledgement", cleanup)}
	}
	if err := worker.Ready(ctx); err != nil {
		cleanup := s.terminateAndWait(op, worker)
		diagnostic := workerFailureDiagnostic(worker, err)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("startup refused and worker cleanup is incomplete: "+diagnostic, cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("sandbox worker did not acknowledge startup: "+diagnostic, cleanup)}
	}
	op.probe = worker.Probe()
	op.evidence.Probe = op.probe
	if !validWorkerProbe(op.probe) {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("sandbox probe failed and worker cleanup is incomplete", cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("required sandbox probes were not established", cleanup)}
	}
	s.mu.Lock()
	if !s.currentLocked(op) || op.revoked {
		s.mu.Unlock()
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("revoked worker cleanup could not be confirmed", cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("revoked before read capability handoff", cleanup)}
	}
	op.state = StateReady
	op.evidence.WorkerReady = true
	op.evidence.Outcome = ReadNotExecuted
	s.mu.Unlock()

	if err := runCheckpoint(s.checkpointHook(), CheckpointBeforeHandoff); err != nil {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("cancelled before handoff; worker cleanup is incomplete", cleanup)}
		}
		return WorkerResult{Outcome: ReadCancelled, Detail: withTerminationDiagnostic("cancelled before read capability handoff; no target-data descriptor was released", cleanup)}
	}
	if err := s.broker.Revalidate(prepared); err != nil {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("protected-location refusal and worker cleanup is incomplete", cleanup)}
		}
		refusal := refusalFor(err)
		refusal.Detail = withTerminationDiagnostic(refusal.Detail, cleanup)
		return refusal
	}
	if err := runCheckpoint(s.checkpointHook(), CheckpointAfterFinalRevalidation); err != nil {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("post-validation fixture transition failed and worker cleanup is incomplete", cleanup)}
		}
		return WorkerResult{Outcome: ReadCancelled, Detail: withTerminationDiagnostic("post-validation fixture transition failed before capability handoff", cleanup)}
	}
	capability, err := prepared.DuplicateForWorker()
	if err != nil {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("read capability could not be duplicated and worker cleanup is incomplete", cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("read capability could not be duplicated", cleanup)}
	}
	defer capability.Close()

	// This short lock serializes permission revision checks with the actual FD
	// handoff. It does not cover filesystem namespace changes by independent
	// same-user processes; Revalidate is a bounded observation, not a lock.
	s.mu.Lock()
	if !s.currentLocked(op) || op.revoked {
		s.mu.Unlock()
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("revoked before handoff; worker cleanup is incomplete", cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("revoked before read capability handoff", cleanup)}
	}
	if err := worker.TransferCapability(ctx, capability, prepared.Identity()); err != nil {
		s.mu.Unlock()
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("capability handoff failed and worker cleanup is incomplete", cleanup)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("capability handoff was refused", cleanup)}
	}
	op.released = true
	op.evidence.CapabilitySent = true
	op.evidence.CapabilityIdentity = prepared.Identity()
	s.mu.Unlock()
	if heldWorker, ok := worker.(HeldCapabilityWorker); ok {
		if err := heldWorker.AwaitCapabilityHeld(ctx); err != nil {
			cleanup := s.terminateAndWait(op, worker)
			if !cleanup.cleanupConfirmed() {
				return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("held-capability acknowledgement and cleanup are unconfirmed", cleanup)}
			}
			return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("worker did not confirm held capability", cleanup)}
		}
		s.mu.Lock()
		op.evidence.CapabilityHeld = true
		op.state = StateCapabilityHeld
		invalid := !s.currentLocked(op) || op.revoked
		s.mu.Unlock()
		if invalid {
			cleanup := s.terminateAndWait(op, worker)
			if !cleanup.cleanupConfirmed() {
				return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("policy changed while worker held the capability; cleanup is unconfirmed", cleanup)}
			}
			return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("policy changed before held read release; no read started", cleanup)}
		}
		if err := runCheckpoint(s.checkpointHook(), CheckpointAfterCapabilityHeld); err != nil {
			cleanup := s.terminateAndWait(op, worker)
			if !cleanup.cleanupConfirmed() {
				return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("held read cancelled and cleanup is unconfirmed", cleanup)}
			}
			return WorkerResult{Outcome: ReadCancelled, Detail: withTerminationDiagnostic("held read cancelled before release; no read started", cleanup)}
		}
		// Serialize the final validity check and release packet against owner
		// registration/revocation. If invalidation wins, the worker is killed
		// while still at the hold gate. If release wins, invalidation must join
		// the worker before any sensitive owner write.
		s.mu.Lock()
		invalid = !s.currentLocked(op) || op.revoked
		if !invalid {
			if err := heldWorker.ReleaseHeldRead(ctx); err != nil {
				s.mu.Unlock()
				cleanup := s.terminateAndWait(op, worker)
				return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("held read release outcome is unknown", cleanup)}
			}
		}
		s.mu.Unlock()
		if invalid {
			cleanup := s.terminateAndWait(op, worker)
			if !cleanup.cleanupConfirmed() {
				return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("invalidated held read cleanup is unconfirmed", cleanup)}
			}
			return WorkerResult{Outcome: ReadNotExecuted, Detail: withTerminationDiagnostic("policy changed before held read release; no read started", cleanup)}
		}
	}

	if err := worker.AwaitReadStarted(ctx); err != nil {
		cleanup := s.terminateAndWait(op, worker)
		return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("capability was released but read-start acknowledgement is missing", cleanup)}
	}
	s.mu.Lock()
	op.state = StateRunning
	op.evidence.ReadStarted = true
	stale := !s.currentLocked(op) || op.revoked
	s.mu.Unlock()
	if stale {
		cleanup := s.terminateAndWait(op, worker)
		if !cleanup.cleanupConfirmed() {
			return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("read capability was released before its authorization generation became stale; read outcome and cleanup are unconfirmed", cleanup)}
		}
		return WorkerResult{Outcome: ReadUnknown, Detail: withTerminationDiagnostic("read capability and start acknowledgement were received before revocation; read result was not established and no data was published", cleanup)}
	}

	workerResult, readErr := worker.ReadResult(ctx)
	wait := s.waitWorker(op, worker)
	if readErr != nil || wait.ExitErr != nil || !wait.Joined {
		return WorkerResult{Outcome: ReadUnknown, Detail: "worker result or cleanup could not be confirmed"}
	}
	if len(workerResult.Bytes) > MaxReadBytes {
		return WorkerResult{Outcome: ReadUnknown, Detail: ErrResultTooLarge.Error()}
	}
	if workerResult.Outcome != ReadSucceeded {
		workerResult.Bytes = nil
		if workerResult.Outcome == "" {
			workerResult.Outcome = ReadUnknown
		}
		return workerResult
	}
	if err := runCheckpoint(s.checkpointHook(), CheckpointAfterCapture); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "captured bytes discarded after cancellation"}
	}
	if err := s.broker.Revalidate(prepared); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "captured bytes discarded after protected-policy revalidation failed"}
	}
	if err := runCheckpoint(s.checkpointHook(), CheckpointBeforePublish); err != nil {
		return WorkerResult{Outcome: ReadCancelled, Detail: "captured bytes discarded before publication"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.currentLocked(op) || op.revoked {
		return WorkerResult{Outcome: ReadCancelled, Detail: "stale captured bytes suppressed before publication"}
	}
	copyForSink := append([]byte(nil), workerResult.Bytes...)
	s.published = append(s.published, copyForSink)
	op.evidence.Published = true
	op.evidence.Outcome = ReadSucceeded
	return WorkerResult{Outcome: ReadSucceeded, Bytes: append([]byte(nil), copyForSink...), Detail: workerResult.Detail}
}

func workerFailureDiagnostic(worker SandboxWorker, fallback error) string {
	if source, ok := worker.(interface{ FailureDetail() string }); ok {
		if detail := source.FailureDetail(); detail != "" {
			return boundedText(detail, 768)
		}
	}
	if fallback == nil {
		return "worker startup failed"
	}
	return boundedText(fallback.Error(), 768)
}

func validWorkerProbe(probe WorkerProbe) bool {
	return probe.NoNewPrivs && probe.SeccompMode == 2 && probe.SocketCreationDenied &&
		probe.PathOpenDenied && probe.ReadCapabilityInitiallyAbsent &&
		probe.OnlyProtocolDescriptor && probe.EnvironmentCleared
}

func (s *fixtureSession) checkpointHook() CheckpointHook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hook
}

func runCheckpoint(hook CheckpointHook, checkpoint Checkpoint) error {
	if hook == nil {
		return nil
	}
	return hook(checkpoint)
}

func refusalFor(err error) WorkerResult {
	switch {
	case errors.Is(err, ErrProtected):
		return WorkerResult{Outcome: ReadRefused, Detail: ErrProtected.Error()}
	case errors.Is(err, ErrDirectoryCoverage):
		return WorkerResult{Outcome: ReadRefused, Detail: ErrDirectoryCoverage.Error()}
	case errors.Is(err, ErrPolicyChanged), errors.Is(err, ErrStaleReceipt):
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "prepared read or policy changed before execution"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return WorkerResult{Outcome: ReadCancelled, Detail: "read cancelled before operation release"}
	default:
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "read preparation or policy validation refused"}
	}
}

func (s *fixtureSession) currentLocked(op *readOperation) bool {
	if s.closed || s.cleanupBlocked || op == nil || op.revoked || op.gen != s.gen {
		return false
	}
	if s.registry.View().Generation != s.gen.Policy {
		return false
	}
	return s.inventory != nil && s.inventory.Snapshot().Complete && s.inventory.Snapshot().Revision == s.gen.Inventory
}

func (s *fixtureSession) invalidateLocked(selectOp func(*readOperation) bool) []SandboxWorker {
	workers := make([]SandboxWorker, 0)
	for _, op := range s.active {
		if !selectOp(op) {
			continue
		}
		op.revoked = true
		op.cancel()
		op.cleanupObligation = true
		if op.state != StateJoined {
			op.state = StateTerminating
			s.cleanupBlocked = true
		}
		if op.worker != nil {
			workers = append(workers, op.worker)
		}
	}
	return workers
}

func (s *fixtureSession) refreshCleanupBarrierLocked() {
	s.cleanupBlocked = false
	for _, op := range s.active {
		if op.cleanupObligation && !op.workerJoined {
			s.cleanupBlocked = true
			return
		}
	}
}

func terminateWorkers(workers []SandboxWorker) error {
	var errs []error
	for _, worker := range workers {
		if err := worker.Terminate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *fixtureSession) terminateAndCheck(workers []SandboxWorker) error {
	terminateErr := terminateWorkers(workers)
	s.mu.Lock()
	pending := false
	for _, op := range s.active {
		if !op.completed && !op.workerJoined {
			pending = true
			break
		}
	}
	s.mu.Unlock()
	if pending {
		return errors.Join(terminateErr, ErrCleanupPending)
	}
	return terminateErr
}

func (s *fixtureSession) waitWorker(op *readOperation, worker SandboxWorker) workerWaitResult {
	if worker == nil {
		return workerWaitResult{Joined: true}
	}
	<-s.startWorkerWait(op, worker)
	return s.workerWaitSnapshot(op)
}

func (s *fixtureSession) waitWorkerContext(ctx context.Context, op *readOperation, worker SandboxWorker) (workerWaitResult, error) {
	if worker == nil {
		return workerWaitResult{Joined: true}, nil
	}
	select {
	case <-s.startWorkerWait(op, worker):
		return s.workerWaitSnapshot(op), nil
	case <-ctx.Done():
		return workerWaitResult{}, ctx.Err()
	}
}

// workerWaitSnapshot reads mutable join state under the same lock used by
// Close. waitDone publishes the waiter goroutine's writes, but Close may
// independently confirm a join after that channel has closed.
func (s *fixtureSession) workerWaitSnapshot(op *readOperation) workerWaitResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return workerWaitResult{ExitErr: op.waitErr, Joined: op.workerJoined}
}

func (s *fixtureSession) startWorkerWait(op *readOperation, worker SandboxWorker) <-chan struct{} {
	op.waitOnce.Do(func() {
		op.waitDone = make(chan struct{})
		go func() {
			err := worker.Wait()
			joined := worker.Joined()
			if !joined && err == nil {
				err = ErrCleanupPending
			}
			s.mu.Lock()
			op.waitErr = err
			op.workerJoined = joined
			s.mu.Unlock()
			close(op.waitDone)
		}()
	})
	return op.waitDone
}

func (s *fixtureSession) terminateAndWait(op *readOperation, worker SandboxWorker) workerTerminationResult {
	if worker == nil {
		return workerTerminationResult{Wait: workerWaitResult{Joined: true}}
	}
	return workerTerminationResult{TerminationErr: worker.Terminate(), Wait: s.waitWorker(op, worker)}
}

func (s *fixtureSession) finish(op *readOperation, result WorkerResult) {
	var terminate []SandboxWorker
	s.mu.Lock()
	if op.cleanupRequired && !op.workerJoined {
		s.cleanupBlocked = true
		op.cleanupObligation = true
		op.state = StateTerminating
		op.completed = false
		op.evidence.Outcome = ReadUnknown
		op.evidence.WorkerJoined = false
		if !op.evidenceRecorded {
			s.evidence = append(s.evidence, op.evidence)
			op.evidenceRecorded = true
		}
		for _, other := range s.active {
			if other == op || other.completed {
				continue
			}
			other.revoked = true
			other.cancel()
			other.cleanupObligation = true
			other.state = StateTerminating
			if other.worker != nil {
				terminate = append(terminate, other.worker)
			}
		}
		s.mu.Unlock()
		_ = terminateWorkers(terminate)
		return
	}
	s.completeJoinedLocked(op, result)
	s.mu.Unlock()
}

func (s *fixtureSession) completeJoinedLocked(op *readOperation, result WorkerResult) {
	if op.completed {
		return
	}
	op.state = StateJoined
	op.completed = true
	op.evidence.WorkerJoined = op.workerJoined
	if result.Outcome == "" {
		result.Outcome = ReadUnknown
	}
	if !op.evidence.Published {
		op.evidence.Outcome = result.Outcome
	}
	if op.evidence.WorkerStarted && !op.evidence.ReadStarted && op.evidence.Outcome == "" {
		op.evidence.Outcome = ReadNotExecuted
	}
	if !op.evidenceRecorded {
		s.evidence = append(s.evidence, op.evidence)
		op.evidenceRecorded = true
	}
	delete(s.active, op.id)
	close(op.done)
	s.refreshCleanupBarrierLocked()
}

func (s *fixtureSession) Close(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	s.mu.Lock()
	s.closed = true
	s.gen.Root++
	s.gen.Policy++
	s.gen.Grant++
	if s.inventory != nil {
		s.inventory.close()
		s.gen.Inventory = s.inventory.Snapshot().Revision
	}
	workers := s.invalidateLocked(func(*readOperation) bool { return true })
	done := make([]<-chan struct{}, 0, len(s.active))
	activeOps := make([]*readOperation, 0, len(s.active))
	type workerWait struct {
		op     *readOperation
		worker SandboxWorker
	}
	waits := make([]workerWait, 0, len(s.active))
	for _, op := range s.active {
		done = append(done, op.done)
		activeOps = append(activeOps, op)
		if op.worker != nil {
			waits = append(waits, workerWait{op: op, worker: op.worker})
		}
	}
	s.mu.Unlock()

	terminateErr := terminateWorkers(workers)
	for _, item := range waits {
		if item.worker.Joined() {
			s.mu.Lock()
			item.op.workerJoined = true
			s.mu.Unlock()
			continue
		}
		waitResult, waitErr := s.waitWorkerContext(ctx, item.op, item.worker)
		if waitErr != nil {
			if !item.worker.Joined() {
				terminateErr = errors.Join(terminateErr, ErrCleanupPending)
			} else {
				terminateErr = errors.Join(terminateErr, waitErr)
			}
		} else if waitResult.ExitErr != nil {
			// Wait errors after Join are process-exit diagnostics, not evidence
			// that the direct child remains alive or unreaped.
			terminateErr = errors.Join(terminateErr, waitResult.ExitErr)
		}
	}
	s.mu.Lock()
	for _, op := range activeOps {
		if !op.completed && op.cleanupRequired && op.workerJoined {
			s.completeJoinedLocked(op, WorkerResult{Outcome: ReadUnknown})
		}
	}
	s.mu.Unlock()
	for _, wait := range done {
		select {
		case <-wait:
		case <-ctx.Done():
			return errors.Join(ctx.Err(), terminateErr)
		}
	}
	launcherErr := s.launcher.Close()
	return errors.Join(terminateErr, launcherErr, s.registry.Close())
}

func (s *fixtureSession) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("readbroker fixture session: active=%d closed=%t", len(s.active), s.closed)
}

func (s *fixtureSession) ActiveOperationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}
