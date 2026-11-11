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
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"os"
	"path/filepath"
	"testing"
)

func TestChangeWorkingDirectoryResolvesAbsoluteAndRelativePathsWithoutRetiringContext(t *testing.T) {
	root := canonicalFilesystemRootFixture(t, t.TempDir())
	chat := newFilesystemRootTestChat(t, &filesystemRootHostFixture{available: true})
	if err := chat.InitializeWorkingDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	child := filepath.Join(root, "notes with spaces λ")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	child = canonicalFilesystemRootFixture(t, child)
	chat.mu.Lock()
	chat.history = []provider.Message{{Role: provider.RoleUser, Text: "authorized file context"}}
	chat.historySensitive = true
	chat.historyDestination = "openai/model/profile"
	chat.turns = []Turn{{Text: "file-derived answer"}}
	historyBefore := len(chat.history)
	chat.mu.Unlock()

	if err := chat.ChangeWorkingDirectory(context.Background(), "notes with spaces λ"); err != nil {
		t.Fatal(err)
	}
	snapshot := chat.Snapshot()
	if snapshot.WorkingDirectory != child {
		t.Fatalf("working directory=%q, want %q", snapshot.WorkingDirectory, child)
	}
	if snapshot.FilesystemRoot.Path != root {
		t.Fatalf("eligible-target root changed to %q, want retained launch boundary %q", snapshot.FilesystemRoot.Path, root)
	}
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.history) != historyBefore || !chat.historySensitive || chat.historyDestination != "openai/model/profile" || chat.turns[0].Message != "" {
		t.Fatalf("working-directory change retired still-valid conversation context: history=%+v turns=%+v", chat.history, chat.turns)
	}
}

func TestChangeWorkingDirectorySupportsHomeAndPreservesStateOnInvalidPath(t *testing.T) {
	root := canonicalFilesystemRootFixture(t, t.TempDir())
	chat := newFilesystemRootTestChat(t, nil)
	if err := chat.InitializeWorkingDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before := chat.Snapshot().WorkingDirectory

	if err := chat.ChangeWorkingDirectory(context.Background(), filepath.Join("missing", "directory")); !errors.Is(err, ErrFilesystemRootMissing) {
		t.Fatalf("invalid path error=%v, want missing-directory error", err)
	}
	if got := chat.Snapshot().WorkingDirectory; got != before {
		t.Fatalf("invalid path changed working directory to %q, want %q", got, before)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ChangeWorkingDirectory(context.Background(), "~"); err != nil {
		t.Fatal(err)
	}
	want, err := CanonicalFilesystemRoot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if got := chat.Snapshot().WorkingDirectory; got != want {
		t.Fatalf("home working directory=%q, want %q", got, want)
	}
}

func TestChangeWorkingDirectoryRequiresAnIdleTurnAndRejectsMissingArgument(t *testing.T) {
	chat := newFilesystemRootTestChat(t, nil)
	root := canonicalFilesystemRootFixture(t, t.TempDir())
	if err := chat.InitializeWorkingDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := chat.ChangeWorkingDirectory(context.Background(), "  "); !errors.Is(err, ErrWorkingDirectoryArgument) {
		t.Fatalf("missing argument error=%v, want usage error", err)
	}
	chat.mu.Lock()
	chat.active = true
	chat.mu.Unlock()
	if err := chat.ChangeWorkingDirectory(context.Background(), t.TempDir()); !errors.Is(err, ErrRequestActive) {
		t.Fatalf("active-turn change error=%v, want ErrRequestActive", err)
	}
	if got := chat.Snapshot().WorkingDirectory; got != root {
		t.Fatalf("active or missing change changed directory to %q, want %q", got, root)
	}
}
