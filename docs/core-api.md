# Token Logger core API

The root Go module remains `github.com/ayeshLK/komposyn`; the reusable core is
the `plugins/library/token-logger` package. It has no dependency on the CLI.
This document records the supported adapter surface for the current pre-1.0
module and distinguishes it from command policy.

## Supported core surface

- **Events and identity:** `UsageEvent`, `TokenUsage`, `Cost`, `Client`,
  `Provenance`, `BillingContext`, `Project`, `Session`, `Cursor`, validation,
  and the reserved Unattributed IDs.
- **Storage and reports:** `Open`, `Store.Close`, `Append` variants,
  `EventCount`, `Cursor`, project/session upserts and resource queries,
  `UsageFilter`, `UsageReport`, `Store.Query`, JSON/CSV/table writers, and
  human-safe formatters.
- **Provider adapters and collection:** `ClaudeAdapter`, `CodexAdapter`,
  `DetectAdapter`, import results and typed outcomes, `NewCollector`,
  `NewCollectorWithRoots`, and the collector polling/run/health methods.
- **Persistent collection state:** registered-source types and Store methods,
  collector leases, heartbeat, cooperative stop, run status/history, and
  typed lease/source errors. Process creation, terminal behavior, root defaults,
  settings, and user consent remain CLI policy.
- **Pricing:** `PricingRule`, token category constants, `Store.UpsertPricing`,
  billing-context evidence values, report pricing provenance, and pricing
  coverage rows.
- **Diagnostics:** sanitized local diagnostic values, stable categories,
  controlled version metadata, Store methods for recording, reading, listing,
  reviewing, and generating destination-neutral reports. Tracker selection,
  security-sensitive public-draft blocking, and external draft URLs belong to
  the CLI.
- **Diagnostics contract v1 foundation:** `OperationalError`,
  `DiagnosticClassification`, `DiagnosticFailure`, `DiagnosticEvent`,
  `DiagnosticObserver`, bounded counter/evidence/reference values,
  `NewDiagnosticObserver`, `FailureDetails`, and `OpenWithObservation`.
  These are additive and separate from legacy `Diagnostic` serialization.
- **Persisted diagnostic details:** bounded detail projections, exact
  counters, condition and review evidence, and the `Store.ListDiagnosticDetails`,
  `Store.InspectDiagnosticDetail`, and `Store.ReviewDiagnosticDetail` APIs.
  These APIs inspect stored evidence only. See
  [persisted diagnostic details](diagnostic-details.md) for privacy, bounds,
  migration side effects, and the production instrumentation dependency.

SQLite migration mechanics, raw cursor internals, SQL helpers, provider parser
types, pricing catalog synchronization details, and the mutable `Version`
variable are implementation or release metadata rather than stable adapter
contracts. The exported `Version` variable remains as a deprecated core-version
compatibility surface; `CoreVersion()` names its meaning, and combined release
builds inject the core and CLI versions separately even while their values
match. `Store.Migrate` is no longer part of the supported surface; callers
open stores through `Open`, which owns compatibility checks and migrations.
Provider adapter entry points remain supported for consumers that need to
import local provider logs without using the CLI.

## CLI-owned settings

Settings are local application policy. The `settings.json` format and behavior
remain unchanged, including `onboarding_complete`, `collector_enabled`,
`auto_start`, `poll_interval_seconds`, `poll_interval_millis`, `failure_mode`,
atomic replacement, private file permissions, and consent semantics. The Go
`Settings`, `DefaultSettings`, `LoadSettings`, `SaveSettings`, and
`CollectorPollInterval` APIs were removed from core and are private to the CLI.
This is an intentional pre-1.0 source compatibility change: callers that used
these APIs should own their application settings or invoke CLI behavior. No
database, settings file, or consent value is reset or migrated by this change.

`Store.Migrate` and core `GitHubIssueDraftURL` were also removed from the
supported API. `Open` owns migration and compatibility checks; callers needing
diagnostic routing should use the CLI or their own application policy. These
intentional pre-1.0 changes do not alter the JSONL protocol or local diagnostic
review state.

The module has published pre-1.0 tags, so source compatibility is considered
for existing consumers even though a v1 compatibility promise has not begun.
Changes to supported adapter APIs require an explicit migration note and
prefer a transitional wrapper when it does not retain CLI policy in core.

## Exported declaration inventory

This inventory is generated from `go doc -all` and classifies every exported
core declaration. The CLI currently imports the listed core identifiers below;
the package remains independently usable by other consumers.

**Supported models and errors:** `BillingContext`, `BillingContextValue`,
`Client`, `Cost`, `CostReasonCounts`, `Cursor`, `ImportResult`,
`OutcomeCounts`, `Project`, `Provenance`, `RecordOutcome`, `Receipt`,
`Session`, `TokenUsage`, `UsageEvent`, `UnsupportedSchemaError`,
`ErrUnsupportedSchema`, `ErrInvalidSchema`, `ErrCollectorActive`,
`ErrCollectorLeaseLost`, and `ErrInvalidCollectorSource`.

**Supported persistence, reporting, identity, and resource APIs:** `Store`,
`Open`, `OpenWithOptions`, `OpenOptions`, `MigrationBackupMetadata`,
`CurrentSchemaVersion`, `OldestMigratableSchemaVersion`, `SchemaVersion`,
`SupportedEventSchemaVersions`, `UsageFilter`, `UsageMeasures`, `UsageReport`,
`UsageRow`, `PricingCoverageRow`, `ResourceFilter`, `ProjectResource`,
`ProjectResourceList`, `SessionResource`, `SessionResourceList`,
`UnattributedProjectID`, `UnattributedSessionID`, `Store.Close`,
`Store.MigrationBackupPath`, `Store.Append`, `Store.AppendWithCursor`,
`Store.AppendWithReferences`, `Store.AppendWithReferencesAndCursor`,
`Store.EventCount`, `Store.Cursor`, `Store.UpsertProject`, `Store.UpsertSession`,
`Store.ListProjects`, `Store.ProjectResource`, `Store.ListSessions`,
`Store.SessionResource`, `Store.Query`, `WriteJSON`, `WriteCSV`, `WriteTable`,
`WriteTableWidth`, `WriteTableWidthWithoutPricingCoverage`,
`FormatHumanCost`, `FormatHumanLabel`, `FormatHumanText`,
`FormatHumanTokens`, `ProjectInspectionRequest`, `ProjectInspection`,
`ProjectInspectionActivity`, `ProjectInspectionAlias`,
`ProjectInspectionLocation`, `ProjectInspectionWarning`,
`ProjectInspectionSource`, `UsageInspectionRange`,
`InspectionEvidenceStatus`, `InspectionUsage`, `ProjectInspectionFormatVersion`,
`DefaultInspectionPageSize`, `MaxInspectionPageSize`,
`MaxInspectionOffset`, `InspectionPage`, `InspectionEvidenceAvailable`,
`InspectionEvidenceNotRecorded`, `InspectionEvidenceUnavailable`,
`InspectionRecoveryNotFound`, `InspectionRecoveryRetainMetadataOnly`,
`InspectionRecoveryInactiveInSelectedRange`,
`InspectionRecoveryReviewReconciliation`,
`InspectionRecoveryCandidateNameReview`,
`InspectionRecoveryInsufficientEvidence`,
`InspectionRecoveryNoRepairIndicated`, and `Store.InspectProject`.

The separate `ProjectInspection` response is a versioned, bounded evidence
contract. It does not alter the established JSON/CSV report or protocol
contracts. See [project inspection evidence](project-inspection.md) for
range/history semantics, privacy rules, schema limitations, and recovery
classification.

The remaining exported constants are the report groups `GroupHour`, `GroupDay`,
`GroupWeek`, `GroupMonth`, `GroupProject`, `GroupSession`, `GroupClient`,
`GroupProvider`, and `GroupModel`; typed outcomes `OutcomeIgnoredNonUsage`,
`OutcomeUnchangedSnapshot`, `OutcomeDeferredIncompleteTail`,
`OutcomeUnsupportedUsage`, `OutcomeMalformedRecord`, and `OutcomeFailedSource`;
and the diagnostic categories and pricing/billing constants listed below.
`CostReasonCounts.MarshalJSON`, `CostReasonCounts.UnmarshalJSON`,
`OutcomeCounts.Add`, `OutcomeCounts.Merge`, `OutcomeCounts.Pending`,
`OutcomeCounts.Incomplete`, `ImportResult.Merge`,
`UnsupportedSchemaError.Error`, and `UnsupportedSchemaError.Is` are also
exported methods.

**Supported adapter, collector, and persistent-runtime APIs:** `ClaudeAdapter`,
`CodexAdapter`, `DetectAdapter`, `Collector`, `CollectorHealth`,
`CollectorRoot`, `CollectorRootHealth`, `CollectorRuntimeStatus`,
`RegisteredSource`, `SourceRegistrationResult`, `NewCollector`,
`NewCollectorWithRoots`, `NewCollectorInstanceID`, `Collector.Health`,
`Collector.PollOnce`, `Collector.PollOnceContext`, `Collector.Run`,
`Collector.RunImmediate`, `Collector.RunWithResults`, `ClaudeAdapter.Discover`,
`ClaudeAdapter.Import`, `CodexAdapter.Import`, `Store.RegisterCollectorSource`,
`Store.RemoveCollectorSource`, `Store.ListCollectorSources`,
`Store.UpdateCollectorSourceHealth`, `Store.AcquireCollectorLease`,
`Store.MarkCollectorReady`, `Store.HeartbeatCollector`,
`Store.BeginCollectorPoll`, `Store.CompleteCollectorPoll`,
`Store.RequestCollectorStop`, `Store.ReleaseCollectorLease`, and
`Store.CollectorRuntimeStatus`.

**Supported pricing and diagnostic APIs:** `PricingRule`,
`TokenCategoryInput`, `TokenCategoryOutput`, `TokenCategoryCacheRead`,
`TokenCategoryCacheWrite`, `TokenCategoryCacheWrite5m`,
`TokenCategoryCacheWrite1h`, `TokenCategoryReasoning`,
`BillingEvidenceProviderResponse`, `BillingEvidenceProviderRecord`,
`BillingEvidenceHarness`, `Store.UpsertPricing`, `WriteHumanPricingCoverage`,
`Diagnostic`, `DiagnosticMetadata`, `DiagnosticReport`,
`DiagnosticCategoryCLI`, `DiagnosticCategoryProvider`,
`DiagnosticCategoryIngestion`, `DiagnosticCategoryStorage`,
`DiagnosticCategoryMigration`, `DiagnosticCategoryPricing`,
`DiagnosticCategoryReporting`, `DiagnosticCategoryValidation`,
`DiagnosticCategorySecuritySensitive`, `DiagnosticCategoryUnknown`,
`SanitizeDiagnostic`, `Store.RecordDiagnostic`,
`Store.RecordDiagnosticWithMetadata`, `Store.Diagnostic`,
`Store.PendingDiagnostics`, `Store.DiagnosticReport`, and
`Store.MarkDiagnosticReviewed`.

**Persisted diagnostic detail APIs:** `DiagnosticCondition`,
`DiagnosticReviewState`, `DiagnosticConditionReason`,
`DiagnosticCoverageKind`, `DiagnosticExactCount`,
`DiagnosticObservationCount`, `DiagnosticConditionEvidence`,
`DiagnosticReview`, `DiagnosticLocationSample`, `DiagnosticDetail`,
`DiagnosticQuery`, `DiagnosticPage`, `DiagnosticReviewRequest`,
`Store.ListDiagnosticDetails`, `Store.InspectDiagnosticDetail`,
`Store.ReviewDiagnosticDetail`, `ErrInvalidDiagnosticQuery`,
`ErrDiagnosticDetailNotFound`, `ErrDiagnosticReviewConflict`, and
`ErrDiagnosticInspectionUnavailable`. Limits are
`DefaultDiagnosticPageSize`, `MaxDiagnosticPageSize`,
`MaxDiagnosticContinuationBytes`, `MaxDiagnosticListResponseBytes`,
`MaxDetailedDiagnosticProblems`, `MaxDiagnosticLocationSamples`, and
`MaxDiagnosticFindingReplayKeysPerProblem`.

**Structured diagnostics v1 APIs:** `DiagnosticContractVersion`,
`DiagnosticComponent`, `DiagnosticOperation`, `DiagnosticReason`,
`DiagnosticSeverity`, `DiagnosticCategory`, `DiagnosticAdapter`,
`DiagnosticRetryability`, `DiagnosticImpact`, `DiagnosticExplanationCode`,
`DiagnosticRemediationCode`, `DiagnosticClassification`, `DiagnosticFailure`,
`OperationalError`, `DiagnosticEvent`, `DiagnosticEventKind`,
`DiagnosticID`, `DiagnosticSubject`, `DiagnosticSubjectKind`,
`DiagnosticIdentityScope`, `DiagnosticAvailability`,
`DiagnosticOptionalUint64`, `DiagnosticPosition`, `DiagnosticEvidence`,
`DiagnosticEvidenceScope`, `DiagnosticUnavailableReason`, `DiagnosticCountScope`,
`DiagnosticOutcomeCounts`, `DiagnosticEmissionDisposition`,
`DiagnosticObserver`, `DiagnosticObserverStats`,
`NewDiagnosticObserver`, `OpenWithObservation`, `FailureDetails`,
`ErrInvalidDiagnostic`, and `ErrInvalidDiagnosticObserverCapacity`.
The corresponding closed catalogue constants are exported under these
families: `DiagnosticComponent*`, `DiagnosticOperation*`,
`DiagnosticReason*`, `DiagnosticSeverity*`, `DiagnosticAdapter*`,
`DiagnosticRetry*`, `DiagnosticImpact*`, `DiagnosticExplanation*`,
`DiagnosticRemediation*`, `DiagnosticEvent*`, `DiagnosticSubject*`,
`DiagnosticIdentity*`, `DiagnosticAvailability*`,
`DiagnosticUnavailable*`, `DiagnosticCount*`,
`DiagnosticEvidence*`, and `DiagnosticEmission*`. The capacity/item-size
limits are `DefaultDiagnosticObserverCapacity`,
`MaxDiagnosticObserverCapacity`, and `MaxDiagnosticItemBytes`.
Validation methods are `DiagnosticClassification.Validate`,
`DiagnosticFailure.Validate`, `DiagnosticEvent.Validate`,
`DiagnosticSubject.Validate`, `DiagnosticID.Validate`,
`DiagnosticOptionalUint64.Validate`, `DiagnosticPosition.Validate`,
`DiagnosticEvidence.Validate`, `DiagnosticOutcomeCounts.Validate`, and
`DiagnosticOutcomeCounts.AddOutcome`.
See [diagnostics contract v1 foundation](diagnostics-contract-v1.md) for
catalogue values, validation, mailbox loss semantics, and current limits.

**Version compatibility:** `CoreVersion` is the named core-version accessor.
The mutable exported `Version` variable remains deprecated for consumers that
already use it and for existing linker injection. It continues to mean the core
version. The CLI owns its separate build version and reports it independently.

**Current CLI imports from core:** `ClaudeAdapter`, `CodexAdapter`, `Collector`,
`CollectorHealth`, `CollectorRoot`, `CoreVersion`, `DetectAdapter`, `Diagnostic`,
the diagnostic categories/metadata/report types, `ErrCollectorActive`,
`ErrInvalidCollectorSource`, human formatters, report group constants,
`ImportResult`, `NewCollector`, `NewCollectorInstanceID`,
`NewCollectorWithRoots`, `Open`, `OutcomeCounts`, `Project`, project resource
types, `RegisteredSource`, `ResourceFilter`, `SanitizeDiagnostic`, `SchemaVersion`,
`Session`, session resource types, `Store`, `SupportedEventSchemaVersions`,
Unattributed IDs, `UsageEvent`, `UsageFilter`, `UsageMeasures`, `UsageReport`,
and the JSON/CSV/table writers. No core API reads CLI settings, resolves data
directories, prompts for consent, opens terminals, or starts an executable.
