# Release verification and publication

This repository releases the Go module source and the experimental temporary
chat executable named `komposyn` under one Release Please version. It does not
release the production `komposyn-lg` executable. The source tag remains usable
by Go module consumers; binary archives are additional release assets.

## Manual release lifecycle

Both Release Please operations and binary publication are explicitly
dispatched. A push to `master` and merging a release PR do not create tags or
publish assets.

1. From **Actions → Prepare or tag a reviewed source release**, choose
   `master` and run the workflow. This opens or updates the Release Please PR.
2. Review the proposed repository-wide version, changelog, module metadata, and
   release compatibility. Merge through the normal repository review and
   branch policy. That merge alone creates no tag.
3. Dispatch **Prepare or tag a reviewed source release** again from `master`.
   Release Please recognizes the merged PR and creates the immutable `vX.Y.Z`
   source tag and a draft GitHub release. The tag is created by Release Please;
   the release stays unpublished until signed binaries are ready. Do not create
   or move tags manually.
4. Resolve the full commit SHA at that tag and dispatch **Publish experimental
   komposyn binaries** from `master`, entering the numeric version (without
   `v`) and the exact 40-character tag commit SHA.
5. The workflow verifies the tag, manifest version, and source release; builds
   and runs native checks for all six supported targets; signs the aggregate
   checksum manifest; verifies it; adds all assets to the draft source release;
   and publishes the release only after confirming the complete asset set.

The binary workflow accepts only stable `X.Y.Z`, requires the supplied SHA to
match `vX.Y.Z`, and never moves tags. Its build, signing, and publication jobs
use separate least-privilege permissions. It does not use production provider
credentials or make live model requests.

## Artifact and signature contract

Each archive contains only the platform executable, `LICENSE`, `NOTICE`, and
`THIRD_PARTY_LICENSES.txt`:

| Target | Release archive |
| --- | --- |
| Linux amd64 / arm64 | `komposyn_X.Y.Z_linux_ARCH.tar.gz` |
| macOS amd64 / arm64 | `komposyn_X.Y.Z_darwin_ARCH.tar.gz` |
| Windows amd64 / arm64 | `komposyn_X.Y.Z_windows_ARCH.zip` |

The release also contains one `checksums.txt` with SHA-256 digests for all six
archives, plus the keyless Sigstore bundle `checksums.txt.sigstore.json` that
signs that manifest. Installers verify the signature and exact signer identity
before trusting a digest; they verify the selected archive digest, inspect the
archive layout and executable metadata, and only then stage replacement.

The expected signer identity is
`https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master`.
The OIDC issuer is `https://token.actions.githubusercontent.com`. This identity
is specific to this repository's manually dispatched workflow; it is distinct
from the production `komposyn-lg` signer identity. The workflow pins Cosign
v2.4.3, and installers require Cosign on `PATH`.

To verify a downloaded release set with Cosign v2.4.3:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
sha256sum --check checksums.txt
```

On macOS, `shasum -a 256 --check checksums.txt` can verify the digests after
Cosign verification. A checksum without a valid bundle and expected signer
does not establish release authenticity.

## Retry and recovery

If validation, signing, or publication fails after the tag exists, fix the
workflow issue and dispatch **Publish experimental komposyn binaries** again
with the same version and exact tag SHA. Builds are deterministic for a given
tag, commit, and target. Already uploaded assets are accepted only when their
bytes match the newly validated asset; the workflow never overwrites assets
and fails on mismatches. It can reuse a matching, previously verified checksum
manifest or bundle after a partial upload. Investigate any mismatch manually;
do not delete or replace release assets as an automatic recovery step.

Release Please remains responsible for source tags and the source release. Its
`draft` and `force-tag-creation` settings create a non-public source release
while ensuring the tag exists for validation/builds. The binary workflow adds
the six archives and signed checksum manifest, then publishes the draft.
Do not start another workflow from a `release:
published` event: GitHub suppresses workflow runs triggered by a release made
with `GITHUB_TOKEN`, and explicit dispatch is the recovery path.

## CI and scope

PR/master CI builds and runs candidate packages with read-only permissions,
fixture-based installer tests, and no signing secrets or publication access.
The release workflow repeats build and smoke validation on native Linux,
macOS, and Windows amd64/arm64 runners before requesting an OIDC certificate.
Fixture tests use a mock Cosign executable and are not cryptographic
verification. Genuine signed-artifact verification happens in the signing and
publication jobs after manual release dispatch.

The production `komposyn-lg` binary release lifecycle remains in
[ayeshLK/komposyn-lg](https://github.com/ayeshLK/komposyn-lg). Historical
combined releases through v0.7.0 retain their original artifact and signer
instructions in [historical combined release verification](historical-combined-releases.md).
