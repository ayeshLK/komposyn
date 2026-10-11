# CLI reference

The executable is `komposyn-lg`. Run `komposyn-lg version` to print the
embedded release version.

## Data directory

Commands that access stored data use `--data-dir <path>`. The CLI also honors
`TOKEN_LOGGER_DATA_DIR` when no flag is supplied. The `version` and `harness`
commands do not accept this option; harness clients select a directory through
`initialize.params.data_dir`.

## Commands

### `version`

```text
komposyn-lg version
komposyn-lg --version
```

Prints `komposyn-lg <version>`.

### `import`

```text
komposyn-lg import <file-or-directory> [--data-dir <path>]
```

Detects supported provider source shapes, imports normalized events, and
reports imported, duplicate, skipped, and failed counts.

### `usage`, `cost`, and `sessions`

```text
komposyn-lg usage|cost|sessions [options]
```

Supported options include:

```text
--data-dir <path>
--format table|json|csv
--from <date-or-RFC3339-time>
--to <date-or-RFC3339-time>
--project <id>
--session <id>
--client <name>
--provider <name>
--model <name>
--group-by <comma-separated-dimensions>
--timezone <IANA time zone>
```

`--timezone` selects the reporting location for time-based groups such as
`hour`. It accepts an IANA time zone name such as `Asia/Kathmandu` or
`America/New_York`; omitted, reports use UTC. The selected timezone is included
in JSON output as `timezone`.

### `watch`

```text
komposyn-lg watch <file-or-directory> [--interval <duration>] [--data-dir <path>]
```

Polls once immediately and continues until interrupted. The default interval is
five seconds.

### `settings`

```text
komposyn-lg settings [--data-dir <path>] [--collector on|off] [--auto-start on|off]
```

Writes local collector preferences. Settings writes replace the file atomically
and protect it with user-only permissions where the platform supports them.

### `report-issue`

```text
komposyn-lg report-issue --output <path> [--data-dir <path>]
```

Exports pending, locally sanitized diagnostics to a ZIP archive without
overwriting an existing output or following an output symlink. On Unix-like
systems the archive is created with mode `0600`; on Windows it inherits the ACL
of the destination directory, so choose a directory with appropriate access
controls. The archive contains `diagnostics.json`; it does not include provider
transcripts, prompts, responses, credentials, or source files. This command
performs no network request and does not mark diagnostics as reviewed.

### `harness`

```text
komposyn-lg harness
```

Runs the versioned JSONL sidecar protocol over stdin/stdout. See the
[harness protocol](HARNESS_PROTOCOL.md).

## Interactive mode

Running `komposyn-lg` without a subcommand starts onboarding on first use and
then opens the interactive report view. Type `/help` for the command list.

## Exit behavior

The CLI returns zero for successful commands and non-zero for invalid usage,
initialization failures, import/report failures, or protocol setup failures.
Human-readable diagnostics go to stderr; machine-readable report output goes
to stdout.
