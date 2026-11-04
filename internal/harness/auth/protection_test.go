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

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type protectionFunc func(context.Context, string, string, string, *os.File) error

func (f protectionFunc) Protect(ctx context.Context, owner, class, route string, file *os.File) error {
	return f(ctx, owner, class, route, file)
}

func TestProtectedStoreRegistersEmptyReplacementBeforeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	var calls int
	store := Store{Path: path, Protection: protectionFunc(func(_ context.Context, owner, class, route string, file *os.File) error {
		calls++
		if owner != SensitiveOwnerAuth || class != SensitiveClassAuth || route != path || file == nil {
			t.Fatalf("unexpected owner registration: %q %q %q file=%v", owner, class, route, file)
		}
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Size() != 0 {
			t.Fatalf("protected temp already contains %d bytes", info.Size())
		}
		return nil
	})}
	if err := store.AddAPIKey("p", "openai", "label", "synthetic-secret", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("protection calls=%d", calls)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), "synthetic-secret") {
		t.Fatal("expected saved synthetic credential")
	}
}

func TestProtectedStoreFailurePreventsSecretWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	want := errors.New("cleanup pending")
	store := Store{Path: path, Protection: protectionFunc(func(_ context.Context, _, _, _ string, _ *os.File) error { return want })}
	if err := store.AddAPIKey("p", "openai", "label", "synthetic-secret", nil); !errors.Is(err, want) {
		t.Fatalf("AddAPIKey error=%v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("destination state after refusal: %v", err)
	}
}

func TestProtectedStoreDoesNotRemoveSubstitutedTemporaryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	var replacement string
	store := Store{Path: path, Protection: protectionFunc(func(_ context.Context, _, _, _ string, file *os.File) error {
		replacement = file.Name()
		if err := os.Rename(replacement, replacement+".pinned"); err != nil {
			return err
		}
		return os.WriteFile(replacement, []byte("replacement"), 0o600)
	})}
	if err := store.AddAPIKey("p", "openai", "label", "synthetic-secret", nil); err == nil {
		t.Fatal("expected identity mismatch refusal")
	}
	b, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
	if string(b) != "replacement" {
		t.Fatalf("replacement content changed: %q", b)
	}
}

func contains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
