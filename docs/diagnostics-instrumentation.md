# Structured diagnostic instrumentation

This document records the core instrumentation delivered after the shared
diagnostics foundation in #168. It implements the remaining in-memory
observation portion of [#163](https://github.com/ayeshLK/komposyn/issues/163)
against the accepted [contract v1](https://github.com/ayeshLK/komposyn/issues/167).
It does not implement persisted diagnostics in [#112](https://github.com/ayeshLK/komposyn/issues/112).

The observer is optional, bounded, lossy troubleshooting evidence. It does not
count unique bad records, audit ingestion, change import outcomes, or authorize
recovery. A queued event means only that the caller-owned mailbox admitted it.
Disabled and full observers do not change operation results; `FailureDetails`
still reports the controlled classification and whether evidence was disabled,
queued, dropped, or unavailable. Raw `Error()` text remains a local operational
surface and must not be serialized or shared.

## Coverage matrix

| Operation / failure boundary | Existing behavior retained | Evidence and classification | Publication / correlation | Synthetic regression |
| --- | --- | --- | --- | --- |
| `OpenWithObservation`: schema snapshot preflight | No Store on failure; schema error and unwrap chain retained | `schema_unsupported`; unstable copy is `snapshot_unstable`; other unknown failures remain `failure_unclassified` | Observer is supplied before preflight; event is queued after the boundary returns, with operation scope and unavailable subject | `TestOpenWithObservationPreflightFailureIsSafeAndPreservesDatabase`; schema snapshot retry tests |
| SQLite configure / open | Existing local text and driver causes retained | Permission and recognized SQLite busy/locked/full/read-only/corrupt/failure codes; otherwise unclassified | After configuration failure and connection close; no path, SQL, parameters, or error text | `TestOpenWithObservationDisabledProjectionAndExistingOpenCompatibility`; observed open regressions |
| Verified backup creation / verification | Backup failure prevents migration; existing destination and original remain unchanged | Permission, recognized SQLite failure, or `backup_failed` | After migration transaction unwinds; operation-scoped, no destination reference | `TestMigrationBackupFailureLeavesSchemaUnmigratedAndPreservesDestination` |
| Transactional migration | Rollback and pre-migration backup behavior unchanged; unsupported newer schema remains `ErrUnsupportedSchema` | `migration_failed`, `schema_unsupported`, or recognized SQLite operational reason | After rollback; operation-scoped, no database or backup path | `TestMigrationFailureLeavesOriginalAndVerifiedBackupUsable`; newer-schema regressions |
| Required-root discovery | Required failure remains a failed source and updates existing collector health; optional missing root remains expected | Permission, source unavailable, typed path I/O, deadline, or controlled unclassified fallback | A failed-source count is recorded only where the poll outcome is incremented; deadlines remain operation failures without fabricated source counts. Registered roots use their existing identity; unregistered explicit roots use an HMAC of the normalized root identity; subtree failures use an HMAC discovery-target reference | `TestRequiredRootFailureIsScopedButOptionalMissingRootIsExpected`; `TestExpiredPollDeadlineDoesNotFabricateFailedSourceOutcome`; `TestUnregisteredRequiredRootsHaveDistinctHandleScopedReferences` |
| Source acquisition/read and classification | Read-only snapshot, retry, replacement, cursor, and checkpoint rules unchanged; unrecognized/non-usage data remains an outcome, not an invented error | Typed permission/not-found/path/deadline/SQLite cause, otherwise unclassified; no event for a normal unsupported/unknown source absent an actual error | After source close and persistence boundary; one operation failure, with source reference only after a source identity exists | `TestImportReadAndPersistenceErrorsAreTypedAndDoNotLeak`; existing stream/replacement/checkpoint tests |
| Claude/Codex parser validation | Existing receipts/outcomes and valid metadata, auxiliary records, legacy Codex, and incomplete-tail behavior retained | Closed parser reasons from actual validation branches; aggregate malformed/unsupported counts remain replay-equivalent; byte offset samples are bounded to the first matching failure per reason | Import operation directly, or grouped by poll/source; repeated failures mark the representative offset truncated. Poll aggregation retains a position only while evidence stays within one source generation; cross-generation positions become unavailable. Run summaries do not claim parser positions. No provider IDs, aliases, names, records, or content fingerprints | `TestClaudeAndCodexImportsPublishSafeClassifiedFindings`; `TestParserPositionsAreBoundedRepresentativeSamples`; `TestParserPositionFromDiscardedSnapshotRetryIsNotPublished`; existing provider parser regressions |
| Parsed-result persistence | Event/reference/cursor transaction remains all-or-nothing | Recognized SQLite cause or controlled `storage_failure` at `persist` | After transaction rollback; operation failure plus bounded poll summary when polled | `TestImportReadAndPersistenceErrorsAreTypedAndDoNotLeak`; existing rollback/idempotency tests |
| Lease, heartbeat, stop, release | Existing ownership, expected active lease, cancellation, and return values retained | `lease_lost`, recognized SQLite reason, or controlled unclassified failure; `lease_active` remains expected | After operation transaction/statement; core operation ID, never provider or CLI process ID | `TestExpectedCollectorOwnershipAndLeaseLossClassification`; `TestCollectorRuntimeFailuresAreClassifiedAtTheirPublicBoundaries`; existing lease lifecycle tests |
| Poll and run completion | Collector health and legacy diagnostic behavior unchanged | Bounded `condition_summary` and `poll_summary`; cancellation itself is not failure, actual deadline is visible | Poll counts identify the poll; run condition counts identify the collector run; at most 256 keys at each scope; truncation is explicit | `TestCollectorPublishesPollAndConditionSummariesWithMatchingCorrelation`; `TestCollectorRunSummariesUseRunScopeAndMatchingRunID`; `TestRunImmediateCooperativeStopDoesNotEmitFailure`; `TestCollectorCancellationIsExpectedAndDeadlineIsNotHidden`; `TestDiagnosticPollAggregationIsBoundedAndReportsTruncation` |

The #168 foundation already supplies the shared catalogue and value types,
`OperationalError`/`FailureDetails`, mailbox validation/loss accounting, and
pre-open observer installation. This change adds the real failure-boundary
classifications and the in-memory scope/correlation described above. Direct
adapter discovery helpers that have no Store or observer remain unobserved;
collector-owned discovery and Store-backed imports are the instrumented public
operation boundaries.

## Privacy and identity

Each successfully opened Store handle receives an independent random key held
only in memory. Source references are domain-separated HMACs over the existing
internal source identity; registered root references use the existing root ID.
They are labelled `store_handle` and are not stable across Store handles or
process restarts. An explicit required root that is not registered uses its
normalized canonical root identity as the HMAC input; this does not register a
root or allocate ingestion state. Discovery-target references use the
canonical target path observed by the existing walk. If that identity cannot
be resolved or the key cannot be
created, evidence is `not_collected`. Instrumentation does not allocate source
identities, provider aliases, references, cursors, checkpoints, or persistent
diagnostic namespaces.

Events contain controlled enums, opaque core-generated correlation IDs,
bounded numeric counters, UTC observation time, and optional explicitly
unavailable measurements. They never contain a raw path, provider ID, alias,
record, SQL, parameter, custom pricing value, or error message. A stable
store-handle HMAC can still correlate activity within that handle and should be
treated as potentially identifying. Exact aggregate activity counts can also
be identifying. This is privacy-safe projection, not anonymization.

Core creates no observer worker or callback and never waits for a consumer.
Poll and active-run aggregation are each bounded to 256 condition keys; run
aggregation exists only for an observed collector run and is flushed when that
run exits. Mailbox statistics count admitted events and dropped publication attempts
(invalid or full), saturating at `math.MaxUint64`; draining does not change
them. Per-poll aggregation retains no more than 256 condition keys. Further
distinct keys are omitted and the poll/condition truncation flags are set;
numeric totals continue to saturating-count observed outcomes. Repeated scans
are repeated observations, not a deduplicated count of unique records.

## Validation, limits, and ownership

Parser condition counts are derived from outcomes and controlled reasons in
the same parse pass. If a parser branch has an outcome but no narrower proven
reason, it uses `malformed_record` or `unsupported_usage`; no arbitrary error
string is parsed. Pending tails and valid non-usage records do not become
failures. Parser findings retain at most the first known byte offset per reason
and mark it truncated when additional matching records exist. A poll condition
summary may retain that representative offset only while all its position
samples refer to the same source generation; otherwise position is unavailable.
Position does not identify a generation in the public v1 event. Run summaries
aggregate across polls and therefore do not retain parser positions. A
speculative parse pass discarded after source mutation does not publish parser
findings; a continuation excludes outcomes already represented by its
validated checkpoint prefix.

Observation is emitted after source closure, transaction rollback/commit, and
collector lock release. A `FailureDetails` classification is still returned
when observation is disabled or dropped. Correlation IDs are generated per
operation, run, and poll; they are not provider IDs, lease IDs, or CLI process
run IDs. A poll summary describes only outcomes observed in that poll. It does
not prove that uninspected sources recovered, that skipped history is healthy,
or that replacement content was repaired.

This implementation emits no `operation_recovered` event. Current successful
poll/health signals cannot prove that a prior source condition disappeared:
unchanged snapshots and append continuation may skip historical failures, and
parsing replacement content is not evidence that the old generation recovered.
Recovery remains unavailable until the core has trustworthy same-subject,
same-generation coverage evidence; #112's persisted condition model must not
infer it from a healthy global poll.

No schema or persisted diagnostic behavior is added. #112 still owns
database-scoped namespace identity, persisted findings/summaries, observation
counters, active/resolved/review state, bounded query APIs, and migrations.
The standalone CLI still needs to adopt a published core release before it can
consume these additions; this PR does not add CLI commands, logging, export, or
consent behavior.

## Overhead evidence

`BenchmarkDiagnosticObservationOverhead` compares observer-disabled and
observer-enabled synthetic ordinary polling and repeated missing-source errors
on the same host. Run with:

```sh
go test ./plugins/library/token-logger -run '^$' \
  -bench '^BenchmarkDiagnosticObservationOverhead$' -benchmem -count=3
```

The benchmark drains the mailbox after each operation so the enabled case
measures publication rather than queue saturation. On Linux amd64 with Go
1.26.10 and an Intel Core i7-10510U, the three samples were:

| Workload | Observer | ns/op range | B/op range | allocs/op | events/op | drops/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| ordinary collection | disabled | 474,906–704,450 | 100,452–100,636 | 518 | 0 | 0 |
| ordinary collection | enabled | 488,346–505,261 | 103,985–104,125 | 533 | 1 | 0 |
| repeated missing-source errors | disabled | 291,361–300,440 | 25,648–25,679 | 348 | 0 | 0 |
| repeated missing-source errors | enabled | 316,694–345,207 | 31,416–31,452 | 373 | 1 | 0 |

Timing variation is too wide to support a general CPU-overhead conclusion.
The observed enabled-case allocation deltas were 15 allocations and about
3.5 KiB per ordinary poll, and 25 allocations and about 5.7 KiB per repeated
error. The mailbox admitted one event per enabled operation and dropped none.
These small synthetic workloads do not represent large production datasets.
