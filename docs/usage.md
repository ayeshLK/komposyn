# Usage guide

KompoSyn currently ships the Token Logger component as a standalone tool. Use
`komposyn-lg` with existing agent tools and provider-owned transcript files;
the broader KompoSyn harness is a future direction, not a prerequisite for
these workflows.

The supported flow is:

1. Build or install `komposyn-lg`.
2. Import or watch supported local transcript sources.
3. Inspect normalized usage, session, and cost reports.

Token Logger does not replace or modify provider-owned source files, and it
does not collect prompts, responses, or provider credentials by default.

Stored-data commands accept `--data-dir`. The no-argument interactive mode
uses `TOKEN_LOGGER_DATA_DIR` when set, otherwise
`$HOME/.komposyn/token-logger/data` (or the equivalent under the Windows user
profile). Legacy data discovery and conflict behavior are documented in
[Configuration](configuration.md). The harness accepts
`initialize.params.data_dir`, then `TOKEN_LOGGER_DATA_DIR`, then the same
default and compatibility discovery.

## Import

Import one source file or a provider source directory:

```text
komposyn-lg import /path/to/source.jsonl --data-dir /path/to/data
komposyn-lg import /path/to/transcripts --data-dir /path/to/data
komposyn-lg import /path/to/transcripts --format json --data-dir /path/to/data
```

The importer detects Codex and Claude Code source shapes. Imports are retry-safe:
repeating an import deduplicates records using stable ingestion keys. Provider
files are read-only. When a source is replaced, the adapter creates a new
source generation so reused offsets do not collide with prior records.

Import results do not use a catch-all "skipped" count. They distinguish normal
non-usage records, unchanged cumulative snapshots, a retryable incomplete final
line, unsupported usage-bearing records, malformed records, and failed
sources. Unsupported, malformed, or failed-source outcomes exit with status 3
because usage accounting may be incomplete. An incomplete final line is common
while an agent is writing; it is reported as pending, exits successfully, and
is retried when the source grows. Use `--format json` for the versioned,
path-free machine contract described in the [CLI reference](cli-reference.md).

### Codex usage records

Recent Codex CLI sources can contain `token_usage_record` envelopes. KompoSyn
uses the record's per-response `usage` object as the canonical event and maps
its input, cached input, cache-write input, output, and reasoning fields once.
The cumulative `turn_token_usage` and `thread_token_usage` objects are used
only to validate ordering, resets, and reconciliation; they never create
additional events. `total_tokens` is also a consistency check, not another
token category.

Attribution comes from the Codex record context: the event time is the
`token_usage_record` envelope's `timestamp`, which must parse as a supported
RFC3339 timestamp. Session identity is read from the record payload and must
resolve to the matching `session_meta` state (or the active session context
when the payload omits its session ID). Project attribution requires an
explicit `project_id` in that matching session metadata; a source-scoped
placeholder used for other Codex record shapes is not accepted for these usage
records. The session's initial model comes from the metadata. Later
`turn_context` records update the applicable session model, so responses after
a model change use the new model. The adapter does not infer a model from usage
values or unrelated records. Legacy `token_count` payloads alone do not supply
metadata for a `token_usage_record`; when matching session metadata exists,
legacy snapshots also cannot replace its project or model attribution for
response records.

Codex may also emit legacy `event_msg`/`token_count` snapshots for the same
responses. Before a usage-record stream starts, legacy snapshots continue to
be imported as cumulative deltas. Once a valid response record is observed for
a session, later mirrored legacy snapshots are deliberately ignored so the
same usage cannot be counted twice. Ambiguous or regressing cumulative state
is reported as unsupported rather than estimated heuristically.

Response identity is based on the adapter, session/thread scope, and
`response_id`, so retries, appends, and source rewrites remain idempotent even
when file offsets or source generations change. A missing response ID, invalid
timestamp, unknown session, unavailable model context, or missing explicit
project metadata is reported as malformed and is not imported. The adapter
does not recover these values by guessing from unrelated records or by using
the source-scoped placeholder project identifier.

Support provenance: the `token_usage_record` decoder is validated against the
Codex protocol definitions at the pinned upstream revision
[`a9b0815`](https://github.com/openai/codex/tree/a9b0815167c27ab13719dd12d13b63e02b70d221)
and is capability-based rather than gated on a Codex version string. The
repository does not currently claim that this shape maps to one specific
released Codex CLI version; provider files do not carry reliable client-version
metadata for that purpose.

Claude Code can express per-message usage, but its transcript representation
does not expose the Codex response/thread cumulative fields or the same
legacy/new mirrored stream. Therefore the shared harness comparison uses
provider-appropriate fixtures and normalized event projections; it does not
pretend that the two raw formats are interchangeable.

## Reports

Usage, cost, and session reports share the same filtering model:

```text
komposyn-lg usage --data-dir /path/to/data --format table
komposyn-lg cost --data-dir /path/to/data --format json --provider openai
komposyn-lg sessions --data-dir /path/to/data --format csv --project project-id
```

Common filters are `--from`, `--to`, `--project`, `--session`, `--client`,
`--provider`, and `--model`. Grouping is controlled with comma-separated
`--group-by` values. Use `--timezone <IANA time zone>` to choose the reporting
location; it defaults to UTC. Hourly grouping uses that location's civil hour
and retains its numeric offset, including fractional offsets and DST folds.

Cost reports distinguish provider-reported amounts from public-list-price
estimates. Estimates are not invoice amounts and do not account for
subscriptions, negotiated terms, credits, tax, cloud resellers, or unobserved
billing modifiers. Both reported and estimated amounts are retained when both
exist. The bundled catalog is loaded locally; normal import and reporting make
no pricing network requests.

The initial reviewed catalog currently covers the exact `gpt-5-codex` API model.
Claude events remain unknown until the adapter can establish billing context.
Unsupported models, missing categories (including cache-write duration where
required), and events outside a known effective interval remain unknown. JSON/CSV include unknown
reason and per-reason counts, catalog version/source, matched model
selector/effective interval, and separate reported and estimated values.
Catalog entries and review dates are maintained in
`plugins/library/token-logger/pricing-catalog.json`; update them only in a
reviewed repository change against the linked official provider documentation.

Table and interactive reports are optimized for terminal scanning. Selected
grouping dimensions appear as separate columns in the order supplied to
`--group-by`; a time group such as `hour` appears once and retains its numeric
UTC offset. Ungrouped output uses a labelled `TOTAL` row, and an empty selected
dimension is shown as `(none)`.

Token totals below 1,000 remain exact. Larger totals use decimal `K`, `M`, `B`,
or `T` suffixes with at most one fractional digit; rounding carries into the
next suffix. Request and session counts remain exact and use comma separators.
Dimension values are terminal-safe and bounded to 32 Unicode code points;
truncated values include a visible marker and short distinguishing hash. A
wide or untruncated table mode is not currently provided.

Human cost output labels public list-price values as estimates and displays
provider-reported amounts separately. It keeps known zero, fully unknown,
partially known, and mixed-currency states distinct. Machine-readable output
also identifies the unknown reason and matched catalog version/source when
available. Use JSON or CSV for exact token integers, untruncated dimension
values, stable machine-readable fields, and detailed cost measures. A report
with no matching events displays `NO USAGE DATA` instead of fabricating an
empty group.

## Watch

`watch` performs an initial poll and then continues at the requested interval:

```text
komposyn-lg watch /path/to/transcripts --interval 10s --data-dir /path/to/data
```

Use Ctrl-C or SIGTERM to stop it. The collector records health and sanitized
diagnostics locally; it continues polling after an individual source failure.
Normal ignores and unchanged snapshots do not degrade health. A final partial
record is visible as pending but remains healthy. Unsupported usage, malformed
records, and failed sources degrade health; repeated polls deduplicate their
path-free diagnostics. Each completed poll is printed, and the stop summary
retains cumulative warning counts and the latest pending state.

## Settings and interactive mode

Configure collector preferences explicitly:

```text
komposyn-lg settings --data-dir /path/to/data --collector on --auto-start off
```

Running `komposyn-lg` without a subcommand opens interactive mode. The first
run asks whether to enable the local collector. In a terminal, the prompt
supports Left/Right, Backspace, Delete, Home/End, and Up/Down session history.
History is held in memory for this invocation only: it contains non-empty REPL
commands, excludes onboarding and diagnostic answers, and is never written to
disk. Ctrl-D on an empty prompt exits. Ctrl-C cancels the current input and
exits interactive mode cleanly. Ctrl-L clears the visible terminal and keeps
the prompt; it does not erase scrollback or application data. Commands are
limited to 16,384 Unicode code points (runes), or at most 64 KiB of UTF-8;
commands made from shorter UTF-8 characters have a lower byte limit.

Interactive navigation follows `Projects → Project → Sessions → Session →
Models → Usage`:

```text
/projects
/project <row-number|unambiguous-name|stable-id>
/sessions
/session <row-number|unambiguous-id-prefix|stable-id>
/models
/model <row-number|unambiguous-name>
/back
/home
```

Row numbers apply only to the currently displayed result. Ambiguous names and
prefixes are rejected, and a session selection is verified against the active
project. The prompt/report breadcrumb shows the current scope. `/back` removes
only the deepest selection; `/home` clears all navigation selections. Neither
changes the rolling or pinned range. Models are computed from session events
using resolved model with the existing requested-model fallback.

Interactive reports also support `/total-usage`, `/by-client`, `/by-model`,
`/cost`, `/range`, `/refresh`, `/clear`, `/settings`, `/report-issue`, and
`/quit`. `/clear` clears the visible
terminal, returns the cursor home, and does not rerun the report; use
`/refresh` when a redraw is wanted. It never deletes usage, settings, consent,
diagnostics, or history. Multiline paste is folded into one command and is not
executed as a batch. Unix-like terminals use bracketed paste. On Windows,
Ctrl+Enter submits a REPL command and plain Enter inserts a space, so pasted
line breaks cannot submit a command batch. Onboarding and diagnostic prompts
continue to accept plain Enter for the displayed default.

Interactive reports use a rolling last-seven-calendar-days range in UTC by
default. The range is recalculated immediately before each report render, and
the output shows the exact half-open bounds used for the query. `/refresh` and
each report-view command therefore include newly committed usage on the next
command-driven render; the terminal does not update passively while idle.
`/range` displays whether the mode is `rolling` or `pinned` and its effective
UTC bounds. `/range <from> <to>` pins an explicit half-open range until
`/range reset` returns to rolling mode and renders immediately. Dates and
ISO-8601 timestamps are accepted. When `watch` and the interactive CLI use a
data-directory override (`--data-dir` or `TOKEN_LOGGER_DATA_DIR`), both must
resolve to the same directory to share the database; the CLI does not search
other data directories.

When stdin or stdout is not a terminal, the CLI uses deterministic scanner
input for pipes, redirects, and tests. In that mode `/clear` prints
`clear is available only in an interactive terminal` and emits no terminal
control sequences.

Diagnostics are sanitized and remain pending until explicitly reviewed. The
tool only creates an issue draft after the user asks; it does not silently
submit diagnostics. To export pending diagnostics for local review or support,
use:

```text
komposyn-lg report-issue --output /path/to/report.zip --data-dir /path/to/data
```

The ZIP is local-only and contains sanitized diagnostic JSON. Existing output
files are never overwritten; on Windows, create it in a directory whose ACLs
restrict access to the intended users.

## Harness mode

Use `komposyn-lg harness` for newline-delimited JSON integration. Keep stdout
reserved for protocol responses and read diagnostics from stderr. The wire
contract and metadata rules are defined in
[HARNESS_PROTOCOL.md](HARNESS_PROTOCOL.md). This is the Token Logger
component's current sidecar integration surface; it is not a general
KompoSyn harness or plugin protocol.
