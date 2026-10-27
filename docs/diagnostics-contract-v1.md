# Core diagnostics contract v1 foundation

This document describes the shared, additive API foundation for core issues
[#163](https://github.com/ayeshLK/komposyn/issues/163) and
[#167](https://github.com/ayeshLK/komposyn/issues/167). It does not implement
the later #163 instrumentation sweep or the persisted evidence/query/review
work owned by [#112](https://github.com/ayeshLK/komposyn/issues/112).

`DiagnosticContractVersion` is `1`. It is independent of SQLite schema,
`DiagnosticReport`, project-inspection format, and harness protocol versions.
Existing `Diagnostic`, `DiagnosticReport`, `ImportResult`, `OutcomeCounts`,
`CollectorHealth`, their JSON encodings, and existing Open APIs are unchanged.

## Classification

`DiagnosticClassification` is the closed shared failure value:

| Field | Type and allowed meaning |
| --- | --- |
| `Component` | `DiagnosticComponent`: source, parser, storage, schema, migration, collector, diagnostics, unknown |
| `Operation` | `DiagnosticOperation`: open, inspect_schema, backup, migrate, discover, read, classify, parse, persist, acquire_lease, heartbeat, request_stop, release_lease, query_diagnostics, review_diagnostic |
| `Reason` | `DiagnosticReason`: the catalogue below |
| `Severity` | info, warning, error; independent from collector health |
| `Category` | An existing diagnostic category; the catalogue default or sticky security-sensitive |
| `Adapter` | claude, codex, unknown, or empty when unavailable |
| `Retryability` | retryable, not_retryable, unknown; not an automatic retry instruction |
| `Impact` | none, ingestion_may_be_incomplete, ingestion_unavailable, inspection_unavailable, unknown |
| `ExplanationCode` | Fixed `DiagnosticExplanation...` catalogue token |
| `RemediationCode` | Fixed `DiagnosticRemediation...` catalogue token; not a command or URL |

`DiagnosticClassification.Validate` rejects unsupported component/operation/
reason combinations, wrong defaults, and unknown values. Security-sensitive is
the only category override and cannot be downgraded by a later wrapper.
Explanation tokens are identifiers only; user-facing prose is resolved from a
fixed catalogue by the consumer.

| Reason | Compatible component / operation | Default severity; impact | Remediation token |
| --- | --- | --- | --- |
| permission_denied | source or storage / open, backup, discover, read | error; ingestion_unavailable | review_access |
| source_unavailable | source / discover, read | error; ingestion_unavailable | review_source |
| io_failure | source or storage / source or storage I/O operations | error; ingestion_unavailable | review_storage |
| snapshot_unstable | schema / inspect_schema, open | warning; inspection_unavailable | retry_inspection |
| schema_unsupported | schema / inspect_schema, open | error; inspection_unavailable | upgrade_core |
| backup_failed | migration / backup, migrate | error; ingestion_unavailable | review_backup |
| migration_failed | migration / migrate | error; ingestion_unavailable | review_migration |
| storage_busy, storage_locked | storage / open, backup, migrate, persist | error; ingestion_unavailable; retryable | review_storage |
| storage_busy, storage_locked | storage / query_diagnostics, review_diagnostic | error; inspection_unavailable; retryable | review_storage |
| storage_full, storage_readonly, storage_corrupt, storage_failure | storage / open, backup, migrate, persist | error; ingestion_unavailable | review_storage |
| storage_full, storage_readonly, storage_corrupt, storage_failure | storage / query_diagnostics, review_diagnostic | error; inspection_unavailable | review_storage |
| invalid_json, invalid_timestamp, invalid_token_count, required_context_missing, unsupported_usage, malformed_record | parser / classify, parse | warning; ingestion_may_be_incomplete | review_record |
| lease_active | collector / acquire_lease | info; none | wait_for_collector |
| lease_lost | collector / lease and stop operations | warning; ingestion_may_be_incomplete | check_collector |
| cancelled | any known component/operation with an actual cancellation cause | info; none; retryable | retry_operation |
| deadline_exceeded | any known component/operation with an actual deadline cause | warning; unknown; retryable | retry_operation |
| failure_unclassified | any known component/operation when no narrower typed cause exists | error; unknown | review_operation |

All reasons have a fixed matching `explain_<reason>` identifier. Classifiers
use typed causes and known operation context; arbitrary error-message parsing
is not part of the contract. Expected missing optional roots, healthy lease
ownership, ignored/auxiliary records, supported legacy usage, and incomplete
tails are not failures.

## Safe failures and raw errors

`OperationalError` preserves the original error text and unwrap chain. Its
`Error()` output is local operational text and may include sensitive paths,
driver details, or input-derived text. It is not a serialization, logging, or
sharing format. `errors.Is` and `errors.As` continue through `Unwrap()`.

`FailureDetails(error)` returns `(DiagnosticFailure, true)` only when the chain
contains a core-classified operational error. The value contains only a
validated classification, optional 128-bit lowercase-hex operation ID, and
emission disposition (`disabled`, `queued`, `dropped`, `unavailable`). It never
contains the cause or its message. A nil or unclassified error returns the zero
value and `false`. Wrapping an already-classified cause preserves its original
classification and correlation rather than creating a second classification.

## Event and evidence values

`DiagnosticEvent` has a fixed DTO, no arbitrary attribute map, no error or raw
text field, and validates at most 8 KiB of JSON. It contains contract/core
versions, a UTC observation time, event kind, optional controlled
classification, core-generated 128-bit operation/run/poll IDs, optional opaque
subject, optional numeric schema versions, optional duration/retry/position
measurements, scoped fixed outcome counters, and evidence availability and
truncation state.

The exact v1 fields are:

```text
DiagnosticFailure:
  Classification DiagnosticClassification
  OperationID    DiagnosticID
  EmissionDisposition DiagnosticEmissionDisposition

DiagnosticEvent:
  ContractVersion       int
  CoreVersion           string
  ObservedAt            time.Time
  Kind                  DiagnosticEventKind
  Classification        DiagnosticClassification
  HasClassification     bool
  OperationID           DiagnosticID
  CollectorRunID        DiagnosticID
  PollID                DiagnosticID
  Subject               DiagnosticSubject
  SourceSchemaVersion   DiagnosticOptionalUint64
  TargetSchemaVersion   DiagnosticOptionalUint64
  DurationMilliseconds  DiagnosticOptionalUint64
  RetryAttempt          DiagnosticOptionalUint64
  RetryCount            DiagnosticOptionalUint64
  Position              DiagnosticPosition
  Evidence              DiagnosticEvidence
  Counts                DiagnosticOutcomeCounts

DiagnosticSubject:
  Kind          DiagnosticSubjectKind
  Reference     string
  IdentityScope DiagnosticIdentityScope
  Availability  DiagnosticAvailability

DiagnosticOptionalUint64:
  Value        uint64
  Availability DiagnosticAvailability
  Saturated    bool
  Truncated    bool

DiagnosticPosition:
  ByteOffset DiagnosticOptionalUint64
  LineNumber DiagnosticOptionalUint64

DiagnosticEvidence:
  Scope              DiagnosticEvidenceScope
  Availability       DiagnosticAvailability
  UnavailableReason DiagnosticUnavailableReason
  Truncated          bool

DiagnosticOutcomeCounts:
  Scope                  DiagnosticCountScope
  ScopeID                DiagnosticID
  Availability           DiagnosticAvailability
  IgnoredNonUsage        uint64
  UnchangedSnapshot      uint64
  DeferredIncompleteTail uint64
  UnsupportedUsage       uint64
  MalformedRecord        uint64
  FailedSource           uint64
  Saturated              bool
  Truncated              bool
```

`DiagnosticEvent.Validate`, `DiagnosticClassification.Validate`,
`DiagnosticSubject.Validate`, and the value validators enforce the v1 closed
catalogues and identity/value limits before enqueue. Unknown caller-supplied
values are rejected; they are never copied into an explanation field.

Every optional numeric value uses `DiagnosticOptionalUint64`. Its
`Availability` distinguishes a measured zero (`available`, `Value: 0`) from
not collected, not applicable, unsupported, redacted, lost, or unknown. Its
saturation and truncation flags state whether a bound affected the value.
`DiagnosticPosition` separately represents byte offset and one-based line
number. Missing positions are not reported as zero.

`DiagnosticSubject` only admits fixed subject kinds and either no reference
with an unavailable status or a 64-character lowercase-hex reference plus an
explicit `store_handle` or `store` scope. The first scope is handle-local and
cannot be compared across opens; `store` is reserved for the later #112
persisted namespace. This foundation does not create namespace keys, derive
references, or allocate source identities.

`DiagnosticOutcomeCounts` has fixed counters for the six existing record
outcomes, one explicit operation/poll/run/lifetime scope, optional run/poll ID,
availability, saturation, and truncation. `AddOutcome` saturates at
`math.MaxUint64`; `Saturated` becomes true on reaching that bound. Rejected
outcome names do not mutate the value. These counts do not claim unique bad
records or lost usage. `operation_failed` and `operation_recovered` events carry
operation-scoped counts with `not_collected` availability. `condition_summary`
events require available counts and may scope them to the operation, poll, run,
or lifetime; poll/run IDs and evidence scopes must match. `poll_summary` events
require poll-scoped counts. This makes emitted summaries and deliberately
uncollected counts explicit.

## Observer ownership and loss

`NewDiagnosticObserver(0)` selects capacity 64; values 1 through 256 are
accepted. Other capacities return `ErrInvalidDiagnosticObserverCapacity`.
`Events()` exposes one receive-only mailbox. Multiple producers may publish;
there is one logical consumer, not broadcast. Core creates no worker,
callback, filesystem writer, or goroutine for observation, and never closes
the channel. The caller owns stopping producers, draining/discarding the
bounded remainder, and joining its consumer.

Core enqueue is nonblocking with respect to the consumer. `Stats()` reports
lifetime admitted enqueues and dropped publication attempts, independent of
draining. An admitted event counts even after consumption. A drop counts an
invalid event or an event rejected because the queue was full. Both counters
saturate at `math.MaxUint64`; their flags become true when that limit is
reached. Concurrent stats fields are independent atomic snapshots, not a
transactionally consistent pair. Loss never retries, waits for the consumer,
fails an application operation, or changes SQLite state. The mailbox is
troubleshooting evidence, not an audit log or persistence outbox.

Values are fixed structs containing only scalars and immutable strings; there
are no shared mutable maps, slices, or pointers. Validation rejects unsafe
enum/version/ID/reference values before enqueue and checks serialized size.

## Observed open

`OpenWithObservation(path, options, observer)` uses the same private
initialization path and unchanged `OpenOptions` as `OpenWithOptions`. It makes
the caller-owned observer available before schema preflight and keeps it on a
successful Store for later core instrumentation. A nil observer leaves
initialization behavior unchanged. A preflight error returns no Store; the
safe event remains on the caller's observer. This foundation instruments the
real unsupported-schema preflight failure and uses `failure_unclassified` for
other open failures not yet classified at a typed boundary. It does not
instrument every parser, storage, discovery, or collector failure.

When an observer is full, the open error still returns immediately with its
original error text/chain and safe failure projection marks the event dropped.
Failure to make an operation or reference ID available does not fail a
successful operation. Events are published after initialization returns and
outside SQLite transactions or Store/Collector locks.

## Remaining work

Subsequent #163 work instruments discovery, parser validation, persistence,
collector lease/heartbeat/stop, correlation, scoped references, and
operation/poll summaries. It must aggregate independently of observer loss
where persistence is required. #112 owns persisted diagnostic namespaces,
versioned detail identity, migrations, observation counters, evidence-derived
condition state, bounded query paging, and review APIs. No schema change,
persistence, CLI adapter, logging, export, consent, or repair behavior is
included here.
