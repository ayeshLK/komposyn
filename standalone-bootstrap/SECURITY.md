# Security Policy

KompoSyn Token Logger CLI is a local-first application. The CLI reads
provider-owned source files and must not modify them or capture prompts,
responses, credentials, or other source content. The local data directory may
contain usage metadata, identifiers, diagnostics, and provenance; protect it
and its backups with host access controls.

Release installers must verify checksums and Cosign certificates before
replacing an executable. Standalone artifacts are trusted only when the
certificate identity is exactly
`https://github.com/ayeshLK/komposyn-lg/.github/workflows/release.yml@refs/heads/master`
and the OIDC issuer is `https://token.actions.githubusercontent.com`.
Never weaken verification or accept the former combined repository's identity
for new standalone artifacts.

Please do not report vulnerabilities in public issues, pull requests, or
discussions. Use the repository's private vulnerability reporting form at
`https://github.com/ayeshLK/komposyn-lg/security/advisories/new`. If it is
unavailable, contact the maintainers privately before disclosure. Include the
affected version, operating system, reproduction steps, impact, and
prerequisites. Redact credentials, provider transcripts, prompts, responses,
private paths, and unsanitized diagnostics.
