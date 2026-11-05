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
	"errors"
	"fmt"
	"sort"
	"sync"
)

var ErrInventoryIncomplete = errors.New("protected owner inventory is incomplete")

// CoverageInventory records declared owners separately from their actual
// route/object registrations. An empty inventory is never complete. The
// caller that constructs requirements is trusted composition code, not a
// model or tool argument.
type CoverageInventory struct {
	mu            sync.RWMutex
	requirements  []OwnerRequirement
	declarations  map[string]OwnerRequirement
	pending       map[string]bool
	retired       map[string]bool
	registrations map[string]map[OwnerRegistration]map[FileIdentity]struct{}
	revision      uint64
	closed        bool
}

// NewSyntheticStudyInventory is the sole complete profile used by the
// feasibility command. Its four fixture owners are a bounded synthetic
// universe, never a declaration that the corresponding production owners are
// integrated.
func NewSyntheticStudyInventory() *CoverageInventory {
	copyOf := SyntheticStudyRequirements()
	return &CoverageInventory{requirements: copyOf, declarations: make(map[string]OwnerRequirement), pending: make(map[string]bool), retired: make(map[string]bool), registrations: make(map[string]map[OwnerRegistration]map[FileIdentity]struct{})}
}

// SyntheticStudyRequirements returns a detached copy of the only owner
// universe accepted by this experimental profile.
func SyntheticStudyRequirements() []OwnerRequirement {
	return []OwnerRequirement{
		{Owner: "fixture-auth-inventory", Class: ClassCredential, Evidence: OwnerEvidenceFixture, Lifecycle: LifecyclePinnedReplacement},
		{Owner: "fixture-settings-inventory", Class: ClassSettings, Evidence: OwnerEvidenceFixture, Lifecycle: LifecyclePinnedReplacement},
		{Owner: "fixture-token-db-inventory", Class: ClassTokenDatabase, Evidence: OwnerEvidenceFixture, Lifecycle: LifecyclePinnedReplacement},
		{Owner: "fixture-provider-inventory", Class: ClassProvider, Evidence: OwnerEvidenceFixture, Lifecycle: LifecycleReadOnlySnapshot},
	}
}

func (i *CoverageInventory) declareOwner(requirement OwnerRequirement) error {
	if i == nil {
		return ErrInventoryIncomplete
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrSessionClosed
	}
	var required *OwnerRequirement
	for index := range i.requirements {
		candidate := &i.requirements[index]
		if candidate.Owner == requirement.Owner {
			required = candidate
			break
		}
	}
	if required == nil || *required != requirement {
		return errors.New("owner declaration does not match trusted inventory requirements")
	}
	if prior, exists := i.declarations[requirement.Owner]; exists {
		if prior != requirement {
			return errors.New("owner declaration changed without inventory replacement")
		}
		return nil
	}
	i.declarations[requirement.Owner] = requirement
	i.revision++
	return nil
}

func (i *CoverageInventory) retireOwner(owner string) error {
	if i == nil {
		return ErrInventoryIncomplete
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrSessionClosed
	}
	if _, exists := i.declarations[owner]; !exists {
		return ErrInventoryIncomplete
	}
	if i.retired[owner] {
		return nil
	}
	i.retired[owner] = true
	i.revision++
	return nil
}

func (i *CoverageInventory) beginOwnerUpdate(owner string) bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return false
	}
	if _, required := i.declarations[owner]; !required || i.retired[owner] || i.pending[owner] {
		return required && i.pending[owner]
	}
	i.pending[owner] = true
	i.revision++
	return true
}

func (i *CoverageInventory) completeOwnerUpdate(owner string) bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || !i.pending[owner] || i.retired[owner] {
		return false
	}
	if len(i.registrations[owner]) == 0 {
		return false
	}
	delete(i.pending, owner)
	i.revision++
	return true
}

func (i *CoverageInventory) observe(view PolicyView) bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return false
	}
	changed := false
	for _, entry := range view.Entries {
		requirement, declared := i.declarations[entry.Owner]
		if !declared || requirement.Class != entry.Class || entry.Directory {
			continue
		}
		if i.registrations[entry.Owner] == nil {
			i.registrations[entry.Owner] = make(map[OwnerRegistration]map[FileIdentity]struct{})
		}
		registration := OwnerRegistration{Owner: entry.Owner, Class: entry.Class, Path: entry.Path, Directory: entry.Directory}
		if i.registrations[entry.Owner][registration] == nil {
			i.registrations[entry.Owner][registration] = make(map[FileIdentity]struct{})
		}
		if _, exists := i.registrations[entry.Owner][registration][entry.Identity]; !exists {
			i.registrations[entry.Owner][registration][entry.Identity] = struct{}{}
			changed = true
		}
	}
	if changed {
		i.revision++
	}
	return changed
}

func (i *CoverageInventory) Snapshot() CoverageSnapshot {
	if i == nil {
		return CoverageSnapshot{Complete: false, Requirements: []OwnerCoverage{{UnavailableReason: "trusted inventory requirements were not supplied"}}}
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	snapshot := CoverageSnapshot{Revision: i.revision, Complete: true, Requirements: make([]OwnerCoverage, 0, len(i.requirements))}
	if i.closed || len(i.requirements) == 0 {
		snapshot.Complete = false
	}
	for _, requirement := range i.requirements {
		coverage := OwnerCoverage{Owner: requirement.Owner, Class: requirement.Class, Evidence: requirement.Evidence, Lifecycle: requirement.Lifecycle}
		declaration, declared := i.declarations[requirement.Owner]
		coverage.Declared = declared && declaration == requirement
		coverage.PendingUpdate = i.pending[requirement.Owner]
		coverage.Retired = i.retired[requirement.Owner]
		for registration, identities := range i.registrations[requirement.Owner] {
			if registration.Owner != requirement.Owner || registration.Class != requirement.Class {
				continue
			}
			coverage.RegisteredRoutes++
			for identity := range identities {
				if identity != (FileIdentity{}) {
					coverage.PinnedIdentities++
				}
			}
		}
		switch {
		case i.closed:
			coverage.UnavailableReason = "inventory has been closed"
		case !coverage.Declared:
			coverage.UnavailableReason = "required owner lifecycle is undeclared"
		case coverage.PendingUpdate:
			coverage.UnavailableReason = "owner registration/update is incomplete"
		case coverage.Retired:
			coverage.UnavailableReason = "required owner lifecycle ended; a fresh inventory and owner registration are required"
		case coverage.RegisteredRoutes == 0:
			coverage.UnavailableReason = "required owner has no effective route registration"
		default:
			coverage.Complete = true
		}
		if !coverage.Complete {
			snapshot.Complete = false
		}
		snapshot.Requirements = append(snapshot.Requirements, coverage)
	}
	return snapshot
}

func (i *CoverageInventory) close() {
	if i == nil {
		return
	}
	i.mu.Lock()
	if !i.closed {
		i.closed = true
		i.revision++
	}
	i.mu.Unlock()
}

func (s CoverageSnapshot) AdmissionError() error {
	if s.Complete {
		return nil
	}
	missing := make([]string, 0)
	for _, owner := range s.Requirements {
		if !owner.Complete {
			missing = append(missing, owner.Owner+":"+owner.UnavailableReason)
		}
	}
	if len(missing) == 0 {
		missing = append(missing, "inventory requirements are unknown")
	}
	sort.Strings(missing)
	return fmt.Errorf("%w (%v)", ErrInventoryIncomplete, missing)
}
