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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxRetainedAdmissions = 1
const retainedAdmissionTimeout = 5 * time.Minute
const retainedCleanupTimeout = 5 * time.Second

var (
	ErrAdmissionPending = errors.New("a retained read admission or cleanup obligation is already pending")
	ErrAdmissionStale   = errors.New("retained read admission is stale")
	ErrAdmissionClosed  = errors.New("retained read admission is closed")
)

type admissionState uint8

const (
	admissionOpening admissionState = iota + 1
	admissionPendingApproval
	admissionPrepared
	admissionLaunching
	admissionReady
	admissionCapabilityHeld
	admissionRunning
	admissionCaptured
	admissionRetiring
	admissionCleanupPending
)

type admissionRecord struct {
	retireLock chan struct{}
	lifetime   context.Context
	cancel     context.CancelFunc
	deadline   time.Time
	ref        *AdmissionReference
	request    AdmissionRequest
	metadata   AdmissionMetadata
	state      admissionState
	prepared   PreparedRead
	operation  *readOperation
	lookupDone chan struct{}
	lookupErr  error
	retired    bool
	closeErr   error
}

type retainedPreparation struct {
	session *fixtureSession
	record  *admissionRecord
}

func newAdmissionID() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (p *retainedPreparation) Metadata() AdmissionMetadata {
	if p == nil || p.record == nil {
		return AdmissionMetadata{}
	}
	p.session.mu.Lock()
	defer p.session.mu.Unlock()
	return p.record.metadata
}

func (p *retainedPreparation) Close(ctx context.Context) error {
	if p == nil || p.session == nil || p.record == nil {
		return nil
	}
	return p.session.retireAdmission(ctx, p.record)
}

func (p *retainedPreparation) WithCommit(ctx context.Context, commit func() error) error {
	if p == nil || p.session == nil || p.record == nil || commit == nil || ctx == nil {
		return ErrAdmissionStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s := p.session
	s.mu.Lock()
	defer s.mu.Unlock()
	record := p.record
	if s.closed || s.admissions[record.ref.id] != record || record.ref.owner != s || record.state != admissionPrepared || record.retired || record.lifetime == nil || record.lifetime.Err() != nil || time.Now().After(record.deadline) || record.prepared == nil || s.root != record.request.Root || s.rootGeneration != record.request.RootGeneration || s.gen != record.metadata.Generations || s.registry.View().Generation != record.metadata.Generations.Policy || s.inventory == nil || !s.inventory.Snapshot().Complete || s.inventory.Snapshot().Revision != record.metadata.Generations.Inventory {
		return ErrAdmissionStale
	}
	if err := s.broker.Revalidate(record.prepared); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit()
}

func (s *fixtureSession) Admit(ctx context.Context, request AdmissionRequest) (AdmissionMetadata, error) {
	reference, err := s.BeginAdmission(ctx, request)
	if err != nil {
		return AdmissionMetadata{}, err
	}
	return s.WaitAdmission(ctx, reference)
}

func (s *fixtureSession) BeginAdmission(ctx context.Context, request AdmissionRequest) (*AdmissionReference, error) {
	if ctx == nil || request.ConversationID == "" || request.TurnID == "" || request.CallID == "" || request.Destination == "" || len(request.Destination) > 512 || !filepath.IsAbs(request.Root) || filepath.Clean(request.Root) != request.Root || !filepath.IsAbs(request.Target) || filepath.Clean(request.Target) != request.Target || strings.ContainsRune(request.Target, '\x00') {
		return nil, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSessionClosed
	}
	if s.cleanupBlocked {
		s.mu.Unlock()
		return nil, ErrCleanupPending
	}
	if s.root == "" || request.Root != s.root || request.RootGeneration != s.rootGeneration {
		s.mu.Unlock()
		return nil, ErrRootNotSet
	}
	if s.inventory == nil {
		s.mu.Unlock()
		return nil, ErrInventoryIncomplete
	}
	if err := s.inventory.Snapshot().AdmissionError(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if len(s.admissions) >= maxRetainedAdmissions {
		s.mu.Unlock()
		return nil, ErrAdmissionPending
	}
	view := s.registry.View()
	if view.Generation != s.gen.Policy {
		s.gen.Policy = view.Generation
	}
	coverage := s.inventory.Snapshot()
	if coverage.Revision != s.gen.Inventory {
		s.mu.Unlock()
		return nil, ErrPolicyChanged
	}
	id, err := newAdmissionID()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	timeout := s.admissionTimeout
	if timeout <= 0 || timeout > retainedAdmissionTimeout {
		timeout = retainedAdmissionTimeout
	}
	lifetime, cancel := context.WithTimeout(ctx, timeout)
	deadline, _ := lifetime.Deadline()
	record := &admissionRecord{
		retireLock: make(chan struct{}, 1), lifetime: lifetime, cancel: cancel, deadline: deadline,
		ref: &AdmissionReference{owner: s, id: id}, request: request,
		state: admissionOpening, lookupDone: make(chan struct{}),
		metadata: AdmissionMetadata{ServiceID: s.serviceID, ConversationID: request.ConversationID, TurnID: request.TurnID, CallID: request.CallID, Root: request.Root, Target: request.Target, RootGeneration: request.RootGeneration, Deadline: deadline, Destination: request.Destination, Generations: s.gen},
	}
	s.admissions[id] = record
	s.mu.Unlock()

	go func() {
		prepared, err := s.broker.PrepareRead(request.Root, request.Target, record.metadata.Generations)
		if err == nil {
			if revalidateErr := s.broker.Revalidate(prepared); revalidateErr != nil {
				err = revalidateErr
			}
		}
		s.finishAdmissionLookup(record, prepared, err)
	}()
	go func() {
		<-lifetime.Done()
		s.mu.Lock()
		if s.admissions[id] == record && record.lifetime == lifetime {
			record.retired = true
			record.state = admissionRetiring
		} else {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), retainedCleanupTimeout)
		defer cancel()
		_ = s.retireAdmission(cleanupCtx, record)
	}()

	return record.ref, nil
}

func (s *fixtureSession) WaitAdmission(ctx context.Context, reference *AdmissionReference) (AdmissionMetadata, error) {
	if ctx == nil || reference == nil || reference.owner != s || reference.id == "" {
		return AdmissionMetadata{}, ErrAdmissionStale
	}
	if err := ctx.Err(); err != nil {
		return AdmissionMetadata{}, err
	}
	s.mu.Lock()
	record := s.admissions[reference.id]
	if record == nil || record.ref != reference {
		s.mu.Unlock()
		return AdmissionMetadata{}, ErrAdmissionStale
	}
	lifetime := record.lifetime
	s.mu.Unlock()
	select {
	case <-record.lookupDone:
	case <-lifetime.Done():
		s.mu.Lock()
		record.retired = true
		record.state = admissionRetiring
		s.mu.Unlock()
		return AdmissionMetadata{}, lifetime.Err()
	case <-ctx.Done():
		s.mu.Lock()
		record.retired = true
		record.state = admissionRetiring
		record.cancel()
		s.mu.Unlock()
		return AdmissionMetadata{}, ctx.Err()
	}

	s.mu.Lock()
	if err := record.lookupErr; err != nil {
		s.mu.Unlock()
		return AdmissionMetadata{}, err
	}
	if s.admissions[reference.id] != record || record.retired || record.state != admissionPendingApproval {
		s.mu.Unlock()
		return AdmissionMetadata{}, ErrAdmissionStale
	}
	if record.lifetime == nil || record.lifetime.Err() != nil || time.Now().After(record.deadline) {
		record.retired = true
		record.state = admissionRetiring
		if record.cancel != nil {
			record.cancel()
		}
		s.mu.Unlock()
		_ = s.retireAdmission(ctx, record)
		return AdmissionMetadata{}, ErrAdmissionStale
	}
	metadata := record.metadata
	metadata.Reference = record.ref
	s.mu.Unlock()
	return metadata, nil
}

func (s *fixtureSession) finishAdmissionLookup(record *admissionRecord, prepared PreparedRead, lookupErr error) {
	s.mu.Lock()
	current := !s.closed && !record.retired && record.lifetime != nil && record.lifetime.Err() == nil && time.Now().Before(record.deadline) && s.admissions[record.ref.id] == record && s.root == record.request.Root && s.rootGeneration == record.request.RootGeneration && s.gen == record.metadata.Generations && s.registry.View().Generation == record.metadata.Generations.Policy && s.inventory != nil && s.inventory.Snapshot().Complete && s.inventory.Snapshot().Revision == record.metadata.Generations.Inventory
	if !current {
		record.retired = true
		record.state = admissionRetiring
		if record.cancel != nil {
			record.cancel()
		}
	}
	if current && lookupErr == nil && prepared != nil {
		class, protected := prepared.Class(), prepared.Class() != ClassOrdinary
		if class == ClassCredential || class == ClassSettings || class == ClassTokenDatabase {
			lookupErr = ErrProtected
		} else if protected && class != ClassProvider {
			lookupErr = ErrProtected
		} else if s.registry.DirectoryCoverageIncomplete(prepared.Identity()) {
			lookupErr = ErrDirectoryCoverage
		} else {
			record.metadata.Identity = prepared.Identity()
			record.metadata.Class = class
			digest, digestErr := admissionDigest(record.metadata)
			if digestErr != nil {
				lookupErr = digestErr
			} else {
				record.metadata.Binding = digest
				record.metadata.Reference = record.ref
				record.prepared = prepared
				record.state = admissionPendingApproval
			}
		}
	} else if current && lookupErr == nil {
		lookupErr = ErrAdmissionStale
	}
	if record.state != admissionPendingApproval && prepared != nil {
		record.prepared = prepared
	}
	record.lookupErr = lookupErr
	close(record.lookupDone)
	retire := record.retired || lookupErr != nil
	s.mu.Unlock()
	if retire {
		_ = s.retireAdmission(context.Background(), record)
	}
}

func admissionDigest(metadata AdmissionMetadata) ([32]byte, error) {
	canonical, err := json.Marshal(struct {
		ServiceID      string
		ConversationID string
		TurnID         string
		CallID         string
		Root           string
		Target         string
		RootGeneration uint64
		Deadline       time.Time
		Destination    string
		Identity       FileIdentity
		Class          ResourceClass
		Generations    GenerationSet
	}{metadata.ServiceID, metadata.ConversationID, metadata.TurnID, metadata.CallID, metadata.Root, metadata.Target, metadata.RootGeneration, metadata.Deadline, metadata.Destination, metadata.Identity, metadata.Class, metadata.Generations})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func (s *fixtureSession) PrepareAdmitted(ctx context.Context, reference *AdmissionReference, admissionBinding string, expectedPolicy uint64) (AdmittedPreparation, error) {
	if ctx == nil || reference == nil || reference.owner != s || reference.id == "" {
		return nil, ErrAdmissionStale
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	record := s.admissions[reference.id]
	if record == nil || record.ref != reference || record.state != admissionPendingApproval || record.retired || record.lifetime == nil || record.lifetime.Err() != nil || time.Now().After(record.deadline) {
		if record != nil && s.admissions[reference.id] == record {
			record.retired = true
			record.state = admissionRetiring
			if record.cancel != nil {
				record.cancel()
			}
		}
		s.mu.Unlock()
		return nil, ErrAdmissionStale
	}
	if hex.EncodeToString(record.metadata.Binding[:]) != admissionBinding || record.metadata.Generations.Policy != expectedPolicy || s.root != record.request.Root || s.rootGeneration != record.request.RootGeneration || s.gen != record.metadata.Generations || s.registry.View().Generation != expectedPolicy || s.inventory == nil || !s.inventory.Snapshot().Complete || s.inventory.Snapshot().Revision != record.metadata.Generations.Inventory {
		record.retired = true
		record.state = admissionRetiring
		if record.cancel != nil {
			record.cancel()
		}
		s.mu.Unlock()
		_ = s.retireAdmission(ctx, record)
		return nil, ErrAdmissionStale
	}
	prepared := record.prepared
	if prepared == nil {
		s.mu.Unlock()
		return nil, ErrAdmissionStale
	}
	if err := s.broker.Revalidate(prepared); err != nil {
		record.retired = true
		record.state = admissionRetiring
		if record.cancel != nil {
			record.cancel()
		}
		s.mu.Unlock()
		_ = s.retireAdmission(ctx, record)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	record.state = admissionPrepared
	s.mu.Unlock()
	return &retainedPreparation{session: s, record: record}, nil
}

// ExecuteAdmitted claims exactly one attempt for the same retained object
// approved by the runtime. It never reopens a path, consults the fixture
// authorizer, or publishes bytes to the study sink.
func (s *fixtureSession) ExecuteAdmitted(ctx context.Context, preparation AdmittedPreparation, authority ReadExecutionGate, capture func(AdmissionMetadata, ReadCompletion) error) (err error) {
	retained, ok := preparation.(*retainedPreparation)
	if ctx == nil || !ok || retained == nil || retained.session != s || retained.record == nil || authority == nil || capture == nil {
		return ErrAdmissionStale
	}
	record := retained.record
	retired := false
	retire := func() error {
		if retired {
			return nil
		}
		retired = true
		return retained.Close(ctx)
	}
	defer func() {
		if !retired {
			err = errors.Join(err, retire())
		}
	}()
	s.mu.Lock()
	valid := !s.closed && !s.cleanupBlocked && s.admissions[record.ref.id] == record && record.ref.owner == s && record.state == admissionPrepared && !record.retired && record.prepared != nil && record.lifetime != nil && record.lifetime.Err() == nil && time.Now().Before(record.deadline) && s.root == record.request.Root && s.rootGeneration == record.request.RootGeneration && s.gen == record.metadata.Generations && s.registry.View().Generation == record.metadata.Generations.Policy && s.inventory != nil && s.inventory.Snapshot().Complete && s.inventory.Snapshot().Revision == record.metadata.Generations.Inventory
	metadata := record.metadata
	prepared := record.prepared
	s.mu.Unlock()
	if !valid || metadata.Reference != record.ref || metadata.Binding == ([32]byte{}) || prepared == nil {
		return ErrAdmissionStale
	}
	if err := authority.Claim(); err != nil {
		return err
	}
	var op *readOperation
	var operationCtx context.Context
	var cancel context.CancelFunc
	start := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.cleanupBlocked || s.admissions[record.ref.id] != record || record.state != admissionPrepared || record.retired || record.prepared != prepared || record.lifetime == nil || record.lifetime.Err() != nil || time.Now().After(record.deadline) || s.gen != metadata.Generations || s.registry.View().Generation != metadata.Generations.Policy || s.inventory == nil || !s.inventory.Snapshot().Complete || s.inventory.Snapshot().Revision != metadata.Generations.Inventory {
			return ErrAdmissionStale
		}
		operationCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		previousCancel := record.cancel
		record.lifetime = operationCtx
		record.cancel = cancel
		if deadline, ok := operationCtx.Deadline(); ok {
			record.deadline = deadline
		}
		if previousCancel != nil {
			previousCancel()
		}
		op = &readOperation{id: record.ref.id, conversationID: metadata.ConversationID, gen: metadata.Generations, state: StatePrepared, cancel: cancel, done: make(chan struct{}), evidence: ReadEvidence{Case: record.request.CallID, Generations: metadata.Generations, Outcome: ReadNotExecuted}}
		record.state = admissionLaunching
		s.active[op.id] = op
		record.operation = op
		op.admission = record
		return nil
	}
	if err := authority.WithCurrent(start); err != nil {
		return err
	}
	if op == nil {
		return ErrAdmissionStale
	}
	if operationCtx == nil || cancel == nil {
		return ErrAdmissionStale
	}
	// The claim is spent even if launch now refuses.
	defer cancel()
	result := s.runWorkerMode(operationCtx, op, prepared, authority, false)
	cancel()
	s.finish(op, result)
	s.mu.Lock()
	joined, cleanupRequired := op.workerJoined, op.cleanupRequired
	s.mu.Unlock()
	if !joined && cleanupRequired {
		// Do not close the retained preparation while the worker may still own
		// its transferred descriptor. Keep the admission and cleanup obligation
		// reachable for a later bounded retirement retry.
		s.mu.Lock()
		if s.admissions[record.ref.id] == record {
			record.retired = true
			record.state = admissionCleanupPending
			s.cleanupBlocked = true
		}
		s.mu.Unlock()
		retired = true
	}
	if result.Outcome != ReadSucceeded || !joined {
		clear(result.Bytes)
		outcomeErr := fmt.Errorf("retained worker outcome %s; join confirmed=%t", result.Outcome, joined)
		if cleanupRequired && !joined {
			return errors.Join(ErrCleanupPending, outcomeErr)
		}
		return outcomeErr
	}
	if err := retire(); err != nil {
		clear(result.Bytes)
		return errors.Join(ErrCleanupPending, err)
	}
	completion := ReadCompletion{Result: result, WorkerJoined: joined, CapabilitiesRetired: true, ReadStarted: true}
	stage := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.cleanupBlocked || op.revoked || s.gen != metadata.Generations || s.registry.View().Generation != metadata.Generations.Policy || s.inventory == nil || !s.inventory.Snapshot().Complete || s.inventory.Snapshot().Revision != metadata.Generations.Inventory {
			return ErrAdmissionStale
		}
		return capture(metadata, completion)
	}
	if err := authority.WithCurrent(stage); err != nil {
		clear(result.Bytes)
		return err
	}
	clear(result.Bytes)
	return nil
}

func (s *fixtureSession) CloseAdmission(ctx context.Context, reference *AdmissionReference) error {
	if reference == nil || reference.owner != s || reference.id == "" {
		return ErrAdmissionStale
	}
	s.mu.Lock()
	record := s.admissions[reference.id]
	s.mu.Unlock()
	if record == nil {
		return nil
	}
	if record.ref != reference {
		return ErrAdmissionStale
	}
	return s.retireAdmission(ctx, record)
}

func (s *fixtureSession) InvalidateAdmissions(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	s.mu.Lock()
	records := s.invalidateAdmissionsLocked()
	s.mu.Unlock()
	return s.retireAdmissionRecords(ctx, records)
}

func (s *fixtureSession) InvalidateConversationReads(ctx context.Context, conversationID string) error {
	if ctx == nil || conversationID == "" {
		return context.Canceled
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	s.gen.Grant++
	workers := s.invalidateLocked(func(op *readOperation) bool { return op.conversationID == "" || op.conversationID == conversationID })
	admissions := make([]*admissionRecord, 0)
	for _, record := range s.admissions {
		if record.request.ConversationID == conversationID {
			record.retired = true
			record.state = admissionRetiring
			if record.cancel != nil {
				record.cancel()
			}
			admissions = append(admissions, record)
		}
	}
	s.mu.Unlock()
	return errors.Join(s.terminateAndCheck(workers), s.retireAdmissionRecords(ctx, admissions))
}

func (s *fixtureSession) AdmissionConversations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{}, len(s.admissions))
	for _, record := range s.admissions {
		if record.request.ConversationID != "" {
			seen[record.request.ConversationID] = struct{}{}
		}
	}
	conversations := make([]string, 0, len(seen))
	for conversation := range seen {
		conversations = append(conversations, conversation)
	}
	sort.Strings(conversations)
	return conversations
}

func (s *fixtureSession) invalidateAdmissionsLocked() []*admissionRecord {
	records := make([]*admissionRecord, 0, len(s.admissions))
	for _, record := range s.admissions {
		record.retired = true
		record.state = admissionRetiring
		if record.cancel != nil {
			record.cancel()
		}
		records = append(records, record)
	}
	return records
}

func (s *fixtureSession) retireAdmissionRecords(ctx context.Context, records []*admissionRecord) error {
	var errs []error
	for _, record := range records {
		if err := s.retireAdmission(ctx, record); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *fixtureSession) retireAdmission(ctx context.Context, record *admissionRecord) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	select {
	case record.retireLock <- struct{}{}:
	case <-ctx.Done():
		return errors.Join(ErrCleanupPending, ctx.Err())
	}
	defer func() { <-record.retireLock }()
	s.mu.Lock()
	if s.admissions[record.ref.id] != record {
		s.mu.Unlock()
		return nil
	}
	record.retired = true
	record.state = admissionRetiring
	lookupDone := record.lookupDone
	s.mu.Unlock()
	select {
	case <-lookupDone:
	case <-ctx.Done():
		s.mu.Lock()
		record.state = admissionCleanupPending
		s.mu.Unlock()
		return errors.Join(ErrCleanupPending, ctx.Err())
	}
	if record.prepared != nil {
		s.mu.Lock()
		workerPending := record.operation != nil && record.operation.cleanupRequired && !record.operation.workerJoined
		if workerPending {
			record.retired = true
			record.state = admissionCleanupPending
			s.cleanupBlocked = true
		}
		s.mu.Unlock()
		if workerPending {
			return ErrCleanupPending
		}
		if err := record.prepared.Close(); err != nil {
			record.cancel()
			s.mu.Lock()
			record.closeErr = errors.Join(record.closeErr, err)
			record.state = admissionCleanupPending
			s.cleanupBlocked = true
			s.mu.Unlock()
			return errors.Join(ErrCleanupPending, record.closeErr)
		}
		record.prepared = nil
	}
	if record.cancel != nil {
		record.cancel()
	}
	s.mu.Lock()
	if s.admissions[record.ref.id] == record {
		record.lifetime = nil
		record.cancel = nil
		delete(s.admissions, record.ref.id)
	}
	s.mu.Unlock()
	return nil
}

func (s *fixtureSession) admissionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.admissions)
}

func (s *fixtureSession) admissionState(reference *AdmissionReference) (string, bool) {
	if reference == nil || reference.owner != s {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.admissions[reference.id]
	if record == nil || record.ref != reference {
		return "", false
	}
	return fmt.Sprint(record.state), true
}
