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

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

const MaxClarificationAnswerBytes = 4 << 10

type ClarificationOutcome string

const (
	ClarificationAnswered  ClarificationOutcome = "answered"
	ClarificationDeclined  ClarificationOutcome = "declined"
	ClarificationCancelled ClarificationOutcome = "cancelled"
)

type ClarificationRequest struct {
	ID             string
	ConversationID string
	Question       string
	Options        []tools.SuggestedAnswer
	MaxAnswerBytes int
}

type ClarificationResponse struct {
	ID             string
	ConversationID string
	Outcome        ClarificationOutcome
	Answer         string
}

// ToolClarificationInteraction is a detached, runtime-owned user interaction.
// Implementations transport explicit answers and never authorize permissions.
type ToolClarificationInteraction interface {
	Request(context.Context, ClarificationRequest) (ClarificationResponse, error)
}

type ToolClarificationFunc func(context.Context, ClarificationRequest) (ClarificationResponse, error)

func (f ToolClarificationFunc) Request(ctx context.Context, request ClarificationRequest) (ClarificationResponse, error) {
	return f(ctx, request)
}

type planUpdater interface {
	ReplacePlan(context.Context, string, []tools.PlanStep) error
}

type conversationPlanUpdater struct{ plans *ConversationPlans }

func (u conversationPlanUpdater) ReplacePlan(ctx context.Context, conversationID string, steps []tools.PlanStep) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return u.plans.Replace(conversationID, steps)
}

type updatePlanTool struct{ plans planUpdater }

func (t *updatePlanTool) Definition() tools.Definition { return tools.UpdatePlanDefinition() }
func (*updatePlanTool) ValidateArguments(raw json.RawMessage) error {
	_, err := tools.ParseUpdatePlan(raw)
	return err
}
func (t *updatePlanTool) Execute(ctx context.Context, invocation tools.Invocation) tools.Result {
	input, err := tools.ParseUpdatePlan(invocation.Arguments)
	if err != nil {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeNotExecuted, ErrorCode: "invalid_plan"}
	}
	if t.plans == nil {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "plan_unavailable"}
	}
	if err := t.plans.ReplacePlan(ctx, invocation.ConversationID, input.Plan); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeCancelled, ErrorCode: "cancelled"}
		}
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "plan_update_failed"}
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "Current conversation plan replaced with %d step(s):", len(input.Plan))
	for _, step := range input.Plan {
		fmt.Fprintf(&summary, "\n- [%s] %s", step.Status, step.Step)
	}
	return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeSucceeded, Content: []tools.Content{{Kind: tools.ContentText, Text: summary.String()}}}
}

type requestUserInputTool struct{ interaction ToolClarificationInteraction }

func (t *requestUserInputTool) Definition() tools.Definition {
	return tools.RequestUserInputDefinition()
}
func (*requestUserInputTool) ValidateArguments(raw json.RawMessage) error {
	_, err := tools.ParseRequestUserInput(raw)
	return err
}
func (t *requestUserInputTool) Execute(ctx context.Context, invocation tools.Invocation) tools.Result {
	input, err := tools.ParseRequestUserInput(invocation.Arguments)
	if err != nil {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeNotExecuted, ErrorCode: "invalid_clarification"}
	}
	if t.interaction == nil {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeNotExecuted, ErrorCode: "clarification_unavailable"}
	}
	requestID, err := newConversationID()
	if err != nil {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "clarification_identity_unavailable"}
	}
	request := ClarificationRequest{ID: requestID, ConversationID: invocation.ConversationID, Question: input.Question, Options: append([]tools.SuggestedAnswer(nil), input.Options...), MaxAnswerBytes: MaxClarificationAnswerBytes}
	response, err := t.interaction.Request(ctx, request)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeCancelled, ErrorCode: "cancelled"}
	}
	if err != nil || response.ID != request.ID || response.ConversationID != invocation.ConversationID {
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "clarification_response_invalid"}
	}
	switch response.Outcome {
	case ClarificationAnswered:
		if len(response.Answer) > MaxClarificationAnswerBytes || !utf8.ValidString(response.Answer) {
			return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "clarification_answer_invalid"}
		}
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeSucceeded, Content: []tools.Content{{Kind: tools.ContentText, Text: response.Answer}}}
	case ClarificationDeclined:
		if response.Answer != "" {
			return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "clarification_response_invalid"}
		}
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeDeclined, ErrorCode: "user_declined"}
	case ClarificationCancelled:
		if response.Answer != "" {
			return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "clarification_response_invalid"}
		}
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeCancelled, ErrorCode: "cancelled"}
	default:
		return tools.Result{ProviderCallID: invocation.ProviderCallID, Outcome: tools.OutcomeFailed, ErrorCode: "clarification_response_invalid"}
	}
}

// BundledConversationToolRuntimeConfig constructs the only production tool
// executor in this milestone. It registers exactly two in-memory tools and
// leaves tools unavailable when approval or clarification UI is absent.
func BundledConversationToolRuntimeConfig(approval ToolApprovalInteraction, clarification ToolClarificationInteraction) ToolRuntimeConfig {
	if approval == nil || clarification == nil {
		return ToolRuntimeConfig{}
	}
	plans := NewConversationPlans()
	registry := tools.NewRegistry()
	planTool := &updatePlanTool{plans: conversationPlanUpdater{plans: plans}}
	inputTool := &requestUserInputTool{interaction: clarification}
	if registry.Register(planTool) != nil || registry.Register(inputTool) != nil {
		return ToolRuntimeConfig{}
	}
	return ToolRuntimeConfig{
		Registry:      registry,
		Plans:         plans,
		Approval:      approval,
		Clarification: clarification,
		Authorizer: ToolAuthorizerFunc(func(_ context.Context, invocation tools.Invocation) (permissions.Request, error) {
			if invocation.ConversationID == "" || invocation.ToolID != tools.UpdatePlanID && invocation.ToolID != tools.RequestUserInputID {
				return permissions.Request{}, errors.New("conversation tool authorization identity is invalid")
			}
			description := "Update the in-memory plan for this conversation"
			if invocation.ToolID == tools.RequestUserInputID {
				description = "Ask the user for input and return the answer to the selected model"
			}
			action := permissions.Requirement{Kind: permissions.RequirementAction, Key: "action", ToolID: string(invocation.ToolID), Description: description, Operation: permissions.OperationInteract, ActionKey: conversationToolActionKey(invocation.ToolID), SessionAllowed: true}
			resource := permissions.ResourceRequest{Kind: permissions.ResourceConversation, Target: invocation.ConversationID, Operation: permissions.OperationInteract}
			conversation := permissions.Requirement{Kind: permissions.RequirementResource, Key: "conversation", ToolID: string(invocation.ToolID), Description: "Access current conversation-local state", Operation: permissions.OperationInteract, Resource: &resource, SessionAllowed: true}
			return permissions.Request{ConversationID: invocation.ConversationID, CallID: invocation.ProviderCallID, Requirements: []permissions.Requirement{action, conversation}}, nil
		}),
		Enforcer: bundledConversationEnforcer{},
	}
}

func conversationToolActionKey(id tools.ID) string {
	digest := sha256.Sum256([]byte("komposyn-conversation-tool-v1\x00" + string(id)))
	return hex.EncodeToString(digest[:])
}

type bundledConversationEnforcer struct{}

func (bundledConversationEnforcer) Execute(ctx context.Context, implementation tools.Tool, invocation tools.Invocation, request permissions.Request) (tools.Result, error) {
	if request.ConversationID != invocation.ConversationID || request.CallID != invocation.ProviderCallID || len(request.Requirements) != 2 {
		return tools.Result{}, errors.New("bundled conversation tool authorization mismatch")
	}
	expectedActionKey := conversationToolActionKey(invocation.ToolID)
	var actionOK, resourceOK bool
	for _, requirement := range request.Requirements {
		if requirement.ToolID != string(invocation.ToolID) || requirement.Operation != permissions.OperationInteract {
			return tools.Result{}, errors.New("bundled conversation tool requirement mismatch")
		}
		switch requirement.Kind {
		case permissions.RequirementAction:
			actionOK = requirement.ActionKey == expectedActionKey
		case permissions.RequirementResource:
			resourceOK = requirement.Resource != nil && requirement.Resource.Kind == permissions.ResourceConversation && requirement.Resource.Target == invocation.ConversationID && requirement.Resource.Operation == permissions.OperationInteract
		default:
			return tools.Result{}, errors.New("bundled conversation tool requirement mismatch")
		}
	}
	if !actionOK || !resourceOK {
		return tools.Result{}, errors.New("bundled conversation tool scope mismatch")
	}
	switch tool := implementation.(type) {
	case *updatePlanTool:
		if invocation.ToolID != tools.UpdatePlanID {
			return tools.Result{}, errors.New("tool implementation identity mismatch")
		}
		return tool.Execute(ctx, invocation), nil
	case *requestUserInputTool:
		if invocation.ToolID != tools.RequestUserInputID {
			return tools.Result{}, errors.New("tool implementation identity mismatch")
		}
		return tool.Execute(ctx, invocation), nil
	default:
		return tools.Result{}, errors.New("tool implementation is not in the bundled conversation set")
	}
}
