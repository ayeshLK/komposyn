# Agent instructions

## Repository ownership

This repository is the single Go module `github.com/ayeshLK/komposyn`. It owns
the reusable Token Logger API: domain models and validation, SQLite storage
and numbered migrations, provider adapters, collector behavior, pricing,
diagnostics, and report queries/formatting. It also contains an experimental
temporary text chat under `plugins/model-provider/`, `plugins/ui/`, and
`internal/harness/`, with source entrypoint `cmd/harness/` and binary name
`komposyn`. This repository owns packaging, installers, and binary releases
for this experimental executable. The standalone `komposyn-lg` production
executable, production settings/consent/onboarding, data-directory selection,
and JSONL harness transport remain owned by
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg).

Do not add a nested module or independently version core packages. Release
Please owns one repository-wide version for core source and experimental
`komposyn` binaries. The production `komposyn-lg` CLI remains independently
versioned and depends on a tagged core release. Keep the dependency one-way.
Keep this chat and its role-specific plugin contracts
experimental; do not add projects, sessions, tasks, persistent conversations,
tool execution, a marketplace, or a universal plugin API to this milestone.
Read [docs/vision.md](docs/vision.md), [docs/architecture.md](docs/architecture.md),
and [docs/harness/README.md](docs/harness/README.md).

Read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md),
[LICENSE](LICENSE), and [NOTICE](NOTICE) before changes. Inspect the worktree
before editing and preserve unrelated changes.

Every new Go source file must begin with the repository's standard copyright
and Apache-2.0 license header, matching existing Go source files. Keep Go build
constraints immediately after that header with a blank line before the package
declaration.

## Core invariants

- Keep provider source files read-only. Do not capture prompts, responses,
  credentials, or other source content by default.
- Ingestion remains idempotent through stable keys. An event append and source
  cursor advance commit together or neither commits.
- Projects and sessions use stable KompoSyn-owned identities. Provider IDs are
  scoped aliases; display names do not merge projects. Preserve path-free
  reconciliation warnings and synthetic Unattributed resources.
- SQLite schema is version 18. Any schema change needs a new immutable,
  numbered transactional migration, runner update, and complete upgrade tests.
  Do not add automatic down-migrations.
- Migration backups use SQLite's online backup API, represent a consistent
  pre-migration state, and are verified before migration. Backup failure must
  prevent migration; failed migration must leave the original and backup
  usable.
- Provider adapters parse one immutable snapshot for fingerprinting and event
  offsets. Preserve cursor generation, pending-tail visibility, and full-digest
  unchanged checks.
- Usage-shape allowlists remain strict. Preserve billing provenance and avoid
  inferring unsupported pricing modifiers. Keep provider-reported cost apart
  from estimates.
- Persistent collector roots, lease, heartbeat, stop request, and run history
  remain library-owned state. Foreground/embedded collection must not mutate
  registered roots. Preserve one healthy lease owner per data directory.
- Reports preserve exact JSON/CSV values, local civil-hour grouping, UTC
  offsets, fractional offsets, and DST-fold distinctions.
- Harness protocol transport is owned by the standalone CLI. Core changes that
  affect event or schema compatibility must preserve its published protocol-v1
  semantics and be reviewed with the CLI compatibility checks.
- The experimental chat sends submitted text only to the selected provider;
  messages, selection, continuation data, discovery, and request usage remain
  in memory. Persist only validated auth profiles and UI settings. Do not open
  a chat database, require a workspace, or route through a session runtime.
- Keep provider and UI contracts role-specific. Providers receive one resolved
  credential per operation, never load the credential store, and never expose
  tools in the temporary chat path. Keep Responses and Messages continuation
  formats provider-owned.
- Diagnostics remain sanitized, deduplicated, locally persisted, and
  destination-neutral. Public issue drafts require explicit review in the CLI;
  security-sensitive reports use private disclosure.
- Preserve existing databases and backups. Never silently copy or merge data
  directories.

## Development and validation

Requires Go 1.26.10 or newer. Run focused checks while iterating and, before
submitting, run:

```sh
go mod verify
go vet ./...
go test ./...
go test -race ./...
make check
```

`make check` verifies formatting, modules, vet, and tests. CI also runs the
public standalone CLI test suite against candidate core using an isolated,
temporary module replacement; it uses read-only access, no cross-repository
credentials, and never commits a `replace` directive. Review any generated
diffs from formatting. Keep binaries, databases, WAL files, archives, and
agent metadata out of commits.

## Repository releases

Release Please prepares and reviews the repository-wide version and changelog.
The workflow is manual-only: merging a release PR on `master` does not create a
tag. After normal review and merge, explicitly dispatch Release Please from
`master` to create the source tag and GitHub release, then dispatch the binary
release workflow with that exact tag and commit. It builds and signs only the
experimental `komposyn` binary; it does not build or publish `komposyn-lg`.
Never create or move tags manually. Historical combined releases through
v0.7.0 remain available; see
[historical combined release verification](docs/historical-combined-releases.md).

Keep Actions permissions least-privilege and third-party actions pinned to
immutable commit SHAs. The standalone CLI suite in core CI runs untrusted
public code without secrets or write permissions.
