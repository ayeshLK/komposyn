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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	sqlite3 "modernc.org/sqlite"
)

const maxDiagnosticAggregationKeysPerPoll = 256
const initialDiagnosticAggregationKeys = 8

type diagnosticCorrelation struct {
	operationID         DiagnosticID
	evidenceOrder       uint64
	coverage            DiagnosticCoverageKind
	coverageOffset      uint64
	runID               DiagnosticID
	pollID              DiagnosticID
	sourceGeneration    uint64
	hasSourceGeneration bool
	accumulator         *diagnosticPollAccumulator
	runAccumulator      *diagnosticRunAccumulator
	parserFindings      *diagnosticParserFindings
}

type diagnosticParserFindings struct {
	counts       map[DiagnosticReason]uint64
	positions    map[DiagnosticReason]DiagnosticPosition
	baseOutcomes OutcomeCounts
}

func newDiagnosticParserFindings() *diagnosticParserFindings {
	return &diagnosticParserFindings{counts: make(map[DiagnosticReason]uint64, 8), positions: make(map[DiagnosticReason]DiagnosticPosition, 8)}
}

func (findings *diagnosticParserFindings) reset() {
	if findings != nil {
		clear(findings.counts)
		clear(findings.positions)
		findings.baseOutcomes = OutcomeCounts{}
	}
}

func diagnosticOutcomeForReason(reason DiagnosticReason) RecordOutcome {
	switch reason {
	case DiagnosticReasonInvalidJSON, DiagnosticReasonInvalidTimestamp, DiagnosticReasonInvalidTokenCount,
		DiagnosticReasonRequiredContextMissing, DiagnosticReasonMalformedRecord:
		return OutcomeMalformedRecord
	case DiagnosticReasonUnsupportedUsage:
		return OutcomeUnsupportedUsage
	default:
		return ""
	}
}

func noteDiagnosticParserFailure(ctx context.Context, reason DiagnosticReason, byteOffset int64) {
	findings := diagnosticCorrelationFrom(ctx).parserFindings
	if findings == nil || diagnosticOutcomeForReason(reason) == "" {
		return
	}
	if findings.counts[reason] < math.MaxUint64 {
		findings.counts[reason]++
	}
	if byteOffset < 0 {
		return
	}
	position, exists := findings.positions[reason]
	if !exists {
		findings.positions[reason] = DiagnosticPosition{
			ByteOffset: DiagnosticOptionalUint64{Value: uint64(byteOffset), Availability: DiagnosticAvailabilityAvailable},
			LineNumber: unavailableMeasurement(),
		}
		return
	}
	position.ByteOffset.Truncated = true
	findings.positions[reason] = position
}

type diagnosticCorrelationContextKey struct{}

func withDiagnosticCorrelation(ctx context.Context, correlation diagnosticCorrelation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, diagnosticCorrelationContextKey{}, correlation)
}

func diagnosticCorrelationFrom(ctx context.Context) diagnosticCorrelation {
	if ctx == nil {
		return diagnosticCorrelation{}
	}
	correlation, _ := ctx.Value(diagnosticCorrelationContextKey{}).(diagnosticCorrelation)
	return correlation
}

func newStoreDiagnosticKey() ([32]byte, bool) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return [32]byte{}, false
	}
	return key, true
}

func (store *Store) diagnosticSubject(kind DiagnosticSubjectKind, identity string) DiagnosticSubject {
	if store == nil || !store.diagnosticKeyAvailable || identity == "" {
		return DiagnosticSubject{Kind: kind, Availability: DiagnosticAvailabilityNotCollected}
	}
	mac := hmac.New(sha256.New, store.diagnosticHandleKey[:])
	_, _ = mac.Write([]byte("komposyn/diagnostic/v1/subject/"))
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(identity))
	return DiagnosticSubject{Kind: kind, Reference: hex.EncodeToString(mac.Sum(nil)),
		IdentityScope: DiagnosticIdentityStoreHandle, Availability: DiagnosticAvailabilityAvailable}
}

func (store *Store) registeredRootDiagnosticSubject(path string) DiagnosticSubject {
	if store == nil {
		return DiagnosticSubject{Kind: DiagnosticSubjectRoot, Availability: DiagnosticAvailabilityNotCollected}
	}
	id, _ := store.registeredRootDiagnosticIdentity(path)
	if id == "" {
		return DiagnosticSubject{Kind: DiagnosticSubjectRoot, Availability: DiagnosticAvailabilityNotCollected}
	}
	return store.diagnosticSubject(DiagnosticSubjectRoot, id)
}

func (store *Store) registeredRootDiagnosticIdentity(path string) (string, bool) {
	canonical, err := canonicalCollectorPath(path)
	if err != nil {
		return "", false
	}
	displayPath, err := absoluteCollectorPath(path)
	if err != nil {
		return canonical, true
	}
	var id string
	if err := store.db.QueryRow(`SELECT id FROM collector_sources
		WHERE canonical_path = ? OR display_path = ? ORDER BY CASE WHEN canonical_path = ? THEN 0 ELSE 1 END LIMIT 1`,
		canonical, filepath.Clean(displayPath), canonical).Scan(&id); err != nil || errors.Is(err, sql.ErrNoRows) {
		return canonical, true
	}
	return id, true
}

func (store *Store) discoveryTargetDiagnosticSubject(path string) DiagnosticSubject {
	if store == nil {
		return DiagnosticSubject{Kind: DiagnosticSubjectDiscoveryTarget, Availability: DiagnosticAvailabilityNotCollected}
	}
	canonical, err := canonicalCollectorPath(path)
	if err != nil {
		return DiagnosticSubject{Kind: DiagnosticSubjectDiscoveryTarget, Availability: DiagnosticAvailabilityNotCollected}
	}
	return store.diagnosticSubject(DiagnosticSubjectDiscoveryTarget, canonical)
}

// diagnosticFinding is the core-owned, normalized internal finding seam. The
// observer is its lossy in-memory sink; future persistence must consume
// findings independently here rather than draining the observer mailbox.
type diagnosticFinding struct {
	classification DiagnosticClassification
	kind           DiagnosticEventKind
	correlation    diagnosticCorrelation
	subject        DiagnosticSubject
	counts         DiagnosticOutcomeCounts
	evidence       DiagnosticEvidence
	position       DiagnosticPosition
}

// diagnosticBoundaryFailure marks an actual internal failure boundary without
// changing its local error text or unwrap chain. It is converted to
// OperationalError only after the owning transaction has unwound.
type diagnosticBoundaryFailure struct {
	cause          error
	classification DiagnosticClassification
}

func (failure *diagnosticBoundaryFailure) Error() string {
	if failure == nil || failure.cause == nil {
		return "<nil>"
	}
	return failure.cause.Error()
}

func (failure *diagnosticBoundaryFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func markDiagnosticBoundary(cause error, classification DiagnosticClassification) error {
	if cause == nil {
		return nil
	}
	return &diagnosticBoundaryFailure{cause: cause, classification: classification}
}

func (store *Store) publishFinding(finding diagnosticFinding) DiagnosticEmissionDisposition {
	if store == nil || store.diagnosticObserver == nil {
		return DiagnosticEmissionDisabled
	}
	correlation := finding.correlation
	if correlation.operationID == "" {
		correlation.operationID, _ = newDiagnosticID()
	}
	if correlation.operationID == "" {
		return DiagnosticEmissionUnavailable
	}
	counts := finding.counts
	if counts.Scope == "" {
		counts = unavailableDiagnosticOutcomeCounts()
	}
	evidence := finding.evidence
	if evidence.Scope == "" {
		evidence = DiagnosticEvidence{Scope: DiagnosticEvidenceOperation,
			Availability: DiagnosticAvailabilityNotCollected, UnavailableReason: DiagnosticUnavailableNotCollected}
	}
	position := finding.position
	if position.ByteOffset.Availability == "" {
		position.ByteOffset = unavailableMeasurement()
	}
	if position.LineNumber.Availability == "" {
		position.LineNumber = unavailableMeasurement()
	}
	event := DiagnosticEvent{ContractVersion: DiagnosticContractVersion, CoreVersion: safeCoreVersion(),
		ObservedAt: time.Now().UTC(), Kind: finding.kind, Classification: finding.classification,
		HasClassification: finding.kind != DiagnosticEventPollSummary, OperationID: correlation.operationID,
		CollectorRunID: correlation.runID, PollID: correlation.pollID, Subject: finding.subject,
		SourceSchemaVersion: unavailableMeasurement(), TargetSchemaVersion: unavailableMeasurement(),
		DurationMilliseconds: unavailableMeasurement(), RetryAttempt: unavailableMeasurement(), RetryCount: unavailableMeasurement(),
		Position: position,
		Evidence: evidence, Counts: counts}
	return store.diagnosticObserver.publish(event)
}

func (store *Store) observeFailure(ctx context.Context, cause error, classification DiagnosticClassification,
	subject DiagnosticSubject) error {
	if cause == nil {
		return nil
	}
	var existing *OperationalError
	if errors.As(cause, &existing) {
		return cause
	}
	correlation := diagnosticCorrelationFrom(ctx)
	if diagnosticCancellationOnly(cause) {
		return cause
	}
	if classification.Validate() != nil {
		classification = makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationRead,
			DiagnosticReasonFailureUnclassified, DiagnosticAdapterUnavailable)
	}
	if correlation.operationID == "" {
		correlation.operationID, _ = newDiagnosticID()
	}
	failure := DiagnosticFailure{Classification: classification, OperationID: correlation.operationID,
		EmissionDisposition: DiagnosticEmissionDisabled}
	if store == nil {
		failure.EmissionDisposition = DiagnosticEmissionUnavailable
	} else {
		failure.EmissionDisposition = store.publishFinding(diagnosticFinding{classification: classification,
			kind: DiagnosticEventOperationFailed, correlation: correlation, subject: subject})
	}
	return newOperationalError(cause, failure)
}

// persistOperationFailure consumes the trusted internal classification and
// identity before it is projected to the optional observer. It is best effort:
// a diagnostic write can never change the result of the owning operation.
func (store *Store) persistOperationFailure(ctx context.Context, classification DiagnosticClassification,
	kind DiagnosticSubjectKind, identity string) {
	if store == nil || !store.diagnosticPersistenceReady.Load() || classification.Validate() != nil {
		return
	}
	if identity == "" && kind != DiagnosticSubjectStore {
		return
	}
	c := diagnosticCorrelationFrom(ctx)
	if c.operationID == "" {
		var idErr error
		c.operationID, idErr = newDiagnosticID()
		if idErr != nil || c.operationID == "" {
			store.markDiagnosticPersistenceLost()
			return
		}
	}
	if c.evidenceOrder == 0 {
		var orderErr error
		c.evidenceOrder, orderErr = store.reserveDiagnosticEvidenceOrder()
		if orderErr != nil || c.evidenceOrder == 0 {
			store.markDiagnosticPersistenceLost()
			return
		}
	}
	if c.operationID == "" || c.evidenceOrder == 0 {
		return
	}
	availability := DiagnosticAvailabilityAvailable
	if identity == "" {
		availability = DiagnosticAvailabilityNotCollected
	}
	finding := persistedDiagnosticFinding{Classification: classification, FindingID: c.operationID,
		SubjectKind: kind, SubjectIdentity: identity, SubjectAvailability: availability,
		PollID: c.pollID, RunID: c.runID, EvidenceOrder: c.evidenceOrder, ObservationCount: 1,
		Generation: diagnosticGeneration(c), CoverageOffset: unavailableMeasurement(),
		Position:   DiagnosticPosition{ByteOffset: unavailableMeasurement(), LineNumber: unavailableMeasurement()},
		ObservedAt: time.Now().UTC(), Coverage: DiagnosticCoverageUninspected}
	_ = store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []persistedDiagnosticFinding{finding}})
}

// recordFailedSourceOutcomeAt records FailedSource only at boundaries that
// also increment the caller-visible ImportResult outcome.
func recordFailedSourceOutcomeAt(ctx context.Context, classification DiagnosticClassification, subject DiagnosticSubject) {
	correlation := diagnosticCorrelationFrom(ctx)
	if correlation.accumulator == nil {
		return
	}
	correlation.accumulator.addCounts(OutcomeCounts{FailedSource: 1})
	correlation.accumulator.addCondition(classification, subject, OutcomeFailedSource, 1)
}

func diagnosticCancellationOnly(err error) bool {
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !diagnosticCancellationOnly(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return diagnosticCancellationOnly(wrapped.Unwrap())
	}
	return false
}

func classifyImportFailure(err error, adapter string) DiagnosticClassification {
	if err == nil {
		return DiagnosticClassification{}
	}
	adapterValue := diagnosticAdapter(adapter)
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationRead,
			DiagnosticReasonPermissionDenied, adapterValue)
	}
	if errors.Is(err, os.ErrNotExist) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationRead,
			DiagnosticReasonSourceUnavailable, adapterValue)
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationRead,
			DiagnosticReasonIOFailure, adapterValue)
	}
	var sqliteError *sqlite3.Error
	if errors.As(err, &sqliteError) {
		return sqliteDiagnosticClassification(err, DiagnosticOperationRead)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationRead,
			DiagnosticReasonDeadlineExceeded, adapterValue)
	}
	if errors.Is(err, context.Canceled) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationRead,
			DiagnosticReasonCancelled, adapterValue)
	}
	return makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationRead,
		DiagnosticReasonFailureUnclassified, adapterValue)
}

func classifyDiscoveryFailure(err error) DiagnosticClassification {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationDiscover,
			DiagnosticReasonPermissionDenied, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, os.ErrNotExist) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationDiscover,
			DiagnosticReasonSourceUnavailable, DiagnosticAdapterUnavailable)
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationDiscover,
			DiagnosticReasonIOFailure, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationDiscover,
			DiagnosticReasonDeadlineExceeded, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, context.Canceled) {
		return makeDiagnosticClassification(DiagnosticComponentSource, DiagnosticOperationDiscover,
			DiagnosticReasonCancelled, DiagnosticAdapterUnavailable)
	}
	return makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationDiscover,
		DiagnosticReasonFailureUnclassified, DiagnosticAdapterUnavailable)
}

func classifyBackupFailure(err error) DiagnosticClassification {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
		return makeDiagnosticClassification(DiagnosticComponentStorage, DiagnosticOperationBackup,
			DiagnosticReasonPermissionDenied, DiagnosticAdapterUnavailable)
	}
	if recognizedSQLiteOperationalFailure(err) {
		return sqliteDiagnosticClassification(err, DiagnosticOperationBackup)
	}
	return makeDiagnosticClassification(DiagnosticComponentMigration, DiagnosticOperationBackup,
		DiagnosticReasonBackupFailed, DiagnosticAdapterUnavailable)
}

func classifyMigrationFailure(err error) DiagnosticClassification {
	var unsupported *UnsupportedSchemaError
	if errors.As(err, &unsupported) || errors.Is(err, ErrUnsupportedSchema) {
		return makeDiagnosticClassification(DiagnosticComponentSchema, DiagnosticOperationOpen,
			DiagnosticReasonSchemaUnsupported, DiagnosticAdapterUnavailable)
	}
	if recognizedSQLiteOperationalFailure(err) {
		return sqliteDiagnosticClassification(err, DiagnosticOperationMigrate)
	}
	return makeDiagnosticClassification(DiagnosticComponentMigration, DiagnosticOperationMigrate,
		DiagnosticReasonMigrationFailed, DiagnosticAdapterUnavailable)
}

func recognizedSQLiteOperationalFailure(err error) bool {
	var sqliteError *sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}
	switch sqliteError.Code() & 0xff {
	case 5, 6, 8, 10, 11, 13, 14, 15, 26:
		return true
	default:
		return false
	}
}

func classifyCollectorFailure(err error, operation DiagnosticOperation) DiagnosticClassification {
	if errors.Is(err, ErrCollectorLeaseLost) {
		return makeDiagnosticClassification(DiagnosticComponentCollector, operation,
			DiagnosticReasonLeaseLost, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, ErrCollectorActive) {
		return makeDiagnosticClassification(DiagnosticComponentCollector, DiagnosticOperationAcquireLease,
			DiagnosticReasonLeaseActive, DiagnosticAdapterUnavailable)
	}
	var sqliteError *sqlite3.Error
	if errors.As(err, &sqliteError) {
		return sqliteDiagnosticClassification(err, operation)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return makeDiagnosticClassification(DiagnosticComponentCollector, operation,
			DiagnosticReasonDeadlineExceeded, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, context.Canceled) {
		return makeDiagnosticClassification(DiagnosticComponentCollector, operation,
			DiagnosticReasonCancelled, DiagnosticAdapterUnavailable)
	}
	return makeDiagnosticClassification(DiagnosticComponentCollector, operation,
		DiagnosticReasonFailureUnclassified, DiagnosticAdapterUnavailable)
}

func (store *Store) observeCollectorFailure(err error, operation DiagnosticOperation) error {
	if err == nil || errors.Is(err, ErrCollectorActive) {
		return err
	}
	classification := classifyCollectorFailure(err, operation)
	if classification.Reason != DiagnosticReasonCancelled {
		store.persistOperationFailure(context.Background(), classification, DiagnosticSubjectStore, "")
	}
	return store.observeFailure(context.Background(), err, classification,
		DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected})
}

func sqliteDiagnosticClassification(err error, operation DiagnosticOperation) DiagnosticClassification {
	var sqliteError *sqlite3.Error
	reason := DiagnosticReasonStorageFailure
	if errors.As(err, &sqliteError) {
		switch sqliteError.Code() & 0xff {
		case 5:
			reason = DiagnosticReasonStorageBusy
		case 6:
			reason = DiagnosticReasonStorageLocked
		case 13:
			reason = DiagnosticReasonStorageFull
		case 8:
			reason = DiagnosticReasonStorageReadonly
		case 11, 26:
			reason = DiagnosticReasonStorageCorrupt
		}
	}
	return makeDiagnosticClassification(DiagnosticComponentStorage, operation, reason, DiagnosticAdapterUnavailable)
}

func diagnosticAdapter(name string) DiagnosticAdapter {
	switch name {
	case "claude":
		return DiagnosticAdapterClaude
	case "codex":
		return DiagnosticAdapterCodex
	default:
		return DiagnosticAdapterUnknown
	}
}

type diagnosticConditionKey struct {
	classification DiagnosticClassification
	subject        DiagnosticSubject
}

type diagnosticPollCondition struct {
	counts                 DiagnosticOutcomeCounts
	position               DiagnosticPosition
	positionGeneration     uint64
	hasPosition            bool
	positionGenerationLost bool
}

type diagnosticPollAccumulator struct {
	store       *Store
	correlation diagnosticCorrelation
	conditions  map[diagnosticConditionKey]diagnosticPollCondition
	counts      DiagnosticOutcomeCounts
	truncated   bool
}

type diagnosticRunAccumulator struct {
	store       *Store
	correlation diagnosticCorrelation
	conditions  map[diagnosticConditionKey]DiagnosticOutcomeCounts
	truncated   bool
}

func newDiagnosticRunAccumulator(store *Store, correlation diagnosticCorrelation) *diagnosticRunAccumulator {
	correlation.accumulator = nil
	correlation.runAccumulator = nil
	return &diagnosticRunAccumulator{store: store, correlation: correlation,
		conditions: make(map[diagnosticConditionKey]DiagnosticOutcomeCounts, initialDiagnosticAggregationKeys)}
}

func (accumulator *diagnosticRunAccumulator) addCondition(classification DiagnosticClassification, subject DiagnosticSubject,
	outcome RecordOutcome, count int) {
	if accumulator == nil || count <= 0 {
		return
	}
	key := diagnosticConditionKey{classification: classification, subject: subject}
	counts, exists := accumulator.conditions[key]
	if !exists && len(accumulator.conditions) >= maxDiagnosticAggregationKeysPerPoll {
		accumulator.truncated = true
		return
	}
	if !exists {
		counts = DiagnosticOutcomeCounts{Scope: DiagnosticCountRun, ScopeID: accumulator.correlation.runID,
			Availability: DiagnosticAvailabilityAvailable}
	}
	addDiagnosticOutcomeCount(&counts, outcome, count)
	accumulator.conditions[key] = counts
}

func (accumulator *diagnosticRunAccumulator) flush() {
	if accumulator == nil || accumulator.store == nil {
		return
	}
	for key, counts := range accumulator.conditions {
		counts.Truncated = accumulator.truncated
		accumulator.store.publishFinding(diagnosticFinding{classification: key.classification,
			kind: DiagnosticEventConditionSummary, correlation: accumulator.correlation, subject: key.subject,
			counts: counts, evidence: DiagnosticEvidence{Scope: DiagnosticEvidenceRun,
				Availability: DiagnosticAvailabilityAvailable, Truncated: accumulator.truncated}})
	}
}

func newDiagnosticPollAccumulator(store *Store, correlation diagnosticCorrelation) *diagnosticPollAccumulator {
	correlation.accumulator = nil
	return &diagnosticPollAccumulator{store: store, correlation: correlation,
		conditions: make(map[diagnosticConditionKey]diagnosticPollCondition, initialDiagnosticAggregationKeys),
		counts: DiagnosticOutcomeCounts{Scope: DiagnosticCountPoll, ScopeID: correlation.pollID,
			Availability: DiagnosticAvailabilityAvailable}}
}

func (accumulator *diagnosticPollAccumulator) addCounts(outcomes OutcomeCounts) {
	if accumulator == nil {
		return
	}
	for _, item := range []struct {
		outcome RecordOutcome
		count   int
	}{
		{OutcomeIgnoredNonUsage, outcomes.IgnoredNonUsage},
		{OutcomeUnchangedSnapshot, outcomes.UnchangedSnapshot},
		{OutcomeDeferredIncompleteTail, outcomes.DeferredIncompleteTail},
		{OutcomeUnsupportedUsage, outcomes.UnsupportedUsage},
		{OutcomeMalformedRecord, outcomes.MalformedRecord},
		{OutcomeFailedSource, outcomes.FailedSource},
	} {
		addDiagnosticOutcomeCount(&accumulator.counts, item.outcome, item.count)
	}
}

func (accumulator *diagnosticPollAccumulator) addCondition(classification DiagnosticClassification, subject DiagnosticSubject,
	outcome RecordOutcome, count int) {
	accumulator.addConditionWithPosition(classification, subject, outcome, count, DiagnosticPosition{}, 0, false)
}

func (accumulator *diagnosticPollAccumulator) addConditionWithPosition(classification DiagnosticClassification, subject DiagnosticSubject,
	outcome RecordOutcome, count int, position DiagnosticPosition, generation uint64, hasGeneration bool) {
	if accumulator == nil || count <= 0 {
		return
	}
	if accumulator.correlation.runAccumulator != nil {
		accumulator.correlation.runAccumulator.addCondition(classification, subject, outcome, count)
	}
	key := diagnosticConditionKey{classification: classification, subject: subject}
	condition, exists := accumulator.conditions[key]
	if !exists && len(accumulator.conditions) >= maxDiagnosticAggregationKeysPerPoll {
		accumulator.truncated = true
		return
	}
	if !exists {
		condition.counts = DiagnosticOutcomeCounts{Scope: DiagnosticCountPoll, ScopeID: accumulator.correlation.pollID,
			Availability: DiagnosticAvailabilityAvailable}
	}
	addDiagnosticOutcomeCount(&condition.counts, outcome, count)
	if position.ByteOffset.Availability == DiagnosticAvailabilityAvailable {
		if !condition.hasPosition && !condition.positionGenerationLost && hasGeneration {
			condition.position = position
			condition.positionGeneration = generation
			condition.hasPosition = true
		} else if condition.hasPosition && (!hasGeneration || condition.positionGeneration != generation) {
			condition.position = DiagnosticPosition{ByteOffset: unavailableMeasurement(), LineNumber: unavailableMeasurement()}
			condition.hasPosition = false
			condition.positionGenerationLost = true
		} else if condition.hasPosition {
			condition.position.ByteOffset.Truncated = true
		}
	}
	accumulator.conditions[key] = condition
}

func addDiagnosticOutcomeCount(counts *DiagnosticOutcomeCounts, outcome RecordOutcome, amount int) {
	if counts == nil || amount <= 0 {
		return
	}
	addDiagnosticOutcomeCount64(counts, outcome, uint64(amount))
}

func addDiagnosticOutcomeCount64(counts *DiagnosticOutcomeCounts, outcome RecordOutcome, amount uint64) {
	if counts == nil || amount == 0 {
		return
	}
	var target *uint64
	switch outcome {
	case OutcomeIgnoredNonUsage:
		target = &counts.IgnoredNonUsage
	case OutcomeUnchangedSnapshot:
		target = &counts.UnchangedSnapshot
	case OutcomeDeferredIncompleteTail:
		target = &counts.DeferredIncompleteTail
	case OutcomeUnsupportedUsage:
		target = &counts.UnsupportedUsage
	case OutcomeMalformedRecord:
		target = &counts.MalformedRecord
	case OutcomeFailedSource:
		target = &counts.FailedSource
	default:
		return
	}
	if math.MaxUint64-*target < amount {
		*target = math.MaxUint64
		counts.Saturated = true
		return
	}
	*target += amount
	if *target == math.MaxUint64 {
		counts.Saturated = true
	}
}

func (accumulator *diagnosticPollAccumulator) flush() {
	if accumulator == nil || accumulator.store == nil {
		return
	}
	for key, condition := range accumulator.conditions {
		condition.counts.Truncated = accumulator.truncated
		evidenceTruncated := accumulator.truncated || condition.positionGenerationLost || condition.position.ByteOffset.Truncated
		finding := diagnosticFinding{classification: key.classification, kind: DiagnosticEventConditionSummary,
			correlation: accumulator.correlation, subject: key.subject, counts: condition.counts, position: condition.position,
			evidence: DiagnosticEvidence{Scope: DiagnosticEvidencePoll,
				Availability: DiagnosticAvailabilityAvailable, Truncated: evidenceTruncated}}
		accumulator.store.publishFinding(finding)
	}
	accumulator.counts.Truncated = accumulator.truncated
	finding := diagnosticFinding{kind: DiagnosticEventPollSummary, correlation: accumulator.correlation,
		subject: DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected},
		counts:  accumulator.counts, evidence: DiagnosticEvidence{Scope: DiagnosticEvidencePoll,
			Availability: DiagnosticAvailabilityAvailable, Truncated: accumulator.truncated}}
	accumulator.store.publishFinding(finding)
}

func (store *Store) recordOutcomeFindingsAt(ctx context.Context, adapter, sourceID string, outcomes OutcomeCounts, operation DiagnosticOperation) {
	if store == nil {
		return
	}
	correlation := diagnosticCorrelationFrom(ctx)
	observedOutcomes := diagnosticOutcomeDelta(outcomes, OutcomeCounts{})
	if correlation.parserFindings != nil {
		observedOutcomes = diagnosticOutcomeDelta(outcomes, correlation.parserFindings.baseOutcomes)
	}
	if correlation.accumulator != nil {
		correlation.accumulator.addCounts(observedOutcomes)
	}
	if adapter != "claude" && adapter != "codex" {
		return
	}
	if correlation.operationID == "" {
		correlation.operationID, _ = newDiagnosticID()
	}
	subject := store.diagnosticSubject(DiagnosticSubjectSource, sourceID)
	type condition struct {
		reason   DiagnosticReason
		outcome  RecordOutcome
		count    uint64
		position DiagnosticPosition
	}
	conditions := make([]condition, 0, 8)
	var malformedClassified, unsupportedClassified uint64
	if correlation.parserFindings != nil {
		for reason, count := range correlation.parserFindings.counts {
			outcome := diagnosticOutcomeForReason(reason)
			if count == 0 || outcome == "" {
				continue
			}
			conditions = append(conditions, condition{reason: reason, outcome: outcome, count: count,
				position: correlation.parserFindings.positions[reason]})
			if outcome == OutcomeMalformedRecord {
				malformedClassified = saturatingAdd(malformedClassified, count)
			} else {
				unsupportedClassified = saturatingAdd(unsupportedClassified, count)
			}
		}
	}
	malformedCount := uint64(max(observedOutcomes.MalformedRecord, 0))
	if malformedCount > malformedClassified {
		conditions = append(conditions, condition{reason: DiagnosticReasonMalformedRecord, outcome: OutcomeMalformedRecord,
			count: malformedCount - malformedClassified})
	}
	unsupportedCount := uint64(max(observedOutcomes.UnsupportedUsage, 0))
	if unsupportedCount > unsupportedClassified {
		conditions = append(conditions, condition{reason: DiagnosticReasonUnsupportedUsage, outcome: OutcomeUnsupportedUsage,
			count: unsupportedCount - unsupportedClassified})
	}
	batch := diagnosticEvidenceBatch{}
	for _, item := range conditions {
		if item.count <= 0 {
			continue
		}
		classification := makeDiagnosticClassification(DiagnosticComponentParser, operation,
			item.reason, diagnosticAdapter(adapter))
		if correlation.accumulator != nil {
			correlation.accumulator.addConditionWithPosition(classification, subject, item.outcome, uint64ToCount(item.count),
				item.position, correlation.sourceGeneration, correlation.hasSourceGeneration)
		}
		if store.diagnosticPersistenceReady.Load() && sourceID != "" && correlation.evidenceOrder != 0 {
			coverage := correlation.coverage
			if coverage == "" {
				coverage = DiagnosticCoveragePartial
			}
			finding := persistedDiagnosticFinding{Classification: classification, FindingID: correlation.operationID,
				SubjectKind: DiagnosticSubjectSource, SubjectIdentity: sourceID, SubjectAvailability: DiagnosticAvailabilityAvailable,
				PollID: correlation.pollID, RunID: correlation.runID, EvidenceOrder: correlation.evidenceOrder,
				ObservationCount: item.count, Generation: diagnosticGeneration(correlation),
				CoverageOffset: DiagnosticOptionalUint64{Value: correlation.coverageOffset, Availability: DiagnosticAvailabilityAvailable}, Position: item.position,
				ObservedAt: time.Now().UTC(), Coverage: coverage}
			batch.Findings = append(batch.Findings, finding)
		}
		if correlation.accumulator != nil {
			continue
		}
		counts := DiagnosticOutcomeCounts{Scope: DiagnosticCountOperation, Availability: DiagnosticAvailabilityAvailable}
		addDiagnosticOutcomeCount64(&counts, item.outcome, item.count)
		store.publishFinding(diagnosticFinding{classification: classification, kind: DiagnosticEventConditionSummary,
			correlation: correlation, subject: subject, counts: counts, position: item.position,
			evidence: DiagnosticEvidence{Scope: DiagnosticEvidenceOperation, Availability: DiagnosticAvailabilityAvailable,
				Truncated: item.position.ByteOffset.Truncated}})
	}
	coverage := correlation.coverage
	completeCoverage := coverage == DiagnosticCoverageFullSnapshot && !observedOutcomes.Incomplete() && !observedOutcomes.Pending()
	if !completeCoverage && (observedOutcomes.Incomplete() || observedOutcomes.Pending()) {
		coverage = DiagnosticCoveragePartial
	}
	if correlation.coverage == DiagnosticCoverageFullSnapshot || correlation.coverage == DiagnosticCoverageIncremental {
		store.appendParserCoverageEvaluation(&batch, correlation, adapter, sourceID, operation, coverage, completeCoverage)
	}
	if len(batch.Findings) > 0 || len(batch.Evaluations) > 0 {
		_ = store.recordDiagnosticEvidence(batch)
	}
}

func (store *Store) recordParserCoverageEvaluation(ctx context.Context, adapter, sourceID string, operation DiagnosticOperation, coverage DiagnosticCoverageKind, complete bool) {
	if store == nil || !store.diagnosticPersistenceReady.Load() || sourceID == "" || (adapter != "claude" && adapter != "codex") {
		return
	}
	correlation := diagnosticCorrelationFrom(ctx)
	if correlation.evidenceOrder == 0 {
		order, err := store.reserveDiagnosticEvidenceOrder()
		if err != nil {
			store.markDiagnosticPersistenceLost()
			return
		}
		correlation.evidenceOrder = order
	}
	var batch diagnosticEvidenceBatch
	store.appendParserCoverageEvaluation(&batch, correlation, adapter, sourceID, operation, coverage, complete)
	if len(batch.Evaluations) > 0 {
		_ = store.recordDiagnosticEvidence(batch)
	}
}

func (store *Store) appendParserCoverageEvaluation(batch *diagnosticEvidenceBatch, correlation diagnosticCorrelation, adapter, sourceID string, operation DiagnosticOperation, coverage DiagnosticCoverageKind, complete bool) {
	if store == nil || batch == nil || !store.diagnosticPersistenceReady.Load() || sourceID == "" || correlation.evidenceOrder == 0 {
		return
	}
	batch.Evaluations = append(batch.Evaluations, diagnosticEvaluation{SubjectKind: DiagnosticSubjectSource,
		SubjectIdentity: sourceID, Component: DiagnosticComponentParser, Operation: operation,
		Adapter: diagnosticAdapter(adapter), PollID: correlation.pollID, RunID: correlation.runID,
		EvidenceOrder: correlation.evidenceOrder, Generation: diagnosticGeneration(correlation),
		CoverageOffset: DiagnosticOptionalUint64{Value: correlation.coverageOffset, Availability: DiagnosticAvailabilityAvailable},
		ObservedAt:     time.Now().UTC(), Coverage: coverage, CompleteCoverage: complete,
		EvaluatedReasons: []DiagnosticReason{DiagnosticReasonInvalidJSON, DiagnosticReasonInvalidTimestamp,
			DiagnosticReasonInvalidTokenCount, DiagnosticReasonRequiredContextMissing, DiagnosticReasonUnsupportedUsage,
			DiagnosticReasonMalformedRecord}})
}

func diagnosticGeneration(c diagnosticCorrelation) DiagnosticOptionalUint64 {
	if !c.hasSourceGeneration {
		return unavailableMeasurement()
	}
	return DiagnosticOptionalUint64{Value: c.sourceGeneration, Availability: DiagnosticAvailabilityAvailable}
}

func diagnosticOutcomeDelta(total, baseline OutcomeCounts) OutcomeCounts {
	delta := OutcomeCounts{}
	if total.IgnoredNonUsage > baseline.IgnoredNonUsage {
		delta.IgnoredNonUsage = total.IgnoredNonUsage - baseline.IgnoredNonUsage
	}
	if total.UnchangedSnapshot > baseline.UnchangedSnapshot {
		delta.UnchangedSnapshot = total.UnchangedSnapshot - baseline.UnchangedSnapshot
	}
	if total.DeferredIncompleteTail > baseline.DeferredIncompleteTail {
		delta.DeferredIncompleteTail = total.DeferredIncompleteTail - baseline.DeferredIncompleteTail
	}
	if total.UnsupportedUsage > baseline.UnsupportedUsage {
		delta.UnsupportedUsage = total.UnsupportedUsage - baseline.UnsupportedUsage
	}
	if total.MalformedRecord > baseline.MalformedRecord {
		delta.MalformedRecord = total.MalformedRecord - baseline.MalformedRecord
	}
	if total.FailedSource > baseline.FailedSource {
		delta.FailedSource = total.FailedSource - baseline.FailedSource
	}
	return delta
}

func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

func uint64ToCount(value uint64) int {
	if value > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}

func newDiagnosticCorrelation(ctx context.Context) diagnosticCorrelation {
	correlation := diagnosticCorrelationFrom(ctx)
	correlation.operationID, _ = newDiagnosticID()
	correlation.evidenceOrder = 0
	return correlation
}
