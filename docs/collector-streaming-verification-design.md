# Collector bounded source verification design

Status: approved design and implementation under review in core PR #143 for
[issue #138](https://github.com/ayeshLK/komposyn/issues/138). The design is
implemented on top of the acquisition path merged in core PR #141 at
`36fea986b18d2c5ad32f4876b50412a03f9d933f`, with the baseline and accounting
oracle from #136 retained in
[`collector-performance-baseline.md`](collector-performance-baseline.md).

## Implemented approach

Replace the collector and explicit adapter import paths' whole-file byte
slice with a two-pass stream over one opened file descriptor. The first pass
computes the full SHA-256 digest, the
digest of the previously committed prefix, total byte length, and (for the
collector) adapter classification. It uses a fixed-size read buffer and a
record reader that retains at most the current JSONL record for classification.
Once the first
pass completes, compare its prefix digest and length with the captured cursor
to determine the source generation, and compare its full digest and length to
the reusable cursor. An unchanged source can return immediately after this
pass, after revalidating path/descriptor identity, without allocating a
source-sized byte slice or invoking the provider parser. Explicit adapter
imports already know their adapter; the collector performs classification
during each pass.

For a changed source, rewind the same descriptor and run the selected adapter
parser as a stream. The parser pass computes its own full and committed-prefix
digests from the exact bytes it consumes. The adapter found on pass one is
provisional for pass two: feed each record immediately to that adapter while
independently running the existing adapter classifier over the same record.
Do not wait for classification before parsing and do not retain an
unclassified prefix. At EOF, compare the pass-two classification with pass one
along with the digests and lengths. Existing classification semantics remain
unchanged: skip unrecognized or malformed records until a recognized shape is
found, and use the Codex fallback only at EOF. Thus even a late classification
or EOF fallback retains at most the fixed read buffer and current record as
raw source bytes; the parser's normalized state remains separately accounted
below.

The provisional provider decision, normalized records, byte offsets, event
fingerprints, and generation are accepted only if they refer to the same
ordered byte stream. Event fingerprints use the first-pass full digest only
after the parser-pass digest and byte length match it. The parser must not
persist events, references, receipts, outcomes, or cursors while either pass
or the comparison is incomplete.

The first and second pass must agree on full digest, total length, prefix
digest, adapter result, and opened-file identity. A mismatch discards all
normalized in-memory work and retries the complete acquisition within the
existing bounded retry limit, recapturing cursor expectations before the next
read. Exhaustion returns an error and leaves the event/cursor transaction
untouched. Cancellation is checked between fixed-size reads and record
decodes. Path canonical identity and descriptor/path identity are checked
before acquisition and again before the short compare-and-swap persistence
transaction. The database transaction begins only after parsing and
verification finish.

## Mutation behavior and limits

| Change | Detection and outcome |
| --- | --- |
| Append, truncate, or in-place edit between passes | Length and/or full digest differs; discard the candidate and retry. |
| Same-size, same-mtime rewrite between passes | Full digest differs; metadata is not an unchanged shortcut. |
| Change during the parser pass | Its observed byte stream's digest or length differs from pass one; do not commit. |
| Pathname retarget or file replacement | Canonical path and `os.SameFile` checks fail; discard and reacquire. |
| Adapter classification changes between passes | Adapter mismatch; discard and reacquire. |
| Change after a verified pass but before the next poll | The committed cursor describes the verified bytes; the next poll detects the changed full digest and imports a new generation or append. |
| Repeated changes through the retry bound | Return the existing source-change/retry error; no partial database state. |

The invariant is equivalence of the complete ordered byte streams observed on
both passes, subject to SHA-256 collision resistance. It is not an operating
system level atomic filesystem snapshot: provider writers do not participate
in a shared lock protocol, and a portable Go API cannot freeze a file against
arbitrary in-place writes. This is the same class of limitation as the current
read-to-EOF acquisition; the proposal strengthens change detection by
requiring two matching observations before a changed source is committed. A
file changed after validation is handled on the next poll. This tradeoff must
be accepted in review before implementation.

The captured cursor expectation remains the one read before the first source
pass. Do not refresh it between passes. The persistence operation retains the
existing compare-and-swap check against that expectation; a concurrent
importer causes bounded reacquisition rather than advancing from a cursor that
was not the starting point for these bytes. Resolve an existing source mapping
read-only during acquisition; if there is no mapping, defer claiming the
deterministic identity until both passes verify. If a concurrent import claims
a different identity or advances its cursor first, discard this attempt and
reacquire with the new pre-read expectation. Identity mapping, events, and
cursor must not expose a partially parsed or unverified result.

## Memory, I/O, and parser accounting

- Source verification uses a fixed-size buffer (proposed 64 KiB); it does not
  retain a file-sized source slice.
- Record handling retains a single current JSONL record, including records
  larger than 64 KiB. No new maximum record size is introduced. Peak memory
  therefore includes the largest active record and JSON decoder allocations.
- Full replay still retains normalized events until the existing atomic write
  transaction. Claude session state and Codex session/cumulative/response
  maps also remain proportional to the state the existing parser needs. The
  design does not claim bounded total memory independent of event count.
- Unchanged polls read the source once and hash it once. Changed polls read it
  twice and hash it twice, while decoding provider records once. The additional
  changed-source I/O is an explicit cost for proving that the parsed stream
  matches the verified full fingerprint without retaining source bytes.
- Prefix hashing and full-file hashing are computed together in each pass;
  only bytes before the captured committed offset feed the prefix digest.
- No raw source bytes are written to temporary files, databases, logs,
  diagnostics, or persistent snapshots. Provider files remain read-only.
- Normalization stays outside SQLite write transactions so collector
  heartbeats, cooperative stop requests, and other store operations remain
  serviceable.

If measured changed-source I/O causes an unacceptable regression, do not
silently drop the second-pass validation or substitute metadata-only checks.
Return to design review with a different bounded mechanism and the same
correctness obligations.

## Implementation and validation

The implementation keeps the public `ClaudeAdapter.Import`,
`CodexAdapter.Import`, and collector APIs unchanged. Private read hooks support
deterministic mutation injection and are not exported. The streaming JSONL
reader preserves current line offsets, CRLF handling, blank-line behavior,
malformed/interior records, incomplete final records, strict usage-shape
allowlists, and unlimited record size. Existing Codex tests cover cumulative
baselines, response deduplication, and session metadata.

The implementation streams collector and explicit adapter imports through one
opened descriptor. Deterministic tests cover append, truncation,
same-size/same-mtime rewrite, mutation during parser reads, path replacement,
adapter changes, cancellation, late provider recognition beyond 64 KiB, and
Codex EOF fallback. The mutation tests verify discarded attempts do not
persist events or advance cursors. Existing tests retain large-record,
partial-tail, cursor-conflict, concurrent-import, and generated full-replay
oracle coverage. The oracle checks the established collector behavior against
fixed expected receipts, outcomes, reports, and stored data.

The read hooks and benchmark observer are private test instrumentation. No
exported API or on-disk protocol changed. Parsing still holds normalized
events and provider state needed for a full replay; only raw source retention
is removed.

## Matched measurement evidence

Baseline was measured at merged #141 (`36fea986b18d2c5ad32f4876b50412a03f9d933f`)
and candidate measurements were taken from this PR before documentation-only
evidence updates. Both used Go 1.26.9 on Linux/amd64, Intel Core i7-10510U, with
five repetitions (`-benchtime=10x -count=5`) for generated 1,000-record
workloads and three repetitions (`-benchtime=1x -count=3`) for 1,000- and
10,000-record scale workloads. The unchanged numbers below use the retained
collector case; changed workloads append one complete generated history.

| Provider / history | Unchanged B/op baseline → candidate | Changed B/op baseline → candidate | Median changed time baseline → candidate | Source bytes read baseline → candidate |
| --- | ---: | ---: | ---: | ---: |
| Claude, 1k | 465,459 → 88,455 | 11,343,970 → 11,041,744 | 143.5 ms → 138.1 ms | 182,203 → 364,406 |
| Codex, 1k | 497,507 → 87,749 | 13,401,676 → 13,066,228 | 158.6 ms → 182.6 ms | 220,548 → 441,096 |
| Claude, 10k | 4,505,080 → 96,016 | 121,189,808 → 116,850,088 | 1,325.5 ms → 1,863.3 ms | 1,869,213 → 3,738,426 |
| Codex, 10k | 4,913,912 → 95,376 | 142,431,752 → 137,680,320 | 1,563.1 ms → 1,629.2 ms | 2,281,564 → 4,563,128 |

Unchanged source reads and hash input remain one source length; unchanged
polling no longer allocates a source-sized byte slice. Changed input is read
twice as designed. Changed-workload allocation falls modestly because event
and provider state still dominate. Timing is mixed: Claude 10k changed replay
is slower, Codex 1k changed replay is slower, while other cases are close or
improved. These measurements establish the allocation tradeoff, not a general
latency improvement. Re-run them after any runtime changes and review the
changed-source cost before merge.

Reproduction commands (run from the module root):

```sh
go test ./plugins/library/token-logger -run '^$' -bench '^BenchmarkCollectorGeneratedBaselines/(claude|codex)/(unchanged-retained-collector|usage-append|non-usage-append|malformed-unchanged|unsupported-unchanged)$' -benchtime=10x -count=5
go test ./plugins/library/token-logger -run '^$' -bench '^BenchmarkCollectorGeneratedHistoryScale$' -benchtime=1x -count=3
```

Local validation for the implementation: `go mod verify`, `go vet ./...`,
`go test ./...`, `go test -race ./...`, and `make check`. GitHub CI additionally
runs Linux, macOS, and Windows tests, Race tests, CodeQL, and the
Standalone CLI compatibility job, which runs the public CLI suite against the
candidate core via a temporary module replacement. The replacement remains
uncommitted. PR checks must pass on the implementation revision before this
issue is ready to close. The checked-in generated-history benchmarks include
unchanged, usage append, non-usage append, malformed, and unsupported records
with source bytes read, bytes hashed, parser/classifier records, event insert
attempts, B/op, and allocs/op. This PR records matched 1k/10k unchanged and
append results; the expanded large-record, many-small-file, and few-large-file
performance matrix remains useful follow-up evidence rather than a latency
claim for this change.
