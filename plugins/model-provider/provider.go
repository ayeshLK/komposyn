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
	Known           bool
	InputTokens     int64
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

type Result struct {
	RequestID    string
	ModelID      string
	Outcome      Outcome
	StopReason   string
	Text         string
	Refusal      string
	Native       []json.RawMessage
	Usage        Usage
	ProviderCode string
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
