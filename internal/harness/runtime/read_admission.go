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
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

// FilesystemReadAdmission is detached metadata plus a host-created opaque
// reference. Runtime carries it only between the trusted host and its own
// approval transaction; it is never sent to a tool or UI snapshot.
type FilesystemReadAdmission struct {
	Reference        any
	ServiceID        string
	ConversationID   string
	TurnID           string
	CallID           string
	Root             string
	Deadline         time.Time
	AdmissionBinding string
	Target           string
	Class            string
	Identity         string
	RootGeneration   uint64
	PolicyRevision   uint64
	CoverageRevision uint64
	Destination      string
}

// FilesystemReadPreparation has no read, execution, or descriptor-transfer
// method. Commit serializes once consumption with host invalidation while the
// retained object is still host-owned.
type FilesystemReadPreparation interface {
	Metadata() FilesystemReadAdmission
	WithAuthorityCommit(context.Context, func() error) error
	Close(context.Context) error
}

// FilesystemReadExecutionResult is an in-process host result. Bytes are never
// encoded into tools.Result, provider history, activity, snapshots or logs.
type FilesystemReadExecutionResult struct {
	Outcome              tools.Outcome
	Bytes                []byte
	ServiceID            string
	AdmissionBinding     string
	AuthorizationBinding string
	Identity             string
	RootGeneration       uint64
	PolicyRevision       uint64
	CoverageRevision     uint64
	Destination          string
	ReadStarted          bool
	WorkerJoined         bool
	CapabilitiesRetired  bool
}

// FilesystemReadExecutionAuthority is a narrow wrapper over the private
// runtime ledger. The callback is run under the runtime authority lock and
// must only perform bounded host checks, descriptor release, or private
// capture insertion.
type FilesystemReadExecutionAuthority interface {
	Claim() error
	WithCurrent(func() error) error
	AuthorizationBinding() string
	Stage(FilesystemReadAdmission, FilesystemReadExecutionResult) (*PrivateReadCapture, error)
}

type FilesystemReadExecutionHost interface {
	ExecuteAdmittedFilesystemRead(context.Context, FilesystemReadAdmission, FilesystemReadPreparation, *ReadExecutionReference, FilesystemReadExecutionAuthority) (*PrivateReadCapture, error)
}

type FilesystemReadExecutionInvalidator interface {
	InvalidateFilesystemReadExecutions(string) error
}

type filesystemReadExecutionAuthority struct {
	runtime *toolRuntime
	ref     *ReadExecutionReference
	request permissions.Request
	inside  bool
}

func (a *filesystemReadExecutionAuthority) Claim() error {
	if a == nil || a.runtime == nil {
		return errReadAuthorityInvalid
	}
	return a.runtime.ClaimReadExecution(a.ref, a.request.Binding, a.request.AdmissionBinding)
}

func (a *filesystemReadExecutionAuthority) WithCurrent(operation func() error) error {
	if a == nil || a.runtime == nil || operation == nil {
		return errReadAuthorityInvalid
	}
	return a.runtime.withClaimedReadCurrent(a.ref, a.request.Binding, a.request.AdmissionBinding, func() error {
		a.inside = true
		defer func() { a.inside = false }()
		return operation()
	})
}

func (a *filesystemReadExecutionAuthority) AuthorizationBinding() string {
	if a == nil {
		return ""
	}
	return a.request.Binding
}

func (a *filesystemReadExecutionAuthority) Stage(admission FilesystemReadAdmission, result FilesystemReadExecutionResult) (*PrivateReadCapture, error) {
	if a == nil || !a.inside {
		return nil, errReadAuthorityInvalid
	}
	return a.runtime.stagePrivateReadCaptureLocked(a.ref, admission, result)
}

// FilesystemReadAdmissionHost is the narrow retained-object seam for Stage 3.
// Implementations derive the selected root and owner coverage themselves.
type FilesystemReadAdmissionHost interface {
	AdmitFilesystemRead(context.Context, tools.Invocation, string) (FilesystemReadAdmission, error)
	PrepareAdmittedFilesystemRead(context.Context, FilesystemReadAdmission, uint64) (FilesystemReadPreparation, error)
	CloseFilesystemReadAdmission(context.Context, FilesystemReadAdmission) error
}

// FilesystemReadPublicationGate serializes final sensitive transcript and
// provider-request commitment with host owner, policy, and selected-root
// changes. The callback must remain bounded and must not perform provider I/O.
type FilesystemReadPublicationGate interface {
	WithFilesystemReadPublicationCurrent(context.Context, string, string, uint64, uint64, uint64, func() error) error
}

// FilesystemReadAdmissionInvalidator receives host lifecycle notifications.
// An empty conversation ID requests global capture/continuation retirement for
// host-wide owner or policy transitions.
type FilesystemReadAdmissionInvalidator interface {
	SetFilesystemReadInvalidator(func(string))
}

func filesystemReadActionKey() string {
	digest := sha256.Sum256([]byte("komposyn-filesystem-read-once-v1"))
	return hex.EncodeToString(digest[:])
}

func buildFilesystemReadRequest(invocation tools.Invocation, admission FilesystemReadAdmission) (permissions.Request, error) {
	if invocation.ToolID != tools.ID("read_file") || invocation.ConversationID == "" || invocation.ProviderCallID == "" || admission.Reference == nil || admission.AdmissionBinding == "" || admission.Target == "" || admission.Destination == "" || admission.Deadline.IsZero() || !time.Now().Before(admission.Deadline) {
		return permissions.Request{}, errors.New("trusted retained read admission is incomplete")
	}
	resourceKey := "disclosure"
	resourceDescription := "Share this exact file with the selected provider"
	if admission.Class == "provider_source" {
		resourceKey = "provider_source_disclosure"
		resourceDescription = "Share this classified provider source with the selected provider"
	}
	action := permissions.Requirement{
		Kind: permissions.RequirementAction, Key: "read", ToolID: string(invocation.ToolID),
		Description: "Read one exact regular file once", Operation: permissions.OperationRead,
		ActionKey: filesystemReadActionKey(), SessionAllowed: false,
	}
	resource := permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: admission.Target, Operation: permissions.OperationRead}
	resourceRequirement := permissions.Requirement{
		Kind: permissions.RequirementResource, Key: resourceKey, ToolID: string(invocation.ToolID),
		Description: resourceDescription, Operation: permissions.OperationRead, Resource: &resource, SessionAllowed: false,
	}
	request := permissions.Request{
		ConversationID:   invocation.ConversationID,
		CallID:           invocation.ProviderCallID,
		AdmissionBinding: admission.AdmissionBinding,
		Requirements:     []permissions.Requirement{action, resourceRequirement},
		RequireOnceKeys:  []string{resourceKey},
	}
	return bindAuthorizationRequest(invocation, request)
}

func (r *toolRuntime) approveFilesystemRead(ctx context.Context, invocation tools.Invocation, destination string) (admission FilesystemReadAdmission, preparation FilesystemReadPreparation, request permissions.Request, reference *ReadExecutionReference, err error) {
	if ctx == nil || r == nil || r.readAdmission == nil || r.approval == nil || !validToolCall(providerCallFor(invocation)) {
		return admission, nil, request, nil, errors.New("retained read approval is unavailable")
	}
	readEpoch := r.readAuthorityEpochSnapshot()
	admission, err = r.readAdmission.AdmitFilesystemRead(ctx, cloneInvocation(invocation), destination)
	if err != nil {
		return admission, nil, request, nil, err
	}
	admissionOpen := true
	defer func() {
		if admissionOpen {
			err = errors.Join(err, r.readAdmission.CloseFilesystemReadAdmission(ctx, admission))
		}
	}()
	request, err = buildFilesystemReadRequest(invocation, admission)
	if err != nil {
		return admission, nil, request, nil, err
	}
	decision := r.evaluator.Evaluate(request)
	if len(decision.Denied) != 0 || len(decision.Missing) == 0 || !r.checkReadAuthorityRevision(decision.Revision) {
		return admission, nil, request, nil, errors.New("retained read is denied or lacks target-specific once approval")
	}
	pending, beginDecision, err := r.beginApproval(request, readEpoch)
	if err != nil || pending.ID == "" || beginDecision.Revision != decision.Revision {
		r.coordinator.CancelConversation(invocation.ConversationID)
		return admission, nil, request, nil, errors.New("retained read approval could not be opened")
	}
	approvalCtx, cancelApproval := context.WithDeadline(ctx, admission.Deadline)
	defer cancelApproval()
	choice, redirect, reviewErr := r.approval.Review(approvalCtx, pending)
	if reviewErr != nil || choice != permissions.ApprovalOnce || redirect != "" || approvalCtx.Err() != nil || ctx.Err() != nil || !r.readAuthorityEpochCurrent(readEpoch) {
		r.coordinator.CancelConversation(invocation.ConversationID)
		return admission, nil, request, nil, errors.New("retained read requires an explicit once approval")
	}
	approved, err := r.respondApproval(pending.ID, choice, "", request, true, readEpoch)
	if err != nil || approved.Outcome != permissions.ApprovalGranted || !approved.Decision.Ready() || approved.Transition.BeforeRevision != decision.Revision || approved.Transition.AfterRevision == 0 || approved.Transition.Binding != request.Binding {
		return admission, nil, request, nil, errors.New("retained read approval evidence is invalid or stale")
	}
	request.RequireOnceKeys = []string{"disclosure"}
	if admission.Class == "provider_source" {
		request.RequireOnceKeys = []string{"provider_source_disclosure"}
	}
	preparation, err = r.readAdmission.PrepareAdmittedFilesystemRead(ctx, admission, approved.Transition.AfterRevision)
	if err != nil || preparation == nil {
		if preparation != nil {
			err = errors.Join(err, preparation.Close(ctx))
		}
		return admission, nil, request, nil, errors.Join(errors.New("retained read preparation was refused before once consumption"), err)
	}
	admissionOpen = false // preparation now owns retirement of the same record

	preparedRequest, bindErr := buildFilesystemReadRequest(invocation, preparation.Metadata())
	if bindErr != nil || !sameAuthorizationBinding(request, preparedRequest) {
		return admission, preparation, request, nil, errors.New("retained read authorization binding changed during preparation")
	}
	currentDecision := r.evaluator.Evaluate(preparedRequest)
	if !currentDecision.Ready() || currentDecision.Revision != approved.Transition.AfterRevision {
		return admission, preparation, request, nil, errors.New("permission revision changed before retained read consumption")
	}
	err = r.withReadAuthorityGate(readEpoch, func() error {
		return preparation.WithAuthorityCommit(ctx, func() error {
			if ctx.Err() != nil {
				return errors.New("retained read was invalidated before once consumption")
			}
			consumption, consumeErr := r.consumePermissionLocked(preparedRequest, approved.Transition.AfterRevision, readEpoch, true)
			if consumeErr != nil || consumption.BeforeRevision != approved.Transition.AfterRevision || consumption.Binding != preparedRequest.Binding {
				return errors.Join(errors.New("retained read once approval could not be consumed"), consumeErr)
			}
			issued, issueErr := r.issueReadExecutionReferenceLocked(invocation, preparedRequest, approved.Transition, consumption, readEpoch)
			if issueErr != nil {
				return fmt.Errorf("retained read authority could not be issued: %w", issueErr)
			}
			reference = issued
			return nil
		})
	})
	if err != nil {
		return admission, preparation, request, nil, err
	}
	return admission, preparation, preparedRequest, reference, nil
}

// validateFilesystemReadApproval preserves the Stage 3 assertion fixture:
// checked approval and once consumption occur, but authority is immediately
// retired without launching a worker or capturing bytes.
func (r *toolRuntime) validateFilesystemReadApproval(ctx context.Context, invocation tools.Invocation, destination string) (err error) {
	_, preparation, _, reference, err := r.approveFilesystemRead(ctx, invocation, destination)
	if preparation != nil {
		defer func() { err = errors.Join(err, preparation.Close(ctx)) }()
	}
	if err == nil && reference != nil {
		err = r.FinishReadExecution(reference)
	}
	return err
}

// executeFilesystemRead is the Stage 4 runtime seam. It is deliberately not
// connected to generic tool dispatch or registration. Approval and once
// consumption are identical to Stage 3; only the fixed trusted host executor
// may consume the retained preparation and the opaque runtime reference.
func (r *toolRuntime) executeFilesystemRead(ctx context.Context, invocation tools.Invocation, destination string) (*PrivateReadCapture, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	admission, preparation, request, reference, err := r.approveFilesystemRead(ctx, invocation, destination)
	if err != nil {
		if preparation != nil {
			err = errors.Join(err, preparation.Close(ctx))
		}
		return nil, err
	}
	executor, ok := r.readAdmission.(FilesystemReadExecutionHost)
	if !ok || executor == nil {
		_ = r.FinishReadExecution(reference)
		return nil, errors.Join(errors.New("retained read worker execution is unavailable"), preparation.Close(ctx))
	}
	authority := &filesystemReadExecutionAuthority{runtime: r, ref: reference, request: request}
	capture, err := executor.ExecuteAdmittedFilesystemRead(ctx, admission, preparation, reference, authority)
	if err != nil || capture == nil {
		_ = r.FinishReadExecution(reference)
		if capture != nil {
			_ = r.discardPrivateReadCapture(capture)
		}
		return nil, errors.Join(err, errors.New("retained read did not produce a verified private capture"))
	}
	return capture, nil
}

// ExecuteFilesystemRead is the narrow Stage 4 composition seam. It is not
// called by model dispatch and read_file remains unregistered; callers must
// supply the current in-memory conversation identity and host-selected
// destination, and the user must approve the exact read once.
func (c *Chat) ExecuteFilesystemRead(ctx context.Context, invocation tools.Invocation, destination string) (*PrivateReadCapture, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	c.mu.Lock()
	runtime := c.toolRuntime
	current := !c.closed && (invocation.ConversationID == "" || invocation.ConversationID == c.conversationID)
	if current {
		invocation.ConversationID = c.conversationID
	}
	c.mu.Unlock()
	if !current || runtime == nil {
		return nil, errors.New("retained read execution is unavailable for this conversation")
	}
	return runtime.executeFilesystemRead(ctx, invocation, destination)
}

// DiscardPrivateReadCapture explicitly retires a Stage 4 private result. No
// API exposes its bytes to ordinary chat history or tool/provider contracts.
func (c *Chat) DiscardPrivateReadCapture(capture *PrivateReadCapture) error {
	if c == nil || capture == nil {
		return errReadAuthorityInvalid
	}
	c.mu.Lock()
	runtime := c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return errReadAuthorityInvalid
	}
	return runtime.discardPrivateReadCapture(capture)
}

func providerCallFor(invocation tools.Invocation) provider.ToolCall {
	return provider.ToolCall{CallID: invocation.ProviderCallID, ToolID: invocation.ToolID, Arguments: append(json.RawMessage(nil), invocation.Arguments...)}
}
