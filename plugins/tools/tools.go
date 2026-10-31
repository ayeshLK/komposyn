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

// Package tools defines the role-specific contract for bundled experimental
// chat tools. Registering a tool exposes its definition; it does not authorize
// or execute it. The trusted runtime owns validation, policy, approval,
// enforcement, and dispatch.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

type ID string

var toolIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Definition struct {
	ID          ID
	Description string
	InputSchema json.RawMessage
	MaxInput    int
}

type Invocation struct {
	ConversationID string
	TurnID         string
	ProviderCallID string
	ToolID         ID
	Arguments      json.RawMessage
}

type Outcome string

const (
	OutcomeNotExecuted Outcome = "not_executed"
	OutcomeSucceeded   Outcome = "succeeded"
	OutcomeFailed      Outcome = "failed"
	OutcomePartial     Outcome = "partial"
	OutcomeUnknown     Outcome = "unknown"
	OutcomeCancelled   Outcome = "cancelled"
)

type ContentKind string

const (
	ContentText   ContentKind = "text"
	ContentImage  ContentKind = "image"
	ContentSource ContentKind = "source"
)

// Content is data returned to the selected provider. Credentials and
// authorization grants must never be represented as content.
type Content struct {
	Kind      ContentKind
	Text      string
	MediaType string
	Data      []byte
	Source    string
}

type Result struct {
	ProviderCallID string
	Outcome        Outcome
	Content        []Content
	Truncated      bool
	ErrorCode      string
}

// Tool implementations are trusted bundled adapters. Execute must only be
// called by the runtime dispatcher after centralized authorization and
// enforcement checks. Implementations do not receive grant stores, providers,
// auth stores, or UI capabilities.
type Tool interface {
	Definition() Definition
	ValidateArguments(json.RawMessage) error
	Execute(context.Context, Invocation) Result
}

var (
	ErrDuplicateTool = errors.New("tool is already registered")
	ErrUnknownTool   = errors.New("tool is not registered")
)

type Registry struct {
	mu            sync.RWMutex
	registrations map[ID]registration
}

type registration struct {
	tool       Tool
	definition Definition
}

func NewRegistry() *Registry { return &Registry{registrations: make(map[ID]registration)} }

func (r *Registry) Register(tool Tool) error {
	if r == nil || tool == nil {
		return errors.New("tool registry and implementation are required")
	}
	d := tool.Definition()
	if !toolIDPattern.MatchString(string(d.ID)) {
		return fmt.Errorf("invalid tool ID %q", d.ID)
	}
	if strings.TrimSpace(d.Description) == "" {
		return errors.New("tool description is required")
	}
	if d.MaxInput < 1 {
		return errors.New("tool maximum input size must be positive")
	}
	var schema map[string]json.RawMessage
	if len(d.InputSchema) == 0 || json.Unmarshal(d.InputSchema, &schema) != nil || schema == nil {
		return errors.New("tool input schema must be a JSON object")
	}
	var rootType string
	if raw := schema["type"]; len(raw) == 0 || json.Unmarshal(raw, &rootType) != nil || rootType != "object" {
		return errors.New("tool input schema root type must be object")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registrations == nil {
		r.registrations = make(map[ID]registration)
	}
	if _, exists := r.registrations[d.ID]; exists {
		return ErrDuplicateTool
	}
	d.InputSchema = append(json.RawMessage(nil), d.InputSchema...)
	r.registrations[d.ID] = registration{tool: tool, definition: d}
	return nil
}

func (r *Registry) Lookup(id ID) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.registrations[id]
	return registered.tool, ok
}

// Definitions returns detached copies suitable for provider request encoding.
// The returned list is sorted so provider fixtures and UI projections are
// deterministic.
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Definition, 0, len(r.registrations))
	for _, registered := range r.registrations {
		d := registered.definition
		d.InputSchema = append(json.RawMessage(nil), d.InputSchema...)
		out = append(out, d)
	}
	sortDefinitions(out)
	return out
}

func sortDefinitions(definitions []Definition) {
	for i := 1; i < len(definitions); i++ {
		for j := i; j > 0 && definitions[j].ID < definitions[j-1].ID; j-- {
			definitions[j], definitions[j-1] = definitions[j-1], definitions[j]
		}
	}
}
