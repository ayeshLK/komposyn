# Experimental release validation record — #269

Prepared on 2026-11-10; [v0.14.0](https://github.com/ayeshLK/komposyn/releases/tag/v0.14.0)
was published at **2026-11-10T18:22:01Z**. Published assets were independently
verified on 2026-11-11 (Asia/Colombo). Live provider validation remains
**unverified**, tracked in [#272](https://github.com/ayeshLK/komposyn/issues/272)
with @ayeshLK coordinating authorized account operators. This record does not
authorize account login, credential access, or inference.

## Published release evidence

| Stage | Exact evidence |
| --- | --- |
| Preparation | [PR #270](https://github.com/ayeshLK/komposyn/pull/270), merged as `da80cc28995a01a9b46226aa16850a914911452e`; [preparation CI](https://github.com/ayeshLK/komposyn/actions/runs/42866851173) and security passed at reviewed head `2339bba` |
| Release Please proposal | [Run 42868227358](https://github.com/ayeshLK/komposyn/actions/runs/42868227358) proposed repository-wide 0.14.0; [PR #271](https://github.com/ayeshLK/komposyn/pull/271) reviewed version/changelog/highlights and merged as `46f84ea5923b7bb04b3b530f57cec46d7c291595` |
| Immutable source tag/draft | [Release Please run 42869893558](https://github.com/ayeshLK/komposyn/actions/runs/42869893558) created `v0.14.0` at that exact merge commit; no manual tag was created or moved |
| Exact tagged commit | [CI 42869813491](https://github.com/ayeshLK/komposyn/actions/runs/42869813491) and [security 42869813490](https://github.com/ayeshLK/komposyn/actions/runs/42869813490) passed, including race, three OS suites, public standalone CLI compatibility and all six native candidate targets |
| Publication go/no-go and execution | Maintainer @ayeshLK explicitly dispatched [binary workflow 42871823003](https://github.com/ayeshLK/komposyn/actions/runs/42871823003) with source commit `46f84ea`; all jobs succeeded before automatic publication |
| Published identity | `v0.14.0`, commit `46f84ea5923b7bb04b3b530f57cec46d7c291595`; latest-release metadata resolves to this version; six archives plus manifest and signature bundle are present |

The full `v0.13.1..v0.14.0` delta is 221 files, 64,708 insertions and
1,097 deletions. It includes the implementation delta reviewed below, release
preparation documentation, changelog and version manifest. The tag has the same
Git tree as tested release-PR head `3671fba`. Comparing accepted native-read
baseline `91efabd` with the tag finds **no Go source, dependency, script or
workflow changes**; native evidence applicability and its limitations remain
as recorded below. This does not convert unavailable native cases into passes.

### Signed native installation matrix

Every row passed both native build/smoke and real signed installation with
Cosign v2.4.3 in publication workflow 42871823003. The installation scripts
check first install, reinstall, replacement, version/commit/target metadata,
help, isolated configuration, uninstall, auth/settings content and access
protections, and coexistence with `komposyn-lg`.

| Target | Native build/smoke job | Signed installation job |
| --- | --- | --- |
| Linux amd64 | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130027052691) | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130030506506) |
| Linux arm64 | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130027053154) | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130030506561) |
| macOS amd64 | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130027053125) | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130030507291) |
| macOS arm64 | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130027052728) | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130030506576) |
| Windows amd64 | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130027052760) | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130030507176) |
| Windows arm64 | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130027052706) | [Passed](https://github.com/ayeshLK/komposyn/actions/runs/42871823003/job/130030506716) |

### Independent published-asset verification

All eight assets were downloaded from the published release. Local Cosign
v3.1.3 returned `Verified OK` for the bundle with the exact required identity
and issuer; `sha256sum --check checksums.txt` verified all six archives.
The publication jobs above separately used pinned Cosign v2.4.3. A read-only
local TUF-cache failure was resolved by selecting a writable cache, without
changing trust roots, signer identity, issuer, or verification requirements.

```sh
gh release download v0.14.0 --repo ayeshLK/komposyn --dir /tmp/komposyn-269-published
cd /tmp/komposyn-269-published
TUF_ROOT=/tmp/komposyn-269-tuf cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
sha256sum --check checksums.txt
```

| Published asset | SHA-256 |
| --- | --- |
| `checksums.txt` | `2ed73661fae96d696f25f45fb3d680fe38713a71ab25a3b0a8b53433d7e969e4` |
| `checksums.txt.sigstore.json` | `c5769190a32f259a2865f5c2c1f8edcacca22e7425ac4354cf949e050980f55b` |
| `komposyn_0.14.0_darwin_amd64.tar.gz` | `f99cf326648b89fe7466d70e6bd6703dd1c5c96a84327c8df9a485d8f58cf71a` |
| `komposyn_0.14.0_darwin_arm64.tar.gz` | `a21086da96bbf80765aef923cc9d28083be596982c317948a1293e365230f227` |
| `komposyn_0.14.0_linux_amd64.tar.gz` | `eea9a8112a565fe027555621d443145885671eb0b700df61c0a4b389248ada49` |
| `komposyn_0.14.0_linux_arm64.tar.gz` | `f460deab49873bd81a4fd0c99caa248ffb51661e1ecab4d07042850753c627ab` |
| `komposyn_0.14.0_windows_amd64.zip` | `f6ad55e5d8c99d82e94dfe3cbda0896d46c3c62b674517fec3ad0dd6f1258788` |
| `komposyn_0.14.0_windows_arm64.zip` | `bda8a02dd7cf78a6818f9288891913110ac9daba3bb7613022258df07245918b` |

The additional local Linux amd64 run used the published archives and real
signature verifier:

```sh
TUF_ROOT=/tmp/komposyn-269-tuf bash scripts/test-release-install-unix.sh \
  --version 0.14.0 --commit 46f84ea5923b7bb04b3b530f57cec46d7c291595 \
  --artifacts-dir /tmp/komposyn-269-published
```

The original fixture failed twice with `ETXTBSY` at its in-place write of a
synthetic previous executable, after successful first install and reinstall.
Direct CLI and reduced staging controls did not reproduce it. The fixture now
seeds that prior file through a fresh inode and rename, avoiding any dependency
on writing a just-executed inode. The full signed install/reinstall/replacement/
uninstall and preservation checks then passed. No installer or released binary
was changed, and no runtime cleanup defect is claimed fixed; the cause of the
local write-denial was not established. The six hosted gate results above
used the original released fixture and remain separate evidence.

Logs are retained locally under `/tmp/komposyn-269-evidence/`; durable evidence
is the exact workflow/job links, release identities and digests recorded here.
The preparation evidence below retains its original revision and test scope.

## Candidate and scope

Published baseline: `v0.13.1`, commit
`5405df6c05694dfb2a6ef89e528c8205989b93c8`. GitHub lists six target archives,
`checksums.txt`, and `checksums.txt.sigstore.json`; listing assets alone is not
fresh signature verification.

Implementation candidate: remote/local master
`a3774cf99ca6478057310a02267ca48eccd68069`. Preparation PR: [#270](https://github.com/ayeshLK/komposyn/pull/270).
The preparation changed Markdown only. Its reviewed merge, Release Please
commit and final CI are recorded above. The implementation baseline and dirty
worktree were not labeled as the final release commit.

Delta review commands:

```sh
git log --oneline v0.13.1..a3774cf99ca6478057310a02267ca48eccd68069
git diff --stat v0.13.1 a3774cf99ca6478057310a02267ca48eccd68069
git diff v0.13.1 a3774cf99ca6478057310a02267ca48eccd68069 -- go.mod go.sum
git diff v0.13.1 a3774cf99ca6478057310a02267ca48eccd68069 -- plugins/library/token-logger
```

The implementation delta at this preparation baseline contained 213 files,
63,981 insertions and 1,058 deletions.
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
also passed. These checks identify the implementation baseline; final preparation and
tagged-release CI are recorded separately above.

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
publication go/no-go. Those completed steps and exact signed-artifact evidence
are recorded above.
The binary workflow publishes automatically after passing its gates; permission
to prepare this record does not dispatch it. Same-tag draft recovery remains
required on failure. Follow [release verification](../release-verification.md).

| Artifact gate | Current evidence |
| --- | --- |
| Six native candidate builds/help/config checks | Passed on the exact tagged commit and repeated in publication |
| Signed release archives and checksum bundle | All six archives, manifest and signature bundle published and verified |
| First install/reinstall/replacement/metadata/uninstall | All six native signed lifecycle jobs passed |
| Configuration/data and `komposyn-lg` preservation | Signed lifecycle jobs passed content/access protection and coexistence checks |
| Published assets and signature verification | Published set downloaded; expected signature identity/issuer and all digests verified |

Live checks remain unverified by agreement and are owned by @ayeshLK in
[#272](https://github.com/ayeshLK/komposyn/issues/272). The maintainer coordinates an authorized
account operator after publication and records the exact released binary,
version/commit, platform, auth route, date, sanitized result and follow-up link.
No credentials or billable inference are authorized by this document.

| Route | Login/discovery/stream/follow-up/cancel | Tools and synthetic Linux read | Owner |
| --- | --- | --- | --- |
| OpenAI API key | Unverified; published binary available, await authorized account operator | Unverified | @ayeshLK / authorized account operator (#272) |
| Anthropic API key | Unverified; published binary available, await authorized account operator | Unverified | @ayeshLK / authorized account operator (#272) |
| ChatGPT sign-in | Source-built PR binary live-tested on one account/model; published binary remains unverified | Planning, clarification, cancellation, and exact-file read reported passed on one account/model/host; denial remains unverified | @ayeshLK / authorized account operator (#272) |

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
