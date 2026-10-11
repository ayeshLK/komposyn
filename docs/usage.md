# Usage guide

Commands that access stored data accept a data directory through `--data-dir`.
If it is omitted, Komposyn uses `TOKEN_LOGGER_DATA_DIR` when set and otherwise
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
`--group-by` values. Hourly grouping uses the event’s local civil hour and
retains its numeric offset, including fractional offsets and DST folds.

If pricing is unavailable, reports preserve the usage event and mark its cost
as unknown rather than inventing a price.

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
submit diagnostics.

## Harness mode

Use `komposyn-lg harness` for newline-delimited JSON integration. Keep stdout
reserved for protocol responses and read diagnostics from stderr. The wire
contract and metadata rules are defined in [HARNESS_PROTOCOL.md](HARNESS_PROTOCOL.md).
