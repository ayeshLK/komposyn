# Operations

Komposyn is a local application. Its operational state is the data directory;
provider transcript directories remain outside Komposyn ownership.

## Locate the data directory

The CLI uses this precedence:

1. `TOKEN_LOGGER_DATA_DIR`
2. `$XDG_DATA_HOME/token-logger`
3. `$HOME/.local/share/token-logger`
4. `./.token-logger` when the home directory cannot be resolved

The primary files are `token-logger.db` and `settings.json`. SQLite may also
create `token-logger.db-wal` and `token-logger.db-shm` while the database is
active.

## Health and collection

Use a one-shot import when you need an immediate result. Use `watch` for
continuous polling; it performs an initial poll and then continues at the
configured interval. The collector reads source files and never modifies them.

```sh
komposyn-lg usage --format table
komposyn-lg watch --data-dir /path/to/data /path/to/source
komposyn-lg settings --collector on --auto-start off
```

For predictable collection scope, pass explicit source paths or set
`TOKEN_LOGGER_CODEX_DIR` and `TOKEN_LOGGER_CLAUDE_DIR`. The default roots are
provider-specific directories under the home directory; see
[configuration](configuration.md).

## Backups and recovery

Stop `komposyn-lg`, including any `watch` process, before taking a file copy.
Back up the complete data directory, including `settings.json`, the database,
and any SQLite sidecars. See [Backups and recovery](backups.md) for a repeatable
procedure.

To recover, stop the application, replace the data directory with the backup,
and start the same or a compatible Komposyn version. Do not edit the database
or copy only the main `.db` file while a WAL database is active.

## Upgrades and uninstall

The initial upgrade operation is to rerun the latest standalone installer. It
replaces the executable and preserves the data directory. Uninstall removes the
executable only; it preserves the database, settings, diagnostics, and consent
choices. See [upgrades](upgrades.md) before changing versions.
