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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaudeImportAttributesAndNormalizesUsage(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join("testdata", "claude-code", "normal.jsonl")
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("import Claude fixture: %v", err)
	}
	if result.Receipt.Imported != 2 || result.Outcomes.IgnoredNonUsage != 2 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	var input, cached, cacheWrite, output int64
	var client, provider, project, session string
	err = store.db.QueryRow(`SELECT input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, client_name, provider_name, project_id, session_id FROM usage_events ORDER BY occurred_at`).Scan(&input, &cached, &cacheWrite, &output, &client, &provider, &project, &session)
	if err != nil {
		t.Fatal(err)
	}
	var aliasedProject, aliasedSession string
	if err := store.db.QueryRow("SELECT project_id FROM project_aliases WHERE scope = 'claude-code' AND alias = 'claude-project-fixture'").Scan(&aliasedProject); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT session_id FROM session_aliases WHERE scope = 'claude-code:claude-code' AND alias = 'claude-session-fixture'").Scan(&aliasedSession); err != nil {
		t.Fatal(err)
	}
	if input != 110 || cached != 20 || cacheWrite != 10 || output != 15 || client != "claude-code" || provider != "anthropic" || project != aliasedProject || session != aliasedSession {
		t.Fatalf("unexpected Claude event: input=%d cached=%d cacheWrite=%d output=%d client=%s provider=%s project=%s session=%s", input, cached, cacheWrite, output, client, provider, project, session)
	}
	second, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatal(err)
	}
	if second.Receipt != (Receipt{}) || second.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("unexpected re-import result: %+v", second)
	}
}

func TestClaudeImportPricesVerifiedContextAndCacheDurationSplits(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "priced-claude.jsonl")
	contents := `{"type":"user","sessionId":"session-priced","cwd":"/sanitized/project","project_id":"project-priced"}
{"type":"assistant","uuid":"response-priced","sessionId":"session-priced","timestamp":"2026-10-16T12:00:00Z","cwd":"/sanitized/project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":1000000,"cache_creation_input_tokens":100000,"cache_read_input_tokens":200000,"output_tokens":300000,"cache_creation":{"ephemeral_5m_input_tokens":60000,"ephemeral_1h_input_tokens":40000},"service_tier":"standard","inference_geo":"global","speed":"standard","fallback_credit":{"status":{"type":"redeemed"}},"output_tokens_details":{"thinking_tokens":100000}}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("Claude import = %+v, err = %v", result, err)
	}
	var input, write5m, write1h int64
	var billing string
	if err := store.db.QueryRow("SELECT input_tokens, cache_write_5m_tokens, cache_write_1h_tokens, billing_context_json FROM usage_events").Scan(&input, &write5m, &write1h, &billing); err != nil {
		t.Fatal(err)
	}
	if input != 1_300_000 || write5m != 60_000 || write1h != 40_000 {
		t.Fatalf("persisted input/cache split = %d/%d/%d, want 1300000/60000/40000", input, write5m, write1h)
	}
	var context BillingContext
	if err := json.Unmarshal([]byte(billing), &context); err != nil {
		t.Fatal(err)
	}
	if context.ServiceTier == nil || context.ServiceTier.Value != "standard" || context.Region == nil || context.Region.Value != "global" || context.Speed == nil || context.Speed.Value != "standard" {
		t.Fatalf("persisted billing evidence = %+v", context)
	}
	if strings.Contains(billing, "fallback_credit") {
		t.Fatalf("fallback credit was persisted in pricing context: %s", billing)
	}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	report, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.CalculatedCostMicros != 10_660_000 || report.Summary.UnknownCostRequests != 0 || report.Summary.PricingVersion != "anthropic-api-2026-10-16-v1" {
		t.Fatalf("priced Anthropic report = %+v, want 10660000 USD micros and catalog provenance", report.Summary)
	}
}

func TestClaudeAggregateCacheWriteWithoutDurationSplitStaysUnknown(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "missing-cache-split.jsonl")
	contents := `{"type":"user","sessionId":"session-missing-split","cwd":"/sanitized/project"}
{"type":"assistant","uuid":"response-missing-split","sessionId":"session-missing-split","timestamp":"2026-10-16T12:00:00Z","cwd":"/sanitized/project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":1000000,"cache_creation_input_tokens":100000,"cache_read_input_tokens":200000,"output_tokens":300000,"service_tier":"standard","inference_geo":"global","speed":"standard"}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, store); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("Claude import = %+v, err = %v", result, err)
	}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	report, err := store.Query(UsageFilter{From: at.Add(-time.Minute), To: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.UnknownCostRequests != 1 || report.Summary.CalculatedCostMicros != 10_040_000 {
		t.Fatalf("report with aggregate-only cache writes = %+v, want unknown with only the verified subtotal", report.Summary)
	}
}

func TestClaudeUnknownCacheCreationFieldPreventsReusableSnapshot(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "unknown-cache-field.jsonl")
	contents := `{"type":"user","sessionId":"session-unknown-cache","cwd":"/sanitized/project"}
{"type":"assistant","uuid":"response-unknown-cache","sessionId":"session-unknown-cache","timestamp":"2026-10-16T12:00:00Z","cwd":"/sanitized/project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0,"future_ttl_tokens":7}}}}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 0 || result.Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("unknown nested usage field import = %+v, err = %v", result, err)
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Cursor("claude-file-" + digest([]byte(canonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.SnapshotReusable {
		t.Fatal("snapshot with an unknown cache_creation field was marked reusable")
	}
}

func TestClaudePartialCacheDurationCountsAreValidated(t *testing.T) {
	tests := []struct {
		name, breakdown string
	}{
		{"negative five-minute count", `"ephemeral_5m_input_tokens":-1`},
		{"five-minute count exceeds aggregate", `"ephemeral_5m_input_tokens":3`},
		{"negative one-hour count", `"ephemeral_1h_input_tokens":-1`},
		{"one-hour count exceeds aggregate", `"ephemeral_1h_input_tokens":3`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "invalid-partial-split.jsonl")
			contents := fmt.Sprintf(`{"type":"assistant","uuid":"bad-split","sessionId":"bad-session","timestamp":"2026-10-16T12:00:00Z","cwd":"/sanitized/project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":0,"cache_creation_input_tokens":2,"cache_read_input_tokens":0,"output_tokens":1,"cache_creation":{%s}}}}`+"\n", test.breakdown)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (ClaudeAdapter{}).Import(path, store)
			if err != nil || result.Receipt != (Receipt{}) || result.Outcomes.MalformedRecord != 1 {
				t.Fatalf("invalid partial split result = %+v, err = %v", result, err)
			}
			canonicalPath, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			cursor, err := store.Cursor("claude-file-" + digest([]byte(canonicalPath)))
			if err != nil {
				t.Fatal(err)
			}
			if cursor.SnapshotReusable {
				t.Fatal("snapshot with malformed partial duration count was marked reusable")
			}
		})
	}
}

func TestClaudeImportAcceptsNewerUsageFields(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		fallbackCredit string
	}{
		{name: "absent", model: "claude-sonnet-5"},
		{name: "null", model: "claude-sonnet-5-5", fallbackCredit: `,"fallback_credit":null`},
		{name: "redeemed", model: "<synthetic>", fallbackCredit: `,"fallback_credit":{"status":{"type":"redeemed"}}`},
		{name: "not applied", model: "claude-sonnet-5", fallbackCredit: `,"fallback_credit":{"status":{"type":"not_applied","reason":"unsupported","remove_to_redeem":[]}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "newer-usage-fields.jsonl")
			record := claudeFallbackCreditRecord("u1", test.model, test.fallbackCredit, "")
			if err := os.WriteFile(path, record, 0o600); err != nil {
				t.Fatal(err)
			}

			result, err := (ClaudeAdapter{}).Import(path, store)
			if err != nil || result.Receipt.Imported != 1 || result.Outcomes != (OutcomeCounts{}) {
				t.Fatalf("newer usage fields result = %+v, err = %v", result, err)
			}
			var model string
			var input, cacheRead, cacheWrite, output, reasoning int64
			if err := store.db.QueryRow(`SELECT resolved_model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens FROM usage_events`).
				Scan(&model, &input, &cacheRead, &cacheWrite, &output, &reasoning); err != nil {
				t.Fatal(err)
			}
			if model != test.model || input != 13 || cacheRead != 1 || cacheWrite != 2 || output != 5 || reasoning != 0 {
				t.Fatalf("normalized event = model %q, input %d, cache read %d, cache write %d, output %d, reasoning %d", model, input, cacheRead, cacheWrite, output, reasoning)
			}
		})
	}
}

func TestClaudeFallbackCreditDoesNotRelaxUsageValidation(t *testing.T) {
	tests := []struct {
		name     string
		extra    string
		outcomes OutcomeCounts
	}{
		{name: "unknown field remains unsupported", extra: `,"future_token_units":1`, outcomes: OutcomeCounts{UnsupportedUsage: 1}},
		{name: "negative count remains malformed", extra: `,"input_tokens":-11`, outcomes: OutcomeCounts{MalformedRecord: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "invalid.jsonl")
			if err := os.WriteFile(path, claudeFallbackCreditRecord("u1", "claude-sonnet-5", `,"fallback_credit":{"status":{"type":"redeemed"}}`, test.extra), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := (ClaudeAdapter{}).Import(path, store)
			if err != nil || result.Receipt != (Receipt{}) || result.Outcomes != test.outcomes {
				t.Fatalf("result = %+v, err = %v; want outcomes %+v", result, err, test.outcomes)
			}
		})
	}
}

func TestClaudeFallbackCreditUpgradeRecoversUnchangedSource(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "upgrade.jsonl")
	contents := append(
		claudeFallbackCreditRecord("already-supported", "claude-sonnet-5", "", ""),
		claudeFallbackCreditRecord("previously-skipped", "claude-sonnet-5-5", `,"fallback_credit":{"status":{"type":"redeemed"}}`, "")...,
	)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	withoutClaudeFallbackCreditSupport(t, func() {
		legacy, err := (ClaudeAdapter{}).Import(path, store)
		if err != nil || legacy.Receipt.Imported != 1 || legacy.Outcomes.UnsupportedUsage != 1 {
			t.Fatalf("legacy import = %+v, err = %v", legacy, err)
		}
	})
	legacyCursor := claudeCursorForPath(t, store, path)
	if legacyCursor.SnapshotReusable {
		t.Fatalf("legacy cursor unexpectedly reusable: %+v", legacyCursor)
	}
	var legacyEventID, legacyProjectID, legacySessionID string
	if err := store.db.QueryRow(`SELECT event_id, project_id, session_id FROM usage_events WHERE source_record_id = 'already-supported'`).
		Scan(&legacyEventID, &legacyProjectID, &legacySessionID); err != nil {
		t.Fatal(err)
	}

	recovered, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || recovered.Receipt.Imported != 1 || recovered.Receipt.Duplicates != 1 || recovered.Outcomes != (OutcomeCounts{}) {
		t.Fatalf("recovery import = %+v, err = %v", recovered, err)
	}
	recoveredCursor := claudeCursorForPath(t, store, path)
	if recoveredCursor.SourceID != legacyCursor.SourceID || recoveredCursor.FileFingerprint != legacyCursor.FileFingerprint ||
		recoveredCursor.ByteOffset != legacyCursor.ByteOffset || recoveredCursor.SourceGeneration != legacyCursor.SourceGeneration || !recoveredCursor.SnapshotReusable {
		t.Fatalf("recovered cursor = %+v; legacy cursor = %+v", recoveredCursor, legacyCursor)
	}
	if count, err := store.EventCount(); err != nil || count != 2 {
		t.Fatalf("event count = %d, err = %v; want 2", count, err)
	}
	var recoveredEventID, recoveredProjectID, recoveredSessionID string
	if err := store.db.QueryRow(`SELECT event_id, project_id, session_id FROM usage_events WHERE source_record_id = 'already-supported'`).
		Scan(&recoveredEventID, &recoveredProjectID, &recoveredSessionID); err != nil {
		t.Fatal(err)
	}
	if recoveredEventID != legacyEventID || recoveredProjectID != legacyProjectID || recoveredSessionID != legacySessionID {
		t.Fatalf("existing event identity changed: before=(%q,%q,%q) after=(%q,%q,%q)", legacyEventID, legacyProjectID, legacySessionID, recoveredEventID, recoveredProjectID, recoveredSessionID)
	}
	var projectCount, sessionCount int
	if err := store.db.QueryRow(`SELECT COUNT(DISTINCT project_id), COUNT(DISTINCT session_id) FROM usage_events`).Scan(&projectCount, &sessionCount); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 || sessionCount != 1 {
		t.Fatalf("references changed during recovery: projects=%d sessions=%d", projectCount, sessionCount)
	}
	if unchanged, err := (ClaudeAdapter{}).Import(path, store); err != nil || unchanged.Receipt != (Receipt{}) || unchanged.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("post-recovery import = %+v, err = %v", unchanged, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(contents) {
		t.Fatalf("provider source changed during recovery: err=%v", err)
	}
}

func TestClaudeFallbackCreditUpgradeAffectedOnlyAndFutureUnknown(t *testing.T) {
	tests := []struct {
		name              string
		extra             string
		wantRecovered     int
		wantUnsupported   int
		wantReusableAfter bool
	}{
		{name: "affected only recovers", wantRecovered: 1, wantReusableAfter: true},
		{name: "independent unknown remains unsupported", extra: `,"future_token_units":1`, wantUnsupported: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "upgrade.jsonl")
			contents := claudeFallbackCreditRecord("affected", "claude-sonnet-5", `,"fallback_credit":null`, test.extra)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			withoutClaudeFallbackCreditSupport(t, func() {
				legacy, err := (ClaudeAdapter{}).Import(path, store)
				if err != nil || legacy.Receipt.Imported != 0 || legacy.Outcomes.UnsupportedUsage != 1 {
					t.Fatalf("legacy import = %+v, err = %v", legacy, err)
				}
			})
			recovered, err := (ClaudeAdapter{}).Import(path, store)
			if err != nil || recovered.Receipt.Imported != test.wantRecovered || recovered.Outcomes.UnsupportedUsage != test.wantUnsupported {
				t.Fatalf("recovery import = %+v, err = %v", recovered, err)
			}
			cursor := claudeCursorForPath(t, store, path)
			if cursor.SnapshotReusable != test.wantReusableAfter {
				t.Fatalf("cursor reusable = %v, want %v: %+v", cursor.SnapshotReusable, test.wantReusableAfter, cursor)
			}
			if test.wantReusableAfter {
				if unchanged, err := (ClaudeAdapter{}).Import(path, store); err != nil || unchanged.Outcomes.UnchangedSnapshot != 1 {
					t.Fatalf("unchanged import = %+v, err = %v", unchanged, err)
				}
			} else if repeated, err := (ClaudeAdapter{}).Import(path, store); err != nil || repeated.Outcomes.UnsupportedUsage != 1 || repeated.Outcomes.UnchangedSnapshot != 0 {
				t.Fatalf("unknown-field retry = %+v, err = %v", repeated, err)
			}
		})
	}
}

func claudeFallbackCreditRecord(id, model, fallbackCredit, extraUsage string) []byte {
	return []byte(fmt.Sprintf(`{"type":"assistant","uuid":%q,"sessionId":"fallback-session","cwd":"/sanitized/fallback-project","timestamp":"2026-10-11T00:00:00Z","message":{"role":"assistant","model":%q,"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":2},"server_tool_use":{"web_search_requests":1},"output_tokens_details":{"reasoning_tokens":3},"iterations":1,"inference_geo":"us","speed":"fast"%s%s}}}`+"\n", id, model, fallbackCredit, extraUsage))
}

func TestClaudeProjectNameMetadataPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		records    []claudeEnvelope
		wantName   string
		wantSource projectNameSource
		wantRoot   string
	}{
		{
			name:     "fallback upgrades when cwd arrives",
			records:  []claudeEnvelope{{}, {CWD: "/Users/ayesh/Studio Ledger"}},
			wantName: "Studio Ledger", wantSource: projectNameSourceCWD, wantRoot: "/Users/ayesh/Studio Ledger",
		},
		{
			name:     "provider name after cwd wins",
			records:  []claudeEnvelope{{CWD: "/Users/ayesh/Studio Ledger"}, {CWD: "/Users/ayesh/Studio Ledger", ProjectName: "Ledger Studio"}},
			wantName: "Ledger Studio", wantSource: projectNameSourceProvider, wantRoot: "/Users/ayesh/Studio Ledger",
		},
		{
			name:     "cwd after provider name only updates root",
			records:  []claudeEnvelope{{ProjectName: "Ledger Studio"}, {CWD: "/Users/ayesh/別の場所"}},
			wantName: "Ledger Studio", wantSource: projectNameSourceProvider, wantRoot: "/Users/ayesh/別の場所",
		},
		{
			name:    "no cwd remains provisional",
			records: []claudeEnvelope{{}}, wantName: "Unattributed", wantSource: projectNameSourceFallback,
		},
		{
			name:     "filesystem root does not become a project label",
			records:  []claudeEnvelope{{CWD: string(filepath.Separator)}},
			wantName: "Imported Claude Code project", wantSource: projectNameSourceFallback, wantRoot: string(filepath.Separator),
		},
		{
			name:    "blank explicit name is not evidence",
			records: []claudeEnvelope{{ProjectName: "   "}}, wantName: "Unattributed", wantSource: projectNameSourceFallback,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var session claudeSession
			for _, record := range test.records {
				session = updateClaudeSession(session, record, claudeMessage{}, "source")
			}
			if session.ProjectName != test.wantName || session.ProjectNameSource != test.wantSource || session.Root != test.wantRoot {
				t.Fatalf("session = %+v; want name %q source %q root %q", session, test.wantName, test.wantSource, test.wantRoot)
			}
		})
	}
}

func TestClaudeProjectNameSourcePersistsAcrossCheckpointRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "names.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	first := []byte("{\"type\":\"user\",\"sessionId\":\"name-session\",\"message\":{\"role\":\"user\"}}\n")
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, store); err != nil || result.Outcomes.IgnoredNonUsage != 1 {
		t.Fatalf("initial import = %+v, err = %v", result, err)
	}
	projectID, nameSource := claudeProjectForPath(t, store, path)
	if nameSource != string(projectNameSourceFallback) {
		t.Fatalf("initial project source = %q; want fallback", nameSource)
	}
	collectorSource, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := collectorSourceIDForRead(store, collectorSource, "claude")
	if err != nil {
		t.Fatal(err)
	}
	checkpointExpectation, err := parserCheckpointExpectation(store, sourceID)
	if err != nil || !checkpointExpectation.exists {
		t.Fatalf("initial checkpoint = %+v, err = %v", checkpointExpectation, err)
	}
	checkpoint, valid := decodeParserCheckpoint(checkpointExpectation.row)
	if !valid || checkpoint.State.Claude.Session.ProjectNameSource != projectNameSourceFallback {
		t.Fatalf("initial checkpoint = %+v, valid=%v; want fallback name source", checkpoint, valid)
	}
	if checkpoint.ParserRevision != claudeParserRevision {
		t.Fatalf("initial Claude checkpoint parser revision = %d, want %d", checkpoint.ParserRevision, claudeParserRevision)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	appendix := []byte("{\"type\":\"user\",\"sessionId\":\"name-session\",\"cwd\":\"/Users/ayesh/Studio Ledger\"}\n")
	if err := os.WriteFile(path, append(first, appendix...), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, store); err != nil || result.Outcomes.UnchangedSnapshot != 0 {
		t.Fatalf("append import = %+v, err = %v", result, err)
	}
	var gotID, name, root, source string
	if err := store.db.QueryRow(`SELECT id, display_name, root, display_name_source
		FROM projects WHERE id = ?`, projectID).Scan(&gotID, &name, &root, &source); err != nil {
		t.Fatal(err)
	}
	if gotID != projectID || name != "Studio Ledger" || root != "/Users/ayesh/Studio Ledger" || source != string(projectNameSourceCWD) {
		t.Fatalf("project after resumed append = id %q name %q root %q source %q", gotID, name, root, source)
	}
	if result, err := (ClaudeAdapter{}).Import(path, store); err != nil || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("unchanged reimport = %+v, err = %v", result, err)
	}
	if err := store.db.QueryRow("SELECT display_name FROM projects WHERE id = ?", projectID).Scan(&name); err != nil || name != "Studio Ledger" {
		t.Fatalf("project name after unchanged reimport = %q, err = %v", name, err)
	}
}

func TestClaudeRejectedCWDNameCannotBePromotedByLaterLocation(t *testing.T) {
	for _, fullReplay := range []bool{false, true} {
		name := "checkpoint append"
		if fullReplay {
			name = "verified full replay"
		}
		t.Run(name, func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			root := t.TempDir()
			conflict := filepath.Join(root, "OtherProject")
			accepted := filepath.Join(root, "ActualProject")
			for _, path := range []string{conflict, accepted} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpsertProject(Project{ID: "other-project", DisplayName: "Other", Root: conflict}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "claude.jsonl")
			initial := []byte(`{"type":"user","sessionId":"session-a","message":{"role":"user"}}` + "\n")
			if err := os.WriteFile(path, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			projectID, source := claudeProjectForPath(t, store, path)
			if source != string(projectNameSourceFallback) {
				t.Fatalf("initial project source=%q; want fallback", source)
			}
			contents := append(append([]byte(nil), initial...), claudeUserMetadataRecord(t, "session-a", conflict)...)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			var displayName, nameSource string
			if err := store.db.QueryRow("SELECT display_name, display_name_source FROM projects WHERE id = ?", projectID).Scan(&displayName, &nameSource); err != nil {
				t.Fatal(err)
			}
			if nameSource != string(projectNameSourceFallback) {
				t.Fatalf("conflicting CWD promoted project name early: name=%q source=%q", displayName, nameSource)
			}
			if fullReplay {
				if _, err := store.db.Exec("DELETE FROM parser_checkpoints"); err != nil {
					t.Fatal(err)
				}
			}
			contents = append(contents, claudeUserMetadataRecord(t, "session-a", accepted)...)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow("SELECT display_name, display_name_source FROM projects WHERE id = ?", projectID).Scan(&displayName, &nameSource); err != nil {
				t.Fatal(err)
			}
			if displayName != "ActualProject" || nameSource != string(projectNameSourceCWD) {
				t.Fatalf("final project name=%q source=%q; want ActualProject/cwd", displayName, nameSource)
			}
		})
	}
}

func claudeUserMetadataRecord(t *testing.T, sessionID, cwd string) []byte {
	t.Helper()
	data, err := json.Marshal(claudeEnvelope{Type: "user", SessionID: sessionID, CWD: cwd, Message: json.RawMessage(`{"role":"user"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestClaudeProjectNamePromotionRespectsLegacyUnknownAndExplicitNames(t *testing.T) {
	for _, test := range []struct {
		name           string
		prepare        func(*testing.T, *Store, string)
		wantName       string
		wantNameSource string
	}{
		{
			name: "legacy unknown remains unchanged", wantName: "Unattributed", wantNameSource: "unknown",
			prepare: func(t *testing.T, store *Store, projectID string) {
				if _, err := store.db.Exec("UPDATE projects SET display_name_source = 'unknown' WHERE id = ?", projectID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "explicit API label remains pinned", wantName: "Pinned by caller", wantNameSource: "explicit",
			prepare: func(t *testing.T, store *Store, projectID string) {
				if err := store.UpsertProject(Project{ID: projectID, DisplayName: "Pinned by caller"}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			path := filepath.Join(t.TempDir(), "claude.jsonl")
			first := []byte("{\"type\":\"user\",\"sessionId\":\"name-session\",\"message\":{\"role\":\"user\"}}\n")
			if err := os.WriteFile(path, first, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			projectID, _ := claudeProjectForPath(t, store, path)
			test.prepare(t, store, projectID)
			if result, err := (ClaudeAdapter{}).Import(path, store); err != nil || result.Outcomes.UnchangedSnapshot != 1 {
				t.Fatalf("unchanged import = %+v, err = %v", result, err)
			}
			appendix := []byte("{\"type\":\"user\",\"sessionId\":\"name-session\",\"cwd\":\"/Users/ayesh/Studio Ledger\",\"message\":{\"role\":\"user\"}}\n")
			if err := os.WriteFile(path, append(first, appendix...), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			var name, source string
			if err := store.db.QueryRow("SELECT display_name, display_name_source FROM projects WHERE id = ?", projectID).Scan(&name, &source); err != nil {
				t.Fatal(err)
			}
			if name != test.wantName || source != test.wantNameSource {
				t.Fatalf("name/source = %q/%q, want %q/%q", name, source, test.wantName, test.wantNameSource)
			}
		})
	}
}

func TestClaudeProjectNameDoesNotOscillateAcrossAliases(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	contents := []byte("{\"type\":\"user\",\"sessionId\":\"name-session\",\"cwd\":\"/Users/ayesh/Studio Ledger\",\"project_name\":\"Ledger Studio\",\"message\":{\"role\":\"user\"}}\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
		t.Fatal(err)
	}
	projectID, source := claudeProjectForPath(t, store, path)
	if source != string(projectNameSourceProvider) {
		t.Fatalf("Claude name source = %q, want provider", source)
	}
	if _, err := store.AppendWithReferences(nil, []Project{{ID: "codex-project", Root: "/Users/ayesh/Studio Ledger",
		DisplayName: "Different Codex label", SourceAdapter: "codex", ProviderAlias: "codex-project"}}, nil); err != nil {
		t.Fatal(err)
	}
	var reconciledID, name, nameSource string
	if err := store.db.QueryRow(`SELECT p.id, p.display_name, p.display_name_source FROM projects p
		JOIN project_aliases a ON a.project_id = p.id WHERE a.scope = 'codex' AND a.alias = 'codex-project'`).
		Scan(&reconciledID, &name, &nameSource); err != nil {
		t.Fatal(err)
	}
	if reconciledID != projectID || name != "Ledger Studio" || nameSource != string(projectNameSourceProvider) {
		t.Fatalf("cross-alias project = id %q name %q source %q; want %q / Ledger Studio / provider", reconciledID, name, nameSource, projectID)
	}
}

func claudeProjectForPath(t *testing.T, store *Store, path string) (string, string) {
	t.Helper()
	source, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	baseSourceID, err := establishedCollectorSourceID(store, source, "claude")
	if err != nil {
		t.Fatal(err)
	}
	alias := baseSourceID + "-project"
	var id, nameSource string
	if err := store.db.QueryRow(`SELECT p.id, p.display_name_source FROM projects p
		JOIN project_aliases a ON a.project_id = p.id WHERE a.scope = 'claude-code' AND a.alias = ?`, alias).Scan(&id, &nameSource); err != nil {
		t.Fatal(err)
	}
	return id, nameSource
}

func mustCanonicalPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := canonicalCollectorPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func withoutClaudeFallbackCreditSupport(t *testing.T, run func()) {
	t.Helper()
	_, existed := claudeUsageFields["fallback_credit"]
	delete(claudeUsageFields, "fallback_credit")
	defer func() {
		if existed {
			claudeUsageFields["fallback_credit"] = struct{}{}
		}
	}()
	run()
}

func claudeCursorForPath(t *testing.T, store *Store, path string) Cursor {
	t.Helper()
	source, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := establishedCollectorSourceID(store, source, "claude")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Cursor(sourceID)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func TestDetectClaudeAdapter(t *testing.T) {
	path := filepath.Join("testdata", "claude-code", "normal.jsonl")
	adapter, err := DetectAdapter(path)
	if err != nil || adapter != "claude" {
		t.Fatalf("adapter=%q err=%v", adapter, err)
	}
}

func TestClaudeImportDetectsReplacementWithProviderID(t *testing.T) {
	store := openTestStore(t)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	message := func(input int) string {
		return `{"type":"assistant","uuid":"provider-record-1","sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"input_tokens":` + string(rune('0'+input)) + `,"output_tokens":1}}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(message(1)), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || first.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", first, err)
	}
	initialInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(message(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, initialInfo.ModTime(), initialInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if replacementInfo.Size() != initialInfo.Size() || !replacementInfo.ModTime().Equal(initialInfo.ModTime()) {
		t.Fatalf("replacement metadata size=%d mtime=%s; initial size=%d mtime=%s", replacementInfo.Size(), replacementInfo.ModTime(), initialInfo.Size(), initialInfo.ModTime())
	}
	replacement, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || replacement.Receipt.Imported != 1 || replacement.Receipt.Duplicates != 0 {
		t.Fatalf("replacement import = %+v, err = %v; want one new event", replacement, err)
	}
	repeat, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || repeat.Receipt != (Receipt{}) || repeat.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("replacement re-import = %+v, err = %v; want unchanged snapshot", repeat, err)
	}
	if count, err := store.EventCount(); err != nil || count != 2 {
		t.Fatalf("event count = %d, err = %v; want 2", count, err)
	}
}

func TestConcurrentClaudeReplacementsRevalidateCursorBeforeCommit(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	stores := make([]*Store, 3)
	for index := range stores {
		store, err := Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
		defer store.Close()
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	message := func(input int) []byte {
		return []byte(fmt.Sprintf(`{"type":"assistant","uuid":"provider-record-1","sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", input))
	}
	if err := os.WriteFile(path, message(110), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", result, err)
	}

	type importResult struct {
		result ImportResult
		err    error
	}
	installPause := func(store *Store) (<-chan struct{}, chan<- struct{}) {
		reached := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		store.beforeAppend = func() {
			once.Do(func() {
				close(reached)
				<-release
			})
		}
		return reached, release
	}
	bReached, releaseB := installPause(stores[1])
	cReached, releaseC := installPause(stores[2])

	if err := os.WriteFile(path, message(111), 0o600); err != nil {
		t.Fatal(err)
	}
	bDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[1])
		bDone <- importResult{result: result, err: err}
	}()
	<-bReached

	if err := os.WriteFile(path, message(112), 0o600); err != nil {
		t.Fatal(err)
	}
	cDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[2])
		cDone <- importResult{result: result, err: err}
	}()
	<-cReached

	close(releaseB)
	b := <-bDone
	if b.err != nil || b.result.Receipt.Imported != 1 || b.result.Receipt.Duplicates != 0 {
		t.Fatalf("first replacement = %+v, err = %v", b.result, b.err)
	}
	close(releaseC)
	c := <-cDone
	if c.err != nil || c.result.Receipt.Imported != 1 || c.result.Receipt.Duplicates != 0 {
		t.Fatalf("stale replacement retry = %+v, err = %v", c.result, c.err)
	}

	if count, err := stores[0].EventCount(); err != nil || count != 3 {
		t.Fatalf("event count = %d, err = %v; want 3", count, err)
	}
	source, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	baseSourceID, err := establishedCollectorSourceID(stores[0], source, "claude")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := stores[0].Cursor(baseSourceID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.SourceGeneration != 2 || cursor.FileFingerprint != digest(message(112)) || !cursor.SnapshotReusable {
		t.Fatalf("latest cursor = %+v; want reusable generation 2 for final replacement", cursor)
	}
	var latestInput int64
	if err := stores[0].db.QueryRow(`SELECT input_tokens FROM usage_events WHERE source_id = ?`, generationSourceID(baseSourceID, 2)).Scan(&latestInput); err != nil {
		t.Fatal(err)
	}
	if latestInput != 112 {
		t.Fatalf("latest replacement input = %d, want 112", latestInput)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("final retry = %+v, err = %v", result, err)
	}
}

func TestConcurrentClaudeAppendDiscardsSupersededSnapshot(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	stores := make([]*Store, 3)
	for index := range stores {
		store, err := Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
		defer store.Close()
	}
	record := func(id string, input int, second int) []byte {
		return []byte(fmt.Sprintf(`{"type":"assistant","uuid":%q,"sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:%02dZ","message":{"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", id, second, input))
	}
	a := record("provider-record-1", 110, 1)
	b := append(append([]byte(nil), a...), record("provider-record-2", 20, 2)...)
	c := append(append([]byte(nil), b...), record("provider-record-3", 30, 3)...)
	path := filepath.Join(t.TempDir(), "append-only.jsonl")
	if err := os.WriteFile(path, a, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", result, err)
	}

	type importResult struct {
		result ImportResult
		err    error
	}
	installPause := func(store *Store) (<-chan struct{}, chan<- struct{}) {
		reached := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		store.beforeAppend = func() {
			once.Do(func() {
				close(reached)
				<-release
			})
		}
		return reached, release
	}
	bReached, releaseB := installPause(stores[1])
	cReached, releaseC := installPause(stores[2])

	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	bDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[1])
		bDone <- importResult{result: result, err: err}
	}()
	<-bReached
	if err := os.WriteFile(path, c, 0o600); err != nil {
		t.Fatal(err)
	}
	cDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[2])
		cDone <- importResult{result: result, err: err}
	}()
	<-cReached

	close(releaseC)
	newer := <-cDone
	if newer.err != nil || newer.result.Receipt.Imported != 2 {
		t.Fatalf("newer append = %+v, err = %v", newer.result, newer.err)
	}
	close(releaseB)
	older := <-bDone
	if older.err != nil || older.result.Receipt != (Receipt{}) || older.result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("superseded append = %+v, err = %v", older.result, older.err)
	}
	if count, err := stores[0].EventCount(); err != nil || count != 3 {
		t.Fatalf("event count = %d, err = %v; want 3", count, err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("current snapshot retry = %+v, err = %v", result, err)
	}
}

func TestClaudeImportBoundsCursorConflictRetries(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	primary, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	competitor, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer competitor.Close()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	message := func(input int) []byte {
		return []byte(fmt.Sprintf(`{"type":"assistant","uuid":"provider-record-1","sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:00Z","message":{"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", input))
	}
	if err := os.WriteFile(path, message(110), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, primary); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", result, err)
	}
	source, err := collectorSourceForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	baseSourceID, err := establishedCollectorSourceID(primary, source, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, message(111), 0o600); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	primary.beforeAppend = func() {
		attempts++
		cursor, cursorErr := competitor.Cursor(baseSourceID)
		if cursorErr != nil {
			t.Errorf("read competing cursor: %v", cursorErr)
			return
		}
		cursor.FileFingerprint = fmt.Sprintf("competing-fingerprint-%d", attempts)
		cursor.SourceGeneration++
		cursor.SnapshotReusable = false
		if _, appendErr := competitor.AppendWithCursor(nil, cursor); appendErr != nil {
			t.Errorf("advance competing cursor: %v", appendErr)
		}
	}
	result, err := (ClaudeAdapter{}).Import(path, primary)
	if !errors.Is(err, errSourceCursorRetryExhausted) {
		t.Fatalf("bounded import result = %+v, err = %v; want retry exhaustion", result, err)
	}
	if attempts != sourceCursorRetryLimit {
		t.Fatalf("append attempts = %d, want %d", attempts, sourceCursorRetryLimit)
	}
	if count, countErr := primary.EventCount(); countErr != nil || count != 1 {
		t.Fatalf("event count after exhausted retries = %d, err = %v; want 1", count, countErr)
	}
}

func TestClaudeImportCapturesCursorBeforeAuthoritativeSnapshot(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	stores := make([]*Store, 3)
	for index := range stores {
		store, err := Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
		defer store.Close()
	}
	record := func(id string, input int, second int) []byte {
		return []byte(fmt.Sprintf(`{"type":"assistant","uuid":%q,"sessionId":"session-1","cwd":"/tmp/project","timestamp":"2026-10-11T00:00:%02dZ","message":{"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", id, second, input))
	}
	a := record("provider-record-1", 10, 1)
	b := append(append([]byte(nil), a...), record("provider-record-2", 20, 2)...)
	c := append(append([]byte(nil), b...), record("provider-record-3", 30, 3)...)
	path := filepath.Join(t.TempDir(), "append-only.jsonl")
	if err := os.WriteFile(path, a, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[0]); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial import = %+v, err = %v", result, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	stores[1].beforeSnapshotRead = func() {
		once.Do(func() {
			close(reached)
			<-release
		})
	}
	type importResult struct {
		result ImportResult
		err    error
	}
	olderDone := make(chan importResult, 1)
	go func() {
		result, err := (ClaudeAdapter{}).Import(path, stores[1])
		olderDone <- importResult{result: result, err: err}
	}()
	<-reached
	if err := os.WriteFile(path, c, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(path, stores[2]); err != nil || result.Receipt.Imported != 2 {
		t.Fatalf("newer import = %+v, err = %v", result, err)
	}
	close(release)
	older := <-olderDone
	if older.err != nil || older.result.Receipt != (Receipt{}) || older.result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("delayed import = %+v, err = %v", older.result, older.err)
	}
	if count, err := stores[0].EventCount(); err != nil || count != 3 {
		t.Fatalf("event count = %d, err = %v; want 3", count, err)
	}
}

func TestClaudeConflictRefreshRebindsRetargetedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("source symlink scheduling requires symlink privileges")
	}
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	stores := make([]*Store, 3)
	for index := range stores {
		store, err := Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
		defer store.Close()
	}
	record := func(id, session string, input int, second int) []byte {
		return []byte(fmt.Sprintf(`{"type":"assistant","uuid":%q,"sessionId":%q,"cwd":"/tmp/project","timestamp":"2026-10-11T00:00:%02dZ","message":{"model":"claude-test","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n", id, session, second, input))
	}
	a1 := record("a-record-1", "a-session", 10, 1)
	a2 := append(append([]byte(nil), a1...), record("a-record-2", "a-session", 20, 2)...)
	b := record("b-record-1", "b-session", 30, 3)
	directory := t.TempDir()
	aPath := filepath.Join(directory, "a.jsonl")
	bPath := filepath.Join(directory, "b.jsonl")
	link := filepath.Join(directory, "current.jsonl")
	if err := os.WriteFile(aPath, a1, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aPath, link); err != nil {
		t.Fatal(err)
	}
	if result, err := (ClaudeAdapter{}).Import(link, stores[0]); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial A import = %+v, err = %v", result, err)
	}
	if result, err := (ClaudeAdapter{}).Import(bPath, stores[0]); err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("initial B import = %+v, err = %v", result, err)
	}
	if err := os.WriteFile(aPath, a2, 0o600); err != nil {
		t.Fatal(err)
	}
	var hookErr error
	var once sync.Once
	stores[1].beforeAppend = func() {
		once.Do(func() {
			if result, err := (ClaudeAdapter{}).Import(aPath, stores[2]); err != nil || result.Receipt.Imported != 1 {
				hookErr = fmt.Errorf("competing A import = %+v, err = %v", result, err)
				return
			}
			if err := os.Remove(link); err != nil {
				hookErr = err
				return
			}
			hookErr = os.Symlink(bPath, link)
		})
	}
	result, err := (ClaudeAdapter{}).Import(link, stores[1])
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("retargeted retry = %+v, err = %v", result, err)
	}
	if count, err := stores[0].EventCount(); err != nil || count != 3 {
		t.Fatalf("event count = %d, err = %v; want 3", count, err)
	}
	if result, err := (ClaudeAdapter{}).Import(aPath, stores[0]); err != nil || result.Receipt != (Receipt{}) || result.Outcomes.UnchangedSnapshot != 1 {
		t.Fatalf("final A import = %+v, err = %v", result, err)
	}
}
