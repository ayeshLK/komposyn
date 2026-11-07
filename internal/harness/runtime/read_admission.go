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
	Root             string
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

// FilesystemReadAdmissionHost is the narrow retained-object seam for Stage 3.
// Implementations derive the selected root and owner coverage themselves.
type FilesystemReadAdmissionHost interface {
	AdmitFilesystemRead(context.Context, tools.Invocation, string) (FilesystemReadAdmission, error)
	PrepareAdmittedFilesystemRead(context.Context, FilesystemReadAdmission, uint64) (FilesystemReadPreparation, error)
	CloseFilesystemReadAdmission(context.Context, FilesystemReadAdmission) error
}

type FilesystemReadAdmissionInvalidator interface {
	SetFilesystemReadInvalidator(func(string))
}

func filesystemReadActionKey() string {
	digest := sha256.Sum256([]byte("komposyn-filesystem-read-once-v1"))
	return hex.EncodeToString(digest[:])
}

func buildFilesystemReadRequest(invocation tools.Invocation, admission FilesystemReadAdmission) (permissions.Request, error) {
	if invocation.ToolID != tools.ID("read_file") || invocation.ConversationID == "" || invocation.ProviderCallID == "" || admission.Reference == nil || admission.AdmissionBinding == "" || admission.Target == "" || admission.Destination == "" {
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

// validateFilesystemReadApproval exercises the real Stage 2 transaction and
// retains the same admitted object through preparation. This stage deliberately
// retires the one-shot authority without dispatching a worker or reading bytes.
func (r *toolRuntime) validateFilesystemReadApproval(ctx context.Context, invocation tools.Invocation, destination string) (err error) {
	if ctx == nil || r == nil || r.readAdmission == nil || r.approval == nil || !validToolCall(providerCallFor(invocation)) {
		return errors.New("retained read approval is unavailable")
	}
	readEpoch := r.readAuthorityEpochSnapshot()
	admission, err := r.readAdmission.AdmitFilesystemRead(ctx, cloneInvocation(invocation), destination)
	if err != nil {
		return err
	}
	admissionOpen := true
	defer func() {
		if admissionOpen {
			err = errors.Join(err, r.readAdmission.CloseFilesystemReadAdmission(context.Background(), admission))
		}
	}()
	request, err := buildFilesystemReadRequest(invocation, admission)
	if err != nil {
		return err
	}
	decision := r.evaluator.Evaluate(request)
	if len(decision.Denied) != 0 || len(decision.Missing) == 0 || !r.checkReadAuthorityRevision(decision.Revision) {
		return errors.New("retained read is denied or lacks target-specific once approval")
	}
	pending, beginDecision, err := r.beginApproval(request, readEpoch)
	if err != nil || pending.ID == "" || beginDecision.Revision != decision.Revision {
		r.coordinator.CancelConversation(invocation.ConversationID)
		return errors.New("retained read approval could not be opened")
	}
	choice, redirect, reviewErr := r.approval.Review(ctx, pending)
	if reviewErr != nil || choice != permissions.ApprovalOnce || redirect != "" || ctx.Err() != nil || !r.readAuthorityEpochCurrent(readEpoch) {
		r.coordinator.CancelConversation(invocation.ConversationID)
		return errors.New("retained read requires an explicit once approval")
	}
	approved, err := r.respondApproval(pending.ID, choice, "", request, true, readEpoch)
	if err != nil || approved.Outcome != permissions.ApprovalGranted || !approved.Decision.Ready() || approved.Transition.BeforeRevision != decision.Revision || approved.Transition.AfterRevision == 0 || approved.Transition.Binding != request.Binding {
		return errors.New("retained read approval evidence is invalid or stale")
	}
	request.RequireOnceKeys = []string{"disclosure"}
	if admission.Class == "provider_source" {
		request.RequireOnceKeys = []string{"provider_source_disclosure"}
	}
	preparation, err := r.readAdmission.PrepareAdmittedFilesystemRead(ctx, admission, approved.Transition.AfterRevision)
	if err != nil || preparation == nil {
		if preparation != nil {
			err = errors.Join(err, preparation.Close(context.Background()))
		}
		return errors.Join(errors.New("retained read preparation was refused before once consumption"), err)
	}
	admissionOpen = false // preparation now owns retirement of the same record
	defer func() { err = errors.Join(err, preparation.Close(context.Background())) }()

	preparedRequest, bindErr := buildFilesystemReadRequest(invocation, preparation.Metadata())
	if bindErr != nil || !sameAuthorizationBinding(request, preparedRequest) {
		return errors.New("retained read authorization binding changed during preparation")
	}
	currentDecision := r.evaluator.Evaluate(preparedRequest)
	if !currentDecision.Ready() || currentDecision.Revision != approved.Transition.AfterRevision {
		return errors.New("permission revision changed before retained read consumption")
	}
	return r.withReadAuthorityGate(readEpoch, func() error {
		return preparation.WithAuthorityCommit(ctx, func() error {
			if ctx.Err() != nil {
				return errors.New("retained read was invalidated before once consumption")
			}
			consumption, consumeErr := r.consumePermissionLocked(preparedRequest, approved.Transition.AfterRevision, readEpoch, true)
			if consumeErr != nil || consumption.BeforeRevision != approved.Transition.AfterRevision || consumption.Binding != preparedRequest.Binding {
				return errors.Join(errors.New("retained read once approval could not be consumed"), consumeErr)
			}
			reference, issueErr := r.issueReadExecutionReferenceLocked(invocation, preparedRequest, approved.Transition, consumption, readEpoch)
			if issueErr != nil {
				return fmt.Errorf("retained read authority could not be issued: %w", issueErr)
			}
			return r.finishReadExecutionLocked(reference)
		})
	})
}

func providerCallFor(invocation tools.Invocation) provider.ToolCall {
	return provider.ToolCall{CallID: invocation.ProviderCallID, ToolID: invocation.ToolID, Arguments: append(json.RawMessage(nil), invocation.Arguments...)}
}
