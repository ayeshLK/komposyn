# KompoSyn documentation

Use this page to choose the right guide. The repository README is the short
product overview; these pages are the maintained project, operational, and
integration reference.

## Start here

- [Project vision](vision.md) — KompoSyn's proposed direction, current
  component, and open harness questions.
- [Getting started](getting-started.md) — install or build the CLI, import a
  first source, and verify a report.
- [Installation](installation.md) — release installers, platform paths,
  signatures, upgrades, and uninstall behavior.
- [Usage guide](usage.md) — imports, reports, watching, settings, and the
  interactive mode.
- [CLI reference](cli-reference.md) — command syntax, options, exit behavior,
  and output formats.
- [Configuration and data locations](configuration.md) — data directories,
  settings, collector roots, and ownership boundaries.

## Choose by audience

- **New to KompoSyn:** read the [project vision](vision.md), then follow
  [getting started](getting-started.md) to use Token Logger today.
- **Using Token Logger:** use the [usage guide](usage.md),
  [CLI reference](cli-reference.md), and [privacy guide](privacy.md).
- **Integrating the current component:** read the
  [token logger harness protocol](HARNESS_PROTOCOL.md); it is a token-logger
  integration contract, not a general KompoSyn plugin API.
- **Contributing or proposing a component:** read
  [CONTRIBUTING.md](../CONTRIBUTING.md), [AGENTS.md](../AGENTS.md), and the
  [architecture](architecture.md).
- **Reviewing operations and releases:** use the security, operations, backup,
  upgrade, troubleshooting, and release guides below.

## Understand the system

- [Architecture](architecture.md) — current token logger boundaries, import
  flow, source replacement safety, and reporting semantics.
- [Privacy and data handling](privacy.md) — what is read, stored, excluded,
  and sent externally.
- [Security and privacy model](security-model.md) — trust boundaries, data
  inventory, disclosure review, and security-sensitive change checks.
- [Data model](data-model.md) — tables, event identity, provenance, time, and
  transaction guarantees.
- [Database migrations](migrations.md) — schema history, compatibility rules,
  and the procedure for adding a migration.
- [Development guide](contributing/development.md) — local setup, validation,
  release checks, and final inspection.
- [Testing guide](contributing/testing.md) — baseline checks and change-area
  coverage expectations.
- [Pull request guide](contributing/pull-requests.md) — readable descriptions,
  evidence, compatibility impact, and review handoff.
- [Operations](operations.md) — data locations, collection health, backups,
  recovery, upgrades, and uninstall boundaries.
- [Backups and recovery](backups.md) — consistent backup and restore
  procedures for local state.
- [Troubleshooting](troubleshooting.md) — common installation, import,
  reporting, migration, and release issues.
- [Release verification](release-verification.md) — artifact, signature,
  installer, and lifecycle checks.
- [Upgrades and data preservation](upgrades.md) — installer replacement,
  migrations, and uninstall boundaries.
- [Harness protocol](HARNESS_PROTOCOL.md) — the normative versioned JSONL wire
  contract.

Security-sensitive maintenance remains governed by the repository
[AGENTS.md](../AGENTS.md), [CONTRIBUTING.md](../CONTRIBUTING.md), and
[SECURITY.md](../SECURITY.md). These guides complement those authoritative
project-maintenance references.
