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

package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
)

const testFileTool = "fixture_read_text"

func readPermissionRequest(target *ReadTarget, conversationID, callID string) permissions.Request {
	resource := target.ResourceRequest()
	action := permissions.Requirement{Kind: permissions.RequirementAction, Key: "action", ToolID: testFileTool, Operation: permissions.OperationRead, ActionKey: "fixture-read-v1", SessionAllowed: true}
	resourceRequirement := permissions.Requirement{Kind: permissions.RequirementResource, Key: "resource", ToolID: testFileTool, Operation: permissions.OperationRead, Resource: &resource, SessionAllowed: true}
	return permissions.Request{ConversationID: conversationID, CallID: callID, Binding: "fixture-binding", Requirements: []permissions.Requirement{action, resourceRequirement}}
}

func authorizedRead(t *testing.T, target *ReadTarget, request permissions.Request, coordinator *permissions.Coordinator, evaluator permissions.Evaluator, grants *permissions.MemoryStore, choice permissions.ApprovalChoice) (ReadResult, error) {
	t.Helper()
	pending, decision, err := coordinator.Begin(request)
	if err != nil {
		return ReadResult{Outcome: OutcomeNotExecuted}, err
	}
	if len(decision.Denied) != 0 {
		return ReadResult{Outcome: OutcomeNotExecuted}, errors.New("permission denied")
	}
	if !decision.Ready() {
		if choice == "" {
			return ReadResult{Outcome: OutcomeNotExecuted}, nil
		}
		approved, err := coordinator.Respond(pending.ID, choice, "")
		if err != nil {
			return ReadResult{Outcome: OutcomeNotExecuted}, err
		}
		if approved.Outcome != permissions.ApprovalGranted {
			return ReadResult{Outcome: OutcomeNotExecuted}, nil
		}
	}
	return readAfterPermit(target, request, evaluator, grants)
}

func readAfterPermit(target *ReadTarget, request permissions.Request, evaluator permissions.Evaluator, grants *permissions.MemoryStore) (ReadResult, error) {
	fresh := evaluator
	fresh.Grants = grants
	decision := fresh.Evaluate(request)
	if !decision.Ready() {
		return ReadResult{Outcome: OutcomeNotExecuted}, permissions.ErrGrantRevisionChanged
	}
	if err := grants.ConsumeOnceMatchingAtRevision(request, decision.Revision); err != nil {
		return ReadResult{Outcome: OutcomeNotExecuted}, err
	}
	return target.ReadText(context.Background(), MaxReadBytes)
}

func TestLinuxActualReadIsGatedByCentralAuthorization(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	file := filepath.Join(rootPath, "secret.txt")
	if err := os.WriteFile(file, []byte("only after approval"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := root.PrepareRead(file)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	didRead := false
	root.backend.(*linuxRoot).hook = func(stage string) {
		if stage == "read-after-revalidate" {
			didRead = true
		}
	}
	grants := permissions.NewMemoryStore()
	evaluator := permissions.Evaluator{}
	coordinator := permissions.NewCoordinator(evaluator, grants)
	request := readPermissionRequest(target, "conversation", "call")
	pending, decision, err := coordinator.Begin(request)
	if err != nil || decision.Ready() {
		t.Fatalf("read authorization decision=%+v err=%v", decision, err)
	}
	if didRead {
		t.Fatal("filesystem read ran without approval")
	}
	approved, err := coordinator.Respond(pending.ID, permissions.ApprovalOnce, "")
	if err != nil || approved.Outcome != permissions.ApprovalGranted {
		t.Fatalf("explicit read approval=%+v err=%v", approved, err)
	}
	result, err := readAfterPermit(target, request, evaluator, grants)
	if err != nil || result.Outcome != OutcomeSucceeded || result.Text != "only after approval" {
		t.Fatalf("approved actual read result=%+v err=%v", result, err)
	}
	if !didRead {
		t.Fatal("approved filesystem read did not execute")
	}
}

func TestLinuxDeniedRevokedAndChangedTargetsDoNotReachIO(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	first := filepath.Join(rootPath, "first.txt")
	second := filepath.Join(rootPath, "second.txt")
	for name, value := range map[string]string{first: "first", second: "second"} {
		if err := os.WriteFile(name, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	firstTarget, err := root.PrepareRead(first)
	if err != nil {
		t.Fatal(err)
	}
	defer firstTarget.Close()
	request := readPermissionRequest(firstTarget, "conversation", "call")
	deniedRules := []permissions.Rule{{Kind: permissions.RequirementResource, ToolID: testFileTool, Operation: permissions.OperationRead, Effect: permissions.EffectDeny, Resource: &permissions.ResourceScope{Kind: permissions.ResourceFilesystem, Root: rootPath, Recursive: true, Operations: []permissions.Operation{permissions.OperationRead}}}}
	deniedGrants := permissions.NewMemoryStore()
	deniedEvaluator := permissions.Evaluator{Rules: deniedRules, Grants: deniedGrants}
	deniedCoordinator := permissions.NewCoordinator(deniedEvaluator, deniedGrants)
	result, err := authorizedRead(t, firstTarget, request, deniedCoordinator, deniedEvaluator, deniedGrants, permissions.ApprovalOnce)
	if err == nil || result.Outcome != OutcomeNotExecuted {
		t.Fatalf("denied read result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "first" {
		t.Fatalf("denied request changed source: %q err=%v", got, err)
	}

	grants := permissions.NewMemoryStore()
	evaluator := permissions.Evaluator{Grants: grants}
	coordinator := permissions.NewCoordinator(evaluator, grants)
	pending, decision, err := coordinator.Begin(readPermissionRequest(firstTarget, "conversation", "revoked-call"))
	if err != nil || decision.Ready() {
		t.Fatalf("pending request decision=%+v err=%v", decision, err)
	}
	approved, err := coordinator.Respond(pending.ID, permissions.ApprovalOnce, "")
	if err != nil || approved.Outcome != permissions.ApprovalGranted {
		t.Fatalf("approval result=%+v err=%v", approved, err)
	}
	revokedRequest := readPermissionRequest(firstTarget, "conversation", "revoked-call")
	if err := grants.Revoke("conversation", "approval-"+pending.ID+"-resource"); err != nil {
		t.Fatal(err)
	}
	if err := grants.ConsumeOnceMatchingAtRevision(revokedRequest, approved.Decision.Revision); !errors.Is(err, permissions.ErrGrantRevisionChanged) {
		t.Fatalf("revoked grant passed atomic revision check: %v", err)
	}
	if decision := evaluator.Evaluate(revokedRequest); decision.Ready() {
		t.Fatalf("revoked request remained authorized: %+v", decision)
	}

	// A once grant for the first target must not authorize a different target.
	grants = permissions.NewMemoryStore()
	evaluator = permissions.Evaluator{Grants: grants}
	coordinator = permissions.NewCoordinator(evaluator, grants)
	pending, _, err = coordinator.Begin(readPermissionRequest(firstTarget, "conversation", "changed-call"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Respond(pending.ID, permissions.ApprovalOnce, ""); err != nil {
		t.Fatal(err)
	}
	secondTarget, err := root.PrepareRead(second)
	if err != nil {
		t.Fatal(err)
	}
	defer secondTarget.Close()
	changed := readPermissionRequest(secondTarget, "conversation", "changed-call")
	result, err = authorizedRead(t, secondTarget, changed, coordinator, evaluator, grants, "")
	if err != nil || result.Outcome != OutcomeNotExecuted || result.Text != "" {
		t.Fatalf("changed-target read result=%+v err=%v", result, err)
	}
}

func TestLinuxWriteGrantImpliesReadButNotDeleteAndDeleteIsOnceOnly(t *testing.T) {
	_, rootPath := openFixtureRoot(t)
	target := filepath.Join(rootPath, "document.txt")
	writeScope := permissions.ResourceScope{Kind: permissions.ResourceFilesystem, Root: rootPath, Recursive: true, Operations: []permissions.Operation{permissions.OperationWrite}}
	store := permissions.NewMemoryStore()
	if err := store.Add(permissions.Grant{Kind: permissions.RequirementResource, ID: "write-grant", ConversationID: "conversation", ToolID: "file-edit", Operation: permissions.OperationWrite, ResourceTarget: target, Resource: &writeScope, Lifetime: permissions.LifetimeConversation, Origin: "user_approval"}); err != nil {
		t.Fatal(err)
	}
	resourceReq := func(operation permissions.Operation) permissions.Request {
		resource := permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: operation}
		return permissions.Request{ConversationID: "conversation", CallID: string(operation), Requirements: []permissions.Requirement{{Kind: permissions.RequirementResource, Key: "resource", ToolID: "file-edit", Operation: operation, Resource: &resource, SessionAllowed: true}}}
	}
	evaluator := permissions.Evaluator{Grants: store}
	if decision := evaluator.Evaluate(resourceReq(permissions.OperationRead)); !decision.Ready() {
		t.Fatalf("write grant did not imply read: %+v", decision)
	}
	if decision := evaluator.Evaluate(resourceReq(permissions.OperationDelete)); decision.Ready() {
		t.Fatalf("write grant authorized deletion: %+v", decision)
	}

	deleteRequest := permissions.Request{ConversationID: "conversation", CallID: "delete", Requirements: []permissions.Requirement{{Kind: permissions.RequirementResource, Key: "resource", ToolID: "file-edit", Operation: permissions.OperationDelete, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationDelete}, SessionAllowed: false}}}
	coordinator := permissions.NewCoordinator(evaluator, store)
	pending, decision, err := coordinator.Begin(deleteRequest)
	if err != nil || decision.Ready() {
		t.Fatalf("delete default decision=%+v err=%v", decision, err)
	}
	if _, err := coordinator.Respond(pending.ID, permissions.ApprovalConversation, ""); !errors.Is(err, permissions.ErrSessionNotAllowed) {
		t.Fatalf("conversation deletion approval err=%v", err)
	}
}

func TestLinuxAuthorizationScopeIsExactExplicitDirectory(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	scope := root.AuthorizationScope()
	if scope.Kind != permissions.ResourceFilesystem || scope.Root != rootPath || !scope.Recursive {
		t.Fatalf("authorization scope=%+v", scope)
	}
	for _, operation := range []permissions.Operation{permissions.OperationRead, permissions.OperationWrite, permissions.OperationDelete} {
		found := false
		for _, candidate := range scope.Operations {
			found = found || candidate == operation
		}
		if !found {
			t.Errorf("authorization scope omitted %s", operation)
		}
	}
}
