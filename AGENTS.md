# Agent instructions

## Repository ownership

This repository is the single Go module `github.com/ayeshLK/komposyn`. It owns
the reusable token-logger API: domain models and validation, SQLite storage
and numbered migrations, provider adapters, collector behavior, pricing,
diagnostics, and report queries/formatting. It also contains an experimental,
internal phase-one harness under `internal/harness/` with a local entrypoint at
`cmd/komposyn-harness/`; this is not a stable public API or general plugin
runtime. The standalone `komposyn-lg` executable, interactive onboarding,
settings and consent policy, data-directory selection, production process
lifecycle, JSONL harness transport, installers, and binary releases remain
owned by [ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg).

Do not add a nested module or independently version core packages. Core and CLI
versions are independent; the CLI depends on a tagged core release. Keep the
dependency one-way. Keep harness packages internal and experimental; do not
turn this slice into a general plugin runtime, marketplace, or universal plugin
API without a separate reviewed design. Read [docs/vision.md](docs/vision.md),
[docs/architecture.md](docs/architecture.md), and [docs/harness/README.md](docs/harness/README.md).

Read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md),
[LICENSE](LICENSE), and [NOTICE](NOTICE) before changes. Inspect the worktree
before editing and preserve unrelated changes.

## Core invariants

- Keep provider source files read-only. Do not capture prompts, responses,
  credentials, or other source content by default.
- Ingestion remains idempotent through stable keys. An event append and source
  cursor advance commit together or neither commits.
- Projects and sessions use stable KompoSyn-owned identities. Provider IDs are
  scoped aliases; display names do not merge projects. Preserve path-free
  reconciliation warnings and synthetic Unattributed resources.
- SQLite schema is version 17. Any schema change needs a new immutable,
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
- The production JSONL harness transport is owned by the standalone CLI. Core
  changes that affect its event or schema compatibility must preserve published
  protocol-v1 semantics and be reviewed with the CLI compatibility checks.
- Diagnostics remain sanitized, deduplicated, locally persisted, and
  destination-neutral. Public issue drafts require explicit review in the CLI;
  security-sensitive reports use private disclosure.
- Preserve existing databases and backups. Never silently copy or merge data
  directories.

## Development and validation

Requires Go 1.26 or newer. Run focused checks while iterating and, before
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

## Core releases

Release Please prepares and reviews core version/changelog changes. Merging a
release PR on `master` creates the source tag and GitHub release notes through
the pinned Release Please workflow. Core releases contain the Go module source;
they do not build or publish `komposyn-lg` binaries. Never create or move tags
manually. Historical combined releases through v0.7.0 remain available; see
[historical combined release verification](docs/historical-combined-releases.md).

Keep Actions permissions least-privilege and third-party actions pinned to
immutable commit SHAs. The standalone CLI suite in core CI runs untrusted
public code without secrets or write permissions.
