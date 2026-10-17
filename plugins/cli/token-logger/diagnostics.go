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
	"fmt"
	"net/url"
	"strings"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

func recordDiagnostic(store *tokenlogger.Store, message, category string) (tokenlogger.Diagnostic, error) {
	return store.RecordDiagnosticWithMetadata(message, tokenlogger.DiagnosticMetadata{
		Category: category, CoreVersion: tokenlogger.CoreVersion(), CLIVersion: cliVersion, ProtocolVersion: protocolVersion,
		SchemaVersion: tokenlogger.SchemaVersion(),
	})
}

func diagnosticIssueDraftURL(diagnostic tokenlogger.Diagnostic) (string, bool) {
	if strings.EqualFold(diagnostic.Category, tokenlogger.DiagnosticCategorySecuritySensitive) {
		return "", false
	}
	repository := "komposyn-lg"
	switch diagnostic.Category {
	case tokenlogger.DiagnosticCategoryProvider, tokenlogger.DiagnosticCategoryIngestion,
		tokenlogger.DiagnosticCategoryStorage, tokenlogger.DiagnosticCategoryMigration,
		tokenlogger.DiagnosticCategoryPricing, tokenlogger.DiagnosticCategoryReporting,
		tokenlogger.DiagnosticCategoryValidation:
		repository = "komposyn"
	}
	fingerprint := diagnostic.Fingerprint
	if len(fingerprint) > 12 {
		fingerprint = fingerprint[:12]
	}
	title := fmt.Sprintf("KompoSyn %s diagnostic: %s", repository, fingerprint)
	body := fmt.Sprintf("Category: %s\nCore version: %s\nCLI version: %s\nProtocol version: %d\nSQLite schema version: %d\n\nSanitized fingerprint: %s\n\nMessage:\n%s\n\nOccurrences: %d",
		diagnostic.Category, diagnostic.CoreVersion, diagnostic.CLIVersion, diagnostic.ProtocolVersion,
		diagnostic.SchemaVersion, diagnostic.Fingerprint, diagnostic.Message, diagnostic.OccurrenceCount)
	return "https://github.com/ayeshLK/" + repository + "/issues/new?" + url.Values{"title": {title}, "body": {body}}.Encode(), true
}

func isSecuritySensitiveDiagnostic(diagnostic tokenlogger.Diagnostic) bool {
	return strings.EqualFold(diagnostic.Category, tokenlogger.DiagnosticCategorySecuritySensitive)
}
