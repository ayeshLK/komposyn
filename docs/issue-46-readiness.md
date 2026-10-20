# Issue #46 release and extraction readiness

> **Archived preparation ledger.** Its proposed v0.7.1 baseline and unchecked
> pre-publication items are historical. Standalone CLI v0.8.0 was published
> and verified at source commit `9369bc327e70928c69b332a4aa54ad38de5a226c`;
> current release evidence is maintained in the
> [standalone evidence ledger](https://github.com/ayeshLK/komposyn-lg/blob/master/docs/issue-46-release-evidence.md).
> The current post-release cutover checklist is issue #46 itself.

This checklist tracks implementation and local readiness evidence. A checked
item records a completed code/documentation task; it does not imply that
standalone extraction or publication is ready.

## Accepted baseline and revalidated PR status

| Identifier | Accepted value |
| --- | --- |
| Core baseline tag | `v0.7.0` |
| Published source and extraction commit | `d66b4117d8b7771f9c3c22b3b251e19054ba9e88` |
| Combined CLI version | `0.7.0` |
| Protocol version | `1` |
| SQLite schema | `15` |
| Proposed first standalone CLI | `v0.7.1`, pinned to core `v0.7.0`; subsequent CLI/core versions remain independent |

PR #126 is merged at `43cb71014680abbe57011885a150604eef6b93a6`; its
preparation changes are ancestors of the v0.7.0 release commit. PR #130 is
merged at `2c27610b0cfc3dc4d7ff55bfea0b405b810afbea` and extends verification
after the recorded extraction commit. Its historical-verifier and Windows
archive fixes must be transferred with the CLI. The current checkout fetched
`origin/master` at that PR #130 merge commit before follow-up work.

The destination `ayeshLK/komposyn-lg` is public and was empty when access was
revalidated. Its initial `master` branch is commit
`5f2956468cdc3ad634997552c984f6408f06c704`; the extracted CLI review is
[draft PR #1](https://github.com/ayeshLK/komposyn-lg/pull/1), currently at
`a4394df877f7e4173296ba7ae4e4d8b7830f37b0`. No repository permission or
protection settings were changed.

## Preparation increments

- [x] Revalidate handoff baseline: fetched `origin/master` at
  `2c27610b0cfc3dc4d7ff55bfea0b405b810afbea` (PR #130 merge); the current
  follow-up branch `issue-46-follow-up` is based on that commit.
- [x] 1. Inventory supported core APIs, add an external-package API contract
  test and move settings policy into the CLI. Document intentional pre-1.0 API
  removals.
- [x] 2. Add read-only schema preflight and typed rejection of unsupported
  newer schemas; regression checks compare database and SQLite sidecar bytes.
- [x] 3. Add verified online pre-migration backups, non-overwriting explicit
  destinations, metadata/integrity verification, and fail-closed tests.
- [x] 4. Move tracker routing into the CLI, persist destination-neutral
  diagnostic metadata, and block public routing for security-sensitive reports.
- [x] 5. Keep protocol version 1 and core `plugin_version`, add `cli_version`,
  and inject independent CLI/core build versions.
- [x] 6. Add the old-binary upgrade verifier, run a signed v0.6.0 Linux
  binary-to-candidate upgrade, and correct release/security/contributor docs.
- [x] 7. Distinguish backup-creation failure from migration-transaction
  failure. The regression proves a failed migration rolls back schema changes
  and preserves the original database and verified pre-migration backup.

## Verified evidence

- Toolchain: Go 1.26.9, pinned by `go.mod` and CI/release workflows.
- `make check`, `go mod verify`, `go test -race ./...`, `bash -n scripts/*.sh`:
  passed on Linux.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...`: no known
  vulnerabilities on Go 1.26.9. The earlier Go 1.26.2 scan found standard
  library fixes, which drove the toolchain pin update.
- PR #126 checks passed after the Windows SQLite file-URI fix: Analyze Go,
  CodeQL, Linux/macOS/Windows test jobs, and race tests.
- Local PR-review regressions: `go test ./...`, `go test -race ./...`,
  `go vet ./...`, `make check`, and `bash -n scripts/*.sh` passed. This
  includes refusal through a symlink to a newer WAL-backed database without
  changing the target database or sidecars, diagnostic category escalation
  and collector metadata/routing, and compatible-binary backup restoration.
- `TestMigrationFailureLeavesOriginalAndVerifiedBackupUsable` injects a
  failure while recording migration 2 after the online backup succeeds. It
  verifies the original and backup remain integrity-check clean at schema 1
  with the seeded project intact and no partial diagnostics table, then proves
  both snapshots can migrate after removing only the injected test trigger.
  `TestMigrationBackupFailureLeavesSchemaUnmigratedAndPreservesDestination`
  separately covers backup-creation failure before migration starts.
- External consumer test importing the public core package with a temporary
  local module replacement: passed.
- Published consumer test importing `github.com/ayeshLK/komposyn@v0.7.0`
  without a `replace` directive: passed from an isolated module outside the
  repository.
- Six-platform candidate archives, license bundle, and SHA-256 manifest:
  built. Local installer trust acceptance/rejection tests passed.
- Historical fixture: downloaded v0.6.0 Linux amd64 archive, verified its
  checksum and Cosign identity/issuer, imported a synthetic Codex source, and
  recorded a second event with project/session references. The candidate
  preserved the 123-token total, project/session identity, unchanged-source
  cursor reuse, settings, registered source, stopped collector run history,
  unavailable pricing evidence, and migration backup. Restoring that backup
  into a clean data directory recovered the total; uninstall preserved both
  database and backup. This covers Linux amd64 only. The six local candidate
  archives were rebuilt after the recovery fix at
  `/tmp/komposyn-issue46-final.BZ9kaW`.
- Updated release lifecycle verifier: built six candidate archives at
  `/tmp/komposyn-pr126-review-release`, restored the verified migration backup
  with the supplied v0.6.0 executable, and confirmed uninstall preserved the
  backup digest and local data. This remains Linux amd64 historical-binary
  evidence.
- Published v0.7.0 release: all six archive checksums and Cosign signatures,
  plus the signed `SHA256SUMS`, verified against the release workflow identity
  and GitHub OIDC issuer. Required executable, documentation, and license
  entries were present in all six archives. The successful [Publish release
  workflow](https://github.com/ayeshLK/komposyn/actions/runs/39029735058)
  also ran installer upgrade/uninstall checks on Linux, macOS, and Windows.
- Independently revalidated the published release from a fresh download in
  `/tmp/komposyn-v070-audit.R9jIls`: all six archive checksums and Cosign
  signatures, and the signed `SHA256SUMS`, passed. The archive checks required
  executable, `LICENSE`, `NOTICE`, `README.md`, `THIRD_PARTY_LICENSES.md`, and
  third-party license files in each archive. The `SHA256SUMS` asset digest is
  `82057898bf5906585087108ed0a29db32fe99226a23b913d671e300c0df482e2`.
  The exact signing identity is
  `https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master`
  with issuer `https://token.actions.githubusercontent.com`.
- Published archive SHA-256 values, independently checked against `SHA256SUMS`:

  | Target | SHA-256 |
  | --- | --- |
  | macOS amd64 | `b0af004ab8028d8ce1980801bf2ecde47e6c56acfaba4e14026abee9f11fa30b` |
  | macOS arm64 | `99c563e939328adf6a8e4d5cc5d22edfaba9e751036c1d1e360a27447e77a76b` |
  | Linux amd64 | `a1e1930d131a5f235beda80bbbb19ca956485ebaac819cbdeb1082bf665e79f9` |
  | Linux arm64 | `0470314209d677ab6c5074507f3b199f58b88bef69dec70c4568e516759f1760` |
  | Windows amd64 | `06edc6ea9756221791d95442cc5d5567c2a29439f34f26cf84edb53b7725cf38` |
  | Windows arm64 | `c630e827c6adbd169f787f8783122050bf899e33c53bf6600fa4cbe120f8662b` |
- Executed the published Linux amd64 binary: `komposyn-lg version` reported
  CLI/core `0.7.0`; a protocol-v1 `initialize`/`shutdown` exchange succeeded
  and reported `plugin_version: 0.7.0`, `cli_version: 0.7.0`. This is runtime
  evidence for Linux amd64 only.
- Historical lifecycle against published v0.7.0: the enhanced fixture now
  preserves a reviewed diagnostic from v0.6.0, verifies the state after
  migration, restarts/stops the candidate collector, restores the migration
  backup with the v0.6.0 executable, and compares the backup digest after
  uninstall. It passed on Linux amd64.
- Re-ran the full historical v0.6.0-to-v0.7.0 `scripts/verify-release.sh`
  lifecycle on Linux amd64 against the published v0.7.0 archive and verified
  v0.6.0 binary. Usage, project/session identities, cursor reuse, settings,
  collector registry/history, pricing evidence, diagnostic review state,
  migration backup restoration, and data-preserving uninstall passed.
- Ran `scripts/verify-active-published-upgrade.sh` with the signed v0.7.0
  installer and verified v0.6.0 and v0.5.0 binaries on Linux amd64. In both
  runs the old background collector was cooperatively stopped, the signed
  release replaced it, and collection restarted under a new PID/instance with
  one running background lease owner. Usage totals and settings were
  preserved. Both historical archive and checksum manifest signatures were
  checked against the release identity and issuer above.
- PR #131's native workflow passed all four signed active-collector upgrades:
  v0.5.0 and v0.6.0 to v0.7.0 on macOS arm64 and Windows amd64. The old
  collector stopped cooperatively, the installer replaced it and restarted a
  new healthy background lease owner, and usage/settings were preserved. This
  also executes the published v0.7.0 binary natively on those two targets.
- PR #131's full v0.6.0-to-v0.7.0 fixture passed on macOS arm64 and Windows
  amd64. It verified migration, preserved usage and settings/consent, restored
  the migration backup with the compatible historical executable, and
  completed data-preserving uninstall. The completed native evidence is in
  [workflow run 39037544184](https://github.com/ayeshLK/komposyn/actions/runs/39037544184).

Cross-platform follow-up tooling: the verifier now accepts macOS `shasum` and
has a Windows Git Bash path for ZIP extraction, `.exe` invocation, and
PowerShell uninstall. Native macOS/Windows historical runs are complete and
passed as recorded below.

The follow-up workflow also has a six-target native runtime matrix for Linux
amd64/arm64, macOS amd64/arm64, and Windows amd64/arm64. It verifies the
published checksum manifest and archive signatures, checks the target archive
checksum, runs the executable's version command, and exchanges protocol-v1
initialize/shutdown messages. Linux amd64 passed locally; PR #131 run
39038326152 passed on the other five targets.
The first Windows arm64 attempt reached the native runner but the pinned
Cosign installer rejected its `ARM64` architecture before running the
verifier. The workflow now builds pinned Cosign v2.4.3 with Go 1.26.9 on that
native runner, and the retry passed on Windows arm64.

The temporary standalone repository bootstrap was prepared in this repository
for extraction; it was not CLI source and is no longer needed now that the
standalone repository owns the executable and its release lifecycle. The
extracted CLI was reviewed in destination PR #1. It preserves the CLI layout,
transfers PR #130 verification fixes, and pins core v0.7.0 without a permanent
`replace`; the build reports CLI 0.7.1 and core 0.7.0. Local tests,
vet, module verification, race tests, `make check`, shell syntax, exact signer
acceptance/rejection tests, and a fresh six-archive build plus install/upgrade/
uninstall verifier passed. Destination CI, CodeQL, and native Go test/build
jobs passed on Linux amd64, macOS, Windows amd64, and Windows ARM64 in
[run 39047984391](https://github.com/ayeshLK/komposyn-lg/actions/runs/39047984391)
and [CodeQL run 39047984349](https://github.com/ayeshLK/komposyn-lg/actions/runs/39047984349).
The first Windows run exposed CRLF checkout behavior in `gofmt`; `.gitattributes`
now keeps Go sources LF and the rerun passed.

## Remaining verification gates

- [x] Run the v0.5.0 and v0.6.0 historical active-collector upgrade fixtures
  on native macOS arm64 and Windows amd64, with signed historical archives,
  cooperative stop/restart, usage/settings preservation, and one healthy
  background lease owner.
- [x] Run full v0.6.0 historical migration, backup restoration, and uninstall
  verification on native macOS arm64 and Windows amd64. Linux amd64 evidence
  was already recorded above.
- [x] Execute published binaries natively on all six targets: Linux amd64 and
  arm64, macOS amd64 and arm64, and Windows amd64 and arm64. The published
  artifact signatures/checksums, CLI/core version output, and protocol-v1
  initialize/shutdown passed on every target in
  [workflow run 39038326152](https://github.com/ayeshLK/komposyn/actions/runs/39038326152).
- [x] Retain direct-upgrade and collector-state evidence from v0.5.0 on Linux
  amd64 and full historical state-preservation evidence from v0.6.0 on Linux
  amd64, macOS arm64, and Windows amd64.
- [x] Prepare destination bootstrap and standalone extraction, then open
  [draft PR #1](https://github.com/ayeshLK/komposyn-lg/pull/1). All destination
  CI and CodeQL checks pass on its current head.
- [x] Rerun exact signer trust tests, the six-archive build, and the
  host-native install/upgrade/uninstall verifier after final trust-test
  hardening.
- [ ] Run standalone historical upgrades from v0.7.0 and earlier supported
  versions, including a still-active background collector, on native
  platforms. Combined-repository lifecycle evidence does not substitute for
  this destination evidence.
- [ ] Verify standalone signed release artifacts and exact new-workflow
  identity on all six targets; exercise install, restore, upgrade, and
  data-preserving uninstall. No standalone release has been published.
- [ ] Keep the combined repository authoritative until signed standalone
  publication and verification pass; prepare legacy cleanup separately.
