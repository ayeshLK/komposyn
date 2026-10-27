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
		claudeEvent.Counts.Scope != DiagnosticCountOperation || !diagnosticReferenceAvailable(claudeEvent.Subject) {
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
		!diagnosticReferenceAvailable(codexEvent.Subject) {
		t.Fatalf("Codex event = %+v", codexEvent)
	}
	assertDiagnosticPrivacy(t, codexEvent, diagnosticPrivateMarker, codexPath, "synthetic-project", "synthetic-session", "synthetic-thread", "synthetic-response")
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
