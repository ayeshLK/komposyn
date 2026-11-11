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

package tview

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	harnessruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"github.com/gdamore/tcell/v2"
)

func TestConversationToolApprovalUsesGenericLabels(t *testing.T) {
	provider := &conversationApprovalProvider{}
	providers, err := model.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	if err := store.AddAPIKey("fixture-profile", "openai", "Fixture", "synthetic-key", nil); err != nil {
		t.Fatal(err)
	}
	approval := harnessui.NewApprovalBridge()
	clarification := harnessruntime.ToolClarificationFunc(func(context.Context, harnessruntime.ClarificationRequest) (harnessruntime.ClarificationResponse, error) {
		return harnessruntime.ClarificationResponse{}, errors.New("unexpected clarification")
	})
	chat := harnessruntime.NewChatWithToolRuntime(providers, store, nil, harnessruntime.BundledConversationToolRuntimeConfig(approval, clarification))
	discovery := chat.Discover(context.Background())
	if len(discovery.Models) != 1 {
		t.Fatalf("models=%+v", discovery.Models)
	}
	if err := chat.Select(context.Background(), discovery.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chat.Close)
	client := harnessui.Client{Chat: chat, ProfileStore: store, Approval: approval}
	simulation := tcell.NewSimulationScreen("UTF-8")
	simulation.SetSize(120, 32)
	screen := &synchronizedSimulation{SimulationScreen: simulation}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- (Plugin{screen: screen}).Run(ctx, client) }()
	press := func(key tcell.Key, r rune) {
		simulation.InjectKey(key, r, tcell.ModNone)
		time.Sleep(25 * time.Millisecond)
	}
	typeText := func(text string) {
		for _, r := range text {
			press(tcell.KeyRune, r)
		}
	}
	waitForScreen(t, screen, "KompoSyn is ready", "")
	typeText("Update the plan")
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Tool approval", "")
	waitForScreen(t, screen, "Review approval", "")
	waitForScreen(t, screen, "Allow once · authorize this action", "")
	if strings.Contains(renderedScreen(screen), "share this exact file") || strings.Contains(renderedScreen(screen), "Deny this read") {
		t.Fatal("non-read approval showed filesystem-specific labels")
	}
	if chat.Snapshot().Plan != nil {
		t.Fatalf("plan changed before explicit approval: %+v", chat.Snapshot().Plan)
	}
	press(tcell.KeyEnd, 0)
	press(tcell.KeyEnd, 0)
	press(tcell.KeyTab, 0)
	for range 3 { // Cancel turn is initially selected; deliberately navigate to Allow once.
		press(tcell.KeyUp, 0)
	}
	press(tcell.KeyEnter, 0)
	waitForScreen(t, screen, "Tool update_plan · finished · succeeded", "")
	if plan := chat.Snapshot().Plan; len(plan) != 1 || plan[0].Step != "review" {
		t.Fatalf("approved update_plan did not update the in-memory plan: %+v", plan)
	}
	provider.mu.Lock()
	requests := append([]modelprovider.ChatRequest(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("provider did not receive the initial plan request")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("conversation approval terminal did not stop")
	}
}

type conversationApprovalProvider struct {
	mu       sync.Mutex
	requests []modelprovider.ChatRequest
}

func (*conversationApprovalProvider) ID() modelprovider.ID { return modelprovider.OpenAI }
func (*conversationApprovalProvider) AuthMethods() []modelprovider.AuthMethod {
	return []modelprovider.AuthMethod{modelprovider.AuthAPIKey}
}
func (*conversationApprovalProvider) Authenticate(context.Context, modelprovider.Credential) (modelprovider.AuthResult, error) {
	return modelprovider.AuthResult{Provider: modelprovider.OpenAI}, nil
}
func (*conversationApprovalProvider) RouteCapabilities(method modelprovider.AuthMethod) modelprovider.RouteCapabilities {
	if method == modelprovider.AuthAPIKey {
		return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportSupported}
	}
	return modelprovider.RouteCapabilities{ClientTools: modelprovider.SupportUnsupported}
}
func (*conversationApprovalProvider) ListModels(_ context.Context, credential modelprovider.Credential) ([]modelprovider.Model, error) {
	if credential.APIKey == "" {
		return nil, errors.New("fixture credential is unavailable")
	}
	return []modelprovider.Model{{ID: "conversation-approval-fixture", DisplayName: "Conversation Approval Fixture", Compatibility: modelprovider.CompatibilitySupported}}, nil
}
func (p *conversationApprovalProvider) StreamChat(_ context.Context, request modelprovider.ChatRequest, delta func(string)) (modelprovider.Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	requestNumber := len(p.requests)
	p.mu.Unlock()
	switch requestNumber {
	case 1:
		arguments := json.RawMessage(`{"plan":[{"step":"review","status":"in_progress"}]}`)
		encodedArguments, _ := json.Marshal(string(arguments))
		native := json.RawMessage(`{"type":"function_call","id":"item-plan-1","call_id":"plan-1","name":"update_plan","arguments":` + string(encodedArguments) + `,"status":"completed"}`)
		return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionToolCalls, Text: "Updating the plan.", Native: []json.RawMessage{native}, ToolCalls: []modelprovider.ToolCall{{CallID: "plan-1", ToolID: tools.UpdatePlanID, Arguments: arguments}}}, nil
	case 2:
		if delta != nil {
			delta("Plan updated.")
		}
		return modelprovider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: modelprovider.OutcomeCompleted, Completion: modelprovider.CompletionFinal, Text: "Plan updated.", Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"Plan updated."}`)}}, nil
	default:
		return modelprovider.Result{}, errors.New("unexpected conversation approval provider request")
	}
}

var _ modelprovider.Provider = (*conversationApprovalProvider)(nil)
