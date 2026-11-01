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
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	p "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

func bundledCall(callID string, toolID tools.ID, args string) p.Result {
	arguments := json.RawMessage(args)
	native, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-" + callID, "call_id": callID, "name": string(toolID), "arguments": args, "status": "completed"})
	return p.Result{Outcome: p.OutcomeCompleted, Completion: p.CompletionToolCalls, Native: []json.RawMessage{native}, ToolCalls: []p.ToolCall{{CallID: callID, ToolID: toolID, Arguments: arguments}}}
}

func TestBundledConversationToolsAuthorizeExecuteAndContinue(t *testing.T) {
	answer := "  literal [blue] answer 雪  "
	var approved []permissions.PendingApproval
	approval := ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approved = append(approved, pending)
		return permissions.ApprovalOnce, "", nil
	})
	clarification := ToolClarificationFunc(func(_ context.Context, request ClarificationRequest) (ClarificationResponse, error) {
		if request.ConversationID == "" || request.ID == "" || request.Question != "Which format?" || len(request.Options) != 1 {
			t.Errorf("clarification request=%+v", request)
		}
		return ClarificationResponse{ID: request.ID, ConversationID: request.ConversationID, Outcome: ClarificationAnswered, Answer: answer}, nil
	})
	config := BundledConversationToolRuntimeConfig(approval, clarification)
	if config.Registry == nil || len(config.Registry.Definitions()) != 2 || config.Registry.Definitions()[0].ID != tools.RequestUserInputID || config.Registry.Definitions()[1].ID != tools.UpdatePlanID {
		t.Fatalf("bundled registry definitions=%+v", config.Registry.Definitions())
	}
	provider := &toolLoopProvider{step: func(index int, request p.ChatRequest) (p.Result, error) {
		if len(request.Tools) != 2 || !strings.Contains(request.Instructions, "request_user_input, update_plan") || strings.Contains(request.Instructions, "Do not claim to call tools") {
			t.Errorf("tool exposure/instructions mismatch: tools=%+v instructions=%q", request.Tools, request.Instructions)
		}
		switch index {
		case 0:
			return bundledCall("plan-call", tools.UpdatePlanID, `{"plan":[{"step":"inspect","status":"completed"},{"step":"implement","status":"in_progress"}]}`), nil
		case 1:
			if len(request.Messages) < 2 || request.Messages[len(request.Messages)-1].ToolResult == nil || !strings.Contains(request.Messages[len(request.Messages)-1].ToolResult.Content, "Current conversation plan replaced") {
				t.Errorf("plan result did not continue to provider: %+v", request.Messages)
			}
			return bundledCall("input-call", tools.RequestUserInputID, `{"question":"Which format?","options":[{"label":"JSON","description":"structured"}]}`), nil
		case 2:
			message := request.Messages[len(request.Messages)-1]
			if message.ToolResult == nil || !strings.Contains(message.ToolResult.Content, answer) {
				t.Errorf("exact clarification answer missing from continuation: %+v", message)
			}
			return p.Result{Outcome: p.OutcomeCompleted, Completion: p.CompletionFinal, Text: "finished", Native: []json.RawMessage{json.RawMessage(`{"type":"message"}`)}}, nil
		default:
			t.Fatalf("unexpected provider request %d", index)
			return p.Result{}, nil
		}
	}}
	providers, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: t.TempDir() + "/auth.json"}, nil, config)
	t.Cleanup(chat.Close)
	if err := chat.AddProfile(context.Background(), "profile", string(p.OpenAI), "fixture", "not-a-real-secret", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	turn, err := chat.Send(context.Background(), "make a plan and ask me", nil)
	if err != nil || turn.Outcome != p.OutcomeCompleted || turn.Text != "finished" || len(provider.captured()) != 3 {
		t.Fatalf("turn=%+v requests=%d err=%v", turn, len(provider.captured()), err)
	}
	if len(approved) != 2 {
		t.Fatalf("approvals=%d, want one per call", len(approved))
	}
	for _, pending := range approved {
		if len(pending.Request.Requirements) != 2 || pending.Request.Requirements[1].Resource.Kind != permissions.ResourceConversation || pending.Request.Requirements[1].Resource.Target != pending.Request.ConversationID {
			t.Fatalf("approval did not bind exact conversation resource: %+v", pending.Request)
		}
	}
	plan := chat.Snapshot().Plan
	if len(plan) != 2 || plan[0].Status != tools.PlanCompleted || plan[1].Status != tools.PlanInProgress {
		t.Fatalf("current plan snapshot=%+v", plan)
	}
	activities := chat.Snapshot().ToolActivities
	finished := 0
	for _, activity := range activities {
		if activity.Phase == "finished" && activity.Outcome == tools.OutcomeSucceeded {
			finished++
		}
	}
	if finished != 2 {
		t.Fatalf("tool outcomes=%+v", activities)
	}

	discovery := chat.Discover(context.Background())
	if len(discovery.Models) < 2 {
		t.Fatalf("fixture models=%+v", discovery.Models)
	}
	if err := chat.Select(context.Background(), discovery.Models[1].Key(), true); err != nil {
		t.Fatal(err)
	}
	if plan := chat.Snapshot().Plan; len(plan) != 0 {
		t.Fatalf("confirmed conversation reset retained plan: %+v", plan)
	}
}

func TestUpdatePlanToolClearsExistingPlanWithEmptyArray(t *testing.T) {
	plans := NewConversationPlans()
	tool := &updatePlanTool{plans: conversationPlanUpdater{plans: plans}}
	if err := plans.Replace("conversation", []tools.PlanStep{{Step: "existing step", Status: tools.PlanInProgress}}); err != nil {
		t.Fatal(err)
	}
	result := tool.Execute(context.Background(), tools.Invocation{
		ConversationID: "conversation",
		ProviderCallID: "clear-call",
		ToolID:         tools.UpdatePlanID,
		Arguments:      json.RawMessage(`{"plan":[]}`),
	})
	if result.Outcome != tools.OutcomeSucceeded || result.ErrorCode != "" {
		t.Fatalf("empty-plan update result = %+v", result)
	}
	if got := plans.Snapshot("conversation"); got == nil || len(got) != 0 {
		t.Fatalf("empty-plan update snapshot = %#v, want non-nil empty plan", got)
	}
}

func TestClarificationDeclineContinuesAndCancelStopsProvider(t *testing.T) {
	for _, test := range []struct {
		name        string
		interaction ClarificationOutcome
		wantTurn    p.Outcome
		wantCalls   int
	}{
		{"decline", ClarificationDeclined, p.OutcomeCompleted, 2},
		{"cancel", ClarificationCancelled, p.OutcomeInterrupted, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return permissions.ApprovalOnce, "", nil
			})
			clarification := ToolClarificationFunc(func(_ context.Context, request ClarificationRequest) (ClarificationResponse, error) {
				return ClarificationResponse{ID: request.ID, ConversationID: request.ConversationID, Outcome: test.interaction}, nil
			})
			config := BundledConversationToolRuntimeConfig(approval, clarification)
			provider := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
				if index == 0 {
					return bundledCall("input-call", tools.RequestUserInputID, `{"question":"Need context?","options":[]}`), nil
				}
				return p.Result{Outcome: p.OutcomeCompleted, Completion: p.CompletionFinal, Text: "continued", Native: []json.RawMessage{json.RawMessage(`{"type":"message"}`)}}, nil
			}}
			providers, err := model.NewRegistry(provider)
			if err != nil {
				t.Fatal(err)
			}
			chat := NewChatWithToolRuntime(providers, auth.Store{Path: t.TempDir() + "/auth.json"}, nil, config)
			defer chat.Close()
			if err := chat.AddProfile(context.Background(), "profile", string(p.OpenAI), "fixture", "not-a-real-secret", nil); err != nil {
				t.Fatal(err)
			}
			selectFirst(t, chat)
			turn, err := chat.Send(context.Background(), "ask", nil)
			if test.interaction == ClarificationCancelled {
				if turn.Outcome != test.wantTurn {
					t.Fatalf("cancel turn=%+v err=%v", turn, err)
				}
			} else if turn.Outcome != test.wantTurn || err != nil {
				t.Fatalf("decline turn=%+v err=%v", turn, err)
			}
			if got := len(provider.captured()); got != test.wantCalls {
				t.Fatalf("provider requests=%d want=%d", got, test.wantCalls)
			}
		})
	}
}

func TestConversationGrantReusesToolActionButOnceGrantDoesNot(t *testing.T) {
	for _, test := range []struct {
		name          string
		choice        permissions.ApprovalChoice
		wantApprovals int
	}{
		{"conversation", permissions.ApprovalConversation, 1},
		{"once", permissions.ApprovalOnce, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			approvalCount := 0
			approval := ToolApprovalFunc(func(_ context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				approvalCount++
				return test.choice, "", nil
			})
			clarification := ToolClarificationFunc(func(_ context.Context, request ClarificationRequest) (ClarificationResponse, error) {
				return ClarificationResponse{ID: request.ID, ConversationID: request.ConversationID, Outcome: ClarificationDeclined}, nil
			})
			provider := &toolLoopProvider{step: func(index int, _ p.ChatRequest) (p.Result, error) {
				switch index {
				case 0:
					return bundledCall("plan-one", tools.UpdatePlanID, `{"plan":[{"step":"first","status":"pending"}]}`), nil
				case 1:
					return bundledCall("plan-two", tools.UpdatePlanID, `{"plan":[{"step":"replacement","status":"in_progress"}]}`), nil
				default:
					return p.Result{Outcome: p.OutcomeCompleted, Completion: p.CompletionFinal, Text: "done", Native: []json.RawMessage{json.RawMessage(`{"type":"message"}`)}}, nil
				}
			}}
			providers, err := model.NewRegistry(provider)
			if err != nil {
				t.Fatal(err)
			}
			chat := NewChatWithToolRuntime(providers, auth.Store{Path: t.TempDir() + "/auth.json"}, nil, BundledConversationToolRuntimeConfig(approval, clarification))
			defer chat.Close()
			if err := chat.AddProfile(context.Background(), "profile", string(p.OpenAI), "fixture", "not-a-real-secret", nil); err != nil {
				t.Fatal(err)
			}
			selectFirst(t, chat)
			turn, err := chat.Send(context.Background(), "update the plan twice", nil)
			if err != nil || turn.Outcome != p.OutcomeCompleted {
				t.Fatalf("turn=%+v err=%v", turn, err)
			}
			if approvalCount != test.wantApprovals {
				t.Fatalf("approval count=%d want=%d", approvalCount, test.wantApprovals)
			}
			if plan := chat.Snapshot().Plan; len(plan) != 1 || plan[0].Step != "replacement" || plan[0].Status != tools.PlanInProgress {
				t.Fatalf("deterministic replacement plan=%+v", plan)
			}
		})
	}
}

func TestBundledToolsAreUnavailableWithoutTrustedInteraction(t *testing.T) {
	if config := BundledConversationToolRuntimeConfig(nil, ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, nil
	})); config.Registry != nil {
		t.Fatal("tools registered without trusted approval UI")
	}
	if config := BundledConversationToolRuntimeConfig(ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	}), nil); config.Registry != nil {
		t.Fatal("tools registered without trusted clarification UI")
	}
}

func TestBundledToolsAreAdvertisedOnlyOnExplicitlySupportedRoutes(t *testing.T) {
	approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	})
	clarification := ToolClarificationFunc(func(context.Context, ClarificationRequest) (ClarificationResponse, error) {
		return ClarificationResponse{}, nil
	})
	runtime := newToolRuntime(BundledConversationToolRuntimeConfig(approval, clarification))
	provider := &toolLoopProvider{}
	if definitions := runtime.definitions(provider, p.AuthAPIKey); len(definitions) != 2 {
		t.Fatalf("supported API-key route definitions=%+v", definitions)
	}
	if definitions := runtime.definitions(provider, p.AuthOAuth); len(definitions) != 0 {
		t.Fatalf("unknown OAuth route exposed tools: %+v", definitions)
	}
	provider.routeSupport = p.SupportUnsupported
	if definitions := runtime.definitions(provider, p.AuthAPIKey); len(definitions) != 0 {
		t.Fatalf("unsupported API-key route exposed tools: %+v", definitions)
	}
}

func TestChatShutdownCancelsPendingClarificationWithoutContinuation(t *testing.T) {
	approval := ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	})
	entered := make(chan ClarificationRequest, 1)
	clarification := ToolClarificationFunc(func(ctx context.Context, request ClarificationRequest) (ClarificationResponse, error) {
		entered <- request
		<-ctx.Done()
		return ClarificationResponse{}, ctx.Err()
	})
	provider := &toolLoopProvider{step: func(_ int, _ p.ChatRequest) (p.Result, error) {
		return bundledCall("pending-input", tools.RequestUserInputID, `{"question":"Wait?","options":[]}`), nil
	}}
	providers, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatWithToolRuntime(providers, auth.Store{Path: t.TempDir() + "/auth.json"}, nil, BundledConversationToolRuntimeConfig(approval, clarification))
	if err := chat.AddProfile(context.Background(), "profile", string(p.OpenAI), "fixture", "not-a-real-secret", nil); err != nil {
		t.Fatal(err)
	}
	selectFirst(t, chat)
	done := make(chan struct {
		turn Turn
		err  error
	}, 1)
	go func() {
		turn, err := chat.Send(context.Background(), "ask", nil)
		done <- struct {
			turn Turn
			err  error
		}{turn, err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tool did not reach clarification wait")
	}
	chat.Close()
	select {
	case result := <-done:
		if result.turn.Outcome != p.OutcomeInterrupted {
			t.Fatalf("shutdown turn=%+v err=%v", result.turn, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel clarification wait")
	}
	if got := len(provider.captured()); got != 1 {
		t.Fatalf("shutdown sent a continuation request: count=%d", got)
	}
	if plan := chat.Snapshot().Plan; len(plan) != 0 {
		t.Fatalf("shutdown retained conversation plan: %+v", plan)
	}
}
