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
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestProfileLifecycleAndSecretFreeMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "auth.json")
	s := Store{Path: path}
	if err := s.AddAPIKey("oai-personal", "openai", "Personal", "sk-secret-value", map[string]string{"organization": "org-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAPIKey("anthropic-work", "anthropic", "Work", "ant-secret-value", nil); err != nil {
		t.Fatal(err)
	}
	meta, err := s.ListMetadata()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(meta)
	if strings.Contains(string(raw), "secret-value") {
		t.Fatal("metadata included a credential")
	}
	if len(meta) != 2 {
		t.Fatalf("profiles=%d", len(meta))
	}
	p, key, err := s.Resolve("oai-personal")
	if err != nil || key != "sk-secret-value" || p.Route["organization"] != "org-1" {
		t.Fatalf("resolve: profile=%+v key=%q err=%v", p, key, err)
	}
	if err := s.ReplaceAPIKey("oai-personal", "Personal updated", "new-key", nil); err != nil {
		t.Fatal(err)
	}
	p, key, err = s.Resolve("oai-personal")
	if err != nil || key != "new-key" || p.Route["organization"] != "org-1" {
		t.Fatalf("replace lost routing metadata: %+v %q %v", p, key, err)
	}
	if err := s.Remove("anthropic-work"); err != nil {
		t.Fatal(err)
	}
	meta, err = s.ListMetadata()
	if err != nil || len(meta) != 1 {
		t.Fatalf("after remove: %v %v", meta, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if err := checkPrivateFile(path, info); err != nil {
			t.Fatalf("auth file ACL is not private: %v", err)
		}
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("auth file mode=%o", info.Mode().Perm())
	}
}

func TestConcurrentAddsDoNotLoseUpdates(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.AddAPIKey("profile-"+string(rune('a'+i)), "openai", "Profile", "key", nil)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := s.ListMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != n {
		t.Fatalf("lost update: got %d profiles", len(profiles))
	}
}

func TestMalformedUnsupportedAndSymlinkStoresArePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	bad := []byte(`{"schema_version":99,"credentials":[]}`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Store{Path: path}
	if err := s.AddAPIKey("x", "openai", "x", "secret", nil); err == nil {
		t.Fatal("unsupported version accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(bad) {
		t.Fatal("failed update changed unsupported store")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":1,"credentials":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := s.ListMetadata(); err == nil {
		t.Fatal("symlink store accepted")
	}
}

func TestEnvironmentReferenceDoesNotCopyValue(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "auth.json"), LookupEnv: func(k string) (string, bool) { return "env-secret", k == "OPENAI_API_KEY" }}
	if err := s.Add(Profile{ID: "env", Provider: "openai", Label: "Env", Auth: AuthConfig{Type: "env", Env: "OPENAI_API_KEY"}}); err != nil {
		t.Fatal(err)
	}
	p, key, err := s.Resolve("env")
	if err != nil || key != "env-secret" || p.Auth.APIKey != "" {
		t.Fatalf("resolve env ref: %+v %q %v", p, key, err)
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "env-secret") {
		t.Fatal("environment value persisted")
	}
}
