# Contributing

Contributions to KompoSyn Core are provided under Apache-2.0; see [LICENSE](LICENSE)
and [NOTICE](NOTICE). Read [AGENTS.md](AGENTS.md) and [SECURITY.md](SECURITY.md)
before changing storage, migrations, provider handling, or security behavior.

## Ownership

This repository owns the reusable `github.com/ayeshLK/komposyn` Go module:
domain behavior, APIs, SQLite storage/migrations, adapters, collection, pricing,
diagnostics, and reports. The standalone executable, command policy, settings,
consent/onboarding, data-directory resolution, process lifecycle, harness
transport, installers, and binary release lifecycle belong in
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg).

Keep dependency direction one-way: CLI imports core; core never imports the
CLI. Core and CLI versions advance independently. Preserve provider-source
privacy, data paths and schema compatibility, protocol-v1 semantics where
core changes affect the contract, and exact machine-readable report values.

## Development and validation

Requires Go 1.26.9 or newer. The normal gate is:

```sh
make check
go test -race ./...
```

The CI workflow also runs formatting, `go mod verify`, vet, tests, vulnerability
scanning, and the public standalone CLI test suite against candidate core. The
cross-repository test clones public CLI source without credentials or write
permissions and uses a temporary module replacement in the runner's temporary
copy only. Never commit a `replace` directive.

See [the development guide](docs/contributing/development.md),
[testing guide](docs/contributing/testing.md), and [pull request guide](docs/contributing/pull-requests.md).

## Dependency and compatibility review

Core dependency updates can affect the separately versioned CLI. Review a core
change for exported API compatibility, schema/migration behavior, protocol-v1
compatibility, privacy/security notes, schema/version metadata, and downstream
release validation. A core candidate must pass the standalone CLI suite in CI.
The detailed review and coordinated security-release procedure is in
[standalone compatibility and security](docs/standalone-compatibility-security.md).

## Versioning and releases

Use Conventional Commit prefixes for changes that affect release notes or
versioning. Release Please opens a reviewed core release PR. When that PR is
merged to `master`, the pinned Release Please workflow creates the source tag
and release notes. Core releases contain module source, not CLI binaries. Do
not create or move tags manually. The standalone repository independently
versions and publishes signed CLI binaries. Historical combined releases
through v0.7.0 remain available; see [historical release verification](docs/historical-combined-releases.md).

## Change-specific requirements

- **Storage or migration:** add an immutable numbered migration, update the
  runner and schema contract, and test fresh creation, complete history,
  restoration, backup failure, and migration failure preservation.
- **Provider or collector:** test normalization, malformed input, snapshots,
  cursor behavior, replacement detection, idempotency, source immutability, and
  health outcomes.
- **Pricing or reports:** preserve provenance, unknown/partial semantics, local
  civil-hour grouping, and exact JSON/CSV values.
- **Security or privacy:** describe the threat model, disclosure boundary, and
  data-preservation impact. Do not add telemetry or external transmission
  without an explicit design change and user consent.
- **Workflow:** retain read-only permissions in normal CI, no cross-repository
  credentials, least-privilege release permissions, and immutable Action pins.
- **Documentation:** verify links and examples against the implementation.

Keep PRs focused. Include exact commands and outcomes, summarize compatibility
and privacy impact, and avoid sensitive provider data in examples or evidence.
