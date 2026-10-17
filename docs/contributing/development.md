# Development guide

## Local setup

Install Go 1.26.9 or newer, clone the repository, and work from a feature
branch. The module pins Go 1.26.9 because earlier 1.26 patch releases have
known standard-library vulnerabilities.
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
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
make check
make build
```

Focused tests can be run while iterating:

```sh
go test ./plugins/library/token-logger -run TestName
go test ./plugins/cli/token-logger -run TestName
```

The CI vulnerability check uses govulncheck v1.1.4, pinned in the command, and
fails for reachable findings. Review any findings before release; do not
silence them with exclusions without a recorded security rationale.

`make check` runs formatting, vet, all tests, and a CLI build. Formatting may
modify Go files, so inspect the resulting diff. CI additionally checks module
verification, tests and builds on Linux, macOS, and Windows, runs Linux race
tests, and runs CodeQL separately.

## Release validation

Before changing release scripts or installers, set `CANDIDATE_VERSION` to the
candidate being prepared and run the following checks. This local build does
not create a tag.

```sh
bash -n scripts/*.sh
make release VERSION="$CANDIDATE_VERSION" OUTPUT_DIR=/tmp/komposyn-release-test
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
