# Komposyn Token Logger

The Komposyn Token Logger is a local-first usage observability plugin. Its
standalone executable will be distributed as `komposyn-lg`.

## Development

Requirements: Go 1.26 or newer.

```text
make fmt
make lint
make test
make build
```

The implementation is split into an independent library and CLI surface:

* `plugins/library/token-logger` — logger domain and integration library.
* `plugins/cli/token-logger` — the `komposyn-lg` executable.

The project is licensed under Apache-2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).

## Usage

Import a Codex JSONL source and inspect the normalized usage report:

```text
komposyn-lg import <file-or-directory> --data-dir <path>
komposyn-lg usage --data-dir <path> --format table
komposyn-lg cost --data-dir <path> --format json
komposyn-lg sessions --data-dir <path> --format csv
komposyn-lg watch <file-or-directory> --data-dir <path>
komposyn-lg settings --data-dir <path> --collector on
komposyn-lg version
komposyn-lg harness
```

Reports support `--from`, `--to`, `--project`, `--session`, `--client`,
`--provider`, `--model`, and comma-separated `--group-by` filters.

The `harness` mode runs the versioned JSON-lines sidecar protocol over stdin
and stdout. It supports `initialize`, `record`, `flush`, `health`, and
`shutdown`; stdout is reserved for protocol responses and diagnostics are
written to stderr. See [the harness protocol](docs/HARNESS_PROTOCOL.md) for
the wire contract.

## Upgrades

For the initial release, upgrade by rerunning the latest standalone installer.
The installer must reuse the existing data directory so the SQLite database,
configuration, and consent settings are preserved. In-CLI self-updating is
deferred.
