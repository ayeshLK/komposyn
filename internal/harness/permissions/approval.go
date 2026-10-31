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

package permissions

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"sync"
)

type ApprovalChoice string

const (
	ApprovalOnce         ApprovalChoice = "once"
	ApprovalConversation ApprovalChoice = "conversation"
	ApprovalRedirect     ApprovalChoice = "redirect"
	ApprovalDeny         ApprovalChoice = "deny"
	ApprovalCancel       ApprovalChoice = "cancel"
)

type ApprovalOutcome string

const (
	ApprovalGranted    ApprovalOutcome = "granted"
	ApprovalRedirected ApprovalOutcome = "redirected"
	ApprovalDenied     ApprovalOutcome = "denied"
	ApprovalCancelled  ApprovalOutcome = "cancelled"
)

type PendingApproval struct {
	ID       string
	Request  Request
	Decision Decision
}

type ApprovalResponse struct {
	Outcome      ApprovalOutcome
	Decision     Decision
	RedirectText string
}

var (
	ErrApprovalPending   = errors.New("an approval is already pending")
	ErrApprovalNotFound  = errors.New("approval is no longer pending")
	ErrApprovalStale     = errors.New("approval is stale because permissions changed")
	ErrSessionNotAllowed = errors.New("conversation approval is not allowed for every requested permission")
	ErrInvalidApproval   = errors.New("invalid approval response")
)

// Coordinator binds one combined user decision to an immutable request and
// grant revision. It creates grants only; the runtime must revalidate and
// consume once grants immediately before execution.
type Coordinator struct {
	mu        sync.Mutex
	evaluator Evaluator
	store     *MemoryStore
	pending   map[string]PendingApproval
}

func NewCoordinator(evaluator Evaluator, store *MemoryStore) *Coordinator {
	evaluator.Grants = store
	evaluator.Rules = cloneRules(evaluator.Rules)
	return &Coordinator{evaluator: evaluator, store: store, pending: make(map[string]PendingApproval)}
}

func (c *Coordinator) Begin(request Request) (PendingApproval, Decision, error) {
	if c == nil || c.store == nil || request.ConversationID == "" || request.CallID == "" || len(request.Requirements) == 0 {
		return PendingApproval{}, Decision{}, ErrInvalidApproval
	}
	decision := c.evaluator.Evaluate(request)
	if len(decision.Denied) > 0 || len(decision.Missing) == 0 {
		return PendingApproval{}, decision, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		return PendingApproval{}, decision, ErrApprovalPending
	}
	id, err := randomID()
	if err != nil {
		return PendingApproval{}, decision, err
	}
	pending := PendingApproval{ID: id, Request: cloneRequest(request), Decision: cloneDecision(decision)}
	c.pending[id] = pending
	return clonePending(pending), decision, nil
}

func (c *Coordinator) Respond(id string, choice ApprovalChoice, redirectText string) (ApprovalResponse, error) {
	if c == nil || c.store == nil || id == "" {
		return ApprovalResponse{}, ErrApprovalNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, ok := c.pending[id]
	if !ok {
		return ApprovalResponse{}, ErrApprovalNotFound
	}
	if choice != ApprovalOnce && choice != ApprovalConversation && choice != ApprovalRedirect && choice != ApprovalDeny && choice != ApprovalCancel {
		return ApprovalResponse{}, ErrInvalidApproval
	}
	if choice == ApprovalRedirect && redirectText == "" || choice != ApprovalRedirect && redirectText != "" {
		return ApprovalResponse{}, ErrInvalidApproval
	}
	revision, _ := c.store.Snapshot(pending.Request.ConversationID)
	if revision != pending.Decision.Revision {
		delete(c.pending, id)
		return ApprovalResponse{}, ErrApprovalStale
	}
	decision := c.evaluator.Evaluate(pending.Request)
	if decision.Revision != pending.Decision.Revision || len(decision.Denied) > 0 || !sameRequirements(decision.Missing, pending.Decision.Missing) {
		delete(c.pending, id)
		return ApprovalResponse{}, ErrApprovalStale
	}
	response := ApprovalResponse{Decision: decision}
	switch choice {
	case ApprovalDeny:
		response.Outcome = ApprovalDenied
		response.Decision.Denied = append(response.Decision.Denied, response.Decision.Missing...)
		response.Decision.Missing = nil
	case ApprovalCancel:
		response.Outcome = ApprovalCancelled
	case ApprovalRedirect:
		response.Outcome = ApprovalRedirected
		response.RedirectText = redirectText
	case ApprovalOnce, ApprovalConversation:
		grants, err := grantsForApproval(pending, choice)
		if err != nil {
			return ApprovalResponse{}, err
		}
		if err := c.store.AddMany(grants); err != nil {
			return ApprovalResponse{}, err
		}
		response.Outcome = ApprovalGranted
		response.Decision = c.evaluator.Evaluate(pending.Request)
	}
	delete(c.pending, id)
	return response, nil
}

func (c *Coordinator) CancelConversation(conversationID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, pending := range c.pending {
		if pending.Request.ConversationID == conversationID {
			delete(c.pending, id)
		}
	}
}

func grantsForApproval(pending PendingApproval, choice ApprovalChoice) ([]Grant, error) {
	grants := make([]Grant, 0, len(pending.Decision.Missing))
	for _, requirement := range pending.Decision.Missing {
		lifetime := LifetimeOnce
		if choice == ApprovalConversation {
			if !requirement.SessionAllowed || requirement.Operation == OperationDelete {
				return nil, ErrSessionNotAllowed
			}
			lifetime = LifetimeConversation
		}
		grant := Grant{
			Kind:           requirement.Kind,
			ID:             "approval-" + pending.ID + "-" + requirement.Key,
			ConversationID: pending.Request.ConversationID,
			CallID:         pending.Request.CallID,
			RequirementKey: requirement.Key,
			ToolID:         requirement.ToolID,
			Operation:      requirement.Operation,
			ActionKey:      requirement.ActionKey,
			Lifetime:       lifetime,
			Origin:         "user_approval",
		}
		if requirement.Kind == RequirementResource {
			scope, err := exactScope(*requirement.Resource)
			if err != nil {
				return nil, err
			}
			grant.Resource = &scope
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

func exactScope(request ResourceRequest) (ResourceScope, error) {
	root := request.Target
	if request.Kind == ResourceNetwork {
		parsed, err := url.Parse(request.Target)
		if err != nil {
			return ResourceScope{}, ErrInvalidApproval
		}
		root, err = normalizeOrigin(parsed.Scheme + "://" + parsed.Host)
		if err != nil {
			return ResourceScope{}, ErrInvalidApproval
		}
	}
	return ResourceScope{Kind: request.Kind, Root: root, Operations: []Operation{request.Operation}}, nil
}

func sameRequirements(a, b []Requirement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].Key != b[i].Key || a[i].ToolID != b[i].ToolID || a[i].Operation != b[i].Operation || a[i].ActionKey != b[i].ActionKey || !sameResource(a[i].Resource, b[i].Resource) {
			return false
		}
	}
	return true
}

func sameResource(a, b *ResourceRequest) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func cloneRequest(request Request) Request {
	request.Requirements = append([]Requirement(nil), request.Requirements...)
	for i := range request.Requirements {
		if request.Requirements[i].Resource != nil {
			resource := *request.Requirements[i].Resource
			request.Requirements[i].Resource = &resource
		}
	}
	return request
}

func cloneDecision(decision Decision) Decision {
	decision.Allowed = cloneRequirements(decision.Allowed)
	decision.Missing = cloneRequirements(decision.Missing)
	decision.Denied = cloneRequirements(decision.Denied)
	return decision
}

func cloneRequirements(requirements []Requirement) []Requirement {
	return cloneRequest(Request{Requirements: requirements}).Requirements
}

func clonePending(pending PendingApproval) PendingApproval {
	pending.Request = cloneRequest(pending.Request)
	pending.Decision = cloneDecision(pending.Decision)
	return pending
}

func cloneRules(rules []Rule) []Rule {
	cloned := append([]Rule(nil), rules...)
	for i := range cloned {
		if cloned[i].Resource != nil {
			resource := *cloned[i].Resource
			resource.Operations = append([]Operation(nil), resource.Operations...)
			cloned[i].Resource = &resource
		}
	}
	return cloned
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
