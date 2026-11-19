# Creation integration protocol proposal (#321)

**Status:** design proposal following merged PRs [#343](https://github.com/ayeshLK/komposyn/pull/343), [#344](https://github.com/ayeshLK/komposyn/pull/344), and [#345](https://github.com/ayeshLK/komposyn/pull/345). A maintainer worker assignment authorizes the bounded disposable-fixture prototype only. The source has strict create-only argument parsing and an explicit opt-in runtime/approval composition, with provider/runtime and UI-bridge fixture tests. The Linux sandbox implementation now has a tested service-wide read barrier that invalidates admissions/preparations, blocks read authority commits and remains closed after incomplete cleanup. Stage-entry and one-use publication permits are ordered against owner pending under the same short service-state lock; owner rotation cancels and waits for barrier retirement. This is not yet connected to a creation effect. The complete synthetic owner manifest, confined stage worker, full lifecycle/race matrix and native study remain unimplemented. No production scope coverage, native acceptance or model-facing availability is established. Issues #267, #315, #321 and #322 remain open.

The companion [mechanism proposal](creation-mechanism-proposal.md) selects a candidate stage file plus `renameat2(RENAME_NOREPLACE)`. This document specifies how that candidate could join the existing runtime, UI, provider and read-admission composition. The syscall candidate remains unproven and all creation scopes remain unavailable.

## Source facts and coverage boundary

The Linux sandbox composition currently has one `linuxImplementation` mutex, an `ownerUpdateMu`, a protected-owner `revision`, auth/settings owner states, a `blocked` owner map, and a read coordinator. `Protect` serializes owner changes, advances the revision, blocks the owner, invalidates conversation state and read admissions, registers the new owner identity/routes, and then completes or leaves coverage pending. It is an owner-rotation operation, not a transient-stage operation. Calling it for a stage after consuming the create receipt would invalidate that save's captured state and could make a drain wait on its own operation.

Read admission parses the requested read/list/search operation, checks service and auth/settings state, begins an admission in the coordinator, waits for its receipt, then rechecks service and coverage revision. Preparation rechecks again. Execution and publication have runtime authority claims and worker lifecycle state. The current public `sandbox.Service` and runtime `FilesystemReadCompositionHost` are read-oriented; neither exposes a create effect or a create-owned read drain. `cmd/harness` wires auth/settings protection and read admission/preparation/execution to the same sandbox service. Provider tool schemas and UI approval are mediated by runtime, but there is no creation schema, receipt or effect path.

The trusted composition's declared owners are:

| Owner | Current declaration | What it establishes | Creation consequence |
| --- | --- | --- | --- |
| Auth store | Required; `cmd/harness` wires `auth.Store` to `Service.Protect` before load | Current effective route and registered protected identity after successful registration/rotation | Required owner must be registered and stable at preparation and effect. |
| Settings store | Required; independently wired to `Service.Protect` before load | Current effective settings route and registered identity | Same requirement as auth; auth-file relocation does not relocate settings. |
| Token Logger store | Absent in this chat composition; trusted dependency graph does not open/own a Token Logger Store | Only that this composition does not own that store | It says nothing about arbitrary SQLite databases under a user-selected directory. Such data remains unclassified. |
| Provider source | Unsupported; no source identity/open lifecycle is registered in chat composition | No route/identity completeness | Any scope whose eligibility depends on excluding provider-owned sources is unavailable. |
| Unknown root data | Unsupported; `ErrUnknownRootData` remains the host authorizer result | No generic user-tree completeness | Arbitrary selected scopes cannot currently be declared complete. |

An empty registration list or an `OwnerAbsent` declaration is not a coverage proof for arbitrary user data. Eligibility requires a complete owner manifest for the selected scope and target, supplied by trusted composition owners, plus stable route/identity generations. Coverage includes canonical routes, pinned object identities, aliases and protected ancestors supplied by those owners. No machine-wide scan, transcript scan, ignore rule or path deny-list may fill missing coverage. User approval cannot override a known protected object or unsupported/missing owner.

The current composition therefore has **no generally eligible creation scope**. A future prototype may test a disposable fixture composition with explicit synthetic auth and settings owners, a declared complete synthetic provider-source inventory, a declared Token Logger owner with DB/WAL/SHM/journal/backup identities, and an explicit complete-scope manifest. That fixture validates protocol behavior only; it does not prove shipped host coverage. Before any real scope becomes eligible, maintainers must decide which trusted component can attest provider-source and unknown-root coverage for a user-selected scope. The choices are to keep all scopes unavailable until such registration exists, or to approve a narrower, clearly bounded scope contract with its own completeness evidence. The present design chooses no exception.

## Proposed typed creation and approval path

The intended path reuses the existing composition seam rather than adding a provider-facing filesystem executor:

1. Trusted UI selects a mutation scope independently of working-directory navigation. Host records the selected scope identity and generation. Selection itself grants no access.
2. Provider submits only a strict relative target and complete UTF-8 content. Runtime ignores model-supplied scope, identity, digest and generation. Host resolves one existing parent and observes an absent basename leaf. The runtime retains immutable bytes, digest, parent identity/descriptor, route, baseline, scope/policy/owner generations, conversation/turn/call identity and provider/model/auth-profile destination.
3. Host refuses before prompting if profile, service, owner completeness, parent, target or generations are unavailable or ambiguous. Known protected resources are never approvable.
4. Trusted UI receives a typed approval view containing exact target, operation, complete diff/content, byte count and exact provider/model/auth-profile destination. It returns an explicit once decision. It is bound to the immutable proposal and all identities/generations above. No conversation-level permission substitutes for this receipt.
5. Runtime validates the response and moves the proposal to queued state. On dequeue, it consumes the receipt exactly once before attempting the create-owned barrier. Barrier/drain failure consumes the receipt and ends that proposal without effect; the user must prepare a new proposal and grant fresh approval. There is no automatic retry or baseline rebase.
6. Host acquires the service-wide create effect lease, final-checks route, parent identity, owner manifest/generations and absent leaf, stages exact bytes, and publishes only using the reviewed no-replace primitive. The host returns a typed outcome, never a writable capability or descriptor to runtime/provider.
7. Runtime records one-shot completion before continuation. It publishes only while the originating conversation/provider destination remains current. Continuation failure, lost result or service restart never replays an effect.

Proposed interfaces (not implemented):

```go
type CreationRequest struct {
    ProposalID, ConversationID, TurnID, CallID string
    Destination, ScopeRoute, TargetRoute       string
    ScopeGeneration, PolicyGeneration          uint64
    OwnerGeneration                            uint64
    ParentIdentity, Baseline, ContentDigest     string
    Content                                     []byte // immutable copy, bounded
}

type CreationApproval struct { // opaque, host/runtime-owned; never provider-visible
    ProposalID, Binding string
    Generation          uint64
}

type CreationHost interface {
    PrepareCreation(context.Context, CreationRequest) (PreparedCreation, error)
    ExecuteApprovedCreation(context.Context, PreparedCreation, CreationApproval) (CreationOutcome, error)
    CancelAndJoinCreation(context.Context, string) error
}
```

Production signatures should prefer opaque typed values over string identities. `PreparedCreation` owns immutable proposal state and host descriptors and exposes metadata only; it has no provider-callable `Write`, `Open` or descriptor-transfer operation. Providers own tool schema and continuation wire formats. Runtime owns authorization, receipt consumption, generations, queueing, result lifecycle and stale-publication suppression. Host owns path resolution, owner checks, worker execution, joining and cleanup. UI owns trusted scope selection and presentation/once decision.

## Transient-stage/read barrier protocol

Use a create-owned barrier distinct from `Protect`, owner registration and coverage generations. A simple first design is service-wide: it pauses all new read, listing and search admissions, even outside the selected scope, for the short stage/publication window. This avoids alias, bind-alias and namespace-move gaps in path-scoped gating. It is a latency cost, not broader authority. Basic reads reopen only after the conditions below.

Proposed states are `open`, `closing`, `draining`, `active`, `retiring`, and `blocked`. One opaque barrier token identifies the create request. The barrier mutex protects only barrier/admission bookkeeping. It never spans UI/provider callbacks, worker waits, cancellation joins or filesystem calls.

1. After explicit once approval, runtime marks the proposal queued and consumes the receipt once. It asks host to acquire the effect lease. Under the host coordinator's short state lock, host verifies no other effect or barrier owns the service, sets `closing`, and atomically rejects new read/list/search admissions before any stage entry is created. The lock is then released. This ordering protects discovery names/counts as well as contents.
2. Host marks pre-existing, not-yet-dispatched read admissions stale and closes their coordinator references. It cancels active readers/listings/searches that could observe the selected parent/scope and waits for each worker and its descriptors to join. No stage is created until this drain is complete. A prepared but unclaimed request must fail its next authority claim; it cannot launch after the barrier closes.
3. A read worker that already completed before barrier acquisition has no live filesystem authority after join. Its immutable capture is not a stage observation and may proceed through the existing publication gate if its own read authorization generations remain current. The barrier does not alter those generations. A running worker is cancelled/joined rather than allowed to publish a potentially stage-observing capture. If the implementation cannot prove the worker completed before the barrier's admission linearization point, it is treated as active and drained.
4. If any worker cannot join, any descriptor remains unaccounted, or any admission cannot be invalidated, transition to `blocked`; do not create the stage or release the read gate. Keep cleanup ownership and report unavailable/unknown. Do not wait while holding runtime, service, owner-update, effect-admission or barrier state mutexes.
5. Once drained, transition to `active` and retain the barrier token. Before creating the stage entry, host takes a short-lived stage-entry permit under the same coordinator that records owner-transition-pending state. If owner transition or cancellation wins that lock, no stage entry is created. If the stage-entry permit wins, the worker creates the stage while no KompoSyn reader can run. The token records the temporary route/identity after creation; this is private host exclusion metadata, not an owner-registration revision. Then write, sync, close and publish or clean up using the candidate mechanism.
6. Real auth/settings/provider owner updates remain independent. An owner update first marks its transition pending under the shared effect-state coordinator, blocking new create/read admissions; it requests cancellation and drains outside locks, then registers/rotates routes and identities and advances owner/coverage generations. If pending wins before stage-entry permit, no stage is made. If stage-entry permit wins first, the update cancels the effect; it must still win the separate publication-release race before publication can be authorized. The transient barrier never advances or refreshes owner, policy or coverage generations and never revives the consumed approval.
7. On confirmed publication or confirmed pre-publication cleanup, host closes stage/parent/worker descriptors and confirms the temporary name is absent (or is now the confirmed published leaf). It enters `retiring`; after all worker joins and result classification, it removes the temporary exclusion and atomically reopens read admissions if service, owner state and cleanup are healthy. Existing completed captures may publish only under their unchanged authorization bindings.
8. If publication may have happened but identity/result processing is uncertain, preserve the one-shot outcome as `partial_or_unknown`; never delete a possible committed file or replay. If the helper cannot join, cleanup is incomplete, or a temporary entry may remain, transition to `blocked` and retain the token/ownership. Reads remain closed until an explicit host recovery operation proves the worker joined, descriptors closed and temporary entry disposition. No automatic sweeper or restart-time deletion is assumed. Service close sets `closing`, cancels and joins active workers, retires only confirmed resources, and returns an error while incomplete cleanup remains; process exit is not evidence of successful cleanup.

The service-wide barrier excludes all in-process reads during its active interval, including requests through aliases or after namespace moves. It cannot prevent an external process from reading a same-user-visible stage, nor does it establish mutation isolation. That external race is part of the accepted #343 boundary. The transient route/identity record prevents future KompoSyn admission from treating the stage as ordinary data; it is not a substitute for owner coverage.

### Owner transition and publication-release linearization

The in-memory ordering point must be shared by owner transitions, cancellation,
stage-entry admission and publication release. A final filesystem check performed
outside the coordinator lock is only an observation; it cannot alone authorize
publication. After those bounded checks, the worker requests a one-use release
permit in a short critical section:

```text
AcquireStageEntry(token, expected generations):
    lock effectState
    reject if closed, cancelled, ownerTransitionPending, or generations changed
    mark stageEntryAdmitted
    unlock effectState

ReleasePublication(token, expected generations):
    lock effectState
    reject if closed, cancelled, ownerTransitionPending, or generations changed
    mark publicationReleased       // linearization point; one use only
    unlock effectState
    return permit

BeginOwnerTransition(owner):
    lock effectState
    mark ownerTransitionPending; reject later stage/release permits
    cancel affected effects
    note any effect whose publicationReleased bit is already set
    unlock effectState
    wait for affected effect tokens and joins; classify released effects
```

If owner transition/cancellation wins before `ReleasePublication`, no permit is
issued and the stage is cleaned up under the still-closed read barrier. If
`ReleasePublication` wins first, the owner transition treats publication as
potentially in flight, requests cancellation, and waits for the token's actual
outcome before completing owner rotation. The worker may observe cancellation
before calling the syscall or race with/invoke it; the permit does not claim to
hold a lock across the syscall. A released permit is never reused. The outcome
is classified from the worker and filesystem evidence; uncertainty remains
`partial_or_unknown`, with no replay. This defines which internal transition
wins without strengthening the accepted external-race boundary.

### Lock and wait order

Use a logical order, not nested long-lived locks:

1. Runtime proposal/queue mutex: validate state and consume receipt; release before host calls.
2. Host effect lease: acquired by reservation under the service coordinator lock; it is a logical exclusive token, not a mutex held while waiting.
3. Barrier/admission state: briefly close admission and snapshot active workers; release before cancellation/join.
4. Shared effect-state coordinator: atomically order owner-pending, cancellation, stage-entry admission and one-use publication release; release before joins, filesystem calls or registration I/O.
5. Worker execution: filesystem operations and joins with no runtime/service/owner/barrier mutex held.
6. Retirement: short coordinator transaction confirms joins, descriptor cleanup, owner state and outcome; then opens admissions.

If an owner transition races an acquired effect lease, the shared coordinator determines whether stage-entry admission or publication release already won. A transition that wins first prevents the corresponding permit; a transition after publication release cancels and waits for lease retirement and outcome classification outside the lock. If publication has already happened, report the confirmed or unknown outcome and complete cleanup before owner rotation completes. Never wait for the create worker from that worker's own completion path. Never hold an effect mutex across user approval. The barrier is acquired only after the UI response and receipt consumption, and a failed barrier attempt does not restore the receipt.

## Cancellation, conflicts and publication outcomes

Before the no-replace publication syscall, cancellation stops further writes, closes the worker, removes only the stage whose identity still matches the request, and reopens reads only after join and confirmed cleanup. Cancellation at or after the syscall is resolved from observed primitive/result state; it cannot promise no effect. Owner/policy/scope change invalidates proposals and prevents stale result publication; it does not turn a possibly published file into a rollback. Double invocation finds the receipt already consumed or the proposal terminal. Existing leaf at final check is a conflict; a leaf appearing at the primitive is a no-replace collision. Both require a new proposal and explicit approval. Never read or disclose the colliding object's content, and never offer overwrite/append/merge in this creation path.

The worker/helper reports `not_executed` only when publication is known not to have happened; `created` only when the published entry matches the frozen bytes and completion checks; `conflict` for detected pre-effect change or primitive collision; `failed` for confirmed pre-publication failure with stage cleanup; and `partial_or_unknown` if publication, cleanup, identity or result is uncertain. A provider continuation failure after a confirmed effect remains a completed one-shot effect and cannot repeat on retry.

## Evidence and readiness

| Blocker / claim | Protocol resolution | Still required before readiness |
| --- | --- | --- |
| Stage visibility during read/list/search | Service-wide admission barrier closes before stage creation; active work is cancelled/joined; prepared work is invalidated; completed captures retain only their own read binding. | Implement a real drain/linearization seam and integration cases for active, queued and completed work, aliases and pending publication. |
| Stage self-registration invalidating its receipt | Temporary exclusion uses barrier-local token/identity and does not call `Protect` or advance owner/policy/coverage generations. | Implement and prove one-consumer receipt binding remains unchanged through stage lifecycle. |
| Drain, cancellation, close and uncertain cleanup | Explicit `blocked` state retains gate and cleanup ownership; no reopen before join, descriptor closure and stage disposition. | Integration and native cases at each transition, incomplete join, service shutdown and uncertain publication. |
| Protected owner coverage | Eligibility requires complete trusted manifest; absent, required, pending, stale, retired, unsupported or ambiguous states are distinct; unknown selected-root data fails closed. | Resolve provider-source and unknown-root scope completeness. Current production composition cannot make arbitrary scopes eligible. |
| Auth/settings rotation ordering | Shared coordinator linearizes owner pending against stage-entry and publication-release permits; no state lock spans syscall/join. | Implement and test owner transition before stage permit, between final filesystem validation and release, after release but before syscall, and after publication. |
| Creation approval path | Typed runtime receipt binds exact bytes, target, parent, destination and generations; one-consumer queue; no prompt under effect lock; fresh approval on detected conflict. | Add create-specific UI/runtime/provider integration and verify exact rendering, stale arguments and no replay. |
| Stage-and-rename mechanism | Candidate has a single no-replace publication point; external races remain accepted. | Prototype authorization is separate; then native Linux amd64/ext4 evidence and reviewed implementation are required. |

Behavioral evidence should use the real composed sandbox Service and runtime/UI/provider seam. Cases include eligible/ineligible profile/schema and instructions, unsupported argument rejection, basic reads unchanged, same-profile admission and execution, exact approval/destination, argument-binding rejection, alias and root/policy/owner changes, protected alias filtering, incomplete owner refusal before approval/worker, closed service, cancellation, stale publication, private metadata stripping and descriptor cleanup. Creation-specific deterministic cases also cover active/queued reads, completed capture publication, barrier before visible stage, an owner transition after final filesystem validation but before publication-release arbitration, both owner-transition-wins and release-wins orderings, cancellation after release but before syscall, owner rotation at each transition, failure to join, double execution, collision at publication, parent moves, exact bytes/limits, continuation failure/no replay and external mutation after final check.

Native evidence is required for actual stage visibility, no-replace collision behavior, filesystem/kernel support, identity/cleanup and independent-actor residual races. Behavioral tests establish runtime/UI/provider composition and ordering only; ordinary tests and Go race tests do not prove native filesystem enforcement. Use synthetic disposable files and independent actors through the existing harness. Stop on missing coverage, unsupported primitive behavior, an unjoined worker or any need to change the accepted threat boundary. Current Go fixtures cover parsing, the opt-in runtime/UI approval path and the Linux Service's read-barrier state transitions. They do not exercise a composed creation host, owner/publication arbitration, a worker or file creation.

## Prototype progress and remaining boundary

The assignment authorizes the non-model-facing disposable prototype. The opt-in runtime/UI/provider seam and Linux Service barrier are partial slices; they do not satisfy the internal integration milestone. The service-state lock now orders owner pending against stage and publication permits, but no worker yet consumes those permits. Continue by composing a real sandbox Service with a complete synthetic auth/settings/provider-source/Token Logger owner manifest, then connect the barrier and permits to a retained-parent worker with truthful cleanup/outcomes. The first acceptance milestone remains barrier ordering, owner fail-closed behavior, once-consumer semantics and cleanup under deterministic runtime actors. Only after it passes should the existing native harness exercise stage publication and accepted residual external races. Production scope coverage and explicit model-facing enablement remain separate gates.
