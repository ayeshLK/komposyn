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

package model

import (
	"context"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	p "github.com/ayeshLK/komposyn/plugins/model-provider"
)

type catalogProvider struct{ id p.ID }

func (f catalogProvider) ID() p.ID                    { return f.id }
func (f catalogProvider) AuthMethods() []p.AuthMethod { return []p.AuthMethod{p.AuthAPIKey} }
func (f catalogProvider) Authenticate(context.Context, p.Credential) (p.AuthResult, error) {
	return p.AuthResult{Provider: f.id}, nil
}
func (f catalogProvider) ListModels(_ context.Context, c p.Credential) ([]p.Model, error) {
	if c.APIKey == "reject" {
		return nil, &p.Error{Outcome: p.OutcomePermission}
	}
	return []p.Model{{ID: "shared-id", DisplayName: "Shared", Compatibility: p.CompatibilityUnknown}}, nil
}
func (f catalogProvider) StreamChat(context.Context, p.ChatRequest, func(string)) (p.Result, error) {
	return p.Result{}, nil
}

func TestDiscoveryKeepsProfilesSeparateAndPartialFailuresVisible(t *testing.T) {
	store := auth.Store{Path: t.TempDir() + "/auth.json"}
	for _, entry := range []struct{ id, key string }{{"openai-one", "good"}, {"openai-two", "good"}, {"openai-denied", "reject"}} {
		if err := store.AddAPIKey(entry.id, "openai", entry.id, entry.key, nil); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry(catalogProvider{id: p.OpenAI})
	if err != nil {
		t.Fatal(err)
	}
	d := (Catalog{Providers: registry, Profiles: store}).Discover(context.Background())
	if len(d.Models) != 2 || len(d.Failures) != 1 || d.Failures[0].Outcome != p.OutcomePermission {
		t.Fatalf("discovery=%+v", d)
	}
	if d.Models[0].Key() == d.Models[1].Key() || d.Models[0].ID != d.Models[1].ID {
		t.Fatalf("profile identity collided: %+v", d.Models)
	}
}

func TestOAuthTokenRotationDoesNotChangeProfileVersion(t *testing.T) {
	profile := auth.Profile{ID: "openai-chatgpt-1", Provider: "openai", Label: "ChatGPT", Revision: "stable-revision", Auth: auth.AuthConfig{Type: "oauth", OAuth: &auth.OAuthCredential{ClientID: "issued-client", Issuer: "https://auth.openai.com", Subject: "stable-subject", HostID: "urn:uuid:host", IDToken: "id-one", AccessToken: "access-one", RefreshToken: "refresh-one", TokenType: "Bearer", Scopes: []string{"chatgpt.tokens.use.direct"}, ExpiresAt: time.Now().Add(time.Hour)}}}
	first := ProfileVersion(profile, "access-one")
	profile.Auth.OAuth.IDToken = "id-two"
	profile.Auth.OAuth.AccessToken = "access-two"
	profile.Auth.OAuth.RefreshToken = "refresh-two"
	profile.Auth.OAuth.ExpiresAt = time.Now().Add(2 * time.Hour)
	if got := ProfileVersion(profile, "access-two"); got != first {
		t.Fatalf("rotating tokens reset profile version: %s != %s", got, first)
	}
	profile.Auth.OAuth.Subject = "different-subject"
	if got := ProfileVersion(profile, ""); got == first {
		t.Fatal("account identity change kept profile version")
	}
}
