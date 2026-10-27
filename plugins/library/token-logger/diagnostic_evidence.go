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
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// diagnosticFinding is the trusted internal persistence seam. It is produced
// by core operations, never from DiagnosticObserver.Events or public DTOs.
// SubjectIdentity must already exist at the core discovery/identity boundary.
type diagnosticFinding struct {
	Classification      DiagnosticClassification
	FindingID           DiagnosticID
	SubjectKind         DiagnosticSubjectKind
	SubjectIdentity     string
	SubjectAvailability DiagnosticAvailability
	PollID              DiagnosticID
	RunID               DiagnosticID
	EvidenceOrder       uint64
	ObservationCount    uint64
	Generation          DiagnosticOptionalUint64
	CoverageOffset      DiagnosticOptionalUint64
	Position            DiagnosticPosition
	ObservedAt          time.Time
	Coverage            DiagnosticCoverageKind
}

// diagnosticEvaluation is explicit evidence that a source/reason scope was
// inspected. Absence only resolves problems when CompleteCoverage is true.
type diagnosticEvaluation struct {
	SubjectKind      DiagnosticSubjectKind
	SubjectIdentity  string
	Adapter          DiagnosticAdapter
	PollID           DiagnosticID
	RunID            DiagnosticID
	EvidenceOrder    uint64
	Generation       DiagnosticOptionalUint64
	CoverageOffset   DiagnosticOptionalUint64
	ObservedAt       time.Time
	Coverage         DiagnosticCoverageKind
	CompleteCoverage bool
	EvaluatedReasons []DiagnosticReason
}

type diagnosticEvidenceBatch struct {
	Findings    []diagnosticFinding
	Evaluations []diagnosticEvaluation
}

func (s *Store) recordDiagnosticEvidence(batch diagnosticEvidenceBatch) (result error) {
	defer func() {
		if result != nil {
			s.diagnosticPersistenceLost.Store(true)
		}
	}()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	defer tx.Rollback()
	key, err := diagnosticNamespace(tx)
	if err != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	seenFindings := make(map[string]struct{}, len(batch.Findings))
	for _, finding := range batch.Findings {
		encoded, _ := json.Marshal(struct {
			ID           DiagnosticID
			Class        DiagnosticClassification
			Kind         DiagnosticSubjectKind
			Identity     string
			Poll         DiagnosticID
			Run          DiagnosticID
			Order, Count uint64
			Position     DiagnosticPosition
		}{finding.FindingID, finding.Classification, finding.SubjectKind, finding.SubjectIdentity, finding.PollID, finding.RunID, finding.EvidenceOrder, finding.ObservationCount, finding.Position})
		if _, duplicate := seenFindings[string(encoded)]; duplicate {
			continue
		}
		seenFindings[string(encoded)] = struct{}{}
		if err := persistDiagnosticFinding(tx, key, finding); err != nil {
			return ErrDiagnosticInspectionUnavailable
		}
	}
	for _, evaluation := range batch.Evaluations {
		if err := applyDiagnosticEvaluation(tx, key, evaluation); err != nil {
			return ErrDiagnosticInspectionUnavailable
		}
	}
	if s.diagnosticPersistenceLost.Load() {
		if _, err := tx.Exec("UPDATE diagnostic_detail_meta SET persistence_available=0 WHERE singleton=1"); err != nil {
			return ErrDiagnosticInspectionUnavailable
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	return nil
}

// reserveDiagnosticEvidenceOrder supplies a persisted monotonic ordering token
// before asynchronous source work begins. Gaps are expected when scans cancel.
func (s *Store) reserveDiagnosticEvidenceOrder() (uint64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, ErrDiagnosticInspectionUnavailable
	}
	defer tx.Rollback()
	var previous string
	if err := tx.QueryRow("SELECT next_evidence_order FROM diagnostic_detail_meta WHERE singleton=1").Scan(&previous); err != nil {
		return 0, ErrDiagnosticInspectionUnavailable
	}
	value, err := strconv.ParseUint(previous, 10, 64)
	if err != nil || value == math.MaxUint64 {
		return 0, ErrDiagnosticInspectionUnavailable
	}
	value++
	if _, err := tx.Exec("UPDATE diagnostic_detail_meta SET next_evidence_order=? WHERE singleton=1", exactCount(value)); err != nil {
		return 0, ErrDiagnosticInspectionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrDiagnosticInspectionUnavailable
	}
	return value, nil
}

func diagnosticRefs(key []byte, kind DiagnosticSubjectKind, identity string, availability DiagnosticAvailability, class DiagnosticClassification) (string, string, string, string) {
	ref := ""
	scope := ""
	actualAvailability := availability
	if identity != "" {
		ref = diagnosticHMAC(key, "subject:"+string(kind), identity)
		scope = string(DiagnosticIdentityStore)
		actualAvailability = DiagnosticAvailabilityAvailable
	}
	if actualAvailability == "" {
		actualAvailability = DiagnosticAvailabilityUnknown
	}
	identityPart := ref
	if ref == "" {
		identityPart = "unavailable:" + string(actualAvailability)
	}
	problem := diagnosticHMAC(key, "problem", string(class.Component), string(class.Operation), string(class.Reason), string(class.Adapter), string(kind), identityPart)
	return ref, scope, string(actualAvailability), problem
}

func persistDiagnosticFinding(tx *sql.Tx, key []byte, f diagnosticFinding) error {
	if f.ObservationCount == 0 {
		f.ObservationCount = 1
	}
	if f.ObservedAt.IsZero() {
		f.ObservedAt = time.Now().UTC()
	}
	f.ObservedAt = f.ObservedAt.UTC()
	if err := f.Classification.Validate(); err != nil {
		return err
	}
	if f.PollID.Validate() != nil || f.RunID.Validate() != nil || f.EvidenceOrder == 0 {
		return ErrInvalidDiagnostic
	}
	if f.FindingID.Validate() != nil {
		return ErrInvalidDiagnostic
	}
	if err := validDiagnosticFindingSubject(f.SubjectKind, f.SubjectIdentity, f.SubjectAvailability, f.Generation, f.Coverage); err != nil {
		return err
	}
	ref, scope, availability, problem := diagnosticRefs(key, f.SubjectKind, f.SubjectIdentity, f.SubjectAvailability, f.Classification)
	now := formatStoredTime(f.ObservedAt)
	var exists int
	err := tx.QueryRow("SELECT 1 FROM diagnostic_details WHERE problem_id=?", problem).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err := tx.QueryRow("SELECT problem_count FROM diagnostic_detail_meta WHERE singleton=1").Scan(&count); err != nil {
			return err
		}
		if count >= MaxDetailedDiagnosticProblems {
			return incrementDiagnosticOverflow(tx, f.ObservationCount)
		}
		generation := ""
		if f.Generation.Availability == DiagnosticAvailabilityAvailable {
			generation = exactCount(f.Generation.Value)
		}
		coverage, reason := conditionEvidenceFor(f.Coverage, true)
		pollAvailability, runAvailability := string(DiagnosticAvailabilityNotApplicable), string(DiagnosticAvailabilityNotApplicable)
		pollCount, runCount := "0", "0"
		if f.PollID != "" {
			pollAvailability = string(DiagnosticAvailabilityAvailable)
			pollCount = exactCount(f.ObservationCount)
		}
		if f.RunID != "" {
			runAvailability = string(DiagnosticAvailabilityAvailable)
			runCount = exactCount(f.ObservationCount)
		}
		_, err = tx.Exec("INSERT INTO diagnostic_details(problem_id,created_at,component,operation,reason,severity,category,adapter,subject_kind,subject_reference,subject_availability,identity_scope,first_seen_at,last_seen_at,last_positive_at,latest_poll_id,latest_poll_order,latest_poll_count,latest_poll_available,latest_poll_saturated,latest_run_id,latest_run_order,latest_run_count,latest_run_available,latest_run_saturated,lifetime_count,lifetime_saturated,condition,condition_as_of,condition_generation,condition_offset,condition_order,condition_coverage,condition_reason,material_revision,review_state,reviewed_revision,samples_truncated,overflowed) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			problem, now, string(f.Classification.Component), string(f.Classification.Operation), string(f.Classification.Reason), string(f.Classification.Severity), string(f.Classification.Category), string(f.Classification.Adapter), string(f.SubjectKind), ref, availability, scope,
			now, now, now, string(f.PollID), exactCount(f.EvidenceOrder), pollCount, pollAvailability, boolInt(f.PollID != "" && f.ObservationCount == math.MaxUint64),
			string(f.RunID), exactCount(f.EvidenceOrder), runCount, runAvailability, boolInt(f.RunID != "" && f.ObservationCount == math.MaxUint64), exactCount(f.ObservationCount), boolInt(f.ObservationCount == math.MaxUint64),
			string(DiagnosticConditionActive), now, generation, decimalOrEmpty(f.CoverageOffset), exactCount(f.EvidenceOrder), string(coverage), string(reason), "1", string(DiagnosticReviewPending), "0", 0, 0)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE diagnostic_detail_meta SET problem_count=problem_count+1 WHERE singleton=1"); err != nil {
			return err
		}
		return mergeDiagnosticSamples(tx, problem, f.Position)
	}
	var oldCategory, oldCondition, oldGeneration, oldOffset, oldOrder, oldPollOrder, oldRunOrder, oldCoverage, oldReason string
	var oldPollID, oldRunID, oldPollAvailability, oldRunAvailability string
	var oldMaterial, oldLifetime, oldPollCount, oldRunCount string
	var oldLifetimeSaturated, oldPollSaturated, oldRunSaturated int
	err = tx.QueryRow("SELECT category,condition,condition_generation,condition_offset,condition_order,latest_poll_order,latest_run_order,material_revision,lifetime_count,lifetime_saturated,latest_poll_count,latest_poll_saturated,latest_run_count,latest_run_saturated,latest_poll_id,latest_run_id,condition_coverage,condition_reason,latest_poll_available,latest_run_available FROM diagnostic_details WHERE problem_id=?", problem).Scan(&oldCategory, &oldCondition, &oldGeneration, &oldOffset, &oldOrder, &oldPollOrder, &oldRunOrder, &oldMaterial, &oldLifetime, &oldLifetimeSaturated, &oldPollCount, &oldPollSaturated, &oldRunCount, &oldRunSaturated, &oldPollID, &oldRunID, &oldCoverage, &oldReason, &oldPollAvailability, &oldRunAvailability)
	if err != nil {
		return err
	}
	lifetime, lifetimeSat := addExactCounter(oldLifetime, f.ObservationCount, oldLifetimeSaturated != 0)
	material := oldMaterial
	newGeneration := decimalOrEmpty(f.Generation)
	conditionNewer := diagnosticEvidenceOrder(f.EvidenceOrder, f.Generation, f.CoverageOffset, oldOrder, oldGeneration, oldOffset) > 0
	condition := DiagnosticCondition(oldCondition)
	asof := now
	conditionOrder := oldOrder
	conditionGen := oldGeneration
	conditionOff := oldOffset
	coverage := DiagnosticCoverageKind(oldCoverage)
	conditionReason := DiagnosticConditionReason(oldReason)
	if conditionNewer {
		condition = DiagnosticConditionActive
		conditionOrder = exactCount(f.EvidenceOrder)
		conditionGen = newGeneration
		conditionOff = decimalOrEmpty(f.CoverageOffset)
		coverage, conditionReason = conditionEvidenceFor(f.Coverage, true)
	}
	category := oldCategory
	if oldCategory == string(DiagnosticCategorySecuritySensitive) || f.Classification.Category == DiagnosticCategorySecuritySensitive {
		category = string(DiagnosticCategorySecuritySensitive)
	}
	if (conditionNewer && (condition != DiagnosticCondition(oldCondition) || conditionGen != oldGeneration || conditionOff != oldOffset)) || category != oldCategory {
		n, e := strconv.ParseUint(material, 10, 64)
		if e != nil || n == math.MaxUint64 {
			return ErrInvalidDiagnostic
		}
		material = exactCount(n + 1)
	}
	pollOrder := oldPollOrder
	pollCount := oldPollCount
	pollSat := oldPollSaturated
	pollID := oldPollID
	if string(f.PollID) != "" && string(f.PollID) == oldPollID {
		var saturated bool
		pollCount, saturated = addExactCounter(oldPollCount, f.ObservationCount, oldPollSaturated != 0)
		pollSat = boolInt(saturated)
		if compareDecimal(f.EvidenceOrder, oldPollOrder) > 0 {
			pollOrder = exactCount(f.EvidenceOrder)
		}
	} else if f.PollID != "" && compareDecimal(f.EvidenceOrder, oldPollOrder) > 0 {
		pollOrder = exactCount(f.EvidenceOrder)
		pollCount = exactCount(f.ObservationCount)
		pollSat = boolInt(f.ObservationCount == math.MaxUint64)
		pollID = string(f.PollID)
	}
	pollAvailability := oldPollAvailability
	if pollID != "" {
		pollAvailability = string(DiagnosticAvailabilityAvailable)
	}
	runOrder := oldRunOrder
	runCount := oldRunCount
	runSat := oldRunSaturated
	runID := oldRunID
	if string(f.RunID) != "" && string(f.RunID) == oldRunID {
		var saturated bool
		runCount, saturated = addExactCounter(oldRunCount, f.ObservationCount, oldRunSaturated != 0)
		runSat = boolInt(saturated)
		if compareDecimal(f.EvidenceOrder, oldRunOrder) > 0 {
			runOrder = exactCount(f.EvidenceOrder)
		}
	} else if f.RunID != "" && compareDecimal(f.EvidenceOrder, oldRunOrder) > 0 {
		runOrder = exactCount(f.EvidenceOrder)
		runCount = exactCount(f.ObservationCount)
		runSat = boolInt(f.ObservationCount == math.MaxUint64)
		runID = string(f.RunID)
	}
	runAvailability := oldRunAvailability
	if runID != "" {
		runAvailability = string(DiagnosticAvailabilityAvailable)
	}
	lastSeen, lastPositive, conditionAsOf := now, now, now
	if !conditionNewer {
		lastSeen, lastPositive, conditionAsOf = "", "", ""
	}
	_, err = tx.Exec("UPDATE diagnostic_details SET last_seen_at=CASE WHEN ?='' THEN last_seen_at ELSE ? END,last_positive_at=CASE WHEN ?='' THEN last_positive_at ELSE ? END,category=?,lifetime_count=?,lifetime_saturated=?,latest_poll_id=?,latest_poll_order=?,latest_poll_count=?,latest_poll_available=?,latest_poll_saturated=?,latest_run_id=?,latest_run_order=?,latest_run_count=?,latest_run_available=?,latest_run_saturated=?,condition=?,condition_as_of=CASE WHEN ?='' THEN condition_as_of ELSE ? END,condition_generation=?,condition_offset=?,condition_order=?,condition_coverage=?,condition_reason=?,material_revision=? WHERE problem_id=?",
		lastSeen, lastSeen, lastPositive, lastPositive, category, lifetime, boolInt(lifetimeSat), pollID, pollOrder, pollCount, pollAvailability, pollSat, runID, runOrder, runCount, runAvailability, runSat, string(condition), conditionAsOf, asof, conditionGen, conditionOff, conditionOrder, string(coverage), string(conditionReason), material, problem)
	if err != nil {
		return err
	}
	beforeSamples, err := diagnosticSamplesState(tx, problem)
	if err != nil {
		return err
	}
	if err := mergeDiagnosticSamples(tx, problem, f.Position); err != nil {
		return err
	}
	afterSamples, err := diagnosticSamplesState(tx, problem)
	if err != nil {
		return err
	}
	if beforeSamples != afterSamples {
		return bumpDiagnosticMaterialRevision(tx, problem)
	}
	return nil
}

func validDiagnosticFindingSubject(kind DiagnosticSubjectKind, identity string, availability DiagnosticAvailability, generation DiagnosticOptionalUint64, coverage DiagnosticCoverageKind) error {
	subject := DiagnosticSubject{Kind: kind, Availability: availability}
	if identity != "" {
		subject.Reference = strings.Repeat("a", 64)
		subject.IdentityScope = DiagnosticIdentityStore
		subject.Availability = DiagnosticAvailabilityAvailable
	}
	if err := subject.Validate(); err != nil {
		return err
	}
	if generation.Availability == DiagnosticAvailabilityAvailable && generation.Value > math.MaxInt64 {
		return ErrInvalidDiagnostic
	}
	if coverage != "" && coverage != DiagnosticCoverageFullSnapshot && coverage != DiagnosticCoverageUnchangedFull && coverage != DiagnosticCoverageIncremental && coverage != DiagnosticCoveragePartial && coverage != DiagnosticCoverageCancelled && coverage != DiagnosticCoverageUninspected {
		return ErrInvalidDiagnostic
	}
	if err := generation.Validate(); err != nil {
		return err
	}
	return DiagnosticPosition{ByteOffset: DiagnosticOptionalUint64{Availability: DiagnosticAvailabilityNotCollected},
		LineNumber: DiagnosticOptionalUint64{Availability: DiagnosticAvailabilityNotCollected}}.Validate()
}

func conditionEvidenceFor(coverage DiagnosticCoverageKind, positive bool) (DiagnosticCoverageKind, DiagnosticConditionReason) {
	if positive {
		return coverage, DiagnosticConditionObserved
	}
	switch coverage {
	case DiagnosticCoverageFullSnapshot, DiagnosticCoverageUnchangedFull:
		return coverage, DiagnosticConditionCoveredAbsence
	case DiagnosticCoverageIncremental, DiagnosticCoveragePartial, DiagnosticCoverageCancelled, DiagnosticCoverageUninspected:
		return coverage, DiagnosticConditionCoverageInsufficient
	default:
		return DiagnosticCoverageUninspected, DiagnosticConditionNotEvaluated
	}
}

func applyDiagnosticEvaluation(tx *sql.Tx, key []byte, e diagnosticEvaluation) error {
	if e.SubjectIdentity == "" {
		return nil
	}
	if e.EvidenceOrder == 0 || e.PollID.Validate() != nil || e.RunID.Validate() != nil {
		return ErrInvalidDiagnostic
	}
	if err := validDiagnosticFindingSubject(e.SubjectKind, e.SubjectIdentity, DiagnosticAvailabilityAvailable, e.Generation, e.Coverage); err != nil {
		return err
	}
	if e.ObservedAt.IsZero() {
		e.ObservedAt = time.Now().UTC()
	}
	now := formatStoredTime(e.ObservedAt.UTC())
	ref, _, _, _ := diagnosticRefs(key, e.SubjectKind, e.SubjectIdentity, DiagnosticAvailabilityUnknown, DiagnosticClassification{})
	rows, err := tx.Query("SELECT problem_id,component,operation,reason,adapter,condition,condition_generation,condition_offset,condition_order,latest_poll_order,material_revision,condition_reason,latest_poll_id,latest_poll_available FROM diagnostic_details WHERE subject_kind=? AND subject_reference=?", string(e.SubjectKind), ref)
	if err != nil {
		return err
	}
	defer rows.Close()
	type target struct{ id, component, operation, reason, adapter, condition, generation, offset, order, pollOrder, revision, conditionReason, pollID, pollAvailability string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.component, &t.operation, &t.reason, &t.adapter, &t.condition, &t.generation, &t.offset, &t.order, &t.pollOrder, &t.revision, &t.conditionReason, &t.pollID, &t.pollAvailability); err != nil {
			return err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range targets {
		found := false
		for _, r := range e.EvaluatedReasons {
			if string(r) == t.reason {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if compareDiagnosticEvidence(e.EvidenceOrder, e.Generation, e.CoverageOffset, t.order, t.generation, t.offset) <= 0 {
			continue
		}
		complete := e.CompleteCoverage && (e.Coverage == DiagnosticCoverageFullSnapshot || e.Coverage == DiagnosticCoverageUnchangedFull) &&
			e.Generation.Availability == DiagnosticAvailabilityAvailable && t.generation != ""
		state := DiagnosticConditionUnknown
		why := DiagnosticConditionCoverageInsufficient
		generation := decimalOrEmpty(e.Generation)
		if complete {
			state = DiagnosticConditionResolved
			why = DiagnosticConditionCoveredAbsence
			if e.Generation.Availability == DiagnosticAvailabilityAvailable && t.generation != "" && compareDecimalString(generation, t.generation) > 0 {
				why = DiagnosticConditionSourceReplaced
			}
		}
		revision := t.revision
		if string(state) != t.condition || generation != t.generation || string(why) != t.conditionReason || decimalOrEmpty(e.CoverageOffset) != t.offset {
			n, er := strconv.ParseUint(revision, 10, 64)
			if er != nil || n == math.MaxUint64 {
				return ErrInvalidDiagnostic
			}
			revision = exactCount(n + 1)
		}
		pollOrder, pollCount, pollAvail, pollID := t.pollOrder, "0", t.pollAvailability, string(DiagnosticID(t.pollID))
		if e.PollID != "" && compareDecimal(e.EvidenceOrder, t.pollOrder) > 0 {
			pollOrder = exactCount(e.EvidenceOrder)
			pollID = string(e.PollID)
			pollAvail = string(DiagnosticAvailabilityUnknown)
			if complete {
				pollCount = "0"
				pollAvail = string(DiagnosticAvailabilityAvailable)
			}
		}
		_, err := tx.Exec("UPDATE diagnostic_details SET condition=?,condition_as_of=?,condition_generation=?,condition_offset=?,condition_order=?,condition_coverage=?,condition_reason=?,material_revision=?,latest_poll_id=?,latest_poll_order=?,latest_poll_count=?,latest_poll_available=?,latest_poll_saturated=0 WHERE problem_id=?", string(state), now, generation, decimalOrEmpty(e.CoverageOffset), exactCount(e.EvidenceOrder), string(e.Coverage), string(why), revision, pollID, pollOrder, pollCount, pollAvail, t.id)
		if err != nil {
			return err
		}
	}
	return nil
}

func validDiagnosticCoverage(value DiagnosticCoverageKind) bool {
	switch value {
	case DiagnosticCoverageFullSnapshot, DiagnosticCoverageUnchangedFull, DiagnosticCoverageIncremental,
		DiagnosticCoveragePartial, DiagnosticCoverageCancelled, DiagnosticCoverageUninspected:
		return true
	}
	return false
}

func mergeDiagnosticSamples(tx *sql.Tx, id string, p DiagnosticPosition) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, v := range []struct {
		name  string
		value DiagnosticOptionalUint64
	}{{"byte_offset", p.ByteOffset}, {"line_number", p.LineNumber}} {
		if v.value.Availability == DiagnosticAvailabilityAvailable && v.value.Value > math.MaxInt64 {
			return ErrInvalidDiagnostic
		}
	}
	byteValue, lineValue := optionalDecimalText(p.ByteOffset), optionalDecimalText(p.LineNumber)
	if byteValue == "" && lineValue == "" {
		return nil
	}
	var exists int
	err := tx.QueryRow("SELECT 1 FROM diagnostic_detail_samples WHERE problem_id=? AND byte_offset=? AND line_number=?", id, byteValue, lineValue).Scan(&exists)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := tx.Query("SELECT byte_offset,line_number FROM diagnostic_detail_samples WHERE problem_id=? ORDER BY slot", id)
	if err != nil {
		return err
	}
	type sample struct{ b, l string }
	items := []sample{}
	for rows.Next() {
		var x sample
		if err := rows.Scan(&x.b, &x.l); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	items = append(items, sample{byteValue, lineValue})
	sort.Slice(items, func(i, j int) bool {
		if items[i].b == "" {
			return false
		}
		if items[j].b == "" {
			return true
		}
		if cmp := compareDecimalString(items[i].b, items[j].b); cmp != 0 {
			return cmp < 0
		}
		return compareDecimalString(items[i].l, items[j].l) < 0
	})
	if len(items) > MaxDiagnosticLocationSamples {
		items = items[:MaxDiagnosticLocationSamples]
		if _, err := tx.Exec("UPDATE diagnostic_details SET samples_truncated=1 WHERE problem_id=?", id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM diagnostic_detail_samples WHERE problem_id=?", id); err != nil {
		return err
	}
	for i, x := range items {
		if _, err := tx.Exec("INSERT INTO diagnostic_detail_samples(problem_id,slot,byte_offset,line_number) VALUES(?,?,?,?)", id, i, x.b, x.l); err != nil {
			return err
		}
	}
	return nil
}

func diagnosticSamplesState(tx *sql.Tx, id string) (string, error) {
	var truncated int
	if err := tx.QueryRow("SELECT samples_truncated FROM diagnostic_details WHERE problem_id=?", id).Scan(&truncated); err != nil {
		return "", err
	}
	rows, err := tx.Query("SELECT byte_offset,line_number FROM diagnostic_detail_samples WHERE problem_id=? ORDER BY slot", id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	value := strconv.Itoa(truncated) + "|"
	for rows.Next() {
		var b, l string
		if err := rows.Scan(&b, &l); err != nil {
			return "", err
		}
		value += b + ":" + l + "|"
	}
	return value, rows.Err()
}
func bumpDiagnosticMaterialRevision(tx *sql.Tx, id string) error {
	var revision string
	if err := tx.QueryRow("SELECT material_revision FROM diagnostic_details WHERE problem_id=?", id).Scan(&revision); err != nil {
		return err
	}
	n, err := strconv.ParseUint(revision, 10, 64)
	if err != nil || n == math.MaxUint64 {
		return ErrInvalidDiagnostic
	}
	_, err = tx.Exec("UPDATE diagnostic_details SET material_revision=? WHERE problem_id=?", exactCount(n+1), id)
	return err
}

func incrementDiagnosticOverflow(tx *sql.Tx, amount uint64) error {
	var value string
	var saturated int
	if err := tx.QueryRow("SELECT overflow_count,overflow_saturated FROM diagnostic_detail_meta WHERE singleton=1").Scan(&value, &saturated); err != nil {
		return err
	}
	n, sat := addExactCounter(value, amount, saturated != 0)
	_, err := tx.Exec("UPDATE diagnostic_detail_meta SET overflow_count=?,overflow_saturated=? WHERE singleton=1", n, boolInt(sat))
	return err
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func optionalDecimalText(v DiagnosticOptionalUint64) string {
	if v.Availability != DiagnosticAvailabilityAvailable {
		return ""
	}
	return exactCount(v.Value)
}
func decimalOrEmpty(v DiagnosticOptionalUint64) string { return optionalDecimalText(v) }
func compareDecimal(value uint64, stored string) int {
	return compareDecimalString(exactCount(value), stored)
}
func compareDecimalString(a, b string) int {
	x, _ := new(big.Int).SetString(a, 10)
	y, _ := new(big.Int).SetString(b, 10)
	if x == nil {
		x = new(big.Int)
	}
	if y == nil {
		y = new(big.Int)
	}
	return x.Cmp(y)
}
func diagnosticEvidenceOrder(order uint64, generation DiagnosticOptionalUint64, offset DiagnosticOptionalUint64, oldOrder, oldGeneration, oldOffset string) int {
	if generation.Availability == DiagnosticAvailabilityAvailable && oldGeneration != "" {
		if c := compareDecimalString(exactCount(generation.Value), oldGeneration); c != 0 {
			return c
		}
		if offset.Availability == DiagnosticAvailabilityAvailable && oldOffset != "" {
			if c := compareDecimalString(exactCount(offset.Value), oldOffset); c != 0 {
				return c
			}
		}
	}
	return compareDecimal(order, oldOrder)
}
func compareDiagnosticEvidence(order uint64, generation, offset DiagnosticOptionalUint64, oldOrder, oldGeneration, oldOffset string) int {
	return diagnosticEvidenceOrder(order, generation, offset, oldOrder, oldGeneration, oldOffset)
}
