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
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	p "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

type toolLoopProvider struct {
	mu           sync.Mutex
	requests     []p.ChatRequest
	step         func(int, p.ChatRequest) (p.Result, error)
	routeSupport p.Support
}

func (*toolLoopProvider) ID() p.ID                    { return p.OpenAI }
func (*toolLoopProvider) AuthMethods() []p.AuthMethod { return []p.AuthMethod{p.AuthAPIKey} }
func (*toolLoopProvider) Authenticate(context.Context, p.Credential) (p.AuthResult, error) {
	return p.AuthResult{Provider: p.OpenAI}, nil
}
func (*toolLoopProvider) ListModels(context.Context, p.Credential) ([]p.Model, error) {
	return []p.Model{{ID: "tool-model", DisplayName: "Tool Model", Compatibility: p.CompatibilitySupported}, {ID: "tool-model-next", DisplayName: "Next Tool Model", Compatibility: p.CompatibilitySupported}}, nil
}
func (f *toolLoopProvider) RouteCapabilities(method p.AuthMethod) p.RouteCapabilities {
	if method == p.AuthAPIKey {
		support := f.routeSupport
		if support == "" {
			support = p.SupportSupported
		}
		return p.RouteCapabilities{ClientTools: support}
	}
	return p.RouteCapabilities{ClientTools: p.SupportUnknown}
}
func (f *toolLoopProvider) StreamChat(_ context.Context, request p.ChatRequest, _ func(string)) (p.Result, error) {
	f.mu.Lock()
	index := len(f.requests)
	f.requests = append(f.requests, cloneRequest(request))
	f.mu.Unlock()
	return f.step(index, request)
}
func (f *toolLoopProvider) captured() []p.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]p.ChatRequest, len(f.requests))
	for i := range f.requests {
		out[i] = cloneRequest(f.requests[i])
	}
	return out
}
func cloneRequest(request p.ChatRequest) p.ChatRequest {
	request.Messages = cloneMessages(request.Messages)
	request.Tools = append(request.Tools[:0:0], request.Tools...)
	return request
}

type fixtureTool struct {
	mu      sync.Mutex
	calls   []tools.Invocation
	content string
	outcome tools.Outcome
}

func (f *fixtureTool) Definition() tools.Definition {
	return tools.Definition{ID: "lookup", Description: "Look up a fixture value", InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}}}`), MaxInput: 1024}
}
func (*fixtureTool) ValidateArguments(arguments json.RawMessage) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(arguments, &object) != nil || object == nil {
		return errors.New("invalid object")
	}
	return nil
}
func (f *fixtureTool) Execute(_ context.Context, invocation tools.Invocation) tools.Result {
	f.mu.Lock()
	f.calls = append(f.calls, cloneInvocation(invocation))
	f.mu.Unlock()
	outcome := f.outcome
	if outcome == "" {
		outcome = tools.OutcomeSucceeded
	}
	return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: outcome, Content: []tools.Content{{Kind: tools.ContentText, Text: f.content}}}
}
func (f *fixtureTool) captured() []tools.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tools.Invocation, len(f.calls))
	for i := range f.calls {
		out[i] = cloneInvocation(f.calls[i])
	}
	return out
}

type fixtureAuthorizer struct {
	mu      sync.Mutex
	calls   []tools.Invocation
	prepare func(int, tools.Invocation) (permissions.Request, error)
}

func (f *fixtureAuthorizer) Prepare(_ context.Context, invocation tools.Invocation) (permissions.Request, error) {
	f.mu.Lock()
	index := len(f.calls)
	f.calls = append(f.calls, cloneInvocation(invocation))
	f.mu.Unlock()
	if f.prepare != nil {
		return f.prepare(index, invocation)
	}
	var args map[string]string
	_ = json.Unmarshal(invocation.Arguments, &args)
	target := args["target"]
	if target == "" {
		target = "/safe/fixture.txt"
	}
	digest := sha256.Sum256(invocation.Arguments)
	return permissions.Request{ConversationID: invocation.ConversationID, CallID: invocation.ProviderCallID, Requirements: []permissions.Requirement{
		{Kind: permissions.RequirementAction, Key: "action", ToolID: string(invocation.ToolID), Operation: permissions.OperationRead, ActionKey: hex.EncodeToString(digest[:]), SessionAllowed: true},
		{Kind: permissions.RequirementResource, Key: "resource", ToolID: string(invocation.ToolID), Operation: permissions.OperationRead, SessionAllowed: true,
			Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: target, Operation: permissions.OperationRead}},
	}}, nil
}
func (f *fixtureAuthorizer) captured() []tools.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tools.Invocation, len(f.calls))
	for i := range f.calls {
		out[i] = cloneInvocation(f.calls[i])
	}
	return out
}

func makeToolLoopChat(t *testing.T, fp *toolLoopProvider, config ToolRuntimeConfig) (*Chat, *fixtureTool) {
	t.Helper()
	tool, ok := config.Registry.Lookup("lookup")
	if !ok {
		t.Fatal("fixture tool was not registered")
	}
	fixture, ok := tool.(*fixtureTool)
	if !ok {
		t.Fatalf("tool type=%T", tool)
	}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	chat := NewChatWithToolRuntime(providers, store, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(p.OpenAI), "Fixture", "not-a-real-secret", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	t.Cleanup(chat.Close)
	return chat, fixture
}

func newToolConfig(tool *fixtureTool, approval ToolApprovalInteraction) ToolRuntimeConfig {
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		panic(err)
	}
	return ToolRuntimeConfig{Registry: registry, Authorizer: &fixtureAuthorizer{}, Enforcer: ToolEnforcerFunc(func(ctx context.Context, implementation tools.Tool, invocation tools.Invocation, _ permissions.Request) (tools.Result, error) {
		return implementation.Execute(ctx, invocation), nil
	}), Approval: approval, Grants: permissions.NewMemoryStore()}
}

func callResult(callID string, target string) p.Result {
	arguments, _ := json.Marshal(map[string]string{"target": target})
	callRaw, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-" + callID, "call_id": callID, "name": "lookup", "arguments": string(arguments), "status": "completed"})
	return p.Result{Outcome: p.OutcomeCompleted, Completion: p.CompletionToolCalls, Text: "checking", Native: []json.RawMessage{callRaw}, ToolCalls: []p.ToolCall{{CallID: callID, ToolID: "lookup", Arguments: arguments}}, Usage: p.Usage{InputKnown: true, InputTokens: 2, OutputKnown: true, OutputTokens: 1, CacheReadKnown: true, CacheRead: 0}}
}

func finalResult(text string) p.Result {
	message, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
	return p.Result{Outcome: p.OutcomeCompleted, Completion: p.CompletionFinal, Text: text, Native: []json.RawMessage{message}, Usage: p.Usage{InputKnown: true, InputTokens: 3, OutputKnown: true, OutputTokens: 2, CacheReadKnown: true, CacheRead: 1}}
}

func TestRuntimeToolLoopSerializesCorrelatedCallsAndContinues(t *testing.T) {
	tool := &fixtureTool{content: "fixture data"}
	approvalCount := 0
	config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvalCount++
		return permissions.ApprovalOnce, "", nil
	}))
	fp := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
		switch index {
		case 0:
			first := callResult("call-a", "/safe/a")
			second := callResult("call-b", "/safe/b")
			first.Text = "checking both"
			first.Native = append(first.Native, second.Native...)
			first.ToolCalls = append(first.ToolCalls, second.ToolCalls...)
			return first, nil
		case 1:
			if len(request.Messages) != 4 || request.Messages[1].Role != p.RoleAssistant || request.Messages[2].ToolResult == nil || request.Messages[3].ToolResult == nil {
				t.Errorf("continuation messages=%+v", request.Messages)
			}
			if request.Messages[2].ToolResult.CallID != "call-a" || request.Messages[3].ToolResult.CallID != "call-b" {
				t.Errorf("tool results are out of order: %+v", request.Messages)
			}
			return finalResult("answer"), nil
		default:
			return finalResult("follow-up"), nil
		}
	}}
	chat, _ := makeToolLoopChat(t, fp, config)
	turn, err := chat.Send(context.Background(), "inspect both", nil)
	if err != nil || turn.Outcome != p.OutcomeCompleted || turn.Text != "checking bothanswer" || len(turn.ToolActivities) != 2 || approvalCount != 2 {
		t.Fatalf("turn=%+v approvals=%d err=%v", turn, approvalCount, err)
	}
	if turn.Usage.InputTokens != 5 || turn.Usage.OutputTokens != 3 || !turn.Usage.Known || !turn.Usage.CacheReadKnown || turn.Usage.CacheRead != 1 {
		t.Fatalf("aggregated request usage=%+v", turn.Usage)
	}
	if got := tool.captured(); len(got) != 2 || got[0].ProviderCallID != "call-a" || got[1].ProviderCallID != "call-b" {
		t.Fatalf("execution order=%+v", got)
	}
	if _, grants := config.Grants.Snapshot(tool.captured()[0].ConversationID); len(grants) != 0 {
		t.Fatalf("once grants survived their dispatch: %+v", grants)
	}
	requests := fp.captured()
	if len(requests) != 2 || len(requests[0].Tools) != 1 || len(requests[1].Tools) != 1 {
		t.Fatalf("provider request tool definitions=%+v", requests)
	}
	if !json.Valid(requests[0].Tools[0].InputSchema) {
		t.Fatalf("schema is not valid detached JSON: %s", requests[0].Tools[0].InputSchema)
	}

	if _, err := chat.Send(context.Background(), "follow up", nil); err != nil {
		t.Fatal(err)
	}
	if got := tool.captured(); len(got) != 2 {
		t.Fatalf("follow-up unexpectedly repeated a completed tool call: %+v", got)
	}
	usage := chat.Snapshot().Usage
	if len(usage) != 1 || usage[0].Input.Tokens != 8 || usage[0].Output.Tokens != 5 || usage[0].CachedRead.Partial {
		t.Fatalf("conversation usage=%+v", usage)
	}
}

func TestRuntimeApprovalOutcomesAndHeadlessFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		choice     permissions.ApprovalChoice
		wantCalls  int
		wantReqs   int
		wantPrompt string
	}{
		{name: "deny", choice: permissions.ApprovalDeny, wantReqs: 2},
		{name: "redirect", choice: permissions.ApprovalRedirect, wantReqs: 2, wantPrompt: "Look up this other fact"},
		{name: "cancel", choice: permissions.ApprovalCancel, wantReqs: 1},
		{name: "no approval surface", wantReqs: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := &fixtureTool{content: "must not run"}
			var approval ToolApprovalInteraction
			if test.name != "no approval surface" {
				approval = ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
					return test.choice, test.wantPrompt, nil
				})
			}
			config := newToolConfig(tool, approval)
			fp := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
				if index == 0 {
					return callResult("call-a", "/safe/a"), nil
				}
				for _, message := range request.Messages {
					if message.Role == p.RoleUser && message.Text == test.wantPrompt && test.wantPrompt != "" {
						return finalResult("redirected answer"), nil
					}
					if message.ToolResult != nil && (!strings.Contains(message.ToolResult.Content, `"outcome":"not_executed"`) || !message.ToolResult.IsError) {
						t.Errorf("denied call was not reported as not executed: %+v", message.ToolResult)
					}
				}
				return finalResult("denied safely"), nil
			}}
			chat, _ := makeToolLoopChat(t, fp, config)
			turn, err := chat.Send(context.Background(), "run lookup", nil)
			if test.choice == permissions.ApprovalCancel {
				if err != nil || turn.Outcome != p.OutcomeInterrupted {
					t.Fatalf("cancel turn=%+v err=%v", turn, err)
				}
			} else if err != nil || turn.Outcome != p.OutcomeCompleted {
				t.Fatalf("turn=%+v err=%v", turn, err)
			}
			if len(tool.captured()) != test.wantCalls || len(fp.captured()) != test.wantReqs {
				t.Fatalf("tool calls=%d provider requests=%d", len(tool.captured()), len(fp.captured()))
			}
			if test.wantPrompt != "" {
				messages := fp.captured()[1].Messages
				if messages[len(messages)-1].Role != p.RoleUser || messages[len(messages)-1].Text != test.wantPrompt {
					t.Fatalf("redirect instruction missing: %+v", messages)
				}
			}
		})
	}
}

func TestRuntimeFailsClosedForUnknownRouteOrMissingEnforcement(t *testing.T) {
	for _, test := range []struct {
		name        string
		capability  p.Support
		noEnforcer  bool
		wantRequest int
	}{
		{name: "unknown route", capability: p.SupportUnknown, wantRequest: 1},
		{name: "missing enforcement", capability: p.SupportSupported, noEnforcer: true, wantRequest: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := &fixtureTool{}
			config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return permissions.ApprovalOnce, "", nil
			}))
			if test.noEnforcer {
				config.Enforcer = nil
			}
			fp := &toolLoopProvider{step: func(_ int, request p.ChatRequest) (p.Result, error) {
				if len(request.Tools) != 0 {
					t.Errorf("tool definitions leaked to unavailable runtime: %+v", request.Tools)
				}
				return callResult("unknown-call", "/safe/a"), nil
			}}
			fp.routeSupport = test.capability
			chat, _ := makeToolLoopChat(t, fp, config)
			turn, err := chat.Send(context.Background(), "try tool", nil)
			if err != nil || turn.Outcome != p.OutcomeUnsupported || len(tool.captured()) != 0 || len(fp.captured()) != test.wantRequest {
				t.Fatalf("turn=%+v calls=%v requests=%d err=%v", turn, tool.captured(), len(fp.captured()), err)
			}
		})
	}
}

func TestRuntimeDoesNotReplaySideEffectAfterContinuationFailure(t *testing.T) {
	tool := &fixtureTool{content: "completed once"}
	config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}))
	var failed bool
	fp := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
		if index == 0 {
			return callResult("call-side-effect", "/safe/a"), nil
		}
		if !failed {
			failed = true
			return p.Result{Usage: p.Usage{InputKnown: true, InputTokens: 9}}, &p.Error{Outcome: p.OutcomeService}
		}
		return finalResult("recovered context"), nil
	}}
	chat, _ := makeToolLoopChat(t, fp, config)
	first, err := chat.Send(context.Background(), "do the side effect", nil)
	if err == nil || first.Outcome != p.OutcomeService || len(first.ToolActivities) != 1 || first.ToolActivities[0].Outcome != tools.OutcomeSucceeded {
		t.Fatalf("failed continuation lost execution truth: turn=%+v err=%v", first, err)
	}
	second, err := chat.Send(context.Background(), "continue", nil)
	if err != nil || second.Outcome != p.OutcomeCompleted || len(tool.captured()) != 1 {
		t.Fatalf("follow-up replayed a side effect: turn=%+v calls=%d err=%v", second, len(tool.captured()), err)
	}
	requests := fp.captured()
	if len(requests) != 3 || len(requests[2].Messages) < 3 || requests[2].Messages[2].ToolResult == nil || requests[2].Messages[2].ToolResult.CallID != "call-side-effect" {
		t.Fatalf("recovery request did not retain valid tool continuation: request count=%d messages=%d", len(requests), len(requests[2].Messages))
	}
	usage := chat.Snapshot().Usage
	if len(usage) != 1 || usage[0].Input.Tokens != 14 || usage[0].Input.Partial || usage[0].Output.Tokens != 3 || !usage[0].Output.Partial {
		t.Fatalf("intermediate/failed request usage was not recorded exactly once: %+v", usage)
	}
}

func TestRuntimeRevalidatesAuthorizationAndStaleApproval(t *testing.T) {
	t.Run("changed target", func(t *testing.T) {
		tool := &fixtureTool{}
		config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}))
		authorizer := &fixtureAuthorizer{}
		authorizer.prepare = func(index int, invocation tools.Invocation) (permissions.Request, error) {
			request, _ := (&fixtureAuthorizer{}).Prepare(context.Background(), invocation)
			if index > 0 {
				request.Requirements[1].Resource.Target = "/safe/changed.txt"
			}
			return request, nil
		}
		config.Authorizer = authorizer
		fp := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
			if index == 0 {
				return callResult("call-a", "/safe/original.txt"), nil
			}
			return finalResult("not executed"), nil
		}}
		chat, _ := makeToolLoopChat(t, fp, config)
		turn, err := chat.Send(context.Background(), "read", nil)
		if err != nil || len(tool.captured()) != 0 || turn.ToolActivities[0].Outcome != tools.OutcomeNotExecuted {
			t.Fatalf("changed authorization context was dispatched: %+v err=%v", turn, err)
		}
	})

	t.Run("stale grant revision", func(t *testing.T) {
		tool := &fixtureTool{}
		config := newToolConfig(tool, nil)
		store := config.Grants
		config.Approval = ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			if err := store.Add(permissions.Grant{Kind: permissions.RequirementAction, ID: "concurrent-grant", ConversationID: pending.Request.ConversationID, CallID: "other-call", RequirementKey: "action", ToolID: "lookup", Operation: permissions.OperationRead, ActionKey: "other-action", Lifetime: permissions.LifetimeOnce, Origin: "test"}); err != nil {
				t.Fatal(err)
			}
			return permissions.ApprovalOnce, "", nil
		})
		fp := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
			if index == 0 {
				return callResult("call-a", "/safe/a"), nil
			}
			return finalResult("no execution"), nil
		}}
		chat, _ := makeToolLoopChat(t, fp, config)
		turn, err := chat.Send(context.Background(), "read", nil)
		if err != nil || len(tool.captured()) != 0 || turn.ToolActivities[0].Outcome != tools.OutcomeNotExecuted {
			t.Fatalf("stale approval dispatched: %+v err=%v", turn, err)
		}
	})
}

func TestRuntimeBoundsToolResultWireContent(t *testing.T) {
	encoded, truncated, err := envelopeForToolResult(tools.OutcomeSucceeded, []tools.Content{{Kind: tools.ContentText, Text: strings.Repeat("雪", MaxToolResultBytes)}}, false, 1024)
	if err != nil || !truncated || len(encoded) > 1024 || !utf8.ValidString(encoded) {
		t.Fatalf("bounded envelope bytes=%d truncated=%v err=%v", len(encoded), truncated, err)
	}
	var envelope struct {
		Outcome   tools.Outcome `json:"outcome"`
		Truncated bool          `json:"truncated"`
		Content   string        `json:"content"`
	}
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil || envelope.Outcome != tools.OutcomeSucceeded || !envelope.Truncated {
		t.Fatalf("envelope=%+v err=%v", envelope, err)
	}
}

func TestRuntimeCallLimitAndProviderLimitAreBounded(t *testing.T) {
	t.Run("provider requests", func(t *testing.T) {
		tool := &fixtureTool{}
		config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}))
		fp := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
			if index < MaxProviderRequestsPerTurn {
				return callResult(fmt.Sprintf("call-%d", index), "/safe/a"), nil
			}
			return finalResult("too many"), nil
		}}
		chat, _ := makeToolLoopChat(t, fp, config)
		turn, err := chat.Send(context.Background(), "run", nil)
		if err != nil || turn.Outcome != p.OutcomeUnsupported || len(fp.captured()) != MaxProviderRequestsPerTurn || len(tool.captured()) != MaxProviderRequestsPerTurn-1 {
			t.Fatalf("request limit turn=%+v requests=%d calls=%d err=%v", turn, len(fp.captured()), len(tool.captured()), err)
		}
	})

	t.Run("invocations", func(t *testing.T) {
		tool := &fixtureTool{}
		config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}))
		fp := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
			if index == 0 {
				calls := callResult("call-1", "/safe/a")
				for n := 2; n <= MaxToolInvocationsPerTurn+1; n++ {
					extra := callResult(fmt.Sprintf("call-%d", n), "/safe/a")
					calls.ToolCalls = append(calls.ToolCalls, extra.ToolCalls...)
					calls.Native = append(calls.Native, extra.Native...)
				}
				return calls, nil
			}
			return finalResult("not reached"), nil
		}}
		chat, _ := makeToolLoopChat(t, fp, config)
		turn, err := chat.Send(context.Background(), "run many", nil)
		if err != nil || turn.Outcome != p.OutcomeUnsupported || len(tool.captured()) != 0 || len(fp.captured()) != 1 {
			t.Fatalf("invocation limit turn=%+v requests=%d calls=%d err=%v", turn, len(fp.captured()), len(tool.captured()), err)
		}
	})
}

func TestRuntimeConversationGrantReuseAndCloseClear(t *testing.T) {
	tool := &fixtureTool{}
	approvals := 0
	config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvals++
		return permissions.ApprovalConversation, "", nil
	}))
	fp := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
		switch index {
		case 0, 2:
			return callResult(fmt.Sprintf("call-%d", index), "/safe/same"), nil
		default:
			return finalResult("done"), nil
		}
	}}
	chat, _ := makeToolLoopChat(t, fp, config)
	if _, err := chat.Send(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Send(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
	if approvals != 1 || len(tool.captured()) != 2 {
		t.Fatalf("conversation grant not reused: approvals=%d calls=%d", approvals, len(tool.captured()))
	}
	conversationID := tool.captured()[0].ConversationID
	if _, grants := config.Grants.Snapshot(conversationID); len(grants) == 0 {
		t.Fatal("conversation grant disappeared before shutdown")
	}
	discovery := chat.Discover(context.Background())
	var otherModel string
	for _, item := range discovery.Models {
		if item.ID == "tool-model-next" {
			otherModel = item.Key()
		}
	}
	if err := chat.Select(context.Background(), otherModel, false); !errors.Is(err, ErrConversationReset) {
		t.Fatalf("model change without confirmation error=%v", err)
	}
	if err := chat.Select(context.Background(), otherModel, true); err != nil {
		t.Fatal(err)
	}
	if _, grants := config.Grants.Snapshot(conversationID); len(grants) != 0 {
		t.Fatalf("confirmed model reset retained conversation grants: %+v", grants)
	}
	chat.Close()
	if _, grants := config.Grants.Snapshot(conversationID); len(grants) != 0 {
		t.Fatal("shutdown retained conversation grants")
	}
}

func TestRuntimeRejectsUnknownAndMalformedProviderCalls(t *testing.T) {
	for _, test := range []struct {
		name       string
		completion p.CompletionKind
		toolID     tools.ID
	}{
		{name: "unknown tool", completion: p.CompletionToolCalls, toolID: "unknown"},
		{name: "malformed completion", completion: p.CompletionFinal, toolID: "lookup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := &fixtureTool{}
			config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return permissions.ApprovalOnce, "", nil
			}))
			fp := &toolLoopProvider{step: func(_ int, _ p.ChatRequest) (p.Result, error) {
				result := callResult("call-a", "/safe/a")
				result.ToolCalls[0].ToolID = test.toolID
				result.Completion = test.completion
				return result, nil
			}}
			chat, _ := makeToolLoopChat(t, fp, config)
			turn, err := chat.Send(context.Background(), "try invalid call", nil)
			if err != nil || turn.Outcome != p.OutcomeUnsupported || len(tool.captured()) != 0 || len(fp.captured()) != 1 {
				t.Fatalf("invalid response was dispatched/continued: turn=%+v calls=%d requests=%d err=%v", turn, len(tool.captured()), len(fp.captured()), err)
			}
		})
	}
}

func TestRuntimeCancellationWhileApprovalIsPending(t *testing.T) {
	tool := &fixtureTool{}
	entered := make(chan struct{})
	config := newToolConfig(tool, ToolApprovalFunc(func(ctx context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		close(entered)
		<-ctx.Done()
		return permissions.ApprovalOnce, "", nil
	}))
	fp := &toolLoopProvider{step: func(_ int, _ p.ChatRequest) (p.Result, error) { return callResult("call-a", "/safe/a"), nil }}
	chat, _ := makeToolLoopChat(t, fp, config)
	done := make(chan Turn, 1)
	go func() {
		turn, _ := chat.Send(context.Background(), "cancel pending", nil)
		done <- turn
	}()
	<-entered
	// Snapshot takes the chat mutex. It must remain responsive while the
	// approval adapter waits, proving Send does not hold that mutex here.
	_ = chat.Snapshot()
	chat.Cancel()
	turn := <-done
	if turn.Outcome != p.OutcomeInterrupted || len(tool.captured()) != 0 || len(fp.captured()) != 1 || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != tools.OutcomeNotExecuted {
		t.Fatalf("cancellation did not prevent pending execution: turn=%+v calls=%d requests=%d", turn, len(tool.captured()), len(fp.captured()))
	}
}

func TestRuntimeCancellationAfterExecutionRetainsResultWithoutContinuation(t *testing.T) {
	tool := &fixtureTool{content: "side effect completed"}
	started := make(chan struct{})
	config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}))
	config.Enforcer = ToolEnforcerFunc(func(ctx context.Context, implementation tools.Tool, invocation tools.Invocation, _ permissions.Request) (tools.Result, error) {
		result := implementation.Execute(ctx, invocation)
		close(started)
		<-ctx.Done()
		return result, nil
	})
	fp := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
		if index == 0 {
			return callResult("call-a", "/safe/a"), nil
		}
		if len(request.Messages) < 3 || request.Messages[2].ToolResult == nil {
			t.Errorf("follow-up omitted the completed tool result: %+v", request.Messages)
		}
		return finalResult("continued without replay"), nil
	}}
	chat, _ := makeToolLoopChat(t, fp, config)
	done := make(chan Turn, 1)
	go func() {
		turn, _ := chat.Send(context.Background(), "run", nil)
		done <- turn
	}()
	<-started
	chat.Cancel()
	turn := <-done
	if turn.Outcome != p.OutcomeInterrupted || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != tools.OutcomeSucceeded || len(fp.captured()) != 1 {
		t.Fatalf("cancellation lost completed side-effect truth: turn=%+v requests=%d", turn, len(fp.captured()))
	}
	if _, err := chat.Send(context.Background(), "new turn", nil); err != nil {
		t.Fatal(err)
	}
	if len(tool.captured()) != 1 || len(fp.captured()) != 2 {
		t.Fatalf("cancelled action was replayed: calls=%d requests=%d", len(tool.captured()), len(fp.captured()))
	}
}

func TestRuntimePreservesPartialAndUnknownOutcomes(t *testing.T) {
	for _, outcome := range []tools.Outcome{tools.OutcomePartial, tools.OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			tool := &fixtureTool{content: "some output", outcome: outcome}
			config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return permissions.ApprovalOnce, "", nil
			}))
			fp := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
				if index == 0 {
					return callResult("call-a", "/safe/a"), nil
				}
				toolResult := request.Messages[2].ToolResult
				if toolResult == nil || !toolResult.IsError || !strings.Contains(toolResult.Content, `"outcome":"`+string(outcome)+`"`) {
					t.Errorf("unsafe tool result continuation: %+v", toolResult)
				}
				return finalResult("handled"), nil
			}}
			chat, _ := makeToolLoopChat(t, fp, config)
			turn, err := chat.Send(context.Background(), "run", nil)
			if err != nil || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != outcome {
				t.Fatalf("tool outcome=%+v err=%v", turn, err)
			}
		})
	}
}

func TestRuntimeEnforcesPerResultAndPerTurnResultBudgets(t *testing.T) {
	tool := &fixtureTool{content: strings.Repeat("x", MaxToolResultBytes+100)}
	config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}))
	fp := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
		if index == 0 {
			result := callResult("call-1", "/safe/a")
			for n := 2; n <= 5; n++ {
				extra := callResult(fmt.Sprintf("call-%d", n), "/safe/a")
				result.ToolCalls = append(result.ToolCalls, extra.ToolCalls...)
				result.Native = append(result.Native, extra.Native...)
			}
			return result, nil
		}
		var total int
		var truncated bool
		for _, message := range request.Messages {
			if message.ToolResult != nil {
				total += len(message.ToolResult.Content)
				truncated = truncated || strings.Contains(message.ToolResult.Content, `"truncated":true`)
				if len(message.ToolResult.Content) > MaxToolResultBytes {
					t.Errorf("individual result exceeded bound: %d", len(message.ToolResult.Content))
				}
			}
		}
		if total > MaxToolResultBytesPerTurn || !truncated {
			t.Errorf("turn result budget total=%d truncated=%v", total, truncated)
		}
		return finalResult("bounded"), nil
	}}
	chat, _ := makeToolLoopChat(t, fp, config)
	turn, err := chat.Send(context.Background(), "run five", nil)
	if err != nil || turn.Outcome != p.OutcomeCompleted || len(turn.ToolActivities) != 5 {
		t.Fatalf("bounded turn=%+v err=%v", turn, err)
	}
}

func TestRuntimeUnmatchedToolResultIDBecomesUnknownAndIsNotReplayed(t *testing.T) {
	tool := &fixtureTool{content: "maybe completed"}
	config := newToolConfig(tool, ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}))
	config.Enforcer = ToolEnforcerFunc(func(ctx context.Context, implementation tools.Tool, invocation tools.Invocation, _ permissions.Request) (tools.Result, error) {
		result := implementation.Execute(ctx, invocation)
		result.ProviderCallID = "wrong-call-id"
		return result, nil
	})
	fp := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
		if index == 0 {
			return callResult("expected-call", "/safe/a"), nil
		}
		if len(request.Messages) > 2 && !strings.Contains(request.Messages[2].ToolResult.Content, `"outcome":"unknown"`) {
			t.Errorf("mismatched result was not downgraded: %+v", request.Messages[2].ToolResult)
		}
		return finalResult("handled"), nil
	}}
	chat, _ := makeToolLoopChat(t, fp, config)
	turn, err := chat.Send(context.Background(), "run", nil)
	if err != nil || turn.ToolActivities[0].Outcome != tools.OutcomeUnknown || len(tool.captured()) != 1 {
		t.Fatalf("mismatched tool result=%+v err=%v", turn, err)
	}
}
