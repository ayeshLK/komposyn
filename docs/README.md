# KompoSyn documentation

Start with the experimental `komposyn` terminal experience: exchange text,
inspect a tool approval, and help shape the direction toward an agent harness.
For production token reports, use the separate
[`komposyn-lg` CLI](https://github.com/ayeshLK/komposyn-lg); library integrators
can jump to [Token Logger core](#project-and-token-logger-core).

## Try the terminal experience

- [Five-minute guided trial](harness/first-use.md) — provider selection,
  text, a temporary plan, approval, feedback, and optional synthetic Linux reads.
- [Demonstration and verification](harness/first-use-evidence.md) — staged actual-UI
  capture, v0.16.0 installation checks, scoped prior source-build live evidence,
  and pending signed-binary live validation.
- [Experimental CLI installation](installation.md) — prerequisites, verified
  install, pinning, upgrades, removal, and PATH setup.
- [Harness guide](harness/README.md) — source build, controls, local
  configuration, provider/UI boundaries, and implementation evidence.
- [Capability availability](harness/capability-availability.md) — supported routes,
  authentication, platform conditions, and live-verification limits.
- [Chat data handling](harness/phase-one-security.md) — what persists and what
  goes to the selected provider.

## Project and Token Logger core

- [Project vision](vision.md) — implemented module and open product questions.
- [Architecture](architecture.md) — core ownership, provider ingestion,
  persistence, collector, and report semantics.
- [Collector performance baseline](collector-performance-baseline.md) —
  repeatable generated workloads, correctness oracle, measured Linux baseline,
  and proposed workload-specific improvement budgets.
- [Collector bounded source verification design](collector-streaming-verification-design.md) —
  streaming verification protocol and mutation-safety gate for
  follow-on collector optimization.
- [Collector validated append continuation design](collector-append-checkpoint-design.md) —
  accepted and implemented durable parser checkpoints, safe record boundaries, replay
  equivalence, fallback rules, and acceptance evidence for issue #139.
- [Token Logger core API](core-api.md) — supported exports and compatibility.
- [Persisted diagnostic details](diagnostic-details.md) — local diagnostic
  identity, evidence, counters, condition and review semantics.
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

- [Content plan and review](devrel-content-plan.md) — prepared visitor journey,
  pending publication rollout, and manual biweekly review checklist.
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
- [Token Logger package relocation](token-logger-package-relocation.md) —
  import-path migration decision, consumer evidence, and standalone CLI handoff.

For production `komposyn-lg` installation, commands, settings, or protocol-v1
transport, use the
[standalone CLI documentation](https://github.com/ayeshLK/komposyn-lg/tree/master/docs).
