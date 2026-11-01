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

package openai

import (
	"encoding/json"
	"errors"

	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
)

func toolRequestDiagnostic(in provider.ChatRequest, method provider.AuthMethod) provider.DiagnosticCode {
	hasResults := false
	for _, message := range in.Messages {
		if message.Role == provider.RoleTool {
			hasResults = true
		}
	}
	if len(in.Tools) == 0 && !hasResults {
		return ""
	}
	if len(in.Tools) == 0 || provider.ValidateToolDefinitions(in.Tools) != nil {
		return provider.DiagnosticToolProtocolInvalid
	}
	if provider.CapabilitiesFor(Plugin{}, method).ClientTools != provider.SupportSupported {
		return provider.DiagnosticToolProtocolRouteUnsupported
	}
	return ""
}

func toolProtocolUnsupported(code provider.DiagnosticCode) provider.Result {
	return provider.Result{Outcome: provider.OutcomeUnsupported, DiagnosticCode: code}
}

func validateToolHistory(messages []provider.Message) error {
	pending := make(map[string]struct{})
	seen := make(map[string]struct{})
	for _, message := range messages {
		switch message.Role {
		case provider.RoleUser:
			if len(pending) != 0 {
				return errors.New("tool results are missing before the next user message")
			}
		case provider.RoleAssistant:
			for _, raw := range message.Native {
				var item struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
				}
				if json.Unmarshal(raw, &item) != nil {
					return errors.New("invalid native assistant item")
				}
				if item.Type != "function_call" {
					continue
				}
				if item.CallID == "" {
					return errors.New("function call is missing its provider ID")
				}
				if _, duplicate := seen[item.CallID]; duplicate {
					return errors.New("duplicate function call ID in native history")
				}
				seen[item.CallID] = struct{}{}
				pending[item.CallID] = struct{}{}
			}
		case provider.RoleTool:
			if message.ToolResult == nil || message.ToolResult.CallID == "" {
				return errors.New("tool result is missing its call ID")
			}
			if _, ok := pending[message.ToolResult.CallID]; !ok {
				return errors.New("tool result does not match a pending call")
			}
			delete(pending, message.ToolResult.CallID)
		default:
			return errors.New("invalid message role")
		}
	}
	if len(pending) != 0 {
		return errors.New("native function calls are missing results")
	}
	return nil
}

type streamedArguments struct {
	itemID    string
	fragments string
	sawDelta  bool
	done      *string
}

type toolArgumentStream struct {
	byIndex map[int]*streamedArguments
	invalid bool
}

func newToolArgumentStream() *toolArgumentStream {
	return &toolArgumentStream{byIndex: make(map[int]*streamedArguments)}
}

func (s *toolArgumentStream) observeItem(kind string, index *int, raw json.RawMessage) {
	if index == nil || *index < 0 {
		s.invalid = true
		return
	}
	var item struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &item) != nil {
		s.invalid = true
		return
	}
	if item.Type != "function_call" {
		return
	}
	entry := s.byIndex[*index]
	if entry == nil {
		entry = &streamedArguments{}
		s.byIndex[*index] = entry
	}
	if entry.itemID != "" && item.ID != "" && entry.itemID != item.ID {
		s.invalid = true
	}
	if item.ID != "" {
		entry.itemID = item.ID
	}
	_ = kind // Both added and done events contribute identity only.
}

func (s *toolArgumentStream) observeArguments(kind string, index *int, itemID string, value string) {
	if index == nil || *index < 0 {
		s.invalid = true
		return
	}
	entry := s.byIndex[*index]
	if entry == nil || entry.itemID == "" || itemID == "" || entry.itemID != itemID {
		s.invalid = true
		return
	}
	switch kind {
	case "response.function_call_arguments.delta":
		entry.fragments += value
		entry.sawDelta = true
	case "response.function_call_arguments.done":
		if entry.done != nil && *entry.done != value {
			s.invalid = true
			return
		}
		copy := value
		entry.done = &copy
	default:
		s.invalid = true
	}
}

func (s *toolArgumentStream) validate(finalItems []indexedToolCall) bool {
	if s.invalid {
		return false
	}
	finalByIndex := make(map[int]indexedToolCall, len(finalItems))
	for _, item := range finalItems {
		finalByIndex[item.index] = item
	}
	for index, stream := range s.byIndex {
		final, ok := finalByIndex[index]
		if !ok || stream.itemID != "" && final.itemID != "" && stream.itemID != final.itemID {
			return false
		}
		if stream.sawDelta && stream.fragments != final.arguments {
			return false
		}
		if stream.done != nil && *stream.done != final.arguments {
			return false
		}
	}
	return true
}
