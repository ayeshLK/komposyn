# Privacy and data handling

KompoSyn Core is designed for local usage accounting. It does not require
provider credentials to normalize local transcripts or query reports. The
standalone CLI owns production command, settings, consent, and process policy;
its user documentation is maintained in
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg). This repository
also contains an experimental temporary chat; see its
[data-handling notes](harness/phase-one-security.md).

## What Komposyn reads

The Codex and Claude Code adapters read provider-owned local JSONL files or
directories selected by the user. The collector can use configured roots and
platform default roots when enabled. Source files are read-only.

## What is stored

The Token Logger SQLite database stores normalized usage events, timestamps, token
counts, provider/model/client identifiers, project and session references,
provenance, ingestion keys, source cursors, registered collector paths, local
collector runtime/health, pricing rules, and sanitized local diagnostics.
Production settings and consent are managed by the standalone CLI. Registered paths are
local administrative data and are excluded from core diagnostic reports.

## What is not stored by default

Token Logger does not intentionally persist prompts, model responses, full provider
transcripts, API keys, or provider credentials. Source record hashes and
metadata may be stored to support provenance, deduplication, and replacement
detection; they are not a transcript archive.

## External communication

Core normalization and report queries are local operations. The experimental
chat sends submitted text to the selected provider and does not persist the
conversation. The standalone CLI downloads and verifies its signed release
artifacts. Diagnostic issue
review and routing are CLI-owned; no issue is submitted automatically.
The public pricing catalog is bundled with the application; routine catalog
loading, import, and reporting do not contact providers or send usage/model
data over the network.
Opening a GitHub issue-draft URL sends the sanitized diagnostic text to
GitHub, where it may also appear in browser history or other URL logs. The
sanitizer is best-effort, not a guarantee of secret removal. Review the URL
before opening it, and use private vulnerability reporting instead of an issue
draft for security vulnerabilities.

Review [SECURITY.md](../SECURITY.md) before reporting a vulnerability. Never
include credentials, transcripts, prompts, responses, or other sensitive source
content in public issues or diagnostic reports.

## Data preservation

The standalone CLI installer replaces the executable while preserving the
existing database, settings, consent choices, diagnostics, backups, and data
directory. Uninstall removes the executable but intentionally preserves local
data. See the standalone repository's
[installation and upgrade guide](https://github.com/ayeshLK/komposyn-lg/blob/master/docs/installation.md).
