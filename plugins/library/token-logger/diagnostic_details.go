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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultDiagnosticPageSize      = 20
	MaxDiagnosticPageSize          = 100
	MaxDiagnosticContinuationBytes = 1024
	MaxDiagnosticListResponseBytes = 1024 * 1024
	MaxDetailedDiagnosticProblems  = 4096
	MaxDiagnosticLocationSamples   = 4
)

var (
	ErrInvalidDiagnosticQuery          = errors.New("invalid diagnostic query")
	ErrDiagnosticDetailNotFound        = errors.New("diagnostic detail not found")
	ErrDiagnosticReviewConflict        = errors.New("diagnostic review conflict")
	ErrDiagnosticInspectionUnavailable = errors.New("diagnostic inspection unavailable")
	diagnosticProblemIDPattern         = regexp.MustCompile("^[0-9a-f]{64}$")
	diagnosticCounterPattern           = regexp.MustCompile("^(0|[1-9][0-9]*)$")
)

type DiagnosticCondition string

const (
	DiagnosticConditionActive   DiagnosticCondition = "active"
	DiagnosticConditionResolved DiagnosticCondition = "resolved"
	DiagnosticConditionUnknown  DiagnosticCondition = "unknown"
)

type DiagnosticReviewState string

const (
	DiagnosticReviewPending  DiagnosticReviewState = "pending"
	DiagnosticReviewReviewed DiagnosticReviewState = "reviewed"
	DiagnosticReviewSkipped  DiagnosticReviewState = "skipped"
)

type DiagnosticConditionReason string

const (
	DiagnosticConditionObserved             DiagnosticConditionReason = "observed"
	DiagnosticConditionCoveredAbsence       DiagnosticConditionReason = "covered_absence"
	DiagnosticConditionCoverageInsufficient DiagnosticConditionReason = "coverage_insufficient"
	DiagnosticConditionSourceReplaced       DiagnosticConditionReason = "source_replaced"
	DiagnosticConditionLegacyUnavailable    DiagnosticConditionReason = "legacy_detail_unavailable"
	DiagnosticConditionEvidenceLost         DiagnosticConditionReason = "evidence_lost"
	DiagnosticConditionNotEvaluated         DiagnosticConditionReason = "not_evaluated"
)

type DiagnosticCoverageKind string

const (
	DiagnosticCoverageFullSnapshot  DiagnosticCoverageKind = "full_snapshot"
	DiagnosticCoverageUnchangedFull DiagnosticCoverageKind = "unchanged_full"
	DiagnosticCoverageIncremental   DiagnosticCoverageKind = "incremental"
	DiagnosticCoveragePartial       DiagnosticCoverageKind = "partial"
	DiagnosticCoverageCancelled     DiagnosticCoverageKind = "cancelled"
	DiagnosticCoverageUninspected   DiagnosticCoverageKind = "uninspected"
)

// DiagnosticExactCount uses decimal text to preserve all uint64 values in
// JSON and SQLite without signed-integer or floating-point coercion.
type DiagnosticExactCount struct {
	Value        string                 `json:"value"`
	Availability DiagnosticAvailability `json:"availability"`
	Saturated    bool                   `json:"saturated,omitempty"`
	Truncated    bool                   `json:"truncated,omitempty"`
}
type DiagnosticObservationCount struct {
	Scope   DiagnosticCountScope `json:"scope"`
	ScopeID DiagnosticID         `json:"scope_id,omitempty"`
	Count   DiagnosticExactCount `json:"count"`
}
type DiagnosticConditionEvidence struct {
	State          DiagnosticCondition       `json:"state"`
	AsOf           time.Time                 `json:"as_of"`
	Scope          DiagnosticEvidenceScope   `json:"scope"`
	Coverage       DiagnosticCoverageKind    `json:"coverage"`
	Availability   DiagnosticAvailability    `json:"availability"`
	Reason         DiagnosticConditionReason `json:"reason"`
	Generation     DiagnosticOptionalUint64  `json:"generation"`
	CoverageOffset DiagnosticOptionalUint64  `json:"coverage_offset"`
}
type DiagnosticReview struct {
	State            DiagnosticReviewState `json:"state"`
	EvidenceRevision string                `json:"evidence_revision"`
	ReviewedAt       time.Time             `json:"reviewed_at,omitempty"`
	PredatesEvidence bool                  `json:"predates_evidence"`
}
type DiagnosticLocationSample struct {
	Position             DiagnosticPosition       `json:"position"`
	Generation           DiagnosticOptionalUint64 `json:"generation"`
	Coverage             DiagnosticCoverageKind   `json:"coverage"`
	CoverageAvailability DiagnosticAvailability   `json:"coverage_availability"`
}

// DiagnosticDetail is a bounded privacy-filtered projection. Legacy messages
// and caller-supplied metadata are intentionally not included.
type DiagnosticDetail struct {
	ContractVersion          int                         `json:"diagnostic_contract_version"`
	ProblemID                string                      `json:"problem_id"`
	CreatedAt                time.Time                   `json:"created_at"`
	Classification           DiagnosticClassification    `json:"classification"`
	Subject                  DiagnosticSubject           `json:"subject"`
	FirstSeenAt              time.Time                   `json:"first_seen_at"`
	LastSeenAt               time.Time                   `json:"last_seen_at"`
	LastPositiveAt           time.Time                   `json:"last_positive_at"`
	LatestPoll               DiagnosticObservationCount  `json:"latest_poll"`
	LatestObservedRun        DiagnosticObservationCount  `json:"latest_observed_run"`
	Lifetime                 DiagnosticExactCount        `json:"lifetime"`
	Condition                DiagnosticConditionEvidence `json:"condition"`
	Review                   DiagnosticReview            `json:"review"`
	Samples                  []DiagnosticLocationSample  `json:"samples"`
	SamplesTruncated         bool                        `json:"samples_truncated"`
	CapacityOverflow         bool                        `json:"capacity_overflow"`
	LegacyDetailAvailability DiagnosticAvailability      `json:"legacy_detail_availability"`
	EvidenceAvailability     DiagnosticAvailability      `json:"evidence_availability"`
	Truncated                bool                        `json:"truncated"`
}
type DiagnosticQuery struct {
	ReviewState       DiagnosticReviewState `json:"review_state,omitempty"`
	Condition         DiagnosticCondition   `json:"condition,omitempty"`
	Category          DiagnosticCategory    `json:"category,omitempty"`
	Reason            DiagnosticReason      `json:"reason,omitempty"`
	SubjectReference  string                `json:"subject_reference,omitempty"`
	PageSize          int                   `json:"page_size,omitempty"`
	ContinuationToken string                `json:"continuation_token,omitempty"`
}
type DiagnosticPage struct {
	ContractVersion      int                    `json:"diagnostic_contract_version"`
	Items                []DiagnosticDetail     `json:"items"`
	NextToken            string                 `json:"next_token,omitempty"`
	Truncated            bool                   `json:"truncated"`
	Overflow             bool                   `json:"overflow"`
	OverflowCount        DiagnosticExactCount   `json:"overflow_count"`
	EvidenceAvailability DiagnosticAvailability `json:"evidence_availability"`
}
type DiagnosticReviewRequest struct {
	ProblemID                string                `json:"problem_id"`
	ExpectedEvidenceRevision string                `json:"expected_evidence_revision"`
	State                    DiagnosticReviewState `json:"state"`
}

type diagnosticContinuation struct {
	Version      int    `json:"v"`
	CreatedAt    string `json:"c"`
	ProblemID    string `json:"p"`
	FilterDigest string `json:"f"`
}
type diagnosticListFilter struct {
	review           DiagnosticReviewState
	condition        DiagnosticCondition
	category         DiagnosticCategory
	reason           DiagnosticReason
	subjectReference string
	pageSize         int
}

func (query DiagnosticQuery) validate() (diagnosticListFilter, error) {
	f := diagnosticListFilter{query.ReviewState, query.Condition, query.Category, query.Reason, query.SubjectReference, query.PageSize}
	if f.pageSize == 0 {
		f.pageSize = DefaultDiagnosticPageSize
	}
	if f.pageSize < 1 || f.pageSize > MaxDiagnosticPageSize {
		return f, ErrInvalidDiagnosticQuery
	}
	if f.review != "" && f.review != DiagnosticReviewPending && f.review != DiagnosticReviewReviewed && f.review != DiagnosticReviewSkipped {
		return f, ErrInvalidDiagnosticQuery
	}
	if f.condition != "" && f.condition != DiagnosticConditionActive && f.condition != DiagnosticConditionResolved && f.condition != DiagnosticConditionUnknown {
		return f, ErrInvalidDiagnosticQuery
	}
	if f.category != "" && !validDiagnosticCategory(string(f.category)) {
		return f, ErrInvalidDiagnosticQuery
	}
	if f.reason != "" {
		if _, ok := diagnosticReasonSpecFor(f.reason); !ok {
			return f, ErrInvalidDiagnosticQuery
		}
	}
	if f.subjectReference != "" && !diagnosticProblemIDPattern.MatchString(f.subjectReference) {
		return f, ErrInvalidDiagnosticQuery
	}
	if len(query.ContinuationToken) > MaxDiagnosticContinuationBytes {
		return f, ErrInvalidDiagnosticQuery
	}
	return f, nil
}
func (f diagnosticListFilter) digest() string {
	data, _ := json.Marshal([]any{f.review, f.condition, f.category, f.reason, f.subjectReference, f.pageSize})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func encodeDiagnosticContinuation(c diagnosticContinuation) (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", ErrInvalidDiagnosticQuery
	}
	token := base64.RawURLEncoding.EncodeToString(data)
	if len(token) > MaxDiagnosticContinuationBytes {
		return "", ErrInvalidDiagnosticQuery
	}
	return token, nil
}
func decodeDiagnosticContinuation(token string, f diagnosticListFilter) (diagnosticContinuation, error) {
	var c diagnosticContinuation
	if token == "" || len(token) > MaxDiagnosticContinuationBytes {
		return c, ErrInvalidDiagnosticQuery
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return c, ErrInvalidDiagnosticQuery
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil {
		return c, ErrInvalidDiagnosticQuery
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, ErrInvalidDiagnosticQuery
	}
	if c.Version != 1 || c.FilterDigest != f.digest() || !diagnosticProblemIDPattern.MatchString(c.ProblemID) {
		return c, ErrInvalidDiagnosticQuery
	}
	if _, err := time.Parse(storedTimeLayout, c.CreatedAt); err != nil {
		return c, ErrInvalidDiagnosticQuery
	}
	return c, nil
}

func (s *Store) ListDiagnosticDetails(query DiagnosticQuery) (DiagnosticPage, error) {
	f, err := query.validate()
	if err != nil {
		return DiagnosticPage{}, err
	}
	var c diagnosticContinuation
	if query.ContinuationToken != "" {
		c, err = decodeDiagnosticContinuation(query.ContinuationToken, f)
		if err != nil {
			return DiagnosticPage{}, err
		}
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
	}
	defer tx.Rollback()
	where := []string{"1=1"}
	args := []any{}
	if f.review != "" {
		where = append(where, "review_state = ?")
		args = append(args, string(f.review))
	}
	if f.condition != "" {
		where = append(where, "condition = ?")
		args = append(args, string(f.condition))
	}
	if f.category != "" {
		where = append(where, "category = ?")
		args = append(args, string(f.category))
	}
	if f.reason != "" {
		where = append(where, "reason = ?")
		args = append(args, string(f.reason))
	}
	if f.subjectReference != "" {
		where = append(where, "subject_reference = ?")
		args = append(args, f.subjectReference)
	}
	if c.ProblemID != "" {
		where = append(where, "(created_at > ? OR (created_at = ? AND problem_id > ?))")
		args = append(args, c.CreatedAt, c.CreatedAt, c.ProblemID)
	}
	args = append(args, f.pageSize+1)
	rows, err := tx.Query(diagnosticDetailSelect+" WHERE "+strings.Join(where, " AND ")+" ORDER BY created_at, problem_id LIMIT ?", args...)
	if err != nil {
		return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
	}
	defer rows.Close()
	items := make([]DiagnosticDetail, 0, f.pageSize+1)
	for rows.Next() {
		item, scanErr := scanDiagnosticDetail(tx, rows)
		if scanErr != nil {
			return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
	}
	hasMore := len(items) > f.pageSize
	if hasMore {
		items = items[:f.pageSize]
	}
	page := DiagnosticPage{ContractVersion: DiagnosticContractVersion, Items: items, OverflowCount: DiagnosticExactCount{Value: "0", Availability: DiagnosticAvailabilityAvailable}, EvidenceAvailability: DiagnosticAvailabilityAvailable}
	var persistenceAvailable int
	if err := tx.QueryRow("SELECT overflow_count, overflow_saturated, persistence_available FROM diagnostic_detail_meta WHERE singleton=1").Scan(&page.OverflowCount.Value, &page.OverflowCount.Saturated, &persistenceAvailable); err != nil {
		return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
	}
	if persistenceAvailable == 0 || s.diagnosticPersistenceLost.Load() {
		page.EvidenceAvailability = DiagnosticAvailabilityLost
	}
	for i := range page.Items {
		page.Items[i].EvidenceAvailability = page.EvidenceAvailability
	}
	page.Overflow = page.OverflowCount.Value != "0" || page.OverflowCount.Saturated
	page.Truncated = page.OverflowCount.Saturated
	for len(page.Items) > 0 {
		data, e := json.Marshal(page)
		if e == nil && len(data) <= MaxDiagnosticListResponseBytes {
			break
		}
		page.Items = page.Items[:len(page.Items)-1]
		page.Truncated = true
		hasMore = true
	}
	if len(page.Items) == 0 && hasMore {
		return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
	}
	if hasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		page.NextToken, err = encodeDiagnosticContinuation(diagnosticContinuation{1, last.CreatedAt.UTC().Format(storedTimeLayout), last.ProblemID, f.digest()})
		if err != nil {
			return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
		}
	}
	if tx.Commit() != nil {
		return DiagnosticPage{}, ErrDiagnosticInspectionUnavailable
	}
	return page, nil
}

const diagnosticDetailSelect = "SELECT problem_id,created_at,component,operation,reason,severity,category,adapter," +
	"subject_kind,subject_reference,subject_availability,identity_scope,first_seen_at,last_seen_at,last_positive_at," +
	"latest_poll_id,latest_poll_count,latest_poll_available,latest_poll_saturated,latest_run_id,latest_run_count," +
	"latest_run_available,latest_run_saturated,lifetime_count,lifetime_saturated,condition,condition_as_of," +
	"condition_generation,condition_offset,condition_coverage,condition_reason,material_revision,review_state," +
	"reviewed_revision,reviewed_at,samples_truncated,overflowed,legacy_fingerprint FROM diagnostic_details"

func scanDiagnosticDetail(tx *sql.Tx, rows *sql.Rows) (DiagnosticDetail, error) {
	var d DiagnosticDetail
	var created, first, last, positive, asof, reviewed string
	var component, operation, reason, severity, category, adapter string
	var kind, ref, subjectAvail, scope string
	var pollID, pollCount, pollAvail, runID, runCount, runAvail string
	var pollSat, runSat, lifeSat, st, overflow int
	var life, condition, generation, offset, coverage, conditionReason string
	var revision, reviewRevision, reviewState string
	var legacy sql.NullString
	err := rows.Scan(&d.ProblemID, &created, &component, &operation, &reason, &severity, &category, &adapter, &kind, &ref, &subjectAvail, &scope,
		&first, &last, &positive, &pollID, &pollCount, &pollAvail, &pollSat, &runID, &runCount, &runAvail, &runSat, &life, &lifeSat, &condition, &asof, &generation, &offset, &coverage, &conditionReason, &revision, &reviewState, &reviewRevision, &reviewed, &st, &overflow, &legacy)
	if err != nil {
		return d, err
	}
	if !diagnosticProblemIDPattern.MatchString(d.ProblemID) {
		return d, ErrInvalidDiagnostic
	}
	if d.CreatedAt, err = parseDetailTime(created); err != nil {
		return d, err
	}
	if d.FirstSeenAt, err = parseDetailTime(first); err != nil {
		return d, err
	}
	if d.LastSeenAt, err = parseDetailTime(last); err != nil {
		return d, err
	}
	if d.LastPositiveAt, err = parseDetailTime(positive); err != nil {
		return d, err
	}
	if d.Condition.AsOf, err = parseDetailTime(asof); err != nil {
		return d, err
	}
	d.Classification = makeDiagnosticClassification(DiagnosticComponent(component), DiagnosticOperation(operation),
		DiagnosticReason(reason), DiagnosticAdapter(adapter))
	d.Classification.Severity = DiagnosticSeverity(severity)
	d.Classification.Category = DiagnosticCategory(category)
	if err = d.Classification.Validate(); err != nil {
		return d, err
	}
	d.Subject = DiagnosticSubject{Kind: DiagnosticSubjectKind(kind), Reference: ref, Availability: DiagnosticAvailability(subjectAvail), IdentityScope: DiagnosticIdentityScope(scope)}
	if err = d.Subject.Validate(); err != nil {
		return d, err
	}
	d.LatestPoll = DiagnosticObservationCount{Scope: DiagnosticCountPoll, ScopeID: DiagnosticID(pollID), Count: DiagnosticExactCount{Value: pollCount, Availability: DiagnosticAvailability(pollAvail), Saturated: pollSat != 0}}
	d.LatestObservedRun = DiagnosticObservationCount{Scope: DiagnosticCountRun, ScopeID: DiagnosticID(runID), Count: DiagnosticExactCount{Value: runCount, Availability: DiagnosticAvailability(runAvail), Saturated: runSat != 0}}
	d.Lifetime = DiagnosticExactCount{Value: life, Availability: DiagnosticAvailabilityAvailable, Saturated: lifeSat != 0}
	if d.LatestPoll.ScopeID.Validate() != nil || d.LatestObservedRun.ScopeID.Validate() != nil ||
		!validAvailability(d.LatestPoll.Count.Availability) || !validAvailability(d.LatestObservedRun.Count.Availability) ||
		(pollSat != 0 && pollCount != exactCount(^uint64(0))) || (runSat != 0 && runCount != exactCount(^uint64(0))) ||
		(lifeSat != 0 && life != exactCount(^uint64(0))) {
		return d, ErrInvalidDiagnostic
	}
	for _, v := range []string{pollCount, runCount, life, revision, reviewRevision, generation, offset} {
		if v != "" && validateExactCount(v) != nil {
			return d, ErrInvalidDiagnostic
		}
	}
	d.Condition = DiagnosticConditionEvidence{State: DiagnosticCondition(condition), AsOf: d.Condition.AsOf, Scope: DiagnosticEvidenceSourceGeneration, Coverage: DiagnosticCoverageKind(coverage), Availability: DiagnosticAvailabilityAvailable, Reason: DiagnosticConditionReason(conditionReason), Generation: optionalDecimal(generation), CoverageOffset: optionalDecimal(offset)}
	if (d.Condition.State != DiagnosticConditionActive && d.Condition.State != DiagnosticConditionResolved && d.Condition.State != DiagnosticConditionUnknown) ||
		d.Condition.Scope != DiagnosticEvidenceSourceGeneration || !validDiagnosticCoverage(d.Condition.Coverage) || !validConditionReason(d.Condition.Reason) {
		return d, ErrInvalidDiagnostic
	}
	d.Review = DiagnosticReview{State: DiagnosticReviewState(reviewState), EvidenceRevision: revision,
		PredatesEvidence: reviewed != "" && reviewRevision != revision}
	if d.Review.State != DiagnosticReviewPending && d.Review.State != DiagnosticReviewReviewed && d.Review.State != DiagnosticReviewSkipped {
		return d, ErrInvalidDiagnostic
	}
	if reviewed != "" {
		d.Review.ReviewedAt, err = parseDetailTime(reviewed)
		if err != nil {
			return d, err
		}
	}
	d.SamplesTruncated = st != 0
	d.CapacityOverflow = overflow != 0
	d.ContractVersion = DiagnosticContractVersion
	d.LegacyDetailAvailability = DiagnosticAvailabilityNotCollected
	d.Samples, err = loadDiagnosticSamples(tx, d.ProblemID)
	if err != nil {
		return d, err
	}
	data, err := json.Marshal(d)
	if err != nil || len(data) > MaxDiagnosticItemBytes {
		return d, ErrInvalidDiagnostic
	}
	return d, nil
}
func validConditionReason(value DiagnosticConditionReason) bool {
	switch value {
	case DiagnosticConditionObserved, DiagnosticConditionCoveredAbsence, DiagnosticConditionCoverageInsufficient, DiagnosticConditionSourceReplaced, DiagnosticConditionLegacyUnavailable, DiagnosticConditionEvidenceLost, DiagnosticConditionNotEvaluated:
		return true
	}
	return false
}
func loadDiagnosticSamples(tx *sql.Tx, id string) ([]DiagnosticLocationSample, error) {
	rows, err := tx.Query("SELECT byte_offset,line_number,generation,coverage FROM diagnostic_detail_samples WHERE problem_id=? ORDER BY slot LIMIT 4", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DiagnosticLocationSample, 0, 4)
	for rows.Next() {
		var b, l, generation, coverage string
		if err := rows.Scan(&b, &l, &generation, &coverage); err != nil {
			return nil, err
		}
		if generation != "" && validateExactCount(generation) != nil {
			return nil, ErrInvalidDiagnostic
		}
		sample := DiagnosticLocationSample{Position: DiagnosticPosition{optionalDecimal(b), optionalDecimal(l)}, Generation: optionalDecimal(generation)}
		if coverage != "" {
			sample.Coverage = DiagnosticCoverageKind(coverage)
			if !validDiagnosticCoverage(sample.Coverage) {
				return nil, ErrInvalidDiagnostic
			}
			sample.CoverageAvailability = DiagnosticAvailabilityAvailable
		} else {
			sample.CoverageAvailability = DiagnosticAvailabilityNotCollected
		}
		out = append(out, sample)
	}
	return out, rows.Err()
}
func optionalDecimal(value string) DiagnosticOptionalUint64 {
	if value == "" {
		return DiagnosticOptionalUint64{Availability: DiagnosticAvailabilityNotCollected}
	}
	n, e := strconv.ParseUint(value, 10, 64)
	if e != nil {
		return DiagnosticOptionalUint64{Availability: DiagnosticAvailabilityUnknown}
	}
	return DiagnosticOptionalUint64{Value: n, Availability: DiagnosticAvailabilityAvailable}
}
func parseDetailTime(value string) (time.Time, error) { return time.Parse(storedTimeLayout, value) }

func (s *Store) InspectDiagnosticDetail(problemID string) (DiagnosticDetail, error) {
	if !diagnosticProblemIDPattern.MatchString(problemID) {
		return DiagnosticDetail{}, ErrInvalidDiagnosticQuery
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DiagnosticDetail{}, ErrDiagnosticInspectionUnavailable
	}
	defer tx.Rollback()
	rows, err := tx.Query(diagnosticDetailSelect+" WHERE problem_id=?", problemID)
	if err != nil {
		return DiagnosticDetail{}, ErrDiagnosticInspectionUnavailable
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return DiagnosticDetail{}, ErrDiagnosticInspectionUnavailable
		}
		return DiagnosticDetail{}, ErrDiagnosticDetailNotFound
	}
	d, err := scanDiagnosticDetail(tx, rows)
	if err != nil {
		return DiagnosticDetail{}, ErrDiagnosticInspectionUnavailable
	}
	var persistenceAvailable int
	if err := tx.QueryRow("SELECT persistence_available FROM diagnostic_detail_meta WHERE singleton=1").Scan(&persistenceAvailable); err != nil {
		return DiagnosticDetail{}, ErrDiagnosticInspectionUnavailable
	}
	d.EvidenceAvailability = DiagnosticAvailabilityAvailable
	if persistenceAvailable == 0 || s.diagnosticPersistenceLost.Load() {
		d.EvidenceAvailability = DiagnosticAvailabilityLost
	}
	if tx.Commit() != nil {
		return DiagnosticDetail{}, ErrDiagnosticInspectionUnavailable
	}
	return d, nil
}
func (s *Store) ReviewDiagnosticDetail(r DiagnosticReviewRequest) error {
	if !diagnosticProblemIDPattern.MatchString(r.ProblemID) || !validExactRevision(r.ExpectedEvidenceRevision) || (r.State != DiagnosticReviewReviewed && r.State != DiagnosticReviewSkipped) {
		return ErrInvalidDiagnosticQuery
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE diagnostic_details SET review_state=?,reviewed_revision=material_revision,reviewed_at=? WHERE problem_id=? AND material_revision=?", string(r.State), formatStoredTime(time.Now().UTC()), r.ProblemID, r.ExpectedEvidenceRevision)
	if err != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	if n == 0 {
		var found int
		err = tx.QueryRow("SELECT 1 FROM diagnostic_details WHERE problem_id=?", r.ProblemID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDiagnosticDetailNotFound
		}
		if err != nil {
			return ErrDiagnosticInspectionUnavailable
		}
		return ErrDiagnosticReviewConflict
	}
	if tx.Commit() != nil {
		return ErrDiagnosticInspectionUnavailable
	}
	return nil
}
func validExactRevision(s string) bool {
	if len(s) == 0 || len(s) > 20 || !diagnosticCounterPattern.MatchString(s) {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
func validateExactCount(s string) error {
	if len(s) == 0 || len(s) > 20 || !diagnosticCounterPattern.MatchString(s) {
		return ErrInvalidDiagnostic
	}
	if _, e := strconv.ParseUint(s, 10, 64); e != nil {
		return ErrInvalidDiagnostic
	}
	return nil
}
func exactCount(n uint64) string { return strconv.FormatUint(n, 10) }
func addExactCounter(value string, add uint64, saturated bool) (string, bool) {
	n, e := strconv.ParseUint(value, 10, 64)
	if e != nil {
		return "0", true
	}
	if saturated || math.MaxUint64-n < add {
		return exactCount(math.MaxUint64), true
	}
	n += add
	return exactCount(n), n == math.MaxUint64
}
func diagnosticHMAC(key []byte, domain string, values ...string) string {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte("komposyn-diagnostic-v1\x00" + domain + "\x00"))
	for _, v := range values {
		_, _ = m.Write([]byte(strconv.Itoa(len(v)) + ":" + v))
	}
	return hex.EncodeToString(m.Sum(nil))
}
func diagnosticNamespace(tx *sql.Tx) ([]byte, error) {
	var key []byte
	if e := tx.QueryRow("SELECT namespace_key FROM diagnostic_detail_meta WHERE singleton=1").Scan(&key); e != nil {
		return nil, e
	}
	if len(key) != 32 {
		return nil, ErrInvalidDiagnostic
	}
	return key, nil
}
