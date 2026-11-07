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

package readbrokerstudy

type OwnerEvidence string

const OwnerEvidenceFixture OwnerEvidence = "synthetic_fixture"
const OwnerEvidenceComposition OwnerEvidence = "trusted_chat_composition"

type OwnerLifecycle string

const (
	LifecyclePinnedReplacement OwnerLifecycle = "pinned_before_sensitive_write_and_rotation"
	LifecycleReadOnlySnapshot  OwnerLifecycle = "read_only_open_identity_registration"
)

// OwnerRequirement is supplied by trusted study composition. It describes a
// bounded universe; it does not imply machine-wide discovery or production
// owner integration.
type OwnerRequirement struct {
	Owner     string         `json:"owner"`
	Class     ResourceClass  `json:"class"`
	Evidence  OwnerEvidence  `json:"evidence"`
	Lifecycle OwnerLifecycle `json:"lifecycle"`
}

type OwnerCoverage struct {
	Owner             string         `json:"owner"`
	Class             ResourceClass  `json:"class"`
	Evidence          OwnerEvidence  `json:"evidence"`
	Lifecycle         OwnerLifecycle `json:"lifecycle"`
	Declared          bool           `json:"declared"`
	PendingUpdate     bool           `json:"pending_update"`
	Retired           bool           `json:"retired"`
	RegisteredRoutes  int            `json:"registered_routes"`
	PinnedIdentities  int            `json:"pinned_identities"`
	Complete          bool           `json:"complete"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`
}

type CoverageSnapshot struct {
	Revision     uint64          `json:"revision"`
	Complete     bool            `json:"complete"`
	Requirements []OwnerCoverage `json:"requirements"`
}
