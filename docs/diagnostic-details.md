# Persisted diagnostic details

The core exposes a bounded, local inspection model alongside the legacy
diagnostic API. Use `Store.ListDiagnosticDetails`,
`Store.InspectDiagnosticDetail`, and `Store.ReviewDiagnosticDetail` after
opening a `Store`. These calls inspect stored evidence; they do not inspect
provider files, create identities, repair data, or acquire a collector lease.

## Identity and privacy

Migration 18 creates one random 256-bit diagnostic namespace key per database.
Core derives domain-separated HMAC references for subjects and detailed
problems. References remain stable across reopen and database backup/restore;
independently created databases have independent namespaces. The key is never
returned. Diagnostic identity does not allocate provider aliases, cursors, or
checkpoints. Subject identity is accepted only from trusted core findings.

Inspection projections contain controlled catalogue values, opaque references,
timestamps, offsets, generations, and exact counters. Those local values may
still be identifying. They omit legacy message text, arbitrary metadata,
paths, provider IDs, filenames, and source content. `legacy_detail_availability`
reports unavailable legacy text; it does not imply that the old row was
rewritten or linked. Inspection and review do not grant consent to export or
disclose evidence.

## Evidence and counters

Detailed observations are aggregates, not an event journal. Lifetime counts
count repeated observations since recording began, including a historical
record encountered again while parsing. They are neither unique bad-record
counts nor lost-usage estimates. Decimal strings preserve the full `uint64`
range; availability, saturation, and truncation are explicit.

The latest applicable poll and latest observed run have separate scopes and
identities. The latter is historical and is not necessarily the current run.
Legacy occurrence counts remain separate; migrated legacy rows do not gain
inferred detailed provenance or counts.

Each source operation receives a monotonically reserved evidence order, but
reservation order does not imply commit order. Each problem therefore retains
a bounded 64-key ring of committed finding IDs for replay detection; distinct
findings count even when their operations commit out of order. When the ring
evicts an ID, counter projections set `truncated` so that the bounded history
is not represented as fully exact. A later parse uses a new finding ID and
counts as a new observation even when it sees the same location. Producers
must aggregate multiple causes for one problem within an operation before
persistence.

Each location sample includes its generation and coverage availability so
positions from replaced sources remain distinguishable. The public page and
detail projections include `diagnostic_contract_version` (currently 1).

Operational condition is derived from ordered internal findings and
source/generation coverage. A positive finding marks a problem active. Absence
can resolve it only after sufficient subsequent evaluation of that same
subject and condition. Incomplete, cancelled, uninspected, incremental, or
generation-unknown coverage cannot establish absence. A successful or
unchanged poll alone is not resolution evidence. Evidence order is reserved
before an operation and prevents delayed older work from replacing newer
applicable evidence; wall-clock or commit order alone is not used.

The private reducer accepts trusted findings/evaluations from core
instrumentation; it does not consume `DiagnosticObserver.Events()`. Synthetic
tests exercise the persistence and reducer seam. Production population and
coverage evaluation require the instrumentation work tracked by issue
[#163](https://github.com/ayeshLK/komposyn/issues/163); until that integration
lands, this change does not claim end-to-end production collection of detailed
evidence.

## Review and limits

Review state (`pending`, `reviewed`, `skipped`) is independent from active,
resolved, or unknown condition. Review uses compare-and-set against the
material evidence revision returned by inspection. A stale revision returns
`ErrDiagnosticReviewConflict`. Count/time-only recurrence does not change that
revision; new material classification, condition, or location evidence does.
Review has no repair, export, or transmission effect and does not change legacy
review state.

Each database stores at most 4,096 new detailed problems and four bounded
location samples per problem. Capacity overflow, sample truncation, and
diagnostic persistence availability are explicit. These logical-model bounds
do not bound total SQLite file size or legacy diagnostics. Pages default to 20
and cap at 100; continuation tokens are at most 1 KiB; individual detail items
are at most 8 KiB and list responses at most 1 MiB. Pages use bounded keyset
ordering, but separate requests do not share a frozen snapshot.

`Store.Open` may create or migrate the database and synchronize the pricing
catalogue. The inspection and review methods themselves operate on an
already-open store; do not treat opening a store as a strictly read-only CLI
invocation. Migration 18 is transactional and uses the normal verified
pre-migration backup flow. There are no automatic down-migrations or data
directory moves.

The CLI owns rendering, export selection/preview/consent, destination files,
and disclosure routing. It should consume these public APIs rather than query
diagnostic tables directly.

## Requirement-to-test coverage

| Requirement area | Focused test |
| --- | --- |
| Same-reason subjects remain distinct; namespace survives reopen; independent stores differ; legacy rows stay unavailable | `TestDiagnosticDetailsNamespacesCountersAndReviewCAS` |
| Partial coverage cannot resolve; sufficient later coverage can; older findings cannot overwrite newer evidence | `TestDiagnosticConditionRequiresSufficientLaterCoverage` |
| Exact `uint64` counters and saturation | `TestDiagnosticCountersAreExactAndSaturating` |
| Bounded keyset paging and filter-bound continuation validation | `TestDiagnosticPagingBindsFiltersAndRejectsMalformedTokens` |
| Safe projection and response-size bound | `TestDiagnosticDetailOutputOmitsRawIdentityAndLegacyText` |
| Four-sample cap and 4,096-problem capacity overflow | `TestDiagnosticEvidenceCapacityOverflowAndSamplesAreBounded` |
| Replayed operation idempotence vs a new parse at the same location | `TestDiagnosticFindingReplayIsIdempotentAcrossCalls` |
| Reverse completion order, replay after reverse completion, old-generation ordering | `TestDiagnosticOutOfOrderFindingsCountOnceAndKeepConditionOrdering` |
| Replay-window eviction and explicit counter truncation | `TestDiagnosticReplayWindowOverflowMarksCounterTruncation` |
| Unscoped/same-scope evaluations preserve historical poll counts and saturation | `TestDiagnosticEvaluationPreservesPollCounterWithoutNewPoll` |
| Samples retain source generation and coverage across replacement/late evidence | `TestDiagnosticSamplesRetainGenerationAndCoverage` |
| Public version in nonempty/empty list and detail JSON | `TestDiagnosticDetailsNamespacesCountersAndReviewCAS` |
| Persistence failure returns safe projections and marks the open store's evidence unavailable | `TestDiagnosticPersistenceFailureIsSafeAndVisibleForStoreHandle` |
| Migration failure preserves the source schema and verified backup | `TestMigrationEighteenFailurePreservesSchemaAndVerifiedBackup` |

These tests cover the reducer/storage seam with synthetic trusted findings.
They do not substitute for the pending production integration and source
coverage tests tracked by #163.
