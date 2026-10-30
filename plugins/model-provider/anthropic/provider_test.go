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
	"strings"
	"testing"

	p "github.com/ayeshLK/komposyn/plugins/model-provider"
)

func TestPaginatedDiscoveryAndMessagesStream(t *testing.T) {
	page := 0
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("Anthropic headers missing")
		}
		switch r.URL.Path {
		case "/v1/models":
			page++
			if page == 1 {
				fmt.Fprint(w, `{"data":[{"id":"m1","display_name":"Model One"}],"has_more":true,"last_id":"m1"}`)
			} else {
				if r.URL.Query().Get("after_id") != "m1" {
					t.Errorf("cursor=%q", r.URL.Query().Get("after_id"))
				}
				fmt.Fprint(w, `{"data":[{"id":"m2","display_name":"Model Two"}],"has_more":false,"last_id":"m2"}`)
			}
		case "/v1/messages":
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, s := range []string{
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"m2\",\"usage\":{\"input_tokens\":4,\"cache_read_input_tokens\":1}}}\n\n",
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			} {
				fmt.Fprint(w, s)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	plugin := Plugin{BaseURL: srv.URL + "/v1", Client: srv.Client()}
	models, err := plugin.ListModels(context.Background(), p.Credential{APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if page != 2 || len(models) != 2 || models[1].Compatibility != p.CompatibilityUnknown {
		t.Fatalf("pages=%d models=%+v", page, models)
	}
	var got string
	res, err := plugin.StreamChat(context.Background(), p.ChatRequest{ModelID: "m2", Credential: p.Credential{APIKey: "secret"}, Messages: []p.Message{{Role: p.RoleUser, Text: "first"}, {Role: p.RoleAssistant, Native: []json.RawMessage{json.RawMessage(`{"type":"text","text":"previous"}`)}}, {Role: p.RoleUser, Text: "next"}}, Instructions: "help", MaxOutput: 64}, func(s string) { got += s })
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != p.OutcomeCompleted || res.RequestID != "msg-1" || got != "hello" || !res.Usage.Known || !res.Usage.InputKnown || !res.Usage.OutputKnown || res.Usage.InputTokens != 5 || res.Usage.OutputTokens != 2 || len(res.Native) != 1 {
		t.Fatalf("result=%+v streamed=%q", res, got)
	}
	if body["max_tokens"] != float64(64) || body["system"] != "help" {
		t.Fatalf("request=%v", body)
	}
	if _, ok := body["tools"]; ok {
		t.Fatal("tools were advertised")
	}
	messages := body["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages=%d", len(messages))
	}
	if !strings.Contains(fmt.Sprint(messages[1]), "previous") {
		t.Fatalf("native history missing: %v", messages)
	}
}

func TestStatusErrorsAreTyped(t *testing.T) {
	for status, want := range map[int]p.Outcome{401: p.OutcomeAuthentication, 403: p.OutcomePermission, 429: p.OutcomeRateLimited, 400: p.OutcomeUnsupported, 500: p.OutcomeService} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":"sensitive body ignored"}`)
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
