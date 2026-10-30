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
	Role   Role
	Text   string
	Native []json.RawMessage
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type ChatRequest struct {
	RequestID    string
	ModelID      string
	Credential   Credential
	Messages     []Message
	Instructions string
	MaxOutput    int64
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
	case DiagnosticOpenAICompletedMissingResponse, DiagnosticOpenAICompletedNoAssistantMessage,
		DiagnosticOpenAICompletedFunctionCall, DiagnosticOpenAICompletedComputerCall,
		DiagnosticOpenAICompletedOtherOutputItem, DiagnosticOpenAICompletedInvalidRole,
		DiagnosticOpenAICompletedEmptyMessage, DiagnosticOpenAICompletedOtherContent,
		DiagnosticOpenAICompletedTextMismatch,
		DiagnosticOpenAICompletedStatusFailed, DiagnosticOpenAICompletedStatusCancelled,
		DiagnosticOpenAICompletedStatusInProgress, DiagnosticOpenAICompletedStatusQueued,
		DiagnosticOpenAICompletedStatusIncomplete, DiagnosticOpenAICompletedStatusOther,
		DiagnosticOpenAIIncompleteOtherStopReason, DiagnosticRuntimeMissingNative:
		return true
	default:
		return false
	}
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
}

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
