---
status: accepted
---

# Use approved directory-entry replacement for bounded file editing

The accepted editing direction keeps a host-owned receipt from a prior
approved read in the same conversation, exact patch-context matching, a
complete diff and target in explicit once approval, and fresh read/proposal/
approval after any detected conflict. For the effect, it selects preparing the
complete new contents separately and replacing the approved directory entry.
This intentionally changes file-object identity and accepts an external race
after the last observation; it does not promise atomic revision-at-effect or
hostile same-user isolation. This ADR records the selected design for review,
not a concrete replacement primitive, supported profile, implementation or
model-facing capability. Editing remains unavailable.

## Status and scope

The maintainer accepted this editing boundary. This ADR supersedes the
applicable editing requirements in
[ADR-0007](0007-proposed-exact-revision-bound-file-editing-contract.md): it
replaces the requirement that the effect modify the exact mounted object and
enforce the exact read revision at the effect point. It does not change
historical #320 results or the independent creation, rename and delete
contracts in #267.

No replacement mechanism, filesystem profile or proof that this boundary can
be enforced has been accepted. PR #359 creation behavior and its Linux
amd64/local-ext4/Bubblewrap evidence do not satisfy editing requirements.
Issue #320 remains a scoped negative result for the mechanisms and contract it
examined; this ADR neither claims that all other mechanisms fail nor
retroactively changes those measurements. No staged replacement prototype,
privileged component or model-facing tool is authorized.

### Bounded in-place prototype investigation (2026-11-28)

The local prototype may implement an in-place rewrite through a retained
descriptor for the approved file. Immediately before the host releases the
worker to begin writing, it checks that the approved name still identifies
that file. If the name is missing, has been renamed away, or identifies a
different file, the attempt aborts before content mutation. Once that check
passes, the worker writes through the same retained descriptor. If another process moves or unlinks the name after the check, the
write still affects the retained inode. Report a detected path change
separately from the write outcome. A write or truncate interruption may
leave partial contents, and readers may observe intermediate bytes. The
prototype makes no atomic-write, rollback, or crash-durability promise and
must classify confirmed, no-effect, partial, and unknown outcomes without
replay.

This scoped investigation does not approve the mechanism, establish its
metadata or worker-authority guarantees, register a model-facing capability,
enable editing in normal composition, or constitute release approval. The
operation remains unavailable unless a separate review accepts its evidence
and explicitly decides enablement. Unsupported or unverified cases refuse.

## Decision

### Read authority, proposal and approval

An edit remains based on a prior approved read of the exact target in the same
conversation. The host, not the model, retains the read identity and revision
authority for the bytes disclosed. A model-supplied path, digest, inode or
revision is never authority. Preparation binds that read receipt to one exact
patch, exact target, complete proposed result, mutation-scope generation,
policy and protected-owner generations, conversation/call identity and
provider destination. The trusted UI shows the exact target and complete diff
and obtains explicit once approval at save time.

After approval, revalidate the target, content and metadata baseline and all
applicable generations. A detected change invalidates preparation and the
approval. The user must obtain a fresh approved read, make a new proposal and
approve a new diff. There is no force-overwrite, append, fuzzy matching or
automatic rebase, even if old patch context still matches.

The final observation is not a lock or atomic compare-and-replace. An external
change after that observation and before or during replacement is an accepted
residual race; it can be overwritten without detection. Do not claim strict
revision-at-effect enforcement or complete isolation from a hostile same-user
process. This explicitly revises ADR-0007's former strict effect-point
revision guarantee for editing only.

### Replacement semantics

The proposed effect writes the complete approved result to an attempt-owned
staging file in the target directory, then replaces the approved directory
entry using a supported atomic replacement operation. This is a design
direction, not approval of `rename`, `renameat2`, or any other primitive.

Replacement installs a different file object at the approved name. Existing
open handles and other hard links continue to refer to the old object. The
authority is therefore the prepared directory entry and reviewed base,
subject to the accepted residual race; the design does not claim to edit the
same inode that was read. Atomic namespace replacement alone does not prove
conditional revision checking, metadata preservation, durability, rollback,
or atomicity of surrounding validation and cleanup.

### Scope and protected owners

Use the fixed directory from which the user launched `komposyn` and eligible
existing descendant parents, following the accepted startup-scope decision in
[ADR-0009](0009-startup-directory-creation-scope.md). `/cd` is navigation
only and never widens mutation scope. Reset does not recapture startup scope.
Models may suggest a target but cannot select or widen scope; selection grants
no edit permission. Exact reads outside mutation scope retain their existing
approval behavior. Bind a prepared target to its scope at preparation; never
reinterpret it relative to later navigation.

Require the real protected-owner coverage and lifecycle checks already used
by composition. Auth/settings protections, aliases and rotation remain
enforced; provider sources remain read-only; protect owned Token Logger state
when instantiated. Trusted composition alone may declare a component
uninstantiated. Do not scan transcripts or perform machine-wide discovery.
Missing, stale, ambiguous or changing required coverage refuses the affected
operation. Scope changes invalidate affected preparation and permissions,
cancel and drain affected work, and block replacement admission while cleanup
is incomplete. If movement or replacement of the retained startup root is
detected, invalidate pending edits and disable editing for the rest of that
run even if the original root is restored. Restart establishes a new scope.
This detected-movement rule does not remove the accepted post-check race.

### Content, type and metadata eligibility

Keep the initial boundary to one existing UTF-8 regular file, with original
and resulting contents each at most 64 KiB. Require exact patch-context
matching. Refuse binary or unsupported encodings, symlink or non-regular
targets, hard-linked targets, multiple files, directory creation, rename,
delete, append and unrestricted fallback. Replacement is an internal edit
mechanism, not a general rename capability.

Preserve the file's ordinary permission bits and its existing line-ending
convention; modification time reflects the edit. Do not promise inode or
timestamp identity preservation. The initial eligibility policy is
conservative and testable: accept only metadata the selected mechanism can
enumerate and reproduce without privilege; refuse ownership/group changes,
special permission bits, ACLs, extended attributes, or unknown metadata unless
the mechanism proves lossless preservation and revalidation. A change in
ownership, permissions, ACLs, xattrs or link status invalidates preparation;
reassess eligibility and require a fresh proposal and approval, including a
fresh approved read where the conflict rule requires it. Never restore old
permissions or erase newly added metadata silently. The implementation must
prove that staged metadata is complete before publication; copying mode bits
alone is insufficient.

The chosen initial line-ending policy must be encoded in tests: preserve LF,
CRLF or no-terminator files exactly, and refuse mixed or unsupported newline
forms until a mechanism can preserve them losslessly. Do not normalize bytes.

### Staging, lifecycle and recovery

Temporary staging may contain the complete proposed new contents in the
target's directory and must use restrictive permissions during preparation.
This content is temporarily persisted on the filesystem, separately from
the in-memory conversation, approval and receipt records. On refusal or
cancellation, remove only an attempt-owned staging file whose identity is
verified. Never delete a substituted or merely suspected file. Cleanup failure
is reported truthfully and retains applicable lifecycle/admission restrictions.

A crash can leave a staging file containing proposed contents. No automatic
startup edit recovery, effect replay or deletion of suspected leftovers is
authorized. A mechanism proposal must establish ordering for final metadata
application, sync/close, replacement and cleanup without claiming durability,
rollback or all-or-nothing behavior unless separately demonstrated.

Reset invalidates prior-read edit receipts, pending diffs and approvals. A
later edit needs a new approved read. Retain cancellation, join/cleanup
ownership, blocked admission while cleanup is incomplete, stale-result
suppression and truthful outcomes. Cancellation after replacement cannot
undo the effect. Provider-continuation failure never triggers an effect
replay.

### Inspection and one bounded automatic retry

After an uncertain completion, inspect locally only while authorization
remains valid. Inspect only the approved target and the attempt-owned staging
file; recheck scope, policy and protected-owner state first. Inspection does
not authorize another provider disclosure. If changed content is needed for
a new proposal, obtain a fresh approved read.

Classify the result using both target state and trustworthy execution
evidence:

- Report success without retry only when evidence identifies the approved
  replacement object at the target and confirms its approved contents. This
  is not a durability guarantee.
- Retry automatically at most once only if inspection **and execution
  evidence** prove that no replacement occurred, the approved target/base and
  metadata remain valid after revalidation, authorization is still valid, and
  the attempt-owned stage is safe to reuse or recreate. Equal bytes alone do
  not establish this: an external actor can write equal contents, and ABA
  changes can return to the same observed state.
- A changed target/content or unresolved outcome is `unknown` or the most
  precise supported non-success result and requires a fresh approved read,
  proposal and approval. If inspection is unavailable/inconclusive, do not
  retry. Do not inspect after revocation to enable retry.

One once receipt is consumed once by the host coordinator. A bounded retry,
if the mechanism can prove it safe, must be an internal continuation of that
single authorized coordinator transaction after a positively established
no-effect result; it must not consume a second receipt or create a second
independent dispatch authorization. The concrete receipt/dispatch semantics
must prove this one-consumer property. If they cannot, automatic retry is
disabled. The mechanism must identify the evidence distinguishing
`not_executed` from `unknown`; a syscall return or matching bytes must not be
assumed sufficient without proof for the selected primitive.

## Alternatives and tradeoffs

| Option | Assessment |
| --- | --- |
| Keep exact mounted-object and strict revision-at-effect requirements from ADR-0007 | Preserves the original authority model, but the examined lease/in-place candidates did not establish atomic revision-bound effects or hard-link refusal. It remains a sound reason to keep editing unavailable if no alternative meets the new reviewed boundary. |
| Replace the approved directory entry from staged complete contents | Selected for mechanism investigation. It gives a bounded single-entry replacement shape and makes the identity change explicit, but accepts a race after final observation and does not itself preserve all metadata or prove no-effect retry. No primitive/profile has passed acceptance. |
| Keep editing unavailable without changing the contract | Safe current runtime behavior. It remains the outcome if no mechanism can meet the selected boundary and evidence gates; it does not answer whether all possible mechanisms are impossible. |

## Proof obligations before a mechanism can be accepted

The next mechanism proposal must answer and prove each item; this ADR does
not claim any is implemented:

- Retained startup root, target parent and directory-entry identity across
  preparation, approval and publication, including movement/replacement.
- Host-owned prior-read receipt and exact content revision, one-conversation
  lifetime, revocation/reset invalidation and bounded retention.
- Exact patch preparation, post-approval final validation, detected-conflict
  re-read/re-proposal flow and the explicit residual external race.
- Replacement semantics, atomicity limits and independent-actor tests for
  target/base changes before and after the final observation.
- Permission, ownership/group, special-bit, ACL, xattr, newline, link-count
  eligibility; metadata enumeration, preservation, change detection and
  publication ordering on the proposed profile.
- Restrictive same-directory staging, attempt identity, collision handling,
  ownership verification, refusal/cancellation cleanup and crash leftovers.
- Protected-owner registration/rotation, aliases, scope/policy generations,
  admission closure, cancellation, worker join and cleanup failure.
- Trusted UI display and exact once approval binding through runtime,
  dispatch, host worker and result publication; one once-receipt consumer.
- Completion inspection for only the target and attempt-owned stage; evidence
  that distinguishes confirmed replacement, proven no-effect and unknown.
- At most one internal retry only after positive no-effect proof and fresh
  revalidation; ABA and equal-byte external actor controls; otherwise no retry.
- Truthful `not_executed`, failed, partial and unknown outcomes; cancellation
  after replacement, provider continuation failure, stale publication, and no
  rollback/durability claims.
- Native cases on the proposed Linux amd64/local-ext4/current trusted
  Bubblewrap composition using independent actors. This is an investigation
  target, not an accepted profile. Other profiles refuse initially.

## Ordered follow-up and gates

The replacement research steps below apply to staged replacement. The
[bounded in-place prototype](#bounded-in-place-prototype-investigation-2026-11-28)
has its own effect and final validation. Both paths require separate
maintainer approval before model-facing enablement.

The [staged replacement mechanism proposal](../harness/editing-replacement-mechanism-proposal.md)
names a concrete candidate, phase protocol and proof gaps for review. It does
not accept a primitive, authorize a prototype or change editing availability.

The initial investigation target is Linux amd64/local ext4 with the current
trusted Bubblewrap host composition. This is a target for study, not an
accepted editing profile. Other profiles must refuse initially. Do not add or
install privileged components, load kernel hooks, create mounts, or change
host-security settings as part of this design or its first investigation.

1. Compare concrete unprivileged replacement primitives against the proof
   obligations, including metadata behavior and the exact evidence available
   after interruption. Do not prototype until a concrete mechanism and its
   assumptions are separately reviewed.
2. If a mechanism is accepted for a bounded prototype, add the host-owned
   receipt, preparation, once-approval and coordinator behavior at the
   existing Service/runtime/UI/provider seams. Preserve current read,
   discovery and creation gates; do not add a parallel test-only executor.
3. Complete focused behavioral tests for eligibility, exact diff/destination
   binding, stale arguments, conflicts, owner/scope transitions, reset,
   cancellation, inspection, cleanup, one-consumer retry and truthful
   outcomes.
4. Run native synthetic-file acceptance with independent actors for target
   replacement, content/ABA races, metadata, stage substitution, protected
   owner changes, cancellation, inspection certainty and cleanup. Record exact
   source/profile and unavailable cases. Creation evidence is not editing
   evidence.
5. Keep editing unavailable unless the exact candidate passes its independent
   behavioral and native gates and a separate maintainer review explicitly
   enables the model-facing operation.

## References

- [Issue #315](https://github.com/ayeshLK/komposyn/issues/315)
- [Editing gate #322](https://github.com/ayeshLK/komposyn/issues/322)
- [Mutation boundary #267](https://github.com/ayeshLK/komposyn/issues/267)
- [Scoped negative research #320](https://github.com/ayeshLK/komposyn/issues/320)
- [ADR-0007](0007-proposed-exact-revision-bound-file-editing-contract.md)
- [ADR-0008](0008-propose-runtime-file-objects-and-approved-saves.md)
- [ADR-0009](0009-startup-directory-creation-scope.md)
