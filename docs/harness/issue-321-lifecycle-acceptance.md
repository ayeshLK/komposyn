# Issue #321 creation lifecycle acceptance

This record tracks behavioral lifecycle evidence separately from native filesystem evidence and production enablement. The implementation is a disposable-fixture prototype; the normal `cmd/harness` composition does not register creation and production owner coverage remains unresolved.

## Behavioral evidence

Focused tests use the real composed sandbox service and runtime seam where applicable. They cover:

| Lifecycle claim | Evidence |
| --- | --- |
| Barrier closes admission before stage creation and drains active reads | `TestCreationBarrierWaitsForActiveReadWorkerToJoin`; active worker is cancelled and joined without changing read generations. |
| Completed private read captures remain publishable | `TestCompletedReadCaptureSurvivesTransientCreationBarrierBeforeProviderHandoff`; the transient barrier does not revoke already completed captures. |
| Effect remains one-shot across continuation failure | `TestComposedCreationSuccessAndContinuationFailureDoNotReplay`; a later turn requires its own call and approval. |
| Cleanup uncertainty blocks admission and retains ownership | `TestCreationCleanupFailureRetainsBarrierAndDoesNotRetry`; `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked`. |
| Lost completion classification cannot replay | `TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed`. |
| Owner transition and publication release are ordered | `TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification`; a concurrent alias attempt fails closed and owner rotation waits for classification. |

Existing creation tests continue to cover once binding, retained-parent checks, root/service transitions, worker protocol, byte limits and collision behavior. These tests establish control-flow and lifecycle behavior; they do not establish kernel/filesystem enforcement.

## Acceptance matrix

| Area | Existing evidence / candidate action | Observed result |
| --- | --- | --- |
| A. Barrier: queued/prepared and active read, list, search | Barrier invalidates pending read admissions and drains active read workers. New dedicated test covers active read. Separate active list/search cancellation case is not added. | Read case passes; list/search-specific cancellation coverage remains incomplete. |
| A. Completed capture, aliases, concurrent same-leaf create | Completed capture/provider handoff and same-parent alias/concurrent attempt are covered by composed tests; root/policy/owner invalidation remains covered by existing authority tests. | Covered cases pass; not every alias/leaf interleaving is exhaustively enumerated. |
| B. Drain/join/cleanup/publication uncertainty | Injected cleanup error, delayed join, and completion loss after publish assert unknown/blocked/no replay. | Covered cases pass; no recovery operation exists, so the gate remains blocked. |
| B. Close/root transitions and ownership | Existing close/root transition tests plus after-release owner transition ordering. Active creation close at every delayed-retirement boundary is not separately covered. | Existing and new cases pass; the additional lifecycle boundary remains open. |
| C. Cancellation and owner arbitration | Existing permit winner tests plus new publication-release winner test. Native syscall interruption and every before/after permit timing are not controlled by the behavioral fixtures. | Selected deterministic cases pass; other checkpoints remain incomplete. |
| D. Successful composition and no replay | Real Service/runtime/provider fixture proves success followed by continuation failure and a separate approved next turn; worker test covers exact bytes/mode and empty input. Lost response/cancel/duplicate dispatch and full composed size boundaries are not all exercised together. | Covered cases pass; additional outcome combinations remain incomplete. |
| Native: worker, collision, parent movement | Existing pinned native cases retained; runner adds a separate same-UID actor moving the parent and substituting the old route. Interrupted write/sync/close/publication syscalls are not controllably induced. | Build completed; native execution unavailable on this host, so candidate native cases are pending. |

## Native evidence and limits

The previous accepted run is historical evidence only: source `39279e93080a2306f5ed1a702bd83ab852eb1055`, overlay SHA-256 `560c845a55a0e52c4f76b8710407ce914113f168c41b14196432d47b0b66f5c4`, archive SHA-256 `4a43e17e123e4fb6e2a0b55af42d276df7433092094ee0fb4b814e1b50047dfa`. It does not include the lifecycle changes in this branch.

The candidate runner adds a separate-actor parent move/substitution case. It is not executed here: this host's Bubblewrap ownership is `nobody:nogroup 755`, which fails the trusted-root-owned prerequisite. The new case and the existing native creation cases therefore remain pending on an eligible regular-user Linux/ext4 host. Run the exact committed candidate using the steps in [native acceptance](issue-321-native-acceptance.md); do not alter host ownership or permissions.

The candidate runner was built and reached its native preflight against source
`e7c5fbd884682916602a23a9909c0f81938d3f4e`. It recorded `native_unavailable`,
not a native pass. Archive:
`/tmp/komposyn-composition-native-results.nyjn6xql.tar.gz` (SHA-256
`5cbc1ac735826def9e34d6bad67fda1546be5a04376be07d4fb6d8f6df65b3cd`);
manifest SHA-256
`99844cd67fa49e9485294adb43a07c6577a2978addc33597f5a1010a5b0d595d`.
The Go 1.26.11/Linux 7.0.0-42-generic amd64, UID 1000 run completed unit
checks and native binary build, skipped all backend-dependent cases because
the configured Bubblewrap was untrusted (UID 65534, mode 0755), and confirmed
cleanup. Overlay SHA-256 is
`cdf1712739ea9ea179550a6ee59ef53273afe9ad4e95cd2191d5d15948e65747`; runner
SHA-256 is `088837dc7d2de35588a80959193b7a5d5b0f673a13f35154e6ef2aa855b4fa3d`.

Remaining evidence gaps include native execution of the new parent-move case and syscall-level interruption behavior. Behavioral gaps include a dedicated active list/search cancellation case, full composed byte-limit boundary coverage, and every possible owner/cancellation checkpoint. The present work does not close those gaps by inference.

## Enablement boundary

No production capability is enabled. The accepted prototype remains synthetic and fixture-only. Production owner coverage, the supported profile decision, native acceptance for this candidate, and explicit maintainer enablement approval remain separate gates. Issues #267, #315, #321 and #322 remain open.
