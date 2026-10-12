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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

type codexTokenInfo struct {
	TotalTokenUsage *codexUsage `json:"total_token_usage"`
}

type codexSession struct {
	ID          string
	ProjectID   string
	ProjectName string
	Root        string
	Model       string
}

// Import reads a single Codex JSONL source and atomically persists its
// normalized events and final source cursor.
func (CodexAdapter) Import(path string, store *Store) (ImportResult, error) {
	if store == nil {
		return ImportResult{}, errors.New("store is required")
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("resolve Codex source: %w", err)
	}
	contents, err := os.ReadFile(canonicalPath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("fingerprint Codex source: %w", err)
	}
	baseSourceID := "codex-file-" + digest([]byte(canonicalPath))
	fingerprint := digest(contents)
	generation, err := sourceGeneration(store, baseSourceID, contents)
	if err != nil {
		return ImportResult{}, fmt.Errorf("inspect Codex source cursor: %w", err)
	}
	sourceID := generationSourceID(baseSourceID, generation)
	result := ImportResult{}
	var offset int64
	var current codexSession
	var previous codexUsage
	var hasPrevious bool
	var events []UsageEvent
	var lastRecordID string

	reader := bufio.NewReader(bytes.NewReader(contents))
	for {
		lineOffset := offset
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		if len(line) > 0 {
			line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
		}
		if len(strings.TrimSpace(string(line))) > 0 {
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
					result.Outcomes.Add(OutcomeUnsupportedUsage)
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
							result.Outcomes.Add(OutcomeIgnoredNonUsage)
						case "turn_context":
							if payload.Model != "" {
								current.Model = payload.Model
							}
							result.Outcomes.Add(OutcomeIgnoredNonUsage)
						case "token_usage_record":
							result.Outcomes.Add(OutcomeUnsupportedUsage)
						case "token_count":
							if payload.SessionID != "" || payload.ProjectID != "" || payload.CWD != "" {
								current = updateCodexSession(current, payload, sourceID)
							}
							if payload.Model != "" {
								current.Model = payload.Model
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
							delta, changed := codexDelta(previous, usage, hasPrevious)
							previous, hasPrevious = usage, true
							if !changed {
								result.Outcomes.Add(OutcomeUnchangedSnapshot)
								break
							}
							occurredAt, timeErr := codexTimestamp(envelope.Timestamp)
							if timeErr != nil {
								result.Outcomes.Add(OutcomeMalformedRecord)
								break
							}
							events = append(events, codexEvent(sourceID, fingerprint, lineOffset, recordID, current, delta, occurredAt, line))
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
				return result, fmt.Errorf("read Codex source: %w", readErr)
			}
			break
		}
	}

	if current.ID == "" {
		current = updateCodexSession(current, codexPayload{}, sourceID)
	}
	for index := range events {
		events[index].ProjectID = current.ProjectID
		events[index].SessionID = current.ID
	}
	receipt, err := store.AppendWithReferencesAndCursor(events,
		[]Project{{ID: current.ProjectID, Root: current.Root, DisplayName: current.ProjectName}},
		[]Session{{ID: current.ID, ProjectID: current.ProjectID, Client: Client{Name: "codex-cli"}, SourceMetadata: json.RawMessage(`{"adapter":"codex"}`)}},
		Cursor{SourceID: baseSourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID, SourceGeneration: generation})
	if err != nil {
		return result, fmt.Errorf("persist Codex project, session, events, and cursor: %w", err)
	}
	result.Receipt = receipt
	return result, nil
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
	"cache_write_tokens": {}, "output_tokens": {}, "reasoning_output_tokens": {},
	"total_tokens": {},
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
	"cache_read_input_tokens": {}, "cache_write_tokens": {}, "output_tokens": {},
	"reasoning_output_tokens": {}, "total_tokens": {},
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
	}
	if session.ProjectID == "" {
		session.ProjectID = sourceID + "-project"
	}
	if payload.ProjectName != "" {
		session.ProjectName = payload.ProjectName
	}
	if session.ProjectName == "" {
		session.ProjectName = "Imported Codex project"
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
	key := digest([]byte(sourceID + "\x00" + recordID))
	offsetCopy := offset
	return UsageEvent{
		EventID: "codex-event-" + key, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		Client: Client{Name: "codex-cli"}, Provider: "openai", RequestedModel: session.Model,
		ResolvedModel: session.Model,
		Usage: TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheReadTokens:  usage.CachedInputTokens + usage.CacheReadInputTokens,
			CacheWriteTokens: usage.CacheWriteTokens, ReasoningTokens: usage.ReasoningOutputTokens},
		Provenance: Provenance{Adapter: "codex", SourceID: sourceID, SourceRecord: recordID,
			SourceOffset: &offsetCopy, RecordHash: digest(line)},
		IngestionKey: "codex:" + key,
		Attributes:   json.RawMessage(fmt.Sprintf(`{"source_fingerprint":%q}`, fingerprint)),
	}
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
