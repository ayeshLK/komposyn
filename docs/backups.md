# Backups and recovery

Back up the Komposyn data directory when usage history, settings, diagnostics,
registered collector sources, runtime history, or consent choices must be
recoverable. Provider transcript files are separate
provider-owned data and are not included in a Komposyn data-directory backup.

## Create a consistent backup

Before upgrading an existing database, `Store.Open` takes and verifies a
SQLite online backup while holding the migration writer lock. The default
backup is created beside `token-logger.db` with user-only permissions and a
unique name. Its `komposyn_migration_backup` table records source schema,
target schema, UTC creation time, and core version. The backup is checked with
SQLite `integrity_check` and its recorded schema and metadata are verified
before migrations begin. If creation or verification fails, the migration does
not run. New, in-memory, and already-current databases do not get a migration
backup.

Library callers can select a destination with
`tokenlogger.OpenWithOptions(path, tokenlogger.OpenOptions{BackupPath: path})`.
The destination must not already exist; it is created with user-only
permissions. The returned Store exposes `MigrationBackupPath()` when a backup
was created. Backups contain sensitive local usage data and must be protected
like the database.

1. Run `komposyn-lg watch stop` for the selected data directory, exit the REPL,
   and stop any foreground watcher using it.
2. Resolve the directory using [configuration](configuration.md). The
   `komposyn-lg settings` output shows the absolute `data_dir`; alternatively,
   set `TOKEN_LOGGER_DATA_DIR` explicitly for all commands and harness clients.
3. Copy the complete directory to protected backup storage, preserving file
   permissions. Include `token-logger.db`, `settings.json`, and any
   `token-logger.db-wal` or `token-logger.db-shm` files.
4. Record the Komposyn version and backup date alongside the backup.
5. Protect the backup as local usage data; it may contain diagnostics and
   project/session metadata.

The automatic pre-migration backup is specifically for recovery from a failed
schema upgrade. For general recovery, use SQLite's online backup tooling or a
filesystem snapshot instead of copying a live database. A plain file copy is
only safe after the application has stopped.

## Restore

1. Stop Komposyn.
2. Move the current data directory aside rather than deleting it.
3. Restore the complete backup directory at the configured data path.
4. Start Komposyn and run a read-only report such as
   `komposyn-lg usage --format json`.
5. Confirm settings and expected historical totals before resuming collection.

Keep the old directory until the restored database has been verified. A
pre-migration backup has an additional metadata table ignored by Komposyn; open
it with a compatible binary to apply its forward migrations. A database newer
than the installed binary's supported schema is refused before the application
changes the database or its WAL sidecars. Older published binaries predate this
refusal and may modify a newer database; use only a compatible binary for
rollback. Database restoration is separate from executable replacement.

## What is not backed up here

Komposyn does not copy, modify, or delete Codex or Claude transcript sources.
Back up those directories separately if they are part of the user's recovery
plan.
