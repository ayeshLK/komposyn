# KompoSyn Core documentation

This repository documents the reusable `github.com/ayeshLK/komposyn` module.
The executable, user configuration, JSONL harness transport, installers, and
binary release process are maintained in the
[standalone CLI repository](https://github.com/ayeshLK/komposyn-lg).

## Core API and operation

- [Project vision](vision.md) — implemented module and open product questions.
- [Architecture](architecture.md) — core ownership, provider ingestion,
  persistence, collector, and report semantics.
- [Token Logger core API](core-api.md) — supported exports and compatibility.
- [Data model](data-model.md) — events, identity, provenance, and transactions.
- [Database migrations](migrations.md) — schema history and migration policy.
- [Pricing catalog](pricing-catalog.md) — offline rules, provenance, and review.
- [Privacy](privacy.md) and [security model](security-model.md) — data
  handling, boundaries, and disclosure.
- [OpenTelemetry alignment](otel-alignment.md) — correspondences, gaps, and
  current non-interoperability boundary.

## Contributing and releases

- [Development](contributing/development.md) and
  [testing](contributing/testing.md) — local checks and regression coverage.
- [Pull requests](contributing/pull-requests.md) — evidence and review guidance.
- [Contributing](../CONTRIBUTING.md), [agent instructions](../AGENTS.md), and
  [security policy](../SECURITY.md).
- [Core release verification](release-verification.md) — reviewed source tags
  and module release notes.
- [Historical combined releases](historical-combined-releases.md) — retained
  v0.7.0-and-earlier CLI artifact provenance and signer verification.
- [Standalone CLI compatibility and coordinated security](standalone-compatibility-security.md)
  — cross-repository review, dependency, and private-release procedures.

For executable installation, upgrades, commands, settings, or the protocol-v1
transport contract, use the
[standalone CLI documentation](https://github.com/ayeshLK/komposyn-lg/tree/master/docs).
