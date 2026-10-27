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
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testDetailedFinding(identity string, order, count uint64, offset uint64) diagnosticFinding {
	return diagnosticFinding{
		Classification: makeDiagnosticClassification(DiagnosticComponentParser, DiagnosticOperationParse,
			DiagnosticReasonMalformedRecord, DiagnosticAdapterCodex),
		SubjectKind: DiagnosticSubjectSource, SubjectIdentity: identity,
		SubjectAvailability: DiagnosticAvailabilityUnknown,
		PollID:              DiagnosticID("00112233445566778899aabbccddeeff"),
		RunID:               DiagnosticID("ffeeddccbbaa99887766554433221100"),
		EvidenceOrder:       order, ObservationCount: count,
		Generation:     DiagnosticOptionalUint64{Value: 1, Availability: DiagnosticAvailabilityAvailable},
		CoverageOffset: DiagnosticOptionalUint64{Value: offset, Availability: DiagnosticAvailabilityAvailable},
		Position: DiagnosticPosition{
			ByteOffset: DiagnosticOptionalUint64{Value: offset, Availability: DiagnosticAvailabilityAvailable},
			LineNumber: DiagnosticOptionalUint64{Value: offset + 1, Availability: DiagnosticAvailabilityAvailable},
		},
		ObservedAt: time.Date(2026, 10, 27, 10, 0, 0, int(order), time.UTC),
		Coverage:   DiagnosticCoverageFullSnapshot,
	}
}

func testDetailedEvaluation(identity string, order, generation, offset uint64, coverage DiagnosticCoverageKind, complete bool) diagnosticEvaluation {
	return diagnosticEvaluation{SubjectKind: DiagnosticSubjectSource, SubjectIdentity: identity,
		PollID: DiagnosticID("11112222333344445555666677778888"), RunID: DiagnosticID("88887777666655554444333322221111"),
		EvidenceOrder: order, Generation: DiagnosticOptionalUint64{Value: generation, Availability: DiagnosticAvailabilityAvailable},
		CoverageOffset: DiagnosticOptionalUint64{Value: offset, Availability: DiagnosticAvailabilityAvailable},
		ObservedAt:     time.Date(2026, 10, 27, 11, 0, 0, int(order), time.UTC), Coverage: coverage, CompleteCoverage: complete,
		EvaluatedReasons: []DiagnosticReason{DiagnosticReasonMalformedRecord}}
}

func persistTestFinding(t *testing.T, store *Store, finding diagnosticFinding) {
	t.Helper()
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{finding}}); err != nil {
		tx, _ := store.db.Begin()
		key, _ := diagnosticNamespace(tx)
		cause := persistDiagnosticFinding(tx, key, finding)
		t.Logf("validation class=%v subject=%v generation=%v pos=%v poll=%v run=%v", finding.Classification.Validate(),
			validDiagnosticFindingSubject(finding.SubjectKind, finding.SubjectIdentity, finding.SubjectAvailability, finding.Generation, finding.Coverage),
			finding.Generation.Validate(), finding.Position.Validate(), finding.PollID.Validate(), finding.RunID.Validate())
		_ = tx.Rollback()
		t.Fatalf("persist synthetic diagnostic finding: %v (cause %v)", err, cause)
	}
}

func TestDiagnosticDetailsNamespacesCountersAndReviewCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	finding := testDetailedFinding("source-alpha", 1, 2, 100)
	persistTestFinding(t, store, finding)
	persistTestFinding(t, store, testDetailedFinding("source-beta", 2, 1, 100))
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ProblemID == page.Items[1].ProblemID || page.Items[0].Subject.Reference == page.Items[1].Subject.Reference {
		t.Fatalf("same-reason sources not distinct: %#v", page.Items)
	}
	first := page.Items[0]
	if first.Lifetime.Value != "2" || first.LatestPoll.Count.Value != "2" || first.LatestObservedRun.Count.Value != "2" {
		t.Fatalf("scoped counters incorrect: %#v", first)
	}
	if first.Subject.IdentityScope != DiagnosticIdentityStore || len(first.Subject.Reference) != 64 || first.Review.PredatesEvidence {
		t.Fatalf("unsafe or invalid initial detail: %#v", first)
	}
	var writesBefore, writesAfter int
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&writesBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InspectDiagnosticDetail(first.ProblemID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListDiagnosticDetails(DiagnosticQuery{}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT total_changes()").Scan(&writesAfter); err != nil {
		t.Fatal(err)
	}
	if writesAfter != writesBefore {
		t.Fatalf("inspection wrote to SQLite: changes %d -> %d", writesBefore, writesAfter)
	}
	if err := store.ReviewDiagnosticDetail(DiagnosticReviewRequest{ProblemID: first.ProblemID, ExpectedEvidenceRevision: first.Review.EvidenceRevision, State: DiagnosticReviewReviewed}); err != nil {
		t.Fatal(err)
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("source-alpha", 3, 1, 100)}}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.InspectDiagnosticDetail(first.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Lifetime.Value != "3" || updated.LatestPoll.Count.Value != "3" || updated.Review.State != DiagnosticReviewReviewed || updated.Review.PredatesEvidence {
		t.Fatalf("count-only recurrence changed review/material revision: %#v", updated)
	}
	if err := store.ReviewDiagnosticDetail(DiagnosticReviewRequest{ProblemID: first.ProblemID, ExpectedEvidenceRevision: "999", State: DiagnosticReviewSkipped}); !errors.Is(err, ErrDiagnosticReviewConflict) {
		t.Fatalf("stale review error=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reopened, err := store.InspectDiagnosticDetail(first.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Subject.Reference != first.Subject.Reference || reopened.ProblemID != first.ProblemID {
		t.Fatal("diagnostic namespace changed across open")
	}
	other, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	persistTestFinding(t, other, finding)
	otherPage, err := other.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if otherPage.Items[0].ProblemID == first.ProblemID || otherPage.Items[0].Subject.Reference == first.Subject.Reference {
		t.Fatal("independent databases shared diagnostic namespace")
	}
	legacy, err := store.RecordDiagnostic("legacy message /private/path token=marker")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.InspectDiagnosticDetail(legacy.Fingerprint); !errors.Is(err, ErrDiagnosticDetailNotFound) {
		t.Fatalf("legacy row was inferred as detailed evidence: %#v, %v", got, err)
	}
}

func TestDiagnosticConditionRequiresSufficientLaterCoverage(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	finding := testDetailedFinding("source-generation", 10, 4, 100)
	persistTestFinding(t, store, finding)
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	d := page.Items[0]
	if d.Condition.State != DiagnosticConditionActive {
		t.Fatalf("positive finding condition=%s", d.Condition.State)
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Evaluations: []diagnosticEvaluation{
		testDetailedEvaluation("source-generation", 11, 1, 200, DiagnosticCoverageIncremental, false),
	}}); err != nil {
		t.Fatal(err)
	}
	d, err = store.InspectDiagnosticDetail(d.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Condition.State != DiagnosticConditionUnknown || d.Condition.Reason != DiagnosticConditionCoverageInsufficient {
		t.Fatalf("incremental scan falsely resolved: %#v", d.Condition)
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Evaluations: []diagnosticEvaluation{
		testDetailedEvaluation("source-generation", 12, 1, 300, DiagnosticCoverageFullSnapshot, true),
	}}); err != nil {
		t.Fatal(err)
	}
	d, err = store.InspectDiagnosticDetail(d.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Condition.State != DiagnosticConditionResolved || d.Condition.Reason != DiagnosticConditionCoveredAbsence || d.LatestPoll.Count.Value != "0" || d.LatestPoll.Count.Availability != DiagnosticAvailabilityAvailable {
		t.Fatalf("full evaluation did not resolve with measured zero: %#v", d)
	}
	if d.LastPositiveAt.Equal(d.Condition.AsOf) {
		t.Fatal("resolution overwrote the last positive observation")
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("source-generation", 9, 5, 250)}}); err != nil {
		t.Fatal(err)
	}
	d, err = store.InspectDiagnosticDetail(d.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Condition.State != DiagnosticConditionResolved || d.LatestPoll.Count.Value != "0" {
		t.Fatalf("older finding overwrote newer resolution: %#v", d)
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("source-generation", 13, 1, 300)}}); err != nil {
		t.Fatal(err)
	}
	d, err = store.InspectDiagnosticDetail(d.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Condition.State != DiagnosticConditionActive {
		t.Fatalf("re-observed condition not active: %#v", d.Condition)
	}
	if d.Review.State != DiagnosticReviewPending {
		t.Fatalf("initial pending review unexpectedly changed: %#v", d.Review)
	}
}

func TestDiagnosticCountersAreExactAndSaturating(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	large := uint64(math.MaxInt64) + 123
	persistTestFinding(t, store, testDetailedFinding("wide-counter", 1, large, 0))
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].Lifetime.Value != strconv.FormatUint(large, 10) {
		t.Fatalf("large counter rounded: %#v", page.Items[0].Lifetime)
	}
	f := testDetailedFinding("wide-counter", 2, math.MaxUint64-large, 0)
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{f}}); err != nil {
		t.Fatal(err)
	}
	d, err := store.InspectDiagnosticDetail(page.Items[0].ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Lifetime.Value != strconv.FormatUint(math.MaxUint64, 10) || !d.Lifetime.Saturated {
		t.Fatalf("counter did not saturate exactly: %#v", d.Lifetime)
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("wide-counter", 3, 1, 0)}}); err != nil {
		t.Fatal(err)
	}
	d, err = store.InspectDiagnosticDetail(page.Items[0].ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Lifetime.Value != strconv.FormatUint(math.MaxUint64, 10) || !d.Lifetime.Saturated {
		t.Fatalf("saturated counter wrapped: %#v", d.Lifetime)
	}
}

func TestDiagnosticPagingBindsFiltersAndRejectsMalformedTokens(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := uint64(1); i <= 3; i++ {
		persistTestFinding(t, store, testDetailedFinding("page-"+strconv.FormatUint(i, 10), i, 1, 0))
	}
	first, err := store.ListDiagnosticDetails(DiagnosticQuery{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextToken == "" {
		t.Fatalf("bad first page: %#v", first)
	}
	second, err := store.ListDiagnosticDetails(DiagnosticQuery{PageSize: 1, ContinuationToken: first.NextToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || first.Items[0].ProblemID == second.Items[0].ProblemID {
		t.Fatal("keyset pagination repeated or skipped first item")
	}
	if _, err := store.ListDiagnosticDetails(DiagnosticQuery{PageSize: 2, ContinuationToken: first.NextToken}); !errors.Is(err, ErrInvalidDiagnosticQuery) {
		t.Fatalf("mismatched filters accepted: %v", err)
	}
	if _, err := store.ListDiagnosticDetails(DiagnosticQuery{ContinuationToken: strings.Repeat("x", MaxDiagnosticContinuationBytes+1)}); !errors.Is(err, ErrInvalidDiagnosticQuery) {
		t.Fatalf("oversized token accepted: %v", err)
	}
	if _, err := store.ListDiagnosticDetails(DiagnosticQuery{ContinuationToken: "e30"}); !errors.Is(err, ErrInvalidDiagnosticQuery) {
		t.Fatalf("malformed token accepted: %v", err)
	}
}

func TestDiagnosticDetailOutputOmitsRawIdentityAndLegacyText(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := "/home/private/source.jsonl secret-marker"
	f := testDetailedFinding(identity, 1, 1, 9)
	f.SubjectKind = DiagnosticSubjectDiscoveryTarget
	persistTestFinding(t, store, f)
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{identity, "/home/private", "secret-marker", "legacy message"} {
		if bytes.Contains(encoded, []byte(marker)) {
			t.Fatalf("private marker %q escaped in %s", marker, encoded)
		}
	}
	if len(encoded) > MaxDiagnosticListResponseBytes || len(page.Items) != 1 {
		t.Fatalf("response exceeded bounds: %d", len(encoded))
	}
}

func TestDiagnosticEvidenceCapacityOverflowAndSamplesAreBounded(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := uint64(1); i <= MaxDiagnosticLocationSamples+1; i++ {
		if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("sample-source", i, 1, i)}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(page.Items[0].Samples) != MaxDiagnosticLocationSamples || !page.Items[0].SamplesTruncated {
		t.Fatalf("samples not bounded/truncated: %#v", page.Items)
	}
	for i, sample := range page.Items[0].Samples {
		if sample.Position.ByteOffset.Value != uint64(i+1) {
			t.Fatalf("sample order=%d offset=%d", i, sample.Position.ByteOffset.Value)
		}
	}
	for i := uint64(1); i <= MaxDetailedDiagnosticProblems; i++ {
		f := testDetailedFinding("capacity-"+strconv.FormatUint(i, 10), i, 1, 0)
		if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{f}}); err != nil {
			t.Fatalf("persist problem %d: %v", i, err)
		}
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("capacity-overflow", MaxDetailedDiagnosticProblems+1, 7, 0)}}); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListDiagnosticDetails(DiagnosticQuery{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Overflow || page.OverflowCount.Value != "8" {
		t.Fatalf("capacity overflow missing exact count: %#v", page)
	}
	var count int
	if err := store.db.QueryRow("SELECT problem_count FROM diagnostic_detail_meta").Scan(&count); err != nil || count != MaxDetailedDiagnosticProblems {
		t.Fatalf("admitted problem count=%d err=%v", count, err)
	}
}

func TestDiagnosticPersistenceFailureIsSafeAndVisibleForStoreHandle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec("CREATE TRIGGER reject_diagnostic_detail BEFORE INSERT ON diagnostic_details BEGIN SELECT RAISE(ABORT,'private sql marker'); END"); err != nil {
		t.Fatal(err)
	}
	if err := store.recordDiagnosticEvidence(diagnosticEvidenceBatch{Findings: []diagnosticFinding{testDetailedFinding("failed-persist", 1, 1, 0)}}); !errors.Is(err, ErrDiagnosticInspectionUnavailable) {
		t.Fatalf("persistence failure=%v", err)
	}
	page, err := store.ListDiagnosticDetails(DiagnosticQuery{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(page)
	if page.EvidenceAvailability != DiagnosticAvailabilityLost || bytes.Contains(encoded, []byte("private sql marker")) {
		t.Fatalf("failure state/privacy invalid: %s", encoded)
	}
}

func TestMigrationEighteenFailurePreservesSchemaAndVerifiedBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema17.db")
	backup := filepath.Join(dir, "schema17-backup.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP TABLE diagnostic_detail_samples", "DROP TABLE diagnostic_details", "DROP TABLE diagnostic_detail_meta", "DELETE FROM schema_migrations WHERE version=18"} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("CREATE TRIGGER reject_migration_eighteen BEFORE INSERT ON schema_migrations WHEN NEW.version=18 BEGIN SELECT RAISE(ABORT,'injected migration 18 failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenWithOptions(path, OpenOptions{BackupPath: backup})
	if opened != nil {
		_ = opened.Close()
		t.Fatal("open succeeded although migration 18 failed")
	}
	if err == nil || !strings.Contains(err.Error(), "injected migration 18 failure") {
		t.Fatalf("migration failure=%v", err)
	}
	for _, candidate := range []string{path, backup} {
		if _, err := os.Stat(candidate); err != nil {
			t.Fatalf("missing preserved database/backup %s: %v", filepath.Base(candidate), err)
		}
		readOnly, err := sql.Open("sqlite", sqliteFileURI(candidate, "mode=ro"))
		if err != nil {
			t.Fatal(err)
		}
		var integrity string
		var version int
		if err := readOnly.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
			readOnly.Close()
			t.Fatalf("integrity %s = %q, %v", filepath.Base(candidate), integrity, err)
		}
		if err := readOnly.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != 17 {
			readOnly.Close()
			t.Fatalf("preserved version %s=%d err=%v", filepath.Base(candidate), version, err)
		}
		var exists bool
		if err := readOnly.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='diagnostic_detail_meta')").Scan(&exists); err != nil || exists {
			readOnly.Close()
			t.Fatalf("partial migration in %s: exists=%v err=%v", filepath.Base(candidate), exists, err)
		}
		if err := readOnly.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
