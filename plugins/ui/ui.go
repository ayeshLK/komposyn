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

// Package ui defines the experimental frontend lifecycle and its small
// headless client. It does not depend on a terminal package or chat runtime.
package ui

import (
	"context"
	"errors"

	"github.com/ayeshLK/komposyn/plugins/model-provider"
)

var (
	ErrConversationReset = errors.New("changing the selected provider or model requires confirmation to start a fresh chat")
	ErrRequestActive     = errors.New("a request is active; cancel or wait before changing models or credentials")
)

type Plugin interface {
	ID() string
	DisplayName() string
	Run(context.Context, Client) error
}
type Profile struct {
	ID, Provider, Label, AuthType string
	Account, Status               string
	Permissions                   []Permission
}
type Permission struct{ ID, Name, State, Description string }
type LoginResult struct {
	ProfileID, Account, Status string
	Permissions                []Permission
	Err                        error
}
type LoginEvent struct {
	Kind, Message string
	Succeeded     bool
}
type LoginAttempt struct {
	ID     string
	Events <-chan LoginEvent
	Done   <-chan LoginResult
}
type DisconnectResult struct{ RemoteRevocationConfirmed, LocalRegistrationRemoved, LocalTokensRemoved bool }
type Model struct {
	Key, Provider, ProfileID, ProfileLabel, ID, DisplayName string
	Compatibility                                           modelprovider.Compatibility
	Reason                                                  string
}
type Failure struct {
	ProfileID, Provider, Label string
	Message                    string
	Outcome                    modelprovider.Outcome
}
type Discovery struct {
	Models   []Model
	Failures []Failure
}
type Turn struct {
	Prompt, Text, Refusal, Message, RequestID, ModelID, ProviderCode string
	DiagnosticCode                                                   modelprovider.DiagnosticCode
	Outcome                                                          modelprovider.Outcome
	Usage                                                            modelprovider.Usage
	ToolActivities                                                   []ToolActivity
}

type ToolActivity struct {
	CallID, ToolID, Phase, Outcome, Message string
	Truncated                               bool
}
type PlanStep struct{ Step, Status string }
type ApprovalChoice string

const (
	ApprovalOnce         ApprovalChoice = "once"
	ApprovalConversation ApprovalChoice = "conversation"
	ApprovalDeny         ApprovalChoice = "deny"
	ApprovalCancel       ApprovalChoice = "cancel"
	ApprovalRedirect     ApprovalChoice = "redirect"
)

type ApprovalRequirement struct {
	Kind, ToolID, Operation, Description, ResourceKind, ResourceScope string
	ConversationAllowed                                               bool
}
type ApprovalRequest struct {
	ID, ToolID, ToolDescription string
	Requirements                []ApprovalRequirement
	AllowConversation           bool
}
type ApprovalEvent struct {
	Request     *ApprovalRequest
	DismissedID string
}
type ApprovalResponse struct {
	ID           string
	Choice       ApprovalChoice
	RedirectText string
}
type ToolGrant struct{ ID, ToolID, Action, ResourceKind, ResourceScope, Operation, Lifetime, Origin string }

type ClarificationChoice string

const (
	ClarificationAnswer  ClarificationChoice = "answer"
	ClarificationDecline ClarificationChoice = "decline"
	ClarificationCancel  ClarificationChoice = "cancel"
)

type SuggestedAnswer struct{ Label, Description string }
type ClarificationRequest struct {
	ID, ConversationID, Question string
	MaxAnswerBytes               int
	Options                      []SuggestedAnswer
}
type ClarificationEvent struct {
	Request     *ClarificationRequest
	DismissedID string
}
type ClarificationResponse struct {
	ID, ConversationID string
	Choice             ClarificationChoice
	Answer             string
}

// MetricUsage retains exact reported counts and marks incomplete coverage.
// A count may be known and partial when some requests omitted that metric.
type MetricUsage struct {
	Tokens  int64
	Known   bool
	Partial bool
}

// ModelUsage groups credentials for the same provider/model within one process.
// It intentionally exposes no credential profile or account metadata.
type ModelUsage struct {
	Provider, ModelID      string
	Input, Output          MetricUsage
	CachedRead, CacheWrite MetricUsage
}
type Snapshot struct {
	Selected       *Model
	Turns          []Turn
	Usage          []ModelUsage
	Active         bool
	ToolActivities []ToolActivity
	Plan           []PlanStep
	Version        string
}

type Client interface {
	Profiles(context.Context) ([]Profile, error)
	AuthMethods(string) []string
	Discover(context.Context) (Discovery, error)
	AddProfile(context.Context, string, string, string, string, map[string]string) error
	ReplaceProfile(context.Context, string, string, string, map[string]string, bool) error
	StartLogin(context.Context, string, string, string) (LoginAttempt, error)
	CancelLogin(string) bool
	Disconnect(context.Context, string, string, string, bool, bool) (DisconnectResult, error)
	OpenUsage(context.Context) error
	RemoveProfile(string, bool) error
	Select(context.Context, string, bool) error
	Snapshot() Snapshot
	Send(context.Context, string, func(string)) (Turn, error)
	Cancel()
	ApprovalEvents() <-chan ApprovalEvent
	RespondApproval(ApprovalResponse) error
	ClarificationEvents() <-chan ClarificationEvent
	RespondClarification(ClarificationResponse) error
	Permissions(context.Context) ([]ToolGrant, error)
	RevokePermission(context.Context, string) error
	ClearPermissions(context.Context) (int, error)
}
