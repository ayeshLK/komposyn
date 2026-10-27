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
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Method identifies the authentication implementation used by a profile.
type Method string

const (
	MethodAPIKey Method = "api_key"
	MethodEnv    Method = "env"
	MethodOAuth  Method = "oauth"
)

type Key struct {
	Provider string
	Method   Method
}

// Resolver resolves one selected profile into secret material for exactly one
// provider operation. Implementations must not return public UI metadata here.
type Implementation interface {
	Provider() string
	Method() Method
	Resolve(context.Context, string) (Profile, string, error)
}

type Resolver = Implementation

// StatusResolver reports sanitized connection and permission state without
// refreshing tokens, resolving secrets, or contacting the provider.
type StatusResolver interface {
	PublicStatus(context.Context, string) (PublicStatus, error)
}

type PermissionState string

const (
	PermissionGranted PermissionState = "granted"
	PermissionMissing PermissionState = "missing"
	PermissionUnknown PermissionState = "unknown"
)

type Permission struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	State       PermissionState `json:"state"`
	Description string          `json:"description,omitempty"`
}

type PublicStatus struct {
	Connection  string       `json:"connection"`
	Account     string       `json:"account,omitempty"`
	Permissions []Permission `json:"permissions,omitempty"`
}

// Registry maps an explicit provider and auth method pair to its auth
// implementation. It intentionally does not load plugins or infer fallbacks.
type Registry struct {
	mu      sync.RWMutex
	entries map[Key]Resolver
}

func NewRegistry() *Registry { return &Registry{entries: make(map[Key]Resolver)} }

func (r *Registry) Register(provider string, method Method, implementation Implementation) error {
	provider = strings.TrimSpace(provider)
	if r == nil || provider == "" || method == "" || implementation == nil {
		return errors.New("auth registration requires provider, method, and implementation")
	}
	if implementation.Provider() != provider || implementation.Method() != method {
		return errors.New("auth implementation identity does not match its registration key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[Key]Resolver)
	}
	key := Key{Provider: provider, Method: method}
	if _, exists := r.entries[key]; exists {
		return fmt.Errorf("auth implementation already registered for %s/%s", provider, method)
	}
	r.entries[key] = implementation
	return nil
}

func (r *Registry) Lookup(provider string, method Method) (Resolver, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	implementation, ok := r.entries[Key{Provider: provider, Method: method}]
	return implementation, ok
}

func (r *Registry) Resolve(ctx context.Context, provider string, method Method, profileID string) (Profile, string, error) {
	implementation, ok := r.Lookup(provider, method)
	if !ok {
		return Profile{}, "", NewFailure(FailureConfiguration, "The selected provider does not support this authentication method.", fmt.Errorf("unsupported auth pair %s/%s", provider, method))
	}
	profile, secret, err := implementation.Resolve(ctx, profileID)
	if err != nil {
		if _, typed := AsFailure(err); typed {
			return Profile{}, "", err
		}
		kind, guidance := FailureTemporary, "The provider could not verify this account. Retry in a moment."
		switch {
		case ctx.Err() != nil:
			kind, guidance = FailureCanceled, "Authentication was canceled."
			err = ctx.Err()
		case errors.Is(err, ErrOAuthPermissionRequired):
			kind, guidance = FailurePermission, "The selected account is missing permission for this operation. Review its permissions and reconnect if needed."
		case errors.Is(err, ErrOAuthReconnectRequired), errors.Is(err, ErrOAuthRefreshOutcomeUnknown):
			kind, guidance = FailureTerminal, "The selected account must reconnect before it can be used."
		case errors.Is(err, ErrInvalidClientConfiguration):
			kind, guidance = FailureInvalidClient, "The authentication client configuration is invalid. Contact the application administrator."
		case isStoreResolver(implementation):
			kind, guidance = FailureConfiguration, "The selected credential is missing or unavailable. Check the selected profile configuration."
		}
		return Profile{}, "", NewFailure(kind, guidance, err)
	}
	if profile.Provider != provider || Method(profile.Auth.Type) != method {
		return Profile{}, "", NewFailure(FailureConfiguration, "The selected profile does not match this authentication method.", errors.New("profile auth method mismatch"))
	}
	return profile, secret, nil
}

func isStoreResolver(implementation Implementation) bool {
	_, ok := implementation.(StoreResolver)
	return ok
}

func (r *Registry) Methods(provider string) []Method {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	methods := make([]Method, 0)
	for key := range r.entries {
		if key.Provider == provider && key.Method != MethodEnv {
			methods = append(methods, key.Method)
		}
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i] < methods[j] })
	return methods
}

// StoreResolver handles locally configured API-key and explicit environment
// profiles. It never handles OAuth profiles or calls a provider.
type StoreResolver struct {
	Store      Store
	ProviderID string
	AuthMethod Method
}

func (r StoreResolver) Provider() string { return r.ProviderID }
func (r StoreResolver) Method() Method   { return r.AuthMethod }

func (r StoreResolver) Resolve(ctx context.Context, profileID string) (Profile, string, error) {
	profile, secret, err := r.Store.ResolveContext(ctx, profileID)
	if err != nil {
		return Profile{}, "", err
	}
	if profile.Provider != r.ProviderID || Method(profile.Auth.Type) != r.AuthMethod {
		return Profile{}, "", errors.New("profile is not supported by this authentication implementation")
	}
	return profile, secret, nil
}

func (r StoreResolver) PublicStatus(ctx context.Context, profileID string) (PublicStatus, error) {
	profile, err := r.Store.GetContext(ctx, profileID)
	if err != nil {
		return PublicStatus{}, err
	}
	if profile.Provider != r.ProviderID || Method(profile.Auth.Type) != r.AuthMethod {
		return PublicStatus{}, errors.New("profile is not supported by this authentication implementation")
	}
	return PublicStatus{Connection: "configured"}, nil
}
