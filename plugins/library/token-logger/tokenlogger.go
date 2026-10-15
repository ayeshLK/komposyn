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

// Package tokenlogger is the independent library surface for Komposyn's
// local-first token logger.
package tokenlogger

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Version is the current library version. Development builds use an explicit
// pre-release marker; release builds replace this value from the release tag
// at link time.
var Version = "0.0.0-dev"

const currentSchemaVersion = 7

// TokenUsage contains normalized token counts. Cache-read and cache-write
// tokens are input subsets; reasoning tokens are an output subset.
type TokenUsage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
}

func (u TokenUsage) validate() error {
	counts := map[string]int64{
		"input_tokens":       u.InputTokens,
		"output_tokens":      u.OutputTokens,
		"cache_read_tokens":  u.CacheReadTokens,
		"cache_write_tokens": u.CacheWriteTokens,
		"reasoning_tokens":   u.ReasoningTokens,
	}
	for name, count := range counts {
		if count < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	if u.CacheReadTokens+u.CacheWriteTokens > u.InputTokens {
		return errors.New("cache token counts must not exceed input tokens")
	}
	if u.ReasoningTokens > u.OutputTokens {
		return errors.New("reasoning tokens must not exceed output tokens")
	}
	return nil
}

// Cost records the provenance of an event's cost. AmountMicros is expressed
// in integer micro-units of Currency and is absent when Status is unknown.
type Cost struct {
	Status           string `json:"status"`
	AmountMicros     *int64 `json:"amount_micros,omitempty"`
	Currency         string `json:"currency,omitempty"`
	PricingVersion   string `json:"pricing_version,omitempty"`
	ProviderAmount   *int64 `json:"provider_amount,omitempty"`
	ProviderCurrency string `json:"provider_currency,omitempty"`
}

func (c Cost) validate() error {
	if c.Status == "" {
		return nil
	}
	if c.Status != "calculated" && c.Status != "reported" && c.Status != "unknown" {
		return fmt.Errorf("unsupported cost status %q", c.Status)
	}
	if c.Status == "unknown" && c.AmountMicros != nil {
		return errors.New("unknown cost must not have an amount")
	}
	if c.AmountMicros != nil && *c.AmountMicros < 0 {
		return errors.New("cost amount must not be negative")
	}
	return nil
}

// Client identifies the local agent that initiated a model request.
type Client struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// Provenance identifies the provider-owned record from which an event was
// normalized. Source content is intentionally not part of this structure.
type Provenance struct {
	Adapter      string `json:"adapter"`
	SourceID     string `json:"source_id"`
	SourceRecord string `json:"source_record"`
	SourceOffset *int64 `json:"source_offset,omitempty"`
	RecordHash   string `json:"record_hash,omitempty"`
}

// UsageEvent is the provider-neutral durable unit of usage. It represents one
// completed model request and is safe to retry when IngestionKey is stable.
type UsageEvent struct {
	EventID       string     `json:"event_id"`
	SchemaVersion int        `json:"schema_version"`
	EventType     string     `json:"event_type"`
	OccurredAt    time.Time  `json:"occurred_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`

	ProjectID      string          `json:"project_id,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	Attempt        int             `json:"attempt,omitempty"`
	Operation      string          `json:"operation,omitempty"`
	Client         Client          `json:"client"`
	Provider       string          `json:"provider"`
	RequestedModel string          `json:"requested_model,omitempty"`
	ResolvedModel  string          `json:"resolved_model,omitempty"`
	Usage          TokenUsage      `json:"usage"`
	Cost           Cost            `json:"cost,omitempty"`
	Provenance     Provenance      `json:"provenance"`
	IngestionKey   string          `json:"ingestion_key"`
	Attributes     json.RawMessage `json:"attributes,omitempty"`
}

// Validate checks the normalized event contract before persistence.
func (e UsageEvent) Validate() error {
	if strings.TrimSpace(e.EventID) == "" {
		return errors.New("event ID is required")
	}
	if e.SchemaVersion <= 0 {
		return errors.New("schema version must be positive")
	}
	if strings.TrimSpace(e.EventType) == "" {
		return errors.New("event type is required")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("occurred-at is required")
	}
	if e.OccurredAt.Location() != time.UTC {
		return errors.New("occurred-at must use UTC")
	}
	if e.StartedAt != nil && e.StartedAt.Location() != time.UTC {
		return errors.New("started-at must use UTC")
	}
	if e.CompletedAt != nil && e.CompletedAt.Location() != time.UTC {
		return errors.New("completed-at must use UTC")
	}
	if e.StartedAt != nil && e.CompletedAt != nil && e.CompletedAt.Before(*e.StartedAt) {
		return errors.New("completed-at must not precede started-at")
	}
	if e.Attempt < 0 {
		return errors.New("attempt must not be negative")
	}
	if strings.TrimSpace(e.Client.Name) == "" {
		return errors.New("client name is required")
	}
	if strings.TrimSpace(e.Provider) == "" {
		return errors.New("provider is required")
	}
	if strings.TrimSpace(e.RequestedModel) == "" && strings.TrimSpace(e.ResolvedModel) == "" {
		return errors.New("requested or resolved model is required")
	}
	if strings.TrimSpace(e.Provenance.Adapter) == "" || strings.TrimSpace(e.Provenance.SourceID) == "" || strings.TrimSpace(e.Provenance.SourceRecord) == "" {
		return errors.New("adapter, source ID, and source record are required")
	}
	if strings.TrimSpace(e.IngestionKey) == "" {
		return errors.New("ingestion key is required")
	}
	if len(e.Attributes) > 0 && !json.Valid(e.Attributes) {
		return errors.New("attributes must contain valid JSON")
	}
	if err := e.Usage.validate(); err != nil {
		return err
	}
	return e.Cost.validate()
}

// Project is a stable local project identity.
type Project struct {
	ID              string `json:"id"`
	Root            string `json:"root,omitempty"`
	DisplayName     string `json:"display_name"`
	SourceAdapter   string `json:"source_adapter,omitempty"`
	ProviderAlias   string `json:"provider_alias,omitempty"`
	AliasProvenance string `json:"alias_provenance,omitempty"`
}

// Session associates a sequence of events with a project and client.
type Session struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id,omitempty"`
	Client         Client          `json:"client"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	EndedAt        *time.Time      `json:"ended_at,omitempty"`
	SourceMetadata json.RawMessage `json:"source_metadata,omitempty"`
	SourceAdapter  string          `json:"source_adapter,omitempty"`
	ProviderAlias  string          `json:"provider_alias,omitempty"`
}

// Cursor records the committed read position of a provider source.
type Cursor struct {
	SourceID             string
	FileFingerprint      string
	ByteOffset           int64
	LastProviderRecordID string
	SourceGeneration     int64
}

func (c Cursor) validate() error {
	if strings.TrimSpace(c.SourceID) == "" {
		return errors.New("cursor source ID is required")
	}
	if c.ByteOffset < 0 {
		return errors.New("cursor byte offset must not be negative")
	}
	return nil
}
