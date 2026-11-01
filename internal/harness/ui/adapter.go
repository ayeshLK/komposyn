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
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/ui"
)

type Client struct {
	Chat          *runtime.Chat
	ProfileStore  auth.Store
	Version       string
	Approval      *ApprovalBridge
	Clarification *ClarificationBridge
}

func (c Client) ApprovalEvents() <-chan ui.ApprovalEvent {
	if c.Approval == nil {
		return nil
	}
	return c.Approval.Events()
}
func (c Client) RespondApproval(response ui.ApprovalResponse) error {
	if c.Approval == nil {
		return ErrApprovalResponse
	}
	return c.Approval.Respond(response)
}
func (c Client) ClarificationEvents() <-chan ui.ClarificationEvent {
	if c.Clarification == nil {
		return nil
	}
	return c.Clarification.Events()
}
func (c Client) RespondClarification(response ui.ClarificationResponse) error {
	if c.Clarification == nil {
		return ErrClarificationResponse
	}
	return c.Clarification.Respond(response)
}
func (c Client) Permissions(ctx context.Context) ([]ui.ToolGrant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []ui.ToolGrant{}
	for _, grant := range c.Chat.ToolGrants() {
		action := grant.ActionDescription
		if action == "" {
			action = grant.ToolID + " " + string(grant.Operation) + " action"
		}
		item := ui.ToolGrant{ID: grant.ID, ToolID: grant.ToolID, Action: action, Operation: string(grant.Operation), Lifetime: string(grant.Lifetime), Origin: grant.Origin}
		if grant.Resource != nil {
			item.ResourceKind = string(grant.Resource.Kind)
			item.ResourceScope = grant.Resource.Root
			if grant.Resource.Kind == permissions.ResourceConversation {
				item.ResourceScope = "current conversation"
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func (c Client) RevokePermission(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Chat.RevokeToolGrant(id)
}
func (c Client) ClearPermissions(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return c.Chat.ClearToolGrants(), nil
}

func (c Client) Profiles(ctx context.Context) ([]ui.Profile, error) {
	items, err := c.ProfileStore.ListMetadataContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ui.Profile, 0, len(items))
	for _, p := range items {
		status, statusErr := c.Chat.AuthStatus(ctx, p.Provider, auth.Method(p.AuthType), p.ID)
		if statusErr != nil {
			return nil, statusErr
		}
		out = append(out, ui.Profile{ID: p.ID, Provider: p.Provider, Label: p.Label, AuthType: p.AuthType,
			Account: status.Account, Status: status.Connection, Permissions: convertPermissions(status.Permissions)})
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
		out.Failures = append(out.Failures, ui.Failure{ProfileID: f.ProfileID, Provider: f.Provider, Label: f.Label, Message: f.Message, Outcome: f.Outcome})
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
func (c Client) StartLogin(ctx context.Context, providerID, method, profileID string) (ui.LoginAttempt, error) {
	attempt, err := c.Chat.StartLogin(ctx, providerID, auth.Method(method), profileID)
	if err != nil {
		return ui.LoginAttempt{}, err
	}
	done := make(chan ui.LoginResult, 1)
	events := make(chan ui.LoginEvent, 4)
	go func() {
		defer close(events)
		if attempt.Events == nil {
			return
		}
		for event := range attempt.Events {
			events <- ui.LoginEvent{Kind: string(event.Kind), Message: event.Message, Succeeded: event.Succeeded}
		}
	}()
	go func() {
		result := <-attempt.Done
		item := ui.LoginResult{Err: result.Err, Status: result.Status.Connection, Permissions: convertPermissions(result.Status.Permissions)}
		if result.Err == nil {
			item.ProfileID = result.ProfileID
			item.Account = result.Status.Account
		}
		done <- item
		close(done)
	}()
	return ui.LoginAttempt{ID: attempt.ID, Events: events, Done: done}, nil
}
func (c Client) CancelLogin(id string) bool { return c.Chat.CancelOAuthLogin(id) }
func (c Client) Disconnect(ctx context.Context, providerID, method, id string, remove, confirm bool) (ui.DisconnectResult, error) {
	result, err := c.Chat.Disconnect(ctx, providerID, auth.Method(method), id, remove, confirm)
	return ui.DisconnectResult{RemoteRevocationConfirmed: result.RemoteRevocationConfirmed, LocalRegistrationRemoved: result.LocalRegistrationRemoved, LocalTokensRemoved: result.LocalTokensRemoved}, adaptError(err)
}
func (c Client) OpenUsage(ctx context.Context) error { return c.Chat.OpenOAuthUsage(ctx) }
func (c Client) RemoveProfile(id string, confirm bool) error {
	return adaptError(c.Chat.RemoveProfile(id, confirm))
}
func (c Client) Select(ctx context.Context, key string, confirm bool) error {
	return adaptError(c.Chat.Select(ctx, key, confirm))
}
func (c Client) Snapshot() ui.Snapshot {
	s := c.Chat.Snapshot()
	out := ui.Snapshot{Turns: make([]ui.Turn, 0, len(s.Turns)), Usage: make([]ui.ModelUsage, 0, len(s.Usage)), Active: s.Active, Version: c.Version}
	for _, step := range s.Plan {
		out.Plan = append(out.Plan, ui.PlanStep{Step: step.Step, Status: string(step.Status)})
	}
	for _, activity := range s.ToolActivities {
		out.ToolActivities = append(out.ToolActivities, convertActivity(activity))
	}
	if s.Selected != nil {
		out.Selected = convertModel(*s.Selected)
	}
	for _, t := range s.Turns {
		turn := ui.Turn{Prompt: t.Prompt, Text: t.Text, Refusal: t.Refusal, Message: t.Message, RequestID: t.RequestID, ModelID: t.ModelID, ProviderCode: t.ProviderCode, DiagnosticCode: t.DiagnosticCode, Outcome: t.Outcome, Usage: t.Usage}
		for _, activity := range t.ToolActivities {
			turn.ToolActivities = append(turn.ToolActivities, convertActivity(activity))
		}
		out.Turns = append(out.Turns, turn)
	}
	for _, item := range s.Usage {
		out.Usage = append(out.Usage, ui.ModelUsage{Provider: item.Provider, ModelID: item.ModelID,
			Input:      ui.MetricUsage{Tokens: item.Input.Tokens, Known: item.Input.Known, Partial: item.Input.Partial},
			Output:     ui.MetricUsage{Tokens: item.Output.Tokens, Known: item.Output.Known, Partial: item.Output.Partial},
			CachedRead: ui.MetricUsage{Tokens: item.CachedRead.Tokens, Known: item.CachedRead.Known, Partial: item.CachedRead.Partial},
			CacheWrite: ui.MetricUsage{Tokens: item.CacheWrite.Tokens, Known: item.CacheWrite.Known, Partial: item.CacheWrite.Partial}})
	}
	return out
}
func (c Client) Send(ctx context.Context, text string, delta func(string)) (ui.Turn, error) {
	t, err := c.Chat.Send(ctx, text, delta)
	turn := ui.Turn{Prompt: t.Prompt, Text: t.Text, Refusal: t.Refusal, Message: t.Message, RequestID: t.RequestID, ModelID: t.ModelID, ProviderCode: t.ProviderCode, DiagnosticCode: t.DiagnosticCode, Outcome: t.Outcome, Usage: t.Usage}
	for _, activity := range t.ToolActivities {
		turn.ToolActivities = append(turn.ToolActivities, convertActivity(activity))
	}
	return turn, err
}
func convertActivity(activity runtime.ToolActivity) ui.ToolActivity {
	return ui.ToolActivity{CallID: activity.CallID, ToolID: string(activity.ToolID), Phase: activity.Phase, Outcome: string(activity.Outcome), Message: activity.Message, Truncated: activity.Truncated}
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

func convertPermissions(permissions []auth.Permission) []ui.Permission {
	out := make([]ui.Permission, 0, len(permissions))
	for _, permission := range permissions {
		out = append(out, ui.Permission{ID: permission.ID, Name: permission.Name, State: string(permission.State), Description: permission.Description})
	}
	return out
}
