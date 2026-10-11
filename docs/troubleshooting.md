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

## Reports show unexpected hourly buckets

Hourly reports use each event's local civil hour and preserve its numeric UTC
offset. Fractional offsets and repeated daylight-saving hours are therefore
distinct. Confirm the source timestamp and offset before treating this as data
loss.

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
