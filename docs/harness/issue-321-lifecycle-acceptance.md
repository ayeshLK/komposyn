# Issue #321 creation lifecycle acceptance

This record tracks behavioral lifecycle evidence separately from native filesystem evidence and production enablement. Merged master `f79e754b1cba327aaeda78964e9672b4c50f88ce` does not register creation. This unmerged change proposes the shipping CLI wiring, gated to the measured host/startup profile. Startup-scope preparation and terminal movement behavior have focused behavioral coverage. The historical PR #355 and PR #356 runs each had 17 measured cases and an unavailable mount-boundary case; those results remain `native_unavailable`, not passes. A later exact-current-master run measured all 18 cases, including mount-boundary refusal on a real nested bind mount, as passes. The exact candidate run for PR #359 exercised the shipping registration path and measured its native creation case as pass, but the mount-boundary case was unavailable, leaving the aggregate `native_unavailable`.

## Fixed-scope runtime and UI integration

The normal CLI captures the trusted launch directory through
`NewHostServiceWithStartupDirectory` before runtime navigation. Runtime snapshots
carry a detached fixed-scope status to the UI separately from
`WorkingDirectory`; `/cd` and conversation reset do not recapture it. The local
header and `/help` identify both values. This change proposes schema
registration only when the trusted Service has inspected the Linux amd64,
Bubblewrap `ro-bind-fd`, retained local ext4 profile; unsupported profiles
continue to report creation unavailable. The fixed path is not added to
provider instructions, requests, or diagnostics.

`TestFixedStartupScopeCreationUsesRealServiceAndApprovalBridge` composes the
profile-gated schema with the real sandbox Service, retained startup descriptor,
host proposal and preparation path, actual auth/settings owner registrations,
runtime once authorization, and the existing UI `ApprovalBridge`. The settings
route is reserved while absent; auth is then populated through its registered
store path. It checks the exact nested target, complete multiline/trailing
newline diff, provider/model/auth-profile destination, no conversation grant,
no effect before approval, one explicit once response, and exact resulting
bytes. Its worker adapter is deterministic behavioral evidence; it does not
establish Bubblewrap or kernel enforcement.

The availability view distinguishes tool registration from retained-scope and
owner readiness. Schema registration is profile-derived, while incomplete
owner coverage, root movement, closure, or cleanup still makes the capability
unavailable. The separate private fixture gate and synthetic manifest are not
used by shipping composition. Scope status is informational and the Service
repeats all authorization and generation checks at preparation, dispatch, and
publication.

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
| Startup-root movement at named checkpoints is classified and the worker is joined | `TestStartupRootMoveAtPublicationCheckpointAbortsPreparedCreation` observes the real preparation/execution boundary with a deterministic fake worker. Native cases `TestNativeServiceStartupRootMovementIsTerminal` and `TestNativeServiceStartupRootMovementAfterPublicationRelease` measured pass on the recorded host profile; the latter demonstrates ADR-0008's allowed retained-object effect, not strict live-pathname containment. |

Existing creation tests continue to cover once binding, retained-parent checks, root/service transitions, worker protocol, byte limits and collision behavior. `TestCreationPrototypeEnforcesContentAndSerializedArgumentLimitsBeforeApproval` checks exact 64 KiB content and over-limit content/serialized arguments before approval; the composed service success test also exercises exact-limit and empty-file effects. `TestCreationUnknownOutcomeStopsProviderContinuation` confirms uncertain completion ends the provider turn without replay. These tests establish control-flow and lifecycle behavior; they do not establish kernel/filesystem enforcement.

The joined-search-worker race run exposed an ordering defect: the asynchronous
join waiter could retire an operation before the caller finished closing and
validating its private search snapshot. Operation completion now waits until
the execution path returns; the waiter still retains cleanup ownership and
completes a returned, cleanup-pending operation after join. This keeps snapshot
cleanup and final evidence updates inside the operation lifetime.

## Acceptance matrix

### ADR-0009 claim-by-claim disposition

The historical PR #355 native run tested exact candidate
`d9d0dfc3230c0bb05e48f981b562ec66e133d1be` on Linux amd64/ext4 with trusted
Bubblewrap descriptor binding (`ro-bind-fd`). Seventeen of 18 records are
`measured_pass`; the mount-boundary case is `unavailable`. The corrected
follow-up run at `34be8c54fa7ebb5a355ce8c8ccbc6fc89f818f7b` supplied a scope,
but that scope had no pre-existing nested mount, so that case was also
unavailable. Those historical aggregates are `native_unavailable` (exit 2),
not passes. The later exact-current-master run measured all 18 cases as passes;
see the [native acceptance record](issue-321-native-acceptance.md#nested-mount-boundary-follow-up)
for its provenance and hashes.

| Contract / observable claim | Behavioral evidence and seam | Native case / checkpoint / profile | Disposition and remaining action |
| --- | --- | --- | --- |
| Fixed launch scope survives `/cd` and conversation reset; scope selection is not authority; exact reads keep their independent rules. | `TestCreationStatusKeepsTrustedStartupScopeSeparateFromNavigation`, `TestFixedStartupScopeCreationUsesRealServiceAndApprovalBridge`, `TestBannerSeparatesFixedCreationScopeFromWorkingDirectory`; runtime snapshot, real Service/runtime/UI bridge and rendered tview. | `TestNativeServiceStartupRootMovementIsTerminal` and `TestNativeServiceStartupRootMovementAfterPublicationRelease` measured pass on the profile above. The exact PR #359 candidate also measured `TestNativeServiceNestedCreationUsesStartupScope` pass with `creation_schema_registered: true`. Navigation/reset remain behavioral claims, not native claims. | Activation path and movement checkpoints measured pass. The PR #359 run is still aggregate `native_unavailable` because its mount-boundary case had no pre-existing nested mount. See the [candidate record](issue-321-native-acceptance.md#shipping-composition-candidate-run-2026-11-24). |
| Ordinary project entries do not require a whole-tree census; existing nested parents resolve beneath the retained scope. | `TestCreationResolvesNestedTargetFromFixedStartupScopeAndRejectsChangedParent`, `TestCreationPreparationRejectsUnsupportedTargetsBeforeApproval`, real sandbox Service. | `TestNativeServiceNestedCreationUsesStartupScope` measured pass and records ext4/mount ID. | Implemented and behaviorally/native tested for the recorded profile; broader filesystems and profiles remain unestablished. |
| Applicable protected routes, identities, aliases, designated ancestors, and required owner coverage remain enforced; absent auth/settings routes remain covered by trusted lifecycle registration. | `TestHostCompositionRegistersIndependentAuthAndSettingsRoutes` (default paths and independent auth/settings overrides), `TestCreationFoundationUsesTrustedOwnersAndFixedStartupScope` (required auth/settings plus not-instantiated Token Logger/provider-source owners and absent reserved routes), `TestRetainedStartupPreparationUsesCurrentAuthSettingsOwners` (present and absent route preparation plus auth/settings rotation), `TestCreationFoundationRejectsStaleCoordinatorOwnerAndRefreshes`, `TestServiceCreationProposalBindsScopeContentDestinationAndConsumesOnce`, and owner transition tests. | `TestNativeServiceScopeAndTargetRefusalsPreserveFixtures`, `TestNativeServiceProtectedRoutesAndAliases`, `TestNativeServiceRotationJoinsHeldWorker`, and `TestNativeServiceAuthRotationJoinsHeldWorker` measured pass using actual auth/settings lifecycle. The absent auth route is refused; hard-link alias destinations are occupied collisions, proving no replacement, not a separate identity-classification reason. | Behavioral and listed native cases measured pass. Historical synthetic four-owner evidence remains distinct and is not composition coverage. |
| Exact once approval binds target, bytes/diff, and provider/model/profile destination; changed arguments cannot reuse it. | `TestFixedStartupScopeCreationUsesRealServiceAndApprovalBridge`, `TestServiceCreationProposalBindsScopeContentDestinationAndConsumesOnce`, runtime once/denial/cancellation tests, UI approval bridge. | `TestNativeServiceNestedCreationUsesStartupScope` drives Service preparation and approved preparation; it measured pass but does not exercise UI presentation or a live provider. | Behavioral/UI-provider boundary and native host Service case measured. A live provider is outside scope and not required. |
| Nested parent replacement or movement before approval release invalidates that operation and cannot silently rebind; it does not terminally disable an otherwise unchanged startup scope. | `TestCreationResolvesNestedTargetFromFixedStartupScopeAndRejectsChangedParent`. | `TestNativeServiceNestedParentSubstitutionRefusesPreparedProposal` measured pass: fixed same-UID actor moves the retained parent and creates a substitute between preparation and approved preparation; no effect/stage/worker, identity hashes recorded, actor joined. | Behavioral and native checkpoint measured pass for the recorded profile. This does not establish every scheduling interleaving. |
| Direct/nested escape, outside navigation, symlinked components/leaves, missing/non-directory parents, collisions, protected target routes refuse before approval/worker; ordinary existing content remains usable. | `TestCreationPreparationRejectsUnsupportedTargetsBeforeApproval` and the valid nested proposal tests at the Service seam. | `TestNativeServiceScopeAndTargetRefusalsPreserveFixtures` measured pass after a valid nested baseline; checks the refusal matrix, absent protected route, unchanged outside sentinel, no extra worker, and no stage. | Behavioral and native evidence measured for the recorded profile. |
| Mount crossing is refused by descriptor-relative resolution. | Existing behavioral target preparation tests do not establish a real mount boundary. | `TestNativeServiceMountBoundaryRefusal` measured pass on source `c0af7cda7bbbce3de289367af9e468ea8bea73df` with an authorized private ext4 scope and a real nested bind mount; it recorded distinct mount IDs 33 and 157. The direct-child proposal was prepared and discarded without execution; the crossing proposal was refused before worker launch, with no target or private stage. | Measured for the exact Linux amd64/ext4 and Bubblewrap `ro-bind-fd` profile. This does not enable creation or establish other filesystems, profiles or platforms. See the [native evidence record](issue-321-native-acceptance.md#nested-mount-boundary-follow-up). |
| Detectable startup-root move/replacement before worker launch is terminal even after restoration; an already released effect is classified truthfully under ADR-0008's residual race. | `TestStartupScopeMovementIsTerminalAfterReplacementAndRestore`, `TestStartupRootMoveAtPublicationCheckpointAbortsPreparedCreation`; deterministic fake-worker Service execution. | `TestNativeServiceStartupRootMovementIsTerminal` and `TestNativeServiceStartupRootMovementAfterPublicationRelease` measured pass: actor move/replace/restore, retained-object publication at the named after-release checkpoint, and terminal future refusal. | Named pre-worker and after-release checkpoints measured. Do not claim strict live-pathname containment, rollback, or exhaustive race coverage. |
| Cancellation, drain/join, cleanup ownership, uncertain outcomes, stale publication and no automatic replay remain intact. | `TestCreationBarrierWaitsForRuntimeAdmittedReadWorkerToJoin`, `TestCreationBarrierDrainsRuntimeAdmittedDirectoryWorkers`, `TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked`, `TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed`, `TestComposedCreationOwnerChangeAfterEffectSuppressesResultAndRejectsDuplicateCall`, plus close/root transition tests. Real Service/runtime/provider seam with controlled worker timing. | Native held-worker read cases measured pass for process retirement; the creation stage worker has no native syscall interruption case. Startup actor scenarios record join and stage disposition. | Behavioral lifecycle tested at named checkpoints, not exhaustive scheduling. Native creation syscall interruption is unproven and explicitly deferred; do not expand this PR into a new enforcement project. |
| Initial profile refuses unsupported backend/profile and does not widen model-facing capability. | `TestShippingCreationCompositionUsesServiceEligibilityAndKeepsChatTools`, `TestUnsupportedStartupCreationProfileDoesNotBlockHostStartup`, `TestHostCompositionCannotActivateDeferredOwnerOrBypassUnknownGate`, and the shipping runtime composition helper. | Historical records identify trusted Bubblewrap 0.9.0 using `ro-bind-fd` on Linux amd64/ext4; they do not exercise the new activation path. | Other backends, bind modes, filesystems, and platforms remain unavailable. Fresh native acceptance must exercise shipping activation before this PR is ready to merge. |

The historical 12/12 run at `dbfd90974f0979832a9abc60e32aeb3c87399840`
predates PRs #352–#354 and this overlay. It is evidence only for its recorded
worker/fixture and Linux amd64/ext4/`ro-bind-fd` profile. It does not prove the
current owner composition, startup scope, UI integration, the cases above, or
production enablement. See the preserved hashes and failures in the native
record below.

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

The earlier accepted run at source `39279e93080a2306f5ed1a702bd83ab852eb1055` remains historical. The intervening `native_unavailable` run at `e7c5fbd884682916602a23a9909c0f81938d3f4e` and corrected #356 run at `34be8c54fa7ebb5a355ce8c8ccbc6fc89f818f7b` are also preserved as unavailable historical results. The latter reached mount discovery but its supplied scope contained no nested mount. The new measured result is recorded separately in the [native acceptance record](issue-321-native-acceptance.md#nested-mount-boundary-follow-up).

The later trusted-host run tested source `dbfd90974f0979832a9abc60e32aeb3c87399840` and recorded 12/12 `scoped_native_pass`. Environment: Go 1.26.11, Linux 7.0.0-42-generic amd64, regular UID 1000, trusted root-owned Bubblewrap 0.9.0, `ro-bind-fd`, local ext4 (mount ID 33). Archive SHA-256 `3d76449b95ba037db17d46b3404d36f398d993cd0af3c940904857be305236dc`; manifest SHA-256 `1822336d698e55d533cc71a3a54738885e99fdaff64b81b0cb51e9c3173d9a02`; report SHA-256 `ac66529ef8a9a8e562505c5964d080d01aeb040122be804fabe86959b82f7ce6`. The supplied archive is `komposyn-composition-native-results.ah0j_t7n.tar.gz` (and the preceding run is `komposyn-composition-native-results.y0n88dnv.tar.gz`). This run predates the coordinator completion-ordering guard added in this branch. The guard prevents a joined-worker waiter from publishing completion while caller-owned search snapshot cleanup and evidence updates are still running. The run remains evidence for the unchanged worker enforcement, worker image, native protocol and measured host profile; it does not exercise or prove the new coordinator ordering. The new lifecycle and coordinator cases are covered by behavioral tests and the Go race suite. They do not widen the native claim.

Remaining gaps include syscall-level interruption behavior, exhaustive owner/cancellation timing, service-wide production owner coverage, and broader hosts/filesystems/platforms. The Go tests add runtime control-flow evidence; they do not widen the native claim.

The exact-current-master follow-up at `c0af7cda7bbbce3de289367af9e468ea8bea73df`
measured 18/18 native cases as `scoped_native_pass`, including the nested
mount-boundary refusal. Its archive SHA-256 is
`00a4ffccd3c4acf156faae120a863e04ffc1a74f185702b4f1e0acafc6077f59`, manifest
SHA-256 is `53aad50556c103fb2e2bda2b0ea4dbc47d6f77061b3bc597e69f531cd7cd2f23`,
and report SHA-256 is
`f378d34c3b87bad6691c3979e42e0c318a0b5e28a871c2dfd130694d867ff2f1`. The
recorded environment is Go 1.26.11, Linux `7.0.0-42-generic` amd64, regular UID
1000, trusted root-owned Bubblewrap 0.9.0 with `ro-bind-fd`, and ext4 startup
scope mount ID 33 with nested mount ID 157. It establishes this one measured
host/profile only. It does not establish production owner coverage, broader
profiles/platforms, syscall interruption behavior, or model-facing enablement.

## Enablement boundary

This unmerged change proposes the first production-composition registration of
creation. The recorded 18/18 native run supports the unchanged worker and
enforcement path for its measured host profile, but it does not measure this
new activation path. The enablement PR must remain draft until an exact
candidate native run exercises that path and maintainer review is complete.
Issues #267, #315, #321 and #322 remain open pending their separate criteria.
