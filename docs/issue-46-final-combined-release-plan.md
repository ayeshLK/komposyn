# Issue #46: final combined release implementation handoff

Status: the final combined release was published as `v0.7.0`. Continue from
that accepted baseline, and use `docs/issue-46-readiness.md` for the current
verification ledger. PR #126 is included in the release ancestry; PR #130
adds verification changes after the extraction commit that must be transferred
with the CLI.

## Objective and agreed sequence

Complete verification of the published `ayeshLK/komposyn` v0.7.0 baseline,
then extract the CLI into `ayeshLK/komposyn-lg`. Keep the combined application
authoritative until standalone publication and verification.

The sections below preserve the original preparation checklist as a historical
handoff record. PR #126 and the v0.7.0 release completed the preparation work;
PR #130 extended verification afterward. Use
[issue-46-readiness.md](issue-46-readiness.md) as the current gate ledger and
do not treat the old unchecked boxes below as current status.
The [review](issue-46-review.md) records source evidence and rationale.
[Issue #46](https://github.com/ayeshLK/komposyn/issues/46) contains the accepted
architecture and original migration gates. The release-first-then-prepare
alternative discussed earlier has been superseded by the sequence above.

Read repository `AGENTS.md`, `LICENSE`, `NOTICE`, `CONTRIBUTING.md`,
`SECURITY.md`, `docs/architecture.md`, and the relevant normative docs before
editing. Preserve unrelated worktree files. Current untracked review/planning
documents may belong to other tasks; do not delete or silently commit them.

Keep one root module, `github.com/ayeshLK/komposyn`, and one version for its
reusable packages. Keep executable name `komposyn-lg`, protocol version 1,
existing data paths, consent, provider inputs and source-content privacy.

## Release identifiers

| Identifier | Value or selection rule |
| --- | --- |
| `UPGRADE_REFERENCE_TAG` | `v0.6.0` (`176d80e1a11c9c2e010afd585268ee356a891d37`) and representative older `v0.5.0` (`8543dc67cbce1d0e03a99e828a402bd69ca65f63`). |
| `CORE_BASELINE_TAG` | `v0.7.0`. |
| `FINAL_COMBINED_CLI_VERSION` | `0.7.0`. |
| `EXTRACTION_COMMIT` | `d66b4117d8b7771f9c3c22b3b251e19054ba9e88`. |
| Protocol version | `1`. |
| SQLite schema | `15`. |
| Proposed first standalone CLI | `v0.7.1`, requiring core `github.com/ayeshLK/komposyn v0.7.0`; version streams remain independent after the first standalone release. |

PR #126 (`43cb71014680abbe57011885a150604eef6b93a6`) is an ancestor of the
accepted v0.7.0 tag. PR #130 (`2c27610b0cfc3dc4d7ff55bfea0b405b810afbea`)
is later than `EXTRACTION_COMMIT`; transfer its verifier improvements in
`scripts/verify-release.sh`, `scripts/expand-release-archive.ps1`, and
`scripts/test-release-resolution.ps1`, plus the matching release evidence.
The current open native historical gates are listed in the readiness ledger.

## Historical preparation checklist (completed before the v0.7.0 baseline)

### PR 1 — Supported API and CLI settings boundary

Primary files: `plugins/library/token-logger/`,
`plugins/cli/token-logger/`, API documentation and external-consumer tests.

- [ ] Inventory every exported core symbol and actual CLI import. Classify
  supported domain/store/collector/provider/pricing/reporting/diagnostic APIs,
  CLI policy, and implementation details. Include identity/resource queries,
  source registry, leases, typed outcomes and pricing coverage APIs.
- [ ] Move settings/defaults/onboarding/auto-start policy into the current CLI
  package. Data-directory resolution already lives in CLI `datadir.go`.
- [ ] Preserve `settings.json` field names, legacy seconds/millisecond polling
  behavior, user-only permissions, atomic replacement and consent semantics.
- [ ] Keep registry persistence, transactional lease/heartbeat/stop/history
  operations and collector behavior in core; keep process spawning, terminal
  handling and environment/default-root selection in CLI.
- [ ] Document the concrete supported adapter API or define a small interface
  justified by current consumers. Avoid introducing a general plugin API.
- [ ] Review public migration/cursor/raw parsing APIs for privatization where
  practical. Document removals or transitional wrappers: published consumers
  already exist as a compatibility concern, even before v1.
- [ ] Add meaningful `package tokenlogger_test` contract tests and a temporary
  external-consumer build using only exported APIs. Keep existing internal
  tests for implementation coverage.

Acceptance: current commands and settings still work; library consumers need
no CLI import; supported API and intentional compatibility changes are
documented. No settings, consent or data-directory reset occurs.

### PR 2 — Schema compatibility and safe store opening

Primary files: library `storage.go`, `tokenlogger.go`, storage tests,
`docs/migrations.md`, and protocol compatibility documentation as needed.

- [ ] Define public database compatibility information: current schema,
  supported opening/migration range, and typed unsupported-schema errors.
- [ ] Inspect an existing database read-only before writable configuration,
  migration-table DDL, WAL activation, catalog synchronization or any other
  application write.
- [ ] Refuse a newer unsupported database without changing contents,
  timestamps or existing sidecars, or creating WAL/SHM sidecars.
- [ ] Handle new databases, in-memory databases, empty/invalid schema state,
  read failures and concurrent opens explicitly. Coordinate/recheck state
  before writable migration so preflight cannot become a stale safety check.
- [ ] Specify protocol version, event-format schema and SQLite schema as
  distinct contracts. Resolve what existing `schema_versions: [1,2,3,4]`
  advertises; generate its documented meaning from core information.
  Do not mechanically substitute SQLite versions 1–14 or silently restrict
  event validation, which currently accepts positive schema versions.

Acceptance: tests prove typed, write-free refusal, including existing WAL
states, and safe concurrent opening. The combined app still supports the full
existing migration history. PR 3 must complete before release.

### PR 3 — Verified pre-migration backup and recovery

Depends on PR 2. Primary files: core store/backup implementation, storage tests,
`docs/backups.md`, `docs/migrations.md`, and `docs/upgrades.md`.

- [ ] Use the pinned SQLite driver's online backup API to create a consistent
  pre-migration database backup. Do not copy a live DB/WAL pair.
- [ ] Provide a safe default inside the application data boundary and a
  caller-selected backup location without introducing CLI path policy in core.
- [ ] Record source schema, target schema, creation time and core version;
  verify completion, metadata and database integrity before migration.
- [ ] Fail closed if backup creation/verification fails. Do not overwrite
  existing backups or expose them more broadly than local usage data.
- [ ] Skip migration backups for new/in-memory databases and already-current
  databases. Define safe behavior for legacy nonempty schema-zero databases.
- [ ] Coordinate backup and migration with concurrent opens and collectors.
  Define the write boundary so a verified backup is the recoverable state
  immediately preceding migration, including committed WAL data.
- [ ] Keep numbered migrations immutable, ordered and transactional. Cover
  schemas 1–14 plus any newly added migration; test fresh creation, reopen,
  backup failure, migration failure and preservation of representative rows.
- [ ] Restore a backup into a separate working database and open it with a
  compatible released binary. Document executable versus database rollback
  and stopping all collectors before restoration.

Acceptance: a failed backup prevents migration; the archived backup remains
usable after a migration failure; restoration recovers verified historical
data. Older binaries cannot retroactively acquire write-free refusal, so
document their rollback limitations rather than claiming that guarantee.

### PR 4 — Destination-neutral diagnostics and CLI routing

Depends on backup support if it adds a migration. Primary files: core
`diagnostics.go`, CLI diagnostic flows, local export tests and security docs.

- [ ] Add stable CLI/provider/ingestion/storage/migration/pricing/reporting/
  validation/security-sensitive/unknown categories and persist needed metadata
  through a new numbered migration. Legacy diagnostics need a safe default.
- [ ] Keep sanitization, deduplication, local persistence, review state and
  destination-neutral report generation in core. Preserve fingerprint
  behavior where possible; document any necessary change.
- [ ] Move `GitHubIssueDraftURL` and tracker construction out of core, with an
  explicit compatibility decision for the published API.
- [ ] Route CLI/install/update/process failures to `komposyn-lg`; route
  provider/ingestion/storage/migration/pricing/reporting/core-validation
  failures to `komposyn`; unknown cases default to CLI triage.
- [ ] Block public draft routing for suspected vulnerabilities or sensitive
  exposure and provide private `SECURITY.md` instructions.
- [ ] Keep explicit review/approval before external issue-draft actions;
  never submit automatically. Test both REPL and noninteractive paths.
- [ ] Include optional CLI/core/protocol/schema metadata in report contracts;
  finish wiring version values in PR 5.
- [ ] Preserve local ZIP export's exclusive creation, permissions and sanitized
  content. Explicitly document local pending export versus reviewed external
  disclosure; do not silently change export selection as part of extraction.

Acceptance: routing/category/version behavior is tested, privacy is preserved,
and core report generation has no hardcoded tracker destination. Coordinate
contracts with proposed #112; its full browsing/active-history UI is outside
this release-preparation scope.

### PR 5 — Separate CLI/core version and build metadata

Depends on the compatibility APIs and diagnostic report contract. Primary
files: CLI `main.go`, `protocol.go`, `watch.go`, core version API decisions,
release builder, lifecycle verifier and `docs/HARNESS_PROTOCOL.md`.

- [ ] Add CLI-owned version metadata with explicit development defaults.
  Record the exact core version in combined and future standalone builds.
- [ ] Keep protocol-v1 `plugin_version` as core; add `cli_version` additively.
- [ ] Make `version` output and interactive banner identify the CLI and core.
- [ ] Use CLI version for collector `executable_version` in background,
  foreground and embedded modes. Wire the report version tuple from PR 4.
- [ ] Resolve the fate of published mutable `tokenlogger.Version`; document
  compatibility effects instead of relying on the obsolete “before first
  tag” assumption.
- [ ] Update build injection and verification for separate CLI/core versions.
  During the combined release they match the release version; standalone
  builds will obtain core provenance from their pinned module dependency.
- [ ] Verify development, combined-release and simulated unequal CLI/core
  versions. Validate exact provenance and reject unintended local replacement
  or development dependencies in standalone release checks.
- [ ] Test protocol clients for the additive field and preserve methods,
  stable errors, input limits, stdout purity and metadata/event atomicity.

Acceptance: no executable-version surface accidentally reports the core
version when the two differ; `plugin_version` retains its existing meaning.

### PR 6 — Real upgrades, release evidence and documentation

Depends on PRs 1–5. Reuse existing release machinery rather than replacing it.

- [ ] Extend verification beyond copying a candidate binary over itself.
  Upgrade verified released binaries/data from `v0.6.0` and representative
  older versions to the candidate through the installer lifecycle.
- [ ] Assert actual data: usage totals, aliases/identities, pricing provenance,
  source cursors, registry, run history, settings, consent, diagnostics/review
  state and backups. File-existence checks alone are insufficient.
- [ ] Test collector-aware replacement: cooperative stop, one lease owner,
  background restart only when previously active, and no automatic foreground
  or embedded restart. Cover Unix and Windows behavior.
- [ ] Verify uninstall preserves owned data and backups, and stops collection
  safely. Verify supported rollback/refusal/restoration scenarios.
- [ ] Preserve command/report/resource/protocol behavior, help/completions,
  platform terminal behavior, integer JSON/CSV values, pricing evidence and
  unknown/partial calculations. Capture reusable fixtures for extraction.
- [ ] Add dependency vulnerability checking with a reproducibly pinned tool
  and reviewed handling of findings; retain CodeQL and least-privilege CI.
- [ ] Update API, architecture, support/security, configuration, migration,
  backup, rollback, release and contributor docs. Correct stale pre-release
  statements and explain the upcoming ownership transfer.
- [ ] Clarify `confirm_issue_46_gates` as readiness to extract after the final
  combined release, not completion of standalone publication and cleanup.
  Keep the gate and complete its prerequisites; do not bypass signing,
  protected publication or release-PR validation.
- [ ] Record candidate test results and security analysis, six-platform archive
  contents, licenses, checksums, signing/trust checks and actual installer
  results. Keep third-party Actions pinned to immutable SHAs.

Acceptance: another reviewer can reproduce final combined release readiness
without provider transcripts, credentials or private source content. Any
unavailable platform/security validation remains explicitly pending.

## Validation and publication gates

Run focused tests while iterating, then the repository gates:

```sh
go mod verify
go vet ./...
go test ./...
go test -race ./...
make check
bash -n scripts/*.sh
```

Review formatting changes made by `make check`. Build candidate archives in a
new, dedicated output directory and run the lifecycle verifier:

```sh
release_test_dir="$(mktemp -d /tmp/komposyn-final-combined.XXXXXX)"
make release VERSION="$CANDIDATE_TAG" OUTPUT_DIR="$release_test_dir"
bash scripts/verify-release.sh "$release_test_dir"
```

Set `CANDIDATE_TAG` to the reviewed candidate version before running the build;
do not create a tag manually. The verifier alone does not establish signature,
cross-version or all-platform coverage: record the additional checks above.
Before tagging, build a clean external consumer against the candidate with a
temporary module override outside tracked repository files.

Publication remains the existing maintainer-controlled workflow: Prepare
release → review and merge the generated release PR → Publish release with
truthful prerequisite confirmation and protected-environment approval. This
handoff requests implementation/readiness work; it does not instruct another
agent to publish, merge, tag or change repository permissions automatically.

After publication, record the identifiers, exact source commit, CLI/core/
protocol/schema tuple, checksums and signing identity. Verify the published
signed installer and a clean external consumer requiring `CORE_BASELINE_TAG`
without a `replace` directive. These post-publication checks block extraction.

## Deferred until after the final combined release

Do not remove/copy the CLI, transfer signing ownership, change installer URLs
or trust identity, delete workflows, or declare the new repository authoritative
as part of these preparation PRs. Those belong to the extraction phase.
Do not bundle full #111/#112 workflows, analytics, identity repair, task
tracking, a general backup manager, telemetry or a self-updater into this work.

After the final combined release is verified, freeze the extraction surfaces,
extract from its recorded commit, pin its core tag, verify standalone parity
and direct upgrades from existing releases, publish standalone, then clean up
the original repository. Security fixes override the scoped freeze.

## Copyable agent handoff

> Implement the pending final combined release preparation described in
> `docs/issue-46-final-combined-release-plan.md`. Read repository instructions
> and the linked review first, refresh remote master, preserve unrelated work,
> and begin with PR 1's API inventory/settings boundary. Complete changes in
> focused, usable increments with the stated tests and documentation. Keep
> the CLI in this repository throughout preparation. Track each acceptance
> criterion and report evidence and unresolved gates. Do not publish a release,
> remove the CLI, or start cutover as part of this implementation task.
