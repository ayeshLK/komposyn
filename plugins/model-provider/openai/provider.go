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

// Package openai is the bundled OpenAI Responses API provider plugin.
package openai

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
	"strings"
	"time"

	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	chattools "github.com/ayeshLK/komposyn/plugins/tools"
)

type Plugin struct {
	BaseURL string
	Client  *http.Client
}

func (Plugin) ID() provider.ID { return provider.OpenAI }
func (Plugin) AuthMethods() []provider.AuthMethod {
	return []provider.AuthMethod{provider.AuthAPIKey, provider.AuthOAuth}
}

func (Plugin) RouteCapabilities(method provider.AuthMethod) provider.RouteCapabilities {
	switch method {
	case provider.AuthAPIKey:
		return provider.RouteCapabilities{ClientTools: provider.SupportSupported, Images: provider.SupportUnsupported}
	case provider.AuthOAuth:
		return provider.RouteCapabilities{ClientTools: provider.SupportUnknown, Images: provider.SupportUnknown}
	default:
		return provider.RouteCapabilities{ClientTools: provider.SupportUnsupported, Images: provider.SupportUnsupported}
	}
}

func (p Plugin) Authenticate(ctx context.Context, c provider.Credential) (provider.AuthResult, error) {
	method := credentialMethod(c)
	if (method == provider.AuthAPIKey && strings.TrimSpace(c.APIKey) == "") ||
		(method == provider.AuthOAuth && strings.TrimSpace(c.AccessToken) == "") ||
		(method != provider.AuthAPIKey && method != provider.AuthOAuth) {
		return provider.AuthResult{}, &provider.Error{Outcome: provider.OutcomeAuthentication}
	}
	_, err := p.list(ctx, c)
	if err != nil {
		return provider.AuthResult{}, err
	}
	return provider.AuthResult{Provider: provider.OpenAI}, nil
}

func (p Plugin) ListModels(ctx context.Context, c provider.Credential) ([]provider.Model, error) {
	return p.list(ctx, c)
}

func (p Plugin) list(ctx context.Context, c provider.Credential) ([]provider.Model, error) {
	method := credentialMethod(c)
	if method != provider.AuthAPIKey && method != provider.AuthOAuth {
		return nil, &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	req, err := p.newRequest(ctx, http.MethodGet, "/models", nil, c)
	if err != nil {
		return nil, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, classify(err, 0, "")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(resp, method)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if method == provider.AuthOAuth {
		var body struct {
			Models []struct {
				Slug        string `json:"slug"`
				DisplayName string `json:"display_name"`
				Visibility  string `json:"visibility"`
			} `json:"models"`
		}
		if err := decoder.Decode(&body); err != nil {
			return nil, &provider.Error{Outcome: provider.OutcomeService, StatusCode: resp.StatusCode, RequestID: resp.Header.Get("x-request-id")}
		}
		out := make([]provider.Model, 0, len(body.Models))
		for _, m := range body.Models {
			if m.Visibility != "list" || m.Slug == "" {
				continue
			}
			out = append(out, provider.Model{ID: m.Slug, DisplayName: m.DisplayName, Compatibility: provider.CompatibilityUnknown, Reason: "the model catalog does not report text-generation capabilities"})
		}
		return out, nil
	}
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := decoder.Decode(&body); err != nil {
		return nil, &provider.Error{Outcome: provider.OutcomeService, StatusCode: resp.StatusCode, RequestID: resp.Header.Get("x-request-id")}
	}
	out := make([]provider.Model, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, provider.Model{ID: m.ID, DisplayName: m.ID, Compatibility: provider.CompatibilityUnknown, Reason: "the models endpoint does not report text-generation capabilities"})
	}
	return out, nil
}

func (p Plugin) StreamChat(ctx context.Context, in provider.ChatRequest, delta func(string)) (provider.Result, error) {
	method := credentialMethod(in.Credential)
	if in.ModelID == "" || (method == provider.AuthAPIKey && in.MaxOutput <= 0) || (method != provider.AuthAPIKey && method != provider.AuthOAuth) {
		return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	if diagnostic := toolRequestDiagnostic(in, method); diagnostic != "" {
		return toolProtocolUnsupported(diagnostic), nil
	}
	input := make([]json.RawMessage, 0, len(in.Messages))
	if err := validateToolHistory(in.Messages); err != nil {
		return toolProtocolUnsupported(provider.DiagnosticToolProtocolInvalid), nil
	}
	for _, m := range in.Messages {
		switch m.Role {
		case provider.RoleUser:
			b, _ := json.Marshal(map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": m.Text}}})
			input = append(input, b)
		case provider.RoleAssistant:
			if len(m.Native) == 0 {
				return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
			}
			for _, raw := range m.Native {
				if !json.Valid(raw) {
					return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
				}
				input = append(input, append(json.RawMessage(nil), raw...))
			}
		case provider.RoleTool:
			if m.ToolResult == nil || m.ToolResult.CallID == "" {
				return toolProtocolUnsupported(provider.DiagnosticToolProtocolInvalid), nil
			}
			b, err := json.Marshal(map[string]any{"type": "function_call_output", "call_id": m.ToolResult.CallID, "output": m.ToolResult.Content})
			if err != nil {
				return toolProtocolUnsupported(provider.DiagnosticToolProtocolInvalid), nil
			}
			input = append(input, b)
		default:
			return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
		}
	}
	payload := map[string]any{"model": in.ModelID, "input": input, "stream": true, "store": false}
	if len(in.Tools) > 0 {
		definitions := make([]map[string]any, 0, len(in.Tools))
		for _, definition := range in.Tools {
			definitions = append(definitions, map[string]any{
				"type": "function", "name": string(definition.ID), "description": definition.Description,
				"parameters": definition.InputSchema,
			})
		}
		payload["tools"] = definitions
	}
	if method == provider.AuthAPIKey {
		payload["max_output_tokens"] = in.MaxOutput
		payload["include"] = []string{"reasoning.encrypted_content"}
	}
	if in.Instructions != "" {
		payload["instructions"] = in.Instructions
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return provider.Result{}, &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	req, err := p.newRequest(ctx, http.MethodPost, "/responses", bytes.NewReader(b), in.Credential)
	if err != nil {
		return provider.Result{}, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return provider.Result{}, classify(err, 0, "")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return provider.Result{}, statusError(resp, method)
	}
	result := provider.Result{RequestID: resp.Header.Get("x-request-id"), ModelID: in.ModelID}
	var terminal string
	var finalResponse *resultResponse
	streamOutput := completedOutput{items: make(map[int]json.RawMessage), added: make(map[int]bool)}
	argumentStream := newToolArgumentStream()
	err = readSSE(resp.Body, func(event string, raw []byte) error {
		var v struct {
			Type        string          `json:"type"`
			OutputIndex *int            `json:"output_index"`
			Item        json.RawMessage `json:"item"`
			ItemID      string          `json:"item_id"`
			Arguments   string          `json:"arguments"`
			Delta       string          `json:"delta"`
			Response    json.RawMessage `json:"response"`
			Message     string          `json:"message"`
			Error       json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return &provider.Error{Outcome: provider.OutcomeService}
		}
		if event != "" && v.Type != "" && event != v.Type {
			return &provider.Error{Outcome: provider.OutcomeService}
		}
		switch v.Type {
		case "response.output_item.added", "response.output_item.done":
			streamOutput.observe(v.Type, v.OutputIndex, v.Item)
			argumentStream.observeItem(v.Type, v.OutputIndex, v.Item)
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			value := v.Delta
			if v.Type == "response.function_call_arguments.done" {
				value = v.Arguments
			}
			argumentStream.observeArguments(v.Type, v.OutputIndex, v.ItemID, value)
		case "response.output_text.delta":
			result.Text += v.Delta
			if delta != nil {
				delta(v.Delta)
			}
		case "response.completed", "response.incomplete", "response.failed":
			terminal = v.Type
			if len(v.Response) > 0 {
				response := v.Response
				if terminal == "response.completed" {
					var valid bool
					response, valid = streamOutput.fillEmptyOutput(response)
					if !valid {
						result.Outcome = provider.OutcomeUnsupported
						result.DiagnosticCode = provider.DiagnosticOpenAIStreamOutputInvalid
						response = v.Response // Retain reported metadata/usage even on rejection.
					}
				}
				finalResponse = &resultResponse{Target: &result}
				if err := json.Unmarshal(response, finalResponse); err != nil {
					return &provider.Error{Outcome: provider.OutcomeService}
				}
			}
		case "error":
			var apiErr struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(v.Error, &apiErr)
			result.ProviderCode = apiErr.Code
			return &provider.Error{Outcome: outcomeForCode(apiErr.Code, provider.OutcomeService), RequestID: result.RequestID, ProviderCode: apiErr.Code}
		}
		return nil
	})
	if err != nil {
		var typed *provider.Error
		if errors.As(err, &typed) {
			return result, err
		}
		return result, classify(err, 0, result.RequestID)
	}
	if terminal == "" {
		return result, &provider.Error{Outcome: provider.OutcomeInterrupted, RequestID: result.RequestID}
	}
	if result.DiagnosticCode == provider.DiagnosticOpenAIStreamOutputInvalid {
		result.ToolCalls = nil
		result.Completion = ""
		return result, nil
	}
	if terminal == "response.completed" && finalResponse != nil && finalResponse.FinalText != result.Text {
		if strings.HasPrefix(finalResponse.FinalText, result.Text) {
			missing := strings.TrimPrefix(finalResponse.FinalText, result.Text)
			result.Text = finalResponse.FinalText
			if missing != "" && delta != nil {
				delta(missing)
			}
		} else {
			result.Outcome = provider.OutcomeUnsupported
			result.DiagnosticCode = provider.DiagnosticOpenAICompletedTextMismatch
		}
	}
	if terminal == "response.completed" && finalResponse == nil {
		result.Outcome = provider.OutcomeUnsupported
		result.DiagnosticCode = provider.DiagnosticOpenAICompletedMissingResponse
		return result, nil
	}
	if terminal == "response.completed" && result.Outcome == provider.OutcomeUnsupported {
		result.ToolCalls = nil
		result.Completion = ""
		return result, nil
	}
	if terminal == "response.completed" && !finalResponse.HasReplayableMessage && len(result.ToolCalls) == 0 {
		result.Outcome = provider.OutcomeUnsupported
		result.DiagnosticCode = provider.DiagnosticOpenAICompletedNoAssistantMessage
		return result, nil
	}
	switch terminal {
	case "response.completed":
		if result.Outcome != provider.OutcomeUnsupported && result.Outcome != provider.OutcomeRefused {
			result.Outcome = provider.OutcomeCompleted
			if len(result.ToolCalls) > 0 {
				if code := provider.ToolCallsDiagnostic(result.ToolCalls, in.Tools); code != "" {
					result.Outcome = provider.OutcomeUnsupported
					result.DiagnosticCode = code
					result.ToolCalls = nil
					return result, nil
				}
				if !argumentStream.validate(finalResponse.ToolItems) {
					result.Outcome = provider.OutcomeUnsupported
					result.DiagnosticCode = provider.DiagnosticToolProtocolInvalid
					result.ToolCalls = nil
					return result, nil
				}
				result.Completion = provider.CompletionToolCalls
			} else {
				result.Completion = provider.CompletionFinal
			}
		}
	case "response.incomplete":
		result.ToolCalls = nil
		result.Completion = ""
		if result.StopReason == "max_output_tokens" {
			result.Outcome = provider.OutcomeTruncated
		} else {
			result.Outcome = provider.OutcomeUnsupported
			result.DiagnosticCode = provider.DiagnosticOpenAIIncompleteOtherStopReason
		}
	case "response.failed":
		result.ToolCalls = nil
		result.Completion = ""
		result.Outcome = outcomeForCode(result.ProviderCode, provider.OutcomeService)
	}
	// The terminal event is authoritative when a compatible Responses route
	// omits response.status. An explicit conflicting status remains invalid.
	if result.Outcome == provider.OutcomeCompleted && result.StopReason != "" && result.StopReason != "completed" {
		result.Outcome = provider.OutcomeUnsupported
		result.DiagnosticCode = completedStatusDiagnostic(result.StopReason)
	}
	return result, nil
}

// Completed item events can carry the replay data even when the terminal
// response omits its output. Use only complete, contiguous, consistent items;
// never synthesize continuation from visible text or in-progress item events.
type completedOutput struct {
	items   map[int]json.RawMessage
	added   map[int]bool
	invalid bool
}

func (o *completedOutput) observe(kind string, index *int, item json.RawMessage) {
	if index == nil || *index < 0 {
		o.invalid = true
		return
	}
	if kind == "response.output_item.added" {
		o.added[*index] = true
		return
	}
	var shape struct{ Type, Status string }
	if json.Unmarshal(item, &shape) != nil || (shape.Type == "message" && shape.Status != "completed") || (shape.Type == "reasoning" && shape.Status != "" && shape.Status != "completed") {
		o.invalid = true
		return
	}
	if old, exists := o.items[*index]; exists && !bytes.Equal(old, item) {
		o.invalid = true
		return
	}
	o.items[*index] = append(json.RawMessage(nil), item...)
}

func (o *completedOutput) fillEmptyOutput(response json.RawMessage) (json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(response, &object) != nil || object == nil {
		return response, true
	}
	var output []json.RawMessage
	if raw, exists := object["output"]; exists && json.Unmarshal(raw, &output) != nil {
		return response, true
	}
	if len(output) > 0 {
		return response, true
	} // Explicit final content keeps its normal validation.
	if o.invalid {
		return response, false
	}
	if len(o.items) == 0 {
		return response, true
	}
	for index := range o.added {
		if _, ok := o.items[index]; !ok {
			return response, false
		}
	}
	for index := 0; index < len(o.items); index++ {
		item, ok := o.items[index]
		if !ok {
			return response, false
		}
		output = append(output, item)
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return response, false
	}
	object["output"] = raw
	merged, err := json.Marshal(object)
	return merged, err == nil
}

// resultResponse decodes only stable fields needed by the chat boundary.
type resultResponse struct {
	Target               *provider.Result
	FinalText            string
	HasReplayableMessage bool
	ToolItems            []indexedToolCall
}

type indexedToolCall struct {
	index     int
	itemID    string
	arguments string
}

func (r *resultResponse) UnmarshalJSON(b []byte) error {
	var v struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			Input        *int64 `json:"input_tokens"`
			Output       *int64 `json:"output_tokens"`
			InputDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				Reasoning int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	t := r.Target
	t.RequestID = v.ID
	if v.Model != "" {
		t.ModelID = v.Model
	}
	t.StopReason = v.Status
	if v.Error != nil {
		t.ProviderCode = v.Error.Code
	}
	if v.Incomplete != nil {
		t.StopReason = v.Incomplete.Reason
	}
	if v.Usage != nil {
		t.Usage.InputKnown = v.Usage.Input != nil
		t.Usage.OutputKnown = v.Usage.Output != nil
		if v.Usage.Input != nil {
			t.Usage.InputTokens = *v.Usage.Input
		}
		if v.Usage.Output != nil {
			t.Usage.OutputTokens = *v.Usage.Output
		}
		t.Usage.Known = t.Usage.InputKnown && t.Usage.OutputKnown
		if v.Usage.InputDetails.Cached != nil {
			t.Usage.CacheReadKnown = true
			t.Usage.CacheRead = *v.Usage.InputDetails.Cached
		}
		t.Usage.ReasoningTokens = v.Usage.OutputDetails.Reasoning
	}
	for index, raw := range v.Output {
		var item struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Status    string `json:"status"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		if item.Type == "function_call" {
			if item.Status != "" && item.Status != "completed" {
				t.Outcome = provider.OutcomeUnsupported
				t.DiagnosticCode = provider.DiagnosticToolProtocolInvalid
				continue
			}
			var arguments map[string]json.RawMessage
			if json.Unmarshal([]byte(item.Arguments), &arguments) != nil || arguments == nil {
				t.Outcome = provider.OutcomeUnsupported
				t.DiagnosticCode = provider.DiagnosticToolProtocolInvalid
				continue
			}
			t.ToolCalls = append(t.ToolCalls, provider.ToolCall{CallID: item.CallID, ToolID: chattools.ID(item.Name), Arguments: json.RawMessage(item.Arguments)})
			r.ToolItems = append(r.ToolItems, indexedToolCall{index: index, itemID: item.ID, arguments: item.Arguments})
			t.Native = append(t.Native, append(json.RawMessage(nil), raw...))
			continue
		}
		if item.Type == "computer_call" {
			t.Outcome = provider.OutcomeUnsupported
			t.DiagnosticCode = provider.DiagnosticOpenAICompletedComputerCall
			continue
		}
		if item.Type != "message" && item.Type != "reasoning" {
			t.Outcome = provider.OutcomeUnsupported
			t.DiagnosticCode = provider.DiagnosticOpenAICompletedOtherOutputItem
			continue
		}
		if item.Type == "message" {
			if item.Role != "assistant" {
				t.Outcome = provider.OutcomeUnsupported
				t.DiagnosticCode = provider.DiagnosticOpenAICompletedInvalidRole
				continue
			}
			if len(item.Content) == 0 {
				t.Outcome = provider.OutcomeUnsupported
				t.DiagnosticCode = provider.DiagnosticOpenAICompletedEmptyMessage
				continue
			}
			r.HasReplayableMessage = true
		}
		for _, c := range item.Content {
			if c.Type == "refusal" {
				t.Outcome = provider.OutcomeRefused
				t.Refusal += c.Text
			} else if c.Type == "output_text" {
				r.FinalText += c.Text
			} else {
				t.Outcome = provider.OutcomeUnsupported
				t.DiagnosticCode = provider.DiagnosticOpenAICompletedOtherContent
			}
		}
		t.Native = append(t.Native, append(json.RawMessage(nil), raw...))
	}
	return nil
}

func completedStatusDiagnostic(status string) provider.DiagnosticCode {
	switch status {
	case "failed":
		return provider.DiagnosticOpenAICompletedStatusFailed
	case "cancelled":
		return provider.DiagnosticOpenAICompletedStatusCancelled
	case "in_progress":
		return provider.DiagnosticOpenAICompletedStatusInProgress
	case "queued":
		return provider.DiagnosticOpenAICompletedStatusQueued
	case "incomplete":
		return provider.DiagnosticOpenAICompletedStatusIncomplete
	default:
		return provider.DiagnosticOpenAICompletedStatusOther
	}
}

func (p Plugin) newRequest(ctx context.Context, method, path string, body io.Reader, c provider.Credential) (*http.Request, error) {
	authMethod := credentialMethod(c)
	if authMethod != provider.AuthAPIKey && authMethod != provider.AuthOAuth {
		return nil, &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	u, err := url.Parse(strings.TrimRight(p.baseURL(), "/") + path)
	if err != nil {
		return nil, &provider.Error{Outcome: provider.OutcomeService}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, &provider.Error{Outcome: provider.OutcomeService}
	}
	if authMethod == provider.AuthOAuth {
		if strings.TrimSpace(c.AccessToken) == "" {
			return nil, &provider.Error{Outcome: provider.OutcomeAuthentication}
		}
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authMethod == provider.AuthAPIKey && c.Route.Organization != "" {
		req.Header.Set("OpenAI-Organization", c.Route.Organization)
	}
	if authMethod == provider.AuthAPIKey && c.Route.Project != "" {
		req.Header.Set("OpenAI-Project", c.Route.Project)
	}
	return req, nil
}

func credentialMethod(c provider.Credential) provider.AuthMethod {
	if c.Method == "" {
		return provider.AuthAPIKey
	}
	return c.Method
}
func (p Plugin) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return "https://api.openai.com/v1"
}
func (p Plugin) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}
func statusError(r *http.Response, method provider.AuthMethod) error {
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
	apiCode := ""
	if method == provider.AuthOAuth {
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&payload)
		apiCode = payload.Error.Code
		kind = outcomeForCode(apiCode, kind)
	}
	return &provider.Error{Outcome: kind, StatusCode: r.StatusCode, RequestID: r.Header.Get("x-request-id"), ProviderCode: apiCode}
}

func outcomeForCode(code string, fallback provider.Outcome) provider.Outcome {
	switch code {
	case "subscription_sharing_user_not_eligible", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		return provider.OutcomePermission
	case "subscription_sharing_usage_limit_exceeded":
		return provider.OutcomeRateLimited
	case "subscription_sharing_usage_unavailable":
		return provider.OutcomeService
	case "subscription_sharing_unsupported_capability":
		return provider.OutcomeUnsupported
	case "subscription_sharing_invalid_user":
		return provider.OutcomeAuthentication
	default:
		return fallback
	}
}
func classify(err error, status int, id string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &provider.Error{Outcome: provider.OutcomeInterrupted, StatusCode: status, RequestID: id}
	}
	return &provider.Error{Outcome: provider.OutcomeNetwork, StatusCode: status, RequestID: id}
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
