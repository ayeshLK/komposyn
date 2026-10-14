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
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	if len(report.Rows) != 1 || report.Rows[0].RequestCount != 1 {
		t.Fatalf("unexpected grouped rows: %+v", report.Rows)
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
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version IN (3, 4, 5)"); err != nil {
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
	if migrationCount != 5 || latestMigration != 5 {
		t.Fatalf("migration history = count %d, latest %d; want five migrations through version 5", migrationCount, latestMigration)
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
	}
	for _, tc := range cases {
		if got := formatHumanTokens(tc.value); got != tc.want {
			t.Errorf("formatHumanTokens(%d) = %q, want %q", tc.value, got, tc.want)
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
