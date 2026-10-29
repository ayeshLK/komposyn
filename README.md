# KompoSyn

KompoSyn is an open-source project exploring how independent, useful
components can grow into a modular, composable harness for AI agent workflows.
We are taking a component-first, or “plugin-first,” approach: solve real
problems with components that work on their own, learn from their use, and
compose them when the shared architecture is clear.

The general KompoSyn harness, plugin runtime, and universal plugin API remain
future work. Today, KompoSyn delivers the independently useful Token Logger
component and an experimental temporary text chat that demonstrates bundled
provider and UI boundaries. The `komposyn` chat CLI is installable from its
signed release once that release is published; it does not establish a general
plugin platform.

> **Build independently. Compose seamlessly. Evolve openly.**

## The first component: Token Logger

Token Logger gives developers a local view of token usage across supported AI
coding tools, projects, and sessions. It reads provider-owned usage records,
normalizes them into a shared event model, and produces usage and cost reports.
Its reusable library lives in this repository as the Go module
`github.com/ayeshLK/komposyn`; the independently versioned
[`komposyn-lg` CLI](https://github.com/ayeshLK/komposyn-lg) makes the component
useful with existing tools before a broader harness is built.

The production CLI's versioned JSONL sidecar is an integration contract for
Token Logger, not a general KompoSyn plugin protocol. This module includes the
experimental `cmd/harness/` entrypoint and its repository-versioned binary
release path. That path does not replace or distribute the production
`komposyn-lg` executable.

## Principles and open questions

KompoSyn favors clear component boundaries, useful independent releases,
composable contracts, open standards, and community feedback. Open standards
are a foundation, not a limit on meaningful innovation; extensions should
address real requirements and preserve clear compatibility semantics. Token
Logger adopts OpenTelemetry conventions where they fit and documents the
current gaps in its [alignment audit](docs/otel-alignment.md); it does not
claim OpenTelemetry conformance or interoperability.

We do not yet know whether every future component should have a standalone
CLI, what a shared plugin API should contain, or how a future harness should
discover, trust, and distribute components. These decisions should follow
working components and real developer needs, rather than a rule imposed for
consistency. See the [project vision](docs/vision.md),
[architecture](docs/architecture.md), and the founding
[issue #41](https://github.com/ayeshLK/komposyn/issues/41) for context.

## This repository

KompoSyn Core contains the reusable Token Logger library: domain models,
SQLite persistence and migrations, provider adapters, collection, pricing,
diagnostics, and report behavior. It reads provider-owned local data without
capturing prompts, responses, or credentials. The experimental chat sends
submitted text to the selected provider, stores API keys and ChatGPT OAuth
tokens in a private plaintext configuration file, and keeps chat state only in
memory. This
repository prepares signed `komposyn` binaries for the experimental
temporary chat. The standalone CLI repository owns production command
behavior, settings, consent, data-directory selection, process lifecycle, and
the JSONL sidecar transport for `komposyn-lg`.

## Try the experimental `komposyn` chat

`komposyn` is a temporary text chat. It is separate from the production
[`komposyn-lg` Token Logger CLI](https://github.com/ayeshLK/komposyn-lg#install).
The broader agent runtime, universal plugin API, projects, sessions, tasks,
persistent conversations, tool execution, and MCP support are not part of this
release.

> **Release preparation (checked 2026-10-29):** the latest GitHub release is
> v0.9.0 and has no binary assets. v0.10.0 and its signed binaries are not
> published yet. The quick-start scripts can be fetched from `master`, but
> installation succeeds only after a suitable signed binary release is
> published.

### Linux and macOS

Requires Bash, `curl`, `jq`, `tar`, `sha256sum` or `shasum`, and Cosign v2.4.3
on `PATH`. See the [Cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/)
for supported setup options.

```sh
curl -fsSL https://raw.githubusercontent.com/ayeshLK/komposyn/master/scripts/install.sh | bash
```

The script resolves the latest stable binary release. The default install
location is `~/.local/bin`; the installer does not change `PATH`. If needed,
add `export PATH="$HOME/.local/bin:$PATH"` to your shell startup file and open
a new terminal. See the [installation guide](docs/installation.md) for pinned
versions, manual script review, custom directories, PATH details, signature
verification, and troubleshooting.

### Windows (PowerShell)

Requires Windows PowerShell 5.1 or PowerShell 7 and Cosign v2.4.3 on `PATH`.
See the [Cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/).
The command downloads the installer and architecture helper from the same
current `master` ref into one temporary directory before running the installer:

```powershell
$d = Join-Path $env:TEMP ([guid]::NewGuid()); New-Item -ItemType Directory -Path $d -ErrorAction Stop | Out-Null; try { Invoke-WebRequest -ErrorAction Stop -Uri 'https://raw.githubusercontent.com/ayeshLK/komposyn/master/scripts/install.ps1' -OutFile (Join-Path $d 'install.ps1'); Invoke-WebRequest -ErrorAction Stop -Uri 'https://raw.githubusercontent.com/ayeshLK/komposyn/master/scripts/windows-architecture.ps1' -OutFile (Join-Path $d 'windows-architecture.ps1'); & (Join-Path $d 'install.ps1') } finally { Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue }
```

This fetches the installer and architecture helper from the same current
`master` ref. The installer resolves the latest stable binary release. Its
default location is `%LOCALAPPDATA%\Komposyn\bin`; it does not change `PATH`.
See the [Windows PATH instructions](docs/installation.md#windows) and open a
new terminal after adding the directory.

### Check your installation

```sh
komposyn --version
komposyn --help
```

For custom directories, manual verification, and troubleshooting, see the
[installation guide](docs/installation.md).

## First chat

```sh
komposyn
```

In the chat, open `/model`, add an OpenAI provider (API key or Continue with
ChatGPT) or an Anthropic provider (API key), then explicitly choose a model.
Login and model discovery do not send a chat inference request. Submitted chat
messages go to the selected provider. The selected model and chat context stay
in memory. Validated auth profiles and UI settings persist locally; auth
credentials are stored as plaintext in a private configuration file. See
[temporary chat security](docs/harness/phase-one-security.md) and the
[harness guide](docs/harness/README.md).

## Upgrade

Rerun the same install command above to install the latest stable binary. On
Windows, close a running chat first. Upgrading replaces the `komposyn`
executable and preserves auth/settings, the separate `komposyn-lg` executable,
Token Logger data, and backups.

## Uninstall

Uninstall removes only the `komposyn` executable and preserves
`~/.komposyn/auth.json`, `~/.komposyn/settings.json` (or the corresponding
Windows home paths), `komposyn-lg`, and Token Logger data and backups.

**Linux and macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/ayeshLK/komposyn/master/scripts/uninstall.sh | bash
```

**Windows (PowerShell):**

```powershell
$d = Join-Path $env:TEMP ([guid]::NewGuid()); New-Item -ItemType Directory -Path $d -ErrorAction Stop | Out-Null; try { Invoke-WebRequest -ErrorAction Stop -Uri 'https://raw.githubusercontent.com/ayeshLK/komposyn/master/scripts/uninstall.ps1' -OutFile (Join-Path $d 'uninstall.ps1'); & (Join-Path $d 'uninstall.ps1') } finally { Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue }
```

For a custom install directory, see the [installation guide](docs/installation.md)
for the matching install and uninstall options. Install or upgrade the
production Token Logger CLI using the
[standalone instructions](https://github.com/ayeshLK/komposyn-lg#install); its
executable and data are separate from `komposyn`.

The historical combined [v0.7.0 release](https://github.com/ayeshLK/komposyn/releases/tag/v0.7.0)
and earlier tags remain available. Their historical artifact layout and
verification instructions are in
[historical combined release verification](docs/historical-combined-releases.md).
New experimental `komposyn` archives use this repository's manual release
workflow identity; `komposyn-lg` artifacts continue to use the standalone
repository's identity.

## Development

Requires Go 1.26.10 or newer. Run the core checks with:

```sh
make check
go test -race ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md), the [development guide](docs/contributing/development.md),
and [the documentation index](docs/README.md). Production CLI command
behavior, settings, and protocol transport belong in
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg); reusable API,
provider, storage, migration, pricing, diagnostics, reports, and the
experimental chat and its release artifacts belong here.

The project is licensed under Apache-2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).
