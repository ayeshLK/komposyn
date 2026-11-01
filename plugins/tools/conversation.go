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
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	UpdatePlanID       ID = "update_plan"
	RequestUserInputID ID = "request_user_input"

	MaxPlanInputBytes       = 16 << 10
	MaxPlanSteps            = 32
	MaxPlanStepRunes        = 160
	MaxPlanTextBytes        = 8 << 10
	MaxUserInputBytes       = 4 << 10
	MaxUserInputQuestion    = 500
	MaxSuggestedAnswers     = 3
	MaxSuggestedAnswerLabel = 80
	MaxSuggestedAnswerHelp  = 160
	MaxUserInputTextBytes   = 2 << 10
)

// PlanStatus is a status in the current conversation's replaceable plan.
type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanInProgress PlanStatus = "in_progress"
	PlanCompleted  PlanStatus = "completed"
)

// PlanStep is one bounded, in-memory plan item.
type PlanStep struct {
	Step   string     `json:"step"`
	Status PlanStatus `json:"status"`
}

// UpdatePlanInput replaces the complete plan. A valid update replaces all
// prior steps atomically; omitted steps are removed.
type UpdatePlanInput struct {
	Plan []PlanStep `json:"plan"`
}

// SuggestedAnswer is an optional suggestion shown alongside free text entry.
type SuggestedAnswer struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// RequestUserInputInput describes one concise clarification interaction.
// Options are suggestions only; an empty list still permits a free-text answer.
type RequestUserInputInput struct {
	Question string            `json:"question"`
	Options  []SuggestedAnswer `json:"options"`
}

var updatePlanSchema = json.RawMessage(`{"type":"object","properties":{"plan":{"type":"array","maxItems":32,"items":{"type":"object","properties":{"step":{"type":"string","maxLength":160},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["step","status"],"additionalProperties":false}}},"required":["plan"],"additionalProperties":false}`)

var requestUserInputSchema = json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","maxLength":500},"options":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":80},"description":{"type":"string","maxLength":160}},"required":["label","description"],"additionalProperties":false}}},"required":["question","options"],"additionalProperties":false}`)

// UpdatePlanDefinition is a detached provider-facing strict JSON schema.
func UpdatePlanDefinition() Definition {
	return Definition{ID: UpdatePlanID, Description: "Atomically replace the complete in-memory plan for this conversation. Use pending, in_progress, or completed statuses; an empty plan clears it. This creates no permissions or persistent task.", InputSchema: append(json.RawMessage(nil), updatePlanSchema...), MaxInput: MaxPlanInputBytes}
}

// RequestUserInputDefinition is a detached provider-facing strict JSON schema.
func RequestUserInputDefinition() Definition {
	return Definition{ID: RequestUserInputID, Description: "Ask the user one concise clarification. Suggested answers are optional; the user must explicitly choose one, submit free text, or decline. Submitted answers are returned to the selected model; cancellation stops the turn.", InputSchema: append(json.RawMessage(nil), requestUserInputSchema...), MaxInput: MaxUserInputBytes}
}

// ParseUpdatePlan validates a complete bounded replacement before any caller
// mutates conversation state.
func ParseUpdatePlan(raw []byte) (UpdatePlanInput, error) {
	var input UpdatePlanInput
	if err := decodeStrictObject(raw, MaxPlanInputBytes, &input); err != nil {
		return UpdatePlanInput{}, err
	}
	if err := ValidatePlanSteps(input.Plan); err != nil {
		return UpdatePlanInput{}, err
	}
	input.Plan = append(make([]PlanStep, 0, len(input.Plan)), input.Plan...)
	return input, nil
}

// ValidatePlanSteps validates the semantic bounds shared by provider input and
// the in-memory conversation plan store. It does not apply the provider wire
// encoding limit to already-decoded values.
func ValidatePlanSteps(plan []PlanStep) error {
	if plan == nil || len(plan) > MaxPlanSteps {
		return errors.New("plan must contain at most 32 steps")
	}
	inProgress := 0
	textBytes := 0
	for _, step := range plan {
		if !validBoundedText(step.Step, MaxPlanStepRunes) || strings.TrimSpace(step.Step) == "" {
			return errors.New("plan step text is invalid")
		}
		textBytes += len(step.Step)
		switch step.Status {
		case PlanPending, PlanCompleted:
		case PlanInProgress:
			inProgress++
		default:
			return errors.New("plan step status is invalid")
		}
	}
	if inProgress > 1 || textBytes > MaxPlanTextBytes {
		return errors.New("plan exceeds its status or text bound")
	}
	return nil
}

// ParseRequestUserInput validates a bounded clarification request. Suggested
// answers never become an answer unless the user explicitly chooses one.
func ParseRequestUserInput(raw []byte) (RequestUserInputInput, error) {
	var input RequestUserInputInput
	if err := decodeStrictObject(raw, MaxUserInputBytes, &input); err != nil {
		return RequestUserInputInput{}, err
	}
	if !validBoundedText(input.Question, MaxUserInputQuestion) || strings.TrimSpace(input.Question) == "" || input.Options == nil || len(input.Options) > MaxSuggestedAnswers {
		return RequestUserInputInput{}, errors.New("clarification question or suggestions are invalid")
	}
	textBytes := len(input.Question)
	for _, option := range input.Options {
		if !validBoundedText(option.Label, MaxSuggestedAnswerLabel) || strings.TrimSpace(option.Label) == "" || !validBoundedText(option.Description, MaxSuggestedAnswerHelp) {
			return RequestUserInputInput{}, errors.New("clarification suggestion is invalid")
		}
		textBytes += len(option.Label) + len(option.Description)
	}
	if textBytes > MaxUserInputTextBytes {
		return RequestUserInputInput{}, errors.New("clarification text exceeds its bound")
	}
	input.Options = append([]SuggestedAnswer(nil), input.Options...)
	return input, nil
}

func validBoundedText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && len([]rune(value)) <= maxRunes
}

func decodeStrictObject(raw []byte, maxBytes int, target any) error {
	if len(raw) == 0 || len(raw) > maxBytes || !utf8.Valid(raw) {
		return errors.New("tool input is empty, invalid, or oversized")
	}
	duplicateCheck := json.NewDecoder(bytes.NewReader(raw))
	if err := checkJSONValue(duplicateCheck, 0); err != nil {
		return errors.New("tool input is not a strict JSON value")
	}
	if _, err := duplicateCheck.Token(); err != io.EOF {
		return errors.New("tool input has trailing data")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("tool input does not match its schema")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("tool input has trailing data")
	}
	return nil
}

func checkJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds its bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := checkJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("object is malformed")
		}
	case '[':
		for decoder.More() {
			if err := checkJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("array is malformed")
		}
	default:
		return errors.New("JSON delimiter is invalid")
	}
	return nil
}
