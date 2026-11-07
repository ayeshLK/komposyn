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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

var errReadAuthorityInvalid = errors.New("read execution authority is invalid or stale")

const maxReadAuthorities = MaxToolInvocationsPerTurn
const maxReadInvocationClaims = 256

// ReadExecutionReference is an opaque, runtime-issued capability. Its fields
// are deliberately private; copying the value does not create another claim.
type ReadExecutionReference struct {
	owner *toolRuntime
	id    string
}

// ReadExecutionValidator is the narrow future host seam. A successful Claim
// authorizes one execution attempt only; this stage issues no target or opens
// no file and the production executable does not construct a reader.
type ReadExecutionValidator interface {
	ClaimReadExecution(*ReadExecutionReference, string, string) error
	RevokeReadExecution(*ReadExecutionReference) error
	FinishReadExecution(*ReadExecutionReference) error
}

type readAuthorityRecord struct {
	conversationID  string
	turnID          string
	callID          string
	authorization   string
	admission       string
	grantRevision   uint64
	epoch           uint64
	claimed         bool
	attemptFinished bool
	captureID       string
	transitions     []readGrantTransition
}

// PrivateReadCapture is an opaque handle to bounded in-memory bytes. It has
// no conversion to a tool/provider result; Stage 5 owns any future disclosure.
type PrivateReadCapture struct {
	owner *toolRuntime
	id    string
}

type privateReadCaptureRecord struct {
	conversationID      string
	turnID              string
	callID              string
	serviceID           string
	admission           string
	authorization       string
	identity            string
	rootGeneration      uint64
	policyRevision      uint64
	coverage            uint64
	destination         string
	outcome             tools.Outcome
	readStarted         bool
	workerJoined        bool
	capabilitiesRetired bool
	bytes               []byte
	expires             time.Time
	timer               *time.Timer
}

const (
	maxPrivateReadCaptures = MaxToolInvocationsPerTurn
	maxPrivateReadBytes    = 1 << 20
	privateReadLifetime    = 5 * time.Minute
)

type readGrantTransition struct {
	before  uint64
	after   uint64
	binding string
	ids     []string
}

// issueReadExecutionReference is called only after the runtime has validated
// the exact checked approval and once-consumption chain. It is intentionally
// private so neither a UI nor a tool can mint a reference.
func (r *toolRuntime) issueReadExecutionReference(invocation tools.Invocation, request permissions.Request, approval, consumption permissions.GrantTransition, expectedEpoch uint64) (*ReadExecutionReference, error) {
	if r == nil {
		return nil, errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return r.issueReadExecutionReferenceLocked(invocation, request, approval, consumption, expectedEpoch)
}

func (r *toolRuntime) issueReadExecutionReferenceLocked(invocation tools.Invocation, request permissions.Request, approval, consumption permissions.GrantTransition, expectedEpoch uint64) (*ReadExecutionReference, error) {
	if r == nil || invocation.ToolID != tools.ID("read_file") || request.ConversationID != invocation.ConversationID || request.CallID != invocation.ProviderCallID || request.Binding == "" || request.AdmissionBinding == "" || approval.Binding != request.Binding || consumption.Binding != request.Binding || approval.AfterRevision == 0 || approval.AfterRevision != consumption.BeforeRevision || consumption.AfterRevision <= consumption.BeforeRevision {
		return nil, errReadAuthorityInvalid
	}
	if len(approval.InsertedIDs) == 0 || len(consumption.ConsumedIDs) == 0 || !validGrantEvidence(approval.InsertedIDs, approval.InsertedRequirementKeys, approval.Inserted, request.RequireOnceKeys) || !validGrantEvidence(consumption.ConsumedIDs, consumption.ConsumedRequirementKeys, consumption.Consumed, request.RequireOnceKeys) || !approval.Decision.Ready() {
		return nil, errReadAuthorityInvalid
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(random[:])
	invocationKey := invocation.ConversationID + "\x00" + invocation.TurnID + "\x00" + invocation.ProviderCallID
	if expectedEpoch != r.readAuthorityEpoch || len(r.readAuthorities) >= maxReadAuthorities || len(r.readInvocationClaims) >= maxReadInvocationClaims || consumption.AfterRevision <= r.lastReadIssuanceRevision {
		return nil, errReadAuthorityInvalid
	}
	if _, exists := r.readInvocationClaims[invocationKey]; exists {
		return nil, errReadAuthorityInvalid
	}
	currentRevision, _ := r.grants.Snapshot(invocation.ConversationID)
	if currentRevision != consumption.AfterRevision || r.readGrantRevision != 0 && r.readGrantRevision != consumption.AfterRevision {
		r.clearReadAuthoritiesLocked()
		r.readAuthorityEpoch++
		return nil, errReadAuthorityInvalid
	}
	r.readGrantRevision = consumption.AfterRevision
	record := readAuthorityRecord{
		conversationID: invocation.ConversationID,
		turnID:         invocation.TurnID,
		callID:         invocation.ProviderCallID,
		authorization:  request.Binding,
		admission:      request.AdmissionBinding,
		grantRevision:  consumption.AfterRevision,
		epoch:          r.readAuthorityEpoch,
		transitions: []readGrantTransition{
			{before: approval.BeforeRevision, after: approval.AfterRevision, binding: approval.Binding, ids: append([]string(nil), approval.InsertedIDs...)},
			{before: consumption.BeforeRevision, after: consumption.AfterRevision, binding: consumption.Binding, ids: append([]string(nil), consumption.ConsumedIDs...)},
		},
	}
	r.readAuthorities[id] = record
	r.readInvocationClaims[invocationKey] = struct{}{}
	r.lastReadIssuanceRevision = consumption.AfterRevision
	return &ReadExecutionReference{owner: r, id: id}, nil
}

// ClaimReadExecution validates runtime ownership, the exact immutable
// admission digest, current permission-store revision, and one-shot state.
// The reference is consumed even if a later sandbox launch fails.
func (r *toolRuntime) ClaimReadExecution(reference *ReadExecutionReference, authorizationBinding, admissionBinding string) error {
	if r == nil || reference == nil || reference.owner != r || reference.id == "" {
		return errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	record, ok := r.readAuthorities[reference.id]
	if !ok || record.claimed || record.epoch != r.readAuthorityEpoch || record.authorization != authorizationBinding || record.admission != admissionBinding || !validReadTransitionChain(record) {
		return errReadAuthorityInvalid
	}
	currentRevision, _ := r.grants.Snapshot(record.conversationID)
	if currentRevision != record.grantRevision || currentRevision != r.readGrantRevision {
		r.clearReadAuthoritiesLocked()
		r.readAuthorityEpoch++
		return errReadAuthorityInvalid
	}
	// Claim marks the operation before returning. The same reference cannot be
	// retried after launch failure or uncertainty, and the record remains
	// addressable by its owned identity for revocation until Finish.
	record.claimed = true
	r.readAuthorities[reference.id] = record
	return nil
}

// withClaimedReadCurrent serializes the bounded host release/staging callback
// against runtime grant changes. It does not wait for UI, worker I/O, or join.
func (r *toolRuntime) withClaimedReadCurrent(reference *ReadExecutionReference, authorizationBinding, admissionBinding string, operation func() error) error {
	if r == nil || reference == nil || reference.owner != r || reference.id == "" || operation == nil {
		return errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	record, ok := r.readAuthorities[reference.id]
	if !ok || !record.claimed || record.attemptFinished || record.captureID != "" || record.epoch != r.readAuthorityEpoch || record.authorization != authorizationBinding || record.admission != admissionBinding || !validReadTransitionChain(record) {
		return errReadAuthorityInvalid
	}
	currentRevision, _ := r.grants.Snapshot(record.conversationID)
	if currentRevision != record.grantRevision || currentRevision != r.readGrantRevision {
		r.invalidateReadAuthorityLocked()
		return errReadAuthorityInvalid
	}
	return operation()
}

func (r *toolRuntime) stagePrivateReadCaptureLocked(reference *ReadExecutionReference, admission FilesystemReadAdmission, result FilesystemReadExecutionResult) (*PrivateReadCapture, error) {
	if r == nil || reference == nil || reference.owner != r || reference.id == "" || result.Outcome != tools.OutcomeSucceeded || !result.WorkerJoined || !result.CapabilitiesRetired || !result.ReadStarted || len(result.Bytes) > 64<<10 {
		return nil, errReadAuthorityInvalid
	}
	record, ok := r.readAuthorities[reference.id]
	if !ok || !record.claimed || record.attemptFinished || record.captureID != "" || record.conversationID != admission.ConversationID || record.turnID != admission.TurnID || record.callID != admission.CallID || record.admission != admission.AdmissionBinding || record.authorization != result.AuthorizationBinding || result.ServiceID != admission.ServiceID || result.AdmissionBinding != admission.AdmissionBinding || result.Identity != admission.Identity || result.RootGeneration != admission.RootGeneration || result.PolicyRevision != admission.PolicyRevision || result.CoverageRevision != admission.CoverageRevision || result.Destination != admission.Destination {
		return nil, errReadAuthorityInvalid
	}
	r.expirePrivateReadCapturesLocked(time.Now())
	if len(r.privateReadCaptures) >= maxPrivateReadCaptures || privateReadCaptureBytes(r.privateReadCaptures)+len(result.Bytes) > maxPrivateReadBytes {
		return nil, errors.New("private read capture budget exhausted")
	}
	id, err := newReadAuthorityID()
	if err != nil {
		return nil, err
	}
	capture := privateReadCaptureRecord{
		conversationID: admission.ConversationID, turnID: admission.TurnID, callID: admission.CallID,
		serviceID: admission.ServiceID, admission: admission.AdmissionBinding,
		authorization: result.AuthorizationBinding, identity: admission.Identity,
		rootGeneration: admission.RootGeneration, policyRevision: admission.PolicyRevision,
		coverage: admission.CoverageRevision, destination: admission.Destination,
		outcome: result.Outcome, readStarted: result.ReadStarted,
		workerJoined: result.WorkerJoined, capabilitiesRetired: result.CapabilitiesRetired,
		bytes: append([]byte(nil), result.Bytes...), expires: time.Now().Add(privateReadLifetime),
	}
	capture.timer = time.AfterFunc(privateReadLifetime, func() { r.expirePrivateReadCapture(id) })
	r.privateReadCaptures[id] = capture
	record.captureID = id
	record.attemptFinished = true
	r.readAuthorities[reference.id] = record
	return &PrivateReadCapture{owner: r, id: id}, nil
}

func privateReadCaptureBytes(records map[string]privateReadCaptureRecord) int {
	total := 0
	for _, record := range records {
		total += len(record.bytes)
	}
	return total
}

func newReadAuthorityID() (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func (r *toolRuntime) expirePrivateReadCapturesLocked(now time.Time) {
	for id, record := range r.privateReadCaptures {
		if !now.Before(record.expires) {
			if record.timer != nil {
				record.timer.Stop()
			}
			clear(record.bytes)
			delete(r.privateReadCaptures, id)
			for authorityID, authority := range r.readAuthorities {
				if authority.captureID == id {
					delete(r.readAuthorities, authorityID)
				}
			}
		}
	}
}

func (r *toolRuntime) expirePrivateReadCapture(id string) {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	record, ok := r.privateReadCaptures[id]
	if !ok || time.Now().Before(record.expires) {
		return
	}
	clear(record.bytes)
	delete(r.privateReadCaptures, id)
	for authorityID, authority := range r.readAuthorities {
		if authority.captureID == id {
			delete(r.readAuthorities, authorityID)
		}
	}
}

// discardPrivateReadCapture scrubs the private payload and retires its
// continuing authority provenance. It is currently used by Stage 4 tests and
// failure/lifecycle paths; Stage 5 will own publication eligibility.
func (r *toolRuntime) discardPrivateReadCapture(capture *PrivateReadCapture) error {
	if r == nil || capture == nil || capture.owner != r || capture.id == "" {
		return errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	record, ok := r.privateReadCaptures[capture.id]
	if !ok {
		return errReadAuthorityInvalid
	}
	clear(record.bytes)
	if record.timer != nil {
		record.timer.Stop()
	}
	delete(r.privateReadCaptures, capture.id)
	for id, authority := range r.readAuthorities {
		if authority.captureID == capture.id {
			delete(r.readAuthorities, id)
		}
	}
	return nil
}

func (r *toolRuntime) RevokeReadExecution(reference *ReadExecutionReference) error {
	if r == nil || reference == nil || reference.owner != r || reference.id == "" {
		return errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	record, ok := r.readAuthorities[reference.id]
	if !ok {
		return errReadAuthorityInvalid
	}
	r.discardAuthorityCaptureLocked(record)
	delete(r.readAuthorities, reference.id)
	return nil
}

func (r *toolRuntime) FinishReadExecution(reference *ReadExecutionReference) error {
	if r == nil || reference == nil || reference.owner != r || reference.id == "" {
		return errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return r.finishReadExecutionLocked(reference)
}

func (r *toolRuntime) finishReadExecutionLocked(reference *ReadExecutionReference) error {
	if r == nil || reference == nil || reference.owner != r || reference.id == "" {
		return errReadAuthorityInvalid
	}
	if _, ok := r.readAuthorities[reference.id]; !ok {
		return errReadAuthorityInvalid
	}
	record := r.readAuthorities[reference.id]
	if record.captureID != "" {
		record.attemptFinished = true
		r.readAuthorities[reference.id] = record
		return nil
	}
	delete(r.readAuthorities, reference.id)
	return nil
}

// ClaimReadExecution exposes only the future host validator seam on Chat.
// There is no public constructor for a reference, and ordinary UI contracts
// do not carry one.
func (c *Chat) ClaimReadExecution(reference *ReadExecutionReference, authorizationBinding, admissionBinding string) error {
	c.mu.Lock()
	runtime := c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return errReadAuthorityInvalid
	}
	return runtime.ClaimReadExecution(reference, authorizationBinding, admissionBinding)
}

// RevokeReadExecution addresses consumed authority by its runtime-owned
// operation reference, independent of once-grant IDs that are already gone.
// Terminating an already released worker remains the host service's job.
func (c *Chat) RevokeReadExecution(reference *ReadExecutionReference) error {
	c.mu.Lock()
	runtime := c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return errReadAuthorityInvalid
	}
	return runtime.RevokeReadExecution(reference)
}

// FinishReadExecution retires an operation reference after the host has
// completed or truthfully classified its attempt.
func (c *Chat) FinishReadExecution(reference *ReadExecutionReference) error {
	c.mu.Lock()
	runtime := c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return errReadAuthorityInvalid
	}
	return runtime.FinishReadExecution(reference)
}

func validReadTransitionChain(record readAuthorityRecord) bool {
	if len(record.transitions) < 2 || len(record.transitions) > maxReadAuthorities*2 {
		return false
	}
	for i, transition := range record.transitions {
		if transition.binding == "" || transition.after != transition.before+1 || len(transition.ids) == 0 {
			return false
		}
		if i > 0 && record.transitions[i-1].after != transition.before {
			return false
		}
	}
	return record.transitions[len(record.transitions)-1].after == record.grantRevision
}

// advanceReadAuthorityRevision accepts only an exact runtime-owned permission
// transaction. Any gap retires all continuing authority instead of refreshing
// from an arbitrary store snapshot.
func (r *toolRuntime) advanceReadAuthorityRevision(transition permissions.GrantTransition, request permissions.Request, approval bool) bool {
	if r == nil {
		return false
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return r.advanceReadAuthorityRevisionLocked(transition, request, approval)
}

func (r *toolRuntime) advanceReadAuthorityRevisionLocked(transition permissions.GrantTransition, request permissions.Request, approval bool) bool {
	if r == nil || transition.AfterRevision != transition.BeforeRevision+1 || transition.Binding == "" || transition.Binding != request.Binding || len(transition.InsertedIDs) != len(transition.InsertedRequirementKeys) || len(transition.ConsumedIDs) != len(transition.ConsumedRequirementKeys) {
		return false
	}
	if approval {
		if len(transition.InsertedIDs) == 0 || !validGrantEvidence(transition.InsertedIDs, transition.InsertedRequirementKeys, transition.Inserted, request.RequireOnceKeys) {
			return false
		}
	} else if len(transition.ConsumedIDs) == 0 || !validGrantEvidence(transition.ConsumedIDs, transition.ConsumedRequirementKeys, transition.Consumed, request.RequireOnceKeys) {
		return false
	}
	for _, id := range transition.InsertedIDs {
		if id == "" {
			return false
		}
	}
	for _, id := range transition.ConsumedIDs {
		if id == "" {
			return false
		}
	}
	if len(r.readAuthorities) == 0 {
		r.readGrantRevision = transition.AfterRevision
		return true
	}
	if transition.BeforeRevision != r.readGrantRevision || transition.AfterRevision != transition.BeforeRevision+1 {
		r.clearReadAuthoritiesLocked()
		r.readAuthorityEpoch++
		r.readGrantRevision = transition.AfterRevision
		return false
	}
	r.readGrantRevision = transition.AfterRevision
	for id, record := range r.readAuthorities {
		if len(record.transitions) >= maxReadAuthorities*2 {
			r.clearReadAuthoritiesLocked()
			r.readAuthorityEpoch++
			return false
		}
		record.grantRevision = transition.AfterRevision
		ids := transition.InsertedIDs
		if !approval {
			ids = transition.ConsumedIDs
		}
		record.transitions = append(record.transitions, readGrantTransition{before: transition.BeforeRevision, after: transition.AfterRevision, binding: transition.Binding, ids: append([]string(nil), ids...)})
		r.readAuthorities[id] = record
	}
	return true
}

func (r *toolRuntime) beginApproval(request permissions.Request, expectedEpoch uint64) (permissions.PendingApproval, permissions.Decision, error) {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	if expectedEpoch != r.readAuthorityEpoch {
		return permissions.PendingApproval{}, permissions.Decision{}, errReadAuthorityInvalid
	}
	return r.coordinator.Begin(request)
}

func (r *toolRuntime) respondApproval(id string, choice permissions.ApprovalChoice, redirect string, request permissions.Request, readCall bool, expectedEpoch uint64) (permissions.ApprovalResponse, error) {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	if expectedEpoch != r.readAuthorityEpoch {
		return permissions.ApprovalResponse{}, errReadAuthorityInvalid
	}
	response, err := r.coordinator.Respond(id, choice, redirect)
	if err != nil {
		return permissions.ApprovalResponse{}, err
	}
	if readCall && response.Outcome == permissions.ApprovalGranted {
		if !r.advanceReadAuthorityRevisionLocked(response.Transition, request, true) {
			return permissions.ApprovalResponse{}, errReadAuthorityInvalid
		}
	} else if len(r.readAuthorities) > 0 {
		r.invalidateReadAuthorityLocked()
	}
	return response, nil
}

func (r *toolRuntime) consumePermission(request permissions.Request, expectedRevision, expectedEpoch uint64, readCall bool) (permissions.GrantTransition, error) {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return r.consumePermissionLocked(request, expectedRevision, expectedEpoch, readCall)
}

func (r *toolRuntime) consumePermissionLocked(request permissions.Request, expectedRevision, expectedEpoch uint64, readCall bool) (permissions.GrantTransition, error) {
	if expectedEpoch != r.readAuthorityEpoch {
		return permissions.GrantTransition{}, errReadAuthorityInvalid
	}
	transition, err := r.grants.ConsumeAuthorizedAtRevision(r.evaluator, request, expectedRevision)
	if err != nil {
		return permissions.GrantTransition{}, err
	}
	if readCall {
		if !r.advanceReadAuthorityRevisionLocked(transition, request, false) {
			return permissions.GrantTransition{}, errReadAuthorityInvalid
		}
	} else if len(r.readAuthorities) > 0 {
		r.invalidateReadAuthorityLocked()
	}
	return transition, nil
}

func (r *toolRuntime) withReadAuthorityGate(expectedEpoch uint64, operation func() error) error {
	if r == nil || operation == nil {
		return errReadAuthorityInvalid
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	if expectedEpoch != r.readAuthorityEpoch {
		return errReadAuthorityInvalid
	}
	return operation()
}

func (r *toolRuntime) checkReadAuthorityRevision(revision uint64) bool {
	if r == nil {
		return false
	}
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	if len(r.readAuthorities) == 0 {
		return true
	}
	if revision != r.readGrantRevision {
		r.clearReadAuthoritiesLocked()
		r.readAuthorityEpoch++
		r.readGrantRevision = revision
		return false
	}
	return true
}

func (r *toolRuntime) invalidateReadAuthority() {
	if r == nil {
		return
	}
	r.readAuthorityMu.Lock()
	r.invalidateReadAuthorityLocked()
	r.readAuthorityMu.Unlock()
}

func (r *toolRuntime) invalidateReadAuthorityLocked() {
	r.clearReadAuthoritiesLocked()
	r.readAuthorityEpoch++
	r.readGrantRevision = 0
}

func (r *toolRuntime) clearReadAuthoritiesLocked() {
	for _, authority := range r.readAuthorities {
		r.discardAuthorityCaptureLocked(authority)
	}
	clear(r.readAuthorities)
	for id, capture := range r.privateReadCaptures {
		clear(capture.bytes)
		if capture.timer != nil {
			capture.timer.Stop()
		}
		delete(r.privateReadCaptures, id)
	}
}

func (r *toolRuntime) discardAuthorityCaptureLocked(record readAuthorityRecord) {
	if record.captureID == "" {
		return
	}
	if capture, ok := r.privateReadCaptures[record.captureID]; ok {
		clear(capture.bytes)
		if capture.timer != nil {
			capture.timer.Stop()
		}
		delete(r.privateReadCaptures, record.captureID)
	}
}

func (r *toolRuntime) revokeConversationGrant(conversationID, grantID string) error {
	r.readAuthorityMu.Lock()
	if err := r.grants.Revoke(conversationID, grantID); err != nil {
		r.readAuthorityMu.Unlock()
		return err
	}
	r.invalidateReadAuthorityLocked()
	r.coordinator.CancelConversation(conversationID)
	r.readAuthorityMu.Unlock()
	cleanupErr := r.invalidateHostReadExecutions(conversationID)
	r.invalidateConversationUI(conversationID)
	return cleanupErr
}

func (r *toolRuntime) clearConversationGrants(conversationID string) (int, error) {
	r.readAuthorityMu.Lock()
	removed := r.grants.ClearCount(conversationID)
	r.invalidateReadAuthorityLocked()
	r.coordinator.CancelConversation(conversationID)
	r.readAuthorityMu.Unlock()
	cleanupErr := r.invalidateHostReadExecutions(conversationID)
	r.invalidateConversationUI(conversationID)
	return removed, cleanupErr
}

// clearConversationGrantsLocked is used while Chat owns its mutex; it omits
// UI callbacks, which notifyFilesystemRoot performs after releasing that lock.
func (r *toolRuntime) clearConversationGrantsLocked(conversationID string) {
	r.readAuthorityMu.Lock()
	r.grants.ClearCount(conversationID)
	r.invalidateReadAuthorityLocked()
	r.coordinator.CancelConversation(conversationID)
	r.readAuthorityMu.Unlock()
}

func (r *toolRuntime) invalidateConversation(conversationID string) {
	r.readAuthorityMu.Lock()
	r.invalidateReadAuthorityLocked()
	r.coordinator.CancelConversation(conversationID)
	r.readAuthorityMu.Unlock()
	_ = r.invalidateHostReadExecutions(conversationID)
	r.invalidateConversationUI(conversationID)
}

func (r *toolRuntime) invalidateHostReadExecutions(conversationID string) error {
	if r == nil || r.readExecutionInvalidator == nil || conversationID == "" {
		return nil
	}
	return r.readExecutionInvalidator.InvalidateFilesystemReadExecutions(conversationID)
}

func (r *toolRuntime) invalidateConversationUI(conversationID string) {
	if invalidator, ok := r.approval.(ToolApprovalInvalidator); ok {
		invalidator.InvalidateConversation(conversationID)
	}
	if invalidator, ok := r.clarification.(ToolClarificationInvalidator); ok {
		invalidator.InvalidateConversation(conversationID)
	}
}

// retireReadAuthorities ends synthetic operation authority when the active
// tool-loop result has been resolved. Later transcript continuation is owned
// by a subsequent stage and cannot reuse these execution references.
func (r *toolRuntime) retireReadAuthorities(conversationID string) {
	if r == nil || conversationID == "" {
		return
	}
	r.readAuthorityMu.Lock()
	for id, record := range r.readAuthorities {
		if record.conversationID == conversationID {
			r.discardAuthorityCaptureLocked(record)
			delete(r.readAuthorities, id)
		}
	}
	for id, capture := range r.privateReadCaptures {
		if capture.conversationID == conversationID {
			clear(capture.bytes)
			if capture.timer != nil {
				capture.timer.Stop()
			}
			delete(r.privateReadCaptures, id)
		}
	}
	r.readAuthorityMu.Unlock()
}

func (r *toolRuntime) readAuthorityCount() int {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return len(r.readAuthorities)
}

func (r *toolRuntime) readAuthorityEpochSnapshot() uint64 {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return r.readAuthorityEpoch
}

func (r *toolRuntime) readAuthorityEpochCurrent(epoch uint64) bool {
	r.readAuthorityMu.Lock()
	defer r.readAuthorityMu.Unlock()
	return epoch == r.readAuthorityEpoch
}

func validGrantEvidence(ids, keys []string, evidence []permissions.GrantEvidence, required []string) bool {
	if len(ids) == 0 || len(ids) != len(evidence) || len(keys) != len(ids) {
		return false
	}
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || idSet[id] {
			return false
		}
		idSet[id] = true
	}
	keySet := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" || keySet[key] {
			return false
		}
		keySet[key] = true
	}
	evidenceKeys := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		if !idSet[item.ID] || !keySet[item.RequirementKey] || evidenceKeys[item.RequirementKey] {
			return false
		}
		delete(idSet, item.ID)
		evidenceKeys[item.RequirementKey] = true
	}
	if len(idSet) != 0 {
		return false
	}
	for _, key := range required {
		if !keySet[key] {
			return false
		}
	}
	return len(required) > 0
}

var _ ReadExecutionValidator = (*toolRuntime)(nil)
var _ ReadExecutionValidator = (*Chat)(nil)
