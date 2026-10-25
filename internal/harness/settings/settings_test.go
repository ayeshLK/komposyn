package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
)

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

func TestInvalidSettingsDoNotResetFile(t *testing.T) {
	for _, content := range []string{`{"schema_version":1,"ui":{"plugin":""}}`, `{"schema_version":9}`, `{"schema_version":1,"ui":[]}`} {
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
