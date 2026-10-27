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

// Package model coordinates provider discovery and running-chat selection.
// Selection and any discovery cache are deliberately process-local.
package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[provider.ID]provider.Provider
}

func NewRegistry(plugins ...provider.Provider) (*Registry, error) {
	r := &Registry{providers: map[provider.ID]provider.Provider{}}
	for _, p := range plugins {
		if p == nil || p.ID() == "" {
			return nil, errors.New("provider plugin needs an ID")
		}
		if _, ok := r.providers[p.ID()]; ok {
			return nil, fmt.Errorf("duplicate provider plugin %q", p.ID())
		}
		r.providers[p.ID()] = p
	}
	return r, nil
}
func (r *Registry) Get(id provider.ID) (provider.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}
func (r *Registry) All() []provider.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]provider.Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

type ProfileStore interface {
	ListMetadataContext(context.Context) ([]auth.Metadata, error)
	ResolveContext(context.Context, string) (auth.Profile, string, error)
	AddAPIKey(string, string, string, string, map[string]string) error
	ReplaceAPIKey(string, string, string, map[string]string) error
	Remove(string) error
}
type Model struct {
	Provider                                 provider.ID
	ProfileID, ProfileLabel, ID, DisplayName string
	Compatibility                            provider.Compatibility
	Reason                                   string
	ProfileVersion                           string `json:"-"`
}

func (m Model) Key() string { return string(m.Provider) + "\x00" + m.ProfileID + "\x00" + m.ID }

type ProfileFailure struct {
	ProfileID, Provider, Label string
	Message                    string
	Outcome                    provider.Outcome
}
type Discovery struct {
	Models   []Model
	Failures []ProfileFailure
}
type Catalog struct {
	Providers *Registry
	Profiles  ProfileStore
	Auth      *auth.Registry
}

func (c Catalog) Discover(ctx context.Context) Discovery {
	profiles, err := c.Profiles.ListMetadataContext(ctx)
	if err != nil {
		return Discovery{Failures: []ProfileFailure{{Outcome: provider.OutcomeService}}}
	}
	type item struct {
		models  []Model
		failure *ProfileFailure
	}
	ch := make(chan item, len(profiles))
	var wg sync.WaitGroup
	for _, profile := range profiles {
		p := profile
		wg.Add(1)
		go func() {
			defer wg.Done()
			plug, ok := c.Providers.Get(provider.ID(p.Provider))
			if !ok {
				ch <- item{failure: &ProfileFailure{ProfileID: p.ID, Provider: p.Provider, Label: p.Label, Outcome: provider.OutcomeUnsupported}}
				return
			}
			if c.Auth == nil {
				ch <- item{failure: &ProfileFailure{ProfileID: p.ID, Provider: p.Provider, Label: p.Label, Outcome: provider.OutcomeAuthentication}}
				return
			}
			resolved, key, err := c.Auth.Resolve(ctx, p.Provider, auth.Method(p.AuthType), p.ID)
			if err != nil {
				outcome, message := authFailure(err)
				ch <- item{failure: &ProfileFailure{ProfileID: p.ID, Provider: p.Provider, Label: p.Label, Outcome: outcome, Message: message}}
				return
			}
			cred := credential(resolved, key)
			models, err := plug.ListModels(ctx, cred)
			if err != nil {
				ch <- item{failure: &ProfileFailure{ProfileID: p.ID, Provider: p.Provider, Label: p.Label, Outcome: provider.ErrorOutcome(err)}}
				return
			}
			version := ProfileVersion(resolved, key)
			out := make([]Model, 0, len(models))
			seen := map[string]bool{}
			for _, m := range models {
				if m.ID == "" || seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				out = append(out, Model{Provider: plug.ID(), ProfileID: p.ID, ProfileLabel: p.Label, ID: m.ID, DisplayName: m.DisplayName, Compatibility: m.Compatibility, Reason: m.Reason, ProfileVersion: version})
			}
			ch <- item{models: out}
		}()
	}
	wg.Wait()
	close(ch)
	out := Discovery{Models: []Model{}, Failures: []ProfileFailure{}}
	for x := range ch {
		if x.failure != nil {
			out.Failures = append(out.Failures, *x.failure)
		}
		out.Models = append(out.Models, x.models...)
	}
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].Key() < out.Models[j].Key() })
	sort.Slice(out.Failures, func(i, j int) bool {
		if out.Failures[i].Provider == out.Failures[j].Provider {
			return out.Failures[i].ProfileID < out.Failures[j].ProfileID
		}
		return out.Failures[i].Provider < out.Failures[j].Provider
	})
	return out
}

func authFailure(err error) (provider.Outcome, string) {
	failure, ok := auth.AsFailure(err)
	if !ok {
		return provider.OutcomeAuthentication, "The selected credential could not be resolved."
	}
	switch failure.Kind {
	case auth.FailurePermission:
		return provider.OutcomePermission, failure.Error()
	case auth.FailureTemporary:
		return provider.OutcomeNetwork, failure.Error()
	case auth.FailureCanceled:
		return provider.OutcomeInterrupted, failure.Error()
	default:
		return provider.OutcomeAuthentication, failure.Error()
	}
}

func credential(p auth.Profile, key string) provider.Credential {
	r := provider.Routing{}
	if p.Route != nil {
		r.Organization = p.Route["organization"]
		r.Project = p.Route["project"]
	}
	if p.Auth.Type == "oauth" && p.Auth.OAuth != nil {
		return provider.Credential{Method: provider.AuthOAuth, AccessToken: key}
	}
	return provider.Credential{Method: provider.AuthAPIKey, APIKey: key, Route: r}
}
func ProfileVersion(p auth.Profile, key string) string {
	p.Auth.APIKey = ""
	if p.Auth.OAuth != nil {
		oauth := *p.Auth.OAuth
		oauth.IDToken = ""
		oauth.AccessToken = ""
		oauth.RefreshToken = ""
		oauth.TokenType = ""
		oauth.ExpiresAt = time.Time{}
		oauth.LifecycleGeneration = 0
		oauth.ReconnectRequired = false
		oauth.RefreshOutcomeUnknown = false
		p.Auth.OAuth = &oauth
		key = ""
	}
	b, _ := json.Marshal(struct {
		Profile auth.Profile
		Secret  string
	}{p, key})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
