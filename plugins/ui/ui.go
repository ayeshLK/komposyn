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
	Selected *Model
	Turns    []Turn
	Usage    []ModelUsage
	Active   bool
	Version  string
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
}
