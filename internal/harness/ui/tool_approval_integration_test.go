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

package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	hruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	frontend "github.com/ayeshLK/komposyn/plugins/ui"
)

type approvalTestProvider struct {
	mu     sync.Mutex
	calls  int
	target string
}

func (*approvalTestProvider) ID() modelprovider.ID { return modelprovider.OpenAI }
func (*approvalTestProvider) AuthMethods() []modelprovider.AuthMethod {
	return []modelprovider.AuthMethod{modelprovider.AuthAPIKey}
}
func (*approvalTestProvider) Authenticate(context.Context, modelprovider.Credential) (modelprovider.AuthResult, error) {
	return modelprovider.AuthResult{Provider: modelprovider.OpenAI}, nil
}
func (*approvalTestProvider) ListModels(context.Context, modelprovider.Credential) ([]modelprovider.Model, error) {
	return []modelprovider.Model{{ID: "fixture-model", DisplayName: "Fixture"}}, nil
}
func (*approvalTestProvider) RouteCapabilities(method modelprovider.AuthMethod) modelprovider.RouteCapabilities {
	if method == modelprovider.AuthAPIKey {
		return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportSupported}
	}
	return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportUnknown}
}
func (p *approvalTestProvider) StreamChat(_ context.Context, req modelprovider.ChatRequest, _ func(string)) (modelprovider.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		args, _ := json.Marshal(map[string]string{"target": p.target})
		native, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-call", "call_id": "call", "name": "lookup", "arguments": string(args), "status": "completed"})
		return modelprovider.Result{Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionToolCalls, Native: []json.RawMessage{native}, ToolCalls: []modelprovider.ToolCall{{CallID: "call", ToolID: "lookup", Arguments: args}}}, nil
	}
	return modelprovider.Result{Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: "done", Native: []json.RawMessage{json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}`)}}, nil
}

type approvalTestTool struct {
	calls   chan struct{}
	release chan struct{}
}

func (*approvalTestTool) Definition() tools.Definition {
	return tools.Definition{ID: "lookup", Description: "Fixture lookup", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 1024}
}
func (*approvalTestTool) ValidateArguments(raw json.RawMessage) error {
	var value map[string]string
	return json.Unmarshal(raw, &value)
}
func (t *approvalTestTool) Execute(ctx context.Context, inv tools.Invocation) tools.Result {
	t.calls <- struct{}{}
	select {
	case <-t.release:
	case <-ctx.Done():
		return tools.Result{ProviderCallID: inv.ProviderCallID, Outcome: tools.OutcomeCancelled}
	}
	return tools.Result{ProviderCallID: inv.ProviderCallID, Outcome: tools.OutcomeSucceeded, Content: []tools.Content{{Kind: tools.ContentText, Text: "fixture result"}}}
}

func TestInjectedToolApprovalFlowsThroughUIAdapterExactlyOnce(t *testing.T) {
	target := t.TempDir() + "/report.txt"
	fakeProvider := &approvalTestProvider{target: target}
	providers, err := model.NewRegistry(fakeProvider)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	bridge := NewApprovalBridge()
	tool := &approvalTestTool{calls: make(chan struct{}, 2), release: make(chan struct{})}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	grants := permissions.NewMemoryStore()
	config := hruntime.ToolRuntimeConfig{Registry: registry, Grants: grants, Approval: bridge, Authorizer: hruntime.ToolAuthorizerFunc(func(_ context.Context, inv tools.Invocation) (permissions.Request, error) {
		sum := sha256.Sum256(inv.Arguments)
		return permissions.Request{ConversationID: inv.ConversationID, CallID: inv.ProviderCallID, Requirements: []permissions.Requirement{{Kind: permissions.RequirementAction, Key: "action", ToolID: string(inv.ToolID), Description: "Read the requested report", Operation: permissions.OperationRead, ActionKey: hex.EncodeToString(sum[:]), SessionAllowed: true}, {Kind: permissions.RequirementResource, Key: "resource", ToolID: string(inv.ToolID), Operation: permissions.OperationRead, SessionAllowed: true, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationRead}}}}, nil
	}), Enforcer: hruntime.ToolEnforcerFunc(func(ctx context.Context, implementation tools.Tool, inv tools.Invocation, _ permissions.Request) (tools.Result, error) {
		return implementation.Execute(ctx, inv), nil
	})}
	chat := hruntime.NewChatWithToolRuntime(providers, store, nil, config)
	t.Cleanup(chat.Close)
	if err := chat.AddProfile(context.Background(), "fixture", "openai", "Fixture", "fake-key", nil); err != nil {
		t.Fatal(err)
	}
	discovery := chat.Discover(context.Background())
	if len(discovery.Models) != 1 {
		t.Fatalf("models=%+v", discovery)
	}
	if err := chat.Select(context.Background(), discovery.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	client := Client{Chat: chat, ProfileStore: store, Approval: bridge}
	type sendResult struct {
		turn frontend.Turn
		err  error
	}
	done := make(chan sendResult, 1)
	go func() {
		turn, err := client.Send(context.Background(), "read report", nil)
		done <- sendResult{turn, err}
	}()
	var request frontend.ApprovalRequest
	select {
	case event := <-client.ApprovalEvents():
		if event.Request == nil {
			t.Fatal("approval request missing")
		}
		request = *event.Request
	case <-time.After(time.Second):
		t.Fatal("approval not delivered")
	}
	if len(request.Requirements) != 2 || request.Requirements[0].Description != "Read the requested report" || request.Requirements[1].ResourceScope != target || !request.AllowConversation {
		t.Fatalf("UI approval record=%+v", request)
	}
	if activities := client.Snapshot().ToolActivities; len(activities) != 1 || activities[0].Phase != "awaiting approval" {
		t.Fatalf("live approval activity=%+v", activities)
	}
	if err := client.RespondApproval(frontend.ApprovalResponse{ID: request.ID, Choice: frontend.ApprovalConversation}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tool.calls:
	case <-time.After(time.Second):
		t.Fatal("fake tool did not execute")
	}
	if activities := client.Snapshot().ToolActivities; len(activities) < 2 || activities[len(activities)-1].Phase != "running" {
		t.Fatalf("live running activity=%+v", activities)
	}
	close(tool.release)
	select {
	case result := <-done:
		if result.err != nil || result.turn.Outcome != modelprovider.OutcomeCompleted || len(result.turn.ToolActivities) != 1 || result.turn.ToolActivities[0].Outcome != string(tools.OutcomeSucceeded) {
			t.Fatalf("completed turn=%+v err=%v", result.turn, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider continuation did not complete")
	}
	if got := <-client.ApprovalEvents(); got.DismissedID != request.ID {
		t.Fatalf("approval dismissal=%+v", got)
	}
	select {
	case <-tool.calls:
		t.Fatal("tool executed more than once")
	default:
	}
	if len(client.Snapshot().Turns) != 1 || len(client.Snapshot().Turns[0].ToolActivities) != 1 {
		t.Fatalf("adapter lost completed activity: %+v", client.Snapshot().Turns)
	}
	listed, err := client.Permissions(context.Background())
	if err != nil || len(listed) != 2 || listed[0].ID == "" || listed[0].Lifetime != string(permissions.LifetimeConversation) {
		t.Fatalf("conversation grants=%+v err=%v", listed, err)
	}
	if err := client.RevokePermission(context.Background(), listed[0].ID); err != nil {
		t.Fatal(err)
	}
	listed, err = client.Permissions(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("revocation did not update authoritative grant list: %+v err=%v", listed, err)
	}
	cleared, err := client.ClearPermissions(context.Background())
	if err != nil || cleared != 1 {
		t.Fatalf("clear grants=%d err=%v", cleared, err)
	}
}
