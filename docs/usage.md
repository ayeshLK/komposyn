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

Commands that access stored data accept a data directory through `--data-dir`.
If it is omitted, KompoSyn uses `TOKEN_LOGGER_DATA_DIR` when set and otherwise
its platform default. The `harness` command selects its directory through
`initialize.params.data_dir` or the environment/default resolution.

## Import

Import one source file or a provider source directory:

```text
komposyn-lg import /path/to/source.jsonl --data-dir /path/to/data
komposyn-lg import /path/to/transcripts --data-dir /path/to/data
```

The importer detects Codex and Claude Code source shapes. Imports are retry-safe:
repeating an import deduplicates records using stable ingestion keys. Provider
files are read-only. When a source is replaced, the adapter creates a new
source generation so reused offsets do not collide with prior records.

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

If pricing is unavailable, reports preserve the usage event and mark its cost
as unknown rather than inventing a price.

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

Human cost output converts stored currency microunits to currency units. It
keeps known zero, fully unknown, partially known, and mixed-currency states
distinct. Missing local pricing is reported as `no matching price`; the tool
does not guess a rate. Use JSON or CSV for exact token integers, untruncated
dimension values, stable machine-readable fields, and detailed cost measures.

## Watch

`watch` performs an initial poll and then continues at the requested interval:

```text
komposyn-lg watch /path/to/transcripts --interval 10s --data-dir /path/to/data
```

Use Ctrl-C or SIGTERM to stop it. The collector records health and sanitized
diagnostics locally; it continues polling after an individual source failure.

## Settings and interactive mode

Configure collector preferences explicitly:

```text
komposyn-lg settings --data-dir /path/to/data --collector on --auto-start off
```

Running `komposyn-lg` without a subcommand opens interactive mode. The first
run asks whether to enable the local collector. Interactive reports support
`/projects`, `/sessions`, `/total-usage`, `/by-client`, `/by-model`, `/cost`,
`/range`, `/refresh`, `/settings`, `/report-issue`, and `/quit`.

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
