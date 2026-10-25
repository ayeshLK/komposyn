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
	ListMetadata() ([]auth.Metadata, error)
	Resolve(string) (auth.Profile, string, error)
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
	Outcome                    provider.Outcome
}
type Discovery struct {
	Models   []Model
	Failures []ProfileFailure
}
type Catalog struct {
	Providers *Registry
	Profiles  ProfileStore
}

func (c Catalog) Discover(ctx context.Context) Discovery {
	profiles, err := c.Profiles.ListMetadata()
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
			resolved, key, err := c.Profiles.Resolve(p.ID)
			if err != nil {
				ch <- item{failure: &ProfileFailure{ProfileID: p.ID, Provider: p.Provider, Label: p.Label, Outcome: provider.OutcomeAuthentication}}
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

func credential(p auth.Profile, key string) provider.Credential {
	r := provider.Routing{}
	if p.Route != nil {
		r.Organization = p.Route["organization"]
		r.Project = p.Route["project"]
	}
	return provider.Credential{APIKey: key, Route: r}
}
func ProfileVersion(p auth.Profile, key string) string {
	p.Auth.APIKey = ""
	b, _ := json.Marshal(struct {
		Profile auth.Profile
		Secret  string
	}{p, key})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
