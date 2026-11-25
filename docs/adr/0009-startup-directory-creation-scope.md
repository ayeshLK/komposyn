---
status: accepted
---

# Use the CLI startup directory as the initial creation scope

The maintainer accepted the eight decisions below during review of the
proposal merged in PR #350. The first creation scope is the directory from
which the user launches `komposyn`, including existing subdirectories. This
supports creating files in an existing project without first inventorying
every project entry. Exact once approval, containment checks, applicable
protected-owner coverage and atomic refusal of existing destinations remain
required.

This accepted the design and ordered delivery plan; at decision time it did
not itself enable model-facing creation. Implementation and evidence were
subsequently delivered through reviewed PRs, followed by the separate
enablement review in PR #359.

## Post-merge implementation status — 2026-11-25

PR #359 merged the profile-gated creation composition at
`32ae327bd44e4581fbf65933f7f5b02afb9ad67c`. Creation is available to source
builds containing that merge only on Linux amd64/local ext4 with trusted
Bubblewrap using measured `ro-bind-fd`, and only while live owner, scope,
policy, service, and cleanup checks pass. This is source delivery, not a claim
that a new tagged binary release was published. The accepted startup scope,
owner coverage, exact once approval, and residual-race boundaries below remain
unchanged. Editing, rename, and delete are outside this decision.

## Accepted decisions

1. **Startup-directory scope.** Capture the CLI startup directory through the
   trusted host and retain its directory identity. The scope is not a fresh
   KompoSyn output folder and the model cannot choose it. Starting the CLI
   there selects the scope but grants no file-creation permission; every exact
   target and complete proposed contents still require explicit once approval.
2. **Existing subdirectories.** A target may be a direct child or lie beneath
   existing subdirectories within that scope. Parent directories must already
   exist. Refuse symlink traversal, mount crossing and model-facing directory
   creation. Bind preparation to the retained intended parent and absent leaf.
3. **Ordinary existing content is allowed.** Do not classify all entries or
   disable the whole scope because an unrelated project entry is unrecognized.
   Validate the requested target, existing parent chain and applicable
   protected resources. Creation atomically refuses any existing destination;
   it never converts into editing, overwrite, truncation or append. Missing
   required owner coverage refuses the affected operation.
4. **Fixed for the initial CLI run.** `/cd` changes relative-path lookup and
   navigation only. It cannot replace or widen the creation scope. No separate
   scope-change control is introduced initially; users restart `komposyn` in
   another directory to select another scope. Conversation reset does not
   recapture the root from a later working directory.
5. **Coverage follows the running composition.** Require real lifecycle
   registration for the sensitive components instantiated by this chat,
   currently auth and settings. If Token Logger or local provider-source
   ingestion is later composed, its actual owner lifecycle must be registered
   before eligibility. Only trusted composition wiring can declare a component
   uninstantiated. An absent path or empty registry is not coverage proof.
   Existing destinations and known protected locations remain refused. This
   contract does not promise an inventory of external applications' resources.
6. **Detected startup-directory movement stops creation.** If a lifecycle
   check detects that the startup directory itself moved or was replaced,
   disable creation for the rest of that run and request restart in the
   intended directory. Never adopt a replacement at the old path. This does
   not exclude an external move after the final check: the accepted residual
   race in [ADR-0008](0008-propose-runtime-file-objects-and-approved-saves.md)
   remains. Retain cleanup ownership and classify any already released effect
   truthfully rather than claiming rollback.
7. **Measured initial profile.** Support Linux amd64 on local ext4 with
   trusted Bubblewrap and the measured descriptor-based binding mode. Other
   platforms, filesystems and unverified launch modes remain unavailable.
   Startup-directory and nested-target behavior require new native acceptance;
   the historical twelve-case fixture run does not prove those claims.
8. **Gated implementation, separate enablement.** Record this design, implement
   through reviewed PRs with normal creation registration disabled, pass the
   composed approval/lifecycle tests, new native scope tests and required CI
   checks, then obtain explicit maintainer approval through a separate
   enablement PR. A design merge or passing test alone does not enable a tool.

## Applicability and rejected alternatives

This decision replaces ADR-0009's original fresh, per-conversation output
folder recommendation. The fixed XDG output base, output-folder provisioning,
output ledger, no-reuse policy and 256-entry/250 ms scope-wide census are not
requirements of the accepted startup-directory design. Created files remain
ordinary user files in the selected project; KompoSyn does not automatically
delete or relocate them.

It also revises the creation-only unknown-root eligibility requirement in
the [integration protocol](../harness/creation-integration-protocol.md). A
complete composition-owned manifest is still required for applicable owners,
but the absence of a machine-wide inventory or classification of unrelated
project entries is not itself a reason to reject creation. There is no
permission bypass for a known protected route, identity, owner-designated
protected ancestor, or incomplete applicable owner lifecycle. This change is
creation-specific; it does not relax existing read/discovery authorizers or
their protected-resource disclosure rules.

Arbitrary runtime scope switching was rejected for the initial version. A
future scope-change feature needs its own trusted control, invalidation and
cleanup design. Continued unavailability remains the runtime behavior until
the implementation, evidence and separate enablement gates pass.

ADR-0008's save-time approval and residual-race boundary is unchanged. A
detected conflict invalidates the proposal and needs fresh preparation and
approval. Descriptor identity binds the intended object; path checks must not
be represented as strict live-pathname containment through external races.
[ADR-0007](0007-proposed-exact-revision-bound-file-editing-contract.md) and
the non-creation requirements of issue #267 remain unchanged.

The startup directory is also the selected editing-scope direction recorded
in [proposed ADR-0010](0010-propose-approved-entry-replacement-for-file-editing.md).
That editing record separately changes the conflict/effect contract under
review; it does not change this creation decision, implement editing, or enable
an editing tool.

## Ownership and lifecycle requirements

| Component | Responsibility |
| --- | --- |
| Trusted CLI composition | Capture the startup scope before navigation changes, wire the actual required owner set, and expose the fixed scope and its availability. Never accept model-supplied scope or completeness assertions. |
| Host scope and owner services | Retain scope/parent identities; resolve eligible existing descendants without symlinks or mount crossing; check effective protected routes, identities, aliases and owner-designated protected ancestors; synchronize pending owner changes, generations, admission and cleanup. |
| Runtime | Bind scope identity/generation, exact target and UTF-8 bytes, complete diff, provider/model/auth-profile destination, conversation/call identity and once receipt through preparation, dispatch and publication. Suppress stale results and prevent replay. |
| UI | Show the fixed startup scope and availability, and present each exact target, complete diff and destination for explicit once approval. Navigation and selection grant no access. |
| Provider | Own native continuation formats; receive only authorized, classified results. Never select scope, authorize or execute filesystem effects. |
| Fixed filesystem worker | Stage approved bytes and publish with no-replace semantics under the retained parent, preserving supervision and truthful cleanup/completion outcomes. |

The applicable required owners are auth and settings in the current chat
composition, including their effective override/default routes, reserved
absent routes, pinned objects and replacement/rotation lifecycles. New owners
must be integrated before their composition can claim eligibility. No
transcript scan, machine-wide search, path-prefix allowlist or user statement
substitutes for required owner lifecycle registration.

Owner/policy/profile changes first close affected admission, invalidate
proposals and grants, cancel and drain affected work, and retain descriptors
and cleanup obligations until workers actually join and classification
finishes. A successfully refreshed owner may restore future eligibility;
old approvals are never revived. An unconfirmed cleanup keeps the affected
gate closed. Detection that the startup root moved or was replaced is terminal
for creation in that run, even if a replacement has the same pathname.

An existing descendant parent is resolved and revalidated against the fixed
scope and applicable protection rules for each proposal. A detected replaced,
out-of-scope or otherwise invalid parent cannot reuse old preparation. Its
operation must be refused or freshly prepared and approved if still eligible;
do not silently rebind an approved proposal to a new parent.

The existing service-wide create-owned read/list/search barrier and
owner-ordered stage/publication permits remain required. Preserve completed
immutable captures under their original authorization, one receipt consumer,
stale-publication suppression and no automatic side-effect replay. Supported
files remain single regular UTF-8 text files of at most 64 KiB under the
existing input limits, with empty content allowed. Existing destinations,
unsupported target types and multi-file requests remain refused.

## Evidence and acceptance

The trusted-host study at source
`dbfd90974f0979832a9abc60e32aeb3c87399840` recorded twelve scoped native passes
on Linux amd64/local ext4 with trusted Bubblewrap 0.9.0 and `ro-bind-fd`.
Archive SHA-256:
`3d76449b95ba037db17d46b3404d36f398d993cd0af3c940904857be305236dc`.
The [native record](../harness/issue-321-native-acceptance.md) preserves its
manifest/report hashes and historical failures. That run predates PR #349's
coordinator ordering fix; it proves neither that new coordinator behavior nor
this startup-scope design. The [lifecycle matrix](../harness/issue-321-lifecycle-acceptance.md)
records behavioral/race coverage separately.

| Claim | Required new evidence |
| --- | --- |
| Fixed startup scope | Real CLI/runtime/UI composition captures the startup directory once; `/cd` and conversation reset do not recapture/widen it; scope selection alone grants no write permission; exact reads retain their existing independent behavior. |
| Existing project contents | Ordinary pre-existing project entries do not trigger a whole-tree census or disable creation; target absence, applicable owner coverage and protected route/identity/ancestor refusals are checked independently. |
| Nested existing parents | Native independent-actor cases cover descendant resolution, existing parent retention/replacement/movement, symlink traversal, mount crossing, out-of-scope targets and missing parents. No model-facing directory creation. |
| Startup root movement | Detectable move/replacement before release disables further creation for the run; never adopt a replacement; characterize the accepted race after final observation without claiming strict pathname containment. |
| Composition owner coverage | Exercise auth/settings defaults and overrides, reserved routes, aliases, pending registration, rotation, stale revisions and cleanup failure; prove uninstantiated-owner status comes from trusted wiring and adding an owner makes its registration required. |
| Approval and lifecycle | Use the existing Service/runtime/UI/provider seams for exact bytes/diff/destination, 64 KiB and empty content, input limits, changed arguments, cancellation, drain/join, owner ordering, uncertain effects, stale result suppression and duplicate-call refusal. |
| Worker enforcement and supported profile | Run operation-specific native tests on a clean exact candidate with measured Linux amd64/ext4/Bubblewrap capabilities; confirm no-replace collisions, stage cleanup, worker joining, profile refusal and evidence provenance. Go/race tests are not native enforcement evidence. |

## Ordered delivery

1. Define the typed production owner profile and fixed startup-scope snapshot,
   reusing current auth/settings lifecycle hooks. Encode the accepted
   composition-owned coverage boundary and leave read/discovery policy intact.
2. Implement startup identity capture, existing nested-parent preparation,
   containment/protection checks, terminal detected-root-movement behavior
   and cleanup lifecycle. Keep normal creation registration disabled.
3. Integrate fixed-scope availability and exact once approval through the
   existing runtime/UI/provider contracts. Do not add a scope picker or
   silently tie creation eligibility to later navigation.
4. Complete behavioral/race and required repository checks, then collect new
   native evidence for the exact candidate and scope claims above. Preserve
   unavailable/failed results and supported-profile limits honestly.
5. Present a separate enablement PR for explicit maintainer approval after
   evidence review. This ordered plan was completed through PR #359; issues
   #267, #315 and #322 remain open against their independent criteria. Editing
   and unrelated rename/delete work are not enabled by this design.

Implementation interfaces, bounded path-processing details and test
checkpoints must be reviewed in those slices. This decision authorizes that
gated delivery sequence, not privileged installation, host policy changes,
unrestricted fallbacks or automatic enablement.
