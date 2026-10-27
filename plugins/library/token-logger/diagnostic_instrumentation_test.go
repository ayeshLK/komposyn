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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const diagnosticPrivateMarker = "SYNTHETIC_PRIVATE_DIAGNOSTIC_MARKER"

func syntheticClaudeDiagnosticSource() string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"synthetic-response","sessionId":"synthetic-session","timestamp":"2026-10-16T12:00:00Z","cwd":"/synthetic/private/project","message":{"role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":2}}}` + "\n")
}

func syntheticCodexUnsupportedSource() string {
	return `{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"synthetic-session","project_id":"synthetic-project","model":"gpt-5.1-codex"}}` + "\n" +
		`{"type":"token_usage_record","timestamp":"2026-10-16T12:00:01Z","payload":{"thread_id":"synthetic-thread","session_id":"synthetic-session","response_id":"synthetic-response","usage":{"input_tokens":3,"output_tokens":2},"private":"` + diagnosticPrivateMarker + `"}}` + "\n"
}

func drainDiagnosticEvents(observer *DiagnosticObserver) []DiagnosticEvent {
	var events []DiagnosticEvent
	for {
		select {
		case event := <-observer.Events():
			events = append(events, event)
		default:
			return events
		}
	}
}

func diagnosticReferenceAvailable(subject DiagnosticSubject) bool {
	return subject.Availability == DiagnosticAvailabilityAvailable && subject.Reference != "" &&
		subject.IdentityScope == DiagnosticIdentityStoreHandle
}

func assertDiagnosticPrivacy(t *testing.T, event DiagnosticEvent, markers ...string) {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range markers {
		if marker != "" && strings.Contains(string(encoded), marker) {
			t.Fatalf("diagnostic event contains private marker %q: %s", marker, encoded)
		}
	}
}

func TestClaudeAndCodexImportsPublishSafeClassifiedFindings(t *testing.T) {
	root := t.TempDir()
	claudePath := filepath.Join(root, "claude-private.jsonl")
	claudeBytes := []byte(`{"type":"assistant","cwd":"/` + diagnosticPrivateMarker + `","message": }` + "\n" + syntheticClaudeDiagnosticSource())
	if err := os.WriteFile(claudePath, claudeBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	claudeObserver, _ := NewDiagnosticObserver(8)
	claudeStore, err := OpenWithObservation(filepath.Join(t.TempDir(), "claude.sqlite"), OpenOptions{}, claudeObserver)
	if err != nil {
		t.Fatal(err)
	}
	defer claudeStore.Close()
	claudeResult, err := (ClaudeAdapter{}).Import(claudePath, claudeStore)
	if err != nil || claudeResult.Receipt.Imported != 1 || claudeResult.Outcomes.MalformedRecord != 1 {
		t.Fatalf("Claude result=%+v err=%v", claudeResult, err)
	}
	claudeEvents := drainDiagnosticEvents(claudeObserver)
	if len(claudeEvents) != 1 {
		t.Fatalf("Claude findings = %+v; want one condition summary", claudeEvents)
	}
	claudeEvent := claudeEvents[0]
	if claudeEvent.Kind != DiagnosticEventConditionSummary || claudeEvent.Classification.Reason != DiagnosticReasonInvalidJSON ||
		claudeEvent.Classification.Adapter != DiagnosticAdapterClaude || claudeEvent.Counts.MalformedRecord != 1 ||
		claudeEvent.Counts.Scope != DiagnosticCountOperation || !diagnosticReferenceAvailable(claudeEvent.Subject) ||
		claudeEvent.Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable || claudeEvent.Position.ByteOffset.Value != 0 {
		t.Fatalf("Claude event = %+v", claudeEvent)
	}
	assertDiagnosticPrivacy(t, claudeEvent, diagnosticPrivateMarker, claudePath, "/synthetic/private/project", "synthetic-session", "synthetic-response")

	codexPath := filepath.Join(root, "codex-private.jsonl")
	if err := os.WriteFile(codexPath, []byte(syntheticCodexUnsupportedSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	codexObserver, _ := NewDiagnosticObserver(8)
	codexStore, err := OpenWithObservation(filepath.Join(t.TempDir(), "codex.sqlite"), OpenOptions{}, codexObserver)
	if err != nil {
		t.Fatal(err)
	}
	defer codexStore.Close()
	codexResult, err := (CodexAdapter{}).Import(codexPath, codexStore)
	if err != nil || codexResult.Receipt.Imported != 0 || codexResult.Outcomes.UnsupportedUsage != 1 {
		t.Fatalf("Codex result=%+v err=%v", codexResult, err)
	}
	codexEvents := drainDiagnosticEvents(codexObserver)
	if len(codexEvents) != 1 {
		t.Fatalf("Codex findings = %+v; want one condition summary", codexEvents)
	}
	codexEvent := codexEvents[0]
	if codexEvent.Kind != DiagnosticEventConditionSummary || codexEvent.Classification.Reason != DiagnosticReasonUnsupportedUsage ||
		codexEvent.Classification.Adapter != DiagnosticAdapterCodex || codexEvent.Counts.UnsupportedUsage != 1 ||
		!diagnosticReferenceAvailable(codexEvent.Subject) ||
		codexEvent.Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable ||
		codexEvent.Position.ByteOffset.Value != uint64(len(strings.Split(syntheticCodexUnsupportedSource(), "\n")[0])+1) {
		t.Fatalf("Codex event = %+v", codexEvent)
	}
	assertDiagnosticPrivacy(t, codexEvent, diagnosticPrivateMarker, codexPath, "synthetic-project", "synthetic-session", "synthetic-thread", "synthetic-response")
}

func TestPublicImportsPersistDetailedEvidenceWithoutObserver(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "details.sqlite")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	claudePath := filepath.Join(root, "private-claude.jsonl")
	codexPath := filepath.Join(root, "private-codex.jsonl")
	claude := []byte(`{"type":"assistant","cwd":"/` + diagnosticPrivateMarker + `","message": }` + "\n" + syntheticClaudeDiagnosticSource())
	codex := []byte(`{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"private-session","model":"gpt-5.1-codex"}}` + "\n" +
		`{"type":"token_usage_record","timestamp":"2026-10-16T12:00:01Z","payload":{"thread_id":"private-thread","session_id":"private-session","response_id":"private-response","usage": }}` + "\n")
	for path, data := range map[string][]byte{claudePath: claude, codexPath: codex} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (ClaudeAdapter{}).Import(claudePath, store); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(codexPath, store); err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(store, []string{codexPath}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(codexPath, store); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("persisted details=%d, want 2: page=%+v lost=%t", len(page.Items), page, store.diagnosticPersistenceLost.Load())
	}
	seen := map[DiagnosticAdapter]DiagnosticDetail{}
	for _, item := range page.Items {
		seen[item.Classification.Adapter] = item
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{diagnosticPrivateMarker, claudePath, codexPath, "private-session", "private-response", "private-thread"} {
			if strings.Contains(string(encoded), marker) {
				t.Fatalf("private marker %q leaked in %s", marker, encoded)
			}
		}
	}
	for _, adapter := range []DiagnosticAdapter{DiagnosticAdapterClaude, DiagnosticAdapterCodex} {
		item, ok := seen[adapter]
		if !ok {
			t.Fatalf("missing persisted %s parser finding: %+v", adapter, page.Items)
		}
		wantLifetime := "1"
		if adapter == DiagnosticAdapterCodex {
			wantLifetime = "3"
		}
		if item.Condition.State != DiagnosticConditionActive || item.Subject.Availability != DiagnosticAvailabilityAvailable ||
			item.Lifetime.Value != wantLifetime || len(item.Samples) == 0 || item.Samples[0].Generation.Availability != DiagnosticAvailabilityAvailable {
			t.Fatalf("unexpected persisted %s evidence: %+v", adapter, item)
		}
		if adapter == DiagnosticAdapterCodex && (item.LatestPoll.Count.Value != "1" || item.LatestPoll.ScopeID == "") {
			t.Fatalf("collector poll scope was not persisted without an observer: %+v", item.LatestPoll)
		}
		if got, err := store.InspectDiagnosticDetail(item.ProblemID); err != nil || got.ProblemID != item.ProblemID {
			t.Fatalf("inspect %s: %+v, %v", adapter, got, err)
		}
	}
	backupPath, err := createMigrationBackup(dbPath, filepath.Join(root, "restore.sqlite"), CurrentSchemaVersion, CurrentSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		copy, err := restored.InspectDiagnosticDetail(item.ProblemID)
		if err != nil || copy.Subject.Reference != item.Subject.Reference {
			_ = restored.Close()
			t.Fatalf("diagnostic identity changed after backup restore: detail=%+v err=%v", copy, err)
		}
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	codexProblem := seen[DiagnosticAdapterCodex]
	if err := store.ReviewDiagnosticDetail(DiagnosticReviewRequest{ProblemID: codexProblem.ProblemID,
		ExpectedEvidenceRevision: codexProblem.Review.EvidenceRevision, State: DiagnosticReviewReviewed}); err != nil {
		t.Fatal(err)
	}
	if _, err := (CodexAdapter{}).Import(codexPath, store); err != nil {
		t.Fatal(err)
	}
	recurred, err := store.InspectDiagnosticDetail(codexProblem.ProblemID)
	if err != nil || recurred.Lifetime.Value != "4" || recurred.Review.State != DiagnosticReviewReviewed || recurred.Review.PredatesEvidence {
		t.Fatalf("count-only recurrence changed reviewed state: detail=%+v err=%v", recurred, err)
	}
	claudeProblem := seen[DiagnosticAdapterClaude]
	if err := os.WriteFile(claudePath, []byte(syntheticClaudeDiagnosticSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (ClaudeAdapter{}).Import(claudePath, store); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.InspectDiagnosticDetail(claudeProblem.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Condition.State != DiagnosticConditionResolved || resolved.Condition.Reason != DiagnosticConditionSourceReplaced {
		t.Fatalf("complete replacement scan did not resolve with generation evidence: %+v", resolved.Condition)
	}
}

func TestPublicImportEvidenceSurvivesSaturatedUnreadObserver(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source-private.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"assistant","message": }`+"\n"+syntheticClaudeDiagnosticSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	observer, err := NewDiagnosticObserver(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := observer.publish(testDiagnosticEvent()); got != DiagnosticEmissionQueued {
		t.Fatalf("prefill disposition=%s", got)
	}
	store, err := OpenWithObservation(filepath.Join(root, "observed.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	if observer.Stats().Dropped == 0 {
		t.Fatal("expected parser finding to be dropped by the full mailbox")
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Classification.Adapter != DiagnosticAdapterClaude ||
		page.Items[0].LatestPoll.Count.Value != "1" || page.Items[0].LatestPoll.ScopeID == "" {
		t.Fatalf("persisted evidence depended on observer delivery: %+v", page.Items)
	}
}

func TestDiagnosticWriteFailureDoesNotFailCommittedImportAndSurvivesReopen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"assistant","message": }`+"\n"+syntheticClaudeDiagnosticSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "store.sqlite")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_detail BEFORE INSERT ON diagnostic_details BEGIN SELECT RAISE(FAIL, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	result, err := (ClaudeAdapter{}).Import(path, store)
	if err != nil || result.Receipt.Imported != 1 {
		t.Fatalf("committed import masked by detail persistence failure: %+v, %v", result, err)
	}
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("committed usage count=%d err=%v", count, err)
	}
	if !store.diagnosticPersistenceLost.Load() {
		t.Fatal("diagnostic persistence loss was not exposed in memory")
	}
	var persistenceAvailable int
	if err := store.db.QueryRow("SELECT persistence_available FROM diagnostic_detail_meta WHERE singleton=1").Scan(&persistenceAvailable); err != nil || persistenceAvailable != 0 {
		t.Fatalf("failure marker before reopen=%d err=%v", persistenceAvailable, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil || page.EvidenceAvailability != DiagnosticAvailabilityLost {
		t.Fatalf("diagnostic write loss not preserved across reopen: page=%+v err=%v", page, err)
	}
	if _, err := store.db.Exec("DROP TRIGGER reject_detail"); err != nil {
		t.Fatal(err)
	}
	result, err = (ClaudeAdapter{}).Import(path, store)
	if err != nil {
		t.Fatalf("idempotent retry failed after diagnostic persistence failure: %v", err)
	}
	if count, err := store.EventCount(); err != nil || count != 1 {
		t.Fatalf("retry changed committed accounting: %d, %v", count, err)
	}
}

func TestPublicImportFailurePersistsPathFreeDiscoveryTargetEvidence(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "private-missing-source.jsonl")
	store, err := Open(filepath.Join(root, "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := (ClaudeAdapter{}).Import(missing, store); err == nil {
		t.Fatal("missing provider source unexpectedly imported")
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Subject.Kind != DiagnosticSubjectDiscoveryTarget ||
		page.Items[0].Subject.Availability != DiagnosticAvailabilityAvailable || page.Items[0].Condition.State != DiagnosticConditionActive {
		t.Fatalf("missing-source evidence=%+v", page.Items)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), missing) {
		t.Fatalf("missing source path leaked: %s", encoded)
	}
}

func TestPendingReplacementTailCannotResolvePriorParserEvidence(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "source.jsonl")
			valid := syntheticClaudeDiagnosticSource()
			meta := ""
			if provider == "codex" {
				meta = `{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"session-private","project_id":"project-private","model":"gpt-5.1-codex"}}` + "\n"
				valid = `{"type":"token_usage_record","timestamp":"2026-10-16T12:00:01Z","payload":{"thread_id":"thread-private","session_id":"session-private","response_id":"response-private","usage":{"input_tokens":3,"output_tokens":2}}}` + "\n"
			}
			malformed := `{"type":"assistant","message": }` + "\n"
			if provider == "codex" {
				malformed = `{"type":"token_usage_record","timestamp":"2026-10-16T12:00:00Z","payload":{"thread_id":"thread-private","session_id":"session-private","response_id":"bad-private","usage": }}` + "\n"
			}
			if err := os.WriteFile(path, []byte(meta+malformed+valid), 0o600); err != nil {
				t.Fatal(err)
			}
			observer, err := NewDiagnosticObserver(1)
			if err != nil {
				t.Fatal(err)
			}
			store, err := OpenWithObservation(filepath.Join(root, "store.sqlite"), OpenOptions{}, observer)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			importSource := func() (ImportResult, error) {
				if provider == "claude" {
					return (ClaudeAdapter{}).Import(path, store)
				}
				return (CodexAdapter{}).Import(path, store)
			}
			initial, err := importSource()
			if err != nil || initial.Outcomes.MalformedRecord == 0 {
				t.Fatalf("initial import=%+v err=%v", initial, err)
			}
			page, err := store.ListDiagnosticDetails(DiagnosticQuery{Reason: DiagnosticReasonInvalidJSON})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("initial details=%+v err=%v", page, err)
			}
			problemID := page.Items[0].ProblemID

			validLine := strings.TrimSuffix(valid, "\n")
			cut := len(validLine) / 2
			pending := validLine[:cut]
			active, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || active.Condition.State != DiagnosticConditionActive {
				t.Fatalf("initial parser problem was not active: condition=%+v err=%v", active.Condition, err)
			}
			if err := os.WriteFile(path, []byte(meta+malformed+valid+pending), 0o600); err != nil {
				t.Fatal(err)
			}
			activeTail, err := importSource()
			if err != nil || activeTail.Outcomes.DeferredIncompleteTail == 0 {
				t.Fatalf("pending appended tail=%+v err=%v", activeTail, err)
			}
			activeUnknown, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || activeUnknown.Condition.State != DiagnosticConditionActive {
				t.Fatalf("positive malformed evidence was erased by an incomplete scan: condition=%+v err=%v", activeUnknown.Condition, err)
			}
			if err := os.WriteFile(path, []byte(meta+malformed+valid+pending+validLine[cut:]+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if completed, err := importSource(); err != nil || completed.Outcomes.Pending() {
				t.Fatalf("completed initial tail=%+v err=%v", completed, err)
			}
			activeAfterTail, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || activeAfterTail.Condition.State != DiagnosticConditionActive {
				t.Fatalf("positive historical record was not retained through tail completion: condition=%+v err=%v", activeAfterTail.Condition, err)
			}

			cleanReplacement := meta + valid
			if err := os.WriteFile(path, []byte(cleanReplacement), 0o600); err != nil {
				t.Fatal(err)
			}
			clean, err := importSource()
			if err != nil || clean.Outcomes.Incomplete() || clean.Outcomes.Pending() {
				t.Fatalf("complete replacement=%+v err=%v", clean, err)
			}
			resolved, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || resolved.Condition.State != DiagnosticConditionResolved {
				t.Fatalf("complete replacement did not resolve prior evidence: condition=%+v err=%v", resolved.Condition, err)
			}
			if err := store.ReviewDiagnosticDetail(DiagnosticReviewRequest{ProblemID: problemID,
				ExpectedEvidenceRevision: resolved.Review.EvidenceRevision, State: DiagnosticReviewReviewed}); err != nil {
				t.Fatalf("review resolved detail: %v", err)
			}
			resolved, err = store.InspectDiagnosticDetail(problemID)
			if err != nil || resolved.Review.State != DiagnosticReviewReviewed {
				t.Fatalf("review state was not committed: review=%+v err=%v", resolved.Review, err)
			}

			replacement := cleanReplacement + pending
			if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
				t.Fatal(err)
			}
			partial, err := importSource()
			if err != nil || partial.Outcomes.DeferredIncompleteTail == 0 {
				t.Fatalf("pending replacement=%+v err=%v", partial, err)
			}
			unknown, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || unknown.Condition.State != DiagnosticConditionUnknown ||
				unknown.Condition.Reason != DiagnosticConditionCoverageInsufficient ||
				unknown.Condition.Coverage != DiagnosticCoveragePartial ||
				!unknown.Condition.AsOf.After(resolved.Condition.AsOf) ||
				unknown.Condition.Availability != DiagnosticAvailabilityAvailable ||
				!unknown.LastPositiveAt.Equal(resolved.LastPositiveAt) ||
				unknown.Lifetime != resolved.Lifetime || unknown.LatestPoll != resolved.LatestPoll ||
				unknown.LatestObservedRun != resolved.LatestObservedRun ||
				unknown.Review.State != DiagnosticReviewReviewed || !unknown.Review.PredatesEvidence {
				t.Fatalf("pending tail did not replace resolved condition with scoped unknown: condition=%+v err=%v", unknown.Condition, err)
			}
			if err := store.ReviewDiagnosticDetail(DiagnosticReviewRequest{ProblemID: problemID,
				ExpectedEvidenceRevision: resolved.Review.EvidenceRevision, State: DiagnosticReviewSkipped}); !errors.Is(err, ErrDiagnosticReviewConflict) {
				t.Fatalf("stale review after condition change=%v; want conflict", err)
			}

			if err := os.WriteFile(path, []byte(replacement+validLine[cut:]+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			completed, err := importSource()
			if err != nil || completed.Outcomes.Pending() {
				t.Fatalf("completed tail=%+v err=%v", completed, err)
			}
			stillUnknown, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || stillUnknown.Condition.State != DiagnosticConditionUnknown {
				t.Fatalf("appended-tail scan changed condition without full coverage: condition=%+v err=%v", stillUnknown.Condition, err)
			}
			if _, err := store.db.Exec("UPDATE source_cursors SET snapshot_reusable=0"); err != nil {
				t.Fatal(err)
			}
			full, err := importSource()
			if err != nil || full.Outcomes.Incomplete() || full.Outcomes.Pending() {
				t.Fatalf("complete full scan=%+v err=%v", full, err)
			}
			resolvedAgain, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || resolvedAgain.Condition.State != DiagnosticConditionResolved ||
				(resolvedAgain.Condition.Reason != DiagnosticConditionCoveredAbsence && resolvedAgain.Condition.Reason != DiagnosticConditionSourceReplaced) {
				t.Fatalf("complete coverage did not resolve prior evidence: result=%+v condition=%+v err=%v", full, resolvedAgain.Condition, err)
			}
			if observer.Stats().Dropped == 0 {
				t.Fatal("observer mailbox should be saturated and unread while details persist")
			}
		})
	}
}

func TestCancelledKnownSourceCoverageMarksActiveProblemUnknown(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "source.jsonl")
			valid := syntheticClaudeDiagnosticSource()
			malformed := `{"type":"assistant","message": }` + "\n"
			if provider == "codex" {
				valid = `{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"session-private","project_id":"project-private","model":"gpt-5.1-codex"}}` + "\n" +
					`{"type":"token_usage_record","timestamp":"2026-10-16T12:00:01Z","payload":{"thread_id":"thread-private","session_id":"session-private","response_id":"response-private","usage":{"input_tokens":3,"output_tokens":2}}}` + "\n"
				malformed = `{"type":"token_usage_record","timestamp":"2026-10-16T12:00:00Z","payload":{"thread_id":"thread-private","session_id":"session-private","response_id":"bad-private","usage": }}` + "\n"
			}
			contents := []byte(malformed + valid)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(filepath.Join(root, "store.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if provider == "claude" {
				if _, err := (ClaudeAdapter{}).Import(path, store); err != nil {
					t.Fatal(err)
				}
			} else if _, err := (CodexAdapter{}).Import(path, store); err != nil {
				t.Fatal(err)
			}
			page, err := store.ListDiagnosticDetails(DiagnosticQuery{Reason: DiagnosticReasonInvalidJSON})
			if err != nil || len(page.Items) != 1 || page.Items[0].Condition.State != DiagnosticConditionActive {
				t.Fatalf("initial active problem=%+v err=%v", page, err)
			}
			problemID := page.Items[0].ProblemID
			collector, err := NewCollector(store, []string{path}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store.sourceReadHook = func(pass int, bytesRead int64) {
				if pass == 2 && bytesRead > 0 {
					cancel()
				}
			}
			if _, err := collector.PollOnceContext(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("poll error=%v; want cancellation during parser inspection", err)
			}
			detail, err := store.InspectDiagnosticDetail(problemID)
			if err != nil || detail.Condition.State != DiagnosticConditionUnknown ||
				detail.Condition.Coverage != DiagnosticCoverageCancelled ||
				detail.Condition.Reason != DiagnosticConditionCoverageInsufficient {
				t.Fatalf("cancelled known-source scan did not yield scoped unknown: condition=%+v err=%v", detail.Condition, err)
			}
		})
	}
}

func TestCollectorStorageFailurePersistsWithoutObserverDelivery(t *testing.T) {
	for _, observation := range []string{"disabled", "full_unread"} {
		t.Run(observation, func(t *testing.T) {
			var store *Store
			var observer *DiagnosticObserver
			var err error
			if observation == "full_unread" {
				observer, err = NewDiagnosticObserver(1)
				if err != nil {
					t.Fatal(err)
				}
				store, err = OpenWithObservation(filepath.Join(t.TempDir(), "store.sqlite"), OpenOptions{}, observer)
			} else {
				store, err = Open(filepath.Join(t.TempDir(), "store.sqlite"))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.AcquireCollectorLease("private-owner", 1, "v1", "test", time.Second, time.Minute); err != nil {
				t.Fatal(err)
			}
			if observer != nil && observer.publish(testDiagnosticEvent()) != DiagnosticEmissionQueued {
				t.Fatal("failed to fill observation mailbox")
			}
			if _, err := store.db.Exec(`CREATE TRIGGER reject_collector_heartbeat BEFORE UPDATE ON collector_runtime BEGIN SELECT RAISE(ABORT, 'synthetic private heartbeat failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := store.HeartbeatCollector("private-owner"); err == nil {
				t.Fatal("heartbeat unexpectedly succeeded")
			} else if failure, ok := FailureDetails(err); !ok || failure.Classification.Reason != DiagnosticReasonStorageFailure {
				t.Fatalf("heartbeat failure details=%+v ok=%t err=%v", failure, ok, err)
			}
			page, err := store.ListDiagnosticDetails(DiagnosticQuery{Reason: DiagnosticReasonStorageFailure})
			if err != nil || len(page.Items) != 1 || page.Items[0].Classification.Operation != DiagnosticOperationHeartbeat ||
				page.EvidenceAvailability != DiagnosticAvailabilityAvailable {
				t.Fatalf("persistent heartbeat failure details=%+v err=%v", page, err)
			}
			if observer != nil && observer.Stats().Dropped == 0 {
				t.Fatal("expected observer publication to be dropped")
			}
		})
	}
}

func TestOperationalFindingOrderReservationFailureMarksEvidenceLost(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "store.sqlite")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_evidence_order BEFORE UPDATE OF next_evidence_order ON diagnostic_detail_meta BEGIN SELECT RAISE(ABORT, 'synthetic evidence order failure'); END`); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "private-missing.jsonl")
	if _, err := (ClaudeAdapter{}).Import(missing, store); err == nil {
		t.Fatal("missing source unexpectedly imported")
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil || page.EvidenceAvailability != DiagnosticAvailabilityLost {
		t.Fatalf("order reservation failure was hidden: page=%+v err=%v", page, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	page, err = store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil || page.EvidenceAvailability != DiagnosticAvailabilityLost {
		t.Fatalf("reservation loss marker did not survive reopen: page=%+v err=%v", page, err)
	}
}

func BenchmarkDiagnosticEvidencePersistenceOverhead(b *testing.B) {
	for _, workload := range []struct{ name, contents string }{
		{"ordinary_unchanged_poll", syntheticClaudeDiagnosticSource()},
		{"repeated_parser_error", `{"type":"assistant","message": }` + "\n" + syntheticClaudeDiagnosticSource()},
	} {
		for _, persistence := range []struct {
			name    string
			enabled bool
		}{{"baseline", false}, {"persisted", true}} {
			b.Run(workload.name+"/"+persistence.name, func(b *testing.B) {
				root := b.TempDir()
				source := filepath.Join(root, "synthetic.jsonl")
				if err := os.WriteFile(source, []byte(workload.contents), 0o600); err != nil {
					b.Fatal(err)
				}
				dbPath := filepath.Join(root, "store.sqlite")
				store, err := Open(dbPath)
				if err != nil {
					b.Fatal(err)
				}
				defer store.Close()
				collector, err := NewCollector(store, []string{source}, time.Second)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := collector.PollOnce(); err != nil {
					b.Fatal(err)
				}
				store.diagnosticPersistenceReady.Store(persistence.enabled)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := collector.PollOnce(); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				info, err := os.Stat(dbPath)
				if err != nil {
					b.Fatal(err)
				}
				totalBytes := info.Size()
				if walInfo, statErr := os.Stat(dbPath + "-wal"); statErr == nil {
					totalBytes += walInfo.Size()
				}
				b.ReportMetric(float64(totalBytes), "sqlite-db-wal-bytes")
			})
		}
	}
}

func TestParserPositionsAreBoundedRepresentativeSamples(t *testing.T) {
	t.Run("Claude repeated malformed records retain first offset and mark truncation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "claude-malformed.jsonl")
		data := []byte("{\"type\":\"assistant\",\"message\": }\n{\"type\":\"assistant\",\"message\": }\n" + syntheticClaudeDiagnosticSource())
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		observer, _ := NewDiagnosticObserver(8)
		store, err := OpenWithObservation(filepath.Join(t.TempDir(), "claude.sqlite"), OpenOptions{}, observer)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		result, err := (ClaudeAdapter{}).Import(path, store)
		if err != nil || result.Outcomes.MalformedRecord != 2 {
			t.Fatalf("Claude import=%+v err=%v", result, err)
		}
		events := drainDiagnosticEvents(observer)
		if len(events) != 1 || events[0].Counts.MalformedRecord != 2 ||
			events[0].Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable ||
			events[0].Position.ByteOffset.Value != 0 || !events[0].Position.ByteOffset.Truncated || !events[0].Evidence.Truncated {
			t.Fatalf("Claude sampled position = %+v", events)
		}
	})

	t.Run("Codex repeated malformed records retain first suffix offset and mark truncation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "codex-malformed.jsonl")
		metadata := `{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"session","project_id":"project","model":"gpt-5.1-codex"}}` + "\n"
		data := []byte(metadata + "{\"type\":\n{\"type\":\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		observer, _ := NewDiagnosticObserver(8)
		store, err := OpenWithObservation(filepath.Join(t.TempDir(), "codex.sqlite"), OpenOptions{}, observer)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		result, err := (CodexAdapter{}).Import(path, store)
		if err != nil || result.Outcomes.MalformedRecord != 2 {
			t.Fatalf("Codex import=%+v err=%v", result, err)
		}
		events := drainDiagnosticEvents(observer)
		if len(events) != 1 || events[0].Counts.MalformedRecord != 2 ||
			events[0].Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable ||
			events[0].Position.ByteOffset.Value != uint64(len(metadata)) || !events[0].Position.ByteOffset.Truncated || !events[0].Evidence.Truncated {
			t.Fatalf("Codex sampled position = %+v", events)
		}
	})
}

func TestParserPositionFromCursorRetryIsRegeneratedWithoutDuplicateSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retry-source.jsonl")
	initial := []byte(syntheticClaudeDiagnosticSource())
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "retry.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	_ = drainDiagnosticEvents(observer)
	malformed := []byte("{\"type\":\"assistant\",\"message\": }\n")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(malformed); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var cursorID string
	if err := store.db.QueryRow(`SELECT source_id FROM source_cursors LIMIT 1`).Scan(&cursorID); err != nil {
		t.Fatal(err)
	}
	parserPasses := 0
	store.beforeSnapshotParse = func() { parserPasses++ }
	var once bool
	store.sourceReadHook = func(pass int, bytesRead int64) {
		if pass == 2 && bytesRead > 0 {
			if once {
				return
			}
			once = true
			if _, err := store.db.Exec(`UPDATE source_cursors SET last_provider_record_id = 'concurrent-cursor-change' WHERE source_id = ?`, cursorID); err != nil {
				t.Errorf("force cursor compare-and-swap retry: %v", err)
			}
		}
	}
	result, err := collector.PollOnce()
	if err != nil || result.Outcomes.MalformedRecord != 1 || parserPasses < 2 {
		t.Fatalf("retried import=%+v err=%v", result, err)
	}
	events := drainDiagnosticEvents(observer)
	var parserEvent *DiagnosticEvent
	for _, event := range events {
		if event.Kind == DiagnosticEventConditionSummary && event.Classification.Component == DiagnosticComponentParser {
			copy := event
			parserEvent = &copy
		}
	}
	if parserEvent == nil || parserEvent.Counts.MalformedRecord != 1 ||
		parserEvent.Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable ||
		parserEvent.Position.ByteOffset.Value != uint64(len(initial)) || parserEvent.Position.ByteOffset.Truncated {
		t.Fatalf("cursor retry retained duplicate or stale position samples: %+v", events)
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{Reason: DiagnosticReasonInvalidJSON})
	if err != nil || len(page.Items) != 1 || page.Items[0].Lifetime.Value != "1" || len(page.Items[0].Samples) != 1 ||
		page.Items[0].Samples[0].Position.ByteOffset.Value != uint64(len(initial)) {
		t.Fatalf("cursor retry persisted speculative/duplicate evidence: page=%+v err=%v", page, err)
	}
}

func TestParserPositionsUseAbsoluteOffsetsAfterCheckpointContinuation(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), provider+"-checkpoint.jsonl")
			initial := []byte(syntheticClaudeDiagnosticSource())
			if provider == "codex" {
				initial = []byte(`{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"session","project_id":"project","model":"gpt-5.1-codex"}}` + "\n")
			}
			if err := os.WriteFile(path, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			observer, _ := NewDiagnosticObserver(8)
			store, err := OpenWithObservation(filepath.Join(t.TempDir(), provider+".sqlite"), OpenOptions{}, observer)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			importProvider := func() (ImportResult, error) {
				if provider == "claude" {
					return (ClaudeAdapter{}).Import(path, store)
				}
				return (CodexAdapter{}).Import(path, store)
			}
			if _, err := importProvider(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(initial, []byte("{\"type\":\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := importProvider()
			if err != nil || result.Outcomes.MalformedRecord != 1 {
				t.Fatalf("continued import=%+v err=%v", result, err)
			}
			events := drainDiagnosticEvents(observer)
			if len(events) != 1 || events[0].Classification.Reason != DiagnosticReasonInvalidJSON ||
				events[0].Counts.MalformedRecord != 1 ||
				events[0].Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable ||
				events[0].Position.ByteOffset.Value != uint64(len(initial)) || events[0].Position.ByteOffset.Truncated {
				t.Fatalf("checkpoint continuation position=%+v", events)
			}
		})
	}
}

func TestPollParserPositionBecomesUnavailableAcrossSourceGenerations(t *testing.T) {
	observer, _ := NewDiagnosticObserver(4)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "generations.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pollID, err := newDiagnosticID()
	if err != nil {
		t.Fatalf("create poll ID: %v", err)
	}
	correlation := diagnosticCorrelation{pollID: pollID}
	accumulator := newDiagnosticPollAccumulator(store, correlation)
	classification := makeDiagnosticClassification(DiagnosticComponentParser, DiagnosticOperationParse,
		DiagnosticReasonInvalidJSON, DiagnosticAdapterClaude)
	subject := store.diagnosticSubject(DiagnosticSubjectSource, "existing-source-identity")
	for generation, offset := range map[uint64]uint64{3: 19, 4: 37} {
		position := DiagnosticPosition{ByteOffset: DiagnosticOptionalUint64{Value: offset, Availability: DiagnosticAvailabilityAvailable},
			LineNumber: unavailableMeasurement()}
		accumulator.addConditionWithPosition(classification, subject, OutcomeMalformedRecord, 1, position, generation, true)
	}
	accumulator.flush()
	events := drainDiagnosticEvents(observer)
	for _, event := range events {
		if event.Kind == DiagnosticEventConditionSummary {
			if event.Position.ByteOffset.Availability != DiagnosticAvailabilityNotCollected || !event.Evidence.Truncated ||
				event.Counts.MalformedRecord != 2 {
				t.Fatalf("cross-generation poll position was overstated: %+v", event)
			}
			return
		}
	}
	t.Fatalf("poll condition summary missing: %+v", events)
}

func TestExpectedProviderRecordsAndIncompleteTailDoNotBecomeFailures(t *testing.T) {
	root := t.TempDir()
	claudePath := filepath.Join(root, "claude-tail.jsonl")
	claudeData := syntheticClaudeDiagnosticSource() + `{"type":"assistant","message":` // valid pending tail
	if err := os.WriteFile(claudePath, []byte(claudeData), 0o600); err != nil {
		t.Fatal(err)
	}
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(root, "observed.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := (ClaudeAdapter{}).Import(claudePath, store)
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes.MalformedRecord != 0 || result.Outcomes.DeferredIncompleteTail == 0 {
		t.Fatalf("Claude tail result=%+v err=%v", result, err)
	}
	if events := drainDiagnosticEvents(observer); len(events) != 0 {
		t.Fatalf("pending incomplete tail emitted a failure: %+v", events)
	}

	codexPath := filepath.Join(root, "codex-metadata.jsonl")
	codexMetadata := `{"type":"session_meta","timestamp":"2026-10-16T12:00:00Z","payload":{"id":"session-private","project_id":"project-private","model":"gpt-5.1-codex"}}` + "\n"
	if err := os.WriteFile(codexPath, []byte(codexMetadata), 0o600); err != nil {
		t.Fatal(err)
	}
	codexResult, err := (CodexAdapter{}).Import(codexPath, store)
	if err != nil || codexResult.Outcomes.MalformedRecord != 0 || codexResult.Outcomes.UnsupportedUsage != 0 {
		t.Fatalf("Codex metadata result=%+v err=%v", codexResult, err)
	}
	if events := drainDiagnosticEvents(observer); len(events) != 0 {
		t.Fatalf("metadata-only Codex session emitted a failure: %+v", events)
	}
}

func TestSourceReferencesArePerStoreHandleAndDoNotCreateDiagnosticIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(path, []byte(syntheticClaudeDiagnosticSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(t.TempDir(), "malformed-"+diagnosticPrivateMarker+".jsonl")
	badAndGood := `{"type":"assistant","message": }` + "\n" + syntheticClaudeDiagnosticSource()
	if err := os.WriteFile(privatePath, []byte(badAndGood), 0o600); err != nil {
		t.Fatal(err)
	}
	observerA, _ := NewDiagnosticObserver(2)
	storeA, err := OpenWithObservation(filepath.Join(t.TempDir(), "handle-a.sqlite"), OpenOptions{}, observerA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (ClaudeAdapter{}).Import(privatePath, storeA); err != nil {
		t.Fatal(err)
	}
	eventA := drainDiagnosticEvents(observerA)[0]
	storeA.Close()
	observerB, _ := NewDiagnosticObserver(2)
	storeB, err := OpenWithObservation(filepath.Join(t.TempDir(), "handle-b.sqlite"), OpenOptions{}, observerB)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	if _, err := (ClaudeAdapter{}).Import(privatePath, storeB); err != nil {
		t.Fatal(err)
	}
	eventB := drainDiagnosticEvents(observerB)[0]
	if eventA.Subject.Reference == eventB.Subject.Reference {
		t.Fatal("same source received a comparable handle-scoped reference across Store handles")
	}
	target := filepath.Join(t.TempDir(), "discovery-target-"+diagnosticPrivateMarker+".jsonl")
	targetSubject := storeB.discoveryTargetDiagnosticSubject(target)
	if targetSubject.Kind != DiagnosticSubjectDiscoveryTarget || !diagnosticReferenceAvailable(targetSubject) {
		t.Fatalf("discovery target subject = %+v", targetSubject)
	}
	targetJSON, err := json.Marshal(targetSubject)
	if err != nil || strings.Contains(string(targetJSON), diagnosticPrivateMarker) || strings.Contains(string(targetJSON), target) {
		t.Fatalf("discovery target subject leaked path: %s (%v)", targetJSON, err)
	}
	assertDiagnosticPrivacy(t, eventA, privatePath, diagnosticPrivateMarker)
	assertDiagnosticPrivacy(t, eventB, privatePath, diagnosticPrivateMarker)

}

func TestCollectorPublishesPollAndConditionSummariesWithMatchingCorrelation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poll-source.jsonl")
	contents := `{"type":"assistant","message": }` + "\n" + syntheticClaudeDiagnosticSource()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "poll.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.PollOnce()
	if err != nil || result.Receipt.Imported != 1 || result.Outcomes.MalformedRecord != 1 {
		t.Fatalf("poll result=%+v err=%v", result, err)
	}
	events := drainDiagnosticEvents(observer)
	if len(events) != 2 {
		t.Fatalf("poll events = %+v; want condition and poll summary", events)
	}
	var condition, summary *DiagnosticEvent
	for index := range events {
		switch events[index].Kind {
		case DiagnosticEventConditionSummary:
			condition = &events[index]
		case DiagnosticEventPollSummary:
			summary = &events[index]
		}
	}
	if condition == nil || summary == nil || condition.PollID == "" || condition.PollID != summary.PollID ||
		condition.Counts.Scope != DiagnosticCountPoll || condition.Counts.ScopeID != condition.PollID ||
		condition.Counts.MalformedRecord != 1 || condition.Evidence.Scope != DiagnosticEvidencePoll ||
		condition.Position.ByteOffset.Availability != DiagnosticAvailabilityAvailable || condition.Position.ByteOffset.Value != 0 ||
		summary.Counts.Scope != DiagnosticCountPoll || summary.Counts.ScopeID != summary.PollID || summary.Counts.MalformedRecord != 1 {
		t.Fatalf("condition=%+v summary=%+v", condition, summary)
	}
	for _, event := range events {
		assertDiagnosticPrivacy(t, event, path, "/synthetic/private/project", "synthetic-response", "synthetic-session")
	}
}

func TestCollectorRunSummariesUseRunScopeAndMatchingRunID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-source.jsonl")
	contents := `{"type":"assistant","message": }` + "\n" + syntheticClaudeDiagnosticSource()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "run.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{path}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := collector.RunImmediate(ctx, func(ImportResult, error) { cancel() }); err != nil {
		t.Fatal(err)
	}
	events := drainDiagnosticEvents(observer)
	var runCondition *DiagnosticEvent
	for index := range events {
		if events[index].Kind == DiagnosticEventConditionSummary && events[index].Counts.Scope == DiagnosticCountRun {
			runCondition = &events[index]
		}
	}
	if runCondition == nil || runCondition.CollectorRunID == "" || runCondition.Counts.ScopeID != runCondition.CollectorRunID ||
		runCondition.Evidence.Scope != DiagnosticEvidenceRun || runCondition.Counts.MalformedRecord != 1 {
		t.Fatalf("run condition = %+v; all events=%+v", runCondition, events)
	}
}

func TestRunImmediateCooperativeStopDoesNotEmitFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.jsonl"), []byte(syntheticClaudeDiagnosticSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "run-stop.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{root}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	collector.beforeSource = func(string) { close(entered); <-release }
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() { resultCh <- collector.RunImmediate(ctx, nil) }()
	<-entered
	cancel()
	close(release)
	if err := <-resultCh; err != nil {
		t.Fatalf("cooperative RunImmediate returned %v", err)
	}
	for _, event := range drainDiagnosticEvents(observer) {
		if event.Kind == DiagnosticEventOperationFailed {
			t.Fatalf("cooperative stop emitted operation failure: %+v", event)
		}
	}
}

func TestRequiredRootFailureIsScopedButOptionalMissingRootIsExpected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "registered-root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "required.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registered, err := store.RegisterCollectorSource(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	collector, _ := NewCollectorWithRoots(store, []CollectorRoot{{Path: root, Required: true}}, time.Second)
	if _, err := collector.PollOnce(); err == nil {
		t.Fatal("required missing root did not fail")
	}
	events := drainDiagnosticEvents(observer)
	var rootEvent *DiagnosticEvent
	for index := range events {
		if events[index].Kind == DiagnosticEventOperationFailed {
			rootEvent = &events[index]
			break
		}
	}
	if rootEvent == nil || rootEvent.Classification.Reason != DiagnosticReasonSourceUnavailable ||
		rootEvent.Classification.Operation != DiagnosticOperationDiscover || rootEvent.Subject.Kind != DiagnosticSubjectRoot ||
		!diagnosticReferenceAvailable(rootEvent.Subject) || rootEvent.Subject.Reference == registered.Source.ID {
		t.Fatalf("required root event = %+v", rootEvent)
	}
	var failedSourceCondition, failedSourcePoll uint64
	for _, event := range events {
		if event.Kind == DiagnosticEventConditionSummary {
			failedSourceCondition += event.Counts.FailedSource
		}
		if event.Kind == DiagnosticEventPollSummary {
			failedSourcePoll += event.Counts.FailedSource
		}
	}
	if failedSourceCondition != 1 || failedSourcePoll != 1 {
		t.Fatalf("actual missing-root failure counts: condition=%d poll=%d events=%+v", failedSourceCondition, failedSourcePoll, events)
	}
	assertDiagnosticPrivacy(t, *rootEvent, root, registered.Source.ID)
	var identityCount, cursorCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM collector_source_identities`).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM source_cursors`).Scan(&cursorCount); err != nil {
		t.Fatal(err)
	}
	if identityCount != 0 || cursorCount != 0 {
		t.Fatalf("diagnostics allocated ingestion state: identities=%d cursors=%d", identityCount, cursorCount)
	}
	optionalObserver, _ := NewDiagnosticObserver(4)
	optionalStore, err := OpenWithObservation(filepath.Join(t.TempDir(), "optional.sqlite"), OpenOptions{}, optionalObserver)
	if err != nil {
		t.Fatal(err)
	}
	defer optionalStore.Close()
	optional, _ := NewCollectorWithRoots(optionalStore, []CollectorRoot{{Path: filepath.Join(t.TempDir(), "absent"), Required: false}}, time.Second)
	if result, err := optional.PollOnce(); err != nil || result.Outcomes.FailedSource != 0 {
		t.Fatalf("optional root result=%+v err=%v", result, err)
	}
	optionalEvents := drainDiagnosticEvents(optionalObserver)
	if len(optionalEvents) != 1 || optionalEvents[0].Kind != DiagnosticEventPollSummary {
		t.Fatalf("optional missing root emitted failure: %+v", optionalEvents)
	}
}

func TestExpiredPollDeadlineDoesNotFabricateFailedSourceOutcome(t *testing.T) {
	root := t.TempDir()
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "deadline.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollector(store, []string{root}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err := collector.PollOnceContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || result.Outcomes.FailedSource != 0 {
		t.Fatalf("expired poll result=%+v err=%v", result, err)
	}
	events := drainDiagnosticEvents(observer)
	var poll *DiagnosticEvent
	var conditionFailed uint64
	for index := range events {
		if events[index].Kind == DiagnosticEventPollSummary {
			poll = &events[index]
		}
		if events[index].Kind == DiagnosticEventConditionSummary {
			conditionFailed += events[index].Counts.FailedSource
		}
	}
	if poll == nil || poll.Counts.FailedSource != 0 || conditionFailed != 0 {
		t.Fatalf("deadline was counted as a failed source: poll=%+v conditionFailed=%d events=%+v", poll, conditionFailed, events)
	}
	foundDeadline := false
	for _, event := range events {
		if event.Kind == DiagnosticEventOperationFailed && event.Classification.Reason == DiagnosticReasonDeadlineExceeded {
			foundDeadline = true
		}
	}
	if !foundDeadline {
		t.Fatalf("deadline classification was lost: %+v", events)
	}
}

func TestUnregisteredRequiredRootsHaveDistinctHandleScopedReferences(t *testing.T) {
	rootDir := t.TempDir()
	first := filepath.Join(rootDir, "first-private-root")
	second := filepath.Join(rootDir, "second-private-root")
	observer, _ := NewDiagnosticObserver(32)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "unregistered-roots.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector, err := NewCollectorWithRoots(store, []CollectorRoot{{Path: first, Required: true}, {Path: second, Required: true}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := collector.PollOnce(); err == nil {
			t.Fatal("required missing roots did not fail")
		}
	}
	events := drainDiagnosticEvents(observer)
	var rootReferences []string
	for _, event := range events {
		if event.Kind != DiagnosticEventOperationFailed || event.Classification.Operation != DiagnosticOperationDiscover {
			continue
		}
		if event.Subject.Kind != DiagnosticSubjectRoot || !diagnosticReferenceAvailable(event.Subject) {
			t.Fatalf("unregistered root reference is unavailable: %+v", event)
		}
		rootReferences = append(rootReferences, event.Subject.Reference)
		assertDiagnosticPrivacy(t, event, first, second, "first-private-root", "second-private-root")
	}
	if len(rootReferences) != 4 || rootReferences[0] == rootReferences[1] ||
		rootReferences[0] != rootReferences[2] || rootReferences[1] != rootReferences[3] {
		t.Fatalf("unregistered roots were not distinct and stable within the Store handle: refs=%v events=%+v", rootReferences, events)
	}
	for table, query := range map[string]string{
		"collector_sources":           `SELECT count(*) FROM collector_sources`,
		"collector_source_identities": `SELECT count(*) FROM collector_source_identities`,
		"source_cursors":              `SELECT count(*) FROM source_cursors`,
	} {
		var count int
		if err := store.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("diagnostics changed %s: count=%d err=%v", table, count, err)
		}
	}
}

func TestImportReadAndPersistenceErrorsAreTypedAndDoNotLeak(t *testing.T) {
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(filepath.Join(t.TempDir(), "failures.sqlite"), OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	missing := filepath.Join(t.TempDir(), "missing-"+diagnosticPrivateMarker+".jsonl")
	_, readErr := (ClaudeAdapter{}).Import(missing, store)
	failure, ok := FailureDetails(readErr)
	if !ok || failure.Classification.Reason != DiagnosticReasonSourceUnavailable || failure.Classification.Operation != DiagnosticOperationRead {
		t.Fatalf("read failure details = %+v, %v (%v)", failure, ok, readErr)
	}
	readEvent := <-observer.Events()
	assertDiagnosticPrivacy(t, readEvent, missing, diagnosticPrivateMarker, readErr.Error())
	var identityCount, cursorCount int
	_ = store.db.QueryRow(`SELECT count(*) FROM collector_source_identities`).Scan(&identityCount)
	_ = store.db.QueryRow(`SELECT count(*) FROM source_cursors`).Scan(&cursorCount)
	if identityCount != 0 || cursorCount != 0 {
		t.Fatalf("failed read created ingestion state: identities=%d cursors=%d", identityCount, cursorCount)
	}

	path := filepath.Join(t.TempDir(), "persist-failure.jsonl")
	if err := os.WriteFile(path, []byte(syntheticClaudeDiagnosticSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_diagnostic_usage BEFORE INSERT ON usage_events
		BEGIN SELECT RAISE(ABORT, 'private persistence marker'); END`); err != nil {
		t.Fatal(err)
	}
	_, persistErr := (ClaudeAdapter{}).Import(path, store)
	failure, ok = FailureDetails(persistErr)
	if !ok || failure.Classification.Reason != DiagnosticReasonStorageFailure ||
		failure.Classification.Operation != DiagnosticOperationPersist || persistErr == nil {
		t.Fatalf("persistence failure details = %+v, %v (%v)", failure, ok, persistErr)
	}
	persistEvent := <-observer.Events()
	assertDiagnosticPrivacy(t, persistEvent, path, "private persistence marker", "usage_events", "synthetic-response", "synthetic-session")
	if got := observer.Stats(); got.Admitted != 2 || got.Dropped != 0 {
		t.Fatalf("observer stats = %+v", got)
	}
	_ = store.db.QueryRow(`SELECT count(*) FROM collector_source_identities`).Scan(&identityCount)
	_ = store.db.QueryRow(`SELECT count(*) FROM source_cursors`).Scan(&cursorCount)
	if identityCount != 0 || cursorCount != 0 {
		t.Fatalf("rolled-back import created ingestion state: identities=%d cursors=%d", identityCount, cursorCount)
	}
}

func TestExpectedCollectorOwnershipAndLeaseLossClassification(t *testing.T) {
	observer, _ := NewDiagnosticObserver(8)
	store, err := OpenWithObservation(":memory:", OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.AcquireCollectorLease("owner-private-id", 1, "v1", "test", time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireCollectorLease("second-private-id", 2, "v1", "test", time.Second, time.Minute); !errors.Is(err, ErrCollectorActive) {
		t.Fatalf("active lease error = %v", err)
	}
	if observer.Stats().Admitted != 0 {
		t.Fatalf("healthy existing ownership emitted failure: %+v", observer.Stats())
	}
	if _, err := store.HeartbeatCollector("unrelated-private-id"); !errors.Is(err, ErrCollectorLeaseLost) {
		t.Fatalf("lease loss error = %v", err)
	}
	event := <-observer.Events()
	if event.Classification.Reason != DiagnosticReasonLeaseLost || event.Classification.Operation != DiagnosticOperationHeartbeat {
		t.Fatalf("lease loss event = %+v", event)
	}
	assertDiagnosticPrivacy(t, event, "owner-private-id", "second-private-id", "unrelated-private-id")
}

func TestCollectorRuntimeFailuresAreClassifiedAtTheirPublicBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		trigger   string
		operation DiagnosticOperation
		invoke    func(*Store) error
		lease     bool
	}{
		{
			name: "acquire",
			trigger: `CREATE TRIGGER fail_diagnostic_acquire BEFORE INSERT ON collector_runs
				BEGIN SELECT RAISE(ABORT, 'synthetic private acquire failure'); END`,
			operation: DiagnosticOperationAcquireLease,
			invoke: func(store *Store) error {
				_, err := store.AcquireCollectorLease("synthetic-instance", 1, "v1", "test", time.Second, time.Minute)
				return err
			},
		},
		{
			name: "heartbeat",
			trigger: `CREATE TRIGGER fail_diagnostic_heartbeat BEFORE UPDATE ON collector_runtime
				BEGIN SELECT RAISE(ABORT, 'synthetic private heartbeat failure'); END`,
			operation: DiagnosticOperationHeartbeat, lease: true,
			invoke: func(store *Store) error { _, err := store.HeartbeatCollector("synthetic-instance"); return err },
		},
		{
			name: "stop request",
			trigger: `CREATE TRIGGER fail_diagnostic_stop BEFORE UPDATE ON collector_runtime
				BEGIN SELECT RAISE(ABORT, 'synthetic private stop failure'); END`,
			operation: DiagnosticOperationRequestStop, lease: true,
			invoke: func(store *Store) error { _, err := store.RequestCollectorStop(); return err },
		},
		{
			name: "release",
			trigger: `CREATE TRIGGER fail_diagnostic_release BEFORE UPDATE ON collector_runs
				BEGIN SELECT RAISE(ABORT, 'synthetic private release failure'); END`,
			operation: DiagnosticOperationReleaseLease, lease: true,
			invoke: func(store *Store) error { return store.ReleaseCollectorLease("synthetic-instance", "stopped", "") },
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			observer, _ := NewDiagnosticObserver(4)
			store, err := OpenWithObservation(filepath.Join(t.TempDir(), "collector.sqlite"), OpenOptions{}, observer)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if scenario.lease {
				if _, err := store.AcquireCollectorLease("synthetic-instance", 1, "v1", "test", time.Second, time.Minute); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.db.Exec(scenario.trigger); err != nil {
				t.Fatal(err)
			}
			callErr := scenario.invoke(store)
			failure, ok := FailureDetails(callErr)
			if callErr == nil || !ok || failure.Classification.Reason != DiagnosticReasonStorageFailure ||
				failure.Classification.Operation != scenario.operation {
				t.Fatalf("failure=%+v ok=%t err=%v", failure, ok, callErr)
			}
			event := <-observer.Events()
			if event.Kind != DiagnosticEventOperationFailed || event.Classification.Operation != scenario.operation ||
				event.Classification.Reason != DiagnosticReasonStorageFailure {
				t.Fatalf("event=%+v", event)
			}
			assertDiagnosticPrivacy(t, event, "synthetic private", "synthetic-instance", "collector_runtime", "collector_runs")
		})
	}
}

func TestCollectorCancellationIsExpectedAndDeadlineIsNotHidden(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		deadline   bool
		wantReason DiagnosticReason
	}{
		{name: "cooperative cancellation"},
		{name: "deadline", deadline: true, wantReason: DiagnosticReasonDeadlineExceeded},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			observer, _ := NewDiagnosticObserver(8)
			store, err := OpenWithObservation(":memory:", OpenOptions{}, observer)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "source.jsonl"), []byte(syntheticClaudeDiagnosticSource()), 0o600); err != nil {
				t.Fatal(err)
			}
			collector, _ := NewCollector(store, []string{root}, time.Second)
			entered := make(chan struct{})
			release := make(chan struct{})
			collector.beforeSource = func(string) {
				close(entered)
				<-release
			}
			var ctx context.Context
			var cancel context.CancelFunc
			if scenario.deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			resultCh := make(chan error, 1)
			go func() {
				_, pollErr := collector.PollOnceContext(ctx)
				resultCh <- pollErr
			}()
			<-entered
			if scenario.deadline {
				<-ctx.Done()
			} else {
				cancel()
			}
			close(release)
			pollErr := <-resultCh
			if scenario.deadline && !errors.Is(pollErr, context.DeadlineExceeded) {
				t.Fatalf("deadline error = %v", pollErr)
			}
			events := drainDiagnosticEvents(observer)
			var found bool
			for _, event := range events {
				if event.Kind == DiagnosticEventOperationFailed && event.Classification.Reason == scenario.wantReason {
					found = true
				}
				if !scenario.deadline && event.Kind == DiagnosticEventOperationFailed {
					t.Fatalf("cooperative cancellation emitted failure: %+v", event)
				}
			}
			if found != scenario.deadline {
				t.Fatalf("deadline failure observed=%t events=%+v", found, events)
			}
		})
	}
}

func TestCooperativeCancellationDoesNotHideJoinedStorageFailure(t *testing.T) {
	observer, _ := NewDiagnosticObserver(4)
	store, err := OpenWithObservation(":memory:", OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`CREATE TABLE diagnostic_join_failure (value INTEGER CHECK (value > 0))`); err != nil {
		t.Fatal(err)
	}
	_, storageErr := store.db.Exec(`INSERT INTO diagnostic_join_failure(value) VALUES (0)`)
	if storageErr == nil {
		t.Fatal("synthetic storage failure was not produced")
	}
	joined := errors.Join(context.Canceled, storageErr)
	classification := classifyCollectorFailure(joined, DiagnosticOperationHeartbeat)
	if diagnosticCancellationOnly(joined) || classification.Reason != DiagnosticReasonStorageFailure {
		t.Fatalf("joined failure was hidden/classified as stop: %+v", classification)
	}
	projected := store.observeFailure(context.Background(), joined, classification,
		DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected})
	if failure, ok := FailureDetails(projected); !ok || failure.Classification.Reason != DiagnosticReasonStorageFailure {
		t.Fatalf("joined failure details = %+v, %t", failure, ok)
	}
	event := <-observer.Events()
	if event.Classification.Reason != DiagnosticReasonStorageFailure || event.Classification.Operation != DiagnosticOperationHeartbeat {
		t.Fatalf("joined failure event = %+v", event)
	}
}

func TestDiagnosticPollAggregationIsBoundedAndReportsTruncation(t *testing.T) {
	observer, _ := NewDiagnosticObserver(MaxDiagnosticObserverCapacity)
	pollID := DiagnosticID("ffeeddccbbaa99887766554433221100")
	correlation := diagnosticCorrelation{pollID: pollID}
	accumulator := newDiagnosticPollAccumulator(&Store{diagnosticObserver: observer}, correlation)
	classification := makeDiagnosticClassification(DiagnosticComponentParser, DiagnosticOperationParse,
		DiagnosticReasonMalformedRecord, DiagnosticAdapterClaude)
	for index := 0; index < maxDiagnosticAggregationKeysPerPoll+1; index++ {
		subject := DiagnosticSubject{Kind: DiagnosticSubjectSource, Reference: fmt.Sprintf("%064x", index+1),
			IdentityScope: DiagnosticIdentityStoreHandle, Availability: DiagnosticAvailabilityAvailable}
		accumulator.addCounts(OutcomeCounts{MalformedRecord: 1})
		accumulator.addCondition(classification, subject, OutcomeMalformedRecord, 1)
	}
	if len(accumulator.conditions) != maxDiagnosticAggregationKeysPerPoll || !accumulator.truncated {
		t.Fatalf("aggregation size=%d truncated=%t counts=%+v", len(accumulator.conditions), accumulator.truncated, accumulator.counts)
	}
	accumulator.flush()
	if !accumulator.counts.Truncated {
		t.Fatalf("poll summary did not report aggregation truncation: %+v", accumulator.counts)
	}
	if got := observer.Stats(); got.Capacity != MaxDiagnosticObserverCapacity || got.Admitted > MaxDiagnosticObserverCapacity || got.Dropped == 0 {
		t.Fatalf("bounded observer stats = %+v", got)
	}
}

func BenchmarkDiagnosticObservationOverhead(b *testing.B) {
	for _, mode := range []struct {
		name     string
		observer bool
	}{
		{name: "disabled"},
		{name: "enabled", observer: true},
	} {
		for _, workload := range []string{"ordinary_collection", "repeated_source_error"} {
			b.Run(mode.name+"/"+workload, func(b *testing.B) {
				root := b.TempDir()
				path := filepath.Join(root, "source.jsonl")
				if workload == "ordinary_collection" {
					if err := os.WriteFile(path, []byte(syntheticClaudeDiagnosticSource()), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				var store *Store
				var observer *DiagnosticObserver
				var err error
				if mode.observer {
					observer, err = NewDiagnosticObserver(MaxDiagnosticObserverCapacity)
					if err == nil {
						store, err = OpenWithObservation(filepath.Join(root, "store.sqlite"), OpenOptions{}, observer)
					}
				} else {
					store, err = Open(filepath.Join(root, "store.sqlite"))
				}
				if err != nil {
					b.Fatal(err)
				}
				defer store.Close()
				var collector *Collector
				if workload == "ordinary_collection" {
					collector, err = NewCollector(store, []string{path}, time.Second)
					if err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				var initialStats DiagnosticObserverStats
				if observer != nil {
					initialStats = observer.Stats()
				}
				b.ResetTimer()
				for index := 0; index < b.N; index++ {
					if workload == "ordinary_collection" {
						if _, err := collector.PollOnce(); err != nil {
							b.Fatal(err)
						}
					} else {
						_, _ = (ClaudeAdapter{}).Import(filepath.Join(root, "missing.jsonl"), store)
					}
					if observer != nil {
						_ = drainDiagnosticEvents(observer)
					}
				}
				if observer != nil && b.N > 0 {
					finalStats := observer.Stats()
					b.ReportMetric(float64(finalStats.Admitted-initialStats.Admitted)/float64(b.N), "events/op")
					b.ReportMetric(float64(finalStats.Dropped-initialStats.Dropped)/float64(b.N), "drops/op")
				}
			})
		}
	}
}
