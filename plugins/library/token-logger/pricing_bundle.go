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
	_ "embed"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed pricing-catalog.json
var bundledPricingCatalog []byte

type bundledCatalog struct {
	Version    string `json:"version"`
	ReviewedAt string `json:"reviewed_at"`
	Entries    []struct {
		ID            string `json:"id"`
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		Category      string `json:"category"`
		Numerator     int64  `json:"numerator_micros"`
		Denominator   int64  `json:"denominator_tokens"`
		Currency      string `json:"currency"`
		EffectiveFrom string `json:"effective_from"`
		EffectiveTo   string `json:"effective_to"`
		SourceURL     string `json:"source_url"`
		Notes         string `json:"notes"`
	} `json:"entries"`
}

func (s *Store) syncBundledPricingCatalog() error {
	var bundle bundledCatalog
	if err := json.Unmarshal(bundledPricingCatalog, &bundle); err != nil {
		return fmt.Errorf("decode bundled pricing catalog: %w", err)
	}
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
		rule := PricingRule{Provider: entry.Provider, ModelSelector: entry.Model, TokenCategory: entry.Category,
			RateNumeratorMicros: entry.Numerator, RateDenominatorTokens: entry.Denominator, Currency: entry.Currency,
			EffectiveFrom: from.UTC(), EffectiveTo: to, PricingVersion: bundle.Version, Source: "official public API pricing",
			Notes: entry.Notes, CatalogEntryID: entry.ID, SourceURL: entry.SourceURL, SourceReviewedAt: bundle.ReviewedAt}
		if err := rule.validate(); err != nil {
			return fmt.Errorf("invalid bundled pricing entry %s: %w", entry.ID, err)
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO pricing_rules(
			provider_name, model_selector, token_category, rate_micros, currency, effective_from,
			effective_to, pricing_version, source, notes, rate_numerator_micros,
			rate_denominator_tokens, catalog_entry_id, source_url, source_reviewed_at)
			VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rule.Provider, rule.ModelSelector,
			rule.TokenCategory, rule.Currency, formatStoredTime(rule.EffectiveFrom), nullableTime(rule.EffectiveTo), rule.PricingVersion,
			rule.Source, rule.Notes, rule.RateNumeratorMicros, rule.RateDenominatorTokens,
			rule.CatalogEntryID, rule.SourceURL, rule.SourceReviewedAt); err != nil {
			return fmt.Errorf("sync catalog entry %s: %w", entry.ID, err)
		}
	}
	return nil
}
