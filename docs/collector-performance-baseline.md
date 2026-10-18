# Collector performance and correctness baseline

Issue [#136](https://github.com/ayeshLK/komposyn/issues/136) establishes a
measurement baseline before collector optimizations. This is a developer
verification artifact, not a production metrics API or CI timing gate.
Fixtures and databases are generated under Go's temporary test directory.

## Reproduce

From the core repository root:

```sh
go test ./plugins/library/token-logger -run '^$' \
  -bench '^BenchmarkCollectorGeneratedBaselines$' -benchtime=10x -count=5
```

This measures ten operations per sample, repeated five times. Cold-import
timing includes a poll and persistence but excludes fixture generation and
schema initialization. Steady-state, append, malformed/unsupported, and
pending-tail cases exclude fixture generation, schema initialization, and
initial import. There is no additional explicit warm-up. Generated files may
be in the OS page cache; “cold import” means a first import into an empty
database, not a cold filesystem cache. Run on an otherwise idle host and
retain complete benchmark stdout with the PR or issue results.

The matrix includes Claude and Codex cold imports (1,000 events); unchanged
polls with retained and newly constructed collectors; usage and non-usage
appends after 1,000 events; unchanged malformed and unsupported sources; a
pending-tail completion; a generated 64 KiB record; 100 files with two events
each; and two files with 100 events each.

For scaling through 10,000 events, run:

```sh
go test ./plugins/library/token-logger -run '^$' \
  -bench '^BenchmarkCollectorGeneratedHistoryScale$' -benchtime=1x -count=3
```

The same host produced these three-sample scaling observations (one operation
per sample):

| Provider | History | Unchanged median (range) | Append median (range) | Append B/op | Append allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Claude | 1,000 | 1.38 ms (1.31–1.66) | 124.95 ms (124.27–126.52) | 11,535,416 | 113,721 |
| Claude | 10,000 | 11.42 ms (11.03–16.80) | 935.52 ms (862.34–1,276.21) | 123,067,488 | 1,130,768 |
| Codex | 1,000 | 1.42 ms (1.26–1.56) | 96.85 ms (95.59–100.75) | 13,654,136 | 151,752 |
| Codex | 10,000 | 10.57 ms (9.32–10.91) | 978.44 ms (957.82–982.53) | 144,717,832 | 1,510,805 |

The optional unexported observer measures collector and adapter source reads,
bytes passed to source and record hashes, top-level JSONL records decoded by
adapter detection and parsing, and SQLite event insert attempts immediately
before insertion. Historical attempts are counted by matching event IDs
persisted before the timed operation; public receipt duplicate counts are not
used as a work proxy. Observer fields are inactive when nil and are never
exported or persisted.

## Linux baseline

Captured 2026-10-18 on an issue branch based on core master commit
`87b7b2b4008748446093a0ba940e6160812ceb37` (release manifest version 0.7.0): Go
1.26.9, Ubuntu 24.04.5, Linux amd64, Intel Core i7-10510U at 1.80 GHz, 8
logical CPUs. Values are medians of five samples of ten operations. Ranges are
minimum–maximum sample time. Memory values are median B/op and allocs/op. This
host was not reserved from other work; timings are observations, not gates.

| Provider | Scenario | Time/op (median; range) | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| Claude | Cold import, 1,000 events | 102.50 ms (98.15–103.51) | 11,538,412 | 113,679 |
| Codex | Cold import, 1,000 events | 111.82 ms (107.78–112.49) | 13,626,839 | 151,680 |
| Claude | Unchanged, retained collector | 1.02 ms (0.96–1.13) | 656,072 | 249 |
| Codex | Unchanged, retained collector | 1.15 ms (1.14–1.23) | 720,168 | 227 |
| Claude | Usage append after 1,000 events | 87.86 ms (84.83–91.17) | 11,534,628 | 113,722 |
| Codex | Usage append after 1,000 events | 100.43 ms (96.35–124.31) | 13,624,648 | 151,761 |
| Claude | Non-usage append after 1,000 events | 86.52 ms (84.60–87.69) | 11,527,065 | 113,622 |
| Codex | Non-usage append after 1,000 events | 118.09 ms (106.10–124.63) | 13,615,447 | 151,631 |
| Claude | Malformed unchanged, 50 events | 5.00 ms (4.95–5.82) | 616,373 | 6,304 |
| Codex | Malformed unchanged, 50 events | 5.64 ms (5.28–6.07) | 720,497 | 8,196 |
| Claude | Unsupported unchanged, 50 events | 5.04 ms (4.93–5.96) | 616,083 | 6,360 |
| Codex | Unsupported unchanged, 50 events | 5.60 ms (5.51–6.76) | 723,916 | 8,244 |
| Claude | Pending-tail completion, 50 events | 6.02 ms (5.91–6.39) | 605,024 | 6,207 |
| Codex | Pending-tail completion, 50 events | 6.86 ms (6.44–7.81) | 710,035 | 8,099 |
| Claude | 64 KiB record, 10 events | 3.29 ms (3.16–3.45) | 813,861 | 1,794 |
| Codex | 64 KiB record, 10 events | 3.58 ms (3.38–4.21) | 835,424 | 2,175 |
| Claude | 100 files × 2 events | 118.35 ms (113.46–124.08) | 6,528,517 | 82,082 |
| Codex | 100 files × 2 events | 118.98 ms (115.46–121.95) | 6,918,412 | 88,685 |
| Claude | 2 files × 100 events | 21.51 ms (19.99–23.56) | 2,344,973 | 23,856 |
| Codex | 2 files × 100 events | 24.09 ms (22.98–24.40) | 2,789,357 | 31,441 |

| Provider / scenario | Source bytes read/op | Source bytes hashed/op | Parser records/op | Event insert attempts/op | Historical attempts/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Claude / unchanged, 1,000 | 364,040 | 182,020 | 4 | 0 | 0 |
| Codex / unchanged, 1,000 | 440,648 | 220,324 | 2 | 0 | 0 |
| Claude / usage append, 1,000 | 364,406 | 545,298 | 1,004 | 1,001 | 1,000 |
| Codex / usage append, 1,000 | 441,096 | 660,222 | 1,003 | 1,001 | 1,000 |
| Claude / non-usage append, 1,000 | 364,218 | 545,022 | 1,004 | 1,000 | 1,000 |
| Codex / non-usage append, 1,000 | 440,878 | 659,890 | 1,003 | 1,000 | 1,000 |
| Claude / malformed unchanged, 50 | 18,370 | 27,361 | 54 | 50 | 50 |
| Codex / malformed unchanged, 50 | 21,968 | 32,688 | 53 | 50 | 50 |
| Claude / unsupported unchanged, 50 | 18,740 | 27,731 | 54 | 50 | 50 |
| Codex / unsupported unchanged, 50 | 22,366 | 33,086 | 53 | 50 | 50 |

An unchanged poll reads each source once for collector adapter detection and
again for the adapter's authoritative snapshot. Each snapshot is decoded once
for each detection pass, so the parser-record counter includes both detection
passes as well as adapter parsing. Healthy reusable snapshots avoid parsing
history and issue no event insert attempts. Appends reparse history to preserve
cumulative provider context. These counters explain work; they do not replace
correctness checks.

## Correctness oracle

`TestGeneratedCollectorMatchesDirectImportOracle` compares generated Claude
and Codex histories imported directly and through `Collector`: full event rows,
project/session references and aliases, cursor fingerprint/generation/offset,
receipts, outcomes, collector health, diagnostics, and byte-for-byte JSON/CSV
reports. Independently frozen event accounting verifies three canonical events
at one input and one output token each, while JSON/CSV expectations check the
three-request aggregate, provider pricing provenance, unavailable-cost basis,
and report shape. It also verifies both paths classify the next poll as
unchanged. `TestUnchangedCollectorCountsBothAdapterDetectionPasses` protects
the observer's accounting for the initial collector detection and authoritative
snapshot validation.

Existing focused tests cover same-size/same-mtime rewrites
(`TestClaudeImportDetectsReplacementWithProviderID`,
`TestCodexImportDetectsSameSizeSameMtimeReplacement`), replacements and
aliases (`TestCodexImportDetectsReplacementAndPreservesAppendIdempotency`,
`TestExplicitImportsReuseCollectorSourceIdentity`,
`TestCollectorRetargetedAliasKeepsCanonicalSourcesDistinct`), concurrent
replacement/append and cursor conflicts
(`TestConcurrentClaudeReplacementsRevalidateCursorBeforeCommit`,
`TestConcurrentClaudeAppendDiscardsSupersededSnapshot`,
`TestClaudeImportBoundsCursorConflictRetries`), cancellation and rollback
(`TestCollectorCancellationJoinsWithoutPartialPersistence`,
`TestCodexTokenUsageRecordPersistenceFailureRollsBackReferencesAndCursor`),
metadata/cumulative usage and response conflicts
(`TestCodexTokenUsageRecordAttributionUsesMetadataAfterLegacyRecords`,
`TestCodexImportNormalizesCumulativeSnapshots`,
`TestCodexTokenUsageRecordAppendReportsExactReceiptAndTotals`,
`TestCodexTokenUsageRecordDuplicateResponseReachesStorageDeduplication`),
pending tails and diagnostic/health behavior (`TestCollectorLargeRecordsUseOneUnlimitedSnapshot`,
`TestCollectorOutcomeHealthAndDiagnosticDeduplication`,
`TestCollectorReportsHealthForEachConfiguredRoot`), and exact machine output
(`TestMachineFormatsKeepExactValuesAndStableShape`, `TestReportFormats`). All
fixtures are synthetic; these regressions assert expected values independently
of the new differential oracle.

## Proposed improvement budgets

These are review targets, not CI timing gates. Compare on the same otherwise
idle host with five samples of at least ten operations; report medians and
ranges. Run correctness tests before claiming an improvement.

| Workload | Proposed target | Required invariants |
| --- | --- | --- |
| Healthy unchanged at 1,000 and 10,000 events | At least 30% lower median time and 50% fewer source bytes read | Zero insert attempts; same cursor, receipt, outcomes, and exact reports |
| Usage/non-usage append at 1,000 and 10,000 events | At least 25% lower median time and 50% fewer historical insert attempts | Same cumulative usage, identities, offsets, references, receipts, and report values |
| 100 small files and 2 large files | At least 20% lower median time for each distribution | Same sources, outcomes, diagnostics, and persisted rows |
| Malformed/unsupported, pending-tail, and 64 KiB records | No more than 10% median regression | Preserve retryable/incomplete state, strict usage shape, and unlimited records |

Do not claim a stable improvement from this single-host baseline alone. A
candidate must publish new repeated results and explain any workload that
misses its target before merge.
