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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	parserCheckpointFormatVersion = 1
	claudeParserRevision          = 1
	// The Codex source classifier no longer treats an unrecognized source as
	// Codex. Invalidate checkpoints produced while that fallback was active so
	// a later recognized append gets a verified full replay under the new
	// classification contract.
	codexParserRevision          = 2
	maxCheckpointStateBytes      = 1 << 20
	maxCheckpointScopes          = 4096
	maxCheckpointResponses       = 16384
	maxCheckpointIdentifierBytes = 1024
)

var errParserCheckpointChanged = errors.New("parser checkpoint changed while source was being imported")
var errParserCheckpointFallback = errors.New("parser continuation requires verified full replay")

type sourceCheckpointExpectation struct {
	exists bool
	token  string
	row    parserCheckpoint
}

type collectorIdentityExpectation struct {
	CanonicalKey string
	Adapter      string
	SourceID     string
	Exists       bool
}

type parserCheckpoint struct {
	SourceID          string
	Adapter           string
	ParserRevision    int
	FormatVersion     int
	SourceGeneration  int64
	BoundaryOffset    int64
	PrefixFingerprint string
	StateJSON         []byte
	StateDigest       string
	State             parserCheckpointState
}

type parserCheckpointState struct {
	Outcomes                OutcomeCounts          `json:"outcomes"`
	HistoricalEventAttempts int                    `json:"historical_event_attempts"`
	LastRecordID            string                 `json:"last_record_id"`
	Claude                  *claudeCheckpointState `json:"claude,omitempty"`
	Codex                   *codexCheckpointState  `json:"codex,omitempty"`
}

type parserContinuationOptions struct {
	snapshot    sourceSnapshot
	allowResume bool
}

type claudeCheckpointState struct {
	Session      claudeSession `json:"session"`
	PrefixEvents int           `json:"prefix_events"`
}

type codexSeenResponseCheckpoint struct {
	Usage       codexUsage `json:"usage"`
	TurnUsage   codexUsage `json:"turn_usage"`
	ThreadUsage codexUsage `json:"thread_usage"`
	HasTurn     bool       `json:"has_turn"`
	HasThread   bool       `json:"has_thread"`
}

type codexCheckpointState struct {
	Current           codexSession                           `json:"current"`
	ActiveSessionID   string                                 `json:"active_session_id"`
	Sessions          map[string]codexSession                `json:"sessions"`
	MetadataCurrent   codexSession                           `json:"metadata_current"`
	MetadataSessions  map[string]codexSession                `json:"metadata_sessions"`
	LegacyPrevious    map[string]codexUsage                  `json:"legacy_previous"`
	LegacyHasPrevious map[string]bool                        `json:"legacy_has_previous"`
	NewStreamSessions map[string]bool                        `json:"new_stream_sessions"`
	ThreadCumulative  map[string]codexUsage                  `json:"thread_cumulative"`
	TurnCumulative    map[string]codexUsage                  `json:"turn_cumulative"`
	SeenResponses     map[string]codexSeenResponseCheckpoint `json:"seen_responses"`
	PrefixEvents      int                                    `json:"prefix_events"`
}

func parserRevision(adapter string) int {
	switch adapter {
	case "claude":
		return claudeParserRevision
	case "codex":
		return codexParserRevision
	default:
		return 0
	}
}

func encodeParserCheckpoint(checkpoint parserCheckpoint) (parserCheckpoint, bool) {
	if checkpoint.Adapter == "claude" {
		if checkpoint.State.Claude == nil || checkpoint.State.Codex != nil {
			return parserCheckpoint{}, false
		}
	} else if checkpoint.Adapter == "codex" {
		if checkpoint.State.Codex == nil || checkpoint.State.Claude != nil {
			return parserCheckpoint{}, false
		}
	} else {
		return parserCheckpoint{}, false
	}
	if !validateCheckpointState(checkpoint.Adapter, checkpoint.State) {
		return parserCheckpoint{}, false
	}
	data, err := json.Marshal(checkpoint.State)
	if err != nil || len(data) > maxCheckpointStateBytes {
		return parserCheckpoint{}, false
	}
	digest := sha256.Sum256(data)
	checkpoint.ParserRevision = parserRevision(checkpoint.Adapter)
	checkpoint.FormatVersion = parserCheckpointFormatVersion
	checkpoint.StateJSON = data
	checkpoint.StateDigest = hex.EncodeToString(digest[:])
	return checkpoint, true
}

func validateCheckpointState(adapter string, state parserCheckpointState) bool {
	if state.HistoricalEventAttempts < 0 || state.Outcomes.IgnoredNonUsage < 0 || state.Outcomes.UnchangedSnapshot < 0 ||
		state.Outcomes.DeferredIncompleteTail < 0 || state.Outcomes.UnsupportedUsage < 0 || state.Outcomes.MalformedRecord < 0 ||
		state.Outcomes.FailedSource < 0 {
		return false
	}
	if len(state.LastRecordID) > maxCheckpointIdentifierBytes {
		return false
	}
	if adapter == "claude" {
		return state.Claude != nil && state.Codex == nil && state.Claude.PrefixEvents >= 0 &&
			state.Claude.PrefixEvents == state.HistoricalEventAttempts && validClaudeSession(state.Claude.Session)
	}
	if adapter != "codex" || state.Codex == nil || state.Claude != nil {
		return false
	}
	c := state.Codex
	if c.PrefixEvents < 0 || c.PrefixEvents != state.HistoricalEventAttempts || len(c.Sessions) > maxCheckpointScopes ||
		len(c.MetadataSessions) > maxCheckpointScopes || len(c.SeenResponses) > maxCheckpointResponses ||
		len(c.Sessions)+len(c.MetadataSessions)+len(c.LegacyPrevious)+len(c.LegacyHasPrevious)+len(c.NewStreamSessions)+
			len(c.ThreadCumulative)+len(c.TurnCumulative) > maxCheckpointScopes {
		return false
	}
	if !validCodexSession(c.Current) || !validCodexSession(c.MetadataCurrent) || len(c.ActiveSessionID) > maxCheckpointIdentifierBytes {
		return false
	}
	for key, value := range c.Sessions {
		if len(key) > maxCheckpointIdentifierBytes || key != value.ID || !validCodexSession(value) {
			return false
		}
	}
	for key, value := range c.MetadataSessions {
		if len(key) > maxCheckpointIdentifierBytes || key != value.ID || !validCodexSession(value) {
			return false
		}
	}
	for key := range c.LegacyPrevious {
		if len(key) > maxCheckpointIdentifierBytes || !validCodexUsage(c.LegacyPrevious[key]) {
			return false
		}
	}
	for key := range c.LegacyHasPrevious {
		if len(key) > maxCheckpointIdentifierBytes {
			return false
		}
	}
	for key := range c.NewStreamSessions {
		if len(key) > maxCheckpointIdentifierBytes {
			return false
		}
	}
	for key := range c.ThreadCumulative {
		if len(key) > 2*maxCheckpointIdentifierBytes+1 || !validCodexUsage(c.ThreadCumulative[key]) {
			return false
		}
	}
	for key := range c.TurnCumulative {
		if len(key) > 2*maxCheckpointIdentifierBytes+1 || !validCodexUsage(c.TurnCumulative[key]) {
			return false
		}
	}
	for key := range c.SeenResponses {
		seen := c.SeenResponses[key]
		if len(key) > 3*maxCheckpointIdentifierBytes+2 || !validCodexUsage(seen.Usage) ||
			!validCodexUsage(seen.TurnUsage) || !validCodexUsage(seen.ThreadUsage) {
			return false
		}
	}
	return true
}

func validCodexUsage(usage codexUsage) bool {
	return usage.InputTokens >= 0 && usage.CachedInputTokens >= 0 && usage.CacheReadInputTokens >= 0 &&
		usage.CacheWriteTokens >= 0 && usage.OutputTokens >= 0 && usage.ReasoningOutputTokens >= 0
}

func validClaudeSession(session claudeSession) bool {
	return len(session.ID) <= maxCheckpointIdentifierBytes && len(session.ProjectID) <= maxCheckpointIdentifierBytes &&
		len(session.ProjectName) <= maxCheckpointIdentifierBytes && len(session.Root) <= 4*maxCheckpointIdentifierBytes &&
		len(session.Model) <= maxCheckpointIdentifierBytes
}

func validCodexSession(session codexSession) bool {
	return len(session.ID) <= maxCheckpointIdentifierBytes && len(session.ProjectID) <= maxCheckpointIdentifierBytes &&
		len(session.ProjectName) <= maxCheckpointIdentifierBytes && len(session.Root) <= 4*maxCheckpointIdentifierBytes &&
		len(session.Model) <= maxCheckpointIdentifierBytes
}

func cloneCodexMap[V any](input map[string]V) map[string]V {
	output := make(map[string]V, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func codexCheckpointSnapshot(current codexSession, activeSessionID string, sessions map[string]codexSession,
	metadataCurrent codexSession, metadataSessions map[string]codexSession, legacyPrevious map[string]codexUsage,
	legacyHasPrevious map[string]bool, newStreamSessions map[string]bool, threadCumulative, turnCumulative map[string]codexUsage,
	seenResponses map[string]codexUsageRecordSeen, prefixEvents int) *codexCheckpointState {
	seen := make(map[string]codexSeenResponseCheckpoint, len(seenResponses))
	for key, value := range seenResponses {
		seen[key] = codexSeenResponseCheckpoint{Usage: value.usage, TurnUsage: value.turnUsage, ThreadUsage: value.threadUsage, HasTurn: value.hasTurn, HasThread: value.hasThread}
	}
	return &codexCheckpointState{Current: current, ActiveSessionID: activeSessionID,
		Sessions: cloneCodexMap(sessions), MetadataCurrent: metadataCurrent, MetadataSessions: cloneCodexMap(metadataSessions),
		LegacyPrevious: cloneCodexMap(legacyPrevious), LegacyHasPrevious: cloneCodexMap(legacyHasPrevious),
		NewStreamSessions: cloneCodexMap(newStreamSessions), ThreadCumulative: cloneCodexMap(threadCumulative),
		TurnCumulative: cloneCodexMap(turnCumulative), SeenResponses: seen, PrefixEvents: prefixEvents}
}

func restoreCodexCheckpoint(state *codexCheckpointState) (codexSession, string, map[string]codexSession, codexSession,
	map[string]codexSession, map[string]codexUsage, map[string]bool, map[string]bool,
	map[string]codexUsage, map[string]codexUsage, map[string]codexUsageRecordSeen) {
	seen := make(map[string]codexUsageRecordSeen, len(state.SeenResponses))
	for key, value := range state.SeenResponses {
		seen[key] = codexUsageRecordSeen{usage: value.Usage, turnUsage: value.TurnUsage, threadUsage: value.ThreadUsage,
			hasTurn: value.HasTurn, hasThread: value.HasThread}
	}
	return state.Current, state.ActiveSessionID, cloneCodexMap(state.Sessions), state.MetadataCurrent,
		cloneCodexMap(state.MetadataSessions), cloneCodexMap(state.LegacyPrevious), cloneCodexMap(state.LegacyHasPrevious),
		cloneCodexMap(state.NewStreamSessions), cloneCodexMap(state.ThreadCumulative), cloneCodexMap(state.TurnCumulative), seen
}

func codexSessionAttributionEqual(a, b codexSession) bool {
	return a.ID == b.ID && a.ProjectID == b.ProjectID && a.HasExplicitProjectID == b.HasExplicitProjectID &&
		a.ProjectName == b.ProjectName && a.Root == b.Root && a.Model == b.Model
}

func codexAttributionCompatible(prefix, current *codexCheckpointState) bool {
	if prefix == nil || current == nil {
		return false
	}
	for id, previous := range prefix.Sessions {
		updated, ok := current.Sessions[id]
		if !ok || !codexSessionAttributionEqual(previous, updated) {
			return false
		}
	}
	for id := range current.Sessions {
		if _, existed := prefix.Sessions[id]; existed {
			continue
		}
		// A new scope can reconcile with project aliases and warning state
		// established by another source. Until that database-wide relationship
		// is proven safe, use verified full replay.
		return false
	}
	for id, previous := range prefix.MetadataSessions {
		updated, ok := current.MetadataSessions[id]
		if !ok || !codexSessionAttributionEqual(previous, updated) {
			return false
		}
	}
	if prefix.Current.ID != "" {
		if prefix.Current.ID == current.Current.ID {
			if !codexSessionAttributionEqual(prefix.Current, current.Current) {
				return false
			}
		} else if restored, ok := current.Sessions[prefix.Current.ID]; !ok || !codexSessionAttributionEqual(prefix.Current, restored) {
			return false
		}
	}
	if prefix.MetadataCurrent.ID != "" {
		if prefix.MetadataCurrent.ID == current.MetadataCurrent.ID {
			if !codexSessionAttributionEqual(prefix.MetadataCurrent, current.MetadataCurrent) {
				return false
			}
		} else if restored, ok := current.MetadataSessions[prefix.MetadataCurrent.ID]; !ok || !codexSessionAttributionEqual(prefix.MetadataCurrent, restored) {
			return false
		}
	}
	return true
}

func decodeParserCheckpoint(row parserCheckpoint) (parserCheckpoint, bool) {
	if row.SourceID == "" || len(row.SourceID) > maxCheckpointIdentifierBytes || row.Adapter == "" || row.ParserRevision != parserRevision(row.Adapter) ||
		row.FormatVersion != parserCheckpointFormatVersion || row.SourceGeneration < 0 || row.BoundaryOffset < 0 ||
		len(row.StateJSON) == 0 || len(row.StateJSON) > maxCheckpointStateBytes || len(row.PrefixFingerprint) != 64 {
		return parserCheckpoint{}, false
	}
	if _, err := hex.DecodeString(row.PrefixFingerprint); err != nil {
		return parserCheckpoint{}, false
	}
	actualDigest := sha256.Sum256(row.StateJSON)
	if !strings.EqualFold(hex.EncodeToString(actualDigest[:]), row.StateDigest) {
		return parserCheckpoint{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(string(row.StateJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&row.State); err != nil {
		return parserCheckpoint{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || !validateCheckpointState(row.Adapter, row.State) ||
		!checkpointAccountingFitsBoundary(row.State, row.BoundaryOffset) {
		return parserCheckpoint{}, false
	}
	row.StateJSON = append([]byte(nil), row.StateJSON...)
	return row, true
}

func checkpointAccountingFitsBoundary(state parserCheckpointState, boundary int64) bool {
	if boundary < 0 || int64(state.HistoricalEventAttempts) > boundary {
		return false
	}
	counts := []int{state.Outcomes.IgnoredNonUsage, state.Outcomes.UnchangedSnapshot,
		state.Outcomes.DeferredIncompleteTail, state.Outcomes.UnsupportedUsage,
		state.Outcomes.MalformedRecord, state.Outcomes.FailedSource}
	var total int64
	for _, count := range counts {
		if count < 0 || int64(count) > boundary-total {
			return false
		}
		total += int64(count)
	}
	return true
}

func checkpointRowToken(row parserCheckpoint) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%q\x00%q\x00%d\x00%d\x00%d\x00%d\x00%q\x00%q\x00", row.SourceID, row.Adapter,
		row.ParserRevision, row.FormatVersion, row.SourceGeneration, row.BoundaryOffset, row.PrefixFingerprint, row.StateDigest)
	_, _ = hash.Write(row.StateJSON)
	return hex.EncodeToString(hash.Sum(nil))
}

func parserCheckpointExpectation(store *Store, sourceID string) (sourceCheckpointExpectation, error) {
	row, err := store.parserCheckpoint(sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCheckpointExpectation{}, nil
	}
	if err != nil {
		return sourceCheckpointExpectation{}, err
	}
	return sourceCheckpointExpectation{exists: true, token: checkpointRowToken(row), row: row}, nil
}

func collectorIdentityExpectationFor(store *Store, canonicalKey, adapter string) (collectorIdentityExpectation, error) {
	expectation := collectorIdentityExpectation{CanonicalKey: canonicalKey, Adapter: adapter}
	err := store.db.QueryRow(`SELECT source_id FROM collector_source_identities WHERE canonical_key = ? AND adapter = ?`, canonicalKey, adapter).Scan(&expectation.SourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return expectation, nil
	}
	if err != nil {
		return collectorIdentityExpectation{}, err
	}
	expectation.Exists = true
	return expectation, nil
}

func checkpointEqual(a, b sourceCheckpointExpectation) bool {
	return a.exists == b.exists && (!a.exists || a.token == b.token)
}
