package model

import (
	"context"
	"testing"

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
