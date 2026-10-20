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
	InputTokens              int64                `json:"input_tokens"`
	CacheCreationInputTokens int64                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                `json:"cache_read_input_tokens"`
	OutputTokens             int64                `json:"output_tokens"`
	CacheCreation            *claudeCacheCreation `json:"cache_creation"`
	ServiceTier              *string              `json:"service_tier"`
	InferenceGeo             *string              `json:"inference_geo"`
	Speed                    *string              `json:"speed"`
}

type claudeCacheCreation struct {
	Ephemeral5mInputTokens *int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens *int64 `json:"ephemeral_1h_input_tokens"`
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
	return detectAdapterSnapshotObserved(contents, nil)
}

func detectAdapterSnapshotObserved(contents []byte, observer *collectorBenchmarkObserver) (string, error) {
	reader := bufio.NewReader(bytes.NewReader(contents))
	classifier := &adapterClassifier{}
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		classifier.observe(lineBytes, observer)
		if classifier.done {
			return classifier.adapter, nil
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", readErr
		}
	}
	return classifier.adapterName(), nil
}

type adapterClassifier struct {
	adapter string
	done    bool
}

func (c *adapterClassifier) observe(lineBytes []byte, observer *collectorBenchmarkObserver) {
	if c.done || len(strings.TrimSpace(string(lineBytes))) == 0 {
		return
	}
	if observer != nil {
		observer.recordsDecoded++
		observer.classificationRecordsDecoded++
	}
	var raw struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(lineBytes, &raw) != nil {
		return
	}
	if raw.Type == "assistant" || len(raw.Message) > 0 {
		c.adapter, c.done = "claude", true
		return
	}
	if raw.Type == "session_meta" || raw.Type == "event_msg" || raw.Type == "token_count" || raw.Type == "token_usage_record" {
		c.adapter, c.done = "codex", true
	}
}

func (c *adapterClassifier) adapterName() string {
	if c.adapter != "" {
		return c.adapter
	}
	return "codex"
}

// Import reads a single Claude transcript and atomically persists its events
// and source cursor.
func (ClaudeAdapter) Import(path string, store *Store) (ImportResult, error) {
	if store == nil {
		return ImportResult{}, errors.New("store is required")
	}
	return importClaudeSource(context.Background(), path, store)
}

func importClaudeSource(ctx context.Context, path string, store *Store) (ImportResult, error) {
	result, _, err := importStreamingSource(ctx, path, store, "claude")
	if err != nil {
		return result, fmt.Errorf("import Claude source: %w", err)
	}
	return result, nil
}

func importClaudeSnapshotAttempt(ctx context.Context, contents []byte, store *Store, baseSourceID string, expectedCursor sourceCursorExpectation) (ImportResult, bool, error) {
	if unchanged, ok := unchangedExpectedSourceSnapshotObserved(expectedCursor, contents, store.benchmarkObserver); ok {
		return unchanged, false, nil
	}
	fingerprint := digestSourceBytes(contents, store.benchmarkObserver)
	generation := sourceGenerationObserved(expectedCursor, contents, store.benchmarkObserver)
	return importClaudeReaderAttempt(ctx, bytes.NewReader(contents), store, baseSourceID, expectedCursor,
		fingerprint, generation, nil, nil)
}

func importClaudeReaderAttempt(ctx context.Context, source io.Reader, store *Store, baseSourceID string, expectedCursor sourceCursorExpectation,
	fingerprint string, generation int64, classifier *adapterClassifier, verify func(int64) error) (ImportResult, bool, error) {
	return importClaudeReaderAttemptWithCheckpoint(ctx, source, store, baseSourceID, expectedCursor, fingerprint, generation, classifier, verify, nil)
}

func importClaudeReaderContinuation(ctx context.Context, source io.Reader, store *Store, snapshot sourceSnapshot,
	classifier *adapterClassifier, verify func(int64) error) (ImportResult, bool, error) {
	options := &parserContinuationOptions{snapshot: snapshot, allowResume: snapshot.checkpointUsable}
	return importClaudeReaderAttemptWithCheckpoint(ctx, source, store, snapshot.baseSourceID, snapshot.expectedCursor,
		snapshot.fingerprint, snapshot.generation, classifier, verify, options)
}

func importClaudeReaderAttemptWithCheckpoint(ctx context.Context, source io.Reader, store *Store, baseSourceID string, expectedCursor sourceCursorExpectation,
	fingerprint string, generation int64, classifier *adapterClassifier, verify func(int64) error, options *parserContinuationOptions) (ImportResult, bool, error) {
	sourceID := generationSourceID(baseSourceID, generation)
	result := ImportResult{}
	var offset int64
	var session claudeSession
	var events []UsageEvent
	var lastRecordID string
	historicalAttempts := 0
	var boundaryState parserCheckpointState
	var boundaryCaptured bool
	if options != nil && options.allowResume {
		checkpoint := options.snapshot.checkpoint
		if checkpoint.Adapter != "claude" || checkpoint.BoundaryOffset > int64(^uint64(0)>>1) {
			return ImportResult{}, false, errors.New("incompatible Claude parser checkpoint")
		}
		state := checkpoint.State
		result.Outcomes = state.Outcomes
		session = state.Claude.Session
		lastRecordID = state.LastRecordID
		historicalAttempts = state.HistoricalEventAttempts
		boundaryState = state
		boundaryCaptured = true
	}
	boundaryOffset := int64(0)
	if options != nil {
		boundaryOffset = options.snapshot.checkpointBoundaryOffset
		if options.allowResume && boundaryOffset < options.snapshot.checkpoint.BoundaryOffset {
			boundaryOffset = options.snapshot.checkpoint.BoundaryOffset
		}
	}
	if options != nil && !options.allowResume && boundaryOffset == 0 {
		boundaryState = parserCheckpointState{Claude: &claudeCheckpointState{Session: session}}
		boundaryCaptured = true
	}
	reader := bufio.NewReaderSize(source, sourceVerificationBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return result, false, err
		}
		lineOffset := offset
		line, readErr := reader.ReadBytes('\n')
		if classifier != nil {
			classifier.observe(line, store.benchmarkObserver)
		}
		offset += int64(len(line))
		if options != nil && options.allowResume && offset <= options.snapshot.checkpoint.BoundaryOffset {
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					return result, false, fmt.Errorf("read Claude source prefix: %w", readErr)
				}
				break
			}
			continue
		}
		line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
		if len(strings.TrimSpace(string(line))) > 0 {
			if store.benchmarkObserver != nil {
				store.benchmarkObserver.recordsDecoded++
				store.benchmarkObserver.parserRecordsDecoded++
			}
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
					message.Usage.CacheCreationInputTokens < 0 || message.Usage.CacheReadInputTokens < 0 ||
					!claudeCacheDurationBreakdownValid(*message.Usage) || session.Model == "" {
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
						events = append(events, claudeEventObserved(sourceID, fingerprint, lineOffset, recordID, session, *message.Usage, occurredAt, line, store.benchmarkObserver))
					}
				}
			}
		}
		if offset == boundaryOffset {
			boundaryState = parserCheckpointState{Outcomes: result.Outcomes, LastRecordID: lastRecordID,
				HistoricalEventAttempts: historicalAttempts + len(events), Claude: &claudeCheckpointState{Session: session, PrefixEvents: historicalAttempts + len(events)}}
			boundaryCaptured = true
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
	if verify != nil {
		if err := verify(offset); err != nil {
			return result, true, err
		}
	}
	if options != nil && !boundaryCaptured {
		return ImportResult{}, true, errSourceSnapshotChanged
	}
	if options != nil && options.allowResume && !sameClaudeAttribution(options.snapshot.checkpoint.State.Claude.Session, session) {
		return ImportResult{}, false, errParserCheckpointFallback
	}
	if session.ID == "" {
		session = updateClaudeSession(session, claudeEnvelope{}, claudeMessage{}, sourceID)
	}
	if options != nil && !sameClaudeAttribution(boundaryState.Claude.Session, session) {
		boundaryCaptured = false
	}
	for index := range events {
		events[index].ProjectID = session.ProjectID
		events[index].SessionID = session.ID
	}
	var receipt Receipt
	var err error
	if options == nil {
		receipt, err = store.appendWithReferencesAndCursorIfCurrent(events,
			[]Project{{ID: session.ProjectID, Root: session.Root, DisplayName: session.ProjectName, SourceAdapter: "claude-code", ProviderAlias: session.ProjectID, AliasProvenance: "claude-code metadata"}},
			[]Session{{ID: session.ID, ProjectID: session.ProjectID, Client: Client{Name: "claude-code"}, SourceMetadata: json.RawMessage(`{"adapter":"claude-code"}`), SourceAdapter: "claude-code", ProviderAlias: session.ID}},
			Cursor{SourceID: baseSourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID, SourceGeneration: generation,
				PendingTail: result.Outcomes.Pending(), SnapshotReusable: !result.Outcomes.Incomplete()}, expectedCursor)
	} else {
		var checkpoint *parserCheckpoint
		if boundaryCaptured && !result.Outcomes.Incomplete() && boundaryState.Claude != nil {
			boundaryFingerprint := options.snapshot.boundaryFingerprint
			if stream, ok := source.(*sourceStreamReader); ok {
				boundaryFingerprint, _ = stream.prefixFingerprint(boundaryOffset)
			}
			if boundaryFingerprint != "" {
				candidate := parserCheckpoint{SourceID: baseSourceID, Adapter: "claude", SourceGeneration: generation,
					BoundaryOffset: boundaryOffset, PrefixFingerprint: boundaryFingerprint, State: boundaryState}
				if encoded, ok := encodeParserCheckpoint(candidate); ok {
					checkpoint = &encoded
				}
			}
		}
		receipt, err = store.appendWithParserStateIfCurrent(events,
			[]Project{{ID: session.ProjectID, Root: session.Root, DisplayName: session.ProjectName, SourceAdapter: "claude-code", ProviderAlias: session.ProjectID, AliasProvenance: "claude-code metadata"}},
			[]Session{{ID: session.ID, ProjectID: session.ProjectID, Client: Client{Name: "claude-code"}, SourceMetadata: json.RawMessage(`{"adapter":"claude-code"}`), SourceAdapter: "claude-code", ProviderAlias: session.ID}},
			Cursor{SourceID: baseSourceID, FileFingerprint: fingerprint, ByteOffset: offset, LastProviderRecordID: lastRecordID, SourceGeneration: generation,
				PendingTail: result.Outcomes.Pending(), SnapshotReusable: !result.Outcomes.Incomplete()}, expectedCursor,
			options.snapshot.expectedCheckpoint, checkpoint, options.snapshot.identityExpectation, ctx)
	}
	if options != nil && options.allowResume {
		receipt.Duplicates += options.snapshot.checkpoint.State.HistoricalEventAttempts
	}
	if errors.Is(err, errSourceCursorChanged) {
		return ImportResult{}, true, nil
	}
	if err != nil {
		return result, false, fmt.Errorf("persist Claude project, session, events, and cursor: %w", err)
	}
	result.Receipt = receipt
	return result, false, nil
}

func sameClaudeAttribution(a, b claudeSession) bool {
	return a.ID == b.ID && a.ProjectID == b.ProjectID && a.ProjectName == b.ProjectName && a.Root == b.Root && a.Model == b.Model
}

func claudeUsageBearingType(recordType string) bool {
	recordType = strings.ToLower(recordType)
	return strings.Contains(recordType, "usage") || strings.Contains(recordType, "token")
}

// claudeUsageFields allowlists Claude usage fields. Cache duration splits and
// pricing selectors are normalized below; other recognized fields, including
// fallback_credit, are not persisted or used to alter provider token counts.
var claudeUsageFields = map[string]struct{}{
	"input_tokens": {}, "cache_creation_input_tokens": {}, "cache_read_input_tokens": {},
	"output_tokens": {}, "service_tier": {}, "cache_creation": {}, "inference_geo": {},
	"fallback_credit": {}, "iterations": {}, "output_tokens_details": {}, "server_tool_use": {}, "speed": {},
}

var claudeCacheCreationFields = map[string]struct{}{
	"ephemeral_5m_input_tokens": {}, "ephemeral_1h_input_tokens": {},
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
	if !jsonObjectFieldsAllowed(usageRaw, claudeUsageFields) {
		return false
	}
	var usageShape struct {
		CacheCreation json.RawMessage `json:"cache_creation"`
	}
	if err := json.Unmarshal(usageRaw, &usageShape); err != nil {
		return false
	}
	if len(usageShape.CacheCreation) > 0 && string(usageShape.CacheCreation) != "null" && !jsonObjectFieldsAllowed(usageShape.CacheCreation, claudeCacheCreationFields) {
		return false
	}
	return true
}

func claudeCacheDurationBreakdownValid(usage claudeUsage) bool {
	if usage.CacheCreation == nil {
		return true
	}
	fivePtr, onePtr := usage.CacheCreation.Ephemeral5mInputTokens, usage.CacheCreation.Ephemeral1hInputTokens
	five, one := int64(0), int64(0)
	if fivePtr != nil {
		five = *fivePtr
		if five < 0 || five > usage.CacheCreationInputTokens {
			return false
		}
	}
	if onePtr != nil {
		one = *onePtr
		if one < 0 || one > usage.CacheCreationInputTokens {
			return false
		}
	}
	if fivePtr == nil || onePtr == nil {
		return true
	}
	return one <= usage.CacheCreationInputTokens-five && five+one == usage.CacheCreationInputTokens
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
	return claudeEventObserved(sourceID, fingerprint, offset, recordID, session, usage, occurredAt, line, nil)
}

func claudeEventObserved(sourceID, fingerprint string, offset int64, recordID string, session claudeSession, usage claudeUsage, occurredAt time.Time, line []byte, observer *collectorBenchmarkObserver) UsageEvent {
	key := digest([]byte(sourceID + "\x00" + recordID))
	offsetCopy := offset
	var billing BillingContext
	if usage.ServiceTier != nil && *usage.ServiceTier != "" {
		billing.ServiceTier = &BillingContextValue{Value: *usage.ServiceTier, Evidence: BillingEvidenceProviderResponse}
	}
	if usage.InferenceGeo != nil && *usage.InferenceGeo != "" {
		billing.Region = &BillingContextValue{Value: *usage.InferenceGeo, Evidence: BillingEvidenceProviderResponse}
	}
	if usage.Speed != nil && *usage.Speed != "" {
		billing.Speed = &BillingContextValue{Value: *usage.Speed, Evidence: BillingEvidenceProviderResponse}
	}
	var cacheWrite5m, cacheWrite1h int64
	if usage.CacheCreation != nil && usage.CacheCreation.Ephemeral5mInputTokens != nil && usage.CacheCreation.Ephemeral1hInputTokens != nil {
		cacheWrite5m = *usage.CacheCreation.Ephemeral5mInputTokens
		cacheWrite1h = *usage.CacheCreation.Ephemeral1hInputTokens
	}
	return UsageEvent{
		EventID: "claude-event-" + key, SchemaVersion: 1, EventType: "model.request", OccurredAt: occurredAt,
		Client: Client{Name: "claude-code"}, Provider: "anthropic", RequestedModel: session.Model,
		ResolvedModel: session.Model,
		Usage: TokenUsage{InputTokens: usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
			OutputTokens: usage.OutputTokens, CacheReadTokens: usage.CacheReadInputTokens,
			CacheWriteTokens: usage.CacheCreationInputTokens, CacheWrite5mTokens: cacheWrite5m, CacheWrite1hTokens: cacheWrite1h},
		BillingContext: billing,
		Provenance: Provenance{Adapter: "claude-code", SourceID: sourceID, SourceRecord: recordID,
			SourceOffset: &offsetCopy, RecordHash: digestRecordBytes(line, observer)},
		IngestionKey: "claude:" + key,
		Attributes:   json.RawMessage(fmt.Sprintf(`{"source_fingerprint":%q}`, fingerprint)),
	}
}
