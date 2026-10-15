# Database migrations

Komposyn upgrades its SQLite schema automatically when `Store.Open` is called.
Migrations are numbered, applied in order, and recorded in the
`schema_migrations` table. The current schema version is 10.

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

## Compatibility rules

- Opening an older database applies each missing migration in sequence.
- Each migration is committed transactionally and is safe to observe only
  after its schema and migration record are both committed.
- Opening a database with a schema version newer than the binary supports
  fails explicitly. Komposyn does not downgrade or delete unknown schema.
- Migrations must preserve existing usage, configuration, consent, and
  diagnostic data. They must not modify provider-owned source files.

## Adding a migration

When a schema change is required:

1. Increment `currentSchemaVersion` in
   `plugins/library/token-logger/tokenlogger.go`.
2. Add one immutable migration statement and an ordered application block in
   `plugins/library/token-logger/migrations.go` and `storage.go`.
3. Use a transaction and record the new version only after the change succeeds.
4. Add tests for a fresh database, upgrade from the immediately previous
   version, rollback/failure behavior where relevant, and preservation of
   representative existing rows.
5. Update this history and the upgrade guidance in [Upgrades and data
   preservation](upgrades.md).

Do not rewrite an already released migration. Later migrations should detect
state that may already exist when compatibility requires it, as migration 4
does for `source_generation`.
