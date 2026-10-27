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
	"errors"
	"testing"
)

type testResolver struct {
	profile Profile
	secret  string
	err     error
}

func (r testResolver) Provider() string { return r.profile.Provider }
func (r testResolver) Method() Method   { return Method(r.profile.Auth.Type) }

func (r testResolver) Resolve(_ context.Context, id string) (Profile, string, error) {
	if r.err != nil {
		return Profile{}, "", r.err
	}
	if r.profile.ID != id {
		return Profile{}, "", errors.New("profile not found")
	}
	return r.profile, r.secret, nil
}

func TestRegistryRoutesByProviderAndMethod(t *testing.T) {
	registry := NewRegistry()
	apiProfile := Profile{ID: "openai-key", Provider: "openai", Auth: AuthConfig{Type: "api_key"}}
	oauthProfile := Profile{ID: "openai-oauth", Provider: "openai", Auth: AuthConfig{Type: "oauth"}}
	if err := registry.Register("openai", MethodAPIKey, testResolver{profile: apiProfile, secret: "api-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("openai", MethodOAuth, testResolver{profile: oauthProfile, secret: "oauth-access"}); err != nil {
		t.Fatal(err)
	}
	profile, secret, err := registry.Resolve(context.Background(), "openai", MethodOAuth, oauthProfile.ID)
	if err != nil || profile.ID != oauthProfile.ID || secret != "oauth-access" {
		t.Fatalf("oauth resolution=(%+v,%q,%v)", profile, secret, err)
	}
	profile, secret, err = registry.Resolve(context.Background(), "openai", MethodAPIKey, apiProfile.ID)
	if err != nil || profile.ID != apiProfile.ID || secret != "api-secret" {
		t.Fatalf("API key resolution=(%+v,%q,%v)", profile, secret, err)
	}
	if _, _, err := registry.Resolve(context.Background(), "anthropic", MethodOAuth, "openai-oauth"); err == nil {
		t.Fatal("unsupported provider/method combination resolved")
	}
	if _, _, err := registry.Resolve(context.Background(), "openai", MethodOAuth, apiProfile.ID); err == nil {
		t.Fatal("mismatched profile auth method resolved")
	}
	if err := registry.Register("anthropic", MethodOAuth, testResolver{profile: oauthProfile, secret: "wrong-provider"}); err == nil {
		t.Fatal("implementation identity mismatch was registered")
	}
}

func TestRegistryReturnsTypedSanitizedFailures(t *testing.T) {
	cases := []struct {
		name  string
		cause error
		ctx   context.Context
		want  FailureKind
	}{
		{name: "permission", cause: ErrOAuthPermissionRequired, ctx: context.Background(), want: FailurePermission},
		{name: "terminal", cause: ErrOAuthReconnectRequired, ctx: context.Background(), want: FailureTerminal},
		{name: "client", cause: ErrInvalidClientConfiguration, ctx: context.Background(), want: FailureInvalidClient},
		{name: "temporary", cause: errors.New("sensitive transport detail"), ctx: context.Background(), want: FailureTemporary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry()
			profile := Profile{ID: "openai-profile", Provider: "openai", Auth: AuthConfig{Type: "oauth"}}
			if err := registry.Register("openai", MethodOAuth, testResolver{profile: profile, err: tc.cause}); err != nil {
				t.Fatal(err)
			}
			_, _, err := registry.Resolve(tc.ctx, "openai", MethodOAuth, profile.ID)
			failure, ok := AsFailure(err)
			if !ok || failure.Kind != tc.want {
				t.Fatalf("failure=%+v err=%v", failure, err)
			}
			if tc.name == "temporary" && err.Error() == tc.cause.Error() {
				t.Fatal("underlying transport details escaped sanitized guidance")
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	registry := NewRegistry()
	profile := Profile{ID: "openai-profile", Provider: "openai", Auth: AuthConfig{Type: "oauth"}}
	if err := registry.Register("openai", MethodOAuth, testResolver{profile: profile, err: canceled.Err()}); err != nil {
		t.Fatal(err)
	}
	_, _, err := registry.Resolve(canceled, "openai", MethodOAuth, profile.ID)
	failure, ok := AsFailure(err)
	if !ok || failure.Kind != FailureCanceled {
		t.Fatalf("canceled failure=%+v err=%v", failure, err)
	}
}

func TestMissingExplicitEnvironmentCredentialIsConfigurationFailure(t *testing.T) {
	store := Store{Path: t.TempDir() + "/auth.json", LookupEnv: func(string) (string, bool) { return "", false }}
	if err := store.Add(Profile{ID: "openai-env", Provider: "openai", Label: "Explicit environment", Auth: AuthConfig{Type: "env", Env: "OPENAI_API_KEY"}}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.Register("openai", MethodEnv, StoreResolver{Store: store, ProviderID: "openai", AuthMethod: MethodEnv}); err != nil {
		t.Fatal(err)
	}
	_, _, err := registry.Resolve(context.Background(), "openai", MethodEnv, "openai-env")
	failure, ok := AsFailure(err)
	if !ok || failure.Kind != FailureConfiguration {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}
