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
	"fmt"
	"path/filepath"
	"strings"
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
	toolID tools.ID
	args   json.RawMessage
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
		toolID, args := p.toolID, append(json.RawMessage(nil), p.args...)
		if toolID == "" {
			toolID = "lookup"
			args, _ = json.Marshal(map[string]string{"target": p.target})
		}
		native, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-call", "call_id": "call", "name": string(toolID), "arguments": string(args), "status": "completed"})
		return modelprovider.Result{Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionToolCalls, Native: []json.RawMessage{native}, ToolCalls: []modelprovider.ToolCall{{CallID: "call", ToolID: toolID, Arguments: args}}}, nil
	}
	return modelprovider.Result{Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: "done", Native: []json.RawMessage{json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}`)}}, nil
}

type creationApprovalFixture struct {
	root     string
	prepared int
	executed int
	closed   int
}

func (h *creationApprovalFixture) PrepareFilesystemCreation(_ context.Context, invocation tools.Invocation, destination, workingDirectory string) (permissions.Request, error) {
	input, err := tools.ParseCreateFile(invocation.Arguments)
	if err != nil {
		return permissions.Request{}, err
	}
	if workingDirectory == "" {
		workingDirectory = h.root
	}
	target := filepath.Join(workingDirectory, filepath.FromSlash(input.Path))
	description := fmt.Sprintf("Create exact target %s (%d bytes) for %s. Complete diff:\n--- /dev/null\n+++ %s\n+%s", target, len(input.Content), destination, target, input.Content)
	digest := sha256.Sum256([]byte(invocation.ConversationID + "\x00" + invocation.ProviderCallID + "\x00" + destination + "\x00" + target + "\x00" + input.Content))
	return permissions.Request{
		ConversationID: invocation.ConversationID, CallID: invocation.ProviderCallID, Destination: destination,
		AdmissionBinding: hex.EncodeToString(digest[:]),
		Requirements: []permissions.Requirement{
			{Kind: permissions.RequirementAction, Key: "create", ToolID: string(tools.CreateFileID), Description: description, Operation: permissions.OperationWrite, ActionKey: "fixture-create-once"},
			{Kind: permissions.RequirementResource, Key: "target", ToolID: string(tools.CreateFileID), Description: "Create this exact target once", Operation: permissions.OperationWrite, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationWrite}},
		},
	}, nil
}

func (h *creationApprovalFixture) PrepareApprovedFilesystemCreation(_ context.Context, invocation tools.Invocation, request permissions.Request, _ uint64) (hruntime.ToolPreparation, error) {
	if request.CallID != invocation.ProviderCallID || request.Destination == "" || request.Binding == "" {
		return nil, fmt.Errorf("approved creation binding is incomplete")
	}
	h.prepared++
	return &creationApprovalPreparation{host: h, callID: invocation.ProviderCallID}, nil
}

type creationApprovalPreparation struct {
	host   *creationApprovalFixture
	callID string
}

func (p *creationApprovalPreparation) Execute(context.Context) (tools.Result, error) {
	p.host.executed++
	return tools.Result{ProviderCallID: p.callID, Outcome: tools.OutcomeSucceeded, Content: []tools.Content{{Kind: tools.ContentText, Text: "fixture creation completed"}}}, nil
}

func (p *creationApprovalPreparation) Close(context.Context) error {
	p.host.closed++
	return nil
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
	target := filepath.Join(t.TempDir(), "report.txt")
	fakeProvider := &approvalTestProvider{target: target}
	providers, err := model.NewRegistry(fakeProvider)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
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

func TestCreationPrototypeShowsExactDiffAndDestinationForOnceApproval(t *testing.T) {
	bridge := NewApprovalBridge()
	root := t.TempDir()
	input, err := json.Marshal(tools.CreateFileInput{Path: "nested/new.txt", Content: "synthetic approved text\n"})
	if err != nil {
		t.Fatal(err)
	}
	fakeProvider := &approvalTestProvider{toolID: tools.CreateFileID, args: input}
	providers, err := model.NewRegistry(fakeProvider)
	if err != nil {
		t.Fatal(err)
	}
	clarification := hruntime.ToolClarificationFunc(func(context.Context, hruntime.ClarificationRequest) (hruntime.ClarificationResponse, error) {
		return hruntime.ClarificationResponse{}, fmt.Errorf("not expected")
	})
	base := hruntime.BundledConversationToolRuntimeConfig(bridge, clarification)
	host := &creationApprovalFixture{root: root}
	config := hruntime.WithBoundedCreationPrototype(base, host)
	if config.Registry == nil {
		t.Fatal("explicit prototype composition was rejected")
	}
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
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
		turn, err := client.Send(context.Background(), "create the approved fixture", nil)
		done <- sendResult{turn, err}
	}()
	var request frontend.ApprovalRequest
	select {
	case event := <-client.ApprovalEvents():
		if event.Request == nil {
			t.Fatal("creation approval request missing")
		}
		request = *event.Request
	case <-time.After(time.Second):
		t.Fatal("creation approval was not delivered")
	}
	target := filepath.Join(root, "nested", "new.txt")
	if request.ToolID != string(tools.CreateFileID) || len(request.Requirements) != 2 || request.Requirements[1].ResourceScope != target || request.AllowConversation {
		t.Fatalf("creation approval did not bind once to exact target: %+v", request)
	}
	if request.Provider != "openai" || request.Model != "fixture-model" || request.AuthProfile != "fixture" || !strings.Contains(request.Requirements[0].Description, "Complete diff") || !strings.Contains(request.Requirements[0].Description, "synthetic approved text") || !strings.Contains(request.Requirements[0].Description, target) {
		t.Fatalf("approval omitted the exact diff or provider destination: %+v", request)
	}
	if err := client.RespondApproval(frontend.ApprovalResponse{ID: request.ID, Choice: frontend.ApprovalConversation}); err == nil {
		t.Fatal("conversation-level approval was accepted for creation")
	}
	if err := client.RespondApproval(frontend.ApprovalResponse{ID: request.ID, Choice: frontend.ApprovalOnce}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.turn.Outcome != modelprovider.OutcomeCompleted || len(result.turn.ToolActivities) != 1 || result.turn.ToolActivities[0].Outcome != string(tools.OutcomeSucceeded) {
			t.Fatalf("creation fixture turn=%+v err=%v", result.turn, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider continuation did not complete")
	}
	if host.prepared != 1 || host.executed != 1 || host.closed != 1 {
		t.Fatalf("creation fixture lifecycle prepared=%d executed=%d closed=%d", host.prepared, host.executed, host.closed)
	}
	if event := <-client.ApprovalEvents(); event.DismissedID != request.ID {
		t.Fatalf("creation approval dismissal=%+v", event)
	}
}
