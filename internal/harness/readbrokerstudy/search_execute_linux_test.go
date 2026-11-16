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
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchFilteredCandidatesAppliesIgnoreAndSafeSkipBounds(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string][]byte{
		"match.txt":   []byte("Needle needle\r\nother\nneedle"),
		".hidden.txt": []byte("needle hidden\n"),
		"ignored.txt": []byte("needle ignored\n"),
		"binary.txt":  []byte{'n', 'e', 'e', 'd', 'l', 'e', 0},
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
	if !result.Incomplete || result.SkippedFiles != 2 {
		t.Fatalf("incomplete=%t skipped=%d; want binary/oversized skipped", result.Incomplete, result.SkippedFiles)
	}
	for _, match := range result.Matches {
		if match.Path == ".hidden.txt" || match.Path == "ignored.txt" {
			t.Fatalf("excluded path leaked: %+v", match)
		}
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
	snapshot, err := prepared.prepareSearchSnapshot(context.Background(), filtered, query)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.File.Close()
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
