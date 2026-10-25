package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
)

func TestStreamFakeHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Errorf("authorization missing")
		}
		var req map[string]any
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
		}
		if req["store"] != false {
			t.Errorf("store=%v", req["store"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		event := `event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"hel"}\n\nevent: response.completed\ndata: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":123,"status":"completed","model":"gpt-test","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5,"input_tokens_details":{"cached_tokens":1,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}\n\n`
		fmt.Fprint(w, strings.ReplaceAll(event, `\n`, "\n"))
		fl.Flush()
	}))
	defer server.Close()
	p := Provider{Credentials: auth.EnvironmentResolver{LookupEnv: func(string) (string, bool) { return "fixture-key", true }}, BaseURL: server.URL + "/v1"}
	var streamed string
	resp, e := p.Stream(context.Background(), model.Request{Config: model.ModelConfig{Provider: model.OpenAI, ModelID: "gpt-test", CredentialRef: "env:OPENAI_TEST"}, History: []model.Message{{Role: model.RoleUser, Text: "hi"}}}, func(d model.Delta) { streamed += d.Text })
	if e != nil {
		t.Fatal(e)
	}
	if resp.Text != "hello" || streamed != "hel" || resp.Outcome != model.OutcomeCompleted || resp.Usage.InputTokens != 3 || resp.Usage.CacheReadTokens != 1 {
		t.Fatalf("response=%+v streamed=%q", resp, streamed)
	}
}
