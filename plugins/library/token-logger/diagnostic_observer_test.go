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
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sqlite3 "modernc.org/sqlite"
)

const sqlite3DriverName = "sqlite"

func testDiagnosticClassification() DiagnosticClassification {
	return makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationOpen,
		DiagnosticReasonFailureUnclassified, DiagnosticAdapterUnavailable)
}

func testDiagnosticEvent() DiagnosticEvent {
	return DiagnosticEvent{ContractVersion: DiagnosticContractVersion, CoreVersion: "0.0.0-dev",
		ObservedAt: time.Now().UTC(), Kind: DiagnosticEventOperationFailed,
		Classification: testDiagnosticClassification(), HasClassification: true,
		OperationID:         DiagnosticID("00112233445566778899aabbccddeeff"),
		Subject:             DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected},
		SourceSchemaVersion: unavailableMeasurement(), TargetSchemaVersion: unavailableMeasurement(),
		DurationMilliseconds: unavailableMeasurement(), RetryAttempt: unavailableMeasurement(), RetryCount: unavailableMeasurement(),
		Position: DiagnosticPosition{ByteOffset: unavailableMeasurement(), LineNumber: unavailableMeasurement()},
		Evidence: DiagnosticEvidence{Scope: DiagnosticEvidenceOperation, Availability: DiagnosticAvailabilityNotCollected,
			UnavailableReason: DiagnosticUnavailableNotCollected}, Counts: unavailableDiagnosticOutcomeCounts()}
}

func TestDiagnosticObserverCapacityAndLoss(t *testing.T) {
	observer, err := NewDiagnosticObserver(0)
	if err != nil {
		t.Fatal(err)
	}
	if observer.Stats().Capacity != DefaultDiagnosticObserverCapacity {
		t.Fatalf("default capacity = %d", observer.Stats().Capacity)
	}
	for _, capacity := range []int{1, MaxDiagnosticObserverCapacity} {
		if _, err := NewDiagnosticObserver(capacity); err != nil {
			t.Fatalf("capacity %d: %v", capacity, err)
		}
	}
	for _, capacity := range []int{-1, MaxDiagnosticObserverCapacity + 1} {
		if _, err := NewDiagnosticObserver(capacity); !errors.Is(err, ErrInvalidDiagnosticObserverCapacity) {
			t.Fatalf("capacity %d error = %v", capacity, err)
		}
	}

	observer, _ = NewDiagnosticObserver(1)
	event := testDiagnosticEvent()
	if got := observer.publish(event); got != DiagnosticEmissionQueued {
		t.Fatalf("first publish = %q", got)
	}
	if got := observer.publish(testDiagnosticEvent()); got != DiagnosticEmissionDropped {
		t.Fatalf("full publish = %q", got)
	}
	if got := observer.Stats(); got.Admitted != 1 || got.Dropped != 1 || got.AdmittedSaturated || got.DroppedSaturated {
		t.Fatalf("stats = %+v", got)
	}
	if len(observer.events) != 1 {
		t.Fatalf("stats drained queue; len = %d", len(observer.events))
	}
	if event := <-observer.Events(); event.OperationID != "00112233445566778899aabbccddeeff" {
		t.Fatalf("event = %+v", event)
	}
}

func TestDiagnosticObserverRejectsUncontrolledValues(t *testing.T) {
	observer, _ := NewDiagnosticObserver(32)
	base := testDiagnosticEvent()
	private := "/private/synthetic/path credential=synthetic-secret"
	tests := map[string]func(*DiagnosticEvent){
		"version":              func(e *DiagnosticEvent) { e.CoreVersion = private },
		"component":            func(e *DiagnosticEvent) { e.Classification.Component = DiagnosticComponent(private) },
		"operation":            func(e *DiagnosticEvent) { e.Classification.Operation = DiagnosticOperation(private) },
		"reason":               func(e *DiagnosticEvent) { e.Classification.Reason = DiagnosticReason(private) },
		"severity":             func(e *DiagnosticEvent) { e.Classification.Severity = DiagnosticSeverity(private) },
		"category":             func(e *DiagnosticEvent) { e.Classification.Category = DiagnosticCategory(private) },
		"adapter":              func(e *DiagnosticEvent) { e.Classification.Adapter = DiagnosticAdapter(private) },
		"retryability":         func(e *DiagnosticEvent) { e.Classification.Retryability = DiagnosticRetryability(private) },
		"impact":               func(e *DiagnosticEvent) { e.Classification.Impact = DiagnosticImpact(private) },
		"explanation":          func(e *DiagnosticEvent) { e.Classification.ExplanationCode = DiagnosticExplanationCode(private) },
		"remediation":          func(e *DiagnosticEvent) { e.Classification.RemediationCode = DiagnosticRemediationCode(private) },
		"operation id":         func(e *DiagnosticEvent) { e.OperationID = DiagnosticID(private) },
		"run id":               func(e *DiagnosticEvent) { e.CollectorRunID = DiagnosticID(private) },
		"poll id":              func(e *DiagnosticEvent) { e.PollID = DiagnosticID(private) },
		"subject kind":         func(e *DiagnosticEvent) { e.Subject.Kind = DiagnosticSubjectKind(private) },
		"subject availability": func(e *DiagnosticEvent) { e.Subject.Availability = DiagnosticAvailability(private) },
		"subject ref": func(e *DiagnosticEvent) {
			e.Subject = DiagnosticSubject{Kind: DiagnosticSubjectSource, Reference: private, IdentityScope: DiagnosticIdentityStoreHandle, Availability: DiagnosticAvailabilityAvailable}
		},
		"identity scope": func(e *DiagnosticEvent) {
			e.Subject = DiagnosticSubject{Kind: DiagnosticSubjectSource, Reference: fmt.Sprintf("%064x", 1), IdentityScope: DiagnosticIdentityScope(private), Availability: DiagnosticAvailabilityAvailable}
		},
		"classification when absent": func(e *DiagnosticEvent) {
			e.HasClassification = false
			e.Classification.Reason = DiagnosticReason(private)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			event := base
			mutate(&event)
			if err := event.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
				t.Fatalf("Validate error = %v", err)
			}
			if got := observer.publish(event); got != DiagnosticEmissionDropped {
				t.Fatalf("publish = %q", got)
			}
			encoded, _ := json.Marshal(event)
			if !containsBytes(encoded, []byte("synthetic-secret")) {
				t.Fatal("test marker not present in invalid fixture")
			}
		})
	}
	oversized := base
	oversized.CoreVersion = string(make([]byte, MaxDiagnosticItemBytes+1))
	if err := oversized.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("oversized event validation = %v", err)
	}
	if got := observer.publish(oversized); got != DiagnosticEmissionDropped {
		t.Fatalf("oversized event publish = %q", got)
	}
	if got := observer.Stats(); got.Admitted != 0 || got.Dropped != uint64(len(tests)+1) {
		t.Fatalf("stats = %+v", got)
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestDiagnosticObserverConcurrentProducersAndConsumer(t *testing.T) {
	const producers, eventsPerProducer = 8, 500
	observer, err := NewDiagnosticObserver(17)
	if err != nil {
		t.Fatal(err)
	}
	var consumed atomicCounterForTest
	stop := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for {
			select {
			case <-stop:
				return
			case event, open := <-observer.Events():
				if !open {
					return
				}
				if event.Validate() == nil {
					consumed.Add(1)
				}
			}
		}
	}()
	var workers sync.WaitGroup
	for producer := 0; producer < producers; producer++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < eventsPerProducer; i++ {
				observer.publish(testDiagnosticEvent())
			}
		}()
	}
	workers.Wait()
	close(stop)
	<-consumerDone
	stats := observer.Stats()
	if stats.Admitted+stats.Dropped != producers*eventsPerProducer {
		t.Fatalf("stats=%+v", stats)
	}
	if consumed.Load() > stats.Admitted {
		t.Fatalf("consumed=%d stats=%+v", consumed.Load(), stats)
	}
	select {
	case _, open := <-observer.Events():
		if !open {
			t.Fatal("core closed observer channel")
		}
	default:
	}
}

type atomicCounterForTest struct{ value atomic.Uint64 }

func (counter *atomicCounterForTest) Add(value uint64) { counter.value.Add(value) }
func (counter *atomicCounterForTest) Load() uint64     { return counter.value.Load() }

func TestDiagnosticObserverStatsSaturate(t *testing.T) {
	observer, _ := NewDiagnosticObserver(1)
	observer.stats.admitted.Store(math.MaxUint64)
	if observer.publish(testDiagnosticEvent()) != DiagnosticEmissionQueued {
		t.Fatal("event not queued")
	}
	stats := observer.Stats()
	if stats.Admitted != math.MaxUint64 || !stats.AdmittedSaturated {
		t.Fatalf("admitted stats = %+v", stats)
	}
	observer.stats.dropped.Store(math.MaxUint64)
	observer.publish(testDiagnosticEvent())
	stats = observer.Stats()
	if stats.Dropped != math.MaxUint64 || !stats.DroppedSaturated {
		t.Fatalf("dropped stats = %+v", stats)
	}
}

func TestDiagnosticObserverEventsAreDetachedValues(t *testing.T) {
	observer, _ := NewDiagnosticObserver(1)
	original := testDiagnosticEvent()
	if observer.publish(original) != DiagnosticEmissionQueued {
		t.Fatal("event was not queued")
	}
	original.CoreVersion = "/private/path"
	original.Classification.Reason = DiagnosticReason("credential=synthetic-secret")
	got := <-observer.Events()
	if got.CoreVersion != "0.0.0-dev" || got.Classification.Reason != DiagnosticReasonFailureUnclassified {
		t.Fatalf("queued value changed with publisher input: %+v", got)
	}
	got.CoreVersion = "changed by consumer"
	if again := got; again.CoreVersion != "changed by consumer" {
		t.Fatal("received value was not caller-owned")
	}
}

func TestFailureDetailsSafeProjectionAndWrapping(t *testing.T) {
	marker := "synthetic-private /users/example/project credential=synthetic-secret"
	cause := fmt.Errorf("%w: %s", os.ErrPermission, marker)
	classification := makeDiagnosticClassification(DiagnosticComponentStorage, DiagnosticOperationOpen,
		DiagnosticReasonPermissionDenied, DiagnosticAdapterUnavailable)
	failure := DiagnosticFailure{Classification: classification, OperationID: "00112233445566778899aabbccddeeff", EmissionDisposition: DiagnosticEmissionQueued}
	operational := newOperationalError(cause, failure)
	wrapped := fmt.Errorf("outer context: %w", operational)
	if !errors.Is(wrapped, os.ErrPermission) {
		t.Fatal("errors.Is did not preserve OS sentinel")
	}
	var pathError *os.PathError
	pathCause := &os.PathError{Op: "open", Path: "/synthetic/private/path", Err: os.ErrPermission}
	pathWrapped := newOperationalError(pathCause, failure)
	if !errors.As(pathWrapped, &pathError) {
		t.Fatal("errors.As did not preserve OS cause")
	}
	if got := wrapped.Error(); !containsBytes([]byte(got), []byte(marker)) {
		t.Fatalf("raw Error() no longer preserves local text: %q", got)
	}
	projected, ok := FailureDetails(wrapped)
	if !ok {
		t.Fatal("FailureDetails rejected classified error")
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if containsBytes(encoded, []byte("synthetic-private")) || containsBytes(encoded, []byte("synthetic-secret")) || containsBytes(encoded, []byte("/users")) {
		t.Fatalf("private marker escaped: %s", encoded)
	}
	if projected.OperationID != failure.OperationID || projected.EmissionDisposition != DiagnosticEmissionQueued || projected.Classification.Reason != DiagnosticReasonPermissionDenied {
		t.Fatalf("projection = %+v", projected)
	}
	if details, ok := FailureDetails(fmt.Errorf("unclassified: %w", os.ErrNotExist)); ok || details != (DiagnosticFailure{}) {
		t.Fatalf("unclassified projection = %+v, %v", details, ok)
	}
	if _, ok := FailureDetails(nil); ok {
		t.Fatal("nil error classified")
	}
	if rewrapped := newOperationalError(operational, failure); rewrapped != operational {
		t.Fatal("existing typed error was wrapped redundantly")
	}
}

func TestOperationalErrorPreservesSQLiteDriverCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readonly.sqlite")
	database, err := sql.Open(sqlite3DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE sample(value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := sql.Open(sqlite3DriverName, "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	_, cause := readOnly.Exec("CREATE TABLE forbidden(value INTEGER)")
	if cause == nil {
		t.Fatal("read-only database unexpectedly accepted a write")
	}
	failure := DiagnosticFailure{Classification: makeDiagnosticClassification(DiagnosticComponentStorage,
		DiagnosticOperationOpen, DiagnosticReasonStorageReadonly, DiagnosticAdapterUnavailable), EmissionDisposition: DiagnosticEmissionDisabled}
	operational := newOperationalError(cause, failure)
	var driverError *sqlite3.Error
	if !errors.As(operational, &driverError) || driverError.Code()&0xff != 8 {
		t.Fatalf("SQLite cause = %T %v", driverError, operational)
	}
	if classified := classifyOpenFailure(cause); classified.Reason != DiagnosticReasonStorageReadonly {
		t.Fatalf("classification = %+v", classified)
	}
}

func TestDiagnosticClassificationCatalogueAndUnavailableZero(t *testing.T) {
	zero := DiagnosticOptionalUint64{Availability: DiagnosticAvailabilityAvailable}
	if err := zero.Validate(); err != nil {
		t.Fatalf("available zero = %v", err)
	}
	unavailable := unavailableMeasurement()
	if err := unavailable.Validate(); err != nil || unavailable.Availability == zero.Availability {
		t.Fatalf("unavailable = %+v, %v", unavailable, err)
	}
	classification := makeDiagnosticClassification(DiagnosticComponentSchema, DiagnosticOperationOpen,
		DiagnosticReasonSchemaUnsupported, DiagnosticAdapterUnavailable)
	if err := classification.Validate(); err != nil {
		t.Fatal(err)
	}
	classification.Operation = DiagnosticOperationParse
	if err := classification.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("incompatible combination = %v", err)
	}
	if !validDiagnosticVersion("v0.7.1") || !validDiagnosticVersion("0.0.0-dev") || validDiagnosticVersion("1.0.0 token=secret") {
		t.Fatal("version grammar validation is incorrect")
	}
	counts := DiagnosticOutcomeCounts{Scope: DiagnosticCountLifetime, Availability: DiagnosticAvailabilityAvailable,
		FailedSource: math.MaxUint64 - 1}
	if err := counts.AddOutcome(OutcomeFailedSource); err != nil || counts.FailedSource != math.MaxUint64 || !counts.Saturated {
		t.Fatalf("saturating count = %+v, %v", counts, err)
	}
	before := counts
	if err := counts.AddOutcome(RecordOutcome("private arbitrary text")); !errors.Is(err, ErrInvalidDiagnostic) || counts != before {
		t.Fatalf("unsupported outcome changed counters: %+v, %v", counts, err)
	}
}

func TestDiagnosticPollSummaryHasExplicitCountScope(t *testing.T) {
	event := testDiagnosticEvent()
	event.Kind = DiagnosticEventPollSummary
	event.HasClassification = false
	event.Classification = DiagnosticClassification{}
	event.PollID = "ffeeddccbbaa99887766554433221100"
	event.Evidence = DiagnosticEvidence{Scope: DiagnosticEvidencePoll, Availability: DiagnosticAvailabilityAvailable}
	event.Counts = DiagnosticOutcomeCounts{Scope: DiagnosticCountPoll, ScopeID: event.PollID,
		Availability: DiagnosticAvailabilityAvailable, MalformedRecord: 0, Saturated: false}
	if err := event.Validate(); err != nil {
		t.Fatalf("poll event validation: %v", err)
	}
	event.Counts.MalformedRecord = 1
	if err := event.Validate(); err != nil {
		t.Fatalf("nonzero counter validation: %v", err)
	}
	event.Counts.Scope = DiagnosticCountRun
	if err := event.Validate(); !errors.Is(err, ErrInvalidDiagnostic) {
		t.Fatalf("mismatched scope validation: %v", err)
	}
}

func TestOpenWithObservationPreflightFailureIsSafeAndPreservesDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsupported.sqlite")
	database, err := sql.Open(sqlite3DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", CurrentSchemaVersion+1, "synthetic"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := sha256.Sum256(before)
	observer, _ := NewDiagnosticObserver(0)
	store, openErr := OpenWithObservation(path, OpenOptions{}, observer)
	if store != nil {
		_ = store.Close()
		t.Fatal("failed preflight returned a Store")
	}
	var unsupported *UnsupportedSchemaError
	if !errors.Is(openErr, ErrUnsupportedSchema) || !errors.As(openErr, &unsupported) {
		t.Fatalf("open error = %v", openErr)
	}
	if want := fmt.Sprintf("database schema version %d is newer than supported version %d", CurrentSchemaVersion+1, CurrentSchemaVersion); openErr.Error() != want {
		t.Fatalf("error text = %q; want %q", openErr, want)
	}
	var event DiagnosticEvent
	select {
	case event = <-observer.Events():
	default:
		t.Fatal("pre-open failure was not observed")
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("event invalid: %v", err)
	}
	if event.Classification.Reason != DiagnosticReasonSchemaUnsupported || event.SourceSchemaVersion.Value != uint64(CurrentSchemaVersion+1) || event.TargetSchemaVersion.Value != CurrentSchemaVersion {
		t.Fatalf("event = %+v", event)
	}
	failure, ok := FailureDetails(openErr)
	if !ok || failure.EmissionDisposition != DiagnosticEmissionQueued || failure.OperationID != event.OperationID {
		t.Fatalf("failure details = %+v, %v", failure, ok)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest := sha256.Sum256(after); beforeDigest != afterDigest {
		t.Fatal("preflight failure modified the original database")
	}
	if got := observer.Stats(); got.Admitted != 1 || got.Dropped != 0 {
		t.Fatalf("observer stats = %+v", got)
	}
}

func TestOpenWithObservationNilAndStoreObserverCompatibility(t *testing.T) {
	store, err := OpenWithObservation(":memory:", OpenOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.diagnosticObserver != nil {
		t.Fatal("nil observer was retained")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	observer, _ := NewDiagnosticObserver(1)
	store, err = OpenWithObservation(":memory:", OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if store.diagnosticObserver != observer {
		t.Fatal("successful observed store lost its observer")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenWithObservationDisabledProjectionAndExistingOpenCompatibility(t *testing.T) {
	_, observedErr := OpenWithObservation("", OpenOptions{}, nil)
	if observedErr == nil || observedErr.Error() != "database path is required" {
		t.Fatalf("observed open error = %v", observedErr)
	}
	details, ok := FailureDetails(observedErr)
	if !ok || details.EmissionDisposition != DiagnosticEmissionDisabled || details.Classification.Reason != DiagnosticReasonFailureUnclassified {
		t.Fatalf("disabled failure projection = %+v, %v", details, ok)
	}
	_, existingErr := OpenWithOptions("", OpenOptions{})
	if existingErr == nil || existingErr.Error() != observedErr.Error() {
		t.Fatalf("existing open error changed: %v", existingErr)
	}
	if _, ok := FailureDetails(existingErr); ok {
		t.Fatal("existing OpenWithOptions behavior was unexpectedly wrapped")
	}
}

func TestObservedOpenFailureDoesNotWaitForFullMailbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsupported.sqlite")
	database, err := sql.Open(sqlite3DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = database.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)")
	_, _ = database.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", CurrentSchemaVersion+1, "synthetic")
	_ = database.Close()
	observer, _ := NewDiagnosticObserver(1)
	observer.publish(testDiagnosticEvent())
	start := time.Now()
	_, err = OpenWithObservation(path, OpenOptions{}, observer)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("full observer delayed failed open: err=%v elapsed=%s", err, time.Since(start))
	}
	failure, ok := FailureDetails(err)
	if !ok || failure.EmissionDisposition != DiagnosticEmissionDropped {
		t.Fatalf("failure details = %+v, %v", failure, ok)
	}
}
