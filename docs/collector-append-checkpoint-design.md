# Collector validated append continuation design

Status: proposal for review under [core issue #139](https://github.com/ayeshLK/komposyn/issues/139). This document changes no runtime behavior and proposes no migration for implementation. Runtime work must wait until reviewers approve the checkpoint identity, provider state, fallback, accounting, and storage transaction design below.

## Baseline and scope

The design starts from core v0.7.1, commit `4519551527b28461b250bfdbc7f9f66473a6a219` (the published source tag and current `master` at preparation time). Core schema is 15. The streaming acquisition and two-pass verification work from #138 is already in this baseline. The public standalone CLI continues its independent measurements and integration under [komposyn-lg issue #23](https://github.com/ayeshLK/komposyn-lg/issues/23).

Core remains responsible for provider parsing, normalized ingestion, references, storage, migrations, and reports. CLI configuration, policy, commands, and process lifecycle stay in the standalone repository. This proposal preserves full replay as the compatibility and recovery path. It adds no protocol, public API, report, or data-directory change.

## Current ingestion and persistence path

`acquireStreamingSourceSnapshot` captures the source's canonical identity and cursor expectations before reading. Collector identity is derived from the canonical path identity and adapter mapping; migration 10 stores the stable mapping in `collector_source_identities`. The opened file is hashed in a first pass for its complete fingerprint, the old cursor prefix, byte length, and (for collector discovery) adapter classification. `sourceGenerationFromStream` retains the generation when the committed prefix matches, and advances it on truncation or rewrite. A source is unchanged only when the reusable cursor's full length and fingerprint still match.

For changed input, `importStreamingSnapshot` rewinds the same descriptor. The selected adapter parses the second pass while it computes a second complete digest and prefix digest, repeats classification when needed, and verifies file identity. No events, references, receipt, or cursor are persisted until both passes agree. Cancellation or a mismatch discards the in-memory attempt; cursor conflicts retain the pre-read expectation and use the existing bounded reacquisition (`sourceCursorRetryLimit`, currently three).

Claude parsing carries session/project/model attribution across records, then emits events and final references. Codex parsing carries session/thread scopes, legacy cumulative token baselines, turn and thread cumulative baselines, response-ID deduplication and conflict state, and late session metadata. Both parsers currently replay the complete JSONL history, collect normalized events and references, summarize per-record outcomes, then call the storage compare-and-swap append. `appendWithReferencesAndCursorIfCurrent` compares the captured cursor within the SQLite transaction; the transaction reconciles and writes references, inserts events, advances the cursor, and commits atomically.

The cursor's `ByteOffset` is the observed file length, not a continuation boundary: it can include an incomplete final record. The `FileFingerprint` is the full file fingerprint, and source generations and event provenance use the fingerprint of the complete observed file. Neither field alone proves that parser state can safely resume after an append.

## Proposed checkpoint identity and boundary

Add one private checkpoint per stable source ID, stored in the same SQLite database as its cursor and events. A checkpoint is reusable only when all of these agree:

- Stable `source_id` resolved through the existing canonical source identity mapping, and the same adapter.
- Explicit parser revision (`claude` or `codex`) and checkpoint format revision. Any behavior or serialized-state change that can alter normalization, attribution, or outcomes increments the relevant revision.
- The cursor's source generation.
- A complete-record boundary offset and SHA-256 fingerprint of exactly the bytes before that boundary.
- A strict, validated, bounded, provider-specific normalized-state payload and its version/digest.
- The captured cursor expectation still matches at commit time.

The boundary is the byte immediately after the last complete LF-terminated record, including its LF. CRLF is consumed as part of that record. A final JSON object without LF is deliberately not a safe boundary: a writer can append bytes that complete that same record. Blank and malformed LF-terminated records are complete boundaries and their outcome counts are part of the checkpoint. Any incomplete final bytes remain only in the provider file and are reread on the next import. In particular, never resume from `Cursor.ByteOffset` merely because it is the last committed cursor value.

The checkpoint is keyed to persistent source identity, rather than a `Collector` object, so it can be reused after process restart, explicit adapter imports, or construction of a new collector. A restored database backup naturally restores cursor, checkpoint, references, and events together. A missing checkpoint, an older cursor with no checkpoint, or an unsupported explicit import falls back to the existing full parser.

Before suffix parsing, the current source pass must verify the checkpoint prefix bytes against its saved prefix fingerprint. The parser pass then re-reads from byte zero, hashes/discards complete prefix records without provider decoding, independently verifies the same prefix at the boundary, and decodes only the suffix. It continues hashing through EOF. The existing full-digest, byte-length, adapter, path/descriptor identity, and generation checks remain mandatory, and event provenance uses the full fingerprint of the verified current file, not the prefix digest. This reduces historical decoding and insert attempts; it does not claim total append I/O is proportional only to appended bytes. The complete file still has to be read and hashed for rewrite detection, and changed-source verification still uses the existing two observations.

## Provider state proposal

Checkpoint payloads are typed internal structures, not serialized parser objects. Allowlist fields explicitly and reject unknown fields, invalid values, negative counters, duplicate keys, and over-limit state. Never serialize raw JSON envelopes, source lines, prompts/responses, parser buffers, incomplete tails, or a hash implementation's internal state. Persist only ordinary normalized fields and a hex digest of the verified prefix.

For Claude, carry the current normalized session state: stable session ID, project ID, project display name, project root, and resolved model. This preserves late attribution behavior when metadata appears after usage records: the continuation must produce the same final attribution that replay applies to all events from the session. A state or metadata shape not proven equivalent falls back to replay.

For Codex, carry current and active session/thread scopes; normalized session/project/name/root/model metadata and explicit-project-ID status; per-scope legacy cumulative token baselines and presence flags; turn and thread cumulative baselines; stream-session markers; and seen response IDs with their normalized usage values, turn/thread values, and presence flags. These values preserve counter-reset handling, response duplicate versus conflict classification, session transitions, and late metadata across a checkpoint. No raw payload fields are retained.

Both payloads also carry only the scalar continuation accounting required to reproduce replay results: outcomes through the boundary, final provider-record ID, normalized reference state, and historical event insertion-attempt count. Events themselves are already represented by the database transaction and are not copied into the checkpoint. On a continuation, the historical insertion-attempt count contributes the same historical duplicate receipt count that replay would have produced, while only suffix candidate events reach SQLite. This accounting equivalence is a correctness gate, not an approximation.

Initial conservative bounds proposed for review are a 1 MiB encoded state ceiling, no more than 4,096 session/scope entries, no more than 16,384 seen Codex response IDs, and a 1 KiB maximum for a single persisted provider identifier. Reaching a limit disables continuation and selects full replay; state must never be pruned or probabilistically summarized. If full state cannot be encoded or validated, do not write a checkpoint. Reviewers should confirm or adjust these bounds against real supported histories before implementation.

## Fallback and equivalence rules

Use full replay for a missing, legacy, corrupt, oversized, or incompatible checkpoint; prefix mismatch; truncation/rewrite or generation change; adapter or parser revision change; invalid/non-boundary offset; a non-reusable cursor; unsupported parser state; or any metadata case for which equivalence has not been demonstrated. A failed continuation attempt discards all in-memory suffix work and retries from the original pre-read cursor expectation under the current bounded reacquisition policy. Repeated mutation or cursor conflict returns the existing error without advancing cursor, events, references, outcomes, or checkpoint.

Same inode, descriptor, size, or modification time is never sufficient verification. Full file hashing, source identity revalidation, and current generation semantics stay in place. Checkpoint read/validation and prefix hashing occur outside the SQLite write transaction. The short transaction rechecks the expected cursor and checkpoint identity, then atomically commits normalized references, suffix events, cursor, and replacement checkpoint (or checkpoint invalidation). No heartbeat or cooperative stop request should be blocked by source reading or parsing under a write lock.

Receipt, outcomes, diagnostics, and health must match full replay exactly. Outcome counts through the boundary are combined with suffix counts, including blank, malformed, unsupported, deferred-tail, and unchanged semantics. Historical event insert attempts contribute their replay-equivalent duplicate receipt count without issuing those inserts. Provider references and warning/reconciliation effects must also match; if that cannot be done without replay, that source state falls back. This design does not claim equivalence after unsupported out-of-band edits to database rows; normal application transactions, backup/restore, migration, and injected failures must preserve the coupled state.

## Storage and migration proposal

Schema 15 is the starting schema. If durable checkpoints are accepted, add one new immutable numbered migration (currently proposed as migration 16, after rechecking the schema at implementation time) creating a private checkpoint table with source ID primary key, adapter, parser revision, format version, generation, boundary offset, prefix fingerprint, normalized state bytes, state digest, and update timestamp. The migration must not alter or rewrite historical migrations. The table stores no source text or serialized hash state.

Extend the private append transaction with expected cursor and expected checkpoint conditions. It must compare both before any writes, and commit the checkpoint, normalized suffix events/references, cursor, and any identity claim in one transaction. Failure injection must prove rollback of all of them together. Cancellation, source mutation, cursor/checkpoint conflicts, parse errors, and failed reference/event writes advance none of that coupled state. Preserve the existing bounded reacquisition behavior.

Checkpoint serialization, verification, source reading, and normalization remain outside the write transaction. Migration validation must cover fresh databases, every supported historical schema, backup verification/restoration, migration failure with the original database and backup still usable, and refusal of a newer unknown schema.

## Deterministic correctness and performance gates

Use the published v0.7.1 source at `4519551527b28461b250bfdbc7f9f66473a6a219` as an independent pre-continuation full-replay oracle. The oracle must execute old code, not two wrappers around the candidate. Compare canonical event rows (including complete-file fingerprints and source offsets), references and aliases, source identity mappings, generation/cursor fields, receipts and outcomes, diagnostics/health, and exact JSON and CSV output.

Cover both providers for initial import, unchanged polling, one/multiple appends, rewrite, truncation, missing/incompatible/corrupt checkpoint fallback, and recreated collectors/explicit imports. Include pending-tail completion, blank/malformed/interior records, records larger than 64 KiB, late Claude and Codex attribution, Codex duplicate and conflicting response IDs, cumulative counter resets, and session/thread transitions. Exercise concurrent explicit imports, cursor and checkpoint conflicts, source mutation retries, cancellation, process restart, and failure injection proving events/references/cursor/checkpoint all roll back together. Retain existing immutable migration, verified-backup, and newer-schema refusal coverage if storage changes.

Run the deterministic work-counter benchmarks against v0.7.1 on the same host and report repeated timing ranges, B/op, allocs/op, source bytes read and hashed, parser records, historical event insert attempts, and checkpoint/dedup state size. Include Claude and Codex unchanged and one-record append workloads at 1,000 and 10,000 records, plus all fallback cases and bounded-state growth. Acceptance for supported one-record appends: zero historical decoding and zero historical insert attempts, with exact accounting unchanged. Report that bytes read/hashed may remain proportional to complete history; do not claim append-byte-only I/O or a timing improvement without measured results.

Before implementation review, run `go mod verify`, `go vet ./...`, `go test ./...`, `go test -race ./...`, `make check`, and the standalone CLI candidate-compatibility workflow. The replacement used by that workflow remains temporary and must never be committed.

## Decisions requested before implementation

1. Approve or revise the newline-terminated complete-record boundary rule, including always rereading an incomplete tail.
2. Approve the per-source SQLite checkpoint table and atomic cursor/checkpoint/event/reference transaction, or specify another durable store.
3. Confirm parser revision bump policy and the proposed state limits.
4. Confirm that exact receipt/outcome/health equivalence is a hard gate and any state that cannot meet it must use full replay.
5. Confirm the v0.7.1 published source is the independent differential baseline and accept the proposed test/performance evidence matrix.

No implementation should begin until these decisions are reviewed. Keep issue #139 open until the implementation and its correctness and performance evidence have been reviewed and merged. CLI measurement and delivery remain tracked separately under komposyn-lg #23/#20.
