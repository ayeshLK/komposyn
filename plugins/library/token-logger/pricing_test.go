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

func TestMigrationFivePreservesLegacyPricingMeaning(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []struct {
		version int
		sql     string
	}{{1, schemaMigrationOne}, {2, schemaMigrationTwo}, {4, schemaMigrationFour}} {
		if migration.version == 4 { // migration 3 is represented by its version record; empty tables need no normalization.
			if _, err := db.Exec(schemaMigrationFour); err != nil {
				t.Fatal(err)
			}
		} else if _, err := db.Exec(migration.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", migration.version, "2026-01-01T00:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
	}
	from := "2026-01-01T00:00:00.000000000Z"
	if _, err := db.Exec(`INSERT INTO pricing_rules(provider_name, model_selector, token_category, rate_micros, currency,
		effective_from, pricing_version) VALUES ('custom', 'model', 'input', 7, 'USD', ?, 'legacy-v1')`, from); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	var legacy, numerator, denominator int64
	if err := db.QueryRow("SELECT rate_micros, rate_numerator_micros, rate_denominator_tokens FROM pricing_rules WHERE provider_name = 'custom'").Scan(&legacy, &numerator, &denominator); err != nil {
		t.Fatal(err)
	}
	if legacy != 7 || numerator != 7 || denominator != 1 {
		t.Fatalf("migrated rate = legacy %d, rational %d/%d; want 7 and 7/1", legacy, numerator, denominator)
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
