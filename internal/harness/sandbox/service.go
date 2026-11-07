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

// Package sandbox composes the experimental chat's host-owned protected-file
// lifecycle with the fixed Linux read worker. It exposes no filesystem tool.
package sandbox

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

var (
	ErrUnknownRootData = errors.New("generic reads are unavailable because selected-root data coverage is unresolved")
	ErrCoverageMissing = errors.New("required auth/settings protection is not registered")
	ErrServiceClosed   = errors.New("sandbox composition service is closed")
)

type OwnerState string

const (
	OwnerRequired    OwnerState = "required"
	OwnerRegistered  OwnerState = "registered"
	OwnerPending     OwnerState = "pending"
	OwnerStale       OwnerState = "stale"
	OwnerRetired     OwnerState = "retired"
	OwnerUnsupported OwnerState = "unsupported"
	OwnerAbsent      OwnerState = "absent-from-composition"
)

type OwnerCoverage struct {
	Owner      string
	Class      string
	State      OwnerState
	Routes     int
	Identities int
	Evidence   string
	Reason     string
}

type CoverageSnapshot struct {
	Revision            uint64
	Composition         string
	Capability          string
	UnknownRootDataGate bool
	Owners              []OwnerCoverage
}

type implementation interface {
	auth.SensitiveFileProtector
	Close(context.Context) error
	CoverageSnapshot() CoverageSnapshot
	BackendStatus() (available bool, detail string)
	PrepareGenericRead(context.Context, string, string) error
}

// Service is constructed only by trusted executable composition. The
// constructor fixes the production profile; callers cannot submit owner
// applicability or a generic-read completeness assertion.
type Service struct {
	impl               implementation
	rootMu             sync.Mutex
	rootClosed         bool
	rootConversationID string
	rootGeneration     uint64
	rootPath           string
}

var _ auth.SensitiveFileProtector = (*Service)(nil)
var _ runtime.ToolPreparer = (*Service)(nil)
var _ runtime.FilesystemRootHost = (*Service)(nil)

func NewHostService(bubblewrapPath string) (*Service, error) {
	impl, err := newHostImplementation(bubblewrapPath)
	if err != nil {
		return nil, err
	}
	return &Service{impl: impl}, nil
}

// WorkerBootstrap handles only the fixed internal re-exec entry used by the
// Bubblewrap worker. It must run before command parsing, auth loading, UI setup,
// or provider initialization. Other invocations continue through normal CLI
// startup.
func WorkerBootstrap(args []string) (handled bool, exitCode int) {
	return workerBootstrap(args)
}

func (s *Service) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	return s.impl.Protect(ctx, owner, class, route, object)
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.impl == nil {
		return nil
	}
	s.rootMu.Lock()
	s.rootClosed = true
	s.rootPath = ""
	s.rootConversationID = ""
	s.rootGeneration++
	s.rootMu.Unlock()
	return s.impl.Close(ctx)
}

// ValidateFilesystemRoot checks only the user-selected path for display and
// future lookup. It opens no target, retains no descriptor, and does not turn
// selection into permission.
func (s *Service) ValidateFilesystemRoot(ctx context.Context, path string) (string, error) {
	if s == nil || s.impl == nil {
		return "", ErrServiceClosed
	}
	s.rootMu.Lock()
	closed := s.rootClosed
	s.rootMu.Unlock()
	if closed {
		return "", ErrServiceClosed
	}
	return runtime.CanonicalFilesystemRoot(ctx, path)
}

// InvalidateFilesystemRoot records the newest conversation root generation.
// This Stage 1 service has no admitted read work to join; later stages must
// extend this callback to cancel and confirm cleanup before acknowledging it.
func (s *Service) InvalidateFilesystemRoot(ctx context.Context, transition runtime.FilesystemRootTransition) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	s.rootMu.Lock()
	defer s.rootMu.Unlock()
	if s.rootClosed {
		return ErrServiceClosed
	}
	if transition.Generation < s.rootGeneration {
		return nil
	}
	s.rootConversationID = transition.ConversationID
	s.rootGeneration = transition.Generation
	s.rootPath = transition.Root
	return ctx.Err()
}

func (s *Service) CoverageSnapshot() CoverageSnapshot {
	if s == nil || s.impl == nil {
		return CoverageSnapshot{UnknownRootDataGate: true, Owners: []OwnerCoverage{{State: OwnerUnsupported, Reason: ErrServiceClosed.Error()}}}
	}
	return s.impl.CoverageSnapshot()
}

func (s *Service) BackendStatus() (bool, string) {
	if s == nil || s.impl == nil {
		return false, ErrServiceClosed.Error()
	}
	return s.impl.BackendStatus()
}

// Prepares reports whether this trusted host service owns pre-dispatch
// admission for a tool ID. read_file is intentionally not registered; this
// guard makes accidental future registration fail closed before grant use.
func (s *Service) Prepares(id tools.ID) bool { return id == tools.ID("read_file") }

func (s *Service) PrepareTool(ctx context.Context, _ tools.Tool, invocation tools.Invocation, _ permissions.Request, _ uint64) (runtime.ToolPreparation, error) {
	if !s.Prepares(invocation.ToolID) {
		return nil, errors.New("sandbox service received an unsupported tool")
	}
	return nil, s.PrepareGenericRead(ctx, "", "")
}

func (s *Service) PrepareGenericRead(ctx context.Context, root, target string) error {
	if s == nil || s.impl == nil {
		return ErrServiceClosed
	}
	return s.impl.PrepareGenericRead(ctx, root, target)
}
