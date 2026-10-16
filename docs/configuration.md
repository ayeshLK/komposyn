# Configuration and data locations

Komposyn keeps its database and settings in a local data directory. The
provider source directories are separate and remain provider-owned.

## Data directory selection

The data-directory selection order is:

1. A non-empty command-line `--data-dir` option or harness
   `initialize.params.data_dir` value.
2. A non-empty `TOKEN_LOGGER_DATA_DIR` environment variable.
3. `$HOME/.komposyn/token-logger/data` (on Windows, the equivalent path below
   the resolved user profile directory).

`XDG_DATA_HOME` does not change the new default. For compatibility, if no
explicit path or `TOKEN_LOGGER_DATA_DIR` is set, Komposyn checks the former
location (`$XDG_DATA_HOME/token-logger`, or `$HOME/.local/share/token-logger`
when XDG is unset). If that legacy directory contains a Komposyn database,
SQLite sidecar, or `settings.json` and the new directory does not, the legacy
directory continues to be used in place; data is not copied. If both locations
contain Komposyn state, the new directory wins and Komposyn warns that no merge
was performed. Set `TOKEN_LOGGER_DATA_DIR` to the legacy path to select it.
Other files do not trigger compatibility selection.

If the home directory cannot be resolved, Komposyn stops with an actionable
error; it does not fall back to the current working directory. Explicit and
environment paths bypass legacy discovery. All selected paths are resolved to
absolute paths.

The no-argument interactive mode has no `--data-dir` option; set
`TOKEN_LOGGER_DATA_DIR` to override it. `harness` accepts
`initialize.params.data_dir` in the request, or uses the environment/default
resolution when omitted.

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
enabled/auto-start choices, data directory, exact millisecond polling interval
(plus the legacy whole-second compatibility field), and diagnostic failure
mode. Defaults are a five-second poll interval and non-blocking diagnostic
handling. Settings writes are atomic.

## Collector source roots

Persistent background sources belong to the selected data directory:

```text
komposyn-lg watch add /path/to/codex /path/to/claude
komposyn-lg watch add --defaults
komposyn-lg watch list
```

They are stored transactionally in SQLite with stable IDs, canonical paths,
enabled state, and local health. Existing targets resolve symlinks for duplicate
detection. Paths are local administrative metadata shown by `watch list`; they
are never included in diagnostics or issue drafts. A background collector
reloads enabled roots before every poll.

The interactive auto-start collector reads, but never modifies, provider-owned
source files. Set its roots with:

```text
TOKEN_LOGGER_CODEX_DIR=/path/to/codex TOKEN_LOGGER_CLAUDE_DIR=/path/to/claude \
  komposyn-lg
```

These environment variables configure embedded interactive auto-start only;
they do not register background sources. Explicit foreground roots also do not
change the registry. Configured environment roots augment the
defaults below; they do not replace them. Environment roots are required and a
missing/inaccessible one degrades collector health. Built-in defaults are
optional and may be absent without creating a failure.

When interactive auto-start is enabled, default roots include:

- `$HOME/.codex/sessions`
- `$HOME/.claude/projects`

Use explicit `watch` paths when you need predictable scope:

```text
komposyn-lg watch /path/to/source --data-dir /path/to/data
```

`collector_enabled` and `auto_start` control embedded REPL collection only and
never launch a detached process. A healthy background lease suppresses
embedded startup for the same data directory.

Directory discovery accepts provider JSON and JSONL files according to the
adapter’s detection rules. Repeated, overlapping, relative/absolute, and
symlinked roots are canonicalized and each discovered source is processed at
most once per poll where the platform supports resolving the alias.

## Ownership and removal

Provider transcripts are not Komposyn-owned and are never deleted by Komposyn.
The uninstall scripts remove the executable but preserve the Komposyn data
directory. Delete the database and settings manually only after confirming that
you no longer need local usage history or diagnostics.
