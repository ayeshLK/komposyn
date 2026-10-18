# Core source release verification

KompoSyn Core and `komposyn-lg` are independently versioned. Core releases are
Go module source tags and GitHub release notes; they do not build or publish
CLI binaries.

## Release path

The pinned Release Please workflow supports a reviewed release PR and source
release:

1. Run **Core source release** from GitHub Actions on `master` to open or
   update the Release Please PR.
2. Review its proposed version, changelog, module metadata, and compatibility
   notes, then merge the PR through the normal branch policy.
3. The resulting push to `master` runs Release Please to create the `vX.Y.Z`
   source tag and GitHub release notes.

Do not create or move tags manually. The module version is repository-wide;
individual packages do not receive separate versions. The standalone CLI
updates its pinned core dependency independently after reviewing the release.
See [the compatibility and security procedure](standalone-compatibility-security.md).

Normal CI is read-only. It runs core checks and, for candidate core changes,
clones the public standalone CLI and runs its suite against the candidate source
using a temporary `go.mod` replacement in the runner's temporary checkout. The
replacement is never committed, and the job has no cross-repository credential
or write permission.

## Historical combined releases

The historical combined releases through v0.7.0 included `komposyn-lg` binaries
and remain available. Their signer identity and verification instructions are
kept in [historical combined release verification](historical-combined-releases.md).
New CLI releases are signed only by the standalone repository workflow.
