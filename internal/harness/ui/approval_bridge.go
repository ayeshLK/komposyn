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

package ui

import (
	"context"
	"errors"
	"sync"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/ui"
)

var ErrApprovalResponse = errors.New("approval response is stale, unmatched, or invalid")

// ApprovalBridge adapts runtime-owned approval requests to detached frontend
// records. The bridge only transports choices; the runtime coordinator alone
// validates choices and creates grants.
type ApprovalBridge struct {
	mu      sync.Mutex
	events  chan ui.ApprovalEvent
	pending map[string]*bridgeWait
}
type bridgeWait struct {
	conversation string
	request      ui.ApprovalRequest
	response     chan ui.ApprovalResponse
	invalidated  chan struct{}
	answered     bool
}

func NewApprovalBridge() *ApprovalBridge {
	return &ApprovalBridge{events: make(chan ui.ApprovalEvent, 4), pending: make(map[string]*bridgeWait)}
}
func (b *ApprovalBridge) Events() <-chan ui.ApprovalEvent { return b.events }

func (b *ApprovalBridge) Review(ctx context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
	request := ui.ApprovalRequest{ID: pending.ID}
	allowConversation := len(pending.Decision.Missing) > 0
	for _, req := range pending.Request.Requirements {
		if !req.SessionAllowed || req.Operation == permissions.OperationDelete {
			allowConversation = false
		}
	}
	for _, req := range pending.Decision.Missing {
		item := ui.ApprovalRequirement{Kind: string(req.Kind), ToolID: req.ToolID, Operation: string(req.Operation), ConversationAllowed: req.SessionAllowed && req.Operation != permissions.OperationDelete}
		item.Description = req.Description
		if req.Kind == permissions.RequirementAction && item.Description == "" {
			item.Description = req.ToolID + " " + string(req.Operation) + " action"
		}
		if req.Resource != nil {
			item.ResourceKind, item.ResourceScope = string(req.Resource.Kind), req.Resource.Target
		}
		request.Requirements = append(request.Requirements, item)
	}
	request.ToolID = pending.Request.Requirements[0].ToolID
	request.ToolDescription = request.ToolID + " requested access"
	request.AllowConversation = allowConversation
	wait := &bridgeWait{conversation: pending.Request.ConversationID, request: request, response: make(chan ui.ApprovalResponse, 1), invalidated: make(chan struct{})}
	b.mu.Lock()
	if len(b.pending) != 0 {
		b.mu.Unlock()
		return "", "", errors.New("approval already pending")
	}
	b.pending[pending.ID] = wait
	b.mu.Unlock()
	defer b.finish(pending.ID)
	select {
	case b.events <- ui.ApprovalEvent{Request: cloneApprovalRequest(&request)}:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	select {
	case response := <-wait.response:
		return permissions.ApprovalChoice(response.Choice), response.RedirectText, nil
	case <-wait.invalidated:
		return "", "", errors.New("approval was invalidated")
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

func (b *ApprovalBridge) Respond(response ui.ApprovalResponse) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	wait := b.pending[response.ID]
	if wait == nil || wait.answered || response.Choice == ui.ApprovalConversation && !wait.request.AllowConversation {
		return ErrApprovalResponse
	}
	switch response.Choice {
	case ui.ApprovalOnce, ui.ApprovalConversation, ui.ApprovalDeny, ui.ApprovalCancel:
		if response.RedirectText != "" {
			return ErrApprovalResponse
		}
	case ui.ApprovalRedirect:
		if response.RedirectText == "" {
			return ErrApprovalResponse
		}
	default:
		return ErrApprovalResponse
	}
	wait.answered = true
	wait.response <- response
	return nil
}

func (b *ApprovalBridge) InvalidateConversation(conversation string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, wait := range b.pending {
		if wait.conversation == conversation {
			delete(b.pending, id)
			close(wait.invalidated)
			select {
			case b.events <- ui.ApprovalEvent{DismissedID: id}:
			default:
			}
		}
	}
}
func (b *ApprovalBridge) finish(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.pending[id]; ok {
		delete(b.pending, id)
		select {
		case b.events <- ui.ApprovalEvent{DismissedID: id}:
		default:
		}
	}
}
func cloneApprovalRequest(request *ui.ApprovalRequest) *ui.ApprovalRequest {
	if request == nil {
		return nil
	}
	copy := *request
	copy.Requirements = append([]ui.ApprovalRequirement(nil), request.Requirements...)
	return &copy
}
