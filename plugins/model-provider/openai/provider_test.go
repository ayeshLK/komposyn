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
	if res.Outcome != p.OutcomeCompleted || res.RequestID != "resp-1" || res.ModelID != "text-model" || res.Text != "hello" || streamed != "hello" || !res.Usage.Known || res.Usage.InputTokens != 3 || res.Usage.ReasoningTokens != 2 {
		t.Fatalf("result=%+v streamed=%q", res, streamed)
	}
	if chatBody["store"] != false || chatBody["stream"] != true || chatBody["instructions"] != "be concise" || chatBody["max_output_tokens"] != float64(128) {
		t.Fatalf("request options=%v", chatBody)
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
