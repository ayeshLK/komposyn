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
	"reflect"
	"strings"
	"testing"

	p "github.com/ayeshLK/komposyn/plugins/model-provider"
)

func TestDiscoverAndStreamResponsesWithNativeReplay(t *testing.T) {
	var chatBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("auth header=%q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("OpenAI-Organization") != "org" || r.Header.Get("OpenAI-Project") != "project" {
			t.Errorf("routing headers missing")
		}
		switch r.URL.Path {
		case "/v1/models":
			if r.Method != "GET" {
				t.Errorf("method=%s", r.Method)
			}
			fmt.Fprint(w, `{"data":[{"id":"text-model","owned_by":"owner"},{"id":"embedding-model"}]}`)
		case "/v1/responses":
			if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
			fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"model\":\"text-model\",\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"id\":\"r1\",\"encrypted_content\":\"opaque\"},{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":1},\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	plugin := Plugin{BaseURL: srv.URL + "/v1", Client: srv.Client()}
	cred := p.Credential{APIKey: "secret", Route: p.Routing{Organization: "org", Project: "project"}}
	models, err := plugin.ListModels(context.Background(), cred)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Compatibility != p.CompatibilityUnknown {
		t.Fatalf("models=%+v", models)
	}
	var streamed string
	res, err := plugin.StreamChat(context.Background(), p.ChatRequest{RequestID: "request", ModelID: "text-model", Credential: cred, Messages: []p.Message{{Role: p.RoleUser, Text: "first"}, {Role: p.RoleAssistant, Text: "prior", Native: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`), json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prior"}]}`)}}, {Role: p.RoleUser, Text: "second"}}, Instructions: "be concise", MaxOutput: 128}, func(s string) { streamed += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != p.OutcomeCompleted || res.RequestID != "resp-1" || res.ModelID != "text-model" || res.Text != "hello" || streamed != "hello" || !res.Usage.Known || !res.Usage.InputKnown || !res.Usage.OutputKnown || res.Usage.InputTokens != 3 || res.Usage.ReasoningTokens != 2 || !res.Usage.CacheReadKnown || res.Usage.CacheRead != 1 || res.Usage.CacheWriteKnown {
		t.Fatalf("result=%+v streamed=%q", res, streamed)
	}
	if chatBody["store"] != false || chatBody["stream"] != true || chatBody["instructions"] != "be concise" || chatBody["max_output_tokens"] != float64(128) {
		t.Fatalf("request options=%v", chatBody)
	}
	if !reflect.DeepEqual(chatBody["include"], []any{"reasoning.encrypted_content"}) {
		t.Fatalf("API-key request include=%v", chatBody["include"])
	}
	if _, ok := chatBody["tools"]; ok {
		t.Fatal("tools were advertised")
	}
	items := chatBody["input"].([]any)
	if len(items) != 4 {
		t.Fatalf("input items=%d", len(items))
	}
	native, _ := json.Marshal(items[1])
	if !strings.Contains(string(native), "encrypted_content") || len(res.Native) != 2 {
		t.Fatalf("native replay lost: %s %+v", native, res.Native)
	}
}

func TestResultResponsePreservesPartialUsage(t *testing.T) {
	response := resultResponse{Target: &p.Result{}}
	if err := json.Unmarshal([]byte(`{"usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":0}},"output":[]}`), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Target.Usage.InputKnown || response.Target.Usage.OutputKnown || response.Target.Usage.Known || response.Target.Usage.InputTokens != 7 || !response.Target.Usage.CacheReadKnown || response.Target.Usage.CacheRead != 0 || response.Target.Usage.CacheWriteKnown {
		t.Fatalf("partial usage was lost or treated as complete: %+v", response.Target.Usage)
	}
}

func TestCompletedEventWithoutEmbeddedStatusReplaysNativeResponse(t *testing.T) {
	var requestBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requestBodies = append(requestBodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture answer\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-fixture\",\"model\":\"subscription-model\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"fixture answer\"}]}]}}\n\n")
	}))
	defer server.Close()
	plugin := Plugin{BaseURL: server.URL, Client: server.Client()}
	credential := p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}
	var visible string
	first, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "subscription-model", Credential: credential, Messages: []p.Message{{Role: p.RoleUser, Text: "first"}}}, func(delta string) { visible += delta })
	if err != nil || first.Outcome != p.OutcomeCompleted || visible != "fixture answer" || len(first.Native) != 1 {
		t.Fatalf("first response=%+v visible=%q err=%v", first, visible, err)
	}
	second, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "subscription-model", Credential: credential, Messages: []p.Message{{Role: p.RoleUser, Text: "first"}, {Role: p.RoleAssistant, Text: first.Text, Native: first.Native}, {Role: p.RoleUser, Text: "follow-up"}}}, nil)
	if err != nil || second.Outcome != p.OutcomeCompleted {
		t.Fatalf("follow-up=%+v err=%v", second, err)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("requests=%d", len(requestBodies))
	}
	if requestBodies[0]["store"] != false || requestBodies[1]["store"] != false {
		t.Fatal("subscription fixture enabled response storage")
	}
	input := requestBodies[1]["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("follow-up input=%v", input)
	}
	replayed, _ := json.Marshal(input[1])
	if !strings.Contains(string(replayed), "fixture answer") || !strings.Contains(string(replayed), "output_text") {
		t.Fatalf("follow-up omitted provider-native assistant item: %s", replayed)
	}
}

func TestCompletedEventWithoutReplayableResponseIsUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		code           p.DiagnosticCode
	}{
		{"missing response", "", p.DiagnosticOpenAICompletedMissingResponse},
		{"conflicting status", `,"status":"in_progress"`, p.DiagnosticOpenAICompletedStatusInProgress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible only\"}\n\n")
				if tc.response == "" {
					fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
				} else {
					fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"model\":\"m\"%s,\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"visible only\"}]}]}}\n\n", tc.response)
				}
			}))
			defer server.Close()
			result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "m", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "hi"}}}, nil)
			if err != nil || result.Outcome != p.OutcomeUnsupported || result.DiagnosticCode != tc.code {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCompletedEventUnsupportedOutputUsesBoundedDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		code         p.DiagnosticCode
	}{
		{"function call", `{"type":"function_call","name":"private-tool-name","arguments":"private arguments"}`, p.DiagnosticOpenAICompletedFunctionCall},
		{"unknown output", `{"type":"private-output-kind"}`, p.DiagnosticOpenAICompletedOtherOutputItem},
		{"unknown content", `{"type":"message","role":"assistant","content":[{"type":"private-content-kind","text":"private response text"}]}`, p.DiagnosticOpenAICompletedOtherContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible reply\"}\n\n")
				fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[%s,{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"visible reply\"}]}]}}\n\n", tc.output)
			}))
			defer server.Close()
			result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "m", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture"}, Messages: []p.Message{{Role: p.RoleUser, Text: "private prompt"}}}, nil)
			if err != nil || result.Outcome != p.OutcomeUnsupported || result.DiagnosticCode != tc.code {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !p.SafeDiagnosticCode(result.DiagnosticCode) || strings.Contains(string(result.DiagnosticCode), "private") || strings.Contains(string(result.DiagnosticCode), "visible") {
				t.Fatalf("unsafe diagnostic code %q", result.DiagnosticCode)
			}
		})
	}
}

func TestOAuthCatalogAndResponsesRequestUseSupportedSubset(t *testing.T) {
	methods := (Plugin{}).AuthMethods()
	if !reflect.DeepEqual(methods, []p.AuthMethod{p.AuthAPIKey, p.AuthOAuth}) {
		t.Fatalf("auth methods=%v", methods)
	}
	var chatBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-access-token" {
			t.Errorf("auth header=%q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("OpenAI-Organization") != "" || r.Header.Get("OpenAI-Project") != "" {
			t.Error("OAuth request unexpectedly included API-key routing headers")
		}
		switch r.URL.Path {
		case "/v1/models":
			if r.Method != http.MethodGet {
				t.Errorf("method=%s", r.Method)
			}
			fmt.Fprint(w, `{"models":[{"slug":"chat-model","display_name":"Chat Model","visibility":"list"},{"slug":"hidden-model","display_name":"Hidden","visibility":"hidden"},{"slug":"","display_name":"Missing Slug","visibility":"list"}]}`)
		case "/v1/responses":
			if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
			fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-oauth\",\"model\":\"chat-model\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}]}}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	plugin := Plugin{BaseURL: srv.URL + "/v1", Client: srv.Client()}
	credential := p.Credential{Method: p.AuthOAuth, AccessToken: "oauth-access-token", Route: p.Routing{Organization: "ignored-org", Project: "ignored-project"}}
	models, err := plugin.ListModels(context.Background(), credential)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "chat-model" || models[0].DisplayName != "Chat Model" {
		t.Fatalf("OAuth models=%+v", models)
	}
	var streamed string
	result, err := plugin.StreamChat(context.Background(), p.ChatRequest{
		ModelID: "chat-model", Credential: credential,
		Messages:     []p.Message{{Role: p.RoleUser, Text: "hello"}},
		Instructions: "be concise",
	}, func(delta string) { streamed += delta })
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != p.OutcomeCompleted || result.Text != "hello" || streamed != "hello" || len(result.Native) != 1 {
		t.Fatalf("OAuth result=%+v streamed=%q", result, streamed)
	}
	if len(chatBody) != 5 {
		t.Fatalf("OAuth request fields=%v", chatBody)
	}
	for _, key := range []string{"model", "input", "instructions", "stream", "store"} {
		if _, ok := chatBody[key]; !ok {
			t.Errorf("OAuth request omitted %q: %v", key, chatBody)
		}
	}
	for _, key := range []string{"max_output_tokens", "include", "tools"} {
		if _, ok := chatBody[key]; ok {
			t.Errorf("OAuth request included unsupported %q: %v", key, chatBody)
		}
	}
	if chatBody["store"] != false || chatBody["stream"] != true || chatBody["instructions"] != "be concise" {
		t.Fatalf("OAuth request options=%v", chatBody)
	}
	input, ok := chatBody["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("OAuth input=%#v", chatBody["input"])
	}
}

func TestStatusErrorsAreTypedAndSanitized(t *testing.T) {
	for status, want := range map[int]p.Outcome{401: p.OutcomeAuthentication, 403: p.OutcomePermission, 429: p.OutcomeRateLimited, 400: p.OutcomeUnsupported, 503: p.OutcomeService} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":"contains no useful safe detail"}`)
			}))
			defer srv.Close()
			_, err := (Plugin{BaseURL: srv.URL}).ListModels(context.Background(), p.Credential{APIKey: "secret"})
			typed, ok := err.(*p.Error)
			if !ok || typed.Outcome != want {
				t.Fatalf("error=%T %v", err, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestOAuthPlanErrorsPreserveSafeCodes(t *testing.T) {
	for _, tc := range []struct {
		status  int
		code    string
		outcome p.Outcome
	}{
		{403, "subscription_sharing_user_not_eligible", p.OutcomePermission},
		{429, "subscription_sharing_usage_limit_exceeded", p.OutcomeRateLimited},
		{503, "subscription_sharing_usage_unavailable", p.OutcomeService},
		{400, "subscription_sharing_unsupported_capability", p.OutcomeUnsupported},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("x-request-id", "request-safe")
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"error":{"code":%q,"message":"private detail"}}`, tc.code)
			}))
			defer srv.Close()
			_, err := (Plugin{BaseURL: srv.URL}).ListModels(context.Background(), p.Credential{Method: p.AuthOAuth, AccessToken: "token"})
			typed, ok := err.(*p.Error)
			if !ok || typed.Outcome != tc.outcome || typed.ProviderCode != tc.code || typed.RequestID != "request-safe" {
				t.Fatalf("typed=%+v err=%v", typed, err)
			}
			if strings.Contains(err.Error(), "private detail") || strings.Contains(err.Error(), "token") {
				t.Fatal("provider error exposed a secret or raw response")
			}
		})
	}
}

func TestCanceledStreamIsInterrupted(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	type response struct {
		result p.Result
		err    error
	}
	done := make(chan response, 1)
	delta := make(chan string, 1)
	go func() {
		result, err := (Plugin{BaseURL: srv.URL, Client: srv.Client()}).StreamChat(ctx, p.ChatRequest{ModelID: "m", Credential: p.Credential{APIKey: "secret"}, Messages: []p.Message{{Role: p.RoleUser, Text: "hi"}}, MaxOutput: 10}, func(s string) { delta <- s })
		done <- response{result: result, err: err}
	}()
	<-started
	if got := <-delta; got != "partial" {
		t.Fatalf("delta=%q", got)
	}
	cancel()
	out := <-done
	typed, ok := out.err.(*p.Error)
	if !ok || typed.Outcome != p.OutcomeInterrupted {
		t.Fatalf("canceled error=%T %v", out.err, out.err)
	}
	if out.result.Text != "partial" {
		t.Fatalf("partial response lost: %+v", out.result)
	}
}

func TestCompletedStreamItemsSupplyEmptyTerminalOutput(t *testing.T) {
	message := `{"type":"message","id":"fixture-message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}`
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
		fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", message)
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n")
	}))
	defer server.Close()
	plugin := Plugin{BaseURL: server.URL, Client: server.Client()}
	cred := p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}
	first, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture-model", Credential: cred, Messages: []p.Message{{Role: p.RoleUser, Text: "Reply with OK"}}}, nil)
	if err != nil || first.Outcome != p.OutcomeCompleted || first.Text != "OK" || len(first.Native) != 1 || first.Usage.InputTokens != 3 {
		t.Fatalf("completed stream rejected: %+v %v", first, err)
	}
	_, err = plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture-model", Credential: cred, Messages: []p.Message{{Role: p.RoleUser, Text: "Reply with OK"}, {Role: p.RoleAssistant, Text: first.Text, Native: first.Native}, {Role: p.RoleUser, Text: "Follow up"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := bodies[1]["input"].([]any)
	if len(input) != 3 || input[1].(map[string]any)["id"] != "fixture-message" {
		t.Fatalf("native continuation was lost: %v", input)
	}
	if bodies[0]["store"] != false || bodies[1]["store"] != false {
		t.Fatal("storage enabled")
	}
}

func TestStreamOutputAssemblyRejectsIncompleteAndConflictingItems(t *testing.T) {
	valid := json.RawMessage(`{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}`)
	empty := json.RawMessage(`{"status":"completed","output":[]}`)
	for _, name := range []string{"unfinished message", "missing index", "gap", "pending item", "conflicting duplicate"} {
		t.Run(name, func(t *testing.T) {
			state := completedOutput{items: map[int]json.RawMessage{}, added: map[int]bool{}}
			zero, one := 0, 1
			switch name {
			case "unfinished message":
				state.observe("response.output_item.done", &zero, json.RawMessage(`{"type":"message","role":"assistant","status":"in_progress"}`))
			case "missing index":
				state.observe("response.output_item.done", nil, valid)
			case "gap":
				state.observe("response.output_item.done", &one, valid)
			case "pending item":
				state.observe("response.output_item.done", &zero, valid)
				state.observe("response.output_item.added", &one, nil)
			case "conflicting duplicate":
				state.observe("response.output_item.done", &zero, valid)
				state.observe("response.output_item.done", &zero, json.RawMessage(`{"type":"message","role":"assistant","status":"completed","content":[]}`))
			}
			if _, ok := state.fillEmptyOutput(empty); ok {
				t.Fatal("unsafe stream assembly accepted")
			}
		})
	}
	// Exact repeats do not duplicate native output; authoritative final output is unchanged.
	state := completedOutput{items: map[int]json.RawMessage{}, added: map[int]bool{}}
	zero := 0
	state.observe("response.output_item.done", &zero, valid)
	state.observe("response.output_item.done", &zero, valid)
	merged, ok := state.fillEmptyOutput(empty)
	var obj struct{ Output []json.RawMessage }
	json.Unmarshal(merged, &obj)
	if !ok || len(obj.Output) != 1 {
		t.Fatal("duplicate stream item counted twice")
	}
	final := json.RawMessage(`{"status":"in_progress","output":[{"type":"function_call"}]}`)
	unchanged, ok := state.fillEmptyOutput(final)
	if !ok || string(unchanged) != string(final) {
		t.Fatal("explicit final output was replaced")
	}
}

func TestStreamFallbackKeepsFailureGuards(t *testing.T) {
	for _, tc := range []struct{ name, terminal, status, item, text string }{
		{"unfinished item", "response.completed", "completed", `{"type":"message","role":"assistant","status":"in_progress","content":[]}`, "OK"},
		{"conflicting status", "response.completed", "in_progress", `{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}`, "OK"},
		{"tool only", "response.completed", "completed", `{"type":"function_call","name":"fixture","arguments":"{}"}`, "OK"},
		{"invalid role", "response.completed", "completed", `{"type":"message","role":"user","status":"completed","content":[{"type":"output_text","text":"OK"}]}`, "OK"},
		{"text mismatch", "response.completed", "completed", `{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Different"}]}`, "OK"},
		{"incomplete terminal", "response.incomplete", "incomplete", `{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}`, "OK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", tc.text)
				fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", tc.item)
				fmt.Fprintf(w, "event: %s\ndata: {\"type\":%q,\"response\":{\"status\":%q,\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n", tc.terminal, tc.terminal, tc.status)
			}))
			defer server.Close()
			result, err := (Plugin{BaseURL: server.URL, Client: server.Client()}).StreamChat(context.Background(), p.ChatRequest{ModelID: "fixture", Credential: p.Credential{Method: p.AuthOAuth, AccessToken: "fixture-token"}, Messages: []p.Message{{Role: p.RoleUser, Text: "hi"}}}, nil)
			if err != nil || result.Outcome != p.OutcomeUnsupported {
				t.Fatalf("unsafe fallback accepted: %+v err=%v", result, err)
			}
			if result.Usage.InputTokens != 5 || !result.Usage.InputKnown {
				t.Fatalf("rejected request lost reported usage: %+v", result.Usage)
			}
		})
	}
}

func TestCompletedStreamOutputOrdersReasoningAndPreservesOpaqueFields(t *testing.T) {
	state := completedOutput{items: map[int]json.RawMessage{}, added: map[int]bool{}}
	zero, one := 0, 1
	state.observe("response.output_item.done", &one, json.RawMessage(`{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}`))
	state.observe("response.output_item.done", &zero, json.RawMessage(`{"type":"reasoning","encrypted_content":"fixture-opaque"}`))
	raw, ok := state.fillEmptyOutput(json.RawMessage(`{"status":"completed","output":[],"usage":{"input_tokens":4}}`))
	var response struct {
		Output []map[string]any
		Usage  struct {
			Input int64 `json:"input_tokens"`
		}
	}
	if json.Unmarshal(raw, &response) != nil || !ok || len(response.Output) != 2 || response.Output[0]["type"] != "reasoning" || response.Output[0]["encrypted_content"] != "fixture-opaque" || response.Output[1]["type"] != "message" || response.Usage.Input != 4 {
		t.Fatalf("ordered native data or usage lost: %s", raw)
	}
}
