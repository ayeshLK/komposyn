# Issue #139 implementation evidence

This document tracks runtime work after the accepted design in
[collector-append-checkpoint-design.md](collector-append-checkpoint-design.md).

## Revisions and schema

- Design PR #144 merged at `a65a672edc30650f48c9fc6b8b99bc06dd39af44`.
- Implementation work started from current `origin/master` at that exact SHA in
  a dedicated worktree and branch.
- Independent full-replay oracle: published core v0.7.1 at
  `4519551527b28461b250bfdbc7f9f66473a6a219`.
- Tested implementation commit: `8a1b5dbc8ddb6d3a438cf84fd7842f9409fd6dfe`.
- SQLite schema before implementation: 15. The implementation adds immutable
  migration 16 for private per-source parser checkpoints.

## Implementation

- Claude and Codex persist bounded, allowlisted normalized parser state after
  LF-terminated record boundaries. State is versioned by format and parser
  revision, tied to source generation and a SHA-256 prefix fingerprint, and
  protected by a digest. No raw records, source tails, prompts/responses, or
  serialized hash internals are persisted.
- Acquisition captures cursor, checkpoint, and identity expectations before
  reading the provider file. Append transactions recheck cursor/checkpoint and
  atomically commit source identity, reconciled references, events, cursor,
  and replacement checkpoint or invalidation. Context cancellation is checked
  before transaction work and before commit; failure injection covers rollback.
- Changed snapshots retain complete-file hashing and provenance. Supported
  continuation parses only records after the checkpoint boundary. Unterminated
  records remain after that boundary and are re-read and deduplicated normally.
  Missing, incompatible, corrupt, oversized, prefix-mismatched, rewritten,
  truncated, incomplete, or unsafe-attribution state selects full replay.
- Codex continuation carries cumulative baselines and response dedup/conflict
  state without pruning. New session scopes conservatively select full replay
  until database-wide reconciliation safety can be proven.

## Validation evidence

Commands run on the implementation worktree:

- `go mod verify`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...`
- `make check`
- `bash -n scripts/*.sh`
- `git diff --check`
- Standalone CLI compatibility: temporary CLI checkout with `GOWORK=off` and a
  temporary module replacement to the candidate core, followed by
  `GOWORK=off go test ./...`.
- Independent differential runner:
  `scripts/verify-append-checkpoint-baseline.sh`. It executes the oracle test
  separately at the published v0.7.1 commit and on the candidate, then compares
  events, references, provenance, cursors, identities, receipts, outcomes,
  diagnostics, health, and JSON/CSV. It normalizes only diagnostic
  `schema_version`, which intentionally changes from 15 to 16.
- Targeted fallback and atomicity tests cover both providers, including missing,
  corrupt, oversized, old-parser, old-format, prefix-mismatched checkpoints,
  unterminated-tail completion, Codex duplicate/conflicting response IDs,
  cumulative reset, late attribution/session transitions, injected storage
  failure, and cancellation rollback.

Repeated benchmark command:

```sh
BENCH_REPEAT_COUNT=5 scripts/benchmark-append-checkpoint.sh
```

This compares the v0.7.1 oracle and candidate on the same host at 1,000 and
10,000 generated records. It reports elapsed time, B/op, allocs/op, bytes read
and hashed, parser records, event insert attempts, historical insert attempts,
and encoded checkpoint size. Full-history verification remains proportional to
history size; no append-byte-only I/O claim is made.

| Provider / records | Scenario | v0.7.1 time range (median) | Candidate time range (median) | Baseline → candidate B/op; allocs/op | Candidate parse / inserts | Read / hashed bytes | State |
| --- | --- | ---: | ---: | --- | --- | --- | ---: |
| Claude / 1,000 | unchanged | 1.18–1.31 ms (1.21) | 1.27–1.47 ms (1.42) | 96,016 → 110,408; 275 → 518 | 0 / 0 | 186,024 / 186,024 | — |
| Claude / 1,000 | one-record append | 99.18–120.38 ms (105.74) | 5.00–8.86 ms (5.73) | 11,041,960 → 405,280; 113,808 → 2,150 | 1 / 0 historical, 1 total | 372,422 / 930,867 | 389 B |
| Claude / 10,000 | unchanged | 7.23–7.81 ms (7.50) | 7.30–12.65 ms (10.40) | 96,016 → 110,408; 275 → 518 | 0 / 0 | 1,869,025 / 1,869,025 | — |
| Claude / 10,000 | one-record append | 900.94–1,385.23 ms (1,277.77) | 36.56–44.15 ms (38.08) | 116,849,192 → 2,133,280; 1,130,863 → 11,150 | 1 / 0 historical, 1 total | 3,738,426 / 9,345,876 | 392 B |
| Codex / 1,000 | unchanged | 1.37–2.08 ms (1.52) | 1.56–2.61 ms (1.71) | 95,376 → 120,376; 265 → 554 | 0 / 0 | 224,328 / 224,328 | — |
| Codex / 1,000 | one-record append | 102.28–126.89 ms (110.64) | 6.76–7.56 ms (7.39) | 13,082,856 → 479,416; 151,853 → 2,290 | 1 / 0 historical, 1 total | 449,112 / 1,122,551 | 1,327 B |
| Codex / 10,000 | unchanged | 9.16–15.33 ms (10.39) | 9.87–15.18 ms (12.88) | 95,376 → 120,376; 265 → 554 | 0 / 0 | 2,281,332 / 2,281,332 | — |
| Codex / 10,000 | one-record append | 1,028.45–1,270.09 ms (1,061.63) | 54.52–96.34 ms (70.32) | 137,680,904 → 2,639,512; 1,510,903 → 11,291 | 1 / 0 historical, 1 total | 4,563,128 / 11,407,587 | 1,332 B |

The generated append cases avoid historical decoding and insert attempts, with
full-file verification still visible in the hashed-byte counts. The synthetic append cases were faster in these samples; unchanged-poll timing
was roughly flat. Because of the shared-host contention described above, these
measurements do not establish a general latency improvement. Five samples
were collected per scenario with `-benchtime=1x -count=5`. An unrelated
standalone CLI foreground measurement was active on this shared host during
part of the run, so treat timing ranges as noisy local measurements rather than
production latency guarantees or a general latency claim.

Corrupt-checkpoint and over-1-MiB state-limit fallback cases replayed all
10,002 records at 10,000-event scale on both providers, submitted all 10,000
historical event attempts, and then replaced the bad checkpoint with a valid
bounded state (392 B Claude, 1,332 B Codex). Candidate fallback medians were 1,116.05 ms (Claude corrupt), 1,167.89 ms
(Claude state limit), 1,507.92 ms (Codex corrupt), and 1,452.68 ms (Codex
state limit). The corresponding v0.7.1 full-replay append medians were 995.93,
1,121.10, 1,146.40, and 1,087.34 ms. The v0.7.1 oracle has no checkpoint
feature, so its fallback-labelled workload is its ordinary full-replay append
path.
## Remaining review gates

- Review the migration 16 and parser/storage consistency changes in the
  implementation PR.
- Review independent differential and repeated benchmark evidence on the PR
  head. This document does not claim merge or release readiness.
- CLI measurement and integration remain separately tracked under
  `komposyn-lg` issues #23 and #20.
