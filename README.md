# Komposyn Token Logger

Komposyn Token Logger is a local-first usage observability tool for AI coding
workflows. It reads provider-owned local transcripts, normalizes usage into a
small SQLite database, and produces usage and cost reports without collecting
prompts, responses, or provider credentials.

The standalone executable is `komposyn-lg`.

> **Release status:** Komposyn is currently pre-release; GitHub Releases does
> not yet contain a published signed release. Building from source is the
> supported way to try the current repository state.

## Why Komposyn

- **Local-first:** the database, settings, and diagnostics remain on the
  machine where the tool runs.
- **Provider-neutral:** Codex and Claude Code records are normalized into one
  event model.
- **Retry-safe:** stable ingestion keys make repeated imports idempotent.
- **Scriptable:** reports support table, JSON, and CSV output.
- **Inspectable:** the SQLite store, source provenance, and migration history
  are local and explicit.

Komposyn does not send provider transcripts, prompts, responses, or credentials
to a hosted Komposyn service. See [Privacy and data handling](docs/privacy.md)
for the complete data contract.

## Quick start

Build the CLI locally, import a transcript, and inspect a report:

```text
go build -o bin/komposyn-lg ./plugins/cli/token-logger
bin/komposyn-lg import path/to/transcript.jsonl --data-dir "$HOME/.local/share/komposyn"
bin/komposyn-lg usage --data-dir "$HOME/.local/share/komposyn" --format table
```

Release installation and platform-specific paths are documented in
[Installation](docs/installation.md).

## Installation and upgrades

Release archives are built with:

```text
make release VERSION=v0.1.0 OUTPUT_DIR=dist
```

The release output directory must be dedicated and empty (or not yet exist).
The release builder never deletes or overwrites caller-owned files.

For macOS and Linux, the standalone installer is `scripts/install.sh`; on
Windows, use `scripts/install.ps1`. The installer verifies checksums and
Cosign signatures before installing. Rerunning the latest installer replaces
only the executable. The SQLite database, configuration, and consent settings
remain in the existing data directory. The uninstall scripts remove the
executable but preserve local data. See [Upgrades and data preservation](docs/upgrades.md).

Release archives also contain
`THIRD_PARTY_LICENSES.md` and the corresponding license texts for resolved
dependencies.

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

For contributor setup, see [CONTRIBUTING.md](CONTRIBUTING.md). The full
documentation map is in [docs/README.md](docs/README.md).

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

## Documentation

| Goal | Guide |
| --- | --- |
| Install and run the first import | [Getting started](docs/getting-started.md) |
| Choose an installation method | [Installation](docs/installation.md) |
| Learn common workflows | [Usage guide](docs/usage.md) |
| Find every command and option | [CLI reference](docs/cli-reference.md) |
| Understand data and privacy | [Privacy and data handling](docs/privacy.md) |
| Integrate through JSONL | [Harness protocol](docs/HARNESS_PROTOCOL.md) |
| Contribute or extend the tool | [Contributing](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md) |
