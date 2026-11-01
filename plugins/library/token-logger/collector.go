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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CollectorHealth describes the latest explicit collector activity.
type CollectorHealth struct {
	Status            string
	Lifecycle         string
	LastPollAt        time.Time
	LastCompletedAt   time.Time
	LastSuccessAt     time.Time
	LastError         string
	Imported          int
	Duplicates        int
	Outcomes          OutcomeCounts
	Pending           bool
	Interrupted       bool
	LatestInterrupted bool
	Sources           int
	DiscoveryFailures int
	Roots             map[string]CollectorRootHealth
}

// CollectorRootHealth associates the latest poll outcome with one configured
// root. A deduplicated source discovered through overlapping roots contributes
// to each owning root without being imported more than once.
type CollectorRootHealth struct {
	Status    string
	LastError string
	Outcomes  OutcomeCounts
	Sources   int
}

// CollectorRoot identifies a configured source root. Required roots report a
// visible discovery failure when missing; optional roots may be absent.
type CollectorRoot struct {
	Path     string
	Required bool
}

// Collector polls provider-owned local artifacts using the same idempotent
// adapters as explicit import. It never writes to source files.
type Collector struct {
	store        *Store
	roots        []CollectorRoot
	interval     time.Duration
	mu           sync.RWMutex
	health       CollectorHealth
	beforeSource func(string)
}

// NewCollector creates an explicit collector. Roots are source files or
// directories; no collection begins until PollOnce or Run is called.
func NewCollector(store *Store, roots []string, interval time.Duration) (*Collector, error) {
	configured := make([]CollectorRoot, 0, len(roots))
	for _, root := range roots {
		configured = append(configured, CollectorRoot{Path: root, Required: true})
	}
	return NewCollectorWithRoots(store, configured, interval)
}

// NewCollectorWithRoots creates a collector with explicit required/optional
// source semantics.
func NewCollectorWithRoots(store *Store, roots []CollectorRoot, interval time.Duration) (*Collector, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Collector{store: store, roots: append([]CollectorRoot(nil), roots...), interval: interval,
		health: CollectorHealth{Status: "idle", Lifecycle: "idle"}}, nil
}

// Health returns a snapshot of collector health.
func (c *Collector) Health() CollectorHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	health := c.health
	if c.health.Roots != nil {
		health.Roots = make(map[string]CollectorRootHealth, len(c.health.Roots))
		for path, root := range c.health.Roots {
			health.Roots[path] = root
		}
	}
	return health
}

// PollOnce scans configured roots once and imports discovered records.
func (c *Collector) PollOnce() (ImportResult, error) {
	return c.PollOnceContext(context.Background())
}

// PollOnceContext scans once and stops between discovery/import records when
// cancelled. A persistence transaction already in progress remains atomic.
func (c *Collector) PollOnceContext(ctx context.Context) (total ImportResult, pollErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	correlation := diagnosticCorrelationFrom(ctx)
	if correlation.pollID == "" {
		correlation.pollID, _ = newDiagnosticID()
	}
	var accumulator *diagnosticPollAccumulator
	if c.store.diagnosticObserver != nil {
		correlation.operationID = ""
		accumulator = newDiagnosticPollAccumulator(c.store, correlation)
		correlation.accumulator = accumulator
		defer accumulator.flush()
	}
	ctx = withDiagnosticCorrelation(ctx, correlation)
	now := time.Now().UTC()
	c.mu.Lock()
	c.health.Status = "running"
	c.health.LastPollAt = now
	c.mu.Unlock()
	discovery := discoverCollectorSources(ctx, c.roots)
	var pollErrors []error
	rootHealth := make(map[string]CollectorRootHealth, len(c.roots))
	for _, root := range c.roots {
		rootHealth[root.Path] = CollectorRootHealth{Status: "healthy"}
	}
	for _, failure := range discovery.Failures {
		var subject DiagnosticSubject
		var subjectKind DiagnosticSubjectKind
		var subjectIdentity string
		if failure.TargetPath != "" {
			subject = c.store.discoveryTargetDiagnosticSubject(failure.TargetPath)
			subjectKind = DiagnosticSubjectDiscoveryTarget
			subjectIdentity, _ = canonicalCollectorPath(failure.TargetPath)
		} else {
			subject = c.store.registeredRootDiagnosticSubject(failure.RootPath)
			subjectKind = DiagnosticSubjectRoot
			subjectIdentity, _ = c.store.registeredRootDiagnosticIdentity(failure.RootPath)
		}
		if errors.Is(failure.Err, context.Canceled) || errors.Is(failure.Err, context.DeadlineExceeded) {
			classification := classifyDiscoveryFailure(failure.Err)
			c.store.persistOperationFailure(ctx, classification, subjectKind, subjectIdentity)
			failureErr := c.store.observeFailure(ctx, failure.Err, classification, subject)
			pollErrors = append(pollErrors, failureErr)
			continue
		}
		total.Outcomes.Add(OutcomeFailedSource)
		classification := classifyDiscoveryFailure(failure.Err)
		c.store.persistOperationFailure(ctx, classification, subjectKind, subjectIdentity)
		recordFailedSourceOutcomeAt(ctx, classification, subject)
		failureErr := c.store.observeFailure(ctx, failure.Err, classification, subject)
		pollErrors = append(pollErrors, failureErr)
		c.recordFailure(failure.Err, DiagnosticCategoryIngestion)
		root := rootHealth[failure.RootPath]
		root.Status = "degraded"
		if errors.Is(failure.Err, os.ErrNotExist) {
			root.Status = "missing"
		}
		root.LastError = SanitizeDiagnostic(failure.Err.Error())
		root.Outcomes.Add(OutcomeFailedSource)
		rootHealth[failure.RootPath] = root
	}
	interrupted := false
	for _, failure := range discovery.Failures {
		if errors.Is(failure.Err, context.Canceled) || errors.Is(failure.Err, context.DeadlineExceeded) {
			interrupted = true
		}
	}
	for _, source := range discovery.Sources {
		path := source.Path
		for _, rootPath := range source.RootPaths {
			root := rootHealth[rootPath]
			root.Sources++
			rootHealth[rootPath] = root
		}
		if err := ctx.Err(); err != nil {
			pollErrors = append(pollErrors, c.store.observeFailure(ctx, err,
				classifyDiscoveryFailure(err), DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected}))
			interrupted = true
			break
		}
		if c.beforeSource != nil {
			c.beforeSource(path)
		}
		if err := ctx.Err(); err != nil {
			pollErrors = append(pollErrors, c.store.observeFailure(ctx, err,
				classifyDiscoveryFailure(err), DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected}))
			interrupted = true
			break
		}
		result, adapter, importErr := importCollectorSource(ctx, path, c.store)
		if importErr != nil {
			if errors.Is(importErr, context.Canceled) || errors.Is(importErr, context.DeadlineExceeded) {
				pollErrors = append(pollErrors, importErr)
				interrupted = true
				break
			}
			total.Outcomes.Add(OutcomeFailedSource)
			failureClassification := classifyImportFailure(importErr, adapter)
			if details, ok := FailureDetails(importErr); ok {
				failureClassification = details.Classification
			}
			recordFailedSourceOutcomeAt(ctx, failureClassification,
				DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected})
			pollErrors = append(pollErrors, importErr)
			c.recordFailure(fmt.Errorf("import %s source: %w", adapter, importErr), DiagnosticCategoryIngestion)
			mergeCollectorRootFailure(rootHealth, source.RootPaths, importErr)
			continue
		}
		total.Merge(result)
		c.recordOutcomeDiagnostics(adapter, result.Outcomes)
		for _, rootPath := range source.RootPaths {
			root := rootHealth[rootPath]
			root.Outcomes.Merge(result.Outcomes)
			if result.Outcomes.Incomplete() {
				root.Status = "degraded"
				root.LastError = collectorOutcomeSummary(result.Outcomes)
			}
			rootHealth[rootPath] = root
		}
	}
	c.mu.Lock()
	completedAt := time.Now().UTC()
	c.health.Status = "healthy"
	if ctx.Err() != nil {
		c.health.Status = "cancelled"
	} else if total.Outcomes.Incomplete() {
		c.health.Status = "degraded"
	} else {
		c.health.LastError = ""
		c.health.LastSuccessAt = completedAt
	}
	c.health.LastCompletedAt = completedAt
	c.health.Imported += total.Receipt.Imported
	c.health.Duplicates += total.Receipt.Duplicates
	c.health.Outcomes.Merge(total.Outcomes)
	c.health.Pending = total.Outcomes.Pending()
	c.health.Interrupted = c.health.Interrupted || interrupted
	c.health.LatestInterrupted = interrupted
	c.health.Sources = len(discovery.Sources)
	c.health.DiscoveryFailures = len(discovery.Failures)
	c.health.Roots = rootHealth
	c.mu.Unlock()
	pollErr = errors.Join(pollErrors...)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && !errors.Is(pollErr, context.DeadlineExceeded) {
		pollErr = errors.Join(pollErr, c.store.observeFailure(ctx, context.DeadlineExceeded,
			makeDiagnosticClassification(DiagnosticComponentCollector, DiagnosticOperationRead,
				DiagnosticReasonDeadlineExceeded, DiagnosticAdapterUnavailable),
			DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected}))
	}
	return total, pollErr
}

func mergeCollectorRootFailure(health map[string]CollectorRootHealth, roots []string, err error) {
	for _, rootPath := range roots {
		root := health[rootPath]
		root.Status = "degraded"
		root.LastError = SanitizeDiagnostic(err.Error())
		root.Outcomes.Add(OutcomeFailedSource)
		health[rootPath] = root
	}
}

func collectorOutcomeSummary(outcomes OutcomeCounts) string {
	parts := []string{}
	if outcomes.UnsupportedUsage > 0 {
		parts = append(parts, fmt.Sprintf("unsupported_usage=%d", outcomes.UnsupportedUsage))
	}
	if outcomes.MalformedRecord > 0 {
		parts = append(parts, fmt.Sprintf("malformed_record=%d", outcomes.MalformedRecord))
	}
	if outcomes.FailedSource > 0 {
		parts = append(parts, fmt.Sprintf("failed_source=%d", outcomes.FailedSource))
	}
	return strings.Join(parts, " ")
}

func importCollectorSource(ctx context.Context, path string, store *Store) (ImportResult, string, error) {
	return importStreamingSource(ctx, path, store, "")
}

func (c *Collector) recordOutcomeDiagnostics(adapter string, outcomes OutcomeCounts) {
	for _, item := range []struct {
		outcome RecordOutcome
		count   int
	}{
		{OutcomeUnsupportedUsage, outcomes.UnsupportedUsage},
		{OutcomeMalformedRecord, outcomes.MalformedRecord},
	} {
		if item.count == 0 {
			continue
		}
		// The stable, path-free message lets RecordDiagnostic deduplicate
		// repeated polls without retaining transcript contents.
		category := DiagnosticCategoryIngestion
		if item.outcome == OutcomeUnsupportedUsage {
			category = DiagnosticCategoryProvider
		}
		if _, err := c.recordDiagnostic(fmt.Sprintf("adapter=%s reason=%s", adapter, item.outcome), category); err != nil {
			c.mu.Lock()
			c.health.LastError = SanitizeDiagnostic(err.Error())
			c.mu.Unlock()
		}
	}
}

// Run polls on the configured interval until ctx is cancelled. Callers that
// need an immediate result should call PollOnce before Run. Operational source
// failures are recorded as health errors while polling continues.
func (c *Collector) Run(ctx context.Context) error {
	return c.RunWithResults(ctx, nil)
}

// RunImmediate performs one poll immediately, then continues at the configured
// interval until cancellation.
func (c *Collector) RunImmediate(ctx context.Context, observer func(ImportResult, error)) error {
	ctx = c.collectorRunContext(ctx)
	c.mu.Lock()
	c.health.Lifecycle = "starting"
	c.mu.Unlock()
	result, err := c.PollOnceContext(ctx)
	if observer != nil {
		observer(result, err)
	}
	if ctx.Err() != nil {
		diagnosticCorrelationFrom(ctx).runAccumulator.flush()
		c.mu.Lock()
		c.health.Lifecycle = "stopped"
		c.mu.Unlock()
		return nil
	}
	return c.RunWithResults(ctx, observer)
}

// RunWithResults polls until ctx is cancelled and reports every completed poll
// to observer. The observer runs synchronously and must return promptly.
func (c *Collector) RunWithResults(ctx context.Context, observer func(ImportResult, error)) error {
	ctx = c.collectorRunContext(ctx)
	defer diagnosticCorrelationFrom(ctx).runAccumulator.flush()
	if err := ctx.Err(); err != nil {
		c.mu.Lock()
		c.health.Lifecycle = "stopped"
		c.mu.Unlock()
		return nil
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.mu.Lock()
	c.health.Lifecycle = "running"
	c.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.health.Lifecycle = "stopped"
			c.mu.Unlock()
			return nil
		case <-ticker.C:
			result, err := c.PollOnceContext(ctx)
			if ctx.Err() != nil {
				c.mu.Lock()
				c.health.Lifecycle = "stopped"
				c.mu.Unlock()
				return nil
			}
			if observer != nil {
				observer(result, err)
			}
		}
	}
}

func (c *Collector) collectorRunContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.store == nil {
		return ctx
	}
	correlation := diagnosticCorrelationFrom(ctx)
	if correlation.runID == "" {
		correlation.runID, _ = newDiagnosticID()
	}
	if correlation.runAccumulator == nil {
		correlation.runAccumulator = newDiagnosticRunAccumulator(c.store, correlation)
	}
	return withDiagnosticCorrelation(ctx, correlation)
}

func (c *Collector) recordFailure(err error, category string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health.Status = "degraded"
	c.health.LastError = SanitizeDiagnostic(err.Error())
	// Persist only a stable category. The local health view may retain the
	// sanitized operational error, but diagnostics never retain source paths,
	// provider record identifiers, or transcript fragments.
	if _, diagnosticErr := c.recordDiagnostic("reason=failed_source", category); diagnosticErr != nil && c.health.LastError == "" {
		c.health.LastError = diagnosticErr.Error()
	}
}

func (c *Collector) recordDiagnostic(message, category string) (Diagnostic, error) {
	return c.store.RecordDiagnosticWithMetadata(message, DiagnosticMetadata{
		Category: category, CoreVersion: CoreVersion(), SchemaVersion: SchemaVersion(),
	})
}

type collectorDiscoveryFailure struct {
	RootPath   string
	TargetPath string
	Err        error
}

type collectorDiscovery struct {
	Sources  []collectorSource
	Failures []collectorDiscoveryFailure
}

type collectorSource struct {
	Path          string
	CanonicalKey  string
	IdentityPaths []string
	RootPaths     []string
}

func discoverCollectorSources(ctx context.Context, roots []CollectorRoot) collectorDiscovery {
	result := collectorDiscovery{}
	seen := map[string]*collectorSource{}
	addSource := func(rootPath, canonicalPath, path string, identityPaths ...string) {
		source, exists := seen[canonicalPath]
		if !exists {
			source = &collectorSource{Path: path, CanonicalKey: digest([]byte(canonicalPath))}
			seen[canonicalPath] = source
		}
		for _, identityPath := range append([]string{path}, identityPaths...) {
			if !containsCollectorPath(source.IdentityPaths, identityPath) {
				source.IdentityPaths = append(source.IdentityPaths, identityPath)
			}
		}
		if !containsCollectorPath(source.RootPaths, rootPath) {
			source.RootPaths = append(source.RootPaths, rootPath)
		}
	}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path, Err: err})
			break
		}
		absoluteRoot, err := absoluteCollectorPath(root.Path)
		if err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path, Err: fmt.Errorf("resolve source root: %w", err)})
			continue
		}
		canonicalRoot, err := canonicalCollectorPath(absoluteRoot)
		if err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path, Err: fmt.Errorf("resolve source root: %w", err)})
			continue
		}
		info, err := os.Stat(absoluteRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !root.Required {
				continue
			}
			result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path, Err: fmt.Errorf("inspect source root: %w", err)})
			continue
		}
		if !info.IsDir() {
			resolvedRoot, _ := filepath.EvalSymlinks(absoluteRoot)
			addSource(root.Path, canonicalRoot, absoluteRoot, resolvedRoot)
			continue
		}
		walkRoot := absoluteRoot
		if linkInfo, linkErr := os.Lstat(absoluteRoot); linkErr == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
			walkRoot = canonicalRoot
		}
		err = filepath.WalkDir(walkRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path,
					TargetPath: path, Err: fmt.Errorf("inspect source subtree: %w", walkErr)})
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() && (filepath.Ext(path) == ".jsonl" || filepath.Ext(path) == ".json") {
				canonicalPath, canonicalErr := canonicalCollectorPath(path)
				if canonicalErr != nil {
					result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path,
						TargetPath: path, Err: fmt.Errorf("resolve discovered source: %w", canonicalErr)})
					return nil
				}
				resolvedPath, _ := filepath.EvalSymlinks(path)
				logicalPath := path
				if walkRoot != absoluteRoot {
					if relativePath, relativeErr := filepath.Rel(walkRoot, path); relativeErr == nil {
						logicalPath = filepath.Join(absoluteRoot, relativePath)
					}
				}
				addSource(root.Path, canonicalPath, path, logicalPath, resolvedPath)
			}
			return nil
		})
		if err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{RootPath: root.Path, Err: fmt.Errorf("walk source root: %w", err)})
		}
	}
	for _, source := range seen {
		result.Sources = append(result.Sources, *source)
	}
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Path < result.Sources[j].Path })
	return result
}

func containsCollectorPath(paths []string, candidate string) bool {
	if candidate == "" {
		return true
	}
	for _, path := range paths {
		if path == candidate {
			return true
		}
	}
	return false
}

func establishedCollectorSourceID(store *Store, source collectorSource, adapter string) (string, error) {
	if sourceID, err := store.collectorSourceIdentity(source.CanonicalKey, adapter); err == nil {
		return sourceID, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	prefix := adapter + "-file-"
	for _, path := range source.IdentityPaths {
		sourceID := prefix + digest([]byte(path))
		if _, err := store.Cursor(sourceID); err == nil {
			if storedSourceID, claimed, claimErr := store.rememberCollectorSourceIdentity(source.CanonicalKey, adapter, sourceID); claimErr != nil {
				return "", claimErr
			} else if claimed {
				return storedSourceID, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	for _, path := range source.IdentityPaths {
		sourceID := prefix + digest([]byte(path))
		if storedSourceID, claimed, err := store.rememberCollectorSourceIdentity(source.CanonicalKey, adapter, sourceID); err != nil {
			return "", err
		} else if claimed {
			return storedSourceID, nil
		}
	}
	// A pathname may already belong to an older canonical source when a
	// symlink is replaced by a regular file at the same location. Derive a
	// stable, path-safe fallback from the new canonical key so the replacement
	// gets its own cursor without displacing the older source.
	fallbackSourceID := prefix + digest([]byte("canonical\x00"+source.CanonicalKey))
	if storedSourceID, claimed, err := store.rememberCollectorSourceIdentity(source.CanonicalKey, adapter, fallbackSourceID); err != nil {
		return "", err
	} else if claimed {
		return storedSourceID, nil
	}
	return "", errors.New("could not allocate a distinct source identity")
}

// collectorSourceCursorExpectations captures every cursor identity resolution
// can select without claiming a mapping. These expectations are read before
// source bytes so a concurrent import cannot make its newer cursor look like
// the starting point for this snapshot.
func collectorSourceCursorExpectations(store *Store, source collectorSource) (map[string]sourceCursorExpectation, error) {
	ids := make([]string, 0, (len(source.IdentityPaths)+1)*2)
	for _, adapter := range []string{"claude", "codex"} {
		ids = append(ids, collectorSourceIDCandidates(source, adapter)...)
	}
	expectations := make(map[string]sourceCursorExpectation, len(ids))
	for _, sourceID := range ids {
		expectations[sourceID] = sourceCursorExpectation{sourceID: sourceID}
	}
	if len(ids) == 0 {
		return expectations, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for index, sourceID := range ids {
		args[index] = sourceID
	}
	args = append(args, source.CanonicalKey)
	cursorRows, err := store.db.Query(`SELECT source_id, file_fingerprint, byte_offset,
		last_provider_record_id, source_generation, pending_tail, snapshot_reusable, 1
		FROM source_cursors WHERE source_id IN (`+placeholders+`)
		UNION ALL
		SELECT identity.source_id, COALESCE(cursor.file_fingerprint, ''), COALESCE(cursor.byte_offset, 0),
			COALESCE(cursor.last_provider_record_id, ''), COALESCE(cursor.source_generation, 0),
			COALESCE(cursor.pending_tail, 0), COALESCE(cursor.snapshot_reusable, 0),
			CASE WHEN cursor.source_id IS NULL THEN 0 ELSE 1 END
		FROM collector_source_identities AS identity
		LEFT JOIN source_cursors AS cursor ON cursor.source_id = identity.source_id
		WHERE identity.canonical_key = ? AND identity.adapter IN ('claude', 'codex')`, args...)
	if err != nil {
		return nil, fmt.Errorf("read collector source cursors: %w", err)
	}
	defer cursorRows.Close()
	for cursorRows.Next() {
		var cursor Cursor
		var exists int
		if err := cursorRows.Scan(&cursor.SourceID, &cursor.FileFingerprint, &cursor.ByteOffset,
			&cursor.LastProviderRecordID, &cursor.SourceGeneration, &cursor.PendingTail, &cursor.SnapshotReusable, &exists); err != nil {
			return nil, err
		}
		expectation := sourceCursorExpectation{sourceID: cursor.SourceID, exists: exists != 0}
		if expectation.exists {
			expectation.cursor = cursor
		}
		expectations[cursor.SourceID] = expectation
	}
	if err := cursorRows.Err(); err != nil {
		return nil, err
	}
	for sourceID, expectation := range expectations {
		revision, revisionErr := sourceAllocationRevision(store.db, sourceID)
		if revisionErr != nil {
			return nil, fmt.Errorf("read source allocation revision: %w", revisionErr)
		}
		expectation.allocationRevision = revision
		checkpoint, err := parserCheckpointExpectation(store, sourceID)
		if err != nil {
			return nil, fmt.Errorf("read parser checkpoint %q: %w", sourceID, err)
		}
		expectation.checkpoint = checkpoint
		expectations[sourceID] = expectation
	}
	return expectations, nil
}

func collectorSourceIDCandidates(source collectorSource, adapter string) []string {
	ids := make([]string, 0, len(source.IdentityPaths)+1)
	prefix := adapter + "-file-"
	for _, path := range source.IdentityPaths {
		sourceID := prefix + digest([]byte(path))
		if !containsString(ids, sourceID) {
			ids = append(ids, sourceID)
		}
	}
	fallback := prefix + digest([]byte("canonical\x00"+source.CanonicalKey))
	if !containsString(ids, fallback) {
		ids = append(ids, fallback)
	}
	return ids
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func collectorSourceForPath(path string) (collectorSource, error) {
	absolutePath, err := absoluteCollectorPath(path)
	if err != nil {
		return collectorSource{}, err
	}
	canonicalPath, err := canonicalCollectorPath(absolutePath)
	if err != nil {
		return collectorSource{}, err
	}
	resolvedPath, _ := filepath.EvalSymlinks(absolutePath)
	source := collectorSource{Path: absolutePath, CanonicalKey: digest([]byte(canonicalPath))}
	for _, identityPath := range []string{absolutePath, resolvedPath} {
		if !containsCollectorPath(source.IdentityPaths, identityPath) {
			source.IdentityPaths = append(source.IdentityPaths, identityPath)
		}
	}
	return source, nil
}

func absoluteCollectorPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func canonicalCollectorPath(path string) (string, error) {
	absolute, err := absoluteCollectorPath(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(resolved)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return canonicalPathCase(absolute), nil
}
