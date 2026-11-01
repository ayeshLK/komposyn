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

// Package filesystem owns the narrow, host-only filesystem boundary for
// reviewed bundled operations. It exposes no os.File or directory handle to
// tools. Filesystem tools are not registered by this package.
package filesystem

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
)

const (
	MaxReadBytes     = 256 << 10
	MaxWriteBytes    = 1 << 20
	MaxDeleteEntries = 10_000
	MaxDeleteDepth   = 64
)

var (
	ErrUnsupported           = errors.New("filesystem enforcement is unavailable on this platform or kernel")
	ErrInvalidPath           = errors.New("filesystem path is invalid")
	ErrOutOfScope            = errors.New("filesystem target is outside the authorized directory")
	ErrSymlink               = errors.New("filesystem links are not followed for this operation")
	ErrTargetChanged         = errors.New("filesystem target changed after preparation")
	ErrClosed                = errors.New("filesystem capability is closed")
	ErrInvalidLimit          = errors.New("filesystem operation limit is invalid")
	ErrTooLarge              = errors.New("filesystem operation exceeds its size bound")
	ErrNotRegular            = errors.New("filesystem target is not a regular file")
	ErrAliasedTarget         = errors.New("multiply linked filesystem target is unavailable")
	ErrInvalidText           = errors.New("filesystem text must be valid UTF-8")
	ErrRecursiveIntentNeeded = errors.New("directory deletion requires explicit recursive intent")
)

type Outcome string

const (
	OutcomeNotExecuted Outcome = "not_executed"
	OutcomeSucceeded   Outcome = "succeeded"
	OutcomeFailed      Outcome = "failed"
	OutcomePartial     Outcome = "partial"
	OutcomeUnknown     Outcome = "unknown"
	OutcomeCancelled   Outcome = "cancelled"
)

type ReadResult struct {
	Outcome   Outcome
	Text      string
	Truncated bool
}

type MutationResult struct {
	Outcome        Outcome
	BytesWritten   int64
	EntriesDeleted int64
}

type rootBackend interface {
	close() error
	prepareRead(relative string) (readBackend, error)
	prepareWrite(relative string) (writeBackend, error)
	prepareDelete(relative string, recursive bool) (deleteBackend, error)
}

type readBackend interface {
	close() error
	read(context.Context, int) (ReadResult, error)
}

type writeBackend interface {
	close() error
	write(context.Context, []byte) (MutationResult, error)
}

type deleteBackend interface {
	close() error
	remove(context.Context) (MutationResult, error)
}

// Root is an explicitly selected directory capability. Opening a Root never
// authorizes it; callers must still obtain action and resource permission.
// The native backend binds later operations to descriptors derived from this
// directory. Callers own and must close Roots and every prepared target.
type Root struct {
	mu      sync.Mutex
	path    string
	backend rootBackend
	closed  bool
}

// OpenRoot accepts only an absolute, already-clean path. The native backend
// rejects roots it cannot open without following links or enforcing the
// platform's required descriptor-relative constraints.
func OpenRoot(path string) (*Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return nil, ErrInvalidPath
	}
	backend, err := openRootNative(path)
	if err != nil {
		return nil, err
	}
	return &Root{path: path, backend: backend}, nil
}

// AuthorizationScope returns the recursive directory scope represented by
// this capability. It grants nothing by itself. Delete requirements must set
// SessionAllowed=false; the permission coordinator then enforces per-call
// approval even if other operations have conversation grants.
func (r *Root) AuthorizationScope() permissions.ResourceScope {
	if r == nil {
		return permissions.ResourceScope{}
	}
	return permissions.ResourceScope{
		Kind: permissions.ResourceFilesystem, Root: r.path, Recursive: true,
		Operations: []permissions.Operation{permissions.OperationRead, permissions.OperationWrite, permissions.OperationDelete},
	}
}

// Close closes the root descriptor. Prepared targets own their own descriptors
// and must also be closed. A prepared capability remains bounded to the same
// opened directory object until it is closed.
func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.backend.close()
}

type ReadTarget struct {
	mu      sync.Mutex
	root    *Root
	target  string
	backend readBackend
	closed  bool
}

type WriteTarget struct {
	mu      sync.Mutex
	root    *Root
	target  string
	backend writeBackend
	closed  bool
}

type DeleteTarget struct {
	mu        sync.Mutex
	root      *Root
	target    string
	recursive bool
	backend   deleteBackend
	closed    bool
}

func (r *Root) PrepareRead(target string) (*ReadTarget, error) {
	relative, err := r.relativeTarget(target)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	backend, err := r.backend.prepareRead(relative)
	if err != nil {
		return nil, err
	}
	return &ReadTarget{root: r, target: target, backend: backend}, nil
}

func (r *Root) PrepareWrite(target string) (*WriteTarget, error) {
	relative, err := r.relativeTarget(target)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	backend, err := r.backend.prepareWrite(relative)
	if err != nil {
		return nil, err
	}
	return &WriteTarget{root: r, target: target, backend: backend}, nil
}

func (r *Root) PrepareDelete(target string, recursive bool) (*DeleteTarget, error) {
	relative, err := r.relativeTarget(target)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	backend, err := r.backend.prepareDelete(relative, recursive)
	if err != nil {
		return nil, err
	}
	return &DeleteTarget{root: r, target: target, recursive: recursive, backend: backend}, nil
}

func (r *Root) relativeTarget(target string) (string, error) {
	if r == nil || !filepath.IsAbs(target) || filepath.Clean(target) != target || strings.ContainsRune(target, '\x00') {
		return "", ErrInvalidPath
	}
	relative, err := filepath.Rel(r.path, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrOutOfScope
	}
	return relative, nil
}

func (t *ReadTarget) ResourceRequest() permissions.ResourceRequest {
	return permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: t.target, Operation: permissions.OperationRead}
}

func (t *WriteTarget) ResourceRequest() permissions.ResourceRequest {
	return permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: t.target, Operation: permissions.OperationWrite}
}

func (t *DeleteTarget) ResourceRequest() permissions.ResourceRequest {
	return permissions.ResourceRequest{Kind: permissions.ResourceFilesystem, Target: t.target, Operation: permissions.OperationDelete}
}

func (t *ReadTarget) ReadText(ctx context.Context, maxBytes int) (ReadResult, error) {
	if t == nil || maxBytes <= 0 || maxBytes > MaxReadBytes {
		return ReadResult{Outcome: OutcomeNotExecuted}, ErrInvalidLimit
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ReadResult{Outcome: OutcomeNotExecuted}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return ReadResult{Outcome: OutcomeCancelled}, err
	}
	return t.backend.read(ctx, maxBytes)
}

func (t *ReadTarget) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.backend.close()
}

func (t *WriteTarget) WriteText(ctx context.Context, text string) (MutationResult, error) {
	if t == nil || len(text) > MaxWriteBytes {
		return MutationResult{Outcome: OutcomeNotExecuted}, ErrTooLarge
	}
	if !utf8.ValidString(text) {
		return MutationResult{Outcome: OutcomeNotExecuted}, ErrInvalidText
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return MutationResult{Outcome: OutcomeNotExecuted}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{Outcome: OutcomeCancelled}, err
	}
	return t.backend.write(ctx, []byte(text))
}

func (t *WriteTarget) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.backend.close()
}

func (t *DeleteTarget) Delete(ctx context.Context) (MutationResult, error) {
	if t == nil {
		return MutationResult{Outcome: OutcomeNotExecuted}, ErrInvalidPath
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return MutationResult{Outcome: OutcomeNotExecuted}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{Outcome: OutcomeCancelled}, err
	}
	return t.backend.remove(ctx)
}

func (t *DeleteTarget) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.backend.close()
}
