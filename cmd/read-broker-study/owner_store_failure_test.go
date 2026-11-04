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

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
)

type ownerProtectorFunc func(context.Context, string, string, string, *os.File) error

func (f ownerProtectorFunc) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	return f(ctx, owner, class, route, object)
}

func TestRealOwnerStoreRegistrationFailurePreservesPublishedFiles(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth-override.json")
		store := auth.Store{Path: path}
		if err := store.AddAPIKey("synthetic", "openai", "fixture", "synthetic-old-value", nil); err != nil {
			t.Fatal("seed auth store")
		}
		before, info := readStoreSnapshot(t, path)
		registerErr := errors.New("fixture registration refusal")
		store.Protection = ownerProtectorFunc(func(context.Context, string, string, string, *os.File) error { return registerErr })
		if err := store.ReplaceAPIKeyContext(context.Background(), "synthetic", "fixture", "synthetic-new-value", nil); !errors.Is(err, registerErr) {
			t.Fatalf("replace error=%v, want registration refusal", err)
		}
		assertStoreUnchanged(t, path, before, info)
	})

	t.Run("settings", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings-override.json")
		store := settings.Store{Path: path}
		if err := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
			t.Fatal("seed settings store")
		}
		before, info := readStoreSnapshot(t, path)
		registerErr := errors.New("fixture registration refusal")
		store.Protection = ownerProtectorFunc(func(context.Context, string, string, string, *os.File) error { return registerErr })
		if err := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); !errors.Is(err, registerErr) {
			t.Fatalf("save error=%v, want registration refusal", err)
		}
		assertStoreUnchanged(t, path, before, info)
	})
}

func TestRealOwnerStoreCancellationDuringProtectionPreservesPublishedFiles(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth-override.json")
		store := auth.Store{Path: path}
		if err := store.AddAPIKey("synthetic", "openai", "fixture", "synthetic-old-value", nil); err != nil {
			t.Fatal("seed auth store")
		}
		before, info := readStoreSnapshot(t, path)
		entered := make(chan struct{})
		store.Protection = ownerProtectorFunc(func(ctx context.Context, _, _, _ string, _ *os.File) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- store.ReplaceAPIKeyContext(ctx, "synthetic", "fixture", "synthetic-new-value", nil) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("auth store did not reach its protection hook")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("replace error=%v, want cancellation", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("auth store did not return after cancellation")
		}
		assertStoreUnchanged(t, path, before, info)
	})

	t.Run("settings", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings-override.json")
		store := settings.Store{Path: path}
		if err := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
			t.Fatal("seed settings store")
		}
		before, info := readStoreSnapshot(t, path)
		entered := make(chan struct{})
		store.Protection = ownerProtectorFunc(func(ctx context.Context, _, _, _ string, _ *os.File) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- store.SaveContext(ctx, settings.Settings{Plugin: settings.DefaultUI}) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("settings store did not reach its protection hook")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("save error=%v, want cancellation", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("settings store did not return after cancellation")
		}
		assertStoreUnchanged(t, path, before, info)
	})
}

func readStoreSnapshot(t *testing.T, path string) ([]byte, os.FileInfo) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read synthetic store")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("stat synthetic store")
	}
	return contents, info
}

func assertStoreUnchanged(t *testing.T, path string, before []byte, beforeInfo os.FileInfo) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read store after refused mutation")
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal("stat store after refused mutation")
	}
	if string(after) != string(before) || !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("refused store mutation changed the published fixture")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal("inspect fixture cleanup")
	}
	for _, entry := range entries {
		if len(entry.Name()) >= len(".auth-") && entry.Name()[:len(".auth-")] == ".auth-" && entry.Name() != filepath.Base(path) {
			t.Fatal("refused store mutation left a temporary auth object")
		}
	}
}
