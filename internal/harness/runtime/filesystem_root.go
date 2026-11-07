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
	"os"
	"path/filepath"
	"time"
)

var (
	ErrFilesystemRootAbsolute = errors.New("filesystem root must be an absolute directory path")
	ErrFilesystemRootMissing  = errors.New("filesystem root is unavailable or cannot be resolved")
	ErrFilesystemRootNotDir   = errors.New("filesystem root must name an existing directory")
	ErrFilesystemRootClosed   = errors.New("chat is closed; filesystem root cannot be changed")
	ErrFilesystemRootStale    = errors.New("filesystem root change was superseded by a newer conversation change")
	ErrFilesystemRootPending  = errors.New("filesystem root changed, but old-root invalidation is still pending")
)

type FilesystemRootChangeReason string

const (
	FilesystemRootSelected          FilesystemRootChangeReason = "selected"
	FilesystemRootReplaced          FilesystemRootChangeReason = "replaced"
	FilesystemRootCleared           FilesystemRootChangeReason = "cleared"
	FilesystemRootConversationReset FilesystemRootChangeReason = "conversation_reset"
	FilesystemRootModelChanged      FilesystemRootChangeReason = "model_changed"
	FilesystemRootProfileChanged    FilesystemRootChangeReason = "profile_changed"
	FilesystemRootShutdown          FilesystemRootChangeReason = "shutdown"
)

// FilesystemRootTransition is a detached lifecycle notification. Generation
// is monotonic for the Chat and consumers must ignore stale, out-of-order
// notifications. Root is only an explicitly selected lookup path; it grants
// no filesystem authority and carries no descriptor.
type FilesystemRootTransition struct {
	ConversationID     string
	PreviousRoot       string
	Root               string
	PreviousGeneration uint64
	Generation         uint64
	Reason             FilesystemRootChangeReason
}

// FilesystemRootHost validates an explicit path, reports the independently
// inspected sandbox backend, and invalidates work bound to older root
// generations. Invalidation must honor ctx and return only after its current
// cleanup obligations are resolved; Stage 1 has no admitted read operations.
type FilesystemRootHost interface {
	ValidateFilesystemRoot(context.Context, string) (string, error)
	InvalidateFilesystemRoot(context.Context, FilesystemRootTransition) error
	BackendStatus() (available bool, detail string)
}

// FilesystemRootStatus contains only detached UI-safe state. BackendAvailable
// means host inspection found the candidate backend; it does not mean a worker
// has enforced policy or that reads are enabled.
type FilesystemRootStatus struct {
	Path                string
	Generation          uint64
	Selected            bool
	BackendAvailable    bool
	BackendStatus       string
	InvalidationPending bool
}

const filesystemRootInvalidationTimeout = 5 * time.Second

// CanonicalFilesystemRoot validates an explicitly supplied absolute existing
// directory and resolves its current symlink aliases. It is a display/lookup
// path only: it does not pin the directory or establish a lasting filesystem
// boundary.
func CanonicalFilesystemRoot(ctx context.Context, path string) (string, error) {
	if ctx == nil || ctx.Err() != nil {
		if ctx == nil {
			return "", context.Canceled
		}
		return "", ctx.Err()
	}
	if !filepath.IsAbs(path) {
		return "", ErrFilesystemRootAbsolute
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", ErrFilesystemRootMissing
	}
	resolved = filepath.Clean(resolved)
	if !filepath.IsAbs(resolved) {
		return "", ErrFilesystemRootAbsolute
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", ErrFilesystemRootMissing
	}
	if !info.IsDir() {
		return "", ErrFilesystemRootNotDir
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return resolved, nil
}

// SelectFilesystemRoot explicitly selects one existing directory for the
// current in-memory conversation. It does not grant permission or retain a
// filesystem descriptor. A newer root operation or conversation reset
// supersedes an in-flight path validation.
func (c *Chat) SelectFilesystemRoot(ctx context.Context, path string) error {
	if ctx == nil {
		return context.Canceled
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrFilesystemRootClosed
	}
	if c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	c.filesystemRootOp++
	operation := c.filesystemRootOp
	conversationID := c.conversationID
	host := c.filesystemRootHostLocked()
	c.mu.Unlock()

	canonical := ""
	var err error
	if host != nil {
		canonical, err = host.ValidateFilesystemRoot(ctx, path)
	} else {
		canonical, err = CanonicalFilesystemRoot(ctx, path)
	}
	if err != nil {
		return err
	}
	if !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		return ErrFilesystemRootAbsolute
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrFilesystemRootClosed
	}
	if c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	if c.conversationID != conversationID || c.filesystemRootOp != operation {
		c.mu.Unlock()
		return ErrFilesystemRootStale
	}
	if c.filesystemRoot == canonical {
		event := c.pendingFilesystemRootEventLocked()
		c.mu.Unlock()
		if event != nil {
			return c.notifyFilesystemRoot(ctx, host, *event)
		}
		return nil
	}
	reason := FilesystemRootSelected
	if c.filesystemRoot != "" {
		reason = FilesystemRootReplaced
	}
	event := c.changeFilesystemRootLocked(canonical, reason)
	c.mu.Unlock()
	return c.notifyFilesystemRoot(ctx, host, event)
}

// ClearFilesystemRoot removes the current conversation's explicit root.
// Clearing an empty root is a no-op, except that it retries an unresolved
// lifecycle notification from an earlier committed transition.
func (c *Chat) ClearFilesystemRoot(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrFilesystemRootClosed
	}
	if c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	c.filesystemRootOp++
	operation := c.filesystemRootOp
	conversationID := c.conversationID
	host := c.filesystemRootHostLocked()
	if c.filesystemRoot == "" {
		event := c.pendingFilesystemRootEventLocked()
		c.mu.Unlock()
		if event != nil {
			return c.notifyFilesystemRoot(ctx, host, *event)
		}
		return nil
	}
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrFilesystemRootClosed
	}
	if c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	if c.conversationID != conversationID || c.filesystemRootOp != operation {
		c.mu.Unlock()
		return ErrFilesystemRootStale
	}
	if c.filesystemRoot == "" {
		event := c.pendingFilesystemRootEventLocked()
		c.mu.Unlock()
		if event != nil {
			return c.notifyFilesystemRoot(ctx, host, *event)
		}
		return nil
	}
	event := c.changeFilesystemRootLocked("", FilesystemRootCleared)
	c.mu.Unlock()
	return c.notifyFilesystemRoot(ctx, host, event)
}

func (c *Chat) filesystemRootHostLocked() FilesystemRootHost {
	if c.toolRuntime == nil {
		return nil
	}
	return c.toolRuntime.rootHost
}

func (c *Chat) filesystemRootHost() FilesystemRootHost {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.filesystemRootHostLocked()
}

func (c *Chat) changeFilesystemRootLocked(root string, reason FilesystemRootChangeReason) FilesystemRootTransition {
	if c.toolRuntime != nil {
		c.toolRuntime.invalidateReadAuthority()
	}
	previousRoot := c.filesystemRoot
	previousGeneration := c.filesystemRootGen
	c.filesystemRootOp++
	c.filesystemRoot = root
	c.filesystemRootGen++
	event := FilesystemRootTransition{
		ConversationID: c.conversationID, PreviousRoot: previousRoot, Root: root,
		PreviousGeneration: previousGeneration, Generation: c.filesystemRootGen, Reason: reason,
	}
	c.filesystemRootEvent = &event
	if c.filesystemRootHostLocked() == nil {
		c.filesystemRootAck = c.filesystemRootGen
	}
	return event
}

func (c *Chat) clearFilesystemRootLocked(reason FilesystemRootChangeReason, force bool) *FilesystemRootTransition {
	if !force && c.filesystemRoot == "" {
		return nil
	}
	event := c.changeFilesystemRootLocked("", reason)
	return &event
}

func (c *Chat) supersedeFilesystemRootOperationsLocked() {
	c.filesystemRootOp++
}

func (c *Chat) pendingFilesystemRootEventLocked() *FilesystemRootTransition {
	if c.filesystemRootAck >= c.filesystemRootGen || c.filesystemRootEvent == nil {
		return nil
	}
	event := *c.filesystemRootEvent
	return &event
}

func (c *Chat) notifyFilesystemRoot(ctx context.Context, host FilesystemRootHost, event FilesystemRootTransition) error {
	c.mu.Lock()
	runtime := c.toolRuntime
	c.mu.Unlock()
	if runtime != nil {
		runtime.invalidateConversation(event.ConversationID)
	}
	if host == nil {
		c.mu.Lock()
		if event.Generation > c.filesystemRootAck {
			c.filesystemRootAck = event.Generation
		}
		c.mu.Unlock()
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, filesystemRootInvalidationTimeout)
	defer cancel()
	err := host.InvalidateFilesystemRoot(bounded, event)
	c.mu.Lock()
	if err == nil {
		if event.Generation > c.filesystemRootAck {
			c.filesystemRootAck = event.Generation
		}
	}
	pending := c.filesystemRootAck < c.filesystemRootGen
	c.mu.Unlock()
	if err != nil || pending {
		return ErrFilesystemRootPending
	}
	return nil
}

func filesystemRootStatus(root string, generation, acknowledged uint64, host FilesystemRootHost) FilesystemRootStatus {
	status := FilesystemRootStatus{Path: root, Generation: generation, Selected: root != "", InvalidationPending: acknowledged < generation}
	if host == nil {
		status.BackendStatus = "sandbox service is not configured; filesystem reads remain disabled"
		return status
	}
	status.BackendAvailable, status.BackendStatus = host.BackendStatus()
	return status
}
