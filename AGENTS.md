# AGENTS.md

## Product direction and repository overview

KompoSyn is intended to become a modular, composable harness for AI agent
workflows. This repository currently implements one independently useful
component: KompoSyn Token Logger, a local-first Go application that normalizes
usage events from AI coding clients, stores them in SQLite, and produces usage
and cost reports. The standalone executable is named `komposyn-lg`.

Do not assume that a general harness, plugin runtime, plugin marketplace, or
universal plugin API exists. The current token logger has three concrete
surfaces: its reusable Go library, the `komposyn-lg` CLI, and the token
logger's versioned JSONL `harness` sidecar. The sidecar is a token-logger
integration contract, not a general KompoSyn plugin standard. For product
intent and undecided questions, read [docs/vision.md](docs/vision.md); for the
implemented component boundaries, read [docs/architecture.md](docs/architecture.md).

The module is `github.com/ayeshLK/komposyn` and requires Go 1.26 or newer.
The repository is licensed under Apache-2.0; preserve the headers and consult
`LICENSE`, `NOTICE`, `CONTRIBUTING.md`, and `SECURITY.md` before making changes.

## Repository layout

- `plugins/library/token-logger/` — domain model, validation, SQLite storage,
  migrations, provider adapters, collector, pricing, diagnostics, and report
  formatting. This package must remain usable independently of the CLI.
- `plugins/cli/token-logger/` — the `komposyn-lg` command, interactive mode,
  import/report/settings commands, and the `harness` JSONL sidecar protocol.
- `docs/HARNESS_PROTOCOL.md` — versioned protocol contract. Keep stdout clean:
  protocol responses only; diagnostics go to stderr.
- `docs/pricing-catalog.md` — pricing catalog provenance, interval update,
  validation, and estimate/reporting rules.
- `scripts/` — release archive creation, dependency-license collection,
  installers/uninstallers, and release lifecycle verification.
- `.github/workflows/ci.yml` — formatting, module verification, vet, tests,
  builds, and race tests on GitHub Actions.
- `.github/workflows/codeql.yml` — scheduled and pull-request CodeQL analysis
  with read-only repository access and narrowly scoped security-events access.
- `.github/workflows/release.yml` — tagged release build, Cosign signing and
  verification, publication, and installer lifecycle checks.
- `Makefile` — the preferred local entry points for development and releases.
- `docs/vision.md` — maintained product direction and explicitly open harness
  and plugin questions.

## Core design invariants

- Keep the library/CLI boundary intact. Domain behavior belongs in the library;
  CLI code should adapt arguments, input/output, and process lifecycle.
- Keep provider source files read-only by default. The logger ingests local
  source data and must not modify provider logs.
- Do not capture prompts, responses, credentials, or other source content by
  default. Persist normalized usage and provenance fields only.
- Event ingestion is idempotent through stable ingestion keys. Preserve
  transactional behavior: an append and its source cursor advance together, or
  neither is committed.
- The current SQLite schema version is 9. Migrations 1 and 2 create the core
  and diagnostics tables, migration 3 normalizes stored timestamps to fixed
  width UTC values, and migration 4 adds source-generation tracking for
  transcript replacement detection. Migration 5 adds rational pricing rates
  and catalog identity/source provenance while preserving the legacy
  `rate_micros` meaning. Migration 6 adds KompoSyn-owned project identity,
  canonical locations, scoped provider aliases, and reconciliation warnings
  without rewriting historical event references. Migration 7 normalizes and
  deduplicates nullable reconciliation-warning keys. Migration 8 reserves both
  synthetic Unattributed navigation ID strings across the project and session
  namespaces and remaps any older persisted collisions without losing
  references. Migration 9 records pending-tail and
  safe unchanged-snapshot reuse state on source cursors. SQLite schema changes
  require an
  explicit numbered migration in `plugins/library/token-logger/migrations.go`,
  a matching runner update, and an upgrade test covering the complete history.
- Provider adapters parse one immutable source snapshot for both fingerprinting
  and event offsets; collector detection uses the same snapshot. Preserve
  source-cursor generation, pending-tail visibility, and full-digest unchanged
  checks when handling replacements, and do not reintroduce separate
  file-descriptor parsing or mtime/size-only shortcuts.
- Reports group hourly data by local civil hour while retaining the event's
  numeric UTC offset, including fractional offsets and DST folds.
- Pricing is calculated offline from time-bounded rules. The embedded public
  API catalog is synchronized locally and transactionally; released entry
  rates/provenance are immutable, with only a one-time `effective_to` closure
  allowed when a replacement rate is added. Validate catalog provenance,
  rational denominators, and overlapping equal-precedence selectors/intervals.
  Keep unsupported or ambiguous billing contexts unknown; never infer
  subscription or provider-specific pricing modifiers. See
  [docs/pricing-catalog.md](docs/pricing-catalog.md).
- Keep provider-reported cost separate from calculated estimates. Machine
  reports expose `pricing_provider` and `calculation_basis` alongside pricing
  version/source/model/effective interval; distinguish public-catalog,
  custom, mixed, partial, known-zero, and unavailable bases. When grouping
  combines different provenance, report `mixed` rather than implying one rule.
- Harness reference metadata and its event must be persisted atomically. A
  session that names a project requires matching project metadata in the same
  record request; preserve stable protocol error codes and protocol version 1.
- Preserve existing database, configuration, consent, and local diagnostic
  data during upgrades and uninstall. The initial upgrade strategy is to rerun
  the latest standalone installer; in-CLI self-updating is deferred.
- The harness protocol is versioned. Maintain stable error codes and the
  `initialize`, `record`, `flush`, `health`, and `shutdown` method semantics.
  Keep the per-message input limit and never write logs to protocol stdout.
- Diagnostics must be sanitized, deduplicated, stored locally, and explicitly
  reviewed before an issue draft is opened. Never include sensitive source
  content in diagnostics or issue reports.

## Development workflow

Run the focused checks while iterating:

```sh
go test ./...
go test -race ./...
go vet ./...
make check
```

`make check` runs formatting, vet, tests, and a CLI build. The CI workflow also
verifies `go mod verify`, Go formatting, and builds on Linux, macOS, and
Windows; the race job runs `go test -race ./...`, and CodeQL runs separately.
Note that `make fmt` and the `fmt` phase of `make check` modify Go files;
review the resulting diff afterward. Add or update tests alongside behavior
changes, especially for provider normalization, immutable snapshots,
idempotency, migrations, reporting, protocol errors, and data preservation.

Useful local commands:

```sh
make build
go test ./plugins/library/token-logger -run TestName
go test ./plugins/cli/token-logger -run TestName
```

Keep generated binaries under ignored `bin/`; do not commit local databases,
WAL files, release archives, or agent metadata.

## Release workflow

Build a release with a dedicated empty directory:

```sh
make release VERSION=v0.1.0 OUTPUT_DIR=dist
```

`scripts/build-release.sh` creates the output directory if absent, rejects
unsafe/symlinked paths, and refuses non-empty directories. It must never delete
or overwrite caller-owned files. The release contains six Go targets:
Linux/macOS amd64 and arm64, plus Windows amd64 and arm64. It also includes
third-party license attribution and `SHA256SUMS`.

Tagged releases are built and signed in GitHub Actions. Installers verify both
checksums and Cosign blob signatures, including the expected GitHub Actions
certificate identity and OIDC issuer. Do not weaken those checks or introduce
an installer path that bypasses signature verification.

The release workflow keeps permissions least-privilege: the build job receives
OIDC signing access, installer verification is read-only, and only publication
receives `contents: write`. Keep third-party Actions pinned to immutable commit
SHAs when changing workflows.

Before changing release scripts, run shell syntax checks and the lifecycle
verifier:

```sh
bash -n scripts/*.sh
make release VERSION=v0.1.0 OUTPUT_DIR=/tmp/komposyn-release-test
bash scripts/verify-release.sh /tmp/komposyn-release-test
```

Use a fresh temporary or dedicated output path for each build. Do not point the
release builder at a home directory, repository root, parent directory, or a
directory containing unrelated files.

The release verifier exercises install, upgrade-by-replacement, and
data-preserving uninstall. The initial upgrade contract is executable
replacement by the latest standalone installer; it must preserve the database,
settings, and consent/data directory.

## Change and safety guidelines

- Inspect the worktree before editing and preserve unrelated user changes.
- Prefer `apply_patch` for tracked-file edits.
- Avoid destructive commands. Never recursively remove broad or unresolved
  paths. Confirm exact targets before deleting temporary artifacts.
- Keep changes scoped to the requested behavior and update documentation when
  contracts, commands, protocol behavior, or release assumptions change.
- Do not add network calls, telemetry, provider credentials, or external data
  transmission without an explicit design change and user consent.
- For security-sensitive changes, explain the threat model, add a regression
  test where practical, and run the relevant release or lifecycle checks.
