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

package tokenlogger

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func TestQueryCalculatesCostWithoutDoubleCountingSubsets(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertProject(Project{ID: "project-1", DisplayName: "Project"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSession(Session{ID: "session-1", ProjectID: "project-1", Client: Client{Name: "codex-cli"}}); err != nil {
		t.Fatal(err)
	}
	event := testEvent("pricing-event")
	event.EventID = "pricing-event"
	event.ProjectID = "project-1"
	event.SessionID = "session-1"
	reportedAmount := int64(500)
	event.Cost = Cost{Status: "reported", AmountMicros: &reportedAmount, Currency: "USD"}
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	for _, rule := range []PricingRule{
		{Provider: "openai", ModelSelector: "gpt-test", TokenCategory: TokenCategoryInput, RateMicros: 2, Currency: "USD", EffectiveFrom: from, PricingVersion: "v1"},
		{Provider: "openai", ModelSelector: "gpt-test", TokenCategory: TokenCategoryOutput, RateMicros: 3, Currency: "USD", EffectiveFrom: from, PricingVersion: "v1"},
		{Provider: "openai", ModelSelector: "gpt-test", TokenCategory: TokenCategoryCacheRead, RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "v1"},
		{Provider: "openai", ModelSelector: "gpt-test", TokenCategory: TokenCategoryReasoning, RateMicros: 4, Currency: "USD", EffectiveFrom: from, PricingVersion: "v1"},
	} {
		if err := store.UpsertPricing(rule); err != nil {
			t.Fatal(err)
		}
	}
	report, err := store.Query(UsageFilter{From: from, To: from.AddDate(0, 0, 1), GroupBy: []string{GroupClient, GroupModel}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.InputTokens != 100 || report.Summary.CachedInputTokens != 25 || report.Summary.TotalTokens != 120 {
		t.Fatalf("unexpected usage summary: %+v", report.Summary)
	}
	if report.Summary.CalculatedCostMicros != 240 || report.Summary.CalculatedCurrency != "USD" || report.Summary.UnknownCostRequests != 0 {
		t.Fatalf("unexpected cost summary: %+v", report.Summary)
	}
	if report.Summary.ProviderReportedCostMicros != reportedAmount || report.Summary.EstimatedCostRequests != 1 {
		t.Fatalf("reported and estimated costs were not kept separately: %+v", report.Summary)
	}
	if report.Summary.PricingProvider != "openai" || report.Summary.CalculationBasis != "custom_pricing_estimate" {
		t.Fatalf("pricing provenance = provider %q basis %q", report.Summary.PricingProvider, report.Summary.CalculationBasis)
	}
	var jsonOutput bytes.Buffer
	if err := WriteJSON(&jsonOutput, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOutput.String(), `"pricing_provider": "openai"`) || !strings.Contains(jsonOutput.String(), `"calculation_basis": "custom_pricing_estimate"`) {
		t.Fatalf("JSON omitted provider/basis provenance: %s", jsonOutput.String())
	}
	var csvOutput bytes.Buffer
	if err := WriteCSV(&csvOutput, report); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(csvOutput.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if records[0][18] != "pricing_provider" || records[0][19] != "calculation_basis" || records[1][18] != "openai" || records[1][19] != "custom_pricing_estimate" {
		t.Fatalf("CSV omitted provider/basis provenance: %#v", records)
	}
	if len(report.Rows) != 1 || report.Rows[0].RequestCount != 1 {
		t.Fatalf("unexpected grouped rows: %+v", report.Rows)
	}
}

func TestQueryUsesPersistedBillingContextSelectors(t *testing.T) {
	store := openTestStore(t)
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	rule := PricingRule{Provider: "openai", ModelSelector: "context-model", TokenCategory: TokenCategoryInput,
		RateMicros: 7, Currency: "USD", EffectiveFrom: from, PricingVersion: "context-v1", ServiceTierSelector: "standard"}
	if err := store.UpsertPricing(rule); err != nil {
		t.Fatal(err)
	}
	event := testEvent("context-priced")
	event.EventID, event.RequestedModel, event.ResolvedModel = "context-priced", "context-model", "context-model"
	event.OccurredAt = from.Add(time.Hour)
	event.Usage = TokenUsage{InputTokens: 1}
	event.BillingContext.ServiceTier = &BillingContextValue{Value: "standard", Evidence: BillingEvidenceProviderResponse}
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := store.db.QueryRow("SELECT billing_context_json FROM usage_events WHERE event_id = ?", event.EventID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var roundTrip BillingContext
	if err := json.Unmarshal([]byte(stored), &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.ServiceTier == nil || roundTrip.ServiceTier.Value != "standard" || roundTrip.ServiceTier.Evidence != BillingEvidenceProviderResponse {
		t.Fatalf("persisted billing context = %+v", roundTrip)
	}
	filter := UsageFilter{From: from, To: from.AddDate(0, 0, 1)}
	priced, err := store.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	if priced.Summary.CalculatedCostMicros != 7 || priced.Summary.UnknownCostRequests != 0 {
		t.Fatalf("known context report = %+v", priced.Summary)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET billing_context_json = NULL WHERE event_id = ?", event.EventID); err != nil {
		t.Fatal(err)
	}
	unknown, err := store.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Summary.UnknownCostRequests != 1 || unknown.Summary.CalculatedCostMicros != 0 {
		t.Fatalf("missing context silently selected a tiered rule: %+v", unknown.Summary)
	}
}

func TestOpenAIPublicCatalogPricingRequiresExactModelAndObservedTier(t *testing.T) {
	store := openTestStore(t)
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	makeEvent := func(id, model, tier string) UsageEvent {
		event := testEvent(id)
		event.EventID, event.Provider = id, "openai"
		event.RequestedModel, event.ResolvedModel = model, model
		event.OccurredAt = at
		event.Usage = TokenUsage{InputTokens: 2_000_000, CacheReadTokens: 1_000_000, OutputTokens: 1_000_000, ReasoningTokens: 100_000}
		if tier != "" {
			event.BillingContext.ServiceTier = &BillingContextValue{Value: tier, Evidence: BillingEvidenceProviderResponse}
		}
		return event
	}

	known := makeEvent("openai-default", "gpt-5.3-codex", "default")
	if _, err := store.Append([]UsageEvent{known}); err != nil {
		t.Fatal(err)
	}
	filter := UsageFilter{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	report, err := store.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.CalculatedCostMicros != 15_925_000 || report.Summary.UnknownCostRequests != 0 {
		t.Fatalf("default-tier report cost/unknown = %d/%d, want 15925000/0", report.Summary.CalculatedCostMicros, report.Summary.UnknownCostRequests)
	}
	if report.Summary.PricingVersion != "public-api-2026-10-16-v1" || report.Summary.CalculationBasis != "public_catalog_estimate" {
		t.Fatalf("default-tier pricing provenance = version %q, basis %q", report.Summary.PricingVersion, report.Summary.CalculationBasis)
	}
	fastStore := openTestStore(t)
	defer fastStore.Close()
	fast := makeEvent("openai-fast", "gpt-5.3-codex", "priority")
	if _, err := fastStore.Append([]UsageEvent{fast}); err != nil {
		t.Fatal(err)
	}
	fastReport, err := fastStore.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	if fastReport.Summary.CalculatedCostMicros != 31_850_000 || fastReport.Summary.UnknownCostRequests != 0 {
		t.Fatalf("fast-tier report cost/unknown = %d/%d, want 31850000/0", fastReport.Summary.CalculatedCostMicros, fastReport.Summary.UnknownCostRequests)
	}

	for _, tc := range []struct {
		name, model, tier, reason string
		at                        time.Time
	}{
		{name: "tier absent", model: "gpt-5.3-codex", reason: "billing_context_missing"},
		{name: "unsupported tier", model: "gpt-5.3-codex", tier: "flex", reason: "billing_context_missing"},
		{name: "model alias", model: "codex-gpt-5.3", tier: "default", reason: "no_matching_rate"},
		{name: "cache write", model: "gpt-5.3-codex", tier: "default", reason: "billing_context_missing"},
		{name: "before reviewed interval", model: "gpt-5.3-codex", tier: "default", reason: "outside_effective_range", at: time.Date(2026, 10, 15, 23, 59, 59, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			separate := openTestStore(t)
			defer separate.Close()
			event := makeEvent("unknown-"+tc.name, tc.model, tc.tier)
			if !tc.at.IsZero() {
				event.OccurredAt = tc.at
			}
			if tc.name == "cache write" {
				event.Usage = TokenUsage{InputTokens: 1, CacheWriteTokens: 1}
			}
			if _, err := separate.Append([]UsageEvent{event}); err != nil {
				t.Fatal(err)
			}
			unknownFilter := UsageFilter{From: event.OccurredAt.Add(-time.Hour), To: event.OccurredAt.Add(time.Hour)}
			unknown, err := separate.Query(unknownFilter)
			if err != nil {
				t.Fatal(err)
			}
			if unknown.Summary.UnknownCostRequests != 1 || unknown.Summary.CostUnknownReason != tc.reason {
				t.Fatalf("unknown report = %+v, want reason %q", unknown.Summary, tc.reason)
			}
		})
	}
}

func TestAnthropicCatalogRequiresCompleteObservedModifiers(t *testing.T) {
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, model, tier, region, speed string
		want                             int64
	}{
		{name: "Opus standard global", model: "claude-opus-5-5", tier: "standard", region: "global", speed: "standard", want: 10_660_000},
		{name: "Opus batch global", model: "claude-opus-5-5", tier: "batch", region: "global", speed: "standard", want: 5_330_000},
		{name: "Opus fast global", model: "claude-opus-5-5", tier: "standard", region: "global", speed: "fast", want: 21_320_000},
		{name: "Opus standard US", model: "claude-opus-5-5", tier: "standard", region: "us", speed: "standard", want: 11_726_000},
		{name: "Sonnet standard global", model: "claude-sonnet-5-5", tier: "standard", region: "global", speed: "standard", want: 5_330_000},
		{name: "Sonnet batch US", model: "claude-sonnet-5-5", tier: "batch", region: "us", speed: "standard", want: 2_931_500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			event := testEvent(tc.name)
			event.EventID, event.Provider = tc.name, "anthropic"
			event.RequestedModel, event.ResolvedModel = tc.model, tc.model
			event.OccurredAt = at
			event.Usage = TokenUsage{InputTokens: 1_300_000, CacheReadTokens: 200_000, CacheWriteTokens: 100_000, CacheWrite5mTokens: 60_000, CacheWrite1hTokens: 40_000, OutputTokens: 300_000}
			event.BillingContext = BillingContext{
				ServiceTier: &BillingContextValue{Value: tc.tier, Evidence: BillingEvidenceProviderResponse},
				Region:      &BillingContextValue{Value: tc.region, Evidence: BillingEvidenceProviderResponse},
				Speed:       &BillingContextValue{Value: tc.speed, Evidence: BillingEvidenceProviderResponse},
			}
			if _, err := store.Append([]UsageEvent{event}); err != nil {
				t.Fatal(err)
			}
			report, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			if report.Summary.CalculatedCostMicros != tc.want || report.Summary.UnknownCostRequests != 0 {
				t.Fatalf("reported cost/unknown = %d/%d, want %d/0", report.Summary.CalculatedCostMicros, report.Summary.UnknownCostRequests, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name, tier, region, speed string
	}{
		{name: "missing service tier", region: "global", speed: "standard"},
		{name: "missing geography", tier: "standard", speed: "standard"},
		{name: "missing speed", tier: "standard", region: "global"},
		{name: "unlisted priority tier", tier: "priority", region: "global", speed: "standard"},
		{name: "fast unsupported for Sonnet", tier: "standard", region: "global", speed: "fast"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			event := testEvent(tc.name)
			event.EventID, event.Provider = tc.name, "anthropic"
			event.RequestedModel, event.ResolvedModel = "claude-sonnet-5-5", "claude-sonnet-5-5"
			event.OccurredAt = at
			event.Usage = TokenUsage{InputTokens: 1_300_000, CacheReadTokens: 200_000, CacheWriteTokens: 100_000, CacheWrite5mTokens: 60_000, CacheWrite1hTokens: 40_000, OutputTokens: 300_000}
			if tc.tier != "" {
				event.BillingContext.ServiceTier = &BillingContextValue{Value: tc.tier, Evidence: BillingEvidenceProviderResponse}
			}
			if tc.region != "" {
				event.BillingContext.Region = &BillingContextValue{Value: tc.region, Evidence: BillingEvidenceProviderResponse}
			}
			if tc.speed != "" {
				event.BillingContext.Speed = &BillingContextValue{Value: tc.speed, Evidence: BillingEvidenceProviderResponse}
			}
			if _, err := store.Append([]UsageEvent{event}); err != nil {
				t.Fatal(err)
			}
			report, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			if report.Summary.UnknownCostRequests != 1 || report.Summary.CalculatedCostMicros != 0 {
				t.Fatalf("incomplete-context report = %+v, want an explicitly unknown request", report.Summary)
			}
		})
	}
}

func TestContextMissingReasonIsNotMaskedByExpiredOtherTier(t *testing.T) {
	store := openTestStore(t)
	currentFrom := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	expiredFrom, expiredTo := currentFrom.AddDate(0, -1, 0), currentFrom
	for _, rule := range []PricingRule{
		{Provider: "openai", ModelSelector: "review-model", TokenCategory: TokenCategoryInput, RateMicros: 1, Currency: "USD", EffectiveFrom: currentFrom, PricingVersion: "tier-v1", ServiceTierSelector: "standard"},
		{Provider: "openai", ModelSelector: "review-model", TokenCategory: TokenCategoryInput, RateMicros: 2, Currency: "USD", EffectiveFrom: expiredFrom, EffectiveTo: &expiredTo, PricingVersion: "tier-v1", ServiceTierSelector: "priority"},
	} {
		if err := store.UpsertPricing(rule); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	missing := testEvent("review-missing-context")
	missing.EventID, missing.RequestedModel, missing.ResolvedModel = "review-missing-context", "review-model", "review-model"
	missing.OccurredAt, missing.Usage = at, TokenUsage{InputTokens: 1}
	known := missing
	known.EventID, known.IngestionKey = "review-known-priority", "review-known-priority"
	known.BillingContext.ServiceTier = &BillingContextValue{Value: "priority", Evidence: BillingEvidenceProviderResponse}
	if _, err := store.Append([]UsageEvent{missing, known}); err != nil {
		t.Fatal(err)
	}
	report, err := store.Query(UsageFilter{From: at.Add(-time.Hour), To: at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var reasons map[string]int64
	if err := json.Unmarshal([]byte(report.Summary.CostUnknownReasons), &reasons); err != nil {
		t.Fatal(err)
	}
	if reasons["billing_context_missing"] != 1 || reasons["outside_effective_range"] != 1 {
		t.Fatalf("unknown-cost reason counts = %v", reasons)
	}
	var human strings.Builder
	if err := WriteHumanPricingCoverage(&human, report.PricingCoverage, 100); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"billing context or rate", "outside pricing dates"} {
		if !strings.Contains(human.String(), expected) {
			t.Fatalf("human coverage %q omitted %q", human.String(), expected)
		}
	}
}

func TestQueryMarksMissingPricingAsUnknown(t *testing.T) {
	store := openTestStore(t)
	event := testEvent("unknown-price")
	event.EventID = "unknown-price"
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	report, err := store.Query(UsageFilter{From: from, To: from.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.UnknownCostRequests != 1 || report.Summary.CalculatedCostMicros != 0 {
		t.Fatalf("unexpected unknown cost summary: %+v", report.Summary)
	}
}

func TestStoredCalculatedCostIsNotTreatedAsCatalogEstimate(t *testing.T) {
	store := openTestStore(t)
	event := testEvent("untrusted-stored-cost")
	amount := int64(987654)
	event.Cost = Cost{Status: "calculated", AmountMicros: &amount, Currency: "USD", PricingVersion: "caller-value"}
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	from := event.OccurredAt
	report, err := store.Query(UsageFilter{From: from, To: from.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.CalculatedCostMicros != 0 || report.Summary.EstimatedCostRequests != 0 || report.Summary.UnknownCostRequests != 1 {
		t.Fatalf("stored caller calculation was presented as a catalog estimate: %+v", report.Summary)
	}
}

func TestQueryRejectsDuplicateGroups(t *testing.T) {
	store := openTestStore(t)
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	_, err := store.Query(UsageFilter{From: from, To: from.AddDate(0, 0, 1), GroupBy: []string{GroupClient, GroupClient}})
	if err == nil || !strings.Contains(err.Error(), `duplicate group-by "client"`) {
		t.Fatalf("duplicate group error = %v", err)
	}
}

func TestHourlyBucketsUseLocalCivilHours(t *testing.T) {
	cases := []struct {
		name     string
		location *time.Location
		instant  time.Time
		want     string
	}{
		{name: "UTC", location: time.UTC, instant: time.Date(2026, 10, 11, 10, 45, 0, 0, time.UTC), want: "2026-10-11T10:00:00Z"},
		{name: "whole-hour zone", location: time.FixedZone("UTC-5", -5*60*60), instant: time.Date(2026, 10, 11, 15, 45, 0, 0, time.UTC), want: "2026-10-11T10:00:00-05:00"},
		{name: "Colombo", location: time.FixedZone("Asia/Colombo", 5*60*60+30*60), instant: time.Date(2026, 10, 11, 5, 15, 0, 0, time.UTC), want: "2026-10-11T10:00:00+05:30"},
		{name: "Kathmandu", location: time.FixedZone("Asia/Kathmandu", 5*60*60+45*60), instant: time.Date(2026, 10, 11, 5, 0, 0, 0, time.UTC), want: "2026-10-11T10:00:00+05:45"},
		{name: "DST spring", location: mustLoadLocation(t, "America/New_York"), instant: time.Date(2026, 3, 8, 7, 15, 0, 0, time.UTC), want: "2026-03-08T03:00:00-04:00"},
		{name: "DST fall first hour", location: mustLoadLocation(t, "America/New_York"), instant: time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), want: "2026-11-01T01:00:00-04:00"},
		{name: "DST fall second hour", location: mustLoadLocation(t, "America/New_York"), instant: time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), want: "2026-11-01T01:00:00-05:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, labels, bucket := groupingKey(reportEvent{OccurredAt: tc.instant}, []string{GroupHour}, tc.location)
			if bucket != tc.want || labels["hour"] != tc.want {
				t.Fatalf("bucket=%q labels=%v, want %q", bucket, labels, tc.want)
			}
		})
	}
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestQueryUsesChronologicalFixedWidthTimestamps(t *testing.T) {
	store := openTestStore(t)
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Second)
	eventIDs := []string{"timestamp-event-zero", "timestamp-event-half", "timestamp-event-end"}
	for index, occurredAt := range []time.Time{from, from.Add(500 * time.Millisecond), to} {
		event := testEvent("timestamp-" + eventIDs[index])
		event.EventID = eventIDs[index]
		event.OccurredAt = occurredAt
		if _, err := store.Append([]UsageEvent{event}); err != nil {
			t.Fatal(err)
		}
	}

	var first, second string
	if err := store.db.QueryRow("SELECT occurred_at FROM usage_events WHERE event_id = ?", eventIDs[0]).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT occurred_at FROM usage_events WHERE event_id = ?", eventIDs[1]).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != "2026-10-11T00:00:00.000000000Z" || second != "2026-10-11T00:00:00.500000000Z" {
		t.Fatalf("stored timestamps = %q, %q; want fixed-width UTC values", first, second)
	}
	report, err := store.Query(UsageFilter{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RequestCount != 2 {
		t.Fatalf("range request count = %d, want 2", report.Summary.RequestCount)
	}
}

func TestTimestampMigrationNormalizesLegacyValues(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-logger.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent("legacy-timestamp")
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE usage_events SET occurred_at = ?, started_at = NULL WHERE event_id = ?", "2026-10-11T00:00:00.5Z", event.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("ALTER TABLE source_cursors DROP COLUMN snapshot_reusable"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("ALTER TABLE source_cursors DROP COLUMN pending_tail"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DROP TABLE collector_source_identities"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DROP TABLE parser_checkpoints"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"collector_runs", "collector_runtime", "collector_sources"} {
		if _, err := store.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("DROP TABLE diagnostic_detail_samples"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DROP TABLE diagnostic_details"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DROP TABLE diagnostic_detail_meta"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version IN (3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18)"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("ALTER TABLE projects DROP COLUMN display_name_source"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var occurredAt string
	if err := store.db.QueryRow("SELECT occurred_at FROM usage_events WHERE event_id = ?", event.EventID).Scan(&occurredAt); err != nil {
		t.Fatal(err)
	}
	if occurredAt != "2026-10-11T00:00:00.500000000Z" {
		t.Fatalf("migrated timestamp = %q, want fixed-width value", occurredAt)
	}
	var migrationCount, latestMigration int
	if err := store.db.QueryRow("SELECT COUNT(*), MAX(version) FROM schema_migrations").Scan(&migrationCount, &latestMigration); err != nil {
		t.Fatal(err)
	}
	if migrationCount != CurrentSchemaVersion || latestMigration != CurrentSchemaVersion {
		t.Fatalf("migration history = count %d, latest %d; want %d migrations", migrationCount, latestMigration, CurrentSchemaVersion)
	}
	var sourceGenerationColumn string
	if err := store.db.QueryRow("SELECT name FROM pragma_table_info('source_cursors') WHERE name = 'source_generation'").Scan(&sourceGenerationColumn); err != nil {
		t.Fatalf("source-generation migration was not applied: %v", err)
	}
}

func TestReportFormats(t *testing.T) {
	report := UsageReport{Summary: UsageMeasures{RequestCount: 1, InputTokens: 3, TotalTokens: 5}}
	var jsonOutput, csvOutput, tableOutput bytes.Buffer
	if err := WriteJSON(&jsonOutput, report); err != nil || !strings.Contains(jsonOutput.String(), `"request_count": 1`) {
		t.Fatalf("JSON output = %q, err = %v", jsonOutput.String(), err)
	}
	if err := WriteCSV(&csvOutput, report); err != nil || !strings.Contains(csvOutput.String(), "request_count") {
		t.Fatalf("CSV output = %q, err = %v", csvOutput.String(), err)
	}
	if err := WriteTable(&tableOutput, report); err != nil || !strings.Contains(tableOutput.String(), "REQUESTS") {
		t.Fatalf("table output = %q, err = %v", tableOutput.String(), err)
	}
}

func TestHumanTokenFormatting(t *testing.T) {
	cases := []struct {
		value int64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 999, want: "999"},
		{value: 1_000, want: "1K"},
		{value: 1_001, want: "1K"},
		{value: 1_049, want: "1K"},
		{value: 1_050, want: "1.1K"},
		{value: 999_949, want: "999.9K"},
		{value: 999_950, want: "1M"},
		{value: 999_999, want: "1M"},
		{value: 1_000_000, want: "1M"},
		{value: 1_000_001, want: "1M"},
		{value: 1_050_000, want: "1.1M"},
		{value: 999_949_999, want: "999.9M"},
		{value: 999_950_000, want: "1B"},
		{value: 999_999_999, want: "1B"},
		{value: 1_000_000_000, want: "1B"},
		{value: 1_000_000_001, want: "1B"},
		{value: 999_949_999_999, want: "999.9B"},
		{value: 999_950_000_000, want: "1T"},
		{value: 999_999_999_999, want: "1T"},
		{value: 1_000_000_000_000, want: "1T"},
		{value: 1_000_000_000_001, want: "1T"},
		{value: 1_050_000_000_000, want: "1.1T"},
		{value: -1_500, want: "-1.5K"},
		{value: math.MaxInt64, want: "9223372T"},
		{value: math.MinInt64, want: "-9223372T"},
	}
	for _, tc := range cases {
		if got := FormatHumanTokens(tc.value); got != tc.want {
			t.Errorf("FormatHumanTokens(%d) = %q, want %q", tc.value, got, tc.want)
		}
	}
	if got := formatExactCount(1_234_567); got != "1,234,567" {
		t.Fatalf("formatExactCount = %q, want digit-grouped integer", got)
	}
}

func TestWriteTableUsesRequestedDimensionOrderAndSingleTimeColumn(t *testing.T) {
	hour := "2026-11-01T01:00:00-05:00"
	report := UsageReport{
		Filters: UsageFilter{GroupBy: []string{GroupHour, GroupClient}},
		Rows: []UsageRow{{
			Dimensions: map[string]string{GroupClient: "codex-cli", GroupHour: hour, "bucket": hour},
			Bucket:     hour,
			UsageMeasures: UsageMeasures{
				RequestCount: 1_500, SessionCount: 1_234, InputTokens: 999_950,
				CachedInputTokens: 101_446_656, OutputTokens: 123_021,
				ReasoningOutputTokens: 37_901, TotalTokens: 1_074_142_320,
				UnknownCostRequests: 1_500,
			},
		}},
	}
	var output bytes.Buffer
	if err := WriteTable(&output, report); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if got, want := strings.Fields(lines[0]), []string{"HOUR", "CLIENT", "REQUESTS", "SESSIONS", "INPUT", "CACHED", "OUTPUT", "REASONING", "TOTAL", "ESTIMATE", "REPORTED", "STATUS"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("headings = %v, want %v", got, want)
	}
	if strings.Contains(lines[0], "BUCKET") || strings.Count(lines[1], hour) != 1 {
		t.Fatalf("time bucket was duplicated: %q", output.String())
	}
	for _, expected := range []string{"-05:00", "codex-cli", "1,500", "1,234", "1M", "101.4M", "123K", "37.9K", "1.1B", "unknown", "pricing unavailable"} {
		if !strings.Contains(lines[1], expected) {
			t.Errorf("table row %q does not contain %q", lines[1], expected)
		}
	}
}

func TestWriteTableLabelsTotalAndEmptyDimensions(t *testing.T) {
	var totalOutput bytes.Buffer
	if err := WriteTable(&totalOutput, UsageReport{Summary: UsageMeasures{RequestCount: 1, SessionCount: 1}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(totalOutput.String(), "GROUP") || !strings.Contains(totalOutput.String(), "TOTAL") {
		t.Fatalf("ungrouped table is not clearly labelled: %q", totalOutput.String())
	}

	var groupedOutput bytes.Buffer
	report := UsageReport{Filters: UsageFilter{GroupBy: []string{GroupProvider}}, Rows: []UsageRow{{UsageMeasures: UsageMeasures{RequestCount: 1}}}}
	if err := WriteTable(&groupedOutput, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(groupedOutput.String(), "(none)") {
		t.Fatalf("empty selected dimension has no placeholder: %q", groupedOutput.String())
	}
}

func TestWriteTableDistinguishesNoDataFromEmptyDimension(t *testing.T) {
	for _, report := range []UsageReport{
		{},
		{Filters: UsageFilter{GroupBy: []string{GroupClient}}},
	} {
		var output bytes.Buffer
		if err := WriteTable(&output, report); err != nil {
			t.Fatal(err)
		}
		if got := output.String(); got != "NO USAGE DATA\n" {
			t.Fatalf("no-data table = %q", got)
		}
	}

	var emptyDimensionOutput bytes.Buffer
	report := UsageReport{
		Filters: UsageFilter{GroupBy: []string{GroupClient}},
		Rows:    []UsageRow{{Dimensions: map[string]string{GroupClient: ""}, UsageMeasures: UsageMeasures{RequestCount: 1}}},
	}
	if err := WriteTable(&emptyDimensionOutput, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(emptyDimensionOutput.String(), "(none)") || strings.Contains(emptyDimensionOutput.String(), "NO USAGE DATA") {
		t.Fatalf("empty dimension was confused with no data: %q", emptyDimensionOutput.String())
	}
}

func wideUsageReport() UsageReport {
	return UsageReport{
		Filters: UsageFilter{GroupBy: []string{GroupProject, GroupClient, GroupProvider, GroupModel}},
		Rows: []UsageRow{
			{
				Dimensions: map[string]string{GroupProject: "example-claude-project", GroupClient: "claude-code", GroupProvider: "anthropic", GroupModel: "claude-sonnet-5"},
				UsageMeasures: UsageMeasures{
					RequestCount: 107, SessionCount: 1, InputTokens: 9_200_000, CachedInputTokens: 9_100_000,
					OutputTokens: 53_100, TotalTokens: 9_300_000, UnknownCostRequests: 107,
				},
			},
			{
				Dimensions: map[string]string{GroupProject: "a-very-long-project-qualifier-that-must-be-bounded", GroupClient: "codex-cli", GroupProvider: "openai", GroupModel: "codex-auto-review-with-a-very-long-descriptive-suffix"},
				UsageMeasures: UsageMeasures{
					RequestCount: 2_690, SessionCount: 12, InputTokens: 196_700_000, CachedInputTokens: 185_600_000,
					OutputTokens: 221_000, ReasoningOutputTokens: 66_200, TotalTokens: 196_900_000,
					UnknownCostRequests: 1_000, EstimatedCostRequests: 1_690,
					CalculatedCostMicros: 1_234_000, CalculatedCurrency: "USD",
				},
			},
		},
	}
}

func TestFullTableAlignsASCIIAndEastAsianWideRowsByDisplayCell(t *testing.T) {
	headings := []string{"MODEL", "REQUESTS"}
	rows := [][]string{
		{strings.Repeat("a", 20), "101"},
		{strings.Repeat("测", 20), "202"},
	}
	lines := strings.Split(strings.TrimSuffix(string(renderFullTable(headings, rows)), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("full table lines = %q", lines)
	}
	wantStart := -1
	for index, request := range []string{"REQUESTS", "101", "202"} {
		byteIndex := strings.Index(lines[index], request)
		if byteIndex < 0 {
			t.Fatalf("line %q has no value %q", lines[index], request)
		}
		start := runewidth.StringWidth(lines[index][:byteIndex])
		if wantStart < 0 {
			wantStart = start
		} else if start != wantStart {
			t.Fatalf("column starts differ in display cells: header=%d row=%d line=%q", wantStart, start, lines[index])
		}
	}
}

func TestWriteTableWidthUsesFullLayoutWhenItFits(t *testing.T) {
	report := wideUsageReport()
	var full bytes.Buffer
	if err := WriteTable(&full, report); err != nil {
		t.Fatal(err)
	}
	var wide bytes.Buffer
	if err := WriteTableWidth(&wide, report, 4096); err != nil {
		t.Fatal(err)
	}
	if wide.String() != full.String() {
		t.Fatalf("wide-width output diverged from full layout:\nfull: %q\nwide: %q", full.String(), wide.String())
	}
}

func TestWriteTableWidthDegradesWithoutDroppingData(t *testing.T) {
	report := wideUsageReport()
	_, headings, rows := tableRows(report)

	for _, width := range []int{160, 120, 80, 20} {
		var output bytes.Buffer
		if err := WriteTableWidth(&output, report, width); err != nil {
			t.Fatalf("width %d: %v", width, err)
		}
		rendered := output.String()
		// The vertical fallback (used once nothing else fits, e.g. width 20
		// here) cannot bound width below the length of its longest single
		// field value, so only the full/compact tiers are checked for width.
		if width > 20 && lineDisplayWidth([]byte(rendered)) > width {
			t.Errorf("width %d: rendered line exceeds requested width:\n%s", width, rendered)
		}
		for _, row := range rows {
			for _, value := range row {
				if value == "" {
					continue
				}
				if !strings.Contains(rendered, value) {
					t.Errorf("width %d: value %q missing from output:\n%s", width, value, rendered)
				}
			}
		}
		for _, heading := range headings {
			if !strings.Contains(rendered, heading) && !strings.Contains(rendered, strings.ToLower(heading)) {
				t.Errorf("width %d: heading %q missing from output:\n%s", width, heading, rendered)
			}
		}
	}
}

func TestWriteTableWidthFallsBackToVerticalLayout(t *testing.T) {
	report := wideUsageReport()
	var output bytes.Buffer
	if err := WriteTableWidth(&output, report, 20); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	if !strings.Contains(rendered, "CLIENT:") || !strings.Contains(rendered, "STATUS:") {
		t.Fatalf("narrow width did not use the vertical key/value layout:\n%s", rendered)
	}
}

// TestWriteTableWidthAccountsForEastAsianWideDimensions reproduces a
// regression where the layout-selection width was measured in Unicode code
// points rather than terminal display cells: an East Asian Wide dimension
// value (two display cells per rune) was undercounted, so a line that did
// not actually fit the terminal was still rendered using the full layout.
func TestWriteTableWidthAccountsForEastAsianWideDimensions(t *testing.T) {
	wideModel := strings.Repeat("测", 20) // 20 runes, each an East Asian Wide (2-cell) character.
	report := UsageReport{
		Filters: UsageFilter{GroupBy: []string{GroupClient, GroupProvider, GroupModel}},
		Rows: []UsageRow{{
			Dimensions: map[string]string{GroupClient: "claude-code", GroupProvider: "anthropic", GroupModel: wideModel},
			UsageMeasures: UsageMeasures{
				RequestCount: 1, SessionCount: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
				UnknownCostRequests: 1,
			},
		}},
	}
	_, headings, rows := tableRows(report)
	full := renderFullTable(headings, rows)

	naiveWidth := 0
	for _, line := range strings.Split(string(full), "\n") {
		if n := utf8.RuneCountInString(line); n > naiveWidth {
			naiveWidth = n
		}
	}
	actualWidth := lineDisplayWidth(full)
	if actualWidth <= naiveWidth {
		t.Fatalf("test fixture does not exercise wide characters: code-point width=%d, display-cell width=%d", naiveWidth, actualWidth)
	}

	var output bytes.Buffer
	if err := WriteTableWidth(&output, report, naiveWidth); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(output.Bytes(), full) {
		t.Fatalf("rendered the full layout at width %d even though the East Asian Wide model name needs %d display cells, not %d code points", naiveWidth, actualWidth, naiveWidth)
	}
	if lineDisplayWidth(output.Bytes()) > naiveWidth {
		t.Fatalf("degraded layout still exceeds the requested display width %d:\n%s", naiveWidth, output.String())
	}
	for _, row := range rows {
		for _, value := range row {
			if value != "" && !strings.Contains(output.String(), value) {
				t.Fatalf("value %q missing from degraded output:\n%s", value, output.String())
			}
		}
	}
}

func TestHumanDimensionsEscapeControlsAndBoundLongValues(t *testing.T) {
	unsafe := "client\tline\n\x1b[31m\u200b雪"
	got := formatHumanDimension(unsafe)
	for _, escaped := range []string{`\t`, `\n`, `\x1B`, `\u200B`, "雪"} {
		if !strings.Contains(got, escaped) {
			t.Errorf("escaped value %q does not contain %q", got, escaped)
		}
	}
	if strings.ContainsAny(got, "\t\n\r\x1b") {
		t.Fatalf("escaped value contains a terminal control: %q", got)
	}

	prefix := strings.Repeat("same-prefix-", 4)
	first := formatHumanDimension(prefix + "alpha")
	second := formatHumanDimension(prefix + "bravo")
	if first == second || utf8.RuneCountInString(first) > maxHumanDimensionRunes || utf8.RuneCountInString(second) > maxHumanDimensionRunes {
		t.Fatalf("bounded values are ambiguous or too long: %q, %q", first, second)
	}
	if !strings.Contains(first, "…#") || !strings.Contains(second, "…#") {
		t.Fatalf("truncation is not visible: %q, %q", first, second)
	}
}

func TestHumanCostStatesRemainDistinct(t *testing.T) {
	cases := []struct {
		name     string
		measures UsageMeasures
		cost     string
		status   string
	}{
		{name: "known zero", measures: UsageMeasures{RequestCount: 1, CalculatedCurrency: "USD", KnownZeroCostRequests: 1}, cost: "0 USD", status: "known zero"},
		{name: "known fractional unit", measures: UsageMeasures{RequestCount: 1, CalculatedCostMicros: 240, CalculatedCurrency: "USD", EstimatedCostRequests: 1}, cost: "0.00024 USD", status: "estimated"},
		{name: "fully unknown", measures: UsageMeasures{RequestCount: 2, UnknownCostRequests: 2}, cost: "unknown", status: "pricing unavailable (2)"},
		{name: "unknown reason", measures: UsageMeasures{RequestCount: 1, UnknownCostRequests: 1, CostUnknownReason: "model_unresolved"}, cost: "unknown", status: "model unresolved (1)"},
		{name: "no matching rate", measures: UsageMeasures{RequestCount: 1, UnknownCostRequests: 1, CostUnknownReason: "no_matching_rate"}, cost: "unknown", status: "no public catalog rate (1)"},
		{name: "reported only", measures: UsageMeasures{RequestCount: 1, UnknownCostRequests: 1, ProviderReportedCostMicros: 10, ProviderReportedCurrency: "USD"}, cost: "unknown", status: "reported only; estimate unavailable (pricing unavailable, 1)"},
		{name: "partially known", measures: UsageMeasures{RequestCount: 3, CalculatedCostMicros: 12_340_000, CalculatedCurrency: "USD", UnknownCostRequests: 1}, cost: "12.34 USD", status: "partial; 1 pricing unavailable"},
		{name: "mixed currencies", measures: UsageMeasures{RequestCount: 2, CalculatedCostMicros: 12_340_000, CalculatedCurrency: "mixed"}, cost: "not comparable", status: "mixed currencies"},
		{name: "mixed and partially unknown", measures: UsageMeasures{RequestCount: 3, CalculatedCostMicros: 12_340_000, CalculatedCurrency: "mixed", UnknownCostRequests: 1}, cost: "not comparable", status: "mixed currencies; 1 pricing unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cost, status := humanCostDisplay(tc.measures)
			if cost != tc.cost || status != tc.status {
				t.Fatalf("cost/status = %q/%q, want %q/%q", cost, status, tc.cost, tc.status)
			}
		})
	}
}

func TestPricingCoverageIsIndependentOfReportGroupingAndMachineShape(t *testing.T) {
	store := openTestStore(t)
	date := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(Project{ID: "same-project", DisplayName: "Same project"}); err != nil {
		t.Fatal(err)
	}
	first, second := testEvent("coverage-first"), testEvent("coverage-second")
	for index, event := range []*UsageEvent{&first, &second} {
		event.EventID = fmt.Sprintf("coverage-%d", index)
		event.OccurredAt = date
		event.Provider = "unlisted-provider"
		event.RequestedModel = fmt.Sprintf("unlisted-model-%d", index)
		event.ResolvedModel = event.RequestedModel
		event.ProjectID = "same-project"
		event.Usage = TokenUsage{InputTokens: 10}
	}
	if _, err := store.Append([]UsageEvent{first, second}); err != nil {
		t.Fatal(err)
	}
	report, err := store.Query(UsageFilter{From: date.Add(-time.Hour), To: date.Add(time.Hour), GroupBy: []string{GroupProject}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 || len(report.PricingCoverage) != 2 {
		t.Fatalf("grouped rows/coverage = %d/%d, want 1/2", len(report.Rows), len(report.PricingCoverage))
	}
	var human bytes.Buffer
	if err := WriteTable(&human, report); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"PRICING COVERAGE GAPS", "unlisted-provider / unlisted-model-0: 1 requests", "no public catalog rate (1)"} {
		if !strings.Contains(human.String(), expected) {
			t.Fatalf("human report missing %q: %s", expected, human.String())
		}
	}
	var machine bytes.Buffer
	if err := WriteJSON(&machine, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(machine.String(), "pricing_coverage") || strings.Contains(machine.String(), "PRICING COVERAGE GAPS") {
		t.Fatalf("human-only coverage leaked into JSON: %s", machine.String())
	}
}

func TestHumanPricingCoverageIsBoundedAndSanitized(t *testing.T) {
	rows := make([]PricingCoverageRow, maxHumanPricingCoverageRows+2)
	for index := range rows {
		rows[index] = PricingCoverageRow{Provider: "provider\n\x1b", Model: fmt.Sprintf("model-%d", index), Requests: 1, Reasons: map[string]int64{"no_matching_rate": 1}}
	}
	var output bytes.Buffer
	if err := WriteHumanPricingCoverage(&output, rows, 80); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\t\r\x1b") || strings.Count(output.String(), "model-") != maxHumanPricingCoverageRows || !strings.Contains(output.String(), "and 2 more provider/model combinations") || !strings.Contains(output.String(), `provider\n\x1B`) {
		t.Fatalf("coverage output is unbounded or unsafe: %q", output.String())
	}
}

func TestWriteTableWidthWithoutPricingCoverageKeepsTableOnly(t *testing.T) {
	report := UsageReport{
		Rows:            []UsageRow{{Dimensions: map[string]string{GroupModel: "unlisted-model"}, UsageMeasures: UsageMeasures{RequestCount: 1, UnknownCostRequests: 1, CostUnknownReason: "no_matching_rate"}}},
		Summary:         UsageMeasures{RequestCount: 1, UnknownCostRequests: 1},
		PricingCoverage: []PricingCoverageRow{{Provider: "unlisted-provider", Model: "unlisted-model", Requests: 1, Reasons: map[string]int64{"no_matching_rate": 1}}},
	}
	var output bytes.Buffer
	if err := WriteTableWidthWithoutPricingCoverage(&output, report, 80); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no public catalog rate") || strings.Contains(output.String(), "PRICING COVERAGE GAPS") || strings.Contains(output.String(), "Next:") {
		t.Fatalf("table-only output lost concise status or included coverage guidance: %s", output.String())
	}
}

func TestHumanPricingCoverageWrapsAndExplainsEachGap(t *testing.T) {
	coverage := []PricingCoverageRow{{
		Provider: "anthropic",
		Model:    strings.Repeat("界", 12),
		Requests: 3,
		Reasons: map[string]int64{
			"no_matching_rate":        1,
			"outside_effective_range": 1,
			"billing_context_missing": 1,
		},
	}}
	report := UsageReport{Summary: UsageMeasures{RequestCount: 3, UnknownCostRequests: 3}, PricingCoverage: coverage}
	var output bytes.Buffer
	if err := WriteTableWidth(&output, report, 80); err != nil {
		t.Fatal(err)
	}
	for lineNo, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
		if width := runewidth.StringWidth(line); width > 80 {
			t.Errorf("line %d has display width %d > 80: %q", lineNo+1, width, line)
		}
	}
	for _, expected := range []string{
		"no public catalog rate (1)",
		"outside pricing dates (1)",
		"billing context or rate",
		"unavailable (1)",
		"Review exact-model catalog coverage",
		"Do not apply current prices retroactively",
		"Check for a verified rate for the affected token category",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("coverage output missing %q: %s", expected, output.String())
		}
	}
}

func TestKnownModelMissingTokenCategoryRateHasAccurateGuidance(t *testing.T) {
	store := openTestStore(t)
	date := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	event := testEvent("known-model-missing-cache-write")
	event.EventID = "known-model-missing-cache-write"
	event.OccurredAt = date
	event.Provider = "openai"
	event.RequestedModel = "gpt-5-codex"
	event.ResolvedModel = "gpt-5-codex"
	event.Usage = TokenUsage{InputTokens: 12, CacheWriteTokens: 2}
	if _, err := store.Append([]UsageEvent{event}); err != nil {
		t.Fatal(err)
	}
	report, err := store.Query(UsageFilter{From: date.Add(-time.Minute), To: date.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.UnknownCostRequests != 1 || report.Summary.CostUnknownReason != "billing_context_missing" {
		t.Fatalf("summary unknown count/reason = %d/%q", report.Summary.UnknownCostRequests, report.Summary.CostUnknownReason)
	}
	if len(report.PricingCoverage) != 1 || report.PricingCoverage[0].Categories[TokenCategoryCacheWrite] != 1 {
		t.Fatalf("coverage did not identify missing cache-write pricing: %+v", report.PricingCoverage)
	}
	var output bytes.Buffer
	if err := WriteTable(&output, report); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Affected category: cache-write (1)",
		"Check for a verified rate for the affected token category",
		"If rate selection needs billing evidence absent from the source",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("human report missing %q: %s", expected, output.String())
		}
	}
}

func TestZeroUsageReportDistinguishesKnownAndUnknownModels(t *testing.T) {
	store := openTestStore(t)
	date := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	supported := testEvent("zero-supported")
	supported.EventID, supported.OccurredAt = "zero-supported", date
	supported.RequestedModel, supported.ResolvedModel = "gpt-5-codex", "gpt-5-codex"
	supported.Usage = TokenUsage{}
	unsupported := testEvent("zero-unsupported")
	unsupported.EventID, unsupported.OccurredAt = "zero-unsupported", date
	unsupported.Provider, unsupported.RequestedModel, unsupported.ResolvedModel = "unknown", "unknown-model", "unknown-model"
	unsupported.Usage = TokenUsage{}
	if _, err := store.Append([]UsageEvent{supported, unsupported}); err != nil {
		t.Fatal(err)
	}
	report, err := store.Query(UsageFilter{From: date.Add(-time.Minute), To: date.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.KnownZeroCostRequests != 1 || report.Summary.EstimatedCostRequests != 0 || report.Summary.UnknownCostRequests != 1 {
		t.Fatalf("zero-cost summary = %+v", report.Summary)
	}
	var reasons map[string]int64
	if err := json.Unmarshal([]byte(report.Summary.CostUnknownReasons), &reasons); err != nil {
		t.Fatal(err)
	}
	if reasons["no_matching_rate"] != 1 {
		t.Fatalf("zero event unknown reasons = %v", reasons)
	}
}

func TestGroupedUnknownCostReasonsRetainCounts(t *testing.T) {
	acc := reportAccumulator{sessions: map[string]struct{}{}}
	for _, reason := range []string{"model_unresolved", "outside_effective_range", "outside_effective_range"} {
		if err := addMeasures(&acc, reportEvent{}, eventCost{Status: "unknown", Reason: reason}); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := WriteJSON(&output, UsageReport{Summary: acc.UsageMeasures}); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Summary struct {
			Reasons map[string]int64 `json:"cost_unknown_reasons"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Reasons["model_unresolved"] != 1 || decoded.Summary.Reasons["outside_effective_range"] != 2 {
		t.Fatalf("JSON reason counts = %v", decoded.Summary.Reasons)
	}
	var csvOutput bytes.Buffer
	if err := WriteCSV(&csvOutput, UsageReport{Summary: acc.UsageMeasures}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvOutput.String(), `"{""model_unresolved"":1,""outside_effective_range"":2}"`) {
		t.Fatalf("CSV does not preserve reason-count JSON: %q", csvOutput.String())
	}
}

func TestGroupedPricingProviderAndBasisMergeExplicitly(t *testing.T) {
	acc := reportAccumulator{sessions: map[string]struct{}{}}
	if err := addMeasures(&acc, reportEvent{Provider: "openai"}, eventCost{Status: "calculated", AmountMicros: 10, Currency: "USD", Provider: "openai", PricingOrigin: "public_catalog"}); err != nil {
		t.Fatal(err)
	}
	if err := addMeasures(&acc, reportEvent{Provider: "anthropic"}, eventCost{Status: "unknown", Reason: "no_matching_rate", Provider: "anthropic", PricingOrigin: "custom", Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	if acc.PricingProvider != "mixed" || acc.CalculationBasis != "mixed" {
		t.Fatalf("grouped pricing provider/basis = %q/%q, want mixed/mixed", acc.PricingProvider, acc.CalculationBasis)
	}
}

func TestCostAggregationRejectsOverflow(t *testing.T) {
	acc := reportAccumulator{sessions: map[string]struct{}{}, UsageMeasures: UsageMeasures{CalculatedCostMicros: int64(^uint64(0) >> 1)}}
	if err := addMeasures(&acc, reportEvent{}, eventCost{Status: "calculated", AmountMicros: 1}); err == nil || !strings.Contains(err.Error(), "estimated cost aggregation overflow") {
		t.Fatalf("estimated overflow error = %v", err)
	}
	acc = reportAccumulator{sessions: map[string]struct{}{}, UsageMeasures: UsageMeasures{ProviderReportedCostMicros: int64(^uint64(0) >> 1)}}
	amount := int64(1)
	if err := addMeasures(&acc, reportEvent{ProviderAmount: &amount}, eventCost{}); err == nil || !strings.Contains(err.Error(), "provider-reported cost aggregation overflow") {
		t.Fatalf("reported overflow error = %v", err)
	}
	acc = reportAccumulator{sessions: map[string]struct{}{}, UsageMeasures: UsageMeasures{CalculatedCostMicros: int64(^uint64(0) >> 1)}}
	if err := addMeasures(&acc, reportEvent{}, eventCost{Status: "unknown", AmountMicros: 1, Currency: "USD", Reason: "no_matching_rate"}); err == nil || !strings.Contains(err.Error(), "partial estimated cost aggregation overflow") {
		t.Fatalf("partial estimate overflow error = %v", err)
	}
}

func TestMachineFormatsKeepExactValuesAndStableShape(t *testing.T) {
	report := UsageReport{
		Filters: UsageFilter{GroupBy: []string{GroupClient, GroupProvider}},
		Rows: []UsageRow{{
			Dimensions:    map[string]string{GroupClient: "codex-cli", GroupProvider: "openai"},
			UsageMeasures: UsageMeasures{RequestCount: 1_500, InputTokens: 107_414_232, TotalTokens: 107_537_253},
		}},
	}
	var jsonOutput bytes.Buffer
	if err := WriteJSON(&jsonOutput, report); err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{`"group_by": [`, `"client"`, `"provider"`, `"request_count": 1500`, `"input_tokens": 107414232`, `"total_tokens": 107537253`} {
		if !strings.Contains(jsonOutput.String(), exact) {
			t.Errorf("JSON output lost exact field/value %q: %s", exact, jsonOutput.String())
		}
	}
	if strings.Contains(jsonOutput.String(), "107.4M") {
		t.Fatalf("JSON output contains a human abbreviation: %s", jsonOutput.String())
	}

	var csvOutput bytes.Buffer
	if err := WriteCSV(&csvOutput, report); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(csvOutput.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0][0] != "group" || records[0][4] != "input_tokens" || records[1][0] != "client=codex-cli,provider=openai" || records[1][2] != "1500" || records[1][4] != "107414232" || records[1][9] != "107537253" {
		t.Fatalf("CSV machine contract changed: %#v", records)
	}
}
