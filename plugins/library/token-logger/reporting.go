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
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

const (
	GroupHour     = "hour"
	GroupDay      = "day"
	GroupWeek     = "week"
	GroupMonth    = "month"
	GroupProject  = "project"
	GroupSession  = "session"
	GroupClient   = "client"
	GroupProvider = "provider"
	GroupModel    = "model"
)

// UsageFilter is composable. From is inclusive and To is exclusive.
type UsageFilter struct {
	ProjectID      string         `json:"project_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Client         string         `json:"client,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Model          string         `json:"model,omitempty"`
	ProjectMissing bool           `json:"-"`
	SessionMissing bool           `json:"-"`
	ModelMissing   bool           `json:"-"`
	From           time.Time      `json:"from"`
	To             time.Time      `json:"to"`
	GroupBy        []string       `json:"group_by,omitempty"`
	Timezone       *time.Location `json:"-"`
}

// UsageMeasures contains additive token and cost measures.
type UsageMeasures struct {
	RequestCount               int64            `json:"request_count"`
	SessionCount               int64            `json:"session_count"`
	InputTokens                int64            `json:"input_tokens"`
	CachedInputTokens          int64            `json:"cached_input_tokens"`
	CacheWriteInputTokens      int64            `json:"cache_write_input_tokens"`
	OutputTokens               int64            `json:"output_tokens"`
	ReasoningOutputTokens      int64            `json:"reasoning_output_tokens"`
	TotalTokens                int64            `json:"total_tokens"`
	CalculatedCostMicros       int64            `json:"calculated_cost_micros"`
	CalculatedCurrency         string           `json:"calculated_currency,omitempty"`
	ProviderReportedCostMicros int64            `json:"provider_reported_cost_micros"`
	ProviderReportedCurrency   string           `json:"provider_reported_currency,omitempty"`
	UnknownCostRequests        int64            `json:"unknown_cost_requests"`
	EstimatedCostRequests      int64            `json:"estimated_cost_requests"`
	KnownZeroCostRequests      int64            `json:"known_zero_cost_requests"`
	CostUnknownReason          string           `json:"cost_unknown_reason,omitempty"`
	CostUnknownReasons         CostReasonCounts `json:"cost_unknown_reasons,omitempty"`
	PricingProvider            string           `json:"pricing_provider,omitempty"`
	CalculationBasis           string           `json:"calculation_basis,omitempty"`
	PricingVersion             string           `json:"pricing_version,omitempty"`
	PricingSource              string           `json:"pricing_source,omitempty"`
	PricingModelSelector       string           `json:"pricing_model_selector,omitempty"`
	PricingEffectiveFrom       string           `json:"pricing_effective_from,omitempty"`
	PricingEffectiveTo         string           `json:"pricing_effective_to,omitempty"`
}

// costReasonCounts stores deterministic JSON internally to keep UsageMeasures
// comparable while serializing as a JSON object and as a CSV JSON cell.
type CostReasonCounts string

func (counts CostReasonCounts) MarshalJSON() ([]byte, error) {
	if counts == "" {
		return []byte("{}"), nil
	}
	if !json.Valid([]byte(counts)) {
		return nil, fmt.Errorf("invalid cost reason counts")
	}
	return []byte(counts), nil
}

func (counts *CostReasonCounts) UnmarshalJSON(data []byte) error {
	var values map[string]int64
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if values == nil {
		return fmt.Errorf("cost reason counts must be a JSON object")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	*counts = CostReasonCounts(encoded)
	return nil
}

// UsageRow is a grouped report row. Bucket is populated for time groups.
type UsageRow struct {
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Bucket     string            `json:"bucket,omitempty"`
	UsageMeasures
}

// PricingCoverageRow identifies provider/model combinations whose estimates
// are unavailable. It is used only by human reports; machine report schemas
// remain unchanged.
type PricingCoverageRow struct {
	Provider   string
	Model      string
	Requests   int64
	Reasons    map[string]int64
	Categories map[string]int64
}

// UsageReport is the stable machine-readable report shape.
type UsageReport struct {
	From            time.Time            `json:"from"`
	To              time.Time            `json:"to"`
	Timezone        string               `json:"timezone"`
	Filters         UsageFilter          `json:"filters"`
	Summary         UsageMeasures        `json:"summary"`
	Rows            []UsageRow           `json:"rows"`
	PricingCoverage []PricingCoverageRow `json:"-"`
}

type reportEvent struct {
	OccurredAt          time.Time
	ProjectID           string
	SessionID           string
	Client              string
	Provider            string
	RequestedModel      string
	ResolvedModel       string
	BillingContext      BillingContext
	Usage               TokenUsage
	ProviderAmount      *int64
	ProviderCurrency    string
	RequestedCostStatus string
	ReportedFallback    *int64
	ReportedCurrency    string
}

type reportAccumulator struct {
	UsageMeasures
	sessions map[string]struct{}
}

// Query returns token usage, grouping, and cost for the supplied filter.
func (s *Store) Query(filter UsageFilter) (UsageReport, error) {
	if filter.From.IsZero() || filter.To.IsZero() || !filter.To.After(filter.From) {
		return UsageReport{}, fmt.Errorf("query requires a non-empty half-open time range")
	}
	if filter.From.Location() != time.UTC || filter.To.Location() != time.UTC {
		return UsageReport{}, fmt.Errorf("query range must use UTC")
	}
	seenGroups := make(map[string]struct{}, len(filter.GroupBy))
	for _, group := range filter.GroupBy {
		switch group {
		case GroupHour, GroupDay, GroupWeek, GroupMonth, GroupProject, GroupSession, GroupClient, GroupProvider, GroupModel:
		default:
			return UsageReport{}, fmt.Errorf("unsupported group-by %q", group)
		}
		if _, exists := seenGroups[group]; exists {
			return UsageReport{}, fmt.Errorf("duplicate group-by %q", group)
		}
		seenGroups[group] = struct{}{}
	}
	location := filter.Timezone
	if location == nil {
		location = time.UTC
	}
	catalog, err := s.pricingCatalog()
	if err != nil {
		return UsageReport{}, fmt.Errorf("load pricing catalog: %w", err)
	}
	query := `SELECT occurred_at, COALESCE(project_id, ''), COALESCE(session_id, ''), client_name, provider_name,
		requested_model, resolved_model, input_tokens, output_tokens, cache_read_tokens,
		cache_write_tokens, reasoning_tokens, provider_cost_amount_micros,
		provider_cost_currency, cost_status, cost_amount_micros, cost_currency
		, billing_context_json, cache_write_5m_tokens, cache_write_1h_tokens FROM usage_events WHERE occurred_at >= ? AND occurred_at < ?`
	args := []any{formatStoredTime(filter.From), formatStoredTime(filter.To)}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if filter.ProjectMissing {
		query += " AND project_id IS NULL"
	}
	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}
	if filter.SessionMissing {
		query += " AND session_id IS NULL"
	}
	if filter.Client != "" {
		query += " AND client_name = ?"
		args = append(args, filter.Client)
	}
	if filter.Provider != "" {
		query += " AND provider_name = ?"
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		query += " AND (resolved_model = ? OR requested_model = ?)"
		args = append(args, filter.Model, filter.Model)
	}
	if filter.ModelMissing {
		query += " AND resolved_model = '' AND requested_model = ''"
	}
	query += " ORDER BY occurred_at, event_id"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return UsageReport{}, fmt.Errorf("query usage events: %w", err)
	}
	defer rows.Close()
	summary := reportAccumulator{sessions: map[string]struct{}{}}
	grouped := map[string]*reportAccumulator{}
	groupLabels := map[string]UsageRow{}
	coverage := map[string]*PricingCoverageRow{}
	for rows.Next() {
		event, err := scanReportEvent(rows)
		if err != nil {
			return UsageReport{}, fmt.Errorf("scan usage event: %w", err)
		}
		cost := catalog.calculate(event)
		if event.RequestedCostStatus == "reported" && event.ReportedFallback != nil && event.ProviderAmount == nil {
			amount := *event.ReportedFallback
			event.ProviderAmount, event.ProviderCurrency = &amount, event.ReportedCurrency
		}
		if err := addMeasures(&summary, event, cost); err != nil {
			return UsageReport{}, err
		}
		if cost.Status == "unknown" {
			provider, model := event.Provider, event.ResolvedModel
			if model == "" {
				model = event.RequestedModel
			}
			key := provider + "\x00" + model
			entry := coverage[key]
			if entry == nil {
				entry = &PricingCoverageRow{Provider: provider, Model: model, Reasons: map[string]int64{}, Categories: map[string]int64{}}
				coverage[key] = entry
			}
			entry.Requests++
			entry.Reasons[cost.Reason]++
			for _, category := range cost.MissingCategories {
				entry.Categories[category]++
			}
		}
		if len(filter.GroupBy) == 0 {
			continue
		}
		key, labels, bucket := groupingKey(event, filter.GroupBy, location)
		acc := grouped[key]
		if acc == nil {
			acc = &reportAccumulator{sessions: map[string]struct{}{}}
			grouped[key] = acc
			groupLabels[key] = UsageRow{Dimensions: labels, Bucket: bucket}
		}
		if err := addMeasures(acc, event, cost); err != nil {
			return UsageReport{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return UsageReport{}, err
	}
	summary.SessionCount = int64(len(summary.sessions))
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	resultRows := make([]UsageRow, 0, len(keys))
	for _, key := range keys {
		acc := grouped[key]
		acc.SessionCount = int64(len(acc.sessions))
		row := groupLabels[key]
		row.UsageMeasures = acc.UsageMeasures
		resultRows = append(resultRows, row)
	}
	coverageRows := make([]PricingCoverageRow, 0, len(coverage))
	for _, row := range coverage {
		coverageRows = append(coverageRows, *row)
	}
	sort.Slice(coverageRows, func(i, j int) bool {
		if coverageRows[i].Provider != coverageRows[j].Provider {
			return coverageRows[i].Provider < coverageRows[j].Provider
		}
		return coverageRows[i].Model < coverageRows[j].Model
	})
	return UsageReport{From: filter.From, To: filter.To, Timezone: location.String(), Filters: filter, Summary: summary.UsageMeasures, Rows: resultRows, PricingCoverage: coverageRows}, nil
}

func scanReportEvent(rows *sql.Rows) (reportEvent, error) {
	var event reportEvent
	var occurred string
	var providerAmount sql.NullInt64
	var providerCurrency string
	var reportedFallback sql.NullInt64
	var billingContextJSON sql.NullString
	if err := rows.Scan(&occurred, &event.ProjectID, &event.SessionID, &event.Client, &event.Provider,
		&event.RequestedModel, &event.ResolvedModel, &event.Usage.InputTokens, &event.Usage.OutputTokens,
		&event.Usage.CacheReadTokens, &event.Usage.CacheWriteTokens, &event.Usage.ReasoningTokens,
		&providerAmount, &providerCurrency, &event.RequestedCostStatus, &reportedFallback,
		&event.ReportedCurrency, &billingContextJSON, &event.Usage.CacheWrite5mTokens, &event.Usage.CacheWrite1hTokens); err != nil {
		return reportEvent{}, err
	}
	if billingContextJSON.Valid && billingContextJSON.String != "" {
		if err := json.Unmarshal([]byte(billingContextJSON.String), &event.BillingContext); err != nil {
			return reportEvent{}, err
		}
	}
	parsed, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		return reportEvent{}, err
	}
	event.OccurredAt = parsed.UTC()
	if providerAmount.Valid {
		amount := providerAmount.Int64
		event.ProviderAmount = &amount
		event.ProviderCurrency = providerCurrency
	}
	if reportedFallback.Valid {
		amount := reportedFallback.Int64
		event.ReportedFallback = &amount
	}
	return event, nil
}

func addMeasures(acc *reportAccumulator, event reportEvent, cost eventCost) error {
	acc.RequestCount++
	if event.SessionID != "" {
		acc.sessions[event.SessionID] = struct{}{}
	}
	acc.InputTokens += event.Usage.InputTokens
	acc.CachedInputTokens += event.Usage.CacheReadTokens
	acc.CacheWriteInputTokens += event.Usage.CacheWriteTokens
	acc.OutputTokens += event.Usage.OutputTokens
	acc.ReasoningOutputTokens += event.Usage.ReasoningTokens
	acc.TotalTokens += event.Usage.InputTokens + event.Usage.OutputTokens
	if cost.Status == "calculated" {
		value, ok := checkedCostAdd(acc.CalculatedCostMicros, cost.AmountMicros)
		if !ok {
			return fmt.Errorf("estimated cost aggregation overflow")
		}
		acc.CalculatedCostMicros = value
		acc.CalculatedCurrency = mergeCurrency(acc.CalculatedCurrency, cost.Currency)
		acc.EstimatedCostRequests++
		mergePricingMetadata(acc, event, cost, estimateBasis(cost.PricingOrigin, false))
		acc.PricingVersion = mergeLabel(acc.PricingVersion, cost.PricingVersion)
		acc.PricingSource = mergeLabel(acc.PricingSource, pricingSourceLabel(cost))
		acc.PricingModelSelector = mergeLabel(acc.PricingModelSelector, cost.ModelSelector)
		acc.PricingEffectiveFrom = mergeLabel(acc.PricingEffectiveFrom, cost.EffectiveFrom)
		acc.PricingEffectiveTo = mergeLabel(acc.PricingEffectiveTo, cost.EffectiveTo)
	} else if cost.Status == "zero" {
		acc.KnownZeroCostRequests++
		acc.CalculatedCurrency = mergeCurrency(acc.CalculatedCurrency, cost.Currency)
		mergePricingMetadata(acc, event, cost, "known_zero")
		acc.PricingVersion = mergeLabel(acc.PricingVersion, cost.PricingVersion)
		acc.PricingSource = mergeLabel(acc.PricingSource, pricingSourceLabel(cost))
		acc.PricingModelSelector = mergeLabel(acc.PricingModelSelector, cost.ModelSelector)
		acc.PricingEffectiveFrom = mergeLabel(acc.PricingEffectiveFrom, cost.EffectiveFrom)
		acc.PricingEffectiveTo = mergeLabel(acc.PricingEffectiveTo, cost.EffectiveTo)
	} else if cost.Status == "unknown" {
		acc.UnknownCostRequests++
		acc.CostUnknownReason = mergeLabel(acc.CostUnknownReason, cost.Reason)
		var err error
		acc.CostUnknownReasons, err = incrementReasonCount(acc.CostUnknownReasons, cost.Reason)
		if err != nil {
			return err
		}
		if cost.Currency != "" && cost.Reason != "arithmetic_overflow" && cost.Reason != "currency_conflict" {
			acc.EstimatedCostRequests++
			value, ok := checkedCostAdd(acc.CalculatedCostMicros, cost.AmountMicros)
			if !ok {
				return fmt.Errorf("partial estimated cost aggregation overflow")
			}
			acc.CalculatedCostMicros = value
			acc.CalculatedCurrency = mergeCurrency(acc.CalculatedCurrency, cost.Currency)
			mergePricingMetadata(acc, event, cost, estimateBasis(cost.PricingOrigin, true))
			if cost.PricingVersion != "" {
				acc.PricingVersion = mergeLabel(acc.PricingVersion, cost.PricingVersion)
			}
			if pricingSourceLabel(cost) != "" {
				acc.PricingSource = mergeLabel(acc.PricingSource, pricingSourceLabel(cost))
			}
			acc.PricingModelSelector = mergeLabel(acc.PricingModelSelector, cost.ModelSelector)
			acc.PricingEffectiveFrom = mergeLabel(acc.PricingEffectiveFrom, cost.EffectiveFrom)
			acc.PricingEffectiveTo = mergeLabel(acc.PricingEffectiveTo, cost.EffectiveTo)
		} else {
			mergePricingMetadata(acc, event, cost, "unavailable")
		}
	}
	if event.ProviderAmount != nil {
		value, ok := checkedCostAdd(acc.ProviderReportedCostMicros, *event.ProviderAmount)
		if !ok {
			return fmt.Errorf("provider-reported cost aggregation overflow")
		}
		acc.ProviderReportedCostMicros = value
		acc.ProviderReportedCurrency = mergeCurrency(acc.ProviderReportedCurrency, event.ProviderCurrency)
	}
	return nil
}

func mergePricingMetadata(acc *reportAccumulator, event reportEvent, cost eventCost, basis string) {
	provider := cost.Provider
	if provider == "" {
		provider = event.Provider
	}
	acc.PricingProvider = mergeLabel(acc.PricingProvider, provider)
	acc.CalculationBasis = mergeLabel(acc.CalculationBasis, basis)
}

func estimateBasis(origin string, partial bool) string {
	prefix := ""
	if partial {
		prefix = "partial_"
	}
	switch origin {
	case "public_catalog":
		return prefix + "public_catalog_estimate"
	case "custom":
		return prefix + "custom_pricing_estimate"
	case "mixed":
		return prefix + "mixed_estimate"
	default:
		return "unavailable"
	}
}

func checkedCostAdd(current, next int64) (int64, bool) {
	if next > 0 && current > int64(^uint64(0)>>1)-next {
		return 0, false
	}
	if next < 0 && current < -int64(^uint64(0)>>1)-1-next {
		return 0, false
	}
	return current + next, true
}

func incrementReasonCount(counts CostReasonCounts, reason string) (CostReasonCounts, error) {
	if reason == "" {
		reason = "unspecified"
	}
	values := map[string]int64{}
	if counts != "" {
		if err := json.Unmarshal([]byte(counts), &values); err != nil {
			return "", fmt.Errorf("decode cost reason counts: %w", err)
		}
	}
	if values[reason] == int64(^uint64(0)>>1) {
		return "", fmt.Errorf("unknown cost reason count overflow")
	}
	values[reason]++
	encoded, err := json.Marshal(values)
	return CostReasonCounts(encoded), err
}

func pricingSourceLabel(cost eventCost) string {
	if cost.SourceURL != "" {
		return cost.SourceURL
	}
	return cost.Source
}

func mergeLabel(current, next string) string {
	if current == "" || current == next {
		return next
	}
	if next == "" {
		return current
	}
	if current == "mixed" {
		return current
	}
	return "mixed"
}

func mergeCurrency(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" || current == next {
		return current
	}
	return "mixed"
}

func groupingKey(event reportEvent, groups []string, location *time.Location) (string, map[string]string, string) {
	labels := map[string]string{}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		value := ""
		bucket := ""
		switch group {
		case GroupProject:
			value = event.ProjectID
		case GroupSession:
			value = event.SessionID
		case GroupClient:
			value = event.Client
		case GroupProvider:
			value = event.Provider
		case GroupModel:
			value = event.ResolvedModel
			if value == "" {
				value = event.RequestedModel
			}
		case GroupHour, GroupDay, GroupWeek, GroupMonth:
			local := event.OccurredAt.In(location)
			start := local
			switch group {
			case GroupHour:
				// Truncate operates on an absolute duration boundary, which can
				// start fractional-offset local hours at :30 or :45. Build the
				// bucket from local calendar components and preserve the event's
				// offset across DST folds.
				_, offset := local.Zone()
				start = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.FixedZone("local", offset))
			case GroupDay:
				start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
			case GroupWeek:
				start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
				weekday := int(start.Weekday())
				if weekday == 0 {
					weekday = 7
				}
				start = start.AddDate(0, 0, -(weekday - 1))
			case GroupMonth:
				start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
			}
			bucket = start.Format(time.RFC3339)
			value = bucket
		default:
			value = group
		}
		labels[group] = value
		parts = append(parts, group+"="+value)
		if bucket != "" {
			labels["bucket"] = bucket
		}
	}
	return strings.Join(parts, "\x00"), labels, labels["bucket"]
}

// WriteJSON writes the stable report representation.
func WriteJSON(w io.Writer, report UsageReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// WriteCSV writes one row per report group, or one summary row for an
// ungrouped report.
func WriteCSV(w io.Writer, report UsageReport) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"group", "bucket", "request_count", "session_count", "input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "calculated_cost_micros", "calculated_currency", "provider_reported_cost_micros", "unknown_cost_requests", "estimated_cost_requests", "known_zero_cost_requests", "unknown_cost_reason", "unknown_cost_reasons", "pricing_provider", "calculation_basis", "pricing_version", "pricing_source", "pricing_model_selector", "pricing_effective_from", "pricing_effective_to", "provider_reported_currency"}); err != nil {
		return err
	}
	rows := report.Rows
	if len(rows) == 0 {
		rows = []UsageRow{{UsageMeasures: report.Summary}}
	}
	for _, row := range rows {
		group := ""
		if len(row.Dimensions) > 0 {
			keys := make([]string, 0, len(row.Dimensions))
			for key := range row.Dimensions {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			values := make([]string, 0, len(keys))
			for _, key := range keys {
				values = append(values, key+"="+row.Dimensions[key])
			}
			group = strings.Join(values, ",")
		}
		measures := row.UsageMeasures
		if err := writer.Write([]string{group, row.Bucket, strconv.FormatInt(measures.RequestCount, 10), strconv.FormatInt(measures.SessionCount, 10), strconv.FormatInt(measures.InputTokens, 10), strconv.FormatInt(measures.CachedInputTokens, 10), strconv.FormatInt(measures.CacheWriteInputTokens, 10), strconv.FormatInt(measures.OutputTokens, 10), strconv.FormatInt(measures.ReasoningOutputTokens, 10), strconv.FormatInt(measures.TotalTokens, 10), strconv.FormatInt(measures.CalculatedCostMicros, 10), measures.CalculatedCurrency, strconv.FormatInt(measures.ProviderReportedCostMicros, 10), strconv.FormatInt(measures.UnknownCostRequests, 10), strconv.FormatInt(measures.EstimatedCostRequests, 10), strconv.FormatInt(measures.KnownZeroCostRequests, 10), measures.CostUnknownReason, string(measures.CostUnknownReasons), measures.PricingProvider, measures.CalculationBasis, measures.PricingVersion, measures.PricingSource, measures.PricingModelSelector, measures.PricingEffectiveFrom, measures.PricingEffectiveTo, measures.ProviderReportedCurrency}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// WriteTable writes a compact human-readable usage report using the
// complete tabular layout, independent of any terminal width. Use
// WriteTableWidth to adapt the layout to a known terminal width.
func WriteTable(w io.Writer, report UsageReport) error {
	return WriteTableWidth(w, report, 0)
}

// WriteTableWidth writes a human-readable usage report, adapting the layout
// to an available terminal width of width columns. A width of zero or less
// always renders the complete tabular layout; this is used for redirected
// output, where there is no terminal to adapt to, so output stays
// deterministic. Otherwise the layout degrades from the complete table to a
// compact multi-line-per-row layout to a vertical key/value layout, choosing
// the first tier that fits the given width without dropping any usage or
// cost information.
func WriteTableWidth(w io.Writer, report UsageReport, width int) error {
	return writeTableWidth(w, report, width, true)
}

// WriteTableWidthWithoutPricingCoverage writes the same human-readable usage
// table as WriteTableWidth without the optional pricing coverage explanation.
// It is intended for compact interactive views that offer pricing details on
// demand. Exported table rendering defaults remain unchanged.
func WriteTableWidthWithoutPricingCoverage(w io.Writer, report UsageReport, width int) error {
	return writeTableWidth(w, report, width, false)
}

func writeTableWidth(w io.Writer, report UsageReport, width int, includePricingCoverage bool) error {
	if len(report.Rows) == 0 && report.Summary.RequestCount == 0 {
		_, err := fmt.Fprintln(w, "NO USAGE DATA")
		return err
	}
	writeCoverage := func() error {
		if includePricingCoverage {
			return WriteHumanPricingCoverage(w, report.PricingCoverage, width)
		}
		return nil
	}
	dimCount, headings, rows := tableRows(report)

	full := renderFullTable(headings, rows)
	if width <= 0 || lineDisplayWidth(full) <= width {
		if _, err := w.Write(full); err != nil {
			return err
		}
		return writeCoverage()
	}

	compact := renderCompactTable(dimCount, headings, rows)
	if lineDisplayWidth(compact) <= width {
		if _, err := w.Write(compact); err != nil {
			return err
		}
		return writeCoverage()
	}

	if _, err := w.Write(renderVerticalTable(headings, rows)); err != nil {
		return err
	}
	return writeCoverage()
}

const maxHumanPricingCoverageRows = 8

// WriteHumanPricingCoverage writes a bounded, sanitized explanation of
// provider/model combinations whose estimates are unavailable.
func WriteHumanPricingCoverage(w io.Writer, rows []PricingCoverageRow, width int) error {
	if len(rows) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, ""); err != nil {
		return err
	}
	if err := writeWrappedHumanLine(w, "PRICING COVERAGE GAPS (unpriced requests)", width, ""); err != nil {
		return err
	}
	shown := len(rows)
	if shown > maxHumanPricingCoverageRows {
		shown = maxHumanPricingCoverageRows
	}
	for _, row := range rows[:shown] {
		provider, model := formatHumanDimension(row.Provider), formatHumanDimension(row.Model)
		if row.Provider == "" {
			provider = "(unknown provider)"
		}
		if row.Model == "" {
			model = "(unresolved model)"
		}
		reasons := make([]string, 0, len(row.Reasons))
		for reason := range row.Reasons {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			if err := writeWrappedHumanLine(w, fmt.Sprintf("%s / %s: %s requests; %s (%s)", provider, model, formatExactCount(row.Requests), humanPricingReason(reason), formatExactCount(row.Reasons[reason])), width, "  "); err != nil {
				return err
			}
			if reason == "billing_context_missing" && len(row.Categories) > 0 {
				categories := make([]string, 0, len(row.Categories))
				for category := range row.Categories {
					categories = append(categories, category)
				}
				sort.Strings(categories)
				for _, category := range categories {
					if err := writeWrappedHumanLine(w, fmt.Sprintf("Affected category: %s (%s)", category, formatExactCount(row.Categories[category])), width, "    "); err != nil {
						return err
					}
				}
			}
		}
		remediations := make([]string, 0, len(reasons))
		seenRemediations := make(map[string]struct{}, len(reasons))
		for _, reason := range reasons {
			remediation := humanPricingRemediation(reason)
			if remediation == "" {
				continue
			}
			if _, exists := seenRemediations[remediation]; exists {
				continue
			}
			seenRemediations[remediation] = struct{}{}
			remediations = append(remediations, remediation)
		}
		for _, remediation := range remediations {
			if err := writeWrappedHumanLine(w, "Next: "+remediation, width, "    "); err != nil {
				return err
			}
		}
	}
	if len(rows) > shown {
		if err := writeWrappedHumanLine(w, fmt.Sprintf("... and %s more provider/model combinations", formatExactCount(int64(len(rows)-shown))), width, "  "); err != nil {
			return err
		}
	}
	return nil
}

func humanPricingRemediation(reason string) string {
	switch reason {
	case "no_matching_rate":
		return "Review exact-model catalog coverage and add only an official-source, evidence-backed rate."
	case "outside_effective_range":
		return "Do not apply current prices retroactively; use a verified rate for the event dates or keep costs unknown."
	case "billing_context_missing":
		return "Check for a verified rate for the affected token category. If rate selection needs billing evidence absent from the source, keep the estimate unknown until that evidence is available."
	case "model_unresolved":
		return "Check model extraction with a sanitized fixture; do not guess a model alias."
	case "ambiguous_pricing_rule":
		return "Resolve overlapping rules before estimating; ambiguous matches remain unknown."
	case "currency_conflict":
		return "Resolve the conflicting rule currencies before estimating."
	case "arithmetic_overflow":
		return "Reduce the report range or investigate the out-of-range usage values."
	default:
		return "Keep unsupported or ambiguous usage unknown until its pricing evidence is verified."
	}
}

func writeWrappedHumanLine(w io.Writer, text string, width int, indent string) error {
	text = FormatHumanText(text)
	prefix := indent
	lineWidth := runewidth.StringWidth(prefix)
	if _, err := io.WriteString(w, prefix); err != nil {
		return err
	}
	for _, word := range strings.Fields(text) {
		wordWidth := runewidth.StringWidth(word)
		if width > 0 && lineWidth > runewidth.StringWidth(prefix) && lineWidth+1+wordWidth > width {
			if _, err := io.WriteString(w, "\n"+indent); err != nil {
				return err
			}
			lineWidth = runewidth.StringWidth(indent)
		} else if lineWidth > runewidth.StringWidth(prefix) {
			if _, err := io.WriteString(w, " "); err != nil {
				return err
			}
			lineWidth++
		}
		if width > 0 && wordWidth > width-lineWidth {
			for _, r := range word {
				runeWidth := runewidth.RuneWidth(r)
				if lineWidth > runewidth.StringWidth(indent) && lineWidth+runeWidth > width {
					if _, err := io.WriteString(w, "\n"+indent); err != nil {
						return err
					}
					lineWidth = runewidth.StringWidth(indent)
				}
				if _, err := fmt.Fprint(w, string(r)); err != nil {
					return err
				}
				lineWidth += runeWidth
			}
		} else {
			if _, err := io.WriteString(w, word); err != nil {
				return err
			}
			lineWidth += wordWidth
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// tableRows builds the heading row and formatted cell values shared by every
// table layout tier. dimCount is the number of leading grouping-dimension
// columns; the 10 fixed metric/status columns always follow them.
func tableRows(report UsageReport) (int, []string, [][]string) {
	groups := report.Filters.GroupBy
	headings := make([]string, 0, len(groups)+10)
	if len(groups) == 0 {
		headings = append(headings, "GROUP")
	} else {
		for _, group := range groups {
			headings = append(headings, strings.ToUpper(group))
		}
	}
	headings = append(headings, "REQUESTS", "SESSIONS", "INPUT", "CACHED", "OUTPUT", "REASONING", "TOTAL", "ESTIMATE", "REPORTED", "STATUS")
	dimCount := len(headings) - 10

	reportRows := report.Rows
	if len(reportRows) == 0 {
		reportRows = []UsageRow{{UsageMeasures: report.Summary}}
	}
	rows := make([][]string, 0, len(reportRows))
	for _, row := range reportRows {
		values := make([]string, 0, len(headings))
		if len(groups) == 0 {
			values = append(values, "TOTAL")
		} else {
			for _, group := range groups {
				values = append(values, formatHumanDimension(row.Dimensions[group]))
			}
		}
		cost, status := humanCostDisplay(row.UsageMeasures)
		values = append(values,
			formatExactCount(row.RequestCount),
			formatExactCount(row.SessionCount),
			FormatHumanTokens(row.InputTokens),
			FormatHumanTokens(row.CachedInputTokens),
			FormatHumanTokens(row.OutputTokens),
			FormatHumanTokens(row.ReasoningOutputTokens),
			FormatHumanTokens(row.TotalTokens),
			cost,
			reportedCostDisplay(row.UsageMeasures),
			status,
		)
		rows = append(rows, values)
	}
	return dimCount, headings, rows
}

// renderFullTable renders the complete tabular layout using terminal display
// cells for alignment. Unicode code-point counts are insufficient here:
// East Asian wide runes occupy two cells while combining marks occupy none.
func renderFullTable(headings []string, rows [][]string) []byte {
	widths := make([]int, len(headings))
	for index, heading := range headings {
		widths[index] = runewidth.StringWidth(heading)
	}
	for _, row := range rows {
		for index, value := range row {
			if width := runewidth.StringWidth(value); width > widths[index] {
				widths[index] = width
			}
		}
	}

	var buf bytes.Buffer
	writeRow := func(row []string) {
		for index, value := range row {
			buf.WriteString(value)
			if index < len(row)-1 {
				padding := widths[index] - runewidth.StringWidth(value) + 2
				buf.WriteString(strings.Repeat(" ", padding))
			}
		}
		buf.WriteByte('\n')
	}
	writeRow(headings)
	for _, row := range rows {
		writeRow(row)
	}
	return buf.Bytes()
}

// renderCompactTable renders two self-contained lines per row: dimensions
// plus request/session counts, then the remaining metric and cost/status
// fields indented on the next line. Rows are not aligned against each
// other, so this degrades gracefully for any grouping or value length.
func renderCompactTable(dimCount int, headings []string, rows [][]string) []byte {
	var buf bytes.Buffer
	splitAt := dimCount + 2
	for i, row := range rows {
		if i > 0 {
			buf.WriteByte('\n')
		}
		dims := make([]string, 0, dimCount)
		for index := 0; index < dimCount; index++ {
			dims = append(dims, strings.ToLower(headings[index])+"="+row[index])
		}
		firstLine := []string{strings.Join(dims, " / ")}
		for index := dimCount; index < splitAt; index++ {
			firstLine = append(firstLine, strings.ToLower(headings[index])+" "+row[index])
		}
		buf.WriteString(strings.Join(firstLine, "  "))
		buf.WriteByte('\n')

		secondLine := make([]string, 0, len(headings)-splitAt)
		for index := splitAt; index < len(headings); index++ {
			secondLine = append(secondLine, strings.ToLower(headings[index])+" "+row[index])
		}
		buf.WriteString("  " + strings.Join(secondLine, "  "))
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// renderVerticalTable renders one "LABEL: value" line per field, with a
// blank line between rows. This is the final fallback tier and always fits
// as long as individual field values fit the terminal.
func renderVerticalTable(headings []string, rows [][]string) []byte {
	labelWidth := 0
	for _, heading := range headings {
		if len(heading) > labelWidth {
			labelWidth = len(heading)
		}
	}
	var buf bytes.Buffer
	for i, row := range rows {
		if i > 0 {
			buf.WriteByte('\n')
		}
		for index, heading := range headings {
			fmt.Fprintf(&buf, "%-*s %s\n", labelWidth+1, heading+":", row[index])
		}
	}
	return buf.Bytes()
}

// lineDisplayWidth reports the widest line in data, measured in terminal
// display cells rather than Unicode code points, so East Asian wide
// characters (which occupy two cells) are not undercounted.
func lineDisplayWidth(data []byte) int {
	widest := 0
	for _, line := range strings.Split(string(data), "\n") {
		if width := runewidth.StringWidth(line); width > widest {
			widest = width
		}
	}
	return widest
}
