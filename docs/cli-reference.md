# CLI reference

The executable is `komposyn-lg`. Run `komposyn-lg version` to print the CLI
version and the reusable core version embedded in this build.

## Data directory

Commands that access stored data and document the option below use
`--data-dir <path>`. `TOKEN_LOGGER_DATA_DIR` is the override for no-argument
interactive mode, which does not accept a command-line option. Without an
override, data is stored under `$HOME/.komposyn/token-logger/data`; legacy data
is discovered as described in [Configuration](configuration.md). The `version`,
`help`, and `completion` commands do not use a data directory. Help and
completion also work when the user's home directory cannot be resolved; they
do not open the database, run onboarding, or start collection. Harness clients
may select one through
`initialize.params.data_dir`.

## Commands

### `help`

```text
komposyn-lg --help
komposyn-lg -h
komposyn-lg help [command [subcommand]]
komposyn-lg <command> --help
komposyn-lg <command> <subcommand> --help
```

Help writes to stdout and exits 0. It works before onboarding and does not
create the data directory. Invalid help topics write to stderr and exit 1.
The top-level help includes a first-run import/report path, common data and
format options, exit-status meanings, and links to these details.

### `completion`

```text
komposyn-lg completion <bash|zsh|fish|powershell>
```

Prints deterministic completion source for public commands and options to
stdout. Generation is offline and has no data-directory side effects. See
[Installation](installation.md#shell-completion) for setup examples. Invalid
shell names or syntax exit 1; output failures exit 2.

### `version`

```text
komposyn-lg version
komposyn-lg --version
```

Prints `komposyn-lg <cli-version> (core <core-version>)`. Combined release
builds currently assign both fields the same release tag; they remain separate
so a standalone CLI can identify its pinned core version.

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
komposyn-lg cost [options]
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
fields.

When writing to a real terminal, `table` output adapts to the detected
terminal width without dropping any usage or cost information: wide
terminals use the complete column-aligned table; terminals too narrow for
that fall back to a compact layout with two lines per row (dimensions and
request/session counts, then the remaining metrics, cost, and status);
terminals too narrow even for that fall back to a vertical `LABEL: value`
layout, one field per line. Redirected output (piped or written to a file)
always uses the complete table layout regardless of the invoking shell's
width, so scripted or saved `table` output stays deterministic. Use `json`
or `csv` when you need a stable, unabridged machine-readable shape instead
of a layout that adapts to terminal width.

### Project and session resources

```text
komposyn-lg projects list [--from <time>] [--to <time>] [--format table|json|csv]
komposyn-lg projects show <project-id> [--from <time>] [--to <time>] [--format table|json]
komposyn-lg sessions list --project <project-id> [--from <time>] [--to <time>] [--format table|json|csv]
komposyn-lg sessions show <session-id> [--project <project-id>] [--group-by model] [--format table|json|csv]
```

These commands browse stable local resources. List JSON and CSV have
`format_version: 1`; selectors in non-interactive commands are full stable IDs,
never row numbers. Project output includes the readable name, canonical local
location when known, clients/models, activity bounds, and complete usage/cost
summary. Human-facing project/session totals and per-model token columns use
the same decimal `K`, `M`, `B`, and `T` abbreviations as usage reports. Rounded
cells may not visibly sum to another rounded cell; JSON and CSV retain exact
token integers. A missing path is displayed as `location unavailable`. Session
selection with `--project` verifies ownership, and `sessions show` includes
per-model usage. Its CSV output has one row per model/provider pair and includes
`format_version`, stable resource IDs, all token categories, calculated and
provider-reported cost fields, and pricing provenance. Events without a project
or session reference remain browsable under synthetic `Unattributed` resources;
their IDs are stable CLI identifiers, not rows persisted in the database.
Because the synthetic session is scoped to its selected project, pass
`--project <project-id>` when showing it non-interactively. The
original `komposyn-lg sessions [report options]` command is retained as a
compatibility report alias and keeps its existing output shape.

### `watch`

```text
komposyn-lg watch add <source> [<source>...] [--defaults] [--data-dir <path>]
komposyn-lg watch remove <source-or-id> [<source-or-id>...] [--data-dir <path>]
komposyn-lg watch list [--format text|json] [--data-dir <path>]
komposyn-lg watch start [--interval <duration>] [--data-dir <path>]
komposyn-lg watch status [--format text|json] [--data-dir <path>]
komposyn-lg watch stop [--timeout <duration>] [--data-dir <path>]
komposyn-lg watch <source> [<source>...] [--interval <duration>] [--data-dir <path>]
komposyn-lg watch foreground [<source>...] [--interval <duration>] [--data-dir <path>]
```

`add` validates ordinary paths, canonicalizes existing targets, and is
idempotent across relative, absolute, and symlink forms. `--defaults` permits
the default Codex and Claude roots to be registered before creation. `remove`
disables a source without deleting provider data or history. `list` includes
stable ID, explicit local path, enabled state, and latest health; JSON uses
`format_version: 1`.

`start` launches one detached process for the resolved data directory and
returns after readiness. It requires an enabled source; repeated starts return
the existing instance. `status` reports mode/state, instance identity, PID,
version, interval, timestamps, source counts, cumulative imports/duplicates,
typed outcomes, pending state, and sanitized error. JSON uses
`format_version: 1`. `stop` is cooperative, idempotent, and waits ten seconds
by default. Timeout exits 3 and does not signal the stored PID. A stale lease
can be replaced transactionally by the next start.

Foreground forms poll once immediately and continue until interrupted.
Explicit roots are ephemeral; no-root `foreground` uses the enabled registry.
Foreground, background, and embedded REPL collectors share the same singleton
lease for a data directory. Foreground collection appears in `watch status`,
can be stopped cooperatively from another terminal, and refuses to start while
another collector owns the lease.
Lifecycle words are reserved, so use `watch foreground add` to watch a literal
path named `add`. The default interval is
five seconds. Poll output uses the same typed outcomes as `import`. Ignored and
unchanged records remain healthy; a deferred tail is visible as pending without
degrading health; unsupported, malformed, and failed-source outcomes degrade
health and create path-free, deduplicated local diagnostics. The positional
root is required: a missing or inaccessible root produces a visible degraded
poll while `watch` keeps polling so later creation can recover. Empty existing
directories are healthy and have `sources=0`.

Every poll prints its source and discovery-failure counts, including changes
discovered after watch starts. The final `run_complete` field describes the
whole foreground run: it remains false if any poll had an incomplete outcome,
even if a later poll recovered. `latest_status` separately describes the most
recent completed poll, while `lifecycle` reports whether the worker is running
or stopped. Ctrl-C and SIGTERM are normal operator stops and return exit status
zero; there is currently no strict non-zero-on-degraded watch mode.

The internal `watch serve` child mode is not a user command. It opens no
network listener or remote-control endpoint.

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
Each launch states whether the session-owned collector is starting or off and
why. A starting collector means the initial scan is in progress; it does not
delay the first database report. At command boundaries, a changed collector
status line shows lifecycle, latest health, source/discovery counts, last
completed and successful scan times, pending tails, and sanitized failure
categories. `/settings` labels persisted preferences separately from this
runtime state.

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
calendar days and prints the exact half-open range and `queried_at` time used by
that database query. Source scanning is separate. New committed usage appears
when the next report command is run; reports do not redraw passively while the
prompt is idle. `/refresh` remains database redraw-only and neither starts nor
waits for a source scan.

Interactive row numbers select from the exact rows printed by the most recent
render of that view. A refresh or another report command replaces that
selection snapshot. Stable IDs can still be supplied directly for project and
session selection. Project, session, and model navigation tables use compact
decimal token counts consistently; use the corresponding JSON or CSV resource
commands when exact integers are required.

Use `/range` to show the mode and effective UTC bounds. `/range <from> <to>`
pins an explicit half-open range (accepting ISO-8601 dates or timestamps),
which remains unchanged across refreshes and report views. `/range reset`
returns to rolling mode and renders immediately. If `watch` and interactive
mode should share a database, pass `--data-dir` to `watch` and set
`TOKEN_LOGGER_DATA_DIR` for interactive mode to the same directory.
For explicit session collection controls today, run or stop foreground
`komposyn-lg watch` in another terminal, or register roots with `watch add`
and manage the detached collector with `watch start`, `watch status`, and
`watch stop`.

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
