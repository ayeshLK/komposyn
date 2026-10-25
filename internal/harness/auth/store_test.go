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
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestVersionOneCredentialStoreUpgradesWithoutLosingProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	legacy := `{"schema_version":1,"credentials":[{"id":"old-openai","provider":"openai","label":"Old","auth":{"type":"api_key","api_key":"keep-this-key"}}]}`
	var legacyDocument document
	if err := json.Unmarshal([]byte(legacy), &legacyDocument); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, legacyDocument); err != nil {
		t.Fatal(err)
	}
	s := Store{Path: path}
	profile, value, err := s.Resolve("old-openai")
	if err != nil || value != "keep-this-key" || profile.Auth.Type != "api_key" {
		t.Fatalf("legacy resolve: %+v %q %v", profile, value, err)
	}
	if err := s.AddAPIKey("new-anthropic", "anthropic", "New", "new-key", nil); err != nil {
		t.Fatal(err)
	}
	var upgraded document
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &upgraded); err != nil {
		t.Fatal(err)
	}
	if upgraded.SchemaVersion != SchemaVersion || len(upgraded.Credentials) != 2 {
		t.Fatalf("upgrade=%+v", upgraded)
	}
	_, value, err = s.Resolve("old-openai")
	if err != nil || value != "keep-this-key" {
		t.Fatalf("legacy key lost after upgrade: %q %v", value, err)
	}
}

func TestOAuthProfileMetadataAndRefreshKeepStableIdentity(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	hostID, err := s.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(-time.Minute)
	profile := Profile{ID: "chatgpt-account-1", Provider: "openai", Label: "OpenAI OAuth", Revision: revision, Auth: AuthConfig{Type: "oauth", OAuth: &OAuthCredential{ClientID: "oaiapp_issued", Issuer: "https://auth.openai.com", Subject: "subject-1", Email: "person@example.com", HostID: hostID, IDToken: "id-secret", AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer", Scopes: []string{"openid", "chatgpt.tokens.use.direct"}, ExpiresAt: expires}}}
	if err := s.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.ListMetadata()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(metadata)
	if strings.Contains(string(encoded), "secret") || len(metadata) != 1 || metadata[0].Status != "connected" || !metadata[0].PlanUsageEnabled {
		t.Fatalf("unsafe/inaccurate metadata: %s", encoded)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls int
	var mu sync.Mutex
	refresh := func(current OAuthCredential) (OAuthTokens, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if current.RefreshToken != "refresh-secret" {
			t.Errorf("stale refresh token: %q", current.RefreshToken)
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return OAuthTokens{AccessToken: "next-access", RefreshToken: "next-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	first := make(chan error, 1)
	go func() { _, err := s.RefreshOAuth(profile.ID, time.Now(), refresh); first <- err }()
	<-entered
	second := make(chan error, 1)
	go func() { _, err := s.RefreshOAuth(profile.ID, time.Now(), refresh); second <- err }()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count := calls
	mu.Unlock()
	if count != 1 {
		t.Fatalf("refresh endpoint called %d times", count)
	}
	updated, err := s.Get(profile.ID)
	if err != nil || updated.Revision != revision || updated.Auth.OAuth.Subject != "subject-1" || updated.Auth.OAuth.AccessToken != "next-access" || updated.Auth.OAuth.RefreshToken != "next-refresh" {
		t.Fatalf("rotated profile=%+v err=%v", updated, err)
	}
}

func TestFailedOAuthRefreshPreservesCredentialBytes(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	hostID, err := s.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := NewOAuthRevision()
	profile := Profile{ID: "oauth-test", Provider: "openai", Label: "ChatGPT", Revision: revision, Auth: AuthConfig{Type: "oauth", OAuth: &OAuthCredential{ClientID: "issued", Issuer: "https://auth.openai.com", Subject: "sub", HostID: hostID, AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Second)}}}
	if err := s.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RefreshOAuth(profile.ID, time.Now(), func(OAuthCredential) (OAuthTokens, error) { return OAuthTokens{}, context.DeadlineExceeded })
	if err == nil {
		t.Fatal("temporary refresh failure unexpectedly succeeded")
	}
	after, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(want) {
		t.Fatal("failed refresh changed stored credentials")
	}
}
