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

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	p "github.com/ayeshLK/komposyn/plugins/model-provider"
	chattools "github.com/ayeshLK/komposyn/plugins/tools"
)

func TestResponsesFunctionCallProtocolAndContinuation(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\"}}\n\n")
		fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"item-1\",\"delta\":\"{\\\"q\\\":\"}\n\n")
		fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"item-1\",\"delta\":\"\\\"hello\\\"}\"}\n\n")
		fmt.Fprint(w, "event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"item_id\":\"item-1\",\"arguments\":\"{\\\"q\\\":\\\"hello\\\"}\"}\n\n")
		fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\\\"hello\\\"}\",\"status\":\"completed\"}}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\\\"hello\\\"}\",\"status\":\"completed\"}]}}\n\n")
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`), MaxInput: 128}
	plugin := Plugin{BaseURL: server.URL, Client: server.Client()}
	result, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{APIKey: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}, MaxOutput: 32}, nil)
	if err != nil || result.Outcome != p.OutcomeCompleted || result.Completion != p.CompletionToolCalls || len(result.ToolCalls) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	call := result.ToolCalls[0]
	if call.CallID != "call-1" || call.ToolID != definition.ID || string(call.Arguments) != `{"q":"hello"}` || len(result.Native) != 1 {
		t.Fatalf("call=%+v native=%s", call, result.Native)
	}
	if _, ok := request["tools"]; !ok {
		t.Fatalf("tool definitions not sent: %v", request)
	}

	if got := (Plugin{}).RouteCapabilities(p.AuthOAuth).ClientTools; got != p.SupportUnknown {
		t.Fatalf("OAuth tool capability=%q", got)
	}
}

func TestOAuthFunctionCallUsesAdditionalToolsAndContinues(t *testing.T) {
	if got := (Plugin{}).RouteCapabilities(p.AuthOAuth).ClientTools; got != p.SupportUnknown {
		t.Fatalf("OAuth capability declaration changed to %q", got)
	}
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(requests) == 1 {
			fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\"}}\n\n")
			fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"item-1\",\"delta\":\"{\\\"q\\\":\\\"hello\\\"}\"}\n\n")
			fmt.Fprint(w, "event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"item_id\":\"item-1\",\"arguments\":\"{\\\"q\\\":\\\"hello\\\"}\"}\n\n")
			fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\\\"hello\\\"}\",\"status\":\"completed\"}}\n\n")
			fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\\\"hello\\\"}\",\"status\":\"completed\"}]}}\n\n")
			return
		}
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`), MaxInput: 32}
	plugin := Plugin{BaseURL: server.URL, Client: server.Client()}
	credential := p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}
	first, err := plugin.streamChatProtocol(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: credential, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}}, nil)
	if err != nil || first.Outcome != p.OutcomeCompleted || first.Completion != p.CompletionToolCalls || len(first.ToolCalls) != 1 || first.ToolCalls[0].CallID != "call-1" || string(first.ToolCalls[0].Arguments) != `{"q":"hello"}` {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := plugin.streamChatProtocol(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: credential, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}, {Role: p.RoleAssistant, Native: first.Native}, {Role: p.RoleTool, ToolResult: &p.ToolResult{CallID: "call-1", Content: "fixture result"}}}, Tools: []chattools.Definition{definition}}, nil)
	if err != nil || second.Outcome != p.OutcomeCompleted || second.Text != "done" || len(requests) != 2 {
		t.Fatalf("second=%+v requests=%d err=%v", second, len(requests), err)
	}
	for i, request := range requests {
		if request["store"] != false || request["stream"] != true {
			t.Fatalf("request %d did not use SIWC request flags: %v", i, request)
		}
		if _, exists := request["max_output_tokens"]; exists {
			t.Fatalf("request %d included unsupported max_output_tokens", i)
		}
		if _, exists := request["tools"]; exists {
			t.Fatalf("request %d used top-level tools", i)
		}
		input := request["input"].([]any)
		additional, ok := input[1].(map[string]any)
		if !ok || additional["type"] != "additional_tools" || additional["role"] != "developer" {
			t.Fatalf("request %d additional_tools was not kept at the stable position before continuation items: %v", i, input)
		}
		toolDefs := additional["tools"].([]any)
		if len(toolDefs) != 1 || toolDefs[0].(map[string]any)["name"] != "lookup" {
			t.Fatalf("request %d tool definitions=%v", i, toolDefs)
		}
	}
	secondInput := requests[1]["input"].([]any)
	call, ok := secondInput[2].(map[string]any)
	if !ok || call["type"] != "function_call" || call["call_id"] != "call-1" {
		t.Fatalf("assistant call did not follow additional_tools: %v", secondInput)
	}
	toolResult, ok := secondInput[3].(map[string]any)
	if !ok || toolResult["type"] != "function_call_output" || toolResult["call_id"] != "call-1" || toolResult["output"] != "fixture result" {
		t.Fatalf("tool result was not correlated: %v", secondInput)
	}
}

func TestPublicOAuthToolRequestDoesNotDispatchNetworkRequest(t *testing.T) {
	dispatched := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatched = true
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}}, nil)
	if err != nil || result.Outcome != p.OutcomeUnsupported || result.DiagnosticCode != p.DiagnosticToolProtocolRouteUnsupported || dispatched {
		t.Fatalf("result=%+v err=%v dispatched=%v", result, err, dispatched)
	}
}

func TestConflictingCompletedStatusClearsToolDispatchData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"in_progress\",\"output\":[{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\",\"arguments\":\"{}\",\"status\":\"completed\"}]}}\n\n")
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 32}
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).streamChatProtocol(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{APIKey: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}, MaxOutput: 32}, nil)
	if err != nil || result.Outcome != p.OutcomeUnsupported || result.DiagnosticCode != p.DiagnosticOpenAICompletedStatusInProgress || result.Completion != "" || len(result.ToolCalls) != 0 {
		t.Fatalf("rejected response retained dispatch data: result=%+v err=%v", result, err)
	}
}

func TestOAuthIncompleteFunctionCallIsNotDispatchable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\"}}\n\n")
		fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"item-1\",\"delta\":\"{\\\"q\\\":\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\",\"output\":[{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\",\"status\":\"incomplete\"}]}}\n\n")
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 32}
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).streamChatProtocol(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}}, nil)
	if err != nil || result.Outcome != p.OutcomeUnsupported || result.Completion != "" || len(result.ToolCalls) != 0 {
		t.Fatalf("incomplete call was dispatchable: result=%+v err=%v", result, err)
	}
}

func TestOAuthMalformedFunctionCallIsNotDispatchable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: response.completed
data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"item-1","call_id":"call-1","name":"lookup","arguments":"not-json","status":"completed"}]}}

`)
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 32}
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).streamChatProtocol(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}}, nil)
	if err != nil || result.Outcome != p.OutcomeUnsupported || result.Completion != "" || len(result.ToolCalls) != 0 {
		t.Fatalf("malformed call was dispatchable: result=%+v err=%v", result, err)
	}
}
