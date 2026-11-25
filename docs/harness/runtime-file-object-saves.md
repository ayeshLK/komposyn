# Runtime file objects and approved saves

Status: accepted creation-boundary decision. PR #343 merged on 2026-11-19.
**Post-merge implementation:** PR #359 later delivered the creation mechanism
and profile-gated composition in source for Linux amd64/local ext4 with trusted
Bubblewrap `ro-bind-fd`; the exact candidate's scoped native acceptance is
recorded in [#321 native acceptance](issue-321-native-acceptance.md). This
document remains the boundary decision, not the implementation record, and the
merge does not imply a new tagged binary release. See
[ADR-0008](../adr/0008-propose-runtime-file-objects-and-approved-saves.md).

## Intended boundary

KompoSyn represents a file as a conversation-local runtime file object and
coordinates its own operations on that object. External applications may
change the file. Before attempting a save, KompoSyn checks the observed
filesystem state and asks the user to approve the exact proposed change.
Detected conflicts require a new proposal and approval.

The user approval records intent; it is not a filesystem lock or an atomic
compare-and-write. External changes can happen during the approval prompt or
after the final check and before or during the effect. The proposed contract
accepts that residual race and makes no guarantee of isolation against a
hostile process running with the user's authority. The same limitation applies
to approval-channel or staged-handle interference by such a process. This is
a reduced guarantee, not a new enforcement mechanism for the old contract.

## Relationship to existing decisions

Issues [#267](https://github.com/ayeshLK/komposyn/issues/267) and
[#315](https://github.com/ayeshLK/komposyn/issues/315) retain their original
published wording, with a creation-only applicability note to identify this
accepted boundary. The [deferral](mutation-deferral.md) and [creation closeout](issue-319-closeout.md)
preserve their historical contract and results. PR #343 accepts the
concurrency/approval boundary only; it does not accept the mechanism proposed
in this follow-up or retroactively accept a rejected candidate. Editing,
rename and delete requirements are unchanged.

Existing-file editing remains a later independent operation. The file-object
vocabulary may inform its future design, but this creation decision does not supersede
[ADR-0007](../adr/0007-proposed-exact-revision-bound-file-editing-contract.md)
or meet #322's gate. No operation is enabled by this proposal.

The separate [editing-boundary ADR](../adr/0010-propose-approved-entry-replacement-for-file-editing.md)
records the selected direction to replace the approved directory entry and
accept a post-observation external race. It does not alter this creation
decision. No editing mechanism or capability is established.

## File object and ownership

- The host owns the object's identity, observed baseline, proposed bytes and
  generation. A model-provided path, digest or object ID cannot grant access.
- An existing-file baseline, if editing is later designed, comes from an
  approved read in the current conversation. The in-memory snapshot does not
  assert that disk contents remain unchanged. Creation starts with an intended
  parent and leaf and an observed absent destination; it needs no prior read.
- Coordinate preparation, approval, save and retirement across KompoSyn's own
  operations. A per-path mutex alone is insufficient if two runtime objects
  refer to the same file or to the same creation parent/leaf through aliases.
  Implementation must define identity reconciliation and serialization.
- Keep these objects, proposals and activity in memory. No conversation
  database, persistent execution history or additional provider disclosure is
  introduced.

## Save and approval flow

1. Prepare one complete change within the independently user-selected mutation
   scope. Scope selection and working-directory navigation grant no write
   authority. Validate supported text, size, target type and protected-owner
   coverage before asking for approval.
2. Observe the target through the proposed host filesystem boundary. If it
   differs from the baseline, stop the old proposal and explain the conflict.
   Do not consume an old receipt to authorize a different change.
3. Present the exact target, operation and complete proposed diff for explicit
   once approval immediately before the save attempt. Bind the proposal,
   baseline, proposed bytes, scope/policy generations and provider destination.
   A prompt can remain open while another application changes the target.
4. After approval, revalidate the observed target and applicable generations
   before the effect. A detected conflict invalidates the receipt and returns
   to conflict review. Do not automatically retry through an approval loop.
5. Attempt only the approved operation using its reviewed effect primitive.
   Creation must refuse an existing destination at the creation/publication
   primitive, including one appearing after the last observation. There is no
   fallback from create to truncate, overwrite or append.
6. Publish a truthful result. Verification afterward can detect a discrepancy;
   it cannot prove that no intermediate change happened. Do not label arbitrary
   current disk contents as the successfully saved proposal merely because the
   operation returned. A missing completion or possible effect is unknown or
   partial as appropriate; never automatically replay it.

Own-operation serialization spans validation and the effect, with proposal
generations revalidated after user interaction. Cancellation, revocation and
scope changes invalidate affected proposals, cancel and join affected workers,
and suppress stale publication. An incomplete join retains cleanup ownership
and blocks affected admission. Completed effects are not rolled back by claim.

## Conflict choices

Cancel and prepare a new proposal are always safe workflow choices. Showing
newly observed content in an approval UI or sending it to a provider needs
the applicable read/disclosure authorization; conflict detection does not
silently grant a new read or provider disclosure.

Overwrite, append or merge can be offered only when that operation has its
own reviewed and enabled path. Each produces a new complete diff and a fresh
once approval against a fresh observed baseline. They are not a generic
permission to overwrite future versions. In the initial creation-only slice,
an existing destination is a refused creation; the user can cancel or propose
another target. Existing-file operations remain unavailable.

## Protections retained and limits made explicit

Retain UTF-8 text and 64-KiB limits, one-file proposals, link/type refusals,
complete-diff approval, supported provider/auth routes, scope and policy
generations, no automatic replay and fail-closed missing owner coverage.
Credentials/settings, provider sources and owned Token Logger state remain
protected resources. An overwrite approval cannot override their protections.
Existing read/list/search enforcement and accepted profiles are unchanged.

Runtime protection checks must still reject known protected aliases and
owner transitions. They must not claim to defeat every hostile external
mutation between check and effect under this proposed boundary. The concrete
creation design must document those residual risks and supported profiles
instead of inheriting the old effect-point guarantee by wording alone.

## Remaining decisions and review gates

This document selects a concurrency and approval boundary, not a syscall or
publication algorithm. The next creation implementation proposal must name:

- Creation staging and absent-leaf publication, descriptor ownership and
  retirement, actual file ownership/permissions, and partial/unknown outcomes.
- Runtime object identity and alias reconciliation, approval generation
  binding, and protected-owner registration/rotation and cancellation order.
- Which guarantees the primitive enforces atomically and which conflicts are
  observations only; no digest check may be called an atomic revision guard.

At the time this decision was recorded, review of the concrete creation
mechanism, prototype authorization, evidence, and explicit model-facing
enablement remained separate gates. Those creation gates were later completed
through PR #359. No separate identity, privileged helper or host-policy change
was selected or authorized. Editing remains unavailable under its independent
contract.

## Required evidence for the revised contract

Use the existing runtime/UI/provider seam to test exact diff approval, denial,
baseline and generation changes while approval is pending, conflicting internal
requests, refusal of unsupported conflict choices, cancellation and no replay.
Use disposable files and independent native actors to test creation collisions
at publication, aliases, parent moves, protected owner changes, byte integrity,
interrupted writes and cleanup. Add a deterministic change after the last check
to demonstrate the residual race honestly; it is not a successful isolation
test. Record real source, host, filesystem and per-case outcomes. Ordinary Go
or race tests do not prove native enforcement, and this docs-only proposal
supplies no new runtime or native acceptance.
