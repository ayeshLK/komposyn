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
	"sync"

	"github.com/ayeshLK/komposyn/internal/harness/model"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
)

type providerDispatchResult struct {
	response provider.Result
	err      error
}

type providerDispatchRequest struct {
	ctx     context.Context
	plug    provider.Provider
	request provider.ChatRequest
	delta   func(string)
}

// reserveProviderDispatch starts a single-use, already-ready receiver before
// the publication gate is entered. The worker cannot call the provider until
// Release, which the caller performs only after its transcript commit.
type providerDispatchReservation struct {
	requests  chan providerDispatchRequest
	release   chan struct{}
	result    chan providerDispatchResult
	once      sync.Once
	abortOnce sync.Once
}

func reserveProviderDispatch() *providerDispatchReservation {
	r := &providerDispatchReservation{
		requests: make(chan providerDispatchRequest, 1),
		release:  make(chan struct{}),
		result:   make(chan providerDispatchResult, 1),
	}
	go func() {
		request, ok := <-r.requests
		if !ok {
			return
		}
		<-r.release
		if request.ctx.Err() != nil {
			r.result <- providerDispatchResult{err: request.ctx.Err()}
			return
		}
		response, err := request.plug.StreamChat(request.ctx, request.request, request.delta)
		r.result <- providerDispatchResult{response: response, err: err}
	}()
	return r
}

func (r *providerDispatchReservation) Accept(request providerDispatchRequest) bool {
	if r == nil {
		return false
	}
	select {
	case r.requests <- request:
		return true
	default:
		return false
	}
}

func (r *providerDispatchReservation) Release() {
	if r != nil {
		r.once.Do(func() { close(r.release) })
	}
}

func (r *providerDispatchReservation) Abort() {
	if r == nil {
		return
	}
	r.abortOnce.Do(func() { close(r.requests) })
	r.Release()
}

func (r *providerDispatchReservation) Wait(context.Context) (provider.Result, error) {
	if r == nil {
		return provider.Result{}, context.Canceled
	}
	result := <-r.result
	return result.response, result.err
}

// commitAndDispatch makes the transcript visible and accepts its immutable
// provider request while Chat and runtime authorization state are both held.
// The dispatcher is already reserved; its worker remains gated until Release.
func (c *Chat) commitAndDispatch(ctx context.Context, reservation *providerDispatchReservation, generation uint64, selection model.Model, conversationID string, plug provider.Provider, request provider.ChatRequest, messages []provider.Message, captures map[string]*PrivateReadCapture, captureBudgets map[string]int, sensitiveCandidate bool, delta func(string)) ([]provider.Message, uint64, bool, error) {
	if reservation == nil {
		return nil, 0, false, errors.New("provider dispatcher reservation is unavailable")
	}
	if !sensitiveCandidate {
		return c.commitAndDispatchUnderGate(ctx, reservation, generation, selection, conversationID, plug, request, messages, captures, captureBudgets, delta, false)
	}
	c.mu.Lock()
	root, rootGeneration := c.filesystemRoot, c.filesystemRootGen
	runtime := c.toolRuntime
	c.mu.Unlock()
	if runtime == nil || runtime.readPublicationGate == nil {
		reservation.Abort()
		return nil, 0, false, errors.New("host owner publication gate is unavailable")
	}
	var committed []provider.Message
	var epoch uint64
	var sensitive bool
	policyRevision, coverageRevision, ok := runtime.readPublicationRevisions(conversationID, captures)
	if !ok {
		reservation.Abort()
		return nil, 0, false, errors.New("read continuation generations are unavailable")
	}
	err := runtime.readPublicationGate.WithFilesystemReadPublicationCurrent(ctx, conversationID, root, rootGeneration, policyRevision, coverageRevision, func() error {
		var commitErr error
		committed, epoch, sensitive, commitErr = c.commitAndDispatchUnderGate(ctx, reservation, generation, selection, conversationID, plug, request, messages, captures, captureBudgets, delta, true)
		return commitErr
	})
	if err != nil {
		reservation.Abort()
		return nil, 0, false, err
	}
	return committed, epoch, sensitive, nil
}

func (c *Chat) commitAndDispatchUnderGate(ctx context.Context, reservation *providerDispatchReservation, generation uint64, selection model.Model, conversationID string, plug provider.Provider, request provider.ChatRequest, messages []provider.Message, captures map[string]*PrivateReadCapture, captureBudgets map[string]int, delta func(string), sensitiveCandidate bool) ([]provider.Message, uint64, bool, error) {
	if reservation == nil {
		return nil, 0, false, errors.New("provider dispatcher reservation is unavailable")
	}
	committed := cloneMessages(messages)
	c.mu.Lock()
	if c.closed || c.generation != generation || !c.active || c.conversationID != conversationID || c.selected == nil || *c.selected != selection || (c.credentialBusy && c.credentialBusyProfile == selection.ProfileID) || c.filesystemRootAck < c.filesystemRootGen || ctx.Err() != nil {
		c.mu.Unlock()
		reservation.Abort()
		return nil, 0, false, context.Canceled
	}
	runtime := c.toolRuntime
	destination := readDestination(selection)
	var epoch uint64
	sensitive := false
	if sensitiveCandidate && runtime == nil {
		c.mu.Unlock()
		reservation.Abort()
		return nil, 0, false, errors.New("read continuation runtime is unavailable")
	}
	if runtime != nil {
		runtime.readAuthorityMu.Lock()
		epoch = runtime.readAuthorityEpoch
		continuation, hasContinuation := runtime.readContinuations[conversationID]
		if hasContinuation && !continuationMatchesRoot(continuation, c.filesystemRootGen) {
			hasContinuation = false
		}
		if sensitiveCandidate && len(captures) == 0 && !c.historySensitive {
			runtime.readAuthorityMu.Unlock()
			c.mu.Unlock()
			reservation.Abort()
			return nil, 0, false, errors.New("sensitive draft was retired before provider handoff")
		}
		if c.historySensitive && (!hasContinuation || !continuationMatchesRoot(continuation, c.filesystemRootGen) || continuation.destination != destination || continuation.epoch != epoch || continuation.grantRevision != runtime.readGrantRevision || c.historyReadEpoch != epoch || c.historyDestination != destination) {
			c.retireReadHistoryLocked()
			runtime.invalidateReadAuthorityLocked()
			runtime.readAuthorityMu.Unlock()
			c.mu.Unlock()
			reservation.Abort()
			return nil, 0, false, errors.New("sensitive conversation continuation was retired")
		}
		for _, capture := range captures {
			if capture == nil || capture.owner != runtime {
				continue
			}
			item, ok := runtime.privateReadCaptures[capture.id]
			if !ok || item.rootGeneration != c.filesystemRootGen || c.filesystemRootAck < c.filesystemRootGen || item.destination != destination {
				for _, held := range captures {
					if held != nil {
						runtime.discardCaptureHandleLocked(held)
					}
				}
				c.markReadCaptureWithheldLocked(captures)
				runtime.readAuthorityMu.Unlock()
				c.mu.Unlock()
				reservation.Abort()
				return nil, 0, false, errors.New("captured read became stale before provider commitment")
			}
		}
		var ok bool
		committed, ok = runtime.publishReadCapturesLocked(conversationID, destination, request.Messages, committed, captures, captureBudgets)
		if !ok {
			for _, held := range captures {
				if held != nil {
					runtime.discardCaptureHandleLocked(held)
				}
			}
			c.markReadCaptureWithheldLocked(captures)
			runtime.readAuthorityMu.Unlock()
			c.mu.Unlock()
			reservation.Abort()
			return nil, 0, false, errors.New("captured read could not be committed")
		}
		sensitive = hasContinuation || len(captures) > 0
		if !reservation.Accept(providerDispatchRequest{ctx: ctx, plug: plug, request: request, delta: delta}) {
			runtime.invalidateReadAuthorityLocked()
			c.history, c.historySensitive, c.historyDestination = nil, false, ""
			runtime.readAuthorityMu.Unlock()
			c.mu.Unlock()
			reservation.Abort()
			return nil, 0, false, errors.New("provider dispatcher rejected the reserved request")
		}
		if sensitive {
			c.history = cloneMessages(committed)
			c.historySensitive, c.historyReadEpoch, c.historyDestination = true, epoch, destination
			for index := range c.activeToolActivities {
				if _, disclosed := captures[c.activeToolActivities[index].CallID]; disclosed {
					c.activeToolActivities[index].Message = "Read completed and was disclosed at provider handoff; replay is limited to this conversation context."
				}
			}
		}
		runtime.readAuthorityMu.Unlock()
	} else {
		if !reservation.Accept(providerDispatchRequest{ctx: ctx, plug: plug, request: request, delta: delta}) {
			c.mu.Unlock()
			reservation.Abort()
			return nil, 0, false, errors.New("provider dispatcher rejected the reserved request")
		}
	}
	c.mu.Unlock()
	return committed, epoch, sensitive, nil
}

func (c *Chat) readContinuationCurrent(conversationID string, selection model.Model, epoch uint64) bool {
	c.mu.Lock()
	if c.closed || c.conversationID != conversationID || c.selected == nil || *c.selected != selection || (c.credentialBusy && c.credentialBusyProfile == selection.ProfileID) || !c.historySensitive || c.historyDestination != readDestination(selection) || c.historyReadEpoch != epoch || c.filesystemRootAck < c.filesystemRootGen {
		c.mu.Unlock()
		return false
	}
	runtime := c.toolRuntime
	root, rootGeneration := c.filesystemRoot, c.filesystemRootGen
	c.mu.Unlock()
	if runtime == nil || runtime.readPublicationGate == nil {
		return false
	}
	policy, coverage, ok := runtime.readPublicationRevisions(conversationID, nil)
	if !ok {
		return false
	}
	current := false
	err := runtime.readPublicationGate.WithFilesystemReadPublicationCurrent(context.Background(), conversationID, root, rootGeneration, policy, coverage, func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed || c.conversationID != conversationID || c.selected == nil || *c.selected != selection || (c.credentialBusy && c.credentialBusyProfile == selection.ProfileID) || !c.historySensitive || c.historyDestination != readDestination(selection) || c.historyReadEpoch != epoch || c.filesystemRootAck < c.filesystemRootGen {
			return nil
		}
		current = c.readContinuationCurrentLocked(conversationID, selection, epoch)
		return nil
	})
	return err == nil && current
}

func (c *Chat) readContinuationCurrentLocked(conversationID string, selection model.Model, epoch uint64) bool {
	if c.closed || c.conversationID != conversationID || c.selected == nil || *c.selected != selection || (c.credentialBusy && c.credentialBusyProfile == selection.ProfileID) || !c.historySensitive || c.historyDestination != readDestination(selection) || c.historyReadEpoch != epoch || c.filesystemRootAck < c.filesystemRootGen {
		return false
	}
	runtime := c.toolRuntime
	if runtime == nil {
		return false
	}
	runtime.readAuthorityMu.Lock()
	defer runtime.readAuthorityMu.Unlock()
	return c.readContinuationStateCurrentLocked(conversationID, selection, epoch, runtime)
}

func (c *Chat) readContinuationStateCurrentLocked(conversationID string, selection model.Model, epoch uint64, runtime *toolRuntime) bool {
	if runtime == nil || !c.historySensitive || c.historyDestination != readDestination(selection) || c.historyReadEpoch != epoch || c.filesystemRootAck < c.filesystemRootGen {
		return false
	}
	continuation, ok := runtime.readContinuations[conversationID]
	currentRevision, _ := runtime.grants.Snapshot(conversationID)
	return ok && continuation.destination == readDestination(selection) && continuationMatchesRoot(continuation, c.filesystemRootGen) && continuation.epoch == epoch && epoch == runtime.readAuthorityEpoch && continuation.grantRevision == currentRevision && runtime.readGrantRevision == currentRevision && validReadContinuationRecord(continuation, currentRevision)
}

func continuationMatchesRoot(record readContinuationRecord, generation uint64) bool {
	if len(record.rootGenerations) == 0 {
		return false
	}
	for _, observed := range record.rootGenerations {
		if observed != generation {
			return false
		}
	}
	return true
}

func (c *Chat) markReadCaptureWithheldLocked(captures map[string]*PrivateReadCapture) {
	for index := range c.activeToolActivities {
		if _, found := captures[c.activeToolActivities[index].CallID]; found {
			c.activeToolActivities[index].Message = "Read completed; content was withheld after continuation validation failed and will not be retried."
		}
	}
}
