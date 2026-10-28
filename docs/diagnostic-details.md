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

The private reducer accepts trusted operation results from core instrumentation;
it does not consume `DiagnosticObserver.Events()`. Claude and Codex parser
findings, Store-backed source failures, collector discovery failures, and
collector lease/runtime failures feed this seam after their owning boundaries.
Parser findings are recorded only after the ingestion/cursor/checkpoint
transaction commits. Diagnostic writes are best effort: they cannot fail an
already committed import or make it retry. A failed evidence write marks the
detail model unavailable in the open Store and, when SQLite still accepts the
small availability update, across reopen. The update is non-recursive; if the
database itself cannot persist that marker, the reopened Store cannot know that
evidence was lost, so availability remains explicitly an in-memory limitation
for that failure case.

Coverage is conservative and scope matched. A clean, complete full snapshot
evaluates only the same source identity, parser component, operation, adapter,
and closed parser-reason catalogue; it can resolve only matching older
conditions. A full snapshot with malformed/unsupported outcomes or a pending
final record records partial coverage and makes matching older conditions
unknown. `OutcomeCounts.Incomplete()` does not include pending tails, so the
pending count is checked separately. If the same operation positively finds a
problem, that finding keeps it active. An applicable checkpoint/incremental
scan records insufficient coverage and cannot establish absence; historical
problems that the scan does not positively reobserve become unknown. A later
applicable cancelled or failed parse becomes `cancelled` or `uninspected` when
core already knows the source identity and evidence order. Failures before
that boundary remain transient and do not invent a source subject.
Because those attempts do not commit their cursor/checkpoint, their attempted
generation and byte length are not used as condition evidence; both are
reported unavailable. The reserved operation order still prevents an older
concurrent result from overwriting the evaluation, while a later committed
positive finding can reactivate the condition.

Unknown describes the current condition evidence, while `last_positive_at`
retains the timestamp of the most recent persisted positive finding. Coverage
evaluations do not increment lifetime, poll, or run observation counters and
do not erase those historical counts. Their material state changes advance the
review revision; count-only recurrence does not. Unchanged snapshots create no
new evaluation and do not carry forward conclusions because this schema does
not persist a separate verified coverage basis. A clean full scan of a newer
source generation can resolve an older absent condition with
`source_replaced` evidence. Removed sources are not inferred healthy. Storage
and collector failures use their own controlled classification and do not
resolve parser conditions.

The replay ledger is a bounded 64 committed finding-ID ring per problem. An
evicted ID may be counted again if it is replayed later; projections mark the
counter `truncated`. This bounded replay policy was approved in the PR #169
review discussion, but is not stated as explicitly in accepted issue #167.
Maintainers should carry that clarification into #167 before treating the
replay behavior as an unqualified contract guarantee.

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
| Same-reason subjects remain distinct; namespace survives reopen/online backup restore; independent stores differ; legacy rows stay unavailable | `TestDiagnosticDetailsNamespacesCountersAndReviewCAS`; `TestPublicImportsPersistDetailedEvidenceWithoutObserver` |
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
| Public Claude/Codex imports produce detailed records with safe identities, counters, generations, and offsets | `TestPublicImportsPersistDetailedEvidenceWithoutObserver` |
| Evidence persists when observer is nil or its mailbox is full and unread; collector poll counters retain scope | `TestPublicImportEvidenceSurvivesSaturatedUnreadObserver` |
| Clean full replacement scan resolves only matching prior parser evidence | `TestPublicImportsPersistDetailedEvidenceWithoutObserver` |
| Public Claude/Codex imports preserve positive evidence, resolve after clean full coverage, then transition resolved to unknown on pending replacement tails; counters and last-positive evidence remain stable | `TestPendingReplacementTailCannotResolvePriorParserEvidence` |
| Public Claude/Codex collector cancellation with a known source boundary changes an active problem to unknown/cancelled | `TestCancelledKnownSourceCoverageMarksActiveProblemUnknown` |
| Failed replacement leaves attempted generation/offset unavailable; restored generation-zero positive evidence reactivates the problem | `TestFailedReplacementCoverageCannotOutrankCommittedPositiveFinding` |
| Failed diagnostic writes preserve committed usage and report evidence loss after reopen | `TestDiagnosticWriteFailureDoesNotFailCommittedImportAndSurvivesReopen` |
| Failed evidence-order reservation marks availability lost across reopen | `TestOperationalFindingOrderReservationFailureMarksEvidenceLost` |
| Collector storage failures persist with observation disabled or saturated/unread | `TestCollectorStorageFailurePersistsWithoutObserverDelivery` |
| Persistence failure returns safe projections and marks the open store's evidence unavailable | `TestDiagnosticPersistenceFailureIsSafeAndVisibleForStoreHandle` |
| Migration failure preserves the source schema and verified backup | `TestMigrationEighteenFailurePreservesSchemaAndVerifiedBackup` |

These tests combine synthetic reducer cases with public provider imports,
collector polling, and public list/inspect calls. Production parser coverage
evaluations are integrated for successful full/incremental scans and known
source cancellation/failure boundaries. Issue #112 remains open for review of
this integration and the maintainer completion audit. Standalone CLI work in
komposyn-lg #58 remains separate.
