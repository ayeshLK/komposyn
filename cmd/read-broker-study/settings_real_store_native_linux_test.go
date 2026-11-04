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
	"path/filepath"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
)

func TestObservedSettingsProtectorTracksEmptyRegisteredReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	session := &ownerLifecycleTestSession{registry: registry, policy: registry.View().Generation}
	protector := &observedSettingsProtector{delegate: studyOwnerProtection{session: session, registry: registry}}
	store := settings.Store{Path: path, Protection: protector}
	protector.arm(false)
	if err := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		t.Fatal(err)
	}
	protector.disarm()
	replacement := protector.lastReplacement()
	published, err := identityAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !replacement.empty || !replacement.registered || replacement.identity != published {
		t.Fatalf("replacement observation=%+v published=%+v; want an empty, registered object matching publication", replacement, published)
	}
}

func TestRealSettingsStoreCleanupPendingBlocksPublicationAndRetryRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	session := &ownerLifecycleTestSession{registry: registry, policy: registry.View().Generation, firstRegister: readbrokerstudy.ErrCleanupPending}
	protector := &observedSettingsProtector{delegate: studyOwnerProtection{session: session, registry: registry}}
	store := settings.Store{Path: path, Protection: protector}
	protector.arm(false)
	firstErr := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI})
	protector.disarm()
	first := protector.lastReplacement()
	if !errors.Is(firstErr, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("first settings save error=%v, want cleanup pending", firstErr)
	}
	if _, err := identityAt(path); err == nil {
		t.Fatal("settings destination was published before cleanup completed")
	}
	protector.arm(false)
	if err := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		t.Fatal(err)
	}
	protector.disarm()
	second := protector.lastReplacement()
	published, err := identityAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.identity == (readbrokerstudy.FileIdentity{}) || second.identity == first.identity || second.identity != published || !second.empty || !second.registered {
		t.Fatalf("first=%+v second=%+v published=%+v; want a fresh registered replacement after refusal", first, second, published)
	}
	if !hasSettingsPin(registry.View(), first.identity) {
		t.Fatal("retired settings temporary identity lost its registry pin")
	}
}
