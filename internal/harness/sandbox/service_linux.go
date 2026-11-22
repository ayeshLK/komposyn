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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"golang.org/x/sys/unix"
)

const defaultBubblewrapPath = "/usr/bin/bwrap"

type linuxImplementation struct {
	mu                      sync.Mutex
	ownerUpdateMu           sync.Mutex
	registry                *readbrokerstudy.Registry
	launcher                readbrokerstudy.WorkerLauncher
	session                 readbrokerstudy.HostCoordinator
	inventory               *readbrokerstudy.CoverageInventory
	backendErr              error
	listingOptionsAvailable bool
	searchProfileAvailable  bool
	revision                uint64
	owners                  map[string]OwnerCoverage
	ownerUpdating           map[string]bool
	blocked                 map[string]string
	readInvalidator         func(string)
	closed                  bool
	pendingOwners           atomic.Int32
	creationBarrier         *creationBarrierLease
	creationBarrierSequence uint64
	creationManifest        *syntheticCreationOwnerManifest
	creationProposals       map[string]*serviceCreationProposal
	// creationBeforePublicationReleaseHook is a deterministic in-package test checkpoint.
	creationBeforePublicationReleaseHook func()
	selectedRoot                         string
	selectedConversation                 string
	selectedRootGeneration               uint64
}

type creationRootBinding struct {
	root, conversation string
	generation         uint64
}

type serviceCreationProposal struct {
	invocation       tools.Invocation
	request          permissions.Request
	input            tools.CreateFileInput
	parent           *os.File
	parentIdentity   readbrokerstudy.FileIdentity
	root             string
	rootGeneration   uint64
	coverageRevision uint64
	policyRevision   uint64
	stageName        string
	consumed         bool
}

// creationBarrierLease is an in-process, one-use token for the transient
// service-wide read gate. It is deliberately separate from owner registrations
// and their policy/coverage generations.
type creationBarrierLease struct {
	service             *linuxImplementation
	sequence            uint64
	state               creationBarrierState
	stageEntryAdmitted  bool
	publicationReleased bool
	worker              readbrokerstudy.SandboxWorker
	stage               readbrokerstudy.CreationStage
	ctx                 context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	doneOnce            sync.Once
}

type creationBarrierState uint8

type syntheticCreationOwnerManifest struct {
	scopeParent       readbrokerstudy.FileIdentity
	scopeComplete     bool
	policyRevision    uint64
	coverageRevision  uint64
	tokenDatabasePath string
	owners            map[string]syntheticCreationOwner
}

type syntheticCreationOwner struct {
	class         readbrokerstudy.ResourceClass
	registrations map[string]readbrokerstudy.FileIdentity
}

const (
	creationBarrierClosing creationBarrierState = iota + 1
	creationBarrierActive
	creationBarrierBlocked
)

const creationWorkerJoinTimeout = 2 * time.Second

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
		inventory:         readbrokerstudy.NewChatCompositionInventory(),
		creationProposals: make(map[string]*serviceCreationProposal),
		blocked:           make(map[string]string),
		ownerUpdating:     make(map[string]bool),
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
	profile, ok := launcher.(interface{ AdvancedDirectoryListingProfileAvailable() bool })
	impl.listingOptionsAvailable = ok && profile.AdvancedDirectoryListingProfileAvailable()
	searchProfile, ok := launcher.(interface{ SearchProfilesAvailable() bool })
	impl.searchProfileAvailable = ok && searchProfile.SearchProfilesAvailable()
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
		return ErrServiceClosed
	}
	coverage, exists := s.owners[owner]
	retryBlocked := s.blocked[owner] != "" && coverage.State == OwnerPending
	if s.ownerUpdating[owner] || !exists || coverage.State != OwnerRequired && coverage.State != OwnerRegistered && !retryBlocked {
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
		return errors.New("owner is not required by the trusted chat composition")
	}
	resourceClass, ok := hostResourceClass(class)
	if !ok || class != coverage.Class {
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
		return errors.New("protected owner class does not match the trusted chat composition")
	}
	s.mu.Unlock()
	s.ownerUpdateMu.Unlock()
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
	s.ownerUpdateMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
		return ErrServiceClosed
	}
	coverage, exists = s.owners[owner]
	retryBlocked = s.blocked[owner] != "" && coverage.State == OwnerPending
	if s.ownerUpdating[owner] || !exists || coverage.Class != class || coverage.State != OwnerRequired && coverage.State != OwnerRegistered && !retryBlocked {
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
		return errors.New("owner state changed before protected registration")
	}
	if s.ownerUpdating == nil {
		s.ownerUpdating = make(map[string]bool)
	}
	s.ownerUpdating[owner] = true
	if s.blocked[owner] == "" {
		s.pendingOwners.Add(1)
	}
	s.blocked[owner] = "owner registration awaits cleanup, identity validation and lifecycle completion"
	var effectDone <-chan struct{}
	if effect := s.creationBarrier; effect != nil {
		if effect.cancel != nil {
			effect.cancel()
		}
		effectDone = effect.done
	}
	s.revision++
	session, registry := s.session, s.registry
	s.mu.Unlock()
	s.ownerUpdateMu.Unlock()
	defer func() {
		s.ownerUpdateMu.Lock()
		s.mu.Lock()
		delete(s.ownerUpdating, owner)
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
	}()
	// Invalidate runtime captures and continuations globally, not only
	// conversations with still-open admissions. A completed capture may have
	// already retired its host admission while remaining eligible for handoff.
	s.invalidateConversation("")
	if session != nil {
		err = session.InvalidateAdmissions(ctx)
	} else {
		err = registry.RegisterRotation(owner, registrations)
	}
	if err == nil && effectDone != nil {
		select {
		case <-effectDone:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil && session != nil {
		err = session.RegisterRotationContext(ctx, owner, registrations)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	view := registry.View()
	s.ownerUpdateMu.Lock()
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		s.ownerUpdateMu.Unlock()
	}()
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
	if admission.Operation == string(tools.SearchFilesID) {
		// The search host reads only objects filtered and classified by the
		// retained directory capability. Serialize owner replacement with that
		// content read and publication window.
		s.ownerUpdateMu.Lock()
		defer s.ownerUpdateMu.Unlock()
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
	if admission.Operation == string(tools.SearchFilesID) && errors.Is(err, readbrokerstudy.ErrExecutionFailed) && !errors.Is(err, readbrokerstudy.ErrCleanupPending) {
		return nil, runtime.ErrFilesystemReadFailed
	}
	return capture, err
}

// WithFilesystemReadPublicationCurrent serializes the commit/handoff with
// every Service.Protect owner rotation. ownerUpdateMu spans the mutation's
// pending latch, registry transition, invalidation callbacks, and final state
// update; no callback can observe an intermediate owner snapshot. The callback
// must not perform provider I/O.
func (s *linuxImplementation) WithFilesystemReadPublicationCurrent(policyRevision, coverageRevision uint64, operation func() error) error {
	if s == nil || operation == nil {
		return ErrCoverageMissing
	}
	if !s.ownerUpdateMu.TryLock() {
		return ErrCoverageMissing
	}
	defer s.ownerUpdateMu.Unlock()
	s.mu.Lock()
	if s.registry == nil || s.inventory == nil {
		s.mu.Unlock()
		return ErrCoverageMissing
	}
	view := s.registry.View()
	coverage := s.inventory.Snapshot()
	current := !s.closed && s.pendingOwners.Load() == 0 && len(s.blocked) == 0 && s.owners[auth.SensitiveOwnerAuth].State == OwnerRegistered && s.owners[auth.SensitiveOwnerSettings].State == OwnerRegistered && view.Generation == policyRevision && coverage.Complete && coverage.Revision == coverageRevision
	s.mu.Unlock()
	if !current {
		return ErrCoverageMissing
	}
	return operation()
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
	if callback != nil {
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
	if s.closed {
		s.mu.Unlock()
		return ErrServiceClosed
	}
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return nil // no backend session means there can be no retained worker/read
	}
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
	s.selectedRoot, s.selectedConversation, s.selectedRootGeneration = transition.Root, transition.ConversationID, transition.Generation
	lease := s.creationBarrier
	if lease != nil && lease.cancel != nil {
		lease.cancel()
	}
	session := s.session
	s.mu.Unlock()
	if lease != nil {
		select {
		case <-lease.done:
		case <-ctx.Done():
			return errors.Join(readbrokerstudy.ErrCleanupPending, ctx.Err())
		}
	}
	if session == nil {
		return nil
	}
	for _, conversation := range session.AdmissionConversations() {
		s.invalidateConversation(conversation)
	}
	return session.SelectRootGeneration(ctx, transition.Root, transition.Generation)
}

func (s *linuxImplementation) AdmitFilesystemRead(ctx context.Context, invocation tools.Invocation, destination, root string, rootGeneration, coverageRevision uint64) (runtime.FilesystemReadAdmission, error) {
	if ctx == nil || (invocation.ToolID != tools.ReadFileID && invocation.ToolID != tools.ListFilesID && invocation.ToolID != tools.SearchFilesID) || invocation.ConversationID == "" || invocation.TurnID == "" || invocation.ProviderCallID == "" || destination == "" {
		return runtime.FilesystemReadAdmission{}, readbrokerstudy.ErrAdmissionStale
	}
	var target string
	var listingOptions readbrokerstudy.ListingOptions
	var searchInput tools.SearchFilesInput
	var err error
	if invocation.ToolID == tools.ListFilesID {
		listing, parseErr := tools.ParseListFiles(invocation.Arguments)
		err = parseErr
		target = listing.Path
		listingOptions = readbrokerstudy.ListingOptions{Recursive: listing.Recursive, IncludeHidden: listing.IncludeHidden, IncludeIgnored: listing.IncludeIgnored, NameGlobs: append([]string(nil), listing.NameGlobs...)}
	} else if invocation.ToolID == tools.SearchFilesID {
		searchInput, err = tools.ParseSearchFiles(invocation.Arguments)
		target = searchInput.Path
		listingOptions = readbrokerstudy.ListingOptions{Recursive: searchInput.Recursive, IncludeHidden: searchInput.IncludeHidden, IncludeIgnored: searchInput.IncludeIgnored, NameGlobs: append([]string(nil), searchInput.NameGlobs...)}
		if err == nil && !s.SearchFilesAvailable() {
			err = readbrokerstudy.ErrUnavailable
		}
	} else {
		target, err = exactReadTarget(invocation.Arguments)
	}
	if err != nil {
		return runtime.FilesystemReadAdmission{}, err
	}
	if invocation.ToolID == tools.ListFilesID && !listingOptions.IsZero() {
		optionsHost, ok := any(s).(runtime.FilesystemDirectoryListingOptionsAvailability)
		if !ok || !optionsHost.DirectoryListingOptionsAvailable() {
			return runtime.FilesystemReadAdmission{}, readbrokerstudy.ErrUnavailable
		}
	}
	s.mu.Lock()
	if s.creationBarrier != nil {
		s.mu.Unlock()
		return runtime.FilesystemReadAdmission{}, ErrCreationBarrier
	}
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
		Operation:      readbrokerstudy.AdmissionOperation(invocation.ToolID),
		ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, CallID: invocation.ProviderCallID,
		Root: root, Target: target, RootGeneration: rootGeneration, Destination: destination,
		Listing: listingOptions, SearchQuery: searchInput.Query,
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
	stillEligible := !s.closed && s.creationBarrier == nil && len(s.blocked) == 0 && s.session == session && s.owners[auth.SensitiveOwnerAuth].State == OwnerRegistered && s.owners[auth.SensitiveOwnerSettings].State == OwnerRegistered
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
	if s.creationBarrier != nil || s.closed || s.session == nil || s.backendErr != nil || s.launcher == nil || len(s.blocked) != 0 {
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
	if metadata.ServiceID != admission.ServiceID || metadata.ConversationID != admission.ConversationID || metadata.TurnID == "" || metadata.CallID == "" || metadata.Root != admission.Root || metadata.Target != admission.Target || !metadata.Deadline.Equal(admission.Deadline) || metadata.Destination != admission.Destination || metadata.Listing.Recursive != admission.Listing.Recursive || metadata.Listing.IncludeHidden != admission.Listing.IncludeHidden || metadata.Listing.IncludeIgnored != admission.Listing.IncludeIgnored || !slices.Equal(metadata.Listing.NameGlobs, admission.Listing.NameGlobs) {
		return nil, errors.Join(readbrokerstudy.ErrAdmissionStale, prepared.Close(ctx))
	}
	return &linuxAdmissionPreparation{impl: s, inner: prepared}, nil
}

// beginCreationBarrier closes admission before invalidating existing work. The
// state lock is released before runtime callbacks and coordinator draining.
// On any drain failure the token remains installed in the blocked state so a
// caller cannot accidentally reopen reads while cleanup is uncertain.
func (s *linuxImplementation) beginCreationBarrier(ctx context.Context, rootBinding ...creationRootBinding) (*creationBarrierLease, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrServiceClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServiceClosed
	}
	if s.session == nil || s.backendErr != nil || s.launcher == nil {
		s.mu.Unlock()
		return nil, readbrokerstudy.ErrUnavailable
	}
	if s.creationBarrier != nil {
		s.mu.Unlock()
		return nil, ErrCreationBarrier
	}
	if len(rootBinding) > 0 && (len(rootBinding) != 1 || s.selectedRoot != rootBinding[0].root || s.selectedConversation != rootBinding[0].conversation || s.selectedRootGeneration != rootBinding[0].generation) {
		s.mu.Unlock()
		return nil, runtime.ErrFilesystemRootStale
	}
	if len(s.blocked) != 0 || s.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || s.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		s.mu.Unlock()
		return nil, ErrCoverageMissing
	}
	s.creationBarrierSequence++
	effectContext, cancel := context.WithCancel(ctx)
	lease := &creationBarrierLease{service: s, sequence: s.creationBarrierSequence, state: creationBarrierClosing, ctx: effectContext, cancel: cancel, done: make(chan struct{})}
	s.creationBarrier = lease
	session := s.session
	s.mu.Unlock()

	// A creation barrier only closes host admission and drains retained workers.
	// Calling the runtime read invalidator here would advance its authority epoch
	// and cancel the conversation that owns this create call, invalidating the
	// create result along with any immutable read captures already completed.
	// Trust, root and owner transitions keep using the stronger invalidation path.
	invalidateErr := session.InvalidateAdmissions(ctx)
	drainErr := invalidateErr
	// Retiring an admission can report cleanup-pending while its worker is
	// still active. Drain those workers anyway, then retry admission retirement
	// so the barrier can proceed only after both obligations are confirmed.
	if invalidateErr == nil || errors.Is(invalidateErr, readbrokerstudy.ErrCleanupPending) {
		drainer, ok := session.(readbrokerstudy.ActiveReadDrainer)
		if !ok {
			drainErr = readbrokerstudy.ErrUnavailable
		} else if drainErr = drainer.DrainActiveReads(ctx); drainErr == nil && invalidateErr != nil {
			drainErr = session.InvalidateAdmissions(ctx)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creationBarrier != lease || lease.service != s {
		return lease, ErrServiceClosed
	}
	if drainErr != nil || s.closed || len(s.blocked) != 0 {
		lease.state = creationBarrierBlocked
		if drainErr != nil {
			return lease, drainErr
		}
		if s.closed {
			return lease, ErrServiceClosed
		}
		return lease, ErrCoverageMissing
	}
	lease.state = creationBarrierActive
	return lease, nil
}

// finishCreationBarrier reopens reads only after the caller confirms its
// effect worker and stage disposition are complete. An incomplete cleanup
// deliberately retains the gate and token for host-owned recovery.
func (s *linuxImplementation) finishCreationBarrier(lease *creationBarrierLease, cleanupComplete bool) error {
	if s == nil || lease == nil || lease.service != s {
		return ErrCreationBarrier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creationBarrier != lease || lease.state == creationBarrierClosing {
		return ErrCreationBarrier
	}
	if !cleanupComplete {
		lease.state = creationBarrierBlocked
		return ErrCreationBarrier
	}
	if s.session == nil || s.session.ActiveOperationCount() != 0 {
		lease.state = creationBarrierBlocked
		return readbrokerstudy.ErrCleanupPending
	}
	s.creationBarrier = nil
	lease.state = creationBarrierBlocked // terminal; token cannot be reused
	lease.worker = nil
	lease.stage = readbrokerstudy.CreationStage{}
	if lease.cancel != nil {
		lease.cancel()
	}
	lease.doneOnce.Do(func() { close(lease.done) })
	return nil
}

// admitCreationStage and releaseCreationPublication are the two short
// linearization points shared with Protect's owner-pending transition. They
// never hold the service mutex while the worker performs filesystem effects.
func (s *linuxImplementation) admitCreationStage(lease *creationBarrierLease) error {
	if s == nil || lease == nil || lease.service != s {
		return ErrCreationBarrier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creationBarrier != lease || lease.state != creationBarrierActive || lease.stageEntryAdmitted || lease.ctx.Err() != nil || s.closed || len(s.blocked) != 0 || s.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || s.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		return ErrCoverageMissing
	}
	lease.stageEntryAdmitted = true
	return nil
}

func (s *linuxImplementation) releaseCreationPublication(lease *creationBarrierLease) error {
	if s == nil || lease == nil || lease.service != s {
		return ErrCreationBarrier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creationBarrier != lease || lease.state != creationBarrierActive || !lease.stageEntryAdmitted || lease.publicationReleased || lease.ctx.Err() != nil || s.closed || len(s.blocked) != 0 || s.owners[auth.SensitiveOwnerAuth].State != OwnerRegistered || s.owners[auth.SensitiveOwnerSettings].State != OwnerRegistered {
		return ErrCoverageMissing
	}
	lease.publicationReleased = true
	return nil
}

// executeBoundedCreation is the Linux host-owned effect path for one frozen,
// already-approved create proposal. It accepts only a retained parent
// descriptor and the exact leaf/content tuple; it never resolves a model path.
// Normal command composition does not expose this method or the create schema.
func (s *linuxImplementation) executeBoundedCreation(ctx context.Context, parent *os.File, parentIdentity readbrokerstudy.FileIdentity, stageName, leaf string, content []byte, rootBinding ...creationRootBinding) (readbrokerstudy.CreationResult, error) {
	unknown := readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}
	if ctx == nil || parent == nil || !validCreationStageName(stageName) || !validCreationLeaf(leaf) || len(content) > readbrokerstudy.MaxReadBytes || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, readbrokerstudy.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, err
	}
	var parentStat syscall.Stat_t
	if err := syscall.Fstat(int(parent.Fd()), &parentStat); err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, readbrokerstudy.ErrStaleReceipt
	}
	actualParent := readbrokerstudy.FileIdentity{Device: uint64(parentStat.Dev), Inode: parentStat.Ino, Type: uint32(parentStat.Mode & syscall.S_IFMT)}
	if actualParent != parentIdentity || parentStat.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, readbrokerstudy.ErrStaleReceipt
	}
	if err := readbrokerstudy.ValidateCreationDirectoryDescriptor(int(parent.Fd())); err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, err
	}
	if !s.syntheticCreationManifestEligible(parentIdentity) {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, ErrCoverageMissing
	}
	rootPath := parent.Name()
	if len(rootBinding) == 1 && rootBinding[0].root != "" {
		rootPath = rootBinding[0].root
	}
	if !s.creationTargetUnprotected(filepath.Join(rootPath, leaf)) {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, ErrCoverageMissing
	}

	lease, err := s.beginCreationBarrier(ctx, rootBinding...)
	if err != nil {
		if lease != nil {
			_ = s.finishCreationBarrier(lease, true)
		}
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, err
	}
	barrierRetired := false
	finishBarrier := func(clean bool) error {
		if barrierRetired {
			return nil
		}
		barrierRetired = true
		if finishErr := s.finishCreationBarrier(lease, clean); finishErr != nil {
			return finishErr
		}
		return nil
	}
	defer func() {
		if !barrierRetired {
			_ = finishBarrier(false)
		}
	}()
	if !s.syntheticCreationManifestEligible(parentIdentity) {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(ErrCoverageMissing, finishBarrier(true))
	}
	if err := validateCreationParentAndAbsentLeaf(parent, parentIdentity, leaf); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationConflict}, finishBarrier(true)
		}
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(err, finishBarrier(true))
	}

	s.mu.Lock()
	launcher, available := s.launcher.(readbrokerstudy.OperationWorkerLauncher)
	stillOpen := !s.closed && s.creationBarrier == lease
	s.mu.Unlock()
	if !stillOpen || !available {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(readbrokerstudy.ErrUnavailable, finishBarrier(true))
	}
	worker, err := launcher.StartForOperation(lease.ctx, readbrokerstudy.AdmissionCreateFile)
	if err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(err, finishBarrier(true))
	}
	s.mu.Lock()
	if s.creationBarrier == lease {
		lease.worker = worker
	}
	s.mu.Unlock()
	joined := false
	var workerWaitErr error
	waitDone := make(chan error, 1)
	waitStarted := false
	waitCompleted := false
	waitAndConfirm := func() bool {
		if worker.Joined() {
			return true
		}
		if !waitStarted {
			waitStarted = true
			go func() {
				waitDone <- worker.Wait()
			}()
		}
		if waitCompleted {
			return worker.Joined()
		}
		timer := time.NewTimer(creationWorkerJoinTimeout)
		defer timer.Stop()
		select {
		case workerWaitErr = <-waitDone:
			waitCompleted = true
			return worker.Joined()
		case <-timer.C:
			return false
		}
	}
	stopAndJoin := func() bool {
		_ = worker.Terminate()
		joined = waitAndConfirm()
		return joined
	}
	if err := worker.Ready(lease.ctx); err != nil {
		joined = stopAndJoin()
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(err, finishBarrier(joined))
	}
	if err := validateCreationParentAndAbsentLeaf(parent, parentIdentity, leaf); err != nil {
		joined = stopAndJoin()
		outcome := readbrokerstudy.CreationNotCreated
		if errors.Is(err, unix.EEXIST) {
			outcome = readbrokerstudy.CreationConflict
		}
		if outcome == readbrokerstudy.CreationConflict && joined {
			return readbrokerstudy.CreationResult{Outcome: outcome}, finishBarrier(true)
		}
		return readbrokerstudy.CreationResult{Outcome: outcome}, errors.Join(err, finishBarrier(joined))
	}
	creationWorker, ok := worker.(readbrokerstudy.BoundedCreationWorker)
	if !ok {
		joined = stopAndJoin()
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(readbrokerstudy.ErrProtocol, finishBarrier(joined))
	}
	if err := s.admitCreationStage(lease); err != nil {
		joined = stopAndJoin()
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, errors.Join(err, finishBarrier(joined))
	}
	stage, err := creationWorker.StageCreation(lease.ctx, parent, parentIdentity, stageName, leaf, content)
	if err != nil {
		if stage.StageIdentity().Inode != 0 {
			s.mu.Lock()
			if s.creationBarrier == lease {
				lease.stage = stage
			}
			s.mu.Unlock()
			result, abortErr := creationWorker.AbortCreation(creationWorker.WorkerContext(), stage)
			joined = waitAndConfirm()
			clean := joined && abortErr == nil && result.Outcome == readbrokerstudy.CreationNotCreated
			if !clean {
				result = unknown
			}
			return result, errors.Join(err, abortErr, finishBarrier(clean))
		}
		var operationError *readbrokerstudy.CreationOperationError
		knownNoStage := errors.As(err, &operationError) && operationError.Result.Outcome == readbrokerstudy.CreationNotCreated
		if !knownNoStage {
			_ = stopAndJoin()
			return unknown, errors.Join(err, finishBarrier(false))
		}
		_ = stopAndJoin()
		joined = worker.Joined()
		if !joined {
			return unknown, errors.Join(err, finishBarrier(false))
		}
		finishErr := finishBarrier(true)
		if errors.Is(operationError.Cause, context.Canceled) || errors.Is(operationError.Cause, context.DeadlineExceeded) {
			return operationError.Result, errors.Join(operationError.Cause, finishErr)
		}
		return operationError.Result, finishErr
	}
	s.mu.Lock()
	if s.creationBarrier == lease {
		lease.stage = stage
	}
	s.mu.Unlock()
	if lease.ctx.Err() != nil {
		result, abortErr := creationWorker.AbortCreation(creationWorker.WorkerContext(), stage)
		joined = waitAndConfirm()
		clean := joined && abortErr == nil && result.Outcome == readbrokerstudy.CreationNotCreated
		if !clean {
			result = unknown
		}
		return result, errors.Join(lease.ctx.Err(), abortErr, finishBarrier(clean))
	}
	if !s.syntheticCreationManifestEligible(parentIdentity) {
		result, abortErr := creationWorker.AbortCreation(creationWorker.WorkerContext(), stage)
		joined = waitAndConfirm()
		clean := joined && abortErr == nil && result.Outcome == readbrokerstudy.CreationNotCreated
		if !clean {
			result = unknown
		}
		return result, errors.Join(ErrCoverageMissing, abortErr, finishBarrier(clean))
	}
	if err := validateCreationParentAndAbsentLeaf(parent, parentIdentity, leaf); err != nil {
		result, abortErr := creationWorker.AbortCreation(creationWorker.WorkerContext(), stage)
		joined = waitAndConfirm()
		clean := joined && abortErr == nil && result.Outcome == readbrokerstudy.CreationNotCreated
		outcome := readbrokerstudy.CreationNotCreated
		if errors.Is(err, unix.EEXIST) {
			outcome = readbrokerstudy.CreationConflict
		}
		if !clean {
			outcome = readbrokerstudy.CreationUnknown
		}
		result = readbrokerstudy.CreationResult{Outcome: outcome}
		if outcome == readbrokerstudy.CreationConflict && clean {
			return result, errors.Join(abortErr, finishBarrier(true))
		}
		return result, errors.Join(err, abortErr, finishBarrier(clean))
	}
	if hook := s.creationBeforePublicationReleaseHook; hook != nil {
		hook()
	}
	if err := s.releaseCreationPublication(lease); err != nil {
		result, abortErr := creationWorker.AbortCreation(creationWorker.WorkerContext(), stage)
		joined = waitAndConfirm()
		clean := joined && abortErr == nil && result.Outcome == readbrokerstudy.CreationNotCreated
		if !clean {
			result = unknown
		}
		return result, errors.Join(err, abortErr, finishBarrier(clean))
	}
	result, publishErr := creationWorker.PublishCreation(creationWorker.WorkerContext(), stage)
	joined = waitAndConfirm()
	if !joined || workerWaitErr != nil || publishErr != nil || result.Outcome == readbrokerstudy.CreationUnknown {
		return unknown, errors.Join(workerWaitErr, publishErr, finishBarrier(false))
	}
	return result, errors.Join(publishErr, finishBarrier(true))
}

// PrepareFilesystemCreation implements the explicit fixture-only runtime seam.
// Normal cmd/harness composition never wraps the runtime with this service.
func (s *Service) PrepareFilesystemCreation(ctx context.Context, invocation tools.Invocation, destination string) (permissions.Request, error) {
	if ctx == nil || s == nil || s.impl == nil || invocation.ToolID != tools.CreateFileID || invocation.ConversationID == "" || invocation.ProviderCallID == "" {
		return permissions.Request{}, ErrServiceClosed
	}
	input, err := tools.ParseCreateFile(invocation.Arguments)
	if err != nil {
		return permissions.Request{}, err
	}
	// This prototype intentionally supports one existing parent and one leaf;
	// it does not create directories or resolve a model-selected scope.
	if strings.Contains(input.Path, "/") {
		return permissions.Request{}, errors.New("creation prototype accepts a single leaf beneath the selected scope")
	}
	s.rootMu.Lock()
	if s.rootClosed || s.rootPending || s.rootPath == "" || invocation.ConversationID != s.rootConversationID {
		s.rootMu.Unlock()
		return permissions.Request{}, ErrCoverageMissing
	}
	root, rootGeneration := s.rootPath, s.rootGeneration
	s.rootMu.Unlock()
	parent, err := os.Open(root)
	if err != nil {
		return permissions.Request{}, ErrCoverageMissing
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(parent.Fd()), &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = parent.Close()
		return permissions.Request{}, ErrCoverageMissing
	}
	identity := identityFromUnixStat(&st)
	if err := readbrokerstudy.ValidateCreationDirectoryDescriptor(int(parent.Fd())); err != nil {
		_ = parent.Close()
		return permissions.Request{}, err
	}
	impl, ok := s.impl.(*linuxImplementation)
	if !ok || !impl.syntheticCreationManifestEligible(identity) {
		_ = parent.Close()
		return permissions.Request{}, ErrCoverageMissing
	}
	target := filepath.Join(root, input.Path)
	if !impl.creationTargetUnprotected(target) {
		_ = parent.Close()
		return permissions.Request{}, ErrCoverageMissing
	}
	if err = validateCreationParentAndAbsentLeaf(parent, identity, input.Path); err != nil {
		_ = parent.Close()
		if errors.Is(err, unix.EEXIST) {
			return permissions.Request{}, errors.New("creation destination already exists; a new proposal is required")
		}
		return permissions.Request{}, err
	}
	coverageRevision := s.CoverageSnapshot().Revision
	policyRevision := impl.registry.View().Generation
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		_ = parent.Close()
		return permissions.Request{}, err
	}
	requestData, _ := json.Marshal(struct {
		ConversationID, TurnID, CallID, Destination, Root, Target, Content string
		RootGeneration, PolicyRevision, CoverageRevision                   uint64
		Parent                                                             readbrokerstudy.FileIdentity
	}{invocation.ConversationID, invocation.TurnID, invocation.ProviderCallID, destination, root, target, input.Content, rootGeneration, policyRevision, coverageRevision, identity})
	digest := sha256.Sum256(requestData)
	diff := creationApprovalDiff(target, destination, []byte(input.Content))
	request := permissions.Request{ConversationID: invocation.ConversationID, CallID: invocation.ProviderCallID, Destination: destination, AdmissionBinding: hex.EncodeToString(digest[:]), RequireOnceKeys: []string{"create", "target"}, Requirements: []permissions.Requirement{
		{Kind: permissions.RequirementAction, Key: "create", ToolID: string(tools.CreateFileID), Description: diff, Operation: permissions.OperationWrite, ActionKey: "bounded-create-once"},
		{Kind: permissions.RequirementResource, Key: "target", ToolID: string(tools.CreateFileID), Description: "Create this exact target once", Operation: permissions.OperationWrite, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationWrite}},
	}}
	proposal := &serviceCreationProposal{invocation: invocation, request: request, input: input, parent: parent, parentIdentity: identity, root: root, rootGeneration: rootGeneration, coverageRevision: coverageRevision, policyRevision: policyRevision, stageName: stageName}
	impl.mu.Lock()
	if impl.closed || impl.creationManifest == nil {
		impl.mu.Unlock()
		_ = parent.Close()
		return permissions.Request{}, ErrCoverageMissing
	}
	key := invocation.ConversationID + "\x00" + invocation.ProviderCallID
	previous := impl.creationProposals[key]
	delete(impl.creationProposals, key)
	impl.mu.Unlock()
	if previous != nil && previous.parent != nil {
		if err := previous.parent.Close(); err != nil {
			impl.mu.Lock()
			if impl.creationProposals[key] == nil {
				impl.creationProposals[key] = previous
			}
			impl.mu.Unlock()
			_ = parent.Close()
			return permissions.Request{}, errors.Join(readbrokerstudy.ErrCleanupPending, err)
		}
	}
	impl.mu.Lock()
	if impl.closed || impl.creationManifest == nil || impl.creationProposals[key] != nil {
		impl.mu.Unlock()
		_ = parent.Close()
		return permissions.Request{}, runtime.ErrFilesystemRootStale
	}
	impl.creationProposals[key] = proposal
	impl.mu.Unlock()
	return request, nil
}

// PrepareApprovedFilesystemCreation consumes the retained proposal exactly
// once after the runtime has accepted the exact once-only approval request.
func (s *Service) PrepareApprovedFilesystemCreation(ctx context.Context, invocation tools.Invocation, request permissions.Request, _ uint64) (runtime.ToolPreparation, error) {
	if ctx == nil || s == nil || s.impl == nil {
		return nil, ErrServiceClosed
	}
	impl, ok := s.impl.(*linuxImplementation)
	if !ok {
		return nil, ErrCoverageMissing
	}
	key := invocation.ConversationID + "\x00" + invocation.ProviderCallID
	impl.mu.Lock()
	proposal := impl.creationProposals[key]
	expectedRequest := permissions.Request{}
	if proposal != nil {
		expectedRequest = proposal.request
		expectedRequest.Binding = request.Binding
	}
	if proposal == nil || proposal.consumed || !reflect.DeepEqual(expectedRequest, request) || proposal.request.ConversationID != invocation.ConversationID || proposal.request.CallID != invocation.ProviderCallID || proposal.invocation.TurnID != invocation.TurnID || !bytes.Equal(proposal.invocation.Arguments, invocation.Arguments) {
		impl.mu.Unlock()
		return nil, runtime.ErrFilesystemRootStale
	}
	proposal.consumed = true
	delete(impl.creationProposals, key)
	impl.mu.Unlock()
	s.rootMu.Lock()
	current := !s.rootClosed && !s.rootPending && s.rootPath == proposal.root && s.rootConversationID == invocation.ConversationID && s.rootGeneration == proposal.rootGeneration
	s.rootMu.Unlock()
	if !current || s.CoverageSnapshot().Revision != proposal.coverageRevision || impl.registry.View().Generation != proposal.policyRevision || !impl.syntheticCreationManifestEligible(proposal.parentIdentity) {
		_ = proposal.parent.Close()
		return nil, runtime.ErrFilesystemRootStale
	}
	return &serviceCreationPreparation{service: s, impl: impl, proposal: proposal}, nil
}

func (s *Service) DiscardFilesystemCreationProposal(_ context.Context, invocation tools.Invocation) error {
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	impl, ok := s.impl.(*linuxImplementation)
	if !ok {
		return ErrCoverageMissing
	}
	key := invocation.ConversationID + "\x00" + invocation.ProviderCallID
	impl.mu.Lock()
	proposal := impl.creationProposals[key]
	if proposal == nil {
		impl.mu.Unlock()
		return nil
	}
	if !bytes.Equal(proposal.invocation.Arguments, invocation.Arguments) {
		impl.mu.Unlock()
		return runtime.ErrFilesystemRootStale
	}
	delete(impl.creationProposals, key)
	impl.mu.Unlock()
	if proposal.parent == nil {
		return nil
	}
	if err := proposal.parent.Close(); err != nil {
		impl.mu.Lock()
		if impl.creationProposals[key] == nil {
			impl.creationProposals[key] = proposal
		}
		impl.mu.Unlock()
		return errors.Join(readbrokerstudy.ErrCleanupPending, err)
	}
	return nil
}

type serviceCreationPreparation struct {
	service          *Service
	impl             *linuxImplementation
	proposal         *serviceCreationProposal
	mu               sync.Mutex
	executed, closed bool
}

func (s *linuxImplementation) invalidateCreationProposals() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	proposals := make(map[string]*serviceCreationProposal, len(s.creationProposals))
	for key, proposal := range s.creationProposals {
		proposals[key] = proposal
	}
	s.mu.Unlock()
	var closeErr error
	closed := make(map[string]bool, len(proposals))
	for key, proposal := range proposals {
		if proposal != nil && proposal.parent != nil {
			err := proposal.parent.Close()
			closeErr = errors.Join(closeErr, err)
			closed[key] = err == nil
		} else {
			closed[key] = true
		}
	}
	s.mu.Lock()
	for key, proposal := range proposals {
		if closed[key] && s.creationProposals[key] == proposal {
			delete(s.creationProposals, key)
		}
	}
	s.mu.Unlock()
	return closeErr
}

func (p *serviceCreationPreparation) Execute(ctx context.Context) (tools.Result, error) {
	p.mu.Lock()
	if p.closed || p.executed {
		p.mu.Unlock()
		return tools.Result{Outcome: tools.OutcomeNotExecuted, ErrorCode: "creation_receipt_consumed"}, runtime.ErrFilesystemRootStale
	}
	p.executed = true
	p.mu.Unlock()
	result, err := p.impl.executeBoundedCreation(ctx, p.proposal.parent, p.proposal.parentIdentity, p.proposal.stageName, p.proposal.input.Path, []byte(p.proposal.input.Content), creationRootBinding{root: p.proposal.root, conversation: p.proposal.invocation.ConversationID, generation: p.proposal.rootGeneration})
	cancelledWithoutEffect := result.Outcome == readbrokerstudy.CreationNotCreated && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
	toolResult := tools.Result{Outcome: tools.OutcomeNotExecuted, ErrorCode: "creation_not_created"}
	switch result.Outcome {
	case readbrokerstudy.CreationCreated:
		toolResult.Outcome = tools.OutcomeSucceeded
		toolResult.Content = []tools.Content{{Kind: tools.ContentText, Text: fmt.Sprintf("Created %s (%d bytes)", filepath.Join(p.proposal.root, p.proposal.input.Path), result.Bytes)}}
	case readbrokerstudy.CreationConflict:
		toolResult.Outcome = tools.OutcomeFailed
		toolResult.ErrorCode = "creation_conflict"
	case readbrokerstudy.CreationNotCreated:
		toolResult.Outcome = tools.OutcomeFailed
		if cancelledWithoutEffect {
			toolResult.Outcome = tools.OutcomeCancelled
			toolResult.ErrorCode = "creation_cancelled"
		}
	case readbrokerstudy.CreationUnknown:
		toolResult.Outcome = tools.OutcomeUnknown
		toolResult.ErrorCode = "creation_outcome_unknown"
	}
	toolResult.ProviderCallID = p.proposal.invocation.ProviderCallID
	if cancelledWithoutEffect {
		return toolResult, nil
	}
	return toolResult, err
}
func (p *serviceCreationPreparation) Close(context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	parent := p.proposal.parent
	p.mu.Unlock()
	if parent == nil {
		return nil
	}
	return parent.Close()
}

func (s *linuxImplementation) syntheticCreationManifestEligible(parent readbrokerstudy.FileIdentity) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	manifest := s.creationManifest
	registry, inventory := s.registry, s.inventory
	owners := make(map[string]OwnerCoverage, len(s.owners))
	for owner, coverage := range s.owners {
		owners[owner] = coverage
	}
	closed := s.closed
	s.mu.Unlock()
	if closed || manifest == nil || !manifest.scopeComplete || manifest.scopeParent != parent || registry == nil || inventory == nil {
		return false
	}
	policy := registry.View()
	coverage := inventory.Snapshot()
	if !coverage.Complete || policy.Generation != manifest.policyRevision || coverage.Revision != manifest.coverageRevision {
		return false
	}
	const minimumTokenIdentities = 5 // DB, WAL, SHM, journal, and backup.
	if len(manifest.owners) != 4 {
		return false
	}
	required := map[string]readbrokerstudy.ResourceClass{
		auth.SensitiveOwnerAuth:     readbrokerstudy.ClassCredential,
		auth.SensitiveOwnerSettings: readbrokerstudy.ClassSettings,
		"tokenlogger-store":         readbrokerstudy.ClassTokenDatabase,
		"provider-source":           readbrokerstudy.ClassProvider,
	}
	for owner, class := range required {
		entry, exists := manifest.owners[owner]
		if !exists || entry.class != class || len(entry.registrations) == 0 {
			return false
		}
		coverageState, exists := owners[owner]
		if !exists || coverageState.State != OwnerRegistered {
			return false
		}
		minimum := 1
		if owner == "tokenlogger-store" {
			minimum = minimumTokenIdentities
		}
		if len(entry.registrations) < minimum {
			return false
		}
		for route, identity := range entry.registrations {
			found := false
			for _, candidate := range policy.Entries {
				if candidate.Owner == owner && candidate.Class == class && candidate.Path == route && candidate.Identity == identity && !candidate.Directory && identity.Inode != 0 {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	tokenDB, ok := manifest.owners["tokenlogger-store"]
	if !ok || !strings.HasSuffix(manifest.tokenDatabasePath, ".db") {
		return false
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".backup"} {
		if _, exists := tokenDB.registrations[manifest.tokenDatabasePath+suffix]; !exists {
			return false
		}
	}
	return true
}

func (s *linuxImplementation) creationTargetUnprotected(target string) bool {
	if s == nil || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return false
	}
	s.mu.Lock()
	registry, closed := s.registry, s.closed
	s.mu.Unlock()
	if closed || registry == nil {
		return false
	}
	_, protected := registry.ClassifyRoute(target)
	return !protected
}

func validateCreationParentAndAbsentLeaf(parent *os.File, expected readbrokerstudy.FileIdentity, leaf string) error {
	if parent == nil || !validCreationLeaf(leaf) {
		return readbrokerstudy.ErrStaleReceipt
	}
	var parentStat unix.Stat_t
	if err := unix.Fstat(int(parent.Fd()), &parentStat); err != nil || identityFromUnixStat(&parentStat) != expected || parentStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return readbrokerstudy.ErrStaleReceipt
	}
	flags, err := unix.FcntlInt(parent.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return readbrokerstudy.ErrStaleReceipt
	}
	var targetStat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), leaf, &targetStat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		return unix.EEXIST
	} else if !errors.Is(err, unix.ENOENT) {
		return readbrokerstudy.ErrUnavailable
	}
	return nil
}

func identityFromUnixStat(stat *unix.Stat_t) readbrokerstudy.FileIdentity {
	if stat == nil {
		return readbrokerstudy.FileIdentity{}
	}
	return readbrokerstudy.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Type: stat.Mode & unix.S_IFMT}
}

func validCreationStageName(value string) bool {
	const prefix = ".komposyn-stage-"
	if len(value) != len(prefix)+32 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, char := range value[len(prefix):] {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validCreationLeaf(value string) bool {
	return value != "" && len(value) <= 255 && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00") && utf8.ValidString(value)
}

func creationApprovalDiff(target, destination string, content []byte) string {
	var diff strings.Builder
	fmt.Fprintf(&diff, "Create %s (%d bytes) for %s. Complete diff:\n--- /dev/null\n+++ %s\n", target, len(content), destination, target)
	text := string(content)
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if index == len(lines)-1 && line == "" {
			break
		}
		diff.WriteByte('+')
		diff.WriteString(line)
		if index < len(lines)-1 {
			diff.WriteByte('\n')
		}
	}
	return diff.String()
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
		Operation: string(metadata.Operation),
		Reference: metadata.Reference, ServiceID: metadata.ServiceID, ConversationID: metadata.ConversationID, TurnID: metadata.TurnID, CallID: metadata.CallID,
		Root: metadata.Root, Deadline: metadata.Deadline, AdmissionBinding: fmt.Sprintf("%x", metadata.Binding[:]), Target: metadata.Target,
		Class: string(metadata.Class), Identity: fmt.Sprintf("%d:%d:%d", metadata.Identity.Device, metadata.Identity.Inode, metadata.Identity.Type),
		RootGeneration: metadata.RootGeneration, PolicyRevision: metadata.Generations.Policy,
		CoverageRevision: metadata.Generations.Inventory, Destination: metadata.Destination,
		Listing: tools.ListFilesInput{Recursive: metadata.Listing.Recursive, IncludeHidden: metadata.Listing.IncludeHidden, IncludeIgnored: metadata.Listing.IncludeIgnored, NameGlobs: append([]string(nil), metadata.Listing.NameGlobs...)},
		Search:  tools.SearchFilesInput{Path: metadata.Target, Query: metadata.SearchQuery, Recursive: metadata.Listing.Recursive, IncludeHidden: metadata.Listing.IncludeHidden, IncludeIgnored: metadata.Listing.IncludeIgnored, NameGlobs: append([]string(nil), metadata.Listing.NameGlobs...)},
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
	if p.impl.closed || p.impl.creationBarrier != nil || p.impl.session == nil || len(p.impl.blocked) != 0 {
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
	var missingServiceOwners []OwnerCoverage
	var requiredOwners []string
	if s.session != nil {
		study := s.session.CoverageSnapshot()
		if study.Revision > s.revision {
			s.revision = study.Revision
		}
		for _, owner := range study.Requirements {
			requiredOwners = append(requiredOwners, owner.Owner)
			coverage, exists := s.owners[owner.Owner]
			if !exists {
				missingServiceOwners = append(missingServiceOwners, OwnerCoverage{
					Owner: owner.Owner, Class: string(owner.Class), State: OwnerRequired,
					Reason: "required owner has no production service lifecycle registration",
				})
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
	owners = append(owners, missingServiceOwners...)
	sortOwnerCoverage(owners)
	slices.Sort(requiredOwners)
	return CoverageSnapshot{Revision: s.revision, Composition: "komposyn-memory-chat", Capability: "linux-exact-file-typed-read", UnknownRootDataGate: true, RequiredOwners: requiredOwners, Owners: owners}
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

// DirectoryListingAvailable requires the same inspected backend and composed
// fixed worker service as exact-file reads. The runtime separately filters
// this capability by provider route and selected-root state; admission checks
// required protected-owner coverage before any approval or worker launch.
func (s *linuxImplementation) DirectoryListingAvailable() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.backendErr == nil && s.launcher != nil && s.session != nil
}

// DirectoryListingOptionsAvailable exposes advanced listing only for the
// trusted, composed descriptor-bind worker profile covered by the accepted
// recursive native evidence. Other launch modes fail closed.
func (s *linuxImplementation) DirectoryListingOptionsAvailable() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.backendErr == nil && s.launcher != nil && s.session != nil && s.listingOptionsAvailable
}

// SearchFilesAvailable requires the measured Linux amd64 descriptor-bind
// worker profile. Admission additionally verifies the selected directory's
// retained descriptor is on ext4 before presenting an approval request.
func (s *linuxImplementation) SearchFilesAvailable() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.launcher.(interface{ SearchProfilesAvailable() bool })
	return !s.closed && s.backendErr == nil && s.session != nil && s.launcher != nil && s.searchProfileAvailable && ok && profile.SearchProfilesAvailable()
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
	lease := s.creationBarrier
	if lease != nil && lease.cancel != nil {
		lease.cancel()
	}
	session, launcher, registry := s.session, s.launcher, s.registry
	s.mu.Unlock()
	if lease != nil {
		select {
		case <-lease.done:
		case <-ctx.Done():
			return errors.Join(readbrokerstudy.ErrCleanupPending, ctx.Err())
		}
	}
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
