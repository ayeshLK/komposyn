# Project vision

KompoSyn is an open-source project exploring a modular, composable harness for
AI agent workflows. Its component-first, or “plugin-first,” approach starts
with independent capabilities that solve real problems, can be used today,
and can be tested and understood before they are composed into a larger
platform. This lets the project learn from working components without
pretending that the eventual harness architecture is already settled.

## Available now

This repository currently provides KompoSyn Token Logger Core:

- A reusable Go library normalizes provider-owned local usage records into a
  versioned event model and stores them in SQLite.
- The independently versioned `komposyn-lg` executable and its versioned
  JSONL sidecar are maintained in the
  [standalone CLI repository](https://github.com/ayeshLK/komposyn-lg).

The standalone CLI lets developers use Token Logger with existing agent tools
without waiting for a complete harness. The sidecar is a token-logger
integration contract, not a general KompoSyn plugin protocol. This repository also contains an experimental phase-one local harness slice
under `internal/harness/`; it does not yet provide a supported general harness
runtime, universal plugin API, or plugin marketplace. See the [harness guide](harness/README.md).

## Proposed direction

Future work may compose independently useful components into an agent harness
with clear boundaries, open contracts, and local control of sensitive data.
Components should address real developer needs and have evidence-backed
contracts before they become part of a shared runtime. Open standards are a
foundation rather than a limit: extensions are appropriate when requirements
justify them and their compatibility semantics remain clear.

The following questions remain open:

- What is the boundary and lifecycle of the eventual harness?
- What capabilities belong in a common plugin API, if one is needed?
- Does every plugin need a standalone CLI, or only a library and integration
  contract?
- How should plugins be discovered, distributed, versioned, and trusted?
- Which components should be independently distributed, and what do users
  actually need to adopt them?

Until those questions are decided and implemented, label the phase-one harness
as experimental and keep its APIs internal. The Token Logger contracts remain
the source of truth for supported library users and integrators.

## Design principles

- Build independently useful components before composing them.
- Do not require every component to follow the same distribution model; choose
  standalone tools, libraries, or harness integration based on practical need.
- Keep contracts explicit, versioned, and testable.
- Prefer local control and data minimization for sensitive agent workflows.
- Build on open standards where they fit, and extend them thoughtfully when
  real requirements call for it. Do not imply interoperability that has not
  been tested. The current [OpenTelemetry alignment audit](otel-alignment.md)
  records correspondences and gaps without claiming an exporter or
  conformance.
- Let developer feedback and real-world use inform the project's direction.
- Record unresolved product decisions instead of silently turning proposals
  into compatibility promises.

The longer founding context and ongoing discussion are in [issue #41](https://github.com/ayeshLK/komposyn/issues/41).
