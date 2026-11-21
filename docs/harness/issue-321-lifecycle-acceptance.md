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

## Native evidence and limits

The previous accepted run is historical evidence only: source `39279e93080a2306f5ed1a702bd83ab852eb1055`, overlay SHA-256 `560c845a55a0e52c4f76b8710407ce914113f168c41b14196432d47b0b66f5c4`, archive SHA-256 `4a43e17e123e4fb6e2a0b55af42d276df7433092094ee0fb4b814e1b50047dfa`. It does not include the lifecycle changes in this branch.

The candidate runner adds a separate-actor parent move/substitution case. It is not executed here: this host's Bubblewrap ownership is `nobody:nogroup 755`, which fails the trusted-root-owned prerequisite. The new case and the existing native creation cases therefore remain pending on an eligible regular-user Linux/ext4 host. Run the exact committed candidate using the steps in [native acceptance](issue-321-native-acceptance.md); do not alter host ownership or permissions.

Remaining evidence gaps include native execution of the new parent-move case and syscall-level interruption behavior. Behavioral gaps include a dedicated active list/search cancellation case, full composed byte-limit boundary coverage, and every possible owner/cancellation checkpoint. The present work does not close those gaps by inference.

## Enablement boundary

No production capability is enabled. The accepted prototype remains synthetic and fixture-only. Production owner coverage, the supported profile decision, native acceptance for this candidate, and explicit maintainer enablement approval remain separate gates. Issues #267, #315, #321 and #322 remain open.
