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
	return c.health
}

// PollOnce scans configured roots once and imports discovered records.
func (c *Collector) PollOnce() (ImportResult, error) {
	return c.PollOnceContext(context.Background())
}

// PollOnceContext scans once and stops between discovery/import records when
// cancelled. A persistence transaction already in progress remains atomic.
func (c *Collector) PollOnceContext(ctx context.Context) (ImportResult, error) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.health.Status = "running"
	c.health.LastPollAt = now
	c.mu.Unlock()
	discovery := discoverCollectorSources(ctx, c.roots)
	total := ImportResult{}
	var pollErrors []error
	for _, failure := range discovery.Failures {
		if errors.Is(failure.Err, context.Canceled) || errors.Is(failure.Err, context.DeadlineExceeded) {
			pollErrors = append(pollErrors, failure.Err)
			continue
		}
		total.Outcomes.Add(OutcomeFailedSource)
		pollErrors = append(pollErrors, failure.Err)
		c.recordFailure(failure.Err)
	}
	interrupted := false
	for _, failure := range discovery.Failures {
		if errors.Is(failure.Err, context.Canceled) || errors.Is(failure.Err, context.DeadlineExceeded) {
			interrupted = true
		}
	}
	for _, source := range discovery.Sources {
		path := source.Path
		if err := ctx.Err(); err != nil {
			pollErrors = append(pollErrors, err)
			interrupted = true
			break
		}
		if c.beforeSource != nil {
			c.beforeSource(path)
		}
		if err := ctx.Err(); err != nil {
			pollErrors = append(pollErrors, err)
			interrupted = true
			break
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			total.Outcomes.Add(OutcomeFailedSource)
			pollErrors = append(pollErrors, readErr)
			c.recordFailure(fmt.Errorf("read source: %w", readErr))
			continue
		}
		adapter, detectErr := detectAdapterSnapshot(contents)
		if detectErr != nil {
			total.Outcomes.Add(OutcomeFailedSource)
			pollErrors = append(pollErrors, detectErr)
			c.recordFailure(fmt.Errorf("detect source: %w", detectErr))
			continue
		}
		result, importedAdapter, importErr := importCollectorSource(ctx, path, c.store, adapter)
		adapter = importedAdapter
		if importErr != nil {
			if errors.Is(importErr, context.Canceled) || errors.Is(importErr, context.DeadlineExceeded) {
				pollErrors = append(pollErrors, importErr)
				interrupted = true
				break
			}
			total.Outcomes.Add(OutcomeFailedSource)
			pollErrors = append(pollErrors, importErr)
			c.recordFailure(fmt.Errorf("import %s source: %w", adapter, importErr))
			continue
		}
		total.Merge(result)
		c.recordOutcomeDiagnostics(adapter, result.Outcomes)
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
	c.mu.Unlock()
	return total, errors.Join(pollErrors...)
}

func importCollectorSource(ctx context.Context, path string, store *Store, adapter string) (ImportResult, string, error) {
	for attempt := 1; attempt <= sourceCursorRetryLimit; attempt++ {
		var result ImportResult
		var err error
		if adapter == "claude" {
			result, err = importClaudeSource(ctx, path, store, true)
		} else {
			result, err = importCodexSource(ctx, path, store, true)
		}
		var changed *sourceAdapterChangedError
		if !errors.As(err, &changed) {
			return result, adapter, err
		}
		adapter = changed.adapter
	}
	return ImportResult{}, adapter, fmt.Errorf("source adapter kept changing during acquisition after %d attempts", sourceCursorRetryLimit)
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
		if _, err := c.store.RecordDiagnostic(fmt.Sprintf("adapter=%s reason=%s", adapter, item.outcome)); err != nil {
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
	c.mu.Lock()
	c.health.Lifecycle = "starting"
	c.mu.Unlock()
	result, err := c.PollOnceContext(ctx)
	if observer != nil {
		observer(result, err)
	}
	if ctx.Err() != nil {
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

func (c *Collector) recordFailure(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health.Status = "degraded"
	c.health.LastError = SanitizeDiagnostic(err.Error())
	// Persist only a stable category. The local health view may retain the
	// sanitized operational error, but diagnostics never retain source paths,
	// provider record identifiers, or transcript fragments.
	if _, diagnosticErr := c.store.RecordDiagnostic("reason=failed_source"); diagnosticErr != nil && c.health.LastError == "" {
		c.health.LastError = diagnosticErr.Error()
	}
}

type collectorDiscoveryFailure struct {
	Err error
}

type collectorDiscovery struct {
	Sources  []collectorSource
	Failures []collectorDiscoveryFailure
}

type collectorSource struct {
	Path          string
	CanonicalKey  string
	IdentityPaths []string
}

func discoverCollectorSources(ctx context.Context, roots []CollectorRoot) collectorDiscovery {
	result := collectorDiscovery{}
	seen := map[string]*collectorSource{}
	addSource := func(canonicalPath, path string, identityPaths ...string) {
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
	}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: err})
			break
		}
		absoluteRoot, err := absoluteCollectorPath(root.Path)
		if err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: fmt.Errorf("resolve source root: %w", err)})
			continue
		}
		canonicalRoot, err := canonicalCollectorPath(absoluteRoot)
		if err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: fmt.Errorf("resolve source root: %w", err)})
			continue
		}
		info, err := os.Stat(absoluteRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !root.Required {
				continue
			}
			result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: fmt.Errorf("inspect source root: %w", err)})
			continue
		}
		if !info.IsDir() {
			resolvedRoot, _ := filepath.EvalSymlinks(absoluteRoot)
			addSource(canonicalRoot, absoluteRoot, resolvedRoot)
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
				result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: fmt.Errorf("inspect source subtree: %w", walkErr)})
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() && (filepath.Ext(path) == ".jsonl" || filepath.Ext(path) == ".json") {
				canonicalPath, canonicalErr := canonicalCollectorPath(path)
				if canonicalErr != nil {
					result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: fmt.Errorf("resolve discovered source: %w", canonicalErr)})
					return nil
				}
				resolvedPath, _ := filepath.EvalSymlinks(path)
				logicalPath := path
				if walkRoot != absoluteRoot {
					if relativePath, relativeErr := filepath.Rel(walkRoot, path); relativeErr == nil {
						logicalPath = filepath.Join(absoluteRoot, relativePath)
					}
				}
				addSource(canonicalPath, path, logicalPath, resolvedPath)
			}
			return nil
		})
		if err != nil {
			result.Failures = append(result.Failures, collectorDiscoveryFailure{Err: fmt.Errorf("walk source root: %w", err)})
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
