// Package anthropic implements the Anthropic Messages API adapter.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
)

type Provider struct {
	Credentials auth.Resolver
	BaseURL     string
	HTTPClient  *http.Client
	Timeout     time.Duration
}

func (Provider) Provider() model.Provider { return model.Anthropic }
func (p Provider) Stream(ctx context.Context, req model.Request, onDelta func(model.Delta)) (model.Response, error) {
	started := time.Now().UTC()
	if req.Config.Provider != model.Anthropic {
		return model.Response{}, errors.New("Anthropic adapter received a different provider configuration")
	}
	if p.Credentials == nil {
		return model.Response{}, errors.New("Anthropic credential resolver is not configured")
	}
	key, e := p.Credentials.Resolve(req.Config.CredentialRef)
	if e != nil {
		return model.Response{}, e
	}
	if req.Config.ModelID == "" {
		return model.Response{}, errors.New("Anthropic model ID is required")
	}
	msgs, e := history(req.History)
	if e != nil {
		return model.Response{}, e
	}
	tools, e := toolParams(req.Tools)
	if e != nil {
		return model.Response{}, e
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	hc := p.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(key), option.WithHTTPClient(hc), option.WithMaxRetries(0)}
	if p.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(p.BaseURL))
	}
	client := sdk.NewClient(opts...)
	max := req.MaxTokens
	if max <= 0 {
		max = 4096
	}
	params := sdk.MessageNewParams{Model: sdk.Model(req.Config.ModelID), MaxTokens: max, Messages: msgs, Tools: tools}
	if req.Instructions != "" {
		params.System = []sdk.TextBlockParam{{Text: req.Instructions}}
	}
	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	result := model.Response{StartedAt: started, Outcome: model.OutcomeUnsupported}
	blocks := map[int64]*block{}
	var stop string
	var usage sdk.MessageDeltaUsage
	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "message_start":
			m := ev.AsMessageStart().Message
			result.RequestID = m.ID
			result.ResolvedModel = string(m.Model)
			result.Usage.CacheReadTokens = m.Usage.CacheReadInputTokens
			result.Usage.CacheWriteTokens = m.Usage.CacheCreationInputTokens
			result.Usage.InputTokens = m.Usage.InputTokens + m.Usage.CacheReadInputTokens + m.Usage.CacheCreationInputTokens
			result.Usage.CacheWrite5mTokens = m.Usage.CacheCreation.Ephemeral5mInputTokens
			result.Usage.CacheWrite1hTokens = m.Usage.CacheCreation.Ephemeral1hInputTokens
			result.Usage.Known = true
		case "content_block_start":
			b := ev.AsContentBlockStart()
			raw := b.ContentBlock.RawJSON()
			var fields struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if !json.Valid([]byte(raw)) {
				return model.Response{}, errors.New("Anthropic returned invalid content block")
			}
			_ = json.Unmarshal([]byte(raw), &fields)
			blocks[b.Index] = &block{raw: json.RawMessage(raw), typ: fields.Type, id: fields.ID, name: fields.Name}
		case "content_block_delta":
			d := ev.AsContentBlockDelta()
			b := blocks[d.Index]
			if b == nil {
				return model.Response{}, errors.New("Anthropic sent delta before content block")
			}
			delta := d.Delta.AsAny()
			switch v := delta.(type) {
			case sdk.TextDelta:
				b.text += v.Text
				result.Text += v.Text
				if onDelta != nil {
					onDelta(model.Delta{Text: v.Text})
				}
			case sdk.InputJSONDelta:
				b.input += v.PartialJSON
			case sdk.ThinkingDelta:
				b.thinking += v.Thinking
			case sdk.SignatureDelta:
				b.signature += v.Signature
			default:
				return model.Response{}, fmt.Errorf("unsupported Anthropic content delta %T", delta)
			}
		case "message_delta":
			md := ev.AsMessageDelta()
			stop = string(md.Delta.StopReason)
			usage = md.Usage
			result.Usage.OutputTokens = usage.OutputTokens
			result.Usage.CacheReadTokens = usage.CacheReadInputTokens
			result.Usage.CacheWriteTokens = usage.CacheCreationInputTokens
			result.Usage.InputTokens = usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
			result.Usage.ReasoningTokens = usage.OutputTokensDetails.ThinkingTokens
			result.Usage.Known = true
		case "message_stop", "content_block_stop", "ping":
		default:
			return model.Response{}, fmt.Errorf("unsupported Anthropic stream event %q", ev.Type)
		}
	}
	if e := stream.Err(); e != nil {
		return model.Response{}, fmt.Errorf("Anthropic request failed (%T)", e)
	}
	indices := make([]int, 0, len(blocks))
	for index := range blocks {
		indices = append(indices, int(index))
	}
	sort.Ints(indices)
	for _, index := range indices {
		b := blocks[int64(index)]
		var base map[string]any
		if len(b.raw) == 0 || json.Unmarshal(b.raw, &base) != nil {
			return model.Response{}, errors.New("Anthropic returned invalid content block")
		}
		switch b.typ {
		case "text":
			base["text"] = b.text
		case "tool_use":
			var obj map[string]any
			if b.input == "" {
				b.input = "{}"
			}
			if json.Unmarshal([]byte(b.input), &obj) != nil || obj == nil {
				return model.Response{}, errors.New("Anthropic returned malformed tool input")
			}
			base["input"] = obj
			arg, _ := json.Marshal(obj)
			result.ToolCalls = append(result.ToolCalls, model.ToolCall{ProviderCallID: b.id, ProviderName: b.name, Arguments: arg})
		case "thinking":
			base["thinking"] = b.thinking
			if b.signature != "" {
				base["signature"] = b.signature
			}
		case "redacted_thinking":
		default:
			return model.Response{}, fmt.Errorf("unsupported Anthropic content block %q", b.typ)
		}
		raw, e := json.Marshal(base)
		if e != nil {
			return model.Response{}, e
		}
		result.Native = append(result.Native, raw)
	}

	result.CompletedAt = time.Now().UTC()
	result.StopReason = stop
	switch stop {
	case "end_turn", "stop_sequence":
		result.Outcome = model.OutcomeCompleted
	case "tool_use":
		result.Outcome = model.OutcomeToolCalls
	case "refusal":
		result.Outcome = model.OutcomeRefusal
	case "max_tokens":
		result.Outcome = model.OutcomeTruncated
	case "model_context_window_exceeded":
		result.Outcome = model.OutcomeContextLimit
	case "":
		return model.Response{}, errors.New("Anthropic stream ended without message_delta")
	default:
		result.Outcome = model.OutcomeUnsupported
	}
	return result, nil
}

type block struct {
	raw                              json.RawMessage
	typ, id, name                    string
	text, input, thinking, signature string
}

func history(in []model.Message) ([]sdk.MessageParam, error) {
	var out []sdk.MessageParam
	for _, m := range in {
		switch m.Role {
		case model.RoleUser:
			out = append(out, sdk.NewUserMessage(sdk.ContentBlockParamUnion{OfText: &sdk.TextBlockParam{Text: m.Text}}))
		case model.RoleAssistant:
			var blocks []sdk.ContentBlockParamUnion
			for _, r := range m.Native {
				if !json.Valid(r) {
					return nil, errors.New("persisted Anthropic history contains malformed content")
				}
				blocks = append(blocks, param.Override[sdk.ContentBlockParamUnion](r))
			}
			out = append(out, sdk.NewAssistantMessage(blocks...))
		case model.RoleToolBatch:
			var blocks []sdk.ContentBlockParamUnion
			for _, r := range m.Results {
				blocks = append(blocks, sdk.NewToolResultBlock(r.ProviderCallID, toolOutput(r), r.IsError))
			}
			out = append(out, sdk.NewUserMessage(blocks...))
		default:
			return nil, fmt.Errorf("unsupported conversation role %q", m.Role)
		}
	}
	return out, nil
}
func toolParams(in []model.Tool) ([]sdk.ToolUnionParam, error) {
	out := make([]sdk.ToolUnionParam, 0, len(in))
	for _, t := range in {
		var schema map[string]any
		if e := json.Unmarshal(t.InputSchema, &schema); e != nil {
			return nil, fmt.Errorf("invalid MCP schema for %s: %w", t.Name, e)
		}
		out = append(out, sdk.ToolUnionParam{OfTool: &sdk.ToolParam{Name: t.ProviderName, Description: sdk.String(t.Description), InputSchema: sdk.ToolInputSchemaParam{ExtraFields: schema}, Type: sdk.ToolTypeCustom}})
	}
	return out, nil
}
func toolOutput(r model.ToolResult) string {
	b, _ := json.Marshal(map[string]any{"content": r.Content, "is_error": r.IsError, "operation_id": r.OperationID})
	return string(b)
}
