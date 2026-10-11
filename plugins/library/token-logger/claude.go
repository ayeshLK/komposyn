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
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	reader := bufio.NewScanner(file)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" {
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
		if raw.Type == "session_meta" || raw.Type == "event_msg" || raw.Type == "token_count" {
			return "codex", nil
		}
	}
	if err := reader.Err(); err != nil {
		return "", err
	}
	return "codex", nil
}

// Import reads a single Claude transcript and atomically persists its events
// and source cursor.
func (ClaudeAdapter) Import(path string, store *Store) (ImportResult, error) {
	if store == nil {
		return ImportResult{}, errors.New("store is required")
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("resolve Claude source: %w", err)
	}
	file, err := os.Open(canonicalPath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("open Claude source: %w", err)
	}
	defer file.Close()
	contents, err := os.ReadFile(canonicalPath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("fingerprint Claude source: %w", err)
	}
	sourceID := "claude-file-" + digest([]byte(canonicalPath))
	fingerprint := digest(contents)
	result := ImportResult{}
	var offset int64
	var session claudeSession
	var events []UsageEvent
	var lastRecordID string
	reader := bufio.NewReader(file)
	for {
		lineOffset := offset
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
		if len(strings.TrimSpace(string(line))) > 0 {
			var envelope claudeEnvelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				result.Skipped++
			} else {
				if envelope.SessionID == "" {
					envelope.SessionID = envelope.SessionIDAlt
				}
				var message claudeMessage
				if envelope.Type == "assistant" {
					decoded, messageErr := decodeClaudeMessage(envelope.Message)
					if messageErr != nil {
						result.Skipped++
						message = claudeMessage{}
					} else {
						message = decoded
					}
				}
				session = updateClaudeSession(session, envelope, message, sourceID)
				if envelope.Type != "assistant" || message.Usage == nil || message.Usage.InputTokens < 0 || message.Usage.OutputTokens < 0 {
					result.Skipped++
				} else {
					recordID := envelope.UUID
					if recordID == "" {
						recordID = fmt.Sprintf("offset-%d", lineOffset)
					}
					lastRecordID = recordID
					occurredAt, timeErr := claudeTimestamp(envelope.Timestamp)
					if timeErr != nil {
						result.Skipped++
					} else {
						events = append(events, claudeEvent(sourceID, fingerprint, lineOffset, recordID, session, *message.Usage, occurredAt, line))
					}
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return result, fmt.Errorf("read Claude source: %w", readErr)
			}
			break
		}
	}
	if session.ID == "" {
		session = updateClaudeSession(session, claudeEnvelope{}, claudeMessage{}, sourceID)
	}
	if err := store.UpsertProject(Project{ID: session.ProjectID, Root: session.Root, DisplayName: session.ProjectName}); err != nil {
		return result, fmt.Errorf("persist Claude project: %w", err)
	}
	if err := store.UpsertSession(Session{ID: session.ID, ProjectID: session.ProjectID, Client: Client{Name: "claude-code"}, SourceMetadata: json.RawMessage(`{"adapter":"claude-code"}`)}); err != nil {
		return result, fmt.Errorf("persist Claude session: %w", err)
	}
	for index := range events {
		events[index].ProjectID = session.ProjectID
		events[index].SessionID = session.ID
	}
	receipt, err := store.AppendWithCursor(events, Cursor{SourceID: sourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID})
	if err != nil {
		return result, fmt.Errorf("persist Claude events: %w", err)
	}
	result.Receipt = receipt
	return result, nil
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
		session.ProjectName = "Imported Claude Code project"
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
