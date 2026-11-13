# Token Logger package relocation

Scope, final compatibility policy, core-first rollout order, and the expected
downstream CI failure are agreed. The consolidated implementation plan was
confirmed. The package relocation is implemented; validation is recorded below.

## Agreed scope

Relocate `plugins/library/token-logger` to `plugins/token-logger`, preserving
the `tokenlogger` package name, exported API, behavior, database schema, single
Go module, and repository-wide versioning. Internal restructuring is outside
this change. The final layout will intentionally remove the old import path,
with a coordinated standalone CLI migration and explicit migration guidance.
See [ADR-0004](adr/0004-relocate-token-logger-package.md).

## Consumer investigation — 2026-11-13

- Within core, the only Go import of the old package path is its external API
  test. The experimental chat does not import Token Logger.
- The standalone production CLI is a confirmed consumer. At commit
  `a24e14b9fa729140cb77d6e1caa562ccf92fac47`, it has 32 Go imports of the old
  path: 13 production files and 19 tests. Its
  [go.mod](https://github.com/ayeshLK/komposyn-lg/blob/a24e14b9fa729140cb77d6e1caa562ccf92fac47/go.mod) requires
  core v0.10.0. Its
  [release script](https://github.com/ayeshLK/komposyn-lg/blob/a24e14b9fa729140cb77d6e1caa562ccf92fac47/scripts/build-release.sh)
  injects `github.com/ayeshLK/komposyn/plugins/library/token-logger.Version`
  through linker flags, in addition to its source imports of that package.
  Its `TestUnequalCLICoreVersionsRemainSeparateAcrossSurfaces` test also
  mutates `tokenlogger.Version` and checks the resulting reported core version.
  A copied variable with a forwarded `CoreVersion` function would break this
  actual consumer behavior.
- GitHub public code search for the exact old import path returned 41 files
  across only `ayeshLK/komposyn` and `ayeshLK/komposyn-lg`. Excluding those
  repositories returned zero matches. A search for the core module path in
  other repositories' `go.mod` files also returned zero matches. These searches
  cannot rule out private, unindexed, or dynamically constructed consumers.
- Core compatibility CI tests the public CLI default branch against candidate
  core via a temporary module replacement. Removing the old package while
  that branch still imports it would break this check.
- Baseline scripts use both candidate and pinned historical package paths.
  Update candidate references while retaining the historical paths used in
  old worktrees. Move embedded pricing data and test fixtures with the package.

## Compatibility decision and rollout order

A breaking move requires coordinating CLI source imports, release linker
flags, dependency upgrades, and the compatibility gate. A deprecated forwarding
package can preserve most source compatibility through type aliases and
function wrappers, but Go does not provide ordinary cross-package aliases for
mutable variables. In particular, copying `Version` into a new variable does
not preserve existing linker injection or mutation semantics automatically.

The agreed policy is a coordinated import-path break rather than maintaining
a permanent compatibility package. CLI imports, release linker target, and
tagged core dependency must migrate together. Preserve compatibility testing.

Release core with the new path in a newer version, without a bridge release.
Migrate the CLI in a separate thread afterward. Existing CLI releases and
builds retaining their pinned old core dependency are unaffected; upgrading
to the new core release requires migrating imports and linker flags.

Core CI is different: its compatibility job replaces the CLI's pinned core
dependency with the candidate checkout. Accept the expected failure between
the core path removal and the CLI default-branch migration, keep the check
enabled, and document the reason. This acceptance does not authorize silently
changing branch protection or bypassing required-check restrictions; handle
any such restriction explicitly if encountered.

## Consolidated implementation plan

- Move the complete package, tests, embedded pricing catalog, and test fixtures
  to `plugins/token-logger`; keep `package tokenlogger` and remove the old path.
- Preserve API declarations, version-variable behavior at the new path,
  runtime behavior, schema, database locations, and module identity. Do not
  introduce a nested module, compatibility package, or internal redesign.
- Update active imports, documentation, and candidate-side script paths.
  Preserve paths referring to pinned historical source trees or evidence.
- Add migration guidance covering the import path and release linker target;
  prepare a worker handoff for the separate CLI migration, without changing
  the CLI repository in this task.
- Run module verification, vet, all tests, race tests, and `make check`, plus
  the affected historical-baseline scripts. Report the expected downstream
  compatibility failure separately from unexpected failures.
- Let Release Please determine the repository-wide version and changelog.
  Follow the documented release PR review, merge, and manual workflow dispatch
  sequence; do not create or move tags manually.

## Handoff for the separate CLI migration

Work in `ayeshLK/komposyn-lg` after core publishes a tagged release containing
the new package path:

1. Read that repository's current agent and contribution instructions. Confirm
   the published core tag actually contains `plugins/token-logger`.
2. Upgrade `github.com/ayeshLK/komposyn` to that exact release, without leaving
   a local `replace` directive in committed module files.
3. Replace active imports of
   `github.com/ayeshLK/komposyn/plugins/library/token-logger` with
   `github.com/ayeshLK/komposyn/plugins/token-logger`. At the investigated CLI
   revision this affects 13 production files and 19 tests; search again rather
   than relying on those counts. Retain `tokenlogger` as the package name.
4. Update `scripts/build-release.sh` to inject the new package's `Version`
   symbol. Search other active build and test scripts for the old linker
   target. Preserve historical evidence referring to old core revisions.
5. Run the CLI's required checks. Verify that distinct CLI/core versions still
   appear correctly in version output, protocol metadata, and diagnostics,
   including `TestUnequalCLICoreVersionsRemainSeparateAcrossSurfaces` and a
   build using the new linker target. Confirm stored data and protocol-v1
   semantics remain unchanged.
6. After the migration merges to the CLI default branch, rerun core's
   unchanged Standalone CLI compatibility job against the relocated core.

Keep this follow-up limited to source/dependency/build-target migration. Do not
move production CLI policy into core or introduce a dependency from core on
the CLI.

## Local validation — 2026-11-13

Checks used Go 1.26.11 and a writable temporary `GOCACHE` on Linux amd64.

| Check | Result |
| --- | --- |
| `go mod verify` | Passed |
| `go vet ./...` | Passed |
| `go test ./...` | Passed |
| `go test -race ./...` | Passed |
| `make check` | Passed |
| `bash scripts/verify-collector-baseline.sh` | Passed; matches pinned merged baseline |
| `bash scripts/verify-append-checkpoint-baseline.sh` | Passed; matches v0.7.1 baseline |
| `BENCH_REPEAT_COUNT=1 bash scripts/benchmark-append-checkpoint.sh` | Passed for historical and relocated packages; smoke verification, not a performance comparison |
| `git diff --check` | Passed |

All 62 package files were compared with the original tree: contents are
identical except for the external API test's import path. Relative links in
changed Markdown files resolve. An isolated executable importing the new path
verified linker injection into `Version` and that subsequent mutation remains
visible through `CoreVersion()`.

An isolated checkout of the investigated CLI revision was pointed at candidate
core with a temporary module replacement. Dependency resolution failed because
candidate core does not contain the old package, as expected. An offline
`go list -mod=mod github.com/ayeshLK/komposyn/plugins/library/token-logger`
reproduced the missing-package error without fetching dependencies. No CLI
source changes or replacement directives were committed.

Release publication has not been performed. Release Please must determine the
next version and changelog after the breaking relocation is submitted with an
appropriate conventional commit, such as
`refactor!: relocate Token Logger to plugins/token-logger`, and reviewed.
