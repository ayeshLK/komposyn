# Issue #46 release and extraction readiness

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

The destination `ayeshLK/komposyn-lg` did not resolve through the current
GitHub credentials (`Could not resolve to a Repository`). This does not
distinguish a nonexistent repository from one hidden from this account; no
repository permission or protection changes were attempted.

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
PowerShell uninstall. Native macOS/Windows historical runs are still required
before counting those paths as verified.

The standalone destination bootstrap is prepared under
`standalone-bootstrap/` as a reviewable root-file template. It includes the
module pin, Apache attribution, scoped repository guidance, read-only CI,
CodeQL, Dependabot, and Release Please configuration. It contains no CLI
implementation or publication workflow; transfer the proven release machinery
and validate its exact standalone signing identity only after the verification
gates pass. GitHub still cannot resolve `ayeshLK/komposyn-lg`, so destination
creation/access remains unresolved.

## Remaining verification gates

- [x] Run the v0.5.0 and v0.6.0 historical active-collector upgrade fixtures
  on native macOS arm64 and Windows amd64, with signed historical archives,
  cooperative stop/restart, usage/settings preservation, and one healthy
  background lease owner.
- [x] Run full v0.6.0 historical migration, backup restoration, and uninstall
  verification on native macOS arm64 and Windows amd64. Linux amd64 evidence
  was already recorded above.
- [ ] Execute published binaries natively on the remaining release targets:
  Linux arm64, macOS amd64, and Windows arm64. Runtime evidence now covers
  Linux amd64, macOS arm64, and Windows amd64. Cross-builds and other hosted
  installer checks do not count as runtime execution for these pending targets.
- [x] Retain direct-upgrade and collector-state evidence from v0.5.0 on Linux
  amd64 and full historical state-preservation evidence from v0.6.0 on Linux
  amd64, macOS arm64, and Windows amd64.
- [x] Prepare reviewable standalone repository bootstrap files without
  copying CLI implementation. Destination existence/access remains blocked
  pending GitHub repository visibility or creation.
- [ ] After the remaining lifecycle and six-platform runtime evidence is
  complete: extract the CLI, verify and publish standalone, then remove legacy
  ownership. All cutover work remains deferred.
