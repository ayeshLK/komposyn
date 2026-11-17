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
	"strings"
	"testing"
)

func TestSearchFilterAndMatcherShareWorkAllowance(t *testing.T) {
	filterBudget := &directoryFilterBudget{ctx: context.Background(), remaining: maxDirectoryFilterWork}
	if err := filterBudget.consume(maxDirectoryFilterWork - 5); err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(searchSnapshotInput{
		Query: "needle", WorkBudget: filterBudget.remaining,
		Files: []searchSnapshotFile{{Content: []byte("needle")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(input)
	limited, err := searchSnapshotBytes(input)
	if err != nil || !limited.Incomplete || len(limited.Matches) != 0 {
		t.Fatalf("matcher with filter allowance remaining=%d returned %+v, err=%v; want truthful incomplete result", filterBudget.remaining, limited, err)
	}

	input, err = json.Marshal(searchSnapshotInput{
		Query: "needle", WorkBudget: maxSearchWorkUnits,
		Files: []searchSnapshotFile{{Content: []byte("needle")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(input)
	if _, err := searchSnapshotBytes(input); err != nil {
		t.Fatalf("matcher with full allowance: %v", err)
	}

	input, err = json.Marshal(searchSnapshotInput{
		Query: "needle", WorkBudget: 0,
		Files: []searchSnapshotFile{{Content: []byte("needle")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(input)
	limited, err = searchSnapshotBytes(input)
	if err != nil || !limited.Incomplete || len(limited.Matches) != 0 {
		t.Fatalf("matcher with zero remaining work returned %+v, err=%v; want truthful incomplete result", limited, err)
	}
}

func TestSearchRejectsChangedApplicableIgnoreRulesBeforePublication(t *testing.T) {
	for _, mutation := range []string{"nested", "ancestor"} {
		t.Run(mutation, func(t *testing.T) {
			workspace := t.TempDir()
			root := filepath.Join(workspace, "selected")
			nested := filepath.Join(root, "nested")
			if err := os.MkdirAll(nested, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, "control.go"), []byte("needle\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			prepared := prepareSearchDirectory(t, root, ListingOptions{Recursive: true})
			defer prepared.Close()
			filtered := filterSearchDirectorySnapshot(t, prepared)
			snapshot, err := prepared.prepareSearchSnapshot(context.Background(), filtered, "needle", maxSearchWorkUnits)
			clear(filtered)
			if err != nil {
				t.Fatal(err)
			}
			encoded := mustReadSearchSnapshot(t, snapshot.File)
			_, err = searchSnapshotBytes(encoded)
			clear(encoded)
			if err != nil {
				t.Fatal(err)
			}

			ignorePath := filepath.Join(nested, ".gitignore")
			if mutation == "ancestor" {
				ignorePath = filepath.Join(workspace, ".gitignore")
			}
			if err := os.WriteFile(ignorePath, []byte("control.go\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			validationCloseErr := prepared.validateAndCloseSearchSnapshot(context.Background(), &snapshot)
			if validationCloseErr == nil {
				t.Fatal("changed ignore observations did not invalidate the search snapshot")
			}
			var classifiedErr *searchSnapshotValidationError
			if !errors.As(validationCloseErr, &classifiedErr) || classifiedErr.validationErr == nil || classifiedErr.cleanupErr != nil {
				t.Fatalf("changed ignore observations were not distinguished from cleanup failure: %v", validationCloseErr)
			}
			if snapshot.File != nil || !searchCandidatesClosed(&snapshot) || prepared.searchDirectories != nil {
				t.Fatal("stale search snapshot retained private descriptors or directory observations")
			}
		})
	}
}

func mustReadSearchSnapshot(t *testing.T, file *os.File) []byte {
	t.Helper()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSearchFilteredCandidatesAppliesIgnoreAndSafeSkipBounds(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string][]byte{
		"match.txt":   []byte("Needle needle\r\nother\nneedle"),
		".hidden.txt": []byte("needle hidden\n"),
		"ignored.txt": []byte("needle ignored\n"),
		"binary.txt":  []byte{'n', 'e', 'e', 'd', 'l', 'e', 0},
		"invalid.txt": {0xff, 'n', 'e', 'e', 'd', 'l', 'e'},
		"large.txt":   append([]byte("needle"), make([]byte, MaxReadBytes)...),
	} {
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared := prepareSearchDirectory(t, root, ListingOptions{})
	defer prepared.Close()
	filtered := filterSearchDirectorySnapshot(t, prepared)
	result := executeSearchCandidates(t, prepared, filtered, "needle")
	if len(result.Matches) != 2 || result.Matches[0].Path != "match.txt" || result.Matches[0].Line != 1 || result.Matches[1].Line != 3 {
		t.Fatalf("matches=%+v", result.Matches)
	}
	if !result.Incomplete || result.SkippedFiles != 3 {
		t.Fatalf("incomplete=%t skipped=%d; want binary/invalid UTF-8/oversized skipped", result.Incomplete, result.SkippedFiles)
	}
	for _, match := range result.Matches {
		if match.Path == ".hidden.txt" || match.Path == "ignored.txt" {
			t.Fatalf("excluded path leaked: %+v", match)
		}
	}
}

func TestSearchResultLineAndEnvelopeBoundsAreTruthful(t *testing.T) {
	lines := make([]byte, 0, 2048)
	for range maxSearchLines + 1 {
		lines = append(lines, []byte("needle\n")...)
	}
	input, err := json.Marshal(searchSnapshotInput{Query: "needle", WorkBudget: maxSearchWorkUnits, Files: []searchSnapshotFile{{Content: lines}}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := searchSnapshotBytes(input)
	clear(input)
	if err != nil || len(worker.Matches) != maxSearchLines || !worker.Incomplete {
		t.Fatalf("worker matches=%d incomplete=%t err=%v", len(worker.Matches), worker.Incomplete, err)
	}
	data, err := json.Marshal(worker)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 1)
	result, err := finalizeSearchWorkerResult(context.Background(), WorkerResult{Outcome: ReadSucceeded, Bytes: data}, preparedSearchSnapshot{Paths: paths, Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded searchProviderResult
	if err := json.Unmarshal(result.Bytes, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Matches) != maxSearchLines || !decoded.Incomplete {
		t.Fatalf("line-bound result=%+v", decoded)
	}

	large := searchProviderResult{Matches: make([]searchProviderMatch, maxSearchLines)}
	for index := range large.Matches {
		large.Matches[index] = searchProviderMatch{Path: "nested/file.go", Line: index + 1, Snippet: "needle" + strings.Repeat("x", searchSnippetMaxBytes-6)}
	}
	bounded, err := encodeSearchProviderResult(context.Background(), large)
	if err != nil || bounded.Outcome != ReadSucceeded || len(bounded.Bytes) > maxSearchResultSize {
		t.Fatalf("bounded output outcome=%s bytes=%d err=%v", bounded.Outcome, len(bounded.Bytes), err)
	}
	if !bytes.Contains(bounded.Bytes, []byte(`"incomplete":true`)) {
		t.Fatalf("truncated result did not disclose incompleteness: %s", bounded.Bytes[:min(len(bounded.Bytes), 256)])
	}
}

func TestSearchFilteredCandidatesExcludesProtectedHardlinkAliasBeforeRead(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(t.TempDir(), "credential.fixture")
	if err := os.WriteFile(protected, []byte("secretneedle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "ordinary-looking.txt")
	if err := os.Link(protected, alias); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "credential-fixture", Class: ClassCredential, Path: protected}); err != nil {
		t.Fatal(err)
	}
	broker, ok := NewBroker(registry).(DirectoryListingBroker)
	if !ok {
		t.Fatal("broker lacks directory listing preparation")
	}
	generation := GenerationSet{Policy: registry.View().Generation}
	preparedRead, err := broker.PrepareDirectoryListWithOptions(root, root, generation, ListingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedRead.(*preparedDirectoryList)
	defer prepared.Close()
	filtered := filterSearchDirectorySnapshot(t, prepared)
	result := executeSearchCandidates(t, prepared, filtered, "needle")
	if len(result.Matches) != 0 || result.SkippedFiles != 0 || result.Incomplete {
		t.Fatalf("protected alias affected provider result: %+v", result)
	}
	if len(result.Raw) == 0 || bytes.Contains(result.Raw, []byte("ordinary-looking")) || bytes.Contains(result.Raw, []byte("secretneedle")) {
		t.Fatalf("protected path or content leaked: %q", result.Raw)
	}
}

func TestSearchSuppressesCandidateReplacedByProtectedObjectBeforePublication(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "candidate.go")
	if err := os.WriteFile(target, []byte("needle ordinary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(t.TempDir(), "credential.fixture")
	if err := os.WriteFile(protected, []byte("needle protected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "credential-fixture", Class: ClassCredential, Path: protected}); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(registry).(DirectoryListingBroker)
	preparedRead, err := broker.PrepareDirectoryListWithOptions(root, root, GenerationSet{Policy: registry.View().Generation}, ListingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedRead.(*preparedDirectoryList)
	defer prepared.Close()
	filtered := filterSearchDirectorySnapshot(t, prepared)
	snapshot, err := prepared.prepareSearchSnapshot(context.Background(), filtered, "needle", maxSearchWorkUnits)
	clear(filtered)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(snapshot.File)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := searchSnapshotBytes(encoded)
	clear(encoded)
	if err != nil {
		t.Fatal(err)
	}
	workerBytes, err := json.Marshal(worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(protected, target); err != nil {
		t.Fatal("replace candidate with protected object:", err)
	}
	if err := prepared.validateAndCloseSearchSnapshot(context.Background(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Changed) != 1 || snapshot.Changed[0] != 0 {
		t.Fatalf("changed candidate indexes=%v, want [0]", snapshot.Changed)
	}
	result, err := finalizeSearchWorkerResult(context.Background(), WorkerResult{Outcome: ReadSucceeded, Bytes: workerBytes}, snapshot)
	clear(workerBytes)
	if err != nil || result.Outcome != ReadSucceeded {
		t.Fatalf("final search result=%+v err=%v", result, err)
	}
	var payload searchProviderResult
	if err := json.Unmarshal(result.Bytes, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Matches) != 0 || !payload.Incomplete || payload.SkippedFiles != 1 {
		t.Fatalf("changed protected candidate was published or claimed complete: %+v", payload)
	}
}

func prepareSearchDirectory(t *testing.T, root string, options ListingOptions) *preparedDirectoryList {
	t.Helper()
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	broker, ok := NewBroker(registry).(DirectoryListingBroker)
	if !ok {
		t.Fatal("broker lacks directory listing preparation")
	}
	preparedRead, err := broker.PrepareDirectoryListWithOptions(root, root, GenerationSet{Policy: registry.View().Generation}, options)
	if err != nil {
		t.Fatal(err)
	}
	return preparedRead.(*preparedDirectoryList)
}

func filterSearchDirectorySnapshot(t *testing.T, prepared *preparedDirectoryList) []byte {
	t.Helper()
	raw := listDirectorySearchBounded(int(prepared.directory.Fd()), prepared.ListingOptions())
	if raw.Outcome != ReadSucceeded || raw.Truncated {
		t.Fatalf("search traversal result=%+v", raw)
	}
	filtered, err := prepared.filterSearchListing(context.Background(), raw.Bytes)
	clear(raw.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return filtered
}

func executeSearchCandidates(t *testing.T, prepared *preparedDirectoryList, filtered []byte, query string) searchProviderResult {
	t.Helper()
	defer clear(filtered)
	snapshot, err := prepared.prepareSearchSnapshot(context.Background(), filtered, query, maxSearchWorkUnits)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.validateAndCloseSearchSnapshot(context.Background(), &snapshot)
	encoded, err := io.ReadAll(snapshot.File)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := searchSnapshotBytes(encoded)
	clear(encoded)
	if err != nil {
		t.Fatal(err)
	}
	workerBytes, err := json.Marshal(worker)
	if err != nil {
		t.Fatal(err)
	}
	workerResult, err := finalizeSearchWorkerResult(context.Background(), WorkerResult{Outcome: ReadSucceeded, Bytes: workerBytes}, snapshot)
	clear(workerBytes)
	if err != nil || workerResult.Outcome != ReadSucceeded {
		t.Fatalf("search result=%+v err=%v", workerResult, err)
	}
	var result searchProviderResult
	if err := json.Unmarshal(workerResult.Bytes, &result); err != nil {
		t.Fatalf("search result payload %q: %v", workerResult.Bytes, err)
	}
	result.Raw = workerResult.Bytes
	return result
}
