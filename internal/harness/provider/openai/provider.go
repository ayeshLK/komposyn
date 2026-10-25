// Package openai implements the OpenAI Responses API adapter.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
)

type Provider struct {
	Credentials auth.Resolver
	BaseURL     string
	HTTPClient  *http.Client
	Timeout     time.Duration
}

func (Provider) Provider() model.Provider { return model.OpenAI }

func (p Provider) Stream(ctx context.Context, req model.Request, onDelta func(model.Delta)) (model.Response, error) {
	started := time.Now().UTC()
	if req.Config.Provider != model.OpenAI {
		return model.Response{}, errors.New("OpenAI adapter received a different provider configuration")
	}
	if p.Credentials == nil {
		return model.Response{}, errors.New("OpenAI credential resolver is not configured")
	}
	key, err := p.Credentials.Resolve(req.Config.CredentialRef)
	if err != nil {
		return model.Response{}, err
	}
	if req.Config.ModelID == "" {
		return model.Response{}, errors.New("OpenAI model ID is required")
	}
	input, err := history(req.History)
	if err != nil {
		return model.Response{}, err
	}
	tools, err := toolParams(req.Tools)
	if err != nil {
		return model.Response{}, err
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	httpClient := p.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	service := responses.NewResponseService(
		option.WithAPIKey(key),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	params := responses.ResponseNewParams{
		Model: req.Config.ModelID,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Tools: tools,
		Store: openai.Bool(false),
	}
	if req.Instructions != "" {
		params.Instructions = openai.String(req.Instructions)
	}
	if req.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(req.MaxTokens)
	}
	// This include is required to replay reasoning items in a stateless request.
	params.Include = []responses.ResponseIncludable{"reasoning.encrypted_content"}

	stream := service.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	var final *responses.Response
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" && onDelta != nil {
				onDelta(model.Delta{Text: event.Delta})
			}
		case "response.completed":
			copy := event.Response
			final = &copy
		case "response.incomplete":
			copy := event.Response
			final = &copy
		case "response.failed", "error":
			if event.Message == "" {
				return model.Response{}, errors.New("OpenAI response failed")
			}
			return model.Response{}, errors.New("OpenAI response failed")
		}
	}
	if err := stream.Err(); err != nil {
		return model.Response{}, safeProviderError("OpenAI request", err)
	}
	if final == nil {
		return model.Response{}, errors.New("OpenAI stream ended without a terminal response")
	}

	result := model.Response{
		RequestID: final.ID, ResolvedModel: string(final.Model), StartedAt: started,
		CompletedAt: time.Now().UTC(), StopReason: string(final.Status), Outcome: model.OutcomeUnsupported,
	}
	if final.Usage.JSON.InputTokens.Valid() || final.Usage.JSON.OutputTokens.Valid() {
		result.Usage = model.Usage{
			Known: true, InputTokens: final.Usage.InputTokens, OutputTokens: final.Usage.OutputTokens,
			CacheReadTokens:  final.Usage.InputTokensDetails.CachedTokens,
			CacheWriteTokens: final.Usage.InputTokensDetails.CacheWriteTokens,
			ReasoningTokens:  final.Usage.OutputTokensDetails.ReasoningTokens,
		}
	}
	for _, item := range final.Output {
		raw := json.RawMessage(item.RawJSON())
		if !json.Valid(raw) {
			return model.Response{}, errors.New("OpenAI returned an invalid output item")
		}
		result.Native = append(result.Native, append(json.RawMessage(nil), raw...))
		switch item.Type {
		case "message":
			message := item.AsMessage()
			for _, content := range message.Content {
				switch content.Type {
				case "output_text":
					result.Text += content.AsOutputText().Text
				case "refusal":
					result.Refusal += content.AsRefusal().Refusal
				default:
					return model.Response{}, fmt.Errorf("unsupported OpenAI response content type %q", content.Type)
				}
			}
		case "reasoning": // Preserve encrypted reasoning and its signatures in Native for replay.
		case "function_call":
			call := item.AsFunctionCall()
			args := []byte(call.Arguments)
			if len(args) == 0 || !json.Valid(args) || string(args) == "null" {
				return model.Response{}, errors.New("OpenAI returned malformed tool arguments")
			}
			result.ToolCalls = append(result.ToolCalls, model.ToolCall{ProviderCallID: call.CallID, ProviderName: call.Name, Arguments: append(json.RawMessage(nil), args...)})
		default:
			return model.Response{}, fmt.Errorf("unsupported OpenAI output item type %q", item.Type)
		}
	}
	if result.Refusal != "" {
		result.Outcome = model.OutcomeRefusal
	} else if final.Status == responses.ResponseStatusIncomplete {
		reason := final.IncompleteDetails.Reason
		result.StopReason = reason
		if reason == "max_output_tokens" {
			result.Outcome = model.OutcomeTruncated
		} else if reason == "model_context_window_exceeded" {
			result.Outcome = model.OutcomeContextLimit
		} else {
			result.Outcome = model.OutcomeUnsupported
		}
	} else if final.Status != responses.ResponseStatusCompleted {
		result.Outcome = model.OutcomeFailed
	} else if len(result.ToolCalls) > 0 {
		result.Outcome = model.OutcomeToolCalls
	} else {
		result.Outcome = model.OutcomeCompleted
	}
	return result, nil
}

func history(messages []model.Message) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam
	for _, message := range messages {
		switch message.Role {
		case model.RoleUser:
			items = append(items, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(message.Text)},
			}})
		case model.RoleAssistant:
			for _, raw := range message.Native {
				if !json.Valid(raw) {
					return nil, errors.New("persisted OpenAI history contains malformed provider content")
				}
				items = append(items, param.Override[responses.ResponseInputItemUnionParam](json.RawMessage(raw)))
			}
		case model.RoleToolBatch:
			for _, result := range message.Results {
				items = append(items, responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(result.ProviderCallID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: openai.String(toolOutput(result))},
				}})
			}
		default:
			return nil, fmt.Errorf("unsupported conversation role %q", message.Role)
		}
	}
	return items, nil
}

func toolParams(tools []model.Tool) ([]responses.ToolUnionParam, error) {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, fmt.Errorf("invalid MCP schema for %s: %w", tool.Name, err)
		}
		out = append(out, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
			Name: tool.ProviderName, Description: openai.String(tool.Description), Parameters: schema,
		}})
	}
	return out, nil
}

func toolOutput(result model.ToolResult) string {
	b, _ := json.Marshal(map[string]any{"content": result.Content, "is_error": result.IsError, "operation_id": result.OperationID})
	return string(b)
}

func safeProviderError(label string, err error) error {
	// SDK errors can contain request metadata. Avoid adding credential/header or
	// request-body details to persisted user-visible diagnostics.
	return fmt.Errorf("%s failed (%T)", label, err)
}
