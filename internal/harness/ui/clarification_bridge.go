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
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/ui"
)

var ErrClarificationResponse = errors.New("clarification response is stale, unmatched, or invalid")

type ClarificationBridge struct {
	mu      sync.Mutex
	events  chan ui.ClarificationEvent
	pending map[string]*clarificationWait
}

type clarificationWait struct {
	conversation string
	request      ui.ClarificationRequest
	response     chan ui.ClarificationResponse
	invalidated  chan struct{}
	answered     bool
}

func NewClarificationBridge() *ClarificationBridge {
	return &ClarificationBridge{events: make(chan ui.ClarificationEvent, 4), pending: make(map[string]*clarificationWait)}
}

func (b *ClarificationBridge) Events() <-chan ui.ClarificationEvent { return b.events }

func (b *ClarificationBridge) Request(ctx context.Context, request runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
	if b == nil || request.ID == "" || request.ConversationID == "" || request.Question == "" {
		return runtime.ClarificationResponse{}, ErrClarificationResponse
	}
	uiRequest := ui.ClarificationRequest{ID: request.ID, ConversationID: request.ConversationID, Question: request.Question, MaxAnswerBytes: request.MaxAnswerBytes, Options: make([]ui.SuggestedAnswer, len(request.Options))}
	for i, option := range request.Options {
		uiRequest.Options[i] = ui.SuggestedAnswer{Label: option.Label, Description: option.Description}
	}
	wait := &clarificationWait{conversation: request.ConversationID, request: uiRequest, response: make(chan ui.ClarificationResponse, 1), invalidated: make(chan struct{})}
	b.mu.Lock()
	if len(b.pending) != 0 {
		b.mu.Unlock()
		return runtime.ClarificationResponse{}, errors.New("clarification is already pending")
	}
	b.pending[request.ID] = wait
	b.mu.Unlock()
	defer b.finish(request.ID)
	select {
	case b.events <- ui.ClarificationEvent{Request: cloneClarificationRequest(&uiRequest)}:
	case <-ctx.Done():
		return runtime.ClarificationResponse{}, ctx.Err()
	}
	select {
	case response := <-wait.response:
		return runtime.ClarificationResponse{ID: response.ID, ConversationID: response.ConversationID, Outcome: runtimeClarificationOutcome(response.Choice), Answer: response.Answer}, nil
	case <-wait.invalidated:
		return runtime.ClarificationResponse{}, errors.New("clarification was invalidated")
	case <-ctx.Done():
		return runtime.ClarificationResponse{}, ctx.Err()
	}
}

func runtimeClarificationOutcome(choice ui.ClarificationChoice) runtime.ClarificationOutcome {
	switch choice {
	case ui.ClarificationAnswer:
		return runtime.ClarificationAnswered
	case ui.ClarificationDecline:
		return runtime.ClarificationDeclined
	case ui.ClarificationCancel:
		return runtime.ClarificationCancelled
	default:
		return ""
	}
}

func (b *ClarificationBridge) Respond(response ui.ClarificationResponse) error {
	if b == nil {
		return ErrClarificationResponse
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	wait := b.pending[response.ID]
	if wait == nil || wait.answered || response.ConversationID != wait.conversation || len(response.Answer) > runtime.MaxClarificationAnswerBytes || !utf8.ValidString(response.Answer) {
		return ErrClarificationResponse
	}
	switch response.Choice {
	case ui.ClarificationAnswer:
	case ui.ClarificationDecline, ui.ClarificationCancel:
		if response.Answer != "" {
			return ErrClarificationResponse
		}
	default:
		return ErrClarificationResponse
	}
	wait.answered = true
	wait.response <- response
	return nil
}

func (b *ClarificationBridge) InvalidateConversation(conversation string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, wait := range b.pending {
		if wait.conversation == conversation {
			delete(b.pending, id)
			close(wait.invalidated)
			select {
			case b.events <- ui.ClarificationEvent{DismissedID: id}:
			default:
			}
		}
	}
}

func (b *ClarificationBridge) finish(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.pending[id]; ok {
		delete(b.pending, id)
		select {
		case b.events <- ui.ClarificationEvent{DismissedID: id}:
		default:
		}
	}
}

func cloneClarificationRequest(request *ui.ClarificationRequest) *ui.ClarificationRequest {
	if request == nil {
		return nil
	}
	copy := *request
	copy.Options = append([]ui.SuggestedAnswer(nil), request.Options...)
	return &copy
}
