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
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/plugins/tools"
)

type filesystemRootHostFixture struct {
	mu            sync.Mutex
	available     bool
	backendStatus string
	transitions   []FilesystemRootTransition
	validate      func(context.Context, string) (string, error)
	invalidate    func(context.Context, FilesystemRootTransition) error
}

func (h *filesystemRootHostFixture) ValidateFilesystemRoot(ctx context.Context, path string) (string, error) {
	if h.validate != nil {
		return h.validate(ctx, path)
	}
	return CanonicalFilesystemRoot(ctx, path)
}

func (h *filesystemRootHostFixture) InvalidateFilesystemRoot(ctx context.Context, transition FilesystemRootTransition) error {
	h.mu.Lock()
	h.transitions = append(h.transitions, transition)
	hook := h.invalidate
	h.mu.Unlock()
	if hook != nil {
		return hook(ctx, transition)
	}
	return ctx.Err()
}

func (h *filesystemRootHostFixture) BackendStatus() (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.available, h.backendStatus
}

func (h *filesystemRootHostFixture) transitionsSnapshot() []FilesystemRootTransition {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]FilesystemRootTransition(nil), h.transitions...)
}

func newFilesystemRootTestChat(t *testing.T, host FilesystemRootHost) *Chat {
	t.Helper()
	chat, _ := makeChat(t, &fakeProvider{id: "openai"})
	chat.toolRuntime = newToolRuntime(ToolRuntimeConfig{Registry: tools.NewRegistry(), RootHost: host})
	return chat
}

func canonicalFilesystemRootFixture(t *testing.T, path string) string {
	t.Helper()
	canonical, err := CanonicalFilesystemRoot(context.Background(), path)
	if err != nil {
		t.Fatalf("canonicalize root fixture %q: %v", path, err)
	}
	return canonical
}

func TestFilesystemRootStartsUnsetAndSelectionIsNotPermission(t *testing.T) {
	host := &filesystemRootHostFixture{available: true, backendStatus: "candidate inspected; no worker launched"}
	chat := newFilesystemRootTestChat(t, host)
	initial := chat.Snapshot().FilesystemRoot
	if initial.Selected || initial.Path != "" || initial.Generation == 0 || !initial.BackendAvailable {
		t.Fatalf("initial root status=%+v", initial)
	}
	if len(chat.ToolGrants()) != 0 {
		t.Fatal("root selection fixture unexpectedly had grants")
	}

	tempParent := t.TempDir()
	canonicalParent := filepath.Join(tempParent, "real temporary root")
	if err := os.Mkdir(canonicalParent, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkedTempRoot := filepath.Join(tempParent, "symlinked temporary root")
	if err := os.Symlink(canonicalParent, symlinkedTempRoot); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(symlinkedTempRoot, "selected root with spaces")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalRoot := canonicalFilesystemRootFixture(t, root)
	alias := filepath.Join(t.TempDir(), "root alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	selected := chat.Snapshot().FilesystemRoot
	if !selected.Selected || selected.Path != canonicalRoot || selected.Generation == initial.Generation || len(chat.ToolGrants()) != 0 {
		t.Fatalf("selected status=%+v grants=%+v", selected, chat.ToolGrants())
	}
	if err := chat.SelectFilesystemRoot(context.Background(), alias); err != nil {
		t.Fatal(err)
	}
	if got := chat.Snapshot().FilesystemRoot; got.Path != canonicalRoot || got.Generation != selected.Generation {
		t.Fatalf("same canonical root changed state: selected=%+v got=%+v", selected, got)
	}
	transitions := host.transitionsSnapshot()
	if len(transitions) != 1 || transitions[0].Reason != FilesystemRootSelected || transitions[0].Root != canonicalRoot {
		t.Fatalf("root transitions=%+v", transitions)
	}
}

func TestFilesystemRootRejectsInvalidInputWithoutChangingSelection(t *testing.T) {
	chat := newFilesystemRootTestChat(t, &filesystemRootHostFixture{})
	root := filepath.Join(t.TempDir(), "current root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before := chat.Snapshot().FilesystemRoot
	file := filepath.Join(t.TempDir(), "not a directory")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want error
	}{
		{path: "relative-root", want: ErrFilesystemRootAbsolute},
		{path: file, want: ErrFilesystemRootNotDir},
		{path: filepath.Join(t.TempDir(), "missing"), want: ErrFilesystemRootMissing},
	}
	for _, test := range cases {
		if err := chat.SelectFilesystemRoot(context.Background(), test.path); !errors.Is(err, test.want) {
			t.Errorf("select %q error=%v, want %v", test.path, err, test.want)
		}
		after := chat.Snapshot().FilesystemRoot
		if after.Path != before.Path || after.Generation != before.Generation {
			t.Fatalf("failed selection mutated root: before=%+v after=%+v", before, after)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := chat.SelectFilesystemRoot(canceled, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selection error=%v", err)
	}
}

func TestFilesystemRootClearAndInvalidationFailureRetry(t *testing.T) {
	host := &filesystemRootHostFixture{}
	chat := newFilesystemRootTestChat(t, host)
	if err := chat.ClearFilesystemRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := chat.Snapshot().FilesystemRoot
	if len(host.transitionsSnapshot()) != 0 {
		t.Fatal("clearing an empty root emitted an invalidation")
	}
	root := t.TempDir()
	canonicalRoot := canonicalFilesystemRootFixture(t, root)
	fail := true
	host.invalidate = func(ctx context.Context, _ FilesystemRootTransition) error {
		if fail {
			return errors.New("fixture invalidation failure")
		}
		return ctx.Err()
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); !errors.Is(err, ErrFilesystemRootPending) {
		t.Fatalf("selection error=%v", err)
	}
	pending := chat.Snapshot().FilesystemRoot
	if !pending.Selected || pending.Path != canonicalRoot || !pending.InvalidationPending || pending.Generation <= initial.Generation {
		t.Fatalf("committed selection status=%+v", pending)
	}
	fail = false
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatalf("retry same root: %v", err)
	}
	cleared := chat.Snapshot().FilesystemRoot
	if cleared.Generation != pending.Generation || cleared.InvalidationPending {
		t.Fatalf("same-root retry changed generation or stayed pending: %+v", cleared)
	}
	if err := chat.ClearFilesystemRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	clearGeneration := chat.Snapshot().FilesystemRoot.Generation
	if err := chat.ClearFilesystemRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := chat.Snapshot().FilesystemRoot; after.Selected || after.Generation != clearGeneration {
		t.Fatalf("clear was not idempotent: %+v", after)
	}
}

func TestFilesystemRootLifecycleResetAndCanceledModelChange(t *testing.T) {
	host := &filesystemRootHostFixture{}
	chat := newFilesystemRootTestChat(t, host)
	selectFirst(t, chat)
	root := t.TempDir()
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	beforeFirstTurn := chat.Snapshot().FilesystemRoot
	other := chat.Discover(context.Background()).Models[1].Key()
	if err := chat.Select(context.Background(), other, false); err != nil {
		t.Fatal(err)
	}
	afterFirstChange := chat.Snapshot().FilesystemRoot
	if afterFirstChange.Selected || afterFirstChange.Path != "" || afterFirstChange.Generation <= beforeFirstTurn.Generation {
		t.Fatalf("pre-turn model change retained root: %+v", afterFirstChange)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Send(context.Background(), "synthetic message", nil); err != nil {
		t.Fatal(err)
	}
	before := chat.Snapshot().FilesystemRoot
	if err := chat.Select(context.Background(), chat.Discover(context.Background()).Models[0].Key(), false); !errors.Is(err, ErrConversationReset) {
		t.Fatalf("unconfirmed post-turn model change error=%v", err)
	}
	if after := chat.Snapshot().FilesystemRoot; after.Path != before.Path || after.Generation != before.Generation {
		t.Fatalf("canceled model reset changed root: before=%+v after=%+v", before, after)
	}
	if err := chat.Select(context.Background(), chat.Discover(context.Background()).Models[0].Key(), true); err != nil {
		t.Fatal(err)
	}
	after := chat.Snapshot().FilesystemRoot
	if after.Selected || after.Path != "" || after.Generation <= before.Generation {
		t.Fatalf("confirmed model change retained root: %+v", after)
	}
	transitions := host.transitionsSnapshot()
	if got := transitions[len(transitions)-1].Reason; got != FilesystemRootModelChanged {
		t.Fatalf("last transition reason=%q", got)
	}
	if err := chat.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); !errors.Is(err, ErrFilesystemRootClosed) {
		t.Fatalf("closed chat root selection error=%v", err)
	}
}

func TestFilesystemRootClearsOnSelectedProfileReplacement(t *testing.T) {
	host := &filesystemRootHostFixture{}
	chat := newFilesystemRootTestChat(t, host)
	selectFirst(t, chat)
	root := t.TempDir()
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before := chat.Snapshot().FilesystemRoot
	if err := chat.ReplaceProfile(context.Background(), "profile", "Updated", "new-synthetic-key", nil, false); err != nil {
		t.Fatal(err)
	}
	after := chat.Snapshot().FilesystemRoot
	if after.Selected || after.Generation <= before.Generation {
		t.Fatalf("pre-turn selected profile replacement retained root: before=%+v after=%+v", before, after)
	}
	if transitions := host.transitionsSnapshot(); transitions[len(transitions)-1].Reason != FilesystemRootProfileChanged {
		t.Fatalf("profile replacement transition=%+v", transitions[len(transitions)-1])
	}

	selectFirst(t, chat)
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Send(context.Background(), "synthetic message", nil); err != nil {
		t.Fatal(err)
	}
	before = chat.Snapshot().FilesystemRoot
	if err := chat.ReplaceProfile(context.Background(), "profile", "Updated again", "another-synthetic-key", nil, false); !errors.Is(err, ErrConversationReset) {
		t.Fatalf("unconfirmed post-turn profile replacement error=%v", err)
	}
	if after := chat.Snapshot().FilesystemRoot; after.Path != before.Path || after.Generation != before.Generation {
		t.Fatalf("canceled profile replacement changed root: before=%+v after=%+v", before, after)
	}
	if err := chat.ReplaceProfile(context.Background(), "profile", "Updated again", "another-synthetic-key", nil, true); err != nil {
		t.Fatal(err)
	}
	if after := chat.Snapshot().FilesystemRoot; after.Selected || after.Generation <= before.Generation {
		t.Fatalf("confirmed post-turn profile replacement retained root: before=%+v after=%+v", before, after)
	}
}

func TestFilesystemRootInvalidationRunsOutsideChatLockAndHonorsDeadline(t *testing.T) {
	var chat *Chat
	host := &filesystemRootHostFixture{}
	host.invalidate = func(ctx context.Context, _ FilesystemRootTransition) error {
		if chat.Snapshot().FilesystemRoot.Generation == 0 {
			t.Error("snapshot did not include root generation")
		}
		return ctx.Err()
	}
	chat = newFilesystemRootTestChat(t, host)
	if err := chat.SelectFilesystemRoot(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("invalidation callback ran under chat lock or failed: %v", err)
	}

	host.invalidate = func(ctx context.Context, _ FilesystemRootTransition) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := chat.SelectFilesystemRoot(ctx, t.TempDir()); !errors.Is(err, ErrFilesystemRootPending) {
		t.Fatalf("deadline-bound invalidation error=%v", err)
	}
	if !chat.Snapshot().FilesystemRoot.InvalidationPending {
		t.Fatal("unconfirmed invalidation was reported as complete")
	}
}

func TestFilesystemRootDelayedValidationCannotOverwriteNewerRootOrReset(t *testing.T) {
	host := &filesystemRootHostFixture{}
	chat := newFilesystemRootTestChat(t, host)
	first := t.TempDir()
	second := t.TempDir()
	canonicalSecond := canonicalFilesystemRootFixture(t, second)
	started := make(chan struct{})
	release := make(chan struct{})
	host.validate = func(ctx context.Context, path string) (string, error) {
		if path == first {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return CanonicalFilesystemRoot(ctx, path)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- chat.SelectFilesystemRoot(context.Background(), first) }()
	<-started
	if err := chat.SelectFilesystemRoot(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-firstDone; !errors.Is(err, ErrFilesystemRootStale) {
		t.Fatalf("older selection error=%v", err)
	}
	if got := chat.Snapshot().FilesystemRoot.Path; got != canonicalSecond {
		t.Fatalf("older selection replaced newer root: %q", got)
	}
}

func TestFilesystemRootOperationDuringActiveTurnIsExplicitAndInvalidated(t *testing.T) {
	host := &filesystemRootHostFixture{}
	chat := newFilesystemRootTestChat(t, host)
	chat.mu.Lock()
	chat.active = true
	chat.mu.Unlock()
	root := t.TempDir()
	canonicalRoot := canonicalFilesystemRootFixture(t, root)
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	transitions := host.transitionsSnapshot()
	if len(transitions) != 1 || transitions[0].Root != canonicalRoot {
		t.Fatalf("active-turn root transition=%+v", transitions)
	}
	chat.mu.Lock()
	chat.active = false
	chat.mu.Unlock()
}
