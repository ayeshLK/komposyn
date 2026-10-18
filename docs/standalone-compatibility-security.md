# Standalone CLI compatibility and coordinated security releases

KompoSyn Core is the reusable library module. The standalone
[`komposyn-lg`](https://github.com/ayeshLK/komposyn-lg) repository owns the
executable, settings and consent policy, data-directory selection, process
lifecycle, protocol transport, installers, and binary releases. CLI and core
versions advance independently; the CLI records its exact core dependency.

## Dependency-update review

Review core changes that may affect the CLI for:

- exported API additions/removals and clean external-consumer builds;
- schema metadata, immutable migration history, backup/restoration behavior,
  refusal of newer schemas, and data preservation;
- protocol-v1 semantics, including `plugin_version` as core and `cli_version`
  as executable, plus exact JSON/CSV values;
- privacy, provider-source immutability, consent/data-path policy, diagnostics,
  dependency vulnerabilities, and release version metadata;
- the standalone CLI suite against the candidate core in core CI, and CLI
  release/lifecycle validation after updating its pinned dependency.

Core candidate CI clones the public CLI without credentials, replaces the core
module only in that temporary clone, and runs the CLI tests with read-only
permissions. It does not commit a `replace` directive or require cross-repo
write access. Normal CLI CI tests its pinned core dependency. The standalone
minimum supported version and current pin are both v0.7.0; the CLI job is not
duplicated for identical versions. If those versions diverge, explicitly test
both before widening the documented compatibility range.

## Coordinated security releases

The repository containing vulnerable code owns the advisory/CVE. Core owns the
reusable API, storage/migrations, adapters, collector, pricing, diagnostics,
and report behavior. The standalone repository owns executable behavior,
settings/consent, data paths, process lifecycle, protocol transport,
installers, and binary releases. Reports received by the other repository are
forwarded privately before disclosure.

For a core vulnerability affecting released CLI versions:

1. Privately fix and validate core; record affected and fixed core versions.
2. Privately update the CLI dependency and validate API use, migrations,
   protocol behavior, data preservation, and CLI/core/protocol/schema metadata.
3. Prepare both changes in private advisory branches/forks for an embargo.
   Publish the fixed core source tag first, then promptly publish a signed CLI
   release pinned to that tag.
4. Coordinate advisories that map affected and fixed core and CLI versions,
   support boundaries, impact, and prerequisites.
5. Give users explicit instructions to install the fixed standalone CLI over
   the existing executable. Preserve its database, settings, consent,
   diagnostics, and backups; no self-updater is provided.

For a core-only defect, core publishes the advisory and fix. For a CLI-only
defect, the standalone repository owns the advisory and signed release. Never
use public issues or pull requests to report an embargoed vulnerability.
See [SECURITY.md](../SECURITY.md) and the CLI's
[security policy](https://github.com/ayeshLK/komposyn-lg/blob/master/SECURITY.md).
