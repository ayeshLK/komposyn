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
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestApprovalCoordinatorCombinesRequirementsAndBindsOnceGrant(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{}, store)
	root := filepath.Join(t.TempDir(), "file.txt")
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: root, Operation: OperationWrite}
	request := Request{ConversationID: "conversation", CallID: "call", Requirements: []Requirement{
		{Kind: RequirementAction, Key: "a", ToolID: "apply_patch", Operation: OperationWrite, ActionKey: "patch-digest"},
		{Kind: RequirementResource, Key: "r", ToolID: "apply_patch", Operation: OperationWrite, Resource: &resource},
	}}
	pending, decision, err := coordinator.Begin(request)
	if err != nil || pending.ID == "" || len(decision.Missing) != 2 {
		t.Fatalf("pending=%+v decision=%+v err=%v", pending, decision, err)
	}
	if _, _, err := coordinator.Begin(request); !errors.Is(err, ErrApprovalPending) {
		t.Fatalf("second begin error=%v", err)
	}
	// Mutating the returned projection cannot change the request awaiting approval.
	pending.Request.Requirements[0].ActionKey = "different"
	response, err := coordinator.Respond(pending.ID, ApprovalOnce, "")
	if err != nil || response.Outcome != ApprovalGranted || !response.Decision.Ready() {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	changedAction := cloneRequest(request)
	changedAction.Requirements[0].ActionKey = "changed-patch-digest"
	if got := coordinator.evaluator.Evaluate(changedAction); got.Ready() {
		t.Fatalf("once approval covered a changed action: %+v", got)
	}
	changedResource := cloneRequest(request)
	changedResource.Requirements[1].Resource.Target = filepath.Join(t.TempDir(), "unapproved.txt")
	if got := coordinator.evaluator.Evaluate(changedResource); got.Ready() {
		t.Fatalf("once approval covered a changed resource target: %+v", got)
	}
	if err := store.ConsumeOnce("conversation", "call", []string{"a", "r"}); err != nil {
		t.Fatal(err)
	}
	if got := coordinator.evaluator.Evaluate(request); got.Ready() {
		t.Fatalf("consumed once permissions were reusable: %+v", got)
	}
}

func TestCheckedApprovalReturnsDetachedG1Evidence(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{}, store)
	target := filepath.Join(t.TempDir(), "exact file")
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: target, Operation: OperationRead}
	request := Request{ConversationID: "conversation", CallID: "call", Binding: "binding-a", Requirements: []Requirement{
		{Kind: RequirementAction, Key: "action", ToolID: "read_file", Operation: OperationRead, ActionKey: "action-a"},
		{Kind: RequirementResource, Key: "disclosure", ToolID: "read_file", Operation: OperationRead, Resource: &resource},
	}, RequireOnceKeys: []string{"disclosure"}}
	pending, decision, err := coordinator.Begin(request)
	if err != nil || pending.ID == "" || decision.Revision != 0 || len(decision.Missing) != 2 {
		t.Fatalf("begin pending=%+v decision=%+v err=%v", pending, decision, err)
	}
	approved, err := coordinator.Respond(pending.ID, ApprovalOnce, "")
	if err != nil || approved.Outcome != ApprovalGranted || !approved.Decision.Ready() {
		t.Fatalf("approval=%+v err=%v", approved, err)
	}
	transition := approved.Transition
	if transition.BeforeRevision != 0 || transition.AfterRevision != 1 || transition.Binding != request.Binding || len(transition.InsertedIDs) != 2 || len(transition.Inserted) != 2 || len(transition.Snapshot) != 2 || !transition.Decision.Ready() || transition.Decision.Revision != 1 {
		t.Fatalf("G1 transaction evidence=%+v", transition)
	}
	transition.Snapshot[0].ID = "caller-mutated"
	transition.Decision.Allowed[0].Key = "caller-mutated"
	if err := store.Add(Grant{Kind: RequirementAction, ID: "later", ConversationID: "conversation", ToolID: "other", Operation: OperationInteract, ActionKey: "later", Lifetime: LifetimeConversation, Origin: "test"}); err != nil {
		t.Fatal(err)
	}
	if transition.AfterRevision != 1 || transition.Decision.Revision != 1 {
		t.Fatalf("later mutation altered immutable transaction revision evidence: %+v", transition)
	}
	if _, grants := store.Snapshot("conversation"); len(grants) != 3 || grants[0].ID == "caller-mutated" {
		t.Fatalf("caller edits to detached evidence mutated the store: %+v", grants)
	}
}

func TestMemoryStoreClonePreservesRevisionButOwnsMutableState(t *testing.T) {
	store := NewMemoryStore()
	grant := Grant{Kind: RequirementAction, ID: "existing", ConversationID: "c", ToolID: "update_plan", Operation: OperationInteract, ActionKey: "plan", Lifetime: LifetimeConversation, Origin: "test"}
	if err := store.Add(grant); err != nil {
		t.Fatal(err)
	}
	clone := store.Clone()
	originalRevision, _ := store.Snapshot("c")
	cloneRevision, cloned := clone.Snapshot("c")
	if originalRevision != cloneRevision || len(cloned) != 1 {
		t.Fatalf("clone revision/state mismatch: original=%d clone=%d grants=%+v", originalRevision, cloneRevision, cloned)
	}
	if err := store.Add(Grant{Kind: RequirementAction, ID: "later", ConversationID: "c", ToolID: "update_plan", Operation: OperationInteract, ActionKey: "later", Lifetime: LifetimeConversation, Origin: "test"}); err != nil {
		t.Fatal(err)
	}
	if revision, grants := clone.Snapshot("c"); revision != cloneRevision || len(grants) != 1 || grants[0].ID != "existing" {
		t.Fatalf("caller mutation changed runtime clone: revision=%d grants=%+v", revision, grants)
	}
}

func TestCheckedApprovalRejectsInterveningMutationWithoutPartialInsert(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{}, store)
	request := Request{ConversationID: "c", CallID: "call", Binding: "bind", Requirements: []Requirement{
		{Kind: RequirementAction, Key: "a", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "command"},
	}}
	pending, _, err := coordinator.Begin(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(Grant{Kind: RequirementAction, ID: "intervening", ConversationID: "c", ToolID: "other", Operation: OperationInteract, ActionKey: "other", Lifetime: LifetimeConversation, Origin: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Respond(pending.ID, ApprovalOnce, ""); !errors.Is(err, ErrApprovalStale) {
		t.Fatalf("stale approval error=%v", err)
	}
	revision, grants := store.Snapshot("c")
	if revision != 1 || len(grants) != 1 || grants[0].ID != "intervening" {
		t.Fatalf("failed checked approval partially inserted: revision=%d grants=%+v", revision, grants)
	}
}

func TestCheckedApprovalRejectsWidenedResourceScope(t *testing.T) {
	store := NewMemoryStore()
	root := t.TempDir()
	target := filepath.Join(root, "approved.txt")
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: target, Operation: OperationRead}
	request := Request{ConversationID: "c", CallID: "call", Binding: "binding", Requirements: []Requirement{{Kind: RequirementResource, Key: "resource", ToolID: "read_file", Operation: OperationRead, Resource: &resource}}, RequireOnceKeys: []string{"resource"}}
	widened := Grant{Kind: RequirementResource, ID: "widened", ConversationID: "c", CallID: "call", RequirementKey: "resource", ToolID: "read_file", Operation: OperationRead, Lifetime: LifetimeOnce, Origin: "user_approval", Binding: "binding", ResourceTarget: target, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: root, Recursive: true, Operations: []Operation{OperationRead}}}
	if _, err := store.ApproveAtRevision(Evaluator{}, request, 0, request.Requirements, []Grant{widened}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("widened scope approval error=%v", err)
	}
	if revision, grants := store.Snapshot("c"); revision != 0 || len(grants) != 0 {
		t.Fatalf("widened approval partially mutated state: revision=%d grants=%+v", revision, grants)
	}
}

func TestCheckedApprovalRacesUnrelatedMutationAtomically(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		store := NewMemoryStore()
		coordinator := NewCoordinator(Evaluator{}, store)
		request := Request{ConversationID: "c", CallID: "call", Binding: "binding", Requirements: []Requirement{{Kind: RequirementAction, Key: "action", ToolID: "read_file", Operation: OperationRead, ActionKey: "read"}}}
		pending, _, err := coordinator.Begin(request)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var response ApprovalResponse
		var respondErr error
		var mutationErr error
		go func() {
			defer wait.Done()
			<-start
			response, respondErr = coordinator.Respond(pending.ID, ApprovalOnce, "")
		}()
		go func() {
			defer wait.Done()
			<-start
			mutationErr = store.Add(Grant{Kind: RequirementAction, ID: "other", ConversationID: "c", ToolID: "other", Operation: OperationInteract, ActionKey: "other", Lifetime: LifetimeConversation, Origin: "test"})
		}()
		close(start)
		wait.Wait()
		if mutationErr != nil {
			t.Fatal(mutationErr)
		}
		_, grants := store.Snapshot("c")
		if respondErr == nil {
			if response.Outcome != ApprovalGranted || !response.Decision.Ready() || len(grants) != 2 {
				t.Fatalf("iteration %d successful approval was not atomic: response=%+v grants=%+v", iteration, response, grants)
			}
		} else if (!errors.Is(respondErr, ErrApprovalStale) && !errors.Is(respondErr, ErrGrantRevisionChanged)) || len(grants) != 1 || grants[0].ID != "other" {
			t.Fatalf("iteration %d stale approval partially committed: response=%+v err=%v grants=%+v", iteration, response, respondErr, grants)
		}
	}
}

func TestRequiredOnceDisclosureOverridesBroadRulesAndConversationGrants(t *testing.T) {
	store := NewMemoryStore()
	root := t.TempDir()
	ordinary := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(root, "ordinary.txt"), Operation: OperationRead}
	source := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(root, "provider-source.jsonl"), Operation: OperationRead}
	request := Request{ConversationID: "c", CallID: "call", Binding: "binding-exact", RequireOnceKeys: []string{"ordinary-disclosure", "source-disclosure"}, Requirements: []Requirement{
		{Kind: RequirementAction, Key: "action", ToolID: "read_file", Operation: OperationRead, ActionKey: "read-action"},
		{Kind: RequirementResource, Key: "ordinary-disclosure", ToolID: "read_file", Operation: OperationRead, SessionAllowed: true, Resource: &ordinary},
		{Kind: RequirementResource, Key: "source-disclosure", ToolID: "read_file", Operation: OperationRead, SessionAllowed: true, Resource: &source},
	}}
	evaluator := Evaluator{Rules: []Rule{
		{Kind: RequirementAction, ToolID: "read_file", Operation: OperationRead, Effect: EffectAllow},
		{Kind: RequirementResource, ToolID: "read_file", Operation: OperationRead, Effect: EffectAllow},
	}}
	if err := store.Add(Grant{Kind: RequirementResource, ID: "broad-session", ConversationID: "c", ToolID: "read_file", Operation: OperationRead, ResourceTarget: root, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: root, Recursive: true, Operations: []Operation{OperationRead}}, Lifetime: LifetimeConversation, Origin: "test"}); err != nil {
		t.Fatal(err)
	}
	decision := (Evaluator{Rules: evaluator.Rules, Grants: store}).Evaluate(request)
	if len(decision.Missing) != 2 || len(decision.Allowed) != 1 || len(decision.Denied) != 0 {
		t.Fatalf("broad allow/session grant bypassed required once disclosure: %+v", decision)
	}
	coordinator := NewCoordinator(evaluator, store)
	pending, _, err := coordinator.Begin(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Respond(pending.ID, ApprovalConversation, ""); !errors.Is(err, ErrSessionNotAllowed) {
		t.Fatalf("conversation lifetime satisfied required-once read: %v", err)
	}
	if _, err := coordinator.Respond(pending.ID, ApprovalOnce, ""); err != nil {
		t.Fatalf("exact once approval failed: %v", err)
	}
	if got := (Evaluator{Rules: evaluator.Rules, Grants: store}).Evaluate(request); !got.Ready() {
		t.Fatalf("exact approval did not satisfy separate requirements: %+v", got)
	}
	changed := cloneRequest(request)
	changed.Binding = "different-object-or-context"
	if got := (Evaluator{Rules: evaluator.Rules, Grants: store}).Evaluate(changed); got.Ready() {
		t.Fatalf("once read grant crossed host binding: %+v", got)
	}
}

func TestConsumeAuthorizedAtRevisionRequiresAndConsumesExactOnceBinding(t *testing.T) {
	store := NewMemoryStore()
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(t.TempDir(), "target"), Operation: OperationRead}
	request := Request{ConversationID: "c", CallID: "call", Binding: "admission-object-a", RequireOnceKeys: []string{"disclosure"}, Requirements: []Requirement{
		{Kind: RequirementResource, Key: "disclosure", ToolID: "read_file", Operation: OperationRead, Resource: &resource},
	}}
	evaluator := Evaluator{Rules: []Rule{{Kind: RequirementResource, ToolID: "read_file", Operation: OperationRead, Effect: EffectAllow}}, Grants: store}
	if err := store.Add(Grant{Kind: RequirementResource, ID: "wrong-binding", ConversationID: "c", CallID: "call", RequirementKey: "disclosure", ToolID: "read_file", Operation: OperationRead, ResourceTarget: resource.Target, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: resource.Target, Operations: []Operation{OperationRead}}, Lifetime: LifetimeOnce, Origin: "test", Binding: "other-object"}); err != nil {
		t.Fatal(err)
	}
	revision, _ := store.Snapshot("c")
	if _, err := store.ConsumeAuthorizedAtRevision(evaluator, request, revision); !errors.Is(err, ErrOnceGrantMissing) {
		t.Fatalf("wrong binding consumed: %v", err)
	}
	_, grants := store.Snapshot("c")
	if len(grants) != 1 || grants[0].ID != "wrong-binding" {
		t.Fatalf("failed consume mutated grants: %+v", grants)
	}
	if err := store.Add(Grant{Kind: RequirementResource, ID: "exact", ConversationID: "c", CallID: "call", RequirementKey: "disclosure", ToolID: "read_file", Operation: OperationRead, ResourceTarget: resource.Target, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: resource.Target, Operations: []Operation{OperationRead}}, Lifetime: LifetimeOnce, Origin: "test", Binding: request.Binding}); err != nil {
		t.Fatal(err)
	}
	revision, _ = store.Snapshot("c")
	transition, err := store.ConsumeAuthorizedAtRevision(evaluator, request, revision)
	if err != nil || transition.BeforeRevision != revision || transition.AfterRevision != revision+1 || len(transition.ConsumedIDs) != 1 || transition.ConsumedIDs[0] != "exact" || len(transition.Consumed) != 1 || transition.Consumed[0] != (GrantEvidence{ID: "exact", RequirementKey: "disclosure"}) || transition.Binding != request.Binding {
		t.Fatalf("consume transition=%+v err=%v", transition, err)
	}
	if _, err := store.ConsumeAuthorizedAtRevision(evaluator, request, revision); !errors.Is(err, ErrGrantRevisionChanged) {
		t.Fatalf("replay with stale revision error=%v", err)
	}
	if got := evaluator.Evaluate(request); got.Ready() {
		t.Fatalf("consumed once grant was restored: %+v", got)
	}
}

func TestCheckedConsumeRacesUnrelatedMutationWithoutPartialConsumption(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		store := NewMemoryStore()
		resource := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(t.TempDir(), "target"), Operation: OperationRead}
		request := Request{ConversationID: "c", CallID: "call", Binding: "exact", RequireOnceKeys: []string{"disclosure"}, Requirements: []Requirement{{Kind: RequirementResource, Key: "disclosure", ToolID: "read_file", Operation: OperationRead, Resource: &resource}}}
		coordinator := NewCoordinator(Evaluator{}, store)
		pending, _, err := coordinator.Begin(request)
		if err != nil {
			t.Fatal(err)
		}
		approved, err := coordinator.Respond(pending.ID, ApprovalOnce, "")
		if err != nil {
			t.Fatal(err)
		}
		evaluator := Evaluator{Grants: store}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var consumption GrantTransition
		var consumeErr, mutationErr error
		go func() {
			defer wait.Done()
			<-start
			consumption, consumeErr = store.ConsumeAuthorizedAtRevision(evaluator, request, approved.Transition.AfterRevision)
		}()
		go func() {
			defer wait.Done()
			<-start
			mutationErr = store.Add(Grant{Kind: RequirementAction, ID: "other", ConversationID: "c", ToolID: "other", Operation: OperationInteract, ActionKey: "other", Lifetime: LifetimeConversation, Origin: "test"})
		}()
		close(start)
		wait.Wait()
		if mutationErr != nil {
			t.Fatal(mutationErr)
		}
		_, grants := store.Snapshot("c")
		if consumeErr == nil {
			if len(consumption.ConsumedIDs) != 1 || len(consumption.ConsumedRequirementKeys) != 1 || len(grants) != 1 || grants[0].ID != "other" {
				t.Fatalf("iteration %d successful consumption evidence/state mismatch: transition=%+v grants=%+v", iteration, consumption, grants)
			}
		} else if !errors.Is(consumeErr, ErrGrantRevisionChanged) || len(grants) != 2 {
			t.Fatalf("iteration %d failed consume partially mutated grants: err=%v grants=%+v", iteration, consumeErr, grants)
		}
	}
}

func TestUnknownOperationsFailClosedEvenWithWildcardAllow(t *testing.T) {
	for _, requirement := range []Requirement{
		{Kind: RequirementAction, Key: "action", ToolID: "apply_patch", Operation: Operation("unknown_operation"), ActionKey: "digest"},
		{Kind: RequirementResource, Key: "resource", ToolID: "read_file", Operation: Operation("unknown_operation"), Resource: &ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(t.TempDir(), "file"), Operation: Operation("unknown_operation")}},
	} {
		decision := (Evaluator{Rules: []Rule{{Kind: requirement.Kind, ToolID: requirement.ToolID, Effect: EffectAllow}}}).Evaluate(Request{Requirements: []Requirement{requirement}})
		if len(decision.Denied) != 1 || decision.Ready() {
			t.Fatalf("unknown operation was authorized: requirement=%+v decision=%+v", requirement, decision)
		}
	}
	store := NewMemoryStore()
	err := store.Add(Grant{Kind: RequirementAction, ID: "unknown", ConversationID: "c", ToolID: "apply_patch", Operation: Operation("unknown_operation"), ActionKey: "digest", Lifetime: LifetimeConversation, Origin: "test"})
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("unknown grant operation error=%v", err)
	}
	resourceScope := &ResourceScope{Kind: ResourceFilesystem, Root: t.TempDir(), Operations: []Operation{Operation("unknown_operation")}}
	err = store.Add(Grant{Kind: RequirementResource, ID: "unknown-resource", ConversationID: "c", ToolID: "read_file", Operation: Operation("unknown_operation"), ResourceTarget: resourceScope.Root, Resource: resourceScope, Lifetime: LifetimeConversation, Origin: "test"})
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("unknown resource grant operation error=%v", err)
	}
}

func TestExplicitAskRequiresAndAcceptsOneTimeApproval(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{Rules: []Rule{{Kind: RequirementAction, ToolID: "read_file", Operation: OperationRead, Effect: EffectAsk}}}, store)
	request := Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{Kind: RequirementAction, Key: "read", ToolID: "read_file", Operation: OperationRead, ActionKey: "exact-read"}}}
	pending, decision, err := coordinator.Begin(request)
	if err != nil || pending.ID == "" || len(decision.Missing) != 1 {
		t.Fatalf("explicit ask did not require approval: pending=%+v decision=%+v err=%v", pending, decision, err)
	}
	response, err := coordinator.Respond(pending.ID, ApprovalOnce, "")
	if err != nil || !response.Decision.Ready() {
		t.Fatalf("explicit ask approval did not authorize exact invocation: response=%+v err=%v", response, err)
	}
}

func TestApprovalCoordinatorSessionScopeAndDeleteRestriction(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{}, store)
	root := t.TempDir()
	read := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(root, "safe.txt"), Operation: OperationRead}
	request := Request{ConversationID: "c", CallID: "one", Requirements: []Requirement{
		{Kind: RequirementResource, Key: "read", ToolID: "read_file", Operation: OperationRead, Resource: &read, SessionAllowed: true},
		{Kind: RequirementAction, Key: "command", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "exact-command", SessionAllowed: true},
	}}
	pending, _, err := coordinator.Begin(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := coordinator.Respond(pending.ID, ApprovalConversation, "")
	if err != nil || !response.Decision.Ready() {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	otherCall := cloneRequest(request)
	otherCall.CallID = "two"
	if got := coordinator.evaluator.Evaluate(otherCall); !got.Ready() {
		t.Fatalf("conversation grants did not cover an allowed exact scope: %+v", got)
	}
	deleteTarget := filepath.Join(root, "remove.txt")
	deleteRequest := Request{ConversationID: "c", CallID: "delete", Requirements: []Requirement{{Kind: RequirementResource, Key: "delete", ToolID: "delete_path", Operation: OperationDelete, Resource: &ResourceRequest{Kind: ResourceFilesystem, Target: deleteTarget, Operation: OperationDelete}, SessionAllowed: true}}}
	deletePending, _, err := coordinator.Begin(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Respond(deletePending.ID, ApprovalConversation, ""); !errors.Is(err, ErrSessionNotAllowed) {
		t.Fatalf("delete session approval error=%v", err)
	}
	if _, err := coordinator.Respond(deletePending.ID, ApprovalOnce, ""); err != nil {
		t.Fatalf("delete once approval failed: %v", err)
	}
}

func TestConversationToolApprovalSupportsOnceAndConversationLifetime(t *testing.T) {
	for _, choice := range []ApprovalChoice{ApprovalOnce, ApprovalConversation} {
		store := NewMemoryStore()
		coordinator := NewCoordinator(Evaluator{}, store)
		resource := ResourceRequest{Kind: ResourceConversation, Target: "opaque-conversation-a", Operation: OperationInteract}
		request := Request{ConversationID: resource.Target, CallID: "call", Requirements: []Requirement{
			{Kind: RequirementAction, Key: "action", ToolID: "request_user_input", Operation: OperationInteract, ActionKey: "request-digest", SessionAllowed: true},
			{Kind: RequirementResource, Key: "conversation", ToolID: "request_user_input", Operation: OperationInteract, Resource: &resource, SessionAllowed: true},
		}}
		pending, _, err := coordinator.Begin(request)
		if err != nil || pending.ID == "" {
			t.Fatalf("begin choice=%s pending=%+v err=%v", choice, pending, err)
		}
		response, err := coordinator.Respond(pending.ID, choice, "")
		if err != nil || response.Outcome != ApprovalGranted || !response.Decision.Ready() {
			t.Fatalf("response choice=%s response=%+v err=%v", choice, response, err)
		}
		if decision := (Evaluator{Grants: store}).Evaluate(request); !decision.Ready() {
			t.Fatalf("approved call not authorized for choice=%s: %+v", choice, decision)
		}
		cross := cloneRequest(request)
		cross.ConversationID = "opaque-conversation-b"
		cross.Requirements[1].Resource.Target = "opaque-conversation-b"
		if decision := (Evaluator{Grants: store}).Evaluate(cross); decision.Ready() {
			t.Fatalf("choice=%s grant crossed conversation: %+v", choice, decision)
		}
		if choice == ApprovalOnce {
			if err := store.ConsumeOnceMatchingAtRevision(request, response.Decision.Revision); err != nil {
				t.Fatalf("consume once grant: %v", err)
			}
			if decision := (Evaluator{Grants: store}).Evaluate(request); decision.Ready() {
				t.Fatalf("once grant remained reusable: %+v", decision)
			}
		}
	}
}

func TestApprovalCoordinatorRejectsStaleRedirectAndClearsPending(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{}, store)
	request := Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{Kind: RequirementAction, Key: "a", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "command"}}}
	pending, _, err := coordinator.Begin(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Respond(pending.ID, ApprovalRedirect, ""); !errors.Is(err, ErrInvalidApproval) {
		t.Fatalf("empty redirect error=%v", err)
	}
	if err := store.Add(Grant{Kind: RequirementAction, ID: "external", ConversationID: "c", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "elsewhere", Lifetime: LifetimeConversation, Origin: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Respond(pending.ID, ApprovalOnce, ""); !errors.Is(err, ErrApprovalStale) {
		t.Fatalf("stale response error=%v", err)
	}
	if _, err := coordinator.Respond(pending.ID, ApprovalCancel, ""); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("stale pending remained available: %v", err)
	}
}

func TestApprovalCoordinatorDenyCancelRedirectAndHeadlessPending(t *testing.T) {
	store := NewMemoryStore()
	coordinator := NewCoordinator(Evaluator{}, store)
	request := Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{Kind: RequirementAction, Key: "a", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "command"}}}
	for _, test := range []struct {
		choice  ApprovalChoice
		text    string
		outcome ApprovalOutcome
	}{
		{ApprovalDeny, "", ApprovalDenied},
		{ApprovalCancel, "", ApprovalCancelled},
		{ApprovalRedirect, "try another action", ApprovalRedirected},
	} {
		pending, _, err := coordinator.Begin(request)
		if err != nil {
			t.Fatal(err)
		}
		response, err := coordinator.Respond(pending.ID, test.choice, test.text)
		if err != nil || response.Outcome != test.outcome {
			t.Fatalf("choice=%s response=%+v err=%v", test.choice, response, err)
		}
		if test.choice == ApprovalDeny && (len(response.Decision.Denied) != 1 || len(response.Decision.Missing) != 0) {
			t.Fatalf("deny must produce a non-executed denied decision: %+v", response.Decision)
		}
		if test.choice == ApprovalCancel && (len(response.Decision.Missing) != 1 || len(response.Decision.Denied) != 0) {
			t.Fatalf("cancel should stop with the request still unapproved: %+v", response.Decision)
		}
	}
	if _, decision, err := coordinator.Begin(request); err != nil || len(decision.Missing) != 1 {
		t.Fatalf("headless request did not remain unauthorized: decision=%+v err=%v", decision, err)
	}
}
