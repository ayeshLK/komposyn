// Package model defines the provider-neutral runtime contract. Provider-native
// content is retained as opaque JSON by the adapter that owns its semantics.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Provider string

const (
	OpenAI    Provider = "openai"
	Anthropic Provider = "anthropic"
)

func (p Provider) Valid() bool { return p == OpenAI || p == Anthropic }

type ModelConfig struct {
	ID            string          `json:"id"`
	Provider      Provider        `json:"provider"`
	ModelID       string          `json:"modelId"`
	Alias         string          `json:"alias"`
	CredentialRef string          `json:"credentialRef"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	Options       json.RawMessage `json:"options,omitempty"`
	ConfigVersion uint64          `json:"configVersion"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

func (m ModelConfig) Validate() error {
	if m.ID == "" || !m.Provider.Valid() || m.ModelID == "" || m.Alias == "" || m.CredentialRef == "" {
		return errors.New("configured model needs stable ID, supported provider, model ID, alias, and credential reference")
	}
	if len(m.Options) > 0 && !json.Valid(m.Options) {
		return errors.New("model options must be valid JSON")
	}
	return nil
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleToolBatch Role = "tool_batch"
)

// ToolResult keeps each provider call identity correlated with the runtime
// operation that produced the result. Tool results are grouped per response so
// Anthropic receives a complete result batch in one user message.
type ToolResult struct {
	ProviderCallID string          `json:"providerCallId"`
	OperationID    string          `json:"operationId"`
	ToolName       string          `json:"toolName"`
	Content        string          `json:"content"`
	IsError        bool            `json:"isError"`
	Native         json.RawMessage `json:"native,omitempty"`
}

type Message struct {
	Role      Role              `json:"role"`
	Text      string            `json:"text,omitempty"`
	Native    []json.RawMessage `json:"native,omitempty"`
	Results   []ToolResult      `json:"results,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	Usage     *Usage            `json:"usage,omitempty"`
}

type Tool struct {
	ProviderName string          `json:"providerName"`
	ServerName   string          `json:"serverName"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
}

type ToolCall struct {
	ProviderCallID string          `json:"providerCallId"`
	ProviderName   string          `json:"providerName"`
	Arguments      json.RawMessage `json:"arguments"`
}

type Usage struct {
	InputTokens        int64 `json:"inputTokens"`
	OutputTokens       int64 `json:"outputTokens"`
	CacheReadTokens    int64 `json:"cacheReadTokens"`
	CacheWriteTokens   int64 `json:"cacheWriteTokens"`
	CacheWrite5mTokens int64 `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64 `json:"cacheWrite1hTokens"`
	ReasoningTokens    int64 `json:"reasoningTokens"`
	Known              bool  `json:"known"`
}

type Outcome string

const (
	OutcomeCompleted    Outcome = "completed"
	OutcomeToolCalls    Outcome = "tool_calls"
	OutcomeRefusal      Outcome = "refusal"
	OutcomeTruncated    Outcome = "truncated"
	OutcomeContextLimit Outcome = "context_limit"
	OutcomeUnsupported  Outcome = "unsupported"
	OutcomeFailed       Outcome = "failed"
	OutcomeCanceled     Outcome = "canceled"
)

type Response struct {
	RequestID     string            `json:"requestId"`
	ResolvedModel string            `json:"resolvedModel"`
	Text          string            `json:"text,omitempty"`
	Refusal       string            `json:"refusal,omitempty"`
	Native        []json.RawMessage `json:"native,omitempty"`
	ToolCalls     []ToolCall        `json:"toolCalls,omitempty"`
	Outcome       Outcome           `json:"outcome"`
	StopReason    string            `json:"stopReason,omitempty"`
	Usage         Usage             `json:"usage"`
	StartedAt     time.Time         `json:"startedAt"`
	CompletedAt   time.Time         `json:"completedAt"`
}

type Request struct {
	Config       ModelConfig `json:"config"`
	Instructions string      `json:"instructions"`
	History      []Message   `json:"history"`
	Tools        []Tool      `json:"tools,omitempty"`
	MaxTokens    int64       `json:"maxTokens"`
}

type Delta struct {
	Text string
}

type Plugin interface {
	Provider() Provider
	Stream(context.Context, Request, func(Delta)) (Response, error)
}

type Registry interface {
	Add(Plugin) error
	Get(Provider) (Plugin, bool)
}
