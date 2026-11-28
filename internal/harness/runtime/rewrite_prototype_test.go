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
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

type rewriteApprovalTestHost struct {
	FilesystemReadAdmissionHost
	execute func(context.Context, *ApprovedRewritePrototype) (tools.Result, error)
}

func (h rewriteApprovalTestHost) ExecuteApprovedRewritePrototype(ctx context.Context, approved *ApprovedRewritePrototype) (tools.Result, error) {
	return h.execute(ctx, approved)
}

func TestRewritePrototypeApprovalBindsAndConsumesCompleteEdit(t *testing.T) {
	for _, test := range []struct {
		name         string
		choice       permissions.ApprovalChoice
		reset        bool
		disabled     bool
		wrongContext bool
		wantCalls    int
	}{
		{name: "once", choice: permissions.ApprovalOnce, wantCalls: 1},
		{name: "deny", choice: permissions.ApprovalDeny},
		{name: "conversation grant", choice: permissions.ApprovalConversation},
		{name: "reset before release", choice: permissions.ApprovalOnce, reset: true},
		{name: "default disabled", choice: permissions.ApprovalOnce, disabled: true},
		{name: "wrong base", choice: permissions.ApprovalOnce, wrongContext: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := syntheticAuthorityRuntime()
			root := t.TempDir()
			target := filepath.Join(root, "file")
			r.rewritePrototypeEnabled = !test.disabled
			r.retainRewritePrototypeRead(privateReadCaptureRecord{
				operation: string(tools.ReadFileID), rewriteRevision: "observed-revision",
				target: target, root: root, destination: "provider",
				conversationID: "conversation", callID: "read-call", identity: "1:2:32768",
				admission: "admission", serviceID: "service", expires: time.Now().Add(time.Minute),
				bytes: []byte("before\n"),
			})
			t.Cleanup(r.invalidateReadAuthority)
			r.approval = ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				if pending.Request.Binding == "" || pending.Request.Destination != "provider" ||
					!strings.Contains(pending.Request.Requirements[0].Description, "- \"before\\n\"\n+ \"after\\n\"") {
					t.Fatalf("approval failed to bind complete diff: %+v", pending.Request)
				}
				if test.reset {
					r.invalidateConversation("conversation")
				}
				return test.choice, "", nil
			})
			calls := 0
			r.readAdmission = rewriteApprovalTestHost{execute: func(_ context.Context, approved *ApprovedRewritePrototype) (tools.Result, error) {
				input, release, err := approved.Claim()
				if err != nil {
					t.Fatal(err)
				}
				if input.Target != target || input.ServiceID != "service" || input.FileRevision != "observed-revision" ||
					string(input.Base) != "before\n" || string(input.Content) != "after\n" {
					t.Fatalf("wrong approved input: %+v", input)
				}
				if _, _, err := approved.Claim(); !errors.Is(err, errReadAuthorityInvalid) {
					t.Fatalf("approval replay: %v", err)
				}
				err = release(func() error { calls++; return nil })
				return tools.Result{Outcome: tools.OutcomeSucceeded}, err
			}}
			base := "before\n"
			if test.wrongContext {
				base = "wrong\n"
			}
			ctx := context.Background()
			result, err := r.runRewritePrototype(ctx, "conversation", "turn", "save", "read-call", "provider", base, "after\n")
			if calls != test.wantCalls || calls == 1 && (err != nil || result.Outcome != tools.OutcomeSucceeded) {
				t.Fatalf("released %d times, result=%+v, err=%v", calls, result, err)
			}
			if calls == 1 {
				if _, err := r.runRewritePrototype(ctx, "conversation", "turn", "save-again", "read-call", "provider", base, "after\n"); !errors.Is(err, errReadAuthorityInvalid) {
					t.Fatalf("prior read receipt should be spent: %v", err)
				}
			}
		})
	}
}

func TestRewritePrototypeChatCancelAfterRelease(t *testing.T) {
	for _, outcome := range []tools.Outcome{tools.OutcomePartial, tools.OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			r := syntheticAuthorityRuntime()
			r.rewritePrototypeEnabled = true
			root := t.TempDir()
			r.retainRewritePrototypeRead(privateReadCaptureRecord{
				operation: string(tools.ReadFileID), rewriteRevision: "revision",
				target: filepath.Join(root, "file"), root: root, destination: "provider",
				conversationID: "conversation", callID: "read-call", identity: "1:2:32768",
				admission: "admission", serviceID: "service", expires: time.Now().Add(time.Minute),
				bytes: []byte("before"),
			})
			t.Cleanup(r.invalidateReadAuthority)
			r.approval = ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
				return permissions.ApprovalOnce, "", nil
			})
			chat := &Chat{toolRuntime: r, conversationID: "conversation"}
			req := RewritePrototypeRequest{ReadCallID: "read-call", Destination: "provider", Base: "before", Content: "after"}
			var done chan struct{}
			releases := 0
			r.readAdmission = rewriteApprovalTestHost{execute: func(ctx context.Context, approved *ApprovedRewritePrototype) (tools.Result, error) {
				chat.mu.Lock()
				active := chat.active
				done = chat.activeDone
				chat.mu.Unlock()
				if !active || done == nil {
					t.Fatal("rewrite did not reserve the active chat operation")
				}
				if _, err := chat.RunRewritePrototype(ctx, req); !errors.Is(err, ErrRequestActive) {
					t.Fatalf("concurrent rewrite was not refused: %v", err)
				}
				_, release, err := approved.Claim()
				if err != nil {
					t.Fatal(err)
				}
				if err := release(func() error { releases++; return nil }); err != nil {
					t.Fatal(err)
				}
				chat.Cancel()
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("chat cancellation did not reach the released rewrite")
				}
				select {
				case <-done:
					t.Fatal("chat operation finished before the host returned")
				default:
				}
				return tools.Result{Outcome: outcome, Content: []tools.Content{{Kind: tools.ContentText, Text: "stale detail"}}}, ctx.Err()
			}}
			result, err := chat.RunRewritePrototype(context.Background(), req)
			if !errors.Is(err, context.Canceled) || result.Outcome != outcome || len(result.Content) != 0 {
				t.Fatalf("canceled result = %+v, %v", result, err)
			}
			select {
			case <-done:
			default:
				t.Fatal("chat completion was not signaled after the host returned")
			}
			if _, err := chat.RunRewritePrototype(context.Background(), req); !errors.Is(err, errReadAuthorityInvalid) || releases != 1 {
				t.Fatalf("canceled rewrite replayed: releases=%d, err=%v", releases, err)
			}
		})
	}
}
