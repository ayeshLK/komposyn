// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package tokenlogger

import (
	"database/sql"
	"encoding/json"
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

func TestCatalogSyncClosesIntervalsAtomicallyWithoutRewritingRates(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var bundle bundledCatalog
	if err := json.Unmarshal(bundledPricingCatalog, &bundle); err != nil {
		t.Fatal(err)
	}
	old := bundle.Entries[0]
	boundary := "2026-10-15T00:00:00Z"
	old.EffectiveTo = boundary
	conflicting := old
	conflicting.Numerator++
	bad := bundle
	bad.Entries = []bundledCatalogEntry{old, conflicting}
	badData, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.syncPricingCatalogBytes(badData); err == nil {
		t.Fatal("sync accepted conflicting immutable catalog ID")
	}
	var oldEnd sql.NullString
	if err := store.db.QueryRow("SELECT effective_to FROM pricing_rules WHERE catalog_entry_id = ?", old.ID).Scan(&oldEnd); err != nil {
		t.Fatal(err)
	}
	if oldEnd.Valid {
		t.Fatalf("failed sync partially closed interval at %q", oldEnd.String)
	}

	replacement := old
	replacement.ID += ":v2"
	replacement.EffectiveFrom, replacement.EffectiveTo = boundary, ""
	replacement.PricingVersion = "public-api-2026-10-15-v2"
	replacement.Numerator += 100_000
	bundle.Version = "public-api-2026-10-15-v2"
	bundle.Entries = []bundledCatalogEntry{old, replacement}
	updated, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.syncPricingCatalogBytes(updated); err != nil {
		t.Fatal(err)
	}
	var oldStoredEnd string
	if err := store.db.QueryRow("SELECT effective_to FROM pricing_rules WHERE catalog_entry_id = ?", old.ID).Scan(&oldStoredEnd); err != nil {
		t.Fatal(err)
	}
	if oldStoredEnd != formatStoredTime(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("closed interval = %q", oldStoredEnd)
	}
	var replacementCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id = ?", replacement.ID).Scan(&replacementCount); err != nil {
		t.Fatal(err)
	}
	if replacementCount != 1 {
		t.Fatalf("replacement entries = %d, want 1", replacementCount)
	}
}

func TestCatalogReviewDateIsPerEntryAndOverlapsRollback(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var bundle bundledCatalog
	if err := json.Unmarshal(bundledPricingCatalog, &bundle); err != nil {
		t.Fatal(err)
	}
	var initialCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IS NOT NULL").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}
	first := bundle.Entries[0]
	first.ReviewedAt = "2026-10-14"
	second := first
	second.ID += ":overlap"
	second.PricingVersion += "-next"
	second.Numerator++
	second.ReviewedAt = ""
	bundle.Version = "public-api-2026-11-01-v2"
	bundle.ReviewedAt = "2026-11-01"
	bundle.Entries = []bundledCatalogEntry{first, second}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.syncPricingCatalogBytes(data); err == nil {
		t.Fatal("sync accepted overlapping bundled intervals")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IS NOT NULL").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != initialCount {
		t.Fatalf("overlap sync changed catalog row count to %d, want %d", count, initialCount)
	}

	second.EffectiveFrom = "2026-11-01T00:00:00Z"
	first.EffectiveTo = second.EffectiveFrom
	bundle.Entries = []bundledCatalogEntry{first, second}
	data, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.syncPricingCatalogBytes(data); err != nil {
		t.Fatalf("sync valid closed interval and new review date: %v", err)
	}
	var oldReview, newReview string
	if err := store.db.QueryRow("SELECT source_reviewed_at FROM pricing_rules WHERE catalog_entry_id = ?", first.ID).Scan(&oldReview); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT source_reviewed_at FROM pricing_rules WHERE catalog_entry_id = ?", second.ID).Scan(&newReview); err != nil {
		t.Fatal(err)
	}
	if oldReview != "2026-10-14" || newReview != "2026-11-01" {
		t.Fatalf("review dates old/new = %q/%q", oldReview, newReview)
	}
}

func TestBundledCatalogRejectsMissingProvenanceAndInvalidRate(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for name, mutate := range map[string]func(*bundledCatalogEntry){
		"missing source":      func(e *bundledCatalogEntry) { e.SourceURL = "" },
		"unofficial source":   func(e *bundledCatalogEntry) { e.SourceURL = "https://pricing.example.com/openai" },
		"invalid review date": func(e *bundledCatalogEntry) { e.ReviewedAt = "not-a-date" },
		"zero denominator":    func(e *bundledCatalogEntry) { e.Denominator = 0 },
		"missing ID":          func(e *bundledCatalogEntry) { e.ID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			var bundle bundledCatalog
			if err := json.Unmarshal(bundledPricingCatalog, &bundle); err != nil {
				t.Fatal(err)
			}
			mutate(&bundle.Entries[0])
			data, err := json.Marshal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.syncPricingCatalogBytes(data); err == nil {
				t.Fatal("invalid catalog was accepted")
			}
		})
	}
}

func TestCustomPricingRejectsEqualPrecedenceWildcardOverlap(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rule := PricingRule{Provider: "test", ModelSelector: "gpt-*", TokenCategory: TokenCategoryInput,
		RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "custom-v1"}
	if err := store.UpsertPricing(rule); err != nil {
		t.Fatal(err)
	}
	rule.ModelSelector = "gpt-5*"
	rule.PricingVersion = "custom-v2"
	if err := store.UpsertPricing(rule); err == nil {
		t.Fatal("accepted intersecting wildcard selectors with equal precedence")
	}
	if !selectorsHaveEqualPrecedenceOverlap("gpt-*", "gpt-5*") || selectorsHaveEqualPrecedenceOverlap("gpt-5-codex", "gpt-5*") {
		t.Fatal("selector overlap did not preserve equal-precedence distinctions")
	}
}

func TestCustomPricingRejectsIntersectingContextSelectorsAtEqualSpecificity(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := PricingRule{Provider: "test", ModelSelector: "model", TokenCategory: TokenCategoryInput,
		RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "v1"}
	standard := base
	standard.ServiceTierSelector = "standard"
	if err := store.UpsertPricing(standard); err != nil {
		t.Fatal(err)
	}
	region := base
	region.RegionSelector = "us"
	if err := store.UpsertPricing(region); err == nil {
		t.Fatal("accepted equal-specificity service-tier and region selectors that intersect")
	}
	priority := base
	priority.ServiceTierSelector = "priority"
	if err := store.UpsertPricing(priority); err != nil {
		t.Fatalf("rejected disjoint tier alternative: %v", err)
	}
	standardUS := standard
	standardUS.RegionSelector = "us"
	if err := store.UpsertPricing(standardUS); err != nil {
		t.Fatalf("rejected deterministic more-specific override: %v", err)
	}
}

func TestBundledCatalogRejectsIntersectingContextSelectorsAtEqualSpecificity(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var bundle bundledCatalog
	if err := json.Unmarshal(bundledPricingCatalog, &bundle); err != nil {
		t.Fatal(err)
	}
	first := bundle.Entries[0]
	first.ID, first.Model, first.Category = "phase2:standard", "phase2-overlap-model", TokenCategoryInput
	first.ServiceTier, first.Region = "standard", ""
	second := first
	second.ID, second.ServiceTier, second.Region = "phase2:region", "", "us"
	bundle.Entries = append(bundle.Entries, first, second)
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.syncPricingCatalogBytes(encoded); err == nil {
		t.Fatal("accepted bundled equal-specificity context selectors that intersect")
	}
	var persisted int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IN ('phase2:standard', 'phase2:region')").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatalf("rejected bundled entries persisted %d rows", persisted)
	}
}

func TestPricingContextSelectorsRequireKnownMatchingValues(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	catalog := pricingCatalog{rules: []PricingRule{
		{Provider: "openai", ModelSelector: "model-x", TokenCategory: TokenCategoryInput, RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "tier-v1", CatalogEntryID: "bundle:standard", ServiceTierSelector: "standard"},
		{Provider: "openai", ModelSelector: "model-x", TokenCategory: TokenCategoryInput, RateMicros: 2, Currency: "USD", EffectiveFrom: from, PricingVersion: "tier-v1", CatalogEntryID: "bundle:priority", ServiceTierSelector: "priority"},
	}}
	at := from.Add(time.Hour)
	if _, ok, ambiguous := catalog.find("openai", "model-x", "", TokenCategoryInput, at, BillingContext{}); ok || ambiguous {
		t.Fatal("unknown tier matched a tier-specific rate")
	}
	standard := BillingContext{ServiceTier: &BillingContextValue{Value: "standard", Evidence: BillingEvidenceProviderResponse}}
	rule, ok, ambiguous := catalog.find("openai", "model-x", "", TokenCategoryInput, at, standard)
	if !ok || ambiguous || rule.ServiceTierSelector != "standard" {
		t.Fatalf("known standard context matched %+v (ok=%v ambiguous=%v)", rule, ok, ambiguous)
	}
	other := BillingContext{ServiceTier: &BillingContextValue{Value: "flex", Evidence: BillingEvidenceProviderResponse}}
	if _, ok, ambiguous := catalog.find("openai", "model-x", "", TokenCategoryInput, at, other); ok || ambiguous {
		t.Fatal("unpriced known tier fell back to another tier")
	}
}

func TestCustomContextIndependentRuleKeepsBundledPrecedence(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	catalog := pricingCatalog{rules: []PricingRule{
		{Provider: "openai", ModelSelector: "model-x", TokenCategory: TokenCategoryInput, RateMicros: 1, Currency: "USD", EffectiveFrom: from, PricingVersion: "custom-v1"},
		{Provider: "openai", ModelSelector: "model-x", TokenCategory: TokenCategoryInput, RateMicros: 2, Currency: "USD", EffectiveFrom: from, PricingVersion: "bundle-v1", CatalogEntryID: "bundle:standard", ServiceTierSelector: "standard"},
	}}
	context := BillingContext{ServiceTier: &BillingContextValue{Value: "standard", Evidence: BillingEvidenceProviderResponse}}
	rule, ok, ambiguous := catalog.find("openai", "model-x", "", TokenCategoryInput, from.Add(time.Hour), context)
	if !ok || ambiguous || rule.PricingVersion != "custom-v1" {
		t.Fatalf("context-independent custom rule lost its established precedence: %+v (ok=%v ambiguous=%v)", rule, ok, ambiguous)
	}
}

func TestBillingContextRequiresEvidence(t *testing.T) {
	event := testEvent("billing-context")
	event.BillingContext.ServiceTier = &BillingContextValue{Value: "standard", Evidence: "untrusted free text"}
	if err := event.Validate(); err == nil {
		t.Fatal("billing context without evidence was accepted")
	}
}

func TestCustomPricingCannotClaimBundledCatalogOrigin(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rule := PricingRule{Provider: "test", ModelSelector: "model", TokenCategory: TokenCategoryInput,
		RateMicros: 1, Currency: "USD", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PricingVersion: "caller-v1", CatalogEntryID: "pretend:catalog"}
	if err := store.UpsertPricing(rule); err == nil {
		t.Fatal("custom pricing accepted reserved catalog entry ID")
	}
}

func TestBundledCatalogRejectsEqualPrecedenceWildcardOverlap(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var bundle bundledCatalog
	if err := json.Unmarshal(bundledPricingCatalog, &bundle); err != nil {
		t.Fatal(err)
	}
	base := bundle.Entries[0]
	base.ID = "openai:gpt-prefix:wide"
	base.Model = "gpt-*"
	base.EffectiveFrom = "2027-01-01T00:00:00Z"
	base.PricingVersion = "public-api-2027-v1"
	base.ReviewedAt = "2026-10-14"
	narrow := base
	narrow.ID = "openai:gpt-prefix:narrow"
	narrow.Model = "gpt-5*"
	narrow.Numerator++
	bundle.Entries = append(bundle.Entries, base, narrow)
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IS NOT NULL").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := store.syncPricingCatalogBytes(data); err == nil {
		t.Fatal("accepted overlapping bundled wildcard selectors")
	}
	var after int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pricing_rules WHERE catalog_entry_id IS NOT NULL").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("rejected wildcard sync changed row count from %d to %d", before, after)
	}
}

func TestZeroUsageRequiresSupportedPricingContext(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalog, err := store.pricingCatalog()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	supported := reportEvent{OccurredAt: at, Provider: "openai", RequestedModel: "gpt-5-codex", ResolvedModel: "gpt-5-codex"}
	if got := catalog.calculate(supported); got.Status != "zero" || got.Provider != "openai" || got.PricingVersion == "" || got.ModelSelector != "gpt-5-codex" {
		t.Fatalf("supported zero usage = %+v, want known-zero basis with provenance", got)
	}
	unsupported := reportEvent{OccurredAt: at, Provider: "unknown", RequestedModel: "unknown-model", ResolvedModel: "unknown-model"}
	if got := catalog.calculate(unsupported); got.Status != "unknown" || got.Reason != "no_matching_rate" || got.Provider != "unknown" {
		t.Fatalf("unsupported zero usage = %+v, want unknown with no_matching_rate", got)
	}
}

func TestMigrationsOneThroughTwelvePreserveLegacyData(t *testing.T) {
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
	if _, err := store.AppendWithReferences([]UsageEvent{event}, []Project{{ID: "legacy-project", DisplayName: "Legacy Project"}}, []Session{{ID: "legacy-session", ProjectID: "legacy-project", Client: Client{Name: "codex-cli"}, SourceMetadata: json.RawMessage(`{"adapter":"codex"}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE projects SET root = '/legacy/shared' WHERE id = 'legacy-project'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO projects(id, root, display_name, created_at) VALUES ('legacy-project-2', '/legacy/shared', 'Legacy Project 2', ?)", "2026-01-01T00:00:00Z"); err != nil {
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
	if migrationCount != 12 || latestMigration != 12 {
		t.Fatalf("migration history = %d/%d, want all migrations 1 through 12", migrationCount, latestMigration)
	}
	var identityTable string
	if err := db.QueryRow("SELECT name FROM sqlite_schema WHERE type = 'table' AND name = 'collector_source_identities'").Scan(&identityTable); err != nil {
		t.Fatalf("collector source identity migration was not applied: %v", err)
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
	var projectAlias, sessionAlias, locationWarnings int
	if err := db.QueryRow("SELECT COUNT(*) FROM project_aliases WHERE scope = 'codex' AND alias = 'legacy-project' AND project_id = 'legacy-project'").Scan(&projectAlias); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM session_aliases WHERE scope = 'codex:codex-cli' AND alias = 'legacy-session' AND session_id = 'legacy-session'").Scan(&sessionAlias); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM reconciliation_warnings WHERE kind = 'legacy-location-conflict'").Scan(&locationWarnings); err != nil {
		t.Fatal(err)
	}
	if projectAlias != 1 || sessionAlias != 1 || locationWarnings != 2 {
		t.Fatalf("migration identity seed = project alias %d, session alias %d, warnings %d; want 1/1/2", projectAlias, sessionAlias, locationWarnings)
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
	if got.Status != "calculated" || got.AmountMicros != 11 || got.Provider != "test" || got.PricingVersion != "mixed" || got.SourceURL != "mixed" || got.EffectiveTo != "mixed" {
		t.Fatalf("multi-component provenance = %+v, want total 11 and mixed entry provenance", got)
	}
}

func TestCalculationBasisPreservesCustomAndCatalogOrigins(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	custom := PricingRule{Provider: "test", ModelSelector: "model", TokenCategory: TokenCategoryInput, RateMicros: 2, Currency: "USD", EffectiveFrom: from, PricingVersion: "custom-v1"}
	bundled := custom
	bundled.TokenCategory = TokenCategoryOutput
	bundled.PricingVersion = "public-v1"
	bundled.CatalogEntryID = "public:model:output"
	calculate := func(t *testing.T, rules []PricingRule, usage TokenUsage, wantStatus, wantBasis string) {
		t.Helper()
		catalog := pricingCatalog{rules: rules}
		cost := catalog.calculate(reportEvent{OccurredAt: from, Provider: "test", RequestedModel: "model", ResolvedModel: "model", Usage: usage})
		acc := reportAccumulator{sessions: map[string]struct{}{}}
		if err := addMeasures(&acc, reportEvent{Provider: "test", Usage: usage}, cost); err != nil {
			t.Fatal(err)
		}
		if cost.Status != wantStatus || acc.CalculationBasis != wantBasis {
			t.Fatalf("cost=%+v basis=%q; want status=%q basis=%q", cost, acc.CalculationBasis, wantStatus, wantBasis)
		}
	}
	t.Run("custom only", func(t *testing.T) {
		calculate(t, []PricingRule{custom}, TokenUsage{InputTokens: 3}, "calculated", "custom_pricing_estimate")
	})
	t.Run("bundled only", func(t *testing.T) {
		calculate(t, []PricingRule{bundled}, TokenUsage{OutputTokens: 3}, "calculated", "public_catalog_estimate")
	})
	t.Run("mixed components", func(t *testing.T) {
		calculate(t, []PricingRule{custom, bundled}, TokenUsage{InputTokens: 3, OutputTokens: 3}, "calculated", "mixed_estimate")
	})
	t.Run("partially known custom", func(t *testing.T) {
		calculate(t, []PricingRule{custom}, TokenUsage{InputTokens: 3, OutputTokens: 2}, "unknown", "partial_custom_pricing_estimate")
	})
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
