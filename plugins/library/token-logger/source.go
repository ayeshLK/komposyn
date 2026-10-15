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
)

// sourceGeneration returns the generation for the current source contents.
// A changed file is append-only when the bytes up to the previously committed
// offset still hash to the previous full-file fingerprint. Otherwise it is a
// replacement and receives a new generation so reused offsets cannot collide.
type sourceCursorExpectation struct {
	sourceID string
	cursor   Cursor
	exists   bool
}

func sourceGeneration(store *Store, sourceID string, contents []byte) (int64, sourceCursorExpectation, error) {
	cursor, err := store.Cursor(sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, sourceCursorExpectation{sourceID: sourceID}, nil
	}
	if err != nil {
		return 0, sourceCursorExpectation{}, fmt.Errorf("read source cursor: %w", err)
	}
	expectation := sourceCursorExpectation{sourceID: sourceID, cursor: cursor, exists: true}
	if cursor.ByteOffset < 0 || cursor.ByteOffset > int64(len(contents)) {
		return cursor.SourceGeneration + 1, expectation, nil
	}
	if digest(contents[:cursor.ByteOffset]) != cursor.FileFingerprint {
		return cursor.SourceGeneration + 1, expectation, nil
	}
	return cursor.SourceGeneration, expectation, nil
}

func unchangedSourceSnapshot(store *Store, sourceID string, contents []byte) (ImportResult, bool, error) {
	cursor, err := store.Cursor(sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportResult{}, false, nil
	}
	if err != nil {
		return ImportResult{}, false, fmt.Errorf("read source cursor: %w", err)
	}
	if !cursor.SnapshotReusable || cursor.ByteOffset != int64(len(contents)) || cursor.FileFingerprint != digest(contents) {
		return ImportResult{}, false, nil
	}
	result := ImportResult{}
	result.Outcomes.Add(OutcomeUnchangedSnapshot)
	if cursor.PendingTail {
		result.Outcomes.Add(OutcomeDeferredIncompleteTail)
	}
	return result, true, nil
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
