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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	p "github.com/ayeshLK/komposyn/plugins/model-provider"
	openaiadapter "github.com/ayeshLK/komposyn/plugins/model-provider/openai"
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

func TestVerifiedOpenAIOAuthModelRunsPlanAndClarificationThroughRuntime(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := len(requests) + 1
		if got := r.Header.Get("Authorization"); got != "Bearer access-token-1" {
			t.Errorf("request %d authorization=%q", requestNumber, got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		if body["model"] != "gpt-6-sol" || body["store"] != false || body["stream"] != true {
			t.Errorf("request %d route fields=%v", requestNumber, body)
		}
		if _, exists := body["tools"]; exists {
			t.Errorf("request %d used API-key tools field: %v", requestNumber, body)
		}
		if _, exists := body["max_output_tokens"]; exists {
			t.Errorf("request %d used unsupported API-key output bound: %v", requestNumber, body)
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) < 2 {
			t.Errorf("request %d omitted Responses input/additional_tools: %v", requestNumber, body)
		}
		if !containsAdditionalTools(input, string(tools.UpdatePlanID), string(tools.RequestUserInputID)) {
			t.Errorf("request %d did not carry both client tool definitions: %v", requestNumber, input)
		}
		var callID, toolID, arguments string
		switch requestNumber {
		case 1:
			callID, toolID, arguments = "plan-call", string(tools.UpdatePlanID), `{"plan":[{"step":"verify route","status":"completed"}]}`
		case 2:
			if !containsFunctionOutput(input, "plan-call") {
				t.Error("plan result was not correlated in the provider continuation")
			}
			callID, toolID, arguments = "input-call", string(tools.RequestUserInputID), `{"question":"Which format?","options":[{"label":"JSON","description":"structured"}]}`
		case 3:
			if !containsFunctionOutput(input, "input-call") {
				t.Error("clarification answer was not correlated in the provider continuation")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"finished\"}\n\n")
			fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n")
			return
		default:
			t.Errorf("unexpected provider request %d", requestNumber)
			return
		}
		writeFunctionCallSSE(w, "item-"+callID, callID, toolID, arguments)
	}))
	defer server.Close()

	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "profile", Provider: "openai", Label: "ChatGPT fixture", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "fixture-client", Issuer: "https://auth.openai.com", Subject: "fixture-subject", HostID: hostID, IDToken: "fixture-id", AccessToken: "stored-access", RefreshToken: "stored-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	providers, err := model.NewRegistry(openaiadapter.Plugin{BaseURL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	resolvers := auth.NewRegistry()
	if err := resolvers.Register("openai", auth.MethodOAuth, &rotatingOAuthResolver{store: store}); err != nil {
		t.Fatal(err)
	}
	var approvals []permissions.PendingApproval
	config := BundledConversationToolRuntimeConfig(
		ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			approvals = append(approvals, pending)
			return permissions.ApprovalOnce, "", nil
		}),
		ToolClarificationFunc(func(_ context.Context, request ClarificationRequest) (ClarificationResponse, error) {
			if request.Question != "Which format?" || len(request.Options) != 1 || request.Options[0].Label != "JSON" {
				t.Errorf("clarification request=%+v", request)
			}
			return ClarificationResponse{ID: request.ID, ConversationID: request.ConversationID, Outcome: ClarificationAnswered, Answer: "JSON"}, nil
		}),
	)
	chat := NewChatWithToolRuntime(providers, store, resolvers, config)
	t.Cleanup(chat.Close)
	selected := model.Model{Provider: p.OpenAI, AuthMethod: p.AuthOAuth, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "gpt-6-sol", DisplayName: "GPT-6-Sol", Compatibility: p.CompatibilityUnknown, ProfileVersion: model.ProfileVersion(profile, "")}
	chat.mu.Lock()
	chat.selected = &selected
	chat.mu.Unlock()

	turn, err := chat.Send(context.Background(), "Plan this synthetic check and ask me which format to use.", nil)
	if err != nil || turn.Outcome != p.OutcomeCompleted || turn.Text != "finished" || len(requests) != 3 {
		t.Fatalf("turn=%+v requests=%d err=%v", turn, len(requests), err)
	}
	if len(approvals) != 2 {
		t.Fatalf("approval count=%d; want one for each action", len(approvals))
	}
	for i, expected := range []tools.ID{tools.UpdatePlanID, tools.RequestUserInputID} {
		if len(approvals[i].Request.Requirements) != 2 || approvals[i].Request.Requirements[0].ToolID != string(expected) {
			t.Errorf("approval %d did not bind expected tool %q: %+v", i, expected, approvals[i].Request)
		}
	}
	plan := chat.Snapshot().Plan
	if len(plan) != 1 || plan[0].Step != "verify route" || plan[0].Status != tools.PlanCompleted {
		t.Fatalf("plan snapshot=%+v", plan)
	}
}

func TestOpenAIOAuthExactFileReadUsesTrustedHostAndContinues(t *testing.T) {
	const sentinel = "synthetic-private-file-content"
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	host := &publicationHost{content: []byte(sentinel), root: root, target: target}
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token-1" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		if body["model"] != "gpt-6-sol" || body["store"] != false || body["stream"] != true {
			t.Errorf("request route fields=%v", body)
		}
		input, _ := body["input"].([]any)
		if !containsAdditionalTools(input, "read_file") {
			t.Errorf("read_file definition missing: %v", input)
		}
		if len(requests) == 1 {
			writeFunctionCallSSE(w, "item-read-1", "read-1", "read_file", `{"path":"notes.txt"}`)
			return
		}
		if !containsFunctionOutput(input, "read-1") || !strings.Contains(fmt.Sprint(input), sentinel) {
			t.Errorf("approved read was not correlated in continuation: %v", input)
		}
		if output := functionOutput(input, "read-1"); len(output) == 0 || len(output) > MaxToolResultBytes {
			t.Errorf("continuation output size=%d; want a non-empty bounded result", len(output))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"summarized\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"summarized\"}]}]}}\n\n")
	}))
	defer server.Close()

	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "profile", Provider: "openai", Label: "ChatGPT fixture", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "fixture-client", Issuer: "https://auth.openai.com", Subject: "fixture-subject", HostID: hostID, IDToken: "synthetic-id", AccessToken: "stored-access", RefreshToken: "stored-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	providers, err := model.NewRegistry(openaiadapter.Plugin{BaseURL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	resolvers := auth.NewRegistry()
	if err := resolvers.Register("openai", auth.MethodOAuth, &rotatingOAuthResolver{store: store}); err != nil {
		t.Fatal(err)
	}
	var approved permissions.PendingApproval
	config := publicationReadRuntimeConfig(ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approved = pending
		return permissions.ApprovalOnce, "", nil
	}), host)
	chat := NewChatWithToolRuntime(providers, store, resolvers, config)
	t.Cleanup(chat.Close)
	if err := chat.InitializeWorkingDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected := model.Model{Provider: p.OpenAI, AuthMethod: p.AuthOAuth, ProfileID: profile.ID, ProfileLabel: profile.Label, ID: "gpt-6-sol", Compatibility: p.CompatibilityUnknown, ProfileVersion: model.ProfileVersion(stored, "publication-fixture-token")}
	chat.mu.Lock()
	chat.selected = &selected
	chat.mu.Unlock()
	turn, err := chat.Send(context.Background(), "Read notes.txt and summarize it", nil)
	if err != nil || turn.Outcome != p.OutcomeCompleted || turn.Text != "summarized" || len(requests) != 2 {
		t.Fatalf("turn=%+v requests=%d err=%v", turn, len(requests), err)
	}
	if len(approved.Request.Requirements) != 2 || approved.Request.Requirements[1].Resource == nil || approved.Request.Requirements[1].Resource.Target != target {
		t.Fatalf("approval did not bind resolved exact target %q: %+v", target, approved.Request)
	}
	destination := "openai/gpt-6-sol using auth profile profile"
	if !strings.Contains(approved.Request.Requirements[0].Description, destination) {
		t.Fatalf("approval omitted exact provider destination %q: %+v", destination, approved.Request.Requirements)
	}
	if host.reads != 1 || !strings.Contains(fmt.Sprint(requests[1]["input"]), sentinel) {
		t.Fatalf("host reads=%d; continuation did not receive approved bounded content", host.reads)
	}
}

func writeFunctionCallSSE(w http.ResponseWriter, itemID, callID, toolID, arguments string) {
	w.Header().Set("Content-Type", "text/event-stream")
	item := fmt.Sprintf(`{"type":"function_call","id":%q,"call_id":%q,"name":%q,"arguments":%q,"status":"completed"}`, itemID, callID, toolID, arguments)
	fmt.Fprintf(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":%q,\"call_id\":%q,\"name\":%q}}\n\n", itemID, callID, toolID)
	fmt.Fprintf(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":%q,\"delta\":%q}\n\n", itemID, arguments)
	fmt.Fprintf(w, "event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"item_id\":%q,\"arguments\":%q}\n\n", itemID, arguments)
	fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
	fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[%s]}}\n\n", item)
}

func containsAdditionalTools(input []any, toolIDs ...string) bool {
	for _, item := range input {
		entry, ok := item.(map[string]any)
		if !ok || entry["type"] != "additional_tools" {
			continue
		}
		definitions, ok := entry["tools"].([]any)
		if !ok {
			return false
		}
		found := map[string]bool{}
		for _, definition := range definitions {
			object, ok := definition.(map[string]any)
			if ok {
				found[fmt.Sprint(object["name"])] = true
			}
		}
		for _, id := range toolIDs {
			if !found[id] {
				return false
			}
		}
		return true
	}
	return false
}

func containsFunctionOutput(input []any, callID string) bool {
	for _, item := range input {
		object, ok := item.(map[string]any)
		if ok && object["type"] == "function_call_output" && object["call_id"] == callID {
			return true
		}
	}
	return false
}

func functionOutput(input []any, callID string) string {
	for _, item := range input {
		object, ok := item.(map[string]any)
		if ok && object["type"] == "function_call_output" && object["call_id"] == callID {
			output, _ := object["output"].(string)
			return output
		}
	}
	return ""
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

func TestUpdatePlanToolAcceptsDecodedValuesWhoseStoreEncodingExpands(t *testing.T) {
	var arguments strings.Builder
	arguments.WriteString(`{"plan":[`)
	stepText := strings.Repeat("<", tools.MaxPlanStepRunes)
	for i := 0; i < tools.MaxPlanSteps; i++ {
		if i > 0 {
			arguments.WriteByte(',')
		}
		arguments.WriteString(`{"step":"`)
		arguments.WriteString(stepText)
		arguments.WriteString(`","status":"pending"}`)
	}
	arguments.WriteString(`]}`)
	raw := json.RawMessage(arguments.String())
	if len(raw) >= tools.MaxPlanInputBytes {
		t.Fatalf("fixture wire size=%d, should be below %d", len(raw), tools.MaxPlanInputBytes)
	}
	input, err := tools.ParseUpdatePlan(raw)
	if err != nil {
		t.Fatalf("valid decoded plan rejected: %v", err)
	}
	encoded, err := json.Marshal(tools.UpdatePlanInput{Plan: input.Plan})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= tools.MaxPlanInputBytes {
		t.Fatalf("fixture internal encoding size=%d, expected expansion beyond wire bound", len(encoded))
	}

	plans := NewConversationPlans()
	tool := &updatePlanTool{plans: conversationPlanUpdater{plans: plans}}
	result := tool.Execute(context.Background(), tools.Invocation{
		ConversationID: "conversation",
		ProviderCallID: "expanded-call",
		ToolID:         tools.UpdatePlanID,
		Arguments:      raw,
	})
	if result.Outcome != tools.OutcomeSucceeded {
		t.Fatalf("expanded internal encoding update result = %+v", result)
	}
	if got := plans.Snapshot("conversation"); len(got) != tools.MaxPlanSteps || got[0].Step != stepText {
		t.Fatalf("expanded internal encoding snapshot has %d steps, first=%+v", len(got), got[:min(len(got), 1)])
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
	if definitions := runtime.definitions(provider, p.AuthAPIKey, "fixture-model"); len(definitions) != 2 {
		t.Fatalf("supported API-key route definitions=%+v", definitions)
	}
	if definitions := runtime.definitions(provider, p.AuthOAuth, "fixture-model"); len(definitions) != 0 {
		t.Fatalf("unknown OAuth route exposed tools: %+v", definitions)
	}
	provider.routeSupport = p.SupportUnsupported
	if definitions := runtime.definitions(provider, p.AuthAPIKey, "fixture-model"); len(definitions) != 0 {
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
