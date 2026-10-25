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

// Package ui loads the configured UI and adapts the in-memory chat client.
package ui

import (
	"context"
	"errors"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/ui"
)

type Client struct {
	Chat         *runtime.Chat
	ProfileStore auth.Store
}

func (c Client) Profiles(ctx context.Context) ([]ui.Profile, error) {
	items, err := c.ProfileStore.ListMetadata()
	if err != nil {
		return nil, err
	}
	out := make([]ui.Profile, 0, len(items))
	for _, p := range items {
		out = append(out, ui.Profile{ID: p.ID, Provider: p.Provider, Label: p.Label, AuthType: p.AuthType, Account: p.Account, Status: p.Status, PlanUsageEnabled: p.PlanUsageEnabled})
	}
	return out, nil
}
func (c Client) AuthMethods(providerID string) []string {
	methods := c.Chat.AuthMethods(providerID)
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		out = append(out, string(method))
	}
	return out
}
func (c Client) Discover(ctx context.Context) (ui.Discovery, error) {
	d := c.Chat.Discover(ctx)
	out := ui.Discovery{Models: []ui.Model{}, Failures: []ui.Failure{}}
	for _, m := range d.Models {
		out.Models = append(out.Models, ui.Model{Key: m.Key(), Provider: string(m.Provider), ProfileID: m.ProfileID, ProfileLabel: m.ProfileLabel, ID: m.ID, DisplayName: m.DisplayName, Compatibility: m.Compatibility, Reason: m.Reason})
	}
	for _, f := range d.Failures {
		out.Failures = append(out.Failures, ui.Failure{ProfileID: f.ProfileID, Provider: f.Provider, Label: f.Label, Outcome: f.Outcome})
	}
	return out, nil
}
func (c Client) AddProfile(ctx context.Context, id, providerID, label, key string, route map[string]string) error {
	return c.Chat.AddProfile(ctx, id, providerID, label, key, route)
}
func (c Client) ReplaceProfile(ctx context.Context, id, label, key string, route map[string]string, confirm bool) error {
	err := c.Chat.ReplaceProfile(ctx, id, label, key, route, confirm)
	return adaptError(err)
}
func (c Client) StartLogin(ctx context.Context, profileID string) (ui.LoginAttempt, error) {
	attempt, err := c.Chat.StartOAuthLogin(ctx, profileID)
	if err != nil {
		return ui.LoginAttempt{}, err
	}
	done := make(chan ui.LoginResult, 1)
	go func() {
		result := <-attempt.Done
		item := ui.LoginResult{Err: result.Err}
		if result.Err == nil && result.Profile.Auth.OAuth != nil {
			item.ProfileID = result.Profile.ID
			item.Account = result.Profile.Auth.OAuth.Email
			item.Status = "connected"
			for _, scope := range result.Profile.Auth.OAuth.Scopes {
				if scope == "chatgpt.tokens.use.direct" {
					item.Status = "plan_usage_enabled"
				}
			}
			if item.Account == "" {
				item.Account = "ChatGPT account"
			}
		}
		done <- item
		close(done)
	}()
	return ui.LoginAttempt{ID: attempt.ID, Done: done}, nil
}
func (c Client) CancelLogin(id string) bool { return c.Chat.CancelOAuthLogin(id) }
func (c Client) Disconnect(ctx context.Context, id string, remove, confirm bool) (ui.DisconnectResult, error) {
	result, err := c.Chat.DisconnectOAuth(ctx, id, remove, confirm)
	return ui.DisconnectResult{RemoteRevocationConfirmed: result.RemoteRevocationConfirmed, LocalRegistrationRemoved: result.LocalRegistrationRemoved, LocalTokensRemoved: result.LocalTokensRemoved}, adaptError(err)
}
func (c Client) OpenUsage() error { return c.Chat.OpenOAuthUsage() }
func (c Client) RemoveProfile(id string, confirm bool) error {
	return adaptError(c.Chat.RemoveProfile(id, confirm))
}
func (c Client) Select(ctx context.Context, key string, confirm bool) error {
	return adaptError(c.Chat.Select(ctx, key, confirm))
}
func (c Client) Snapshot() ui.Snapshot {
	s := c.Chat.Snapshot()
	out := ui.Snapshot{Turns: make([]ui.Turn, 0, len(s.Turns)), Active: s.Active}
	if s.Selected != nil {
		out.Selected = convertModel(*s.Selected)
	}
	for _, t := range s.Turns {
		out.Turns = append(out.Turns, ui.Turn{Prompt: t.Prompt, Text: t.Text, Refusal: t.Refusal, Message: t.Message, RequestID: t.RequestID, ModelID: t.ModelID, ProviderCode: t.ProviderCode, Outcome: t.Outcome, Usage: t.Usage})
	}
	return out
}
func (c Client) Send(ctx context.Context, text string, delta func(string)) (ui.Turn, error) {
	t, err := c.Chat.Send(ctx, text, delta)
	return ui.Turn{Prompt: t.Prompt, Text: t.Text, Refusal: t.Refusal, Message: t.Message, RequestID: t.RequestID, ModelID: t.ModelID, ProviderCode: t.ProviderCode, Outcome: t.Outcome, Usage: t.Usage}, err
}
func (c Client) Cancel() { c.Chat.Cancel() }

func adaptError(err error) error {
	if errors.Is(err, runtime.ErrConversationReset) {
		return ui.ErrConversationReset
	}
	if errors.Is(err, runtime.ErrRequestActive) {
		return ui.ErrRequestActive
	}
	return err
}

func Load(id string, plugins []ui.Plugin) (ui.Plugin, error) {
	for _, p := range plugins {
		if p.ID() == id {
			return p, nil
		}
	}
	return nil, errors.New("configured UI plugin is unavailable")
}
func convertModel(m model.Model) *ui.Model {
	return &ui.Model{Key: m.Key(), Provider: string(m.Provider), ProfileID: m.ProfileID, ProfileLabel: m.ProfileLabel, ID: m.ID, DisplayName: m.DisplayName, Compatibility: m.Compatibility, Reason: m.Reason}
}
