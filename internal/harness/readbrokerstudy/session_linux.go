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
	mu         sync.Mutex
	registry   PolicyRegistry
	broker     ReadBroker
	launcher   WorkerLauncher
	authorizer FixtureAuthorizer
	root       string
	gen        GenerationSet
	closed     bool
	claimed    map[string]struct{}
	active     map[string]*readOperation
	hook       CheckpointHook
	published  [][]byte
	evidence   []ReadEvidence
}

type readOperation struct {
	id           string
	gen          GenerationSet
	state        OperationState
	worker       SandboxWorker
	cancel       context.CancelFunc
	revoked      bool
	released     bool
	workerJoined bool
	completed    bool
	done         chan struct{}
	probe        WorkerProbe
	evidence     ReadEvidence
}

var _ FixtureSession = (*fixtureSession)(nil)

func NewFixtureSession(registry PolicyRegistry, broker ReadBroker, launcher WorkerLauncher, authorizer FixtureAuthorizer) (FixtureSession, error) {
	if registry == nil || broker == nil || launcher == nil || authorizer == nil {
		return nil, ErrUnavailable
	}
	return &fixtureSession{
		registry: registry, broker: broker, launcher: launcher, authorizer: authorizer,
		gen:     GenerationSet{Policy: registry.View().Generation, Grant: 1},
		claimed: make(map[string]struct{}), active: make(map[string]*readOperation),
	}, nil
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
	if err := s.registry.Register(registration); err != nil {
		s.mu.Unlock()
		return err
	}
	s.gen.Policy = s.registry.View().Generation
	workers := s.invalidateLocked(func(op *readOperation) bool { return op.gen.Policy != s.gen.Policy })
	s.mu.Unlock()
	return s.terminateAndCheck(workers)
}

func (s *fixtureSession) RegisterRotation(owner string, next []OwnerRegistration) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if err := s.registry.RegisterRotation(owner, next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.gen.Policy = s.registry.View().Generation
	workers := s.invalidateLocked(func(op *readOperation) bool { return op.gen.Policy != s.gen.Policy })
	s.mu.Unlock()
	return s.terminateAndCheck(workers)
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

func (s *fixtureSession) Read(ctx context.Context, root, target, invocationID string) (WorkerResult, error) {
	if ctx == nil || invocationID == "" {
		return WorkerResult{Outcome: ReadRefused, Detail: ErrInvocationID.Error()}, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return WorkerResult{Outcome: ReadRefused, Detail: ErrSessionClosed.Error()}, nil
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
	op.evidence.WorkerStarted = true
	invalid := !s.currentLocked(op) || op.revoked
	s.mu.Unlock()
	if invalid {
		_ = worker.Terminate()
		if waitErr := s.waitWorker(op, worker); waitErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "revoked worker cleanup could not be confirmed"}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "revoked before startup acknowledgement"}
	}
	if err := worker.Ready(ctx); err != nil {
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "startup refused and worker cleanup is incomplete: " + boundedText(err.Error(), 768)}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "sandbox worker did not acknowledge startup: " + boundedText(err.Error(), 768)}
	}
	op.probe = worker.Probe()
	op.evidence.Probe = op.probe
	if !validWorkerProbe(op.probe) {
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "sandbox probe failed and worker cleanup is incomplete"}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "required sandbox probes were not established"}
	}
	s.mu.Lock()
	if !s.currentLocked(op) || op.revoked {
		s.mu.Unlock()
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "revoked worker cleanup could not be confirmed"}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "revoked before read capability handoff"}
	}
	op.state = StateReady
	op.evidence.Outcome = ReadNotExecuted
	s.mu.Unlock()

	if err := runCheckpoint(s.checkpointHook(), CheckpointBeforeHandoff); err != nil {
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "cancelled before handoff; worker cleanup is incomplete"}
		}
		return WorkerResult{Outcome: ReadCancelled, Detail: "cancelled before read capability handoff"}
	}
	if err := s.broker.Revalidate(prepared); err != nil {
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "protected-location refusal and worker cleanup is incomplete"}
		}
		return refusalFor(err)
	}
	capability, err := prepared.DuplicateForWorker()
	if err != nil {
		_ = s.terminateAndWait(op, worker)
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "read capability could not be duplicated"}
	}
	defer capability.Close()

	// This short lock serializes permission revision checks with the actual FD
	// handoff. It does not cover filesystem namespace changes by independent
	// same-user processes; Revalidate is a bounded observation, not a lock.
	s.mu.Lock()
	if !s.currentLocked(op) || op.revoked {
		s.mu.Unlock()
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "revoked before handoff; worker cleanup is incomplete"}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "revoked before read capability handoff"}
	}
	if err := worker.TransferCapability(ctx, capability, prepared.Identity()); err != nil {
		s.mu.Unlock()
		cleanupErr := s.terminateAndWait(op, worker)
		if cleanupErr != nil {
			return WorkerResult{Outcome: ReadUnknown, Detail: "capability handoff failed and worker cleanup is incomplete"}
		}
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "capability handoff was refused"}
	}
	op.released = true
	op.evidence.CapabilitySent = true
	s.mu.Unlock()

	if err := worker.AwaitReadStarted(ctx); err != nil {
		_ = s.terminateAndWait(op, worker)
		return WorkerResult{Outcome: ReadUnknown, Detail: "capability was released but read-start acknowledgement is missing"}
	}
	s.mu.Lock()
	op.state = StateRunning
	op.evidence.ReadStarted = true
	stale := !s.currentLocked(op) || op.revoked
	s.mu.Unlock()
	if stale {
		_ = s.terminateAndWait(op, worker)
		return WorkerResult{Outcome: ReadCancelled, Detail: "read started after its authorization generation became stale"}
	}

	workerResult, readErr := worker.ReadResult(ctx)
	waitErr := s.waitWorker(op, worker)
	if readErr != nil || waitErr != nil {
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
	case errors.Is(err, ErrPolicyChanged), errors.Is(err, ErrStaleReceipt):
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "prepared read or policy changed before execution"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return WorkerResult{Outcome: ReadCancelled, Detail: "read cancelled before operation release"}
	default:
		return WorkerResult{Outcome: ReadNotExecuted, Detail: "read preparation or policy validation refused"}
	}
}

func (s *fixtureSession) currentLocked(op *readOperation) bool {
	if s.closed || op == nil || op.revoked || op.gen != s.gen {
		return false
	}
	return s.registry.View().Generation == s.gen.Policy
}

func (s *fixtureSession) invalidateLocked(selectOp func(*readOperation) bool) []SandboxWorker {
	workers := make([]SandboxWorker, 0)
	for _, op := range s.active {
		if !selectOp(op) {
			continue
		}
		op.revoked = true
		op.cancel()
		if op.state != StateJoined {
			op.state = StateTerminating
		}
		if op.worker != nil {
			workers = append(workers, op.worker)
		}
	}
	return workers
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

func (s *fixtureSession) waitWorker(op *readOperation, worker SandboxWorker) error {
	if worker == nil {
		return nil
	}
	err := worker.Wait()
	if err == nil {
		s.mu.Lock()
		op.workerJoined = true
		s.mu.Unlock()
	}
	return err
}

func (s *fixtureSession) terminateAndWait(op *readOperation, worker SandboxWorker) error {
	if worker == nil {
		return nil
	}
	return errors.Join(worker.Terminate(), s.waitWorker(op, worker))
}

func (s *fixtureSession) finish(op *readOperation, result WorkerResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op.state = StateJoined
	op.completed = true
	if result.Outcome == "" {
		result.Outcome = ReadUnknown
	}
	if !op.evidence.Published {
		op.evidence.Outcome = result.Outcome
	}
	if op.evidence.WorkerStarted && !op.evidence.ReadStarted && op.evidence.Outcome == "" {
		op.evidence.Outcome = ReadNotExecuted
	}
	s.evidence = append(s.evidence, op.evidence)
	delete(s.active, op.id)
	close(op.done)
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
	workers := s.invalidateLocked(func(*readOperation) bool { return true })
	done := make([]<-chan struct{}, 0, len(s.active))
	for _, op := range s.active {
		done = append(done, op.done)
	}
	s.mu.Unlock()

	terminateErr := terminateWorkers(workers)
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
