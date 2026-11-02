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

//go:build linux

package readbrokerstudy

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrNoFixtureApproval = errors.New("fixture read has no explicit once approval")
	ErrGrantAlreadyUsed  = errors.New("fixture once approval has already been consumed")
)

// OnceGrantFixture is an in-memory test authorizer. Tests and the CLI must
// explicitly approve a unique invocation; there is no default allow. It
// models the runtime as the sole once-grant consumer and is not production
// authorization code.
type OnceGrantFixture struct {
	mu       sync.Mutex
	approved map[string]bool
	consumed map[string]int
}

func NewOnceGrantFixture() *OnceGrantFixture {
	return &OnceGrantFixture{approved: make(map[string]bool), consumed: make(map[string]int)}
}

// ApproveOnce explicitly adds one fixture approval correlated to an opaque
// invocation ID. The exact prepared target and classification are bound into
// the receipt at consumption time.
func (a *OnceGrantFixture) ApproveOnce(invocationID string) error {
	if invocationID == "" {
		return ErrStaleReceipt
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.approved[invocationID] || a.consumed[invocationID] != 0 {
		return ErrGrantAlreadyUsed
	}
	a.approved[invocationID] = true
	return nil
}

func (a *OnceGrantFixture) AuthorizeAndConsume(ctx context.Context, request AuthorizationRequest) (AuthorizationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return AuthorizationReceipt{}, err
	}
	if request.InvocationID == "" || request.Identity.Inode == 0 || request.RequestDigest == ([32]byte{}) {
		return AuthorizationReceipt{}, ErrStaleReceipt
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.approved[request.InvocationID] {
		return AuthorizationReceipt{}, ErrNoFixtureApproval
	}
	delete(a.approved, request.InvocationID)
	a.consumed[request.InvocationID]++
	return AuthorizationReceipt{
		InvocationID:  request.InvocationID,
		Identity:      request.Identity,
		Class:         request.Class,
		Generations:   request.Generations,
		OnceConsumed:  true,
		RequestDigest: request.RequestDigest,
	}, nil
}

// ConsumptionCount returns a detached fixture-only count for assertions.
func (a *OnceGrantFixture) ConsumptionCount(invocationID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.consumed[invocationID]
}
