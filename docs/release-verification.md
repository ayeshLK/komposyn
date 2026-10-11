# Release verification

Release Please prepares the version and changelog in a reviewed release pull
request. The later publication workflow will validate that prepared version,
build six platform artifacts, verify installation and data preservation, sign
artifacts with Cosign, and publish only after the verification jobs pass.

The initial unreleased baseline is `0.0.0`; the first release is expected to be
`v0.1.0`. Development binaries report `0.0.0-dev`, while release binaries
report the version injected from their tag.

## Publishing a release

Release publication is a two-step, manually dispatched GitHub Actions flow:

1. Run **Prepare release** from the Actions tab. Release Please opens or updates
   the release pull request with the next version and changelog entry.
2. Review and merge that pull request.
3. Run **Publish release** from the Actions tab, confirming that the issue #46
   API, schema/backup, diagnostics, and version gates are complete. The workflow
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

## Artifacts

Each release targets:

- Linux amd64 and arm64
- macOS amd64 and arm64
- Windows amd64 and arm64

The archive includes the executable, Apache-2.0 license files, README,
third-party license attribution, and `SHA256SUMS`. Signed checksum and archive
artifacts accompany the release.

## Local verification

Use a fresh output directory:

```sh
go vet ./...
go test ./...
go test -race ./...
make release VERSION=v0.1.0 OUTPUT_DIR=/tmp/komposyn-release-test
bash scripts/verify-release.sh /tmp/komposyn-release-test
```

The verifier checks checksums, archive contents, initial settings/database
creation, executable replacement, and uninstall. It confirms that upgrade and
uninstall preserve local data.

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
- [ ] Uninstall removes the executable but preserves local data.
- [ ] No release path bypasses signature verification.
- [ ] GitHub Actions permissions remain least-privilege and action references
      remain pinned to immutable commit SHAs.
