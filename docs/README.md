# Komposyn documentation

Use this page to choose the right guide. The repository README is the short
product overview; these pages are the operational and integration reference.

## Start here

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

## Understand the system

- [Privacy and data handling](privacy.md) — what is read, stored, excluded,
  and sent externally.
- [Security and privacy model](security-model.md) — trust boundaries, data
  inventory, disclosure review, and security-sensitive change checks.
- [Architecture](architecture.md) — component boundaries, import flow,
  source replacement safety, and reporting semantics.
- [Data model](data-model.md) — tables, event identity, provenance, time, and
  transaction guarantees.
- [Database migrations](migrations.md) — schema history, compatibility rules,
  and the procedure for adding a migration.
- [Development guide](contributing/development.md) — local setup, validation,
  release checks, and final inspection.
- [Testing guide](contributing/testing.md) — baseline checks and change-area
  coverage expectations.
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
