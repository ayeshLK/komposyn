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
	"encoding/json"
	"errors"
	"sync"

	"github.com/ayeshLK/komposyn/plugins/tools"
)

// ConversationPlans stores only the current plan for each opaque in-memory
// conversation. It has no persistence or authorization behavior; callers must
// invoke Replace only from the trusted, centrally authorized tool path.
type ConversationPlans struct {
	mu     sync.RWMutex
	plans  map[string][]tools.PlanStep
	closed map[string]bool
}

func NewConversationPlans() *ConversationPlans {
	return &ConversationPlans{plans: make(map[string][]tools.PlanStep), closed: make(map[string]bool)}
}

// Replace atomically replaces the complete plan after validating the same
// bounded schema used for provider arguments.
func (p *ConversationPlans) Replace(conversationID string, steps []tools.PlanStep) error {
	if p == nil || conversationID == "" {
		return errors.New("conversation plan store and identity are required")
	}
	raw, err := json.Marshal(tools.UpdatePlanInput{Plan: steps})
	if err != nil {
		return err
	}
	validated, err := tools.ParseUpdatePlan(raw)
	if err != nil {
		return err
	}
	copy := append(make([]tools.PlanStep, 0, len(validated.Plan)), validated.Plan...)
	p.mu.Lock()
	if p.plans == nil {
		p.plans = make(map[string][]tools.PlanStep)
	}
	if p.closed[conversationID] {
		p.mu.Unlock()
		return errors.New("conversation plan is closed")
	}
	p.plans[conversationID] = copy
	p.mu.Unlock()
	return nil
}

// Snapshot returns a detached copy, or nil when no plan has been set.
func (p *ConversationPlans) Snapshot(conversationID string) []tools.PlanStep {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	steps, exists := p.plans[conversationID]
	if !exists {
		return nil
	}
	return append(make([]tools.PlanStep, 0, len(steps)), steps...)
}

// Clear removes one conversation plan. It is used on confirmed reset and
// shutdown; a later conversation can never inherit the previous plan.
func (p *ConversationPlans) Clear(conversationID string) {
	if p == nil || conversationID == "" {
		return
	}
	p.mu.Lock()
	delete(p.plans, conversationID)
	p.mu.Unlock()
}

// Close clears and permanently closes an opaque conversation identity. A
// late tool callback cannot recreate its plan after reset or shutdown.
func (p *ConversationPlans) Close(conversationID string) {
	if p == nil || conversationID == "" {
		return
	}
	p.mu.Lock()
	if p.closed == nil {
		p.closed = make(map[string]bool)
	}
	p.closed[conversationID] = true
	delete(p.plans, conversationID)
	p.mu.Unlock()
}
