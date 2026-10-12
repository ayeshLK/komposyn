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
komposyn-lg import <file-or-directory> [--data-dir <path>] [--format text|json]
```

Detects supported provider source shapes, imports normalized events, and
reports imported events, storage duplicates, source counts, and typed record
outcomes. `text` is the default. `json` emits the stable `format_version: 1`
contract and never includes source paths:

```json
{"format_version":1,"sources_discovered":1,"sources_processed":1,"imported_events":2,"duplicate_events":0,"outcomes":{"ignored_non_usage":3,"unchanged_snapshot":1,"deferred_incomplete_tail":0,"unsupported_usage":0,"malformed_record":0,"failed_source":0},"failed_sources":0,"complete":true,"pending":false}
```

`ignored_non_usage` and `unchanged_snapshot` are normal. A
`deferred_incomplete_tail` is retryable, sets `pending: true`, and is not a
warning. `unsupported_usage`, `malformed_record`, and `failed_source` mean
accounting may be incomplete. Imported events and storage duplicates are never
reported as record outcomes.

Exit status `0` means no warning outcome occurred (including when only a
retryable tail is pending), `1` is an argument error, `2` is a command setup or
output error, and `3` means import completed with warning outcomes or failed
sources.

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

`table` output is human-oriented and is also used by interactive reports.
Grouping dimensions are separate columns in requested order, time buckets are
shown once, and ungrouped output is labelled `TOTAL`. Tokens use decimal `K`,
`M`, `B`, and `T` abbreviations with at most one fractional digit; request and
session counts remain exact with digit grouping. Empty dimensions display as
`(none)`. Terminal controls are visibly escaped, and values longer than 32
Unicode code points are shortened with an ellipsis and distinguishing hash.

Cost status distinguishes calculated zero, fully unknown, partially known, and
mixed-currency groups. `pricing unavailable` neutrally indicates that the local
catalog could not calculate every request; the aggregate does not yet retain a
more specific cause, and unknown cost is never silently treated as zero. A
query with no matching events prints `NO USAGE DATA`. Use `json` or `csv` for
exact token integers, full dimension values, and stable machine-readable
fields. An untruncated wide table mode is not currently available.

### `watch`

```text
komposyn-lg watch <file-or-directory> [--interval <duration>] [--data-dir <path>]
```

Polls once immediately and continues until interrupted. The default interval is
five seconds. Poll output uses the same typed outcomes as `import`. Ignored and
unchanged records remain healthy; a deferred tail is visible as pending without
degrading health; unsupported, malformed, and failed-source outcomes degrade
health and create path-free, deduplicated local diagnostics.

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
