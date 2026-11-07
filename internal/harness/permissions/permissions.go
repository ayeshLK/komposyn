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

// Package permissions owns in-memory authorization policy for the experimental
// chat tool runtime. It has no UI, provider, persistence, or execution
// enforcement dependency. Resource targets passed here must already have been
// resolved by a trusted host adapter; this package does not prevent symlink or
// time-of-check/time-of-use races.
package permissions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Effect string

const (
	EffectDeny  Effect = "deny"
	EffectAsk   Effect = "ask"
	EffectAllow Effect = "allow"
)

type Operation string

const (
	OperationRead     Operation = "read"
	OperationWrite    Operation = "write"
	OperationDelete   Operation = "delete"
	OperationExecute  Operation = "execute"
	OperationNetwork  Operation = "network"
	OperationInteract Operation = "interact"
)

type ResourceKind string

const (
	ResourceFilesystem   ResourceKind = "filesystem"
	ResourceNetwork      ResourceKind = "network"
	ResourceConversation ResourceKind = "conversation"
)

type RequirementKind string

const (
	RequirementAction   RequirementKind = "action"
	RequirementResource RequirementKind = "resource"
)

type ResourceRequest struct {
	Kind      ResourceKind
	Target    string
	Operation Operation
}

// ResourceScope is a trusted grant/rule scope. For filesystem scopes Root and
// request Target must be canonical absolute paths supplied by a trusted host
// resolver. Network scopes currently match one exact HTTPS origin; network
// address/redirect enforcement is a separate required layer.
type ResourceScope struct {
	Kind       ResourceKind
	Root       string
	Recursive  bool
	Operations []Operation
}

type Requirement struct {
	Kind           RequirementKind
	Key            string
	ToolID         string
	Description    string
	Operation      Operation
	ActionKey      string
	Resource       *ResourceRequest
	SessionAllowed bool
}

type Request struct {
	ConversationID string
	CallID         string
	// Binding is an opaque digest of the immutable invocation and captured
	// authorization context. It is not a substitute for re-preparing that
	// context immediately before dispatch.
	Binding string
	// AdmissionBinding is the trusted host's canonical digest for admission,
	// classification, destination and freshness context. Runtime includes it in
	// Binding instead of replacing it with an unrelated invocation hash.
	AdmissionBinding string
	Requirements     []Requirement
	// RequireOnceKeys is set by trusted runtime policy for requirements that
	// need an invocation-bound disclosure grant even when broader rules or
	// conversation grants would otherwise allow them. It is not a model/UI
	// permission control; the runtime recomputes it for reserved read calls.
	RequireOnceKeys []string
}

type Rule struct {
	Kind      RequirementKind
	ToolID    string
	Operation Operation
	Effect    Effect
	Resource  *ResourceScope
}

type Lifetime string

const (
	LifetimeOnce         Lifetime = "once"
	LifetimeConversation Lifetime = "conversation"
)

type Grant struct {
	Kind              RequirementKind
	ID                string
	ConversationID    string
	CallID            string
	RequirementKey    string
	ToolID            string
	ActionDescription string
	Operation         Operation
	ActionKey         string
	ResourceTarget    string
	Resource          *ResourceScope
	Lifetime          Lifetime
	Origin            string
	// Binding binds a once grant to the complete trusted request context. It is
	// empty for conversation grants, whose existing scope semantics remain.
	Binding string
}

type Decision struct {
	Allowed  []Requirement
	Missing  []Requirement
	Denied   []Requirement
	Revision uint64
}

func (d Decision) Ready() bool { return len(d.Missing) == 0 && len(d.Denied) == 0 }

type GrantStore interface {
	Snapshot(conversationID string) (uint64, []Grant)
}

type Evaluator struct {
	Rules  []Rule
	Grants GrantStore
}

func (e Evaluator) Evaluate(request Request) Decision {
	decision := Decision{}
	var grants []Grant
	if e.Grants != nil {
		decision.Revision, grants = e.Grants.Snapshot(request.ConversationID)
	}
	return e.evaluateSnapshot(request, decision.Revision, grants)
}

func (e Evaluator) evaluateSnapshot(request Request, revision uint64, grants []Grant) Decision {
	decision := Decision{Revision: revision}
	keyCounts := make(map[string]int, len(request.Requirements))
	for _, requirement := range request.Requirements {
		keyCounts[requirement.Key]++
	}
	requireOnce := make(map[string]struct{}, len(request.RequireOnceKeys))
	for _, key := range request.RequireOnceKeys {
		if key == "" || keyCounts[key] != 1 {
			decision.Denied = cloneRequirements(request.Requirements)
			return decision
		}
		if _, duplicate := requireOnce[key]; duplicate {
			decision.Denied = cloneRequirements(request.Requirements)
			return decision
		}
		requireOnce[key] = struct{}{}
	}
	for _, requirement := range request.Requirements {
		if !validRequirement(requirement) || keyCounts[requirement.Key] != 1 || requirement.Resource != nil && requirement.Resource.Kind == ResourceConversation && requirement.Resource.Target != request.ConversationID {
			decision.Denied = append(decision.Denied, requirement)
			continue
		}
		effect := e.effectFor(requirement)
		if effect == EffectDeny {
			decision.Denied = append(decision.Denied, requirement)
			continue
		}
		if _, exactOnce := requireOnce[requirement.Key]; exactOnce {
			if onceGrantCovers(grants, request, requirement) {
				decision.Allowed = append(decision.Allowed, requirement)
			} else {
				decision.Missing = append(decision.Missing, requirement)
			}
			continue
		}
		switch effect {
		case EffectAsk:
			if onceGrantCovers(grants, request, requirement) {
				decision.Allowed = append(decision.Allowed, requirement)
			} else {
				decision.Missing = append(decision.Missing, requirement)
			}
		case EffectAllow:
			decision.Allowed = append(decision.Allowed, requirement)
		default:
			if grantCovers(grants, request, requirement) {
				decision.Allowed = append(decision.Allowed, requirement)
			} else {
				decision.Missing = append(decision.Missing, requirement)
			}
		}
	}
	return decision
}

func (e Evaluator) effectFor(requirement Requirement) Effect {
	var deny, ask, allow bool
	for _, rule := range e.Rules {
		if rule.Kind != requirement.Kind || rule.ToolID != "" && rule.ToolID != requirement.ToolID || !operationCovers(rule.Operation, requirement.Operation, requirement.Kind == RequirementResource) {
			continue
		}
		if rule.Resource != nil && (requirement.Resource == nil || !scopeCovers(*rule.Resource, *requirement.Resource)) {
			continue
		}
		switch rule.Effect {
		case EffectDeny:
			deny = true
		case EffectAsk:
			ask = true
		case EffectAllow:
			allow = true
		}
	}
	if deny {
		return EffectDeny
	}
	if ask {
		return EffectAsk
	}
	if allow {
		return EffectAllow
	}
	return ""
}

func grantCovers(grants []Grant, request Request, requirement Requirement) bool {
	for _, grant := range grants {
		if grant.ConversationID != request.ConversationID || grant.ToolID != requirement.ToolID || grant.Kind != requirement.Kind {
			continue
		}
		switch grant.Lifetime {
		case LifetimeOnce:
			if onceGrantMatches(grant, request, requirement) {
				return true
			}
		case LifetimeConversation:
			if !requirement.SessionAllowed {
				continue
			}
			if requirement.Kind == RequirementResource && grant.Resource != nil && requirement.Resource != nil && scopeCovers(*grant.Resource, *requirement.Resource) {
				if !operationCovers(grant.Operation, requirement.Operation, true) {
					continue
				}
				return true
			}
			if requirement.Kind == RequirementAction && grant.Operation == requirement.Operation && grant.ActionKey != "" && grant.ActionKey == requirement.ActionKey {
				return true
			}
		}
	}
	return false
}

func onceGrantCovers(grants []Grant, request Request, requirement Requirement) bool {
	for _, grant := range grants {
		if grant.ConversationID == request.ConversationID && grant.ToolID == requirement.ToolID && grant.Kind == requirement.Kind && grant.Lifetime == LifetimeOnce && onceGrantMatches(grant, request, requirement) {
			return true
		}
	}
	return false
}

func onceGrantMatches(grant Grant, request Request, requirement Requirement) bool {
	if grant.Operation != requirement.Operation || grant.CallID != request.CallID || grant.RequirementKey == "" || grant.RequirementKey != requirement.Key || grant.Binding != request.Binding {
		return false
	}
	if requirement.Kind == RequirementAction {
		return grant.ActionKey != "" && grant.ActionKey == requirement.ActionKey
	}
	return requirement.Resource != nil && grant.Resource != nil && grant.ResourceTarget == requirement.Resource.Target && grant.Resource.Kind == requirement.Resource.Kind && scopeCovers(*grant.Resource, *requirement.Resource)
}

var (
	ErrInvalidGrant         = errors.New("invalid permission grant")
	ErrGrantNotFound        = errors.New("permission grant not found")
	ErrOnceGrantMissing     = errors.New("once grant is missing or already consumed")
	ErrGrantRevisionChanged = errors.New("permission grants changed after authorization")
)

// MemoryStore is an injected in-memory grant store. It must only be handed to
// the trusted permission coordinator, never to a tool implementation or model.
type MemoryStore struct {
	mu       sync.RWMutex
	revision uint64
	grants   map[string]Grant
}

// GrantTransition is detached evidence for one checked store transaction.
// Mutating its slices cannot alter the store or a runtime authority record.
type GrantTransition struct {
	BeforeRevision          uint64
	AfterRevision           uint64
	InsertedIDs             []string
	InsertedRequirementKeys []string
	Inserted                []GrantEvidence
	ConsumedIDs             []string
	ConsumedRequirementKeys []string
	Consumed                []GrantEvidence
	Snapshot                []Grant
	Decision                Decision
	Binding                 string
}

// GrantEvidence correlates one store ID with the exact requirement key that
// created or consumed it within a checked transition.
type GrantEvidence struct {
	ID             string
	RequirementKey string
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{grants: make(map[string]Grant)} }

// Clone returns an independent in-memory grant snapshot while preserving its
// global revision. Runtime composition uses this to take ownership of the
// store state instead of retaining a caller-mutable store pointer.
func (s *MemoryStore) Clone() *MemoryStore {
	copy := NewMemoryStore()
	if s == nil {
		return copy
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy.revision = s.revision
	for id, grant := range s.grants {
		copy.grants[id] = cloneGrant(grant)
	}
	return copy
}

func (s *MemoryStore) Add(grant Grant) error {
	return s.AddMany([]Grant{grant})
}

// AddMany validates and inserts all grants atomically.
func (s *MemoryStore) AddMany(grants []Grant) error {
	if s == nil || len(grants) == 0 {
		return ErrInvalidGrant
	}
	validated := make([]Grant, len(grants))
	ids := make(map[string]struct{}, len(grants))
	for i, grant := range grants {
		validatedGrant, err := validateGrant(grant)
		if err != nil {
			return err
		}
		if _, duplicate := ids[grant.ID]; duplicate {
			return ErrInvalidGrant
		}
		ids[grant.ID] = struct{}{}
		validated[i] = validatedGrant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, grant := range validated {
		if _, exists := s.grants[grant.ID]; exists {
			return ErrInvalidGrant
		}
	}
	for _, grant := range validated {
		s.grants[grant.ID] = grant
	}
	s.revision++
	return nil
}

func validateGrant(grant Grant) (Grant, error) {
	if grant.ID == "" || grant.ConversationID == "" || grant.ToolID == "" || !validOperation(grant.Operation) || grant.Origin == "" || (grant.Kind != RequirementAction && grant.Kind != RequirementResource) {
		return Grant{}, ErrInvalidGrant
	}
	if grant.Kind == RequirementAction && (grant.Resource != nil || grant.ResourceTarget != "" || grant.ActionKey == "") || grant.Kind == RequirementResource && (grant.Resource == nil || grant.ResourceTarget == "" || grant.ActionKey != "" || !validScope(*grant.Resource) || !containsOperation(grant.Resource.Operations, grant.Operation) || !validResourceRequest(ResourceRequest{Kind: grant.Resource.Kind, Target: grant.ResourceTarget, Operation: grant.Operation}) || !scopeCovers(*grant.Resource, ResourceRequest{Kind: grant.Resource.Kind, Target: grant.ResourceTarget, Operation: grant.Operation}) || grant.Resource.Kind == ResourceConversation && (grant.ConversationID != grant.ResourceTarget || grant.Resource.Root != grant.ConversationID)) {
		return Grant{}, ErrInvalidGrant
	}
	if grant.Lifetime == LifetimeOnce {
		if grant.CallID == "" || grant.RequirementKey == "" {
			return Grant{}, ErrInvalidGrant
		}
	} else if grant.Lifetime == LifetimeConversation {
		if grant.Operation == OperationDelete || grant.Kind == RequirementAction && grant.ActionKey == "" || grant.Kind == RequirementResource && (grant.Operation == OperationExecute || containsOperation(grant.Resource.Operations, OperationDelete)) || grant.Binding != "" {
			return Grant{}, ErrInvalidGrant
		}
	} else {
		return Grant{}, ErrInvalidGrant
	}
	return cloneGrant(grant), nil
}

func validRequirement(requirement Requirement) bool {
	if requirement.Key == "" || requirement.ToolID == "" || !validOperation(requirement.Operation) {
		return false
	}
	switch requirement.Kind {
	case RequirementAction:
		return requirement.Resource == nil && requirement.ActionKey != ""
	case RequirementResource:
		return requirement.Resource != nil && requirement.ActionKey == "" && requirement.Resource.Operation == requirement.Operation && validResourceRequest(*requirement.Resource)
	default:
		return false
	}
}

func validResourceRequest(request ResourceRequest) bool {
	if !validOperation(request.Operation) {
		return false
	}
	switch request.Kind {
	case ResourceFilesystem:
		return request.Operation != OperationNetwork && request.Operation != OperationInteract && filepath.IsAbs(request.Target) && filepath.Clean(request.Target) == request.Target
	case ResourceNetwork:
		if request.Operation != OperationNetwork {
			return false
		}
		_, err := normalizeOrigin(request.Target)
		return err == nil
	case ResourceConversation:
		return request.Operation == OperationInteract && request.Target != ""
	default:
		return false
	}
}

func validOperation(operation Operation) bool {
	switch operation {
	case OperationRead, OperationWrite, OperationDelete, OperationExecute, OperationNetwork, OperationInteract:
		return true
	default:
		return false
	}
}

func (s *MemoryStore) Snapshot(conversationID string) (uint64, []Grant) {
	if s == nil {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision, s.snapshotConversationLocked(conversationID)
}

func (s *MemoryStore) snapshotConversationLocked(conversationID string) []Grant {
	items := make([]Grant, 0, len(s.grants))
	for _, grant := range s.grants {
		if grant.ConversationID == conversationID {
			items = append(items, cloneGrant(grant))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// EvaluateAtRevision evaluates the immutable request against exactly the
// expected store revision. It is used for non-grant approval outcomes so a
// stale dialog cannot be accepted after an intervening mutation.
func (s *MemoryStore) EvaluateAtRevision(evaluator Evaluator, request Request, expectedRevision uint64, expectedMissing []Requirement) (Decision, error) {
	if s == nil || request.ConversationID == "" || len(request.Requirements) == 0 {
		return Decision{}, ErrApprovalStale
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.revision != expectedRevision {
		return Decision{}, ErrGrantRevisionChanged
	}
	decision := evaluator.evaluateSnapshot(request, s.revision, s.snapshotConversationLocked(request.ConversationID))
	if len(decision.Denied) > 0 || !sameRequirementSet(decision.Missing, expectedMissing) {
		return Decision{}, ErrGrantRevisionChanged
	}
	return cloneDecision(decision), nil
}

// ApproveAtRevision validates and inserts exactly the grants corresponding
// to the immutable pending requirements while holding the store lock. The
// returned G1 snapshot/decision is captured in the same transaction.
func (s *MemoryStore) ApproveAtRevision(evaluator Evaluator, request Request, expectedRevision uint64, expectedMissing []Requirement, grants []Grant) (GrantTransition, error) {
	if s == nil || request.ConversationID == "" || request.CallID == "" || len(request.Requirements) == 0 || len(expectedMissing) == 0 || len(grants) != len(expectedMissing) {
		return GrantTransition{}, ErrInvalidApproval
	}
	validated := make([]Grant, len(grants))
	ids := make(map[string]struct{}, len(grants))
	byKey := make(map[string]Grant, len(grants))
	for i, grant := range grants {
		copy, err := validateGrant(grant)
		if err != nil {
			return GrantTransition{}, err
		}
		if _, duplicate := ids[copy.ID]; duplicate {
			return GrantTransition{}, ErrInvalidGrant
		}
		ids[copy.ID] = struct{}{}
		if _, duplicate := byKey[copy.RequirementKey]; duplicate {
			return GrantTransition{}, ErrInvalidGrant
		}
		byKey[copy.RequirementKey] = copy
		validated[i] = copy
	}
	for _, requirement := range expectedMissing {
		grant, ok := byKey[requirement.Key]
		if !ok || !grantMatchesApproval(grant, request, requirement) {
			return GrantTransition{}, ErrInvalidGrant
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != expectedRevision {
		return GrantTransition{}, ErrGrantRevisionChanged
	}
	before := s.revision
	current := s.snapshotConversationLocked(request.ConversationID)
	decision := evaluator.evaluateSnapshot(request, before, current)
	if len(decision.Denied) > 0 || !sameRequirementSet(decision.Missing, expectedMissing) {
		return GrantTransition{}, ErrApprovalStale
	}
	for _, grant := range validated {
		if _, exists := s.grants[grant.ID]; exists {
			return GrantTransition{}, ErrInvalidGrant
		}
	}
	projected := append(append([]Grant(nil), current...), validated...)
	post := evaluator.evaluateSnapshot(request, before+1, projected)
	if !post.Ready() {
		return GrantTransition{}, ErrInvalidApproval
	}
	for _, grant := range validated {
		s.grants[grant.ID] = grant
	}
	s.revision++
	return GrantTransition{
		BeforeRevision:          before,
		AfterRevision:           s.revision,
		InsertedIDs:             grantIDs(validated),
		InsertedRequirementKeys: requirementKeys(validated),
		Inserted:                grantEvidence(validated),
		Snapshot:                s.snapshotConversationLocked(request.ConversationID),
		Decision:                cloneDecision(post),
		Binding:                 request.Binding,
	}, nil
}

func grantMatchesApproval(grant Grant, request Request, requirement Requirement) bool {
	if grant.ConversationID != request.ConversationID || grant.CallID != request.CallID || grant.RequirementKey != requirement.Key || grant.ToolID != requirement.ToolID || grant.Kind != requirement.Kind || grant.Operation != requirement.Operation {
		return false
	}
	if grant.Lifetime == LifetimeOnce && grant.Binding != request.Binding || grant.Lifetime == LifetimeConversation && (grant.Binding != "" || containsString(request.RequireOnceKeys, requirement.Key)) {
		return false
	}
	if requirement.Kind == RequirementAction {
		return grant.ActionKey == requirement.ActionKey
	}
	if requirement.Resource == nil || grant.Resource == nil || grant.ResourceTarget != requirement.Resource.Target {
		return false
	}
	expected, err := exactScope(*requirement.Resource)
	return err == nil && grant.Resource.Kind == expected.Kind && grant.Resource.Root == expected.Root && grant.Resource.Recursive == expected.Recursive && sameOperations(grant.Resource.Operations, expected.Operations)
}

func sameOperations(a, b []Operation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ConsumeAuthorizedAtRevision checks authorization and consumes every exact
// matching once grant atomically at the expected revision. Required-once keys
// cannot be satisfied by allow rules or conversation grants.
func (s *MemoryStore) ConsumeAuthorizedAtRevision(evaluator Evaluator, request Request, expectedRevision uint64) (GrantTransition, error) {
	if s == nil || request.ConversationID == "" || request.CallID == "" || len(request.Requirements) == 0 {
		return GrantTransition{}, ErrOnceGrantMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != expectedRevision {
		return GrantTransition{}, ErrGrantRevisionChanged
	}
	before := s.revision
	decision := evaluator.evaluateSnapshot(request, before, s.snapshotConversationLocked(request.ConversationID))
	if !decision.Ready() {
		return GrantTransition{}, ErrOnceGrantMissing
	}
	required := make(map[string]struct{}, len(request.RequireOnceKeys))
	for _, key := range request.RequireOnceKeys {
		if key == "" {
			return GrantTransition{}, ErrOnceGrantMissing
		}
		if _, duplicate := required[key]; duplicate {
			return GrantTransition{}, ErrOnceGrantMissing
		}
		required[key] = struct{}{}
	}
	consumed := make([]string, 0, len(request.Requirements))
	consumedKeys := make([]string, 0, len(request.Requirements))
	consumedEvidence := make([]GrantEvidence, 0, len(request.Requirements))
	for _, requirement := range request.Requirements {
		matches := make([]string, 0, 1)
		for id, grant := range s.grants {
			if grant.Lifetime == LifetimeOnce && grant.ConversationID == request.ConversationID && grant.CallID == request.CallID && grant.ToolID == requirement.ToolID && grant.Kind == requirement.Kind && onceGrantMatches(grant, request, requirement) {
				matches = append(matches, id)
			}
		}
		if len(matches) > 1 {
			return GrantTransition{}, ErrOnceGrantMissing
		}
		if _, requiredOnce := required[requirement.Key]; requiredOnce && len(matches) != 1 {
			return GrantTransition{}, ErrOnceGrantMissing
		}
		consumed = append(consumed, matches...)
		if len(matches) != 0 {
			consumedKeys = append(consumedKeys, requirement.Key)
			consumedEvidence = append(consumedEvidence, GrantEvidence{ID: matches[0], RequirementKey: requirement.Key})
		}
	}
	for key := range required {
		if !hasRequirementKey(request.Requirements, key) {
			return GrantTransition{}, ErrOnceGrantMissing
		}
	}
	if len(consumed) != 0 {
		for _, id := range consumed {
			delete(s.grants, id)
		}
		s.revision++
	}
	return GrantTransition{
		BeforeRevision:          before,
		AfterRevision:           s.revision,
		ConsumedIDs:             append([]string(nil), consumed...),
		ConsumedRequirementKeys: append([]string(nil), consumedKeys...),
		Consumed:                append([]GrantEvidence(nil), consumedEvidence...),
		Snapshot:                s.snapshotConversationLocked(request.ConversationID),
		Decision:                cloneDecision(decision),
		Binding:                 request.Binding,
	}, nil
}

func requirementKeys(grants []Grant) []string {
	keys := make([]string, 0, len(grants))
	for _, grant := range grants {
		keys = append(keys, grant.RequirementKey)
	}
	sort.Strings(keys)
	return keys
}

func grantEvidence(grants []Grant) []GrantEvidence {
	evidence := make([]GrantEvidence, 0, len(grants))
	for _, grant := range grants {
		evidence = append(evidence, GrantEvidence{ID: grant.ID, RequirementKey: grant.RequirementKey})
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	return evidence
}

func sameRequirementSet(a, b []Requirement) bool {
	if len(a) != len(b) {
		return false
	}
	matched := make([]bool, len(b))
	for _, left := range a {
		found := false
		for i, right := range b {
			if !matched[i] && sameRequirements([]Requirement{left}, []Requirement{right}) {
				matched[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func grantIDs(grants []Grant) []string {
	ids := make([]string, 0, len(grants))
	for _, grant := range grants {
		ids = append(ids, grant.ID)
	}
	sort.Strings(ids)
	return ids
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasRequirementKey(requirements []Requirement, key string) bool {
	for _, requirement := range requirements {
		if requirement.Key == key {
			return true
		}
	}
	return false
}

func (s *MemoryStore) Revoke(conversationID, id string) error {
	if s == nil {
		return ErrGrantNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.grants[id]
	if !ok || grant.ConversationID != conversationID {
		return ErrGrantNotFound
	}
	delete(s.grants, id)
	s.revision++
	return nil
}

func (s *MemoryStore) Clear(conversationID string) {
	_ = s.ClearCount(conversationID)
}

// ClearCount atomically removes a conversation's grants and returns the exact
// number removed. Callers that own pending authority can serialize this under
// their outer lifecycle gate, including the empty-store case.
func (s *MemoryStore) ClearCount(conversationID string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, grant := range s.grants {
		if grant.ConversationID == conversationID {
			delete(s.grants, id)
			removed++
		}
	}
	if removed > 0 {
		s.revision++
	}
	return removed
}

// ConsumeOnce atomically removes the once grants matching every supplied
// requirement key for one call. The trusted coordinator invokes this only
// after combined authorization succeeds and immediately before dispatch.
func (s *MemoryStore) ConsumeOnce(conversationID, callID string, requirementKeys []string) error {
	if s == nil || conversationID == "" || callID == "" || len(requirementKeys) == 0 {
		return ErrOnceGrantMissing
	}
	wanted := make(map[string]struct{}, len(requirementKeys))
	for _, key := range requirementKeys {
		if key == "" {
			return ErrOnceGrantMissing
		}
		if _, duplicate := wanted[key]; duplicate {
			return ErrOnceGrantMissing
		}
		wanted[key] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := make(map[string]string, len(wanted))
	for id, grant := range s.grants {
		if grant.ConversationID == conversationID && grant.CallID == callID && grant.Lifetime == LifetimeOnce {
			if _, ok := wanted[grant.RequirementKey]; ok {
				if _, duplicate := found[grant.RequirementKey]; duplicate {
					return ErrOnceGrantMissing
				}
				found[grant.RequirementKey] = id
			}
		}
	}
	if len(found) != len(wanted) {
		return ErrOnceGrantMissing
	}
	for _, id := range found {
		delete(s.grants, id)
	}
	s.revision++
	return nil
}

// ConsumeOnceMatchingAtRevision removes any once grants that cover the
// supplied requirements only if the grant store still has the revision
// evaluated by the caller. Requirements already authorized by rules or
// conversation grants need no once grant and are left unchanged. Revision
// validation and once-grant consumption happen atomically under the store lock.
func (s *MemoryStore) ConsumeOnceMatchingAtRevision(request Request, expectedRevision uint64) error {
	if s == nil || request.ConversationID == "" || request.CallID == "" || len(request.Requirements) == 0 {
		return ErrOnceGrantMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != expectedRevision {
		return ErrGrantRevisionChanged
	}
	consumed := make([]string, 0, len(request.Requirements))
	for _, requirement := range request.Requirements {
		matches := []string{}
		for id, grant := range s.grants {
			if grant.Lifetime == LifetimeOnce && grant.ConversationID == request.ConversationID && grant.CallID == request.CallID && grant.ToolID == requirement.ToolID && grant.Kind == requirement.Kind && onceGrantMatches(grant, request, requirement) {
				matches = append(matches, id)
			}
		}
		if len(matches) > 1 {
			return ErrOnceGrantMissing
		}
		consumed = append(consumed, matches...)
	}
	if len(consumed) > 0 {
		for _, id := range consumed {
			delete(s.grants, id)
		}
		s.revision++
	}
	return nil
}

func cloneGrant(grant Grant) Grant {
	if grant.Resource != nil {
		resource := *grant.Resource
		resource.Operations = append([]Operation(nil), resource.Operations...)
		grant.Resource = &resource
	}
	return grant
}

func validScope(scope ResourceScope) bool {
	if len(scope.Operations) == 0 || scope.Root == "" {
		return false
	}
	for _, operation := range scope.Operations {
		if !validOperation(operation) {
			return false
		}
	}
	switch scope.Kind {
	case ResourceFilesystem:
		return filepath.IsAbs(scope.Root) && filepath.Clean(scope.Root) == scope.Root && !containsOperation(scope.Operations, OperationNetwork) && !containsOperation(scope.Operations, OperationInteract)
	case ResourceNetwork:
		_, err := normalizeOrigin(scope.Root)
		return err == nil && isNetworkOrigin(scope.Root) && !scope.Recursive && len(scope.Operations) == 1 && scope.Operations[0] == OperationNetwork
	case ResourceConversation:
		return !scope.Recursive && len(scope.Operations) == 1 && scope.Operations[0] == OperationInteract
	default:
		return false
	}
}

func scopeCovers(scope ResourceScope, request ResourceRequest) bool {
	if scope.Kind != request.Kind || !containsOperation(scope.Operations, request.Operation) {
		return false
	}
	switch scope.Kind {
	case ResourceFilesystem:
		if !filepath.IsAbs(scope.Root) || filepath.Clean(scope.Root) != scope.Root || !filepath.IsAbs(request.Target) || filepath.Clean(request.Target) != request.Target {
			return false
		}
		rel, err := filepath.Rel(scope.Root, request.Target)
		if err != nil {
			return false
		}
		if rel == "." {
			return true
		}
		return scope.Recursive && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	case ResourceNetwork:
		root, err := normalizeOrigin(scope.Root)
		if err != nil || !isNetworkOrigin(scope.Root) || scope.Recursive {
			return false
		}
		target, err := normalizeOrigin(request.Target)
		return err == nil && root == target
	case ResourceConversation:
		return !scope.Recursive && scope.Root != "" && scope.Root == request.Target && request.Operation == OperationInteract
	default:
		return false
	}
}

func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", fmt.Errorf("invalid network origin")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(host, port), nil
}

func isNetworkOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u != nil && u.Scheme == "https" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}

func containsOperation(operations []Operation, requested Operation) bool {
	for _, operation := range operations {
		if operation == requested || operation == OperationWrite && requested == OperationRead {
			return true
		}
	}
	return false
}

func operationCovers(granted, requested Operation, resource bool) bool {
	return granted == "" || granted == requested || resource && granted == OperationWrite && requested == OperationRead
}

// CommandActionKey hashes the exact executable, argument vector, working
// directory, and requested execution authority. It is suitable for matching a
// conversation grant; it does not enforce command behavior or sandbox access.
func CommandActionKey(executable string, arguments []string, workingDirectory string, authority []string) (string, error) {
	if executable == "" || !filepath.IsAbs(workingDirectory) || filepath.Clean(workingDirectory) != workingDirectory {
		return "", errors.New("command identity requires executable and canonical absolute working directory")
	}
	permissions := append([]string(nil), authority...)
	sort.Strings(permissions)
	canonical, err := json.Marshal(struct {
		Executable         string   `json:"executable"`
		Arguments          []string `json:"arguments"`
		WorkingDirectory   string   `json:"working_directory"`
		RequestedAuthority []string `json:"requested_authority"`
	}{executable, append([]string(nil), arguments...), workingDirectory, permissions})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
