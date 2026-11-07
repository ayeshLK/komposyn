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

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

const defaultBubblewrapPath = "/usr/bin/bwrap"

type linuxImplementation struct {
	mu              sync.Mutex
	ownerUpdateMu   sync.Mutex
	registry        *readbrokerstudy.Registry
	launcher        readbrokerstudy.WorkerLauncher
	session         readbrokerstudy.HostCoordinator
	inventory       *readbrokerstudy.CoverageInventory
	backendErr      error
	revision        uint64
	owners          map[string]OwnerCoverage
	blocked         map[string]string
	readInvalidator func(string)
	closed          bool
	pendingOwners   atomic.Int32
}

func newHostImplementation(bubblewrapPath string) (implementation, error) {
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, err
	}
	if bubblewrapPath == "" {
		bubblewrapPath = defaultBubblewrapPath
	}
	workerPath, workerErr := os.Executable()
	var launcher readbrokerstudy.WorkerLauncher
	if workerErr == nil {
		launcher, workerErr = readbrokerstudy.NewWorkerLauncher(bubblewrapPath, workerPath)
	}
	if workerErr != nil {
		return composeLinuxImplementation(registry, nil, fmt.Errorf("%w: %s", readbrokerstudy.ErrUnavailable, safeBackendDiagnostic(workerErr)))
	}
	return composeLinuxImplementation(registry, launcher, nil)
}

func composeLinuxImplementation(registry *readbrokerstudy.Registry, launcher readbrokerstudy.WorkerLauncher, backendErr error) (*linuxImplementation, error) {
	impl := &linuxImplementation{
		registry: registry, launcher: launcher, backendErr: backendErr, revision: 1,
		inventory: readbrokerstudy.NewChatCompositionInventory(),
		blocked:   make(map[string]string),
		owners: map[string]OwnerCoverage{
			auth.SensitiveOwnerAuth:     {Owner: auth.SensitiveOwnerAuth, Class: auth.SensitiveClassAuth, State: OwnerRequired, Evidence: "cmd/harness auth.Store wiring", Reason: "effective route and protected identity must be registered"},
			auth.SensitiveOwnerSettings: {Owner: auth.SensitiveOwnerSettings, Class: auth.SensitiveClassSettings, State: OwnerRequired, Evidence: "cmd/harness settings.Store wiring", Reason: "effective route and protected identity must be registered independently"},
			"tokenlogger-store":         {Owner: "tokenlogger-store", Class: "token_database", State: OwnerAbsent, Evidence: "trusted cmd/harness dependency graph", Reason: "this composition does not open or undertake a Token Logger Store; arbitrary databases in a selected root remain unknown"},
			"provider-source":           {Owner: "provider-source", Class: "provider_source", State: OwnerUnsupported, Evidence: "none", Reason: "no source identity/open lifecycle is registered by this chat composition"},
			"unknown-root-data":         {Owner: "unknown-root-data", Class: "unclassified", State: OwnerUnsupported, Evidence: "none", Reason: ErrUnknownRootData.Error()},
		},
	}
	if launcher == nil {
		if impl.backendErr == nil {
			impl.backendErr = readbrokerstudy.ErrUnavailable
		}
		return impl, nil
	}
	var err error
	impl.session, err = readbrokerstudy.NewHostCoordinator(registry, readbrokerstudy.NewBroker(registry), launcher, hostAuthorizer{}, impl.inventory)
	if err == nil {
		for _, requirement := range readbrokerstudy.ChatCompositionRequirements() {
			if err = impl.session.DeclareCoverageOwner(requirement); err != nil {
				break
			}
		}
	}
	if err != nil {
		if impl.session != nil {
			_ = impl.session.Close(context.Background())
		} else {
			_ = launcher.Close()
		}
		return nil, fmt.Errorf("%w: trusted worker service could not be composed", readbrokerstudy.ErrUnavailable)
	}
	return impl, nil
}

type hostAuthorizer struct{}

func (hostAuthorizer) AuthorizeAndConsume(context.Context, readbrokerstudy.AuthorizationRequest) (readbrokerstudy.AuthorizationReceipt, error) {
	return readbrokerstudy.AuthorizationReceipt{}, ErrUnknownRootData
}

func safeBackendDiagnostic(err error) string {
	if errors.Is(err, readbrokerstudy.ErrUnavailable) {
		return "trusted Bubblewrap, required namespaces, compatibility features, or fixed-worker checks are unavailable"
	}
	return "sandbox backend preparation failed"
}

func (s *linuxImplementation) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if ctx == nil || ctx.Err() != nil {
		if ctx == nil {
			return context.Canceled
		}
		return ctx.Err()
	}
	if s == nil {
		return ErrServiceClosed
	}
	s.ownerUpdateMu.Lock()
	defer s.ownerUpdateMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrServiceClosed
	}
	coverage, exists := s.owners[owner]
	retryBlocked := s.blocked[owner] != "" && coverage.State == OwnerPending
	if !exists || coverage.State != OwnerRequired && coverage.State != OwnerRegistered && !retryBlocked {
		s.mu.Unlock()
		return errors.New("owner is not required by the trusted chat composition")
	}
	resourceClass, ok := hostResourceClass(class)
	if !ok || class != coverage.Class {
		s.mu.Unlock()
		return errors.New("protected owner class does not match the trusted chat composition")
	}
	s.mu.Unlock()
	cleanRoute, err := filepath.Abs(route)
	if err != nil || !filepath.IsAbs(cleanRoute) || filepath.Clean(cleanRoute) != cleanRoute {
		return errors.New("protected owner route is invalid")
	}
	registrations := []readbrokerstudy.OwnerRegistration{{Owner: owner, Class: resourceClass, Path: cleanRoute}}
	var expected readbrokerstudy.FileIdentity
	var objectPath string
	if object != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		objectPath, err = filepath.Abs(object.Name())
		if err != nil || filepath.Clean(objectPath) != objectPath {
			return errors.New("protected replacement object path is invalid")
		}
		info, err := object.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("protected replacement object must be a regular file")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("protected replacement identity is unavailable")
		}
		expected = readbrokerstudy.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Type: uint32(stat.Mode & syscall.S_IFMT)}
		registrations = append(registrations, readbrokerstudy.OwnerRegistration{Owner: owner, Class: resourceClass, Path: objectPath})
	}
	// A route or replacement registration is incomplete until the opened object
	// has been checked against the identity pinned by the registry. Keep a
	// service-level pending latch because the coordinator may finish its own
	// update before this final owner-descriptor check completes.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrServiceClosed
	}
	coverage, exists = s.owners[owner]
	if !exists || coverage.Class != class || coverage.State != OwnerRequired && coverage.State != OwnerRegistered && !(s.blocked[owner] != "" && coverage.State == OwnerPending) {
		s.mu.Unlock()
		return errors.New("owner state changed before protected registration")
	}
	if s.blocked[owner] == "" {
		s.pendingOwners.Add(1)
	}
	s.blocked[owner] = "owner registration awaits cleanup, identity validation and lifecycle completion"
	s.revision++
	session, registry := s.session, s.registry
	s.mu.Unlock()
	if session != nil {
		for _, conversation := range session.AdmissionConversations() {
			s.invalidateConversation(conversation)
		}
		err = session.InvalidateAdmissions(ctx)
		if err == nil {
			err = session.RegisterRotationContext(ctx, owner, registrations)
		}
	} else {
		err = registry.RegisterRotation(owner, registrations)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	view := registry.View()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.blocked[owner] == "" {
		return ErrServiceClosed
	}
	view = registry.View()
	if object != nil {
		found := false
		for _, entry := range view.Entries {
			if entry.Owner == owner && entry.Class == resourceClass && entry.Path == objectPath && entry.Identity == expected {
				found = true
				break
			}
		}
		if !found {
			return errors.New("pinned replacement identity does not match the open owner descriptor")
		}
	}
	delete(s.blocked, owner)
	s.pendingOwners.Add(-1)
	coverage.State = OwnerRegistered
	coverage.Reason = "effective route and observed protected identities are retained until service shutdown"
	coverage.Routes = 0
	coverage.Identities = 0
	for _, entry := range view.Entries {
		if entry.Owner == owner && entry.Class == resourceClass && !entry.Directory {
			coverage.Routes++
			if entry.Identity.Inode != 0 {
				coverage.Identities++
			}
		}
	}
	s.owners[owner] = coverage
	s.revision++
	return nil
}

func (s *linuxImplementation) ExecuteAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, preparation runtime.FilesystemReadPreparation, reference *runtime.ReadExecutionReference, authority runtime.FilesystemReadExecutionAuthority) (*runtime.PrivateReadCapture, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	reject := func(err error) (*runtime.PrivateReadCapture, error) {
		if preparation != nil {
			err = errors.Join(err, preparation.Close(ctx))
		}
		return nil, err
	}
	if preparation == nil || reference == nil || authority == nil {
		return reject(readbrokerstudy.ErrAdmissionStale)
	}
	s.mu.Lock()
	if s.closed || s.session == nil || s.launcher == nil || s.backendErr != nil || len(s.blocked) != 0 || s.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || s.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		s.mu.Unlock()
		return reject(readbrokerstudy.ErrUnavailable)
	}
	session := s.session
	s.mu.Unlock()
	prepared, ok := preparation.(*linuxAdmissionPreparation)
	if !ok || prepared == nil || prepared.impl != s || prepared.inner == nil {
		return reject(readbrokerstudy.ErrAdmissionStale)
	}
	executor, ok := session.(readbrokerstudy.RetainedReadExecutor)
	if !ok {
		return reject(readbrokerstudy.ErrUnavailable)
	}
	var capture *runtime.PrivateReadCapture
	var err error
	err = executor.ExecuteAdmitted(ctx, prepared.inner, authority, func(metadata readbrokerstudy.AdmissionMetadata, completion readbrokerstudy.ReadCompletion) error {
		// Called under runtime authority and coordinator generation gates only
		// after worker join and host descriptor retirement. Avoid taking s.mu
		// here: owner/root transitions latch their pending state before entering
		// the coordinator, and pendingOwners/root state is checked separately.
		if s.pendingOwners.Load() != 0 {
			return ErrCoverageMissing
		}
		outcome := mapReadOutcome(completion.Result.Outcome)
		if completion.Result.Truncated && outcome == tools.OutcomeSucceeded {
			outcome = tools.OutcomePartial
		}
		result := runtime.FilesystemReadExecutionResult{
			Outcome: outcome, Bytes: completion.Result.Bytes,
			ServiceID: metadata.ServiceID, AdmissionBinding: fmt.Sprintf("%x", metadata.Binding[:]),
			AuthorizationBinding: authority.AuthorizationBinding(), Identity: fmt.Sprintf("%d:%d:%d", metadata.Identity.Device, metadata.Identity.Inode, metadata.Identity.Type),
			RootGeneration: metadata.RootGeneration, PolicyRevision: metadata.Generations.Policy,
			CoverageRevision: metadata.Generations.Inventory, Destination: metadata.Destination,
			ReadStarted: completion.ReadStarted, WorkerJoined: completion.WorkerJoined,
			CapabilitiesRetired: completion.CapabilitiesRetired,
		}
		capture, err = authority.Stage(admission, result)
		return err
	})
	return capture, err
}

// WithFilesystemReadExecutionCurrent holds the Linux implementation owner
// state lock across the caller's bounded release or capture callback. Service
// applies its root gate before calling this method, preserving root->owner
// ordering without exposing implementation state on other platforms.
func (s *linuxImplementation) WithFilesystemReadExecutionCurrent(operation func() error) error {
	if s == nil || operation == nil {
		return ErrCoverageMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pendingOwners.Load() != 0 || len(s.blocked) != 0 || s.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || s.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		return ErrCoverageMissing
	}
	return operation()
}

func mapReadOutcome(outcome readbrokerstudy.ReadOutcome) tools.Outcome {
	switch outcome {
	case readbrokerstudy.ReadSucceeded:
		return tools.OutcomeSucceeded
	case readbrokerstudy.ReadFailed:
		return tools.OutcomeFailed
	case readbrokerstudy.ReadPartial:
		return tools.OutcomePartial
	case readbrokerstudy.ReadUnknown:
		return tools.OutcomeUnknown
	case readbrokerstudy.ReadCancelled:
		return tools.OutcomeCancelled
	default:
		return tools.OutcomeNotExecuted
	}
}

func hostResourceClass(class string) (readbrokerstudy.ResourceClass, bool) {
	switch class {
	case auth.SensitiveClassAuth:
		return readbrokerstudy.ClassCredential, true
	case auth.SensitiveClassSettings:
		return readbrokerstudy.ClassSettings, true
	default:
		return "", false
	}
}

func (s *linuxImplementation) SetFilesystemReadInvalidator(invalidate func(string)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.readInvalidator = invalidate
	s.mu.Unlock()
}

func (s *linuxImplementation) invalidateConversation(conversation string) {
	s.mu.Lock()
	callback := s.readInvalidator
	s.mu.Unlock()
	if callback != nil && conversation != "" {
		callback(conversation)
	}
}

func (s *linuxImplementation) InvalidateFilesystemAdmissions(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrServiceClosed
	}
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	for _, conversation := range session.AdmissionConversations() {
		s.invalidateConversation(conversation)
	}
	return session.InvalidateAdmissions(ctx)
}

func (s *linuxImplementation) InvalidateFilesystemReadExecutions(conversationID string) error {
	if conversationID == "" {
		return readbrokerstudy.ErrAdmissionStale
	}
	s.mu.Lock()
	if s.closed || s.session == nil {
		s.mu.Unlock()
		return ErrServiceClosed
	}
	session := s.session
	s.mu.Unlock()
	invalidator, ok := session.(readbrokerstudy.ConversationReadInvalidator)
	if !ok {
		return readbrokerstudy.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return invalidator.InvalidateConversationReads(ctx, conversationID)
}

func (s *linuxImplementation) SelectFilesystemRoot(ctx context.Context, transition runtime.FilesystemRootTransition) error {
	if ctx == nil {
		return context.Canceled
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrServiceClosed
	}
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	for _, conversation := range session.AdmissionConversations() {
		s.invalidateConversation(conversation)
	}
	return session.SelectRootGeneration(ctx, transition.Root, transition.Generation)
}

func (s *linuxImplementation) AdmitFilesystemRead(ctx context.Context, invocation tools.Invocation, destination, root string, rootGeneration, coverageRevision uint64) (runtime.FilesystemReadAdmission, error) {
	if ctx == nil || invocation.ToolID != tools.ID("read_file") || invocation.ConversationID == "" || invocation.TurnID == "" || invocation.ProviderCallID == "" || destination == "" {
		return runtime.FilesystemReadAdmission{}, readbrokerstudy.ErrAdmissionStale
	}
	target, err := exactReadTarget(invocation.Arguments)
	if err != nil {
		return runtime.FilesystemReadAdmission{}, err
	}
	s.mu.Lock()
	if s.closed || s.session == nil || s.backendErr != nil || s.launcher == nil {
		s.mu.Unlock()
		return runtime.FilesystemReadAdmission{}, readbrokerstudy.ErrUnavailable
	}
	if len(s.blocked) != 0 || s.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || s.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		s.mu.Unlock()
		return runtime.FilesystemReadAdmission{}, ErrCoverageMissing
	}
	session := s.session
	request := readbrokerstudy.AdmissionRequest{
		ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, CallID: invocation.ProviderCallID,
		Root: root, Target: target, RootGeneration: rootGeneration, Destination: destination,
	}
	reference, err := session.BeginAdmission(ctx, request)
	s.mu.Unlock()
	if err != nil {
		return runtime.FilesystemReadAdmission{}, err
	}
	metadata, err := session.WaitAdmission(ctx, reference)
	if err != nil {
		return runtime.FilesystemReadAdmission{}, errors.Join(err, session.CloseAdmission(ctx, reference))
	}
	s.mu.Lock()
	stillEligible := !s.closed && len(s.blocked) == 0 && s.session == session && s.owners[auth.SensitiveOwnerAuth].State == OwnerRegistered && s.owners[auth.SensitiveOwnerSettings].State == OwnerRegistered
	s.mu.Unlock()
	if !stillEligible || coverageRevision != s.CoverageSnapshot().Revision {
		return runtime.FilesystemReadAdmission{}, errors.Join(readbrokerstudy.ErrAdmissionStale, session.CloseAdmission(ctx, reference))
	}
	if metadata.Class == readbrokerstudy.ClassProvider {
		return runtime.FilesystemReadAdmission{}, errors.Join(ErrCoverageMissing, session.CloseAdmission(ctx, reference))
	}
	return runtimeAdmission(metadata), nil
}

func (s *linuxImplementation) PrepareAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, expectedRevision uint64) (runtime.FilesystemReadPreparation, error) {
	if ctx == nil || expectedRevision == 0 {
		return nil, readbrokerstudy.ErrAdmissionStale
	}
	s.mu.Lock()
	if s.closed || s.session == nil || s.backendErr != nil || s.launcher == nil || len(s.blocked) != 0 {
		s.mu.Unlock()
		return nil, readbrokerstudy.ErrAdmissionStale
	}
	session := s.session
	reference, ok := admission.Reference.(*readbrokerstudy.AdmissionReference)
	if !ok || reference == nil || admission.ServiceID == "" || admission.PolicyRevision == 0 {
		s.mu.Unlock()
		return nil, readbrokerstudy.ErrAdmissionStale
	}
	prepared, err := session.PrepareAdmitted(ctx, reference, admission.AdmissionBinding, admission.PolicyRevision)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	metadata := prepared.Metadata()
	if metadata.ServiceID != admission.ServiceID || metadata.ConversationID != admission.ConversationID || metadata.TurnID == "" || metadata.CallID == "" || metadata.Root != admission.Root || metadata.Target != admission.Target || !metadata.Deadline.Equal(admission.Deadline) || metadata.Destination != admission.Destination {
		return nil, errors.Join(readbrokerstudy.ErrAdmissionStale, prepared.Close(ctx))
	}
	return &linuxAdmissionPreparation{impl: s, inner: prepared}, nil
}

func (s *linuxImplementation) CloseFilesystemReadAdmission(ctx context.Context, admission runtime.FilesystemReadAdmission) error {
	if ctx == nil {
		return context.Canceled
	}
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return readbrokerstudy.ErrSessionClosed
	}
	reference, ok := admission.Reference.(*readbrokerstudy.AdmissionReference)
	if !ok || reference == nil {
		return readbrokerstudy.ErrAdmissionStale
	}
	return session.CloseAdmission(ctx, reference)
}

type linuxAdmissionPreparation struct {
	impl  *linuxImplementation
	inner readbrokerstudy.AdmittedPreparation
}

func runtimeAdmission(metadata readbrokerstudy.AdmissionMetadata) runtime.FilesystemReadAdmission {
	return runtime.FilesystemReadAdmission{
		Reference: metadata.Reference, ServiceID: metadata.ServiceID, ConversationID: metadata.ConversationID, TurnID: metadata.TurnID, CallID: metadata.CallID,
		Root: metadata.Root, Deadline: metadata.Deadline, AdmissionBinding: fmt.Sprintf("%x", metadata.Binding[:]), Target: metadata.Target,
		Class: string(metadata.Class), Identity: fmt.Sprintf("%d:%d:%d", metadata.Identity.Device, metadata.Identity.Inode, metadata.Identity.Type),
		RootGeneration: metadata.RootGeneration, PolicyRevision: metadata.Generations.Policy,
		CoverageRevision: metadata.Generations.Inventory, Destination: metadata.Destination,
	}
}

func (p *linuxAdmissionPreparation) Metadata() runtime.FilesystemReadAdmission {
	if p == nil || p.inner == nil {
		return runtime.FilesystemReadAdmission{}
	}
	return runtimeAdmission(p.inner.Metadata())
}

func (p *linuxAdmissionPreparation) WithAuthorityCommit(ctx context.Context, commit func() error) error {
	if p == nil || p.impl == nil || p.inner == nil || ctx == nil {
		return readbrokerstudy.ErrAdmissionStale
	}
	p.impl.mu.Lock()
	defer p.impl.mu.Unlock()
	if p.impl.closed || p.impl.session == nil || len(p.impl.blocked) != 0 {
		return readbrokerstudy.ErrAdmissionStale
	}
	if p.impl.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || p.impl.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		return ErrCoverageMissing
	}
	return p.inner.WithCommit(ctx, commit)
}

func (p *linuxAdmissionPreparation) Close(ctx context.Context) error {
	if p == nil || p.inner == nil {
		return nil
	}
	return p.inner.Close(ctx)
}

func (s *linuxImplementation) CoverageSnapshot() CoverageSnapshot {
	if s == nil {
		return CoverageSnapshot{UnknownRootDataGate: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		study := s.session.CoverageSnapshot()
		if study.Revision > s.revision {
			s.revision = study.Revision
		}
		for _, owner := range study.Requirements {
			coverage, exists := s.owners[owner.Owner]
			if !exists {
				continue
			}
			coverage.Routes = owner.RegisteredRoutes
			coverage.Identities = owner.PinnedIdentities
			if owner.Retired {
				coverage.State = OwnerRetired
			} else if owner.PendingUpdate {
				coverage.State = OwnerPending
			} else if owner.Complete {
				coverage.State = OwnerRegistered
			} else if owner.RegisteredRoutes > 0 {
				coverage.State = OwnerStale
			} else {
				coverage.State = OwnerRequired
			}
			coverage.Reason = owner.UnavailableReason
			s.owners[owner.Owner] = coverage
		}
	}
	for owner, reason := range s.blocked {
		coverage, exists := s.owners[owner]
		if !exists {
			continue
		}
		coverage.State = OwnerPending
		coverage.Reason = reason
		s.owners[owner] = coverage
	}
	owners := make([]OwnerCoverage, 0, len(s.owners))
	for _, owner := range s.owners {
		owners = append(owners, owner)
	}
	sortOwnerCoverage(owners)
	return CoverageSnapshot{Revision: s.revision, Composition: "komposyn-memory-chat", Capability: "linux-exact-file-typed-read", UnknownRootDataGate: true, Owners: owners}
}

func (s *linuxImplementation) BackendStatus() (bool, string) {
	if s == nil {
		return false, ErrServiceClosed.Error()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrServiceClosed.Error()
	}
	if s.backendErr != nil {
		return false, s.backendErr.Error()
	}
	if s.launcher == nil {
		return false, readbrokerstudy.ErrUnavailable.Error()
	}
	return true, "trusted Bubblewrap and fixed worker capabilities were inspected; no worker has been launched"
}

func (s *linuxImplementation) PrepareGenericRead(ctx context.Context, _, _ string) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceClosed
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrServiceClosed
	}
	// This host profile deliberately has no way to turn arbitrary user-root
	// contents into covered data. No path, approval, fixture flag, or caller
	// assertion can reopen this gate.
	return ErrUnknownRootData
}

func (s *linuxImplementation) Close(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	s.mu.Lock()
	s.closed = true
	session, launcher, registry := s.session, s.launcher, s.registry
	s.mu.Unlock()
	if session != nil {
		err := session.Close(ctx)
		if errors.Is(err, readbrokerstudy.ErrCleanupPending) || ctx.Err() != nil {
			// Keep the coordinator (and its pins/cleanup obligations) reachable
			// so shutdown can be retried after an incomplete join.
			return errors.Join(err, ctx.Err())
		}
		s.mu.Lock()
		if s.session == session {
			s.session, s.launcher, s.registry = nil, nil, nil
		}
		s.mu.Unlock()
		return err
	}
	var closeErr error
	if launcher != nil {
		closeErr = errors.Join(closeErr, launcher.Close())
	}
	if registry != nil {
		closeErr = errors.Join(closeErr, registry.Close())
	}
	s.mu.Lock()
	if s.launcher == launcher {
		s.launcher = nil
	}
	if s.registry == registry {
		s.registry = nil
	}
	s.mu.Unlock()
	return closeErr
}

func sortOwnerCoverage(owners []OwnerCoverage) {
	sort.Slice(owners, func(i, j int) bool { return owners[i].Owner < owners[j].Owner })
}

var _ implementation = (*linuxImplementation)(nil)
