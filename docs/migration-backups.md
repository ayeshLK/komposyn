# Migration backups and recovery

The core library creates a verified SQLite online backup before migrating an
existing database. The backup is made while holding the migration writer lock,
before migration DDL begins. New, in-memory, and already-current databases do
not need a migration backup.

## Backup contract

By default, the backup is created beside the database with user-only
permissions and a unique name. Its `komposyn_migration_backup` table records
the source and target schema versions, UTC creation time, and core version.
Core checks the backup with SQLite `integrity_check` and verifies its recorded
schema and metadata before proceeding. If backup creation or verification
fails, migration does not run. A later migration failure rolls back the
database transaction and leaves the original database and verified backup
usable.

Library consumers can select a destination with
`tokenlogger.OpenWithOptions(path, tokenlogger.OpenOptions{BackupPath: backupPath})`.
The destination must not already exist; core creates it with user-only
permissions. `Store.MigrationBackupPath()` returns the path when a migration
backup was created, and is empty when no backup was needed.

Migration backups contain sensitive usage and provenance data. Protect them
with the same access controls as the database. They are intended to recover
from failed or unwanted schema upgrades; they are not a substitute for a
separate application-level backup strategy.

## Recovery

Open the verified pre-migration snapshot with a core version that supports its
schema. The snapshot includes a metadata table ignored by the normal storage
schema. Opening it may apply forward migrations and create a new migration
backup, so preserve the original snapshot before recovery. Core never performs
automatic down-migrations or deletes unknown schema.

An older core refuses a database whose schema is newer than it supports before
changing the database or its WAL sidecars. Older releases predating that guard
may be unsafe to use with a newer database. Database recovery and replacement
of the calling executable are separate operations. Executable installation,
data-directory selection, and whole-directory recovery instructions belong to
the [standalone CLI documentation](https://github.com/ayeshLK/komposyn-lg/tree/master/docs).

For schema history, transaction behavior, and migration development, see
[database migrations](migrations.md).
