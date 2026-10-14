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
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

//go:embed pricing-catalog.json
var bundledPricingCatalog []byte

type bundledCatalog struct {
	Version    string                `json:"version"`
	ReviewedAt string                `json:"reviewed_at"`
	Entries    []bundledCatalogEntry `json:"entries"`
}

type bundledCatalogEntry struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Category       string `json:"category"`
	Numerator      int64  `json:"numerator_micros"`
	Denominator    int64  `json:"denominator_tokens"`
	Currency       string `json:"currency"`
	EffectiveFrom  string `json:"effective_from"`
	EffectiveTo    string `json:"effective_to"`
	SourceURL      string `json:"source_url"`
	Notes          string `json:"notes"`
	PricingVersion string `json:"pricing_version"`
	ReviewedAt     string `json:"reviewed_at"`
}

func (s *Store) syncBundledPricingCatalog() error {
	return s.syncPricingCatalogBytes(bundledPricingCatalog)
}

func (s *Store) syncPricingCatalogBytes(data []byte) error {
	var bundle bundledCatalog
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("decode bundled pricing catalog: %w", err)
	}
	if strings.TrimSpace(bundle.Version) == "" || len(bundle.Entries) == 0 {
		return errors.New("bundled pricing catalog requires a version and entries")
	}
	seenIDs := make(map[string]struct{}, len(bundle.Entries))
	for _, entry := range bundle.Entries {
		if strings.TrimSpace(entry.ID) == "" {
			return errors.New("bundled pricing entry ID is required")
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return fmt.Errorf("duplicate bundled pricing entry ID %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		reviewed := entry.ReviewedAt
		if reviewed == "" {
			reviewed = bundle.ReviewedAt
		}
		if _, err := time.Parse("2006-01-02", reviewed); err != nil {
			return fmt.Errorf("catalog entry %s reviewed_at must be YYYY-MM-DD: %w", entry.ID, err)
		}
		source, err := url.ParseRequestURI(entry.SourceURL)
		if err != nil || source.Scheme != "https" || !isOfficialPricingHost(entry.Provider, source.Hostname()) {
			return fmt.Errorf("catalog entry %s requires an HTTPS URL on the provider's official pricing domain", entry.ID)
		}
		if entry.Denominator <= 0 {
			return fmt.Errorf("catalog entry %s denominator_tokens must be positive", entry.ID)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin pricing catalog sync: %w", err)
	}
	defer tx.Rollback()
	for _, entry := range bundle.Entries {
		from, err := time.Parse(time.RFC3339, entry.EffectiveFrom)
		if err != nil {
			return fmt.Errorf("catalog entry %s effective_from: %w", entry.ID, err)
		}
		var to *time.Time
		if entry.EffectiveTo != "" {
			parsed, err := time.Parse(time.RFC3339, entry.EffectiveTo)
			if err != nil {
				return fmt.Errorf("catalog entry %s effective_to: %w", entry.ID, err)
			}
			parsed = parsed.UTC()
			to = &parsed
		}
		pricingVersion := entry.PricingVersion
		if pricingVersion == "" {
			pricingVersion = bundle.Version
		}
		reviewedAt := entry.ReviewedAt
		if reviewedAt == "" {
			reviewedAt = bundle.ReviewedAt
		}
		rule := PricingRule{Provider: entry.Provider, ModelSelector: entry.Model, TokenCategory: entry.Category,
			RateNumeratorMicros: entry.Numerator, RateDenominatorTokens: entry.Denominator, Currency: entry.Currency,
			EffectiveFrom: from.UTC(), EffectiveTo: to, PricingVersion: pricingVersion, Source: "official public API pricing",
			Notes: entry.Notes, CatalogEntryID: entry.ID, SourceURL: entry.SourceURL, SourceReviewedAt: reviewedAt}
		if err := rule.validate(); err != nil {
			return fmt.Errorf("invalid bundled pricing entry %s: %w", entry.ID, err)
		}
		if err := syncCatalogEntry(tx, rule); err != nil {
			return fmt.Errorf("sync catalog entry %s: %w", entry.ID, err)
		}
	}
	if err := validateBundledIntervals(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pricing catalog sync: %w", err)
	}
	return nil
}

func isOfficialPricingHost(provider, hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	var domains []string
	switch provider {
	case "openai":
		domains = []string{"openai.com"}
	case "anthropic":
		domains = []string{"anthropic.com", "claude.com"}
	case "google":
		domains = []string{"google.com", "google.dev"}
	case "xai":
		domains = []string{"x.ai"}
	default:
		return false
	}
	for _, domain := range domains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	return false
}

func validateBundledIntervals(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT provider_name, model_selector, token_category, effective_from, effective_to FROM pricing_rules WHERE catalog_entry_id IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var rules []PricingRule
	for rows.Next() {
		var rule PricingRule
		var from string
		var to sql.NullString
		if err := rows.Scan(&rule.Provider, &rule.ModelSelector, &rule.TokenCategory, &from, &to); err != nil {
			return err
		}
		rule.EffectiveFrom, err = time.Parse(time.RFC3339Nano, from)
		if err != nil {
			return err
		}
		if to.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, to.String)
			if parseErr != nil {
				return parseErr
			}
			rule.EffectiveTo = &parsed
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range rules {
		for j := i + 1; j < len(rules); j++ {
			a, b := rules[i], rules[j]
			if a.Provider == b.Provider && a.TokenCategory == b.TokenCategory &&
				selectorsHaveEqualPrecedenceOverlap(a.ModelSelector, b.ModelSelector) && intervalsOverlap(a, b) {
				return fmt.Errorf("bundled pricing intervals overlap for %s/%s/%s", a.Provider, a.ModelSelector, a.TokenCategory)
			}
		}
	}
	return nil
}

func intervalsOverlap(a, b PricingRule) bool {
	return (a.EffectiveTo == nil || b.EffectiveFrom.Before(*a.EffectiveTo)) && (b.EffectiveTo == nil || a.EffectiveFrom.Before(*b.EffectiveTo))
}

func syncCatalogEntry(tx *sql.Tx, rule PricingRule) error {
	var existing PricingRule
	var effectiveFrom string
	var effectiveTo sql.NullString
	var legacyRate int64
	err := tx.QueryRow(`SELECT provider_name, model_selector, token_category, rate_micros, currency,
		effective_from, effective_to, pricing_version, source, notes, rate_numerator_micros,
		rate_denominator_tokens, source_url, source_reviewed_at FROM pricing_rules WHERE catalog_entry_id = ?`, rule.CatalogEntryID).
		Scan(&existing.Provider, &existing.ModelSelector, &existing.TokenCategory, &legacyRate, &existing.Currency,
			&effectiveFrom, &effectiveTo, &existing.PricingVersion, &existing.Source, &existing.Notes,
			&existing.RateNumeratorMicros, &existing.RateDenominatorTokens, &existing.SourceURL, &existing.SourceReviewedAt)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO pricing_rules(
			provider_name, model_selector, token_category, rate_micros, currency, effective_from,
			effective_to, pricing_version, source, notes, rate_numerator_micros,
			rate_denominator_tokens, catalog_entry_id, source_url, source_reviewed_at)
			VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rule.Provider, rule.ModelSelector,
			rule.TokenCategory, rule.Currency, formatStoredTime(rule.EffectiveFrom), nullableTime(rule.EffectiveTo), rule.PricingVersion,
			rule.Source, rule.Notes, rule.RateNumeratorMicros, rule.RateDenominatorTokens,
			rule.CatalogEntryID, rule.SourceURL, rule.SourceReviewedAt)
		return err
	}
	if err != nil {
		return err
	}
	var storedFrom time.Time
	storedFrom, err = time.Parse(time.RFC3339Nano, effectiveFrom)
	if err != nil {
		return fmt.Errorf("parse stored effective_from: %w", err)
	}
	existing.EffectiveFrom = storedFrom.UTC()
	if effectiveTo.Valid {
		parsed, parseErr := time.Parse(time.RFC3339Nano, effectiveTo.String)
		if parseErr != nil {
			return parseErr
		}
		parsed = parsed.UTC()
		existing.EffectiveTo = &parsed
	}
	if legacyRate != 0 || existing.Provider != rule.Provider || existing.ModelSelector != rule.ModelSelector ||
		existing.TokenCategory != rule.TokenCategory || existing.Currency != rule.Currency ||
		existing.PricingVersion != rule.PricingVersion || existing.Source != rule.Source || existing.Notes != rule.Notes ||
		existing.RateNumeratorMicros != rule.RateNumeratorMicros || existing.RateDenominatorTokens != rule.RateDenominatorTokens ||
		existing.SourceURL != rule.SourceURL || existing.SourceReviewedAt != rule.SourceReviewedAt ||
		!existing.EffectiveFrom.Equal(rule.EffectiveFrom) {
		return errors.New("catalog entry ID conflicts with immutable stored pricing fields")
	}
	if existing.EffectiveTo == nil && rule.EffectiveTo != nil {
		result, err := tx.Exec("UPDATE pricing_rules SET effective_to = ? WHERE catalog_entry_id = ? AND effective_to IS NULL", formatStoredTime(*rule.EffectiveTo), rule.CatalogEntryID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("catalog interval closure changed concurrently")
		}
		return nil
	}
	if (existing.EffectiveTo == nil) != (rule.EffectiveTo == nil) || (existing.EffectiveTo != nil && !existing.EffectiveTo.Equal(*rule.EffectiveTo)) {
		return errors.New("catalog entry cannot reopen or change a closed effective interval")
	}
	return nil
}
