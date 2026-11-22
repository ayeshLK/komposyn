# Creation integration protocol proposal (#321)

**Status:** the bounded disposable-fixture prototype has lifecycle behavior evidence following merged PRs [#343](https://github.com/ayeshLK/komposyn/pull/343), [#344](https://github.com/ayeshLK/komposyn/pull/344), and [#345](https://github.com/ayeshLK/komposyn/pull/345), plus a trusted-host scoped native acceptance run. Runtime tests cover admitted read/list/search drain through actual creation staging, delayed close/root retirement, final-validation owner arbitration, post-effect stale-result suppression, exact content limits, and no replay on continuation or unknown outcomes. The search lifecycle also defers joined-operation completion until snapshot cleanup and evidence updates finish. See the [lifecycle acceptance record](issue-321-lifecycle-acceptance.md) for evidence hashes and limits. The normal `cmd/harness` composition still does not register creation. Production owner coverage and separate enablement approval remain outstanding; creation remains unavailable to normal users. Issues #267, #315, #321 and #322 remain open.

The companion [mechanism proposal](creation-mechanism-proposal.md) selects a stage file plus `renameat2(RENAME_NOREPLACE)`. This document records how the fixture prototype joins the existing runtime, UI, provider and read-admission composition. The mechanism passed the recorded scoped Linux native cases, but arbitrary production scopes remain unavailable and no production creation capability is enabled.

## Source facts and coverage boundary

The Linux sandbox composition currently has one `linuxImplementation` mutex, an `ownerUpdateMu`, a protected-owner `revision`, auth/settings owner states, a `blocked` owner map, and a read coordinator. `Protect` serializes owner changes, advances the revision, blocks the owner, invalidates conversation state and read admissions, registers the new owner identity/routes, and then completes or leaves coverage pending. It is an owner-rotation operation, not a transient-stage operation. Calling it for a stage after consuming the create receipt would invalidate that save's captured state and could make a drain wait on its own operation.

Read admission parses the requested read/list/search operation, checks service and auth/settings state, begins an admission in the coordinator, waits for its receipt, then rechecks service and coverage revision. Preparation rechecks again. Execution and publication have runtime authority claims and worker lifecycle state. The Linux implementation's creation barrier now drains active read workers through `ActiveReadDrainer` while preserving completed captures and existing generations. This remains a fixture-only creation path: `cmd/harness` does not register a creation schema or effect path.

The trusted composition's declared owners are:

| Owner | Current declaration | What it establishes | Creation consequence |
| --- | --- | --- | --- |
| Auth store | Required; `cmd/harness` wires `auth.Store` to `Service.Protect` before load | Current effective route and registered protected identity after successful registration/rotation | Required owner must be registered and stable at preparation and effect. |
| Settings store | Required; independently wired to `Service.Protect` before load | Current effective settings route and registered identity | Same requirement as auth; auth-file relocation does not relocate settings. |
| Token Logger store | Absent in this chat composition; trusted dependency graph does not open/own a Token Logger Store | Only that this composition does not own that store | It says nothing about arbitrary SQLite databases under a user-selected directory. Such data remains unclassified. |
| Provider source | Unsupported; no source identity/open lifecycle is registered in chat composition | No route/identity completeness | Any scope whose eligibility depends on excluding provider-owned sources is unavailable. |
| Unknown root data | Unsupported; `ErrUnknownRootData` remains the host authorizer result | No generic user-tree completeness | Arbitrary selected scopes cannot currently be declared complete. |

An empty registration list or absent path is not proof of coverage for an
applicable required owner. The accepted creation-only contract in
[ADR-0009](../adr/0009-startup-directory-creation-scope.md) requires a complete
manifest for the sensitive components actually instantiated by the running
chat, plus stable route/identity generations. Coverage includes their effective
routes, pinned identities, aliases and owner-designated protected ancestors.
Only trusted composition wiring can declare a component uninstantiated. No
machine-wide scan, transcript scan, ignore rule, path-prefix rule or approval
can substitute for missing applicable owner coverage or override a known
protected resource.

The current implementation still has **no generally eligible creation scope**.
ADR-0009 selects the CLI startup directory, fixed for that run, including
existing subdirectories. Its accepted eligibility rule allows ordinary existing
project content without inventorying or classifying every entry. This
supersedes the earlier creation-only unknown-root requirement to attest all
selected-root data; it leaves read/discovery authorization unchanged. The
synthetic complete-owner manifest remains fixture evidence only. The typed
production owner-profile snapshot now records auth/settings as required and
Token Logger/provider-source ingestion as trusted not-instantiated declarations
for the current composition. The host captures the launch directory binding
before runtime navigation, but reports retained identity/movement validation
as deferred. Nested-parent handling, startup identity enforcement and new
native scope evidence remain required before separate model-facing enablement
review. See the [owner-profile foundation](issue-321-owner-profile.md).

## Proposed typed creation and approval path

The intended path reuses the existing composition seam rather than adding a provider-facing filesystem executor:

1. Trusted CLI composition captures the startup directory and its identity as the creation scope for that run, independently of later working-directory navigation. UI shows the scope and availability. Starting there selects scope but grants no file access; every creation still needs exact once approval. No runtime scope picker is introduced in the initial version.
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
| Stage visibility during read/list/search | Service-wide admission barrier closes before stage creation; runtime-admitted read/list/search workers are drained, search content join is held, prepared work is invalidated and completed captures retain their read binding. See lifecycle acceptance record. | Behavioral cases pass; trusted-host scoped native acceptance is recorded. Injected workers establish coordination behavior, not native cancellation of arbitrary syscall schedules. |
| Stage self-registration invalidating its receipt | Temporary exclusion uses barrier-local token/identity and does not call `Protect` or advance owner/policy/coverage generations. | Implement and prove one-consumer receipt binding remains unchanged through stage lifecycle. |
| Drain, cancellation, close and uncertain cleanup | Explicit `blocked` state retains gate and cleanup ownership; bounded join timeout and lost completion are classified unknown without reopening. Close and root transition are tested during delayed retirement. | Selected behavioral cases pass; exhaustive scheduling and syscall interruption remain unproven. Scoped native acceptance is recorded. |
| Protected owner coverage | ADR-0009 requires a trusted manifest for applicable composed owners, distinguishing uninstantiated, required, pending, stale, retired and unsupported states. Ordinary project entries do not require a whole-scope census. | Implement the accepted startup-scope and composition-owned coverage contract; known protection and incomplete applicable-owner checks still fail closed. |
| Auth/settings rotation ordering | Shared coordinator linearizes owner pending against stage-entry and publication-release permits; after-release ordering and delayed root retirement are covered behaviorally. | Selected cases pass; every transition checkpoint is not exhaustively enumerated. Scoped native acceptance does not establish production owner coverage. |
| Creation approval path | Typed runtime receipt binds exact bytes, target, parent, destination and generations; one-consumer queue; no prompt under effect lock; fresh approval on detected conflict. | Add create-specific UI/runtime/provider integration and verify exact rendering, stale arguments and no replay. |
| Stage-and-rename mechanism | Candidate has a single no-replace publication point; external races remain accepted. | Prototype authorization is separate; then native Linux amd64/ext4 evidence and reviewed implementation are required. |

Behavioral evidence should use the real composed sandbox Service and runtime/UI/provider seam. Cases include eligible/ineligible profile/schema and instructions, unsupported argument rejection, basic reads unchanged, same-profile admission and execution, exact approval/destination, argument-binding rejection, alias and root/policy/owner changes, protected alias filtering, incomplete owner refusal before approval/worker, closed service, cancellation, stale publication, private metadata stripping and descriptor cleanup. Creation-specific deterministic cases also cover active/queued reads, completed capture publication, barrier before visible stage, an owner transition after final filesystem validation but before publication-release arbitration, both owner-transition-wins and release-wins orderings, cancellation after release but before syscall, owner rotation at each transition, failure to join, double execution, collision at publication, parent moves, exact bytes/limits, continuation failure/no replay and external mutation after final check.

Native evidence is required for actual stage visibility, no-replace collision behavior, filesystem/kernel support, identity/cleanup and independent-actor residual races. Behavioral tests establish runtime/UI/provider composition and ordering only; ordinary tests and Go race tests do not prove native filesystem enforcement. Use synthetic disposable files and independent actors through the existing harness. Stop on missing coverage, unsupported primitive behavior, an unjoined worker or any need to change the accepted threat boundary. Current Go fixtures cover parsing, the opt-in runtime/UI approval path, service proposal binding/once consumption, active read/list/search drain, delayed close/root retirement, completed-capture handoff, cleanup/join uncertainty, owner ordering after publication release, content and serialized-argument limits, continuation failure/no replay, unknown-outcome stop, and worker protocol behavior. The trusted-host native run passed 12/12 scoped cases with the real Bubblewrap worker, including bounded creation and a parent-move/substitution actor case; provenance and profile limits are recorded in the [lifecycle acceptance record](issue-321-lifecycle-acceptance.md). Interrupted syscall outcomes and exhaustive transition schedules remain unproven.

## Prototype progress and remaining boundary

The assignment authorizes the non-model-facing disposable prototype. The service now has a retained-parent proposal path, a test-fixture owner-manifest gate, a service-wide barrier, owner-ordered permits and a fixed create-worker protocol with stage abort/no-replace publication. Behavioral lifecycle and trusted-host scoped native evidence are recorded separately. Production scope coverage and explicit model-facing enablement remain separate gates.
