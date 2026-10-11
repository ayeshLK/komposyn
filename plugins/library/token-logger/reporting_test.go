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
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version = 3"); err != nil {
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
