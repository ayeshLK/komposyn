# Collector bounded source verification design

Status: proposal for issue [#138](https://github.com/ayeshLK/komposyn/issues/138).
This document is a design gate, not an implementation or a claim of measured
improvement. It is based on the acquisition path merged in core PR #141 at
`36fea986b18d2c5ad32f4876b50412a03f9d933f`, with the baseline and accounting
oracle from #136 retained in
[`collector-performance-baseline.md`](collector-performance-baseline.md).

## Proposed approach

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

## Implementation and validation gate

The implementation should keep the public `ClaudeAdapter.Import`,
`CodexAdapter.Import`, and collector behavior compatible. A private reader
interface may be introduced for deterministic mutation injection; it must not
become a general exported API. The streaming JSONL reader must preserve current
line offsets, CRLF handling, blank-line behavior, malformed/interior records,
incomplete final records, strict usage-shape allowlists, and unlimited record
size. Codex cumulative baselines, response deduplication, and session metadata
must be kept exactly as full replay currently computes them.

Before implementation is considered complete, tests must deterministically
mutate sources between passes and during parser reads for append, truncation,
same-size/same-mtime rewrite, path replacement, adapter change, and repeated
change exhaustion. They must assert no events, references, receipt, outcome,
diagnostic, or cursor leak from discarded attempts. Include a prefix larger
than the verification buffer with late provider recognition, plus EOF fallback
with no recognized shape. Assert prefix offsets, malformed/ignored outcomes,
and final fingerprints match full replay, while retained raw bytes do not grow
with prefix length. Also retain cancellation, large record, partial-tail,
cursor conflict, and concurrent explicit-import coverage. Differential tests
compare canonical events, references, full fingerprints, generations, offsets,
receipts/outcomes, diagnostics, collector health, and exact JSON/CSV reports
with the frozen full-replay oracle.

Benchmark unchanged and changed workloads at 1,000 and 10,000 records for both
providers, plus malformed/unsupported input, pending tails, large records,
many small files, and a small number of large files. Report source bytes read,
bytes hashed, parser records, event insert attempts, B/op, allocs/op, and
repeated timing ranges on the same host as the #137 comparison where possible.
The acceptance target is no source-sized allocation for unchanged polling;
report the expected second read on changed input separately. Do not claim a
latency win unless the repeated evidence supports it. Run `go mod verify`,
`go vet ./...`, `go test ./...`, `go test -race ./...`, `make check`, and the
standalone candidate compatibility workflow before merge.
