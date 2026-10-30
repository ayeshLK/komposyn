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

// Package anthropic is the bundled Anthropic Messages API provider plugin.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
)

type Plugin struct {
	BaseURL string
	Client  *http.Client
}

func (Plugin) ID() provider.ID                    { return provider.Anthropic }
func (Plugin) AuthMethods() []provider.AuthMethod { return []provider.AuthMethod{provider.AuthAPIKey} }

func (p Plugin) Authenticate(ctx context.Context, c provider.Credential) (provider.AuthResult, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return provider.AuthResult{}, &provider.Error{Outcome: provider.OutcomeAuthentication}
	}
	_, err := p.ListModels(ctx, c)
	if err != nil {
		return provider.AuthResult{}, err
	}
	return provider.AuthResult{Provider: provider.Anthropic}, nil
}

func (p Plugin) ListModels(ctx context.Context, c provider.Credential) ([]provider.Model, error) {
	out := []provider.Model{}
	after := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		path := "/models?limit=100"
		if after != "" {
			path += "&after_id=" + url.QueryEscape(after)
		}
		req, err := p.request(ctx, http.MethodGet, path, nil, c)
		if err != nil {
			return nil, err
		}
		resp, err := p.client().Do(req)
		if err != nil {
			return nil, classify(err)
		}
		var body struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(resp)
		}
		if decodeErr != nil {
			return nil, &provider.Error{Outcome: provider.OutcomeService, StatusCode: resp.StatusCode, RequestID: resp.Header.Get("request-id")}
		}
		for _, m := range body.Data {
			if m.ID == "" {
				continue
			}
			name := m.Name
			if name == "" {
				name = m.ID
			}
			out = append(out, provider.Model{ID: m.ID, DisplayName: name, Compatibility: provider.CompatibilityUnknown, Reason: "the models endpoint does not report text-generation capabilities"})
		}
		if !body.HasMore {
			break
		}
		next := body.LastID
		if next == "" && len(body.Data) > 0 {
			next = body.Data[len(body.Data)-1].ID
		}
		if next == "" || seen[next] {
			return nil, &provider.Error{Outcome: provider.OutcomeService}
		}
		seen[next] = true
		after = next
		if page == 99 {
			return nil, &provider.Error{Outcome: provider.OutcomeService}
		}
	}
	return out, nil
}

func (p Plugin) StreamChat(ctx context.Context, in provider.ChatRequest, delta func(string)) (provider.Result, error) {
	if in.ModelID == "" || in.MaxOutput <= 0 {
		return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	msgs := make([]map[string]any, 0, len(in.Messages))
	for _, m := range in.Messages {
		switch m.Role {
		case provider.RoleUser:
			msgs = append(msgs, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": m.Text}}})
		case provider.RoleAssistant:
			if len(m.Native) == 0 {
				return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
			}
			blocks := make([]json.RawMessage, 0, len(m.Native))
			for _, raw := range m.Native {
				if !json.Valid(raw) {
					return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
				}
				blocks = append(blocks, raw)
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})
		default:
			return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
		}
	}
	payload := map[string]any{"model": in.ModelID, "max_tokens": in.MaxOutput, "messages": msgs, "stream": true}
	if in.Instructions != "" {
		payload["system"] = in.Instructions
	}
	b, _ := json.Marshal(payload)
	req, err := p.request(ctx, http.MethodPost, "/messages", bytes.NewReader(b), in.Credential)
	if err != nil {
		return provider.Result{}, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return provider.Result{}, classify(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return provider.Result{}, statusError(resp)
	}
	result := provider.Result{ModelID: in.ModelID}
	blocks := map[int]*contentBlock{}
	terminal := ""
	inputUsageKnown := false
	outputUsageKnown := false
	err = readSSE(resp.Body, func(event string, raw []byte) error {
		var v struct {
			Type         string          `json:"type"`
			Index        int             `json:"index"`
			ContentBlock json.RawMessage `json:"content_block"`
			Delta        json.RawMessage `json:"delta"`
			Message      json.RawMessage `json:"message"`
			Usage        json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return &provider.Error{Outcome: provider.OutcomeService}
		}
		if event != "" && v.Type != "" && event != v.Type {
			return &provider.Error{Outcome: provider.OutcomeService}
		}
		switch v.Type {
		case "message_start":
			var m struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					Input      *int64 `json:"input_tokens"`
					CacheRead  int64  `json:"cache_read_input_tokens"`
					CacheWrite int64  `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(v.Message, &m) != nil {
				return &provider.Error{Outcome: provider.OutcomeService}
			}
			result.RequestID = m.ID
			if m.Model != "" {
				result.ModelID = m.Model
			}
			if m.Usage.Input != nil {
				inputUsageKnown = true
				result.Usage.InputKnown = true
				result.Usage.InputTokens = *m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheWrite
				result.Usage.CacheRead = m.Usage.CacheRead
				result.Usage.CacheWrite = m.Usage.CacheWrite
			}
		case "content_block_start":
			var x struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			}
			if json.Unmarshal(v.ContentBlock, &x) != nil {
				return &provider.Error{Outcome: provider.OutcomeService}
			}
			blocks[v.Index] = &contentBlock{kind: x.Type, id: x.ID, name: x.Name, text: x.Text, raw: append(json.RawMessage(nil), v.ContentBlock...)}
		case "content_block_delta":
			b := blocks[v.Index]
			if b == nil {
				return &provider.Error{Outcome: provider.OutcomeService}
			}
			var d struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				Partial   string `json:"partial_json"`
			}
			if json.Unmarshal(v.Delta, &d) != nil {
				return &provider.Error{Outcome: provider.OutcomeService}
			}
			switch d.Type {
			case "text_delta":
				b.text += d.Text
				result.Text += d.Text
				if delta != nil {
					delta(d.Text)
				}
			case "thinking_delta":
				b.thinking += d.Thinking
			case "signature_delta":
				b.signature += d.Signature
			case "input_json_delta":
				b.partial += d.Partial
			default:
				b.unsupported = true
			}
		case "message_delta":
			var d struct {
				Stop string `json:"stop_reason"`
			}
			if json.Unmarshal(v.Delta, &d) != nil {
				return &provider.Error{Outcome: provider.OutcomeService}
			}
			terminal = d.Stop
			var u struct {
				Output     *int64 `json:"output_tokens"`
				CacheRead  int64  `json:"cache_read_input_tokens"`
				CacheWrite int64  `json:"cache_creation_input_tokens"`
			}
			if len(v.Usage) > 0 && json.Unmarshal(v.Usage, &u) == nil {
				if u.Output != nil {
					result.Usage.OutputTokens = *u.Output
					result.Usage.OutputKnown = true
					outputUsageKnown = true
				}
				if u.CacheRead > 0 {
					result.Usage.CacheRead = u.CacheRead
				}
				if u.CacheWrite > 0 {
					result.Usage.CacheWrite = u.CacheWrite
				}
			}
			result.Usage.Known = inputUsageKnown && outputUsageKnown
		case "message_stop", "content_block_stop", "ping":
		default:
			return &provider.Error{Outcome: provider.OutcomeUnsupported}
		}
		return nil
	})
	if err != nil {
		var typed *provider.Error
		if errors.As(err, &typed) {
			return result, err
		}
		return result, classify(err)
	}
	if terminal == "" {
		return result, &provider.Error{Outcome: provider.OutcomeInterrupted, RequestID: result.RequestID}
	}
	indices := make([]int, 0, len(blocks))
	for i := range blocks {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	for _, i := range indices {
		b := blocks[i]
		var obj map[string]any
		if json.Unmarshal(b.raw, &obj) != nil {
			return provider.Result{}, &provider.Error{Outcome: provider.OutcomeService}
		}
		switch b.kind {
		case "text":
			obj["text"] = b.text
		case "thinking":
			obj["thinking"] = b.thinking
			if b.signature != "" {
				obj["signature"] = b.signature
			}
		case "redacted_thinking":
		case "tool_use":
			b.unsupported = true
		default:
			b.unsupported = true
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			return provider.Result{}, &provider.Error{Outcome: provider.OutcomeService}
		}
		result.Native = append(result.Native, raw)
		if b.unsupported {
			result.Outcome = provider.OutcomeUnsupported
		}
	}
	result.StopReason = terminal
	switch terminal {
	case "end_turn", "stop_sequence":
		if result.Outcome != "unsupported" {
			result.Outcome = provider.OutcomeCompleted
		}
	case "refusal":
		result.Outcome = provider.OutcomeRefused
	case "max_tokens":
		result.Outcome = provider.OutcomeTruncated
	case "model_context_window_exceeded":
		result.Outcome = provider.OutcomeUnsupported
	case "tool_use", "pause_turn":
		result.Outcome = provider.OutcomeUnsupported
	default:
		return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	return result, nil
}

type contentBlock struct {
	kind, id, name, text, thinking, signature, partial string
	raw                                                json.RawMessage
	unsupported                                        bool
}

func (p Plugin) request(ctx context.Context, method, path string, body io.Reader, c provider.Credential) (*http.Request, error) {
	u, err := url.Parse(strings.TrimRight(p.baseURL(), "/") + path)
	if err != nil {
		return nil, &provider.Error{Outcome: provider.OutcomeService}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, &provider.Error{Outcome: provider.OutcomeService}
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
func (p Plugin) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return "https://api.anthropic.com/v1"
}
func (p Plugin) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}
func statusError(r *http.Response) error {
	kind := provider.OutcomeService
	switch r.StatusCode {
	case 401:
		kind = provider.OutcomeAuthentication
	case 403:
		kind = provider.OutcomePermission
	case 429:
		kind = provider.OutcomeRateLimited
	case 400, 404, 422:
		kind = provider.OutcomeUnsupported
	}
	return &provider.Error{Outcome: kind, StatusCode: r.StatusCode, RequestID: r.Header.Get("request-id")}
}
func classify(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &provider.Error{Outcome: provider.OutcomeInterrupted}
	}
	return &provider.Error{Outcome: provider.OutcomeNetwork}
}
func readSSE(r io.Reader, fn func(string, []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 2<<20)
	event := ""
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, []byte(strings.Join(data, "\n")))
		event = ""
		data = nil
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("stream read: %w", err)
	}
	return flush()
}
