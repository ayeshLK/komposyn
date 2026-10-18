# KompoSyn Core

KompoSyn Core is the reusable Go module for local token-usage normalization,
SQLite persistence and migrations, provider adapters, collection, pricing,
diagnostics, and reporting. It reads provider-owned local data without
capturing prompts, responses, or credentials.

The module is `github.com/ayeshLK/komposyn`. It contains no executable or
binary release machinery. The independently versioned `komposyn-lg` CLI,
including its harness transport and installer lifecycle, is owned by the
[standalone repository](https://github.com/ayeshLK/komposyn-lg).

> **Existing CLI users:** install or upgrade using the [standalone CLI
> instructions](https://github.com/ayeshLK/komposyn-lg#install). Running the
> standalone installer over a combined-repository `komposyn-lg` replaces the
> executable and preserves its data directory, settings, consent, diagnostics,
> and backups. Do not reinstall a historical combined binary after upgrading.

The historical combined [v0.7.0 release](https://github.com/ayeshLK/komposyn/releases/tag/v0.7.0)
and earlier tags remain available. Their binary signatures use the former
core release workflow identity; new CLI artifacts are signed only by the
standalone repository. See [historical combined release verification](docs/historical-combined-releases.md).

KompoSyn's broader harness and plugin platform remain proposed. See the
[project vision](docs/vision.md) and [architecture](docs/architecture.md) for
the implemented core boundary and open design questions.

## Development

Requires Go 1.26.9 or newer. Run the core checks with:

```sh
make check
go test -race ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md), the [development guide](docs/contributing/development.md),
and [the documentation index](docs/README.md). CLI issues, command behavior,
settings, protocol transport, and binary releases belong in
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg); reusable API,
provider, storage, migration, pricing, diagnostic, and report behavior belong
here.

The project is licensed under Apache-2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).
