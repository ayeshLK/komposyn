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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
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
	DiagnosticCode     provider.DiagnosticCode
	Message            string
	ToolActivities     []ToolActivity
	At                 time.Time
}
type Snapshot struct {
	Selected       *model.Model
	Turns          []Turn
	Usage          []ModelUsage
	Active         bool
	ToolActivities []ToolActivity
	Plan           []tools.PlanStep
	FilesystemRoot FilesystemRootStatus
}
type UsageMetric struct {
	Tokens  int64
	Known   bool
	Partial bool
	missing bool
}
type ModelUsage struct {
	Provider, ModelID                     string
	Input, Output, CachedRead, CacheWrite UsageMetric
}
type Chat struct {
	mu                    sync.Mutex
	catalog               model.Catalog
	profiles              auth.Store
	authRegistry          *auth.Registry
	oauth                 auth.OAuthCoordinator
	history               []provider.Message
	historySensitive      bool
	historyReadEpoch      uint64
	historyDestination    string
	turns                 []Turn
	usage                 map[string]*ModelUsage
	selected              *model.Model
	discovery             *model.Discovery
	active                bool
	activeProfile         string
	credentialBusy        bool
	credentialBusyProfile string
	cancel                context.CancelFunc
	activeDone            chan struct{}
	generation            uint64
	sequence              atomic.Uint64
	loginMu               sync.Mutex
	loginOwners           map[string]auth.LoginCoordinator
	timeout               time.Duration
	toolRuntime           *toolRuntime
	conversationID        string
	conversationIDErr     error
	toolCallIDs           map[string]struct{}
	activeToolActivities  []ToolActivity
	filesystemRoot        string
	filesystemRootGen     uint64
	filesystemRootAck     uint64
	filesystemRootOp      uint64
	filesystemRootEvent   *FilesystemRootTransition
	closed                bool
}

func NewChat(providers *model.Registry, profiles auth.Store) *Chat {
	registry := auth.NewRegistry()
	if providers != nil {
		for _, plug := range providers.All() {
			providerID := string(plug.ID())
			for _, method := range plug.AuthMethods() {
				if auth.Method(method) == auth.MethodAPIKey {
					_ = registry.Register(providerID, auth.MethodAPIKey, auth.StoreResolver{Store: profiles, ProviderID: providerID, AuthMethod: auth.MethodAPIKey})
					_ = registry.Register(providerID, auth.MethodEnv, auth.StoreResolver{Store: profiles, ProviderID: providerID, AuthMethod: auth.MethodEnv})
				}
			}
		}
	}
	return NewChatWithAuth(providers, profiles, registry)
}

func NewChatWithAuth(providers *model.Registry, profiles auth.Store, registry *auth.Registry) *Chat {
	if registry == nil {
		registry = auth.NewRegistry()
	}
	conversationID, conversationErr := newConversationID()
	chat := &Chat{catalog: model.Catalog{Providers: providers, Profiles: profiles, Auth: registry}, profiles: profiles, authRegistry: registry, timeout: 2 * time.Minute, conversationID: conversationID, conversationIDErr: conversationErr, filesystemRootGen: 1, filesystemRootAck: 1}
	if implementation, ok := registry.Lookup("openai", auth.MethodOAuth); ok {
		if coordinator, ok := implementation.(auth.OAuthCoordinator); ok {
			chat.oauth = coordinator
		}
	}
	return chat
}

// NewChatWithToolRuntime preserves the existing text-only constructors while
// allowing a trusted caller to inject a fixture or reviewed tool runtime.
// The shipped executable intentionally does not call this constructor.
func NewChatWithToolRuntime(providers *model.Registry, profiles auth.Store, registry *auth.Registry, config ToolRuntimeConfig) *Chat {
	var chat *Chat
	if registry == nil {
		chat = NewChat(providers, profiles)
	} else {
		chat = NewChatWithAuth(providers, profiles, registry)
	}
	chat.toolRuntime = newToolRuntime(config)
	if chat.toolRuntime != nil {
		chat.toolRuntime.retireHistory = chat.retireReadHistory
	}
	return chat
}

// Close cancels active work and performs a bounded cleanup drain. Call
// CloseContext when the caller needs to observe an incomplete shutdown.
func (c *Chat) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.CloseContext(ctx)
}

// CloseContext cancels an active turn, waits for it to finish within ctx, and
// retries retained prepared-capability cleanup before returning. A timeout or
// cleanup error leaves runtime-owned obligations reachable so the caller can
// retry CloseContext; it does not claim to roll back a running side effect.
func (c *Chat) CloseContext(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	c.mu.Lock()
	firstClose := !c.closed
	var rootEvent *FilesystemRootTransition
	if firstClose {
		c.closed = true
		c.history = nil
		c.historySensitive = false
		c.historyDestination = ""
		c.toolCallIDs = nil
		rootEvent = c.clearFilesystemRootLocked(FilesystemRootShutdown, true)
	}
	cancel := c.cancel
	activeDone := c.activeDone
	conversationID, runtime := c.conversationID, c.toolRuntime
	rootHost := c.filesystemRootHostLocked()
	if rootEvent == nil && c.filesystemRootAck < c.filesystemRootGen && c.filesystemRootEvent != nil {
		copy := *c.filesystemRootEvent
		rootEvent = &copy
	}
	c.mu.Unlock()
	if runtime != nil {
		runtime.invalidateReadAuthority()
		runtime.beginShutdown()
	}
	if cancel != nil {
		cancel()
	}
	var rootErr error
	if rootEvent != nil {
		rootErr = c.notifyFilesystemRoot(ctx, rootHost, *rootEvent)
	}
	if firstClose && runtime != nil && conversationID != "" {
		runtime.clearConversationGrants(conversationID)
		runtime.plans.Close(conversationID)
	}
	if activeDone != nil {
		select {
		case <-activeDone:
		case <-ctx.Done():
			return errors.Join(rootErr, fmt.Errorf("active chat turn did not stop before shutdown: %w", ctx.Err()))
		}
	}
	if runtime != nil {
		return errors.Join(rootErr, runtime.shutdownAndDrain(ctx))
	}
	return rootErr
}

// ToolGrants returns detached, in-memory grants for the current conversation.
func (c *Chat) ToolGrants() []permissions.Grant {
	c.mu.Lock()
	id, runtime := c.conversationID, c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return nil
	}
	_, grants := runtime.grants.Snapshot(id)
	return grants
}

// RevokeToolGrant revokes one current-conversation grant and invalidates any
// stale pending decision before it can authorize a later dispatch.
func (c *Chat) RevokeToolGrant(id string) error {
	c.mu.Lock()
	conversation, runtime := c.conversationID, c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return errors.New("tool permissions are unavailable")
	}
	return runtime.revokeConversationGrant(conversation, id)
}

// ClearToolGrants clears current-conversation grants and invalidates pending
// approvals and retained read executions, including when the grant store was
// already empty.
func (c *Chat) ClearToolGrants() (int, error) {
	c.mu.Lock()
	conversation, runtime := c.conversationID, c.toolRuntime
	c.mu.Unlock()
	if runtime == nil {
		return 0, nil
	}
	return runtime.clearConversationGrants(conversation)
}

func NewChatWithOAuth(providers *model.Registry, profiles auth.Store, oauth auth.Implementation) *Chat {
	chat := NewChat(providers, profiles)
	if oauth != nil {
		if err := chat.authRegistry.Register("openai", auth.MethodOAuth, oauth); err != nil {
			return chat
		}
	}
	if coordinator, ok := oauth.(auth.OAuthCoordinator); ok {
		chat.oauth = coordinator
	}
	chat.catalog.Auth = chat.authRegistry
	return chat
}

func (c *Chat) StartOAuthLogin(ctx context.Context, profileID string) (auth.OAuthLoginAttempt, error) {
	return c.StartLogin(ctx, "openai", auth.MethodOAuth, profileID)
}

func (c *Chat) StartLogin(ctx context.Context, providerID string, method auth.Method, profileID string) (auth.OAuthLoginAttempt, error) {
	implementation, found := c.authRegistry.Lookup(providerID, method)
	coordinator, ok := implementation.(auth.LoginCoordinator)
	if !found || !ok {
		return auth.OAuthLoginAttempt{}, fmt.Errorf("login is unsupported for %s/%s", providerID, method)
	}
	c.mu.Lock()
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return auth.OAuthLoginAttempt{}, ErrRequestActive
	}
	selectedProfile := c.selected != nil && c.selected.ProfileID == profileID
	if selectedProfile {
		c.supersedeFilesystemRootOperationsLocked()
	}
	c.credentialBusy = true
	c.credentialBusyProfile = profileID
	c.mu.Unlock()
	attempt, err := coordinator.StartLogin(ctx, profileID)
	if err != nil {
		c.mu.Lock()
		c.credentialBusy = false
		c.credentialBusyProfile = ""
		c.mu.Unlock()
		return auth.OAuthLoginAttempt{}, err
	}
	completed := make(chan auth.OAuthLoginResult, 1)
	events := attempt.Events
	loginDone := attempt.Done
	c.loginMu.Lock()
	if c.loginOwners == nil {
		c.loginOwners = make(map[string]auth.LoginCoordinator)
	}
	c.loginOwners[attempt.ID] = coordinator
	c.loginMu.Unlock()
	go func() {
		result := <-loginDone
		c.mu.Lock()
		var rootEvent *FilesystemRootTransition
		if result.Err == nil && !c.closed {
			c.discovery = nil
			if c.selected != nil && c.selected.ProfileID == profileID {
				rootEvent = c.clearFilesystemRootLocked(FilesystemRootProfileChanged, true)
			}
		}
		c.credentialBusy = false
		c.credentialBusyProfile = ""
		rootHost := c.filesystemRootHostLocked()
		c.mu.Unlock()
		if rootEvent != nil {
			_ = c.notifyFilesystemRoot(context.Background(), rootHost, *rootEvent)
		}
		c.loginMu.Lock()
		delete(c.loginOwners, attempt.ID)
		c.loginMu.Unlock()
		completed <- result
		close(completed)
	}()
	attempt.Done = completed
	attempt.Events = events
	return attempt, nil
}

func (c *Chat) CancelOAuthLogin(attemptID string) bool {
	c.loginMu.Lock()
	coordinator := c.loginOwners[attemptID]
	c.loginMu.Unlock()
	return coordinator != nil && coordinator.CancelLogin(attemptID)
}

func (c *Chat) AuthMethods(providerID string) []provider.AuthMethod {
	methods := c.authRegistry.Methods(providerID)
	out := make([]provider.AuthMethod, 0, len(methods))
	for _, method := range methods {
		out = append(out, provider.AuthMethod(method))
	}
	return out
}

func (c *Chat) AuthStatus(ctx context.Context, providerID string, method auth.Method, profileID string) (auth.PublicStatus, error) {
	implementation, ok := c.authRegistry.Lookup(providerID, method)
	if !ok {
		return auth.PublicStatus{}, fmt.Errorf("authentication is unsupported for %s/%s", providerID, method)
	}
	status, ok := implementation.(auth.StatusResolver)
	if !ok {
		return auth.PublicStatus{Connection: "configured"}, nil
	}
	return status.PublicStatus(ctx, profileID)
}

func (c *Chat) DisconnectOAuth(ctx context.Context, profileID string, removeRegistration, confirmReset bool) (auth.OAuthDisconnectResult, error) {
	return c.Disconnect(ctx, "openai", auth.MethodOAuth, profileID, removeRegistration, confirmReset)
}

func (c *Chat) Disconnect(ctx context.Context, providerID string, method auth.Method, profileID string, removeRegistration, confirmReset bool) (auth.OAuthDisconnectResult, error) {
	implementation, found := c.authRegistry.Lookup(providerID, method)
	coordinator, ok := implementation.(auth.LoginCoordinator)
	if !found || !ok {
		return auth.OAuthDisconnectResult{}, fmt.Errorf("disconnect is unsupported for %s/%s", providerID, method)
	}
	c.mu.Lock()
	if c.credentialBusy || (c.active && c.activeProfile == profileID) {
		c.mu.Unlock()
		return auth.OAuthDisconnectResult{}, ErrRequestActive
	}
	selected := c.selected != nil && c.selected.ProfileID == profileID
	if selected && len(c.turns) > 0 && !confirmReset {
		c.mu.Unlock()
		return auth.OAuthDisconnectResult{}, ErrConversationReset
	}
	if selected {
		c.supersedeFilesystemRootOperationsLocked()
	}
	c.credentialBusy = true
	c.credentialBusyProfile = profileID
	c.mu.Unlock()
	result, err := coordinator.Disconnect(ctx, profileID, removeRegistration)
	c.mu.Lock()
	var rootEvent *FilesystemRootTransition
	if result.LocalTokensRemoved || result.LocalRegistrationRemoved {
		if selected && c.selected != nil && c.selected.ProfileID == profileID {
			c.selected = nil
			rootEvent = c.clearConversationLocked(FilesystemRootProfileChanged)
		}
		c.discovery = nil
	}
	c.credentialBusy = false
	c.credentialBusyProfile = ""
	c.mu.Unlock()
	if rootEvent != nil {
		err = errors.Join(err, c.notifyFilesystemRoot(ctx, c.filesystemRootHost(), *rootEvent))
	}
	return result, err
}

func (c *Chat) OpenOAuthUsage(ctx context.Context) error {
	if c.oauth == nil {
		return errors.New("OAuth account usage page is unavailable")
	}
	return c.oauth.OpenUsage(ctx)
}
func (c *Chat) Discover(ctx context.Context) model.Discovery {
	d := c.catalog.Discover(ctx)
	c.mu.Lock()
	c.discovery = &d
	c.mu.Unlock()
	return d
}
func (c *Chat) Snapshot() Snapshot {
	c.mu.Lock()
	var selected *model.Model
	if c.selected != nil {
		x := *c.selected
		selected = &x
	}
	usage := make([]ModelUsage, 0, len(c.usage))
	for _, item := range c.usage {
		usage = append(usage, *item)
	}
	sort.Slice(usage, func(i, j int) bool {
		if usage[i].Provider != usage[j].Provider {
			return usage[i].Provider < usage[j].Provider
		}
		return usage[i].ModelID < usage[j].ModelID
	})
	turns := append([]Turn(nil), c.turns...)
	for i := range turns {
		turns[i].ToolActivities = append([]ToolActivity(nil), turns[i].ToolActivities...)
	}
	activities := append([]ToolActivity(nil), c.activeToolActivities...)
	var plan []tools.PlanStep
	if c.toolRuntime != nil {
		plan = c.toolRuntime.plans.Snapshot(c.conversationID)
	}
	rootPath, rootGeneration, rootAck := c.filesystemRoot, c.filesystemRootGen, c.filesystemRootAck
	rootHost := c.filesystemRootHostLocked()
	active := c.active
	c.mu.Unlock()
	return Snapshot{Selected: selected, Turns: turns, Usage: usage, Active: active, ToolActivities: activities, Plan: plan, FilesystemRoot: filesystemRootStatus(rootPath, rootGeneration, rootAck, rootHost)}
}

func (c *Chat) recordUsageLocked(providerID provider.ID, modelID string, usage provider.Usage) {
	if modelID == "" {
		return
	}
	if c.usage == nil {
		c.usage = make(map[string]*ModelUsage)
	}
	key := string(providerID) + "\x00" + modelID
	item := c.usage[key]
	if item == nil {
		item = &ModelUsage{Provider: string(providerID), ModelID: modelID}
		c.usage[key] = item
	}
	inputKnown, outputKnown := usage.InputKnown, usage.OutputKnown
	if usage.Known && !inputKnown && !outputKnown {
		inputKnown, outputKnown = true, true
	}
	add := func(metric *UsageMetric, known bool, count int64) {
		if !known || count < 0 {
			metric.missing = true
			metric.Partial = metric.Known
			return
		}
		metric.Tokens += count
		metric.Known = true
		metric.Partial = metric.missing
	}
	add(&item.Input, inputKnown, usage.InputTokens)
	add(&item.Output, outputKnown, usage.OutputTokens)
	add(&item.CachedRead, usage.CacheReadKnown, usage.CacheRead)
	add(&item.CacheWrite, usage.CacheWriteKnown, usage.CacheWrite)
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
	if c.credentialBusy || (c.active && id == c.activeProfile) {
		c.mu.Unlock()
		return ErrRequestActive
	}
	c.credentialBusy = true
	c.credentialBusyProfile = id
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.credentialBusy = false
		c.credentialBusyProfile = ""
		c.mu.Unlock()
	}()
	p, _, err := c.profiles.ResolveContext(ctx, id)
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
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.active && id == c.activeProfile {
		c.mu.Unlock()
		return ErrRequestActive
	}
	if c.selected != nil && c.selected.ProfileID == id && len(c.turns) > 0 && !confirmReset {
		c.mu.Unlock()
		return ErrConversationReset
	}
	if c.selected != nil && c.selected.ProfileID == id {
		c.supersedeFilesystemRootOperationsLocked()
	}
	c.mu.Unlock()
	if err := c.profiles.ReplaceAPIKeyContext(ctx, id, label, key, route); err != nil {
		return err
	}
	c.mu.Lock()
	c.discovery = nil
	var rootEvent *FilesystemRootTransition
	if c.selected != nil && c.selected.ProfileID == id {
		if len(c.turns) > 0 {
			rootEvent = c.clearConversationLocked(FilesystemRootProfileChanged)
		} else {
			rootEvent = c.clearFilesystemRootLocked(FilesystemRootProfileChanged, true)
		}
		c.selected = nil
	}
	c.mu.Unlock()
	if rootEvent != nil {
		return c.notifyFilesystemRoot(ctx, c.filesystemRootHost(), *rootEvent)
	}
	return nil
}
func (c *Chat) RemoveProfile(id string, confirmReset bool) error {
	c.mu.Lock()
	if c.credentialBusy || (c.active && c.activeProfile == id) {
		c.mu.Unlock()
		return ErrRequestActive
	}
	if c.selected != nil && c.selected.ProfileID == id && len(c.turns) > 0 && !confirmReset {
		c.mu.Unlock()
		return ErrConversationReset
	}
	if c.selected != nil && c.selected.ProfileID == id {
		c.supersedeFilesystemRootOperationsLocked()
	}
	c.credentialBusy = true
	c.credentialBusyProfile = id
	c.mu.Unlock()
	err := c.profiles.RemoveContext(context.Background(), id)
	c.mu.Lock()
	c.credentialBusy = false
	c.credentialBusyProfile = ""
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.discovery = nil
	var rootEvent *FilesystemRootTransition
	if c.selected != nil && c.selected.ProfileID == id {
		c.selected = nil
		rootEvent = c.clearConversationLocked(FilesystemRootProfileChanged)
	}
	c.mu.Unlock()
	if rootEvent != nil {
		return c.notifyFilesystemRoot(context.Background(), c.filesystemRootHost(), *rootEvent)
	}
	return nil
}

func (c *Chat) Select(ctx context.Context, key string, confirmReset bool) error {
	c.mu.Lock()
	if c.closed || c.active || c.credentialBusy {
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
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	if c.selected != nil && c.selected.Key() == selected.Key() {
		c.mu.Unlock()
		return nil
	}
	if len(c.turns) > 0 && !confirmReset {
		c.mu.Unlock()
		return ErrConversationReset
	}
	var rootEvent *FilesystemRootTransition
	if len(c.turns) > 0 {
		rootEvent = c.clearConversationLocked(FilesystemRootModelChanged)
	} else {
		rootEvent = c.clearFilesystemRootLocked(FilesystemRootModelChanged, true)
	}
	c.selected = &selected
	rootHost := c.filesystemRootHostLocked()
	c.mu.Unlock()
	if rootEvent != nil {
		return c.notifyFilesystemRoot(ctx, rootHost, *rootEvent)
	}
	return nil
}
func (c *Chat) clearConversationLocked(reason FilesystemRootChangeReason) *FilesystemRootTransition {
	oldID := c.conversationID
	rootEvent := c.clearFilesystemRootLocked(reason, true)
	c.history = nil
	c.historySensitive = false
	c.historyDestination = ""
	c.turns = nil
	c.toolCallIDs = nil
	if c.toolRuntime != nil && oldID != "" {
		c.toolRuntime.clearConversationGrantsLocked(oldID)
		c.toolRuntime.plans.Close(oldID)
	}
	c.conversationID, c.conversationIDErr = newConversationID()
	if rootEvent != nil {
		rootEvent.ConversationID = oldID
	}
	return rootEvent
}

func (c *Chat) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	conversation, runtime := c.conversationID, c.toolRuntime
	c.mu.Unlock()
	if runtime != nil {
		runtime.invalidateConversation(conversation)
	}
	if cancel != nil {
		cancel()
	}
}

func (c *Chat) Send(parent context.Context, prompt string, onDelta func(string)) (Turn, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Turn{}, errors.New("message is empty")
	}
	// The turn lifetime follows caller cancellation/deadlines. The internal
	// provider timeout is applied separately to each provider operation so an
	// approval prompt is not silently expired by time spent in the turn.
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return Turn{}, errors.New("chat is closed")
	}
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		cancel()
		return Turn{}, ErrRequestActive
	}
	if c.selected == nil {
		c.mu.Unlock()
		cancel()
		return Turn{}, ErrNoSelection
	}
	selection := *c.selected
	plug, ok := c.catalog.Providers.Get(selection.Provider)
	if !ok {
		c.mu.Unlock()
		cancel()
		return Turn{}, ErrNoSuchModel
	}
	generation := c.sequence.Add(1)
	c.generation = generation
	c.active = true
	activeDone := make(chan struct{})
	c.activeDone = activeDone
	c.activeToolActivities = nil
	c.activeProfile = selection.ProfileID
	c.cancel = cancel
	if c.historySensitive && !c.readContinuationCurrentLocked(c.conversationID, selection, c.historyReadEpoch) {
		c.retireReadHistoryLocked()
	}
	// Keep the authority metadata paired with this private transcript snapshot.
	// A later invalidation can clear c.history after we release the mutex, but it
	// must not make this already-copied sensitive draft look like ordinary text.
	historySensitive := c.historySensitive
	historyEpoch := c.historyReadEpoch
	historyDestination := c.historyDestination
	history := cloneMessages(c.history)
	history = append(history, provider.Message{Role: provider.RoleUser, Text: prompt})
	requestID := fmt.Sprintf("chat-%d", generation)
	conversationID, conversationIDErr := c.conversationID, c.conversationIDErr
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		if c.generation == generation {
			c.active = false
			c.activeProfile = ""
			c.cancel = nil
			if c.activeDone == activeDone {
				c.activeDone = nil
			}
		}
		close(activeDone)
		c.mu.Unlock()
	}()

	finishError := func() {
		c.mu.Lock()
		if c.generation == generation {
			c.active = false
			c.activeProfile = ""
			c.cancel = nil
		}
		c.mu.Unlock()
	}
	stateIsCurrent := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.closed && c.generation == generation && c.active && !(c.credentialBusy && c.credentialBusyProfile == selection.ProfileID) && c.selected != nil && *c.selected == selection && ctx.Err() == nil
	}

	prof, err := c.profiles.GetContext(ctx, selection.ProfileID)
	if err != nil {
		finishError()
		return Turn{}, err
	}
	if prof.ID != selection.ProfileID || prof.Provider != string(selection.Provider) {
		finishError()
		return Turn{}, errors.New("selected credential changed; refresh and select a model again")
	}
	authMethod := auth.Method(prof.Auth.Type)
	prof, key, err := c.authRegistry.Resolve(ctx, prof.Provider, authMethod, selection.ProfileID)
	if err != nil {
		if ctx.Err() != nil {
			finishError()
			return Turn{}, ctx.Err()
		}
		turn := Turn{Prompt: prompt, ModelID: selection.ID, At: time.Now().UTC(), Outcome: provider.OutcomeAuthentication,
			Message: "The selected credential could not be resolved. Check its configuration or reconnect the selected account."}
		if failure, ok := auth.AsFailure(err); ok {
			switch failure.Kind {
			case auth.FailurePermission:
				turn.Outcome = provider.OutcomePermission
			case auth.FailureTemporary:
				turn.Outcome = provider.OutcomeNetwork
			case auth.FailureCanceled:
				turn.Outcome = provider.OutcomeInterrupted
			}
			turn.Message = failure.Error()
		} else if errors.Is(err, auth.ErrOAuthPermissionRequired) {
			turn.Outcome = provider.OutcomePermission
		}
		c.mu.Lock()
		if c.generation == generation && c.active && c.selected != nil && *c.selected == selection && ctx.Err() == nil {
			c.active = false
			c.activeProfile = ""
			c.cancel = nil
			c.turns = append(c.turns, turn)
		}
		c.mu.Unlock()
		return turn, err
	}
	if prof.ID != selection.ProfileID || prof.Provider != string(selection.Provider) {
		finishError()
		return Turn{}, errors.New("selected credential changed; refresh and select a model again")
	}
	if selection.ProfileVersion != "" && selection.ProfileVersion != model.ProfileVersion(prof, key) {
		finishError()
		return Turn{}, errors.New("selected credential changed; refresh and select a model again")
	}
	if !stateIsCurrent() {
		finishError()
		if err := ctx.Err(); err != nil {
			return Turn{}, err
		}
		return Turn{}, errors.New("chat selection changed while resolving credentials")
	}

	turn := Turn{Prompt: prompt, ModelID: selection.ID, At: time.Now().UTC()}
	if prof.ID != selection.ProfileID || prof.Provider != string(selection.Provider) ||
		(selection.ProfileVersion != "" && model.ProfileVersion(prof, key) != selection.ProfileVersion) {
		finishError()
		return Turn{}, errors.New("selected credential changed; refresh and select a model again")
	}
	if !stateIsCurrent() {
		finishError()
		if err := ctx.Err(); err != nil {
			return Turn{}, err
		}
		return Turn{}, errors.New("chat selection changed while resolving credentials")
	}
	loop := c.runToolLoop(ctx, generation, selection, plug, modelCredential(prof, key), history, historySensitive, historyEpoch, historyDestination, requestID, conversationID, conversationIDErr, stateIsCurrent, func(delta string) {
		if stateIsCurrent() && onDelta != nil {
			onDelta(delta)
		}
	})
	result, callErr := loop.result, loop.err
	turn.Text = result.Text
	turn.Refusal = result.Refusal
	turn.Outcome = result.Outcome
	turn.Usage = result.Usage
	turn.RequestID = result.RequestID
	turn.ProviderCode = result.ProviderCode
	turn.DiagnosticCode = result.DiagnosticCode
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
	turn.ToolActivities = append([]ToolActivity(nil), loop.activities...)
	if loop.message != "" && turn.Message == "" {
		turn.Message = loop.message
	}
	finalize := func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.generation != generation || c.closed {
			return errors.New("stale provider result discarded")
		}
		var finalGate *toolRuntime
		if loop.historySensitive && c.toolRuntime != nil {
			finalGate = c.toolRuntime
			finalGate.readAuthorityMu.Lock()
			defer finalGate.readAuthorityMu.Unlock()
		}
		c.active = false
		c.activeProfile = ""
		c.cancel = nil
		keepSensitiveHistory := true
		if loop.historySensitive {
			keepSensitiveHistory = finalGate != nil && c.readContinuationStateCurrentLocked(conversationID, selection, loop.historyEpoch, finalGate)
			if !keepSensitiveHistory {
				c.retireReadHistoryLocked()
				turn.Text, turn.Refusal = "", ""
				turn.Outcome = provider.OutcomeInterrupted
				turn.Message = "Conversation context was retired before the provider response could be retained. Start a fresh conversation to continue."
			}
		}
		if loop.persist {
			if !loop.historySensitive || keepSensitiveHistory {
				c.history = cloneMessages(loop.history)
			}
		} else if !loop.historySensitive && (turn.Outcome == provider.OutcomeCompleted || turn.Outcome == provider.OutcomeRefused) {
			if len(result.Native) == 0 {
				turn.Outcome = provider.OutcomeUnsupported
				turn.Message = "provider returned no replayable continuation data"
				turn.DiagnosticCode = provider.DiagnosticRuntimeMissingNative
			} else {
				c.history = append(c.history, provider.Message{Role: provider.RoleUser, Text: prompt}, provider.Message{Role: provider.RoleAssistant, Text: result.Text, Native: cloneRaw(result.Native)})
			}
		}
		c.turns = append(c.turns, turn)
		return nil
	}
	if loop.historySensitive {
		c.mu.Lock()
		finalGate := c.toolRuntime
		root, rootGeneration := c.filesystemRoot, c.filesystemRootGen
		current := c.generation == generation && !c.closed
		c.mu.Unlock()
		if !current {
			return turn, errors.New("stale provider result discarded")
		}
		if finalGate == nil || finalGate.readPublicationGate == nil {
			return c.finishRetiredSensitiveTurn(generation, &turn), callErr
		}
		policy, coverage, revisionsOK := finalGate.readPublicationRevisions(conversationID, nil)
		if !revisionsOK {
			return c.finishRetiredSensitiveTurn(generation, &turn), callErr
		}
		err := finalGate.readPublicationGate.WithFilesystemReadPublicationCurrent(ctx, conversationID, root, rootGeneration, policy, coverage, finalize)
		if err != nil {
			c.mu.Lock()
			stillCurrent := c.generation == generation && !c.closed
			c.mu.Unlock()
			if !stillCurrent {
				return turn, errors.New("stale provider result discarded")
			}
			return c.finishRetiredSensitiveTurn(generation, &turn), callErr
		}
	} else if err := finalize(); err != nil {
		return turn, err
	}
	if callErr != nil {
		return turn, callErr
	}
	return turn, nil
}

func (c *Chat) finishRetiredSensitiveTurn(generation uint64, turn *Turn) Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation || c.closed {
		return *turn
	}
	c.active = false
	c.activeProfile = ""
	c.cancel = nil
	if c.historySensitive {
		c.retireReadHistoryLocked()
	}
	turn.Text, turn.Refusal = "", ""
	turn.Outcome = provider.OutcomeInterrupted
	turn.Message = "Conversation context was retired before the provider response could be retained. Start a fresh conversation to continue."
	c.turns = append(c.turns, *turn)
	return *turn
}

func (c *Chat) updateToolActivity(generation uint64, activity ToolActivity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation || !c.active {
		return
	}
	c.activeToolActivities = append(c.activeToolActivities, activity)
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

func (c *Chat) retireReadHistory(conversationID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conversationID != conversationID || !c.historySensitive {
		return
	}
	c.retireReadHistoryLocked()
}

func (c *Chat) retireReadHistoryLocked() {
	c.history = nil
	c.historySensitive = false
	c.historyDestination = ""
	c.historyReadEpoch++
	for i := range c.turns {
		c.turns[i].Text = ""
		c.turns[i].Refusal = ""
		c.turns[i].Message = "Conversation context was retired after an authorization change. Start a fresh conversation to continue."
	}
}
