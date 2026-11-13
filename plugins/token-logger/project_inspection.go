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
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProjectInspectionFormatVersion = 2
	DefaultInspectionPageSize      = 20
	MaxInspectionPageSize          = 50
	MaxInspectionOffset            = 100000
	MaxInspectionTextBytes         = 256
	MaxInspectionCostReasonBytes   = 4096
	MaxInspectionCostReasonEntries = 16
	maxInspectionReasonLabelBytes  = 128

	InspectionEvidenceAvailable   = "available"
	InspectionEvidenceNotRecorded = "not_recorded"
	InspectionEvidenceUnavailable = "unavailable"

	InspectionRecoveryNotFound                = "not_found"
	InspectionRecoveryRetainMetadataOnly      = "retain_metadata_only_session"
	InspectionRecoveryInactiveInSelectedRange = "inactive_in_selected_range"
	InspectionRecoveryReviewReconciliation    = "review_reconciliation_evidence"
	InspectionRecoveryCandidateNameReview     = "candidate_name_review"
	InspectionRecoveryInsufficientEvidence    = "insufficient_evidence_leave_unchanged"
	InspectionRecoveryNoRepairIndicated       = "no_repair_indicated"
)

// ProjectInspectionRequest selects one project and an inclusive-start,
// exclusive-end UTC reporting range. Limit and Offset bound each evidence
// collection returned in the response.
type ProjectInspectionRequest struct {
	ProjectID string
	From      time.Time
	To        time.Time
	Limit     int
	Offset    int
}

// InspectionPage describes one deterministic page of persisted evidence.
type InspectionPage struct {
	Offset     int   `json:"offset"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	HasMore    bool  `json:"has_more"`
	NextOffset *int  `json:"next_offset,omitempty"`
	Truncated  bool  `json:"truncated"`
}

// InspectionEvidenceStatus distinguishes an observed empty collection from
// evidence that the schema cannot provide.
type InspectionEvidenceStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ProjectInspectionActivity contains aggregate facts derived only from stored
// usage events. It never implies that all provider history was imported.
type ProjectInspectionActivity struct {
	EventCount               int64      `json:"event_count"`
	UsageBearingSessionCount int64      `json:"usage_bearing_session_count"`
	SessionlessEventCount    int64      `json:"sessionless_event_count"`
	FirstActive              *time.Time `json:"first_active,omitempty"`
	LastActive               *time.Time `json:"last_active,omitempty"`
	HasMatchingEvents        bool       `json:"has_matching_events"`
}

// ProjectInspectionAlias returns scoped alias provenance without exposing
// provider alias values. Digests support comparison with warning evidence.
type ProjectInspectionAlias struct {
	Resource    string `json:"resource"`
	ScopeKind   string `json:"scope_kind"`
	ScopeDigest string `json:"scope_digest"`
	Digest      string `json:"alias_digest"`
	Provenance  string `json:"provenance"`
}

// ProjectInspectionLocation summarizes recorded locations without returning
// private filesystem paths.
type ProjectInspectionLocation struct {
	Digest     string `json:"location_digest"`
	Preferred  bool   `json:"preferred"`
	Provenance string `json:"provenance"`
}

// ProjectInspectionWarning is a persisted, sanitized reconciliation warning.
type ProjectInspectionWarning struct {
	Kind            string    `json:"kind"`
	ScopeKind       string    `json:"scope_kind"`
	ScopeDigest     string    `json:"scope_digest"`
	AliasDigest     string    `json:"alias_digest,omitempty"`
	Message         string    `json:"message"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	OccurrenceCount int64     `json:"occurrence_count"`
}

// ProjectInspectionSource reports persisted event provenance and an exact
// source-cursor join when one exists.
type ProjectInspectionSource struct {
	SourceIDDigest          string     `json:"source_id_digest"`
	Adapter                 string     `json:"adapter"`
	EventCount              int64      `json:"event_count"`
	FirstEventAt            *time.Time `json:"first_event_at,omitempty"`
	LastEventAt             *time.Time `json:"last_event_at,omitempty"`
	CursorStatus            string     `json:"cursor_status"`
	CursorFingerprintDigest string     `json:"cursor_fingerprint_digest,omitempty"`
	CursorByteOffset        *int64     `json:"cursor_byte_offset,omitempty"`
	CursorGeneration        *int64     `json:"cursor_generation,omitempty"`
	PendingTail             *bool      `json:"pending_tail,omitempty"`
	SnapshotReusable        *bool      `json:"snapshot_reusable,omitempty"`
}

// InspectionUsage is the inspection-only projection of report measures. It
// preserves numeric values while replacing caller-controlled provenance text
// with controlled labels or omitting it. The regular UsageMeasures/report
// formats remain unchanged.
type InspectionUsage struct {
	RequestCount               int64            `json:"request_count"`
	SessionCount               int64            `json:"session_count"`
	InputTokens                int64            `json:"input_tokens"`
	CachedInputTokens          int64            `json:"cached_input_tokens"`
	CacheWriteInputTokens      int64            `json:"cache_write_input_tokens"`
	OutputTokens               int64            `json:"output_tokens"`
	ReasoningOutputTokens      int64            `json:"reasoning_output_tokens"`
	TotalTokens                int64            `json:"total_tokens"`
	CalculatedCostMicros       int64            `json:"calculated_cost_micros"`
	CalculatedCurrency         string           `json:"calculated_currency,omitempty"`
	ProviderReportedCostMicros int64            `json:"provider_reported_cost_micros"`
	ProviderReportedCurrency   string           `json:"provider_reported_currency,omitempty"`
	UnknownCostRequests        int64            `json:"unknown_cost_requests"`
	EstimatedCostRequests      int64            `json:"estimated_cost_requests"`
	KnownZeroCostRequests      int64            `json:"known_zero_cost_requests"`
	CostUnknownReason          string           `json:"cost_unknown_reason,omitempty"`
	CostUnknownReasons         CostReasonCounts `json:"cost_unknown_reasons,omitempty"`
	PricingProvider            string           `json:"pricing_provider,omitempty"`
	CalculationBasis           string           `json:"calculation_basis,omitempty"`
	PricingSource              string           `json:"pricing_source,omitempty"`
}

// ProjectInspection is a bounded view of persisted attribution and activity
// evidence. NameOrigin is "unavailable" if label provenance is not persisted.
type ProjectInspection struct {
	FormatVersion               int                         `json:"format_version"`
	ProjectIDDigest             string                      `json:"project_id_digest"`
	Exists                      bool                        `json:"exists"`
	Synthetic                   bool                        `json:"synthetic"`
	DisplayNameDigest           string                      `json:"display_name_digest,omitempty"`
	TextOmitted                 bool                        `json:"text_omitted"`
	NameOrigin                  string                      `json:"name_origin"`
	ClassificationEvidence      InspectionEvidenceStatus    `json:"classification_evidence"`
	LocationEvidence            InspectionEvidenceStatus    `json:"location_evidence"`
	AliasEvidence               InspectionEvidenceStatus    `json:"alias_evidence"`
	SourceEvidence              InspectionEvidenceStatus    `json:"source_evidence"`
	StoredSessionCount          int64                       `json:"stored_session_count"`
	SelectedUsageSessionCount   int64                       `json:"selected_usage_session_count"`
	HistoricalUsageSessionCount int64                       `json:"historical_usage_session_count"`
	SelectedRange               UsageInspectionRange        `json:"selected_range"`
	SelectedActivity            ProjectInspectionActivity   `json:"selected_activity"`
	SelectedUsage               InspectionUsage             `json:"selected_usage"`
	HistoricalCoverage          string                      `json:"historical_coverage"`
	HistoricalActivity          ProjectInspectionActivity   `json:"historical_activity"`
	Locations                   []ProjectInspectionLocation `json:"locations"`
	LocationsPage               InspectionPage              `json:"locations_page"`
	Aliases                     []ProjectInspectionAlias    `json:"aliases"`
	AliasesPage                 InspectionPage              `json:"aliases_page"`
	Warnings                    []ProjectInspectionWarning  `json:"warnings"`
	WarningsPage                InspectionPage              `json:"warnings_page"`
	Sources                     []ProjectInspectionSource   `json:"sources"`
	SourcesPage                 InspectionPage              `json:"sources_page"`
	RecoveryClassification      string                      `json:"recovery_classification"`
	RecoveryExplanation         string                      `json:"recovery_explanation"`
}

// UsageInspectionRange identifies the selected half-open range.
type UsageInspectionRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// InspectProject reads one consistent database snapshot and returns bounded
// evidence pages. Aggregate work scales with matching persisted events and
// references. It never scans provider sources, starts workers, writes to
// SQLite, changes identities, or performs network activity.
//
// Store.Open retains its existing behavior: it may create a database, apply
// migrations with a verified backup, and synchronize embedded pricing. Those
// open-time effects are separate from this query.
func (s *Store) InspectProject(request ProjectInspectionRequest) (ProjectInspection, error) {
	if s == nil || s.db == nil {
		return ProjectInspection{}, errors.New("store is required")
	}
	if strings.TrimSpace(request.ProjectID) == "" {
		return ProjectInspection{}, errors.New("project ID is required")
	}
	if len(request.ProjectID) > 512 {
		return ProjectInspection{}, errors.New("project ID exceeds 512 bytes")
	}
	if request.From.IsZero() || request.To.IsZero() || !request.To.After(request.From) {
		return ProjectInspection{}, errors.New("inspection requires a non-empty half-open time range")
	}
	if request.From.Location() != time.UTC || request.To.Location() != time.UTC {
		return ProjectInspection{}, errors.New("inspection range must use UTC")
	}
	if request.Limit == 0 {
		request.Limit = DefaultInspectionPageSize
	}
	if request.Limit < 1 || request.Limit > MaxInspectionPageSize {
		return ProjectInspection{}, fmt.Errorf("inspection page limit must be between 1 and %d", MaxInspectionPageSize)
	}
	if request.Offset < 0 || request.Offset > MaxInspectionOffset {
		return ProjectInspection{}, fmt.Errorf("inspection offset must be between 0 and %d", MaxInspectionOffset)
	}

	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProjectInspection{}, fmt.Errorf("begin project inspection: %w", err)
	}
	defer tx.Rollback()
	result := ProjectInspection{
		FormatVersion:   ProjectInspectionFormatVersion,
		ProjectIDDigest: digest([]byte(request.ProjectID)),
		NameOrigin:      InspectionEvidenceUnavailable,
		ClassificationEvidence: InspectionEvidenceStatus{
			Status: InspectionEvidenceUnavailable,
			Reason: "the original source-classification decision is not persisted",
		},
		LocationEvidence:   InspectionEvidenceStatus{Status: InspectionEvidenceNotRecorded, Reason: "no location evidence is recorded for this project"},
		AliasEvidence:      InspectionEvidenceStatus{Status: InspectionEvidenceNotRecorded, Reason: "no provider aliases are recorded for this project"},
		SourceEvidence:     InspectionEvidenceStatus{Status: InspectionEvidenceNotRecorded, Reason: "no source provenance is recorded for this project"},
		SelectedRange:      UsageInspectionRange{From: request.From, To: request.To},
		HistoricalCoverage: "stored_events_only",
		Locations:          []ProjectInspectionLocation{},
		Aliases:            []ProjectInspectionAlias{},
		Warnings:           []ProjectInspectionWarning{},
		Sources:            []ProjectInspectionSource{},
	}
	result.Synthetic = request.ProjectID == UnattributedProjectID
	if result.Synthetic {
		result.DisplayNameDigest = digest([]byte("Unattributed"))
		var exists int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE project_id IS NULL)
			OR EXISTS(SELECT 1 FROM effective_usage_events WHERE project_id IS NULL)`).Scan(&exists)
		result.Exists = exists != 0
	} else {
		var rawName string
		err = tx.QueryRow("SELECT display_name FROM projects WHERE id = ?", request.ProjectID).Scan(&rawName)
		result.Exists = err == nil
		if err == nil {
			result.DisplayNameDigest = digest([]byte(rawName))
			result.TextOmitted = rawName != ""
		}
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	}
	if err != nil {
		return ProjectInspection{}, err
	}
	if result.Exists && !result.Synthetic {
		result.NameOrigin, err = inspectNameOrigin(tx, request.ProjectID)
		if err != nil {
			return ProjectInspection{}, err
		}
	}

	filter := UsageFilter{From: request.From, To: request.To, ProjectID: request.ProjectID}
	if result.Synthetic {
		filter.ProjectID = ""
		filter.ProjectMissing = true
	}
	selected, err := queryUsage(tx, filter)
	if err != nil {
		return ProjectInspection{}, err
	}
	result.SelectedUsage = projectInspectionUsage(selected.Summary)
	result.SelectedUsageSessionCount = selected.Summary.SessionCount
	result.SelectedActivity, err = inspectionActivity(tx, request.ProjectID, result.Synthetic, request.From, request.To)
	if err != nil {
		return ProjectInspection{}, err
	}
	result.HistoricalActivity, err = inspectionActivity(tx, request.ProjectID, result.Synthetic, time.Time{}, time.Time{})
	if err != nil {
		return ProjectInspection{}, err
	}
	result.HistoricalUsageSessionCount = result.HistoricalActivity.UsageBearingSessionCount
	if result.Synthetic {
		err = tx.QueryRow("SELECT COUNT(*) FROM sessions WHERE project_id IS NULL").Scan(&result.StoredSessionCount)
	} else {
		err = tx.QueryRow("SELECT COUNT(*) FROM sessions WHERE project_id = ?", request.ProjectID).Scan(&result.StoredSessionCount)
	}
	if err != nil {
		return ProjectInspection{}, err
	}

	result.LocationsPage, result.Locations, err = inspectLocations(tx, request.ProjectID, result.Synthetic, request.Offset, request.Limit)
	if err != nil {
		return ProjectInspection{}, err
	}
	if result.LocationsPage.Total > 0 {
		result.LocationEvidence = InspectionEvidenceStatus{Status: InspectionEvidenceAvailable}
	}
	result.AliasesPage, result.Aliases, err = inspectAliases(tx, request.ProjectID, result.Synthetic, request.Offset, request.Limit)
	if err != nil {
		return ProjectInspection{}, err
	}
	if result.AliasesPage.Total > 0 {
		result.AliasEvidence = InspectionEvidenceStatus{Status: InspectionEvidenceAvailable}
	}
	result.WarningsPage, result.Warnings, err = inspectWarnings(tx, request.ProjectID, result.Synthetic, request.Offset, request.Limit)
	if err != nil {
		return ProjectInspection{}, err
	}
	result.SourcesPage, result.Sources, err = inspectSources(tx, request.ProjectID, result.Synthetic, request.Offset, request.Limit)
	if err != nil {
		return ProjectInspection{}, err
	}
	if result.SourcesPage.Total > 0 {
		result.SourceEvidence = InspectionEvidenceStatus{Status: InspectionEvidenceAvailable}
	}
	result.RecoveryClassification, result.RecoveryExplanation = classifyProjectRecovery(result)
	if err := tx.Commit(); err != nil {
		return ProjectInspection{}, fmt.Errorf("finish project inspection: %w", err)
	}
	return result, nil
}

func inspectionActivity(reader reportQueryer, projectID string, synthetic bool, from, to time.Time) (ProjectInspectionActivity, error) {
	conditions, args := inspectionProjectPredicate(projectID, synthetic)
	if !from.IsZero() {
		conditions += " AND occurred_at >= ? AND occurred_at < ?"
		args = append(args, formatStoredTime(from), formatStoredTime(to))
	}
	query := `SELECT COUNT(*), COUNT(DISTINCT session_id),
		COALESCE(SUM(CASE WHEN session_id IS NULL THEN 1 ELSE 0 END), 0),
		MIN(occurred_at), MAX(occurred_at) FROM effective_usage_events WHERE ` + conditions
	var activity ProjectInspectionActivity
	var first, last sql.NullString
	if err := reader.QueryRow(query, args...).Scan(&activity.EventCount, &activity.UsageBearingSessionCount,
		&activity.SessionlessEventCount, &first, &last); err != nil {
		return ProjectInspectionActivity{}, err
	}
	activity.HasMatchingEvents = activity.EventCount > 0
	var err error
	activity.FirstActive, err = parseNullableStoredTime(first)
	if err != nil {
		return ProjectInspectionActivity{}, err
	}
	activity.LastActive, err = parseNullableStoredTime(last)
	return activity, err
}

func inspectionProjectPredicate(projectID string, synthetic bool) (string, []any) {
	if synthetic {
		return "project_id IS NULL", nil
	}
	return "project_id = ?", []any{projectID}
}

func inspectNameOrigin(tx *sql.Tx, projectID string) (string, error) {
	var hasColumn int
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('projects') WHERE name = 'display_name_source')").Scan(&hasColumn); err != nil {
		return InspectionEvidenceUnavailable, err
	}
	if hasColumn == 0 {
		return InspectionEvidenceUnavailable, nil
	}
	var source string
	if err := tx.QueryRow("SELECT display_name_source FROM projects WHERE id = ?", projectID).Scan(&source); err != nil {
		return InspectionEvidenceUnavailable, err
	}
	switch source {
	case "fallback", "cwd", "provider", "explicit":
		return source, nil
	case "unknown", "":
		return InspectionEvidenceUnavailable, nil
	default:
		return InspectionEvidenceUnavailable, nil
	}
}

func inspectLocations(tx *sql.Tx, projectID string, synthetic bool, offset, limit int) (InspectionPage, []ProjectInspectionLocation, error) {
	if synthetic {
		return inspectionPage(offset, limit, 0, 0), []ProjectInspectionLocation{}, nil
	}
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM project_locations WHERE project_id = ?", projectID).Scan(&total); err != nil {
		return InspectionPage{}, nil, err
	}
	rows, err := tx.Query(`SELECT canonical_path, is_preferred, provenance FROM project_locations
		WHERE project_id = ? ORDER BY is_preferred DESC, canonical_path LIMIT ? OFFSET ?`, projectID, limit+1, offset)
	if err != nil {
		return InspectionPage{}, nil, err
	}
	defer rows.Close()
	values := make([]ProjectInspectionLocation, 0, limit)
	for rows.Next() {
		var path, provenance string
		var preferred bool
		if err := rows.Scan(&path, &preferred, &provenance); err != nil {
			return InspectionPage{}, nil, err
		}
		if len(values) == limit {
			break
		}
		values = append(values, ProjectInspectionLocation{Digest: digest([]byte(path)), Preferred: preferred, Provenance: safeInspectionProvenance(provenance)})
	}
	if err := rows.Err(); err != nil {
		return InspectionPage{}, nil, err
	}
	return inspectionPage(offset, limit, total, len(values)), values, nil
}

func inspectAliases(tx *sql.Tx, projectID string, synthetic bool, offset, limit int) (InspectionPage, []ProjectInspectionAlias, error) {
	sessionPredicate := "s.project_id = ?"
	var sessionArgs []any
	if synthetic {
		sessionPredicate = "s.project_id IS NULL"
	} else {
		sessionArgs = []any{projectID}
	}
	projectAliasCount := "SELECT COUNT(*) FROM project_aliases WHERE project_id = ?"
	countArgs := []any{projectID}
	if synthetic {
		projectAliasCount = "SELECT 0"
		countArgs = nil
	}
	countQuery := "SELECT (" + projectAliasCount + `)
		+ (SELECT COUNT(*) FROM session_aliases sa JOIN sessions s ON s.id = sa.session_id WHERE ` + sessionPredicate + ")"
	countArgs = append(countArgs, sessionArgs...)
	var total int64
	if err := tx.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		return InspectionPage{}, nil, err
	}
	projectPredicate := "project_id = ?"
	if synthetic {
		projectPredicate = "0"
	}
	query := `SELECT resource, scope, alias, provenance FROM (
		SELECT 'project' AS resource, scope, alias, provenance FROM project_aliases WHERE ` + projectPredicate + `
		UNION ALL
		SELECT 'session' AS resource, sa.scope, sa.alias, sa.provenance FROM session_aliases sa
		JOIN sessions s ON s.id = sa.session_id WHERE ` + sessionPredicate + `
	) ORDER BY resource, scope, alias LIMIT ? OFFSET ?`
	queryArgs := []any{}
	if !synthetic {
		queryArgs = append(queryArgs, projectID)
	}
	queryArgs = append(queryArgs, sessionArgs...)
	queryArgs = append(queryArgs, limit+1, offset)
	rows, err := tx.Query(query, queryArgs...)
	if err != nil {
		return InspectionPage{}, nil, err
	}
	defer rows.Close()
	values := make([]ProjectInspectionAlias, 0, limit)
	for rows.Next() {
		var resource, scope, alias, provenance string
		if err := rows.Scan(&resource, &scope, &alias, &provenance); err != nil {
			return InspectionPage{}, nil, err
		}
		if len(values) == limit {
			break
		}
		scopeKind := "adapter"
		if strings.Contains(scope, ":") {
			scopeKind = "adapter_client"
		}
		values = append(values, ProjectInspectionAlias{
			Resource: resource, ScopeKind: scopeKind, ScopeDigest: digest([]byte(scope)),
			Digest: digest([]byte(alias)), Provenance: safeInspectionProvenance(provenance),
		})
	}
	if err := rows.Err(); err != nil {
		return InspectionPage{}, nil, err
	}
	return inspectionPage(offset, limit, total, len(values)), values, nil
}

func inspectWarnings(tx *sql.Tx, projectID string, synthetic bool, offset, limit int) (InspectionPage, []ProjectInspectionWarning, error) {
	var predicate string
	var args []any
	if synthetic {
		predicate = `COALESCE(project_id, '') = '' AND (session_id = '' OR session_id IN (SELECT id FROM sessions WHERE project_id IS NULL))`
	} else {
		predicate = "project_id = ? OR session_id IN (SELECT id FROM sessions WHERE project_id = ?)"
		args = []any{projectID, projectID}
	}
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM reconciliation_warnings WHERE "+predicate, args...).Scan(&total); err != nil {
		return InspectionPage{}, nil, err
	}
	queryArgs := append(append([]any{}, args...), limit+1, offset)
	rows, err := tx.Query(`SELECT kind, scope, alias_digest, message, first_seen_at, last_seen_at, occurrence_count
		FROM reconciliation_warnings WHERE `+predicate+` ORDER BY kind, scope, alias_digest, project_id, session_id LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return InspectionPage{}, nil, err
	}
	defer rows.Close()
	values := make([]ProjectInspectionWarning, 0, limit)
	for rows.Next() {
		var warning ProjectInspectionWarning
		var first, last string
		var scope, storedKind string
		if err := rows.Scan(&storedKind, &scope, &warning.AliasDigest, &warning.Message, &first, &last, &warning.OccurrenceCount); err != nil {
			return InspectionPage{}, nil, err
		}
		if len(values) == limit {
			break
		}
		var err error
		warning.FirstSeenAt, err = time.Parse(time.RFC3339Nano, first)
		if err != nil {
			return InspectionPage{}, nil, err
		}
		warning.LastSeenAt, err = time.Parse(time.RFC3339Nano, last)
		if err != nil {
			return InspectionPage{}, nil, err
		}
		warning.Kind = safeInspectionWarningKind(storedKind)
		if !isSupportedFingerprint(warning.AliasDigest) {
			warning.AliasDigest = ""
		}
		warning.ScopeKind = "adapter"
		if strings.Contains(scope, ":") {
			warning.ScopeKind = "adapter_client"
		}
		warning.ScopeDigest = digest([]byte(scope))
		warning.Message = safeInspectionWarningMessage(storedKind)
		values = append(values, warning)
	}
	if err := rows.Err(); err != nil {
		return InspectionPage{}, nil, err
	}
	return inspectionPage(offset, limit, total, len(values)), values, nil
}

func inspectSources(tx *sql.Tx, projectID string, synthetic bool, offset, limit int) (InspectionPage, []ProjectInspectionSource, error) {
	predicate, args := inspectionProjectPredicate(projectID, synthetic)
	base := `SELECT source_id, source_adapter, COUNT(*) AS event_count, MIN(occurred_at) AS first_event, MAX(occurred_at) AS last_event
		FROM effective_usage_events WHERE ` + predicate + " GROUP BY source_id, source_adapter"
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM ("+base+")", args...).Scan(&total); err != nil {
		return InspectionPage{}, nil, err
	}
	queryArgs := append(append([]any{}, args...), limit+1, offset)
	rows, err := tx.Query(`WITH event_sources AS (`+base+`)
		SELECT e.source_id, e.source_adapter, e.event_count, e.first_event, e.last_event,
		c.file_fingerprint, c.byte_offset, c.source_generation, c.pending_tail, c.snapshot_reusable
		FROM event_sources e LEFT JOIN source_cursors c ON c.source_id = e.source_id
		ORDER BY e.source_id, e.source_adapter LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return InspectionPage{}, nil, err
	}
	defer rows.Close()
	values := make([]ProjectInspectionSource, 0, limit)
	for rows.Next() {
		var source ProjectInspectionSource
		var first, last sql.NullString
		var fingerprint sql.NullString
		var byteOffset, generation sql.NullInt64
		var pending, reusable sql.NullBool
		var sourceID, adapter string
		if err := rows.Scan(&sourceID, &adapter, &source.EventCount, &first, &last,
			&fingerprint, &byteOffset, &generation, &pending, &reusable); err != nil {
			return InspectionPage{}, nil, err
		}
		if len(values) == limit {
			break
		}
		var err error
		source.SourceIDDigest = digest([]byte(sourceID))
		source.Adapter = safeInspectionAdapter(adapter)
		source.FirstEventAt, err = parseNullableStoredTime(first)
		if err != nil {
			return InspectionPage{}, nil, err
		}
		source.LastEventAt, err = parseNullableStoredTime(last)
		if err != nil {
			return InspectionPage{}, nil, err
		}
		if fingerprint.Valid && isSupportedFingerprint(fingerprint.String) {
			source.CursorStatus = InspectionEvidenceAvailable
			source.CursorFingerprintDigest = digest([]byte(fingerprint.String))
			offsetValue, generationValue := byteOffset.Int64, generation.Int64
			pendingValue, reusableValue := pending.Bool, reusable.Bool
			source.CursorByteOffset, source.CursorGeneration = &offsetValue, &generationValue
			source.PendingTail, source.SnapshotReusable = &pendingValue, &reusableValue
		} else {
			source.CursorStatus = InspectionEvidenceUnavailable
		}
		values = append(values, source)
	}
	if err := rows.Err(); err != nil {
		return InspectionPage{}, nil, err
	}
	return inspectionPage(offset, limit, total, len(values)), values, nil
}

func inspectionPage(offset, limit int, total int64, count int) InspectionPage {
	page := InspectionPage{Offset: offset, Limit: limit, Total: total}
	page.HasMore = int64(offset+count) < total
	page.Truncated = page.HasMore && offset+count >= MaxInspectionOffset
	if page.HasMore && !page.Truncated {
		next := offset + count
		page.NextOffset = &next
	}
	return page
}

func classifyProjectRecovery(result ProjectInspection) (string, string) {
	if !result.Exists {
		return InspectionRecoveryNotFound, "No stored project or Unattributed evidence matches this ID; inspection does not recommend deletion or reconstruction."
	}
	if result.WarningsPage.Total > 0 {
		return InspectionRecoveryReviewReconciliation, "Persisted reconciliation warnings warrant review; this read-only result does not merge or reassign identities."
	}
	if result.StoredSessionCount > 0 && result.HistoricalActivity.EventCount == 0 {
		return InspectionRecoveryRetainMetadataOnly, "Stored sessions have no persisted usage events; retain them as metadata-only or unobserved rather than treating zero usage as invalid."
	}
	if !result.SelectedActivity.HasMatchingEvents && result.HistoricalActivity.EventCount > 0 {
		return InspectionRecoveryInactiveInSelectedRange, "Persisted usage exists outside the selected range or filters; this is not a lifetime completeness claim."
	}
	if result.NameOrigin == "fallback" && result.LocationEvidence.Status == InspectionEvidenceAvailable {
		return InspectionRecoveryCandidateNameReview, "A persisted fallback label and recorded location provide candidate evidence for separate name review; no name is changed here."
	}
	if result.NameOrigin == InspectionEvidenceUnavailable {
		return InspectionRecoveryInsufficientEvidence, "Legacy label origin is unavailable; do not infer a fallback decision from the display name or adapter label."
	}
	return InspectionRecoveryNoRepairIndicated, "Stored evidence does not establish a repair need; no mutation is performed."
}

func safeInspectionAdapter(adapter string) string {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case "claude", "claude-code":
		return "claude"
	case "codex":
		return "codex"
	default:
		return "other"
	}
}

func safeInspectionProvenance(provenance string) string {
	switch strings.ToLower(strings.TrimSpace(provenance)) {
	case "explicit local metadata":
		return "explicit_local_metadata"
	case "provider metadata":
		return "provider_metadata"
	case "codex metadata":
		return "codex_metadata"
	case "claude metadata", "claude-code metadata":
		return "claude_metadata"
	case "claude-code":
		return "claude"
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	default:
		return "unknown"
	}
}

func projectInspectionUsage(usage UsageMeasures) InspectionUsage {
	projected := InspectionUsage{
		RequestCount: usage.RequestCount, SessionCount: usage.SessionCount,
		InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens, OutputTokens: usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens, TotalTokens: usage.TotalTokens,
		CalculatedCostMicros:       usage.CalculatedCostMicros,
		CalculatedCurrency:         safeInspectionCurrency(usage.CalculatedCurrency),
		ProviderReportedCostMicros: usage.ProviderReportedCostMicros,
		ProviderReportedCurrency:   safeInspectionCurrency(usage.ProviderReportedCurrency),
		UnknownCostRequests:        usage.UnknownCostRequests, EstimatedCostRequests: usage.EstimatedCostRequests,
		KnownZeroCostRequests: usage.KnownZeroCostRequests,
		CostUnknownReason:     safeInspectionCostReason(usage.CostUnknownReason),
		PricingProvider:       safeInspectionPricingProvider(usage.PricingProvider),
		CalculationBasis:      safeInspectionCalculationBasis(usage.CalculationBasis),
	}
	if usage.PricingSource != "" {
		projected.PricingSource = "recorded"
	}
	var reasons map[string]int64
	if json.Unmarshal([]byte(usage.CostUnknownReasons), &reasons) == nil {
		bounded := make(map[string]int64)
		for reason, count := range reasons {
			label := safeInspectionCostReason(reason)
			bounded[label], _ = checkedCostAdd(bounded[label], count)
		}
		if encoded, err := json.Marshal(bounded); err == nil && len(encoded) <= MaxInspectionCostReasonBytes {
			projected.CostUnknownReasons = CostReasonCounts(encoded)
		}
	}
	return projected
}

func safeInspectionCurrency(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "USD", "EUR", "GBP", "JPY", "CAD", "AUD", "NZD", "CHF", "CNY", "INR", "KRW", "SGD", "BRL", "MXN", "SEK", "NOK", "DKK", "PLN", "CZK", "ILS", "ZAR", "MIXED":
		return strings.ToUpper(strings.TrimSpace(value))
	case "":
		return ""
	default:
		return "other"
	}
}

func safeInspectionPricingProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "anthropic", "openai", "google", "unknown":
		return strings.ToLower(strings.TrimSpace(value))
	case "":
		return ""
	default:
		return "other"
	}
}

func safeInspectionCalculationBasis(value string) string {
	switch value {
	case "public_catalog_estimate", "partial_public_catalog_estimate", "custom_pricing_estimate", "partial_custom_pricing_estimate", "mixed_estimate", "partial_mixed_estimate", "mixed", "provider_reported", "known_zero", "unavailable":
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func safeInspectionCostReason(value string) string {
	switch value {
	case "model_unresolved", "no_matching_rate", "outside_effective_range", "ambiguous_pricing_rule", "arithmetic_overflow", "currency_conflict", "billing_context_missing":
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func isSupportedFingerprint(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func safeInspectionWarningKind(kind string) string {
	switch kind {
	case "ambiguous-location", "alias-location-conflict", "session-project-conflict":
		return kind
	default:
		return "other"
	}
}

func safeInspectionWarningMessage(kind string) string {
	switch kind {
	case "ambiguous-location":
		return "Location evidence matched multiple local projects; the imported identity was kept isolated."
	case "alias-location-conflict":
		return "An established alias conflicts with recorded location evidence; the established mapping was retained."
	case "session-project-conflict":
		return "An established session conflicts with imported project evidence; the established project mapping was retained."
	default:
		return "A reconciliation warning is recorded; its details are unavailable."
	}
}
