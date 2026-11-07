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

//go:build !linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
)

type unsupportedImplementation struct {
	mu     sync.Mutex
	closed bool
}

func newHostImplementation(string) (implementation, error) {
	return &unsupportedImplementation{}, nil
}

func (s *unsupportedImplementation) Protect(ctx context.Context, _, _, _ string, _ *os.File) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrServiceClosed
	}
	// Text-only chat remains available on unsupported platforms. The read tool
	// is not registered and no platform-specific file capability is admitted.
	return nil
}

func (s *unsupportedImplementation) Close(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *unsupportedImplementation) CoverageSnapshot() CoverageSnapshot {
	return CoverageSnapshot{
		Composition:         "komposyn-memory-chat",
		Capability:          "linux-exact-file-typed-read",
		UnknownRootDataGate: true,
		Owners: []OwnerCoverage{
			{Owner: auth.SensitiveOwnerAuth, Class: auth.SensitiveClassAuth, State: OwnerUnsupported, Reason: "no supported platform registration backend"},
			{Owner: auth.SensitiveOwnerSettings, Class: auth.SensitiveClassSettings, State: OwnerUnsupported, Reason: "no supported platform registration backend"},
			{Owner: "tokenlogger-store", Class: "token_database", State: OwnerAbsent, Evidence: "trusted cmd/harness dependency graph", Reason: "this composition does not open or undertake a Token Logger Store"},
			{Owner: "provider-source", Class: "provider_source", State: OwnerUnsupported, Reason: "no source identity/open lifecycle is registered"},
			{Owner: "unknown-root-data", Class: "unclassified", State: OwnerUnsupported, Reason: ErrUnknownRootData.Error()},
		},
	}
}

func (s *unsupportedImplementation) BackendStatus() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrServiceClosed.Error()
	}
	return false, "sandboxed typed reads are supported only on Linux"
}

func (s *unsupportedImplementation) PrepareGenericRead(ctx context.Context, _, _ string) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrServiceClosed
	}
	return errors.Join(ErrUnknownRootData, errors.New("sandboxed typed reads are supported only on Linux"))
}

var _ implementation = (*unsupportedImplementation)(nil)
