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
		out = append(out, ui.Profile{ID: p.ID, Provider: p.Provider, Label: p.Label, AuthType: p.AuthType})
	}
	return out, nil
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
		out.Turns = append(out.Turns, ui.Turn{Prompt: t.Prompt, Text: t.Text, Refusal: t.Refusal, Message: t.Message, RequestID: t.RequestID, ModelID: t.ModelID, Outcome: t.Outcome, Usage: t.Usage})
	}
	return out
}
func (c Client) Send(ctx context.Context, text string, delta func(string)) (ui.Turn, error) {
	t, err := c.Chat.Send(ctx, text, delta)
	return ui.Turn{Prompt: t.Prompt, Text: t.Text, Refusal: t.Refusal, Message: t.Message, RequestID: t.RequestID, ModelID: t.ModelID, Outcome: t.Outcome, Usage: t.Usage}, err
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
