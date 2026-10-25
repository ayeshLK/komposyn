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
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	p "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/model-provider/anthropic"
	"github.com/ayeshLK/komposyn/plugins/model-provider/openai"
)

func TestOfflineProviderWalkthroughBothProviders(t *testing.T) {
	for _, id := range []p.ID{p.OpenAI, p.Anthropic} {
		t.Run(string(id), func(t *testing.T) { runOfflineWalkthrough(t, id) })
	}
}

func runOfflineWalkthrough(t *testing.T, id p.ID) {
	var chats atomic.Int32
	var captureMu sync.Mutex
	partial := make(chan struct{}, 1)
	var lastRequest any
	capture := func(value any) { captureMu.Lock(); lastRequest = value; captureMu.Unlock() }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && r.Header.Get("x-api-key") == "" {
			t.Error("credential header missing")
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"chat-model","display_name":"Chat Model"}],"has_more":false,"last_id":"chat-model"}`)
		case "/v1/responses":
			var req struct {
				Input []json.RawMessage `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			capture(req.Input)
			n := chats.Add(1)
			if n == 3 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 2 {
				fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
				w.(http.Flusher).Flush()
				partial <- struct{}{}
				<-r.Context().Done()
				return
			}
			text := "answer"
			if n == 4 {
				text = "recovered"
			}
			writeOpenAICompletion(w, "resp-"+fmt.Sprint(n), text)
		case "/v1/messages":
			var req struct {
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			capture(req.Messages)
			n := chats.Add(1)
			if n == 3 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 2 {
				writeAnthropicPartial(w)
				partial <- struct{}{}
				<-r.Context().Done()
				return
			}
			text := "answer"
			if n == 4 {
				text = "recovered"
			}
			writeAnthropicCompletion(w, "msg-"+fmt.Sprint(n), text)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	profilePath := filepath.Join(t.TempDir(), ".komposyn", "auth.json")
	profileStore := auth.Store{Path: profilePath}
	var plugin p.Provider
	if id == p.OpenAI {
		plugin = openai.Plugin{BaseURL: srv.URL + "/v1", Client: srv.Client()}
	} else {
		plugin = anthropic.Plugin{BaseURL: srv.URL + "/v1", Client: srv.Client()}
	}
	registry, err := model.NewRegistry(plugin)
	if err != nil {
		t.Fatal(err)
	}
	settingsStore := settings.Store{Path: filepath.Join(filepath.Dir(profilePath), "settings.json")}
	if err := settingsStore.Save(settings.Settings{Plugin: "tview"}); err != nil {
		t.Fatal(err)
	}
	first := NewChat(registry, profileStore)
	if err := first.AddProfile(context.Background(), "walkthrough", string(id), "Offline walkthrough", "dummy-api-key", nil); err != nil {
		t.Fatal(err)
	}
	discovery := first.Discover(context.Background())
	if len(discovery.Models) != 1 || discovery.Models[0].Compatibility != p.CompatibilityUnknown {
		t.Fatalf("discovery=%+v", discovery)
	}
	// Relaunch: configuration survives but chat selection/history do not.
	second := NewChat(registry, profileStore)
	if s := second.Snapshot(); s.Selected != nil || len(s.Turns) != 0 {
		t.Fatalf("relaunch restored chat state: %+v", s)
	}
	prefs, err := settingsStore.Load()
	if err != nil || prefs.Plugin != "tview" {
		t.Fatalf("settings did not survive: %+v %v", prefs, err)
	}
	if err := second.Select(context.Background(), discovery.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	if turn, err := second.Send(context.Background(), "first", nil); err != nil || turn.Outcome != p.OutcomeCompleted {
		t.Fatalf("first message: %+v %v", turn, err)
	}
	partialText := make(chan string, 1)
	done := make(chan Turn, 1)
	go func() {
		turn, _ := second.Send(context.Background(), "cancel this", func(s string) { partialText <- s })
		done <- turn
	}()
	select {
	case <-partial:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not begin canceled response")
	}
	if got := <-partialText; got != "partial" {
		t.Fatalf("partial=%q", got)
	}
	second.Cancel()
	canceled := <-done
	if canceled.Outcome != p.OutcomeInterrupted || canceled.Text != "partial" {
		t.Fatalf("canceled turn=%+v", canceled)
	}
	failed, err := second.Send(context.Background(), "auth fail", nil)
	if err == nil || failed.Outcome != p.OutcomeAuthentication {
		t.Fatalf("auth recovery turn=%+v err=%v", failed, err)
	}
	recovered, err := second.Send(context.Background(), "last", nil)
	if err != nil || recovered.Text != "recovered" || recovered.Outcome != p.OutcomeCompleted {
		t.Fatalf("recovered turn=%+v err=%v", recovered, err)
	}
	if chats.Load() != 4 {
		t.Fatalf("chat request count=%d", chats.Load())
	}
	captureMu.Lock()
	capturedRequest := lastRequest
	captureMu.Unlock()
	switch req := capturedRequest.(type) {
	case []json.RawMessage:
		if len(req) != 3 {
			t.Fatalf("OpenAI follow-up history included canceled/failed turns: %#v", req)
		}
		var final map[string]any
		_ = json.Unmarshal(req[2], &final)
		if final["role"] != "user" {
			t.Fatalf("last OpenAI item=%v", final)
		}
	case []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}:
		if len(req) != 3 || req[0].Role != "user" || req[1].Role != "assistant" || req[2].Role != "user" {
			t.Fatalf("Anthropic follow-up history included canceled/failed turns: %#v", req)
		}
	default:
		t.Fatalf("unexpected request capture type %T", capturedRequest)
	}
	if after := second.Snapshot(); after.Selected == nil || len(after.Turns) != 4 || after.Active {
		t.Fatalf("final state=%+v", after)
	}
}

func writeOpenAICompletion(w http.ResponseWriter, id, text string) {
	fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", text)
	fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"model\":\"chat-model\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"m\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n", id, text)
}
func writeAnthropicCompletion(w http.ResponseWriter, id, text string) {
	for _, event := range []string{fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"model\":\"chat-model\",\"usage\":{\"input_tokens\":2}}}\n\n", id), "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text), "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"} {
		fmt.Fprint(w, event)
	}
}
func writeAnthropicPartial(w http.ResponseWriter) {
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-cancel\",\"model\":\"chat-model\",\"usage\":{\"input_tokens\":2}}}\n\n")
	fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
	w.(http.Flusher).Flush()
}
