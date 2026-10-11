# Contributing

Contributions to KompoSyn are welcome. By contributing, you agree that your
contribution is provided under the Apache License 2.0, subject to the terms in
the repository [LICENSE](LICENSE).

## Before you start

Read [AGENTS.md](AGENTS.md) for repository invariants and
[SECURITY.md](SECURITY.md) before working on security-sensitive behavior. For
product and operational documentation, start at the [documentation
index](docs/README.md).

Inspect the worktree and branch before editing. Preserve unrelated changes and
do not commit local databases, WAL files, release archives, generated binaries,
credentials, provider transcripts, prompts, or responses.

## Repository structure

- `plugins/library/token-logger/` owns domain behavior, persistence, migrations,
  provider adapters, collection, pricing, diagnostics, and reports.
- `plugins/cli/token-logger/` owns command parsing, lifecycle, interactive mode,
  output, and the harness protocol.
- `scripts/` owns release archives, installers, uninstallers, and lifecycle
  verification.
- `docs/HARNESS_PROTOCOL.md` is the normative JSONL protocol contract.

Keep domain behavior in the library and preserve the library/CLI boundary.
Provider source files must remain read-only, and prompt/response content must
not be captured by default.

## Proposing future components

KompoSyn follows a plugin-first direction: a proposed component should solve a
clear problem and be useful, testable, and understandable independently before
it is coupled to a future harness. Start a proposal with a design discussion
that explains the problem, ownership boundary, inputs and outputs, privacy
impact, and likely users.

Do not assume that every component needs a standalone CLI, or that a general
plugin runtime, marketplace, or universal API already exists. Those decisions
remain open. The current Token Logger library, CLI, and JSONL sidecar are the
implemented reference surfaces; new proposals should not silently expand their
contracts.

## Development and validation

KompoSyn requires Go 1.26 or newer. The normal local gate is:

```sh
make check
```

For a complete change, also run:

```sh
go mod verify
go test -race ./...
```

See [development](docs/contributing/development.md) for focused commands and
[testing](docs/contributing/testing.md) for the change-area test matrix. Add or
update regression tests alongside behavior changes.

## Versioning and releases

Use Conventional Commit prefixes for changes that may affect release notes or
versioning, for example `feat:`, `fix:`, `docs:`, `refactor:`, and `test:`.
Release Please uses the merged commit history to prepare a release pull
request, update `CHANGELOG.md`, and calculate the next semantic version. Do not
manually edit a released version or create a release tag as part of an ordinary
feature pull request.

The repository's initial unreleased baseline is `0.0.0`; the first prepared
release is expected to be `0.1.0`. Development binaries report `0.0.0-dev`.
Release builds receive their exact tag version through linker flags.

To publish a release, run **Prepare release** in GitHub Actions, review and
merge the Release Please pull request, and then run **Publish release**. The
publish workflow validates the merged commit, tests the installers, signs the
artifacts, creates the tag and GitHub Release, and uploads the signed assets.
Do not create release tags manually.

## Change-specific requirements

- **Storage or migration:** add a new numbered migration; never edit an
  applied migration. Update the runner, schema version, historical upgrade
  coverage, and [migration documentation](docs/migrations.md).
- **Provider adapter or collector:** test detection, normalization, malformed
  input, immutable snapshots, repeated imports, cursor behavior, replacement
  detection, and unchanged source files.
- **Harness protocol:** update [the protocol contract](docs/HARNESS_PROTOCOL.md),
  preserve protocol version 1 for compatible changes, keep stable error codes,
  and test stdout cleanliness, message limits, lifecycle, and metadata
  atomicity.
- **Release or installer:** preserve checksum and Cosign verification, test
  upgrade and uninstall data preservation, and run the release lifecycle
  verifier.
- **Security or privacy:** describe the threat model, disclosure boundary, and
  data-preservation impact. Do not add telemetry or external transmission
  without an explicit design change and user consent.
- **Documentation:** verify commands, examples, and local links against the
  implementation.

## Pull requests

Keep each pull request focused. Use the repository template and follow the
[pull request guide](docs/contributing/pull-requests.md). Include the commands
run and their outcomes, not only a statement that tests passed. Call out
migration, protocol, release, security, privacy, and upgrade impact even when
the answer is “none”.

Reviewers should be able to reproduce the validation without access to private
source data. Never include provider transcript content, credentials, prompts,
responses, or unsanitized diagnostics in PR evidence.

## Coding-agent workflow

Agents should read `AGENTS.md`, this guide, and the relevant normative docs;
inspect status and history; identify the owning package; search for existing
contracts and tests; make the smallest scoped change; run focused checks; run
the required full checks; review the final diff; and report changed files,
validation, limitations, and follow-up work. Agents must not discard unrelated
work, rewrite migration history, weaken signature verification, write
diagnostics to protocol stdout, modify provider source files, or claim checks
that were not run.
