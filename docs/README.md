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
- [Upgrades and data preservation](upgrades.md) — installer replacement,
  migrations, and uninstall boundaries.
- [Harness protocol](HARNESS_PROTOCOL.md) — the normative versioned JSONL wire
  contract.

Operations, troubleshooting, and contributor guides will be added as the
documentation program expands. Until then, the repository [AGENTS.md](../AGENTS.md),
[CONTRIBUTING.md](../CONTRIBUTING.md), and [SECURITY.md](../SECURITY.md) are the
authoritative project-maintenance references.
