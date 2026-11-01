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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"time"
)

// CodexRecoveryReceipt records the committed, idempotent result of one plan.
type CodexRecoveryReceipt struct {
	FormatVersion        int       `json:"format_version"`
	RunReference         string    `json:"run_reference"`
	PlanToken            string    `json:"plan_token"`
	InsertedEventCount   int       `json:"inserted_event_count"`
	OverriddenEventCount int       `json:"overridden_event_count"`
	CommittedAt          time.Time `json:"committed_at"`
}

type codexRecoveryAudit struct {
	FormatVersion        int                      `json:"format_version"`
	RunReference         string                   `json:"run_reference"`
	PlanToken            string                   `json:"plan_token"`
	SourceReference      string                   `json:"source_reference"`
	Generation           int64                    `json:"generation"`
	SnapshotBytes        int64                    `json:"snapshot_bytes"`
	ParserRevision       int                      `json:"parser_revision"`
	AlgorithmVersion     int                      `json:"algorithm_version"`
	InsertedEventCount   int                      `json:"inserted_event_count"`
	OverriddenEventCount int                      `json:"overridden_event_count"`
	BeforeRequestCount   int                      `json:"before_request_count"`
	AfterRequestCount    int                      `json:"after_request_count"`
	Before               CodexRecoveryTokenVector `json:"before"`
	After                CodexRecoveryTokenVector `json:"after"`
	Changes              []CodexRecoveryChange    `json:"changes"`
}

// ApplyCodexRecovery commits only the plan named by expectedPlanToken. The
// token binds the displayed evidence; it is not a consent or authorization
// credential. The selected source is explicitly read again before mutation.
func (s *Store) ApplyCodexRecovery(ctx context.Context, sourcePath, expectedPlanToken string) (CodexRecoveryReceipt, error) {
	refuse := func(reason string) (CodexRecoveryReceipt, error) {
		return CodexRecoveryReceipt{}, &CodexRecoveryError{Reason: reason}
	}
	if s == nil || s.db == nil || strings.TrimSpace(sourcePath) == "" {
		return refuse(CodexRecoveryReasonInvalidSelection)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tokenBytes, err := hex.DecodeString(expectedPlanToken)
	if err != nil || len(tokenBytes) != sha256Size || strings.ToLower(expectedPlanToken) != expectedPlanToken {
		return refuse(CodexRecoveryReasonInvalidPlanToken)
	}
	if receipt, found, err := recoveryReceipt(s.db, expectedPlanToken); err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	} else if found {
		return receipt, nil
	}
	if err := ctx.Err(); err != nil {
		return refuse(CodexRecoveryReasonCancelled)
	}
	prepared := &preparedCodexRecovery{}
	preview, err := s.prepareCodexRecovery(ctx, sourcePath, prepared)
	if prepared.snapshot.file != nil {
		defer prepared.snapshot.close()
	}
	if err != nil {
		// Another Store handle may have committed this exact plan after the
		// first receipt lookup but before preparation observed the database.
		// In that case preparation can correctly report that no work remains;
		// return the committed receipt instead of turning a successful
		// concurrent apply into a retry error.
		if receipt, found, receiptErr := recoveryReceipt(s.db, expectedPlanToken); receiptErr == nil && found {
			return receipt, nil
		}
		return CodexRecoveryReceipt{}, err
	}
	if !hmac.Equal([]byte(preview.PlanToken), []byte(expectedPlanToken)) {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	audit := codexRecoveryAudit{FormatVersion: 1, RunReference: recoveryOpaque(prepared.namespaceKey, "run", expectedPlanToken),
		PlanToken: expectedPlanToken, SourceReference: preview.SourceReference, Generation: preview.Generation,
		SnapshotBytes: preview.SnapshotBytes, ParserRevision: preview.ParserRevision, AlgorithmVersion: preview.AlgorithmVersion,
		InsertedEventCount: preview.InsertedEventCount, OverriddenEventCount: preview.OverriddenEventCount,
		BeforeRequestCount: preview.BeforeRequestCount, AfterRequestCount: preview.AfterRequestCount,
		Before: preview.Before, After: preview.After, Changes: preview.Changes}
	auditJSON, err := json.Marshal(audit)
	if err != nil || len(auditJSON) > CodexRecoveryMaxPlanBytes {
		return refuse(CodexRecoveryReasonLimitExceeded)
	}
	if err := ctx.Err(); err != nil {
		return refuse(CodexRecoveryReasonCancelled)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	defer tx.Rollback()
	if receipt, found, err := recoveryReceipt(tx, expectedPlanToken); err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	} else if found {
		return receipt, nil
	}
	if err := verifyRecoverySnapshotFile(ctx, sourcePath, prepared.snapshot); err != nil {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	if err := recoveryStoreStateMatches(tx, prepared.snapshot); err != nil {
		if errors.Is(err, ErrCollectorActive) {
			return refuse(CodexRecoveryReasonCollectorActive)
		}
		return refuse(CodexRecoveryReasonStalePreview)
	}
	revision, err := recoveryAllocationRevision(tx, prepared.snapshot.baseSourceID, prepared.snapshot.generation)
	if err != nil || revision != prepared.allocationRevision {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	providerEvents := append([]UsageEvent(nil), prepared.providerEvents...)
	if err := recoveryResolveExistingIdentities(tx, providerEvents); err != nil || !reflect.DeepEqual(providerEvents, prepared.events) {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	if err := recoveryCheckCandidateConflicts(tx, providerEvents); err != nil {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	stored, err := recoveryReadSourceEvents(tx, prepared.snapshot.baseSourceID, prepared.snapshot.generation)
	if err != nil || !reflect.DeepEqual(stored, prepared.stored) {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	_, binding, err := buildCodexRecoveryPreview(prepared.namespaceKey, prepared.snapshot, revision, providerEvents, stored)
	if err != nil {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	encoded, err := json.Marshal(binding)
	if err != nil || len(encoded) > CodexRecoveryMaxPlanBytes {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	mac := hmac.New(sha256.New, prepared.namespaceKey)
	_, _ = mac.Write(encoded)
	if !hmac.Equal(mac.Sum(nil), tokenBytes) || !hmac.Equal(encoded, prepared.bindingJSON) {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	if err := ctx.Err(); err != nil {
		return refuse(CodexRecoveryReasonCancelled)
	}
	committedAt := formatStoredTime(time.Now().UTC())
	runID := audit.RunReference
	if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_runs(run_id,plan_digest,format_version,source_id,source_generation,
		snapshot_digest,snapshot_size,inserted_event_count,overridden_event_count,audit_json,committed_at)
		VALUES(?,?,1,?,?,?,?,?,?,?,?)`, runID, expectedPlanToken, prepared.snapshot.baseSourceID,
		preview.Generation, prepared.snapshot.fingerprint, prepared.snapshot.byteLength, preview.InsertedEventCount,
		preview.OverriddenEventCount, auditJSON, committedAt); err != nil {
		return refuse(CodexRecoveryReasonApplyFailed)
	}
	for _, event := range prepared.events {
		if err := ctx.Err(); err != nil {
			return refuse(CodexRecoveryReasonCancelled)
		}
		old, exists := stored[event.IngestionKey]
		if !exists {
			if err := insertRecoveryUsageEvent(ctx, tx, event); err != nil {
				return refuse(CodexRecoveryReasonApplyFailed)
			}
			if _, err := insertRecoveryAllocation(ctx, tx, prepared.namespaceKey, runID, event.EventID, "inserted", nil,
				nil, recoveryVector(event.Usage), committedAt); err != nil {
				return refuse(CodexRecoveryReasonApplyFailed)
			}
			continue
		}
		if old.effective == recoveryVector(event.Usage) {
			continue
		}
		var previous sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT allocation_id FROM current_event_allocations WHERE event_id=?", event.EventID).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return refuse(CodexRecoveryReasonApplyFailed)
		}
		allocationID, err := insertRecoveryAllocation(ctx, tx, prepared.namespaceKey, runID, event.EventID, "override",
			recoveryNullableString(previous), &old.effective, recoveryVector(event.Usage), committedAt)
		if err != nil {
			return refuse(CodexRecoveryReasonApplyFailed)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO current_event_allocations(event_id,allocation_id) VALUES(?,?)
			ON CONFLICT(event_id) DO UPDATE SET allocation_id=excluded.allocation_id`, event.EventID, allocationID); err != nil {
			return refuse(CodexRecoveryReasonApplyFailed)
		}
	}
	if revision == math.MaxInt64 {
		return refuse(CodexRecoveryReasonApplyFailed)
	}
	newRevision := revision + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_allocation_revisions(source_id,source_generation,revision) VALUES(?,?,?)
		ON CONFLICT(source_id) DO UPDATE SET source_generation=excluded.source_generation,revision=excluded.revision`,
		prepared.snapshot.baseSourceID, preview.Generation, newRevision); err != nil {
		return refuse(CodexRecoveryReasonApplyFailed)
	}
	old := prepared.snapshot.expectedCursor.cursor
	updated, err := tx.ExecContext(ctx, `UPDATE source_cursors SET file_fingerprint=?,byte_offset=?,last_provider_record_id=?,
		pending_tail=0,snapshot_reusable=0,updated_at=? WHERE source_id=? AND file_fingerprint=? AND byte_offset=?
		AND last_provider_record_id=? AND source_generation=? AND pending_tail=? AND snapshot_reusable=?`,
		prepared.snapshot.fingerprint, prepared.snapshot.byteLength, prepared.lastRecordID, committedAt, old.SourceID,
		old.FileFingerprint, old.ByteOffset, old.LastProviderRecordID, old.SourceGeneration, old.PendingTail, old.SnapshotReusable)
	if err != nil {
		return refuse(CodexRecoveryReasonApplyFailed)
	}
	if rows, err := updated.RowsAffected(); err != nil || rows != 1 {
		return refuse(CodexRecoveryReasonStalePreview)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM parser_checkpoints WHERE source_id=?", old.SourceID); err != nil {
		return refuse(CodexRecoveryReasonApplyFailed)
	}
	if err := ctx.Err(); err != nil {
		return refuse(CodexRecoveryReasonCancelled)
	}
	if err := tx.Commit(); err != nil {
		return refuse(CodexRecoveryReasonApplyFailed)
	}
	committed, err := time.Parse(storedTimeLayout, committedAt)
	if err != nil {
		return refuse(CodexRecoveryReasonUnavailable)
	}
	return CodexRecoveryReceipt{FormatVersion: 1, RunReference: runID, PlanToken: expectedPlanToken,
		InsertedEventCount: preview.InsertedEventCount, OverriddenEventCount: preview.OverriddenEventCount, CommittedAt: committed}, nil
}

func recoveryReceipt(queryer allocationRevisionQueryer, planToken string) (CodexRecoveryReceipt, bool, error) {
	var receipt CodexRecoveryReceipt
	var committedAt string
	err := queryer.QueryRow(`SELECT run_id,plan_digest,inserted_event_count,overridden_event_count,committed_at
		FROM recovery_runs WHERE plan_digest=?`, planToken).Scan(&receipt.RunReference, &receipt.PlanToken,
		&receipt.InsertedEventCount, &receipt.OverriddenEventCount, &committedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexRecoveryReceipt{}, false, nil
	}
	if err != nil {
		return CodexRecoveryReceipt{}, false, err
	}
	parsed, err := time.Parse(storedTimeLayout, committedAt)
	if err != nil {
		return CodexRecoveryReceipt{}, false, err
	}
	receipt.FormatVersion, receipt.CommittedAt = 1, parsed
	return receipt, true, nil
}

func verifyRecoverySnapshotFile(ctx context.Context, path string, snapshot sourceSnapshot) error {
	if _, err := snapshot.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	stream := newSourceStreamReader(ctx, snapshot.file, nil, nil, nil, 1)
	if _, err := io.Copy(io.Discard, stream); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if stream.read != snapshot.byteLength || stream.fullFingerprint() != snapshot.fingerprint {
		return errSourceSnapshotChanged
	}
	current, err := collectorSourceMatchesOpenFile(path, snapshot.source, snapshot.openedInfo)
	if err != nil || !current {
		return errSourceSnapshotChanged
	}
	return nil
}

func insertRecoveryUsageEvent(ctx context.Context, tx *sql.Tx, event UsageEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	billingContextJSON, err := json.Marshal(event.BillingContext)
	if err != nil {
		return err
	}
	args := []any{event.EventID, event.SchemaVersion, event.EventType, formatStoredTime(event.OccurredAt), nullableTime(event.StartedAt), nullableTime(event.CompletedAt),
		event.ProjectID, event.SessionID, event.RequestID, event.Attempt, event.Operation, event.Client.Name, event.Client.Version, event.Client.Mode,
		event.Provider, event.RequestedModel, event.ResolvedModel, event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.CacheReadTokens,
		event.Usage.CacheWriteTokens, event.Usage.ReasoningTokens, event.Cost.Status, nullableInt64(event.Cost.AmountMicros), event.Cost.Currency,
		event.Cost.PricingVersion, nullableInt64(event.Cost.ProviderAmount), event.Cost.ProviderCurrency, event.Provenance.Adapter, event.Provenance.SourceID,
		event.Provenance.SourceRecord, nullableInt64(event.Provenance.SourceOffset), event.Provenance.RecordHash, event.IngestionKey, nullableJSON(event.Attributes),
		nullableJSON(billingContextJSON), event.Usage.CacheWrite5mTokens, event.Usage.CacheWrite1hTokens}
	result, err := tx.ExecContext(ctx, eventInsertSQL, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("recovery event insert was not unique")
	}
	return nil
}

func insertRecoveryAllocation(ctx context.Context, tx *sql.Tx, namespaceKey []byte, runID, eventID, kind string,
	previous *string, before *CodexRecoveryTokenVector, after CodexRecoveryTokenVector, createdAt string) (string, error) {
	allocationID := recoveryOpaque(namespaceKey, "allocation", runID+"\x00"+eventID)
	var beforeValues []any
	if before == nil {
		beforeValues = []any{nil, nil, nil, nil, nil, nil, nil}
	} else {
		beforeValues = []any{before.InputTokens, before.OutputTokens, before.CacheReadTokens, before.CacheWriteTokens, before.CacheWrite5mTokens, before.CacheWrite1hTokens, before.ReasoningTokens}
	}
	var previousValue any
	if previous != nil {
		previousValue = *previous
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO recovery_allocations(allocation_id,run_id,event_id,entry_kind,previous_allocation_id,
		before_input_tokens,before_output_tokens,before_cache_read_tokens,before_cache_write_tokens,before_cache_write_5m_tokens,
		before_cache_write_1h_tokens,before_reasoning_tokens,after_input_tokens,after_output_tokens,after_cache_read_tokens,
		after_cache_write_tokens,after_cache_write_5m_tokens,after_cache_write_1h_tokens,after_reasoning_tokens,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, allocationID, runID, eventID, kind, previousValue,
		beforeValues[0], beforeValues[1], beforeValues[2], beforeValues[3], beforeValues[4], beforeValues[5], beforeValues[6],
		after.InputTokens, after.OutputTokens, after.CacheReadTokens, after.CacheWriteTokens, after.CacheWrite5mTokens, after.CacheWrite1hTokens, after.ReasoningTokens, createdAt)
	return allocationID, err
}

func recoveryNullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

const sha256Size = 32
