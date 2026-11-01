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

package anthropic

import (
	"encoding/json"
	"errors"

	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	chattools "github.com/ayeshLK/komposyn/plugins/tools"
)

func toolRequestDiagnostic(in provider.ChatRequest) provider.DiagnosticCode {
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
	method := in.Credential.Method
	if method == "" {
		method = provider.AuthAPIKey
	}
	if provider.CapabilitiesFor(Plugin{}, method).ClientTools != provider.SupportSupported {
		return provider.DiagnosticToolProtocolRouteUnsupported
	}
	return ""
}

func toolProtocolUnsupported(code provider.DiagnosticCode) provider.Result {
	return provider.Result{Outcome: provider.OutcomeUnsupported, DiagnosticCode: code}
}

func toolDefinition(definitions []chattools.Definition, name string) (chattools.Definition, bool) {
	for _, definition := range definitions {
		if string(definition.ID) == name {
			return definition, true
		}
	}
	return chattools.Definition{}, false
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
					Type string `json:"type"`
					ID   string `json:"id"`
				}
				if json.Unmarshal(raw, &item) != nil {
					return errors.New("invalid native assistant block")
				}
				if item.Type != "tool_use" {
					continue
				}
				if item.ID == "" {
					return errors.New("tool use is missing its provider ID")
				}
				if _, duplicate := seen[item.ID]; duplicate {
					return errors.New("duplicate tool use ID in native history")
				}
				seen[item.ID] = struct{}{}
				pending[item.ID] = struct{}{}
			}
		case provider.RoleTool:
			if message.ToolResult == nil || message.ToolResult.CallID == "" {
				return errors.New("tool result is missing its provider ID")
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
		return errors.New("native tool uses are missing results")
	}
	return nil
}
