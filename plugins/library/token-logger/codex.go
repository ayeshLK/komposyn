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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// CodexAdapter reads Codex CLI rollout JSONL files without modifying them.
type CodexAdapter struct{}

type codexEnvelope struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheReadInputTokens  int64 `json:"cache_read_input_tokens"`
	CacheWriteTokens      int64 `json:"cache_write_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

// UnmarshalJSON accepts the legacy cache_write_tokens field name alongside
// the cache_write_input_tokens alias observed in newer Codex transcripts.
func (u *codexUsage) UnmarshalJSON(data []byte) error {
	var aux struct {
		InputTokens           int64 `json:"input_tokens"`
		CachedInputTokens     int64 `json:"cached_input_tokens"`
		CacheReadInputTokens  int64 `json:"cache_read_input_tokens"`
		CacheWriteTokens      int64 `json:"cache_write_tokens"`
		CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
		ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*u = codexUsage{
		InputTokens:           aux.InputTokens,
		CachedInputTokens:     aux.CachedInputTokens,
		CacheReadInputTokens:  aux.CacheReadInputTokens,
		CacheWriteTokens:      aux.CacheWriteTokens,
		OutputTokens:          aux.OutputTokens,
		ReasoningOutputTokens: aux.ReasoningOutputTokens,
	}
	if aux.CacheWriteInputTokens != 0 {
		u.CacheWriteTokens = aux.CacheWriteInputTokens
	}
	return nil
}

type codexPayload struct {
	Type            string          `json:"type"`
	ID              string          `json:"id"`
	SessionID       string          `json:"session_id"`
	ProjectID       string          `json:"project_id"`
	ProjectName     string          `json:"project_name"`
	CWD             string          `json:"cwd"`
	Model           string          `json:"model"`
	Info            *codexTokenInfo `json:"info"`
	TotalTokenUsage *codexUsage     `json:"total_token_usage"`
}

type codexUsageRecordPayload struct {
	ThreadID         string          `json:"thread_id"`
	TurnID           string          `json:"turn_id"`
	SessionID        string          `json:"session_id"`
	RootTurnID       string          `json:"root_turn_id"`
	ResponseID       string          `json:"response_id"`
	Usage            json.RawMessage `json:"usage"`
	TurnTokenUsage   json.RawMessage `json:"turn_token_usage"`
	ThreadTokenUsage json.RawMessage `json:"thread_token_usage"`
}

type codexUsageValue struct {
	InputTokens          *int64 `json:"input_tokens"`
	CachedInputTokens    *int64 `json:"cached_input_tokens"`
	CacheReadInputTokens *int64 `json:"cache_read_input_tokens"`
	CacheWriteTokens     *int64 `json:"cache_write_tokens"`
	CacheWriteInput      *int64 `json:"cache_write_input_tokens"`
	OutputTokens         *int64 `json:"output_tokens"`
	ReasoningTokens      *int64 `json:"reasoning_output_tokens"`
	TotalTokens          *int64 `json:"total_tokens"`
}

type codexUsageRecordSeen struct {
	usage       codexUsage
	turnUsage   codexUsage
	threadUsage codexUsage
	hasTurn     bool
	hasThread   bool
}

var codexUsageRecordFields = map[string]struct{}{
	"thread_id": {}, "turn_id": {}, "session_id": {}, "root_turn_id": {},
	"response_id": {}, "usage": {}, "turn_token_usage": {}, "thread_token_usage": {},
}

var codexTokenUsageFields = map[string]struct{}{
	"input_tokens": {}, "cached_input_tokens": {}, "cache_read_input_tokens": {},
	"cache_write_tokens": {}, "cache_write_input_tokens": {}, "output_tokens": {},
	"reasoning_output_tokens": {}, "total_tokens": {},
}

type codexTokenInfo struct {
	TotalTokenUsage *codexUsage `json:"total_token_usage"`
}

type codexSession struct {
	ID                   string
	ProjectID            string
	HasExplicitProjectID bool
	ProjectName          string
	Root                 string
	Model                string
}

// Import reads a single Codex JSONL source and atomically persists its
// normalized events and final source cursor.
func (CodexAdapter) Import(path string, store *Store) (ImportResult, error) {
	if store == nil {
		return ImportResult{}, errors.New("store is required")
	}
	return importCodexSource(context.Background(), path, store)
}

func importCodexSource(ctx context.Context, path string, store *Store) (ImportResult, error) {
	snapshot, err := acquireSourceSnapshot(ctx, path, store, "codex")
	if err != nil {
		return ImportResult{}, fmt.Errorf("acquire Codex source: %w", err)
	}
	for attempt := 1; ; attempt++ {
		result, retry, err := importCodexSnapshotAttempt(ctx, snapshot.contents, store, snapshot.baseSourceID, snapshot.expectedCursor)
		if err != nil || !retry {
			return result, err
		}
		snapshot, err = retrySourceSnapshotAfterCursorConflict(ctx, path, store, "codex", attempt)
		if err != nil {
			return ImportResult{}, fmt.Errorf("retry Codex source: %w", err)
		}
	}
}

func importCodexSnapshotAttempt(ctx context.Context, contents []byte, store *Store, baseSourceID string, expectedCursor sourceCursorExpectation) (ImportResult, bool, error) {
	if unchanged, ok := unchangedExpectedSourceSnapshotObserved(expectedCursor, contents, store.benchmarkObserver); ok {
		return unchanged, false, nil
	}
	fingerprint := digestSourceBytes(contents, store.benchmarkObserver)
	generation := sourceGenerationObserved(expectedCursor, contents, store.benchmarkObserver)
	sourceID := generationSourceID(baseSourceID, generation)
	result := ImportResult{}
	var offset int64
	var current codexSession
	var activeSessionID string
	sessions := map[string]codexSession{}
	var metadataCurrent codexSession
	metadataSessions := map[string]codexSession{}
	legacyPrevious := map[string]codexUsage{}
	legacyHasPrevious := map[string]bool{}
	newStreamSessions := map[string]bool{}
	threadCumulative := map[string]codexUsage{}
	turnCumulative := map[string]codexUsage{}
	seenResponses := map[string]codexUsageRecordSeen{}
	var events []UsageEvent
	var lastRecordID string

	reader := bufio.NewReader(bytes.NewReader(contents))
	for {
		if err := ctx.Err(); err != nil {
			return result, false, err
		}
		lineOffset := offset
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		if len(line) > 0 {
			line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
		}
		if len(strings.TrimSpace(string(line))) > 0 {
			if store.benchmarkObserver != nil {
				store.benchmarkObserver.recordsDecoded++
			}
			recordID := "offset-" + fmt.Sprint(lineOffset)
			var envelope codexEnvelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				if errors.Is(readErr, io.EOF) && len(line) > 0 && isTruncatedJSON(err) {
					result.Outcomes.Add(OutcomeDeferredIncompleteTail)
				} else {
					result.Outcomes.Add(OutcomeMalformedRecord)
				}
			} else {
				if envelope.ID != "" {
					recordID = envelope.ID
				}
				lastRecordID = recordID
				if envelope.Type == "token_usage_record" {
					record, usage, turnUsage, threadUsage, status := decodeCodexUsageRecord(envelope)
					if status == codexUsageRecordUnsupported {
						result.Outcomes.Add(OutcomeUnsupportedUsage)
					} else if status == codexUsageRecordMalformed {
						result.Outcomes.Add(OutcomeMalformedRecord)
					} else {
						session, ok := codexUsageRecordSession(record, activeSessionID, metadataSessions)
						if !ok || session.Model == "" || session.ProjectID == "" || !session.HasExplicitProjectID {
							result.Outcomes.Add(OutcomeMalformedRecord)
						} else {
							if record.SessionID == "" {
								record.SessionID = session.ID
							}
							responseScope := codexLogicalScope(record.SessionID, record.ThreadID)
							responseIdentity := responseScope + "\x00" + record.ResponseID
							seen, repeated := seenResponses[responseIdentity]
							if repeated {
								hasTurn := len(record.TurnTokenUsage) > 0 && string(record.TurnTokenUsage) != "null"
								hasThread := len(record.ThreadTokenUsage) > 0 && string(record.ThreadTokenUsage) != "null"
								if seen.usage != usage || seen.hasTurn != hasTurn || seen.hasThread != hasThread ||
									(hasTurn && seen.turnUsage != turnUsage) || (hasThread && seen.threadUsage != threadUsage) {
									result.Outcomes.Add(OutcomeUnsupportedUsage)
									continue
								}
								occurredAt, timeErr := codexTimestamp(envelope.Timestamp)
								if timeErr != nil {
									result.Outcomes.Add(OutcomeMalformedRecord)
								} else {
									newStreamSessions[session.ID] = true
									events = append(events, codexUsageEventObserved(sourceID, fingerprint, lineOffset, record, session, usage, occurredAt, line, store.benchmarkObserver))
								}
								continue
							}
							if !codexCumulativeValid(record, usage, turnUsage, threadUsage, turnCumulative, threadCumulative) {
								result.Outcomes.Add(OutcomeUnsupportedUsage)
								codexResetCumulativeBaselines(record, turnUsage, threadUsage, turnCumulative, threadCumulative)
								continue
							}
							occurredAt, timeErr := codexTimestamp(envelope.Timestamp)
							if timeErr != nil {
								result.Outcomes.Add(OutcomeMalformedRecord)
							} else {
								logicalScope := codexLogicalScope(record.SessionID, record.ThreadID)
								newStreamSessions[session.ID] = true
								hasThread := len(record.ThreadTokenUsage) > 0 && string(record.ThreadTokenUsage) != "null"
								hasTurn := len(record.TurnTokenUsage) > 0 && string(record.TurnTokenUsage) != "null"
								if hasThread {
									threadCumulative[logicalScope] = threadUsage
								}
								if record.TurnID != "" && hasTurn {
									turnCumulative[session.ID+"\x00"+record.TurnID] = turnUsage
								}
								seenResponses[responseIdentity] = codexUsageRecordSeen{usage: usage, turnUsage: turnUsage, threadUsage: threadUsage, hasTurn: hasTurn, hasThread: hasThread}
								events = append(events, codexUsageEventObserved(sourceID, fingerprint, lineOffset, record, session, usage, occurredAt, line, store.benchmarkObserver))
							}
						}
					}
				} else {
					payload, payloadErr := decodeCodexPayload(envelope.Payload)
					if payloadErr != nil {
						if codexUsageBearingType(codexRawRecordType(envelope)) {
							result.Outcomes.Add(OutcomeMalformedRecord)
						} else {
							result.Outcomes.Add(OutcomeIgnoredNonUsage)
						}
					} else {
						switch codexRecordType(envelope, payload) {
						case "session_meta":
							current = updateCodexSession(current, payload, sourceID)
							if current.Model == "" {
								current.Model = payload.Model
							}
							metadataSessionID := payload.SessionID
							if metadataSessionID == "" {
								metadataSessionID = payload.ID
							}
							if metadataCurrent.ID != "" && metadataSessionID != "" && metadataCurrent.ID != metadataSessionID {
								metadataCurrent = codexSession{}
							}
							metadataCurrent = updateCodexSession(metadataCurrent, payload, sourceID)
							if metadataCurrent.Model == "" {
								metadataCurrent.Model = payload.Model
							}
							metadataSessions[metadataCurrent.ID] = metadataCurrent
							sessions[metadataCurrent.ID] = metadataCurrent
							activeSessionID = metadataCurrent.ID
							result.Outcomes.Add(OutcomeIgnoredNonUsage)
						case "turn_context":
							if payload.SessionID != "" {
								activeSessionID = payload.SessionID
							}
							if payload.SessionID != "" {
								if known, ok := sessions[payload.SessionID]; ok {
									current = known
								}
							}
							if payload.Model != "" {
								current.Model = payload.Model
							}
							if current.ID != "" {
								sessions[current.ID] = current
							}
							if payload.SessionID != "" {
								if known, ok := metadataSessions[payload.SessionID]; ok {
									metadataCurrent = known
								} else {
									metadataCurrent = codexSession{}
								}
							}
							if metadataCurrent.ID != "" {
								if payload.Model != "" {
									metadataCurrent.Model = payload.Model
								}
								metadataSessions[metadataCurrent.ID] = metadataCurrent
								sessions[metadataCurrent.ID] = metadataCurrent
							}
							result.Outcomes.Add(OutcomeIgnoredNonUsage)
						case "token_usage_record":
							result.Outcomes.Add(OutcomeUnsupportedUsage)
						case "token_count":
							if payload.SessionID != "" || payload.ProjectID != "" || payload.CWD != "" {
								current = updateCodexSession(current, payload, sourceID)
							}
							if payload.SessionID != "" {
								activeSessionID = payload.SessionID
							} else if current.ID != "" {
								activeSessionID = current.ID
							}
							if payload.Model != "" {
								current.Model = payload.Model
							}
							legacyScope := payload.SessionID
							if legacyScope == "" {
								legacyScope = current.ID
							}
							if current.ID != "" {
								if _, hasMetadata := metadataSessions[current.ID]; !hasMetadata {
									sessions[current.ID] = current
								}
							}
							if newStreamSessions[legacyScope] {
								result.Outcomes.Add(OutcomeIgnoredNonUsage)
								break
							}
							if !codexUsageShapeSupported(envelope) {
								result.Outcomes.Add(OutcomeUnsupportedUsage)
								break
							}
							usage, ok := codexCumulativeUsage(envelope, payload)
							if !ok || current.Model == "" {
								result.Outcomes.Add(OutcomeMalformedRecord)
								break
							}
							delta, changed := codexDelta(legacyPrevious[legacyScope], usage, legacyHasPrevious[legacyScope])
							legacyPrevious[legacyScope], legacyHasPrevious[legacyScope] = usage, true
							if !changed {
								result.Outcomes.Add(OutcomeUnchangedSnapshot)
								break
							}
							occurredAt, timeErr := codexTimestamp(envelope.Timestamp)
							if timeErr != nil {
								result.Outcomes.Add(OutcomeMalformedRecord)
								break
							}
							events = append(events, codexEventObserved(sourceID, fingerprint, lineOffset, recordID, current, delta, occurredAt, line, store.benchmarkObserver))
						default:
							if codexUsageBearingType(codexRecordType(envelope, payload)) {
								result.Outcomes.Add(OutcomeUnsupportedUsage)
							} else {
								result.Outcomes.Add(OutcomeIgnoredNonUsage)
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return result, false, fmt.Errorf("read Codex source: %w", readErr)
			}
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return result, false, err
	}

	if current.ID == "" {
		current = updateCodexSession(current, codexPayload{}, sourceID)
	}
	if current.ID != "" {
		if _, hasMetadata := metadataSessions[current.ID]; !hasMetadata {
			sessions[current.ID] = current
		}
	}
	projectsByID := map[string]Project{}
	sessionsByID := map[string]Session{}
	for _, session := range sessions {
		if session.ID == "" || session.ProjectID == "" {
			continue
		}
		projectsByID[session.ProjectID] = Project{ID: session.ProjectID, Root: session.Root, DisplayName: session.ProjectName, SourceAdapter: "codex", ProviderAlias: session.ProjectID, AliasProvenance: "codex metadata"}
		sessionsByID[session.ID] = Session{ID: session.ID, ProjectID: session.ProjectID, Client: Client{Name: "codex-cli"}, SourceMetadata: json.RawMessage(`{"adapter":"codex"}`), SourceAdapter: "codex", ProviderAlias: session.ID}
	}
	projects := make([]Project, 0, len(projectsByID))
	for _, project := range projectsByID {
		projects = append(projects, project)
	}
	knownSessions := make([]Session, 0, len(sessionsByID))
	for _, session := range sessionsByID {
		knownSessions = append(knownSessions, session)
	}
	receipt, err := store.appendWithReferencesAndCursorIfCurrent(events,
		projects,
		knownSessions,
		Cursor{SourceID: baseSourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID, SourceGeneration: generation,
			PendingTail: result.Outcomes.Pending(), SnapshotReusable: !result.Outcomes.Incomplete()}, expectedCursor)
	if errors.Is(err, errSourceCursorChanged) {
		return ImportResult{}, true, nil
	}
	if err != nil {
		return result, false, fmt.Errorf("persist Codex project, session, events, and cursor: %w", err)
	}
	result.Receipt = receipt
	return result, false, nil
}

func codexUsageBearingType(recordType string) bool {
	recordType = strings.ToLower(recordType)
	return recordType == "token_count" || recordType == "token_usage_record" ||
		strings.Contains(recordType, "token_usage")
}

func codexRawRecordType(envelope codexEnvelope) string {
	var payload struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(envelope.Payload, &payload) == nil && payload.Type != "" {
		return payload.Type
	}
	return envelope.Type
}

type codexUsageRecordStatus string

const (
	codexUsageRecordValid       codexUsageRecordStatus = "valid"
	codexUsageRecordUnsupported codexUsageRecordStatus = "unsupported"
	codexUsageRecordMalformed   codexUsageRecordStatus = "malformed"
)

func decodeCodexUsageRecord(envelope codexEnvelope) (codexUsageRecordPayload, codexUsage, codexUsage, codexUsage, codexUsageRecordStatus) {
	var record codexUsageRecordPayload
	if len(envelope.Payload) == 0 || string(envelope.Payload) == "null" {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, codexUsageRecordUnsupported
	}
	if err := json.Unmarshal(envelope.Payload, &record); err != nil {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, codexUsageRecordMalformed
	}
	if !jsonObjectFieldsAllowed(envelope.Payload, codexUsageRecordFields) {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, codexUsageRecordUnsupported
	}
	if strings.TrimSpace(record.ResponseID) == "" || (strings.TrimSpace(record.ThreadID) == "" && strings.TrimSpace(record.SessionID) == "") {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, codexUsageRecordMalformed
	}
	usage, usageStatus := decodeCodexTokenUsage(record.Usage)
	if usageStatus != codexUsageRecordValid {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, usageStatus
	}
	turnUsage, turnStatus := decodeOptionalCodexTokenUsage(record.TurnTokenUsage)
	if turnStatus != codexUsageRecordValid {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, turnStatus
	}
	threadUsage, threadStatus := decodeOptionalCodexTokenUsage(record.ThreadTokenUsage)
	if threadStatus != codexUsageRecordValid {
		return record, codexUsage{}, codexUsage{}, codexUsage{}, threadStatus
	}
	return record, usage, turnUsage, threadUsage, codexUsageRecordValid
}

func decodeOptionalCodexTokenUsage(raw json.RawMessage) (codexUsage, codexUsageRecordStatus) {
	if len(raw) == 0 || string(raw) == "null" {
		return codexUsage{}, codexUsageRecordValid
	}
	return decodeCodexTokenUsage(raw)
}

func decodeCodexTokenUsage(raw json.RawMessage) (codexUsage, codexUsageRecordStatus) {
	if len(raw) == 0 || string(raw) == "null" {
		return codexUsage{}, codexUsageRecordMalformed
	}
	if !jsonObjectFieldsAllowed(raw, codexTokenUsageFields) {
		return codexUsage{}, codexUsageRecordUnsupported
	}
	var value codexUsageValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return codexUsage{}, codexUsageRecordMalformed
	}
	if value.CacheWriteTokens != nil && value.CacheWriteInput != nil {
		return codexUsage{}, codexUsageRecordUnsupported
	}
	if value.CachedInputTokens != nil && value.CacheReadInputTokens != nil {
		return codexUsage{}, codexUsageRecordUnsupported
	}
	usage := codexUsage{}
	if value.InputTokens != nil {
		usage.InputTokens = *value.InputTokens
	}
	if value.CachedInputTokens != nil {
		usage.CachedInputTokens = *value.CachedInputTokens
	}
	if value.CacheReadInputTokens != nil {
		usage.CacheReadInputTokens = *value.CacheReadInputTokens
	}
	if value.CacheWriteTokens != nil {
		usage.CacheWriteTokens = *value.CacheWriteTokens
	}
	if value.CacheWriteInput != nil {
		usage.CacheWriteTokens = *value.CacheWriteInput
	}
	if value.OutputTokens != nil {
		usage.OutputTokens = *value.OutputTokens
	}
	if value.ReasoningTokens != nil {
		usage.ReasoningOutputTokens = *value.ReasoningTokens
	}
	if value.TotalTokens != nil {
		usageTotal := *value.TotalTokens
		if usageTotal < 0 || usageTotal != usage.InputTokens+usage.OutputTokens {
			return codexUsage{}, codexUsageRecordMalformed
		}
	}
	if usage.InputTokens < 0 || usage.CachedInputTokens < 0 || usage.CacheReadInputTokens < 0 ||
		usage.CacheWriteTokens < 0 || usage.OutputTokens < 0 || usage.ReasoningOutputTokens < 0 {
		return codexUsage{}, codexUsageRecordMalformed
	}
	if usage.CachedInputTokens > usage.InputTokens || usage.CacheReadInputTokens > usage.InputTokens {
		return codexUsage{}, codexUsageRecordMalformed
	}
	if value.InputTokens == nil && value.CachedInputTokens == nil && value.CacheReadInputTokens == nil &&
		value.CacheWriteTokens == nil && value.CacheWriteInput == nil && value.OutputTokens == nil && value.ReasoningTokens == nil {
		return codexUsage{}, codexUsageRecordMalformed
	}
	return usage, codexUsageRecordValid
}

func codexLogicalScope(sessionID, threadID string) string {
	if strings.TrimSpace(threadID) != "" {
		return sessionID + "\x00" + threadID
	}
	return sessionID
}

func codexUsageRecordSession(record codexUsageRecordPayload, activeSessionID string, sessions map[string]codexSession) (codexSession, bool) {
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		sessionID = activeSessionID
	}
	if sessionID == "" {
		return codexSession{}, false
	}
	if session, ok := sessions[sessionID]; ok {
		return session, true
	}
	return codexSession{}, false
}

func codexCumulativeValid(record codexUsageRecordPayload, usage, turnUsage, threadUsage codexUsage, turnCumulative, threadCumulative map[string]codexUsage) bool {
	if len(record.ThreadTokenUsage) > 0 && string(record.ThreadTokenUsage) != "null" {
		threadScope := codexLogicalScope(record.SessionID, record.ThreadID)
		if previous, ok := threadCumulative[threadScope]; ok {
			if codexCumulativeRegressed(previous, threadUsage) || !codexCumulativeDeltaMatches(previous, threadUsage, usage) {
				return false
			}
		} else if !codexCumulativeCovers(threadUsage, usage) {
			return false
		}
	}
	if len(record.TurnTokenUsage) > 0 && string(record.TurnTokenUsage) != "null" && record.TurnID != "" {
		turnScope := record.SessionID + "\x00" + record.TurnID
		if previous, ok := turnCumulative[turnScope]; ok {
			if codexCumulativeRegressed(previous, turnUsage) || !codexCumulativeDeltaMatches(previous, turnUsage, usage) {
				return false
			}
		} else if !codexCumulativeCovers(turnUsage, usage) {
			return false
		}
	}
	return true
}

func codexCumulativeCovers(cumulative, usage codexUsage) bool {
	return cumulative.InputTokens >= usage.InputTokens && cumulative.OutputTokens >= usage.OutputTokens &&
		cumulative.CachedInputTokens >= usage.CachedInputTokens && cumulative.CacheReadInputTokens >= usage.CacheReadInputTokens &&
		cumulative.CacheWriteTokens >= usage.CacheWriteTokens && cumulative.ReasoningOutputTokens >= usage.ReasoningOutputTokens
}

func codexCumulativeDeltaMatches(previous, current, usage codexUsage) bool {
	return current.InputTokens-previous.InputTokens == usage.InputTokens &&
		current.OutputTokens-previous.OutputTokens == usage.OutputTokens &&
		current.CachedInputTokens-previous.CachedInputTokens == usage.CachedInputTokens &&
		current.CacheReadInputTokens-previous.CacheReadInputTokens == usage.CacheReadInputTokens &&
		current.CacheWriteTokens-previous.CacheWriteTokens == usage.CacheWriteTokens &&
		current.ReasoningOutputTokens-previous.ReasoningOutputTokens == usage.ReasoningOutputTokens
}

func codexResetCumulativeBaselines(record codexUsageRecordPayload, turnUsage, threadUsage codexUsage, turnCumulative, threadCumulative map[string]codexUsage) {
	if len(record.ThreadTokenUsage) > 0 && string(record.ThreadTokenUsage) != "null" {
		threadCumulative[codexLogicalScope(record.SessionID, record.ThreadID)] = threadUsage
	}
	if record.TurnID != "" && len(record.TurnTokenUsage) > 0 && string(record.TurnTokenUsage) != "null" {
		turnCumulative[record.SessionID+"\x00"+record.TurnID] = turnUsage
	}
}

func codexUsageEvent(sourceID, fingerprint string, offset int64, record codexUsageRecordPayload, session codexSession, usage codexUsage, occurredAt time.Time, line []byte) UsageEvent {
	return codexUsageEventObserved(sourceID, fingerprint, offset, record, session, usage, occurredAt, line, nil)
}

func codexUsageEventObserved(sourceID, fingerprint string, offset int64, record codexUsageRecordPayload, session codexSession, usage codexUsage, occurredAt time.Time, line []byte, observer *collectorBenchmarkObserver) UsageEvent {
	scope := codexLogicalScope(record.SessionID, record.ThreadID)
	identity := digest([]byte("codex-response\x00" + scope + "\x00" + record.ResponseID))
	offsetCopy := offset
	return UsageEvent{
		EventID: "codex-response-event-" + identity, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		ProjectID: session.ProjectID, SessionID: session.ID,
		Client: Client{Name: "codex-cli"}, Provider: "openai", RequestedModel: session.Model,
		ResolvedModel: session.Model,
		Usage: TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheReadTokens:  usage.CachedInputTokens + usage.CacheReadInputTokens,
			CacheWriteTokens: usage.CacheWriteTokens, ReasoningTokens: usage.ReasoningOutputTokens},
		Provenance: Provenance{Adapter: "codex", SourceID: sourceID, SourceRecord: record.ResponseID,
			SourceOffset: &offsetCopy, RecordHash: digestRecordBytes(line, observer)},
		IngestionKey: "codex:response:" + identity,
		Attributes:   json.RawMessage(fmt.Sprintf(`{"source_fingerprint":%q}`, fingerprint)),
	}
}

func codexCumulativeRegressed(previous, current codexUsage) bool {
	return current.InputTokens < previous.InputTokens || current.OutputTokens < previous.OutputTokens ||
		current.CachedInputTokens < previous.CachedInputTokens || current.CacheReadInputTokens < previous.CacheReadInputTokens ||
		current.CacheWriteTokens < previous.CacheWriteTokens || current.ReasoningOutputTokens < previous.ReasoningOutputTokens
}

func codexUsageShapeSupported(envelope codexEnvelope) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal(envelope.Payload, &payload) != nil {
		return true
	}
	_, hasInfo := payload["info"]
	_, hasTotalUsage := payload["total_token_usage"]
	if envelope.Type == "token_count" && !hasInfo && !hasTotalUsage {
		return jsonObjectFieldsAllowed(envelope.Payload, codexDirectUsageFields)
	}
	if !jsonObjectFieldsAllowed(envelope.Payload, codexTokenCountPayloadFields) {
		return false
	}
	if infoRaw, ok := payload["info"]; ok {
		var info map[string]json.RawMessage
		if json.Unmarshal(infoRaw, &info) == nil {
			if !jsonObjectFieldsAllowed(infoRaw, codexTokenInfoFields) {
				return false
			}
			for _, field := range []string{"total_token_usage", "last_token_usage"} {
				if usageRaw, found := info[field]; found && !jsonObjectFieldsAllowed(usageRaw, codexUsageFields) {
					return false
				}
			}
			return true
		}
	}
	if usageRaw, ok := payload["total_token_usage"]; ok {
		return jsonObjectFieldsAllowed(usageRaw, codexUsageFields)
	}
	return true
}

var codexUsageFields = map[string]struct{}{
	"input_tokens": {}, "cached_input_tokens": {}, "cache_read_input_tokens": {},
	"cache_write_tokens": {}, "cache_write_input_tokens": {}, "output_tokens": {},
	"reasoning_output_tokens": {}, "total_tokens": {},
}

var codexTokenInfoFields = map[string]struct{}{
	"total_token_usage": {}, "last_token_usage": {}, "model_context_window": {},
}

var codexTokenCountPayloadFields = map[string]struct{}{
	"type": {}, "id": {}, "session_id": {}, "project_id": {}, "project_name": {},
	"cwd": {}, "model": {}, "info": {}, "total_token_usage": {}, "rate_limits": {},
}

var codexDirectUsageFields = map[string]struct{}{
	"type": {}, "id": {}, "session_id": {}, "project_id": {}, "project_name": {},
	"cwd": {}, "model": {}, "input_tokens": {}, "cached_input_tokens": {},
	"cache_read_input_tokens": {}, "cache_write_tokens": {}, "cache_write_input_tokens": {},
	"output_tokens": {}, "reasoning_output_tokens": {}, "total_tokens": {},
}

func jsonObjectFieldsAllowed(raw json.RawMessage, allowed map[string]struct{}) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return true
	}
	for field := range object {
		if _, ok := allowed[field]; !ok {
			return false
		}
	}
	return true
}

func decodeCodexPayload(raw json.RawMessage) (codexPayload, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return codexPayload{}, errors.New("missing payload")
	}
	var payload codexPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return codexPayload{}, err
	}
	return payload, nil
}

func codexRecordType(envelope codexEnvelope, payload codexPayload) string {
	if payload.Type != "" {
		return payload.Type
	}
	return envelope.Type
}

// codexCWDProjectID derives a stable project identity from a normalized
// working directory for sessions that omit an explicit project_id.
func codexCWDProjectID(cwd string) string {
	normalized := filepath.Clean(strings.TrimSpace(cwd))
	if normalized == "" || normalized == "." {
		return ""
	}
	return "codex-cwd-" + digest([]byte(normalized))
}

func updateCodexSession(session codexSession, payload codexPayload, sourceID string) codexSession {
	if payload.SessionID != "" {
		session.ID = payload.SessionID
	}
	if payload.ID != "" && session.ID == "" {
		session.ID = payload.ID
	}
	if session.ID == "" {
		session.ID = sourceID + "-session"
	}
	if payload.ProjectID != "" {
		session.ProjectID = payload.ProjectID
		session.HasExplicitProjectID = true
	} else if session.ProjectID == "" {
		if derived := codexCWDProjectID(payload.CWD); derived != "" {
			session.ProjectID = derived
			session.HasExplicitProjectID = true
		}
	}
	if session.ProjectID == "" {
		session.ProjectID = sourceID + "-project"
	}
	if payload.ProjectName != "" {
		session.ProjectName = payload.ProjectName
	}
	if session.ProjectName == "" {
		if session.Root == "" && payload.ProjectID == "" {
			session.ProjectName = "Unattributed"
		} else {
			session.ProjectName = "Imported Codex project"
		}
	}
	if payload.CWD != "" {
		session.Root = payload.CWD
		if payload.ProjectName == "" && filepath.Base(payload.CWD) != "." && filepath.Base(payload.CWD) != string(filepath.Separator) {
			session.ProjectName = filepath.Base(payload.CWD)
		}
	}
	return session
}

func codexCumulativeUsage(envelope codexEnvelope, payload codexPayload) (codexUsage, bool) {
	if payload.Info != nil && payload.Info.TotalTokenUsage != nil {
		return *payload.Info.TotalTokenUsage, true
	}
	if payload.TotalTokenUsage != nil {
		return *payload.TotalTokenUsage, true
	}
	if envelope.Type == "token_count" {
		var usage codexUsage
		if err := json.Unmarshal(envelope.Payload, &usage); err == nil {
			return usage, true
		}
	}
	return codexUsage{}, false
}

func codexDelta(previous, current codexUsage, hasPrevious bool) (codexUsage, bool) {
	if !hasPrevious {
		return current, current != (codexUsage{})
	}
	if current.InputTokens < previous.InputTokens || current.OutputTokens < previous.OutputTokens || current.CachedInputTokens < previous.CachedInputTokens || current.CacheReadInputTokens < previous.CacheReadInputTokens || current.CacheWriteTokens < previous.CacheWriteTokens || current.ReasoningOutputTokens < previous.ReasoningOutputTokens {
		return current, current != (codexUsage{})
	}
	delta := codexUsage{
		InputTokens:           current.InputTokens - previous.InputTokens,
		CachedInputTokens:     current.CachedInputTokens - previous.CachedInputTokens,
		CacheReadInputTokens:  current.CacheReadInputTokens - previous.CacheReadInputTokens,
		CacheWriteTokens:      current.CacheWriteTokens - previous.CacheWriteTokens,
		OutputTokens:          current.OutputTokens - previous.OutputTokens,
		ReasoningOutputTokens: current.ReasoningOutputTokens - previous.ReasoningOutputTokens,
	}
	return delta, delta != (codexUsage{})
}

func codexTimestamp(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("missing timestamp")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return timestamp.UTC(), nil
}

func codexEvent(sourceID, fingerprint string, offset int64, recordID string, session codexSession, usage codexUsage, occurredAt time.Time, line []byte) UsageEvent {
	return codexEventObserved(sourceID, fingerprint, offset, recordID, session, usage, occurredAt, line, nil)
}

func codexEventObserved(sourceID, fingerprint string, offset int64, recordID string, session codexSession, usage codexUsage, occurredAt time.Time, line []byte, observer *collectorBenchmarkObserver) UsageEvent {
	key := digest([]byte(sourceID + "\x00" + recordID))
	offsetCopy := offset
	return UsageEvent{
		EventID: "codex-event-" + key, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		ProjectID: session.ProjectID, SessionID: session.ID,
		Client: Client{Name: "codex-cli"}, Provider: "openai", RequestedModel: session.Model,
		ResolvedModel: session.Model,
		Usage: TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheReadTokens:  usage.CachedInputTokens + usage.CacheReadInputTokens,
			CacheWriteTokens: usage.CacheWriteTokens, ReasoningTokens: usage.ReasoningOutputTokens},
		Provenance: Provenance{Adapter: "codex", SourceID: sourceID, SourceRecord: recordID,
			SourceOffset: &offsetCopy, RecordHash: digestRecordBytes(line, observer)},
		IngestionKey: "codex:" + key,
		Attributes:   json.RawMessage(fmt.Sprintf(`{"source_fingerprint":%q}`, fingerprint)),
	}
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
