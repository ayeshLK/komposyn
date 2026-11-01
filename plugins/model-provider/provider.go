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

// Package modelprovider defines the experimental, provider-neutral model API.
// Implementations are bundled and explicitly registered by the executable.
package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	chattools "github.com/ayeshLK/komposyn/plugins/tools"
)

type ID string

const (
	OpenAI    ID = "openai"
	Anthropic ID = "anthropic"
)

type AuthMethod string

const (
	AuthAPIKey AuthMethod = "api_key"
	AuthOAuth  AuthMethod = "oauth"
)

type Routing struct {
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
}

type Credential struct {
	Method      AuthMethod
	APIKey      string
	AccessToken string
	Route       Routing
}

type AuthResult struct {
	Provider ID
	Account  string
}

type Model struct {
	ID            string
	DisplayName   string
	Compatibility Compatibility
	Reason        string
}

type Compatibility string

const (
	CompatibilitySupported   Compatibility = "supported"
	CompatibilityUnsupported Compatibility = "unsupported"
	CompatibilityUnknown     Compatibility = "unknown"
)

type Message struct {
	Role       Role
	Text       string
	Native     []json.RawMessage
	ToolResult *ToolResult
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ChatRequest struct {
	RequestID    string
	ModelID      string
	Credential   Credential
	Messages     []Message
	Instructions string
	MaxOutput    int64
	Tools        []chattools.Definition
}

// ToolCall is a complete, provider-correlated request. Adapters return calls
// only after successful response completion and bounded JSON argument parsing.
type ToolCall struct {
	CallID    string
	ToolID    chattools.ID
	Arguments json.RawMessage
}

// ToolResult is a correlated provider continuation value. It carries no
// permission or execution authority.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

type CompletionKind string

const (
	CompletionFinal     CompletionKind = "final"
	CompletionToolCalls CompletionKind = "tool_calls"
)

type Support string

const (
	SupportUnknown     Support = "unknown"
	SupportUnsupported Support = "unsupported"
	SupportSupported   Support = "supported"
)

type RouteCapabilities struct {
	ClientTools Support
	Images      Support
}

// RouteCapabilityProvider describes protocol support without receiving a
// credential. Providers that do not implement it default to unknown support.
type RouteCapabilityProvider interface {
	RouteCapabilities(AuthMethod) RouteCapabilities
}

func CapabilitiesFor(p Provider, method AuthMethod) RouteCapabilities {
	if capabilities, ok := p.(RouteCapabilityProvider); ok {
		return capabilities.RouteCapabilities(method)
	}
	return RouteCapabilities{ClientTools: SupportUnknown, Images: SupportUnknown}
}

type Usage struct {
	Known           bool  // Both input and output counts were reported.
	InputKnown      bool  // The provider explicitly reported InputTokens, including zero.
	OutputKnown     bool  // The provider explicitly reported OutputTokens, including zero.
	CacheReadKnown  bool  // The provider explicitly reported CacheRead, including zero.
	CacheWriteKnown bool  // The provider explicitly reported CacheWrite, including zero.
	InputTokens     int64 // Provider-reported input; cache categories remain separate fields.
	OutputTokens    int64
	CacheRead       int64
	CacheWrite      int64
	ReasoningTokens int64
}

type Outcome string

const (
	OutcomeCompleted      Outcome = "completed"
	OutcomeRefused        Outcome = "refused"
	OutcomeTruncated      Outcome = "truncated"
	OutcomeInterrupted    Outcome = "interrupted"
	OutcomeUnsupported    Outcome = "unsupported"
	OutcomeAuthentication Outcome = "authentication_failed"
	OutcomePermission     Outcome = "permission_denied"
	OutcomeRateLimited    Outcome = "rate_limited"
	OutcomeNetwork        Outcome = "network_failed"
	OutcomeService        Outcome = "service_failed"
)

// DiagnosticCode identifies a bounded provider/runtime rejection branch.
// Values contain only fixed structural labels; they must never include source
// text, IDs, credentials, URLs, or arbitrary provider strings.
type DiagnosticCode string

const (
	DiagnosticOpenAIStreamOutputInvalid         DiagnosticCode = "openai.response_completed.invalid_stream_output"
	DiagnosticOpenAICompletedMissingResponse    DiagnosticCode = "openai.response_completed.missing_response"
	DiagnosticOpenAICompletedNoAssistantMessage DiagnosticCode = "openai.response_completed.no_assistant_message"
	DiagnosticOpenAICompletedFunctionCall       DiagnosticCode = "openai.response_completed.function_call"
	DiagnosticOpenAICompletedComputerCall       DiagnosticCode = "openai.response_completed.computer_call"
	DiagnosticOpenAICompletedOtherOutputItem    DiagnosticCode = "openai.response_completed.other_output_item"
	DiagnosticOpenAICompletedInvalidRole        DiagnosticCode = "openai.response_completed.invalid_message_role"
	DiagnosticOpenAICompletedEmptyMessage       DiagnosticCode = "openai.response_completed.empty_message"
	DiagnosticOpenAICompletedOtherContent       DiagnosticCode = "openai.response_completed.other_content"
	DiagnosticOpenAICompletedTextMismatch       DiagnosticCode = "openai.response_completed.stream_final_text_mismatch"
	DiagnosticOpenAICompletedStatusFailed       DiagnosticCode = "openai.response_completed.status_failed"
	DiagnosticOpenAICompletedStatusCancelled    DiagnosticCode = "openai.response_completed.status_cancelled"
	DiagnosticOpenAICompletedStatusInProgress   DiagnosticCode = "openai.response_completed.status_in_progress"
	DiagnosticOpenAICompletedStatusQueued       DiagnosticCode = "openai.response_completed.status_queued"
	DiagnosticOpenAICompletedStatusIncomplete   DiagnosticCode = "openai.response_completed.status_incomplete"
	DiagnosticOpenAICompletedStatusOther        DiagnosticCode = "openai.response_completed.status_other"
	DiagnosticOpenAIIncompleteOtherStopReason   DiagnosticCode = "openai.response_incomplete.other_stop_reason"
	DiagnosticRuntimeMissingNative              DiagnosticCode = "runtime.completed.missing_native_continuation"
)

// SafeDiagnosticCode reports whether a diagnostic belongs to the fixed local
// vocabulary suitable for display or copying into a bug report.
func SafeDiagnosticCode(code DiagnosticCode) bool {
	switch code {
	case DiagnosticOpenAIStreamOutputInvalid, DiagnosticOpenAICompletedMissingResponse, DiagnosticOpenAICompletedNoAssistantMessage,
		DiagnosticOpenAICompletedFunctionCall, DiagnosticOpenAICompletedComputerCall,
		DiagnosticOpenAICompletedOtherOutputItem, DiagnosticOpenAICompletedInvalidRole,
		DiagnosticOpenAICompletedEmptyMessage, DiagnosticOpenAICompletedOtherContent,
		DiagnosticOpenAICompletedTextMismatch,
		DiagnosticOpenAICompletedStatusFailed, DiagnosticOpenAICompletedStatusCancelled,
		DiagnosticOpenAICompletedStatusInProgress, DiagnosticOpenAICompletedStatusQueued,
		DiagnosticOpenAICompletedStatusIncomplete, DiagnosticOpenAICompletedStatusOther,
		DiagnosticOpenAIIncompleteOtherStopReason, DiagnosticRuntimeMissingNative,
		DiagnosticToolProtocolInvalid, DiagnosticToolProtocolUnknown,
		DiagnosticToolProtocolDuplicateCall, DiagnosticToolProtocolRouteUnsupported:
		return true
	default:
		return false
	}
}

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidateToolDefinitions checks the provider-facing boundary independently
// from registry registration.
func ValidateToolDefinitions(definitions []chattools.Definition) error {
	seen := make(map[chattools.ID]struct{}, len(definitions))
	for _, definition := range definitions {
		if !toolNamePattern.MatchString(string(definition.ID)) || strings.TrimSpace(definition.Description) == "" || definition.MaxInput <= 0 {
			return errors.New("invalid provider tool definition")
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return errors.New("duplicate provider tool definition")
		}
		seen[definition.ID] = struct{}{}
		var schema map[string]json.RawMessage
		if len(definition.InputSchema) == 0 || json.Unmarshal(definition.InputSchema, &schema) != nil || schema == nil {
			return errors.New("provider tool schema must be a JSON object")
		}
		var rootType string
		if json.Unmarshal(schema["type"], &rootType) != nil || rootType != "object" {
			return errors.New("provider tool schema root type must be object")
		}
	}
	return nil
}

// ParseToolArguments validates a complete, bounded JSON object. Full JSON
// Schema validation remains the trusted tool adapter's responsibility.
func ParseToolArguments(raw string, definition chattools.Definition) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > definition.MaxInput || !json.Valid([]byte(raw)) {
		return nil, errors.New("invalid or oversized tool arguments")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil || object == nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	return append(json.RawMessage(nil), raw...), nil
}

// ToolCallsDiagnostic rejects malformed, duplicate, unknown, or oversized
// calls without including provider-controlled values in the diagnostic.
func ToolCallsDiagnostic(calls []ToolCall, definitions []chattools.Definition) DiagnosticCode {
	byID := make(map[chattools.ID]chattools.Definition, len(definitions))
	for _, definition := range definitions {
		byID[definition.ID] = definition
	}
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if call.CallID == "" || call.ToolID == "" || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
			return DiagnosticToolProtocolInvalid
		}
		if _, duplicate := seen[call.CallID]; duplicate {
			return DiagnosticToolProtocolDuplicateCall
		}
		seen[call.CallID] = struct{}{}
		definition, ok := byID[call.ToolID]
		if !ok {
			return DiagnosticToolProtocolUnknown
		}
		if _, err := ParseToolArguments(string(call.Arguments), definition); err != nil {
			return DiagnosticToolProtocolInvalid
		}
	}
	return ""
}

type Result struct {
	RequestID      string
	ModelID        string
	Outcome        Outcome
	DiagnosticCode DiagnosticCode
	StopReason     string
	Text           string
	Refusal        string
	Native         []json.RawMessage
	Usage          Usage
	ProviderCode   string
	Completion     CompletionKind
	ToolCalls      []ToolCall
}

const (
	DiagnosticToolProtocolInvalid          DiagnosticCode = "provider.tool_protocol.invalid_call"
	DiagnosticToolProtocolUnknown          DiagnosticCode = "provider.tool_protocol.unknown_tool"
	DiagnosticToolProtocolDuplicateCall    DiagnosticCode = "provider.tool_protocol.duplicate_call_id"
	DiagnosticToolProtocolRouteUnsupported DiagnosticCode = "provider.tool_protocol.route_unsupported"
)

type Error struct {
	Outcome      Outcome
	StatusCode   int
	RequestID    string
	ProviderCode string
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("provider request %s (HTTP %d)", e.Outcome, e.StatusCode)
	}
	return fmt.Sprintf("provider request %s", e.Outcome)
}

func ErrorOutcome(err error) Outcome {
	var target *Error
	if errors.As(err, &target) {
		return target.Outcome
	}
	return OutcomeService
}

type Provider interface {
	ID() ID
	AuthMethods() []AuthMethod
	Authenticate(context.Context, Credential) (AuthResult, error)
	ListModels(context.Context, Credential) ([]Model, error)
	StreamChat(context.Context, ChatRequest, func(string)) (Result, error)
}
