# Exact-file read implementation plan: reconstructed planning proposal

**Status: reconstructed for review; disclosure policy unapproved; reads disabled.**
This is a new source-grounded plan, not exact recovery or verification of the
missing local handoff. It changes no runtime behavior, permissions, root UI,
coverage declaration or tool registration. Requesting this document, accepting
opened-object authority B, and merging #244 do not approve disclosure option B.

## Baseline, durable context and evidence limits

On 2026-11-07, upstream `refs/heads/master` was verified as
`f809a791f3fd4696ea0786d702f1b6ee116ae095`; the isolated documentation branch
started at that exact commit. GitHub reports [PR #244](https://github.com/ayeshLK/komposyn/pull/244)
merged there. This file was absent from that tree. The user's older local
`master` was not changed or used as current source. This is a Git checkout,
not an initialized source archive. Recheck upstream before implementation and
identify later changes separately.

Planning inputs are the current [issue #215](https://github.com/ayeshLK/komposyn/issues/215),
its [recovery checkpoint](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6708377457),
and fresh source inspection. The checkpoint preserves reviewed conclusions;
it is not proof that proposed APIs exist or that the missing final draft was
accepted. No unavailable local draft was used as verification material.

Read these durable documents with this plan:

- [Unregistered-file policy packet](read-broker-unregistered-read-policy.md).
- [Initial composition/coverage design](initial-sandbox-composition.md) and
  [implemented non-exposing service](composition-admission-sandbox-service.md).
- [Approved opened-object typed-read contract](read-broker-approved-typed-read.md)
  and its [decision comment](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377).
- [Historical #243 native acceptance](read-broker-composition-native-acceptance.md),
  [sanitized JSON](evidence/read-broker-composition-native-acceptance.json), and
  [preserved runner guide](../../scripts/native-acceptance/README.md).

The historical 9/9 service-fixture result tested #243 source
`d43a7dd7005ae6ccc8102936d00bd2920bbd9fcf` plus its separately hashed overlay.
It replaced the deny-only authorizer for synthetic fixtures and published to
an internal sink, with **no provider request**. The documented later attempt
was `native_unavailable` after backend trust refusal; unavailable is not an
isolation pass. Do not combine results or repeat #244 preservation work.
No proposed implementation or native tests were run for this reconstruction.

## One complete policy decision

Opened-object authority B is accepted direction: an independent ordinary
rename/alias alone need not invalidate the approved opened object. Registered
sensitive identities and authoritative owner transitions still dominate. This
is distinct from **unregistered-file disclosure B**, which remains unapproved.

A retains the current refusal for unknown root data. Recommended B permits a
bounded exact-file disclosure despite unknown contents, with the complete
lifecycle below. The single statement is the proposed policy authority for
this handoff; implementation details later in the document do not approve it.

### Copyable proposed maintainer acceptance statement

**Proposal only. Accept or revise all six clauses explicitly.**

> I accept the following bounded initial read-disclosure contract:
>
> 1. One otherwise-unregistered exact regular file may be executed as a read
>    once, after target-specific approval naming the selected provider/model
>    and auth profile and explaining disclosure there. The root is explicit
>    lookup scope only, with no cwd/home default and no permission from selection.
>
> 2. Unknown content may include credentials, databases, backups, provider
>    records or independent sensitive copies. Consent and opened identity do
>    not classify content as safe, freeze bytes or guarantee continuous live
>    pathname containment. This is not machine-wide secret detection.
>
> 3. Registered auth/settings routes, identities, aliases and retained retired
>    pins remain non-approvable through generic reads. Authoritatively classified
>    provider sources remain immutable and require separate per-invocation
>    source approval. Incomplete protected-directory coverage, unsupported
>    lookup semantics and unavailable enforcement still refuse.
>
> 4. Once permission covers one filesystem execution and an initial disclosure
>    commitment. Already committed content may appear in later requests only
>    within the same valid in-memory conversation/provider/model/auth-profile
>    binding, while root, policy, coverage, owner and authorization state remain
>    valid. Only tracked expected grant transactions preserve that validity.
>    Replay permits no additional read; reset or destination/profile changes
>    end that continuation scope.
>
> 5. Successful synchronized transcript commitment and immutable-request handoff
>    to the trusted ready dispatcher is the logical disclosure point. Invalidation
>    before it withholds undisclosed bytes. If handoff wins, later cancellation
>    is best-effort and may not stop physical transmission. No atomic socket-send
>    revocation or external recall is promised.
>
> 6. Invalidation of committed sensitive context blocks further replay and
>    retires the whole affected continuation, including derived/native state,
>    before another request. Late responses cannot repopulate it. Previously
>    displayed or externally delivered copies cannot be recalled.

Accepting only an earlier exact-file statement does not accept replay,
commitment or retirement. If all recommended clauses are accepted together,
no additional disclosure-policy answer blocks Stage 1. Revisions must be
reconciled across design, copy and tests before implementation. Choosing A or
leaving clauses unresolved keeps the B admission work blocked. Implementation
review, exact-profile native acceptance and tool enablement remain separate.

### Remaining decisions and tradeoffs

| Decision | Recommended initial answer, still unapproved | Alternative and practical consequence |
| --- | --- | --- |
| Unknown file | B: exact once-approved execution and named-provider disclosure, preserving mandatory denials. | A: retain refusal. An unregistered `notes.txt` can contain a copied credential; neither name nor confirmation proves safety. |
| Later context transmission | Allow already committed content only within clause 4's same valid binding, without another filesystem execution. State this in initial consent. | Fresh consent per later disclosure, or omit/reset sensitive context. Provider-native replay and assistant-derived text must be handled too; deleting one visible tool result does not prove omission. A follow-up about the file can otherwise resend its content. |
| Commitment point | Clause 5's synchronized logical handoff. Invalidation before it withholds; afterwards cancellation is best-effort. | Stop before handoff if still-revocable handling is required. A physical-send boundary needs a separate transport design and cannot promise recall from the provider. A root change after accepted handoff can be too late for that request. |
| Invalidated committed context | Clause 6: retire the whole affected continuation, including derivatives, and reject late responses. Usage may remain. | Selective redaction requires proven dependency tracking across raw, derived and native state. Conservative reset loses useful context but avoids silently retaining sensitive derivatives. |

An **execution retry/replay** runs the read again and needs fresh authority;
**context replay** resends already committed conversation content under clause 4.
The no-automatic-replay rule below concerns execution; it does not silently
cancel the separate proposed context-replay consent or authorize new reads.

### Draft user wording

These are proposed templates, not shipped UI. Use trusted, escaped target and
provider/model/profile labels; never credential values or raw file contents.

| Situation | Proposed text |
| --- | --- |
| Unregistered file | **Read `/work/project/notes.txt` once and share it with `{provider} / {model}` using `{profile label}`?** KompoSyn has not classified this file. It may contain credentials, databases, backups, provider records or sensitive copies. Its content may be sent again in later requests in this same valid conversation/model/profile context. Approval does not freeze bytes or permit another read. **Read once and share / Deny**. |
| Classified provider source | **Read classified provider source `/work/fixture/source.jsonl` once and share it with `{provider} / {model}` using `{profile label}`?** Separate source-specific approval is required; ordinary-file permission is insufficient. The source stays read-only. Its content may recur in later requests in this same valid conversation/model/profile context. **Read source once and share / Deny**. |
| Invalidation before commitment | **Content withheld; no new disclosure was committed.** If access occurred or is uncertain, say **The read completed or may have occurred; it will not be repeated automatically.** Keep the truthful execution outcome separate. |
| Invalidation after handoff | **Sharing was already committed; transmission may still occur despite cancellation. Further replay is blocked. The affected conversation context is being retired before another request, and late responses will be discarded. Already displayed or delivered content cannot be recalled.** |

Keep target/destination and replay/sensitivity explanation in the same approval
view. Cancel/redirect are not consent. Explain “withheld” and “sharing committed”
in ordinary language; do not show epoch counters, receipt details or lock state.
A new conversation can proceed after cleanup, but must not silently restore
retired context. Safe profile labels identify the destination without secrets.

## Fresh source audit and necessary corrections

All paths below describe the verified baseline, not proposed APIs.

| Source and symbols | Existing behavior | Consequence for the plan |
| --- | --- | --- |
| [Composition](../../cmd/harness/main.go): `newCommand` | Composes `sandbox.NewHostService`, injects it into independent auth/settings stores, registers protection before Load/ListMetadata, and bundles only conversation tools. | Keep actual owner hooks; no Token Logger store is opened here. Absence in this composition is not proof arbitrary databases are safe. |
| [Service](../../internal/harness/sandbox/service.go): `Prepares`, `PrepareTool`; [Linux implementation](../../internal/harness/sandbox/service_linux.go): `PrepareGenericRead`, `Protect`, `CoverageSnapshot` | Reserved `read_file` guard refuses unknown root data; no read tool exists. Coverage is host-owned and owner pending is latched through identity validation. Non-Linux remains unsupported. | No accidental enablement, fixture authorizer or caller completeness flag. Only accepted policy can change unknown admission; required owner/lookup/backend refusals stay independent. |
| [Runtime](../../internal/harness/runtime/tool_loop.go): `executeOne`, `ToolPreparer`, `ToolPreparation` | Validates, authorizes, reviews, reauthorizes, prepares, consumes once, executes and closes; failed cleanup is retained. | Existing post-approval preparation cannot supply exact-object classification to an earlier consent view. Add pre-approval admission while preserving preparation-before-consumption. |
| Same runtime: `bindAuthorizationRequest`, `sameAuthorizationBinding` | Replaces `Request.Binding` with a hash of invocation IDs, arguments and requirements. It does not independently include admitted identity, destination or host epochs. | Do not assume today's Binding proves the full read. Incorporate the host admission digest into the canonical read action/binding and ledger; merely populating overwritten `Request.Binding` is insufficient. |
| [Approval](../../internal/harness/permissions/approval.go): `Coordinator.Begin/Respond`; [store](../../internal/harness/permissions/permissions.go): `AddMany`, `ConsumeOnceMatchingAtRevision` | Respond snapshots/evaluates, inserts, then reevaluates separately. Insertion and consumption advance global store revision; consumption returns only error and can match zero once grants for otherwise-allowed requirements. | Checked insertion and authoritative atomic transition results are necessary. New read requires exact once disclosure evidence even if broad rules otherwise allow a path. |
| [Chat](../../internal/harness/runtime/chat.go): `RevokeToolGrant`, `ClearToolGrants`, `Select`, `clearConversationLocked`, `CloseContext` | Revoke needs a present grant; clear invalidates pending approval only if grants existed. Select clears conversation on destination change only when turns exist. | Consumed-operation revocation and empty-store clear need an owned-state bridge. Stage 1 must also clear/invalidate root on destination/profile change before the first turn; existing turn-count reset is insufficient. |
| [Host coordinator](../../internal/harness/readbrokerstudy/session_linux.go): `Read`, `Revoke`, `invalidateLocked`, `RegisterRotation` | Read reopens via `PrepareRead`, invokes `AuthorizeAndConsume`, then inserts into active map. Revoke advances independent host Grant generation. Barriers track active operations. | Do not reuse Read unchanged after runtime consumption. Register opening/pending ownership before lookup; extend owner barriers beyond active workers. Store revision and host Grant generation are not the same counter. |
| [Broker](../../internal/harness/readbrokerstudy/broker_linux.go): `PrepareRead`, `Revalidate`, `preparedRead.Close` | Constrained Linux regular-file lookup; Revalidate checks policy generation and incomplete directory coverage. Close clears owned fields even on close error. | Stronger retained-FD identity/classification checks are proposed, not existing. Never reopen or treat second nil Close as proof of first retirement. PrepareRead has no context parameter; a deadline does not prove kernel lookup stopped. |
| [Owner hooks](../../internal/harness/auth/protection.go): `UpdateJSONProtected`; [settings](../../internal/harness/settings/settings.go): `RegisterProtection`, `SaveContext` | Protect empty replacement before writing bytes. Service Protect currently holds its mutex through coordinator registration. | Extend pending-admission drain before sensitive writes and refactor lock order coherently; do not add a reentrant runtime callback beneath service locks. |
| Runtime `runToolLoop`, `appendToolBatch`; Chat `Send`, `Snapshot` | Tool results become strings, wait while later calls seek approval, enter history, and are cloned into StreamChat. Send can install loop.history on interrupted/budget paths. Snapshot exposes detached turns/activity, not raw replay history. | Private result slots and synchronized commitment must span these later boundaries; conversation generation alone is insufficient. Preserve snapshot separation. |
| [OpenAI](../../plugins/model-provider/openai/provider.go) and [Anthropic](../../plugins/model-provider/anthropic/provider.go) | Provider-owned function_call_output / tool_result conversion and native continuation. | Preserve wire formats and complete call/result pairing. Derived/native state prevents naive single-envelope redaction from proving context removal. |

These findings support the recovery checkpoint's three conclusions. The full
binding omission and zero-turn selection behavior are concrete integration
corrections, not reasons to reopen the agreed planning direction.

## Shared proposed binding and ordering domain

All interfaces and counters in the remainder are design sketches unless named
as existing above. Keep them internal and role-specific in the same Go module.

Define immutable binding `B` over conversation/turn/call/tool IDs, canonical
validated arguments, admission/service ID, requested root/route, retained object
identity/type/class, composition/profile identity, selected provider/model and
auth-profile identity/revision, exact action/resource requirements and disclosure
wording/version. Include root/policy/coverage/owner generations. Read-specific
canonical action hashing must include the host admission digest so existing
`bindAuthorizationRequest` cannot discard that binding. Once grant records and
the private ledger must correlate the exact approved IDs and B; path scope alone
is insufficient. UI/model/tool code cannot supply classification or completeness.

Use a small runtime-owned lifecycle/authorization/publication gate. Every relevant
grant mutation, owner pending/registration, root/policy/coverage update, reset,
provider/profile change, cancellation, shutdown and disclosure commit enters it
before becoming effective. Keep production store/registry mutation access private
to trusted composition. Unexpected/untracked revision changes invalidate, never
become accepted by silently refreshing snapshots.

Lock order: outer gate, then short permission-coordinator/store or host-service/
coordinator/registry sections. No subordinate holder calls back to reacquire the
gate. Refactor current owner entrypoints as required. No user wait, network I/O,
worker join or blocking owner callback inside broad locks. Mark pending and
cancel under the gate, drain outside, reenter to finalize. Runtime authority
validation under the gate uses a gate-aware ledger lookup, not recursive locks.

## Contract 1: permission transactions and continuing authority

Distinguish `G` (existing permission-store revision), `I` (proposed runtime
invalidation epoch), and `H` (existing host Grant invalidation generation).
They need not be numerically equal. Root/policy/coverage/owner/provider state
has separate freshness, never inferred from G alone.

| Boundary | Atomic evidence and required state |
| --- | --- |
| Admission / pending | Reserve host ownership, freeze B, capture G0 and current I/H/host epochs. Begin's immutable pending request must use that exact revision/binding. No consumption or release. |
| Accepted approval | Checked insertion at G0 validates pending identity/missing requirements, B and epochs; returns G1, inserted grant IDs and exact post-snapshot/ready decision. Retire pending response. |
| Preparation | Verify retained object and B at G1 with unchanged invalidation/host state. No release. Any unrelated mutation refuses preparation/consumption rather than rebasing to another G. |
| Runtime consume | Atomic exact-grant transaction at G1 returns G2, before/after and consumed IDs/binding. Require the read's exact once resource-disclosure grant and applicable action authority. Runtime installs a private execution record under the same gate. |
| Claim / release | Host validates private record membership, B/admission/service/epochs and atomically claims one attempt. Recheck immediately before retained-FD release after trusted bootstrap/probes. Deleted grants need not remain present. |
| Capture / commitment | Keep descriptor-free private evidence valid until commit/discard. Validate the transition chain and I/H/host state; raw current-G equality to G2 is not the publication contract. |

Recommend narrow store transactions: `ApproveAtRevision(...) -> {before, after,
insertedIDs, postSnapshot, decision}` and `ConsumeExactOnceAtRevision(...) ->
{before, after, consumedIDs, binding}`. Snapshot/evaluation/insertion must form
one checked transaction, with policy immutable or gate-controlled. A later
Snapshot is not authoritative evidence of the earlier result. Preserve general
conversation-tool grant behavior; enforce the stronger once-disclosure rule
only for this read path.

Only exact approval/consume transactions registered for validated invocations
in this active turn leave I/H unchanged. Record each Gbefore/Gafter, B and IDs.
For example, A reaches G2; B's valid approval/consume advances G3/G4 without
widening A or invalidating its private slot. An arbitrary AddMany, a delta of
one or a same-conversation label is not an exemption. Unrelated mutations
invalidate affected read state; conservative initial implementation may
invalidate all owned read state in the conversation. Changed state before
consumption needs fresh admission/consent, not approval reuse.

Runtime alone issues an execution reference from successful consumption into
its private ledger. Host validates through an injected trusted validator,
including owning service/admission, B, exact transition and one-shot status.
A caller-filled receipt/revision or opaque ID with no membership validation is
not proof. This is trusted-host encapsulation, not isolation of malicious Go
code already in the host. Failed launch or uncertainty never restores consumed
authority; a duplicate claim fails even if the first attempt did not release.

Revoke consumed invocations/results through owned operation identity independent
of removed grant IDs. Clear/revoke-all/reset must advance invalidation and
cancel owned state even when the grant store is empty. Once data was committed,
clause 6 also retires affected continuation. Unrelated changes while B waits
invalidate A; B's later response cannot legitimize them. Keep detached UI status
separate from authority issuance. No automatic execution retry after uncertainty.

## Contract 2: retained admission and protected-owner lifecycle

Do not call unchanged `HostCoordinator.Read` after runtime consumption: it
reopens and authorizes/consumes again. Add a narrow production path over the
retained admitted object, keeping historical study entrypoints usable and
separate. No fixture authorizer or caller completeness flag in production.

Conceptual internal contract:

```text
Host.Admit(ctx, validatedBinding) -> AdmissionRef, detachedDisclosure
Host.PrepareAdmitted(ctx, AdmissionRef, acceptedApprovalRef) -> PreparationRef
Runtime.ConsumePrepared(ctx, PreparationRef, G1) -> ExecutionAuthorityRef
Host.ExecuteAdmitted(ctx, PreparationRef, ExecutionAuthorityRef) -> PrivateCaptureRef
Host.CloseAdmission(ctx, AdmissionRef) -> confirmedRetirement | cleanupPending
Runtime.StageCapture(ctx, PrivateCaptureRef) -> PendingResultRef
```

Host alone constructs admission/preparation records; validate references by
owning instance, private map membership and legal state. Runtime alone creates
accepted-approval/execution evidence from Contract 1, host validates it. A
reserved-read authorized-execute interface needs that evidence explicitly;
unchanged `ToolPreparation.Execute(ctx)` alone does not receive it. Model tools
and UI receive neither handles, general FDs, registry, auth store nor provider
client. Their detached metadata cannot mint authority.

| State | Ownership and obligations |
| --- | --- |
| opening | Reserve cancellable lifecycle/cleanup record before lookup. Recommend one pending retained-file admission per active turn and a five-minute trusted admission deadline; existing invocation/result budgets bound later slots. Expiry is cancellation, never consent. |
| pending_approval | Perform constrained lookup exactly once; reenter gate to attach target/parent FDs only if still current, otherwise attach solely for retirement. Hold privately, unread and untransferred. |
| prepared | Validate the same FD's regular type/identity and current protected-route/identity/class/ancestor coverage, accepted B/G1 and owner pending state. Transfer logical ownership within the record, without reopening. |
| consumed/claimed, launching/ready | Runtime consumes, host claims one attempt. Start fixed networkless worker, verify bootstrap/probes; only then duplicate the same FD and perform final validation/serialized release. Host owns original/parent and transport; worker owns received copy until joined. |
| released/captured_private | Bounded private capture, truthful execution outcome and cancellation/join. No history/provider/UI content exposure. Invalidation still suppresses undisclosed bytes. |
| retired or cleanup_pending | Confirm target/parent/duplicate/transport retirement and worker join. Stage only descriptor-free private metadata/bytes. Unconfirmed resources remain owned and block affected admission/owner writes until bounded safe retry; one-shot tombstones prevent duplicate execution without retaining FDs in history. |

A preparation refusal does not consume authority; cancellation/denial/redirect,
expiry, root/policy/coverage/owner change, reset/provider change or shutdown
retires the owned record at every state. Admission deadline runs until release;
worker execution/cleanup uses bounded existing limits thereafter. Since current
PrepareRead lacks context and kernel lookup may not stop on a timer, timeout
must retain the opening obligation, prevent release and retire any late FD.
Do not claim cleanup complete until that operation acknowledges retirement.

Reuse the current Linux openat2 constraints: beneath the selected root, no
symlinks or magic links, no mount crossing, and regular files only. Unsupported
lookup semantics refuse; no directory enumeration or fallback path is added.
Strengthen revalidation by fstat of the retained FD, exact identity/type match,
current registered requested-route/identity class and supported protected
ancestor/directory-coverage checks under the owner gate. Do not reopen by
pathname or substitute a new object after path replacement. An independent
ordinary rename/alias alone follows accepted opened-object authority: original
approved object or refusal, never the replacement. This is neither continuous
pathname containment nor a frozen byte snapshot. Authoritative registration,
rotation or changed classification still invalidates and requires fresh consent.

Extend host records/barriers to opening, pending, prepared, worker and captured
undisclosed states. Owner update sequence:

1. Under the shared gate, mark coverage pending, advance relevant epochs,
   invalidate affected records/slots, dismiss pending consent and cancel work.
2. Drop broad locks; drain host-only admissions and in-progress lookup, terminate
   and join workers, discard private payloads. Unconfirmed drain retains
   cleanup/pins and refuses admission and sensitive owner-write completion.
3. Register reserved route and empty replacement identity, verify the owner's
   opened descriptor, retain retired pins, and clear pending only after complete
   validation/drain. Only then return success for sensitive writes. Errors or
   cancellation leave pending; a fresh checked retry is necessary.

An empty active-worker map proves nothing about a pending host FD. Keep owner
latches effective while cleanup runs unlocked. `preparedRead.Close` currently
clears fields even after error: the new wrapper must retain retirement evidence
without retrying a reused raw numeric FD or treating another no-op Close as
confirmation. Retry safe higher-level join/retirement duties; quarantine
indeterminate retirement. New work resumes only with current coverage and every
applicable cleanup obligation confirmed. No fallback to unrestricted I/O.

## Contract 3: private results, commitment and later replay

Execution returns bounded private slots, not raw read ToolResult strings.
After confirmed worker join/FD retirement each slot retains B, call/admission
identity, root/policy/composition/coverage/owner epochs, I/H, execution-ledger
reference, cleanup status and truthful outcome, but no descriptor/worker.
Retain validity until commit/discard even though worker resources are gone.

Batch drafts contain safe envelopes or slot references. Do not materialize
undisclosed bytes in loop.history, Chat.history, detached snapshots/requests,
activity, diagnostics, logs or provider messages while a later call waits.
Existing turn/activity snapshot separation remains. Validate every slot and
all replay construction, including later turns, through the shared gate.

```text
reserve a ready single-dispatcher slot outside the gate
build a private batch draft / candidate immutable request
enter lifecycle/authorization/publication gate
  validate destination/conversation, root/policy/coverage/owners, I/H,
           exact expected transition chain, every slot and cleanup
  if stale or dispatcher cannot accept:
    discard private payloads; retain only truthful correlated safe envelopes
  else as one visibility transition:
    commit complete transcript batch
    hand one immutable request to the ready trusted dispatcher
    mark disclosure committed and retire private-buffer ownership
exit gate
dispatcher runs StreamChat/network outside broad locks
```

Ready-dispatcher acceptance is a nonblocking, one-shot ownership transition,
not an unvalidated queued draft. No provisional history is visible if acceptance
fails; readers must share the commitment boundary. Do not describe separate
check/unlock/StreamChat on a reusable draft as atomic. The successful logical
handoff is clause 5's irreversible authorization, not physical transmission.
All relevant invalidations must enter the same ordering gate before taking
effect, not merely send eventual callbacks. If invalidation wins, withhold;
if handoff wins, cancel best-effort without a no-transmission/recall guarantee.

For stale A while B waits, or stale state after draft assembly, discard all
new undisclosed payloads in the affected batch, even still-valid siblings.
Keep protocol-complete tool-call/result pairing with original IDs and bounded
payload-free envelopes. Stop sensitive continuation; never automatically rerun
reads. Redirect, cancel, request/result-budget exit, shutdown, failed cleanup
or any exit without immediate authorized dispatch also discards new private
payloads. A completed-but-withheld read remains succeeded (or its actual
partial/unknown/cancelled outcome) with separate withheld status; it must not
be relabeled not_executed. No hidden buffer waits in ordinary history for later
consent. Cleanup-pending still blocks new requests as existing runtime requires.

On commitment, migrate descriptor-free disclosure provenance (B, dependency
epochs and affected replay version) into a private continuation record until
that continuation is retired. Releasing a private buffer must not erase the
metadata needed for later invalidation; no worker/FD stays attached to history.
After commitment, only accepted clause 4 allows context replay under the same
valid binding. On invalidation of sensitive committed context, clause 6 blocks
further dispatch and retires the whole affected continuation, including raw
results, assistant-derived and provider-native state. Centralize history writes
so `Chat.Send`'s loop.persist path, detached drafts, cached requests or late
responses cannot reinstall retired content. Invalidate the affected replay
version before releasing the gate; old in-flight deltas/responses must not
repopulate new context. Usage may remain; already displayed/external copies
cannot be recalled. Fresh-context chat may resume after cleanup. Do not rewrite
provider-owned native formats or claim one envelope redaction removes derivatives.

## Staged implementation and evidence

Every stage is a separate reviewable change. Stage 1 is the first assignment,
not shorthand for the pipeline. Conversation tools remain unchanged; read_file
stays unregistered through implementation and native study. Production
registration requires a distinct enablement decision after all applicable gates.

| Stage / dependency | Scope and likely symbols | Acceptance / stays disabled |
| --- | --- | --- |
| 0: explicit complete policy decision | Record accepted/revised clauses in issue/policy/status documents through a separately authorized change. | All policy choices resolved explicitly; A or unresolved B means no unknown-file admission. This planning task records no acceptance. |
| 1: after 0, root state/UI/lifecycle only | UI Client/Snapshot and tview supportedCommands/runCommand; Chat root state, Select/reset/CloseContext; detached service status. Explicit absolute select/status/clear, no default/persistence/grant. | Chat works with no root/backend; paths with spaces, invalid/relative inputs, canonical status, replace/clear generations, fake invalidation notifications; confirmed reset/provider change/shutdown clears even with zero turns, canceled reset preserves. Unknown gate/tool availability unchanged. No target lookup, retained FD, grant API, worker or provider integration claim. |
| 2: after 0–1, checked authority | Permission Coordinator/store transactions; runtime canonical full-B binding, private ledger, mutation bridge and revoke/clear lifecycle. | G0/G1/G2 exact evidence, stronger read-specific once requirement, expected siblings, unrelated mutation refusal, consumed revoke/empty-store clear, duplicate/uncertain no replay. No filesystem tool enabled. |
| 3: after 1–2, retained admission/owners | Runtime pre-approval seam; sandbox service; broker and HostCoordinator operation registry; real auth/settings owner hooks. | One lookup, bound retained object, no second consumer; denials and owner pending/drain before sensitive writes; timeout/late FD and prelaunch cleanup ownership. Historical study path remains separate. |
| 4: after 2–3, fixed-worker capture | Authorized-execute interface, fixed worker bootstrap/probes/release, private capture and resource retirement. | One claim/release, bounded result and truthful outcomes, join/closure before descriptor-free staging, failed cleanup retained/retried. Bytes remain private; no provider or enablement claim. |
| 5: after 2–4, batch/provider composition | executeOne/runToolLoop/appendToolBatch, Chat.Send history ownership and ready dispatcher; capturing fake provider. | Both invalidation windows, reverse ordering, positive multi-call/replay scope, protocol-safe exits, retired derived/native state and late-response rejection. No live inference; read_file unregistered. |
| 6: after implementation review, exact-profile native study, then separate enablement review | New native runtime/owner/worker/fake-provider composition with exact source/overlay/backend/mode provenance. | Native effects and cleanup demonstrate changed profile; missing/untrusted/unsupported cases unavailable, never fallback. Historical #243 evidence is not substituted. Tool registration, supported routes/platforms and disclosure copy reviewed separately. |

Stage 1 tests use synthetic roots, simulated UI and bounded fake lifecycle
consumers. They cannot prove future admission drain, worker cleanup or provider
withholding; Stages 3–6 supply that evidence. Root alone is memory-only; existing
auth/UI-settings persistence remains. Backend status is independent of access,
coverage and approval. No caller can mark coverage complete by selecting a root.

If B is accepted, Stage 0 must reconcile unknown-file applicability with trusted
composition declarations: unknown content can include unregistered sources,
but authoritatively classified sources still need their separate approval.
Do not relabel unsupported source-owner integration as implemented or use a
synthetic inventory to bypass required coverage. Missing required declared
owners and incomplete directory coverage still refuse. Real source-owner
integration remains a separate explicit requirement wherever claimed. Token
Logger hooks become required only when a composition opens/undertakes that Store;
absence here says nothing about arbitrary unknown DB files.

## Proposed validation matrix

All rows are requirements for future implementation, **not performed tests**.
Use synthetic files/temporary directories, capturing fake providers, explicit
barriers and counted operations; no real credentials, provider records,
production databases or inference. Unit/integration and actual backend evidence
answer different questions. Test-only authorizers may not enter production.

| Contract | Deterministic unit/integration evidence | Later native evidence |
| --- | --- | --- |
| Protected objects | Direct registered routes, aliases, retired pins/replacements refuse before approval/consume/start; source class cannot use ordinary approval. | Actual owner/worker denials and fixture preservation. |
| Unknown file | A refuses; accepted B alone offers exact target/destination once consent. No consent means no descriptor release. | Exact changed policy/profile, not #243 fixture bypass. |
| Expected transactions | Atomic G0/G1/G2/IDs/decision; exact full-B cannot be substituted with same path or caller Binding; broad rules cannot replace required once grant. | Runtime sole consumption and one release recorded. |
| Unexpected changes | Unrelated mutation during pending response/preparation fails; consumed invocation revoke and empty-store clear invalidate owned state; duplicate claim and uncertain outcome never replay. | Separate runtime/host generation evidence; expected sibling advances are not failures. |
| Retained target | Count one lookup; substitute path after admission: original object or refusal, never replacement. Ordinary rename/alias follows opened-object contract. | Independent namespace actor plus exact fixture bytes; no live-path containment claim. |
| Pending owner barrier | Hold lookup/approval, rotate/register owner; no stale release or sensitive write until drain. Bound pending count/lifetime; late FD remains owned after timeout. | Actual retained FDs/owner writes and confirmed cleanup, not empty worker-map inference. |
| A captured, B awaiting approval | Invalidate A before commitment; resolve/cancel B. All affected undisclosed markers absent from committed history, snapshots and every fake-provider request. | Worker join/private capture plus runtime/fake-provider path, not just private sink. |
| Batch assembled before handoff | Pause draft, invalidate root/policy/coverage/authorization, then release. No bytes in transcript or outgoing/later request. | Exact integrated profile with deterministic checkpoints. |
| Handoff before invalidation | Accept immutable request, pause fake transport, invalidate. Truthful committed status, best-effort cancel; reject late response and retire all raw/derived/native replay before next request. | Ordering demonstrated without atomic physical-send or recall claim. |
| Positive multi-call/replay | A/B consume tracked sibling grants, each object read once; same valid context may replay committed data under accepted policy, never execute another read. Destination/reset/invalidation ends reuse. | Fixed worker plus capturing fake provider; no actual endpoint traffic. |
| No-continuation exits | Redirect/cancel/budget/cleanup failure retain truthful correlated payload-free envelopes and complete call/result protocol; no stale loop.persist/draft resurrection. | Selected held-operation cases with join/retirement evidence. |
| Later turn after invalidation | Raw, assistant-derived and native markers cannot return through old history assignment, cached draft/request or late deltas/responses. | Same changed profile; cannot prove external copies were erased. |
| Root lifecycle / availability | Stage 1 state-only tests as above; later pending/worker/capture invalidation, reset/shutdown and cleanup retries; no root persistence. | Missing/untrusted backend or unsupported mode/platform stays unavailable with no privileged/unrestricted fallback. |
| Cleanup uncertainty | Failure before worker and after possible release remains owned; bounded safe retries, no raw-FD reuse or no-op-Close proof; effects/outcomes remain truthful. | Confirm actual join/FD retirement and blocked owner/new-work behavior. |

Keep separate source/overlay/backend/launch-mode hashes, per-case effects,
withholding/commit ordering and cleanup evidence for the new native profile.
Skipping or backend refusal is not a pass. Do not claim shell, mutations,
directory enumeration/recursion, other platforms/modes, all descendant/resource
limits or arbitrary-root safety from these tests. No generic API/plugin/MCP,
persistent project/session/task/conversation or new module is introduced.

## Conditional first implementation-worker prompt

Use only after explicit policy acceptance/reconciliation; this is copyable
future task text, not authorization to implement during this planning review.

> Implement Stage 1 only of the reviewed issue #215 exact-file read plan.
> First quote explicit maintainer acceptance of the complete consolidated
> disclosure contract (or a fully reconciled revision), including replay,
> logical handoff and continuation retirement. An old exact-file statement,
> authority B, merged #244 or a planning request is insufficient. If absent,
> return a design-only report and preserve current behavior.
>
> Read repository conventions, verify current upstream source and preserve
> unrelated work in an isolated checkout. Implement explicit in-memory
> /fs-root selection/status/clear with an absolute path, no cwd/home default,
> no permission from selection, detached canonical root/backend status, and
> ordinary chat usable without a root/backend. Clear root and issue lifecycle
> notifications on confirmed reset/provider/model/profile change and shutdown,
> including before the first turn; canceled reset preserves it. Do not persist
> root or change existing credential/UI-settings ownership.
>
> Keep read_file unregistered, generic-read refusal and real owner protections
> intact. No target admission, retained FDs, grant-store APIs, worker execution,
> private capture or provider publication in this assignment. Add synthetic
> root/UI/lifecycle tests with bounded fake invalidation consumers and run the
> repository-appropriate checks. Report exact commands/outcomes and later-stage
> limits; do not claim native read acceptance or tool enablement. No real
> credentials/data/inference, permission bypass, shell, mutation, enumeration,
> MCP or persistent conversation/platform expansion. Submit only this focused
> root stage for review; Stages 2–6 remain separate assignments and gates.

## Review and validation record

This reconstruction freshly inspected the source seams above and durable issue
context. It confirms the checkpoint's interface concerns; it adds explicit
corrections for canonical binding, zero-turn model changes and uncancelled
in-flight lookup ownership. Proposed APIs/counters and tests do not exist merely
because they are listed. No approval or new native result is recorded here.

For the documentation change, verify local links, source symbol references,
Markdown code fences, whitespace and a diff limited to documentation. Reconfirm
the isolated checkout baseline and preserve user's worktree. Go/native suites
are unnecessary because no executable code changed; report only actual checks
in the review handoff. Keep the issue open and publish nothing automatically.
