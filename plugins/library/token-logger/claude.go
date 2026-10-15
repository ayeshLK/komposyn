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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ClaudeAdapter reads Claude Code transcript JSONL files without modifying
// provider-owned source files.
type ClaudeAdapter struct{}

type claudeEnvelope struct {
	Type         string          `json:"type"`
	UUID         string          `json:"uuid"`
	SessionID    string          `json:"sessionId"`
	SessionIDAlt string          `json:"session_id"`
	Timestamp    string          `json:"timestamp"`
	CWD          string          `json:"cwd"`
	ProjectID    string          `json:"project_id"`
	ProjectName  string          `json:"project_name"`
	Message      json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role  string       `json:"role"`
	Model string       `json:"model"`
	Usage *claudeUsage `json:"usage"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type claudeSession struct {
	ID          string
	ProjectID   string
	ProjectName string
	Root        string
	Model       string
}

// Discover returns Claude transcript JSONL files beneath a file or directory.
func (ClaudeAdapter) Discover(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var paths []string
	err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(candidate) == ".jsonl" {
			paths = append(paths, candidate)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

// DetectAdapter identifies the first recognized transcript shape.
func DetectAdapter(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return detectAdapterSnapshot(contents)
}

func detectAdapterSnapshot(contents []byte) (string, error) {
	reader := bufio.NewReader(bytes.NewReader(contents))
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		line := strings.TrimSpace(string(lineBytes))
		if line == "" {
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					break
				}
				return "", readErr
			}
			continue
		}
		var raw struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if raw.Type == "assistant" || len(raw.Message) > 0 {
			return "claude", nil
		}
		if raw.Type == "session_meta" || raw.Type == "event_msg" || raw.Type == "token_count" || raw.Type == "token_usage_record" {
			return "codex", nil
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", readErr
		}
	}
	return "codex", nil
}

// Import reads a single Claude transcript and atomically persists its events
// and source cursor.
func (ClaudeAdapter) Import(path string, store *Store) (ImportResult, error) {
	if store == nil {
		return ImportResult{}, errors.New("store is required")
	}
	return importClaudeSource(context.Background(), path, store, false)
}

func importClaudeSource(ctx context.Context, path string, store *Store, validateAdapter bool) (ImportResult, error) {
	snapshot, err := acquireSourceSnapshot(ctx, path, store, "claude")
	if err != nil {
		return ImportResult{}, fmt.Errorf("acquire Claude source: %w", err)
	}
	for attempt := 1; ; attempt++ {
		if validateAdapter {
			if err := validateSourceSnapshotAdapter(snapshot.contents, "claude"); err != nil {
				return ImportResult{}, err
			}
		}
		result, retry, err := importClaudeSnapshotAttempt(ctx, snapshot.contents, store, snapshot.baseSourceID, snapshot.expectedCursor)
		if err != nil || !retry {
			return result, err
		}
		snapshot, err = retrySourceSnapshotAfterCursorConflict(ctx, path, store, "claude", attempt)
		if err != nil {
			return ImportResult{}, fmt.Errorf("retry Claude source: %w", err)
		}
	}
}

func importClaudeSnapshotAttempt(ctx context.Context, contents []byte, store *Store, baseSourceID string, expectedCursor sourceCursorExpectation) (ImportResult, bool, error) {
	if unchanged, ok := unchangedExpectedSourceSnapshot(expectedCursor, contents); ok {
		return unchanged, false, nil
	}
	fingerprint := digest(contents)
	generation := sourceGeneration(expectedCursor, contents)
	sourceID := generationSourceID(baseSourceID, generation)
	result := ImportResult{}
	var offset int64
	var session claudeSession
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
		line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
		if len(strings.TrimSpace(string(line))) > 0 {
			var envelope claudeEnvelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				if errors.Is(readErr, io.EOF) && len(line) > 0 && isTruncatedJSON(err) {
					result.Outcomes.Add(OutcomeDeferredIncompleteTail)
				} else {
					result.Outcomes.Add(OutcomeMalformedRecord)
				}
			} else {
				if envelope.SessionID == "" {
					envelope.SessionID = envelope.SessionIDAlt
				}
				var message claudeMessage
				messageDecodeFailed := false
				usageShapeSupported := true
				if envelope.Type == "assistant" {
					decoded, messageErr := decodeClaudeMessage(envelope.Message)
					if messageErr != nil {
						result.Outcomes.Add(OutcomeMalformedRecord)
						messageDecodeFailed = true
						message = claudeMessage{}
					} else {
						message = decoded
						usageShapeSupported = claudeUsageShapeSupported(envelope.Message)
					}
				}
				session = updateClaudeSession(session, envelope, message, sourceID)
				if envelope.Type != "assistant" {
					if claudeUsageBearingType(envelope.Type) {
						result.Outcomes.Add(OutcomeUnsupportedUsage)
					} else {
						result.Outcomes.Add(OutcomeIgnoredNonUsage)
					}
				} else if !usageShapeSupported {
					result.Outcomes.Add(OutcomeUnsupportedUsage)
				} else if message.Usage == nil || message.Usage.InputTokens < 0 || message.Usage.OutputTokens < 0 ||
					message.Usage.CacheCreationInputTokens < 0 || message.Usage.CacheReadInputTokens < 0 || session.Model == "" {
					// Avoid double counting an assistant record whose message could not
					// be decoded; that failure was classified immediately above.
					if !messageDecodeFailed {
						result.Outcomes.Add(OutcomeMalformedRecord)
					}
				} else {
					recordID := envelope.UUID
					if recordID == "" {
						recordID = fmt.Sprintf("offset-%d", lineOffset)
					}
					lastRecordID = recordID
					occurredAt, timeErr := claudeTimestamp(envelope.Timestamp)
					if timeErr != nil {
						result.Outcomes.Add(OutcomeMalformedRecord)
					} else {
						events = append(events, claudeEvent(sourceID, fingerprint, lineOffset, recordID, session, *message.Usage, occurredAt, line))
					}
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return result, false, fmt.Errorf("read Claude source: %w", readErr)
			}
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return result, false, err
	}
	if session.ID == "" {
		session = updateClaudeSession(session, claudeEnvelope{}, claudeMessage{}, sourceID)
	}
	for index := range events {
		events[index].ProjectID = session.ProjectID
		events[index].SessionID = session.ID
	}
	receipt, err := store.appendWithReferencesAndCursorIfCurrent(events,
		[]Project{{ID: session.ProjectID, Root: session.Root, DisplayName: session.ProjectName, SourceAdapter: "claude-code", ProviderAlias: session.ProjectID, AliasProvenance: "claude-code metadata"}},
		[]Session{{ID: session.ID, ProjectID: session.ProjectID, Client: Client{Name: "claude-code"}, SourceMetadata: json.RawMessage(`{"adapter":"claude-code"}`), SourceAdapter: "claude-code", ProviderAlias: session.ID}},
		Cursor{SourceID: baseSourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID, SourceGeneration: generation,
			PendingTail: result.Outcomes.Pending(), SnapshotReusable: !result.Outcomes.Incomplete()}, expectedCursor)
	if errors.Is(err, errSourceCursorChanged) {
		return ImportResult{}, true, nil
	}
	if err != nil {
		return result, false, fmt.Errorf("persist Claude project, session, events, and cursor: %w", err)
	}
	result.Receipt = receipt
	return result, false, nil
}

func claudeUsageBearingType(recordType string) bool {
	recordType = strings.ToLower(recordType)
	return strings.Contains(recordType, "usage") || strings.Contains(recordType, "token")
}

// claudeUsageFields allowlists Claude usage field names, including newer
// fields (cache_creation, inference_geo, iterations, output_tokens_details,
// server_tool_use, speed) that carry no additional normalized token counts
// and are intentionally not captured beyond this shape check.
var claudeUsageFields = map[string]struct{}{
	"input_tokens": {}, "cache_creation_input_tokens": {}, "cache_read_input_tokens": {},
	"output_tokens": {}, "service_tier": {}, "cache_creation": {}, "inference_geo": {},
	"iterations": {}, "output_tokens_details": {}, "server_tool_use": {}, "speed": {},
}

func claudeUsageShapeSupported(raw json.RawMessage) bool {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil {
		return true
	}
	usageRaw, present := message["usage"]
	if !present || string(usageRaw) == "null" {
		return true
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(usageRaw, &usage) != nil {
		return true
	}
	if len(usage) == 0 {
		return false
	}
	return jsonObjectFieldsAllowed(usageRaw, claudeUsageFields)
}

func decodeClaudeMessage(raw json.RawMessage) (claudeMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return claudeMessage{}, errors.New("missing Claude message")
	}
	var message claudeMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return claudeMessage{}, err
	}
	return message, nil
}

func updateClaudeSession(session claudeSession, envelope claudeEnvelope, message claudeMessage, sourceID string) claudeSession {
	if envelope.SessionID != "" {
		session.ID = envelope.SessionID
	}
	if session.ID == "" {
		session.ID = sourceID + "-session"
	}
	if envelope.ProjectID != "" {
		session.ProjectID = envelope.ProjectID
	}
	if session.ProjectID == "" {
		session.ProjectID = sourceID + "-project"
	}
	if envelope.ProjectName != "" {
		session.ProjectName = envelope.ProjectName
	}
	if envelope.CWD != "" {
		session.Root = envelope.CWD
		if session.ProjectName == "" && filepath.Base(envelope.CWD) != "." {
			session.ProjectName = filepath.Base(envelope.CWD)
		}
	}
	if session.ProjectName == "" {
		if session.Root == "" && envelope.ProjectID == "" {
			session.ProjectName = "Unattributed"
		} else {
			session.ProjectName = "Imported Claude Code project"
		}
	}
	if message.Model != "" {
		session.Model = message.Model
	}
	return session
}

func claudeTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("missing timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func claudeEvent(sourceID, fingerprint string, offset int64, recordID string, session claudeSession, usage claudeUsage, occurredAt time.Time, line []byte) UsageEvent {
	key := digest([]byte(sourceID + "\x00" + recordID))
	offsetCopy := offset
	return UsageEvent{
		EventID: "claude-event-" + key, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		Client: Client{Name: "claude-code"}, Provider: "anthropic", RequestedModel: session.Model,
		ResolvedModel: session.Model,
		Usage: TokenUsage{InputTokens: usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
			OutputTokens: usage.OutputTokens, CacheReadTokens: usage.CacheReadInputTokens,
			CacheWriteTokens: usage.CacheCreationInputTokens},
		Provenance: Provenance{Adapter: "claude-code", SourceID: sourceID, SourceRecord: recordID,
			SourceOffset: &offsetCopy, RecordHash: digest(line)},
		IngestionKey: "claude:" + key,
		Attributes:   json.RawMessage(fmt.Sprintf(`{"source_fingerprint":%q}`, fingerprint)),
	}
}
