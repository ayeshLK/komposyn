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
	if first.Category != DiagnosticCategoryUnknown {
		t.Fatalf("legacy diagnostic category = %q", first.Category)
	}
	if _, err := store.DiagnosticReport(); err != nil {
		t.Fatalf("build destination-neutral report: %v", err)
	}
}

func TestDiagnosticMetadataIsSanitizedAndPersisted(t *testing.T) {
	store := openTestStore(t)
	want := DiagnosticMetadata{Category: DiagnosticCategoryMigration, CoreVersion: "0.6.0", CLIVersion: "0.7.0", ProtocolVersion: 1, SchemaVersion: CurrentSchemaVersion}
	diagnostic, err := store.RecordDiagnosticWithMetadata("migration failed token=secret at /home/private/db", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Diagnostic(diagnostic.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != want.Category || got.CoreVersion != want.CoreVersion || got.CLIVersion != want.CLIVersion ||
		got.ProtocolVersion != want.ProtocolVersion || got.SchemaVersion != want.SchemaVersion {
		t.Fatalf("persisted diagnostic metadata = %+v, want %+v", got, want)
	}
	if strings.Contains(got.Message, "secret") || strings.Contains(got.Message, "/home/private") {
		t.Fatalf("diagnostic message was not sanitized: %q", got.Message)
	}
}

func TestDiagnosticRecurrencePromotesAndKeepsSecurityClassification(t *testing.T) {
	store := openTestStore(t)
	first, err := store.RecordDiagnostic("same failure")
	if err != nil {
		t.Fatal(err)
	}
	security, err := store.RecordDiagnosticWithMetadata("same failure", DiagnosticMetadata{
		Category: DiagnosticCategorySecuritySensitive, CoreVersion: "1.2.3", SchemaVersion: CurrentSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if security.Fingerprint != first.Fingerprint || security.Category != DiagnosticCategorySecuritySensitive || security.OccurrenceCount != 2 {
		t.Fatalf("security recurrence = %+v; wanted promoted, deduplicated security diagnostic", security)
	}

	repeated, err := store.RecordDiagnosticWithMetadata("same failure", DiagnosticMetadata{
		Category: DiagnosticCategoryIngestion, CoreVersion: "9.9.9", SchemaVersion: CurrentSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Category != DiagnosticCategorySecuritySensitive || repeated.OccurrenceCount != 3 || repeated.CoreVersion != "1.2.3" {
		t.Fatalf("ordinary recurrence downgraded or overwrote diagnostic metadata: %+v", repeated)
	}
}
