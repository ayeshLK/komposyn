# Security Policy

## Scope and supported versions

Komposyn is a local-first application. The security boundary includes the
standalone executable, installers, release artifacts, SQLite data directory,
settings, diagnostics, and the JSONL harness. Provider transcript files remain
provider-owned inputs and should be treated as sensitive.

Only the latest released version receives security fixes. Development branches
may contain incomplete behavior and should not be used for sensitive workloads.

## Threat model and guarantees

Komposyn is designed not to require provider credentials and not to send
provider transcripts, prompts, responses, or credentials to a hosted Komposyn
service. Imports, reports, and collection are local operations. The tool reads
provider source files but does not modify them.

The database and settings are local files protected by the host filesystem.
Komposyn does not replace operating-system access controls or encrypt the data
directory for you. Anyone who can read the data directory may be able to read
usage metadata, project/session identifiers, diagnostics, and provenance.
Protect backups and shared machines accordingly.

Diagnostics are sanitized and deduplicated before local persistence. Sanitizing
is best effort, not a guarantee that arbitrary secrets are removed. Review a
diagnostic before opening its GitHub issue draft; opening that URL is an
external action that sends the sanitized text to GitHub and may expose it in
browser or URL logs.

Release installers verify SHA-256 checksums and Cosign certificates before
replacing the executable. Do not bypass those checks or install artifacts from
an untrusted source.

See [the security and privacy model](docs/security-model.md) for detailed
data-flow and disclosure review guidance.

## Reporting a vulnerability

Please do not report security vulnerabilities in public issues, pull requests,
or discussions. Use GitHub's private vulnerability reporting for this
repository if it is enabled. If it is not available, contact the maintainers
privately before disclosure.

Include the affected version or commit, operating system, reproduction steps,
impact, attack prerequisites, and any suggested mitigation. Keep the report
minimal and redact credentials, provider transcripts, prompts, responses,
private paths, and other sensitive source content. Do not attach a raw
database, diagnostic dump, or release credential.

If the issue concerns a release artifact or installer, include the artifact
name and verification result without sharing private signing material. If it
concerns the harness, include the protocol method and sanitized request shape,
not sensitive event values.

The token logger is local-first and should never require provider credentials
for ingestion or reporting.

## Security-sensitive changes

Security-sensitive changes should document the threat model, preserve least
privilege in GitHub Actions, keep action references pinned to immutable commit
SHAs, add regression coverage where practical, and include reproducible
validation in the pull request. See [CONTRIBUTING.md](CONTRIBUTING.md).
