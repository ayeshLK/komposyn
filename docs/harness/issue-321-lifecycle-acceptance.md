# Issue #321 creation lifecycle acceptance

This record tracks behavioral lifecycle evidence separately from native filesystem evidence and production enablement. The implementation is a disposable-fixture prototype; the normal `cmd/harness` composition does not register creation and production owner coverage remains unresolved.

## Behavioral evidence

Focused tests use the real composed sandbox service and runtime seam where applicable. They cover:

| Lifecycle claim | Evidence |
| --- | --- |
| Barrier closes admission before stage creation and drains active reads | `TestCreationBarrierWaitsForActiveReadWorkerToJoin`; active worker is cancelled and joined without changing read generations. |
| Runtime-admitted list and search cannot overlap stage creation | `TestCreationBarrierDrainsRuntimeAdmittedDirectoryWorkers` exercises composed list and search admission against the real sandbox service. `TestCreationBarrierJoinsRuntimeAdmittedSearchContentWorker` additionally holds the search content worker until the barrier waits for its join. Injected workers establish lifecycle control flow, not kernel enforcement. |
| Completed private read captures remain publishable | `TestCompletedReadCaptureSurvivesTransientCreationBarrierBeforeProviderHandoff`; the transient barrier does not revoke already completed captures. |
| Effect remains one-shot across continuation failure | `TestComposedCreationSuccessAndContinuationFailureDoNotReplay`; a later turn requires its own call and approval. |
| Cleanup uncertainty blocks admission and retains ownership | `TestCreationCleanupFailureRetainsBarrierAndDoesNotRetry`; `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked`. |
| Service close and root transition preserve pending ownership while creation retires | `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked` checks bounded close with a held worker; `TestRootTransitionRetainsPendingGateDuringDelayedCreationRetirement` verifies replacement admission remains blocked until cleanup and a retry completes the root transition. |
| Lost completion classification cannot replay | `TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed`. |
| Owner transition and publication release are ordered | `TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification`; a concurrent alias attempt fails closed and owner rotation waits for classification. |

Existing creation tests continue to cover once binding, retained-parent checks, root/service transitions, worker protocol, byte limits and collision behavior. `TestCreationPrototypeEnforcesContentAndSerializedArgumentLimitsBeforeApproval` checks exact 64 KiB content and over-limit content/serialized arguments before approval; the composed service success test also exercises exact-limit and empty-file effects. `TestCreationUnknownOutcomeStopsProviderContinuation` confirms uncertain completion ends the provider turn without replay. These tests establish control-flow and lifecycle behavior; they do not establish kernel/filesystem enforcement.

## Acceptance matrix

| Area | Existing evidence / candidate action | Observed result |
| --- | --- | --- |
| A. Barrier: queued/prepared and active read, list, search | Runtime-admitted read, list and search cases exercise the composed admission path. Search also holds the content worker while the barrier waits for join. | Focused cases pass. The injected list/search workers establish coordinator/drain behavior, not native kernel cancellation. |
| A. Completed capture, aliases, concurrent same-leaf create | Completed capture/provider handoff and same-parent alias/concurrent attempt are covered by composed tests; root/policy/owner invalidation remains covered by existing authority tests. | Covered cases pass; not every alias/leaf interleaving is exhaustively enumerated. |
| B. Drain/join/cleanup/publication uncertainty | Injected cleanup error, delayed join, and completion loss after publish assert unknown/blocked/no replay. | Covered cases pass; no recovery operation exists, so the gate remains blocked. |
| B. Close/root transitions and ownership | Delayed creation retirement is exercised across bounded service close and root transition; after-release owner transition ordering remains covered separately. | Selected cases pass; all scheduling interleavings are not exhaustively enumerated. |
| C. Cancellation and owner arbitration | Existing permit winner tests plus new publication-release winner test. Native syscall interruption and every before/after permit timing are not controlled by the behavioral fixtures. | Selected deterministic cases pass; other checkpoints remain incomplete. |
| D. Successful composition and no replay | Real Service/runtime/provider fixture proves success followed by continuation failure and a separate approved next turn; exact 64 KiB and empty-file outcomes are exercised; an unknown result stops provider continuation. Lost response/cancel/duplicate dispatch and every outcome-boundary combination are not all exercised together. | Covered cases pass; additional outcome combinations remain incomplete. |
| Native: worker, collision, parent movement | Trusted-host run for source `dbfd90974f0979832a9abc60e32aeb3c87399840` passed all 12/12 scoped native cases, including bounded creation and independent-actor parent movement/substitution. Interrupted write/sync/close/publication syscalls are not controllably induced. | Recorded `scoped_native_pass`; evidence is limited to the measured Linux amd64/local ext4/Bubblewrap profile below. |

### Named concurrency checkpoints

This is a finite scenario set, not an exhaustive enumeration of scheduler
interleavings:

| Checkpoint | Deterministic evidence | Outcome |
| --- | --- | --- |
| Before / during barrier drain | `TestCreationBarrierClosesAndReopensReadAdmissionAroundDrain`; `TestCreationBarrierWaitsForRuntimeAdmittedReadWorkerToJoin`; runtime-admitted list/search cases in `TestCreationBarrierDrainsRuntimeAdmittedDirectoryWorkers` | New admission closes; stale/prepared work cannot publish; held workers must join before barrier retirement. |
| Search traversal and content-worker joins | `TestCreationBarrierJoinsRuntimeAdmittedSearchContentWorker` | Barrier remains pending while the content worker is held, then joins both phases without publishing stale output. |
| Stage entry versus owner pending | `TestCreationBarrierOrdersOwnerPendingAgainstPublicationRelease` (owner-pending-wins subcase) | Stage/publication permits fail after owner pending wins. |
| Stage in progress versus root transition | `TestRootTransitionCancelsAndJoinsCreationBeforeChangingScope`; `TestRootTransitionRetainsPendingGateDuringDelayedCreationRetirement` | Transition cancels and joins; delayed retirement keeps replacement admissions blocked and retains effect ownership. |
| Publication release versus owner transition | `TestCreationBarrierOrdersOwnerPendingAgainstPublicationRelease` (release-wins subcase); `TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification` | Release winner is classified before transition returns; confirmed effect is not falsely described as absent. |
| Post-effect completion uncertainty | `TestCreationPublicationDelayedJoinClassifiesUnknownWithoutRace`; `TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed`; `TestCreationUnknownOutcomeStopsProviderContinuation` | Published bytes remain; result is unknown, gate ownership stays blocked, and provider continuation/replay stops. |
| Close during delayed retirement | `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked`; `TestCreationPublicationDelayedJoinClassifiesUnknownWithoutRace` | Bounded close returns cleanup pending without discarding ownership. |
| Duplicate approved preparation / dispatch boundary | `TestServiceCreationProposalBindsScopeContentDestinationAndConsumesOnce`; `TestComposedCreationSuccessAndContinuationFailureDoNotReplay` | Changed arguments fail binding, identical preparation is consumed once, and a later call needs a new approval. A provider resend of the identical call after a lost external response is not separately simulated end-to-end. |

## Native evidence and limits

The earlier accepted run at source `39279e93080a2306f5ed1a702bd83ab852eb1055` remains historical. The intervening `native_unavailable` run at `e7c5fbd884682916602a23a9909c0f81938d3f4e` is also preserved in its original record and is not treated as a pass.

The later trusted-host run tested source `dbfd90974f0979832a9abc60e32aeb3c87399840` and recorded 12/12 `scoped_native_pass`. Environment: Go 1.26.11, Linux 7.0.0-42-generic amd64, regular UID 1000, trusted root-owned Bubblewrap 0.9.0, `ro-bind-fd`, local ext4 (mount ID 33). Archive SHA-256 `3d76449b95ba037db17d46b3404d36f398d993cd0af3c940904857be305236dc`; manifest SHA-256 `1822336d698e55d533cc71a3a54738885e99fdaff64b81b0cb51e9c3173d9a02`; report SHA-256 `ac66529ef8a9a8e562505c5964d080d01aeb040122be804fabe86959b82f7ce6`. The supplied archive is `komposyn-composition-native-results.ah0j_t7n.tar.gz` (and the preceding run is `komposyn-composition-native-results.y0n88dnv.tar.gz`). Evidence remains specific to the recorded worker image, profile and host. Intervening content is documentation/tests only; the lifecycle additions in this branch do not change worker enforcement or its native protocol.

Remaining gaps include syscall-level interruption behavior, exhaustive owner/cancellation timing, service-wide production owner coverage, broader hosts/filesystems/platforms, and an end-to-end provider resend of an identical call after a lost external response. The Go tests add runtime control-flow evidence; they do not widen the native claim.

## Enablement boundary

No production capability is enabled. The accepted prototype remains synthetic and fixture-only. The scoped native profile evidence does not establish production owner coverage or maintainer enablement approval. Issues #267, #315, #321 and #322 remain open pending their separate closeout criteria.
