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

package sandboxstudy

import (
	"errors"
	"sync"
)

var (
	ErrGateOrder       = errors.New("operation start gate event is out of order")
	ErrGateCancelled   = errors.New("operation start gate is cancelled")
	ErrOnceNotConsumed = errors.New("runtime has not consumed the once grant")
)

type gateState uint8

const (
	gatePrepared gateState = iota
	gateRegistered
	gateAcknowledged
	gateReleased
	gateCancelled
)

// operationGate is an executable synchronization model for the study only.
// The production runtime remains unchanged. The runtime passes that its
// existing once-grant consumption already occurred; this gate never consumes
// grants itself.
type operationGate struct {
	mu               sync.Mutex
	expectedRevision uint64
	currentRevision  uint64
	state            gateState
	childID          string
	onceConsumed     bool
}

func newOperationGate(revision uint64, onceConsumed bool) *operationGate {
	return &operationGate{expectedRevision: revision, currentRevision: revision, state: gatePrepared, onceConsumed: onceConsumed}
}

func (g *operationGate) RegisterChild(childID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gatePrepared || childID == "" {
		return ErrGateOrder
	}
	g.childID = childID
	g.state = gateRegistered
	return nil
}

func (g *operationGate) AcknowledgeStartup(childID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateRegistered || childID != g.childID {
		return ErrGateOrder
	}
	g.state = gateAcknowledged
	return nil
}

// Release linearizes with Revoke/Cancel. A successful release means an effect
// may begin; revocation after this point requests termination but cannot claim
// that no effect occurred.
func (g *operationGate) Release(childID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == gateCancelled {
		return ErrGateCancelled
	}
	if g.currentRevision != g.expectedRevision {
		g.state = gateCancelled
		return ErrGateCancelled
	}
	if g.state != gateAcknowledged || childID != g.childID {
		return ErrGateOrder
	}
	if !g.onceConsumed {
		return ErrOnceNotConsumed
	}
	g.state = gateReleased
	return nil
}

// Revoke advances the trusted revision. It returns true when a child has
// already passed the gate and needs termination; a pre-release child is simply
// denied release. Caller cancellation/revocation and release share this lock.
func (g *operationGate) Revoke(revision uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if revision > g.currentRevision {
		g.currentRevision = revision
	}
	if g.state == gateReleased {
		return true
	}
	g.state = gateCancelled
	return false
}

func (g *operationGate) Cancel() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == gateReleased {
		return true
	}
	g.state = gateCancelled
	return false
}

func (g *operationGate) Released() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == gateReleased
}
