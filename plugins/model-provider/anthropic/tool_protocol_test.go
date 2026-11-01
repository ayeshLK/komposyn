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

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	p "github.com/ayeshLK/komposyn/plugins/model-provider"
	chattools "github.com/ayeshLK/komposyn/plugins/tools"
)

func TestMessagesToolUseAndCorrelatedResultEncoding(t *testing.T) {
	var request map[string]any
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 2 {
			for _, event := range []string{
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-2\",\"model\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\n",
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n",
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			} {
				fmt.Fprint(w, event)
			}
			return
		}
		for _, event := range []string{
			`event: message_start` + "\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\n",
			`event: content_block_start` + "\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-call-1\",\"name\":\"lookup\",\"input\":{}}}\n\n",
			`event: content_block_delta` + "\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\\\"hello\\\"}\"}}\n\n",
			`event: content_block_stop` + "\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			`event: message_delta` + "\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":2}}\n\n",
			`event: message_stop` + "\ndata: {\"type\":\"message_stop\"}\n\n",
		} {
			fmt.Fprint(w, event)
		}
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`), MaxInput: 128}
	plugin := Plugin{BaseURL: server.URL, Client: server.Client()}
	result, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{APIKey: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}, MaxOutput: 32}, nil)
	if err != nil || result.Outcome != p.OutcomeCompleted || result.Completion != p.CompletionToolCalls || len(result.ToolCalls) != 1 || len(result.Native) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.ToolCalls[0].CallID != "tool-call-1" || string(result.ToolCalls[0].Arguments) != `{"q":"hello"}` {
		t.Fatalf("tool calls=%+v", result.ToolCalls)
	}
	if len(request["tools"].([]any)) != 1 {
		t.Fatalf("tools not sent: %v", request)
	}

	// The next provider request must place the correlated result in a user message.
	continued, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{APIKey: "fixture"}, Messages: []p.Message{
		{Role: p.RoleAssistant, Native: result.Native},
		{Role: p.RoleTool, ToolResult: &p.ToolResult{CallID: "tool-call-1", Content: "found", IsError: true}},
	}, Tools: []chattools.Definition{definition}, MaxOutput: 32}, nil)
	if err != nil || continued.Outcome != p.OutcomeCompleted {
		t.Fatalf("continuation=%+v err=%v", continued, err)
	}
	messages := request["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	content := last["content"].([]any)
	toolResult := content[0].(map[string]any)
	if last["role"] != "user" || toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "tool-call-1" || toolResult["content"] != "found" || toolResult["is_error"] != true {
		t.Fatalf("tool result was not correlated: %v", request["messages"])
	}
}

func TestToolDefinitionValidationAndRouteCapabilities(t *testing.T) {
	if got := (Plugin{}).RouteCapabilities(p.AuthAPIKey).ClientTools; got != p.SupportSupported {
		t.Fatalf("API key tool capability=%q", got)
	}
	if err := p.ValidateToolDefinitions([]chattools.Definition{{ID: "lookup", Description: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 5}}); err != nil {
		t.Fatal(err)
	}
	if code := p.ToolCallsDiagnostic([]p.ToolCall{{CallID: "x", ToolID: "missing", Arguments: json.RawMessage(`{}`)}}, nil); code != p.DiagnosticToolProtocolUnknown {
		t.Fatalf("unknown tool diagnostic=%q", code)
	}
}

func TestToolUseRejectsDeltaAfterBlockClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call-1\",\"name\":\"lookup\",\"input\":{}}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\\\"after-close\\\"}\"}}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		} {
			fmt.Fprint(w, event)
		}
	}))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 64}
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{APIKey: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "find"}}, Tools: []chattools.Definition{definition}, MaxOutput: 32}, nil)
	if err == nil || result.Completion != "" || len(result.ToolCalls) != 0 {
		t.Fatalf("post-close delta yielded a tool call: result=%+v err=%v", result, err)
	}
}

func TestTextStreamAllowsPingAfterTerminalDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
			"event: ping\ndata: {\"type\":\"ping\"}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		} {
			fmt.Fprint(w, event)
		}
	}))
	defer server.Close()
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{APIKey: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "hi"}}, MaxOutput: 32}, nil)
	if err != nil || result.Outcome != p.OutcomeCompleted || result.Completion != p.CompletionFinal || result.Text != "done" || len(result.Native) != 1 {
		t.Fatalf("valid ping disrupted terminal text response: result=%+v err=%v", result, err)
	}
}
