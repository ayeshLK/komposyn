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
	"strings"
	"testing"
)

func TestDiagnosticsAreSanitizedAndDeduplicated(t *testing.T) {
	store := openTestStore(t)
	first, err := store.RecordDiagnostic("failed token=secret-value at /home/private/project")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RecordDiagnostic("failed token=secret-value at /home/private/project")
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint || second.OccurrenceCount != 2 {
		t.Fatalf("unexpected deduplication: first=%+v second=%+v", first, second)
	}
	if strings.Contains(first.Message, "secret-value") || strings.Contains(first.Message, "/home/private") {
		t.Fatalf("diagnostic was not sanitized: %q", first.Message)
	}
	pending, err := store.PendingDiagnostics()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending diagnostics = %d, err = %v", len(pending), err)
	}
	if !strings.Contains(GitHubIssueDraftURL(first), "issues/new") {
		t.Fatal("missing issue draft URL")
	}
}
