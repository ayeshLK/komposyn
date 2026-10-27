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
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"
)

// DiagnosticContractVersion versions the additive structured diagnostics DTOs.
// It is independent of the SQLite schema and harness protocol versions.
const DiagnosticContractVersion = 1

const MaxDiagnosticItemBytes = 8 * 1024

type DiagnosticComponent string
type DiagnosticOperation string
type DiagnosticReason string
type DiagnosticSeverity string
type DiagnosticAdapter string
type DiagnosticRetryability string
type DiagnosticImpact string
type DiagnosticExplanationCode string
type DiagnosticRemediationCode string
type DiagnosticEventKind string
type DiagnosticID string
type DiagnosticSubjectKind string
type DiagnosticIdentityScope string
type DiagnosticAvailability string
type DiagnosticUnavailableReason string
type DiagnosticCountScope string
type DiagnosticEmissionDisposition string
type DiagnosticCategory string
type DiagnosticEvidenceScope string

const (
	DiagnosticComponentSource      DiagnosticComponent = "source"
	DiagnosticComponentParser      DiagnosticComponent = "parser"
	DiagnosticComponentStorage     DiagnosticComponent = "storage"
	DiagnosticComponentSchema      DiagnosticComponent = "schema"
	DiagnosticComponentMigration   DiagnosticComponent = "migration"
	DiagnosticComponentCollector   DiagnosticComponent = "collector"
	DiagnosticComponentDiagnostics DiagnosticComponent = "diagnostics"
	DiagnosticComponentUnknown     DiagnosticComponent = "unknown"
)

const (
	DiagnosticOperationOpen             DiagnosticOperation = "open"
	DiagnosticOperationInspectSchema    DiagnosticOperation = "inspect_schema"
	DiagnosticOperationBackup           DiagnosticOperation = "backup"
	DiagnosticOperationMigrate          DiagnosticOperation = "migrate"
	DiagnosticOperationDiscover         DiagnosticOperation = "discover"
	DiagnosticOperationRead             DiagnosticOperation = "read"
	DiagnosticOperationClassify         DiagnosticOperation = "classify"
	DiagnosticOperationParse            DiagnosticOperation = "parse"
	DiagnosticOperationPersist          DiagnosticOperation = "persist"
	DiagnosticOperationAcquireLease     DiagnosticOperation = "acquire_lease"
	DiagnosticOperationHeartbeat        DiagnosticOperation = "heartbeat"
	DiagnosticOperationRequestStop      DiagnosticOperation = "request_stop"
	DiagnosticOperationReleaseLease     DiagnosticOperation = "release_lease"
	DiagnosticOperationQueryDiagnostics DiagnosticOperation = "query_diagnostics"
	DiagnosticOperationReviewDiagnostic DiagnosticOperation = "review_diagnostic"
)

const (
	DiagnosticReasonPermissionDenied       DiagnosticReason = "permission_denied"
	DiagnosticReasonSourceUnavailable      DiagnosticReason = "source_unavailable"
	DiagnosticReasonIOFailure              DiagnosticReason = "io_failure"
	DiagnosticReasonSnapshotUnstable       DiagnosticReason = "snapshot_unstable"
	DiagnosticReasonSchemaUnsupported      DiagnosticReason = "schema_unsupported"
	DiagnosticReasonBackupFailed           DiagnosticReason = "backup_failed"
	DiagnosticReasonMigrationFailed        DiagnosticReason = "migration_failed"
	DiagnosticReasonStorageBusy            DiagnosticReason = "storage_busy"
	DiagnosticReasonStorageLocked          DiagnosticReason = "storage_locked"
	DiagnosticReasonStorageFull            DiagnosticReason = "storage_full"
	DiagnosticReasonStorageReadonly        DiagnosticReason = "storage_readonly"
	DiagnosticReasonStorageCorrupt         DiagnosticReason = "storage_corrupt"
	DiagnosticReasonStorageFailure         DiagnosticReason = "storage_failure"
	DiagnosticReasonInvalidJSON            DiagnosticReason = "invalid_json"
	DiagnosticReasonInvalidTimestamp       DiagnosticReason = "invalid_timestamp"
	DiagnosticReasonInvalidTokenCount      DiagnosticReason = "invalid_token_count"
	DiagnosticReasonRequiredContextMissing DiagnosticReason = "required_context_missing"
	DiagnosticReasonUnsupportedUsage       DiagnosticReason = "unsupported_usage"
	DiagnosticReasonMalformedRecord        DiagnosticReason = "malformed_record"
	DiagnosticReasonLeaseActive            DiagnosticReason = "lease_active"
	DiagnosticReasonLeaseLost              DiagnosticReason = "lease_lost"
	DiagnosticReasonCancelled              DiagnosticReason = "cancelled"
	DiagnosticReasonDeadlineExceeded       DiagnosticReason = "deadline_exceeded"
	DiagnosticReasonFailureUnclassified    DiagnosticReason = "failure_unclassified"
)

const (
	DiagnosticSeverityInfo    DiagnosticSeverity = "info"
	DiagnosticSeverityWarning DiagnosticSeverity = "warning"
	DiagnosticSeverityError   DiagnosticSeverity = "error"
)

const (
	DiagnosticAdapterClaude      DiagnosticAdapter = "claude"
	DiagnosticAdapterCodex       DiagnosticAdapter = "codex"
	DiagnosticAdapterUnknown     DiagnosticAdapter = "unknown"
	DiagnosticAdapterUnavailable DiagnosticAdapter = ""
)

const (
	DiagnosticRetryable    DiagnosticRetryability = "retryable"
	DiagnosticNotRetryable DiagnosticRetryability = "not_retryable"
	DiagnosticRetryUnknown DiagnosticRetryability = "unknown"
)

const (
	DiagnosticImpactNone                     DiagnosticImpact = "none"
	DiagnosticImpactIngestionMayBeIncomplete DiagnosticImpact = "ingestion_may_be_incomplete"
	DiagnosticImpactIngestionUnavailable     DiagnosticImpact = "ingestion_unavailable"
	DiagnosticImpactInspectionUnavailable    DiagnosticImpact = "inspection_unavailable"
	DiagnosticImpactUnknown                  DiagnosticImpact = "unknown"
)

const (
	DiagnosticEventOperationFailed    DiagnosticEventKind = "operation_failed"
	DiagnosticEventConditionSummary   DiagnosticEventKind = "condition_summary"
	DiagnosticEventPollSummary        DiagnosticEventKind = "poll_summary"
	DiagnosticEventOperationRecovered DiagnosticEventKind = "operation_recovered"
)

const (
	DiagnosticSubjectSource          DiagnosticSubjectKind = "source"
	DiagnosticSubjectDiscoveryTarget DiagnosticSubjectKind = "discovery_target"
	DiagnosticSubjectRoot            DiagnosticSubjectKind = "root"
	DiagnosticSubjectStore           DiagnosticSubjectKind = "store"
	DiagnosticSubjectUnknown         DiagnosticSubjectKind = "unknown"
)

const (
	DiagnosticIdentityStoreHandle DiagnosticIdentityScope = "store_handle"
	DiagnosticIdentityStore       DiagnosticIdentityScope = "store"
)

const (
	DiagnosticAvailabilityAvailable     DiagnosticAvailability = "available"
	DiagnosticAvailabilityNotCollected  DiagnosticAvailability = "not_collected"
	DiagnosticAvailabilityNotApplicable DiagnosticAvailability = "not_applicable"
	DiagnosticAvailabilityUnsupported   DiagnosticAvailability = "unsupported"
	DiagnosticAvailabilityRedacted      DiagnosticAvailability = "redacted"
	DiagnosticAvailabilityLost          DiagnosticAvailability = "lost"
	DiagnosticAvailabilityUnknown       DiagnosticAvailability = "unknown"
)

const (
	DiagnosticUnavailableNotCollected  DiagnosticUnavailableReason = "not_collected"
	DiagnosticUnavailableNotApplicable DiagnosticUnavailableReason = "not_applicable"
	DiagnosticUnavailableUnsupported   DiagnosticUnavailableReason = "unsupported"
	DiagnosticUnavailableRedacted      DiagnosticUnavailableReason = "redacted"
	DiagnosticUnavailableLost          DiagnosticUnavailableReason = "lost"
	DiagnosticUnavailableUnknown       DiagnosticUnavailableReason = "unknown"
)

const (
	DiagnosticCountOperation DiagnosticCountScope = "operation"
	DiagnosticCountPoll      DiagnosticCountScope = "poll"
	DiagnosticCountRun       DiagnosticCountScope = "run"
	DiagnosticCountLifetime  DiagnosticCountScope = "lifetime"
)

const (
	DiagnosticEvidenceOperation        DiagnosticEvidenceScope = "operation"
	DiagnosticEvidencePoll             DiagnosticEvidenceScope = "poll"
	DiagnosticEvidenceRun              DiagnosticEvidenceScope = "run"
	DiagnosticEvidenceSourceGeneration DiagnosticEvidenceScope = "source_generation"
)

const (
	DiagnosticEmissionDisabled    DiagnosticEmissionDisposition = "disabled"
	DiagnosticEmissionQueued      DiagnosticEmissionDisposition = "queued"
	DiagnosticEmissionDropped     DiagnosticEmissionDisposition = "dropped"
	DiagnosticEmissionUnavailable DiagnosticEmissionDisposition = "unavailable"
)

var ErrInvalidDiagnostic = errors.New("invalid diagnostic value")

var diagnosticVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
var diagnosticHexIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var diagnosticHexReferencePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type diagnosticReasonSpec struct {
	components   map[DiagnosticComponent]bool
	operations   map[DiagnosticOperation]bool
	severity     DiagnosticSeverity
	category     DiagnosticCategory
	retryability DiagnosticRetryability
	impact       DiagnosticImpact
	remediation  DiagnosticRemediationCode
}

func diagnosticReasonSpecFor(reason DiagnosticReason) (diagnosticReasonSpec, bool) {
	if len(reason) > len("required_context_missing") {
		return diagnosticReasonSpec{}, false
	}
	anyComponents := set(DiagnosticComponentSource, DiagnosticComponentParser, DiagnosticComponentStorage,
		DiagnosticComponentSchema, DiagnosticComponentMigration, DiagnosticComponentCollector,
		DiagnosticComponentDiagnostics, DiagnosticComponentUnknown)
	anyOperations := set(DiagnosticOperationOpen, DiagnosticOperationInspectSchema, DiagnosticOperationBackup,
		DiagnosticOperationMigrate, DiagnosticOperationDiscover, DiagnosticOperationRead,
		DiagnosticOperationClassify, DiagnosticOperationParse, DiagnosticOperationPersist,
		DiagnosticOperationAcquireLease, DiagnosticOperationHeartbeat, DiagnosticOperationRequestStop,
		DiagnosticOperationReleaseLease, DiagnosticOperationQueryDiagnostics, DiagnosticOperationReviewDiagnostic)
	storageOperations := set(DiagnosticOperationOpen, DiagnosticOperationBackup, DiagnosticOperationMigrate,
		DiagnosticOperationPersist, DiagnosticOperationQueryDiagnostics, DiagnosticOperationReviewDiagnostic)
	parserOperations := set(DiagnosticOperationClassify, DiagnosticOperationParse)
	sourceOperations := set(DiagnosticOperationDiscover, DiagnosticOperationRead)
	collectorOperations := set(DiagnosticOperationAcquireLease, DiagnosticOperationHeartbeat,
		DiagnosticOperationRequestStop, DiagnosticOperationReleaseLease)
	makeSpec := func(component DiagnosticComponent, operations map[DiagnosticOperation]bool, severity DiagnosticSeverity,
		category DiagnosticCategory, retryability DiagnosticRetryability, impact DiagnosticImpact,
		remediation DiagnosticRemediationCode) diagnosticReasonSpec {
		return diagnosticReasonSpec{components: set(component), operations: operations, severity: severity,
			category: category, retryability: retryability, impact: impact, remediation: remediation}
	}
	storageFailure := func() diagnosticReasonSpec {
		return makeSpec(DiagnosticComponentStorage, storageOperations, DiagnosticSeverityError,
			DiagnosticCategory(DiagnosticCategoryStorage), DiagnosticRetryUnknown,
			DiagnosticImpactIngestionUnavailable, DiagnosticRemediationReviewStorage)
	}
	switch reason {
	case DiagnosticReasonPermissionDenied:
		return diagnosticReasonSpec{components: set(DiagnosticComponentSource, DiagnosticComponentStorage),
			operations: set(DiagnosticOperationOpen, DiagnosticOperationBackup, DiagnosticOperationDiscover, DiagnosticOperationRead),
			severity:   DiagnosticSeverityError, category: DiagnosticCategoryProvider,
			retryability: DiagnosticRetryUnknown, impact: DiagnosticImpactIngestionUnavailable,
			remediation: DiagnosticRemediationReviewAccess}, true
	case DiagnosticReasonSourceUnavailable:
		return makeSpec(DiagnosticComponentSource, sourceOperations, DiagnosticSeverityError,
			DiagnosticCategoryProvider, DiagnosticRetryUnknown, DiagnosticImpactIngestionUnavailable,
			DiagnosticRemediationReviewSource), true
	case DiagnosticReasonIOFailure:
		return diagnosticReasonSpec{components: set(DiagnosticComponentSource, DiagnosticComponentStorage),
			operations: union(sourceOperations, storageOperations), severity: DiagnosticSeverityError,
			category: DiagnosticCategoryStorage, retryability: DiagnosticRetryUnknown,
			impact: DiagnosticImpactIngestionUnavailable, remediation: DiagnosticRemediationReviewStorage}, true
	case DiagnosticReasonSnapshotUnstable:
		return makeSpec(DiagnosticComponentSchema, set(DiagnosticOperationInspectSchema, DiagnosticOperationOpen),
			DiagnosticSeverityWarning, DiagnosticCategoryMigration, DiagnosticRetryable,
			DiagnosticImpactInspectionUnavailable, DiagnosticRemediationRetryInspection), true
	case DiagnosticReasonSchemaUnsupported:
		return makeSpec(DiagnosticComponentSchema, set(DiagnosticOperationInspectSchema, DiagnosticOperationOpen),
			DiagnosticSeverityError, DiagnosticCategoryMigration, DiagnosticNotRetryable,
			DiagnosticImpactInspectionUnavailable, DiagnosticRemediationUpgradeCore), true
	case DiagnosticReasonBackupFailed:
		return makeSpec(DiagnosticComponentMigration, set(DiagnosticOperationBackup, DiagnosticOperationMigrate),
			DiagnosticSeverityError, DiagnosticCategoryMigration, DiagnosticRetryUnknown,
			DiagnosticImpactIngestionUnavailable, DiagnosticRemediationReviewBackup), true
	case DiagnosticReasonMigrationFailed:
		return makeSpec(DiagnosticComponentMigration, set(DiagnosticOperationMigrate), DiagnosticSeverityError,
			DiagnosticCategoryMigration, DiagnosticRetryUnknown, DiagnosticImpactIngestionUnavailable,
			DiagnosticRemediationReviewMigration), true
	case DiagnosticReasonStorageBusy:
		spec := storageFailure()
		spec.retryability = DiagnosticRetryable
		return spec, true
	case DiagnosticReasonStorageLocked:
		spec := storageFailure()
		spec.retryability = DiagnosticRetryable
		return spec, true
	case DiagnosticReasonStorageFull, DiagnosticReasonStorageReadonly, DiagnosticReasonStorageCorrupt, DiagnosticReasonStorageFailure:
		return storageFailure(), true
	case DiagnosticReasonInvalidJSON, DiagnosticReasonInvalidTimestamp, DiagnosticReasonInvalidTokenCount,
		DiagnosticReasonRequiredContextMissing, DiagnosticReasonUnsupportedUsage, DiagnosticReasonMalformedRecord:
		return makeSpec(DiagnosticComponentParser, parserOperations, DiagnosticSeverityWarning,
			DiagnosticCategoryValidation, DiagnosticNotRetryable, DiagnosticImpactIngestionMayBeIncomplete,
			DiagnosticRemediationReviewRecord), true
	case DiagnosticReasonLeaseActive:
		return makeSpec(DiagnosticComponentCollector, set(DiagnosticOperationAcquireLease), DiagnosticSeverityInfo,
			DiagnosticCategoryIngestion, DiagnosticRetryable, DiagnosticImpactNone,
			DiagnosticRemediationWaitForCollector), true
	case DiagnosticReasonLeaseLost:
		return makeSpec(DiagnosticComponentCollector, collectorOperations, DiagnosticSeverityWarning,
			DiagnosticCategoryIngestion, DiagnosticRetryable, DiagnosticImpactIngestionMayBeIncomplete,
			DiagnosticRemediationCheckCollector), true
	case DiagnosticReasonCancelled:
		return diagnosticReasonSpec{components: anyComponents, operations: anyOperations, severity: DiagnosticSeverityInfo,
			category: DiagnosticCategoryUnknown, retryability: DiagnosticRetryable, impact: DiagnosticImpactNone,
			remediation: DiagnosticRemediationRetryOperation}, true
	case DiagnosticReasonDeadlineExceeded:
		return diagnosticReasonSpec{components: anyComponents, operations: anyOperations, severity: DiagnosticSeverityWarning,
			category: DiagnosticCategoryUnknown, retryability: DiagnosticRetryable, impact: DiagnosticImpactUnknown,
			remediation: DiagnosticRemediationRetryOperation}, true
	case DiagnosticReasonFailureUnclassified:
		return diagnosticReasonSpec{components: anyComponents, operations: anyOperations, severity: DiagnosticSeverityError,
			category: DiagnosticCategoryUnknown, retryability: DiagnosticRetryUnknown, impact: DiagnosticImpactUnknown,
			remediation: DiagnosticRemediationReviewOperation}, true
	default:
		return diagnosticReasonSpec{}, false
	}
}

func set[T comparable](values ...T) map[T]bool {
	result := make(map[T]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func union[T comparable](maps ...map[T]bool) map[T]bool {
	result := make(map[T]bool)
	for _, values := range maps {
		for value := range values {
			result[value] = true
		}
	}
	return result
}

const (
	DiagnosticExplanationPermissionDenied       DiagnosticExplanationCode = "explain_permission_denied"
	DiagnosticExplanationSourceUnavailable      DiagnosticExplanationCode = "explain_source_unavailable"
	DiagnosticExplanationIOFailure              DiagnosticExplanationCode = "explain_io_failure"
	DiagnosticExplanationSnapshotUnstable       DiagnosticExplanationCode = "explain_snapshot_unstable"
	DiagnosticExplanationSchemaUnsupported      DiagnosticExplanationCode = "explain_schema_unsupported"
	DiagnosticExplanationBackupFailed           DiagnosticExplanationCode = "explain_backup_failed"
	DiagnosticExplanationMigrationFailed        DiagnosticExplanationCode = "explain_migration_failed"
	DiagnosticExplanationStorageBusy            DiagnosticExplanationCode = "explain_storage_busy"
	DiagnosticExplanationStorageLocked          DiagnosticExplanationCode = "explain_storage_locked"
	DiagnosticExplanationStorageFull            DiagnosticExplanationCode = "explain_storage_full"
	DiagnosticExplanationStorageReadonly        DiagnosticExplanationCode = "explain_storage_readonly"
	DiagnosticExplanationStorageCorrupt         DiagnosticExplanationCode = "explain_storage_corrupt"
	DiagnosticExplanationStorageFailure         DiagnosticExplanationCode = "explain_storage_failure"
	DiagnosticExplanationInvalidJSON            DiagnosticExplanationCode = "explain_invalid_json"
	DiagnosticExplanationInvalidTimestamp       DiagnosticExplanationCode = "explain_invalid_timestamp"
	DiagnosticExplanationInvalidTokenCount      DiagnosticExplanationCode = "explain_invalid_token_count"
	DiagnosticExplanationRequiredContextMissing DiagnosticExplanationCode = "explain_required_context_missing"
	DiagnosticExplanationUnsupportedUsage       DiagnosticExplanationCode = "explain_unsupported_usage"
	DiagnosticExplanationMalformedRecord        DiagnosticExplanationCode = "explain_malformed_record"
	DiagnosticExplanationLeaseActive            DiagnosticExplanationCode = "explain_lease_active"
	DiagnosticExplanationLeaseLost              DiagnosticExplanationCode = "explain_lease_lost"
	DiagnosticExplanationCancelled              DiagnosticExplanationCode = "explain_cancelled"
	DiagnosticExplanationDeadlineExceeded       DiagnosticExplanationCode = "explain_deadline_exceeded"
	DiagnosticExplanationFailureUnclassified    DiagnosticExplanationCode = "explain_failure_unclassified"
)

const (
	DiagnosticRemediationReviewAccess     DiagnosticRemediationCode = "review_access"
	DiagnosticRemediationReviewSource     DiagnosticRemediationCode = "review_source"
	DiagnosticRemediationReviewStorage    DiagnosticRemediationCode = "review_storage"
	DiagnosticRemediationRetryInspection  DiagnosticRemediationCode = "retry_inspection"
	DiagnosticRemediationUpgradeCore      DiagnosticRemediationCode = "upgrade_core"
	DiagnosticRemediationReviewBackup     DiagnosticRemediationCode = "review_backup"
	DiagnosticRemediationReviewMigration  DiagnosticRemediationCode = "review_migration"
	DiagnosticRemediationReviewRecord     DiagnosticRemediationCode = "review_record"
	DiagnosticRemediationWaitForCollector DiagnosticRemediationCode = "wait_for_collector"
	DiagnosticRemediationCheckCollector   DiagnosticRemediationCode = "check_collector"
	DiagnosticRemediationRetryOperation   DiagnosticRemediationCode = "retry_operation"
	DiagnosticRemediationReviewOperation  DiagnosticRemediationCode = "review_operation"
)

// DiagnosticClassification is a closed projection. It contains no source,
// error, SQL, path, or free-text explanation fields.
type DiagnosticClassification struct {
	Component       DiagnosticComponent       `json:"component"`
	Operation       DiagnosticOperation       `json:"operation"`
	Reason          DiagnosticReason          `json:"reason"`
	Severity        DiagnosticSeverity        `json:"severity"`
	Category        DiagnosticCategory        `json:"category"`
	Adapter         DiagnosticAdapter         `json:"adapter,omitempty"`
	Retryability    DiagnosticRetryability    `json:"retryability"`
	Impact          DiagnosticImpact          `json:"impact"`
	ExplanationCode DiagnosticExplanationCode `json:"explanation_code"`
	RemediationCode DiagnosticRemediationCode `json:"remediation_code"`
}

// DiagnosticFailure is the safe error projection returned by FailureDetails.
// Error() on the original error remains a local operational surface only.
type DiagnosticFailure struct {
	Classification      DiagnosticClassification      `json:"classification"`
	OperationID         DiagnosticID                  `json:"operation_id,omitempty"`
	EmissionDisposition DiagnosticEmissionDisposition `json:"emission_disposition"`
}

// DiagnosticOptionalUint64 distinguishes an unavailable measurement from a
// measured zero. Saturated and truncated are explicit evidence qualifiers.
type DiagnosticOptionalUint64 struct {
	Value        uint64                 `json:"value,omitempty"`
	Availability DiagnosticAvailability `json:"availability"`
	Saturated    bool                   `json:"saturated,omitempty"`
	Truncated    bool                   `json:"truncated,omitempty"`
}

// DiagnosticPosition contains byte/line positions only when actually known.
type DiagnosticPosition struct {
	ByteOffset DiagnosticOptionalUint64 `json:"byte_offset"`
	LineNumber DiagnosticOptionalUint64 `json:"line_number"`
}

// DiagnosticSubject is an opaque, explicitly scoped identity reference.
type DiagnosticSubject struct {
	Kind          DiagnosticSubjectKind   `json:"kind,omitempty"`
	Reference     string                  `json:"reference,omitempty"`
	IdentityScope DiagnosticIdentityScope `json:"identity_scope,omitempty"`
	Availability  DiagnosticAvailability  `json:"availability"`
}

// DiagnosticOutcomeCounts holds fixed counters for one explicitly labelled
// scope. Saturated means at least one field reached math.MaxUint64.
type DiagnosticOutcomeCounts struct {
	Scope                  DiagnosticCountScope   `json:"scope"`
	ScopeID                DiagnosticID           `json:"scope_id,omitempty"`
	Availability           DiagnosticAvailability `json:"availability"`
	IgnoredNonUsage        uint64                 `json:"ignored_non_usage,omitempty"`
	UnchangedSnapshot      uint64                 `json:"unchanged_snapshot,omitempty"`
	DeferredIncompleteTail uint64                 `json:"deferred_incomplete_tail,omitempty"`
	UnsupportedUsage       uint64                 `json:"unsupported_usage,omitempty"`
	MalformedRecord        uint64                 `json:"malformed_record,omitempty"`
	FailedSource           uint64                 `json:"failed_source,omitempty"`
	Saturated              bool                   `json:"saturated,omitempty"`
	Truncated              bool                   `json:"truncated,omitempty"`
}

// DiagnosticEvidence labels the scope and availability of accompanying
// evidence. UnavailableReason is a closed value, never free text.
type DiagnosticEvidence struct {
	Scope             DiagnosticEvidenceScope     `json:"scope"`
	Availability      DiagnosticAvailability      `json:"availability"`
	UnavailableReason DiagnosticUnavailableReason `json:"unavailable_reason,omitempty"`
	Truncated         bool                        `json:"truncated,omitempty"`
}

// DiagnosticEvent is a bounded, detached value sent through an optional
// observer. It is a lossy troubleshooting signal, not an audit record.
type DiagnosticEvent struct {
	ContractVersion      int                      `json:"contract_version"`
	CoreVersion          string                   `json:"core_version"`
	ObservedAt           time.Time                `json:"observed_at"`
	Kind                 DiagnosticEventKind      `json:"kind"`
	Classification       DiagnosticClassification `json:"classification"`
	HasClassification    bool                     `json:"has_classification,omitempty"`
	OperationID          DiagnosticID             `json:"operation_id"`
	CollectorRunID       DiagnosticID             `json:"collector_run_id,omitempty"`
	PollID               DiagnosticID             `json:"poll_id,omitempty"`
	Subject              DiagnosticSubject        `json:"subject"`
	SourceSchemaVersion  DiagnosticOptionalUint64 `json:"source_schema_version"`
	TargetSchemaVersion  DiagnosticOptionalUint64 `json:"target_schema_version"`
	DurationMilliseconds DiagnosticOptionalUint64 `json:"duration_milliseconds"`
	RetryAttempt         DiagnosticOptionalUint64 `json:"retry_attempt"`
	RetryCount           DiagnosticOptionalUint64 `json:"retry_count"`
	Position             DiagnosticPosition       `json:"position"`
	Evidence             DiagnosticEvidence       `json:"evidence"`
	Counts               DiagnosticOutcomeCounts  `json:"counts"`
}

// Validate checks catalogue compatibility, identity/value bounds, and the
// total serialized v1 item-size budget.
func (event DiagnosticEvent) Validate() error {
	if event.ContractVersion != DiagnosticContractVersion || !validDiagnosticVersion(event.CoreVersion) ||
		event.ObservedAt.IsZero() || event.ObservedAt.Location() != time.UTC || !validEventKind(event.Kind) || !validID(event.OperationID) {
		return ErrInvalidDiagnostic
	}
	if event.CollectorRunID != "" && !validID(event.CollectorRunID) || event.PollID != "" && !validID(event.PollID) {
		return ErrInvalidDiagnostic
	}
	if event.HasClassification {
		if err := event.Classification.Validate(); err != nil {
			return err
		}
	} else if event.Classification != (DiagnosticClassification{}) || event.Kind == DiagnosticEventOperationFailed ||
		event.Kind == DiagnosticEventConditionSummary || event.Kind == DiagnosticEventOperationRecovered {
		return ErrInvalidDiagnostic
	}
	if event.Kind == DiagnosticEventPollSummary && (event.HasClassification || event.Evidence.Scope != DiagnosticEvidencePoll) {
		return ErrInvalidDiagnostic
	}
	if err := event.Subject.Validate(); err != nil {
		return err
	}
	if err := event.Evidence.Validate(); err != nil {
		return err
	}
	for _, measurement := range []DiagnosticOptionalUint64{event.SourceSchemaVersion, event.TargetSchemaVersion,
		event.DurationMilliseconds, event.RetryAttempt, event.RetryCount} {
		if err := measurement.Validate(); err != nil {
			return err
		}
	}
	if err := event.Position.Validate(); err != nil {
		return err
	}
	if event.Kind == DiagnosticEventPollSummary {
		if err := event.Counts.Validate(); err != nil {
			return err
		}
		if event.Counts.Scope != DiagnosticCountPoll || event.Counts.ScopeID != event.PollID {
			return ErrInvalidDiagnostic
		}
	} else if event.Counts.Scope != DiagnosticCountOperation ||
		event.Counts.Availability != DiagnosticAvailabilityNotCollected || event.Counts.Validate() != nil {
		return ErrInvalidDiagnostic
	}
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) > MaxDiagnosticItemBytes {
		return ErrInvalidDiagnostic
	}
	return nil
}

func (classification DiagnosticClassification) Validate() error {
	if len(classification.ExplanationCode) > len("explain_required_context_missing") ||
		len(classification.RemediationCode) > len("wait_for_collector") ||
		len(classification.Category) > len(DiagnosticCategorySecuritySensitive) || len(classification.Severity) > len("warning") ||
		len(classification.Retryability) > len("not_retryable") || len(classification.Impact) > len("ingestion_may_be_incomplete") ||
		len(classification.Adapter) > len("unknown") {
		return ErrInvalidDiagnostic
	}
	spec, ok := diagnosticReasonSpecFor(classification.Reason)
	if !ok || !validComponent(classification.Component) || !validOperation(classification.Operation) ||
		!spec.components[classification.Component] || !spec.operations[classification.Operation] ||
		classification.Severity != spec.severity || classification.Retryability != spec.retryability ||
		classification.Impact != spec.impact || !validDiagnosticCategory(string(classification.Category)) ||
		classification.Category != spec.category && classification.Category != DiagnosticCategory(DiagnosticCategorySecuritySensitive) ||
		!validDiagnosticAdapter(classification.Adapter) ||
		classification.ExplanationCode != explanationCode(classification.Reason) ||
		classification.RemediationCode != spec.remediation {
		return ErrInvalidDiagnostic
	}
	return nil
}

func (subject DiagnosticSubject) Validate() error {
	if !validAvailability(subject.Availability) {
		return ErrInvalidDiagnostic
	}
	if len(subject.IdentityScope) > len("store_handle") {
		return ErrInvalidDiagnostic
	}
	if subject.Availability != DiagnosticAvailabilityAvailable {
		if subject.Reference != "" || subject.IdentityScope != "" {
			return ErrInvalidDiagnostic
		}
		if subject.Kind != "" && !validSubjectKind(subject.Kind) {
			return ErrInvalidDiagnostic
		}
		return nil
	}
	if !validSubjectKind(subject.Kind) || len(subject.Reference) != 64 || !diagnosticHexReferencePattern.MatchString(subject.Reference) ||
		(subject.IdentityScope != DiagnosticIdentityStoreHandle && subject.IdentityScope != DiagnosticIdentityStore) {
		return ErrInvalidDiagnostic
	}
	return nil
}

func (id DiagnosticID) Validate() error {
	if id != "" && !validID(id) {
		return ErrInvalidDiagnostic
	}
	return nil
}

func (position DiagnosticPosition) Validate() error {
	if err := position.ByteOffset.Validate(); err != nil {
		return err
	}
	return position.LineNumber.Validate()
}

func (evidence DiagnosticEvidence) Validate() error {
	if !validEvidenceScope(evidence.Scope) || !validAvailability(evidence.Availability) {
		return ErrInvalidDiagnostic
	}
	if evidence.Availability == DiagnosticAvailabilityAvailable {
		if evidence.UnavailableReason != "" {
			return ErrInvalidDiagnostic
		}
	} else if !validUnavailableReason(evidence.UnavailableReason) {
		return ErrInvalidDiagnostic
	}
	return nil
}

func (count DiagnosticOptionalUint64) Validate() error {
	if count.Availability == DiagnosticAvailabilityAvailable {
		if count.Saturated && count.Value != math.MaxUint64 {
			return ErrInvalidDiagnostic
		}
		return nil
	}
	if !validAvailability(count.Availability) || count.Value != 0 || count.Saturated || count.Truncated {
		return ErrInvalidDiagnostic
	}
	return nil
}

func (counts DiagnosticOutcomeCounts) Validate() error {
	if counts.Availability != DiagnosticAvailabilityAvailable {
		if counts.IgnoredNonUsage != 0 || counts.UnchangedSnapshot != 0 ||
			counts.DeferredIncompleteTail != 0 || counts.UnsupportedUsage != 0 || counts.MalformedRecord != 0 ||
			counts.FailedSource != 0 || counts.Saturated || counts.Truncated {
			return ErrInvalidDiagnostic
		}
	}
	if !validCountScope(counts.Scope) || !validAvailability(counts.Availability) {
		return ErrInvalidDiagnostic
	}
	if counts.Scope == DiagnosticCountPoll || counts.Scope == DiagnosticCountRun {
		if !validID(counts.ScopeID) {
			return ErrInvalidDiagnostic
		}
	} else if counts.ScopeID != "" {
		return ErrInvalidDiagnostic
	}
	if counts.Saturated && counts.IgnoredNonUsage != math.MaxUint64 && counts.UnchangedSnapshot != math.MaxUint64 &&
		counts.DeferredIncompleteTail != math.MaxUint64 && counts.UnsupportedUsage != math.MaxUint64 &&
		counts.MalformedRecord != math.MaxUint64 && counts.FailedSource != math.MaxUint64 {
		return ErrInvalidDiagnostic
	}
	return nil
}

// AddOutcome saturating-adds one observed record outcome to an available,
// explicitly scoped counter value. Unknown outcomes or unavailable counters
// are rejected without mutation.
func (counts *DiagnosticOutcomeCounts) AddOutcome(outcome RecordOutcome) error {
	if counts == nil || counts.Availability != DiagnosticAvailabilityAvailable || !validCountScope(counts.Scope) {
		return ErrInvalidDiagnostic
	}
	var target *uint64
	switch outcome {
	case OutcomeIgnoredNonUsage:
		target = &counts.IgnoredNonUsage
	case OutcomeUnchangedSnapshot:
		target = &counts.UnchangedSnapshot
	case OutcomeDeferredIncompleteTail:
		target = &counts.DeferredIncompleteTail
	case OutcomeUnsupportedUsage:
		target = &counts.UnsupportedUsage
	case OutcomeMalformedRecord:
		target = &counts.MalformedRecord
	case OutcomeFailedSource:
		target = &counts.FailedSource
	default:
		return ErrInvalidDiagnostic
	}
	if *target == math.MaxUint64 {
		counts.Saturated = true
		return nil
	}
	*target++
	if *target == math.MaxUint64 {
		counts.Saturated = true
	}
	return nil
}

func validDiagnosticVersion(value string) bool {
	return value == "unknown" || len(value) <= 64 && diagnosticVersionPattern.MatchString(value)
}

func validID(id DiagnosticID) bool {
	return len(id) == 32 && diagnosticHexIDPattern.MatchString(string(id))
}

func validComponent(value DiagnosticComponent) bool {
	if len(value) > len("diagnostics") {
		return false
	}
	switch value {
	case DiagnosticComponentSource, DiagnosticComponentParser, DiagnosticComponentStorage, DiagnosticComponentSchema,
		DiagnosticComponentMigration, DiagnosticComponentCollector, DiagnosticComponentDiagnostics, DiagnosticComponentUnknown:
		return true
	default:
		return false
	}
}
func validOperation(value DiagnosticOperation) bool {
	if len(value) > len("query_diagnostics") {
		return false
	}
	_, ok := allDiagnosticOperations[value]
	return ok
}
func validEventKind(value DiagnosticEventKind) bool {
	if len(value) > len("operation_recovered") {
		return false
	}
	return value == DiagnosticEventOperationFailed || value == DiagnosticEventConditionSummary ||
		value == DiagnosticEventPollSummary || value == DiagnosticEventOperationRecovered
}
func validDiagnosticAdapter(value DiagnosticAdapter) bool {
	if len(value) > len("unavailable") {
		return false
	}
	return value == "" || value == DiagnosticAdapterClaude || value == DiagnosticAdapterCodex || value == DiagnosticAdapterUnknown
}
func validSubjectKind(value DiagnosticSubjectKind) bool {
	if len(value) > len("discovery_target") {
		return false
	}
	switch value {
	case DiagnosticSubjectSource, DiagnosticSubjectDiscoveryTarget, DiagnosticSubjectRoot, DiagnosticSubjectStore, DiagnosticSubjectUnknown:
		return true
	}
	return false
}
func validAvailability(value DiagnosticAvailability) bool {
	if len(value) > len("not_applicable") {
		return false
	}
	switch value {
	case DiagnosticAvailabilityAvailable, DiagnosticAvailabilityNotCollected, DiagnosticAvailabilityNotApplicable,
		DiagnosticAvailabilityUnsupported, DiagnosticAvailabilityRedacted, DiagnosticAvailabilityLost, DiagnosticAvailabilityUnknown:
		return true
	}
	return false
}

func validUnavailableReason(value DiagnosticUnavailableReason) bool {
	if len(value) > len("not_applicable") {
		return false
	}
	switch value {
	case DiagnosticUnavailableNotCollected, DiagnosticUnavailableNotApplicable, DiagnosticUnavailableUnsupported,
		DiagnosticUnavailableRedacted, DiagnosticUnavailableLost, DiagnosticUnavailableUnknown:
		return true
	}
	return false
}
func validCountScope(value DiagnosticCountScope) bool {
	if len(value) > len("operation") {
		return false
	}
	return value == DiagnosticCountOperation || value == DiagnosticCountPoll || value == DiagnosticCountRun || value == DiagnosticCountLifetime
}

func validEvidenceScope(value DiagnosticEvidenceScope) bool {
	if len(value) > len("source_generation") {
		return false
	}
	return value == DiagnosticEvidenceOperation || value == DiagnosticEvidencePoll ||
		value == DiagnosticEvidenceRun || value == DiagnosticEvidenceSourceGeneration
}

var allDiagnosticOperations = set(DiagnosticOperationOpen, DiagnosticOperationInspectSchema, DiagnosticOperationBackup,
	DiagnosticOperationMigrate, DiagnosticOperationDiscover, DiagnosticOperationRead, DiagnosticOperationClassify,
	DiagnosticOperationParse, DiagnosticOperationPersist, DiagnosticOperationAcquireLease, DiagnosticOperationHeartbeat,
	DiagnosticOperationRequestStop, DiagnosticOperationReleaseLease, DiagnosticOperationQueryDiagnostics, DiagnosticOperationReviewDiagnostic)

func explanationCode(reason DiagnosticReason) DiagnosticExplanationCode {
	switch reason {
	case DiagnosticReasonPermissionDenied:
		return DiagnosticExplanationPermissionDenied
	case DiagnosticReasonSourceUnavailable:
		return DiagnosticExplanationSourceUnavailable
	case DiagnosticReasonIOFailure:
		return DiagnosticExplanationIOFailure
	case DiagnosticReasonSnapshotUnstable:
		return DiagnosticExplanationSnapshotUnstable
	case DiagnosticReasonSchemaUnsupported:
		return DiagnosticExplanationSchemaUnsupported
	case DiagnosticReasonBackupFailed:
		return DiagnosticExplanationBackupFailed
	case DiagnosticReasonMigrationFailed:
		return DiagnosticExplanationMigrationFailed
	case DiagnosticReasonStorageBusy:
		return DiagnosticExplanationStorageBusy
	case DiagnosticReasonStorageLocked:
		return DiagnosticExplanationStorageLocked
	case DiagnosticReasonStorageFull:
		return DiagnosticExplanationStorageFull
	case DiagnosticReasonStorageReadonly:
		return DiagnosticExplanationStorageReadonly
	case DiagnosticReasonStorageCorrupt:
		return DiagnosticExplanationStorageCorrupt
	case DiagnosticReasonStorageFailure:
		return DiagnosticExplanationStorageFailure
	case DiagnosticReasonInvalidJSON:
		return DiagnosticExplanationInvalidJSON
	case DiagnosticReasonInvalidTimestamp:
		return DiagnosticExplanationInvalidTimestamp
	case DiagnosticReasonInvalidTokenCount:
		return DiagnosticExplanationInvalidTokenCount
	case DiagnosticReasonRequiredContextMissing:
		return DiagnosticExplanationRequiredContextMissing
	case DiagnosticReasonUnsupportedUsage:
		return DiagnosticExplanationUnsupportedUsage
	case DiagnosticReasonMalformedRecord:
		return DiagnosticExplanationMalformedRecord
	case DiagnosticReasonLeaseActive:
		return DiagnosticExplanationLeaseActive
	case DiagnosticReasonLeaseLost:
		return DiagnosticExplanationLeaseLost
	case DiagnosticReasonCancelled:
		return DiagnosticExplanationCancelled
	case DiagnosticReasonDeadlineExceeded:
		return DiagnosticExplanationDeadlineExceeded
	case DiagnosticReasonFailureUnclassified:
		return DiagnosticExplanationFailureUnclassified
	default:
		return ""
	}
}

func makeDiagnosticClassification(component DiagnosticComponent, operation DiagnosticOperation, reason DiagnosticReason,
	adapter DiagnosticAdapter) DiagnosticClassification {
	if !validOperation(operation) {
		operation = DiagnosticOperationOpen
	}
	spec, ok := diagnosticReasonSpecFor(reason)
	if !ok {
		reason = DiagnosticReasonFailureUnclassified
		spec, _ = diagnosticReasonSpecFor(reason)
	}
	if !validComponent(component) || !spec.components[component] || !spec.operations[operation] || !validDiagnosticAdapter(adapter) {
		component = DiagnosticComponentUnknown
		reason = DiagnosticReasonFailureUnclassified
		spec, _ = diagnosticReasonSpecFor(reason)
		adapter = DiagnosticAdapterUnavailable
	}
	classification := DiagnosticClassification{Component: component, Operation: operation, Reason: reason,
		Severity: spec.severity, Category: spec.category, Adapter: adapter, Retryability: spec.retryability,
		Impact: spec.impact, ExplanationCode: explanationCode(reason), RemediationCode: spec.remediation}
	return classification
}

func (failure DiagnosticFailure) Validate() error {
	if err := failure.Classification.Validate(); err != nil {
		return err
	}
	if failure.OperationID != "" && !validID(failure.OperationID) {
		return ErrInvalidDiagnostic
	}
	switch failure.EmissionDisposition {
	case DiagnosticEmissionDisabled, DiagnosticEmissionQueued, DiagnosticEmissionDropped, DiagnosticEmissionUnavailable:
		encoded, err := json.Marshal(failure)
		if err != nil || len(encoded) > MaxDiagnosticItemBytes {
			return ErrInvalidDiagnostic
		}
		return nil
	}
	return ErrInvalidDiagnostic
}
