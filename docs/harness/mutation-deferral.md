# Near-term mutation implementation deferral

**Subsequent creation decision:** PR #343 merged on 2026-11-19 and ADR-0008 is
accepted for the creation concurrency/approval boundary only. The
[runtime-file-object contract](runtime-file-object-saves.md) accepts a residual
external-write race. PR #344 merged the [mechanism proposal](creation-mechanism-proposal.md)
as a proposal, and the [integration protocol](creation-integration-protocol.md)
records the separately authorized bounded fixture prototype and its partial
implementation. Neither design nor fixture evidence enables mutation;
production scope coverage remains unresolved. This record preserves the prior
deferral and historical research.

Status: **planning record for the near-term deferral; #315's current-delivery
section tracks its publication and review state**.

Editing implementation remains deferred under its existing authority
contract. Creation has an accepted concurrency/approval boundary and a bounded
stage-plus-no-replace prototype in progress; its candidate and evidence are in
[creation mechanism design](creation-mechanism-proposal.md). Neither operation
has accepted native positive evidence or separate enablement. Discovery remains delivered
within its documented support profiles:
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
gates. Their native research dependencies are resolved; that does not meet the
independent positive-evidence and explicit model-facing enablement conditions.
Creation and editing remain unavailable in normal composition. The disposable
creation fixture prototype is separately authorized; this deferral does not
authorize production enablement, privileged setup, relaxed permission or
host-policy change.

## Conditions for resuming mutation work

For creation, the runtime-file-object boundary is accepted and the
stage-plus-no-replace candidate is being exercised in a disposable fixture.
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
