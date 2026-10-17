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
	"strings"
	"testing"

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
