# Troubleshooting

Start with the data directory and command output. Komposyn keeps diagnostics
local and sanitizes them before displaying or using them in an issue draft.
Do not paste provider transcripts, prompts, responses, credentials, or raw
diagnostics into an issue.

## The CLI cannot be found

Confirm the installer directory is on `PATH`, or invoke the binary by its full
path. A successful installation places `komposyn-lg` in `~/.local/bin` by
default on Unix-like systems. On Windows, use the installed `.exe` path.

```sh
komposyn-lg version
```

## The application opened an unexpected data directory

Run `komposyn-lg settings` to see the absolute directory used by the CLI. Stored
data defaults to `$HOME/.komposyn/token-logger/data`; an existing legacy
directory (`$XDG_DATA_HOME/token-logger`, or `$HOME/.local/share/token-logger`)
is reused only when it contains Komposyn state and the new location does not.
If both locations contain state, the new location wins and a warning explains
how to select the legacy location. No automatic merge or copy occurs. Set
`TOKEN_LOGGER_DATA_DIR` to select a directory explicitly. If the home directory
cannot be resolved, set this variable; Komposyn does not use the current
working directory as a fallback.

## No events are imported

Check that the source path exists, has readable JSON/JSONL files, and belongs
to a supported adapter. Use an explicit path to remove default-root ambiguity:

```sh
komposyn-lg import /path/to/source --data-dir /path/to/data
komposyn-lg usage --data-dir /path/to/data --format json
```

The collector skips missing built-in default roots. A missing explicit `watch`
root or environment-configured root is required and therefore reports
`health=degraded`, `discovery_failures`, and `failed_source`; collection keeps
polling so creation or permission recovery can be observed. `sources=0` with
healthy status means all configured roots were optional/missing or existing
directories were empty. It never edits source files. Check configured roots
and file permissions before changing settings.

## A repeated import reports duplicates

This is expected when records from a changed source have already been
committed. An exactly unchanged, safely reusable snapshot reports
`unchanged_snapshot` without resubmitting its historical events. Stable
ingestion keys remain the accounting backstop. A changed or replaced source
may receive a new source generation so reused offsets do not collide with older
events.

## Interactive totals look stale

Read the `collector runtime` line at the next command prompt or run
`/settings`. `state` is worker lifecycle; `health` describes the latest poll.
Compare `last_completed` and `last_success`, then inspect `sources`,
`discovery_failures`, `pending`, and the typed failure counts. A degraded poll
does not erase the last successful time. Every report prints `queried_at`; that
is the SQLite query time, not proof that provider sources were just scanned.
`/refresh` redraws committed database data only. Use foreground `watch` when an
explicit source scan is needed, and ensure it uses the same data directory.

## Background collector is stopped or stale

Run `komposyn-lg watch status --format json` with the same data directory used
at start. `stale` means an active lease stopped heartbeating; no process is
signalled because the stored PID may have been reused. Review the sanitized
error and registered sources, then run `watch start` to recover the lease
transactionally. `watch stop` waits for the current atomic import. If it times
out, retry later; do not kill the recorded PID solely from status output.

If the REPL says a background collector is active, it is intentionally reading
persisted status instead of starting a duplicate embedded collector. REPL exit
does not stop the independently started process.

## Import reports pending or incomplete accounting

`deferred_incomplete_tail=1` means the source ended with a partial JSON record,
usually because the provider was still writing. This is healthy and retryable:
rerun the import after the source grows, or let `watch` retry it. Do not edit the
provider-owned file.

`unsupported_usage`, `malformed_record`, or `failed_source` means accounting
may be incomplete and causes explicit import to exit with status 3. Unsupported
usage indicates a usage-bearing provider shape that this version deliberately
does not normalize; upgrade before retrying. Malformed records have invalid
JSON or required attribution such as model or timestamp. Failed sources indicate
an access, detection, or persistence failure. Use `--format json` to inspect
typed counts without exposing source paths, and review the locally sanitized
diagnostics before reporting an issue.

If a Codex import reports `unsupported_usage` around a `token_usage_record`,
inspect the sanitized outcome rather than manually adding the cumulative
fields. KompoSyn imports only the per-response `usage`; turn- and
thread-cumulative values are validation metadata. A source that contains both
new response records and legacy `token_count` snapshots will show those
mirrored snapshots as ignored non-usage records. This is expected and prevents
double-counting. Unsupported results can also indicate a cumulative rollback,
conflicting cache-write spellings, unknown usage fields, or ambiguous
accounting; upgrade the importer after the provider format is understood.

The importer intentionally does not require a Codex client-version marker.
Support is selected from the record shape and validated against the pinned
protocol provenance documented in the [usage guide](usage.md). This keeps
forward-compatible records visible as typed unsupported or malformed outcomes
instead of silently assigning behavior based on an unreliable version guess.

## Reports show unexpected hourly buckets

Hourly reports use the selected reporting timezone and default to UTC. Pass
`--timezone <IANA zone>` for a different location; fractional offsets and
repeated daylight-saving hours are then represented in that selected zone.
Confirm the effective timezone in the report before treating a bucket
difference as data loss.

## Upgrade or migration fails

Do not delete or edit the database. Restore a backup if necessary and confirm
the installed binary is compatible with the database schema. Komposyn applies
numbered migrations on open and explicitly rejects a database newer than the
binary supports. See [database migrations](migrations.md) and
[upgrades](upgrades.md).

## Installer verification fails

Ensure `cosign` is installed and that the selected release directory contains
the archive, `SHA256SUMS`, and matching `.sig` and `.pem` files. Do not bypass
checksum or certificate verification. Retry with a fresh release directory and
confirm the release version and platform architecture match.

## Reporting an issue

Include the Komposyn version, operating system, command shape, sanitized error,
and reproducible steps. Redact paths or project names when they reveal private
information. Review any generated issue draft before sending it; external
submission is never required for local operation.
