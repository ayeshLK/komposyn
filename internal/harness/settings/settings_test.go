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

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
)

type settingsProtectionFunc func(context.Context, string, string, string, *os.File) error

func (f settingsProtectionFunc) Protect(ctx context.Context, owner, class, route string, file *os.File) error {
	return f(ctx, owner, class, route, file)
}

func TestDefaultsSaveAndPreservesOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Store{Path: path}
	got, err := s.Load()
	if err != nil || got.Plugin != "tview" {
		t.Fatalf("default: %+v %v", got, err)
	}
	if err := auth.WriteJSON(path, json.RawMessage(`{"schema_version":1,"extension":{"x":true},"ui":{"theme":"dark"}}`)); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Load(); err != nil || got.Plugin != "tview" {
		t.Fatalf("missing plugin default: %+v %v", got, err)
	}
	if err = s.Save(Settings{Plugin: "tview"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"extension"`) || !strings.Contains(string(raw), `"theme": "dark"`) {
		t.Fatalf("unrelated fields were lost: %s", raw)
	}
}

func TestSaveContextRegistersEmptyOverrideBeforeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "override.json")
	calls := 0
	store := Store{Path: path, Protection: settingsProtectionFunc(func(_ context.Context, owner, class, route string, file *os.File) error {
		calls++
		if owner != auth.SensitiveOwnerSettings || class != auth.SensitiveClassSettings || route != path || file == nil {
			t.Fatalf("unexpected protection call: %q %q %q %v", owner, class, route, file)
		}
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Size() != 0 {
			t.Fatalf("settings bytes written before registration: size=%d", info.Size())
		}
		return nil
	})}
	if err := store.SaveContext(context.Background(), Settings{Plugin: DefaultUI}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("protection calls=%d", calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSaveContextRefusesWhenOwnerBarrierIsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := errors.New("cleanup pending")
	store := Store{Path: path, Protection: settingsProtectionFunc(func(context.Context, string, string, string, *os.File) error { return want })}
	if err := store.SaveContext(context.Background(), Settings{Plugin: DefaultUI}); !errors.Is(err, want) {
		t.Fatalf("SaveContext error=%v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("settings appeared despite failed barrier: %v", err)
	}
}

func TestInvalidSettingsDoNotResetFile(t *testing.T) {
	for _, content := range []string{`{"schema_version":1,"ui":{"plugin":""}}`, `{"schema_version":1,"ui":{"plugin":null}}`, `{"schema_version":9}`, `{"schema_version":1,"ui":[]}`} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := auth.WriteJSON(path, json.RawMessage(content)); err != nil {
			t.Fatal(err)
		}
		s := Store{Path: path}
		if _, err := s.Load(); err == nil {
			t.Fatalf("accepted %s", content)
		}
		before, _ := os.ReadFile(path)
		if err := s.Save(Settings{Plugin: "tview"}); err == nil {
			t.Fatalf("overwrote invalid settings %s", content)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatalf("invalid settings changed: %s", content)
		}
	}
}
