# Release verification

Release Please prepares the version and changelog in a reviewed release pull
request. The later publication workflow will validate that prepared version,
build six platform artifacts, verify installation and data preservation, sign
artifacts with Cosign, and publish only after the verification jobs pass.

The combined repository has published signed releases. Use the current
Release Please manifest and merged release PR as the version authority; do not
reset the combined version history during issue #46 preparation. Development
binaries report `0.0.0-dev`, while release binaries report the version
injected from their tag. The core and CLI versions remain separate fields even
when combined releases assign them the same tag.

## Publishing a release

Release publication is a two-step, manually dispatched GitHub Actions flow:

1. Run **Prepare release** from the Actions tab. Release Please opens or updates
   the release pull request with the next version and changelog entry.
2. Review and merge that pull request.
3. Run **Publish release** from the Actions tab, confirming that this final
   combined release passed the issue #46 readiness gates and is ready for the
   post-release extraction phase. The workflow
   checks out the release pull request's merge commit, runs the test and
   lifecycle verification matrix, builds and signs the artifacts, creates a
   draft GitHub Release at that exact commit, uploads the explicit artifact
  file set, verifies the exact tag, reviewed release notes, and asset set, and
  publishes the release.

The publish workflow must be run after the release pull request is merged. It
does not run from a tag push, which prevents a release from recursively
triggering another release workflow. A failed upload leaves a matching draft
release available for retry; a published or mismatched tag is rejected. The
`release` environment should require the repository's release approval policy
before the final publication job can run.

If a release-build fix is merged after the Release Please pull request but
before publication, rerun **Publish release** with `release_commit` set to the
fix commit on `master`. The workflow verifies that this commit descends from
the merged release PR and is reachable from `master`; omit the input for the
normal path.

## Artifacts

Each release targets:

- Linux amd64 and arm64
- macOS amd64 and arm64
- Windows amd64 and arm64

The archive includes the executable, Apache-2.0 license files, README,
third-party license attribution, and `SHA256SUMS`. Signed checksum and archive
artifacts accompany the release.

## Local verification

Use a fresh output directory and set `CANDIDATE_VERSION` to the reviewed
candidate version. The local build does not create a tag.

```sh
go vet ./...
go test ./...
go test -race ./...
make release VERSION="$CANDIDATE_VERSION" OUTPUT_DIR=/tmp/komposyn-release-test
bash scripts/verify-release.sh /tmp/komposyn-release-test
# For an actual old-to-new migration, pass a verified released komposyn-lg binary:
bash scripts/verify-release.sh /tmp/komposyn-release-test /path/to/verified-v0.6.0/komposyn-lg
```

The verifier checks checksums, archive contents, initial settings/database
creation, executable replacement, and uninstall. With a verified historical
binary supplied, it imports a synthetic Codex event and records a second event
with project/session references through protocol v1. It starts and cooperatively
stops the old binary's collector, upgrades with the candidate, and checks
integer usage totals, project/session identity, unchanged-source cursor reuse,
collector registration and run history, exact settings preservation, unknown
pricing evidence, and the retained automatic migration backup. It restores
that backup into a separate data directory and checks the historical total
again. This path requires Python 3 for its SQLite run-history assertion.
Without a reference binary, upgrade coverage is a lifecycle smoke test only.
The verifier also checks uninstall preserves local data and backups. The
smoke-test path starts a registered background collector, stops/restarts it
across replacement, and verifies uninstall stops it before removing the
executable.

To exercise an active historical collector across a real signed installer
upgrade to a published release, run on native Linux or macOS:

```sh
bash scripts/verify-active-published-upgrade.sh v0.6.0 v0.7.0
bash scripts/verify-active-published-upgrade.sh v0.5.0 v0.7.0
```

On native Windows amd64, use the PowerShell equivalent:

```powershell
./scripts/verify-active-published-upgrade.ps1 -OldTag v0.6.0 -NewTag v0.7.0
./scripts/verify-active-published-upgrade.ps1 -OldTag v0.5.0 -NewTag v0.7.0
```

Both scripts verify the historical archive and checksum manifest signatures,
start the old background collector, install the published target through the
normal signature-verifying installer, and check that it restarts under a new
process/instance with one running background lease owner. They compare durable
usage totals and settings and stop the collector during cleanup. These tests
must run on native platforms; cross-compilation is not runtime evidence.
`.github/workflows/issue-46-historical-upgrade.yml` runs the v0.5.0 and v0.6.0
cases on native macOS and Windows for pull requests that change this verifier.

For installer verification, `scripts/install.sh` verifies both SHA-256 and the
Cosign blob certificate identity and OIDC issuer before replacing the binary.
The PowerShell installer follows the same release verification contract on
Windows.

## Release review checklist

- [ ] Version is a `v*` tag and artifact names are expected.
- [ ] All six target archives are present.
- [ ] Checksums and Cosign signatures verify.
- [ ] Certificate identity and OIDC issuer are enforced.
- [ ] Installer replacement preserves database, settings, and consent state.
- [ ] A running background collector stops before replacement and restarts
      afterward; registry and runtime history remain intact.
- [ ] Uninstall removes the executable but preserves local data.
- [ ] No release path bypasses signature verification.
- [ ] GitHub Actions permissions remain least-privilege and action references
      remain pinned to immutable commit SHAs.
