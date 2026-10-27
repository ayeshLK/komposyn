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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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

func TestExistingCredentialStorePreservesProfilesWithoutVersionBump(t *testing.T) {
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
	var updated document
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.SchemaVersion != SchemaVersion || len(updated.Credentials) != 2 {
		t.Fatalf("updated store=%+v", updated)
	}
	_, value, err = s.Resolve("old-openai")
	if err != nil || value != "keep-this-key" {
		t.Fatalf("existing key lost after update: %q %v", value, err)
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
	if strings.Contains(string(encoded), "secret") || len(metadata) != 1 || metadata[0].Status != "connected" {
		t.Fatalf("unsafe/inaccurate metadata: %s", encoded)
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls int
	var mu sync.Mutex
	refresh := func(current OAuthCredential) (OAuthTokens, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if current.RefreshToken != "refresh-secret" {
			t.Errorf("stale refresh token: %q", current.RefreshToken)
		}
		entered <- struct{}{}
		<-release
		return OAuthTokens{AccessToken: "next-access", RefreshToken: "next-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	first := make(chan error, 1)
	go func() { _, err := s.RefreshOAuth(profile.ID, time.Now(), refresh); first <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach the token endpoint")
	}
	second := make(chan error, 1)
	go func() { _, err := s.RefreshOAuth(profile.ID, time.Now(), refresh); second <- err }()
	unblock()
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

func TestRefreshOAuthContextCancelsWhileWaitingForLock(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	profile := addExpiringStoreOAuthProfile(t, s, time.Now().Add(-time.Minute))
	before, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockPath(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		close(started)
		_, refreshErr := s.RefreshOAuthContext(ctx, profile.ID, time.Now, func(OAuthCredential) (OAuthTokens, error) {
			calls.Add(1)
			return OAuthTokens{}, nil
		})
		done <- refreshErr
	}()
	<-started
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			unlock()
			t.Fatalf("refresh wait error=%v", err)
		}
	case <-time.After(time.Second):
		unlock()
		t.Fatal("refresh did not leave the canceled lock wait")
	}
	unlock()
	if calls.Load() != 0 {
		t.Fatalf("refresh callback called %d times after cancellation", calls.Load())
	}
	after, err := os.ReadFile(s.Path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("canceled lock wait changed credentials: err=%v", err)
	}
}

func TestRefreshOAuthContextRechecksExpiryAfterLock(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	base := time.Now().Truncate(time.Second)
	profile := addExpiringStoreOAuthProfile(t, s, base.Add(100*time.Second))
	unlock, err := lockPath(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	var current atomic.Int64
	current.Store(base.UnixNano())
	started := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		close(started)
		_, refreshErr := s.RefreshOAuthContext(context.Background(), profile.ID, func() time.Time {
			return time.Unix(0, current.Load())
		}, func(OAuthCredential) (OAuthTokens, error) {
			calls.Add(1)
			return OAuthTokens{AccessToken: "new-access", RefreshToken: "new-refresh", TokenType: "Bearer", ExpiresAt: time.Unix(0, current.Load()).Add(time.Hour)}, nil
		})
		done <- refreshErr
	}()
	<-started
	time.Sleep(30 * time.Millisecond)
	current.Store(base.Add(20 * time.Second).UnixNano())
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not complete after lock release")
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh callback called %d times; expiry was checked using stale time", calls.Load())
	}
}

func addExpiringStoreOAuthProfile(t *testing.T, s Store, expires time.Time) Profile {
	t.Helper()
	hostID, err := s.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{ID: "oauth-refresh-test", Provider: "openai", Label: "OAuth", Revision: revision, Auth: AuthConfig{Type: "oauth", OAuth: &OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "subject", HostID: hostID, AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: expires}}}
	if err := s.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	return profile
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

func TestOAuthLifecycleGenerationRejectsStaleReconnects(t *testing.T) {
	t.Run("disconnect wins over pending reconnect", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth.json")
		first, second := Store{Path: path}, Store{Path: path}
		profile := addStoreOAuthProfile(t, first)
		candidate := cloneProfile(profile)
		candidate.Auth.OAuth.AccessToken = "candidate-access"
		candidate.Auth.OAuth.RefreshToken = "candidate-refresh"
		candidate.Auth.OAuth.ExpiresAt = time.Now().Add(time.Hour)

		if err := second.ClearOAuthTokens(profile.ID); err != nil {
			t.Fatal(err)
		}
		if err := first.ReplaceOAuth(profile.ID, profile.Revision, candidate); !errors.Is(err, ErrOAuthProfileChanged) {
			t.Fatalf("stale reconnect error=%v", err)
		}
		stored, err := second.Get(profile.ID)
		if err != nil || stored.Auth.OAuth.AccessToken != "" || stored.Auth.OAuth.RefreshToken != "" || !stored.Auth.OAuth.ReconnectRequired || stored.Auth.OAuth.LifecycleGeneration != 2 {
			t.Fatalf("disconnect was overwritten: %+v %v", stored, err)
		}
	})

	t.Run("older reconnect cannot replace newer reconnect", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth.json")
		first, second := Store{Path: path}, Store{Path: path}
		profile := addStoreOAuthProfile(t, first)
		candidateA, candidateB := cloneProfile(profile), cloneProfile(profile)
		candidateA.Auth.OAuth.AccessToken, candidateA.Auth.OAuth.RefreshToken = "access-a", "refresh-a"
		candidateB.Auth.OAuth.AccessToken, candidateB.Auth.OAuth.RefreshToken = "access-b", "refresh-b"
		candidateA.Auth.OAuth.ExpiresAt = time.Now().Add(time.Hour)
		candidateB.Auth.OAuth.ExpiresAt = time.Now().Add(time.Hour)

		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() { <-start; errs <- first.ReplaceOAuth(profile.ID, profile.Revision, candidateA) }()
		go func() { <-start; errs <- second.ReplaceOAuth(profile.ID, profile.Revision, candidateB) }()
		close(start)
		errA, errB := <-errs, <-errs
		if (errA == nil) == (errB == nil) {
			t.Fatalf("expected one reconnect commit, got errors %v and %v", errA, errB)
		}
		if errA != nil && !errors.Is(errA, ErrOAuthProfileChanged) {
			t.Fatalf("first reconnect error=%v", errA)
		}
		if errB != nil && !errors.Is(errB, ErrOAuthProfileChanged) {
			t.Fatalf("second reconnect error=%v", errB)
		}
		stored, err := first.Get(profile.ID)
		if err != nil || stored.Auth.OAuth.LifecycleGeneration != 2 || (stored.Auth.OAuth.AccessToken != "access-a" && stored.Auth.OAuth.AccessToken != "access-b") {
			t.Fatalf("concurrent reconnect result=%+v %v", stored, err)
		}
	})
}

func TestOAuthMutationCancellationWhileWaitingForStoreLock(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		name := "add"
		if reconnect {
			name = "reconnect"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			store := Store{Path: path}
			profile := addStoreOAuthProfile(t, store)
			if !reconnect {
				if err := store.Remove(profile.ID); err != nil {
					t.Fatal(err)
				}
			}
			candidate := cloneProfile(profile)
			candidate.Auth.OAuth.AccessToken = "replacement-access"
			candidate.Auth.OAuth.RefreshToken = "replacement-refresh"
			candidate.Auth.OAuth.ExpiresAt = time.Now().Add(time.Hour)

			unlock, err := lockPath(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				if reconnect {
					result <- store.ReplaceOAuthContext(ctx, profile.ID, profile.Revision, candidate)
				} else {
					result <- store.AddOAuthContext(ctx, profile)
				}
			}()
			time.Sleep(30 * time.Millisecond)
			cancel()
			unlock()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mutation error=%v", err)
			}
			if reconnect {
				stored, err := store.Get(profile.ID)
				if err != nil || stored.Auth.OAuth.AccessToken != profile.Auth.OAuth.AccessToken || stored.Auth.OAuth.LifecycleGeneration != profile.Auth.OAuth.LifecycleGeneration {
					t.Fatalf("canceled reconnect changed profile: %+v %v", stored, err)
				}
			} else if _, err := store.Get(profile.ID); err == nil {
				t.Fatal("canceled add persisted a profile")
			}
		})
	}
}

func addStoreOAuthProfile(t *testing.T, store Store) Profile {
	t.Helper()
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{ID: "oauth-lifecycle", Provider: "openai", Label: "ChatGPT", Revision: revision, Auth: AuthConfig{Type: "oauth", OAuth: &OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "stable-subject", HostID: hostID, IDToken: "id-token", AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	return profile
}
