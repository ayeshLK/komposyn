# Development guide

## Local setup

Install Go 1.26 or newer, clone the repository, and work from a feature branch.
The module is `github.com/ayeshLK/komposyn`; the CLI is built from
`plugins/cli/token-logger` and produces `komposyn-lg`.

Keep local build output in the ignored `bin/` directory. Use a temporary
directory for release output rather than the repository root.

## Useful commands

```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
make check
make build
```

Focused tests can be run while iterating:

```sh
go test ./plugins/library/token-logger -run TestName
go test ./plugins/cli/token-logger -run TestName
```

`make check` runs formatting, vet, all tests, and a CLI build. Formatting may
modify Go files, so inspect the resulting diff. CI additionally checks module
verification, tests and builds on Linux, macOS, and Windows, runs Linux race
tests, and runs CodeQL separately.

## Release validation

Before changing release scripts or installers, run:

```sh
bash -n scripts/*.sh
make release VERSION=v0.1.0 OUTPUT_DIR=/tmp/komposyn-release-test
bash scripts/verify-release.sh /tmp/komposyn-release-test
```

Use a fresh output directory. The verifier covers installation, executable
replacement, and data-preserving uninstall. Do not weaken checksum or Cosign
verification.

## Final inspection

Before committing:

```sh
git diff --check
git status --short
git diff --stat
```

Review generated files and temporary artifacts explicitly. Do not commit local
SQLite files, `-wal`/`-shm` files, release archives, secrets, or agent metadata.
