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

// Package runtime contains the phase-one in-memory chat coordinator. It does
// not open Token Logger or conversation storage.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	oauthauth "github.com/ayeshLK/komposyn/internal/harness/auth/openai"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
)

var (
	ErrNoSelection       = errors.New("select a model with /model first")
	ErrRequestActive     = errors.New("a chat request is active; cancel or wait before changing the model or credential")
	ErrConversationReset = errors.New("changing provider, model, or profile starts a fresh chat; confirm to continue")
	ErrNoSuchModel       = errors.New("model is unavailable; refresh models before selecting it")
)

type Turn struct {
	Prompt, Text       string
	Refusal            string
	Outcome            provider.Outcome
	Usage              provider.Usage
	RequestID, ModelID string
	ProviderCode       string
	Message            string
	At                 time.Time
}
type Snapshot struct {
	Selected *model.Model
	Turns    []Turn
	Active   bool
}
type Chat struct {
	mu            sync.Mutex
	catalog       model.Catalog
	profiles      auth.Store
	openAIAuth    *oauthauth.Manager
	oauthResolver interface {
		Resolve(context.Context, string) (auth.Profile, string, error)
	}
	history        []provider.Message
	turns          []Turn
	selected       *model.Model
	discovery      *model.Discovery
	active         bool
	activeProfile  string
	credentialBusy bool
	cancel         context.CancelFunc
	generation     uint64
	sequence       atomic.Uint64
	timeout        time.Duration
}

func NewChat(providers *model.Registry, profiles auth.Store) *Chat {
	oauthManager := oauthauth.New(profiles)
	return &Chat{catalog: model.Catalog{Providers: providers, Profiles: profiles, OpenAIAuth: oauthManager}, profiles: profiles, openAIAuth: oauthManager, oauthResolver: oauthManager, timeout: 2 * time.Minute}
}

func (c *Chat) StartOpenAILogin(ctx context.Context, profileID string) (oauthauth.LoginAttempt, error) {
	c.mu.Lock()
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return oauthauth.LoginAttempt{}, ErrRequestActive
	}
	c.credentialBusy = true
	c.mu.Unlock()
	attempt, err := c.openAIAuth.StartLogin(ctx, profileID)
	if err != nil {
		c.mu.Lock()
		c.credentialBusy = false
		c.mu.Unlock()
		return oauthauth.LoginAttempt{}, err
	}
	completed := make(chan oauthauth.LoginResult, 1)
	go func() {
		result := <-attempt.Done
		if result.Err == nil {
			c.mu.Lock()
			c.discovery = nil
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.credentialBusy = false
		c.mu.Unlock()
		completed <- result
		close(completed)
	}()
	attempt.Done = completed
	return attempt, nil
}

func (c *Chat) CancelOpenAILogin(attemptID string) bool { return c.openAIAuth.CancelLogin(attemptID) }

func (c *Chat) AuthMethods(providerID string) []provider.AuthMethod {
	plug, ok := c.catalog.Providers.Get(provider.ID(providerID))
	if !ok {
		return nil
	}
	return append([]provider.AuthMethod(nil), plug.AuthMethods()...)
}

func (c *Chat) DisconnectOpenAI(ctx context.Context, profileID string, removeRegistration, confirmReset bool) (oauthauth.DisconnectResult, error) {
	c.mu.Lock()
	if c.credentialBusy || (c.active && c.activeProfile == profileID) {
		c.mu.Unlock()
		return oauthauth.DisconnectResult{}, ErrRequestActive
	}
	selected := c.selected != nil && c.selected.ProfileID == profileID
	if selected && len(c.turns) > 0 && !confirmReset {
		c.mu.Unlock()
		return oauthauth.DisconnectResult{}, ErrConversationReset
	}
	c.credentialBusy = true
	c.mu.Unlock()
	result, err := c.openAIAuth.Disconnect(ctx, profileID, removeRegistration)
	c.mu.Lock()
	if result.LocalTokensRemoved || result.LocalRegistrationRemoved {
		if selected && c.selected != nil && c.selected.ProfileID == profileID {
			c.selected = nil
			c.clearConversationLocked()
		}
		c.discovery = nil
	}
	c.credentialBusy = false
	c.mu.Unlock()
	return result, err
}

func (c *Chat) OpenAIUsage() error { return c.openAIAuth.OpenUsage() }
func (c *Chat) Discover(ctx context.Context) model.Discovery {
	d := c.catalog.Discover(ctx)
	c.mu.Lock()
	c.discovery = &d
	c.mu.Unlock()
	return d
}
func (c *Chat) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	var selected *model.Model
	if c.selected != nil {
		x := *c.selected
		selected = &x
	}
	return Snapshot{Selected: selected, Turns: append([]Turn(nil), c.turns...), Active: c.active}
}

func (c *Chat) AddProfile(ctx context.Context, id, providerID, label, key string, route map[string]string) error {
	plug, ok := c.catalog.Providers.Get(provider.ID(providerID))
	if !ok {
		return &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	cred := provider.Credential{APIKey: key}
	if route != nil {
		cred.Route.Organization = route["organization"]
		cred.Route.Project = route["project"]
	}
	if _, err := plug.Authenticate(ctx, cred); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.profiles.AddAPIKey(id, providerID, label, key, route); err != nil {
		return err
	}
	c.mu.Lock()
	c.discovery = nil
	c.mu.Unlock()
	return nil
}
func (c *Chat) ReplaceProfile(ctx context.Context, id, label, key string, route map[string]string, confirmReset bool) error {
	c.mu.Lock()
	if c.active {
		_, _, err := c.profiles.Resolve(id)
		if err == nil && id == c.activeProfile {
			c.mu.Unlock()
			return ErrRequestActive
		}
	}
	c.mu.Unlock()
	p, _, err := c.profiles.Resolve(id)
	if err != nil {
		return err
	}
	plug, ok := c.catalog.Providers.Get(provider.ID(p.Provider))
	if !ok {
		return &provider.Error{Outcome: provider.OutcomeUnsupported}
	}
	cred := provider.Credential{APIKey: key}
	if route != nil {
		cred.Route.Organization = route["organization"]
		cred.Route.Project = route["project"]
	}
	if _, err := plug.Authenticate(ctx, cred); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.active && id == c.activeProfile {
		return ErrRequestActive
	}
	if c.selected != nil && c.selected.ProfileID == id && len(c.turns) > 0 && !confirmReset {
		return ErrConversationReset
	}
	if err := c.profiles.ReplaceAPIKey(id, label, key, route); err != nil {
		return err
	}
	c.discovery = nil
	if c.selected != nil && c.selected.ProfileID == id {
		if len(c.turns) > 0 {
			c.clearConversationLocked()
		}
		c.selected = nil
	}
	return nil
}
func (c *Chat) RemoveProfile(id string, confirmReset bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active && c.activeProfile == id {
		return ErrRequestActive
	}
	if c.selected != nil && c.selected.ProfileID == id && len(c.turns) > 0 && !confirmReset {
		return ErrConversationReset
	}
	if err := c.profiles.Remove(id); err != nil {
		return err
	}
	c.discovery = nil
	if c.selected != nil && c.selected.ProfileID == id {
		c.selected = nil
		c.clearConversationLocked()
	}
	return nil
}

func (c *Chat) Select(ctx context.Context, key string, confirmReset bool) error {
	c.mu.Lock()
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	cached := c.discovery
	c.mu.Unlock()
	if cached == nil {
		d := c.Discover(ctx)
		cached = &d
	}
	found := false
	var selected model.Model
	for _, m := range cached.Models {
		if m.Key() == key {
			selected = m
			found = true
			break
		}
	}
	if !found {
		return ErrNoSuchModel
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active || c.credentialBusy {
		return ErrRequestActive
	}
	if c.selected != nil && c.selected.Key() == selected.Key() {
		return nil
	}
	if len(c.turns) > 0 && !confirmReset {
		return ErrConversationReset
	}
	if len(c.turns) > 0 {
		c.clearConversationLocked()
	}
	c.selected = &selected
	return nil
}
func (c *Chat) clearConversationLocked() { c.history = nil; c.turns = nil }

func (c *Chat) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Chat) Send(parent context.Context, prompt string, onDelta func(string)) (Turn, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Turn{}, errors.New("message is empty")
	}
	c.mu.Lock()
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return Turn{}, ErrRequestActive
	}
	if c.selected == nil {
		c.mu.Unlock()
		return Turn{}, ErrNoSelection
	}
	selection := *c.selected
	var prof auth.Profile
	var key string
	var err error
	if selection.Provider == provider.OpenAI {
		metadata, metaErr := c.profiles.Get(selection.ProfileID)
		if metaErr == nil && metadata.Auth.Type == "oauth" {
			prof = metadata
			key = ""
		} else {
			prof, key, err = c.profiles.Resolve(selection.ProfileID)
		}
	} else {
		prof, key, err = c.profiles.Resolve(selection.ProfileID)
	}
	if err != nil {
		c.mu.Unlock()
		return Turn{}, err
	}
	version := model.ProfileVersion(prof, key)
	if selection.ProfileVersion != "" && selection.ProfileVersion != version {
		c.mu.Unlock()
		return Turn{}, errors.New("selected credential changed; refresh and select a model again")
	}
	plug, ok := c.catalog.Providers.Get(selection.Provider)
	if !ok {
		c.mu.Unlock()
		return Turn{}, ErrNoSuchModel
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	generation := c.sequence.Add(1)
	c.generation = generation
	c.active = true
	c.activeProfile = selection.ProfileID
	c.cancel = cancel
	history := append([]provider.Message(nil), c.history...)
	history = append(history, provider.Message{Role: provider.RoleUser, Text: prompt})
	requestID := fmt.Sprintf("chat-%d", generation)
	c.mu.Unlock()
	defer cancel()
	turn := Turn{Prompt: prompt, ModelID: selection.ID, At: time.Now().UTC()}
	if prof.Auth.Type == "oauth" {
		var accessToken string
		if c.oauthResolver == nil {
			err = errors.New("OpenAI authentication coordinator is unavailable")
		} else {
			prof, accessToken, err = c.oauthResolver.Resolve(ctx, selection.ProfileID)
		}
		if err != nil {
			outcome := provider.OutcomeAuthentication
			if errors.Is(err, oauthauth.ErrPlanUsageDisabled) {
				outcome = provider.OutcomePermission
			}
			turn.Outcome = outcome
			turn.Message = "OpenAI plan authorization is unavailable; manage this profile and reconnect or enable plan usage."
			c.mu.Lock()
			if c.generation == generation {
				c.active = false
				c.activeProfile = ""
				c.cancel = nil
				c.turns = append(c.turns, turn)
			}
			c.mu.Unlock()
			return turn, err
		}
		key = accessToken
	}
	result, callErr := plug.StreamChat(ctx, provider.ChatRequest{RequestID: requestID, ModelID: selection.ID, Credential: modelCredential(prof, key), Messages: history, Instructions: "You are KompoSyn, a helpful text assistant. Do not claim to call tools.", MaxOutput: 4096}, func(delta string) {
		c.mu.Lock()
		valid := c.active && c.generation == generation && ctx.Err() == nil
		c.mu.Unlock()
		if valid && onDelta != nil {
			onDelta(delta)
		}
	})
	turn.Text = result.Text
	turn.Refusal = result.Refusal
	turn.Outcome = result.Outcome
	turn.Usage = result.Usage
	turn.RequestID = result.RequestID
	turn.ProviderCode = result.ProviderCode
	if result.Outcome != provider.OutcomeCompleted && result.ProviderCode != "" {
		turn.Message = messageForProviderCode(result.ProviderCode)
	}
	if callErr != nil {
		turn.Outcome = provider.ErrorOutcome(callErr)
		turn.Message = safeError(callErr)
	}
	if ctx.Err() != nil {
		turn.Outcome = provider.OutcomeInterrupted
		if turn.Message == "" {
			turn.Message = "request interrupted"
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return turn, errors.New("stale provider result discarded")
	}
	c.active = false
	c.activeProfile = ""
	c.cancel = nil
	if turn.Outcome == provider.OutcomeCompleted || turn.Outcome == provider.OutcomeRefused {
		if len(result.Native) == 0 {
			turn.Outcome = provider.OutcomeUnsupported
			turn.Message = "provider returned no replayable continuation data"
		} else {
			c.history = append(c.history, provider.Message{Role: provider.RoleUser, Text: prompt}, provider.Message{Role: provider.RoleAssistant, Text: result.Text, Native: cloneRaw(result.Native)})
		}
	}
	c.turns = append(c.turns, turn)
	if callErr != nil {
		return turn, callErr
	}
	return turn, nil
}

func modelCredential(p auth.Profile, key string) provider.Credential {
	if p.Auth.Type == "oauth" {
		return provider.Credential{Method: provider.AuthOAuth, AccessToken: key}
	}
	r := provider.Routing{}
	if p.Route != nil {
		r.Organization = p.Route["organization"]
		r.Project = p.Route["project"]
	}
	return provider.Credential{Method: provider.AuthAPIKey, APIKey: key, Route: r}
}
func cloneRaw(in []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, len(in))
	for i, b := range in {
		out[i] = append(json.RawMessage(nil), b...)
	}
	return out
}
func safeError(err error) string {
	var e *provider.Error
	if errors.As(err, &e) {
		if e.ProviderCode != "" {
			return messageForProviderCode(e.ProviderCode)
		}
		switch e.Outcome {
		case provider.OutcomeAuthentication:
			return "provider rejected the credential"
		case provider.OutcomePermission:
			return "credential lacks permission for this provider operation"
		case provider.OutcomeRateLimited:
			return "provider rate limit reached"
		case provider.OutcomeInterrupted:
			return "request interrupted"
		case provider.OutcomeNetwork:
			return "provider network request failed"
		case provider.OutcomeUnsupported:
			return "provider returned unsupported content"
		default:
			return "provider service failed"
		}
	}
	return "request failed"
}

func messageForProviderCode(code string) string {
	switch code {
	case "subscription_sharing_user_not_eligible":
		return "ChatGPT plan usage is unavailable for this account or workspace. Choose another saved account or API-key profile."
	case "subscription_sharing_usage_limit_exceeded":
		return "ChatGPT plan usage limit reached. Review or manage access in ChatGPT Settings → Usage."
	case "subscription_sharing_usage_unavailable":
		return "ChatGPT plan usage is temporarily unavailable. Your credentials are preserved; retry later."
	case "subscription_sharing_unsupported_capability":
		return "This model or request is not supported by the ChatGPT plan route. Choose another listed model."
	case "subscription_sharing_invalid_user", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		return "ChatGPT plan authorization was rejected. Reconnect the selected account and verify its plan-use permission."
	default:
		return "OpenAI could not complete this request. The selected profile remains in use; no billing fallback occurred."
	}
}
