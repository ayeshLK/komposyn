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
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	CodexRecoveryPreviewFormatVersion = 1
	CodexRecoveryAlgorithmVersion     = 1
	CodexRecoveryMaxSnapshotBytes     = 64 << 20
	CodexRecoveryMaxRecords           = 100_000
	CodexRecoveryMaxChanges           = 10_000
	CodexRecoveryMaxPlanBytes         = 16 << 20
)

const (
	CodexRecoveryReasonInvalidSelection       = "invalid_selection"
	CodexRecoveryReasonSourceUnavailable      = "source_unavailable"
	CodexRecoveryReasonSourceNotExisting      = "source_not_existing"
	CodexRecoveryReasonUnsupportedSource      = "unsupported_source"
	CodexRecoveryReasonCollectorActive        = "collector_active"
	CodexRecoveryReasonIncompleteSnapshot     = "incomplete_snapshot"
	CodexRecoveryReasonSourceChanged          = "source_changed"
	CodexRecoveryReasonGenerationChanged      = "generation_changed"
	CodexRecoveryReasonLimitExceeded          = "limit_exceeded"
	CodexRecoveryReasonUnsupportedRecords     = "unsupported_records"
	CodexRecoveryReasonAmbiguousHistory       = "ambiguous_history"
	CodexRecoveryReasonAttributionChanged     = "attribution_changed"
	CodexRecoveryReasonMonetaryReconciliation = "monetary_reconciliation_required"
	CodexRecoveryReasonNoChanges              = "no_changes"
	CodexRecoveryReasonCancelled              = "cancelled"
	CodexRecoveryReasonUnavailable            = "recovery_unavailable"
)

// CodexRecoveryError is a bounded, privacy-safe recovery failure.
type CodexRecoveryError struct{ Reason string }

func (err *CodexRecoveryError) Error() string { return "Codex recovery: " + err.Reason }

// CodexRecoveryTokenVector is an exact normalized seven-category usage vector.
type CodexRecoveryTokenVector struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	CacheReadTokens    int64 `json:"cache_read_tokens"`
	CacheWriteTokens   int64 `json:"cache_write_tokens"`
	CacheWrite5mTokens int64 `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64 `json:"cache_write_1h_tokens"`
	ReasoningTokens    int64 `json:"reasoning_tokens"`
}

// CodexRecoveryChange describes one exact ordinary-event insertion or
// effective-allocation override. References are local opaque values.
type CodexRecoveryChange struct {
	EventReference string                    `json:"event_reference"`
	Kind           string                    `json:"kind"`
	Before         *CodexRecoveryTokenVector `json:"before,omitempty"`
	After          CodexRecoveryTokenVector  `json:"after"`
}

// CodexRecoveryPreview is the complete bounded result of one explicit plan.
type CodexRecoveryPreview struct {
	FormatVersion        int                      `json:"format_version"`
	AlgorithmVersion     int                      `json:"algorithm_version"`
	ParserRevision       int                      `json:"parser_revision"`
	Eligible             bool                     `json:"eligible"`
	PlanToken            string                   `json:"plan_token,omitempty"`
	SourceReference      string                   `json:"source_reference,omitempty"`
	Generation           int64                    `json:"generation"`
	SnapshotBytes        int64                    `json:"snapshot_bytes,omitempty"`
	ExistingEventCount   int                      `json:"existing_event_count"`
	InsertedEventCount   int                      `json:"inserted_event_count"`
	OverriddenEventCount int                      `json:"overridden_event_count"`
	Before               CodexRecoveryTokenVector `json:"before"`
	After                CodexRecoveryTokenVector `json:"after"`
	BeforeRequestCount   int                      `json:"before_request_count"`
	AfterRequestCount    int                      `json:"after_request_count"`
	AffectedSessionCount int                      `json:"affected_session_count"`
	AffectedModelCount   int                      `json:"affected_model_count"`
	FirstEventAt         *time.Time               `json:"first_event_at,omitempty"`
	LastEventAt          *time.Time               `json:"last_event_at,omitempty"`
	Changes              []CodexRecoveryChange    `json:"changes"`
}

type recoveryStoredEvent struct {
	eventID, ingestionKey, occurredAt, projectID, sessionID                  string
	requestID, operation, clientName, clientVersion, clientMode              string
	provider, requestedModel, resolvedModel, adapter, sourceID, sourceRecord string
	recordHash, billingContext, eventType, costStatus                        string
	costCurrency, pricingVersion, providerCostCurrency                       string
	attempt                                                                  int
	schemaVersion                                                            int
	startedAt, completedAt                                                   sql.NullString
	sourceOffset                                                             sql.NullInt64
	base, effective                                                          CodexRecoveryTokenVector
	calculatedCost, providerCost                                             sql.NullInt64
}

type recoveryPlanEvent struct {
	EventID              string                   `json:"event_id"`
	IngestionKey         string                   `json:"ingestion_key"`
	SchemaVersion        int                      `json:"schema_version"`
	EventType            string                   `json:"event_type"`
	SourceRecord         string                   `json:"source_record"`
	SourceOffset         int64                    `json:"source_offset"`
	RecordHash           string                   `json:"record_hash"`
	OccurredAt           string                   `json:"occurred_at"`
	StartedAt            string                   `json:"started_at"`
	CompletedAt          string                   `json:"completed_at"`
	ProjectID            string                   `json:"project_id"`
	SessionID            string                   `json:"session_id"`
	RequestID            string                   `json:"request_id"`
	Attempt              int                      `json:"attempt"`
	Operation            string                   `json:"operation"`
	Client               string                   `json:"client"`
	ClientVersion        string                   `json:"client_version"`
	ClientMode           string                   `json:"client_mode"`
	Provider             string                   `json:"provider"`
	RequestedModel       string                   `json:"requested_model"`
	ResolvedModel        string                   `json:"resolved_model"`
	Adapter              string                   `json:"adapter"`
	SourceID             string                   `json:"source_id"`
	Billing              string                   `json:"billing"`
	CostStatus           string                   `json:"cost_status"`
	CostAmount           sql.NullInt64            `json:"cost_amount"`
	CostCurrency         string                   `json:"cost_currency"`
	PricingVersion       string                   `json:"pricing_version"`
	ProviderCost         sql.NullInt64            `json:"provider_cost"`
	ProviderCostCurrency string                   `json:"provider_cost_currency"`
	Base                 CodexRecoveryTokenVector `json:"base"`
	Before               CodexRecoveryTokenVector `json:"before"`
	After                CodexRecoveryTokenVector `json:"after"`
	Kind                 string                   `json:"kind"`
}

type recoveryPlanBinding struct {
	Domain             string              `json:"domain"`
	NamespaceDigest    string              `json:"namespace_digest"`
	SourceID           string              `json:"source_id"`
	Generation         int64               `json:"generation"`
	CanonicalIdentity  string              `json:"canonical_identity"`
	SnapshotDigest     string              `json:"snapshot_digest"`
	SnapshotSize       int64               `json:"snapshot_size"`
	ParserRevision     int                 `json:"parser_revision"`
	AlgorithmVersion   int                 `json:"algorithm_version"`
	CursorToken        string              `json:"cursor_token"`
	CheckpointToken    string              `json:"checkpoint_token"`
	AllocationRevision int64               `json:"allocation_revision"`
	Events             []recoveryPlanEvent `json:"events"`
}

// PrepareCodexRecovery explicitly reads one selected source and returns a
// bounded plan. It does not write to the Store; Open may already have applied
// migrations or synchronized the catalog.
func (s *Store) PrepareCodexRecovery(ctx context.Context, sourcePath string) (CodexRecoveryPreview, error) {
	preview := CodexRecoveryPreview{FormatVersion: CodexRecoveryPreviewFormatVersion,
		AlgorithmVersion: CodexRecoveryAlgorithmVersion, ParserRevision: codexParserRevision,
		Changes: []CodexRecoveryChange{}}
	refuse := func(reason string) (CodexRecoveryPreview, error) {
		return CodexRecoveryPreview{}, &CodexRecoveryError{Reason: reason}
	}
	if s == nil || s.db == nil || strings.TrimSpace(sourcePath) == "" {
		return refuse(CodexRecoveryReasonInvalidSelection)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return refuse(CodexRecoveryReasonCancelled)
	}
	snapshot, err := acquireRecoverySourceSnapshot(ctx, sourcePath, s, "codex", CodexRecoveryMaxSnapshotBytes)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return refuse(CodexRecoveryReasonCancelled)
		}
		if errors.Is(err, errSourceReadLimitExceeded) {
			return refuse(CodexRecoveryReasonLimitExceeded)
		}
		return refuse(CodexRecoveryReasonSourceUnavailable)
	}
	defer snapshot.close()
	if snapshot.adapter != "codex" {
		return refuse(CodexRecoveryReasonUnsupportedSource)
	}
	if !snapshot.expectedCursor.exists || snapshot.expectedCursor.sourceID != snapshot.baseSourceID || !snapshot.identityExpectation.Exists {
		return refuse(CodexRecoveryReasonSourceNotExisting)
	}
	if snapshot.byteLength > CodexRecoveryMaxSnapshotBytes {
		return refuse(CodexRecoveryReasonLimitExceeded)
	}
	lineCount, err := countRecoveryRecords(ctx, snapshot.file, snapshot.byteLength, s.sourceReadHook)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return refuse(CodexRecoveryReasonCancelled)
		}
		if errors.Is(err, errSourceReadLimitExceeded) {
			return refuse(CodexRecoveryReasonSourceChanged)
		}
		return refuse(CodexRecoveryReasonSourceUnavailable)
	}
	if lineCount > CodexRecoveryMaxRecords {
		return refuse(CodexRecoveryReasonLimitExceeded)
	}
	if snapshot.generation != snapshot.expectedCursor.cursor.SourceGeneration {
		return refuse(CodexRecoveryReasonGenerationChanged)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	defer tx.Rollback()
	if err := recoveryStoreStateMatches(tx, snapshot); err != nil {
		if errors.Is(err, ErrCollectorActive) {
			return refuse(CodexRecoveryReasonCollectorActive)
		}
		return refuse(CodexRecoveryReasonAmbiguousHistory)
	}
	var namespaceKey []byte
	if err := tx.QueryRow("SELECT namespace_key FROM recovery_meta WHERE singleton=1 AND format_version=1").Scan(&namespaceKey); err != nil || len(namespaceKey) != 32 {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	active, err := recoveryCollectorActive(tx)
	if err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	if active {
		return refuse(CodexRecoveryReasonCollectorActive)
	}
	allocationRevision, err := recoveryAllocationRevision(tx, snapshot.baseSourceID, snapshot.generation)
	if err != nil {
		return refuse(CodexRecoveryReasonAmbiguousHistory)
	}

	events := make([]UsageEvent, 0)
	parsed, retry, err := parseCodexRecoverySnapshot(ctx, sourcePath, s, snapshot, &events)
	if err != nil || retry {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return refuse(CodexRecoveryReasonCancelled)
		}
		if errors.Is(err, errSourceReadLimitExceeded) || errors.Is(err, errSourceSnapshotChanged) || retry {
			return refuse(CodexRecoveryReasonSourceChanged)
		}
		return refuse(CodexRecoveryReasonSourceUnavailable)
	}
	if parsed.Outcomes.Incomplete() || parsed.Outcomes.Pending() {
		return refuse(CodexRecoveryReasonIncompleteSnapshot)
	}
	for _, event := range events {
		if !strings.HasPrefix(event.EventID, "codex-event-") || event.Provenance.Adapter != "codex" || event.Provenance.SourceID != generationSourceID(snapshot.baseSourceID, snapshot.generation) {
			return refuse(CodexRecoveryReasonUnsupportedRecords)
		}
		if err := event.Validate(); err != nil {
			return refuse(CodexRecoveryReasonUnsupportedRecords)
		}
	}
	if err := recoveryResolveExistingIdentities(tx, events); err != nil {
		return refuse(CodexRecoveryReasonAttributionChanged)
	}

	stored, err := recoveryReadSourceEvents(tx, snapshot.baseSourceID, snapshot.generation)
	if err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	if len(stored) == 0 {
		return refuse(CodexRecoveryReasonSourceNotExisting)
	}
	if err := recoveryCheckCandidateConflicts(tx, events); err != nil {
		return refuse(CodexRecoveryReasonAmbiguousHistory)
	}
	preview, binding, err := buildCodexRecoveryPreview(namespaceKey, snapshot, allocationRevision, events, stored)
	if err != nil {
		var recoveryErr *CodexRecoveryError
		if errors.As(err, &recoveryErr) {
			return CodexRecoveryPreview{}, recoveryErr
		}
		return refuse(CodexRecoveryReasonAmbiguousHistory)
	}
	encoded, err := json.Marshal(binding)
	if err != nil || len(encoded) > CodexRecoveryMaxPlanBytes {
		return refuse(CodexRecoveryReasonLimitExceeded)
	}
	mac := hmac.New(sha256.New, namespaceKey)
	_, _ = mac.Write(encoded)
	preview.PlanToken = hex.EncodeToString(mac.Sum(nil))
	preview.Eligible = true
	previewBytes, err := json.Marshal(preview)
	if err != nil || len(previewBytes) > CodexRecoveryMaxPlanBytes {
		return refuse(CodexRecoveryReasonLimitExceeded)
	}
	if err := tx.Commit(); err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	return preview, nil
}

func recoveryResolveExistingIdentities(tx *sql.Tx, events []UsageEvent) error {
	projects := make(map[string]string)
	sessions := make(map[string]struct{ id, project string })
	for index := range events {
		event := &events[index]
		projectScope := identityScope(event.Provenance.Adapter, "")
		projectID, ok := projects[event.ProjectID]
		if !ok {
			if err := tx.QueryRow("SELECT project_id FROM project_aliases WHERE scope=? AND alias=?", projectScope, event.ProjectID).Scan(&projectID); err != nil {
				return err
			}
			projects[event.ProjectID] = projectID
		}
		sessionScope := identityScope(event.Provenance.Adapter, event.Client.Name)
		sessionKey := sessionScope + "\x00" + event.SessionID
		session, ok := sessions[sessionKey]
		if !ok {
			if err := tx.QueryRow("SELECT session_id,COALESCE(project_id,'') FROM session_aliases WHERE scope=? AND alias=?", sessionScope, event.SessionID).Scan(&session.id, &session.project); err != nil {
				return err
			}
			sessions[sessionKey] = session
		}
		if session.project != projectID {
			return errors.New("existing session attribution conflicts")
		}
		event.ProjectID, event.SessionID = projectID, session.id
	}
	return nil
}

func recoveryCheckCandidateConflicts(tx *sql.Tx, events []UsageEvent) error {
	const batchSize = 200
	for start := 0; start < len(events); start += batchSize {
		end := min(start+batchSize, len(events))
		clauses := make([]string, 0, (end-start)*2)
		args := make([]any, 0, (end-start)*2)
		for _, event := range events[start:end] {
			clauses = append(clauses, "event_id = ?", "ingestion_key = ?")
			args = append(args, event.EventID, event.IngestionKey)
		}
		rows, err := tx.Query("SELECT event_id, ingestion_key, source_adapter, source_id FROM usage_events WHERE "+strings.Join(clauses, " OR "), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var eventID, key, adapter, sourceID string
			if err := rows.Scan(&eventID, &key, &adapter, &sourceID); err != nil {
				rows.Close()
				return err
			}
			matched := false
			for _, candidate := range events[start:end] {
				if (eventID == candidate.EventID && key == candidate.IngestionKey) && adapter == candidate.Provenance.Adapter && sourceID == candidate.Provenance.SourceID {
					matched = true
					break
				}
			}
			if !matched {
				rows.Close()
				return errors.New("candidate identity conflict")
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func countRecoveryRecords(ctx context.Context, file *os.File, byteLimit int64, hook func(int, int64)) (int, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	stream := newSourceStreamReader(ctx, file, nil, nil, hook, 2)
	stream.maxBytes = byteLimit
	reader := bufio.NewReaderSize(stream, 64*1024)
	count, hasBytes := 0, false
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) != 0 {
			hasBytes = true
		}
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			count++
			hasBytes = false
		} else if hasBytes && err == io.EOF {
			count++
		}
		if count > CodexRecoveryMaxRecords {
			return count, nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func parseCodexRecoverySnapshot(ctx context.Context, path string, store *Store, snapshot sourceSnapshot, events *[]UsageEvent) (ImportResult, bool, error) {
	if _, err := snapshot.file.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, false, err
	}
	stream := newSourceStreamReader(ctx, snapshot.file, []int64{snapshot.expectedCursor.cursor.ByteOffset}, nil, store.sourceReadHook, 3)
	stream.maxBytes = snapshot.byteLength
	verify := func(offset int64) error {
		if stream.read != snapshot.byteLength || offset != snapshot.byteLength || stream.fullFingerprint() != snapshot.fingerprint {
			return errSourceSnapshotChanged
		}
		if snapshot.expectedCursor.cursor.ByteOffset <= stream.read {
			prefix, ok := stream.prefixFingerprint(snapshot.expectedCursor.cursor.ByteOffset)
			if snapshot.expectedCursor.cursor.ByteOffset == stream.read {
				prefix, ok = stream.fullFingerprint(), true
			}
			if !ok || prefix != snapshot.prefixFingerprint {
				return errSourceSnapshotChanged
			}
		}
		current, err := collectorSourceMatchesOpenFile(path, snapshot.source, snapshot.openedInfo)
		if err != nil || !current {
			return errSourceSnapshotChanged
		}
		return nil
	}
	return importCodexReaderForRecovery(ctx, stream, store, snapshot, verify, events)
}

func recoveryStoreStateMatches(tx *sql.Tx, snapshot sourceSnapshot) error {
	var sourceID string
	if err := tx.QueryRow("SELECT source_id FROM collector_source_identities WHERE canonical_key=? AND adapter='codex'", snapshot.identityExpectation.CanonicalKey).Scan(&sourceID); err != nil || sourceID != snapshot.baseSourceID {
		return errors.New("source identity changed")
	}
	var cursor Cursor
	err := tx.QueryRow(`SELECT source_id, file_fingerprint, byte_offset, last_provider_record_id, source_generation,
		pending_tail, snapshot_reusable FROM source_cursors WHERE source_id=?`, snapshot.baseSourceID).Scan(
		&cursor.SourceID, &cursor.FileFingerprint, &cursor.ByteOffset, &cursor.LastProviderRecordID,
		&cursor.SourceGeneration, &cursor.PendingTail, &cursor.SnapshotReusable)
	if err != nil || cursor != snapshot.expectedCursor.cursor {
		return errors.New("cursor changed")
	}
	checkpoint, err := parserCheckpointExpectationTx(tx, snapshot.baseSourceID)
	if err != nil || !checkpointEqual(checkpoint, snapshot.expectedCheckpoint) {
		return errors.New("checkpoint changed")
	}
	var state string
	if err := tx.QueryRow("SELECT state FROM collector_runtime WHERE singleton=1").Scan(&state); err != nil {
		return err
	}
	if state != "stopped" {
		return ErrCollectorActive
	}
	return nil
}

func parserCheckpointExpectationTx(tx *sql.Tx, sourceID string) (sourceCheckpointExpectation, error) {
	row, err := scanParserCheckpoint(tx.QueryRow(`SELECT source_id, adapter, parser_revision, format_version,
		source_generation, boundary_offset, prefix_fingerprint, state_json, state_digest FROM parser_checkpoints WHERE source_id=?`, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCheckpointExpectation{}, nil
	}
	if err != nil {
		return sourceCheckpointExpectation{}, err
	}
	return sourceCheckpointExpectation{exists: true, token: checkpointRowToken(row), row: row}, nil
}

func recoveryCollectorActive(tx *sql.Tx) (bool, error) {
	var state string
	if err := tx.QueryRow("SELECT state FROM collector_runtime WHERE singleton=1").Scan(&state); err != nil {
		return false, err
	}
	return state != "stopped", nil
}

func recoveryAllocationRevision(tx *sql.Tx, sourceID string, generation int64) (int64, error) {
	var storedGeneration, revision int64
	err := tx.QueryRow("SELECT source_generation, revision FROM source_allocation_revisions WHERE source_id=?", sourceID).Scan(&storedGeneration, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil || storedGeneration != generation {
		return 0, errors.New("allocation revision unavailable")
	}
	return revision, nil
}

func recoveryReadSourceEvents(tx *sql.Tx, baseSourceID string, generation int64) (map[string]recoveryStoredEvent, error) {
	sourceID := generationSourceID(baseSourceID, generation)
	rows, err := tx.Query(`SELECT e.event_id,e.ingestion_key,e.schema_version,e.event_type,e.occurred_at,e.started_at,e.completed_at,
		COALESCE(e.project_id,''),COALESCE(e.session_id,''),
		e.request_id,e.attempt,e.operation,e.client_name,e.client_version,e.client_mode,e.provider_name,
		e.requested_model,e.resolved_model,e.source_adapter,e.source_id,e.source_record_id,e.source_offset,
		e.source_record_hash,COALESCE(e.billing_context_json,''),e.cost_status,e.cost_currency,e.pricing_version,
		e.provider_cost_currency,e.input_tokens,e.output_tokens,e.cache_read_tokens,
		e.cache_write_tokens,e.cache_write_5m_tokens,e.cache_write_1h_tokens,e.reasoning_tokens,
		x.input_tokens,x.output_tokens,x.cache_read_tokens,x.cache_write_tokens,x.cache_write_5m_tokens,
		x.cache_write_1h_tokens,x.reasoning_tokens,e.cost_amount_micros,e.provider_cost_amount_micros
		FROM usage_events e JOIN effective_usage_events x ON x.event_id=e.event_id
		WHERE e.source_adapter='codex' AND e.source_id=? ORDER BY e.ingestion_key`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]recoveryStoredEvent)
	for rows.Next() {
		if len(result) >= CodexRecoveryMaxRecords {
			return nil, errors.New("source event count exceeds recovery limit")
		}
		var row recoveryStoredEvent
		if err := rows.Scan(&row.eventID, &row.ingestionKey, &row.schemaVersion, &row.eventType, &row.occurredAt, &row.startedAt, &row.completedAt,
			&row.projectID, &row.sessionID,
			&row.requestID, &row.attempt, &row.operation, &row.clientName, &row.clientVersion, &row.clientMode, &row.provider,
			&row.requestedModel, &row.resolvedModel, &row.adapter, &row.sourceID, &row.sourceRecord, &row.sourceOffset,
			&row.recordHash, &row.billingContext, &row.costStatus, &row.costCurrency, &row.pricingVersion, &row.providerCostCurrency,
			&row.base.InputTokens, &row.base.OutputTokens, &row.base.CacheReadTokens,
			&row.base.CacheWriteTokens, &row.base.CacheWrite5mTokens, &row.base.CacheWrite1hTokens, &row.base.ReasoningTokens,
			&row.effective.InputTokens, &row.effective.OutputTokens, &row.effective.CacheReadTokens, &row.effective.CacheWriteTokens,
			&row.effective.CacheWrite5mTokens, &row.effective.CacheWrite1hTokens, &row.effective.ReasoningTokens,
			&row.calculatedCost, &row.providerCost); err != nil {
			return nil, err
		}
		if _, duplicate := result[row.ingestionKey]; duplicate {
			return nil, errors.New("duplicate source ingestion key")
		}
		result[row.ingestionKey] = row
	}
	return result, rows.Err()
}

func buildCodexRecoveryPreview(namespaceKey []byte, snapshot sourceSnapshot, allocationRevision int64,
	events []UsageEvent, stored map[string]recoveryStoredEvent) (CodexRecoveryPreview, recoveryPlanBinding, error) {
	preview := CodexRecoveryPreview{FormatVersion: CodexRecoveryPreviewFormatVersion, AlgorithmVersion: CodexRecoveryAlgorithmVersion,
		ParserRevision: codexParserRevision, Generation: snapshot.generation, SnapshotBytes: snapshot.byteLength,
		Changes: []CodexRecoveryChange{}}
	plan := recoveryPlanBinding{Domain: "komposyn.codex-recovery.v1", NamespaceDigest: digest(namespaceKey),
		SourceID: snapshot.baseSourceID, Generation: snapshot.generation, SnapshotDigest: snapshot.fingerprint,
		CanonicalIdentity: snapshot.identityExpectation.CanonicalKey,
		SnapshotSize:      snapshot.byteLength, ParserRevision: codexParserRevision, AlgorithmVersion: CodexRecoveryAlgorithmVersion,
		CursorToken: sourceCursorToken(snapshot.expectedCursor), CheckpointToken: snapshot.expectedCheckpoint.token,
		AllocationRevision: allocationRevision, Events: []recoveryPlanEvent{}}
	candidates := make(map[string]UsageEvent, len(events))
	for _, event := range events {
		if _, exists := candidates[event.IngestionKey]; exists {
			return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonAmbiguousHistory}
		}
		candidates[event.IngestionKey] = event
	}
	if len(stored) > len(candidates) {
		return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonAmbiguousHistory}
	}
	preview.ExistingEventCount = len(stored)
	affectedSessions := map[string]struct{}{}
	affectedModels := map[string]struct{}{}
	var firstAffected, lastAffected *time.Time
	for key, row := range stored {
		event, exists := candidates[key]
		if !exists || !storedEventMatches(row, event) {
			return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonAttributionChanged}
		}
		if row.effective != recoveryVector(event.Usage) && (row.calculatedCost.Valid || row.providerCost.Valid ||
			row.costStatus == "calculated" || row.costStatus == "reported" || row.costCurrency != "" ||
			row.pricingVersion != "" || row.providerCostCurrency != "") {
			return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonMonetaryReconciliation}
		}
		if err := addRecoveryVector(&preview.Before, row.effective); err != nil {
			return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonAmbiguousHistory}
		}
		if err := addRecoveryVector(&preview.After, recoveryVector(event.Usage)); err != nil {
			return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonAmbiguousHistory}
		}
		kind := "unchanged"
		if row.effective != recoveryVector(event.Usage) {
			kind = "override"
			affectedSessions[row.sessionID] = struct{}{}
			affectedSessions[event.SessionID] = struct{}{}
			affectedModels[row.resolvedModel] = struct{}{}
			affectedModels[event.ResolvedModel] = struct{}{}
			updateRecoveryTimeExtent(&firstAffected, &lastAffected, event.OccurredAt)
			before := row.effective
			preview.OverriddenEventCount++
			preview.Changes = append(preview.Changes, CodexRecoveryChange{EventReference: recoveryOpaque(namespaceKey, "event", event.EventID), Kind: "override", Before: &before, After: recoveryVector(event.Usage)})
		}
		planEvent := recoveryPlanEventFromCandidate(event, key, row.base, row.effective, kind)
		planEvent.CostStatus, planEvent.CostAmount, planEvent.CostCurrency = row.costStatus, row.calculatedCost, row.costCurrency
		planEvent.PricingVersion, planEvent.ProviderCost, planEvent.ProviderCostCurrency = row.pricingVersion, row.providerCost, row.providerCostCurrency
		plan.Events = append(plan.Events, planEvent)
	}
	for key, event := range candidates {
		if _, exists := stored[key]; exists {
			continue
		}
		if err := addRecoveryVector(&preview.After, recoveryVector(event.Usage)); err != nil {
			return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonAmbiguousHistory}
		}
		preview.InsertedEventCount++
		affectedSessions[event.SessionID] = struct{}{}
		affectedModels[event.ResolvedModel] = struct{}{}
		updateRecoveryTimeExtent(&firstAffected, &lastAffected, event.OccurredAt)
		preview.Changes = append(preview.Changes, CodexRecoveryChange{EventReference: recoveryOpaque(namespaceKey, "event", event.EventID), Kind: "insert", After: recoveryVector(event.Usage)})
		plan.Events = append(plan.Events, recoveryPlanEventFromCandidate(event, key, recoveryVector(event.Usage), CodexRecoveryTokenVector{}, "insert"))
	}
	if len(preview.Changes) > CodexRecoveryMaxChanges {
		return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonLimitExceeded}
	}
	if len(preview.Changes) == 0 {
		return preview, plan, &CodexRecoveryError{Reason: CodexRecoveryReasonNoChanges}
	}
	preview.BeforeRequestCount = len(stored)
	preview.AfterRequestCount = len(events)
	preview.AffectedSessionCount = len(affectedSessions)
	preview.AffectedModelCount = len(affectedModels)
	sort.Slice(plan.Events, func(i, j int) bool { return plan.Events[i].EventID < plan.Events[j].EventID })
	sort.Slice(preview.Changes, func(i, j int) bool { return preview.Changes[i].EventReference < preview.Changes[j].EventReference })
	preview.FirstEventAt = firstAffected
	preview.LastEventAt = lastAffected
	preview.SourceReference = recoveryOpaque(namespaceKey, "source", snapshot.baseSourceID)
	preview.Generation = snapshot.generation
	return preview, plan, nil
}

func updateRecoveryTimeExtent(first, last **time.Time, value time.Time) {
	if *first == nil || value.Before(**first) {
		current := value
		*first = &current
	}
	if *last == nil || value.After(**last) {
		current := value
		*last = &current
	}
}

func recoveryPlanEventFromCandidate(event UsageEvent, key string, base, before CodexRecoveryTokenVector, kind string) recoveryPlanEvent {
	return recoveryPlanEvent{EventID: event.EventID, IngestionKey: key, SchemaVersion: event.SchemaVersion, EventType: event.EventType,
		SourceRecord: event.Provenance.SourceRecord, SourceOffset: nullableOffset(event.Provenance.SourceOffset), RecordHash: event.Provenance.RecordHash,
		OccurredAt: formatStoredTime(event.OccurredAt), StartedAt: recoveryTime(event.StartedAt), CompletedAt: recoveryTime(event.CompletedAt),
		ProjectID: event.ProjectID, SessionID: event.SessionID, RequestID: event.RequestID, Attempt: event.Attempt, Operation: event.Operation,
		Client: event.Client.Name, ClientVersion: event.Client.Version, ClientMode: event.Client.Mode, Provider: event.Provider,
		RequestedModel: event.RequestedModel, ResolvedModel: event.ResolvedModel, Adapter: event.Provenance.Adapter, SourceID: event.Provenance.SourceID,
		Billing: billingContextCanonical(event.BillingContext), CostStatus: event.Cost.Status, CostAmount: nullableCost(event.Cost.AmountMicros),
		CostCurrency: event.Cost.Currency, PricingVersion: event.Cost.PricingVersion, ProviderCost: nullableCost(event.Cost.ProviderAmount),
		ProviderCostCurrency: event.Cost.ProviderCurrency, Base: base, Before: before, After: recoveryVector(event.Usage), Kind: kind}
}

func storedEventMatches(row recoveryStoredEvent, event UsageEvent) bool {
	return storedEventMismatchField(row, event) == ""
}

// storedEventMismatchField names only a fixed schema field and never includes
// database or provider values. It keeps test diagnosis and safe refusal logic
// independent of arbitrary source text.
func storedEventMismatchField(row recoveryStoredEvent, event UsageEvent) string {
	if row.eventID != event.EventID {
		return "event_id"
	}
	if row.schemaVersion != event.SchemaVersion {
		return "schema_version"
	}
	if row.eventType != event.EventType {
		return "event_type"
	}
	if row.occurredAt != formatStoredTime(event.OccurredAt) {
		return "occurred_at"
	}
	if !nullableStoredTimeMatches(row.startedAt, event.StartedAt) {
		return "started_at"
	}
	if !nullableStoredTimeMatches(row.completedAt, event.CompletedAt) {
		return "completed_at"
	}
	if row.projectID != event.ProjectID {
		return "project_id"
	}
	if row.sessionID != event.SessionID {
		return "session_id"
	}
	if row.requestID != event.RequestID {
		return "request_id"
	}
	if row.attempt != event.Attempt {
		return "attempt"
	}
	if row.operation != event.Operation {
		return "operation"
	}
	if row.clientName != event.Client.Name {
		return "client_name"
	}
	if row.clientVersion != event.Client.Version {
		return "client_version"
	}
	if row.clientMode != event.Client.Mode {
		return "client_mode"
	}
	if row.provider != event.Provider {
		return "provider_name"
	}
	if row.requestedModel != event.RequestedModel {
		return "requested_model"
	}
	if row.resolvedModel != event.ResolvedModel {
		return "resolved_model"
	}
	if row.adapter != event.Provenance.Adapter {
		return "source_adapter"
	}
	if row.sourceID != event.Provenance.SourceID {
		return "source_id"
	}
	if row.sourceRecord != event.Provenance.SourceRecord {
		return "source_record_id"
	}
	offset := nullableOffset(event.Provenance.SourceOffset)
	if !row.sourceOffset.Valid || row.sourceOffset.Int64 != offset {
		return "source_offset"
	}
	if row.recordHash != event.Provenance.RecordHash {
		return "source_record_hash"
	}
	if row.billingContext != billingContextCanonical(event.BillingContext) {
		return "billing_context"
	}
	return ""
}

func nullableStoredTimeMatches(stored sql.NullString, value *time.Time) bool {
	if value == nil {
		return !stored.Valid
	}
	return stored.Valid && stored.String == formatStoredTime(*value)
}

func recoveryTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatStoredTime(*value)
}

func nullableCost(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func recoveryVector(u TokenUsage) CodexRecoveryTokenVector {
	return CodexRecoveryTokenVector{u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.CacheWrite5mTokens, u.CacheWrite1hTokens, u.ReasoningTokens}
}

func addRecoveryVector(total *CodexRecoveryTokenVector, value CodexRecoveryTokenVector) error {
	left := TokenUsage{InputTokens: total.InputTokens, OutputTokens: total.OutputTokens, CacheReadTokens: total.CacheReadTokens, CacheWriteTokens: total.CacheWriteTokens, CacheWrite5mTokens: total.CacheWrite5mTokens, CacheWrite1hTokens: total.CacheWrite1hTokens, ReasoningTokens: total.ReasoningTokens}
	sum, ok := addTokenUsage(left, TokenUsage{InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, CacheReadTokens: value.CacheReadTokens, CacheWriteTokens: value.CacheWriteTokens, CacheWrite5mTokens: value.CacheWrite5mTokens, CacheWrite1hTokens: value.CacheWrite1hTokens, ReasoningTokens: value.ReasoningTokens})
	if !ok {
		return errors.New("token total overflow")
	}
	*total = recoveryVector(sum)
	return nil
}

func recoveryOpaque(key []byte, domain, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("komposyn.codex-recovery." + domain + ".v1\x00" + value))
	return hex.EncodeToString(mac.Sum(nil))
}

func billingContextCanonical(value BillingContext) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func nullableOffset(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

func sourceCursorToken(expectation sourceCursorExpectation) string {
	encoded, _ := json.Marshal(struct {
		Exists                bool
		SourceID, Fingerprint string
		Offset                int64
		RecordID              string
		Generation            int64
		Pending, Reusable     bool
	}{expectation.exists, expectation.cursor.SourceID, expectation.cursor.FileFingerprint, expectation.cursor.ByteOffset, expectation.cursor.LastProviderRecordID, expectation.cursor.SourceGeneration, expectation.cursor.PendingTail, expectation.cursor.SnapshotReusable})
	return digest(encoded)
}
