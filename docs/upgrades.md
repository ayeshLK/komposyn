# Upgrades and data preservation

The initial release upgrade operation is deliberately simple: rerun the latest
standalone installer. It replaces the executable while reusing the existing
data directory.

The following local state must survive an upgrade:

- `token-logger.db`, including its migration history and usage data
- `settings.json`
- onboarding and collector preferences
- local diagnostics and their review state
- consent choices represented by local settings

When the new binary opens the database, numbered SQLite migrations run inside
the storage layer. Do not copy or edit database files while the CLI is running.
Create a backup before a major upgrade if the data is important.

In-CLI self-updating is deferred. Release installers verify checksums and Cosign
signatures; do not bypass those checks by downloading an unverified binary.
