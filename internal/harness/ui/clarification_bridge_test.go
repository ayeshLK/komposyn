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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"github.com/ayeshLK/komposyn/plugins/ui"
)

func startClarification(t *testing.T, bridge *ClarificationBridge, ctx context.Context, id, conversation string) <-chan struct {
	response runtime.ClarificationResponse
	err      error
} {
	t.Helper()
	done := make(chan struct {
		response runtime.ClarificationResponse
		err      error
	}, 1)
	go func() {
		response, err := bridge.Request(ctx, runtime.ClarificationRequest{ID: id, ConversationID: conversation, Question: "Which format?", Options: []tools.SuggestedAnswer{{Label: "JSON", Description: "Structured"}}})
		done <- struct {
			response runtime.ClarificationResponse
			err      error
		}{response, err}
	}()
	select {
	case event := <-bridge.Events():
		if event.Request == nil || event.Request.ID != id || event.Request.ConversationID != conversation || len(event.Request.Options) != 1 {
			t.Fatalf("clarification event lost detached request: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("clarification request was not delivered")
	}
	return done
}

func TestClarificationBridgeCorrelatesExplicitAnswerExactly(t *testing.T) {
	bridge := NewClarificationBridge()
	done := startClarification(t, bridge, context.Background(), "opaque-request", "opaque-conversation")
	if _, err := bridge.Request(context.Background(), runtime.ClarificationRequest{ID: "second", ConversationID: "opaque-conversation", Question: "Another?"}); err == nil {
		t.Fatal("a second concurrent clarification was accepted")
	}
	select {
	case result := <-done:
		t.Fatalf("silence became an answer: %+v", result)
	case <-time.After(60 * time.Millisecond):
	}
	if err := bridge.Respond(ui.ClarificationResponse{ID: "wrong", ConversationID: "opaque-conversation", Choice: ui.ClarificationAnswer, Answer: "wrong"}); err == nil {
		t.Fatal("wrong request ID accepted")
	}
	if err := bridge.Respond(ui.ClarificationResponse{ID: "opaque-request", ConversationID: "other", Choice: ui.ClarificationAnswer, Answer: "wrong"}); err == nil {
		t.Fatal("wrong conversation accepted")
	}
	answer := "  keep [red] literal\t雪  "
	response := ui.ClarificationResponse{ID: "opaque-request", ConversationID: "opaque-conversation", Choice: ui.ClarificationAnswer, Answer: answer}
	if err := bridge.Respond(response); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Respond(response); err == nil {
		t.Fatal("duplicate response accepted")
	}
	got := <-done
	if got.err != nil || got.response.Outcome != runtime.ClarificationAnswered || got.response.Answer != answer {
		t.Fatalf("answer changed across bridge: %+v", got)
	}
	select {
	case event := <-bridge.Events():
		if event.DismissedID != response.ID {
			t.Fatalf("dismissal=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("completed clarification did not dismiss")
	}
}

func TestClarificationBridgeDeclineCancelAndInvalidationAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		choice  ui.ClarificationChoice
		outcome runtime.ClarificationOutcome
	}{
		{ui.ClarificationDecline, runtime.ClarificationDeclined},
		{ui.ClarificationCancel, runtime.ClarificationCancelled},
	} {
		bridge := NewClarificationBridge()
		done := startClarification(t, bridge, context.Background(), "request-"+string(tc.choice), "conversation")
		if err := bridge.Respond(ui.ClarificationResponse{ID: "request-" + string(tc.choice), ConversationID: "conversation", Choice: tc.choice}); err != nil {
			t.Fatal(err)
		}
		got := <-done
		if got.err != nil || got.response.Outcome != tc.outcome || got.response.Answer != "" {
			t.Fatalf("choice=%s response=%+v err=%v", tc.choice, got.response, got.err)
		}
	}

	bridge := NewClarificationBridge()
	done := startClarification(t, bridge, context.Background(), "stale", "conversation")
	bridge.InvalidateConversation("conversation")
	if got := <-done; got.err == nil {
		t.Fatal("invalidated clarification returned a response")
	}
	if err := bridge.Respond(ui.ClarificationResponse{ID: "stale", ConversationID: "conversation", Choice: ui.ClarificationAnswer, Answer: "late"}); err == nil {
		t.Fatal("late response accepted")
	}
}

func TestClarificationBridgeCallerCancellationRejectsLateAnswer(t *testing.T) {
	bridge := NewClarificationBridge()
	ctx, cancel := context.WithCancel(context.Background())
	done := startClarification(t, bridge, ctx, "cancelled", "conversation")
	cancel()
	if got := <-done; got.err == nil {
		t.Fatal("caller cancellation did not stop wait")
	}
	if err := bridge.Respond(ui.ClarificationResponse{ID: "cancelled", ConversationID: "conversation", Choice: ui.ClarificationAnswer, Answer: "late"}); err == nil {
		t.Fatal("late answer after caller cancellation accepted")
	}
}

func TestClarificationBridgeAnswerCancelInvalidateInterleavings(t *testing.T) {
	for i := 0; i < 32; i++ {
		bridge := NewClarificationBridge()
		ctx, cancel := context.WithCancel(context.Background())
		id := fmt.Sprintf("pending-%d", i)
		done := startClarification(t, bridge, ctx, id, "conversation")
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_ = bridge.Respond(ui.ClarificationResponse{ID: id, ConversationID: "conversation", Choice: ui.ClarificationAnswer, Answer: "answer"})
		}()
		go func() {
			defer wg.Done()
			<-start
			cancel()
		}()
		go func() {
			defer wg.Done()
			<-start
			bridge.InvalidateConversation("conversation")
		}()
		close(start)
		wg.Wait()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("clarification wait leaked during answer/cancel/invalidate race")
		}
		if err := bridge.Respond(ui.ClarificationResponse{ID: id, ConversationID: "conversation", Choice: ui.ClarificationAnswer, Answer: "late"}); err == nil {
			t.Fatal("late answer accepted after concurrent cleanup")
		}
	}
}
