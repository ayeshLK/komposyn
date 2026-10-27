# Database migrations

Komposyn upgrades its SQLite schema automatically when `Store.Open` is called.
Migrations are numbered, applied in order, and recorded in the
`schema_migrations` table. The current schema version is 18. Core consumers
can read `tokenlogger.CurrentSchemaVersion`; the oldest numbered schema with a
complete forward migration path is `tokenlogger.OldestMigratableSchemaVersion`
(1). The protocol-v1 `schema_versions` field describes event-format versions
and does not describe this SQLite migration range.

## Migration history

1. Creates the core projects, sessions, usage events, source cursors, and
   pricing rules tables, plus reporting indexes.
2. Adds the diagnostics table and status index.
3. Normalizes persisted timestamps to fixed-width UTC RFC3339 values while
   preserving their instants.
4. Adds `source_generation` to source cursors so replacement transcripts can
   reuse byte offsets without colliding with previously imported records.
5. Adds exact rational pricing rates and catalog source/identity metadata while
   preserving legacy `rate_micros` as microunits per token.
6. Adds local project locations, scoped project/session aliases, and
   reconciliation warnings. Existing project/session/event IDs remain valid;
   non-empty legacy roots seed locations, and compatibility aliases preserve
   old filters without destructively rewriting accounting history.
7. Normalizes reconciliation-warning project/session keys to non-null values
   and consolidates duplicate NULL-key warnings while preserving occurrence
   totals and activity bounds.
8. Reserves both synthetic Unattributed navigation ID strings across both the
   project and session namespaces. Any older persisted project or session using
   either reserved value is transactionally moved to a collision-free local ID,
   with all references updated.
9. Adds pending-tail and snapshot-reusability state to source cursors so an
   exactly unchanged full-digest snapshot can avoid historical replay without
   hiding retryable tails or incomplete accounting.
10. Adds a path-safe collector identity map from canonical source digests to
    established adapter source IDs, preserving idempotency when symlink or
    configured-root spellings are added, reordered, or removed.
11. Adds the persistent source registry, singleton runtime lease and heartbeat,
    cooperative stop request, cumulative typed outcomes, and collector-run
    history.
12. Adds optional normalized billing context to usage events and exact service-
    tier and region selectors to pricing rules. Existing events remain unknown
    for these fields and existing pricing rules retain their context-independent
    behavior.
13. Adds optional 5-minute and 1-hour cache-write token counts to usage events.
    Existing aggregate-only counts remain intact and cannot be split
    retroactively.
14. Adds exact speed selectors to pricing rules for provider billing modes
    whose rates vary by inference speed.
15. Adds diagnostic category and core/CLI/protocol/schema version metadata.
    Existing diagnostics use category `unknown` and empty/zero version fields.
16. Adds a private per-source parser checkpoint table for validated append
    continuation. It stores bounded normalized parser state, parser/format
    revisions, source generation, complete-record boundary, verified prefix
    fingerprint, and a state digest. It contains no provider source text.
17. Records only project-name provenance demonstrated by the current import
    path. Historical rows remain unknown.
18. Adds a private random diagnostic namespace and a separately bounded
    detailed-problem aggregate with exact decimal-text counters, per-operation
    replay protection, operational condition evidence, independent review
    revision, and at most four location samples carrying generation/coverage
    scope. Existing diagnostic rows are not rewritten or enriched.

## Compatibility rules

- Opening an older database applies each missing migration in sequence.
- Each migration is committed transactionally and is safe to observe only
  after its schema and migration record are both committed.
- An existing database is inspected read-only before WAL configuration or any
  migration DDL. Opening a database with a newer schema returns a typed
  `tokenlogger.UnsupportedSchemaError` matching `tokenlogger.ErrUnsupportedSchema`.
  The refusal does not alter the database, its timestamps, or WAL sidecars.
- A nonempty database without migration history is rejected as
  `tokenlogger.ErrInvalidSchema`; KompoSyn will not guess how to migrate an
  untracked schema. An empty database without migration history is treated as
  a new database.
- Komposyn does not downgrade or delete unknown schema.
- Migrations must preserve existing usage, configuration, consent, and
  diagnostic data. They must not modify provider-owned source files.

## Adding a migration

When a schema change is required:

1. Increment `CurrentSchemaVersion` in
   `plugins/library/token-logger/schema.go`.
2. Add one immutable migration statement and an ordered application block in
   `plugins/library/token-logger/migrations.go` and `storage.go`.
3. Use a transaction and record the new version only after the change succeeds.
4. Add tests for a fresh database, upgrade from the immediately previous
   version, rollback/failure behavior where relevant, and preservation of
   representative existing rows.
5. Update this history and run the public standalone CLI compatibility suite
   against the candidate core. The user-facing executable upgrade procedure
   is maintained in the standalone CLI's
   [installation guide](https://github.com/ayeshLK/komposyn-lg/blob/master/docs/installation.md).

Do not rewrite an already released migration. Later migrations should detect
state that may already exist when compatibility requires it, as migration 4
does for `source_generation`.
