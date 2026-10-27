# Security Policy

## Scope and support

This repository owns the reusable KompoSyn Go module and the experimental
temporary-chat `komposyn` executable, installer, and signed binary releases.
The standalone [komposyn-lg repository](https://github.com/ayeshLK/komposyn-lg)
owns the production executable, settings and consent policy, data-directory
choice, process lifecycle, and JSONL harness transport. Release Please provides
one repository-wide version for core source and experimental binaries;
`komposyn-lg` remains independently versioned and declares its core dependency.
Historical combined releases through v0.7.0 remain available. See
[compatibility and coordinated security releases](docs/standalone-compatibility-security.md).

## Threat model and guarantees

KompoSyn Core is local-first. It reads provider-owned source files but does not
modify them or intentionally store prompts, responses, full transcripts, API
keys, or provider credentials. Normalization and reporting operate locally.
The database may contain usage counts, timestamps, provider/model/client
identifiers, project/session references, provenance, source hashes, cursors,
collector state, pricing rules, and sanitized diagnostics. The host filesystem
protects this data; KompoSyn does not encrypt it for you.

The experimental chat has a separate boundary: submitted text is sent to the
selected provider, while chat history, model selection, continuation data,
discovery results, and request usage remain in memory. It persists API-key
profiles and ChatGPT OAuth tokens as plaintext in `~/.komposyn/auth.json` and
UI settings in `~/.komposyn/settings.json`; Unix permissions and Windows
current-user ACLs restrict access. It does not create a chat database or send
chat usage to Token Logger. See
[temporary chat data handling](docs/harness/phase-one-security.md).
OAuth authorization URLs are transient inputs to an injected system-browser
capability; progress events, status snapshots, diagnostics, and chat context do
not include them. Auth status reads local metadata without resolving secrets
or contacting a provider. See the [auth boundary](docs/harness/phase-two-auth-boundary.md).

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
report affects `komposyn-lg`, coordinate privately with its maintainers before
disclosure so a fixed core tag and compatible signed CLI release are ready
together. For the experimental `komposyn`, coordinate the repository-wide
source tag and signed binary release. The procedure is in
[standalone compatibility and security](docs/standalone-compatibility-security.md).

## Security-sensitive changes

Describe the threat model, preserve least-privilege CI and immutable Action
pins, add regression coverage where practical, and include reproducible
validation. Never use secrets or writable cross-repository access in normal
candidate validation. See [CONTRIBUTING.md](CONTRIBUTING.md).
