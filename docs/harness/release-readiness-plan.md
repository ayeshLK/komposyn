<!-- Copyright 2026 Ayesh Almeida -->
<!-- Licensed under the Apache License, Version 2.0. -->

# Experimental harness release preparation

## Agreed decisions

The maintainer agreed to this scope on 2026-11-10:

- Prepare a release of the existing experimental capabilities. Scope is frozen
  to documentation, validation, release preparation, and fixes required by that
  validation. New capabilities belong in later releases.
- Live provider validation will happen with the published release. It is not a
  prerequisite for this experimental release. Release notes must distinguish
  implemented behavior, fixture evidence, native evidence, and live flows that
  remain unverified.
- Preserve the repository-wide release model: one version for the Go module
  and experimental `komposyn` binaries. Release Please proposes the version.
  The independently versioned `komposyn-lg` executable is not released here.

The initial audit used remote master
`a3774cf99ca6478057310a02267ca48eccd68069`, with `v0.13.1` as the published
baseline. Refresh these references when choosing the final candidate. Prior
focused harness tests are supporting evidence, not final-candidate validation.
This plan records preparation and publication steps; it does not dispatch them.
The [validation record](release-validation.md) tracks exact evidence and pending
release gates. The [overview](next-release-overview.md) and
[first-use walkthrough](first-use.md) are ready for review.

## 1. Reconcile documentation

- [x] Inspect and preserve existing worktree changes before editing overlapping
  documentation. Review those changes independently of this plan.
- [x] Reconcile the root README, installation guide, harness guide, security
  notes, vision, and architecture against executable wiring.
- [x] Document OpenAI API-key and Anthropic API-key chat with conversation
  tools; distinguish ChatGPT OAuth's text-only route.
- [x] Describe `update_plan`, `request_user_input`, approval choices,
  cancellation, and grant inspection/revocation.
- [x] Describe conditional Linux `read_file`: trusted Bubblewrap prerequisite,
  explicit root selection, exact-file once approval, provider disclosure and
  conversation reuse, 64 KiB worker bound, protected objects, and refusal paths.
- [x] Explain temporary chat state, plaintext credentials under private file
  protections, and the absence of editing, shell, web, and persistent sessions.
- [x] Correct outdated release-availability and current-status statements.
  Preserve historical evidence with its original revision and limitations.
- [x] Verify installation commands, links, and a short first-use walkthrough.

Acceptance: current user guidance consistently describes shipped behavior,
authentication/platform differences, prerequisites, and known limitations.

## 2. Validate the candidate

- [ ] Record the exact candidate commit and changes since the previous release.
- [x] Review Go API, database/migration, and protocol compatibility across the
  complete repository change, including Token Logger core changes.
- [x] Run the required checks and review any formatting diff:

  ```sh
  go mod verify
  go vet ./...
  go test ./...
  go test -race ./...
  make check
  ```

- [ ] Require successful candidate CI, security checks, and the public
  standalone `komposyn-lg` compatibility suite.
- [x] Record offline smoke evidence using isolated configuration, synthetic
  files, fake credentials, and fixture providers where inference is needed.

| Area | Required coverage before publication |
| --- | --- |
| Startup | Help, version, isolated configuration check, existing profile loading |
| Chat | Discovery, selection, streaming, follow-up, cancellation, confirmed model change |
| Conversation tools | Plan replacement/clearing; clarification answer, decline, cancellation |
| Permissions | Once/conversation approval where supported, denial, revocation |
| Linux read | Approved read, protected-file refusal, oversized-file refusal, cancellation and withheld stale results |
| Backend unavailable | Ordinary chat/conversation tools work; `read_file` unavailable |
| Terminal | Multiline paste, selection/copy, narrow/wide layouts |

- [x] Map existing tests to this matrix and fill material gaps. Do not count
  fixture execution as live provider or native sandbox acceptance.
- [x] Review applicable native read evidence for its revision, platform, worker
  image, backend, and outstanding limits. Run the relevant native profile on
  the candidate where required to substantiate the advertised read behavior.
  Unit tests and cross-compilation cannot substitute for native enforcement.
- [x] Record exact commands, commit, platform, artifact identity, results, and
  unavailable cases. Keep credentials and sensitive content out of evidence.

Acceptance: required checks pass, advertised enforcement has applicable
evidence, and no known blocker affects startup, credential protection,
authorization, or data preservation. Missing live provider validation is an
explicit experimental limitation; it does not waive other release gates.

## 3. Prepare release communication

- [x] Draft a short release overview covering user-visible changes since the
  previous release alongside the generated repository-wide changelog.
- [x] Include a five-minute walkthrough and provider/auth/platform capability
  table, especially ChatGPT OAuth and conditional Linux file-read limits.
- [x] State that live provider validation is deferred until after publication;
  do not describe those routes as live-verified.
- [x] Review core compatibility and migration notes for module consumers.
- [x] Link ordinary feedback to GitHub Issues with version, platform, auth
  method, sanitized outcome, and reproduction steps. Direct vulnerabilities
  and sensitive disclosures to the private security reporting route.
- [x] Describe future direction without promising dates or unimplemented
  capabilities in this release.

Acceptance: users can identify what to try, what persists, what leaves their
machine, what is unavailable, and how to report problems safely.

## 4. Review and publish

Follow [release verification and publication](../release-verification.md).

- [ ] Merge reviewed preparation changes and choose the final candidate.
- [ ] Dispatch Release Please from `master` to prepare/update its release PR.
- [ ] Review version, changelog, compatibility, and final CI; merge normally.
- [ ] Dispatch Release Please again from `master` to create its immutable tag
  and draft release. Never create or move tags manually.
- [ ] Resolve the full tagged commit and review the final go/no-go before
  dispatching binary publication: that workflow publishes automatically when
  its required gates pass.
- [ ] Dispatch binary publication with the exact version and tagged commit.
- [ ] Require all six native build and signed installation lifecycle checks,
  the complete archive set, signed checksums, and artifact metadata checks.
- [ ] Verify the published release, asset links, installation instructions,
  and preservation of configuration and the separate `komposyn-lg` binary.

If a draft release build fails, follow the documented same-tag retry process;
do not move tags or automatically replace mismatched assets.

## 5. Validate live flows after publication

- [ ] Using the published binaries, record OpenAI API-key, Anthropic API-key,
  and ChatGPT login/discovery/streaming results as accounts are available.
- [ ] Exercise conversation tools on supported API-key routes. On a supported
  Linux host, use a synthetic non-sensitive file for approved file disclosure.
- [ ] Keep account login and any billable inference under explicit operator
  authorization; this plan does not initiate provider requests.
- [ ] Record exact release, platform, route, and sanitized outcomes. Leave
  unavailable cases unverified rather than inferring success.
- [ ] Publish sanitized known issues and validation updates. Route security
  findings privately; prioritize regressions for the next corrective release.

Acceptance: live results are traceable to the published artifacts and improve
the evidence without changing the release's historical validation claims.
