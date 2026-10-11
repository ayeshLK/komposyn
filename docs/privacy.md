# Privacy and data handling

Komposyn is designed for local usage accounting. It does not require provider
credentials to import local transcripts or generate reports.

## What Komposyn reads

The Codex and Claude Code adapters read provider-owned local JSONL files or
directories selected by the user. The collector can use configured roots and
platform default roots when enabled. Source files are read-only.

## What is stored

The local SQLite database stores normalized usage events, timestamps, token
counts, provider/model/client identifiers, project and session references,
provenance, ingestion keys, source cursors, pricing rules, and sanitized local
diagnostics. Settings are stored separately in the configured data directory.

## What is not stored by default

Komposyn does not intentionally persist prompts, model responses, full provider
transcripts, API keys, or provider credentials. Source record hashes and
metadata may be stored to support provenance, deduplication, and replacement
detection; they are not a transcript archive.

## External communication

Importing and reporting are local operations. Release installation downloads
artifacts and verifies their signatures. Diagnostic issue reporting is
interactive and produces a draft for review; it is not silently submitted.
Opening a GitHub issue-draft URL sends the sanitized diagnostic text to
GitHub, where it may also appear in browser history or other URL logs. The
sanitizer is best-effort, not a guarantee of secret removal.

Review [SECURITY.md](../SECURITY.md) before reporting a vulnerability. Never
include credentials, transcripts, prompts, responses, or other sensitive source
content in public issues or diagnostic reports.

## Data preservation

Rerunning the latest standalone installer preserves the existing database,
settings, consent choices, diagnostics, and data directory. Uninstall removes
the executable but intentionally preserves local data.
