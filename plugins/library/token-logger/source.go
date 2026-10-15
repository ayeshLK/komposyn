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
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
)

const sourceCursorRetryLimit = 3

var errSourceCursorRetryExhausted = errors.New("source cursor kept changing while snapshot was being imported")

type sourceAdapterChangedError struct {
	adapter string
}

func (err *sourceAdapterChangedError) Error() string {
	return fmt.Sprintf("authoritative source snapshot uses %s adapter", err.adapter)
}

func validateSourceSnapshotAdapter(contents []byte, expected string) error {
	adapter, err := detectAdapterSnapshot(contents)
	if err != nil {
		return fmt.Errorf("detect authoritative source snapshot: %w", err)
	}
	if adapter != expected {
		return &sourceAdapterChangedError{adapter: adapter}
	}
	return nil
}

type sourceSnapshot struct {
	contents       []byte
	baseSourceID   string
	expectedCursor sourceCursorExpectation
}

func acquireSourceSnapshot(ctx context.Context, path string, store *Store, adapter string) (sourceSnapshot, error) {
	for attempt := 1; attempt <= sourceCursorRetryLimit; attempt++ {
		if err := ctx.Err(); err != nil {
			return sourceSnapshot{}, err
		}
		source, err := collectorSourceForPath(path)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("resolve source: %w", err)
		}
		baseSourceID, err := establishedCollectorSourceID(store, source, adapter)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("resolve source identity: %w", err)
		}
		expectedCursor, err := currentSourceCursorExpectation(store, baseSourceID)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("read source cursor: %w", err)
		}
		if store.beforeSnapshotRead != nil {
			store.beforeSnapshotRead()
		}
		file, err := os.Open(source.Path)
		if err != nil {
			return sourceSnapshot{}, err
		}
		openedInfo, statErr := file.Stat()
		contents, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if statErr != nil {
			return sourceSnapshot{}, statErr
		}
		if readErr != nil {
			return sourceSnapshot{}, readErr
		}
		if closeErr != nil {
			return sourceSnapshot{}, closeErr
		}
		currentSource, err := collectorSourceForPath(path)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("revalidate source: %w", err)
		}
		currentInfo, err := os.Stat(currentSource.Path)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("revalidate opened source: %w", err)
		}
		if source.CanonicalKey == currentSource.CanonicalKey && os.SameFile(openedInfo, currentInfo) {
			return sourceSnapshot{contents: contents, baseSourceID: baseSourceID, expectedCursor: expectedCursor}, nil
		}
	}
	return sourceSnapshot{}, fmt.Errorf("source identity kept changing during acquisition after %d attempts", sourceCursorRetryLimit)
}

func retrySourceSnapshotAfterCursorConflict(ctx context.Context, path string, store *Store, adapter string, attempt int) (sourceSnapshot, error) {
	if attempt >= sourceCursorRetryLimit {
		return sourceSnapshot{}, fmt.Errorf("%w after %d attempts", errSourceCursorRetryExhausted, sourceCursorRetryLimit)
	}
	snapshot, err := acquireSourceSnapshot(ctx, path, store, adapter)
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf("refresh source after cursor conflict: %w", err)
	}
	return snapshot, nil
}

type sourceCursorExpectation struct {
	sourceID string
	cursor   Cursor
	exists   bool
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
	if !expectation.exists {
		return 0
	}
	cursor := expectation.cursor
	if cursor.ByteOffset < 0 || cursor.ByteOffset > int64(len(contents)) {
		return cursor.SourceGeneration + 1
	}
	if digest(contents[:cursor.ByteOffset]) != cursor.FileFingerprint {
		return cursor.SourceGeneration + 1
	}
	return cursor.SourceGeneration
}

func unchangedExpectedSourceSnapshot(expectation sourceCursorExpectation, contents []byte) (ImportResult, bool) {
	if !expectation.exists {
		return ImportResult{}, false
	}
	cursor := expectation.cursor
	if !cursor.SnapshotReusable || cursor.ByteOffset != int64(len(contents)) || cursor.FileFingerprint != digest(contents) {
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
