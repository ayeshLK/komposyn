# Configuration and data locations

Komposyn keeps its database and settings in a local data directory. The
provider source directories are separate and remain provider-owned.

## Data directory selection

The CLI uses this precedence order:

1. `TOKEN_LOGGER_DATA_DIR`
2. `XDG_DATA_HOME/token-logger` when `XDG_DATA_HOME` is set
3. `$HOME/.local/share/token-logger` on Unix-like systems
4. `./.token-logger` if the home directory cannot be resolved

The database and settings files are:

```text
token-logger.db
settings.json
```

SQLite may also create `token-logger.db-wal` and `token-logger.db-shm` while
the database is active. Stop Komposyn before taking a file-copy backup; see
[Backups and recovery](backups.md).

## Settings

Configure settings explicitly:

```text
komposyn-lg settings --data-dir /path/to/data --collector on --auto-start off
```

The settings file contains the schema version, onboarding state, collector
enabled/auto-start choices, data directory, polling interval, and diagnostic
failure mode. Defaults are a five-second poll interval and non-blocking
diagnostic handling. Settings writes are atomic.

## Collector source roots

The collector reads, but never modifies, provider-owned source files. Set
explicit roots with:

```text
TOKEN_LOGGER_CODEX_DIR=/path/to/codex TOKEN_LOGGER_CLAUDE_DIR=/path/to/claude \
  komposyn-lg watch /path/to/source --data-dir /path/to/data
```

When interactive auto-start is enabled, default roots include:

- `$HOME/.codex/sessions`
- `$HOME/.claude/projects`

Use explicit `watch` paths when you need predictable scope. Directory discovery
accepts provider JSON and JSONL files according to the adapter’s detection rules.

## Ownership and removal

Provider transcripts are not Komposyn-owned and are never deleted by Komposyn.
The uninstall scripts remove the executable but preserve the Komposyn data
directory. Delete the database and settings manually only after confirming that
you no longer need local usage history or diagnostics.
