# Issue #46 combined release preparation evidence

This checklist tracks implementation and local readiness evidence. A checked
item records a completed code/documentation task; it does not imply that the
final release was published or that post-publication verification is complete.

## Preparation increments

- [x] Revalidate handoff baseline: fetched `origin/master` at
  `10dcda83f817c7886d4e0592747bee2b17ba90b6`; work started from it on
  `issue-46-final-combined`.
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
- Historical lifecycle against published v0.7.0: the enhanced fixture now
  preserves a reviewed diagnostic from v0.6.0, verifies the state after
  migration, restarts/stops the candidate collector, restores the migration
  backup with the v0.6.0 executable, and compares the backup digest after
  uninstall. It passed on Linux amd64.

Cross-platform follow-up tooling: the verifier now accepts macOS `shasum` and
has a Windows Git Bash path for ZIP extraction, `.exe` invocation, and
PowerShell uninstall. Native macOS/Windows historical runs are still required
before counting those paths as verified.

## Remaining release gates

- [ ] Run the historical v0.6.0-to-v0.7.0 upgrade and rollback fixture on
  macOS and Windows; add representative historical binaries older than v0.6.0.
- [ ] Run the published binaries on all six target platforms. Linux amd64
  historical lifecycle passed; the release workflow verified installer
  lifecycles on Linux, macOS, and Windows runner hosts.
- [ ] After the remaining lifecycle and six-platform runtime evidence is
  complete: extract the CLI, verify and publish standalone, then remove legacy
  ownership. All cutover work remains deferred.
