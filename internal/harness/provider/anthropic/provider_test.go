package anthropic

import (
	"context"
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
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "fixture-key" {
			t.Errorf("key header missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		events := []string{
			`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":0},"service_tier":"standard"}}}\n\n`,
			`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n`,
			`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}\n\n`,
			`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n`,
			`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens_details":{"thinking_tokens":1},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0}}}\n\n`,
			`event: message_stop\ndata: {"type":"message_stop"}\n\n`,
		}
		for _, e := range events {
			fmt.Fprint(w, strings.ReplaceAll(e, `\n`, "\n"))
			fl.Flush()
		}
	}))
	defer server.Close()
	p := Provider{Credentials: auth.EnvironmentResolver{LookupEnv: func(string) (string, bool) { return "fixture-key", true }}, BaseURL: server.URL}
	resp, e := p.Stream(context.Background(), model.Request{Config: model.ModelConfig{Provider: model.Anthropic, ModelID: "claude-test", CredentialRef: "env:ANTHROPIC_TEST"}, History: []model.Message{{Role: model.RoleUser, Text: "hi"}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if resp.Text != "hello" || resp.Outcome != model.OutcomeCompleted || resp.Usage.InputTokens != 6 || resp.Usage.CacheReadTokens != 1 || resp.Usage.CacheWriteTokens != 2 || resp.Usage.ReasoningTokens != 1 {
		t.Fatalf("response=%+v", resp)
	}
}
