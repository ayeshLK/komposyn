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
komposyn-lg watch add /path/to/codex /path/to/claude --data-dir /path/to/data
komposyn-lg watch start --data-dir /path/to/data
komposyn-lg watch status --data-dir /path/to/data
komposyn-lg settings --collector on --auto-start off
```

For temporary collection without persistence, use `watch foreground` with one
or more explicit paths. The
`TOKEN_LOGGER_CODEX_DIR` and `TOKEN_LOGGER_CLAUDE_DIR` variables configure only
the interactive auto-start collector; see [configuration](configuration.md).

The background runtime has no network listener. Coordination uses one SQLite
lease per data directory, a random instance identity, and a heartbeat. Stop is
cooperative and waits for an atomic import to commit or roll back. It never
signals a PID read from storage. A stale heartbeat remains visible and the
next start may replace it transactionally. Separate data directories have
independent collectors.

## Backups and recovery

Run `komposyn-lg watch stop` and stop foreground/interactive instances before
taking a file copy.
Back up the complete data directory, including `settings.json`, the database,
and any SQLite sidecars. See [Backups and recovery](backups.md) for a repeatable
procedure.

To recover, stop the application, replace the data directory with the backup,
and start the same or a compatible Komposyn version. Do not edit the database
or copy only the main `.db` file while a WAL database is active.

## Upgrades and uninstall

The initial upgrade operation is to rerun the latest standalone installer. It
stops a managed background collector before replacement and restarts it after
success only when it was previously active. Uninstall stops the managed
collector before removing the executable. Both preserve the database,
registered sources, runtime history, settings, diagnostics, and consent
choices. See [upgrades](upgrades.md) before changing versions.
