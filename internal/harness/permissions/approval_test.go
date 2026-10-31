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
