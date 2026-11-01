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

func TestOAuthToolRequestIsRejectedBeforeNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	definition := chattools.Definition{ID: "lookup", Description: "Look up a value", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 32}
	result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "hi"}}, Tools: []chattools.Definition{definition}}, nil)
	if err != nil || called || result.Outcome != p.OutcomeUnsupported || result.DiagnosticCode != p.DiagnosticToolProtocolRouteUnsupported {
		t.Fatalf("result=%+v called=%v err=%v", result, called, err)
	}
}
