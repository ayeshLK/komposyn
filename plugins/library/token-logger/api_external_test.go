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

package tokenlogger_test

import (
	"testing"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

// This contract test deliberately uses only the package's exported API. It
// also ensures the core remains usable without importing the CLI package.
func TestExternalConsumerCoreContract(t *testing.T) {
	store, err := tokenlogger.Open(t.TempDir() + "/usage.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	event := tokenlogger.UsageEvent{
		EventID: "external-event", SchemaVersion: 1, EventType: "model.request",
		OccurredAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		Client:     tokenlogger.Client{Name: "external-client"}, Provider: "example", RequestedModel: "example-model",
		Usage:        tokenlogger.TokenUsage{InputTokens: 100, OutputTokens: 20},
		Provenance:   tokenlogger.Provenance{Adapter: "external", SourceID: "source", SourceRecord: "record"},
		IngestionKey: "external-ingestion-key",
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.AppendWithCursor([]tokenlogger.UsageEvent{event}, tokenlogger.Cursor{SourceID: "source", ByteOffset: 42})
	if err != nil || receipt.Imported != 1 {
		t.Fatalf("append receipt = %+v, %v", receipt, err)
	}
	report, err := store.Query(tokenlogger.UsageFilter{
		From:    time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		GroupBy: []string{tokenlogger.GroupProvider, tokenlogger.GroupModel},
	})
	if err != nil || report.Summary.TotalTokens != 120 {
		t.Fatalf("query summary = %+v, %v", report.Summary, err)
	}
	if _, err := store.ListProjects(tokenlogger.ResourceFilter{}); err != nil {
		t.Fatalf("list resources: %v", err)
	}
	registered, err := store.RegisterCollectorSource(t.TempDir(), false)
	if err != nil || registered.Source.ID == "" {
		t.Fatalf("register collector source = %+v, %v", registered, err)
	}
	sources, err := store.ListCollectorSources(false)
	if err != nil || len(sources) != 1 {
		t.Fatalf("list collector sources = %+v, %v", sources, err)
	}
}

func TestExternalDiagnosticFoundationContract(t *testing.T) {
	observer, err := tokenlogger.NewDiagnosticObserver(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := observer.Stats(); got.Capacity != tokenlogger.DefaultDiagnosticObserverCapacity || got.Admitted != 0 || got.Dropped != 0 {
		t.Fatalf("initial observer stats = %+v", got)
	}
	if observer.Events() == nil {
		t.Fatal("observer returned a nil event stream")
	}
	store, err := tokenlogger.OpenWithObservation(":memory:", tokenlogger.OpenOptions{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityAvailable}).Validate(); err != nil {
		t.Fatalf("known zero measurement = %v", err)
	}
	if err := (tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected}).Validate(); err != nil {
		t.Fatalf("unavailable measurement = %v", err)
	}
	classification := tokenlogger.DiagnosticClassification{
		Component: tokenlogger.DiagnosticComponentUnknown, Operation: tokenlogger.DiagnosticOperationOpen,
		Reason: tokenlogger.DiagnosticReasonFailureUnclassified, Severity: tokenlogger.DiagnosticSeverityError,
		Category: tokenlogger.DiagnosticCategoryUnknown, Adapter: tokenlogger.DiagnosticAdapterUnavailable,
		Retryability: tokenlogger.DiagnosticRetryUnknown, Impact: tokenlogger.DiagnosticImpactUnknown,
		ExplanationCode: tokenlogger.DiagnosticExplanationFailureUnclassified,
		RemediationCode: tokenlogger.DiagnosticRemediationReviewOperation,
	}
	event := tokenlogger.DiagnosticEvent{
		ContractVersion: tokenlogger.DiagnosticContractVersion, CoreVersion: "0.0.0-dev", ObservedAt: time.Now().UTC(),
		Kind: tokenlogger.DiagnosticEventOperationFailed, Classification: classification, HasClassification: true,
		OperationID:          "00112233445566778899aabbccddeeff",
		Subject:              tokenlogger.DiagnosticSubject{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		SourceSchemaVersion:  tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		TargetSchemaVersion:  tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		DurationMilliseconds: tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		RetryAttempt:         tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		RetryCount:           tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		Position: tokenlogger.DiagnosticPosition{
			ByteOffset: tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
			LineNumber: tokenlogger.DiagnosticOptionalUint64{Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
		},
		Evidence: tokenlogger.DiagnosticEvidence{Scope: tokenlogger.DiagnosticEvidenceOperation,
			Availability:      tokenlogger.DiagnosticAvailabilityNotCollected,
			UnavailableReason: tokenlogger.DiagnosticUnavailableNotCollected},
		Counts: tokenlogger.DiagnosticOutcomeCounts{Scope: tokenlogger.DiagnosticCountOperation,
			Availability: tokenlogger.DiagnosticAvailabilityNotCollected},
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("external event validation = %v", err)
	}
}
