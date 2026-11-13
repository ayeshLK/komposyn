# Initial sandbox composition and coverage contract

**Status:** historical design proposal for [issue #215](https://github.com/ayeshLK/komposyn/issues/215), based on source at
`990732888c33c7f6980427e934da6cc1adb6db0e` (merged PR #241). Its
recommendation to keep reads unavailable and its source audit below describe
that revision; later work superseded the read-admission recommendation.
Unless a passage links to later work, its source audit, status and
recommendations are a snapshot of that revision, not current behavior.
Current `cmd/harness` conditionally wires exact-file `read_file` on the
trusted Linux composition, and the documented ChatGPT OAuth route exposes
client tools for catalog-listed models. Reads still require the runtime's
host checks and an exact-target **Allow once** approval; this does not grant
general filesystem access. See the [capability matrix](capability-availability.md),
[accepted unregistered-read policy](read-broker-unregistered-read-policy.md),
and [composition admission implementation](composition-admission-sandbox-service.md).

The subsequent implementation of the auth/settings composition, runtime
pre-consumption seam and non-exposing Linux service is documented in
[composition admission and sandbox service](composition-admission-sandbox-service.md).
The proposal and its recommendations remain useful historical design context;
they do not describe current shipped capability availability. In particular,
the accepted policy permits once-approved disclosure of an otherwise
unregistered exact file while preserving mandatory protections for registered
auth/settings objects and classified provider sources.

## Recommendation

Advance the Linux sandbox composition around the existing fixed, exact-file
typed-read worker. Keep reads unavailable in the shipped chat until trusted
composition coverage is implemented and the remaining protection boundary is
explicitly resolved. The first worker capability should accept one
user-selected root and one exact regular-file route, open the file through the
existing broker, and pass only that read-only descriptor to the fixed worker.
It should not list directories, recurse, write, rename, delete, or run a shell.
The selected model receives the returned bytes only after the user's explicit
read approval; that disclosure is part of the approval.

The accepted object-authority contract (B) protects registered objects and
approved opened objects. It does not establish that arbitrary sensitive data
inside a user-selected root is known and protected. In particular, the chat's
lack of a Token Logger database connection does not make an arbitrary database
in that root safe to read. This is the remaining admission question; user root
selection or a broad warning cannot override it.

The #241 document's immediate next step proposed selecting a SQLite prototype
seam. The later composition-first direction in this assignment supersedes that
sequencing recommendation, not the SQLite findings or lifecycle requirements.
PRs #239–#241 remain valid research. A Token Logger owner is required if a
future composition opens or undertakes to protect a Token Logger Store; its
absence from the current chat composition is not proof that other databases
are protected.

## Audited composition at the source revision above

`cmd/harness/main.go` resolves and loads settings and auth stores, creates the
provider resolver and UI, and configures only the two conversation-local tools
(`update_plan` and `request_user_input`). It does not construct a read-broker
session, sandbox service, or filesystem tool. The auth/settings protection
hooks exist but are optional and are not wired by this composition. The
current `cmd/harness` process does not open a Token Logger store or provider
source records. This is a fact about this process, not a fact about files in a
user-selected root.

| Category | Authoritative source and actual lifecycle | Required protection | Existing evidence and gap | Initial capability consequence |
|---|---|---|---|---|
| Harness auth and OAuth material | `auth.Store.EffectivePath`: `--auth-file` override or `~/.komposyn/auth.json`. Stores API keys, OAuth tokens/registration and profile metadata. Saves and OAuth refresh use private temporary replacement files and atomic replacement. Environment-backed profiles resolve a named variable for the provider operation. | Register effective route and each replacement identity before sensitive bytes; retain retired identities; invalidate/join affected reads before writing. Never expose credential values or environment to worker. | Auth store protection hook and synthetic real-store tests exist (`internal/harness/auth/`, `read-broker-real-store-native-acceptance.md`), but shipped composition does not register it. Environment values are not files known to the auth store. | No read capability until hook registration and lifecycle evidence are part of the selected composition. Never pass provider credentials to worker. |
| Harness settings | `settings.Store.EffectivePath`: `--settings-file` override or `~/.komposyn/settings.json` from `settings.DefaultPath()`. It is resolved independently of `--auth-file`; an auth-only override does not relocate settings. Stores UI selection; atomic temp-file replacement. | Resolve and register the settings store's own effective route and each replacement identity; retain retired identities; invalidate/join affected reads before writing. | Optional settings protection hook and synthetic store acceptance exist; not wired in `cmd/harness`. | Same refusal as auth until its independently resolved route and lifecycle are registered. |
| Provider requests and returned tool data | Selected provider adapter receives the operation credential and sends requests using its HTTP client in the host process. Only a future runtime-approved tool result may be appended to provider continuation. | Keep worker isolated from credentials, clients, sockets and provider state. Bind publication to live turn, root, policy, coverage and grant generations. | Existing provider routes are tested; no filesystem worker/provider publication composition exists. OAuth and unsupported routes remain text-only for tools. | No automatic route fallback. A read result is disclosed to the selected model only after approval and final publication validation. |
| Provider-owned source files | Collector roots/discovery/opening belong to collector/provider adapters; external providers create and rotate these files. The temporary chat composition does not read those records. | If a selected read could encounter a registered source, classify by its opened identity/route and require explicit per-invocation approval. External writes are not made cooperative by root registration. | No complete source owner registration/open-identity hook is connected to the read broker. A root is not an exhaustive source inventory. | Source classification/registration gaps make affected reads unavailable. Hiding a tool or guessing file suffixes does not enforce exclusion. |
| Token Logger database, WAL, SHM and backups | `Store.Open` receives caller-selected DB/backup paths. SQLite creates and retires sidecars; migrations may create a backup. `cmd/harness` has no Token Logger import/open. | If the composition opens/undertakes a store, register database, sidecars, backup and temporary/recovery objects before sensitive writes, and coordinate rotation/close with worker join. | No production broker owner API or demonstrated driver barrier; #239–#241 investigate this lifecycle. The current chat's non-use is source-backed only for this process. | No Token Logger integration is required merely to start chat. It is required before a composition that opens/undertakes that store can attest its protection. Arbitrary DBs in a selected root remain unknown. |
| Other credentials, databases and user files | No KompoSyn owner knows all files or external writers in an arbitrary root. Independent applications can create, copy, rename or link data. | The approved policy requires protected objects/locations to remain denied; object identity cannot identify independent copies of sensitive bytes. No scan, extension/content guessing, or inferred home/root inventory is allowed. | No authoritative inventory or independent-writer barrier exists. | This is the unresolved boundary. Under a strict reading of the current protection contract, generic reads from an arbitrary root cannot yet be admitted as “known safe.” |
| Worker process environment, descriptors and host resources | Current chat has no sandbox child. OAuth browser launch is a separate host auth operation. The study worker profile clears environment and passes a small protocol descriptor set; it does not establish shipped composition behavior. | The worker receives only protocol descriptors and the single approved read capability after trusted startup and checks. No home, credentials, provider sockets/client, broad directory handle or arbitrary inherited descriptor. | Worker/profile behavior is research-only; integration and composition-specific native acceptance are absent. | Sandbox/profile unavailable means refusal, never unrestricted execution. |

Auth/settings path details are implemented in
`internal/harness/auth/store.go` and `internal/harness/settings/settings.go`:
each store exposes `EffectivePath()` and resolves its own default. `cmd/harness`
constructs them from separate flags and registers each store's own effective
path before loading/listing state. An auth-only override does not relocate the
settings route. Protection hooks are in `internal/harness/auth/protection.go`
and the settings store, with platform file helpers alongside them. The
current runtime and service wiring is documented in
[`composition-admission-sandbox-service.md`](composition-admission-sandbox-service.md).
Provider-source lifecycle is audited in
[`read-broker-owner-inventory.md`](read-broker-owner-inventory.md). SQLite
lifecycle findings remain in
[`read-broker-tokenlogger-lifecycle.md`](read-broker-tokenlogger-lifecycle.md)
and the design in
[`sqlite-owner-aware-hook-contract.md`](sqlite-owner-aware-hook-contract.md).

## Trusted composition coverage contract

Coverage is a host-created, revisioned statement about one executable
composition and one capability profile. It is not an authorization grant and
does not widen a resource scope. A tool request, UI choice, root path, filename,
or caller-supplied boolean cannot mark coverage complete.

For a capability, trusted composition declares the participating owners and
required protection obligations from the actual host wiring. Each obligation
has a source-backed applicability record and lifecycle evidence. A proposed
conceptual status set is:

| Status | Meaning | Admission |
|---|---|---|
| `required` | The composition owns or can expose this resource category. | Refuse until all required registrations/evidence are current. |
| `registered` | Effective route(s), identity pins, and required lifecycle barrier are present for the current owner generation. | May proceed subject to all other checks. |
| `pending` | Registration, rotation, cleanup, or owner transition is in progress. | Refuse affected preparation, release and publication. |
| `stale` | Evidence was tied to an earlier composition, route, owner or policy revision. | Refuse and rebuild evidence. |
| `retired` | An owner/capability was removed or shut down; retained pins may still protect old objects. | Never reinterpret as absent in the same revision; refuse until a new trusted composition is built. |
| `unsupported` | The owner or platform cannot supply a required guarantee. | Refuse affected capability. |
| `absent-from-composition` | Source-backed evidence shows this process composition neither opens nor undertakes the category. | Satisfies only that composition's applicability check; says nothing about arbitrary objects in a selected root. |

An `absent-from-composition` record is valid only when produced by trusted
composition construction from its actual dependencies and wiring. Its scope
must name the executable composition revision, enabled capability profile,
and relevant owner graph. Adding an owner, changing effective auth/settings
routes, adding a store or source integration, or expanding the capability
profile invalidates the record and all prepared work/publication tied to its
revision. The record must not be accepted from a plugin, UI, model or generic
caller parameter. Source inspection can establish the current `cmd/harness`
does not open Token Logger; it cannot make “no Token Logger anywhere under the
root” an absence declaration.

Conceptual admission, not a proposed public API:

```text
trusted composition graph + capability profile
  -> required owner obligations and source-backed applicability
  -> current owner registrations / barriers / retained identities
  -> coverage revision
  -> prepare exact target and classify object
  -> authorization and fresh revision checks
  -> consume once grant exactly once
  -> launch fixed worker and release read descriptor
  -> capture privately; join worker
  -> recheck root + composition + coverage + policy + grant revisions
  -> publish only if still current
```

Any unknown or required-but-unregistered obligation refuses the affected
capability before once-grant consumption and before worker launch. Owner
rotation, effective route change, capability expansion, root change, grant
revision, revocation, cancellation or shutdown invalidates affected prepared
work. Active stale output is suppressed; cleanup must be confirmed before
affected writes or new work proceed. Existing pins remain retained through
registry shutdown. No owner invokes another owner while holding the runtime,
registry or owner lock in a conflicting order.

The fixed four-owner `NewSyntheticStudyInventory` remains a research profile.
Its fixture declarations and its results cannot be relabeled as production
composition coverage. Production composition must construct its own trusted
requirements and prove each applicable lifecycle. No machine-wide discovery
is introduced.

### Runtime ordering consequence

The current `internal/harness/runtime/tool_loop.go` performs fresh
authorization, checks for an enforcer, and calls
`ConsumeOnceMatchingAtRevision` before `ToolEnforcer.Execute`. Therefore a
future coverage/preparation refusal that must happen before grant consumption
cannot exist only inside the current `Execute` call. A later implementation
must add a runtime-owned pre-consumption prepare/admission step (or otherwise
demonstrate equivalent ordering), bind its immutable prepared capability to
the request and all relevant generations, and retain runtime as the sole
once-grant consumer. This design does not alter runtime APIs.

## First capability and boundary

The existing brokered worker is the narrowest starting point: one exact regular
file, opened below one explicitly selected root; no symlinks, magic links or
mount crossing under the study's Linux lookup; a read-only descriptor is
transferred to a fixed worker only after readiness and final authorization /
generation checks. It is not a directory mount or a general-purpose file API.
Keep `DirectoryCoverageIncomplete` refusal; a pinned directory does not
inventory descendants. A selected root is a route boundary, not proof of
protected-data completeness and not a permission grant.

Under B, authority follows the prepared/opened object. A rename after open
does not by itself revoke the invocation, and an already-open ordinary object
may remain readable if it gains a late alias. Registered protected identities
remain denied through aliases and renames. This does not identify independent
copies of sensitive bytes, freeze in-place content, or promise live pathname
containment. These distinctions are defined in
[`read-broker-approved-typed-read.md`](read-broker-approved-typed-read.md).

The exact-file worker can reduce exposure to one reviewed descriptor, but it
cannot determine whether an unregistered ordinary object is actually a
credential file, database, backup, or provider record. Therefore the initial
profile has two independent gates:

1. **Composition gate:** current required owners (at minimum the effective
   auth/settings stores) have registered routes and rotation barriers; owner
   applicability is derived from trusted composition. If a future composition
   opens Token Logger, its real owner obligations become required. Provider
   source identity/classification obligations apply wherever such data may be
   offered to this capability.
2. **Unknown-root-data gate:** the profile must have a reviewed rule for
   unregistered objects inside the selected root. The current contract
   requires protected credentials, databases and provider records to remain
   protected, but this composition has no exhaustive inventory for arbitrary
   roots. Until that boundary is resolved and enforced, exact-file reads from
   arbitrary user roots remain unavailable. Neither a per-file read approval
   nor a warning changes that result.

This is not a claim that all reads are impossible in principle. It is a precise
reason the present implementation cannot honestly advertise an available
generic read profile under the accepted mandatory-protection rules.

The separate decision packet
([unregistered exact-file read policy](read-broker-unregistered-read-policy.md))
compares retaining this refusal with a once-approved disclosure of an
otherwise-unregistered exact file. The maintainer accepted the consolidated
six-clause contract for staged implementation in the [issue decision
comment](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6709361902).
The service's current behavior and this document's refusal remain in force
until implementation and enablement gates are separately completed. Stage 1
adds `/fs-root` selection/status/clear as in-memory lookup scope only; it does
not change access. The historical #243 service result and its reproducible
runner are recorded in [composition native acceptance](read-broker-composition-native-acceptance.md);
that evidence does not alter the gate.

## Proposed user and runtime lifecycle

This diagram records the staged design. Stage 1 implements explicit root
selection/status/clear and lifecycle invalidation; admission, approval, worker,
capture and publication remain future work.

```text
text-only chat (no root required)
  -> user explicitly selects an absolute root for this in-memory chat
  -> UI shows canonical root and Linux backend availability
  -> root selection changes root generation; grants/preparations/dialogs stale
  -> composition coverage and backend checked independently
  -> an exact read request is prepared/classified
  -> user approves tool + exact resource and provider disclosure
  -> runtime consumes once grant; broker revalidates; worker gets one FD
  -> bounded bytes captured privately; cancellation/revocation terminates worker
  -> after join and generation check, result may enter provider continuation
```

Selecting a root grants no read or write authority. There is no cwd/home/repo
default. The root is in-memory for the current conversation/process and clears
on confirmed reset/shutdown. Changing or losing the root increments its
generation, invalidates grants/prepared targets that depend on it, dismisses
pending approval, suppresses old output and requires workers to be joined
before affected work resumes. Unsupported namespace/seccomp/backend conditions
are surfaced as unavailable with actionable cause; no weaker backend or
unrestricted fallback is used.

Permission approval remains separate from root selection, backend
availability, owner coverage and target classification. Provider-source reads
require explicit per-invocation approval. A generic read never gains provider
authority through a normal-file grant. Revocation blocks dispatch/publication;
it does not claim to roll back a completed read. No grant or chat state is
persisted.

Failure classification must separate denied, unavailable/unsupported,
not-executed before capability release, and unknown/partial after release when
the read or cleanup result cannot be established. A failed launch does not
restore a consumed once grant, trigger an unrestricted retry, or replay the
read. Keep cleanup obligations and pins until confirmed worker join and
registry shutdown. Shutdown retains resources and reports incomplete cleanup
if it cannot establish join.

## Evidence and implementation stages

| Evidence class | Existing evidence | Does not establish |
|---|---|---|
| Synthetic broker/runtime | Disposable fixtures, authorization/revision checks, fixed four-owner inventory, exact-file descriptor flow, bounded worker, stale publication tests. | Real composition coverage or arbitrary-root protected-data completeness. |
| Real auth/settings lifecycle | Study hooks exercise public stores with synthetic temporary paths, registration-before-write, rotations, held worker and publication invalidation. | Hook wiring in `cmd/harness`, real credential safety, or unrelated owner coverage. |
| Native sandbox | The reviewed #235 archive `6gGbUF` records 59 measured passes, one inconclusive and one unavailable for merged source `c82305ce99d6c3aea8d6cc44c10161a505e79ff6` under a complete synthetic inventory. Later real-store native cases are defined separately. | Current composition, an arbitrary user root, all Bubblewrap modes, independent owners, or complete worker descendant cleanup. A run for a different commit/profile is not transferable. |
| SQLite owner research | #239 bounded writable-object experiment, #240 WAL/SHM ordering, #241 owner-aware callback design. | A production Token Logger hook or a requirement to open Token Logger merely to start chat. |
| Production composition | None. `cmd/harness` has no sandbox/readbroker integration and no filesystem tool. | Any tool execution, auth/settings hook registration, provider publication barrier or user-root guarantee. |
| External data/platform | No exhaustive inventory of unrelated credentials, databases, provider data or independent copies; no non-Linux enforcement acceptance. | Protection of arbitrary roots, non-Linux behavior, shell/write/delete safety. |

### Staged implementation plan

1. **Composition admission skeleton (no tool exposure):** construct a
   host-owned coverage profile from actual `cmd/harness` wiring; register
   effective auth/settings paths before stores can be read or written; bind
   absent-owner attestations to the composition/capability revision. Unknown,
   pending, stale, or unsupported coverage refuses before once consumption.
   Keep Token Logger absent only for the current process composition and do
   not infer anything about arbitrary root contents. Test overrides and
   rotations with held workers.
2. **Sandbox service composition (still no tool exposure):** integrate the
   existing fixed worker as a host-owned service, preserve trusted Bubblewrap
   feature detection, seccomp, descriptor inventory, no-network/environment
   restrictions, cancellation/join and publication gate. Add the runtime
   pre-consumption preparation/admission ordering noted above. Exercise a
   real ordinary-file positive control and protected-route/identity negatives
   in a normal Linux host run.
3. **Resolve unknown in-root objects:** before advertising exact reads, obtain
   explicit maintainer review of a concrete enforceable boundary for
   unregistered objects. Options are: (a) keep arbitrary-root reads disabled
   until participating owners can attest all required categories; or (b)
   revise the protected-data contract to permit explicit per-invocation
   disclosure of unregistered exact objects, with precise treatment of
   credential/database/provider data and alias limits. Option (b) is a policy
   change and must not be implemented by warning text, user root selection or
   an implicit default. This document recommends (a) until the consequences of
   (b) are explicitly accepted.
4. **Independent typed-read enablement review:** after the above has measured
   native evidence, review tool schema, UI approval/disclosure text, supported
   routes, owner obligations, platform limits and checklist separately.
   Shell, writes/rename/delete, other platforms, Token Logger integration if
   composed, and final tool enablement remain separate later work.

The next implementation task should cover stage 1 and the non-exposing service
composition in stage 2, with the unknown-root gate explicitly refusing all
generic reads. It must not register `read_file` or claim useful model-facing
availability. A subsequent maintainer decision on option (a) or (b) is needed
before a task can enable any generic exact-file read.

## Acceptance outline for later implementation

- Trusted composition constructs applicability; no API accepts a caller-set
  completeness flag. Tests prove an absent owner is accepted only with
  composition evidence, while missing, pending, stale, retired and unsupported
  required owners refuse.
- Adding an owner or expanding a profile invalidates previous absence records,
  prepared operations and publication. Late Token Logger activation refuses
  until its actual DB/sidecar/backup owner obligations are met.
- Auth/settings default and override paths register before protected writes;
  replacement object is pinned while empty; held workers block sensitive
  writes until joined; old identities remain protected. Exercise auth-only
  override, settings-only override, and both overrides to unrelated
  directories; verify each registered route matches its own store's
  `EffectivePath()`.
- Root, grant, coverage and policy changes invalidate queued/active work;
  once consumption has one owner; unknown coverage consumes no grant and
  starts no worker.
- Worker gets no credential, provider client, directory handle or unexpected
  descriptor. Unsupported backend, failed startup, failed probe, missing
  owner, no-op worker and early unrelated errors do not count as a read pass.
- Positive read checks returned bytes against the approved opened object and
  confirms readiness, descriptor release, read, join and publication. Negative
  cases confirm fixture state and no stale bytes reach the provider.
- Native acceptance records exact source, Bubblewrap mode and host; synthetic
  or prior-revision evidence is not carried forward. Linux only; other
  platforms, shell, mutations and descendants require separate evidence.

## References

- [Issue #215](https://github.com/ayeshLK/komposyn/issues/215), including the
  [approved object-authority contract](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377)
  and [broker research](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6575572199).
- [PR #241](https://github.com/ayeshLK/komposyn/pull/241) and
  [previous composition/owner audit](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6685276112).
- [Sandbox direction](sandbox-design-proposal.md),
  [broker feasibility](read-broker-feasibility.md),
  [owner inventory](read-broker-owner-inventory.md),
  [real-store acceptance](read-broker-real-store-native-acceptance.md),
  [approved typed-read profile](read-broker-approved-typed-read.md), and
  [SQLite owner-aware hook design](sqlite-owner-aware-hook-contract.md).
- Source at `990732888c33c7f6980427e934da6cc1adb6db0e`: `cmd/harness/main.go`,
  `internal/harness/runtime/tool_loop.go`, auth/settings stores and hooks, and
  `internal/harness/readbrokerstudy/`.
