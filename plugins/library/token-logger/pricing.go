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
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	TokenCategoryInput      = "input"
	TokenCategoryOutput     = "output"
	TokenCategoryCacheRead  = "cache-read"
	TokenCategoryCacheWrite = "cache-write"
	TokenCategoryReasoning  = "reasoning"
)

// PricingRule is an append-only, time-bounded price for one token category.
// RateMicros is the integer micro-unit price for one token.
type PricingRule struct {
	Provider              string
	ModelSelector         string
	TokenCategory         string
	RateMicros            int64
	RateNumeratorMicros   int64
	RateDenominatorTokens int64
	Currency              string
	EffectiveFrom         time.Time
	EffectiveTo           *time.Time
	PricingVersion        string
	Source                string
	Notes                 string
	CatalogEntryID        string
	SourceURL             string
	SourceReviewedAt      string
}

func (r PricingRule) validate() error {
	if strings.TrimSpace(r.Provider) == "" || strings.TrimSpace(r.Currency) == "" || strings.TrimSpace(r.PricingVersion) == "" {
		return errors.New("pricing provider, currency, and version are required")
	}
	switch r.TokenCategory {
	case TokenCategoryInput, TokenCategoryOutput, TokenCategoryCacheRead, TokenCategoryCacheWrite, TokenCategoryReasoning:
	default:
		return fmt.Errorf("unsupported token category %q", r.TokenCategory)
	}
	if r.RateMicros < 0 {
		return errors.New("pricing rate must not be negative")
	}
	if r.RateNumeratorMicros < 0 || r.RateDenominatorTokens < 0 {
		return errors.New("pricing rational rate must not be negative")
	}
	if r.RateDenominatorTokens == 0 && r.RateNumeratorMicros != 0 {
		return errors.New("pricing rate denominator must be positive")
	}
	if r.EffectiveFrom.IsZero() || r.EffectiveFrom.Location() != time.UTC {
		return errors.New("pricing effective-from must be UTC")
	}
	if r.EffectiveTo != nil && (r.EffectiveTo.Location() != time.UTC || !r.EffectiveTo.After(r.EffectiveFrom)) {
		return errors.New("pricing effective-to must be UTC and after effective-from")
	}
	return nil
}

// UpsertPricing adds a pricing rule. Existing rules are never rewritten;
// callers should use a new pricing version and effective interval for updates.
func (s *Store) UpsertPricing(rule PricingRule) error {
	if err := rule.validate(); err != nil {
		return err
	}
	numerator, denominator := rule.RateNumeratorMicros, rule.RateDenominatorTokens
	if denominator == 0 {
		numerator, denominator = rule.RateMicros, 1
	}
	end := any("9999-12-31T23:59:59.999999999Z")
	if rule.EffectiveTo != nil {
		end = formatStoredTime(*rule.EffectiveTo)
	}
	rows, err := s.db.Query(`SELECT model_selector FROM pricing_rules
		WHERE provider_name = ? AND token_category = ?
		AND catalog_entry_id IS NULL AND effective_from < ?
		AND (effective_to IS NULL OR effective_to > ?)`, rule.Provider,
		rule.TokenCategory, end, formatStoredTime(rule.EffectiveFrom))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var existingSelector string
		if err := rows.Scan(&existingSelector); err != nil {
			return err
		}
		if selectorsHaveEqualPrecedenceOverlap(existingSelector, rule.ModelSelector) {
			return errors.New("overlapping custom pricing rules with equal-precedence model selectors are ambiguous")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO pricing_rules(
		provider_name, model_selector, token_category, rate_micros, currency,
		effective_from, effective_to, pricing_version, source, notes,
		rate_numerator_micros, rate_denominator_tokens, catalog_entry_id, source_url, source_reviewed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rule.Provider, rule.ModelSelector,
		rule.TokenCategory, rule.RateMicros, rule.Currency,
		formatStoredTime(rule.EffectiveFrom), nullableTime(rule.EffectiveTo),
		rule.PricingVersion, rule.Source, rule.Notes, numerator, denominator,
		nullableString(rule.CatalogEntryID), rule.SourceURL, rule.SourceReviewedAt)
	return err
}

func selectorsHaveEqualPrecedenceOverlap(a, b string) bool {
	aGlobal, bGlobal := a == "" || a == "*", b == "" || b == "*"
	if aGlobal || bGlobal {
		return aGlobal && bGlobal
	}
	aWildcard, bWildcard := strings.HasSuffix(a, "*"), strings.HasSuffix(b, "*")
	if aWildcard && bWildcard {
		aPrefix, bPrefix := strings.TrimSuffix(a, "*"), strings.TrimSuffix(b, "*")
		return strings.HasPrefix(aPrefix, bPrefix) || strings.HasPrefix(bPrefix, aPrefix)
	}
	return !aWildcard && !bWildcard && a == b
}

type pricingCatalog struct {
	rules []PricingRule
}

func (s *Store) pricingCatalog() (pricingCatalog, error) {
	rows, err := s.db.Query(`SELECT provider_name, model_selector, token_category,
		rate_micros, currency, effective_from, effective_to, pricing_version, source, notes,
		rate_numerator_micros, rate_denominator_tokens, COALESCE(catalog_entry_id, ''), source_url, source_reviewed_at
		FROM pricing_rules ORDER BY effective_from DESC, id DESC`)
	if err != nil {
		return pricingCatalog{}, err
	}
	defer rows.Close()
	var catalog pricingCatalog
	for rows.Next() {
		var rule PricingRule
		var from string
		var to sql.NullString
		if err := rows.Scan(&rule.Provider, &rule.ModelSelector, &rule.TokenCategory,
			&rule.RateMicros, &rule.Currency, &from, &to, &rule.PricingVersion,
			&rule.Source, &rule.Notes, &rule.RateNumeratorMicros, &rule.RateDenominatorTokens,
			&rule.CatalogEntryID, &rule.SourceURL, &rule.SourceReviewedAt); err != nil {
			return pricingCatalog{}, err
		}
		parsedFrom, err := time.Parse(time.RFC3339Nano, from)
		if err != nil {
			return pricingCatalog{}, fmt.Errorf("parse pricing effective-from: %w", err)
		}
		rule.EffectiveFrom = parsedFrom.UTC()
		if to.Valid && to.String != "" {
			parsedTo, err := time.Parse(time.RFC3339Nano, to.String)
			if err != nil {
				return pricingCatalog{}, fmt.Errorf("parse pricing effective-to: %w", err)
			}
			parsedTo = parsedTo.UTC()
			rule.EffectiveTo = &parsedTo
		}
		catalog.rules = append(catalog.rules, rule)
	}
	return catalog, rows.Err()
}

type priceMatch struct {
	rule       PricingRule
	precedence int
	custom     bool
}

func (c pricingCatalog) find(provider, resolvedModel, requestedModel, category string, at time.Time) (PricingRule, bool, bool) {
	best := priceMatch{}
	ambiguous := false
	for _, rule := range c.rules {
		if rule.Provider != provider || rule.TokenCategory != category || at.Before(rule.EffectiveFrom) || (rule.EffectiveTo != nil && !at.Before(*rule.EffectiveTo)) {
			continue
		}
		precedence := modelPrecedence(rule.ModelSelector, resolvedModel, requestedModel)
		custom := rule.CatalogEntryID == ""
		if precedence > best.precedence || (precedence == best.precedence && custom && !best.custom) {
			best = priceMatch{rule: rule, precedence: precedence, custom: custom}
			ambiguous = false
		} else if precedence > 0 && precedence == best.precedence && custom == best.custom && rule != best.rule {
			ambiguous = true
		}
	}
	return best.rule, best.precedence > 0 && !ambiguous, ambiguous
}

func (c pricingCatalog) missingReason(provider, resolvedModel, requestedModel, category string, at time.Time) string {
	if resolvedModel == "" && requestedModel == "" {
		return "model_unresolved"
	}
	modelKnown, categoryKnown := false, false
	for _, rule := range c.rules {
		if rule.Provider != provider || modelPrecedence(rule.ModelSelector, resolvedModel, requestedModel) == 0 {
			continue
		}
		modelKnown = true
		if rule.TokenCategory == category {
			categoryKnown = true
			if at.Before(rule.EffectiveFrom) || (rule.EffectiveTo != nil && !at.Before(*rule.EffectiveTo)) {
				return "outside_effective_range"
			}
		}
	}
	if modelKnown && !categoryKnown {
		return "billing_context_missing"
	}
	return "no_matching_rate"
}

func modelPrecedence(selector, resolved, requested string) int {
	if selector == resolved && resolved != "" {
		return 400
	}
	if selector == requested && requested != "" {
		return 300
	}
	if selector != "" && selector != "*" && strings.HasSuffix(selector, "*") {
		prefix := strings.TrimSuffix(selector, "*")
		if strings.HasPrefix(resolved, prefix) && resolved != "" {
			return 200
		}
		if strings.HasPrefix(requested, prefix) && requested != "" {
			return 150
		}
	}
	if selector == "" || selector == "*" {
		return 100
	}
	return 0
}

type eventCost struct {
	Status         string
	Reason         string
	AmountMicros   int64
	Currency       string
	PricingVersion string
	SourceURL      string
	Source         string
	ModelSelector  string
	EffectiveFrom  string
	EffectiveTo    string
}

type pricingProvenance struct {
	count                                                         int
	version, sourceURL, source, model, effectiveFrom, effectiveTo string
}

func (p *pricingProvenance) add(rule PricingRule) {
	from, to := formatStoredTime(rule.EffectiveFrom), formatEffectiveTo(rule.EffectiveTo)
	if p.count == 0 {
		p.version, p.sourceURL, p.source, p.model, p.effectiveFrom, p.effectiveTo = rule.PricingVersion, rule.SourceURL, rule.Source, rule.ModelSelector, from, to
	} else {
		p.version = strictMergeProvenance(p.version, rule.PricingVersion)
		p.sourceURL = strictMergeProvenance(p.sourceURL, rule.SourceURL)
		p.source = strictMergeProvenance(p.source, rule.Source)
		p.model = strictMergeProvenance(p.model, rule.ModelSelector)
		p.effectiveFrom = strictMergeProvenance(p.effectiveFrom, from)
		p.effectiveTo = strictMergeProvenance(p.effectiveTo, to)
	}
	p.count++
}

func strictMergeProvenance(current, next string) string {
	if current == next || current == "mixed" {
		return current
	}
	return "mixed"
}

func (p pricingProvenance) apply(cost *eventCost) {
	if p.count == 0 {
		return
	}
	cost.PricingVersion, cost.SourceURL, cost.Source = p.version, p.sourceURL, p.source
	cost.ModelSelector, cost.EffectiveFrom, cost.EffectiveTo = p.model, p.effectiveFrom, p.effectiveTo
}

func (c pricingCatalog) calculate(event reportEvent) eventCost {
	usage := event.Usage
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return c.calculateZeroUsage(event)
	}
	amount := new(big.Rat)
	currency := ""
	missing := false
	reason := ""
	var provenance pricingProvenance
	charge := func(category string, tokens int64) {
		if tokens == 0 {
			return
		}
		rule, ok, ambiguous := c.find(event.Provider, event.ResolvedModel, event.RequestedModel, category, event.OccurredAt)
		if !ok {
			missing = true
			if ambiguous {
				reason = "ambiguous_pricing_rule"
			} else if reason == "" {
				reason = c.missingReason(event.Provider, event.ResolvedModel, event.RequestedModel, category, event.OccurredAt)
			}
			return
		}
		provenance.add(rule)
		if currency == "" {
			currency = rule.Currency
		} else if currency != rule.Currency {
			missing = true
			reason = "currency_conflict"
			amount.SetInt64(0)
			currency = "mixed"
			return
		}
		numerator, denominator := rule.RateNumeratorMicros, rule.RateDenominatorTokens
		if denominator == 0 {
			numerator, denominator = rule.RateMicros, 1
		}
		part := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(tokens), big.NewInt(numerator)), big.NewInt(denominator))
		amount.Add(amount, part)
	}
	charge(TokenCategoryInput, usage.InputTokens-usage.CacheReadTokens-usage.CacheWriteTokens)
	charge(TokenCategoryCacheRead, usage.CacheReadTokens)
	charge(TokenCategoryCacheWrite, usage.CacheWriteTokens)
	charge(TokenCategoryOutput, usage.OutputTokens-usage.ReasoningTokens)
	if usage.ReasoningTokens > 0 {
		if _, ok, ambiguous := c.find(event.Provider, event.ResolvedModel, event.RequestedModel, TokenCategoryReasoning, event.OccurredAt); ambiguous {
			missing = true
			reason = "ambiguous_pricing_rule"
		} else if ok {
			charge(TokenCategoryReasoning, usage.ReasoningTokens)
		} else {
			charge(TokenCategoryOutput, usage.ReasoningTokens)
		}
	}
	if missing {
		cost := eventCost{Status: "unknown", Reason: reason, Currency: currency}
		if currency != "" {
			rounded, ok := roundRationalMicros(amount)
			if !ok {
				return eventCost{Status: "unknown", Reason: "arithmetic_overflow", Currency: currency}
			}
			cost.AmountMicros = rounded
		}
		provenance.apply(&cost)
		return cost
	}
	// Round once per event to the nearest microcurrency unit; exact halves round away from zero.
	rounded, ok := roundRationalMicros(amount)
	if !ok {
		return eventCost{Status: "unknown", Reason: "arithmetic_overflow", Currency: currency}
	}
	cost := eventCost{Status: "calculated", AmountMicros: rounded, Currency: currency}
	provenance.apply(&cost)
	return cost
}

func (c pricingCatalog) calculateZeroUsage(event reportEvent) eventCost {
	var provenance pricingProvenance
	currency := ""
	matched := false
	for _, category := range []string{TokenCategoryInput, TokenCategoryCacheRead, TokenCategoryCacheWrite, TokenCategoryOutput, TokenCategoryReasoning} {
		rule, ok, ambiguous := c.find(event.Provider, event.ResolvedModel, event.RequestedModel, category, event.OccurredAt)
		if ambiguous {
			return eventCost{Status: "unknown", Reason: "ambiguous_pricing_rule"}
		}
		if !ok {
			continue
		}
		matched = true
		provenance.add(rule)
		if currency == "" {
			currency = rule.Currency
		} else if currency != rule.Currency {
			currency = "mixed"
		}
	}
	if !matched {
		reason := c.missingReason(event.Provider, event.ResolvedModel, event.RequestedModel, TokenCategoryInput, event.OccurredAt)
		for _, rule := range c.rules {
			if rule.Provider == event.Provider && modelPrecedence(rule.ModelSelector, event.ResolvedModel, event.RequestedModel) > 0 {
				reason = "outside_effective_range"
				break
			}
		}
		return eventCost{Status: "unknown", Reason: reason}
	}
	cost := eventCost{Status: "zero", Currency: currency}
	provenance.apply(&cost)
	return cost
}

func roundRationalMicros(amount *big.Rat) (int64, bool) {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(amount.Num(), amount.Denom(), remainder)
	if new(big.Int).Lsh(new(big.Int).Abs(remainder), 1).Cmp(amount.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}

func formatEffectiveTo(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatStoredTime(*value)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
