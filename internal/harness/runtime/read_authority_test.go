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

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

func syntheticReadRequest(invocation tools.Invocation, target, admission string) permissions.Request {
	return permissions.Request{
		ConversationID:   invocation.ConversationID,
		CallID:           invocation.ProviderCallID,
		AdmissionBinding: admission,
		RequireOnceKeys:  []string{"resource"},
		Requirements: []permissions.Requirement{
			{Kind: permissions.RequirementAction, Key: "action", ToolID: string(invocation.ToolID), Operation: permissions.OperationRead, ActionKey: "read_file", SessionAllowed: true},
			{Kind: permissions.RequirementResource, Key: "resource", ToolID: string(invocation.ToolID), Operation: permissions.OperationRead, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationRead}, SessionAllowed: true},
		},
	}
}

func approveAndConsumeSyntheticRead(t *testing.T, runtime *toolRuntime, invocation tools.Invocation, target, admission string) (permissions.Request, permissions.GrantTransition, permissions.GrantTransition) {
	t.Helper()
	request, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, target, admission))
	if err != nil {
		t.Fatal(err)
	}
	pending, decision, err := runtime.coordinator.Begin(request)
	if err != nil || len(decision.Missing) == 0 {
		t.Fatalf("begin synthetic approval: decision=%+v err=%v", decision, err)
	}
	approved, err := runtime.coordinator.Respond(pending.ID, permissions.ApprovalOnce, "")
	if err != nil || approved.Outcome != permissions.ApprovalGranted {
		t.Fatalf("approve synthetic read: response=%+v err=%v", approved, err)
	}
	if !runtime.advanceReadAuthorityRevision(approved.Transition, request, true) {
		t.Fatal("runtime rejected its checked approval transition")
	}
	consumed, err := runtime.grants.ConsumeAuthorizedAtRevision(runtime.evaluator, request, approved.Transition.AfterRevision)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.advanceReadAuthorityRevision(consumed, request, false) {
		t.Fatal("runtime rejected its checked consumption transition")
	}
	return request, approved.Transition, consumed
}

func syntheticAuthorityRuntime() *toolRuntime {
	return newToolRuntime(ToolRuntimeConfig{Registry: tools.NewRegistry(), Grants: permissions.NewMemoryStore()})
}

func TestReadBindingIncludesAdmissionAndCanonicalArguments(t *testing.T) {
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/file","limit":12}`)}
	first, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, "/root/file", "admission-object-a"))
	if err != nil {
		t.Fatal(err)
	}
	whitespace := invocation
	whitespace.Arguments = json.RawMessage("{ \"limit\": 12, \"path\": \"/root/file\" }")
	canonical, err := bindAuthorizationRequest(whitespace, syntheticReadRequest(whitespace, "/root/file", "admission-object-a"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Binding != canonical.Binding {
		t.Fatal("semantically identical JSON arguments produced different binding")
	}
	other, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, "/root/file", "admission-object-b"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Binding == other.Binding || first.AdmissionBinding != "admission-object-a" {
		t.Fatal("host admission identity was omitted from canonical binding")
	}
}

func TestReadExecutionAuthorityIsOpaqueOneShotAndInstanceBound(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call-a", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/a"}`)}
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, "/root/a", "admission-a")
	reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ClaimReadExecution(reference, "wrong-binding", "admission-a"); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("wrong authorization binding claim = %v", err)
	}
	if err := runtime.ClaimReadExecution(reference, request.Binding, "admission-b"); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("wrong admission claim = %v", err)
	}
	if err := syntheticAuthorityRuntime().ClaimReadExecution(reference, request.Binding, "admission-a"); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("cross-instance claim = %v", err)
	}
	if err := runtime.ClaimReadExecution(reference, request.Binding, "admission-a"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ClaimReadExecution(reference, request.Binding, "admission-a"); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("duplicate claim = %v", err)
	}
	if runtime.readAuthorityCount() != 1 {
		t.Fatal("claimed operation identity was not retained for revocation")
	}
	if err := runtime.FinishReadExecution(reference); err != nil || runtime.readAuthorityCount() != 0 {
		t.Fatalf("finish reference err=%v retained=%d", err, runtime.readAuthorityCount())
	}
}

func TestReadAuthoritySurvivesOnlyRegisteredSiblingTransitions(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	a := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call-a", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/a"}`)}
	epochA := runtime.readAuthorityEpochSnapshot()
	requestA, approvalA, consumptionA := approveAndConsumeSyntheticRead(t, runtime, a, "/root/a", "admission-a")
	referenceA, err := runtime.issueReadExecutionReference(a, requestA, approvalA, consumptionA, epochA)
	if err != nil {
		t.Fatal(err)
	}
	b := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call-b", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/b"}`)}
	_, _, _ = approveAndConsumeSyntheticRead(t, runtime, b, "/root/b", "admission-b")
	if err := runtime.ClaimReadExecution(referenceA, requestA.Binding, "admission-a"); err != nil {
		t.Fatalf("registered sibling transition invalidated A: %v", err)
	}

	other := syntheticAuthorityRuntime()
	c := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call-c", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/c"}`)}
	epochC := other.readAuthorityEpochSnapshot()
	requestC, approvalC, consumptionC := approveAndConsumeSyntheticRead(t, other, c, "/root/c", "admission-c")
	referenceC, err := other.issueReadExecutionReference(c, requestC, approvalC, consumptionC, epochC)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.grants.Add(permissions.Grant{ID: "untracked", ConversationID: "conversation", ToolID: "update_plan", Kind: permissions.RequirementAction, Operation: permissions.OperationInteract, ActionKey: "update", Lifetime: permissions.LifetimeConversation, Origin: "external"}); err != nil {
		t.Fatal(err)
	}
	d := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call-d", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/d"}`)}
	requestD, err := bindAuthorizationRequest(d, syntheticReadRequest(d, "/root/d", "admission-d"))
	if err != nil {
		t.Fatal(err)
	}
	pendingD, _, err := other.coordinator.Begin(requestD)
	if err != nil {
		t.Fatal(err)
	}
	approvedD, err := other.coordinator.Respond(pendingD.ID, permissions.ApprovalOnce, "")
	if err != nil {
		t.Fatal(err)
	}
	if other.advanceReadAuthorityRevision(approvedD.Transition, requestD, true) {
		t.Fatal("untracked revision gap was accepted as a sibling transition")
	}
	if err := other.ClaimReadExecution(referenceC, requestC.Binding, "admission-c"); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("untracked permission mutation claim = %v", err)
	}
}

func TestReadAuthorityRetiresWhenConversationToolLoopEnds(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/a"}`)}
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, "/root/a", "admission")
	reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	runtime.retireReadAuthorities("conversation")
	if runtime.readAuthorityCount() != 0 {
		t.Fatal("conversation authority remained after tool-loop retirement")
	}
	if err := runtime.ClaimReadExecution(reference, request.Binding, "admission"); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("retired authority claim = %v", err)
	}
}

func TestRuntimeGateOrdersInvalidationBeforeApprovalAndConsumption(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call-gated", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/a"}`)}
	request, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, "/root/a", "admission-gated"))
	if err != nil {
		t.Fatal(err)
	}
	epoch := runtime.readAuthorityEpochSnapshot()
	pending, _, err := runtime.beginApproval(request, epoch)
	if err != nil {
		t.Fatal(err)
	}
	responseResult := make(chan error, 1)
	runtime.readAuthorityMu.Lock()
	go func() {
		_, err := runtime.respondApproval(pending.ID, permissions.ApprovalOnce, "", request, true, epoch)
		responseResult <- err
	}()
	runtime.invalidateReadAuthorityLocked()
	runtime.readAuthorityMu.Unlock()
	if err := <-responseResult; !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("approval after invalidation = %v", err)
	}
	if _, grants := runtime.grants.Snapshot(invocation.ConversationID); len(grants) != 0 {
		t.Fatalf("stale response inserted grants: %+v", grants)
	}
	runtime.coordinator.CancelConversation(invocation.ConversationID)

	// Start a second exact approval. Invalidation ordered ahead of consume must
	// leave the once grant untouched and prevent a later execution reference.
	epoch = runtime.readAuthorityEpochSnapshot()
	pending, _, err = runtime.beginApproval(request, epoch)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := runtime.respondApproval(pending.ID, permissions.ApprovalOnce, "", request, true, epoch)
	if err != nil {
		t.Fatal(err)
	}
	consumedResult := make(chan error, 1)
	runtime.readAuthorityMu.Lock()
	go func() {
		_, err := runtime.consumePermission(request, approved.Transition.AfterRevision, epoch, true)
		consumedResult <- err
	}()
	runtime.invalidateReadAuthorityLocked()
	runtime.readAuthorityMu.Unlock()
	if err := <-consumedResult; !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("consume after invalidation = %v", err)
	}
	_, grants := runtime.grants.Snapshot(invocation.ConversationID)
	if len(grants) != len(request.Requirements) {
		t.Fatalf("invalidated consume partially consumed grants: %+v", grants)
	}
}

func TestChatClearAndRevokeInvalidateConsumedReadAuthority(t *testing.T) {
	store := permissions.NewMemoryStore()
	runtime := newToolRuntime(ToolRuntimeConfig{Registry: tools.NewRegistry(), Grants: store})
	store = runtime.grants
	chat := &Chat{toolRuntime: runtime, conversationID: "conversation"}
	pending, _, err := runtime.beginApproval(permissions.Request{ConversationID: chat.conversationID, CallID: "pending", Requirements: []permissions.Requirement{{Kind: permissions.RequirementAction, Key: "plan", ToolID: "update_plan", Operation: permissions.OperationInteract, ActionKey: "plan"}}}, runtime.readAuthorityEpochSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if chat.ClearToolGrants() != 0 {
		t.Fatal("empty clear unexpectedly removed a grant")
	}
	if _, err := runtime.coordinator.Respond(pending.ID, permissions.ApprovalOnce, ""); !errors.Is(err, permissions.ErrApprovalNotFound) {
		t.Fatalf("empty clear did not cancel pending response: %v", err)
	}
	invocation := tools.Invocation{ConversationID: chat.conversationID, TurnID: "turn", ProviderCallID: "call-clear", ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/a"}`)}
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, "/root/a", "admission-a")
	reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if count := chat.ClearToolGrants(); count != 0 || runtime.readAuthorityCount() != 0 {
		t.Fatalf("empty clear count=%d retained=%d", count, runtime.readAuthorityCount())
	}
	if err := chat.ClaimReadExecution(reference, request.Binding, request.AdmissionBinding); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("clear left consumed authority claimable: %v", err)
	}

	invocation.ProviderCallID = "call-revoke"
	epoch = runtime.readAuthorityEpochSnapshot()
	request, approval, consumption = approveAndConsumeSyntheticRead(t, runtime, invocation, "/root/a", "admission-b")
	reference, err = runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(permissions.Grant{ID: "grant-to-revoke", ConversationID: chat.conversationID, ToolID: "update_plan", Kind: permissions.RequirementAction, Operation: permissions.OperationInteract, ActionKey: "update", Lifetime: permissions.LifetimeConversation, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := chat.RevokeToolGrant("grant-to-revoke"); err != nil || runtime.readAuthorityCount() != 0 {
		t.Fatalf("revoke err=%v retained=%d", err, runtime.readAuthorityCount())
	}
	if err := chat.ClaimReadExecution(reference, request.Binding, request.AdmissionBinding); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("grant revocation left consumed authority claimable: %v", err)
	}

	invocation.ProviderCallID = "call-operation-revoke"
	epoch = runtime.readAuthorityEpochSnapshot()
	request, approval, consumption = approveAndConsumeSyntheticRead(t, runtime, invocation, "/root/a", "admission-c")
	reference, err = runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ClaimReadExecution(reference, request.Binding, request.AdmissionBinding); err != nil {
		t.Fatal(err)
	}
	if err := chat.RevokeReadExecution(reference); err != nil {
		t.Fatalf("revoke consumed operation by owned identity: %v", err)
	}
	if err := chat.ClaimReadExecution(reference, request.Binding, request.AdmissionBinding); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("revoked consumed operation remained claimable: %v", err)
	}
}

func TestChatLifecycleInvalidatesReadAuthority(t *testing.T) {
	for _, lifecycle := range []string{"cancel", "root", "reset", "shutdown"} {
		t.Run(lifecycle, func(t *testing.T) {
			runtime := syntheticAuthorityRuntime()
			chat := &Chat{toolRuntime: runtime, conversationID: "conversation"}
			invocation := tools.Invocation{ConversationID: chat.conversationID, TurnID: "turn", ProviderCallID: "call-" + lifecycle, ToolID: tools.ID("read_file"), Arguments: json.RawMessage(`{"path":"/root/a"}`)}
			epoch := runtime.readAuthorityEpochSnapshot()
			request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, "/root/a", "admission-"+lifecycle)
			reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
			if err != nil {
				t.Fatal(err)
			}
			switch lifecycle {
			case "cancel":
				chat.Cancel()
			case "root":
				chat.mu.Lock()
				chat.changeFilesystemRootLocked("/explicit/root", FilesystemRootSelected)
				chat.mu.Unlock()
			case "reset":
				pending, _, err := runtime.coordinator.Begin(permissions.Request{ConversationID: chat.conversationID, CallID: "late", Requirements: []permissions.Requirement{{Kind: permissions.RequirementAction, Key: "plan", ToolID: "update_plan", Operation: permissions.OperationInteract, ActionKey: "plan"}}})
				if err != nil {
					t.Fatal(err)
				}
				chat.mu.Lock()
				chat.clearConversationLocked(FilesystemRootConversationReset)
				chat.mu.Unlock()
				if _, err := runtime.coordinator.Respond(pending.ID, permissions.ApprovalOnce, ""); !errors.Is(err, permissions.ErrApprovalNotFound) {
					t.Fatalf("late approval after reset = %v", err)
				}
			case "shutdown":
				if err := chat.CloseContext(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if err := runtime.ClaimReadExecution(reference, request.Binding, "admission-"+lifecycle); !errors.Is(err, errReadAuthorityInvalid) {
				t.Fatalf("stale authority claim = %v", err)
			}
		})
	}
}
