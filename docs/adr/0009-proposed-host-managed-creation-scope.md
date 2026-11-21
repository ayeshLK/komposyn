---
status: proposed
---

# Propose a host-managed output scope for one-file creation

This proposal is for maintainer review. It does not accept the design, change
the implementation contract, enable creation, or authorize feature work. The
normal `cmd/harness` composition keeps `create_file` unavailable until the
coverage, evidence, and separate enablement gates below are satisfied.

Here, “production eligible” means eligible for a future normal
model-facing `cmd/harness` composition. This repository's `komposyn` chat is
experimental; the standalone `komposyn-lg` executable and its settings,
consent, onboarding, and transport remain owned by its separate repository.

## Context and source findings

The accepted creation boundary in [ADR-0008](0008-propose-runtime-file-objects-and-approved-saves.md)
coordinates KompoSyn's own runtime file objects, requests exact once approval
at save time, and revalidates after approval. A detected conflict needs a new
proposal and fresh approval. External changes during approval and between the
last check and effect remain an accepted residual race. Creation refuses an
existing leaf at the publication primitive. This is not atomic compare-and-
write, a filesystem lock, or strict live-pathname containment. Editing retains
its separate [ADR-0007 contract](0007-proposed-exact-revision-bound-file-editing-contract.md).

Relevant current implementation facts:

| Area | Current source fact | Consequence for creation scope |
| --- | --- | --- |
| User directory and read scope | `runtime.Chat.SelectFilesystemRoot` stores a canonical lookup path and a generation; the path carries no descriptor or authority. `/cd` changes relative-path context. See [`filesystem_root.go`](../../internal/harness/runtime/filesystem_root.go) and [the glossary](../../GLOSSARY.md). | Working-directory navigation cannot select or widen creation scope. A creation scope needs a distinct user control and host-owned object identity. |
| Auth and settings | `cmd/harness` registers each store's effective path with `hostSandbox` before loading it. `--auth-file` and `--settings-file` can independently override defaults. `RegisterSensitiveJSONStore` registers an absent route or pins an existing regular-file identity; protected atomic replacement registers the empty replacement before writing sensitive bytes. See [`main.go`](../../cmd/harness/main.go), [`protection.go`](../../internal/harness/auth/protection.go), and [`store.go`](../../internal/harness/auth/store.go). | These are the only real owner lifecycles currently connected to this chat composition. Their rotations and incomplete updates must keep the scope unavailable. Environment-provided credentials are not a filesystem inventory. |
| Host coverage | `readbrokerstudy.NewChatCompositionInventory` requires auth and settings. `CoverageInventory` distinguishes trusted declarations from registered routes, pinned identities, pending updates, retirement, and revisions. An empty or closed inventory is incomplete. See [`inventory_linux.go`](../../internal/harness/readbrokerstudy/inventory_linux.go). | The current composition profile is narrower than the four-owner study fixture, but still does not attest that arbitrary selected-root data is absent. |
| Study fixture | `NewSyntheticStudyInventory` declares four synthetic owners: auth, settings, Token Logger database, and provider source. Those routes are fixture declarations, not production lifecycle evidence. | Fixture completeness cannot be reused as a production assertion. |
| Protected-object rules | The host registry pins registered identities and applies route/identity/ancestor rules. A pinned protected directory does not enumerate its descendants or retain their identities after movement. Directory coverage can conservatively refuse unpinned objects. See [`registry_linux.go`](../../internal/harness/readbrokerstudy/registry_linux.go). | A path prefix or one directory registration cannot stand in for a complete descendant inventory. Owner-declared protected ancestors and file aliases need explicit coverage. |
| Token Logger | `cmd/harness` does not open a Token Logger store. The core has no host read-broker lifecycle hook for SQLite database, WAL/SHM, journal, backup, migration, and recovery objects. See [the owner inventory audit](../harness/read-broker-owner-inventory.md). | The proposed chat profile can declare Token Logger out of this composition only because it does not instantiate it. If it is composed later, creation is unavailable until its complete lifecycle is registered. No machine-wide database search is proposed. |
| Provider sources | The temporary chat's bundled providers make remote requests and do not discover local Token Logger transcript sources. The collector discovers external provider files separately; configured roots alone do not register every opened/replaced source identity. See [the owner inventory audit](../harness/read-broker-owner-inventory.md). | The proposed profile does not instantiate provider-source ingestion. It must not scan transcripts or treat a selected folder as proof that no external provider source exists. Any unrecognized entry inside the host-managed output scope is unknown and blocks use. |
| Creation prototype | The Linux service uses a synthetic complete-owner manifest and refuses outside that test profile. The normal `cmd/harness` composition does not register the creation runtime. See [`service_linux.go`](../../internal/harness/sandbox/service_linux.go) and [capability availability](../harness/capability-availability.md). | Replacing the synthetic manifest with a production scope/owner lifecycle is a prerequisite, not an implementation detail already solved by the prototype. |

The read broker's `CoverageSnapshot.Complete` means all trusted declared owner
lifecycles are present and current. It does not prove that a caller's arbitrary
directory contains no unknown resources. The current service therefore keeps
an independent unknown-root-data gate closed. Directory selection, private
permissions, a new directory name, and a path prefix do not prove owner
completeness.

## Alternatives

### 1. Arbitrary user-selected directories with trusted owner registration

Under this option, the user selects any existing directory as a creation
scope. Trusted owners must register every protected route, object identity,
protected directory, alias, and lifecycle that can overlap it.

- **Eligibility authority:** a fixed host composition and each trusted owner
  lifecycle, never the model, provider, UI argument, or scope path itself.
- **What it knows:** registered auth/settings objects and any future owners
  with actual hooks. A registered identity can identify known aliases.
- **What it cannot know today:** all Token Logger database/sidecar/backup
  lifecycles, all provider-source discoveries and opens, independent CLI
  instances, external credential managers, or arbitrary files elsewhere on
  the machine. Scanning transcripts or searching the machine would not repair
  missing lifecycle contracts.
- **Unknowns and changes:** undeclared owners, incomplete inventory, stale
  identity, ambiguous alias, owner rotation, or failed cleanup must close
  admission before approval/worker launch and keep it closed until successful
  re-registration and drain. Missing owner declaration never means “no such
  owner exists.”
- **Assessment:** does not satisfy current requirements. It needs concrete
  Token Logger and provider-source owner APIs and evidence, plus a defensible
  rule for unregistered external data. User approval cannot supply those
  facts. Do not use this as the first scope.

### 2. Fresh host-provisioned output directory per conversation

Under this option, the user explicitly selects a host-managed output scope
from the UI. The host creates a fresh per-conversation directory under a
dedicated KompoSyn output base; it never adopts a user-selected existing
directory and never reuses a prior conversation's directory. The first slice
allows creation of one regular-file leaf directly inside that directory. The
host, not the model, provisions the directory. No model-facing directory
creation operation is introduced.

Proposed pilot constraints:

- Linux amd64 only, on the measured local ext4 and trusted Bubblewrap
  `ro-bind-fd` profile. The earlier native run tested Linux 7.0.0-42-generic,
  Go 1.26.11, Bubblewrap 0.9.0 and ext4 mount ID 33; that is evidence
  provenance, not an arbitrary version-string rule. The runtime must inspect
  actual backend capabilities and filesystem facts. Other launch modes,
  filesystems, architectures and platforms remain unavailable until measured.
- A fixed per-user data base, proposed as
  `$XDG_DATA_HOME/komposyn/creation-outputs` (default
  `~/.local/share/komposyn/creation-outputs`). The exact location and behavior
  when `XDG_DATA_HOME` is unset, invalid, symlinked, or on an unsupported mount
  are maintainer decisions. The base is provisioned/validated by the host; a
  model cannot supply it. A fresh child directory is created exclusively for
  each conversation and pinned by descriptor and filesystem identity.
- The user-facing control is an explicit “Use a fresh KompoSyn output folder”
  selection. UI shows its exact path and current availability. This is a
  selection of a bounded host-provisioned scope, not a free-form path picker.
  The user can clear it; the model can suggest a target basename but cannot
  choose, replace, or widen the scope.
- A pre-existing child directory is never adopted. Existing, replaced,
  symlinked, wrong-owner, wrong-mode, wrong-filesystem, or identity-mismatched
  base/scope paths fail closed. A newly provisioned child starts empty by
  exclusive creation; privacy mode and freshness are prerequisites, not proof
  that later contents are safe.
- At admission and again after approval, a bounded, nonrecursive metadata
  census reads only direct directory entries via the retained scope descriptor
  and no-follow metadata operations. It does not open content or search any
  other path. Entries whose exact identities are in this conversation's
  in-memory creation ledger are known outputs. A registered protected identity
  makes the scope ineligible as protected. Any other entry, symlink,
  subdirectory, identity mismatch, enumeration error, or census bound
  exhaustion is unclassified: close the scope and refuse before
  approval/dispatch. Initial
  proposed limits are 256 direct entries total (including KompoSyn outputs)
  and 250 ms per census; exceeding either limit makes the scope unavailable.
  No unbounded enumeration is permitted.
- The output ledger is memory-only and bound to the scope generation. A
  successful, joined, identity-verified creation adds one exact output
  identity. An unknown outcome, failed join, or inability to verify identity
  retires the scope for the rest of that conversation; it is not reconciled by
  guessing. On process exit, the folder is left for the user and is never
  silently deleted or adopted by a later conversation. The next conversation
  receives a new exclusive directory.
- Before provisioning, the host compares the output base/parent with currently
  registered protected routes and owner-declared protected directory roots.
  After provisioning and at each admission it compares scope identity and
  relevant ancestry with those same owner records. A scope overlapping a known
  protected identity, protected ancestor, reserved owner route, or pending
  owner update is ineligible. Owners must expose generation changes and keep
  replaced identities pinned until old work joins.
- A known move or replacement observed at a lifecycle check invalidates the
  scope. The host never adopts the replacement. A move between the last check
  and the effect remains within ADR-0008's accepted external-race boundary;
  authority is bound to the retained directory object and the design makes no
  strict live-pathname-containment claim. The approval path must show the last
  verified target path and scope identity; if movement is detected before the
  effect, require fresh selection and approval.
- Outputs persist after the conversation, but the host does not reopen old
  directories. There is no output cleanup that could delete user data and no
  persistent conversation or grant state. The user manages retained outputs
  with ordinary external tools.

The scope-local census is intentionally narrow. It handles an already present
unrecognized entry without transcripts, machine-wide search, or trust inferred
from a folder name. It cannot stop an independent same-user actor from adding
an entry immediately after the final check; that is the residual race already
accepted in ADR-0008, not a claim of complete hostile-same-user isolation.

- **Eligibility authority:** trusted host composition provisions and pins the
  scope; the owner registry establishes current known owner coverage; the
  scope manager performs the bounded local census. A complete result is a
  conjunction of these independent facts.
- **What it knows:** actual auth/settings registrations and rotations; the
  creation output scope and its exact in-memory outputs; whether Token Logger
  and collector/provider-source subsystems are instantiated in this exact
  composition; and whether any direct scope entry is not accounted for.
- **What it cannot know:** whether unrelated data exists elsewhere on the
  machine or whether another actor changes the directory after the check. It
  does not read transcript contents. If Token Logger or source ingestion is
  later composed, a typed `not in this composition` status is no longer valid;
  the subsystem's real owner lifecycle must be integrated before eligibility.
- **Assessment:** the narrowest testable candidate, but not yet accepted. It
  requires explicit maintainer decisions on this output-scope form, location,
  census limits, owner profile, persistence/no-reuse behavior, and approval
  presentation. Until those decisions and new evidence exist, the production
  gate remains closed.

### 3. Keep creation unavailable

The trusted composition can continue to refuse all creation while preserving
the already available read and discovery operations. This is the required
default whenever the proposed host-managed scope fails any eligibility or
coverage check, or the maintainer does not accept option 2.

- **Eligibility authority:** none; creation is not registered or advertised.
- **What it knows:** no creation authority is granted, so no owner inventory
  assertion is needed to authorize a mutation.
- **What it cannot know:** nothing about arbitrary-directory owner coverage is
  inferred.
- **Assessment:** satisfies the current security boundary and can remain the
  shipped behavior indefinitely. It does not deliver #321's user-facing
  capability. It is the fallback, not a reason to weaken missing-coverage
  behavior.

## Proposed first scope and owner-coverage contract

Recommend option 2 for review as a **conditional design**, with option 3 as
the runtime behavior until every gate is met. Do not authorize arbitrary
directories in the first production slice.

### Trusted scope selection and lifecycle

1. On explicit user selection, the host provisions a fresh output directory
   from the fixed per-user output base. It opens and retains the parent and
   scope descriptors, verifies directory type, owner/mode prerequisites,
   mount identity, supported filesystem and worker launch profile, and
   exclusive creation. These checks establish the intended object and
   measured profile; they do not prove absence of all protected owners.
2. A trusted composition manifest enumerates the owner lifecycles that the
   running composition actually uses. Each applicable owner must declare its
   lifecycle before it can be considered complete. Only composition wiring
   may declare a subsystem out of composition, based on what it instantiates;
   an absent path, empty registry, failed probe, or user/model statement never
   proves absence.
3. The host compares the output scope with registered protected routes,
   exact file identities and owner-declared protected ancestor identities,
   then performs the bounded direct-entry census. The result is a scope-bound
   coverage snapshot with a monotonically increasing scope generation,
   owner/registry revision, and state. It is valid only for that retained
   directory identity and conversation.
4. User selection exposes no permission. A model-proposed basename must be a
   single valid leaf. The runtime binds the leaf, exact UTF-8 bytes and digest,
   complete create diff, scope identity/generation, coverage/policy revision,
   provider/model/auth-profile destination, conversation and provider call ID
   in the retained proposal and once-approval request.
5. After explicit once approval, the host revalidates the scope and owner
   revisions, repeats the local census, rechecks the exact leaf remains absent,
   and runs the existing bounded stage-plus-`RENAME_NOREPLACE` path. A
   collision remains a conflict; it never becomes overwrite, append, or
   editing. One receipt has one consumer. Unknown effect or cleanup outcome
   suppresses success and provider continuation, retains cleanup ownership,
   retires the scope if its state cannot be reconciled, and never replays.
6. Clearing/changing scope, owner rotation, root/policy changes, profile
   changes, conversation reset, and shutdown first close admission, invalidate
   affected proposals/grants, cancel and drain affected work, and retain
   descriptors/cleanup obligations until join is confirmed. New scope
   admission waits for successful retirement. Incomplete cleanup keeps the
   affected gate closed.
7. The scope manager never removes persisted output files on close. It closes
   retained descriptors only after all associated work has joined and result
   classification has finished. If cleanup cannot be confirmed, it retains
   ownership and refuses replacement admission until resolved or process exit.

Working-directory navigation does not alter this scope. Selecting or changing
the creation scope does not change the working directory, grant read access,
or authorize writes. Exact-file reads outside the creation scope keep their
existing independent approval and host-eligibility behavior. The model may
suggest a basename but cannot select a directory, set the output base, or
expand the leaf to a path.

### Component responsibilities and proposed interfaces

Names below are design vocabulary, not committed API signatures.

| Component | Responsibility | Must not do |
| --- | --- | --- |
| `cmd/harness` trusted composition | Construct a fixed owner-requirement set from components actually instantiated; wire auth/settings owner managers and the output-scope manager; keep Token Logger/collector absent from this profile unless their actual lifecycle is composed. | Accept owner requirements, completeness flags, or scope roots from tool arguments. |
| Host owner registry | Track owner ID/class, effective routes, absent-route reservations where applicable, object and protected-directory identities, alias identity, owner generation, pending/retired state, and held cleanup obligations. Retain replaced pins until affected work joins. | Infer coverage from path prefixes, default paths, directories being empty, or a static list without lifecycle callbacks. |
| Scope manager (host) | Provision a unique directory, retain descriptor/object identity, verify the actual supported profile, run bounded local census, maintain the current conversation's output ledger, and transition eligibility fail-closed. | Reuse or adopt old output directories, recursively inspect unrelated data, read transcript contents, or delete outputs. |
| Runtime | Own proposal identity, generations, argument/bytes/destination binding, once-consumer checks, stale-result suppression and publication. Request explicit approval only after scope and coverage pass. | Treat selected scope as permission or accept a model-supplied scope. |
| UI | Provide explicit select/clear control for the host-created scope; show exact path/status; show exact target, bytes/diff and provider destination for each once approval. | Let `/cd`, browsing, model suggestion or approval create/widen scope. |
| Provider | Carry its native call ID/continuation and receive only the final classified result. | Determine scope, coverage, authorization, or perform filesystem effects. |
| Filesystem worker | Perform only the existing bounded one-file stage and no-replace publication under the retained parent, with current supervision/cleanup protocol. | Discover owners, grant authority, enumerate machine state, or weaken effect semantics. |
| Token Logger / collector owners (future only) | If ever composed, expose effective path and opened-object lifecycle before sensitive writes, sidecars, backup/recovery creation, and source opens/rotations, coordinated with scope admission. | Claim complete coverage from only a DB filename, configured source root, or transcript scan. |

Proposed scope states: `unprovisioned`, `provisioning`, `eligible`,
`invalidation_pending`, `incomplete`, `retired`. Each state includes a safe
reason code, scope generation, directory identity (not a public authority
token), coverage revision and profile ID. `eligible` requires all of: active
conversation; current retained scope identity; actual supported backend/profile;
complete composition owner declarations and registrations; no protected
identity/ancestor overlap; complete bounded local census; no pending cleanup;
and unchanged scope/owner/policy generations. Missing or ambiguous data never
collapses into `eligible`.

### Owner classes and unknown-data handling

For the first host composition, the required owner manifest is:

| Owner | First-slice status | Evidence/contract needed |
| --- | --- | --- |
| Auth store | Required and applicable | Effective `--auth-file`/default route; absent reservation or opened regular-file identity; protected replacement identity before write; old identities retained until join; environment credentials remain outside filesystem owner coverage. |
| Settings store | Required and applicable | Independent effective `--settings-file`/default route; same reservation/replacement/rotation semantics as auth. |
| Creation output | Required and applicable | Fresh directory identity, parent/ancestor facts, output ledger, bounded direct-entry census, generation and conversation lifecycle. |
| Token Logger DB and sidecars | Not instantiated by this composition; no claim about machine-wide absence | If a future profile opens Token Logger, this row becomes required. It needs DB, WAL, SHM, journal, backup, temporary and recovery object lifecycles plus close/rotation coordination before writes. Existing core API does not provide this. |
| Provider transcript sources | Not instantiated by this composition; no claim about external source absence | If a future profile reads/collects them, it needs opened identity/source-generation registration and replacement/rotation lifecycle. Configured roots alone are incomplete. No transcript scan. |

The only “not instantiated” statuses are emitted by trusted composition code
from actual construction/wiring. They do not mean the path is missing or the
resource does not exist elsewhere. Within the host-created output directory,
any direct entry not in the in-memory output ledger or explicitly classified
through the current registered owner snapshot is `unknown`. Unknown, stale,
ambiguous, replaced, partially registered, or changing coverage fails before
approval or worker launch. An owner change first marks coverage pending and
advances its revision, blocks admission, invalidates proposals and grants,
cancels/drains old operations, then reopens only after registration and
cleanup complete.

An owner registration includes a stable trusted owner key/class, effective
route, current object identity or explicit reserved-absent route, protected
directory identities that owner declares semantically sensitive, alias
identity, and a lifecycle generation. Secret values and file contents are
never required. Exact identities cover registered hard-link aliases; an
unregistered entry in the scope remains unknown even when its bytes are never
opened. A directory owner must explicitly state whether its directory is a
protected ancestor and provide lifecycle updates; registering a path is not a
descendant census. The scope manager must not infer protected-directory
semantics from every filesystem ancestor (which would make normal locations
unusable), nor ignore an owner-designated protected ancestor.

## Approval and execution invariants

The first implementation reuses the approved create path and preserves the
reviewed boundaries from ADR-0008 and the [creation integration protocol](../harness/creation-integration-protocol.md):

- Bind exact scope identity/generation, target leaf, complete diff/bytes,
  destination, conversation, provider call ID, owner/coverage/policy revisions
  and receipt through preparation, approval, dispatch, effect, and publication.
- Request one explicit UI approval after host coverage succeeds. Approval is
  for this exact target and provider/model/auth-profile destination once.
- A scope change or owner/policy transition invalidates the proposal and
  permission and drains affected work before new scope admission.
- Revalidate after the user responds. Conflicts require a fresh proposal and
  approval; no overwrite, truncate, append, edit, or automatic retry path.
- Keep one receipt consumer. Preserve target absence at the no-replace
  publication syscall, the accepted external race after the last observation,
  retained cleanup ownership, truthful unknown results, provider continuation
  withholding, and no automatic replay.
- Directory selection and working-directory navigation grant no read/write
  authority. Existing exact reads outside the creation scope retain their
  current independent approval path.

## Evidence and acceptance matrix

| Claim | Existing evidence | Required before any production eligibility claim |
| --- | --- | --- |
| Creation protocol and accepted boundary | Merged PRs #343–#349; fixture Service/runtime/provider coverage; ADR-0008; existing stage-plus-`RENAME_NOREPLACE` worker. | Reuse without weakening. Add scope-bound owner snapshot and user-control tests at the existing Service/runtime/UI/provider seams. |
| Scoped native creation worker | Trusted-host study on source `dbfd90974f0979832a9abc60e32aeb3c87399840`: 12/12 `scoped_native_pass`; Linux amd64, local ext4 mount ID 33, trusted Bubblewrap 0.9.0 `ro-bind-fd`. Archive SHA-256 `3d76449b95ba037db17d46b3404d36f398d993cd0af3c940904857be305236dc`; manifest `1822336d698e55d533cc71a3a54738885e99fdaff64b81b0cb51e9c3173d9a02`; report `ac66529ef8a9a8e562505c5964d080d01aeb040122be804fabe86959b82f7ce6`. | Does not prove PR #349's coordinator guard or this proposed output scope. Keep as historical worker evidence only. |
| PR #349 coordinator completion ordering | Behavioral lifecycle and Go race tests on merged PR #349 cover the joined-worker completion-ordering guard and new lifecycle windows. | No native enforcement claim follows; native scope acceptance needs the exact candidate and profile below. |
| Trusted host scope provisioning | No production scope manager exists. | Native acceptance for exclusive directory provisioning, wrong owner/mode/type/symlink/replacement refusal, retained identity, mount/profile checks, cleanup and no adoption/reuse. |
| Scope-local unknown-entry census | No production census exists. | Independent actor tests for unknown file, directory, symlink, registered protected alias, late entry, replacement, bound exhaustion and enumeration failure. Demonstrate metadata-only inspection and fail-closed behavior; characterize the accepted after-check race without claiming to prevent it. |
| Real owner lifecycle | Auth/settings registration is in the chat composition; no Token Logger or provider-source owner hook exists. Synthetic four-owner fixtures are not production coverage. | Composed auth/settings default and override registrations, replacement/rotation/pending failure, ancestor/alias overlap, and cleanup joins. Prove non-instantiated Token Logger/provider-source status comes from composition wiring. If those systems are added, their real lifecycle/native acceptance is an entry gate. |
| User selection and approval | Lookup-root selection and exact read approval are implemented; fixture creation tests cover exact create approval. | UI tests that `/cd` does not change scope; model cannot choose/widen; explicit host-scope select/clear drains and invalidates; approval presents exact target/bytes/diff/destination; modified arguments cannot consume the old receipt. |
| Effect/result | Worker native tests cover exact bytes, collision, cleanup, join; composed fixture tests cover owner transitions, stale publication, duplicate call and no replay. | Run the new scope-bound full composition; verify known success ledger registration, unknown outcome retires scope, no stale provider continuation, no replay, and new proposal requires new approval. |

The accepted historical native archive predates PR #349's coordinator fix.
It supports only its recorded worker image/profile/host. Neither the native
archive, fixture tests, owner declarations, Go tests, nor the race detector
proves the proposed production coverage contract by itself.

## Ordered implementation slices after design approval

1. **Resolve scope product decisions.** Decide whether a fresh per-conversation
   host-created output folder satisfies #321's trusted user-selected scope;
   approve/adjust its location, persistence, unknown-entry behavior and
   bounded census limits. If not accepted, keep creation unavailable.
2. **Define the typed production owner profile.** Replace the synthetic
   creation manifest path with a trusted composition-owned manifest and
   scope-bound coverage snapshot. Integrate current auth/settings route and
   rotation lifecycles; make future Token Logger/collector composition require
   actual owner interfaces. Do not add generic inventory or plugins.
3. **Implement host scope provisioning and lifecycle.** Create exclusive
   unique per-conversation output dirs, pin and validate supported profile,
   implement bounded local census/output ledger, fail closed on unknowns, and
   preserve barrier, cleanup and no-delete behavior. Keep normal tool
   registration disabled.
4. **Integrate explicit UI selection and runtime binding.** Add select/clear
   controls and scope availability projection; bind scope/owner generations
   through existing create preparation and one-time approval. Test at real
   Service/runtime/UI/provider seams.
5. **Measure the new filesystem claims.** Run native independent-actor
   acceptance on a clean exact candidate with trusted Bubblewrap, local ext4,
   Linux amd64 and measured `ro-bind-fd`. Preserve all prior hashes and record
   new scope report/manifest/archive. Rerun the applicable behavioral/race and
   repository suites; do not claim native evidence from them.
6. **Separate enablement review.** After evidence and limitations are reviewed,
   request explicit maintainer authorization before advertising/registering
   model-facing `create_file`. A design merge, implementation merge, or
   native pass alone is not enablement approval.

## Maintainer decisions requested

1. Does a user explicitly selecting a fresh, per-conversation KompoSyn output
   folder satisfy #321's trusted, user-controlled creation-scope requirement,
   even though users cannot pick arbitrary existing directories in the first
   slice?
2. Is the proposed `$XDG_DATA_HOME/komposyn/creation-outputs` base acceptable,
   and should completed conversation output directories persist indefinitely
   without automatic cleanup or reuse?
3. Should any unrecognized direct entry make the scope unavailable for the
   rest of that conversation? This proposal says yes; it does not offer an
   “approve unknown contents” bypass.
4. Are the proposed 256-entry / 250 ms census limits acceptable, and should
   hidden entries be treated identically as unknown? This proposal says yes;
   reaching either limit or seeing any hidden unrecognized entry makes the
   scope unavailable.
5. Does the host composition's explicit non-instantiation of Token Logger and
   collector/provider-source ingestion suffice for those owner classes to be
   `not in this composition`, while refusing any unknown entry in the output
   scope? Or must those owners be implemented before any create capability is
   eligible?
6. Should a known scope move invalidate the scope when detected, while a move
   between final revalidation and effect remains the accepted ADR-0008 race?
7. What evidence threshold and explicit approval path will authorize a later
   model-facing enablement review after implementation/native acceptance?
8. Is limiting the first slice to a single leaf directly under the output
   directory acceptable, with nested targets and all model-facing directory
   creation excluded?

## Tracker corrections required outside this PR

Do not edit issue bodies in this change. Issue #315's 2026-11-21 current-status
block needs correction because it still says PR #349 “changes tests and
records only” and lists its prototype acceptance as “under review.” PR #349
merged and includes the `readbrokerstudy` operation-completion ordering guard
and deterministic sandbox execution checkpoints, in addition to tests and
records. Update #315 to say that PR #349 merged and its behavioral/race
evidence is recorded; retain production scope coverage and separate
model-facing enablement as open. Its remaining-evidence bullets should reflect
the added execution-path owner arbitration and identical-call-after-effect
coverage, without claiming native coverage for these coordinator behaviors.
Issue #321's parent/current status should link this proposed scope design after
it is reviewed and should keep implementation and enablement unchecked.

## References

- [Issue #315](https://github.com/ayeshLK/komposyn/issues/315), including its
  preserved original specification.
- [Issue #321](https://github.com/ayeshLK/komposyn/issues/321), creation scope,
  evidence, and separate enablement acceptance criteria.
- [Issue #267](https://github.com/ayeshLK/komposyn/issues/267), including the
  creation-only applicability of ADR-0008 and retained non-creation contracts.
- [Issue #322](https://github.com/ayeshLK/komposyn/issues/322), unchanged
  separate editing gate.
- Merged PRs [#343](https://github.com/ayeshLK/komposyn/pull/343) through
  [#349](https://github.com/ayeshLK/komposyn/pull/349); these do not decide
  production scope or enablement.
- [Owner inventory/admission audit](../harness/read-broker-owner-inventory.md),
  [creation integration protocol](../harness/creation-integration-protocol.md),
  [mechanism proposal](../harness/creation-mechanism-proposal.md),
  [lifecycle acceptance](../harness/issue-321-lifecycle-acceptance.md), and
  [native acceptance record](../harness/issue-321-native-acceptance.md).
