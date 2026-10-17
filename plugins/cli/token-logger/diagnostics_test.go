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

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

func TestDiagnosticIssueRoutingUsesCategoryAndBlocksSecurityDrafts(t *testing.T) {
	tests := []struct {
		category string
		repo     string
	}{
		{tokenlogger.DiagnosticCategoryCLI, "komposyn-lg"},
		{tokenlogger.DiagnosticCategoryUnknown, "komposyn-lg"},
		{tokenlogger.DiagnosticCategoryIngestion, "komposyn"},
		{tokenlogger.DiagnosticCategoryMigration, "komposyn"},
	}
	for _, test := range tests {
		urlValue, ok := diagnosticIssueDraftURL(tokenlogger.Diagnostic{
			Fingerprint: "0123456789abcdef", Category: test.category, Message: "sanitized", OccurrenceCount: 1,
		})
		if !ok {
			t.Fatalf("category %q did not produce a draft", test.category)
		}
		parsed, err := url.Parse(urlValue)
		if err != nil || !strings.Contains(parsed.Path, "/"+test.repo+"/issues/new") {
			t.Fatalf("category %q routed to %q, err=%v", test.category, urlValue, err)
		}
	}
	if draft, ok := diagnosticIssueDraftURL(tokenlogger.Diagnostic{Category: tokenlogger.DiagnosticCategorySecuritySensitive}); ok || draft != "" {
		t.Fatalf("security-sensitive diagnostic produced public draft %q", draft)
	}
}

func TestCollectorDiagnosticRoutesToCoreTracker(t *testing.T) {
	dataDir := t.TempDir()
	store, err := tokenlogger.Open(filepath.Join(dataDir, "token-logger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "malformed.jsonl")
	fixture := `{"type":"assistant","uuid":"diagnostic-fixture","sessionId":"diagnostic-session","cwd":"/sanitized/project","timestamp":"2026-10-11T12:00:00Z","message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n{not-json}\n"
	if err := os.WriteFile(sourcePath, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := tokenlogger.NewCollector(store, []string{sourceDir}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.PollOnce(); err != nil {
		t.Fatal(err)
	}
	diagnostics, err := store.PendingDiagnostics()
	if err != nil || len(diagnostics) != 1 {
		t.Fatalf("pending collector diagnostics = %+v, err=%v", diagnostics, err)
	}
	diagnostic := diagnostics[0]
	if diagnostic.Category != tokenlogger.DiagnosticCategoryIngestion || diagnostic.CoreVersion == "" || diagnostic.SchemaVersion == 0 {
		t.Fatalf("collector diagnostic omitted core metadata: %+v", diagnostic)
	}
	draft, ok := diagnosticIssueDraftURL(diagnostic)
	parsed, parseErr := url.Parse(draft)
	if !ok || parseErr != nil || !strings.Contains(parsed.Path, "/ayeshLK/komposyn/issues/new") {
		t.Fatalf("collector diagnostic routed to %q, ok=%v err=%v", draft, ok, parseErr)
	}
}
