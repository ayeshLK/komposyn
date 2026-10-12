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
	Status        string
	LastPollAt    time.Time
	LastSuccessAt time.Time
	LastError     string
	Imported      int
	Duplicates    int
	Outcomes      OutcomeCounts
	Pending       bool
	Sources       int
}

// Collector polls provider-owned local artifacts using the same idempotent
// adapters as explicit import. It never writes to source files.
type Collector struct {
	store    *Store
	roots    []string
	interval time.Duration
	mu       sync.RWMutex
	health   CollectorHealth
}

// NewCollector creates an explicit collector. Roots are source files or
// directories; no collection begins until PollOnce or Run is called.
func NewCollector(store *Store, roots []string, interval time.Duration) (*Collector, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Collector{store: store, roots: append([]string(nil), roots...), interval: interval,
		health: CollectorHealth{Status: "idle"}}, nil
}

// Health returns a snapshot of collector health.
func (c *Collector) Health() CollectorHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// PollOnce scans configured roots once and imports discovered records.
func (c *Collector) PollOnce() (ImportResult, error) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.health.Status = "running"
	c.health.LastPollAt = now
	c.mu.Unlock()
	paths, err := discoverCollectorSources(c.roots)
	if err != nil {
		c.recordFailure(err)
		return ImportResult{}, err
	}
	total := ImportResult{}
	for _, path := range paths {
		adapter, detectErr := DetectAdapter(path)
		if detectErr != nil {
			total.Outcomes.Add(OutcomeFailedSource)
			c.recordFailure(fmt.Errorf("detect source: %w", detectErr))
			continue
		}
		var result ImportResult
		var importErr error
		if adapter == "claude" {
			result, importErr = (ClaudeAdapter{}).Import(path, c.store)
		} else {
			result, importErr = (CodexAdapter{}).Import(path, c.store)
		}
		if importErr != nil {
			total.Outcomes.Add(OutcomeFailedSource)
			c.recordFailure(fmt.Errorf("import %s source: %w", adapter, importErr))
			continue
		}
		total.Merge(result)
		c.recordOutcomeDiagnostics(adapter, result.Outcomes)
	}
	c.mu.Lock()
	c.health.Status = "healthy"
	if total.Outcomes.Incomplete() {
		c.health.Status = "degraded"
	} else {
		c.health.LastError = ""
	}
	c.health.LastSuccessAt = time.Now().UTC()
	c.health.Imported += total.Receipt.Imported
	c.health.Duplicates += total.Receipt.Duplicates
	c.health.Outcomes.Merge(total.Outcomes)
	c.health.Pending = total.Outcomes.Pending()
	c.health.Sources = len(paths)
	c.mu.Unlock()
	return total, nil
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

// RunWithResults polls until ctx is cancelled and reports every completed poll
// to observer. The observer runs synchronously and must return promptly.
func (c *Collector) RunWithResults(ctx context.Context, observer func(ImportResult, error)) error {
	if err := ctx.Err(); err != nil {
		c.mu.Lock()
		c.health.Status = "stopped"
		c.mu.Unlock()
		return nil
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.health.Status = "stopped"
			c.mu.Unlock()
			return nil
		case <-ticker.C:
			result, err := c.PollOnce()
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

func discoverCollectorSources(roots []string) ([]string, error) {
	var paths []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			paths = append(paths, root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && (filepath.Ext(path) == ".jsonl" || filepath.Ext(path) == ".json") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}
