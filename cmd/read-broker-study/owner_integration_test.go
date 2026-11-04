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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
)

func TestRealAuthStoreRegistersPublishedAndRetiredObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential-override.json")
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	session := &ownerLifecycleTestSession{registry: registry, policy: registry.View().Generation}
	store := auth.Store{Path: path, Protection: studyOwnerProtection{session: session, registry: registry}}
	if got, err := store.EffectivePath(); err != nil || got != path {
		t.Fatalf("effective path=%q err=%v", got, err)
	}
	if err := store.RegisterProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAPIKey("one", "openai", "synthetic", "synthetic-secret-one", nil); err != nil {
		t.Fatal(err)
	}
	first, err := identityAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterProtection(context.Background()); err != nil {
		t.Fatalf("register existing auth object: %v", err)
	}
	if class, protected := registry.Classify(path, first); !protected || class != readbrokerstudy.ClassCredential {
		t.Fatalf("published auth file classification=%q protected=%v", class, protected)
	}
	alias := filepath.Join(dir, "alias.json")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if class, protected := registry.Classify(alias, first); !protected || class != readbrokerstudy.ClassCredential {
		t.Fatalf("auth alias classification=%q protected=%v", class, protected)
	}
	if err := store.AddAPIKey("two", "openai", "synthetic", "synthetic-secret-two", nil); err != nil {
		t.Fatal(err)
	}
	second, err := identityAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("atomic replacement unexpectedly reused the previous identity")
	}
	if class, protected := registry.Classify(alias, first); !protected || class != readbrokerstudy.ClassCredential {
		t.Fatalf("retired auth identity lost protection: %q %v", class, protected)
	}
	if class, protected := registry.Classify(path, second); !protected || class != readbrokerstudy.ClassCredential {
		t.Fatalf("rotated auth identity not protected: %q %v", class, protected)
	}
}

func TestRealSettingsStoreOverrideUsesOwnerBarrier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings-override.json")
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	session := &ownerLifecycleTestSession{registry: registry, policy: registry.View().Generation}
	store := settings.Store{Path: path, Protection: studyOwnerProtection{session: session, registry: registry}}
	if err := store.RegisterProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		t.Fatal(err)
	}
	identity, err := identityAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if class, protected := registry.Classify(path, identity); !protected || class != readbrokerstudy.ClassSettings {
		t.Fatalf("settings classification=%q protected=%v", class, protected)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Plugin != settings.DefaultUI {
		t.Fatalf("settings roundtrip=%+v err=%v", loaded, err)
	}
}

func TestRealOwnerStoreBlocksWriteUntilLifecycleRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	session := &ownerLifecycleTestSession{registry: registry, policy: registry.View().Generation, firstRegister: readbrokerstudy.ErrCleanupPending}
	store := auth.Store{Path: path, Protection: studyOwnerProtection{session: session, registry: registry}}
	err = store.AddAPIKey("one", "openai", "synthetic", "synthetic-secret", nil)
	if err != readbrokerstudy.ErrCleanupPending {
		t.Fatalf("first write error=%v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("store wrote before cleanup join: %v", err)
	}
	if err := store.AddAPIKey("one", "openai", "synthetic", "synthetic-secret", nil); err != nil {
		t.Fatal(err)
	}
	if session.registerCalls != 2 {
		t.Fatalf("registration retries=%d", session.registerCalls)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "synthetic-secret") {
		t.Fatal("synthetic auth value not persisted after barrier")
	}
}
