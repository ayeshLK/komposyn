# Security Policy

## Scope and support

This repository owns the reusable KompoSyn Go module, including provider
adapters, collection, SQLite storage and migrations, pricing, diagnostics, and
reports. The standalone [komposyn-lg repository](https://github.com/ayeshLK/komposyn-lg)
owns executable behavior, settings and consent policy, data-directory choice,
process lifecycle, JSONL harness transport, installers, and binary releases.

Core security fixes target the latest released core version and `master`.
Historical combined releases through v0.7.0 remain available but are not the
current CLI distribution path. The CLI has an independent version and declares
its exact core dependency; see [compatibility and coordinated security releases](docs/standalone-compatibility-security.md).

## Threat model and guarantees

KompoSyn is local-first. It reads provider-owned source files but does not
modify them or intentionally store prompts, responses, full transcripts, API
keys, or provider credentials. Normalization and reporting operate locally.
The Token Logger database may contain usage counts, timestamps, provider/model/client
identifiers, project/session references, provenance, source hashes, cursors,
collector state, pricing rules, and sanitized diagnostics. The host filesystem
protects this data; KompoSyn does not encrypt it for you.

The experimental harness under `internal/harness/` has a different boundary: it
sends prompts, root instructions, and approved tool results to the selected
provider and persists conversation history in a separate `harness.db`. That
history can be sensitive and is not encrypted by the harness. It uses explicit
environment credential references and does not send conversation content to
Token Logger. See [harness data handling](docs/harness/phase-one-security.md).

Diagnostics are best-effort sanitized and locally persisted. The standalone
CLI owns review and routing of public issue drafts. A suspected vulnerability
or sensitive-data exposure must use private reporting and must never be routed
to a public issue draft.

## Reporting a vulnerability

Do not report vulnerabilities in public issues, pull requests, or discussions.
Use the private [KompoSyn Core vulnerability reporting form](https://github.com/ayeshLK/komposyn/security/advisories/new).
If it is unavailable, contact the maintainers privately before disclosure.
Include affected/fixed versions, operating system, reproduction steps, impact,
and prerequisites. Redact credentials, provider transcripts, prompts,
responses, private paths, and unsanitized diagnostics. Do not attach raw
production databases or release credentials.

The repository that contains the vulnerable code owns the advisory. If a core
report affects standalone CLI releases, coordinate privately with the CLI
maintainers before disclosure so a fixed core tag and compatible signed CLI
release are ready together. The procedure and version mapping requirements are
documented in [standalone compatibility and security](docs/standalone-compatibility-security.md).

## Security-sensitive changes

Describe the threat model, preserve least-privilege CI and immutable Action
pins, add regression coverage where practical, and include reproducible
validation. Never use secrets or writable cross-repository access in normal
candidate validation. See [CONTRIBUTING.md](CONTRIBUTING.md).
