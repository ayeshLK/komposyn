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
	"path/filepath"
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

func syntheticReadInvocation(t *testing.T, conversationID, turnID, callID, target string) tools.Invocation {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	return tools.Invocation{ConversationID: conversationID, TurnID: turnID, ProviderCallID: callID, ToolID: tools.ID("read_file"), Arguments: arguments}
}

func TestReadBindingIncludesAdmissionAndCanonicalArguments(t *testing.T) {
	target := filepath.Join(t.TempDir(), "file")
	invocation := syntheticReadInvocation(t, "conversation", "turn", "call", target)
	arguments, err := json.Marshal(map[string]any{"path": target, "limit": 12})
	if err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = arguments
	first, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, target, "admission-object-a"))
	if err != nil {
		t.Fatal(err)
	}
	whitespace := invocation
	pathJSON, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	whitespace.Arguments = append([]byte(`{ "limit": 12, "path": `), pathJSON...)
	whitespace.Arguments = append(whitespace.Arguments, []byte(" }")...)
	canonical, err := bindAuthorizationRequest(whitespace, syntheticReadRequest(whitespace, target, "admission-object-a"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Binding != canonical.Binding {
		t.Fatal("semantically identical JSON arguments produced different binding")
	}
	other, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, target, "admission-object-b"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Binding == other.Binding || first.AdmissionBinding != "admission-object-a" {
		t.Fatal("host admission identity was omitted from canonical binding")
	}
}

func TestReadExecutionAuthorityIsOpaqueOneShotAndInstanceBound(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	target := filepath.Join(t.TempDir(), "a")
	invocation := syntheticReadInvocation(t, "conversation", "turn", "call-a", target)
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-a")
	reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("same consumed transaction issued a second reference: %v", err)
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
	runtime.retireReadAuthorities(invocation.ConversationID)
	if _, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("retired transaction was issued again: %v", err)
	}
}

func TestPrivateReadCaptureRequiresCurrentClaimAndScrubsOnDiscard(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	target := filepath.Join(t.TempDir(), "file")
	invocation := syntheticReadInvocation(t, "conversation", "turn", "capture-call", target)
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-capture")
	reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ClaimReadExecution(reference, request.Binding, request.AdmissionBinding); err != nil {
		t.Fatal(err)
	}
	admission := FilesystemReadAdmission{ServiceID: "service", ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, CallID: invocation.ProviderCallID, AdmissionBinding: request.AdmissionBinding, Identity: "dev:ino:type", RootGeneration: 3, PolicyRevision: 5, CoverageRevision: 7, Destination: "provider/model/profile"}
	content := []byte("synthetic private bytes")
	result := FilesystemReadExecutionResult{Outcome: tools.OutcomeSucceeded, Bytes: content, ServiceID: admission.ServiceID, AdmissionBinding: admission.AdmissionBinding, AuthorizationBinding: request.Binding, Identity: admission.Identity, RootGeneration: admission.RootGeneration, PolicyRevision: admission.PolicyRevision, CoverageRevision: admission.CoverageRevision, Destination: admission.Destination, ReadStarted: true, WorkerJoined: true, CapabilitiesRetired: true}
	var capture *PrivateReadCapture
	err = runtime.withClaimedReadCurrent(reference, request.Binding, request.AdmissionBinding, func() error {
		var stageErr error
		capture, stageErr = runtime.stagePrivateReadCaptureLocked(reference, admission, result)
		return stageErr
	})
	if err != nil || capture == nil {
		t.Fatalf("stage private capture=%v err=%v", capture, err)
	}
	stored := runtime.privateReadCaptures[capture.id].bytes
	if string(stored) != string(content) {
		t.Fatalf("captured bytes=%q want=%q", stored, content)
	}
	if err := runtime.withClaimedReadCurrent(reference, request.Binding, request.AdmissionBinding, func() error { return nil }); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("captured execution remained releasable: %v", err)
	}
	if err := runtime.discardPrivateReadCapture(capture); err != nil {
		t.Fatal(err)
	}
	for _, b := range stored {
		if b != 0 {
			t.Fatal("discard left the aliased capture payload in memory")
		}
	}
	if runtime.readAuthorityCount() != 0 || len(runtime.privateReadCaptures) != 0 {
		t.Fatal("discard did not retire private result provenance")
	}
}

func TestDuplicateReadInvocationCannotReceiveFreshExecutionReference(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	target := filepath.Join(t.TempDir(), "file")
	invocation := syntheticReadInvocation(t, "conversation", "turn", "same-call", target)
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-one")
	if _, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch); err != nil {
		t.Fatal(err)
	}
	request2, approval2, consumption2 := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-two")
	if _, err := runtime.issueReadExecutionReference(invocation, request2, approval2, consumption2, epoch); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("duplicate provider call received fresh execution reference: %v", err)
	}
}

func TestReadAuthoritySurvivesOnlyRegisteredSiblingTransitions(t *testing.T) {
	runtime := syntheticAuthorityRuntime()
	root := t.TempDir()
	targetA, targetB := filepath.Join(root, "a"), filepath.Join(root, "b")
	a := syntheticReadInvocation(t, "conversation", "turn", "call-a", targetA)
	epochA := runtime.readAuthorityEpochSnapshot()
	requestA, approvalA, consumptionA := approveAndConsumeSyntheticRead(t, runtime, a, targetA, "admission-a")
	referenceA, err := runtime.issueReadExecutionReference(a, requestA, approvalA, consumptionA, epochA)
	if err != nil {
		t.Fatal(err)
	}
	b := syntheticReadInvocation(t, "conversation", "turn", "call-b", targetB)
	_, _, _ = approveAndConsumeSyntheticRead(t, runtime, b, targetB, "admission-b")
	if err := runtime.ClaimReadExecution(referenceA, requestA.Binding, "admission-a"); err != nil {
		t.Fatalf("registered sibling transition invalidated A: %v", err)
	}

	other := syntheticAuthorityRuntime()
	targetC, targetD := filepath.Join(root, "c"), filepath.Join(root, "d")
	c := syntheticReadInvocation(t, "conversation", "turn", "call-c", targetC)
	epochC := other.readAuthorityEpochSnapshot()
	requestC, approvalC, consumptionC := approveAndConsumeSyntheticRead(t, other, c, targetC, "admission-c")
	referenceC, err := other.issueReadExecutionReference(c, requestC, approvalC, consumptionC, epochC)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.grants.Add(permissions.Grant{ID: "untracked", ConversationID: "conversation", ToolID: "update_plan", Kind: permissions.RequirementAction, Operation: permissions.OperationInteract, ActionKey: "update", Lifetime: permissions.LifetimeConversation, Origin: "external"}); err != nil {
		t.Fatal(err)
	}
	d := syntheticReadInvocation(t, "conversation", "turn", "call-d", targetD)
	requestD, err := bindAuthorizationRequest(d, syntheticReadRequest(d, targetD, "admission-d"))
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
	target := filepath.Join(t.TempDir(), "a")
	invocation := syntheticReadInvocation(t, "conversation", "turn", "call", target)
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission")
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
	target := filepath.Join(t.TempDir(), "a")
	invocation := syntheticReadInvocation(t, "conversation", "turn", "call-gated", target)
	request, err := bindAuthorizationRequest(invocation, syntheticReadRequest(invocation, target, "admission-gated"))
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
	if count, err := chat.ClearToolGrants(); count != 0 || err != nil {
		t.Fatal("empty clear unexpectedly removed a grant")
	}
	if _, err := runtime.coordinator.Respond(pending.ID, permissions.ApprovalOnce, ""); !errors.Is(err, permissions.ErrApprovalNotFound) {
		t.Fatalf("empty clear did not cancel pending response: %v", err)
	}
	target := filepath.Join(t.TempDir(), "a")
	invocation := syntheticReadInvocation(t, chat.conversationID, "turn", "call-clear", target)
	epoch := runtime.readAuthorityEpochSnapshot()
	request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-a")
	reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := chat.ClearToolGrants(); count != 0 || err != nil || runtime.readAuthorityCount() != 0 {
		t.Fatalf("empty clear count=%d err=%v retained=%d", count, err, runtime.readAuthorityCount())
	}
	if err := chat.ClaimReadExecution(reference, request.Binding, request.AdmissionBinding); !errors.Is(err, errReadAuthorityInvalid) {
		t.Fatalf("clear left consumed authority claimable: %v", err)
	}

	invocation.ProviderCallID = "call-revoke"
	epoch = runtime.readAuthorityEpochSnapshot()
	request, approval, consumption = approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-b")
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
	request, approval, consumption = approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-c")
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

func TestPermissionClearAndRevokeDoNotRequireReadBackend(t *testing.T) {
	runtime := newToolRuntime(ToolRuntimeConfig{Registry: tools.NewRegistry()})
	chat := &Chat{toolRuntime: runtime, conversationID: "conversation"}
	if err := runtime.grants.Add(permissions.Grant{ID: "clear-me", ConversationID: chat.conversationID, ToolID: "update_plan", Kind: permissions.RequirementAction, Operation: permissions.OperationInteract, ActionKey: "plan", Lifetime: permissions.LifetimeConversation, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	if count, err := chat.ClearToolGrants(); count != 1 || err != nil {
		t.Fatalf("clear without read backend: count=%d err=%v", count, err)
	}
	if err := runtime.grants.Add(permissions.Grant{ID: "revoke-me", ConversationID: chat.conversationID, ToolID: "update_plan", Kind: permissions.RequirementAction, Operation: permissions.OperationInteract, ActionKey: "plan", Lifetime: permissions.LifetimeConversation, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := chat.RevokeToolGrant("revoke-me"); err != nil {
		t.Fatalf("revoke without read backend: %v", err)
	}
}

func TestChatLifecycleInvalidatesReadAuthority(t *testing.T) {
	for _, lifecycle := range []string{"cancel", "root", "reset", "shutdown"} {
		t.Run(lifecycle, func(t *testing.T) {
			runtime := syntheticAuthorityRuntime()
			chat := &Chat{toolRuntime: runtime, conversationID: "conversation"}
			target := filepath.Join(t.TempDir(), "a")
			invocation := syntheticReadInvocation(t, chat.conversationID, "turn", "call-"+lifecycle, target)
			epoch := runtime.readAuthorityEpochSnapshot()
			request, approval, consumption := approveAndConsumeSyntheticRead(t, runtime, invocation, target, "admission-"+lifecycle)
			reference, err := runtime.issueReadExecutionReference(invocation, request, approval, consumption, epoch)
			if err != nil {
				t.Fatal(err)
			}
			switch lifecycle {
			case "cancel":
				chat.Cancel()
			case "root":
				chat.mu.Lock()
				chat.changeFilesystemRootLocked(filepath.Join(t.TempDir(), "explicit-root"), FilesystemRootSelected)
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
