# Development guide

## Local setup

Install Go 1.26.9 or newer, clone the repository, and work from a feature
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

Core releases are module source tags and release notes, prepared and published
by the pinned Release Please workflow. Do not build binary archives or create
or move tags manually. See [core release verification](../release-verification.md)
and [historical combined artifact verification](../historical-combined-releases.md).

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
