# Issue #321 creation lifecycle acceptance

This record tracks behavioral lifecycle evidence separately from native filesystem evidence and production enablement. The normal `cmd/harness` composition does not register creation. Startup-scope preparation and terminal movement behavior now have focused behavioral coverage; new trusted-host native evidence for those claims and separate production enablement remain outstanding.

## Fixed-scope runtime and UI integration

The normal CLI captures the trusted launch directory through
`NewHostServiceWithStartupDirectory` before runtime navigation. Runtime snapshots
carry a detached fixed-scope status to the UI separately from
`WorkingDirectory`; `/cd` and conversation reset do not recapture it. The local
header and `/help` identify both values and explain that normal creation is
unavailable because its schema is not registered. The fixed path is not added
to provider instructions, requests, or diagnostics.

`TestFixedStartupScopeCreationUsesRealServiceAndApprovalBridge` composes the
test-only schema with the real sandbox Service, retained startup descriptor,
host proposal and preparation path, actual auth/settings owner registrations,
runtime once authorization, and the existing UI `ApprovalBridge`. The settings
route is reserved while absent; auth is then populated through its registered
store path. It checks the exact nested target, complete multiline/trailing
newline diff, provider/model/auth-profile destination, no conversation grant,
no effect before approval, one explicit once response, and exact resulting
bytes. Its worker adapter is deterministic behavioral evidence; it does not
establish Bubblewrap or kernel enforcement.

The availability view distinguishes tool registration from retained-scope and
owner readiness. A complete scope cannot make normal chat available: absence
of the creation schema remains the dominant `not_registered` state. The
private prototype gate is never set by production composition. Scope status is
informational and the Service repeats all authorization and generation checks
at preparation, dispatch, and publication.

## Behavioral evidence

Focused tests use the real composed sandbox service and runtime seam where applicable. They cover:

| Lifecycle claim | Evidence |
| --- | --- |
| Barrier closes admission before stage creation and drains active reads | `TestCreationBarrierWaitsForActiveReadWorkerToJoin`; active worker is cancelled and joined without changing read generations. |
| Runtime-admitted list and search cannot overlap stage creation | `TestCreationBarrierDrainsRuntimeAdmittedDirectoryWorkers` exercises composed list and search admission against the real sandbox service while an actual bounded creation execution waits on its barrier. It holds after directory capability transfer, asserts the worker received the descriptor, proves the stage/destination are absent until join, and confirms creation proceeds afterward. Search cases skip when `Service.SearchFilesAvailable()` is false or the fixture is not ext4. `TestCreationBarrierJoinsRuntimeAdmittedSearchContentWorker` additionally holds the content worker. Injected workers establish lifecycle control flow, not kernel enforcement. |
| Completed private read captures remain publishable | `TestCompletedReadCaptureSurvivesTransientCreationBarrierBeforeProviderHandoff`; the transient barrier does not revoke already completed captures. |
| Effect remains one-shot across continuation failure | `TestComposedCreationSuccessAndContinuationFailureDoNotReplay`; a later turn requires its own call and approval. |
| Cleanup uncertainty blocks admission and retains ownership | `TestCreationCleanupFailureRetainsBarrierAndDoesNotRetry`; `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked`. |
| Service close and root transition preserve pending ownership while creation retires | `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked` checks bounded close with a held worker; `TestRootTransitionRetainsPendingGateDuringDelayedCreationRetirement` verifies replacement admission remains blocked until cleanup and a retry completes the root transition. |
| Lost completion classification cannot replay | `TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed`. |
| Owner transition and publication release are ordered | `TestCreationOwnerTransitionWinsAfterFinalValidationBeforePublicationRelease` pauses the actual execution path after final validation; `TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification` covers the release-wins path and a concurrent alias attempt. |
| Effect committed before runtime/provider publication | `TestComposedCreationOwnerChangeAfterEffectSuppressesResultAndRejectsDuplicateCall` holds the composed worker after the filesystem effect, rotates an owner, and verifies the outcome is unknown with no provider continuation or second dispatch. |
| Startup scope is retained across navigation and movement is terminal | `TestStartupScopeMovementIsTerminalAfterReplacementAndRestore` moves/replaces the startup root, restores the original object, and verifies navigation/reset do not clear the latch. This is a fake-worker behavioral test, not native filesystem evidence. |
| Existing nested parents remain scoped; changed parents and invalid targets refuse before approval | `TestCreationResolvesNestedTargetFromFixedStartupScopeAndRejectsChangedParent` and `TestCreationPreparationRejectsUnsupportedTargetsBeforeApproval` cover ordinary project files, nested lookup, stale-parent refusal, escapes, symlink components/leaves, missing/non-directory parents and collisions. |
| Startup-root replacement at the release checkpoint prevents publication and joins the worker | `TestStartupRootMoveAtPublicationCheckpointAbortsPreparedCreation` observes the real preparation/execution boundary with a deterministic fake worker; the separate native actor case remains outstanding. |

Existing creation tests continue to cover once binding, retained-parent checks, root/service transitions, worker protocol, byte limits and collision behavior. `TestCreationPrototypeEnforcesContentAndSerializedArgumentLimitsBeforeApproval` checks exact 64 KiB content and over-limit content/serialized arguments before approval; the composed service success test also exercises exact-limit and empty-file effects. `TestCreationUnknownOutcomeStopsProviderContinuation` confirms uncertain completion ends the provider turn without replay. These tests establish control-flow and lifecycle behavior; they do not establish kernel/filesystem enforcement.

The joined-search-worker race run exposed an ordering defect: the asynchronous
join waiter could retire an operation before the caller finished closing and
validating its private search snapshot. Operation completion now waits until
the execution path returns; the waiter still retains cleanup ownership and
completes a returned, cleanup-pending operation after join. This keeps snapshot
cleanup and final evidence updates inside the operation lifetime.

## Acceptance matrix

| Area | Existing evidence / candidate action | Observed result |
| --- | --- | --- |
| A. Barrier: queued/prepared and active read, list, search | Runtime-admitted read, list and search cases exercise the composed admission path. Search also holds the content worker while the barrier waits for join. | Focused cases pass. The injected list/search workers establish coordinator/drain behavior, not native kernel cancellation. |
| A. Completed capture, aliases, concurrent same-leaf create | Completed capture/provider handoff and same-parent alias/concurrent attempt are covered by composed tests; root/policy/owner invalidation remains covered by existing authority tests. | Covered cases pass; not every alias/leaf interleaving is exhaustively enumerated. |
| B. Drain/join/cleanup/publication uncertainty | Injected cleanup error, delayed join, and completion loss after publish assert unknown/blocked/no replay. | Covered cases pass; no recovery operation exists, so the gate remains blocked. |
| B. Close/root transitions and ownership | Delayed creation retirement is exercised across bounded service close and root transition; after-release owner transition ordering remains covered separately. | Selected cases pass; all scheduling interleavings are not exhaustively enumerated. |
| C. Cancellation and owner arbitration | `TestCreationOwnerTransitionWinsAfterFinalValidationBeforePublicationRelease` pauses the execution path after final filesystem validation; `TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification` covers a release winner; the composed post-effect test covers invalidation before runtime/provider publication. | Selected deterministic cases pass. Native syscall interruption and exhaustive schedules remain untested. |
| D. Successful composition and no replay | Real Service/runtime/provider fixture proves success followed by continuation failure and a separate approved next turn; exact 64 KiB and empty-file outcomes are exercised. `TestComposedCreationOwnerChangeAfterEffectSuppressesResultAndRejectsDuplicateCall` covers a possible effect before a stale result and the identical provider call retry. | Unknown/stale output is withheld; repeated call identity is rejected before another approval or worker. A new distinct proposal still requires fresh approval. |
| Native: worker, collision, parent movement | Trusted-host run for source `dbfd90974f0979832a9abc60e32aeb3c87399840` passed all 12/12 scoped native cases, including bounded creation and independent-actor parent movement/substitution. Interrupted write/sync/close/publication syscalls are not controllably induced. | Recorded `scoped_native_pass`; evidence is limited to the measured Linux amd64/local ext4/Bubblewrap profile below. |

### Named concurrency checkpoints

This is a finite scenario set, not an exhaustive enumeration of scheduler
interleavings:

| Checkpoint | Deterministic evidence | Outcome |
| --- | --- | --- |
| Before / during barrier drain | `TestCreationBarrierClosesAndReopensReadAdmissionAroundDrain`; `TestCreationBarrierWaitsForRuntimeAdmittedReadWorkerToJoin`; runtime-admitted list/search cases in `TestCreationBarrierDrainsRuntimeAdmittedDirectoryWorkers` | New admission closes; stale/prepared work cannot publish; held workers must join before the actual creation stage can start. |
| Search traversal and content-worker joins | `TestCreationBarrierJoinsRuntimeAdmittedSearchContentWorker` | Barrier remains pending while the content worker is held, then joins both phases without publishing stale output. |
| Stage entry versus owner pending | `TestCreationBarrierOrdersOwnerPendingAgainstPublicationRelease` (owner-pending-wins subcase) | Stage/publication permits fail after owner pending wins. |
| Final revalidation versus publication release | `TestCreationOwnerTransitionWinsAfterFinalValidationBeforePublicationRelease` | Owner pending cancels the lease before release; stage is removed and target remains absent. |
| Stage in progress versus root transition | `TestRootTransitionCancelsAndJoinsCreationBeforeChangingScope`; `TestRootTransitionRetainsPendingGateDuringDelayedCreationRetirement` | Transition cancels and joins; delayed retirement keeps replacement admissions blocked and retains effect ownership. |
| Publication release versus owner transition | `TestCreationBarrierOrdersOwnerPendingAgainstPublicationRelease` (release-wins subcase); `TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification` | Release winner is classified before transition returns; confirmed effect is not falsely described as absent. |
| Post-effect completion uncertainty / stale publication | `TestCreationPublicationDelayedJoinClassifiesUnknownWithoutRace`; `TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed`; `TestCreationUnknownOutcomeStopsProviderContinuation`; `TestComposedCreationOwnerChangeAfterEffectSuppressesResultAndRejectsDuplicateCall` | Published bytes remain; result is unknown, stale success is withheld, gate ownership stays blocked where cleanup is uncertain, and provider continuation/replay stops. |
| Close during delayed retirement | `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked`; `TestCreationPublicationDelayedJoinClassifiesUnknownWithoutRace` | Bounded close returns cleanup pending without discarding ownership. |
| Duplicate approved preparation / dispatch boundary | `TestServiceCreationProposalBindsScopeContentDestinationAndConsumesOnce`; `TestComposedCreationOwnerChangeAfterEffectSuppressesResultAndRejectsDuplicateCall`; `TestComposedCreationSuccessAndContinuationFailureDoNotReplay` | Changed arguments fail binding; same call identity after a possible effect is rejected before a second approval/worker; a genuinely new call requires fresh approval. |

## Native evidence and limits

The earlier accepted run at source `39279e93080a2306f5ed1a702bd83ab852eb1055` remains historical. The intervening `native_unavailable` run at `e7c5fbd884682916602a23a9909c0f81938d3f4e` is also preserved in its original record and is not treated as a pass.

The later trusted-host run tested source `dbfd90974f0979832a9abc60e32aeb3c87399840` and recorded 12/12 `scoped_native_pass`. Environment: Go 1.26.11, Linux 7.0.0-42-generic amd64, regular UID 1000, trusted root-owned Bubblewrap 0.9.0, `ro-bind-fd`, local ext4 (mount ID 33). Archive SHA-256 `3d76449b95ba037db17d46b3404d36f398d993cd0af3c940904857be305236dc`; manifest SHA-256 `1822336d698e55d533cc71a3a54738885e99fdaff64b81b0cb51e9c3173d9a02`; report SHA-256 `ac66529ef8a9a8e562505c5964d080d01aeb040122be804fabe86959b82f7ce6`. The supplied archive is `komposyn-composition-native-results.ah0j_t7n.tar.gz` (and the preceding run is `komposyn-composition-native-results.y0n88dnv.tar.gz`). This run predates the coordinator completion-ordering guard added in this branch. The guard prevents a joined-worker waiter from publishing completion while caller-owned search snapshot cleanup and evidence updates are still running. The run remains evidence for the unchanged worker enforcement, worker image, native protocol and measured host profile; it does not exercise or prove the new coordinator ordering. The new lifecycle and coordinator cases are covered by behavioral tests and the Go race suite. They do not widen the native claim.

Remaining gaps include syscall-level interruption behavior, exhaustive owner/cancellation timing, service-wide production owner coverage, and broader hosts/filesystems/platforms. The Go tests add runtime control-flow evidence; they do not widen the native claim.

## Enablement boundary

No production capability is enabled. The accepted prototype remains synthetic and fixture-only. The scoped native profile evidence does not establish production owner coverage or maintainer enablement approval. Issues #267, #315, #321 and #322 remain open pending their separate closeout criteria.
