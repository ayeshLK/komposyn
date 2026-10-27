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
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
)

const sourceVerificationBufferSize = 64 * 1024

var errSourceSnapshotChanged = errors.New("source changed between verification and parsing passes")

type sourceStreamReader struct {
	ctx              context.Context
	r                io.Reader
	full             hash.Hash
	prefixes         map[int64]hash.Hash
	read             int64
	lastLineBoundary int64
	observer         *collectorBenchmarkObserver
	hook             func(int, int64)
	pass             int
}

func newSourceStreamReader(ctx context.Context, r io.Reader, prefixOffsets []int64, observer *collectorBenchmarkObserver, hook func(int, int64), pass int) *sourceStreamReader {
	prefixes := make(map[int64]hash.Hash, len(prefixOffsets))
	for _, offset := range prefixOffsets {
		if offset >= 0 {
			if _, exists := prefixes[offset]; !exists {
				prefixes[offset] = sha256.New()
			}
		}
	}
	return &sourceStreamReader{ctx: ctx, r: r, full: sha256.New(), prefixes: prefixes, observer: observer, hook: hook, pass: pass}
}

func (r *sourceStreamReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if n == 0 {
		return n, err
	}
	chunk := p[:n]
	if index := bytes.LastIndexByte(chunk, '\n'); index >= 0 {
		r.lastLineBoundary = r.read + int64(index) + 1
	}
	_, _ = r.full.Write(chunk)
	if r.observer != nil {
		r.observer.sourceBytesRead += int64(n)
		r.observer.sourceBytesHashed += int64(n)
	}
	for offset, prefix := range r.prefixes {
		if r.read >= offset {
			continue
		}
		count := int64(n)
		if r.read+count > offset {
			count = offset - r.read
		}
		_, _ = prefix.Write(chunk[:count])
		if r.observer != nil {
			r.observer.sourceBytesHashed += count
		}
	}
	r.read += int64(n)
	if r.hook != nil {
		r.hook(r.pass, r.read)
	}
	return n, err
}

func (r *sourceStreamReader) fullFingerprint() string {
	return hex.EncodeToString(r.full.Sum(nil))
}

func (r *sourceStreamReader) prefixFingerprint(offset int64) (string, bool) {
	prefix, ok := r.prefixes[offset]
	if !ok || offset > r.read {
		return "", false
	}
	return hex.EncodeToString(prefix.Sum(nil)), true
}

func streamCursorOffsets(expectations map[string]sourceCursorExpectation, fileSize int64) []int64 {
	offsets := make([]int64, 0, len(expectations))
	for _, expectation := range expectations {
		unchangedLength := expectation.exists && expectation.cursor.ByteOffset == fileSize
		if expectation.exists && !unchangedLength && expectation.cursor.ByteOffset >= 0 && expectation.cursor.ByteOffset <= fileSize {
			offsets = append(offsets, expectation.cursor.ByteOffset)
		}
		if expectation.checkpoint.exists && !unchangedLength && expectation.checkpoint.row.BoundaryOffset >= 0 && expectation.checkpoint.row.BoundaryOffset <= fileSize {
			offsets = append(offsets, expectation.checkpoint.row.BoundaryOffset)
		}
	}
	return offsets
}

func classifyAndDrainSource(ctx context.Context, reader *bufio.Reader, classifier *adapterClassifier, observer *collectorBenchmarkObserver) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadBytes('\n')
		classifier.observe(line, observer)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if classifier.done {
			_, err = io.Copy(io.Discard, reader)
			return err
		}
	}
}

func collectorSourceIDForRead(store *Store, source collectorSource, adapter string) (string, error) {
	if sourceID, err := store.collectorSourceIdentity(source.CanonicalKey, adapter); err == nil {
		return sourceID, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	prefix := adapter + "-file-"
	candidates := collectorSourceIDCandidates(source, adapter)
	for _, sourceID := range candidates {
		owner, err := collectorSourceIdentityOwner(store, adapter, sourceID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if errors.Is(err, sql.ErrNoRows) || owner == source.CanonicalKey {
			if _, cursorErr := store.Cursor(sourceID); cursorErr == nil {
				return sourceID, nil
			} else if !errors.Is(cursorErr, sql.ErrNoRows) {
				return "", cursorErr
			}
		}
	}
	for _, sourceID := range candidates {
		owner, err := collectorSourceIdentityOwner(store, adapter, sourceID)
		if errors.Is(err, sql.ErrNoRows) {
			return sourceID, nil
		}
		if err != nil {
			return "", err
		}
		if owner == source.CanonicalKey {
			return sourceID, nil
		}
	}
	base := prefix + digest([]byte("canonical\x00"+source.CanonicalKey))
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		owner, err := collectorSourceIdentityOwner(store, adapter, candidate)
		if errors.Is(err, sql.ErrNoRows) || owner == source.CanonicalKey {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func collectorSourceIdentityOwner(store *Store, adapter, sourceID string) (string, error) {
	var canonicalKey string
	err := store.db.QueryRow(`SELECT canonical_key FROM collector_source_identities WHERE adapter = ? AND source_id = ?`, adapter, sourceID).Scan(&canonicalKey)
	return canonicalKey, err
}

func acquireStreamingSourceSnapshot(ctx context.Context, path string, store *Store, knownAdapter string) (sourceSnapshot, error) {
	for attempt := 1; attempt <= sourceCursorRetryLimit; attempt++ {
		if err := ctx.Err(); err != nil {
			return sourceSnapshot{}, err
		}
		source, err := collectorSourceForPath(path)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("resolve source: %w", err)
		}
		expectations, err := collectorSourceCursorExpectations(store, source)
		if err != nil {
			return sourceSnapshot{}, fmt.Errorf("capture source cursors: %w", err)
		}
		identityExpectations := make(map[string]collectorIdentityExpectation, 2)
		for _, adapterName := range []string{"claude", "codex"} {
			identity, identityErr := collectorIdentityExpectationFor(store, source.CanonicalKey, adapterName)
			if identityErr != nil {
				return sourceSnapshot{}, fmt.Errorf("capture collector identity: %w", identityErr)
			}
			identityExpectations[adapterName] = identity
		}
		if store.beforeSnapshotRead != nil {
			store.beforeSnapshotRead()
		}
		file, err := os.Open(source.Path)
		if err != nil {
			return sourceSnapshot{}, err
		}
		openedInfo, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return sourceSnapshot{}, err
		}
		stream := newSourceStreamReader(ctx, file, streamCursorOffsets(expectations, openedInfo.Size()), store.benchmarkObserver, store.sourceReadHook, 1)
		reader := bufio.NewReaderSize(stream, sourceVerificationBufferSize)
		adapter := knownAdapter
		var classificationOutcomes OutcomeCounts
		var classificationAdapter string
		classifier := &adapterClassifier{}
		if err := classifyAndDrainSource(ctx, reader, classifier, store.benchmarkObserver); err != nil {
			_ = file.Close()
			return sourceSnapshot{}, fmt.Errorf("read source for adapter detection: %w", err)
		}
		classificationOutcomes = classifier.outcomes
		classificationAdapter = classifier.candidateAdapter
		detectedAdapter := classifier.adapterName()
		if knownAdapter == "" {
			adapter = detectedAdapter
		} else if detectedAdapter != knownAdapter {
			// Direct adapter imports use the same positive-evidence gate as
			// automatic discovery. A caller choosing an adapter is not evidence
			// that an arbitrary JSONL file belongs to that provider.
			adapter = ""
		}
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return sourceSnapshot{}, err
		}
		if stream.read != openedInfo.Size() {
			_ = file.Close()
			continue
		}
		if adapter == "" {
			current, matchErr := collectorSourceMatchesOpenFile(path, source, openedInfo)
			if matchErr != nil {
				_ = file.Close()
				return sourceSnapshot{}, fmt.Errorf("revalidate unrecognized source after classification: %w", matchErr)
			}
			if !current {
				_ = file.Close()
				continue
			}
			return sourceSnapshot{file: file, source: source, fingerprint: stream.fullFingerprint(),
				byteLength: stream.read, classificationOutcomes: classificationOutcomes,
				classificationAdapter: classificationAdapter}, nil
		}
		baseSourceID, err := collectorSourceIDForRead(store, source, adapter)
		if err != nil {
			_ = file.Close()
			return sourceSnapshot{}, fmt.Errorf("resolve source identity: %w", err)
		}
		expectedCursor, captured := expectations[baseSourceID]
		if !captured {
			_ = file.Close()
			continue
		}
		fingerprint := stream.fullFingerprint()
		prefixFingerprint, _ := stream.prefixFingerprint(expectedCursor.cursor.ByteOffset)
		if expectedCursor.exists && expectedCursor.cursor.ByteOffset == stream.read {
			prefixFingerprint = fingerprint
		}
		generation := sourceGenerationFromStream(expectedCursor, stream)
		identityExpectation := identityExpectations[adapter]
		identityMatches, identityErr := collectorIdentityStillMatches(store, identityExpectation)
		if identityErr != nil {
			_ = file.Close()
			return sourceSnapshot{}, fmt.Errorf("revalidate collector identity: %w", identityErr)
		}
		if (identityExpectation.Exists && identityExpectation.SourceID != baseSourceID) || !identityMatches {
			_ = file.Close()
			continue
		}
		checkpoint, checkpointUsable := decodeParserCheckpoint(expectedCursor.checkpoint.row)
		checkpointUsable = expectedCursor.checkpoint.exists && checkpointUsable && expectedCursor.exists &&
			checkpoint.SourceID == baseSourceID && checkpoint.Adapter == adapter && checkpoint.SourceGeneration == generation &&
			checkpoint.BoundaryOffset <= stream.read && checkpoint.BoundaryOffset <= expectedCursor.cursor.ByteOffset &&
			expectedCursor.cursor.SnapshotReusable && checkpointBoundaryIsLF(file, checkpoint.BoundaryOffset) && checkpoint.PrefixFingerprint == func() string {
			prefix, ok := stream.prefixFingerprint(checkpoint.BoundaryOffset)
			if !ok {
				return ""
			}
			return prefix
		}()
		if !checkpointUsable {
			checkpoint = parserCheckpoint{}
		}
		unchanged := expectedCursor.exists && expectedCursor.cursor.SnapshotReusable &&
			expectedCursor.cursor.ByteOffset == stream.read && expectedCursor.cursor.FileFingerprint == fingerprint
		current, err := collectorSourceMatchesOpenFile(path, source, openedInfo)
		if err != nil {
			_ = file.Close()
			return sourceSnapshot{}, fmt.Errorf("revalidate source after verification: %w", err)
		}
		if !current {
			_ = file.Close()
			continue
		}
		snapshot := sourceSnapshot{file: file, source: source, openedInfo: openedInfo, baseSourceID: baseSourceID,
			expectedCursor: expectedCursor, fingerprint: fingerprint, prefixFingerprint: prefixFingerprint, generation: generation,
			byteLength: stream.read, adapter: adapter, unchanged: unchanged, expectedCheckpoint: expectedCursor.checkpoint,
			checkpoint: checkpoint, checkpointUsable: checkpointUsable, checkpointBoundaryOffset: stream.lastLineBoundary,
			identityExpectation: identityExpectation}
		return snapshot, nil
	}
	return sourceSnapshot{}, fmt.Errorf("source identity kept changing during acquisition after %d attempts", sourceCursorRetryLimit)
}

func checkpointBoundaryIsLF(file *os.File, offset int64) bool {
	if offset == 0 {
		return true
	}
	if file == nil || offset < 0 {
		return false
	}
	last := []byte{0}
	if n, err := file.ReadAt(last, offset-1); err != nil || n != 1 {
		return false
	}
	return last[0] == '\n'
}

func collectorIdentityStillMatches(store *Store, expected collectorIdentityExpectation) (bool, error) {
	actual, err := store.collectorSourceIdentity(expected.CanonicalKey, expected.Adapter)
	if errors.Is(err, sql.ErrNoRows) {
		return !expected.Exists, nil
	}
	if err != nil {
		return false, err
	}
	return expected.Exists && actual == expected.SourceID, nil
}

func sourceGenerationFromStream(expectation sourceCursorExpectation, stream *sourceStreamReader) int64 {
	if !expectation.exists {
		return 0
	}
	cursor := expectation.cursor
	if cursor.ByteOffset < 0 || cursor.ByteOffset > stream.read {
		return cursor.SourceGeneration + 1
	}
	if cursor.ByteOffset == stream.read {
		if stream.fullFingerprint() != cursor.FileFingerprint {
			return cursor.SourceGeneration + 1
		}
		return cursor.SourceGeneration
	}
	prefix, ok := stream.prefixFingerprint(cursor.ByteOffset)
	if !ok || prefix != cursor.FileFingerprint {
		return cursor.SourceGeneration + 1
	}
	return cursor.SourceGeneration
}

func importStreamingSource(ctx context.Context, path string, store *Store, knownAdapter string) (result ImportResult, adapter string, resultErr error) {
	correlation := newDiagnosticCorrelation(ctx)
	if store.diagnosticObserver != nil {
		correlation.parserFindings = newDiagnosticParserFindings()
		ctx = withDiagnosticCorrelation(ctx, correlation)
	}
	classificationAdapter := knownAdapter
	var sourceID string
	defer func() {
		if resultErr != nil {
			classification := classifyImportFailure(resultErr, classificationAdapter)
			var boundary *diagnosticBoundaryFailure
			if errors.As(resultErr, &boundary) {
				classification = boundary.classification
			}
			resultErr = store.observeFailure(ctx, resultErr, classification,
				store.diagnosticSubject(DiagnosticSubjectSource, sourceID))
		}
		operation := DiagnosticOperationParse
		if sourceID == "" {
			operation = DiagnosticOperationClassify
		}
		store.recordOutcomeFindingsAt(ctx, classificationAdapter, sourceID, result.Outcomes, operation)
	}()
	for attempt := 1; attempt <= sourceCursorRetryLimit; attempt++ {
		snapshot, err := acquireStreamingSourceSnapshot(ctx, path, store, knownAdapter)
		if err != nil {
			return ImportResult{}, "", err
		}
		adapter = snapshot.adapter
		if adapter == "" {
			result = ImportResult{Outcomes: snapshot.classificationOutcomes}
			classificationAdapter = snapshot.classificationAdapter
			_ = snapshot.close()
			return result, classificationAdapter, nil
		}
		classificationAdapter = adapter
		sourceID = snapshot.baseSourceID
		if snapshot.unchanged {
			result = ImportResult{}
			result.Outcomes.Add(OutcomeUnchangedSnapshot)
			if snapshot.expectedCursor.cursor.PendingTail {
				result.Outcomes.Add(OutcomeDeferredIncompleteTail)
			}
			_ = snapshot.close()
			return result, adapter, nil
		}
		var retry bool
		result, retry, resultErr = importStreamingSnapshot(ctx, path, snapshot, store, knownAdapter == "")
		_ = snapshot.close()
		if errors.Is(resultErr, errSourceSnapshotChanged) {
			correlation.parserFindings.reset()
			continue
		}
		if resultErr != nil {
			return result, adapter, resultErr
		}
		if retry {
			correlation.parserFindings.reset()
			if attempt == sourceCursorRetryLimit {
				return ImportResult{}, adapter, fmt.Errorf("%w after %d attempts", errSourceCursorRetryExhausted, sourceCursorRetryLimit)
			}
			continue
		}
		return result, adapter, nil
	}
	return ImportResult{}, "", fmt.Errorf("%w after %d attempts", errSourceCursorRetryExhausted, sourceCursorRetryLimit)
}

func importStreamingSnapshot(ctx context.Context, path string, snapshot sourceSnapshot, store *Store, detectAdapter bool) (ImportResult, bool, error) {
	if snapshot.file == nil {
		return ImportResult{}, false, errors.New("source snapshot file is required")
	}
	if store.beforeSnapshotParse != nil {
		store.beforeSnapshotParse()
	}
	parse := func(resume bool) (ImportResult, bool, error) {
		correlation := diagnosticCorrelationFrom(ctx)
		if correlation.parserFindings != nil {
			correlation.parserFindings.reset()
			if resume {
				correlation.parserFindings.baseOutcomes = snapshot.checkpoint.State.Outcomes
			}
			ctx = withDiagnosticCorrelation(ctx, correlation)
		}
		if _, err := snapshot.file.Seek(0, io.SeekStart); err != nil {
			return ImportResult{}, false, fmt.Errorf("rewind source for parser pass: %w", err)
		}
		prefixOffsets := []int64{snapshot.checkpointBoundaryOffset}
		if snapshot.expectedCursor.exists && snapshot.expectedCursor.cursor.ByteOffset >= 0 && snapshot.expectedCursor.cursor.ByteOffset <= snapshot.byteLength {
			prefixOffsets = append(prefixOffsets, snapshot.expectedCursor.cursor.ByteOffset)
		}
		if snapshot.checkpointUsable {
			prefixOffsets = append(prefixOffsets, snapshot.checkpoint.BoundaryOffset)
		}
		stream := newSourceStreamReader(ctx, snapshot.file, prefixOffsets, store.benchmarkObserver, store.sourceReadHook, 2)
		var classifier *adapterClassifier
		if detectAdapter {
			classifier = &adapterClassifier{}
		}
		verify := func(offset int64) error {
			fingerprint := stream.fullFingerprint()
			if stream.read != snapshot.byteLength || offset != snapshot.byteLength || fingerprint != snapshot.fingerprint {
				return errSourceSnapshotChanged
			}
			if snapshot.expectedCursor.exists && snapshot.expectedCursor.cursor.ByteOffset >= 0 && snapshot.expectedCursor.cursor.ByteOffset <= stream.read {
				prefix, ok := stream.prefixFingerprint(snapshot.expectedCursor.cursor.ByteOffset)
				if snapshot.expectedCursor.cursor.ByteOffset == stream.read {
					prefix, ok = fingerprint, true
				}
				if !ok || prefix != snapshot.prefixFingerprint {
					return errSourceSnapshotChanged
				}
			}
			if resume {
				prefix, ok := stream.prefixFingerprint(snapshot.checkpoint.BoundaryOffset)
				if !ok || prefix != snapshot.checkpoint.PrefixFingerprint {
					return errSourceSnapshotChanged
				}
			}
			if classifier != nil && classifier.adapterName() != snapshot.adapter {
				return errSourceSnapshotChanged
			}
			current, err := collectorSourceMatchesOpenFile(path, snapshot.source, snapshot.openedInfo)
			if err != nil || !current {
				return fmt.Errorf("%w: source identity changed during parser pass", errSourceSnapshotChanged)
			}
			identityMatches, err := collectorIdentityStillMatches(store, snapshot.identityExpectation)
			if err != nil {
				return fmt.Errorf("revalidate collector source identity: %w", err)
			}
			if !identityMatches || (snapshot.identityExpectation.Exists && snapshot.identityExpectation.SourceID != snapshot.baseSourceID) {
				return errSourceSnapshotChanged
			}
			return nil
		}
		passSnapshot := snapshot
		passSnapshot.checkpointUsable = resume
		if snapshot.adapter == "claude" {
			return importClaudeReaderContinuation(ctx, stream, store, passSnapshot, classifier, verify)
		}
		return importCodexReaderContinuation(ctx, stream, store, passSnapshot, classifier, verify)
	}
	result, retry, err := parse(snapshot.checkpointUsable)
	if errors.Is(err, errParserCheckpointFallback) {
		return parse(false)
	}
	return result, retry, err
}
