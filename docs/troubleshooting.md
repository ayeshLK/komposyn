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

## No events are imported

Check that the source path exists, has readable JSON/JSONL files, and belongs
to a supported adapter. Use an explicit path to remove default-root ambiguity:

```sh
komposyn-lg import /path/to/source --data-dir /path/to/data
komposyn-lg usage --data-dir /path/to/data --format json
```

The collector skips missing default roots and records operational failures in
local diagnostics. It never edits source files. Check the configured roots and
file permissions before changing settings.

## A repeated import reports duplicates

This is expected when the same source records have already been committed.
Stable ingestion keys make imports idempotent. A changed or replaced source
may receive a new source generation so reused offsets do not collide with older
events.

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
