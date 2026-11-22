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

// Package sandbox composes the experimental chat's host-owned protected-file
// lifecycle with the fixed Linux read worker. It exposes no filesystem tool.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

var (
	ErrUnknownRootData = errors.New("generic reads are unavailable because selected-root data coverage is unresolved")
	ErrCoverageMissing = errors.New("required auth/settings protection is not registered")
	ErrServiceClosed   = errors.New("sandbox composition service is closed")
	ErrCreationBarrier = errors.New("filesystem admissions are paused by a creation barrier")
)

type OwnerState string

const (
	OwnerRequired    OwnerState = "required"
	OwnerRegistered  OwnerState = "registered"
	OwnerPending     OwnerState = "pending"
	OwnerStale       OwnerState = "stale"
	OwnerRetired     OwnerState = "retired"
	OwnerUnsupported OwnerState = "unsupported"
	OwnerAbsent      OwnerState = "absent-from-composition"
)

type OwnerCoverage struct {
	Owner      string
	Class      string
	State      OwnerState
	Routes     int
	Identities int
	Evidence   string
	Reason     string
}

type CoverageSnapshot struct {
	Revision            uint64
	Composition         string
	Capability          string
	UnknownRootDataGate bool
	RequiredOwners      []string
	Owners              []OwnerCoverage
}

// CreationOwnerDisposition records whether trusted composition instantiates a
// sensitive owner. It is separate from registration state: a required owner
// with an absent file still needs its effective route registered.
type CreationOwnerDisposition string

const (
	CreationOwnerRequired        CreationOwnerDisposition = "required"
	CreationOwnerNotInstantiated CreationOwnerDisposition = "trusted-not-instantiated"
	CreationOwnerUnknown         CreationOwnerDisposition = "unknown"
)

// CreationOwnerStatus is a detached view of one owner in the fixed production
// chat profile. It is informational and never grants filesystem authority.
type CreationOwnerStatus struct {
	Owner       string
	Class       string
	Disposition CreationOwnerDisposition
	State       OwnerState
	Routes      int
	Identities  int
	Reason      string
}

type StartupScopeState string

const (
	StartupScopeUnavailable      StartupScopeState = "unavailable"
	StartupScopeIdentityDeferred StartupScopeState = "identity-validation-deferred"
	StartupScopeClosed           StartupScopeState = "closed"
)

// StartupScopeSnapshot is a non-authorizing, path-free snapshot of the scope
// captured by trusted CLI composition. The directory identity and movement
// checks are deliberately not claimed by this foundation slice.
type StartupScopeSnapshot struct {
	RunBinding       string
	Generation       uint64
	State            StartupScopeState
	CoverageRevision uint64
	Reason           string
}

// CreationFoundationSnapshot describes trusted owner applicability and the
// fixed startup-scope lifecycle. Complete owner coverage is not creation
// eligibility: CapabilityRegistered remains false until a separate reviewed
// enablement slice.
type CreationFoundationSnapshot struct {
	ProfileRevision       uint64
	CoverageRevision      uint64
	Owners                []CreationOwnerStatus
	OwnerCoverageComplete bool
	StartupScope          StartupScopeSnapshot
	CapabilityRegistered  bool
	Reason                string
}

type creationOwnerDeclaration struct {
	owner, class string
	disposition  CreationOwnerDisposition
	reason       string
}

// This profile is fixed by cmd/harness's current composition: it constructs
// auth.Store and settings.Store, but no Token Logger store or provider-source
// ingestion component. New sensitive components must be added here as
// required before that composition can report complete coverage.
func productionChatCreationOwnerProfile() []creationOwnerDeclaration {
	return []creationOwnerDeclaration{
		{owner: auth.SensitiveOwnerAuth, class: auth.SensitiveClassAuth, disposition: CreationOwnerRequired},
		{owner: auth.SensitiveOwnerSettings, class: auth.SensitiveClassSettings, disposition: CreationOwnerRequired},
		{owner: "tokenlogger-store", class: "token_database", disposition: CreationOwnerNotInstantiated, reason: "trusted chat composition does not construct a Token Logger store"},
		{owner: "provider-source", class: "provider_source", disposition: CreationOwnerNotInstantiated, reason: "trusted chat composition does not ingest provider sources"},
	}
}

type implementation interface {
	auth.SensitiveFileProtector
	Close(context.Context) error
	CoverageSnapshot() CoverageSnapshot
	BackendStatus() (available bool, detail string)
	PrepareGenericRead(context.Context, string, string) error
}

type readAdmissionImplementation interface {
	AdmitFilesystemRead(context.Context, tools.Invocation, string, string, uint64, uint64) (runtime.FilesystemReadAdmission, error)
	PrepareAdmittedFilesystemRead(context.Context, runtime.FilesystemReadAdmission, uint64) (runtime.FilesystemReadPreparation, error)
	CloseFilesystemReadAdmission(context.Context, runtime.FilesystemReadAdmission) error
	SelectFilesystemRoot(context.Context, runtime.FilesystemRootTransition) error
	InvalidateFilesystemAdmissions(context.Context) error
	SetFilesystemReadInvalidator(func(string))
}

// Service is constructed only by trusted executable composition. The
// constructor fixes the production profile; callers cannot submit owner
// applicability or a generic-read completeness assertion.
type Service struct {
	impl               implementation
	rootMu             sync.Mutex
	rootClosed         bool
	rootConversationID string
	rootGeneration     uint64
	rootPath           string
	rootPending        bool
	startupScopeMu     sync.Mutex
	startupScopePath   string
	startupScope       StartupScopeSnapshot
}

var _ auth.SensitiveFileProtector = (*Service)(nil)
var _ runtime.ToolPreparer = (*Service)(nil)
var _ runtime.FilesystemRootHost = (*Service)(nil)
var _ runtime.FilesystemReadAdmissionHost = (*Service)(nil)

func NewHostService(bubblewrapPath string) (*Service, error) {
	impl, err := newHostImplementation(bubblewrapPath)
	if err != nil {
		return nil, err
	}
	service := &Service{impl: impl}
	service.startupScope = unavailableStartupScope("trusted startup directory was not supplied by CLI composition")
	return service, nil
}

// NewHostServiceWithStartupDirectory captures the trusted launch directory
// before runtime navigation can change it. It deliberately does not claim a
// retained directory identity; that validation is a later implementation
// slice, so the resulting scope remains unavailable for creation.
func NewHostServiceWithStartupDirectory(bubblewrapPath, startupDirectory string) (*Service, error) {
	impl, err := newHostImplementation(bubblewrapPath)
	if err != nil {
		return nil, err
	}
	service := &Service{impl: impl}
	service.captureStartupDirectory(startupDirectory)
	return service, nil
}

func unavailableStartupScope(reason string) StartupScopeSnapshot {
	return StartupScopeSnapshot{Generation: 1, State: StartupScopeUnavailable, Reason: reason}
}

func (s *Service) captureStartupDirectory(path string) {
	var binding [16]byte
	if _, err := rand.Read(binding[:]); err != nil {
		s.startupScope = unavailableStartupScope("startup run binding could not be initialized")
		return
	}
	canonical, err := runtime.CanonicalFilesystemRoot(context.Background(), path)
	if err != nil {
		s.startupScope = unavailableStartupScope("trusted startup directory is unavailable")
		return
	}
	s.startupScopePath = canonical
	available, _ := s.impl.BackendStatus()
	if !available {
		s.startupScope = StartupScopeSnapshot{
			RunBinding: hex.EncodeToString(binding[:]), Generation: 1,
			State: StartupScopeUnavailable, Reason: "host filesystem enforcement profile is unavailable",
		}
		return
	}
	s.startupScope = StartupScopeSnapshot{
		RunBinding: hex.EncodeToString(binding[:]), Generation: 1,
		State:  StartupScopeIdentityDeferred,
		Reason: "retained directory identity and movement validation are deferred",
	}
}

// WorkerBootstrap handles only the fixed internal re-exec entry used by the
// Bubblewrap worker. It must run before command parsing, auth loading, UI setup,
// or provider initialization. Other invocations continue through normal CLI
// startup.
func WorkerBootstrap(args []string) (handled bool, exitCode int) {
	return workerBootstrap(args)
}

func (s *Service) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	if invalidator, ok := s.impl.(interface{ invalidateCreationProposals() error }); ok {
		if err := invalidator.invalidateCreationProposals(); err != nil {
			return err
		}
	}
	return s.impl.Protect(ctx, owner, class, route, object)
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.impl == nil {
		return nil
	}
	s.rootMu.Lock()
	s.rootClosed = true
	s.rootPending = true
	s.rootPath = ""
	s.rootConversationID = ""
	s.rootGeneration++
	s.rootMu.Unlock()
	s.startupScopeMu.Lock()
	s.startupScope.Generation++
	s.startupScope.State = StartupScopeClosed
	s.startupScope.Reason = "host service is closed"
	s.startupScopePath = ""
	s.startupScopeMu.Unlock()
	var proposalErr error
	if invalidator, ok := s.impl.(interface{ invalidateCreationProposals() error }); ok {
		proposalErr = invalidator.invalidateCreationProposals()
	}
	return errors.Join(proposalErr, s.impl.Close(ctx))
}

// ValidateFilesystemRoot checks only the user-selected path for display and
// future lookup. It opens no target, retains no descriptor, and does not turn
// selection into permission.
func (s *Service) ValidateFilesystemRoot(ctx context.Context, path string) (string, error) {
	if s == nil || s.impl == nil {
		return "", ErrServiceClosed
	}
	s.rootMu.Lock()
	closed := s.rootClosed
	s.rootMu.Unlock()
	if closed {
		return "", ErrServiceClosed
	}
	return runtime.CanonicalFilesystemRoot(ctx, path)
}

// InvalidateFilesystemRoot records the newest conversation root generation.
// Root invalidation marks the selected root unavailable before draining
// retained admissions. Older asynchronous notifications cannot restore it.
func (s *Service) InvalidateFilesystemRoot(ctx context.Context, transition runtime.FilesystemRootTransition) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	s.rootMu.Lock()
	if s.rootClosed {
		s.rootMu.Unlock()
		return ErrServiceClosed
	}
	if transition.Generation < s.rootGeneration {
		s.rootMu.Unlock()
		return nil
	}
	if transition.Generation == s.rootGeneration && s.rootGeneration != 0 &&
		(transition.ConversationID != s.rootConversationID || transition.Root != s.rootPath) {
		s.rootMu.Unlock()
		return runtime.ErrFilesystemRootStale
	}
	s.rootConversationID = transition.ConversationID
	s.rootGeneration = transition.Generation
	s.rootPath = transition.Root
	s.rootPending = true
	s.rootMu.Unlock()
	if invalidator, ok := s.impl.(interface{ invalidateCreationProposals() error }); ok {
		if err := invalidator.invalidateCreationProposals(); err != nil {
			return err
		}
	}
	if impl, ok := s.impl.(readAdmissionImplementation); ok {
		if err := impl.SelectFilesystemRoot(ctx, transition); err != nil {
			return err
		}
	}
	s.rootMu.Lock()
	if s.rootGeneration == transition.Generation && s.rootConversationID == transition.ConversationID {
		s.rootPending = false
	}
	s.rootMu.Unlock()
	return ctx.Err()
}

// WithFilesystemReadPublicationCurrent orders the final provider handoff with
// selected-root and protected-owner transitions. The host root lock is held
// while the Linux owner gate runs the bounded transcript/request commit.
func (s *Service) WithFilesystemReadPublicationCurrent(ctx context.Context, conversationID, root string, generation, policyRevision, coverageRevision uint64, operation func() error) error {
	if ctx == nil || operation == nil {
		return ErrServiceClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	s.rootMu.Lock()
	defer s.rootMu.Unlock()
	if s.rootClosed || s.rootPending || s.rootPath == "" || s.rootPath != root || s.rootConversationID != conversationID || s.rootGeneration != generation {
		return runtime.ErrFilesystemRootStale
	}
	impl, ok := s.impl.(interface {
		WithFilesystemReadPublicationCurrent(uint64, uint64, func() error) error
	})
	if !ok {
		return ErrCoverageMissing
	}
	return impl.WithFilesystemReadPublicationCurrent(policyRevision, coverageRevision, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return operation()
	})
}

func (s *Service) SetFilesystemReadInvalidator(invalidate func(string)) {
	if s == nil || s.impl == nil {
		return
	}
	if impl, ok := s.impl.(readAdmissionImplementation); ok {
		impl.SetFilesystemReadInvalidator(invalidate)
	}
}

func (s *Service) CoverageSnapshot() CoverageSnapshot {
	if s == nil || s.impl == nil {
		return CoverageSnapshot{UnknownRootDataGate: true, Owners: []OwnerCoverage{{State: OwnerUnsupported, Reason: ErrServiceClosed.Error()}}}
	}
	return s.impl.CoverageSnapshot()
}

// CreationFoundationSnapshot returns detached owner/scope metadata for the
// gated creation implementation. This snapshot is never consumed as a grant;
// normal composition still does not register a creation capability.
func (s *Service) CreationFoundationSnapshot() CreationFoundationSnapshot {
	if s == nil || s.impl == nil {
		return CreationFoundationSnapshot{
			StartupScope:          unavailableStartupScope(ErrServiceClosed.Error()),
			Reason:                "host service is unavailable",
			CapabilityRegistered:  false,
			OwnerCoverageComplete: false,
		}
	}
	coverage := s.CoverageSnapshot()
	registered := make(map[string]OwnerCoverage, len(coverage.Owners))
	for _, owner := range coverage.Owners {
		registered[owner.Owner] = owner
	}
	declarations := productionChatCreationOwnerProfile()
	known := make(map[string]struct{}, len(declarations))
	requiredByInventory := make(map[string]struct{}, len(coverage.RequiredOwners))
	for _, owner := range coverage.RequiredOwners {
		requiredByInventory[owner] = struct{}{}
	}
	result := CreationFoundationSnapshot{
		ProfileRevision: 1, CoverageRevision: coverage.Revision,
		OwnerCoverageComplete: true, CapabilityRegistered: false,
		Reason: "production creation remains disabled pending scope identity enforcement, evidence, and separate enablement",
	}
	for _, declaration := range declarations {
		known[declaration.owner] = struct{}{}
		status := CreationOwnerStatus{
			Owner: declaration.owner, Class: declaration.class,
			Disposition: declaration.disposition, Reason: declaration.reason,
		}
		if declaration.disposition == CreationOwnerRequired {
			owner, ok := registered[declaration.owner]
			_, inventoryRequiresOwner := requiredByInventory[declaration.owner]
			if ok {
				status.State, status.Routes, status.Identities, status.Reason = owner.State, owner.Routes, owner.Identities, owner.Reason
			}
			if !ok || status.State != OwnerRegistered || status.Routes == 0 || len(coverage.RequiredOwners) > 0 && !inventoryRequiresOwner {
				result.OwnerCoverageComplete = false
				if status.Reason == "" {
					status.Reason = "required owner has no registered effective route"
				}
			}
		} else {
			if _, required := requiredByInventory[declaration.owner]; required {
				status.Disposition = CreationOwnerUnknown
				status.State = OwnerRequired
				status.Reason = "trusted composition requires this owner but the creation profile declares it not instantiated"
				result.OwnerCoverageComplete = false
			} else {
				status.State = OwnerAbsent
			}
		}
		result.Owners = append(result.Owners, status)
	}
	for _, owner := range coverage.Owners {
		if owner.Owner == "unknown-root-data" {
			continue // This remains a separate read/discovery authorizer gate.
		}
		if _, ok := known[owner.Owner]; !ok {
			result.OwnerCoverageComplete = false
			result.Owners = append(result.Owners, CreationOwnerStatus{
				Owner: owner.Owner, Class: owner.Class, Disposition: CreationOwnerUnknown,
				State: owner.State, Routes: owner.Routes, Identities: owner.Identities,
				Reason: "sensitive owner is not declared by the trusted production chat profile",
			})
		}
	}
	s.startupScopeMu.Lock()
	result.StartupScope = s.startupScope
	result.StartupScope.CoverageRevision = coverage.Revision
	if result.StartupScope.State == StartupScopeClosed {
		result.OwnerCoverageComplete = false
		result.Reason = "host service is closed"
	}
	s.startupScopeMu.Unlock()
	slices.SortFunc(result.Owners, func(left, right CreationOwnerStatus) int {
		return strings.Compare(left.Owner, right.Owner)
	})
	return result
}

func (s *Service) BackendStatus() (bool, string) {
	if s == nil || s.impl == nil {
		return false, ErrServiceClosed.Error()
	}
	return s.impl.BackendStatus()
}

// DirectoryListingAvailable reports whether this host service has the
// verified Linux worker composition required to advertise the bounded
// list_files capability. Other platforms and unavailable services return
// false.
func (s *Service) DirectoryListingAvailable() bool {
	if s == nil || s.impl == nil {
		return false
	}
	availability, ok := s.impl.(interface{ DirectoryListingAvailable() bool })
	return ok && availability.DirectoryListingAvailable()
}

// DirectoryListingOptionsAvailable reports whether this host service has
// independently reviewed native evidence for the recursive listing profile.
// The Linux implementation keeps this false until that profile is accepted.
func (s *Service) DirectoryListingOptionsAvailable() bool {
	if s == nil || s.impl == nil {
		return false
	}
	availability, ok := s.impl.(interface{ DirectoryListingOptionsAvailable() bool })
	return ok && availability.DirectoryListingOptionsAvailable()
}

// SearchFilesAvailable is independent from both directory listing gates and
// remains closed until search-specific native evidence and review are accepted.
func (s *Service) SearchFilesAvailable() bool {
	if s == nil || s.impl == nil {
		return false
	}
	availability, ok := s.impl.(interface{ SearchFilesAvailable() bool })
	return ok && availability.SearchFilesAvailable()
}

// Prepares reports that this host owns generic pre-dispatch preparation for a
// tool ID. read_file uses its dedicated retained-read path; this generic hook
// intentionally refuses it and cannot execute filesystem operations.
func (s *Service) Prepares(id tools.ID) bool { return id == tools.ID("read_file") }

func (s *Service) PrepareTool(ctx context.Context, _ tools.Tool, invocation tools.Invocation, _ permissions.Request, _ uint64) (runtime.ToolPreparation, error) {
	if !s.Prepares(invocation.ToolID) {
		return nil, errors.New("sandbox service received an unsupported tool")
	}
	return nil, s.PrepareGenericRead(ctx, "", "")
}

func (s *Service) PrepareGenericRead(ctx context.Context, root, target string) error {
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	return s.impl.PrepareGenericRead(ctx, root, target)
}

func (s *Service) AdmitFilesystemRead(ctx context.Context, invocation tools.Invocation, destination string) (runtime.FilesystemReadAdmission, error) {
	if ctx == nil || s == nil || s.impl == nil {
		return runtime.FilesystemReadAdmission{}, ErrServiceClosed
	}
	if (invocation.ToolID != tools.ReadFileID && invocation.ToolID != tools.ListFilesID && invocation.ToolID != tools.SearchFilesID) || invocation.ConversationID == "" || invocation.ProviderCallID == "" {
		return runtime.FilesystemReadAdmission{}, errors.New("filesystem admission invocation identity is invalid")
	}
	s.rootMu.Lock()
	if s.rootClosed || s.rootPending || s.rootPath == "" || invocation.ConversationID != s.rootConversationID {
		s.rootMu.Unlock()
		return runtime.FilesystemReadAdmission{}, ErrCoverageMissing
	}
	root, conversationID, rootGeneration := s.rootPath, s.rootConversationID, s.rootGeneration
	s.rootMu.Unlock()
	impl, ok := s.impl.(readAdmissionImplementation)
	if !ok {
		return runtime.FilesystemReadAdmission{}, ErrCoverageMissing
	}
	admission, err := impl.AdmitFilesystemRead(ctx, invocation, destination, root, rootGeneration, s.CoverageSnapshot().Revision)
	if err != nil {
		return runtime.FilesystemReadAdmission{}, err
	}
	s.rootMu.Lock()
	current := !s.rootClosed && !s.rootPending && s.rootPath == root && s.rootConversationID == conversationID && s.rootGeneration == rootGeneration
	s.rootMu.Unlock()
	if !current {
		return runtime.FilesystemReadAdmission{}, errors.Join(runtime.ErrFilesystemRootStale, impl.CloseFilesystemReadAdmission(ctx, admission))
	}
	return admission, nil
}

func (s *Service) PrepareAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, expectedRevision uint64) (runtime.FilesystemReadPreparation, error) {
	if ctx == nil || s == nil || s.impl == nil {
		return nil, ErrServiceClosed
	}
	impl, ok := s.impl.(readAdmissionImplementation)
	if !ok {
		return nil, ErrCoverageMissing
	}
	s.rootMu.Lock()
	current := !s.rootClosed && !s.rootPending && s.rootPath == admission.Root && s.rootConversationID == admission.ConversationID && s.rootGeneration == admission.RootGeneration
	if !current {
		s.rootMu.Unlock()
		return nil, runtime.ErrFilesystemRootStale
	}
	preparation, err := impl.PrepareAdmittedFilesystemRead(ctx, admission, expectedRevision)
	s.rootMu.Unlock()
	if err != nil {
		return nil, err
	}
	return &serviceAdmissionPreparation{service: s, inner: preparation, expected: admission}, nil
}

func (s *Service) CloseFilesystemReadAdmission(ctx context.Context, admission runtime.FilesystemReadAdmission) error {
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	impl, ok := s.impl.(readAdmissionImplementation)
	if !ok {
		return ErrCoverageMissing
	}
	return impl.CloseFilesystemReadAdmission(ctx, admission)
}

// ExecuteAdmittedFilesystemRead is the only Stage 4 execution seam. It accepts
// the exact preparation returned by this service and an unforgeable runtime
// reference; neither target data nor the reference is exposed to a tool/UI.
func (s *Service) ExecuteAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, preparation runtime.FilesystemReadPreparation, reference *runtime.ReadExecutionReference, authority runtime.FilesystemReadExecutionAuthority) (*runtime.PrivateReadCapture, error) {
	wrapped, ok := preparation.(*serviceAdmissionPreparation)
	if ctx == nil {
		return nil, context.Canceled
	}
	reject := func(err error) (*runtime.PrivateReadCapture, error) {
		if preparation != nil {
			err = errors.Join(err, preparation.Close(ctx))
		}
		return nil, err
	}
	if s == nil || !ok || wrapped == nil || wrapped.service != s || wrapped.inner == nil || authority == nil || reference == nil {
		return reject(ErrServiceClosed)
	}
	if !sameFilesystemReadAdmission(wrapped.expected, admission) || !sameFilesystemReadAdmission(wrapped.Metadata(), admission) {
		return reject(runtime.ErrFilesystemRootStale)
	}
	impl, ok := s.impl.(interface {
		ExecuteAdmittedFilesystemRead(context.Context, runtime.FilesystemReadAdmission, runtime.FilesystemReadPreparation, *runtime.ReadExecutionReference, runtime.FilesystemReadExecutionAuthority) (*runtime.PrivateReadCapture, error)
	})
	if !ok {
		return reject(ErrCoverageMissing)
	}
	return impl.ExecuteAdmittedFilesystemRead(ctx, admission, wrapped.inner, reference, &serviceReadExecutionAuthority{service: s, inner: authority, expected: admission})
}

func (s *Service) InvalidateFilesystemReadExecutions(conversationID string) error {
	if s == nil || s.impl == nil || conversationID == "" {
		return ErrServiceClosed
	}
	impl, ok := s.impl.(interface{ InvalidateFilesystemReadExecutions(string) error })
	if !ok {
		return nil // unsupported read execution has no worker state to invalidate
	}
	return impl.InvalidateFilesystemReadExecutions(conversationID)
}

type serviceReadExecutionAuthority struct {
	service  *Service
	inner    runtime.FilesystemReadExecutionAuthority
	expected runtime.FilesystemReadAdmission
	inside   bool
}

func (a *serviceReadExecutionAuthority) Claim() error { return a.inner.Claim() }

func (a *serviceReadExecutionAuthority) WithCurrent(operation func() error) error {
	if a.service == nil || operation == nil {
		return runtime.ErrFilesystemRootStale
	}
	return a.inner.WithCurrent(func() error {
		a.service.rootMu.Lock()
		defer a.service.rootMu.Unlock()
		if a.service.rootClosed || a.service.rootPending || a.service.rootPath != a.expected.Root || a.service.rootConversationID != a.expected.ConversationID || a.service.rootGeneration != a.expected.RootGeneration {
			return runtime.ErrFilesystemRootStale
		}
		impl, ok := a.service.impl.(interface {
			WithFilesystemReadExecutionCurrent(func() error) error
		})
		if !ok {
			return ErrCoverageMissing
		}
		a.inside = true
		defer func() { a.inside = false }()
		return impl.WithFilesystemReadExecutionCurrent(operation)
	})
}

func (a *serviceReadExecutionAuthority) AuthorizationBinding() string {
	return a.inner.AuthorizationBinding()
}

func (a *serviceReadExecutionAuthority) Stage(admission runtime.FilesystemReadAdmission, result runtime.FilesystemReadExecutionResult) (*runtime.PrivateReadCapture, error) {
	if a.service == nil || !a.inside || !sameFilesystemReadAdmission(a.expected, admission) {
		return nil, runtime.ErrFilesystemRootStale
	}
	return a.inner.Stage(admission, result)
}

func sameFilesystemReadAdmission(a, b runtime.FilesystemReadAdmission) bool {
	return a.ServiceID == b.ServiceID && a.ConversationID == b.ConversationID && a.TurnID == b.TurnID && a.CallID == b.CallID && a.Root == b.Root && a.Deadline.Equal(b.Deadline) && a.AdmissionBinding == b.AdmissionBinding && a.Target == b.Target && a.Class == b.Class && a.Identity == b.Identity && a.RootGeneration == b.RootGeneration && a.PolicyRevision == b.PolicyRevision && a.CoverageRevision == b.CoverageRevision && a.Destination == b.Destination
}

type serviceAdmissionPreparation struct {
	service  *Service
	inner    runtime.FilesystemReadPreparation
	expected runtime.FilesystemReadAdmission
}

func (p *serviceAdmissionPreparation) Metadata() runtime.FilesystemReadAdmission {
	if p == nil || p.inner == nil {
		return runtime.FilesystemReadAdmission{}
	}
	return p.inner.Metadata()
}

func (p *serviceAdmissionPreparation) WithAuthorityCommit(ctx context.Context, commit func() error) error {
	if p == nil || p.service == nil || p.inner == nil || ctx == nil {
		return ErrServiceClosed
	}
	p.service.rootMu.Lock()
	defer p.service.rootMu.Unlock()
	if p.service.rootClosed || p.service.rootPending || p.service.rootPath != p.expected.Root || p.service.rootConversationID != p.expected.ConversationID || p.service.rootGeneration != p.expected.RootGeneration {
		return runtime.ErrFilesystemRootStale
	}
	return p.inner.WithAuthorityCommit(ctx, commit)
}

func (p *serviceAdmissionPreparation) Close(ctx context.Context) error {
	if p == nil || p.inner == nil {
		return nil
	}
	return p.inner.Close(ctx)
}

func exactReadTarget(raw json.RawMessage) (string, error) {
	var arguments struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil || arguments.Path == "" {
		return "", errors.New("read request must name one exact file path")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return "", errors.New("read request arguments contain trailing data")
	}
	if !filepath.IsAbs(arguments.Path) || filepath.Clean(arguments.Path) != arguments.Path || strings.ContainsRune(arguments.Path, '\x00') {
		return "", errors.New("read target must be a clean absolute path")
	}
	return arguments.Path, nil
}
