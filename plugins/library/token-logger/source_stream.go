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
	ctx      context.Context
	r        io.Reader
	full     hash.Hash
	prefixes map[int64]hash.Hash
	read     int64
	observer *collectorBenchmarkObserver
	hook     func(int, int64)
	pass     int
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
		if expectation.exists && expectation.cursor.ByteOffset >= 0 && expectation.cursor.ByteOffset < fileSize {
			offsets = append(offsets, expectation.cursor.ByteOffset)
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
	for _, path := range source.IdentityPaths {
		sourceID := prefix + digest([]byte(path))
		if _, err := store.Cursor(sourceID); err == nil {
			return sourceID, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if len(source.IdentityPaths) > 0 {
		return prefix + digest([]byte(source.IdentityPaths[0])), nil
	}
	return prefix + digest([]byte("canonical\x00"+source.CanonicalKey)), nil
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
		if adapter == "" {
			classifier := &adapterClassifier{}
			if err := classifyAndDrainSource(ctx, reader, classifier, store.benchmarkObserver); err != nil {
				_ = file.Close()
				return sourceSnapshot{}, fmt.Errorf("read source for adapter detection: %w", err)
			}
			adapter = classifier.adapterName()
		} else if _, err := io.Copy(io.Discard, reader); err != nil {
			_ = file.Close()
			return sourceSnapshot{}, fmt.Errorf("read source for verification: %w", err)
		}
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return sourceSnapshot{}, err
		}
		if stream.read != openedInfo.Size() {
			_ = file.Close()
			continue
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
			byteLength: stream.read, adapter: adapter, unchanged: unchanged}
		if unchanged {
			claimedID, claimErr := establishedCollectorSourceID(store, source, adapter)
			if claimErr != nil {
				_ = file.Close()
				return sourceSnapshot{}, fmt.Errorf("claim unchanged source identity: %w", claimErr)
			}
			if claimedID != baseSourceID {
				_ = file.Close()
				continue
			}
		}
		return snapshot, nil
	}
	return sourceSnapshot{}, fmt.Errorf("source identity kept changing during acquisition after %d attempts", sourceCursorRetryLimit)
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

func importStreamingSource(ctx context.Context, path string, store *Store, knownAdapter string) (ImportResult, string, error) {
	for attempt := 1; attempt <= sourceCursorRetryLimit; attempt++ {
		snapshot, err := acquireStreamingSourceSnapshot(ctx, path, store, knownAdapter)
		if err != nil {
			return ImportResult{}, "", err
		}
		adapter := snapshot.adapter
		if snapshot.unchanged {
			result := ImportResult{}
			result.Outcomes.Add(OutcomeUnchangedSnapshot)
			if snapshot.expectedCursor.cursor.PendingTail {
				result.Outcomes.Add(OutcomeDeferredIncompleteTail)
			}
			_ = snapshot.close()
			return result, adapter, nil
		}
		result, retry, importErr := importStreamingSnapshot(ctx, path, snapshot, store, knownAdapter == "")
		_ = snapshot.close()
		if errors.Is(importErr, errSourceSnapshotChanged) {
			continue
		}
		if importErr != nil {
			return result, adapter, importErr
		}
		if retry {
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
	if _, err := snapshot.file.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, false, fmt.Errorf("rewind source for parser pass: %w", err)
	}
	prefixOffsets := []int64{}
	if snapshot.expectedCursor.exists && snapshot.expectedCursor.cursor.ByteOffset >= 0 && snapshot.expectedCursor.cursor.ByteOffset < snapshot.byteLength {
		prefixOffsets = append(prefixOffsets, snapshot.expectedCursor.cursor.ByteOffset)
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
		if snapshot.expectedCursor.exists && snapshot.expectedCursor.cursor.ByteOffset >= 0 &&
			snapshot.expectedCursor.cursor.ByteOffset <= stream.read {
			prefix, ok := stream.prefixFingerprint(snapshot.expectedCursor.cursor.ByteOffset)
			if snapshot.expectedCursor.cursor.ByteOffset == stream.read {
				prefix, ok = fingerprint, true
			}
			if !ok || prefix != snapshot.prefixFingerprint {
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
		claimedID, err := establishedCollectorSourceID(store, snapshot.source, snapshot.adapter)
		if err != nil {
			return fmt.Errorf("claim source identity after verification: %w", err)
		}
		if claimedID != snapshot.baseSourceID {
			return errSourceSnapshotChanged
		}
		return nil
	}
	if snapshot.adapter == "claude" {
		return importClaudeReaderAttempt(ctx, stream, store, snapshot.baseSourceID, snapshot.expectedCursor,
			snapshot.fingerprint, snapshot.generation, classifier, verify)
	}
	return importCodexReaderAttempt(ctx, stream, store, snapshot.baseSourceID, snapshot.expectedCursor,
		snapshot.fingerprint, snapshot.generation, classifier, verify)
}
