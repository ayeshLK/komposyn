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
	Provider       string
	ModelSelector  string
	TokenCategory  string
	RateMicros     int64
	Currency       string
	EffectiveFrom  time.Time
	EffectiveTo    *time.Time
	PricingVersion string
	Source         string
	Notes          string
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
	_, err := s.db.Exec(`INSERT INTO pricing_rules(
		provider_name, model_selector, token_category, rate_micros, currency,
		effective_from, effective_to, pricing_version, source, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rule.Provider, rule.ModelSelector,
		rule.TokenCategory, rule.RateMicros, rule.Currency,
		formatStoredTime(rule.EffectiveFrom), nullableTime(rule.EffectiveTo),
		rule.PricingVersion, rule.Source, rule.Notes)
	return err
}

type pricingCatalog struct {
	rules []PricingRule
}

func (s *Store) pricingCatalog() (pricingCatalog, error) {
	rows, err := s.db.Query(`SELECT provider_name, model_selector, token_category,
		rate_micros, currency, effective_from, effective_to, pricing_version, source, notes
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
			&rule.Source, &rule.Notes); err != nil {
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
}

func (c pricingCatalog) find(provider, resolvedModel, requestedModel, category string, at time.Time) (PricingRule, bool) {
	best := priceMatch{}
	for _, rule := range c.rules {
		if rule.Provider != provider || rule.TokenCategory != category || at.Before(rule.EffectiveFrom) || (rule.EffectiveTo != nil && !at.Before(*rule.EffectiveTo)) {
			continue
		}
		precedence := modelPrecedence(rule.ModelSelector, resolvedModel, requestedModel)
		if precedence > best.precedence {
			best = priceMatch{rule: rule, precedence: precedence}
		}
	}
	return best.rule, best.precedence > 0
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
	Status       string
	AmountMicros int64
	Currency     string
}

func (c pricingCatalog) calculate(event reportEvent) eventCost {
	usage := event.Usage
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return eventCost{Status: "calculated", Currency: ""}
	}
	amount := int64(0)
	currency := ""
	missing := false
	charge := func(category string, tokens int64) {
		if tokens == 0 || missing {
			return
		}
		rule, ok := c.find(event.Provider, event.ResolvedModel, event.RequestedModel, category, event.OccurredAt)
		if !ok {
			missing = true
			return
		}
		if currency == "" {
			currency = rule.Currency
		} else if currency != rule.Currency {
			missing = true
			return
		}
		amount += tokens * rule.RateMicros
	}
	charge(TokenCategoryInput, usage.InputTokens-usage.CacheReadTokens-usage.CacheWriteTokens)
	charge(TokenCategoryCacheRead, usage.CacheReadTokens)
	charge(TokenCategoryCacheWrite, usage.CacheWriteTokens)
	charge(TokenCategoryOutput, usage.OutputTokens-usage.ReasoningTokens)
	if usage.ReasoningTokens > 0 {
		if _, ok := c.find(event.Provider, event.ResolvedModel, event.RequestedModel, TokenCategoryReasoning, event.OccurredAt); ok {
			charge(TokenCategoryReasoning, usage.ReasoningTokens)
		} else {
			charge(TokenCategoryOutput, usage.ReasoningTokens)
		}
	}
	if missing {
		return eventCost{Status: "unknown", Currency: currency}
	}
	return eventCost{Status: "calculated", AmountMicros: amount, Currency: currency}
}
