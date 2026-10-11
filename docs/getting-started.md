# Getting started

This guide takes a new user from an installed binary to a first usage report.
Komposyn stores data locally and does not require provider credentials for
importing or reporting.

## Prerequisites

- macOS, Linux, or Windows
- Go 1.26+ when building from source
- A Codex or Claude Code local transcript when using the import workflow

## Install from a release

Use the platform installer described in [Installation](installation.md). The
installer verifies the release checksum and Cosign signature before replacing
the executable.

## Build from source

```text
git clone https://github.com/ayeshLK/komposyn.git
cd komposyn
go build -o bin/komposyn-lg ./plugins/cli/token-logger
```

Confirm the binary and version:

```text
bin/komposyn-lg version
```

## Import a source

Choose a data directory for Komposyn’s SQLite database and settings:

```text
bin/komposyn-lg import /path/to/source.jsonl \
  --data-dir "$HOME/.local/share/komposyn"
```

The importer detects the Codex or Claude Code source shape, normalizes supported
usage records, skips unsupported records, and can be safely rerun.

## View a report

```text
bin/komposyn-lg usage \
  --data-dir "$HOME/.local/share/komposyn" \
  --format table
```

For automation, use `--format json` or `--format csv`. Add `--from` and `--to`
to limit the time range, or use `--project`, `--session`, `--client`,
`--provider`, and `--model` to narrow the report.

## Keep collecting new records

Run the watcher against a file or directory:

```text
bin/komposyn-lg watch /path/to/transcripts \
  --data-dir "$HOME/.local/share/komposyn" \
  --interval 5s
```

The watcher performs one immediate poll, then polls on the configured interval.
It never writes to provider-owned source files.

## Next steps

- Learn the workflows in the [Usage guide](usage.md).
- Review the [CLI reference](cli-reference.md).
- Understand the [privacy and data contract](privacy.md).
- For integration, read the [Harness protocol](HARNESS_PROTOCOL.md).
