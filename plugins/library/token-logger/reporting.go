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
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
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
	ProjectID string         `json:"project_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Client    string         `json:"client,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	Model     string         `json:"model,omitempty"`
	From      time.Time      `json:"from"`
	To        time.Time      `json:"to"`
	GroupBy   []string       `json:"group_by,omitempty"`
	Timezone  *time.Location `json:"-"`
}

// UsageMeasures contains additive token and cost measures.
type UsageMeasures struct {
	RequestCount               int64  `json:"request_count"`
	SessionCount               int64  `json:"session_count"`
	InputTokens                int64  `json:"input_tokens"`
	CachedInputTokens          int64  `json:"cached_input_tokens"`
	CacheWriteInputTokens      int64  `json:"cache_write_input_tokens"`
	OutputTokens               int64  `json:"output_tokens"`
	ReasoningOutputTokens      int64  `json:"reasoning_output_tokens"`
	TotalTokens                int64  `json:"total_tokens"`
	CalculatedCostMicros       int64  `json:"calculated_cost_micros"`
	CalculatedCurrency         string `json:"calculated_currency,omitempty"`
	ProviderReportedCostMicros int64  `json:"provider_reported_cost_micros"`
	ProviderReportedCurrency   string `json:"provider_reported_currency,omitempty"`
	UnknownCostRequests        int64  `json:"unknown_cost_requests"`
}

// UsageRow is a grouped report row. Bucket is populated for time groups.
type UsageRow struct {
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Bucket     string            `json:"bucket,omitempty"`
	UsageMeasures
}

// UsageReport is the stable machine-readable report shape.
type UsageReport struct {
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	Timezone string        `json:"timezone"`
	Filters  UsageFilter   `json:"filters"`
	Summary  UsageMeasures `json:"summary"`
	Rows     []UsageRow    `json:"rows"`
}

type reportEvent struct {
	OccurredAt       time.Time
	ProjectID        string
	SessionID        string
	Client           string
	Provider         string
	RequestedModel   string
	ResolvedModel    string
	Usage            TokenUsage
	ProviderAmount   *int64
	ProviderCurrency string
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
	for _, group := range filter.GroupBy {
		switch group {
		case GroupHour, GroupDay, GroupWeek, GroupMonth, GroupProject, GroupSession, GroupClient, GroupProvider, GroupModel:
		default:
			return UsageReport{}, fmt.Errorf("unsupported group-by %q", group)
		}
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
		provider_cost_currency FROM usage_events WHERE occurred_at >= ? AND occurred_at < ?`
	args := []any{filter.From.Format(time.RFC3339Nano), filter.To.Format(time.RFC3339Nano)}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
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
	query += " ORDER BY occurred_at, event_id"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return UsageReport{}, fmt.Errorf("query usage events: %w", err)
	}
	defer rows.Close()
	summary := reportAccumulator{sessions: map[string]struct{}{}}
	grouped := map[string]*reportAccumulator{}
	groupLabels := map[string]UsageRow{}
	for rows.Next() {
		event, err := scanReportEvent(rows)
		if err != nil {
			return UsageReport{}, fmt.Errorf("scan usage event: %w", err)
		}
		cost := catalog.calculate(event)
		addMeasures(&summary, event, cost)
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
		addMeasures(acc, event, cost)
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
	return UsageReport{From: filter.From, To: filter.To, Timezone: location.String(), Filters: filter, Summary: summary.UsageMeasures, Rows: resultRows}, nil
}

func scanReportEvent(rows *sql.Rows) (reportEvent, error) {
	var event reportEvent
	var occurred string
	var providerAmount sql.NullInt64
	var providerCurrency string
	if err := rows.Scan(&occurred, &event.ProjectID, &event.SessionID, &event.Client, &event.Provider,
		&event.RequestedModel, &event.ResolvedModel, &event.Usage.InputTokens, &event.Usage.OutputTokens,
		&event.Usage.CacheReadTokens, &event.Usage.CacheWriteTokens, &event.Usage.ReasoningTokens,
		&providerAmount, &providerCurrency); err != nil {
		return reportEvent{}, err
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
	return event, nil
}

func addMeasures(acc *reportAccumulator, event reportEvent, cost eventCost) {
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
		acc.CalculatedCostMicros += cost.AmountMicros
		acc.CalculatedCurrency = mergeCurrency(acc.CalculatedCurrency, cost.Currency)
	} else if cost.Status == "unknown" {
		acc.UnknownCostRequests++
	}
	if event.ProviderAmount != nil {
		acc.ProviderReportedCostMicros += *event.ProviderAmount
		acc.ProviderReportedCurrency = mergeCurrency(acc.ProviderReportedCurrency, event.ProviderCurrency)
	}
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
	if err := writer.Write([]string{"group", "bucket", "request_count", "session_count", "input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "calculated_cost_micros", "calculated_currency", "provider_reported_cost_micros", "unknown_cost_requests"}); err != nil {
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
		if err := writer.Write([]string{group, row.Bucket, strconv.FormatInt(measures.RequestCount, 10), strconv.FormatInt(measures.SessionCount, 10), strconv.FormatInt(measures.InputTokens, 10), strconv.FormatInt(measures.CachedInputTokens, 10), strconv.FormatInt(measures.CacheWriteInputTokens, 10), strconv.FormatInt(measures.OutputTokens, 10), strconv.FormatInt(measures.ReasoningOutputTokens, 10), strconv.FormatInt(measures.TotalTokens, 10), strconv.FormatInt(measures.CalculatedCostMicros, 10), measures.CalculatedCurrency, strconv.FormatInt(measures.ProviderReportedCostMicros, 10), strconv.FormatInt(measures.UnknownCostRequests, 10)}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// WriteTable writes a compact human-readable usage report.
func WriteTable(w io.Writer, report UsageReport) error {
	writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "GROUP\tBUCKET\tREQUESTS\tINPUT\tCACHED\tOUTPUT\tREASONING\tTOTAL\tCOST\tSTATUS")
	rows := report.Rows
	if len(rows) == 0 {
		rows = []UsageRow{{UsageMeasures: report.Summary}}
	}
	for _, row := range rows {
		group := "total"
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
		status := "calculated"
		if row.UnknownCostRequests > 0 {
			status = "unknown"
		}
		cost := strconv.FormatInt(row.CalculatedCostMicros, 10)
		if row.CalculatedCurrency != "" {
			cost += " " + row.CalculatedCurrency
		}
		fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n", group, row.Bucket, row.RequestCount, row.InputTokens, row.CachedInputTokens, row.OutputTokens, row.ReasoningOutputTokens, row.TotalTokens, cost, status)
	}
	return writer.Flush()
}
