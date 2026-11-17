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

//go:build linux

package readbrokerstudy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	maxSearchCandidates = 128
	maxSearchScanBytes  = 8 << 20
	maxSearchWorkUnits  = 5_000_000
	maxSearchLines      = 100
	maxSearchResultSize = 64 << 10
)

type searchProviderMatch struct {
	Path             string `json:"path"`
	Line             int    `json:"line"`
	Snippet          string `json:"snippet"`
	SnippetTruncated bool   `json:"snippet_truncated,omitempty"`
}

type searchProviderResult struct {
	Matches      []searchProviderMatch `json:"matches"`
	Incomplete   bool                  `json:"incomplete"`
	SkippedFiles int                   `json:"skipped_files,omitempty"`
	Raw          []byte                `json:"-"`
}

type preparedSearchCandidate struct {
	relative string
	file     *os.File
	identity FileIdentity
	size     int64
	mtime    unix.Timespec
	ctime    unix.Timespec
}

type searchSnapshotInput struct {
	Query      string               `json:"query"`
	WorkBudget int                  `json:"work_budget"`
	Files      []searchSnapshotFile `json:"files"`
}

type searchSnapshotFile struct {
	Content []byte `json:"content"`
}

type preparedSearchSnapshot struct {
	File       *os.File
	Paths      []string
	Candidates []preparedSearchCandidate
	Changed    []int
	Query      string
	Incomplete bool
	Skipped    int
}

type searchSnapshotValidationError struct {
	validationErr error
	cleanupErr    error
}

func searchCandidatesClosed(snapshot *preparedSearchSnapshot) bool {
	if snapshot == nil {
		return false
	}
	for _, candidate := range snapshot.Candidates {
		if candidate.file != nil {
			return false
		}
	}
	return true
}

func (e *searchSnapshotValidationError) Error() string {
	return errors.Join(e.validationErr, e.cleanupErr).Error()
}

func (e *searchSnapshotValidationError) Unwrap() []error {
	var errs []error
	if e.validationErr != nil {
		errs = append(errs, e.validationErr)
	}
	if e.cleanupErr != nil {
		errs = append(errs, e.cleanupErr)
	}
	return errs
}

func (p *preparedDirectoryList) validateAndCloseSearchSnapshot(ctx context.Context, snapshot *preparedSearchSnapshot) error {
	if p == nil || snapshot == nil || ctx == nil {
		return ErrProtocol
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { p.searchDirectories = nil }()
	var validationErr, cleanupErr error
	if p.closed || p.directory == nil || p.registry == nil {
		validationErr = errors.Join(validationErr, ErrStaleReceipt)
	}
	if err := ctx.Err(); err != nil {
		validationErr = errors.Join(validationErr, err)
	}
	if validationErr == nil {
		budget := &directoryFilterBudget{ctx: ctx, remaining: maxDirectoryFilterWork}
		blocked, err := p.revalidateListingDirectoriesAtRevision(slices.Clone(p.searchDirectories), budget, false)
		validationErr = errors.Join(validationErr, err)
		if err == nil && len(blocked) > 0 {
			validationErr = errors.Join(validationErr, ErrProtected)
		}
		if validationErr == nil && !p.options.IncludeIgnored {
			currentRules, readErr := readDirectoryIgnoreRules(p.directory, p.path, p.identity, p.registry)
			if readErr != nil || !slices.Equal(currentRules, p.ignoreRules) {
				validationErr = errors.Join(validationErr, ErrPolicyChanged)
			}
		}
	}
	rootFD := -1
	var rootMount uint64
	if validationErr == nil {
		rootFD = int(p.directory.Fd())
		var err error
		rootMount, err = mountIDForFD(rootFD)
		validationErr = errors.Join(validationErr, err)
	}
	for index := range snapshot.Candidates {
		if err := ctx.Err(); err != nil {
			validationErr = errors.Join(validationErr, err)
		}
		candidate := &snapshot.Candidates[index]
		changed := false
		if candidate.file == nil {
			changed = true
		} else if stat, err := statSearchCandidate(candidate.file); err != nil || !sameSearchRevision(*candidate, stat) {
			changed = true
		}
		if !changed && rootFD >= 0 {
			parentFD, name, closeParent, err := openListingParent(rootFD, candidate.relative)
			if err != nil {
				changed = true
			} else {
				fd, openErr := unix.Openat2(parentFD, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
				if openErr != nil {
					changed = true
				} else {
					var stat unix.Stat_t
					identityErr := unix.Fstat(fd, &stat)
					mountID, mountErr := mountIDForFD(fd)
					identity := identityFromStat(&stat)
					ancestors, ancestorErr := directoryAncestors(parentFD)
					path := filepath.Join(p.path, filepath.FromSlash(candidate.relative))
					_, protectedRoute := p.registry.ClassifyRoute(path)
					_, protectedIdentity := p.registry.ClassifyWithAncestors(path, identity, ancestors)
					if identityErr != nil || mountErr != nil || ancestorErr != nil || identity != candidate.identity || stat.Size != candidate.size || stat.Mtim != candidate.mtime || stat.Ctim != candidate.ctime || mountID != rootMount || protectedRoute || protectedIdentity {
						changed = true
					}
					cleanupErr = errors.Join(cleanupErr, unix.Close(fd))
				}
				cleanupErr = errors.Join(cleanupErr, closeParent())
			}
		}
		if changed {
			snapshot.Changed = append(snapshot.Changed, index)
		}
		if candidate.file != nil {
			file := candidate.file
			if err := file.Close(); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				p.searchCleanupFiles = append(p.searchCleanupFiles, file)
			} else {
				candidate.file = nil
			}
		}
	}
	if snapshot.File != nil {
		file := snapshot.File
		if err := file.Close(); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			p.searchCleanupFiles = append(p.searchCleanupFiles, file)
		}
		snapshot.File = nil
	}
	p.searchCleanupErr = errors.Join(p.searchCleanupErr, cleanupErr)
	if validationErr == nil && cleanupErr == nil {
		return nil
	}
	return &searchSnapshotValidationError{validationErr: validationErr, cleanupErr: cleanupErr}
}

type searchWorkerMatch struct {
	Index            int    `json:"index"`
	Line             int    `json:"line"`
	Snippet          string `json:"snippet"`
	SnippetTruncated bool   `json:"snippet_truncated,omitempty"`
}

type searchWorkerResult struct {
	Matches        []searchWorkerMatch `json:"matches"`
	SkippedIndices []int               `json:"skipped_indices,omitempty"`
	Incomplete     bool                `json:"incomplete"`
}

var errSearchCandidateChanged = errors.New("search candidate changed during preparation")

func (p *preparedDirectoryList) prepareSearchSnapshot(ctx context.Context, raw []byte, query string, workBudget int) (prepared preparedSearchSnapshot, resultErr error) {
	if ctx == nil || query == "" || len(query) > 512 || strings.ContainsAny(query, "\r\n\x00") || workBudget < 0 || workBudget > maxSearchWorkUnits {
		return preparedSearchSnapshot{}, ErrProtocol
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.directory == nil || p.registry == nil {
		return preparedSearchSnapshot{}, os.ErrClosed
	}
	var traversal directoryListingResult
	if len(raw) > maxWorkerSnapshotBytes || json.Unmarshal(raw, &traversal) != nil || traversal.Names != nil || traversal.Examined > 10_000 || traversal.Returned > 10_000 || len(traversal.Entries) > 10_000 || len(traversal.Directories) != 0 {
		return preparedSearchSnapshot{}, ErrProtocol
	}
	candidates := make([]preparedSearchCandidate, 0, min(len(traversal.Entries), maxSearchCandidates))
	closeCandidates := func() error {
		var closeErr error
		for index := range candidates {
			if candidates[index].file != nil {
				closeErr = errors.Join(closeErr, candidates[index].file.Close())
				candidates[index].file = nil
			}
		}
		return closeErr
	}
	defer func() { resultErr = errors.Join(resultErr, closeCandidates()) }()
	incomplete := traversal.Incomplete
	skipped := 0
	rootFD := int(p.directory.Fd())
	rootMount, err := mountIDForFD(rootFD)
	if err != nil {
		return preparedSearchSnapshot{}, err
	}
	for _, entry := range traversal.Entries {
		if err := ctx.Err(); err != nil {
			return preparedSearchSnapshot{}, err
		}
		if entry.Type != unix.DT_REG {
			continue
		}
		if !validListingRelativePath(entry.Path) || entry.Inode == 0 || entry.Device == 0 {
			return preparedSearchSnapshot{}, ErrProtocol
		}
		parentFD, name, closeParent, err := openListingParent(rootFD, entry.Path)
		if err != nil {
			return preparedSearchSnapshot{}, err
		}
		fileFD, openErr := unix.Openat2(parentFD, name, &unix.OpenHow{
			Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK,
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
		})
		if openErr != nil {
			closeErr := closeParent()
			if closeErr != nil {
				return preparedSearchSnapshot{}, closeErr
			}
			return preparedSearchSnapshot{}, ErrStaleReceipt
		}
		file := os.NewFile(uintptr(fileFD), "prepared-search-candidate")
		var stat unix.Stat_t
		mountID, mountErr := mountIDForFD(fileFD)
		statErr := unix.Fstat(fileFD, &stat)
		ancestors, ancestorErr := directoryAncestors(parentFD)
		closeErr := closeParent()
		if errors.Join(mountErr, statErr, ancestorErr, closeErr) != nil {
			return preparedSearchSnapshot{}, errors.Join(ErrStaleReceipt, file.Close())
		}
		identity := identityFromStat(&stat)
		if identity.Device != entry.Device || identity.Inode != entry.Inode || identity.Type != uint32(unix.S_IFREG) || mountID != rootMount {
			return preparedSearchSnapshot{}, errors.Join(ErrStaleReceipt, file.Close())
		}
		fullPath := filepath.Join(p.path, filepath.FromSlash(entry.Path))
		if _, protected := p.registry.ClassifyRoute(fullPath); protected {
			if err := file.Close(); err != nil {
				return preparedSearchSnapshot{}, err
			}
			continue
		}
		if _, protected := p.registry.ClassifyWithAncestors(fullPath, identity, ancestors); protected {
			if err := file.Close(); err != nil {
				return preparedSearchSnapshot{}, err
			}
			continue
		}
		if p.registry.DirectoryCoverageIncomplete(identity) {
			return preparedSearchSnapshot{}, errors.Join(ErrDirectoryCoverage, file.Close())
		}
		if len(candidates)+skipped >= maxSearchCandidates {
			incomplete = true
			if err := file.Close(); err != nil {
				return preparedSearchSnapshot{}, err
			}
			break
		}
		if stat.Size < 0 {
			return preparedSearchSnapshot{}, errors.Join(ErrStaleReceipt, file.Close())
		}
		if stat.Size > MaxReadBytes {
			skipped++
			incomplete = true
			if err := file.Close(); err != nil {
				return preparedSearchSnapshot{}, err
			}
			continue
		}
		candidates = append(candidates, preparedSearchCandidate{relative: entry.Path, file: file, identity: identity, size: stat.Size, mtime: stat.Mtim, ctime: stat.Ctim})
	}
	if err := ctx.Err(); err != nil {
		return preparedSearchSnapshot{}, err
	}
	result := searchSnapshotInput{Query: query, WorkBudget: workBudget, Files: make([]searchSnapshotFile, 0, len(candidates))}
	prepared = preparedSearchSnapshot{Paths: make([]string, 0, len(candidates)), Query: query, Incomplete: incomplete, Skipped: skipped}
	remainingBytes := maxSearchScanBytes
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			clearSearchSnapshot(result.Files)
			return preparedSearchSnapshot{}, err
		}
		if candidate.size > int64(remainingBytes) {
			prepared.Incomplete = true
			break
		}
		before, err := statSearchCandidate(candidate.file)
		if err != nil || !sameSearchRevision(candidate, before) {
			clearSearchSnapshot(result.Files)
			return preparedSearchSnapshot{}, errSearchCandidateChanged
		}
		content, readErr := io.ReadAll(io.LimitReader(candidate.file, MaxReadBytes+1))
		if readErr != nil {
			clear(content)
			clearSearchSnapshot(result.Files)
			return preparedSearchSnapshot{}, ErrUnavailable
		}
		after, statErr := statSearchCandidate(candidate.file)
		if statErr != nil || !sameSearchRevision(candidate, after) || len(content) != int(candidate.size) {
			clear(content)
			clearSearchSnapshot(result.Files)
			return preparedSearchSnapshot{}, errSearchCandidateChanged
		}
		remainingBytes -= len(content)
		result.Files = append(result.Files, searchSnapshotFile{Content: content})
		prepared.Paths = append(prepared.Paths, candidate.relative)
	}
	encoded, err := json.Marshal(result)
	clearSearchSnapshot(result.Files)
	if err != nil || len(encoded) > maxWorkerSnapshotBytes {
		clear(encoded)
		return preparedSearchSnapshot{}, ErrResultTooLarge
	}
	file, err := createSealedSearchSnapshot(encoded)
	clear(encoded)
	if err != nil {
		return preparedSearchSnapshot{}, err
	}
	prepared.File = file
	prepared.Candidates = candidates
	candidates = nil
	return prepared, nil
}

func searchSnapshotBytes(data []byte) (searchWorkerResult, error) {
	if len(data) == 0 || len(data) > maxWorkerSnapshotBytes {
		return searchWorkerResult{}, ErrProtocol
	}
	var input searchSnapshotInput
	if json.Unmarshal(data, &input) != nil || input.Query == "" || len(input.Query) > 512 || strings.ContainsAny(input.Query, "\r\n\x00") || input.WorkBudget < 0 || input.WorkBudget > maxSearchWorkUnits || len(input.Files) > maxSearchCandidates {
		return searchWorkerResult{}, ErrProtocol
	}
	result := searchWorkerResult{Matches: make([]searchWorkerMatch, 0)}
	if input.WorkBudget == 0 && len(input.Files) > 0 {
		result.Incomplete = true
		return result, nil
	}
	budget := &searchBudget{bytes: maxSearchScanBytes, work: input.WorkBudget}
	for index, file := range input.Files {
		if len(file.Content) > MaxReadBytes || len(file.Content) > budget.bytes {
			return searchWorkerResult{}, ErrProtocol
		}
		matched, err := searchUTF8LiteralLines(file.Content, input.Query, budget)
		clear(file.Content)
		if errors.Is(err, errSearchBudget) {
			result.Incomplete = true
			break
		}
		if err != nil {
			return searchWorkerResult{}, ErrProtocol
		}
		if matched.Unsupported {
			result.Incomplete = true
			result.SkippedIndices = append(result.SkippedIndices, index)
			continue
		}
		omittedMatch := false
		for _, match := range matched.Matches {
			if len(result.Matches) == maxSearchLines {
				result.Incomplete = true
				omittedMatch = true
				break
			}
			result.Matches = append(result.Matches, searchWorkerMatch{Index: index, Line: match.Line, Snippet: match.Snippet, SnippetTruncated: match.SnippetTruncated})
		}
		if omittedMatch {
			break
		}
	}
	return result, nil
}

func finalizeSearchWorkerResult(ctx context.Context, worker WorkerResult, snapshot preparedSearchSnapshot) (WorkerResult, error) {
	if worker.Outcome != ReadSucceeded || worker.Truncated || len(worker.Bytes) > maxSearchWorkerResultBytes {
		return WorkerResult{}, ErrProtocol
	}
	var result searchWorkerResult
	decoder := json.NewDecoder(bytes.NewReader(worker.Bytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || len(result.Matches) > maxSearchLines {
		return WorkerResult{}, ErrProtocol
	}
	providerResult := searchProviderResult{Matches: make([]searchProviderMatch, 0, len(result.Matches)), Incomplete: snapshot.Incomplete || result.Incomplete || len(snapshot.Changed) != 0, SkippedFiles: snapshot.Skipped}
	skipped := make(map[int]struct{}, len(result.SkippedIndices))
	for _, index := range result.SkippedIndices {
		if index < 0 || index >= len(snapshot.Paths) {
			return WorkerResult{}, ErrProtocol
		}
		if _, duplicate := skipped[index]; duplicate {
			return WorkerResult{}, ErrProtocol
		}
		skipped[index] = struct{}{}
	}
	for _, index := range snapshot.Changed {
		if index < 0 || index >= len(snapshot.Paths) {
			return WorkerResult{}, ErrProtocol
		}
		skipped[index] = struct{}{}
	}
	providerResult.SkippedFiles += len(skipped)
	if (len(skipped) != 0 || snapshot.Skipped != 0) && !providerResult.Incomplete {
		return WorkerResult{}, ErrProtocol
	}
	lastIndex, lastLine := -1, 0
	for _, match := range result.Matches {
		_, isSkipped := skipped[match.Index]
		if isSkipped {
			continue
		}
		if match.Index < 0 || match.Index >= len(snapshot.Paths) || isSkipped || match.Line < 1 || len(match.Snippet) > searchSnippetMaxBytes || !utf8.ValidString(match.Snippet) || !strings.Contains(match.Snippet, snapshot.Query) || match.Index < lastIndex || match.Index == lastIndex && match.Line <= lastLine {
			return WorkerResult{}, ErrProtocol
		}
		lastIndex, lastLine = match.Index, match.Line
		providerResult.Matches = append(providerResult.Matches, searchProviderMatch{Path: snapshot.Paths[match.Index], Line: match.Line, Snippet: match.Snippet, SnippetTruncated: match.SnippetTruncated})
	}
	return encodeSearchProviderResult(ctx, providerResult)
}

func clearSearchSnapshot(files []searchSnapshotFile) {
	for index := range files {
		clear(files[index].Content)
	}
}

func createSealedSearchSnapshot(data []byte) (_ *os.File, resultErr error) {
	if len(data) == 0 || len(data) > maxWorkerSnapshotBytes {
		return nil, ErrResultTooLarge
	}
	fd, err := unix.MemfdCreate("komposyn-search-content", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	writable := os.NewFile(uintptr(fd), "search-content-writable")
	if writable == nil {
		_ = unix.Close(fd)
		return nil, ErrUnavailable
	}
	defer func() {
		if writable != nil {
			resultErr = errors.Join(resultErr, writable.Close())
		}
	}()
	for offset := 0; offset < len(data); {
		count, writeErr := unix.Write(fd, data[offset:])
		if writeErr == unix.EINTR {
			continue
		}
		if writeErr != nil || count <= 0 {
			return nil, ErrUnavailable
		}
		offset += count
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return nil, err
	}
	gotSeals, err := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
	if err != nil || gotSeals&seals != seals {
		return nil, ErrUnavailable
	}
	readOnlyFD, err := unix.Open(filepath.Join("/proc/self/fd", strconv.Itoa(fd)), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	readOnly := os.NewFile(uintptr(readOnlyFD), "search-content-readonly")
	if readOnly == nil {
		_ = unix.Close(readOnlyFD)
		return nil, ErrUnavailable
	}
	var writableStat, readonlyStat unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(readOnlyFD), unix.F_GETFL, 0)
	if unix.Fstat(fd, &writableStat) != nil || unix.Fstat(readOnlyFD, &readonlyStat) != nil || writableStat.Dev != readonlyStat.Dev || writableStat.Ino != readonlyStat.Ino || writableStat.Size != int64(len(data)) || flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		_ = readOnly.Close()
		return nil, ErrUnavailable
	}
	if _, err := readOnly.Seek(0, io.SeekStart); err != nil {
		_ = readOnly.Close()
		return nil, err
	}
	if err := writable.Close(); err != nil {
		writable = nil
		_ = readOnly.Close()
		return nil, ErrUnavailable
	}
	writable = nil
	return readOnly, nil
}

func statSearchCandidate(file *os.File) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if file == nil || unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return unix.Stat_t{}, ErrStaleReceipt
	}
	return stat, nil
}

func sameSearchRevision(candidate preparedSearchCandidate, stat unix.Stat_t) bool {
	return identityFromStat(&stat) == candidate.identity && stat.Size == candidate.size && stat.Mtim == candidate.mtime && stat.Ctim == candidate.ctime
}

func encodeSearchProviderResult(ctx context.Context, result searchProviderResult) (WorkerResult, error) {
	if err := ctx.Err(); err != nil {
		return WorkerResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return WorkerResult{}, err
	}
	if len(encoded) <= maxSearchResultSize {
		return WorkerResult{Outcome: ReadSucceeded, Bytes: encoded}, nil
	}
	result.Incomplete = true
	original := result.Matches
	low, high, best := 0, len(original), -1
	var output []byte
	for low <= high {
		if err := ctx.Err(); err != nil {
			return WorkerResult{}, err
		}
		middle := low + (high-low)/2
		result.Matches = original[:middle]
		candidate, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return WorkerResult{}, marshalErr
		}
		if len(candidate) <= maxSearchResultSize {
			best, output = middle, candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if best < 0 {
		return WorkerResult{Outcome: ReadPartial, Truncated: true}, nil
	}
	return WorkerResult{Outcome: ReadSucceeded, Bytes: output}, nil
}
