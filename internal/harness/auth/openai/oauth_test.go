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

package openai

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	jose "github.com/go-jose/go-jose/v4"
)

func TestLoginRegistersAndStoresOnlyValidatedCandidate(t *testing.T) {
	var authQuery url.Values
	var tokenForm url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			tokenForm = r.Form
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "oaiapp-issued" || r.Form.Get("code") != "one-time-code" || r.Form.Get("resource") != resourceURL {
				t.Errorf("token form=%v", r.Form)
			}
			verifier := r.Form.Get("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != authQuery.Get("code_challenge") || r.Form.Get("redirect_uri") != authQuery.Get("redirect_uri") {
				t.Errorf("PKCE/redirect mismatch: form=%v auth=%v", r.Form, authQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"access-secret","refresh_token":"refresh-secret","id_token":"validated-id-token","token_type":"Bearer","expires_in":3600,"scope":"openid email profile offline_access resource.invoke chatgpt.tokens.use.direct"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	manager := New(store)
	manager.authorize = "https://auth.openai.com/api/accounts/authorize"
	manager.token = api.URL + "/token"
	manager.client = api.Client()
	manager.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	manager.verifyIdentity = func(_ context.Context, clientID, raw, nonce string) (verifiedIdentity, error) {
		if clientID != "oaiapp-issued" || raw != "validated-id-token" || nonce == "" {
			return verifiedIdentity{}, fmt.Errorf("invalid test identity")
		}
		return verifiedIdentity{subject: "stable-subject", email: "person@example.com"}, nil
	}
	manager.browser = func(target string) error {
		parsed, err := url.Parse(target)
		if err != nil {
			return err
		}
		authQuery = parsed.Query()
		if authQuery.Get("client_id") != registrationClientID || authQuery.Get("agent_name_hint") != "KompoSyn" || authQuery.Get("ext_agent_host_id") == "" || authQuery.Get("response_type") != "code" || authQuery.Get("scope") == "" || authQuery.Get("resource") != resourceURL || authQuery.Get("code_challenge_method") != "S256" {
			t.Errorf("authorization request=%v", authQuery)
		}
		if strings.Contains(target, "access_token") || strings.Contains(target, "refresh_token") {
			t.Error("authorization URL exposed tokens")
		}
		redirect, err := url.Parse(authQuery.Get("redirect_uri"))
		if err != nil {
			return err
		}
		if redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Path != callbackPath {
			t.Errorf("redirect URI=%s", redirect)
		}
		values := url.Values{"code": {"one-time-code"}, "state": {authQuery.Get("state")}, "client_id": {"oaiapp-issued"}}
		for i := 0; i < 30; i++ {
			resp, callErr := http.Get(redirect.String() + "?" + values.Encode())
			if callErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
				return fmt.Errorf("callback status %d", resp.StatusCode)
			}
			if !strings.Contains(callErr.Error(), "connection refused") {
				return callErr
			}
			time.Sleep(10 * time.Millisecond)
		}
		return fmt.Errorf("callback listener did not start")
	}

	profile, err := manager.Login(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID == "" || profile.Auth.OAuth == nil || profile.Auth.OAuth.Subject != "stable-subject" || profile.Auth.OAuth.ClientID != "oaiapp-issued" || profile.Auth.OAuth.Email != "person@example.com" {
		t.Fatalf("profile=%+v", profile)
	}
	if profile.Auth.OAuth.AccessToken != "access-secret" || profile.Auth.OAuth.RefreshToken != "refresh-secret" || len(tokenForm) == 0 {
		t.Fatal("token response was not stored")
	}
	metadata, err := store.ListMetadata()
	if err != nil || len(metadata) != 1 || !metadata[0].PlanUsageEnabled || metadata[0].Status != "connected" {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "access-secret") || strings.Contains(string(raw), "refresh-secret") || strings.Contains(string(raw), "validated-id-token") {
		t.Fatal("public metadata included secret credentials")
	}
}

func TestLoginCancellationAndWrongStateDoNotPersistCandidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  func(string) string
		cancel bool
	}{
		{name: "canceled", cancel: true},
		{name: "wrong-state", state: func(string) string { return "wrong-state" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := auth.Store{Path: t.TempDir() + "/auth.json"}
			manager := New(store)
			manager.timeout = 2 * time.Second
			manager.verifyIdentity = func(context.Context, string, string, string) (verifiedIdentity, error) {
				return verifiedIdentity{subject: "never-saved"}, nil
			}
			browserStarted := make(chan struct{})
			manager.browser = func(target string) error {
				close(browserStarted)
				if tc.cancel {
					return nil
				}
				parsed, _ := url.Parse(target)
				query := parsed.Query()
				redirect, _ := url.Parse(query.Get("redirect_uri"))
				state := query.Get("state")
				if tc.state != nil {
					state = tc.state(state)
				}
				values := url.Values{"code": {"code"}, "state": {state}, "client_id": {"oaiapp"}}
				for i := 0; i < 20; i++ {
					resp, err := http.Get(redirect.String() + "?" + values.Encode())
					if err == nil {
						_ = resp.Body.Close()
						return nil
					}
					time.Sleep(10 * time.Millisecond)
				}
				return fmt.Errorf("listener missing")
			}
			manager.token = "http://127.0.0.1:1/token"
			attempt, err := manager.StartLogin(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			<-browserStarted
			if tc.cancel {
				if !manager.CancelLogin(attempt.ID) {
					t.Fatal("active attempt was not canceled")
				}
			}
			result := <-attempt.Done
			if result.Err == nil {
				t.Fatal("invalid/canceled flow unexpectedly succeeded")
			}
			metadata, err := store.ListMetadata()
			if err != nil {
				t.Fatal(err)
			}
			if len(metadata) != 0 {
				t.Fatalf("candidate persisted: %+v", metadata)
			}
		})
	}
}

func TestExpiredAndMisboundIDTokensAreRejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	publicJWK := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig"}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q}`, issuer, issuer+"/jwks")
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{publicJWK}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	issuer = api.URL
	manager := New(auth.Store{})
	manager.issuer = issuer
	manager.client = api.Client()
	makeToken := func(signingKey *rsa.PrivateKey, claims map[string]any) string {
		signer, signErr := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, nil)
		if signErr != nil {
			t.Fatal(signErr)
		}
		payload, marshalErr := json.Marshal(claims)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		object, signErr := signer.Sign(payload)
		if signErr != nil {
			t.Fatal(signErr)
		}
		compact, signErr := object.CompactSerialize()
		if signErr != nil {
			t.Fatal(signErr)
		}
		return compact
	}
	baseClaims := func() map[string]any {
		return map[string]any{"iss": issuer, "sub": "sub-1", "aud": "issued-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": "nonce-1", "email": "person@example.com", "email_verified": true}
	}
	valid := makeToken(key, baseClaims())
	identity, err := manager.validateIdentity(context.Background(), "issued-client", valid, "nonce-1")
	if err != nil || identity.subject != "sub-1" || identity.email != "person@example.com" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	wrongAudience := baseClaims()
	wrongAudience["aud"] = "another-client"
	expired := baseClaims()
	expired["exp"] = time.Now().Add(-time.Hour).Unix()
	wrongIssuer := baseClaims()
	wrongIssuer["iss"] = "https://attacker.invalid"
	for name, token := range map[string]string{"signature": makeToken(second, baseClaims()), "audience": makeToken(key, wrongAudience), "expiry": makeToken(key, expired), "issuer": makeToken(key, wrongIssuer)} {
		t.Run(name, func(t *testing.T) {
			if _, err := manager.validateIdentity(context.Background(), "issued-client", token, "nonce-1"); err == nil {
				t.Fatalf("%s validation unexpectedly succeeded", name)
			}
		})
	}
	if _, err := manager.validateIdentity(context.Background(), "issued-client", valid, "wrong-nonce"); err == nil {
		t.Fatal("wrong nonce accepted")
	}
}

func TestCallbackPortMustBeLoopbackAndFailureGuidanceOmitsAuthorizationURL(t *testing.T) {
	manager := New(auth.Store{Path: t.TempDir() + "/auth.json"})
	manager.listen = func() (net.Listener, error) { return nil, fmt.Errorf("address already in use") }
	if _, err := manager.Login(context.Background(), ""); err == nil || strings.Contains(err.Error(), "https://") {
		t.Fatalf("port conflict error=%v", err)
	}
	manager.listen = func() (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") }
	manager.browser = func(string) error { return fmt.Errorf("browser missing") }
	manager.verifyIdentity = func(context.Context, string, string, string) (verifiedIdentity, error) {
		return verifiedIdentity{}, nil
	}
	if _, err := manager.Login(context.Background(), ""); err == nil || strings.Contains(err.Error(), "id_token_hint") {
		t.Fatalf("browser failure exposed URL hint: %v", err)
	}
}

func TestResolveRefreshesUnderLockAndKeepsRegistration(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	profile := addOAuthTestProfile(t, store, time.Now().Add(-time.Minute))
	var refreshCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != profile.Auth.OAuth.ClientID || r.Form.Get("refresh_token") != "refresh-secret" || r.Form.Get("resource") != resourceURL || r.Form.Has("scope") {
			t.Errorf("refresh form=%v", r.Form)
		}
		fmt.Fprint(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()
	manager := New(store)
	manager.token = server.URL
	manager.client = server.Client()
	resolved, access, err := manager.Resolve(context.Background(), profile.ID)
	if err != nil || access != "rotated-access" {
		t.Fatalf("resolve access=%q err=%v", access, err)
	}
	if resolved.Revision != profile.Revision || resolved.Auth.OAuth.Subject != profile.Auth.OAuth.Subject || resolved.Auth.OAuth.IDToken != profile.Auth.OAuth.IDToken || resolved.Auth.OAuth.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh replaced registration identity: %+v", resolved)
	}
	_, access, err = manager.Resolve(context.Background(), profile.ID)
	if err != nil || access != "rotated-access" || refreshCalls != 1 {
		t.Fatalf("second resolve access=%q calls=%d err=%v", access, refreshCalls, err)
	}
}

func TestInvalidGrantRequiresReconnectWithoutDeletingRegistration(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	profile := addOAuthTestProfile(t, store, time.Now().Add(-time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()
	manager := New(store)
	manager.token, manager.client = server.URL, server.Client()
	if _, _, err := manager.Resolve(context.Background(), profile.ID); err != auth.ErrOAuthReconnectRequired {
		t.Fatalf("resolve error=%v", err)
	}
	stored, err := store.Get(profile.ID)
	if err != nil || stored.Auth.OAuth == nil || stored.Auth.OAuth.ClientID != profile.Auth.OAuth.ClientID || stored.Auth.OAuth.Subject != profile.Auth.OAuth.Subject || !stored.Auth.OAuth.ReconnectRequired || stored.Auth.OAuth.AccessToken != "" || stored.Auth.OAuth.RefreshToken != "" {
		t.Fatalf("registration was not retained for reconnect: %+v %v", stored, err)
	}
}

func TestDisconnectReportsRemoteRevocationAndRemovesLocalTokens(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	profile := addOAuthTestProfile(t, store, time.Now().Add(time.Hour))
	var revocationForm url.Values
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			fmt.Fprintf(w, `{"issuer":%q,"revocation_endpoint":%q}`, server.URL, server.URL+"/revoke")
			return
		}
		if r.URL.Path == "/revoke" {
			_ = r.ParseForm()
			revocationForm = r.Form
			if r.Form.Get("token") != "refresh-secret" || r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("client_id") != profile.Auth.OAuth.ClientID {
				t.Errorf("revocation form=%v", r.Form)
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	manager := New(store)
	manager.issuer = server.URL
	manager.client = server.Client()
	result, err := manager.Disconnect(context.Background(), profile.ID, false)
	if err != nil || !result.RemoteRevocationConfirmed || !result.LocalTokensRemoved || result.LocalRegistrationRemoved || len(revocationForm) == 0 {
		t.Fatalf("disconnect result=%+v err=%v", result, err)
	}
	stored, err := store.Get(profile.ID)
	if err != nil || stored.Auth.OAuth.AccessToken != "" || stored.Auth.OAuth.RefreshToken != "" || !stored.Auth.OAuth.ReconnectRequired {
		t.Fatalf("local tokens remain: %+v %v", stored, err)
	}
}

func addOAuthTestProfile(t *testing.T, store auth.Store, expires time.Time) auth.Profile {
	t.Helper()
	hostID, err := store.EnsureHostID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := auth.NewOAuthRevision()
	if err != nil {
		t.Fatal(err)
	}
	profile := auth.Profile{ID: "openai-chatgpt-test", Provider: "openai", Label: "ChatGPT test", Revision: revision, Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "oaiapp-test", Issuer: issuerURL, Subject: "stable-subject", Email: "person@example.com", HostID: hostID, IDToken: "retained-id-token", AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer", Scopes: []string{"openid", "chatgpt.tokens.use.direct"}, ExpiresAt: expires}}}
	if err := store.AddOAuth(profile); err != nil {
		t.Fatal(err)
	}
	return profile
}
