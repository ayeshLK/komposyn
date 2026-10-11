# Backups and recovery

Back up the Komposyn data directory when usage history, settings, diagnostics,
or consent choices must be recoverable. Provider transcript files are separate
provider-owned data and are not included in a Komposyn data-directory backup.

## Create a consistent backup

1. Stop `komposyn-lg` and any `watch` process using the data directory.
2. Resolve the directory using [configuration](configuration.md), or set
   `TOKEN_LOGGER_DATA_DIR` explicitly.
3. Copy the complete directory to protected backup storage, preserving file
   permissions. Include `token-logger.db`, `settings.json`, and any
   `token-logger.db-wal` or `token-logger.db-shm` files.
4. Record the Komposyn version and backup date alongside the backup.
5. Protect the backup as local usage data; it may contain diagnostics and
   project/session metadata.

For an application-managed backup, use SQLite's online backup tooling or a
filesystem snapshot instead of copying a live database. A plain file copy is
only the documented procedure after the application has stopped.

## Restore

1. Stop Komposyn.
2. Move the current data directory aside rather than deleting it.
3. Restore the complete backup directory at the configured data path.
4. Start Komposyn and run a read-only report such as
   `komposyn-lg usage --format json`.
5. Confirm settings and expected historical totals before resuming collection.

Keep the old directory until the restored database has been verified. If the
restored database is older, opening it may apply numbered migrations; back up
again before that upgrade. A database newer than the installed binary's
supported schema is rejected and must be opened with a compatible newer binary.

## What is not backed up here

Komposyn does not copy, modify, or delete Codex or Claude transcript sources.
Back up those directories separately if they are part of the user's recovery
plan.
