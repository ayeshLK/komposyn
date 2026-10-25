// Package auth owns local credential profile validation, persistence, and
// scoped resolution. Providers receive one credential per operation.
package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

const SchemaVersion = 1

var profileIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type AuthConfig struct {
	Type   string `json:"type"`
	APIKey string `json:"api_key,omitempty"`
	Env    string `json:"env,omitempty"`
}

type Profile struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Label    string            `json:"label"`
	Route    map[string]string `json:"routing,omitempty"`
	Auth     AuthConfig        `json:"auth"`
}

type document struct {
	SchemaVersion int       `json:"schema_version"`
	Credentials   []Profile `json:"credentials"`
}

type Metadata struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Label    string            `json:"label"`
	Route    map[string]string `json:"routing,omitempty"`
	AuthType string            `json:"auth_type"`
}

type Store struct {
	Path      string
	LookupEnv func(string) (string, bool)
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if u, e := user.Current(); e == nil {
			home = u.HomeDir
		} else {
			return "", errors.New("user home directory is unavailable")
		}
	}
	return filepath.Join(home, ".komposyn", "auth.json"), nil
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

func (s Store) ListMetadata() ([]Metadata, error) {
	doc, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Metadata, 0, len(doc.Credentials))
	for _, p := range doc.Credentials {
		out = append(out, Metadata{ID: p.ID, Provider: p.Provider, Label: p.Label, Route: cloneRoute(p.Route), AuthType: p.Auth.Type})
	}
	return out, nil
}

func (s Store) Resolve(id string) (Profile, string, error) {
	doc, err := s.readLocked()
	if err != nil {
		return Profile{}, "", err
	}
	for _, p := range doc.Credentials {
		if p.ID != id {
			continue
		}
		switch p.Auth.Type {
		case "api_key":
			return p, p.Auth.APIKey, nil
		case "env":
			lookup := s.LookupEnv
			if lookup == nil {
				lookup = os.LookupEnv
			}
			value, ok := lookup(p.Auth.Env)
			if !ok || strings.TrimSpace(value) == "" {
				return Profile{}, "", errors.New("credential environment reference is unavailable")
			}
			return p, value, nil
		default:
			return Profile{}, "", errors.New("unsupported credential authentication type")
		}
	}
	return Profile{}, "", errors.New("credential profile was not found")
}

func (s Store) Add(profile Profile) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	return s.update(func(doc *document) error {
		for _, p := range doc.Credentials {
			if p.ID == profile.ID {
				return errors.New("credential profile ID already exists")
			}
		}
		doc.Credentials = append(doc.Credentials, cloneProfile(profile))
		return nil
	})
}

func (s Store) AddAPIKey(id, providerID, label, key string, route map[string]string) error {
	return s.Add(Profile{ID: id, Provider: providerID, Label: label, Route: route, Auth: AuthConfig{Type: "api_key", APIKey: key}})
}

func (s Store) ReplaceAPIKey(id, label, key string, route map[string]string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("API key is required")
	}
	return s.update(func(doc *document) error {
		for i, p := range doc.Credentials {
			if p.ID == id {
				p.Label = label
				if route != nil {
					p.Route = cloneRoute(route)
				}
				p.Auth = AuthConfig{Type: "api_key", APIKey: key}
				if err := validateProfile(p); err != nil {
					return err
				}
				doc.Credentials[i] = p
				return nil
			}
		}
		return errors.New("credential profile was not found")
	})
}

func (s Store) Remove(id string) error {
	return s.update(func(doc *document) error {
		for i, p := range doc.Credentials {
			if p.ID == id {
				doc.Credentials = append(doc.Credentials[:i], doc.Credentials[i+1:]...)
				return nil
			}
		}
		return errors.New("credential profile was not found")
	})
}

func validateProfile(p Profile) error {
	if !profileIDPattern.MatchString(p.ID) {
		return errors.New("credential profile ID must use 1-64 letters, digits, dot, underscore, or hyphen")
	}
	if p.Provider != "openai" && p.Provider != "anthropic" {
		return errors.New("unsupported credential provider")
	}
	if strings.TrimSpace(p.Label) == "" || len(p.Label) > 120 {
		return errors.New("credential profile label is required and must be at most 120 characters")
	}
	for k, v := range p.Route {
		switch k {
		case "organization", "project":
			if strings.TrimSpace(v) == "" || len(v) > 256 {
				return fmt.Errorf("invalid provider routing value for %s", k)
			}
		default:
			return fmt.Errorf("unsupported provider routing field %q", k)
		}
	}
	if p.Provider == "anthropic" && len(p.Route) > 0 {
		return errors.New("Anthropic API keys do not support routing metadata")
	}
	switch p.Auth.Type {
	case "api_key":
		if strings.TrimSpace(p.Auth.APIKey) == "" || p.Auth.Env != "" {
			return errors.New("api_key authentication requires only a non-empty api_key")
		}
	case "env":
		if p.Auth.APIKey != "" || !envNamePattern.MatchString(p.Auth.Env) {
			return errors.New("env authentication requires a valid variable name")
		}
	default:
		return errors.New("unsupported credential authentication type")
	}
	return nil
}

func (s Store) readLocked() (*document, error) {
	path, err := s.path()
	if err != nil {
		return nil, err
	}
	if err = ensurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	unlock, err := lockPath(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return readFile(path)
}

func (s Store) update(change func(*document) error) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	unlock, err := lockPath(path)
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := readFile(path)
	if err != nil {
		return err
	}
	if err = change(doc); err != nil {
		return err
	}
	if err = validateDocument(doc); err != nil {
		return err
	}
	return writeAtomic(path, doc)
}

func readFile(path string) (*document, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return &document{SchemaVersion: SchemaVersion, Credentials: []Profile{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("credential store must be a regular file, not a symlink")
	}
	if err := checkPrivateFile(path, info); err != nil {
		return nil, err
	}
	f, err := openPrivateFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("credential store must be a regular file")
	}
	if err = checkPrivateFile(path, opened); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 2<<20))
	if err != nil {
		return nil, errors.New("unable to read credential store")
	}
	if len(data) >= 2<<20 {
		return nil, errors.New("credential store exceeds the size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.New("credential store is malformed")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("credential store contains trailing data")
	}
	if doc.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported credential store schema version %d", doc.SchemaVersion)
	}
	if err := validateDocument(&doc); err != nil {
		return nil, fmt.Errorf("invalid credential store: %w", err)
	}
	return &doc, nil
}

func validateDocument(doc *document) error {
	if doc.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", doc.SchemaVersion)
	}
	if doc.Credentials == nil {
		return errors.New("credentials must be an array")
	}
	seen := map[string]bool{}
	for _, p := range doc.Credentials {
		if err := validateProfile(p); err != nil {
			return err
		}
		if seen[p.ID] {
			return errors.New("duplicate credential profile ID")
		}
		seen[p.ID] = true
	}
	return nil
}
func cloneProfile(p Profile) Profile { p.Route = cloneRoute(p.Route); return p }
func cloneRoute(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ReadJSON and WriteJSON share the private, locked, atomic configuration-file
// boundary with settings. They expose no path or JSON errors that could contain
// a credential value.
func ReadJSON(path string, target any) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	if err = ensurePrivateDir(filepath.Dir(abs)); err != nil {
		return false, err
	}
	unlock, err := lockPath(abs)
	if err != nil {
		return false, err
	}
	defer unlock()
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("configuration file must be a regular file")
	}
	if err := checkPrivateFile(abs, info); err != nil {
		return false, err
	}
	f, err := openPrivateFile(abs)
	if err != nil {
		return false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !opened.Mode().IsRegular() {
		return false, errors.New("configuration file must be a regular file")
	}
	if err = checkPrivateFile(abs, opened); err != nil {
		return false, err
	}
	dec := json.NewDecoder(io.LimitReader(f, 2<<20))
	if err = dec.Decode(target); err != nil {
		return false, errors.New("configuration file is malformed")
	}
	if dec.Decode(new(any)) != io.EOF {
		return false, errors.New("configuration file contains trailing data")
	}
	return true, nil
}
func WriteJSON(path string, value any) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = ensurePrivateDir(filepath.Dir(abs)); err != nil {
		return err
	}
	unlock, err := lockPath(abs)
	if err != nil {
		return err
	}
	defer unlock()
	return writeAtomic(abs, value)
}

// UpdateJSON atomically performs a locked read/modify/write on a private JSON
// object. It is used by small settings stores that preserve unknown fields.
func UpdateJSON(path string, update func(bool, map[string]json.RawMessage) error) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = ensurePrivateDir(filepath.Dir(abs)); err != nil {
		return err
	}
	unlock, err := lockPath(abs)
	if err != nil {
		return err
	}
	defer unlock()
	raw := map[string]json.RawMessage{}
	exists := false
	if _, err = os.Lstat(abs); err == nil {
		exists = true
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("configuration file must be a regular file")
		}
		if err = checkPrivateFile(abs, info); err != nil {
			return err
		}
		f, err := openPrivateFile(abs)
		if err != nil {
			return err
		}
		opened, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if !opened.Mode().IsRegular() {
			f.Close()
			return errors.New("configuration file must be a regular file")
		}
		if err = checkPrivateFile(abs, opened); err != nil {
			f.Close()
			return err
		}
		b, err := io.ReadAll(io.LimitReader(f, 2<<20))
		_ = f.Close()
		if err != nil {
			return err
		}
		if len(b) > 2<<20 {
			return errors.New("configuration file exceeds the size limit")
		}
		if err = json.Unmarshal(b, &raw); err != nil || raw == nil {
			return errors.New("configuration file is malformed")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = update(exists, raw); err != nil {
		return err
	}
	return writeAtomic(abs, raw)
}
