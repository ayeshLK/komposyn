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

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeTool struct{ definition Definition }

func (t fakeTool) Definition() Definition { return t.definition }
func (fakeTool) ValidateArguments(arguments json.RawMessage) error {
	var value map[string]json.RawMessage
	if json.Unmarshal(arguments, &value) != nil || value == nil {
		return errors.New("arguments must be an object")
	}
	return nil
}
func (t fakeTool) Execute(_ context.Context, invocation Invocation) Result {
	return Result{ProviderCallID: invocation.ProviderCallID, Outcome: OutcomeSucceeded}
}

func TestRegistryValidatesDefinitionsAndReturnsDetachedSortedSnapshots(t *testing.T) {
	registry := NewRegistry()
	second := fakeTool{definition: Definition{ID: "write_stdin", Description: "Poll an owned process", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 512}}
	first := fakeTool{definition: Definition{ID: "exec_command", Description: "Run a bounded command", InputSchema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`), MaxInput: 2048}}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].ID != "exec_command" || definitions[1].ID != "write_stdin" {
		t.Fatalf("definitions=%+v", definitions)
	}
	definitions[0].InputSchema[0] = 'x'
	if got, ok := registry.Lookup("exec_command"); !ok || string(got.Definition().InputSchema) != `{"type":"object","properties":{"cmd":{"type":"string"}}}` {
		t.Fatal("definition snapshot mutated the registry")
	}
	if string(registry.Definitions()[0].InputSchema) != `{"type":"object","properties":{"cmd":{"type":"string"}}}` {
		t.Fatal("caller mutation changed the stored provider definition")
	}
	if err := registry.Register(first); !errors.Is(err, ErrDuplicateTool) {
		t.Fatalf("duplicate registration error=%v", err)
	}
}

func TestRegistryRejectsInvalidDefinition(t *testing.T) {
	for _, definition := range []Definition{
		{ID: "Exec", Description: "invalid ID", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 20},
		{ID: "missing_description", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 20},
		{ID: "bad_schema", Description: "invalid schema", InputSchema: json.RawMessage(`[]`), MaxInput: 20},
		{ID: "wrong_root", Description: "wrong schema root", InputSchema: json.RawMessage(`{"type":"string"}`), MaxInput: 20},
		{ID: "no_bound", Description: "no bound", InputSchema: json.RawMessage(`{"type":"object"}`)},
	} {
		if err := NewRegistry().Register(fakeTool{definition: definition}); err == nil {
			t.Fatalf("invalid definition accepted: %+v", definition)
		}
	}
}

func TestZeroValueRegistryCanRegister(t *testing.T) {
	var registry Registry
	tool := fakeTool{definition: Definition{ID: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 32}}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	if len(registry.Definitions()) != 1 {
		t.Fatal("zero-value registry did not retain registration")
	}
}

func TestRegisteredExposureDoesNotImplyExecutionOrAuthorization(t *testing.T) {
	tool := fakeTool{definition: Definition{ID: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object"}`), MaxInput: 100}}
	registry := NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lookup("read_file"); !ok {
		t.Fatal("registered tool not exposed")
	}
	// The contract registry intentionally has no executor, grant store, or
	// approval method. The central runtime is the only execution caller.
}

func TestConversationToolSchemasAndBounds(t *testing.T) {
	for _, definition := range []Definition{UpdatePlanDefinition(), RequestUserInputDefinition()} {
		if definition.MaxInput <= 0 || json.Unmarshal(definition.InputSchema, new(map[string]any)) != nil {
			t.Fatalf("invalid bundled definition: %+v", definition)
		}
	}

	plan, err := ParseUpdatePlan([]byte(`{"plan":[{"step":"inspect","status":"completed"},{"step":"implement","status":"in_progress"}]}`))
	if err != nil || len(plan.Plan) != 2 || plan.Plan[1].Status != PlanInProgress {
		t.Fatalf("valid plan parse = %+v, %v", plan, err)
	}
	for _, raw := range []string{
		`{"plan":[{"step":"one","status":"in_progress"},{"step":"two","status":"in_progress"}]}`,
		`{"plan":[{"step":"one","status":"unknown"}]}`,
		`{"plan":[],"extra":true}`,
		`{"plan":[],"plan":[]}`,
		`{"plan":[]} {}`,
		`{"plan":null}`,
	} {
		if parsed, err := ParseUpdatePlan([]byte(raw)); err == nil {
			t.Fatalf("invalid plan accepted: %s as %+v", raw, parsed)
		}
	}
	nested := `{"plan":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + `}`
	if _, err := ParseUpdatePlan([]byte(nested)); err == nil {
		t.Fatal("excessively nested tool input accepted")
	}
	if _, err := ParseUpdatePlan([]byte(`{"plan":[]}` + strings.Repeat(" ", MaxPlanInputBytes))); err == nil {
		t.Fatal("oversized plan accepted")
	}

	clarification, err := ParseRequestUserInput([]byte(`{"question":"Which format?","options":[{"label":"JSON","description":"Machine-readable"}]}`))
	if err != nil || len(clarification.Options) != 1 || clarification.Options[0].Label != "JSON" {
		t.Fatalf("valid clarification parse = %+v, %v", clarification, err)
	}
	if freeText, err := ParseRequestUserInput([]byte(`{"question":"What should I know?","options":[]}`)); err != nil || len(freeText.Options) != 0 {
		t.Fatalf("free-text-only clarification = %+v, %v", freeText, err)
	}
	for _, raw := range []string{
		`{"question":" ","options":[]}`,
		`{"question":"Choose","options":[{"label":"","description":""}]}`,
		`{"question":"Choose","options":[],"default":"first"}`,
		`{"question":"Choose","question":"Again","options":[]}`,
		`{"question":"Choose","options":null}`,
	} {
		if parsed, err := ParseRequestUserInput([]byte(raw)); err == nil {
			t.Fatalf("invalid clarification accepted: %s as %+v", raw, parsed)
		}
	}
}
