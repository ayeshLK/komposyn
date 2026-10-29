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

> **Release preparation:** v0.10.0 is not downloadable until its signed binary
> release is published. The commands below use the planned immutable `v0.10.0`
> source tag and will work after that tag and signed release assets exist.

### Install on Linux or macOS

Requires Bash, `curl`, `tar`, `sha256sum` or `shasum`, and Cosign v2.4.3 on
`PATH`. `jq` is needed only when selecting the latest stable binary. See the
[Cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/)
for supported setup options. A version-pinned installer does not need `jq`.

```sh
installer_version=v0.10.0
curl -fsSLo /tmp/komposyn-install.sh \
  "https://raw.githubusercontent.com/ayeshLK/komposyn/${installer_version}/scripts/install.sh"
bash /tmp/komposyn-install.sh
```

The installer script is pinned to v0.10.0, while omitting `--version` selects
the latest stable binary at install time. To pin the binary version, run
`bash /tmp/komposyn-install.sh --version v0.10.0`. The default install location
is `~/.local/bin`; the script does not change `PATH`. Add it to your shell
startup file, for example `export PATH="$HOME/.local/bin:$PATH"`, then open a
new terminal. See the [installation guide](docs/installation.md) for custom
directories and full PATH instructions.

### Install on Windows

Requires Windows PowerShell 5.1 or PowerShell 7 and Cosign v2.4.3 on `PATH`.
See the [Cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/).
Download the installer and architecture helper from the same source tag into
the same directory before running the installer:

```powershell
$installerVersion = 'v0.10.0'
$scriptDir = Join-Path $env:TEMP "komposyn-installer-$installerVersion"
New-Item -ItemType Directory -Path $scriptDir -Force | Out-Null
$source = "https://raw.githubusercontent.com/ayeshLK/komposyn/$installerVersion/scripts"
Invoke-WebRequest "$source/install.ps1" -OutFile (Join-Path $scriptDir 'install.ps1')
Invoke-WebRequest "$source/windows-architecture.ps1" -OutFile (Join-Path $scriptDir 'windows-architecture.ps1')
& (Join-Path $scriptDir 'install.ps1')
```

Omitting `-Version` selects the latest stable binary; use
`& (Join-Path $scriptDir 'install.ps1') -Version v0.10.0` to pin it. The
default location is `%LOCALAPPDATA%\Komposyn\bin`; the installer does not
change `PATH`. Follow the [Windows PATH and setup instructions](docs/installation.md#windows)
and open a new terminal afterward.

### Start, upgrade, and uninstall

Verify the install and start the chat:

```sh
komposyn --version
komposyn --help
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

To upgrade, download the v0.10.0 installer script again from its immutable
source tag, then run it without a version option for the latest stable binary
or with a version option to pin the upgrade. A downloaded installer does not
update itself. Close the chat before upgrading on Windows. Upgrades preserve
auth/settings and the separate `komposyn-lg` executable and data.

Unix/macOS, using the installer downloaded as above:

```sh
bash /tmp/komposyn-install.sh
bash /tmp/komposyn-install.sh --version v0.10.0
```

Windows, after downloading `install.ps1` and `windows-architecture.ps1` again
from the same source tag into `$scriptDir` as in the Windows install block:

```powershell
& (Join-Path $scriptDir 'install.ps1')
& (Join-Path $scriptDir 'install.ps1') -Version v0.10.0
```

To uninstall on Unix, download and run the uninstaller from the same tag:

```sh
installer_version=v0.10.0
curl -fsSLo /tmp/komposyn-uninstall.sh \
  "https://raw.githubusercontent.com/ayeshLK/komposyn/${installer_version}/scripts/uninstall.sh"
bash /tmp/komposyn-uninstall.sh
```

On Windows, use the `$source` and `$scriptDir` values from the Windows install
block above to download and run the matching uninstaller:

```powershell
Invoke-WebRequest "$source/uninstall.ps1" -OutFile (Join-Path $scriptDir 'uninstall.ps1')
& (Join-Path $scriptDir 'uninstall.ps1')
```

Uninstall removes only the `komposyn` executable. It preserves
`~/.komposyn/auth.json`, `~/.komposyn/settings.json` (or the corresponding
Windows home paths), `komposyn-lg`, and Token Logger data and backups. For a
custom install directory, pass the same `--install-dir` / `-InstallDir` value
to uninstall that you used to install. See the [detailed installation guide](docs/installation.md).

> For existing Token Logger CLI users: install or upgrade `komposyn-lg` using
> the [standalone CLI instructions](https://github.com/ayeshLK/komposyn-lg#install).
> Installing over a combined-repository `komposyn-lg` replaces the executable
> while preserving its data directory, settings, consent, diagnostics, and
> backups. Do not reinstall a historical combined binary after upgrading.

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
