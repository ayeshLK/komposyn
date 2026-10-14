# CLI reference

The executable is `komposyn-lg`. Run `komposyn-lg version` to print the
embedded release version.

## Data directory

Commands that access stored data and document the option below use
`--data-dir <path>`. `TOKEN_LOGGER_DATA_DIR` is the override for no-argument
interactive mode, which does not accept a command-line option. Without an
override, data is stored under `$HOME/.komposyn/token-logger/data`; legacy data
is discovered as described in [Configuration](configuration.md). The `version`
command does not use a data directory. Harness clients may select one through
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

For Codex, `token_usage_record` contributes one event per non-empty
`response_id` from its per-response `usage` object. The cumulative
`turn_token_usage` and `thread_token_usage` fields, and `total_tokens`, are
validation data only. Legacy cumulative `token_count` records are imported
until a valid response-record stream begins for that session; mirrored legacy
snapshots after that point are reported as `ignored_non_usage`. This precedence
rule keeps mixed Codex files from double-counting responses.

For these records, the envelope `timestamp` must be a valid RFC3339 value.
Session identity comes from the payload, or the active session context when
omitted, and must resolve to matching `session_meta` state. Project attribution
requires an explicit `project_id` in that session metadata; a source-scoped
placeholder project identifier is not accepted for these records. The model
from that metadata applies until a later `turn_context` changes the model for
subsequent responses. Missing or invalid timestamp, unresolved session,
missing project identifier, or unavailable model context is counted as a
malformed record; legacy `token_count` payloads alone do not supply metadata
for a response record or override matching session metadata, and the CLI does
not guess attribution from unrelated records.

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

Cost status distinguishes estimated, provider-reported, fully unknown,
partially known, and mixed-currency groups. JSON/CSV retain both reported and
estimated values, plus catalog provenance and an unknown reason where
applicable; unknown cost is never silently treated as zero. A
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
health and create path-free, deduplicated local diagnostics. Every poll is
printed, including changes discovered after watch starts, and the final line
includes cumulative outcome counts plus the latest pending state.

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

When both stdin and stdout are terminals, the prompt provides cursor-aware
editing with Left/Right, Backspace, Delete, Home/End, and Up/Down session
history. The history is in-memory only, includes non-empty submitted REPL
commands, excludes onboarding and diagnostic answers, and is discarded when
the process exits. Ctrl-D on an empty line exits. Ctrl-C cancels the current
input and exits. Ctrl-L has the same visible-screen clearing behavior as
`/clear`.

`/clear` clears the visible terminal and returns the cursor home without
rerunning the report. It does not erase scrollback and does not delete or
modify KompoSyn data, settings, consent, diagnostics, or session history. Use
`/refresh` to redraw the report. Multiline paste is folded into one command:
Unix-like terminals use bracketed paste. On Windows, press Ctrl+Enter to submit
a REPL command; plain Enter inserts a space, so pasted line breaks cannot
submit a command batch. Onboarding and diagnostic questions continue to accept
plain Enter for their displayed default. Pasted commands are not executed as a
batch.

Interactive reports start in rolling mode. Immediately before each report
render—including `/refresh`, `/projects`, `/sessions`, `/total-usage`,
`/by-client`, `/by-model`, and `/cost`—the CLI resolves the last seven UTC
calendar days and prints the exact half-open range used by that query. New
committed usage appears when the next report command is run; reports do not
redraw passively while the prompt is idle.

Use `/range` to show the mode and effective UTC bounds. `/range <from> <to>`
pins an explicit half-open range (accepting ISO-8601 dates or timestamps),
which remains unchanged across refreshes and report views. `/range reset`
returns to rolling mode and renders immediately. If `watch` and interactive
mode should share a database, pass `--data-dir` to `watch` and set
`TOKEN_LOGGER_DATA_DIR` for interactive mode to the same directory.

For non-TTY input or output, the CLI keeps scanner-style behavior. `/clear`
then prints `clear is available only in an interactive terminal` and emits no
ANSI or other terminal control sequence. No top-level `komposyn-lg clear`
command exists. Interactive commands are limited to 16,384 Unicode code
points (runes), which is at most 64 KiB of UTF-8. Commands made from shorter
UTF-8 characters therefore have a lower byte limit.

## Exit behavior

The CLI returns zero for successful commands and non-zero for invalid usage,
initialization failures, import/report failures, or protocol setup failures.
Human-readable diagnostics go to stderr; machine-readable report output goes
to stdout.
