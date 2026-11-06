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
	"syscall"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
)

const defaultBubblewrapPath = "/usr/bin/bwrap"

type linuxImplementation struct {
	mu         sync.Mutex
	registry   *readbrokerstudy.Registry
	launcher   readbrokerstudy.WorkerLauncher
	session    readbrokerstudy.HostCoordinator
	inventory  *readbrokerstudy.CoverageInventory
	backendErr error
	revision   uint64
	owners     map[string]OwnerCoverage
	closed     bool
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrServiceClosed
	}
	coverage, exists := s.owners[owner]
	if !exists || coverage.State != OwnerRequired && coverage.State != OwnerRegistered {
		return errors.New("owner is not required by the trusted chat composition")
	}
	resourceClass, ok := hostResourceClass(class)
	if !ok || class != coverage.Class {
		return errors.New("protected owner class does not match the trusted chat composition")
	}
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
	if s.session != nil {
		err = s.session.RegisterRotation(owner, registrations)
	} else {
		err = s.registry.RegisterRotation(owner, registrations)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	view := s.registry.View()
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
	defer s.mu.Unlock()
	s.closed = true
	if s.session != nil {
		err := s.session.Close(ctx)
		if errors.Is(err, readbrokerstudy.ErrCleanupPending) || ctx.Err() != nil {
			// Keep the coordinator (and its pins/cleanup obligations) reachable
			// so shutdown can be retried after an incomplete join.
			return errors.Join(err, ctx.Err())
		}
		s.session = nil
		s.launcher = nil
		s.registry = nil
		return err
	}
	var closeErr error
	if s.launcher != nil {
		closeErr = errors.Join(closeErr, s.launcher.Close())
		s.launcher = nil
	}
	if s.registry != nil {
		closeErr = errors.Join(closeErr, s.registry.Close())
		s.registry = nil
	}
	return closeErr
}

func sortOwnerCoverage(owners []OwnerCoverage) {
	sort.Slice(owners, func(i, j int) bool { return owners[i].Owner < owners[j].Owner })
}

var _ implementation = (*linuxImplementation)(nil)
