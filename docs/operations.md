# Operations

Komposyn is a local application. Its operational state is the data directory;
provider transcript directories remain outside Komposyn ownership.

## Locate the data directory

The data directory is selected in this order:

1. An explicit command-line `--data-dir` or harness initialize `data_dir`.
2. `TOKEN_LOGGER_DATA_DIR`.
3. `$HOME/.komposyn/token-logger/data` (or the equivalent under the resolved
   Windows user profile).

`XDG_DATA_HOME` does not set the new default. When neither an explicit path nor
the environment variable is set, Komposyn discovers prior state in
`$XDG_DATA_HOME/token-logger` (or `$HOME/.local/share/token-logger` if XDG is
unset). It reuses a legacy directory only when it contains a Komposyn database,
SQLite sidecar, or `settings.json` and the new directory has no state. If both
have state, the new location wins, a warning is printed, and no merge occurs;
set `TOKEN_LOGGER_DATA_DIR` to the legacy directory to use it. The selected
path is absolute. If the home directory cannot be resolved, set
`TOKEN_LOGGER_DATA_DIR`; the current directory is never used as a fallback.

The primary files are `token-logger.db` and `settings.json`. SQLite may also
create `token-logger.db-wal` and `token-logger.db-shm` while the database is
active.

## Health and collection

Use a one-shot import when you need an immediate result. Use `watch` for
continuous polling; it performs an initial poll and then continues at the
configured interval. The collector reads source files and never modifies them.

```sh
komposyn-lg usage --format table
komposyn-lg watch /path/to/source --data-dir /path/to/data
komposyn-lg settings --collector on --auto-start off
```

For predictable collection scope, pass an explicit source path. The
`TOKEN_LOGGER_CODEX_DIR` and `TOKEN_LOGGER_CLAUDE_DIR` variables configure only
the interactive auto-start collector; see [configuration](configuration.md).

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
