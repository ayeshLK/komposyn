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
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	CodexRecoveryHistoryContractVersion   = 1
	DefaultCodexRecoveryHistoryPageSize   = 20
	MaxCodexRecoveryHistoryPageSize       = 50
	MaxCodexRecoveryHistoryTokenBytes     = 1024
	MaxCodexRecoveryHistoryResponseBytes  = 256 << 10
	MaxCodexRecoveryHistoryReferenceBytes = 64
)

type CodexRecoveryAllocationKind string

const (
	CodexRecoveryAllocationOverride CodexRecoveryAllocationKind = "override"
	CodexRecoveryAllocationInserted CodexRecoveryAllocationKind = "inserted"
)

var (
	ErrInvalidCodexRecoveryHistoryQuery = errors.New("invalid Codex recovery history query")
	ErrCodexRecoveryRunNotFound         = errors.New("Codex recovery run not found")
	ErrCodexRecoveryHistoryUnavailable  = errors.New("Codex recovery history unavailable")
)

// CodexRecoveryHistoryQuery selects a bounded page of committed recovery runs.
// PageSize zero selects the documented default.
type CodexRecoveryHistoryQuery struct {
	PageSize          int    `json:"page_size,omitempty"`
	ContinuationToken string `json:"continuation_token,omitempty"`
}

// CodexRecoveryRunSummary contains only normalized, committed run metadata.
// References are stable local identifiers, not anonymized values.
type CodexRecoveryRunSummary struct {
	RunReference         string    `json:"run_reference"`
	SourceReference      string    `json:"source_reference"`
	SourceGeneration     int64     `json:"source_generation"`
	SnapshotBytes        int64     `json:"snapshot_bytes"`
	FormatVersion        int       `json:"format_version"`
	InsertedEventCount   int64     `json:"inserted_event_count"`
	OverriddenEventCount int64     `json:"overridden_event_count"`
	CommittedAt          time.Time `json:"committed_at"`
}

// CodexRecoveryHistoryPage is one bounded page of committed recovery runs.
type CodexRecoveryHistoryPage struct {
	ContractVersion int                       `json:"contract_version"`
	Runs            []CodexRecoveryRunSummary `json:"runs"`
	NextToken       string                    `json:"next_token,omitempty"`
}

// CodexRecoveryInspectionRequest selects one run and a bounded allocation page.
// PageSize zero selects the documented default.
type CodexRecoveryInspectionRequest struct {
	RunReference      string `json:"run_reference"`
	PageSize          int    `json:"page_size,omitempty"`
	ContinuationToken string `json:"continuation_token,omitempty"`
}

// CodexRecoveryAllocationDetail separates immutable event evidence, the
// historical journal entry, and the current effective allocation.
type CodexRecoveryAllocationDetail struct {
	AllocationReference         string                      `json:"allocation_reference"`
	EventReference              string                      `json:"event_reference"`
	Kind                        CodexRecoveryAllocationKind `json:"kind"`
	PreviousAllocationReference string                      `json:"previous_allocation_reference,omitempty"`
	Original                    CodexRecoveryTokenVector    `json:"original"`
	Before                      *CodexRecoveryTokenVector   `json:"before,omitempty"`
	After                       CodexRecoveryTokenVector    `json:"after"`
	CurrentEffective            CodexRecoveryTokenVector    `json:"current_effective"`
	CurrentAllocationReference  string                      `json:"current_allocation_reference,omitempty"`
	IsCurrentAllocation         bool                        `json:"is_current_allocation"`
}

// CodexRecoveryInspection is a bounded page of normalized committed evidence.
// CurrentEffective is evaluated at EvidenceAsOf; historical journal values
// remain immutable across pages and later recoveries.
type CodexRecoveryInspection struct {
	ContractVersion int                             `json:"contract_version"`
	Run             CodexRecoveryRunSummary         `json:"run"`
	Allocations     []CodexRecoveryAllocationDetail `json:"allocations"`
	NextToken       string                          `json:"next_token,omitempty"`
	EvidenceAsOf    time.Time                       `json:"evidence_as_of"`
}

type codexRecoveryHistoryCursor struct {
	Version      int    `json:"v"`
	Purpose      string `json:"p"`
	QueryDigest  string `json:"q"`
	PageSize     int    `json:"s"`
	HighWaterRow int64  `json:"h,omitempty"`
	AfterRow     int64  `json:"a,omitempty"`
	RunReference string `json:"r,omitempty"`
	AfterEventID string `json:"e,omitempty"`
}

func recoveryHistoryQueryDigest(purpose string, pageSize int, runReference string) string {
	b, _ := json.Marshal(struct {
		Version      int    `json:"v"`
		Purpose      string `json:"p"`
		PageSize     int    `json:"s"`
		RunReference string `json:"r,omitempty"`
	}{CodexRecoveryHistoryContractVersion, purpose, pageSize, runReference})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func recoveryHistoryAEAD(namespaceKey []byte) (cipher.AEAD, error) {
	seed := append([]byte("komposyn.codex-recovery.history-token.v1\x00"), namespaceKey...)
	key := sha256.Sum256(seed)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealRecoveryHistoryCursor(namespaceKey []byte, cursor codexRecoveryHistoryCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	aead, err := recoveryHistoryAEAD(namespaceKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, data, []byte("codex-recovery-history-v1"))
	token := base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > MaxCodexRecoveryHistoryTokenBytes {
		return "", ErrInvalidCodexRecoveryHistoryQuery
	}
	return token, nil
}

func openRecoveryHistoryCursor(namespaceKey []byte, token, purpose string, pageSize int, runReference string) (codexRecoveryHistoryCursor, error) {
	var cursor codexRecoveryHistoryCursor
	if token == "" || len(token) > MaxCodexRecoveryHistoryTokenBytes {
		return cursor, ErrInvalidCodexRecoveryHistoryQuery
	}
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor, ErrInvalidCodexRecoveryHistoryQuery
	}
	aead, err := recoveryHistoryAEAD(namespaceKey)
	if err != nil || len(sealed) <= aead.NonceSize()+aead.Overhead() {
		return cursor, ErrInvalidCodexRecoveryHistoryQuery
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	data, err := aead.Open(nil, nonce, ciphertext, []byte("codex-recovery-history-v1"))
	if err != nil {
		return cursor, ErrInvalidCodexRecoveryHistoryQuery
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil {
		return cursor, ErrInvalidCodexRecoveryHistoryQuery
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return codexRecoveryHistoryCursor{}, ErrInvalidCodexRecoveryHistoryQuery
	}
	if cursor.Version != CodexRecoveryHistoryContractVersion || cursor.Purpose != purpose || cursor.PageSize != pageSize ||
		cursor.QueryDigest != recoveryHistoryQueryDigest(purpose, pageSize, runReference) || cursor.RunReference != runReference ||
		cursor.HighWaterRow < 0 || cursor.AfterRow < 0 || cursor.AfterRow > cursor.HighWaterRow || len(cursor.AfterEventID) > 256 {
		return codexRecoveryHistoryCursor{}, ErrInvalidCodexRecoveryHistoryQuery
	}
	return cursor, nil
}

func recoveryHistoryPageSize(size int) (int, error) {
	if size == 0 {
		return DefaultCodexRecoveryHistoryPageSize, nil
	}
	if size < 1 || size > MaxCodexRecoveryHistoryPageSize {
		return 0, ErrInvalidCodexRecoveryHistoryQuery
	}
	return size, nil
}

func validRecoveryHistoryReference(value string) bool {
	if len(value) != MaxCodexRecoveryHistoryReferenceBytes || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == MaxCodexRecoveryHistoryReferenceBytes/2
}

func readRecoveryHistoryNamespace(tx *sql.Tx) ([]byte, error) {
	var key []byte
	if err := tx.QueryRow("SELECT namespace_key FROM recovery_meta WHERE singleton=1 AND format_version=1").Scan(&key); err != nil || len(key) != 32 {
		return nil, ErrCodexRecoveryHistoryUnavailable
	}
	return key, nil
}

// ListCodexRecoveries returns committed recovery runs newest-first. The first
// page captures a high-water row so later commits do not enter its pagination
// walk. Start a new query to observe runs committed after that boundary.
func (s *Store) ListCodexRecoveries(query CodexRecoveryHistoryQuery) (CodexRecoveryHistoryPage, error) {
	pageSize, err := recoveryHistoryPageSize(query.PageSize)
	if err != nil || len(query.ContinuationToken) > MaxCodexRecoveryHistoryTokenBytes {
		return CodexRecoveryHistoryPage{}, ErrInvalidCodexRecoveryHistoryQuery
	}
	if query.ContinuationToken != "" {
		if _, err := base64.RawURLEncoding.DecodeString(query.ContinuationToken); err != nil {
			return CodexRecoveryHistoryPage{}, ErrInvalidCodexRecoveryHistoryQuery
		}
	}
	if s == nil || s.db == nil {
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	defer tx.Rollback()
	key, err := readRecoveryHistoryNamespace(tx)
	if err != nil {
		return CodexRecoveryHistoryPage{}, err
	}
	purpose := "list"
	cursor := codexRecoveryHistoryCursor{Version: CodexRecoveryHistoryContractVersion, Purpose: purpose,
		QueryDigest: recoveryHistoryQueryDigest(purpose, pageSize, ""), PageSize: pageSize}
	if query.ContinuationToken != "" {
		cursor, err = openRecoveryHistoryCursor(key, query.ContinuationToken, purpose, pageSize, "")
		if err != nil {
			return CodexRecoveryHistoryPage{}, err
		}
	} else if err := tx.QueryRow("SELECT COALESCE(MAX(rowid),0) FROM recovery_runs").Scan(&cursor.HighWaterRow); err != nil {
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	statement := "SELECT rowid,CASE WHEN length(run_id)=64 THEN run_id ELSE '' END,CASE WHEN length(plan_digest)=64 THEN plan_digest ELSE '' END,CASE WHEN length(source_id)<=512 THEN source_id ELSE '' END,source_generation,snapshot_size,format_version,inserted_event_count,overridden_event_count,committed_at FROM recovery_runs WHERE rowid<=?"
	args := []any{cursor.HighWaterRow}
	if cursor.AfterRow > 0 {
		statement += " AND rowid<?"
		args = append(args, cursor.AfterRow)
	}
	statement += " ORDER BY rowid DESC LIMIT ?"
	args = append(args, pageSize+1)
	rows, err := tx.Query(statement, args...)
	if err != nil {
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	page := CodexRecoveryHistoryPage{ContractVersion: CodexRecoveryHistoryContractVersion, Runs: make([]CodexRecoveryRunSummary, 0, pageSize)}
	lastReturnedRow := int64(0)
	hasMore := false
	for rows.Next() {
		var rowID, generation, snapshotBytes, inserted, overridden int64
		var runID, planDigest, sourceID, committedAt string
		var formatVersion int
		if err := rows.Scan(&rowID, &runID, &planDigest, &sourceID, &generation, &snapshotBytes, &formatVersion, &inserted, &overridden, &committedAt); err != nil {
			_ = rows.Close()
			return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
		}
		if len(page.Runs) == pageSize {
			hasMore = true
			break
		}
		summary, err := recoveryRunSummary(key, runID, planDigest, sourceID, generation, snapshotBytes, formatVersion, inserted, overridden, committedAt)
		if err != nil {
			_ = rows.Close()
			return CodexRecoveryHistoryPage{}, err
		}
		page.Runs = append(page.Runs, summary)
		lastReturnedRow = rowID
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	if err := rows.Close(); err != nil {
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	if hasMore {
		cursor.AfterRow = lastReturnedRow
		page.NextToken, err = sealRecoveryHistoryCursor(key, cursor)
		if err != nil {
			return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
		}
	}
	if err := recoveryHistoryResponseWithinLimit(page); err != nil {
		return CodexRecoveryHistoryPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return CodexRecoveryHistoryPage{}, ErrCodexRecoveryHistoryUnavailable
	}
	return page, nil
}

// InspectCodexRecovery returns a bounded page of normalized journal evidence
// for one committed run. It never reads the original provider source or audit
// JSON; current effective values reflect the read transaction's snapshot.
func (s *Store) InspectCodexRecovery(request CodexRecoveryInspectionRequest) (CodexRecoveryInspection, error) {
	pageSize, err := recoveryHistoryPageSize(request.PageSize)
	if err != nil {
		return CodexRecoveryInspection{}, ErrInvalidCodexRecoveryHistoryQuery
	}
	if !validRecoveryHistoryReference(request.RunReference) {
		return CodexRecoveryInspection{}, ErrCodexRecoveryRunNotFound
	}
	if len(request.ContinuationToken) > MaxCodexRecoveryHistoryTokenBytes {
		return CodexRecoveryInspection{}, ErrInvalidCodexRecoveryHistoryQuery
	}
	if request.ContinuationToken != "" {
		if _, err := base64.RawURLEncoding.DecodeString(request.ContinuationToken); err != nil {
			return CodexRecoveryInspection{}, ErrInvalidCodexRecoveryHistoryQuery
		}
	}
	if s == nil || s.db == nil {
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	defer tx.Rollback()
	key, err := readRecoveryHistoryNamespace(tx)
	if err != nil {
		return CodexRecoveryInspection{}, err
	}
	purpose := "inspect"
	cursor := codexRecoveryHistoryCursor{Version: CodexRecoveryHistoryContractVersion, Purpose: purpose,
		QueryDigest: recoveryHistoryQueryDigest(purpose, pageSize, request.RunReference), PageSize: pageSize,
		RunReference: request.RunReference}
	if request.ContinuationToken != "" {
		cursor, err = openRecoveryHistoryCursor(key, request.ContinuationToken, purpose, pageSize, request.RunReference)
		if err != nil {
			return CodexRecoveryInspection{}, err
		}
	}
	var sourceID, planDigest, committedAt string
	var generation, snapshotBytes, inserted, overridden int64
	var formatVersion int
	err = tx.QueryRow("SELECT CASE WHEN length(source_id)<=512 THEN source_id ELSE '' END,CASE WHEN length(plan_digest)=64 THEN plan_digest ELSE '' END,source_generation,snapshot_size,format_version,inserted_event_count,overridden_event_count,committed_at FROM recovery_runs WHERE run_id=?",
		request.RunReference).Scan(&sourceID, &planDigest, &generation, &snapshotBytes, &formatVersion, &inserted, &overridden, &committedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexRecoveryInspection{}, ErrCodexRecoveryRunNotFound
	}
	if err != nil {
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	page := CodexRecoveryInspection{ContractVersion: CodexRecoveryHistoryContractVersion,
		Allocations: make([]CodexRecoveryAllocationDetail, 0, pageSize)}
	page.Run, err = recoveryRunSummary(key, request.RunReference, planDigest, sourceID, generation, snapshotBytes, formatVersion, inserted, overridden, committedAt)
	if err != nil {
		return CodexRecoveryInspection{}, err
	}
	if err := validateRecoveryHistoryRunAllocations(tx, request.RunReference, inserted, overridden); err != nil {
		return CodexRecoveryInspection{}, err
	}
	page.EvidenceAsOf = time.Now().UTC()
	statement := `SELECT CASE WHEN length(a.allocation_id)=64 THEN a.allocation_id ELSE '' END,
		CASE WHEN length(a.event_id)<=256 THEN a.event_id ELSE '' END,a.entry_kind,
		CASE WHEN a.previous_allocation_id IS NULL THEN NULL WHEN length(a.previous_allocation_id)=64 THEN a.previous_allocation_id ELSE '' END,
		a.before_input_tokens,a.before_output_tokens,a.before_cache_read_tokens,a.before_cache_write_tokens,
		a.before_cache_write_5m_tokens,a.before_cache_write_1h_tokens,a.before_reasoning_tokens,
		a.after_input_tokens,a.after_output_tokens,a.after_cache_read_tokens,a.after_cache_write_tokens,
		a.after_cache_write_5m_tokens,a.after_cache_write_1h_tokens,a.after_reasoning_tokens,
		e.input_tokens,e.output_tokens,e.cache_read_tokens,e.cache_write_tokens,e.cache_write_5m_tokens,e.cache_write_1h_tokens,e.reasoning_tokens,
		v.input_tokens,v.output_tokens,v.cache_read_tokens,v.cache_write_tokens,v.cache_write_5m_tokens,v.cache_write_1h_tokens,v.reasoning_tokens,
		CASE WHEN h.allocation_id IS NULL THEN NULL WHEN length(h.allocation_id)=64 THEN h.allocation_id ELSE '' END
		FROM recovery_allocations AS a
		JOIN usage_events AS e ON e.event_id=a.event_id
		JOIN effective_usage_events AS v ON v.event_id=a.event_id
		LEFT JOIN current_event_allocations AS h ON h.event_id=a.event_id
		WHERE a.run_id=?`
	args := []any{request.RunReference}
	if cursor.AfterEventID != "" {
		statement += " AND a.event_id>?"
		args = append(args, cursor.AfterEventID)
	}
	statement += " ORDER BY a.event_id LIMIT ?"
	args = append(args, pageSize+1)
	rows, err := tx.Query(statement, args...)
	if err != nil {
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	lastReturnedID := ""
	hasMore := false
	for rows.Next() {
		var allocationID, eventID string
		var kind CodexRecoveryAllocationKind
		var previousID, currentID sql.NullString
		var before [7]sql.NullInt64
		var after [7]int64
		var original [7]int64
		var current [7]int64
		scanArgs := []any{&allocationID, &eventID, &kind, &previousID}
		for i := range before {
			scanArgs = append(scanArgs, &before[i])
		}
		for i := range after {
			scanArgs = append(scanArgs, &after[i])
		}
		for i := range original {
			scanArgs = append(scanArgs, &original[i])
		}
		for i := range current {
			scanArgs = append(scanArgs, &current[i])
		}
		scanArgs = append(scanArgs, &currentID)
		if err := rows.Scan(scanArgs...); err != nil {
			_ = rows.Close()
			return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
		}
		if len(page.Allocations) == pageSize {
			hasMore = true
			break
		}
		if !validRecoveryHistoryReference(allocationID) || eventID == "" || len(eventID) > 256 ||
			(kind != CodexRecoveryAllocationOverride && kind != CodexRecoveryAllocationInserted) {
			_ = rows.Close()
			return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
		}
		if err := validateRecoveryHistoryAllocation(tx, key, allocationID, eventID); err != nil {
			_ = rows.Close()
			return CodexRecoveryInspection{}, err
		}
		allocation := CodexRecoveryAllocationDetail{AllocationReference: recoveryOpaque(key, "allocation", allocationID),
			EventReference: recoveryOpaque(key, "event", eventID), Kind: kind,
			Original: recoveryVectorFromArray(original), After: recoveryVectorFromArray(after),
			CurrentEffective: recoveryVectorFromArray(current), IsCurrentAllocation: currentID.Valid && currentID.String == allocationID}
		if previousID.Valid {
			if !validRecoveryHistoryReference(previousID.String) {
				_ = rows.Close()
				return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
			}
			if err := validateRecoveryHistoryAllocation(tx, key, previousID.String, eventID); err != nil {
				_ = rows.Close()
				return CodexRecoveryInspection{}, err
			}
			allocation.PreviousAllocationReference = recoveryOpaque(key, "allocation", previousID.String)
		}
		if currentID.Valid {
			if !validRecoveryHistoryReference(currentID.String) {
				_ = rows.Close()
				return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
			}
			if err := validateRecoveryHistoryAllocation(tx, key, currentID.String, eventID); err != nil {
				_ = rows.Close()
				return CodexRecoveryInspection{}, err
			}
			allocation.CurrentAllocationReference = recoveryOpaque(key, "allocation", currentID.String)
		}
		beforePresent := 0
		for _, value := range before {
			if value.Valid {
				beforePresent++
			}
		}
		if kind == CodexRecoveryAllocationOverride {
			if beforePresent != len(before) {
				_ = rows.Close()
				return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
			}
			vector := recoveryVectorFromArray(nullableIntArray(before))
			allocation.Before = &vector
		} else if beforePresent != 0 || previousID.Valid {
			_ = rows.Close()
			return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
		}
		if kind == CodexRecoveryAllocationInserted && allocation.Original != allocation.After {
			_ = rows.Close()
			return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
		}
		if !validRecoveryHistoryVector(allocation.Original) || !validRecoveryHistoryVector(allocation.After) ||
			!validRecoveryHistoryVector(allocation.CurrentEffective) ||
			(allocation.Before != nil && !validRecoveryHistoryVector(*allocation.Before)) {
			_ = rows.Close()
			return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
		}
		page.Allocations = append(page.Allocations, allocation)
		lastReturnedID = eventID
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	if err := rows.Close(); err != nil {
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	if hasMore {
		cursor.AfterEventID = lastReturnedID
		page.NextToken, err = sealRecoveryHistoryCursor(key, cursor)
		if err != nil {
			return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
		}
	}
	if err := recoveryHistoryResponseWithinLimit(page); err != nil {
		return CodexRecoveryInspection{}, err
	}
	if err := tx.Commit(); err != nil {
		return CodexRecoveryInspection{}, ErrCodexRecoveryHistoryUnavailable
	}
	return page, nil
}

func recoveryRunSummary(key []byte, runID, planDigest, sourceID string, generation, snapshotBytes int64, formatVersion int,
	inserted, overridden int64, committedAt string) (CodexRecoveryRunSummary, error) {
	if !validRecoveryHistoryReference(runID) || !validRecoveryHistoryReference(planDigest) ||
		recoveryOpaque(key, "run", planDigest) != runID || sourceID == "" || generation < 0 || snapshotBytes < 0 ||
		formatVersion != 1 || inserted < 0 || overridden < 0 {
		return CodexRecoveryRunSummary{}, ErrCodexRecoveryHistoryUnavailable
	}
	committed, err := time.Parse(storedTimeLayout, committedAt)
	if err != nil {
		return CodexRecoveryRunSummary{}, ErrCodexRecoveryHistoryUnavailable
	}
	return CodexRecoveryRunSummary{RunReference: runID, SourceReference: recoveryOpaque(key, "source", sourceID),
		SourceGeneration: generation, SnapshotBytes: snapshotBytes, FormatVersion: formatVersion,
		InsertedEventCount: inserted, OverriddenEventCount: overridden, CommittedAt: committed.UTC()}, nil
}

func validateRecoveryHistoryAllocation(tx *sql.Tx, key []byte, allocationID, expectedEventID string) error {
	var eventID, runID, planDigest string
	err := tx.QueryRow(`SELECT CASE WHEN length(a.event_id)<=256 THEN a.event_id ELSE '' END,
		CASE WHEN length(a.run_id)=64 THEN a.run_id ELSE '' END,
		CASE WHEN length(r.plan_digest)=64 THEN r.plan_digest ELSE '' END
		FROM recovery_allocations AS a JOIN recovery_runs AS r ON r.run_id=a.run_id WHERE a.allocation_id=?`, allocationID).
		Scan(&eventID, &runID, &planDigest)
	if err != nil || eventID != expectedEventID || !validRecoveryHistoryReference(runID) || !validRecoveryHistoryReference(planDigest) ||
		recoveryOpaque(key, "run", planDigest) != runID || recoveryOpaque(key, "allocation", runID+"\x00"+eventID) != allocationID {
		return ErrCodexRecoveryHistoryUnavailable
	}
	return nil
}

func validateRecoveryHistoryRunAllocations(tx *sql.Tx, runReference string, inserted, overridden int64) error {
	var count, insertedCount, overriddenCount, eventCount int64
	if err := tx.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN entry_kind='inserted' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN entry_kind='override' THEN 1 ELSE 0 END),0)
		FROM recovery_allocations WHERE run_id=?`, runReference).Scan(&count, &insertedCount, &overriddenCount); err != nil {
		return ErrCodexRecoveryHistoryUnavailable
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM recovery_allocations AS a
		JOIN usage_events AS e ON e.event_id=a.event_id
		JOIN effective_usage_events AS v ON v.event_id=a.event_id WHERE a.run_id=?`, runReference).Scan(&eventCount); err != nil {
		return ErrCodexRecoveryHistoryUnavailable
	}
	if inserted < 0 || overridden < 0 || count < inserted || count-inserted != overridden ||
		insertedCount != inserted || overriddenCount != overridden || eventCount != count {
		return ErrCodexRecoveryHistoryUnavailable
	}
	return nil
}

func recoveryVectorFromArray(values [7]int64) CodexRecoveryTokenVector {
	return CodexRecoveryTokenVector{InputTokens: values[0], OutputTokens: values[1], CacheReadTokens: values[2],
		CacheWriteTokens: values[3], CacheWrite5mTokens: values[4], CacheWrite1hTokens: values[5], ReasoningTokens: values[6]}
}

func validRecoveryHistoryVector(vector CodexRecoveryTokenVector) bool {
	return vector.InputTokens >= 0 && vector.OutputTokens >= 0 && vector.CacheReadTokens >= 0 &&
		vector.CacheWriteTokens >= 0 && vector.CacheWrite5mTokens >= 0 && vector.CacheWrite1hTokens >= 0 && vector.ReasoningTokens >= 0
}

func nullableIntArray(values [7]sql.NullInt64) [7]int64 {
	var out [7]int64
	for i, value := range values {
		out[i] = value.Int64
	}
	return out
}

func recoveryHistoryResponseWithinLimit(value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxCodexRecoveryHistoryResponseBytes {
		return ErrCodexRecoveryHistoryUnavailable
	}
	return nil
}
