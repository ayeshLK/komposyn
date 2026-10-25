# Development guide

## Local setup

Install Go 1.26.10 or newer, clone the repository, and work from a feature
branch. This is the root module `github.com/ayeshLK/komposyn`.

## Useful commands

```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
make check
```

Focused library tests can be run while iterating:

```sh
go test ./plugins/library/token-logger -run TestName
```

The CI vulnerability check uses govulncheck v1.1.4 and fails for reachable
findings. Review any findings before release; do not silence them without a
recorded security rationale. `make check` runs formatting, module verification,
vet, and tests. Core CI also runs the public standalone CLI suite against the
candidate core with an isolated temporary module replacement and read-only
access. The normal CLI suite itself runs in the standalone repository.

## Release validation

Release Please owns the repository-wide module and experimental `komposyn`
version. Its workflow is manual-only; a merged release PR does not create a
tag. After review, manually dispatch Release Please from `master`, then
manually dispatch binary validation/signing/publication for that exact tag and
commit. Never create or move tags manually. See
[release verification](../release-verification.md),
[installation](../installation.md), and
[historical combined artifact verification](../historical-combined-releases.md).

## Final inspection

Before committing:

```sh
git diff --check
git status --short
git diff --stat
```

Review generated files and temporary artifacts. Do not commit local SQLite
files, WAL/SHM files, release archives, secrets, provider transcripts, or agent
metadata.
