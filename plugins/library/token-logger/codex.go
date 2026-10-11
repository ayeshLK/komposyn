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

// ImportResult summarizes one source import. Skipped records are unsupported,
// malformed, incomplete, or cumulative snapshots with no new usage delta.
type ImportResult struct {
	Receipt Receipt
	Skipped int
	Failed  int
}

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
	TotalTokenUsage codexUsage `json:"total_token_usage"`
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
	file, err := os.Open(canonicalPath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("open Codex source: %w", err)
	}
	defer file.Close()

	contents, err := os.ReadFile(canonicalPath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("fingerprint Codex source: %w", err)
	}
	sourceID := "codex-file-" + digest([]byte(canonicalPath))
	fingerprint := digest(contents)
	result := ImportResult{}
	var offset int64
	var current codexSession
	var previous codexUsage
	var hasPrevious bool
	var events []UsageEvent
	var lastRecordID string

	reader := bufio.NewReader(file)
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
				result.Skipped++
			} else {
				if envelope.ID != "" {
					recordID = envelope.ID
				}
				lastRecordID = recordID
				payload, payloadErr := decodeCodexPayload(envelope.Payload)
				if payloadErr != nil {
					result.Skipped++
				} else {
					switch codexRecordType(envelope, payload) {
					case "session_meta":
						current = updateCodexSession(current, payload, sourceID)
						if current.Model == "" {
							current.Model = payload.Model
						}
					case "turn_context":
						if payload.Model != "" {
							current.Model = payload.Model
						}
					case "token_count":
						if payload.SessionID != "" || payload.ProjectID != "" || payload.CWD != "" {
							current = updateCodexSession(current, payload, sourceID)
						}
						if payload.Model != "" {
							current.Model = payload.Model
						}
						usage, ok := codexCumulativeUsage(envelope, payload)
						if !ok || current.Model == "" {
							result.Skipped++
							break
						}
						delta, changed := codexDelta(previous, usage, hasPrevious)
						previous, hasPrevious = usage, true
						if !changed {
							result.Skipped++
							break
						}
						occurredAt, timeErr := codexTimestamp(envelope.Timestamp)
						if timeErr != nil {
							result.Skipped++
							break
						}
						events = append(events, codexEvent(sourceID, fingerprint, lineOffset, recordID, current, delta, occurredAt, line))
					default:
						result.Skipped++
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
	if err := store.UpsertProject(Project{ID: current.ProjectID, Root: current.Root, DisplayName: current.ProjectName}); err != nil {
		return result, fmt.Errorf("persist Codex project: %w", err)
	}
	if err := store.UpsertSession(Session{ID: current.ID, ProjectID: current.ProjectID, Client: Client{Name: "codex-cli"}, SourceMetadata: json.RawMessage(`{"adapter":"codex"}`)}); err != nil {
		return result, fmt.Errorf("persist Codex session: %w", err)
	}
	for index := range events {
		events[index].ProjectID = current.ProjectID
		events[index].SessionID = current.ID
	}
	receipt, err := store.AppendWithCursor(events, Cursor{SourceID: sourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID})
	if err != nil {
		return result, fmt.Errorf("persist Codex events: %w", err)
	}
	result.Receipt = receipt
	return result, nil
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
	if payload.Info != nil {
		return payload.Info.TotalTokenUsage, true
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
