# KompoSyn documentation

This repository documents the reusable `github.com/ayeshLK/komposyn` module
and an experimental temporary chat. This repository releases the experimental
`komposyn` binary; the production `komposyn-lg` executable, user configuration,
and JSONL harness transport are maintained in the
[standalone CLI repository](https://github.com/ayeshLK/komposyn-lg).

## Core API and operation

- [Experimental temporary chat](harness/README.md) — `komposyn` build, `/model`
  workflow, local configuration, provider/UI boundaries, and acceptance status.
- [Experimental CLI installation](installation.md) — verified install,
  pinning, upgrades, removal, and platform prerequisites.

- [Project vision](vision.md) — implemented module and open product questions.
- [Architecture](architecture.md) — core ownership, provider ingestion,
  persistence, collector, and report semantics.
- [Collector performance baseline](collector-performance-baseline.md) —
  repeatable generated workloads, correctness oracle, measured Linux baseline,
  and proposed workload-specific improvement budgets.
- [Collector bounded source verification design](collector-streaming-verification-design.md) —
  proposed streaming verification protocol and mutation-safety gate for
  follow-on collector optimization.
- [Collector validated append continuation design](collector-append-checkpoint-design.md) —
  proposed durable parser checkpoints, safe record boundaries, replay
  equivalence, fallback rules, and acceptance evidence for issue #139.
- [Token Logger core API](core-api.md) — supported exports and compatibility.
- [Diagnostics contract v1 foundation](diagnostics-contract-v1.md) — closed
  classifications, safe failures/events, bounded mailbox, and observed open.
- [Structured diagnostic instrumentation](diagnostics-instrumentation.md) —
  failure-boundary coverage, correlation, bounded poll summaries, and privacy
  limits delivered after the shared foundation.
- [Project inspection evidence](project-inspection.md) — bounded read-only
  identity evidence, selected versus stored-history semantics, schema gaps,
  and conservative recovery classifications.
- [Data model](data-model.md) — events, identity, provenance, and transactions.
- [Database migrations](migrations.md) — schema history and migration policy.
- [Migration backups and recovery](migration-backups.md) — the library's
  verified backup API, metadata, failure behavior, and recovery contract.
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
- [Release verification](release-verification.md) — manual Release Please and
  binary publication workflow, signer identity, and recovery steps.
- [Historical combined releases](historical-combined-releases.md) — retained
  v0.7.0-and-earlier CLI artifact provenance and signer verification.
- [Standalone CLI compatibility and coordinated security](standalone-compatibility-security.md)
  — cross-repository review, dependency, and private-release procedures.

For production `komposyn-lg` installation, commands, settings, or protocol-v1
transport, use the
[standalone CLI documentation](https://github.com/ayeshLK/komposyn-lg/tree/master/docs).
