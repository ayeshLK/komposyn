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
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/ui"
)

func TestApprovalBridgeCorrelatesExplicitChoiceAndDoesNotWiden(t *testing.T) {
	bridge := NewApprovalBridge()
	pending := permissions.PendingApproval{ID: "opaque", Request: permissions.Request{ConversationID: "conversation", Requirements: []permissions.Requirement{{Kind: permissions.RequirementAction, ToolID: "fixture", Operation: permissions.OperationWrite, SessionAllowed: true}, {Kind: permissions.RequirementResource, ToolID: "fixture", Operation: permissions.OperationWrite, SessionAllowed: true, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: "/tmp/exact"}}}}, Decision: permissions.Decision{Missing: []permissions.Requirement{{Kind: permissions.RequirementAction, ToolID: "fixture", Operation: permissions.OperationWrite, SessionAllowed: true}, {Kind: permissions.RequirementResource, ToolID: "fixture", Operation: permissions.OperationWrite, SessionAllowed: true, Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: "/tmp/exact"}}}}}
	result := make(chan struct {
		choice   permissions.ApprovalChoice
		redirect string
		err      error
	}, 1)
	go func() {
		choice, redirect, err := bridge.Review(context.Background(), pending)
		result <- struct {
			choice   permissions.ApprovalChoice
			redirect string
			err      error
		}{choice, redirect, err}
	}()
	var event ui.ApprovalEvent
	select {
	case event = <-bridge.Events():
	case <-time.After(time.Second):
		t.Fatal("approval event not delivered")
	}
	if event.Request == nil || event.Request.ID != "opaque" || len(event.Request.Requirements) != 2 || event.Request.Requirements[1].ResourceScope != "/tmp/exact" || !event.Request.AllowConversation {
		t.Fatalf("detached request lost requirements: %+v", event.Request)
	}
	if err := bridge.Respond(ui.ApprovalResponse{ID: "wrong", Choice: ui.ApprovalOnce}); err == nil {
		t.Fatal("unmatched response accepted")
	}
	if err := bridge.Respond(ui.ApprovalResponse{ID: "opaque", Choice: ui.ApprovalOnce}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Respond(ui.ApprovalResponse{ID: "opaque", Choice: ui.ApprovalOnce}); err == nil {
		t.Fatal("duplicate response accepted")
	}
	got := <-result
	if got.err != nil || got.choice != permissions.ApprovalOnce || got.redirect != "" {
		t.Fatalf("Review result = %+v", got)
	}
	select {
	case event = <-bridge.Events():
		if event.DismissedID != "opaque" {
			t.Fatalf("dismissal=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("dismissal event not delivered")
	}
}

func TestApprovalBridgeRejectsConversationChoiceForDelete(t *testing.T) {
	bridge := NewApprovalBridge()
	pending := permissions.PendingApproval{ID: "delete", Request: permissions.Request{ConversationID: "c", Requirements: []permissions.Requirement{{ToolID: "fixture"}}}, Decision: permissions.Decision{Missing: []permissions.Requirement{{ToolID: "fixture", Operation: permissions.OperationDelete}}}}
	result := make(chan error, 1)
	go func() { _, _, err := bridge.Review(context.Background(), pending); result <- err }()
	event := <-bridge.Events()
	if event.Request.AllowConversation {
		t.Fatal("delete approval offered conversation lifetime")
	}
	if err := bridge.Respond(ui.ApprovalResponse{ID: "delete", Choice: ui.ApprovalConversation}); err == nil {
		t.Fatal("conversation choice accepted for delete")
	}
	bridge.InvalidateConversation("c")
	if err := <-result; err != nil {
		t.Fatalf("invalidation transport failed: %v", err)
	}
}
