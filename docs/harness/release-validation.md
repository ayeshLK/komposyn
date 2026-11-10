# Experimental release validation record — #269

Preparation date: 2026-11-10. Publication is **pending**. This record does not
authorize workflow dispatch, merging, tagging, account login, or inference.

## Candidate and scope

Published baseline: `v0.13.1`, commit
`5405df6c05694dfb2a6ef89e528c8205989b93c8`. GitHub lists six target archives,
`checksums.txt`, and `checksums.txt.sigstore.json`; listing assets alone is not
fresh signature verification.

Implementation candidate: remote/local master
`a3774cf99ca6478057310a02267ca48eccd68069`. Preparation PR: [#270](https://github.com/ayeshLK/komposyn/pull/270).
This preparation changes Markdown only. Its eventual reviewed merge and Release Please commit must be recorded
before publication, with final CI attached to that revision. Do not label a
dirty worktree or the implementation baseline as the final release commit.

Delta review commands:

```sh
git log --oneline v0.13.1..a3774cf99ca6478057310a02267ca48eccd68069
git diff --stat v0.13.1 a3774cf99ca6478057310a02267ca48eccd68069
git diff v0.13.1 a3774cf99ca6478057310a02267ca48eccd68069 -- go.mod go.sum
git diff v0.13.1 a3774cf99ca6478057310a02267ca48eccd68069 -- plugins/library/token-logger
```

The complete delta contains 213 files, 63,981 insertions and 1,058 deletions.
It includes conversation tools and provider tool protocols; runtime approval,
permissions and retained-read admission/publication; auth/settings owner
protection; terminal clipboard/paste/selection; filesystem and sandbox
foundations; native/research fixtures, scripts and documentation; and Codex
parsing/recovery in Token Logger. Research executables are not new shipped
chat capabilities. Release notes must cover core changes as well as chat.
No workflow signing or installation gate is removed by this preparation.

Existing worktree changes were inspected before edits. The #215 closure
summaries were independently checked against its reviewed GitHub reports and
are relevant documentation corrections. The untracked exact-object rename
proposal is unrelated to this release and must remain outside its preparation
PR. The pre-existing release-readiness plan supplies the agreed checklist.
No existing work was discarded. The preparation PR must explicitly state any
pre-existing documentation it includes.

## Core compatibility review

| Boundary | Reviewed effect and evidence |
| --- | --- |
| Module/API | One module and repository-wide version. Additive bounded `PrepareCodexRecovery`, `ApplyCodexRecovery`, `ListCodexRecoveries`, `InspectCodexRecovery` APIs and related types; see [core API](../core-api.md). The standalone CLI suite validates its current consumer surface. |
| SQLite | Version 18 → 19. Only migration 19 is appended to the numbered SQL history; runner records it transactionally. Append-only recovery journals and current allocation heads preserve base events. `pricing_test.go` exercises the full legacy version-1 migration chain; `schema_test.go` covers fresh creation, backup failure, failed migration preservation, and migration 19 rollback. No down-migration. |
| Accounting | Reporting/resources/project inspection and persisted usage reconciliation use effective allocations. Recovery revalidates the source/cursor/allocation revision and commits evidence, allocations and cursor changes together. `codex_recovery_preview_test.go`, `codex_recovery_history_test.go`, reporting tests and collector baseline CI cover this boundary. |
| Provider input | Codex ignores token counts lacking usage information, accepts supported optional reasoning metadata, and handles legacy cumulative records without duplicating already covered usage. Existing provider records remain read-only; explicit recovery is bounded and separate from normal import. |
| Protocol | Event schema and standalone JSONL protocol-v1 transport remain unchanged. Database schema metadata advances to 19. Public standalone CLI compatibility must pass against the candidate, using a temporary replacement only. |
| Dependencies | No version changes in `go.mod`/`go.sum`; already pinned `github.com/rivo/uniseg v0.4.7` becomes direct for selection. Go minimum remains 1.26.10. |
| Data/security | Temporary chat opens no Token Logger store. Auth/settings remain private plaintext files. No automatic database copying/merging, data-directory selection changes, provider credential use, or new telemetry. |

## Automated evidence

Host: Linux amd64, kernel `7.0.0-42-generic`, Go 1.26.10.
Commands run from the preparation worktree with unchanged Go source and
`GOCACHE=/tmp/komposyn-269-go-cache`. Initial attempts using the default cache
failed because its directory was read-only; those setup failures are not passes.

| Check | Result |
| --- | --- |
| `go mod verify` | Passed |
| `go vet ./...` | Passed |
| `go test ./...` | Passed |
| `go test -race ./...` | Passed (417 seconds including build; Token Logger tests 332 seconds) |
| `make check` | Passed; generated no Go formatting changes |
| `git diff --check` | Passed |
| Public standalone CLI suite | Passed locally against public CLI `a24e14b9fa729140cb77d6e1caa562ccf92fac47`; base CI passed |
| Native Linux candidate archive build and artifact smoke | Passed on clean implementation candidate; unsigned `0.0.0-dev` build only |
| `bash -n scripts/install.sh scripts/uninstall.sh` and `bash scripts/test-installer-unix.sh` | Passed; Cosign mocked in fixtures |

Local downstream validation cloned the public CLI to a temporary directory,
ran `GOWORK=off go mod edit -replace=github.com/ayeshLK/komposyn=<candidate-directory>`,
`GOWORK=off go mod tidy`, and `GOWORK=off go test ./...` with the writable
cache. The replacement exists only in that disposable CLI copy. Native archive
checks used `scripts/build-release.sh` and `scripts/test-release-artifacts.sh`
with `--version 0.0.0-dev --commit a3774cf99ca6478057310a02267ca48eccd68069
--goos linux --goarch amd64` and a temporary artifact directory. Archive SHA-256:
`1afd65d68b8e8c65d8c0a7eeff56dc3b6dcd1e8345d15437a1328b3354030240`.
An initial packaging attempt correctly refused the dirty source tree; the
successful run used the isolated clean clone. The build also reported a
read-only module-stat cache warning; archive and native smoke checks succeeded.
This unsigned candidate and mocked installer fixture do not satisfy signed
publication gates.

Logs for this local preparation are under `/tmp/komposyn-269-evidence/`; these
temporary files are not durable release artifacts. Final review must retain
the CI run links and candidate identifiers. The implementation baseline's
[CI run](https://github.com/ayeshLK/komposyn/actions/runs/42847681322)
passed all three OS tests, race, vulnerability scanning, collector baseline,
standalone CLI compatibility, and six native candidate release checks.
[Go security analysis](https://github.com/ayeshLK/komposyn/actions/runs/42847681205)
also passed. These checks identify the implementation baseline; the final
preparation/release revision still needs its own required CI.

## Offline acceptance map

All rows below use fake credentials/providers, isolated temporary configuration,
or simulated screens. They are exercised by the full Go suite; none establishes
live-provider connectivity or native enforcement.

| User-visible boundary | Existing evidence inspected |
| --- | --- |
| Profile loading, discovery/selection, streaming, follow-up, cancellation | `internal/harness/runtime/provider_walkthrough_test.go`: `TestOfflineProviderWalkthroughBothProviders` exercises both real adapters against local HTTP fixtures, reloads stored profiles/settings, checks partial streaming, cancellation, auth failure and safe follow-up history. |
| Confirmed model changes, context reset, late deltas | `runtime/chat_test.go`: `TestTemporaryMultiMessageChatAndConfirmedModelReset`, `TestCancellationBlocksLateDeltasAndPreservesSafeHistory`. |
| Plan replacement/clearing, clarification answer/decline/cancel | `runtime/conversation_tools_test.go` and `plan_test.go`; `plugins/ui/tview/ui_test.go` explicit-choice dialogs and plan rendering; `internal/harness/ui/clarification_bridge_test.go` correlation and invalidation. |
| Once/conversation approval, denial, redirect, revocation | `runtime/tool_loop_test.go`: `TestRuntimeApprovalOutcomesAndHeadlessFailure`; `conversation_tools_test.go`: `TestConversationGrantReusesToolActionButOnceGrantDoesNot`; `internal/harness/ui/tool_approval_integration_test.go`; permissions tests and UI permissions panel tests. |
| Read disclosure, withheld stale captures and replay | `runtime/read_publication_test.go`: read/replay, sibling batch revocation, owner invalidation, late response, empty read and encoded-size withholding. These use controlled hosts; enforcement requires native evidence below. |
| Backend unavailable | `TestProductionCatalogDoesNotAdvertiseReadFile`, `TestFilesystemReadCatalogRequiresCompleteInspectedHost`, `TestReadFileAdvertisementRequiresSelectedRootAndApprovalIsExplicit`; Stage 6 controls confirm ordinary chat and unsolicited-read refusal without backend/root. Conversation tool advertisement depends on supported routes and trusted interaction, not read backend availability. |
| Multiline paste, copy/selection, layout | `plugins/ui/tview/clipboard_test.go`, `TestBracketedTrailingNewlineSlashPasteStaysLiteralUntilEnter`, `TestExplicitClipboardCopyUsesPlainReplyAndTranscript`, `TestUsagePaneResizesAndCompactControlKeepsComposerVisible`, narrow/wide usage paging tests in `ui_test.go`. |
| Startup, help/version/configuration | Candidate `scripts/test-release-artifacts.sh` executes native `--version`, `--help`, and isolated `--check`; six native target runs are also in base CI. |

## Native read evidence and applicability

The [reviewed #215 record](issue-215-completion-tracking.md) names exact native
archives, source revisions, environment, checksums, results and limitations.
Its shipped-worker snapshot report covers tested PR head
`d415a19ccf4196e6ea734b3f224d6b7b4ea415df`, whose tree equals merged
`91efabdaeb43bda7c26324b22601cfc74352cbed`. Its descriptor report covers that
merged commit using the study worker. Both measured twelve native-integrated
cases plus six controls; only the study-worker profile measured its bounded
parent-death case. The #215 closure explicitly accepts this read-focused scope.

Applicability was checked with:

```sh
git diff --name-only 91efabdaeb43bda7c26324b22601cfc74352cbed a3774cf99ca6478057310a02267ca48eccd68069
```

Only `AGENTS.md` and `docs/agents/{domain,issue-tracker,triage-labels}.md`
changed. Implementation, worker, fixtures, dependencies and runner are unchanged.
This preparation adds Markdown only. The linked reviewed native reports apply
to that read implementation; they are not new measurements of release archives.

A fresh local attempt used a clean isolated clone at exact `a3774cf`, without
copying the dirty worktree:

```sh
PATH="$(go env GOROOT)/bin:$PATH" python3 -B scripts/run-issue215-stage6.py \
  --revision a3774cf99ca6478057310a02267ca48eccd68069 \
  --bwrap /usr/bin/bwrap --include-shipped-worker \
  --bind-mode file-snapshot --output-root /tmp
```

Result: exit 3, **`native_unavailable`**. Build succeeded; six synthetic controls
passed. The backend appears owned by UID/GID 65534, so the existing trust gate
refused it before probes or worker launch. No native case passed in this
attempt. No elevated retry, trust bypass, or host modification was performed.
Archive: `/tmp/komposyn-issue215-stage6-20261110T225634Z-2.tar.gz`.
Source-built worker: 13,496,446 bytes, SHA-256
`4352dfbb6f232068db8458d2a619c399fa995d3078bc522df8ff20385b097249`.
That image is a native-study build, not a signed release artifact.

Native limitations remain: Linux amd64/Bubblewrap 0.9.0 environment only;
different worker images for snapshot/descriptor profiles; shipped-image
parent-death unavailable; no strict live pathname containment, universal
opened-object proof, arbitrary descendant cleanup, aggregate quotas, other
kernels/filesystems, non-Linux enforcement, or live-provider/UI acceptance.
If code, build profile, dependencies or runner change before release, reassess
applicability and run the relevant trusted-host profile rather than treating
these reports as a permanent pass.

## Publication and post-publication ownership

The maintainer/release operator owns normal review and merging, Release Please
dispatch and version/changelog review, source-tag resolution, and final
publication go/no-go. Record the preparation PR, final CI, Release Please PR,
tag/commit, binary workflow run, signed manifest digest, six asset digests and
successful native installer results here before marking publication complete.
The binary workflow publishes automatically after passing its gates; permission
to prepare this record does not dispatch it. Same-tag draft recovery remains
required on failure. Follow [release verification](../release-verification.md).

| Artifact gate | Current evidence |
| --- | --- |
| Six native candidate builds/help/config checks | Implementation baseline CI passed; final revision CI pending |
| Signed release archives and checksum bundle | Next release not built or published |
| First install/reinstall/replacement/metadata/uninstall | Required six-target signed publication workflow pending |
| Configuration/data and `komposyn-lg` preservation | Existing fixture/CI coverage; exact signed-release lifecycle pending |
| Published assets and signature verification | Pending publication; do not infer from source tags or candidate archives |

Live checks are deferred by agreement. The maintainer coordinates an authorized
account operator after publication and records the exact released binary,
version/commit, platform, auth route, date, sanitized result and follow-up link.
No credentials or billable inference are authorized by this document.

| Route | Login/discovery/stream/follow-up/cancel | Tools and synthetic Linux read | Owner |
| --- | --- | --- | --- |
| OpenAI API key | Unverified; await published binary and authorized account | Unverified | Maintainer / authorized account operator |
| Anthropic API key | Unverified; await published binary and authorized account | Unverified | Maintainer / authorized account operator |
| ChatGPT sign-in | Unverified; await published binary and eligible authorized account | Unavailable by design; text only | Maintainer / authorized account operator |

The original unstripped source-build command produced a 19,308,007-byte
Linux amd64 image, larger than the 14 MiB worker-image bound. Build instructions
now use the static stripped release profile. The exact walkthrough build
produced a 13,496,482-byte image; `--version` and isolated `--check` passed
without creating auth/settings files. The source-built native-study image
above also fits the bound. No product regression was identified during this
documentation audit. The
cache setup failure and local backend trust refusal are recorded above rather
than hidden. Further sanitized startup, authorization, credential-protection,
or data-preservation blockers must stop publication and receive corrective
follow-up priority. Track other ordinary findings in #269 or linked issues;
route security findings privately under [SECURITY.md](../../SECURITY.md).
