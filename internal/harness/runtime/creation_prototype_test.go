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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

type creationRuntimeFixture struct {
	root              string
	previews          []permissions.Request
	prepared          []permissions.Request
	executions        int
	closes            int
	discarded         int
	changeOnReprepare bool
}

func (h *creationRuntimeFixture) PrepareFilesystemCreation(_ context.Context, invocation tools.Invocation, destination string) (permissions.Request, error) {
	input, err := tools.ParseCreateFile(invocation.Arguments)
	if err != nil {
		return permissions.Request{}, err
	}
	target := filepath.Join(h.root, filepath.FromSlash(input.Path))
	bindingBytes, err := json.Marshal(struct {
		ConversationID string
		CallID         string
		Destination    string
		Target         string
		Content        string
	}{invocation.ConversationID, invocation.ProviderCallID, destination, target, input.Content})
	if err != nil {
		return permissions.Request{}, err
	}
	digest := sha256.Sum256(bindingBytes)
	description := fmt.Sprintf("Create %s (%d bytes) for %s. Complete diff:\n--- /dev/null\n+++ %s\n+%s", target, len(input.Content), destination, target, input.Content)
	request := permissions.Request{
		ConversationID:   invocation.ConversationID,
		CallID:           invocation.ProviderCallID,
		Destination:      destination,
		AdmissionBinding: hex.EncodeToString(digest[:]),
		Requirements: []permissions.Requirement{
			{Kind: permissions.RequirementAction, Key: "create", ToolID: string(tools.CreateFileID), Description: description, Operation: permissions.OperationWrite, ActionKey: "fixture-create-once"},
			{Kind: permissions.RequirementResource, Key: "target", ToolID: string(tools.CreateFileID), Description: "Create this exact target once", Operation: permissions.OperationWrite, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationWrite}},
		},
	}
	if h.changeOnReprepare && len(h.previews) > 0 {
		request.AdmissionBinding += "-changed"
	}
	h.previews = append(h.previews, clonePermissionRequest(request))
	return request, nil
}

func (h *creationRuntimeFixture) DiscardFilesystemCreationProposal(_ context.Context, invocation tools.Invocation) error {
	h.discarded++
	return nil
}

func (h *creationRuntimeFixture) PrepareApprovedFilesystemCreation(_ context.Context, _ tools.Invocation, request permissions.Request, _ uint64) (ToolPreparation, error) {
	if len(h.previews) == 0 || request.AdmissionBinding != h.previews[len(h.previews)-1].AdmissionBinding {
		return nil, errors.New("stale fixture proposal")
	}
	h.prepared = append(h.prepared, clonePermissionRequest(request))
	return &creationRuntimePreparation{host: h, callID: request.CallID}, nil
}

type creationRuntimePreparation struct {
	host   *creationRuntimeFixture
	callID string
}

func (p *creationRuntimePreparation) Execute(context.Context) (tools.Result, error) {
	p.host.executions++
	return tools.Result{ProviderCallID: p.callID, Outcome: tools.OutcomeSucceeded, Content: []tools.Content{{Kind: tools.ContentText, Text: "creation fixture completed"}}}, nil
}

func (p *creationRuntimePreparation) Close(context.Context) error {
	p.host.closes++
	return nil
}

func creationProviderCall(t *testing.T, callID string, input tools.CreateFileInput) provider.Result {
	t.Helper()
	arguments, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return creationProviderCallArguments(t, callID, arguments)
}

func creationProviderCallArguments(t *testing.T, callID string, arguments json.RawMessage) provider.Result {
	t.Helper()
	callRaw, err := json.Marshal(map[string]any{"type": "function_call", "id": "item-" + callID, "call_id": callID, "name": string(tools.CreateFileID), "arguments": string(arguments), "status": "completed"})
	if err != nil {
		t.Fatal(err)
	}
	return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionToolCalls, Text: "creating", Native: []json.RawMessage{callRaw}, ToolCalls: []provider.ToolCall{{CallID: callID, ToolID: tools.CreateFileID, Arguments: append(json.RawMessage(nil), arguments...)}}}
}

func newCreationRuntimeChat(t *testing.T, fp *toolLoopProvider, config ToolRuntimeConfig) *Chat {
	t.Helper()
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "not-a-real-secret", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	t.Cleanup(chat.Close)
	return chat
}

func TestCreationPrototypeRequiresExplicitCompositionAndOnceApproval(t *testing.T) {
	baseApproval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	})
	clarification := ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, errors.New("not expected")
	})
	base := BundledConversationToolRuntimeConfig(baseApproval, clarification)
	if _, ok := base.Registry.Lookup(tools.CreateFileID); ok {
		t.Fatal("normal bundled composition registered prototype creation")
	}

	host := &creationRuntimeFixture{root: t.TempDir()}
	var approved permissions.PendingApproval
	base.Approval = ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approved = pending
		return permissions.ApprovalOnce, "", nil
	})
	config := WithBoundedCreationPrototype(base, host)
	if config.Registry == nil {
		t.Fatal("explicit bounded prototype composition was rejected")
	}

	definition, ok := config.Registry.Lookup(tools.CreateFileID)
	if !ok || definition.Definition().ID != tools.CreateFileID {
		t.Fatal("explicit composition did not register the creation schema")
	}
	input := tools.CreateFileInput{Path: "nested/new.txt", Content: "synthetic line\n"}
	fp := &toolLoopProvider{step: func(index int, _ provider.ChatRequest) (provider.Result, error) {
		if index == 0 {
			return creationProviderCall(t, "create-once", input), nil
		}
		return finalResult("done"), nil
	}}
	chat := newCreationRuntimeChat(t, fp, config)
	turn, err := chat.Send(context.Background(), "create the approved fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || host.executions != 1 || host.closes != 1 || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != tools.OutcomeSucceeded {
		t.Fatalf("turn=%+v executions=%d closes=%d err=%v", turn, host.executions, host.closes, err)
	}
	if len(host.previews) != 2 || len(host.prepared) != 1 {
		t.Fatalf("proposal lifecycle previews=%d prepared=%d", len(host.previews), len(host.prepared))
	}
	if len(approved.Request.Requirements) != 2 || approved.Request.Destination == "" || !strings.Contains(approved.Request.Requirements[0].Description, input.Content) || !strings.Contains(approved.Request.Requirements[0].Description, filepath.Join(host.root, input.Path)) {
		t.Fatalf("approval did not show exact target, full content and destination: %+v", approved.Request)
	}
	if len(approved.Request.RequireOnceKeys) != 2 || approved.Request.RequireOnceKeys[0] != "create" || approved.Request.RequireOnceKeys[1] != "target" {
		t.Fatalf("creation approval was not forced once: %+v", approved.Request.RequireOnceKeys)
	}
	if host.previews[0].AdmissionBinding != host.previews[1].AdmissionBinding || host.prepared[0].Binding == "" {
		t.Fatal("approved proposal identity was not retained through final preparation")
	}
	for _, request := range fp.captured() {
		for _, tool := range request.Tools {
			if tool.ID == tools.CreateFileID && !json.Valid(tool.InputSchema) {
				t.Fatalf("creation schema is invalid JSON: %s", tool.InputSchema)
			}
		}
	}
	requests := fp.captured()
	if len(requests) < 1 || !strings.Contains(requests[0].Instructions, "creation prototype") || strings.Contains(requests[0].Instructions, "No filesystem write") {
		t.Fatalf("provider instructions do not match the explicitly composed capability: %+v", requests)
	}
}

func TestCreationPrototypeRejectsMissingOrNullContentBeforeApproval(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments string
	}{
		{name: "missing", arguments: `{"path":"empty.txt"}`},
		{name: "null", arguments: `{"path":"empty.txt","content":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			approvals := 0
			base := BundledConversationToolRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				approvals++
				return permissions.ApprovalOnce, "", nil
			}), ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
				return ClarificationResponse{}, errors.New("not expected")
			}))
			host := &creationRuntimeFixture{root: t.TempDir()}
			config := WithBoundedCreationPrototype(base, host)
			fp := &toolLoopProvider{step: func(index int, _ provider.ChatRequest) (provider.Result, error) {
				if index == 0 {
					return creationProviderCallArguments(t, "invalid-"+test.name, json.RawMessage(test.arguments)), nil
				}
				return finalResult("done"), nil
			}}
			chat := newCreationRuntimeChat(t, fp, config)
			turn, err := chat.Send(context.Background(), "create an empty fixture", nil)
			if err != nil || turn.Outcome != provider.OutcomeCompleted || approvals != 0 || len(host.previews) != 0 || len(host.prepared) != 0 || host.executions != 0 {
				t.Fatalf("invalid content reached approval or host: turn=%+v approvals=%d previews=%d prepared=%d executions=%d err=%v", turn, approvals, len(host.previews), len(host.prepared), host.executions, err)
			}
		})
	}
}

func TestCreationPrototypeRejectsChangedProposalBeforeExecution(t *testing.T) {
	base := BundledConversationToolRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, errors.New("not expected")
	}))
	host := &creationRuntimeFixture{root: t.TempDir(), changeOnReprepare: true}
	config := WithBoundedCreationPrototype(base, host)
	fp := &toolLoopProvider{step: func(index int, _ provider.ChatRequest) (provider.Result, error) {
		if index == 0 {
			return creationProviderCall(t, "create-stale", tools.CreateFileInput{Path: "new.txt", Content: "approved"}), nil
		}
		return finalResult("done"), nil
	}}
	chat := newCreationRuntimeChat(t, fp, config)
	_, err := chat.Send(context.Background(), "create the fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	if host.executions != 0 || len(host.prepared) != 0 {
		t.Fatalf("stale proposal reached execution: executions=%d preparations=%d", host.executions, len(host.prepared))
	}
}

func TestCreationPrototypeDenialAndCancellationDoNotPrepareOrExecute(t *testing.T) {
	for _, test := range []struct {
		name    string
		choice  permissions.ApprovalChoice
		outcome provider.Outcome
	}{
		{name: "denied", choice: permissions.ApprovalDeny, outcome: provider.OutcomeCompleted},
		{name: "cancelled", choice: permissions.ApprovalCancel, outcome: provider.OutcomeInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := BundledConversationToolRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return test.choice, "", nil
			}), ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
				return ClarificationResponse{}, errors.New("not expected")
			}))
			host := &creationRuntimeFixture{root: t.TempDir()}
			config := WithBoundedCreationPrototype(base, host)
			fp := &toolLoopProvider{step: func(index int, _ provider.ChatRequest) (provider.Result, error) {
				if index == 0 {
					return creationProviderCall(t, "create-"+test.name, tools.CreateFileInput{Path: "new.txt", Content: "synthetic"}), nil
				}
				return finalResult("done"), nil
			}}
			chat := newCreationRuntimeChat(t, fp, config)
			turn, err := chat.Send(context.Background(), "create the fixture", nil)
			if err != nil || turn.Outcome != test.outcome {
				t.Fatalf("turn=%+v err=%v; want outcome %s", turn, err, test.outcome)
			}
			if host.executions != 0 || len(host.prepared) != 0 || host.closes != 0 || host.discarded != 1 {
				t.Fatalf("refused request lifecycle: previews=%d preparations=%d executions=%d closes=%d discarded=%d", len(host.previews), len(host.prepared), host.executions, host.closes, host.discarded)
			}
		})
	}
}
