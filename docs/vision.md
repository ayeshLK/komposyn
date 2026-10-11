# Project vision

KompoSyn aims to become a modular, composable harness for AI agent workflows.
The project is intentionally starting with useful components that can be used,
tested, and understood on their own instead of requiring a complete platform
before the first release.

## Available now

This repository currently implements KompoSyn Token Logger:

- A reusable Go library normalizes provider-owned local usage records into a
  versioned event model and stores them in SQLite.
- The standalone `komposyn-lg` CLI imports, watches, and reports on that local
  data.
- The token logger exposes a versioned JSONL `harness` sidecar for machine
  integration.

These are three surfaces of the current token logger component. The sidecar is
not a general KompoSyn plugin protocol, and the repository does not currently
provide a general harness runtime or plugin marketplace.

## Proposed direction

Future work may compose independently useful components into an agent harness
with clear boundaries, open contracts, and local control of sensitive data.
Each proposed component should have a useful purpose and an evidence-backed
contract before it becomes part of a shared runtime.

The following questions remain open:

- What is the boundary and lifecycle of the eventual harness?
- What capabilities belong in a common plugin API, if one is needed?
- Does every plugin need a standalone CLI, or only a library and integration
  contract?
- How should plugins be discovered, distributed, versioned, and trusted?

Until those questions are decided and implemented, documentation should label
the harness and plugin platform as proposed. The current token logger
contracts remain the source of truth for users and integrators.

## Design principles

- Build independently useful components before composing them.
- Keep contracts explicit, versioned, and testable.
- Prefer local control and data minimization for sensitive agent workflows.
- Use open standards where the implementation and compatibility mapping support
  the claim; do not imply interoperability that has not been tested.
- Record unresolved product decisions instead of silently turning proposals
  into compatibility promises.

The longer founding context and ongoing discussion are in [issue #41](https://github.com/ayeshLK/komposyn/issues/41).
