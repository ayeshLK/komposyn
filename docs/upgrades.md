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
- registered collector sources and prior runtime history

When the new binary opens the database, numbered SQLite migrations run inside
the storage layer. Do not copy or edit database files while the CLI is running.
Create a backup before a major upgrade if the data is important.

In-CLI self-updating is deferred. Release installers verify checksums and Cosign
signatures; do not bypass those checks by downloading an unverified binary.

The installer checks the selected data directory for an active managed
collector and requests cooperative stop before replacement. Only a previously
detached background collector is restarted automatically. Foreground and
embedded REPL collection remain stopped with explicit manual-resume guidance,
so an upgrade cannot silently change collection mode or roots. The installer
never terminates a persisted PID. Set `TOKEN_LOGGER_DATA_DIR` during upgrade
for a non-default data directory. Uninstall follows the same stop-before-remove
rule and leaves all SQLite/settings state intact.
