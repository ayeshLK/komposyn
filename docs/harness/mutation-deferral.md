# Near-term mutation implementation deferral

**Current creation status — 2026-11-25:** PR #359 proposes creation through the
shipping CLI composition for Linux amd64, trusted BubbleWrap descriptor
binding, and a retained local ext4 startup scope. The accepted owner, approval,
and lifecycle checks remain authoritative. The exact candidate's native
follow-up measured all 18 cases as `scoped_native_pass`, including the
mount-boundary case. The [native record](issue-321-native-acceptance.md#shipping-composition-candidate-mount-boundary-follow-up-2026-11-25)
contains its provenance and limits. Maintainer review remains before merge;
released builds remain unchanged. Editing continues under its independent
deferred contract.

**Historical evidence update — 2026-11-21:** the disposable creation prototype had
selected runtime lifecycle tests and a trusted-host native run with 12/12
scoped cases on its recorded Linux amd64/local-ext4/Bubblewrap profile. These
results closed those prototype evidence steps only. At that point, production
owner coverage, broader platform/filesystem support and explicit model-facing
enablement remained open; neither creation nor editing was available in normal
composition. See
the [issue #321 lifecycle acceptance record](issue-321-lifecycle-acceptance.md)
for provenance and limits.

**Subsequent creation decision:** PR #343 merged on 2026-11-19 and ADR-0008 is
accepted for the creation concurrency/approval boundary only. The
[runtime-file-object contract](runtime-file-object-saves.md) accepts a residual
external-write race. PR #344 merged the [mechanism proposal](creation-mechanism-proposal.md)
as a proposal, and the [integration protocol](creation-integration-protocol.md)
records the bounded fixture prototype and its partial implementation. At that
stage, neither design nor fixture evidence enabled mutation and production
scope coverage remained unresolved. This record preserves the prior deferral
and historical research.

Status: **planning record for the near-term deferral; #315's current-delivery
section tracks its publication and review state.** Merged master still omits
creation. The unmerged PR #359 proposes profile-gated registration; its exact
candidate native suite is complete for the recorded profile. Maintainer review
remains before merge.

Editing implementation remains deferred under its existing authority
contract. Creation has an accepted concurrency/approval boundary, a bounded
stage-plus-no-replace fixture prototype, and scoped positive native evidence;
its mechanism and evidence are in [creation mechanism design](creation-mechanism-proposal.md).
Editing has no positive native mechanism evidence. On merged master, neither
operation has separate model-facing enablement; this branch proposes creation
registration and remains unmerged. Discovery remains delivered within its documented support profiles:
nonrecursive listing and recursive/filter listing on their recorded Linux
profiles, and literal search on its recorded Linux amd64 ext4 profile.

The creation and editing user stories in umbrella issue #315 remain
undelivered. Issue #315 is partially delivered and remains open; this planning
record does not convert the original milestone into a discovery-only scope.
The accepted research records are [creation #319](issue-319-closeout.md) and
[editing #320](issue-320-closeout.md). They reject or leave unresolved only the
candidates and categories stated in those records. This scheduling choice is
not a proof that all unprivileged mutation is impossible or that privileged
enforcement is the only possible future solution.

Issues #267, #321 and #322 remain open as unfinished contract and enablement
gates. Their earlier native research dependencies are resolved; that does not
establish the current candidate's native activation path or meet the separate
maintainer-review gate. On merged master, creation and editing remain
unavailable in normal composition. The unmerged change proposes creation only
on its documented profile; editing stays deferred. This record does not
authorize privileged setup, relaxed permission or host-policy change.

## Conditions for resuming mutation work

For creation, the runtime-file-object boundary is accepted and the
stage-plus-no-replace candidate has been exercised in a disposable fixture and
the recorded native profile.
Editing has no
accepted mechanism or contract change and remains gated by ADR-0007. A general
request to make patching safe is not a candidate. For either operation, cover
these matters for the operation under consideration:

1. **Authority and effect.** For creation, bind the exact retained parent and
   absent leaf. For editing, bind the exact mounted object and content revision
   from a prior approved read. Identify the effect primitive and the bounded,
   operation-specific worker that applies it.
2. **Independent actors and protected objects.** Test same-user actors,
   existing writable handles and mappings where relevant, late aliases,
   namespace changes, and protected-owner discovery, rotation and lifecycle.
   A check before or after the effect is not by itself effect-point proof.
3. **Trusted approval chain.** Account for startup ordering, inherited handles,
   the approval presentation and release channel, and every holder of
   authority. #337's retained inherited-pipe observation remains relevant; it
   was not a full UI spoof test or acceptance of an eligible Yama mode-2/3
   profile. Do not generalize beyond its measured primitive and stated limits.
4. **Lifecycle and outcomes.** Bind revocation and cancellation to capability
   release; retain cleanup ownership until workers and descriptors are joined
   or retired; suppress stale publication; and report partial or unknown
   outcomes truthfully. Do not promise rollback or automatically replay an
   uncertain side effect.
5. **Host assumptions and rejection evidence.** Name filesystem, kernel,
   platform, launch and deployment assumptions. Use deterministic same-user
   actor checkpoints and rejection controls in the existing native harness,
   with the existing runtime/UI/provider seam for approval, dispatch and
   publication behavior as appropriate. Mark unavailable and inconclusive
   cases explicitly.

## Separate review gates

For each operation, keep these decisions separate and ordered:

1. Review and accept the concrete threat model, authority contract, candidate
   mechanism and host/deployment assumptions.
2. Explicitly authorize a bounded prototype. Design acceptance alone is not
   prototype authorization.
3. Obtain positive, operation-specific native acceptance and relevant
   runtime/UI/provider evidence. Creation evidence does not accept editing;
   editing evidence does not accept creation.
4. Obtain separate explicit maintainer approval before model-facing
   enablement, then document only the accepted profiles and limitations.

Any proposal that changes the authority contract, trusted boundary, deployment
privileges or host settings needs a separate explicit decision before
prototyping. This deferral record selects no mechanism and sets no deadline;
the separate creation candidate is in [creation mechanism design](creation-mechanism-proposal.md).
It does not make rename, delete or other unrelated #267 operations
prerequisites for reconsidering creation or editing.

## Current records

- [Umbrella milestone #315](https://github.com/ayeshLK/komposyn/issues/315) is
  partially delivered and open; its original specification remains in force.
- [Mutation authority contract #267](https://github.com/ayeshLK/komposyn/issues/267)
  remains open.
- [Creation gate #321](https://github.com/ayeshLK/komposyn/issues/321) and
  [editing gate #322](https://github.com/ayeshLK/komposyn/issues/322) remain
  open with positive-evidence and explicit enablement requirements unsatisfied.
- Discovery delivery is recorded in [#317 acceptance](issue-317-completion-tracking.md)
  and [#318 acceptance](issue-318-search-completion.md).
- The detailed creation and editing evidence remains in the linked #319/#320
  closeouts; this document does not replace or reinterpret their historical
  observations, source revisions, hashes or unavailable-case classifications.
