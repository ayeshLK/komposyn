# KompoSyn

KompoSyn is an open-source project exploring how independent, useful
components can grow into a modular, composable harness for AI agent workflows.
We are taking a component-first, or “plugin-first,” approach: solve real
problems with components that work on their own, learn from their use, and
compose them when the shared architecture is clear.

The general KompoSyn harness, plugin runtime, and universal plugin API do not
exist yet. The name describes the project direction, not a platform already
available to install. Today, KompoSyn delivers one independently useful
component: Token Logger.

> **Build independently. Compose seamlessly. Evolve openly.**

## The first component: Token Logger

Token Logger gives developers a local view of token usage across supported AI
coding tools, projects, and sessions. It reads provider-owned usage records,
normalizes them into a shared event model, and produces usage and cost reports.
Its reusable library lives in this repository as the Go module
`github.com/ayeshLK/komposyn`; the independently versioned
[`komposyn-lg` CLI](https://github.com/ayeshLK/komposyn-lg) makes the component
useful with existing tools before a broader harness is built.

The CLI's versioned JSONL sidecar is an integration contract for Token Logger,
not a general KompoSyn plugin protocol. The module here contains no executable
or binary-release machinery.

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
capturing prompts, responses, or credentials. The standalone CLI owns command
behavior, settings, process lifecycle, the JSONL sidecar transport, installers,
and binary releases.

> **Existing CLI users:** install or upgrade using the
> [standalone CLI instructions](https://github.com/ayeshLK/komposyn-lg#install).
> Installing over a combined-repository `komposyn-lg` replaces the executable
> while preserving its data directory, settings, consent, diagnostics, and
> backups. Do not reinstall a historical combined binary after upgrading.

The historical combined [v0.7.0 release](https://github.com/ayeshLK/komposyn/releases/tag/v0.7.0)
and earlier tags remain available. Their binary signatures use the former
core release workflow identity; new CLI artifacts are signed only by the
standalone repository. See [historical combined release verification](docs/historical-combined-releases.md).

## Development

Requires Go 1.26.9 or newer. Run the core checks with:

```sh
make check
go test -race ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md), the [development guide](docs/contributing/development.md),
and [the documentation index](docs/README.md). CLI command behavior, settings,
protocol transport, and binary releases belong in
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg); reusable API,
provider, storage, migration, pricing, diagnostic, and report behavior belong
here.

The project is licensed under Apache-2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).
