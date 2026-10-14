// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package tokenlogger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestBundledPricingCatalogLoadsIdempotently(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var before int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IS NOT NULL").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("bundled pricing catalog was not loaded")
	}
	var anthropicRates int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE provider_name = 'anthropic' AND catalog_entry_id IS NOT NULL").Scan(&anthropicRates); err != nil {
		t.Fatal(err)
	}
	if anthropicRates != 0 {
		t.Fatalf("Anthropic rates loaded without billing-context support: %d", anthropicRates)
	}
	catalog, err := store.pricingCatalog()
	if err != nil {
		t.Fatal(err)
	}
	claudeEvent := reportEvent{OccurredAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), Provider: "anthropic", RequestedModel: "claude-sonnet-4-6", ResolvedModel: "claude-sonnet-4-6", Usage: TokenUsage{InputTokens: 100}}
	if got := catalog.calculate(claudeEvent); got.Status != "unknown" {
		t.Fatalf("Claude cost without normalized billing context = %+v, want unknown", got)
	}
	if err := store.syncBundledPricingCatalog(); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IS NOT NULL").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("catalog entries after second sync = %d, want %d", after, before)
	}
}

func TestMigrationsOneThroughFivePreserveLegacyData(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaMigrationOne); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?)", "2026-01-01T00:00:00.5Z"); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	event := testEvent("v1-legacy-event")
	event.EventID = "v1-legacy-event"
	event.ProjectID, event.SessionID = "legacy-project", "legacy-session"
	reportedAmount, providerAmount := int64(111), int64(222)
	event.Cost = Cost{Status: "reported", AmountMicros: &reportedAmount, Currency: "USD", ProviderAmount: &providerAmount, ProviderCurrency: "USD"}
	if _, err := store.AppendWithReferences([]UsageEvent{event}, []Project{{ID: "legacy-project", DisplayName: "Legacy Project"}}, []Session{{ID: "legacy-session", ProjectID: "legacy-project", Client: Client{Name: "codex-cli"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE usage_events SET occurred_at = ? WHERE event_id = ?", "2026-10-11T00:00:00.5Z", event.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO source_cursors(source_id, file_fingerprint, byte_offset, last_provider_record_id, updated_at) VALUES ('legacy-source', 'fingerprint', 99, 'record-1', ?)", "2026-10-11T00:00:00.5Z"); err != nil {
		t.Fatal(err)
	}
	from := "2026-01-01T00:00:00.000000000Z"
	if _, err := db.Exec(`INSERT INTO pricing_rules(provider_name, model_selector, token_category, rate_micros, currency,
		effective_from, pricing_version) VALUES ('custom', 'model', 'input', 7, 'USD', ?, 'legacy-v1')`, from); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	var migrationCount, latestMigration int
	if err := db.QueryRow("SELECT COUNT(*), MAX(version) FROM schema_migrations").Scan(&migrationCount, &latestMigration); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 5 || latestMigration != 5 {
		t.Fatalf("migration history = %d/%d, want all migrations 1 through 5", migrationCount, latestMigration)
	}
	var legacy, numerator, denominator int64
	if err := db.QueryRow("SELECT rate_micros, rate_numerator_micros, rate_denominator_tokens FROM pricing_rules WHERE provider_name = 'custom'").Scan(&legacy, &numerator, &denominator); err != nil {
		t.Fatal(err)
	}
	if legacy != 7 || numerator != 7 || denominator != 1 {
		t.Fatalf("migrated rate = legacy %d, rational %d/%d; want 7 and 7/1", legacy, numerator, denominator)
	}
	var gotAmount, gotProviderAmount int64
	var status, costCurrency, providerCurrency, occurredAt string
	if err := db.QueryRow(`SELECT cost_status, cost_amount_micros, cost_currency, provider_cost_amount_micros,
		provider_cost_currency, occurred_at FROM usage_events WHERE event_id = ?`, event.EventID).Scan(&status, &gotAmount, &costCurrency, &gotProviderAmount, &providerCurrency, &occurredAt); err != nil {
		t.Fatal(err)
	}
	if status != "reported" || gotAmount != reportedAmount || costCurrency != "USD" || gotProviderAmount != providerAmount || providerCurrency != "USD" {
		t.Fatalf("legacy event cost changed: status=%s amount=%d %s provider=%d %s", status, gotAmount, costCurrency, gotProviderAmount, providerCurrency)
	}
	var projectName, sessionClient string
	if err := db.QueryRow(`SELECT p.display_name, s.client_name FROM usage_events e
		JOIN projects p ON p.id = e.project_id JOIN sessions s ON s.id = e.session_id WHERE e.event_id = ?`, event.EventID).Scan(&projectName, &sessionClient); err != nil {
		t.Fatal(err)
	}
	if projectName != "Legacy Project" || sessionClient != "codex-cli" {
		t.Fatalf("legacy event references changed: %q/%q", projectName, sessionClient)
	}
	if occurredAt != "2026-10-11T00:00:00.500000000Z" {
		t.Fatalf("legacy event timestamp = %q, was not normalized", occurredAt)
	}
	var cursorFingerprint string
	var cursorOffset int64
	if err := db.QueryRow("SELECT file_fingerprint, byte_offset FROM source_cursors WHERE source_id = 'legacy-source'").Scan(&cursorFingerprint, &cursorOffset); err != nil {
		t.Fatal(err)
	}
	if cursorFingerprint != "fingerprint" || cursorOffset != 99 {
		t.Fatalf("legacy source cursor changed: %q/%d", cursorFingerprint, cursorOffset)
	}
}

func TestRationalPricingRoundsOnceAndReportsOverflow(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	catalog := pricingCatalog{rules: []PricingRule{{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput,
		RateNumeratorMicros: 1, RateDenominatorTokens: 2, Currency: "USD", EffectiveFrom: from, PricingVersion: "test-v1"}}}
	event := reportEvent{OccurredAt: from, Provider: "test", RequestedModel: "exact", ResolvedModel: "exact", Usage: TokenUsage{InputTokens: 1}}
	got := catalog.calculate(event)
	if got.Status != "calculated" || got.AmountMicros != 1 || got.PricingVersion != "test-v1" {
		t.Fatalf("half-micro estimate = %+v, want rounded 1 micro with provenance", got)
	}
	catalog.rules[0].RateNumeratorMicros = 2
	catalog.rules[0].RateDenominatorTokens = 1
	event.Usage.InputTokens = int64(^uint64(0) >> 1)
	got = catalog.calculate(event)
	if got.Status != "unknown" || got.Reason != "arithmetic_overflow" {
		t.Fatalf("overflow estimate = %+v, want arithmetic_overflow", got)
	}
}

func TestPartialPricingRetainsKnownSubtotal(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	catalog := pricingCatalog{rules: []PricingRule{{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput,
		RateMicros: 7, Currency: "USD", EffectiveFrom: from, PricingVersion: "custom-v1"}}}
	event := reportEvent{OccurredAt: from, Provider: "test", RequestedModel: "exact", ResolvedModel: "exact",
		Usage: TokenUsage{InputTokens: 10, CacheWriteTokens: 2}}
	got := catalog.calculate(event)
	if got.Status != "unknown" || got.Reason != "billing_context_missing" || got.AmountMicros != 56 || got.Currency != "USD" {
		t.Fatalf("partial estimate = %+v, want retained 56 USD micros and unknown reason", got)
	}
}

func TestPricingPrecedenceAndAmbiguousIntervals(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	catalog := pricingCatalog{rules: []PricingRule{
		{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput, RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "bundled-v1", CatalogEntryID: "bundle:input"},
		{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput, RateMicros: 2, Currency: "USD", EffectiveFrom: from, PricingVersion: "custom-v1"},
	}}
	event := reportEvent{OccurredAt: from, Provider: "test", RequestedModel: "exact", ResolvedModel: "exact", Usage: TokenUsage{InputTokens: 1}}
	if got := catalog.calculate(event); got.AmountMicros != 2 || got.PricingVersion != "custom-v1" {
		t.Fatalf("custom override = %+v, want 2 micros custom rate", got)
	}
	catalog.rules = append(catalog.rules, PricingRule{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput,
		RateMicros: 3, Currency: "USD", EffectiveFrom: from, PricingVersion: "custom-v2"})
	if got := catalog.calculate(event); got.Status != "unknown" || got.Reason != "ambiguous_pricing_rule" {
		t.Fatalf("overlapping rules = %+v, want ambiguous_pricing_rule", got)
	}
}

func TestEstimateAggregatesEveryComponentProvenance(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	catalog := pricingCatalog{rules: []PricingRule{
		{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput, RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "v1", SourceURL: "https://example.test/input"},
		{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryOutput, RateMicros: 2, Currency: "USD", EffectiveFrom: from, EffectiveTo: &to, PricingVersion: "v2", SourceURL: "https://example.test/output"},
	}}
	event := reportEvent{OccurredAt: from.Add(time.Hour), Provider: "test", RequestedModel: "exact", ResolvedModel: "exact",
		Usage: TokenUsage{InputTokens: 3, OutputTokens: 4}}
	got := catalog.calculate(event)
	if got.Status != "calculated" || got.AmountMicros != 11 || got.PricingVersion != "mixed" || got.SourceURL != "mixed" || got.EffectiveTo != "mixed" {
		t.Fatalf("multi-component provenance = %+v, want total 11 and mixed entry provenance", got)
	}
}

func TestExpiredPricingDoesNotApply(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	catalog := pricingCatalog{rules: []PricingRule{{Provider: "test", ModelSelector: "exact", TokenCategory: TokenCategoryInput,
		RateMicros: 1, Currency: "USD", EffectiveFrom: from, EffectiveTo: &to, PricingVersion: "v1"}}}
	event := reportEvent{OccurredAt: to, Provider: "test", RequestedModel: "exact", ResolvedModel: "exact", Usage: TokenUsage{InputTokens: 1}}
	if got := catalog.calculate(event); got.Status != "unknown" || got.Reason != "outside_effective_range" {
		t.Fatalf("expired rate = %+v, want outside_effective_range", got)
	}
}
