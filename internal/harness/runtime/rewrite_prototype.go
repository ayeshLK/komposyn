// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

const rewritePrototypeTool tools.ID = "rewrite_file_prototype"

// RewritePrototypeRequest supplies exact whole-file context and the desired
// result for a prior approved read in the current conversation.
type RewritePrototypeRequest struct {
	ReadCallID, Destination string
	Base, Content           string
}

// RunRewritePrototype is a local opt-in for the isolated prototype. It uses
// the normal approval interaction and has no provider tool definition.
func (c *Chat) RunRewritePrototype(parent context.Context, request RewritePrototypeRequest) (tools.Result, error) {
	refused := tools.Result{Outcome: tools.OutcomeNotExecuted}
	if parent == nil {
		return refused, errReadAuthorityInvalid
	}
	attempt, err := newReadAuthorityID()
	if err != nil {
		return refused, err
	}

	c.mu.Lock()
	r, conversation := c.toolRuntime, c.conversationID
	if c.closed || r == nil || !r.rewritePrototypeEnabled {
		c.mu.Unlock()
		return refused, errReadAuthorityInvalid
	}
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return refused, ErrRequestActive
	}
	profile := ""
	if c.selected != nil {
		profile = c.selected.ProfileID
	}
	ctx, cancel := context.WithCancel(parent)
	_, _, finish := c.reserveChatOperationLocked(cancel, profile)
	c.mu.Unlock()
	defer finish()

	return r.runRewritePrototype(ctx, conversation, attempt, attempt, request.ReadCallID, request.Destination, request.Base, request.Content)
}

type rewritePrototypeHost interface {
	ExecuteApprovedRewritePrototype(context.Context, *ApprovedRewritePrototype) (tools.Result, error)
}

// RewritePrototypeInput is local host data, never an invocation schema.
type RewritePrototypeInput struct {
	Conversation, Root, Target, Identity, ServiceID, FileRevision string
	RootGeneration, PolicyRevision, CoverageRevision              uint64
	Base, Content                                                 []byte
}

// ApprovedRewritePrototype is issued only by the opt-in runtime after taking
// a complete approved read receipt and consuming one exact save approval.
type ApprovedRewritePrototype struct {
	input       RewritePrototypeInput
	claimed     atomic.Bool
	withCurrent func(func() error) error
}

func (a *ApprovedRewritePrototype) Claim() (RewritePrototypeInput, func(func() error) error, error) {
	if a == nil || a.withCurrent == nil || !a.claimed.CompareAndSwap(false, true) {
		return RewritePrototypeInput{}, nil, errReadAuthorityInvalid
	}
	if err := a.withCurrent(func() error { return nil }); err != nil {
		return RewritePrototypeInput{}, nil, err
	}
	return a.input, a.withCurrent, nil
}

func (r *toolRuntime) retainRewritePrototypeRead(capture privateReadCaptureRecord) {
	if !r.rewritePrototypeEnabled || capture.operation != string(tools.ReadFileID) || capture.rewriteRevision == "" {
		return
	}
	r.clearRewritePrototypeRead()
	capture.bytes = bytes.Clone(capture.bytes)
	capture.timer = time.AfterFunc(time.Until(capture.expires), func() {
		r.readAuthorityMu.Lock()
		defer r.readAuthorityMu.Unlock()
		if r.rewritePrototypeRead.timer == capture.timer {
			r.clearRewritePrototypeRead()
		}
	})
	r.rewritePrototypeRead = capture
}

func (r *toolRuntime) clearRewritePrototypeRead() {
	if r.rewritePrototypeRead.timer != nil {
		r.rewritePrototypeRead.timer.Stop()
	}
	clear(r.rewritePrototypeRead.bytes)
	r.rewritePrototypeRead = privateReadCaptureRecord{}
}

// runRewritePrototype uses whole-file exact context for this first slice.
// The caller supplies the prior read call, the exact base, and a full result.
// Production composition never enables this entry point.
func (r *toolRuntime) runRewritePrototype(ctx context.Context, conversation, turn, call, readCall, destination, base, content string) (tools.Result, error) {
	refused := tools.Result{Outcome: tools.OutcomeNotExecuted, ErrorCode: "rewrite_not_executed"}
	if r == nil || ctx == nil || r.approval == nil || conversation == "" || turn == "" || call == "" || readCall == "" || destination == "" {
		return refused, errReadAuthorityInvalid
	}
	host, ok := r.readAdmission.(rewritePrototypeHost)
	if !ok {
		return refused, errReadAuthorityInvalid
	}
	if !rewritePrototypeText(base, content) {
		return refused, errors.New("unsupported edit size, encoding, or line endings")
	}

	r.readAuthorityMu.Lock()
	receipt := r.rewritePrototypeRead
	epoch := r.readAuthorityEpoch
	currentRevision, _ := r.grants.Snapshot(conversation)
	if !r.rewritePrototypeEnabled || receipt.conversationID != conversation || receipt.callID != readCall || receipt.destination != destination ||
		receipt.target == "" || receipt.identity == "" || !time.Now().Before(receipt.expires) ||
		r.readGrantRevision != currentRevision || string(receipt.bytes) != base {
		r.readAuthorityMu.Unlock()
		return refused, errReadAuthorityInvalid
	}
	r.rewritePrototypeRead = privateReadCaptureRecord{}
	if receipt.timer != nil {
		receipt.timer.Stop()
	}
	r.readAuthorityMu.Unlock()
	defer clear(receipt.bytes)

	attempt, err := newReadAuthorityID()
	if err != nil {
		return refused, err
	}
	arguments := []byte(strconv.Quote(attempt))
	diff := rewritePrototypeDiff(receipt.target, destination, base, content)
	invocation := tools.Invocation{ConversationID: conversation, TurnID: turn, ProviderCallID: call, ToolID: rewritePrototypeTool, Arguments: arguments}
	request, err := bindAuthorizationRequest(invocation, permissions.Request{
		ConversationID: conversation, CallID: call, Destination: destination,
		AdmissionBinding: receipt.admission + ":" + attempt, RequireOnceKeys: []string{"edit", "target"},
		Requirements: []permissions.Requirement{
			{Kind: permissions.RequirementAction, Key: "edit", ToolID: string(rewritePrototypeTool), Description: diff, Operation: permissions.OperationWrite, ActionKey: "bounded-rewrite-once"},
			{Kind: permissions.RequirementResource, Key: "target", ToolID: string(rewritePrototypeTool), Description: "Edit this exact file once", Operation: permissions.OperationWrite,
				Resource: &permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: receipt.target, Operation: permissions.OperationWrite}},
		},
	})
	if err != nil {
		return refused, err
	}
	pending, _, err := r.beginApproval(request, epoch)
	if err != nil || pending.ID == "" {
		return refused, errors.Join(errReadAuthorityInvalid, err)
	}
	defer func() { _, _ = r.coordinator.Respond(pending.ID, permissions.ApprovalCancel, "") }()
	choice, redirect, err := r.approval.Review(ctx, pending)
	if err != nil {
		return refused, err
	}
	if choice != permissions.ApprovalOnce || redirect != "" {
		return refused, nil
	}

	r.readAuthorityMu.Lock()
	if epoch != r.readAuthorityEpoch || !time.Now().Before(receipt.expires) || ctx.Err() != nil {
		r.readAuthorityMu.Unlock()
		return refused, errReadAuthorityInvalid
	}
	response, err := r.coordinator.Respond(pending.ID, choice, redirect)
	if err != nil || response.Outcome != permissions.ApprovalGranted {
		r.readAuthorityMu.Unlock()
		return refused, errors.Join(errReadAuthorityInvalid, err)
	}
	transition, err := r.consumePermissionLocked(request, response.Transition.AfterRevision, epoch, false)
	if err != nil || len(transition.ConsumedIDs) != len(request.RequireOnceKeys) {
		r.readAuthorityMu.Unlock()
		return refused, errors.Join(errReadAuthorityInvalid, err)
	}
	retired := epoch != r.readAuthorityEpoch
	epoch = r.readAuthorityEpoch
	r.readAuthorityMu.Unlock()
	if retired && r.retireHistory != nil {
		r.retireHistory(conversation)
	}

	approvedBytes := []byte(content)
	defer clear(approvedBytes)
	approved := &ApprovedRewritePrototype{
		input: RewritePrototypeInput{
			Conversation: conversation, Root: receipt.root, Target: receipt.target, Identity: receipt.identity, ServiceID: receipt.serviceID, FileRevision: receipt.rewriteRevision,
			RootGeneration: receipt.rootGeneration, PolicyRevision: receipt.policyRevision, CoverageRevision: receipt.coverage,
			Base: receipt.bytes, Content: approvedBytes,
		},
		withCurrent: func(op func() error) error {
			return r.withReadAuthorityGate(epoch, func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !time.Now().Before(receipt.expires) {
					return errReadAuthorityInvalid
				}
				return op()
			})
		},
	}
	result, err := host.ExecuteApprovedRewritePrototype(ctx, approved)
	if currentErr := approved.withCurrent(func() error { return nil }); currentErr != nil {
		// Retain a proven partial effect as a status only. The invalidated
		// attempt cannot publish target details or claim verified completion.
		retired := tools.Result{Outcome: tools.OutcomeUnknown, ErrorCode: "rewrite_result_stale"}
		switch result.Outcome {
		case tools.OutcomePartial:
			retired = tools.Result{Outcome: tools.OutcomePartial, ErrorCode: "rewrite_partial"}
		case tools.OutcomeNotExecuted:
			retired = refused
		}
		return retired, errors.Join(err, ctx.Err(), currentErr)
	}
	return result, err
}

func rewritePrototypeText(base, content string) bool {
	convention := func(text string) (string, bool) {
		if len(text) > 64<<10 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return "", false
		}
		rest := strings.ReplaceAll(text, "\r\n", "")
		switch {
		case strings.ContainsRune(rest, '\r'):
			return "", false
		case strings.Contains(text, "\r\n"):
			return "CRLF", !strings.ContainsRune(rest, '\n')
		case strings.ContainsRune(text, '\n'):
			return "LF", true
		default:
			return "", true
		}
	}
	before, beforeOK := convention(base)
	after, afterOK := convention(content)
	return beforeOK && afterOK && (before == "" || after == "" || before == after)
}

func rewritePrototypeDiff(target, destination, base, content string) string {
	var diff strings.Builder
	fmt.Fprintf(&diff, "Edit %q for %q (%d → %d bytes). Complete diff:\n", target, destination, len(base), len(content))
	for _, side := range []struct{ prefix, text string }{{"-", base}, {"+", content}} {
		for line := range strings.SplitAfterSeq(side.text, "\n") {
			if line != "" {
				fmt.Fprintf(&diff, "%s %s\n", side.prefix, strconv.Quote(line))
			}
		}
	}
	return diff.String()
}
