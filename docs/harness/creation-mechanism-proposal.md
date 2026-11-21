# Proposed one-file creation mechanism

**Status:** a bounded disposable-fixture prototype is being implemented under
the worker assignment. The Linux service now consumes its barrier and
owner-ordered stage/publication permits through a retained-parent creation
path. An operation-specific worker protocol stages exact bytes and publishes
with `RENAME_NOREPLACE`; synthetic manifest checks and focused behavior tests
are present. The existing native acceptance runner now includes a real
Bubblewrap creation case for a disposable ext4 fixture, exact bytes/mode,
no-replace incumbent preservation, stage cleanup and worker join. That native
cases have not yet run on an eligible host; native lifecycle acceptance and
remaining boundary evidence are tracked in the [lifecycle acceptance record](issue-321-lifecycle-acceptance.md). This
does not create a supported mutation profile or model-facing capability.
Normal production composition remains closed.

This is a creation-only revision. It accepts external filesystem changes
during approval and between the last check and publication. Approval is not a
lock, and this candidate does not establish isolation against hostile
same-user actors or approval/staging interference. Creation must still refuse
an existing leaf at the publication syscall. Editing, overwrite, append,
rename and delete retain their separate contracts and gates.

## Candidate and proposed pilot boundary

Assess one Linux mechanism: stage the complete approved bytes as a uniquely
named, mode-0600 regular file in the already-open destination parent, then
publish that name using `renameat2(parentFD, stageName, parentFD, leaf,
RENAME_NOREPLACE)`. The source and destination are in the same pinned parent.
The syscall atomically installs the staged directory entry only if the target
name is absent; `EEXIST` is a collision and never falls back to truncation,
overwrite or append. The selected parent directory object is retained by
descriptor, so renaming a parent does not redirect the operation to a
replacement directory.

The first proposed native pilot is Linux amd64, local ext4, with Linux 5.6 or
newer for `openat2` path resolution and a filesystem/kernel pair that accepts
`RENAME_NOREPLACE`. This is a test candidate, not a supported profile: no
evidence has been collected for this composition. `openat2` resolution uses
`RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS`; descendant resolution also uses
`RESOLVE_BENEATH | RESOLVE_NO_XDEV`. Unsupported flags, syscall or filesystem
behavior cause refusal. There is no path-based, procfs, `O_TMPFILE`, rename
without `RENAME_NOREPLACE`, or unrestricted fallback. macOS, Windows, network,
overlay, FUSE, bind-mounted descendant and unverified filesystems remain
unavailable.

The choice is bounded because `renameat2(RENAME_NOREPLACE)` supplies the
required destination-collision rule without making the destination pathname
replaceable, and it does not need `O_TMPFILE` publication through procfs or
`AT_EMPTY_PATH`. It does not close the source-stage, approval-channel or
same-user races accepted by the revised boundary. The prior #319 results
remain negative evidence for their tested direct-create, `O_TMPFILE`, and
broker compositions; they neither test nor accept this proposal.

The syscall contract is documented in the primary Linux man pages:
[openat2(2)](https://man7.org/linux/man-pages/man2/openat2.2.html) describes
the resolution flags and Linux 5.6 availability;
[renameat2(2)](https://man7.org/linux/man-pages/man2/renameat2.2.html)
documents `RENAME_NOREPLACE`, its `EEXIST` outcome and filesystem support
requirements; [write(2)](https://man7.org/linux/man-pages/man2/write.2.html)
documents that writes may be partial; and
[fsync(2)](https://man7.org/linux/man-pages/man2/fsync.2.html) documents
file and directory synchronization. The Linux kernel's
[seccomp filter guide](https://docs.kernel.org/userspace-api/seccomp_filter.html)
states that filters see syscall register values, not pointed-to memory. These
are syscall semantics, not evidence that this application composition works
on any host.

## 1. Runtime file object and serialization

The host runtime would own an in-memory `CreationObject` with:

- A random, process-local object ID; model-supplied IDs are ignored.
- The trusted user-selected scope generation and canonical displayed scope.
- A retained descriptor for the intended parent directory, plus its observed
  device/inode identity and mount/filesystem facts.
- One validated basename leaf and the observed baseline `absent`.
- Immutable exact UTF-8 bytes (maximum 64 KiB), their digest, and monotonically
  increasing proposal/object generations.
- Captured owner-coverage and policy revisions, provider destination and
  conversation/turn/tool-call identity.
- Lifecycle state: prepared, awaiting approval, executing, created, conflict,
  failed, partial/unknown, or retired.

There is not yet a production creation object. The fixture-only service now
retains an in-memory proposal with a parent descriptor, absent leaf, exact
bytes, destination and captured generations. The existing `PrivateReadCapture`
and retained read admission describe a completed read; they do not retain a
writable parent, absent leaf, proposed bytes or creation generation and must
not be repurposed as a ready-made save object.

Each object owns its parent descriptor from preparation until execution has
joined and final result handling completes. On every terminal path, the owner
closes stage and parent descriptors exactly once; if a worker cannot be
joined, it retains cleanup ownership and blocks affected admission. Proposals,
bytes and activity stay in memory and are retired with the conversation or
service.

Serialize internal creation effects with one host-service-wide barrier and
one active effect lease, held from final generation/revalidation checks
through publication and outcome capture. The current prototype rejects a
second simultaneous create while the lease is active; it does not queue it.
Do not hold the effect lease while the trusted UI waits for a user. A service
wide lease avoids string-key locks missing two runtime objects that reach the
same parent/leaf through path aliases, bind aliases or namespace renames. This
lease coordinates only KompoSyn operations in this service; it neither locks the filesystem nor
coordinates another process.

## 2. Preparation and scope

The trusted UI selects a mutation scope independently of the working directory.
Selection grants no access. The host captures a scope generation; model paths
are treated only as a requested relative target beneath that scope. Absolute
paths, `..`, empty components, multiple-file input and a model-selected scope
are rejected. The leaf is a single basename, not `.` or `..`, and contains no
slash, backslash or NUL. No symlink or magic-link component is followed.

The eventual provider-facing schema is a strict object with exactly `path`
(relative target) and `content` (the complete proposed UTF-8 text), with no
patch hunks, operation selector, object ID, digest, permission or scope field;
unknown fields are rejected. The host validates the UTF-8 encoded content at
no more than 64 KiB after JSON decoding. JSON schema character limits alone do
not substitute for this byte limit. The trusted UI derives and displays the
complete creation diff from these exact bytes.

The host opens `/` as an `O_PATH|O_DIRECTORY|O_CLOEXEC` anchor. It resolves the
trusted selected scope relative to that anchor with `openat2`,
`O_RDONLY|O_DIRECTORY|O_CLOEXEC`, and
`RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS`; `RESOLVE_NO_XDEV`
is intentionally omitted for this one resolution so the selected scope may
itself be an ext4 mountpoint. It resolves descendants relative to that scope
with `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|
RESOLVE_NO_XDEV`. It retains the resolved parent descriptor, requires a
directory on the candidate local ext4 filesystem and records its identity.
No missing parent is created. The target baseline is
observed with `fstatat(parentFD, leaf, ..., AT_SYMLINK_NOFOLLOW)`; `ENOENT`
means observed absent, and an existing file, symlink or other entry prevents a
creation proposal. A parent/scope rename does not redirect the
retained descriptor to a replacement object. If re-resolving the displayed
route after the user responds shows a different parent identity, treat that as
a detected conflict and require a new proposal; a move after that check remains
part of the accepted residual race.

Admission is refused if the host cannot establish required protected-owner
coverage for the scope and target, if an owner transition is pending, or if
the service/profile is closed or unsupported. The current read registry and
`NewHostService` do not establish creation coverage for arbitrary user-selected
scopes. This proposal therefore does not claim that existing read composition
is sufficient; the creation prototype must add and prove its own coverage
integration before any write is attempted.

## 3. Proposed effect algorithm

1. Validate exact bytes as valid UTF-8, reject NUL/binary content, and enforce
   the 64 KiB limit. Preserve bytes exactly; creation performs no line-ending
   normalization. Freeze the bytes and digest in the runtime object.
2. Resolve the parent descriptor and absent leaf. Check owner routes,
   registered protected identities/ancestors and current scope, policy and
   owner generations. Use `fstatat(..., AT_SYMLINK_NOFOLLOW)` for the leaf.
   Missing or ambiguous coverage refuses before approval.
3. Ask the trusted UI to show the exact destination, operation, complete
   proposed file/diff, byte count, and provider/model/auth-profile destination.
   Obtain explicit once approval bound to object ID/generation, parent
   identity, absent leaf, bytes digest, scope/policy/owner generations,
   provider destination and call identity. No conversation grant applies.
4. After approval, atomically consume the once receipt in runtime admission
   and reserve the logical effect lease. Revalidate service state,
   scope/policy/owner generations, parent identity and displayed route, target
   absence, and protected-owner checks. The receipt is never restored. A
   detected change requires cancellation or a new proposal and fresh approval.
5. Acquire the separate transient-stage barrier and complete its read/list/search
   drain **before creating any stage directory entry**. It closes new admission
   before the stage name can affect visible names or counts. If admission
   invalidation or draining fails after the gate closes, create no stage and
   retain the blocked gate and cleanup ownership. If the barrier cannot be
   acquired because the service is busy or closed, create no stage and release
   only reservations actually acquired. In either case this consumed proposal
   ends without effect; a retry requires a new proposal and approval. Once the
   barrier is active, obtain a stage-entry permit from the
   shared owner/effect-state coordinator. If an owner transition or cancellation
   won first, create no stage and cleanly retire or retain the barrier as
   required. If stage-entry admission won first, create one random same-parent
   stage name with `openat2(parentFD, name,
   O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC, 0600)` and
   `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|RESOLVE_NO_XDEV`.
   The stage descriptor is `O_WRONLY|O_CLOEXEC`, owned by the process's
   filesystem credentials; creating and renaming it require the worker's
   credentials to have write and search permission on the parent. Its intended
   mode is exactly 0600,
   enforced with `fchmod` before publication. It is a regular file. No
   capabilities, procfs, directory creation, privilege change or temporary
   external store is used. Stage-name collision or creation failure is a
   pre-publication failure; there is no retry of the save. The barrier prevents
   concurrent KompoSyn read, listing and search operations from observing the
   stage. This barrier is not generic protected-owner rotation:
   it must not increment auth/settings owner, policy or coverage generations,
   invalidate its own consumed receipt, or invoke the generic `Protect` path.
   Retire the stage guard only after successful publication or cleanup is
   confirmed; an unjoined worker or uncertain stage keeps the read gate closed.
6. Write the frozen byte slice with a bounded loop until complete or error;
   partial writes are handled as incomplete staging. `fsync(stageFD)`, close
   the writable descriptor. A stage write/sync/close
   failure removes the stage when its identity is still the one created by
   this request; cleanup failure is reported and admission remains blocked
   until cleanup is confirmed.
7. Immediately before publication, recheck filesystem state and obtain a
   one-use publication-release permit from the same short-held coordinator
   lock that records owner-transition-pending state. This atomic in-memory
   transition is the ordering point: if owner transition or cancellation marks
   pending first, no permit is issued and the stage is cleaned up; if release
   wins first, the operation is potentially in flight and the owner transition
   waits for its classified outcome. No lock is held across the syscall. A
   cancellation racing after release may still precede or follow the syscall;
   classify the observed result and never infer rollback.
8. Publish only with `renameat2(parentFD, stageName, parentFD, leaf,
   RENAME_NOREPLACE)`. `EEXIST` is a collision even if the last observation
   found absence. Never retry as replace, truncate, append, link, or plain
   rename. Any unsupported flag/filesystem outcome refuses without fallback.
9. After successful publication, open the leaf without following links and
   compare its identity and bytes with the staged object/proposal; then
   `fsync(parentFD)`. This detects some discrepancies but cannot prove no
   intermediate mutation occurred. Failure after publication is partial or
   unknown, not “not executed”; never delete as rollback. Retire handles and
   publish a result only if runtime generations remain current.

The syscall's no-replace rule is the atomic guarantee: at its publication
point an existing leaf is not overwritten. Pre/post checks are observations.
The proposal does not claim atomic approval-to-write, immunity from a same-user
actor modifying or replacing the stage, or crash-proof durability on every
filesystem. A power loss, filesystem/server uncertainty, process death after
publication, failed parent sync, lost completion or inability to identify the
published object yields an unknown/partial outcome and no automatic replay.

The actual primitive may be refused by ext4 mount options, read-only state,
quota, permissions, filesystem errors or unsupported kernel/syscall behavior.
Each is a refusal/failure; there is no weaker fallback. The pilot must record
exact kernel/filesystem/mount facts. No general ext4 support claim follows from
the man-page contract.

## 4. Approval and conflicts

The UI displays the literal target path under the selected scope, one-file
creation operation, complete bytes/diff, size and destination. The once receipt
binds the exact immutable proposal and generations above. Providers receive
only tool definitions and tool results; they cannot approve, authorize or
execute the effect. A destination change retires the pending receipt.

After the user responds, any detected change to the selected route/parent,
scope, policy, owner coverage or target invalidates the old proposal. Do not
show newly discovered file content in the conflict UI or send it to the
provider without separate read/disclosure authorization. For initial creation,
an existing destination offers only cancel or prepare a new proposal for a
different target. No overwrite, append or merge action is offered.

If an external entry appears after revalidation, `RENAME_NOREPLACE` returns a
collision and the operation stops. If that entry disappears before the
syscall, creation may proceed; this is one of the external timing outcomes
accepted by PR #343. There is no automatic retry with a new baseline.

## 5. Protection and disclosure

Before approval and again before effect, classify the target route, parent and
available identities against the owner inventory. Auth/credential and settings
routes and retained/rotated identities remain protected, including aliases.
Provider-owned source files remain read-only. If this composition owns a Token
Logger store, its database, WAL, SHM, journal and backups remain protected
through owner registration and rotation. If a relevant owner is absent,
stale, pending, unsupported or ambiguous, the affected scope is unavailable.
Do not infer machine-wide sensitive-path discovery; only trusted composition
owners and explicit scope coverage count.

Serialize owner updates with effect admission, advance owner/policy generations,
invalidate affected proposals and stop new admissions before draining work.
Retain old protected identities through aliases/rotation as the existing owner
lifecycle requires. Incomplete cleanup blocks affected scope admission. These
checks prevent known protected targets from being offered or created under a
current observation; they cannot close the accepted race with an external
actor changing namespace or staged content after the last check. Conflict
detection itself grants no read permission and discloses no newly observed
content.

## 6. Lifecycle and outcomes

Runtime owns receipt consumption and internal admission. The once receipt is
consumed exactly once after approval and before staging. Revocation, scope
change, service close, owner/policy change and conversation retirement first
mark affected proposals invalid and prevent new admission; then cancel active
work, join it and close descriptors. Incomplete join keeps cleanup ownership
and blocks affected admission. Cancellation before publication removes the
request's stage if identity-safe cleanup succeeds. Cancellation racing with or
after publication cannot promise no effect; classify from confirmed evidence or
return unknown.

Use explicit outcomes: `not_executed` only when publication is known not to
have happened; `created` only when the published entry matches the approved
proposal and required completion checks succeed; `conflict` for a detected
pre-effect change or primitive collision; `failed` for a confirmed
pre-publication failure with cleanup; and `partial_or_unknown` whenever
publication may have occurred but completion, identity, sync or cleanup is
uncertain. Never claim rollback. A failed provider continuation after a
confirmed creation does not authorize replay; retain a one-shot completion
record in memory so a lost response cannot execute the same save a second
time. Suppress stale UI/provider result publication after generation changes.

## 7. Integration boundaries

- **Runtime:** owns file objects, immutable proposals, authorization receipt,
  one-consumer admission, generations, serialization, cancellation, result
  classification and publication eligibility.
- **Host filesystem boundary:** resolves the selected scope, pins parent
  descriptors, checks trusted owner coverage, starts a fixed one-shot
  creation worker, joins it and owns cleanup. The worker receives one parent
  directory descriptor (opened `O_RDONLY|O_DIRECTORY|O_CLOEXEC`) plus one
  bounded request over a private pipe; its
  protocol has no provider, credential, grant, shell or general-executor
  fields. Before accepting payload, the worker sets `PR_SET_NO_NEW_PRIVS` and
  installs a seccomp allowlist with thread synchronization (`TSYNC`) across all
  threads; it needs no `CAP_SYS_ADMIN`. The allowlist covers only the measured
  Go runtime startup/exit needs and filesystem operations, including `read`,
  `openat2`, `write`, `fchmod`, `fsync`, `unlinkat`, `close`, `renameat2`,
  bounded metadata checks and exit. Network, exec, mount and process-control
  syscalls are denied. Filter or syscall coverage uncertainty means refusal.
  The Go runtime's actual thread/syscall requirements must be measured; filter
  installation must cover all threads or the worker is unavailable.
  Descriptor-number constraints may restrict file operations to the passed
  parent descriptor. Seccomp cannot
  validate the path strings behind syscall pointers, so this is an
  operation-specific trusted worker boundary, not proof that a compromised
  same-user process can affect only the approved leaf. The passed directory
  descriptor itself permits changes to that directory; its same-user
  interference risk is within the accepted residual boundary and must be
  visible in review.
  The transient stage guard is separate from protected-owner registration. It
  closes read/list/search admission and drains existing workers; while active,
  it excludes the temporary stage route/identity. Its lifecycle must not bump
  the auth/settings owner, policy or coverage revisions or refresh the consumed
  save receipt. Retirement/reopen order is an explicit design blocker below.
- **Trusted UI:** owns explicit scope selection and once approval; shows the
  exact path, complete proposal, effective operation and destination. UI
  integration must not grant scope by navigation or expose conflict content.
- **Provider adapters:** own provider-specific schema/continuation formats.
  They receive no filesystem descriptor, host authorization or execution
  responsibility. Creation results return through the existing runtime tool
  result path only when current.

The current `cmd/harness` composition passes one sandbox `Service` to runtime
read admission, preparation and execution. Linux sandbox `AdmitFilesystemRead`
and `PrepareAdmittedFilesystemRead` retain read-specific authorization and
owner checks; `ExecuteAdmittedFilesystemRead` dispatches fixed read workers.
The runtime then builds provider-visible tool definitions and approval
requirements; the UI bridge presents/returns the choice. Provider contracts
carry tool calls and results, not authority. The creation path would need
parallel typed create-specific interfaces through this same composition seam;
it must not route a writer through read capture or expose a generic `Write`
method to providers.

## 8. Limits and design blockers

This candidate means: KompoSyn's serialized in-process attempt stages approved
bytes and asks the kernel/filesystem to install that staged name at the pinned
parent only if the leaf is absent at the publication syscall. A detected
conflict causes fresh user approval. It does **not** mean no external change
can occur during approval or between checking and writing, nor that a hostile
same-user process cannot interfere with the approval process, stage name or
staged inode. That is the accepted revised boundary, not successful
enforcement of the former #267 contract.

The following must be resolved before implementation/evidence can support any
enablement:

1. **Composition owner coverage:** the shipped host composition has protected
   auth/settings lifecycle for reads, but its generic-root-data gate is
   unresolved and it declares provider-source and Token Logger ownership
   limitations. A trusted mutation-scope inventory and fail-closed coverage
   contract are not implemented for production scopes. The prototype uses
   only a synthetic test manifest and remains unavailable in normal
   composition.
2. **Trusted approval/release integration:** the explicit runtime seam and
   service proposal binding now build an exact once request and consume a
   retained proposal once. Full real-worker runtime/UI/provider execution and
   integrity against same-user interference are not yet established. The
   revised boundary accepts this residual risk but does not waive those
   integration tests.
3. **Transient-stage/read lifecycle:** `linuxImplementation.Protect` advances
   `s.revision`, invalidates read continuations and admissions, and registers
   owner rotations. It cannot be reused for a stage created by the save that
   already consumed its once receipt: doing so can stale that save and can
   deadlock while it waits for its own drain. The required design is a distinct
   host-local stage barrier: atomically stop new read/list/search admissions;
   cancel and join existing read/list/search work that could observe the
   selected scope; retain a temporary route/identity exclusion while the stage
   exists; publish or clean up; retire the exclusion only after joins and
   descriptor cleanup; then reopen reads. The barrier uses its own lifecycle
   state and must not refresh or reconsume the approval receipt or mutate
   protected-owner/policy/coverage generations. If cancellation, join or
   retirement is incomplete, keep the affected read gate closed and retain
   cleanup ownership. The Linux fixture now has a create-specific barrier and
   owner-ordered stage/publication permits; focused tests cover selected state
   transitions. The full read-worker lifecycle/race matrix remains pending.
   The separate [integration
   protocol proposal](creation-integration-protocol.md) specifies a
   service-wide fail-closed barrier, lock/wait order, cancellation and
   retirement states, and evidence cases. This resolves the design-description
   sequencing; completing and proving lifecycle behavior remains a blocker.
   Do not use generic owner rotation or skip invalidation as a shortcut.
4. **Candidate evidence:** no native actor study or runtime/UI/provider
   creation journey has tested this stage-and-rename composition. `ext4`,
   amd64 and kernel prerequisites are a proposed pilot tuple only. No claim of
   correctness or support is inferred from #319 or the read studies.

These are blockers, not assumptions to fill with tests that silently relax
owner coverage or approval semantics. PR #344 merged the mechanism proposal;
it did not accept the mechanism as implementation-ready. The integration
protocol proposal adds concrete sequencing but does not resolve production
coverage: the current composition cannot attest complete coverage for
arbitrary user-selected scopes. The current maintainer assignment explicitly
authorizes only a bounded disposable prototype; it does not satisfy positive
native/integration acceptance or model-facing enablement.

## Bounded follow-up prototype plan

Under that bounded prototype authorization, implement only a disposable
Linux/amd64/ext4 study fixture of the named stage plus no-replace publication,
alongside the minimum create-specific runtime/UI/provider integration needed to
exercise the real composition. No production tool exposure, editing, overwrite,
append, rename, deletion, new privilege, host-policy change or alternate
executor is in scope. Stop immediately if owner coverage or the once-approval
receipt cannot fail closed through the existing composition seam.

### Acceptance matrix

| Scenario | Required observation | Stop/refusal condition |
| --- | --- | --- |
| Two internal requests, same identity via distinct path aliases | One effect holds the service effect mutex; second revalidates and receives collision or stale proposal; one receipt consumer | Alias resolution or once-consumption cannot be proven |
| Target appears while approval is pending | Old proposal/receipt invalidated; fresh proposal required; no provider disclosure of its contents | Old receipt remains usable |
| Target appears at publication primitive | `RENAME_NOREPLACE` yields collision; incumbent bytes/identity unchanged | Any replace/truncate/append fallback or unsupported flag |
| Parent moved/replaced before post-approval revalidation | Retained object and displayed route are compared; detected change invalidates approval; no redirect to replacement | Parent identity is path-string-only |
| Parent moves after final observation | Deterministic actor demonstrates documented residual race; outcome reports actual parent object affected | Report claims external exclusion or exact live-path containment |
| Protected owner registered, rotated, aliased or made incomplete | Pending proposal invalidated; missing/stale coverage refuses before stage/publication | Any protected owner gap permits effect |
| Owner transition between final filesystem validation and publication release | Shared coordinator orders pending transition against one-use release permit: transition-first means no permit/syscall; release-first means cancellation request plus wait and truthful outcome classification | An owner update is reported complete while a released effect remains unclassified, or a transition that won first still permits publication |
| Stage self-registration after once-receipt consumption | Dedicated transient barrier stops admissions, drains existing readers, excludes the stage, retires only after cleanup, and leaves auth/policy/coverage generations and consumed receipt unchanged | Generic `Protect` invalidates its own save, or reads can observe stage |
| Stage barrier drain/retirement incomplete | No stage write/publication if drain fails; after effect, keep affected reads closed, retain guard and cleanup ownership until confirmed | Admission reopens or stage guard retires before joins/cleanup |
| Cancellation before publication / during publication | Before publication: stage cleanup confirmed; around/after syscall: truthful created/partial/unknown, never rollback | Incomplete join releases admission or triggers replay |
| Worker/descriptor join incomplete | Cleanup ownership retained; affected scope admission remains blocked; resource leak recorded | Service admits another affected operation |
| Partial write, fsync, close, rename or parent-fsync failure | Correct not-executed, failed, partial or unknown classification; stage cleanup/retention recorded | Outcome guessed as success or safe retry |
| Provider continuation fails after creation | Effect remains one-shot; no automatic repeat when continuation is retried | A retry can repeat the side effect |
| External stage mutation after final observation | Actor changes synthetic staged bytes/name at deterministic barrier; report demonstrates accepted race and whether post-check detects it | Classified as proof of isolation |
| Resource cleanup | Parent/stage descriptors, helper and temporary names are accounted for after success, denial, collision, cancellation, crash and timeout | Unjoined helper or ambiguous stage is silently discarded |

The native run must record source commit and clean-tree digest, harness and
runner hashes, archive/report hashes, host and OS build, kernel, architecture,
filesystem type and mount options, namespace identities, launch/backend mode,
actual same-user actor credentials, syscall outcomes, bytes/identities before
and after, cleanup joins, and per-case measured-pass/refusal/failure/
inconclusive/unavailable classification. Use disposable synthetic files and
independent actors; do not use private/provider data. Native syscall cases do
not replace runtime/UI/provider integration evidence, and ordinary Go/race
tests do not establish filesystem behavior.

Prototype stopping conditions: any wrong bytes or replacement; destination
overwrite; untracked authority/descriptor; incomplete owner coverage; receipt
reuse; admission during incomplete cleanup; untruthful or replayable unknown
outcome; unsupported host fallback; or a discovered need to change the
accepted threat boundary. Stop and return the concrete failure for a separate
decision. A passing study would still not enable creation. Prototype scope,
native acceptance and model-facing enablement require separate approvals.

## References

- Accepted boundary: [ADR-0008](../adr/0008-propose-runtime-file-objects-and-approved-saves.md), [merged PR #343](https://github.com/ayeshLK/komposyn/pull/343).
- Prior scoped negative research: [#319 closeout](issue-319-closeout.md), [#320 closeout](issue-320-closeout.md).
- Creation gate: [issue #321](https://github.com/ayeshLK/komposyn/issues/321); editing gate: [issue #322](https://github.com/ayeshLK/komposyn/issues/322).
- Host composition: `internal/harness/sandbox/service.go`, `internal/harness/sandbox/service_linux.go`, `internal/harness/runtime/read_admission.go`, `internal/harness/runtime/tool_loop.go`, `plugins/tools/conversation.go`, `plugins/ui/ui.go`, and `cmd/harness/main.go`.
