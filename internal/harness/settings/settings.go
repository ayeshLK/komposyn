// Package settings loads the persistent, user-owned harness preferences.
// Conversation and model selection state are intentionally absent.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
)

const SchemaVersion = 1
const DefaultUI = "tview"

type Settings struct {
	Plugin string `json:"plugin"`
}
type Store struct{ Path string }

func DefaultPath() (string, error) {
	authPath, err := auth.DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(authPath), "settings.json"), nil
}
func (s Store) path() (string, error) {
	if s.Path != "" {
		return filepath.Abs(s.Path)
	}
	p, e := DefaultPath()
	if e != nil {
		return "", e
	}
	return filepath.Abs(p)
}

func (s Store) Load() (Settings, error) {
	path, err := s.path()
	if err != nil {
		return Settings{}, err
	}
	var obj map[string]json.RawMessage
	exists, err := auth.ReadJSON(path, &obj)
	if err != nil {
		return Settings{}, err
	}
	if !exists {
		return Settings{Plugin: DefaultUI}, nil
	}
	if obj == nil {
		return Settings{}, errors.New("settings file must be a JSON object")
	}
	return parse(obj)
}

func parse(obj map[string]json.RawMessage) (Settings, error) {
	var version int
	if raw, ok := obj["schema_version"]; !ok || json.Unmarshal(raw, &version) != nil {
		return Settings{}, errors.New("settings schema_version must be an integer")
	}
	if version != SchemaVersion {
		return Settings{}, fmt.Errorf("unsupported settings schema version %d", version)
	}
	out := Settings{Plugin: DefaultUI}
	raw, ok := obj["ui"]
	if !ok {
		return out, nil
	}
	var ui map[string]json.RawMessage
	if json.Unmarshal(raw, &ui) != nil || ui == nil {
		return Settings{}, errors.New("settings ui must be an object")
	}
	plugin, ok := ui["plugin"]
	if !ok {
		return out, nil
	}
	if json.Unmarshal(plugin, &out.Plugin) != nil || out.Plugin == "" {
		return Settings{}, errors.New("settings ui.plugin must be a non-empty string")
	}
	if out.Plugin != DefaultUI {
		return Settings{}, fmt.Errorf("unknown UI plugin %q", out.Plugin)
	}
	return out, nil
}

func (s Store) Save(value Settings) error {
	if value.Plugin == "" {
		return errors.New("UI plugin ID is required")
	}
	if value.Plugin != DefaultUI {
		return fmt.Errorf("unknown UI plugin %q", value.Plugin)
	}
	path, err := s.path()
	if err != nil {
		return err
	}
	return auth.UpdateJSON(path, func(exists bool, obj map[string]json.RawMessage) error {
		if exists {
			if _, err := parse(obj); err != nil {
				return err
			}
		}
		if raw, ok := obj["schema_version"]; ok {
			var version int
			if json.Unmarshal(raw, &version) != nil {
				return errors.New("settings schema_version must be an integer")
			}
			if version != SchemaVersion {
				return fmt.Errorf("unsupported settings schema version %d", version)
			}
		} else {
			obj["schema_version"], _ = json.Marshal(SchemaVersion)
		}
		ui := map[string]json.RawMessage{}
		if raw, ok := obj["ui"]; ok {
			if json.Unmarshal(raw, &ui) != nil || ui == nil {
				return errors.New("settings ui must be an object")
			}
		}
		plugin, _ := json.Marshal(value.Plugin)
		ui["plugin"] = plugin
		raw, _ := json.Marshal(ui)
		obj["ui"] = raw
		return nil
	})
}
