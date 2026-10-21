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
	"database/sql"
	"errors"
	"fmt"
	"os"
)

const sourceCursorRetryLimit = 3

var errSourceCursorRetryExhausted = errors.New("source cursor kept changing while snapshot was being imported")

type sourceSnapshot struct {
	file                     *os.File
	source                   collectorSource
	openedInfo               os.FileInfo
	baseSourceID             string
	expectedCursor           sourceCursorExpectation
	fingerprint              string
	prefixFingerprint        string
	generation               int64
	byteLength               int64
	adapter                  string
	unchanged                bool
	expectedCheckpoint       sourceCheckpointExpectation
	checkpoint               parserCheckpoint
	checkpointUsable         bool
	checkpointBoundaryOffset int64
	boundaryFingerprint      string
	identityExpectation      collectorIdentityExpectation
	classificationOutcomes   OutcomeCounts
	classificationAdapter    string
}

func (snapshot sourceSnapshot) close() error {
	if snapshot.file == nil {
		return nil
	}
	return snapshot.file.Close()
}

func collectorSourceMatchesOpenFile(path string, source collectorSource, openedInfo os.FileInfo) (bool, error) {
	currentSource, err := collectorSourceForPath(path)
	if err != nil {
		return false, err
	}
	currentInfo, err := os.Stat(currentSource.Path)
	if err != nil {
		return false, err
	}
	return source.CanonicalKey == currentSource.CanonicalKey && os.SameFile(openedInfo, currentInfo), nil
}

type sourceCursorExpectation struct {
	sourceID   string
	cursor     Cursor
	exists     bool
	checkpoint sourceCheckpointExpectation
}

func currentSourceCursorExpectation(store *Store, sourceID string) (sourceCursorExpectation, error) {
	cursor, err := store.Cursor(sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCursorExpectation{sourceID: sourceID}, nil
	}
	if err != nil {
		return sourceCursorExpectation{}, err
	}
	return sourceCursorExpectation{sourceID: sourceID, cursor: cursor, exists: true}, nil
}

// sourceGeneration returns the generation for the current source contents.
// A changed file is append-only when the bytes up to the previously committed
// offset still hash to the previous full-file fingerprint. Otherwise it is a
// replacement and receives a new generation so reused offsets cannot collide.
func sourceGeneration(expectation sourceCursorExpectation, contents []byte) int64 {
	return sourceGenerationObserved(expectation, contents, nil)
}

func sourceGenerationObserved(expectation sourceCursorExpectation, contents []byte, observer *collectorBenchmarkObserver) int64 {
	if !expectation.exists {
		return 0
	}
	cursor := expectation.cursor
	if cursor.ByteOffset < 0 || cursor.ByteOffset > int64(len(contents)) {
		return cursor.SourceGeneration + 1
	}
	if digestSourceBytes(contents[:cursor.ByteOffset], observer) != cursor.FileFingerprint {
		return cursor.SourceGeneration + 1
	}
	return cursor.SourceGeneration
}

func unchangedExpectedSourceSnapshot(expectation sourceCursorExpectation, contents []byte) (ImportResult, bool) {
	return unchangedExpectedSourceSnapshotObserved(expectation, contents, nil)
}

func unchangedExpectedSourceSnapshotObserved(expectation sourceCursorExpectation, contents []byte, observer *collectorBenchmarkObserver) (ImportResult, bool) {
	if !expectation.exists {
		return ImportResult{}, false
	}
	cursor := expectation.cursor
	if !cursor.SnapshotReusable || cursor.ByteOffset != int64(len(contents)) || cursor.FileFingerprint != digestSourceBytes(contents, observer) {
		return ImportResult{}, false
	}
	result := ImportResult{}
	result.Outcomes.Add(OutcomeUnchangedSnapshot)
	if cursor.PendingTail {
		result.Outcomes.Add(OutcomeDeferredIncompleteTail)
	}
	return result, true
}

func unchangedSourceSnapshot(store *Store, sourceID string, contents []byte) (ImportResult, bool, error) {
	expectation, err := currentSourceCursorExpectation(store, sourceID)
	if err != nil {
		return ImportResult{}, false, fmt.Errorf("read source cursor: %w", err)
	}
	result, unchanged := unchangedExpectedSourceSnapshot(expectation, contents)
	return result, unchanged, nil
}

func unchangedCollectorSnapshot(store *Store, canonicalPath string, contents []byte) (ImportResult, bool, error) {
	for _, sourceID := range []string{
		"claude-file-" + digest([]byte(canonicalPath)),
		"codex-file-" + digest([]byte(canonicalPath)),
	} {
		result, unchanged, err := unchangedSourceSnapshot(store, sourceID, contents)
		if err != nil || unchanged {
			return result, unchanged, err
		}
	}
	return ImportResult{}, false, nil
}

func generationSourceID(sourceID string, generation int64) string {
	if generation == 0 {
		return sourceID
	}
	return fmt.Sprintf("%s-generation-%d", sourceID, generation)
}
