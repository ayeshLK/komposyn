# Codex historical cumulative recovery

This document records the accepted core design for explicit repair of eligible
historical Codex cumulative-token histories. Recovery is a separate, reviewed
workflow. Normal ingestion remains fail-closed for histories it cannot
reconcile safely. `Store.Open`, reports, project inspection, and diagnostic
inspection never perform recovery.

## Compatibility model

`usage_events` is immutable recovery evidence: existing event IDs, ingestion
keys, provenance, timestamps, attribution, billing context, and original token
vectors remain as imported. A missing valid provider record is added as an
ordinary usage event using the same identity and ingestion-key rules as normal
Codex import. Correcting an already stored allocation appends a journal row and
advances a current-allocation pointer; it does not update the original event.
The journal is not a request stream and never creates synthetic requests.

The effective-events projection contains each ordinary event exactly once. It
retains the original metadata and selects the latest pointed-to allocation for
the seven token categories: input, output, cache read, cache write,
cache-write-5m, cache-write-1h, and reasoning. All current accounting readers
must use this projection. Raw `usage_events` reads are reserved for immutable
evidence, migration, and writes. With no allocation pointers, the projection
has the same values and report formats as the base table.

For the accepted reset example, old stored deltas A=100/10 and C=50/5 stay
unchanged. Recovery inserts missing B=50/5 as a normal event and points C to
an effective 100/10 allocation. Accounting then reports 250/25 over three
requests. A repeated apply, import, or restart cannot count a recovery journal
entry as a request or duplicate B.

## Storage and audit

Schema migration 19 adds a private per-database recovery namespace, versioned
run receipts, bounded audit payloads, an append-only allocation journal,
current allocation heads, and source allocation revisions. The namespace is
random, transactionally created, and retained by the verified online backup
mechanism. Source IDs and generations remain private database evidence; safe
previews use opaque local references. Migration 19 is transactional and
immutable, uses the established verified pre-migration backup, and has no
down-migration.

An override journal row carries the prior and replacement seven-category
vectors. A newly inserted ordinary event is separately recorded as an
`inserted` recovery allocation entry; its base event is already the effective
allocation, so it does not need an override head. Run receipts bind a unique
plan digest and permit an idempotent response after a lost apply response.
Audit data is bounded and must contain controlled fields only: it does not
store source bytes, prompts, responses, paths, aliases, raw errors, or
credentials.

## Staged workflow

The core workflow is intentionally split into three reviewable changes:

1. **Storage and effective accounting.** Add the immutable migration and
   effective projection; move all accounting readers to it. No allocation
   writer or recovery API is exposed in this stage.
2. **Bounded preparation and preview.** Explicitly select one existing Codex
   source generation, read and verify its complete snapshot, and reuse the
   existing parser normalization. Prepare is read-only after `Store.Open` and
   returns a bounded, versioned preview with exact changes, safe references,
   and an evidence-bound plan token. `Store.Open` can still apply migrations
   and synchronize the existing catalog, as documented by its API.
3. **Transactional apply.** Require the expected preview token. Under the
   SQLite writer transaction, revalidate the collector, source generation,
   cursor/checkpoint, allocation revision, existing event vectors/identity,
   candidate keys, and exact plan. Commit the receipt, missing ordinary
   events, override journal/head rows, source revision, cursor changes, and
   checkpoint invalidation together. A committed plan returns its original
   receipt on retry. Diagnostics and observer delivery remain best effort and
   cannot undo or retry committed accounting.

Stage 2 exposes `(*Store).PrepareCodexRecovery(ctx, sourcePath)` and a typed
`CodexRecoveryError` with controlled refusal reasons. It uses the existing
Codex parser in a capture-only mode, resolves project/session aliases through
already stored identity mappings, and performs no Store writes. It requires an
existing cursor and source identity. It does not allocate missing identities.
The exact bounded preview currently includes the seven-category before/after
totals, ordinary request counts, changed event references, affected model and
session counts, and the UTC extent of changes. The evidence-bound plan token is
domain separated and keyed by the persistent recovery namespace. The canonical
binding includes every parsed event, its stable identity and provenance, base
and effective vectors, the complete snapshot digest, source identity,
generation, cursor/checkpoint expectations, parser/algorithm revisions, and
allocation revision.

Stage 3 adds `(*Store).ApplyCodexRecovery(ctx, sourcePath, expectedPlanToken)`.
It first returns an existing receipt for an already committed plan, allowing
retries after a lost response without rereading the source. For a new plan it
repeats preparation, verifies the caller's exact token, rereads and verifies
the selected file, then revalidates the evidence inside the SQLite write
transaction. A mismatch is a controlled stale-preview refusal; the operation
does not silently prepare a replacement token.

The commit contains the run receipt and bounded audit, inserted ordinary
events, append-only allocation rows and override heads, the incremented
source allocation revision, cursor update, and parser-checkpoint deletion.
Any failure before commit rolls all of these back. The updated cursor records
the verified snapshot but marks it non-reusable, so a normal full import must
verify the post-recovery allocation. The importer captures the source
allocation revision with its cursor/checkpoint expectations and rechecks it
inside its own write transaction. Thus work parsed before Apply cannot commit
after recovery using a stale allocation basis.

The allocation revision is monotonic per stable source identity across
generations. It is a concurrency fence, not an event count. Recovery remains
limited to the bounded snapshot and plan sizes above; the plan and audit are
fully refused if their encoded representation exceeds the cap. A committed
receipt is keyed by the exact evidence-bound token. A rolled-back attempt has
no receipt and can be retried with the same still-current token.

## Eligibility and refusal

Version 1 is limited to one complete, readable, recognized Codex legacy
cumulative source generation. Preparation must match the database evidence
for all existing records, including stable identities, provenance, record
hashes, offsets, attribution, and billing context. It refuses incomplete
tails, a changed generation, conflicting identities, mixed-format or
attribution ambiguity, records that disappear or become zero/no-request
allocations, unsupported changes outside the seven token vectors, and
monetary assertions requiring unsupported reconciliation. It does not
allocate provider identities merely to make recovery possible.

The accepted starting resource limits are 64 MiB for a source snapshot,
100,000 records, 10,000 inserted or overridden events, and 16 MiB of
canonical audit-plan data. Limits must be validated and made explicit in the
preview; an applicable plan is refused rather than silently truncated.
Cancellation must stop planning before mutation.

Preparation checks the opened file size before scanning and enforces the byte
budget in the reader itself, including one sentinel byte to detect growth
after the stat. The record-count pass stops at the first record beyond its
limit. Parsing is bounded to the captured snapshot length plus one sentinel;
growth between passes returns a controlled source-changed refusal. These
limits apply only to explicit recovery, not ordinary ingestion.

The existing monotonic covered-gap and trailing-uncovered Codex recovery paths
remain ordinary ingestion behavior. Reset repair is never invoked implicitly
by import, reporting, inspection, or Store.Open.

## Accounting and pricing

Report fields, JSON/CSV shapes, and numeric formats do not change. Without an
override, output remains identical. After an authorized correction, effective
token totals and request counts can change. Estimated prices are recalculated
from effective tokens using the existing catalog, attribution, and event
timestamps. Recovery does not manufacture provider-reported cost or retain a
stale calculated amount as current. A plan requiring monetary reconciliation
is refused. Compare recovered and fresh imports under the same pricing
evidence.

## Privacy and operational boundaries

Prepare and Apply explicitly read the selected provider source. They do not
change that file. Diagnostic inspection and project inspection remain stored
only and never repair or read provider files. Safe output omits source bytes,
paths, provider IDs, arbitrary parser errors, pricing metadata copied from
callers, prompts, responses, and credentials. Opaque local references,
timestamps, offsets, and exact activity counts can still identify local
activity.

Recovery must not stop or resume a collector automatically. Apply requires an
inactive collector and revalidates that condition under the writer
transaction. An importer parsed before Apply must fail its allocation-revision
check rather than commit stale events after recovery. A successful apply makes
the captured snapshot non-reusable and invalidates the parser checkpoint until
a normal full import verifies the effective allocation. Applying recovery is
not evidence that a diagnostic condition has resolved.

## Compatibility and CLI handoff

The core API owns preview evidence, token validation, transactions, audit,
and effective accounting. The standalone CLI owns commands, rendering,
destination files, export selection/preview/consent, and disclosure policy.
Recovery is not implemented through project inspection or protocol-v1
transport. CLI work may begin only against a published core tag containing
the accepted API. It must not stop/resume collectors automatically and must
surface refusal and stale-preview outcomes without regenerating consent.

## Implementation status

The implementation is split into three dependent reviews: storage and
effective accounting, bounded preparation and preview, and transactional
application. The preview API is `PrepareCodexRecovery`; the apply API is
`ApplyCodexRecovery`. Each later change is based on its predecessor so the
writer is never reviewed without the effective accounting readers and
evidence-bound preparation it depends on. The accepted resource limits and
eligibility rules above are implemented as `CodexRecoveryMaxSnapshotBytes` (64 MiB),
`CodexRecoveryMaxRecords` (100,000), `CodexRecoveryMaxChanges` (10,000), and
`CodexRecoveryMaxPlanBytes` (16 MiB). No applicable preview is truncated; a
bound breach returns a controlled refusal. Parser capture retains at most
100,000 normalized events in memory, in addition to the bounded source stream
reader. Tests using candidate-generated pre-state are reconstructed-state
tests, not evidence of opening a database created by a pre-fix binary; that
distinction matters: the A/B/C version-transition was also exercised with a
temporary database created by the public importer at commit
`acbe54c6c81af456756d14c3235c3d0aaafd1496` (the parent of #203). Its synthetic
source had no optional reasoning field on A/C and had that field on B; the old
binary imported A/C only. Candidate core opened and migrated that database,
previewed one insertion and one override, applied the plan, and read effective
totals of 250/25 over three events. The database and source remained under
`/tmp` and are not committed. This verifies the historical importer-to-current
core transition on this platform; it does not establish that any particular
production database has this cause.

## Acceptance coverage map

| Requirement | Implementation / evidence |
| --- | --- |
| Preserve base event rows; effective accounting uses seven categories | Migration 19 view and accounting reader tests from PR 1; Apply public-API test asserts stable A/C IDs and vectors and 250/25 over three events. |
| Preview is bounded, source-selected, read-only, and evidence-bound | `PrepareCodexRecovery`; `TestPrepareCodexRecoveryPreviewsLegacyCumulativeRepairWithoutWrites`, refusal and privacy tests. |
| Apply only exact reviewed preview and return receipt idempotently | `ApplyCodexRecovery`; A/B/C test repeats the same token after deleting the source and checks the unchanged receipt and event count. |
| Identical concurrent applies across Store handles commit once | `TestConcurrentCodexRecoveryApplyReturnsOneCommittedReceiptAcrossStores`; both callers receive the same committed receipt and the database contains one run, two journal entries, and three ordinary events. |
| Commit all accounting state atomically | `TestApplyCodexRecoveryRollsBackAtEachTransactionalWriteBoundary` injects failures at run, event, journal, head, revision, cursor, and checkpoint writes and verifies the pre-apply state remains. |
| Fence parser work started before Apply | `TestImporterAppendRejectsStaleAllocationRevision` validates the source-revision check in the ordinary append transaction. |
| Reverify the effective allocation through normal import | A/B/C Apply test imports the same full snapshot again, asserts no new event, and checks snapshot reuse is restored only after that import. |
| Open a real historical database transition | Synthetic SQLite database generated by the public importer at the pre-#203 commit above; candidate preview/apply produced the accepted totals. |
| CLI compatibility | Standalone `komposyn-lg` test suite run against this candidate with an isolated temporary module replacement; no replacement directive is committed. |

The CLI handoff requires the reviewed public APIs in a published core tag and
must retain explicit preview/consent, stale-token handling, and no automatic
collector stop/resume. Local validation includes native Linux unit/race tests
and the standalone CLI suite with a temporary module replacement. Hosted
macOS/Windows checks remain the evidence for native platform behavior; these
checks do not establish the production cause reported in #197.
