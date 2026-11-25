# Proposed staged replacement mechanism for issue #322

Status: **design proposal for review; no prototype or editing enablement**.
Based on merged [ADR-0010](../adr/0010-propose-approved-entry-replacement-for-file-editing.md)
and source `68c274aba40c2de3d6a63ca242423386ecc8046b` (PR #361).
Creation remains independently delivered; editing remains unavailable.

## Recommendation and review gates

Investigate a fixed editing worker that prepares a named, same-directory
staging file and calls
`renameat2(parentFD, stageLeaf, parentFD, targetLeaf, 0)` exactly once per
publication release. Both leaves are frozen host-owned single components;
the worker never receives a model-selected directory or executable command.

This is a concrete candidate, not an accepted mechanism. It has three explicit
review gates: the late-disappearance behavior of ordinary replacement,
complete metadata eligibility despite unprivileged visibility limits, and
safe cleanup of named stages under external substitution. The proposal does
not waive these gates or authorize a prototype before their disposition is
reviewed. Its evidence below is source inspection and documented API
semantics, not native acceptance.

```text
approved read committed → bounded host-owned read receipt
  → exact patch + eligible target/base/metadata + complete diff
  → once approval → one coordinator attempt
  → stage approved result → final validation → one publication release
  → replace → local completion inspection → classified result + cleanup/join
```

## Existing seams and required additions

| Boundary | Existing source | Proposed addition; not implemented |
| --- | --- | --- |
| Approved read publication | [read authority](../../internal/harness/runtime/read_authority.go) | Create an edit receipt only when an eligible, complete approved read is committed for disclosure. Existing private capture bytes are consumed/cleared; the capture handle is not a reusable editing receipt. |
| Preparation and once authorization | [read admission](../../internal/harness/runtime/read_admission.go), [tool runtime](../../internal/harness/runtime/tool_loop.go) | Prepare an exact edit against host-owned read bytes and identities, bind its complete diff/result and destination, then claim one once receipt. |
| Scope, owners and operation lifecycle | [Linux Service](../../internal/harness/sandbox/service_linux.go) | Retain startup scope and target parent; extend the Service's short admission/release barriers for a distinct edit operation without enabling it. Coordinate owner transitions, read admission and all effect-capable operations. |
| Fixed worker and operation profile | [worker](../../internal/harness/readbrokerstudy/worker_linux.go), [profile](../../internal/harness/readbrokerstudy/profile_linux.go) | Add an editing-specific typed frame protocol/profile and descriptor-relative operations. Do not change creation's `RENAME_NOREPLACE` path or reuse its outcome parser for replacement. |
| Behavioral/native verification | [harness guide](README.md), [ADR proof obligations](../adr/0010-propose-approved-entry-replacement-for-file-editing.md#proof-obligations-before-a-mechanism-can-be-accepted) | Extend existing runtime/UI/provider seams and the native harness. No second executor or model-facing schema registration in the prototype. |

The current launcher recognizes read/list/search/create operations, not edit.
The creation worker's stage, release and wait sequence is relevant lifecycle
prior art, not proof of editing metadata, effect or inspection behavior.
Changing a rename flag in that worker would bypass the required design.

## Candidate comparison

| Candidate | Assessment for this investigation |
| --- | --- |
| Named stage + `renameat2(..., 0)` | Recommended candidate. Fits ADR-0010's directory-entry authority and keeps old file handles on the old object. It has no expected-object or expected-revision argument. |
| `RENAME_NOREPLACE` | Retain for creation; it refuses the existing destination an edit needs to replace. |
| `RENAME_EXCHANGE` | Not selected. It requires both names, but moves the old target into the staging name, can exchange different object types, and adds displaced-object cleanup. No automatic exchange-back/rollback is proposed. |
| `O_TMPFILE`, then link a stage and replace | Alternative if named-stage lifecycle cannot meet the cleanup rule. It reduces the named interval but still needs a named source for replacement; unprivileged linking and the worker's procfs/descriptor policy need separate analysis. Not a fallback selected at runtime. |
| In-place writes, advisory locks or leases | Not selected; they reintroduce original-object writes, partial target writes and separate writer/alias issues. Historical #320 findings remain unchanged. |

The API distinctions are documented in [rename(2)](https://man7.org/linux/man-pages/man2/rename.2.html)
and [open(2)](https://man7.org/linux/man-pages/man2/open.2.html).
The comparison is design analysis, not a newly measured result.

## Host authority and bounded retention

An edit receipt is an opaque, conversation-local host record. It holds a
retained object reference sufficient to prevent identity reuse during its
lifetime, mount/device/inode/type identity, exact complete approved-read bytes
and digest, original disclosure destination, and owner/policy/conversation
generations. A pathname or model-supplied digest cannot mint a receipt.

Proposed initial limits are 16 receipts, 1 MiB total base bytes and a five-minute
receipt lifetime per conversation. These are proposed bounded retention
choices for review, not existing editing constants. Eviction/expiry closes
retained resources and requires another approved read. A claimed receipt
cannot be evicted while its attempt owns cleanup; its deadline instead
cancels admission and drains the attempt. Apply the existing tool-loop and
result budgets without enlargement.

Do not mint receipts for truncated, failed, withheld, protected or stale read
results. Record metadata at read capture and revalidate it at preparation;
do not retrospectively claim newly observed metadata was approved in the
earlier read. If the capture lacks required identity/metadata, require a fresh
eligible approved read after that seam has been implemented. Mint no receipt
from a failed provider-publication transition. Reset, close, revocation,
destination change and relevant owner/policy changes invalidate affected
receipts. An edit attempt claims its receipt; it cannot authorize a second
independent edit. Successful replacement needs a new approved read before
another edit, since the object identity has changed.

Preparation freezes the retained startup root/parent, target leaf, base bytes,
metadata snapshot, exact result/diff, provider destination, scope/owner/policy
generations and unique attempt identifier. Require a complete supported UTF-8
regular file; base and result are each at most 64 KiB. Refuse NUL/binary input,
links and unsupported types. Preserve LF, CRLF and final-terminator state;
refuse mixed/unsupported newline forms rather than normalize. Exact patch
context only; no rebasing, force overwrite, append or multi-file operation.

## Scope, metadata and protected resources

Resolve existing parents from the retained startup root, with descriptor-
relative no-symlink/no-magic-link/no-mount-crossing rules. Bind host mount
identity as well as object identity; no `AT_FDCWD`, absolute operand or
slash-containing leaf reaches the replacement call. Retained descriptors do
not prove live-path containment. Revalidate the startup root and descendant
parent chain; detected root movement disables editing for the run. `/cd`
cannot retarget preparations, and reset does not recapture startup scope.
See [openat2(2)](https://man7.org/linux/man-pages/man2/openat2.2.html) and
[statx(2)](https://man7.org/linux/man-pages/man2/statx.2.html).

Require live instantiated-owner coverage before any target inspection,
staging, final release or completion inspection. Check protected names,
objects, aliases and absent reservations, including stage-name collisions;
do not read protected bytes to decide eligibility. Serialize in-process
owner transitions against admission/release, retain cleanup ownership while
workers drain, and suppress stale provider publication. Current read and
discovery rules remain independent. Do not infer uninstantiated owners from
missing registrations.

The first candidate eligibility subset is deliberately narrow:

- Original UID must equal the regular host UID. Stage UID/GID must match the
  original without privileged ownership changes; otherwise refuse.
- Original and stage must have one link and no special permission bits.
  Preserve ordinary mode bits exactly. Validate that the original is writable
  under the supported policy; directory write access alone must not silently
  authorize replacing a read-only target.
- Refuse ACLs and xattrs initially, including inherited default ACLs on the
  parent and unexpected attributes on the stage. Do not strip attributes to
  make an object eligible. Bound attribute-name enumeration to 64 KiB and at
  most two size/retry cycles; error, overflow or instability refuses.
- Examine filesystem-specific inode flags and supported `statx` attribute
  masks; refuse immutable, append-only, encrypted, verity and other unhandled
  semantic flags. Define an explicit measured allowlist for harmless flags;
  no assumption that mode/UID/GID is the full metadata set.
- Compare eligibility metadata before/after byte reads and again after
  approval. Detected changes require fresh read/proposal/approval. This is
  observation-based validation, not an atomic snapshot or revision lock.

**Unresolved metadata gate:** `flistxattr` may omit inaccessible names, and
`trusted.*` requires privilege to observe. Thus an empty unprivileged list
cannot establish that all metadata is absent. Default ACL inheritance and
security-label policy also affect newly created files. The Linux/ext4/Bubblewrap
label alone does not discharge this gate. A reviewed profile must establish
complete relevant metadata visibility/preservation, or refuse the affected
operation. No new privileged helper or assumption that hidden attributes are
irrelevant is selected here. Controlled synthetic fixtures can test the
mechanism but cannot establish general host eligibility.

These limitations follow [listxattr(2)](https://man7.org/linux/man-pages/man2/listxattr.2.html),
[xattr(7)](https://man7.org/linux/man-pages/man7/xattr.7.html),
[acl(5)](https://man7.org/linux/man-pages/man5/acl.5.html) and
[ioctl_iflags(2)](https://man7.org/linux/man-pages/man2/ioctl_iflags.2.html).
The fail-closed profile conclusion is this proposal's analysis.

## Stage, validate and release protocol

1. Claim one once approval in the host coordinator after complete-diff review.
   Acquire an operation-specific Service barrier; no broad write grant exists.
2. Start the pinned editing worker under an independently inspectable operation
   profile. Transfer only the retained eligible parent, fixed leaves, approved
   bytes and frozen authority metadata. No credentials, grant store, shell,
   provider access or arbitrary command. Audit metadata syscalls and narrowly
   constrained descriptor/control operations; existing profiles are unchanged.
3. Create a host-generated, unpredictable stage with descriptor-relative
   `O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC`, initial mode `0600` and restrictive
   worker umask. A collision refuses; never truncate an existing stage.
   Verify stage identity/UID/GID/mode/link count and inherited metadata before
   writing. Write at most 64 KiB, read back exact bytes and sync the stage.
   In the staged response, transfer an `O_PATH` stage descriptor to the
   Service as an identity/lifetime pin. The host derives and validates its
   identity/type itself; a worker-supplied inode number is insufficient.
   Retain this host-owned pin through worker join and classification to avoid
   inode reuse. The pin grants no provider disclosure or arbitrary reopen.
4. At `staged`, stop the worker at a fixed protocol checkpoint. Apply only the
   verified ordinary final mode and recheck stage metadata/bytes. Applying
   final permissions may expose the stage before replacement; minimize this
   interval and disclose it. Mode `0600` does not exclude another same-UID
   process. Do not claim stage isolation under the accepted threat boundary.
   A missing, unexpected or unverifiable descriptor/frame aborts admission;
   never treat it as proof that no staging side effect occurred.
5. Revalidate the approved target identity, complete base bytes, metadata,
   parent/root, protected owners and generations after approval and immediately
   before release. Detect changes before release: no replacement; fresh
   read/proposal/approval. Recheck the stage and the authority to inspect it.
6. A short synchronized Service transition either observes invalidation first
   and aborts, or records a unique publication release. Record release **before**
   sending its frame; a failed/ambiguous send is treated as possibly delivered.
   The worker accepts at most one release for that ordinal, validates frozen
   single-component operands, then performs one `renameat2(..., 0)` call.
7. Never blindly restart a mutation syscall on an interrupted/lost result.
   Return a bounded typed completion containing attempt/ordinal, phase,
   stage identity and syscall result. Join the worker before classification
   or another ordinal; a lost frame is not a negative acknowledgement.
8. Locally inspect under still-valid authority, classify the effect, then
   close resources and retire the barrier only after cleanup/join is complete.
   Suppress stale provider results. No rollback, exchange-back or automatic
   provider-continuation replay.

Stage sync errors occur before target replacement; cleanup can still be
incomplete. A directory sync after replacement is separately recorded. It
cannot turn a completed replacement into `not_executed`, and a failed sync
never triggers replacement retry. This proposal makes no durable-success
promise. File and directory synchronization differ; see
[fsync(2)](https://man7.org/linux/man-pages/man2/fsync.2.html).

### Late-disappearance review gate

Plain replacement can install the stage if an external actor removes the
target after the last check. A late symlink is replaced as an entry rather
than followed; a changed regular entry may be overwritten. These are concrete
consequences of a nonconditional directory-entry primitive, not detected-
conflict fallbacks. The approved diff is still the only proposed contents.

Review must explicitly confirm whether late disappearance is within
ADR-0010's accepted residual race. This proposal does not silently authorize
an editing invocation to create a missing destination. If existence must be
enforced at effect time, this candidate is insufficient; return for a separate
mechanism decision. Do not substitute exchange, unlink-then-rename or an
in-place write automatically. Pre-release missing targets always conflict.

## Completion inspection and bounded retry

Inspection is bounded to the prepared target and attempt-owned stage, each
at most 64 KiB plus an overflow sentinel and the metadata budgets above.
Apply the existing worker deadline to the entire coordinator attempt,
including inspection and any retry; no new unbounded wait or per-retry
deadline reset. No directory enumeration, general search or newly observed
content is sent to a provider. Refusal/inconclusive inspection leaves the
effect unknown and retry disabled. Authorization loss prevents new inspection.

| Execution evidence and local observation | Proposed classification/action |
| --- | --- |
| No release was ever recorded/sent; conforming worker joined; original base/metadata unchanged; stage safely retired or reusable | Replacement is known not executed. One internal retry may be eligible only for an explicitly classified transient pre-release preparation failure. |
| Trusted completion plus target confirmed as the retained stage object with exact approved bytes/metadata | Confirmed replacement; success, with cleanup/sync status reported separately. No retry. |
| Lost completion after release, but valid local inspection positively identifies the retained stage object at target with exact approved bytes/metadata and worker joined | Confirmed approved result observed; report success without claiming durability or proving an exhaustive external history. No retry. |
| Release recorded or possibly delivered, target now equals the old bytes, stage still exists, or syscall returned an error | None alone proves no replacement. Unknown unless stronger accepted completion evidence classifies it; no automatic retry in this first candidate. |
| Release recorded; target changed/missing/substituted, stage missing, worker not joined, inspection unavailable or evidence inconsistent | Unknown or a more precise supported non-success outcome; no retry, rollback or implicit new approval. |
| Detected conflict, cancellation, reset, revocation, owner/scope failure or unsupported metadata | Stop; no automatic retry. Fresh authority/read/proposal/approval as applicable. |

Even with positive completion evidence, stale-result suppression and
cleanup ownership still apply. An observed match is not permission to publish
newly read bytes, and `unknown` is not evidence that the effect did not happen.

The proposed first candidate deliberately retries only **before release**.
ADR-0010 permits retry only when positive no-effect evidence exists; it does
not require guessing after every interrupted syscall. No errno whitelist for
post-release retry is accepted here. Network filesystems remain excluded;
the documented NFS rename ambiguity is one reason not to generalize results
from local ext4.

One coordinator consumes one once receipt, with a host counter limiting it to
two pre-release preparation ordinals and at most one publication release in
total. An eligible retry must retire/join the first ordinal, inspect and
revalidate unchanged base/metadata/authority, then prepare a new stage under
the same frozen diff. No second coordinator dispatch, receipt consumption,
concurrent ordinal or new model request is allowed. Initially restrict the
retry allowlist to a demonstrated transient failure before publication
release, with no unresolved cleanup; if the study cannot prove that case,
disable automatic retry rather than broaden it.

The proposed first allowlisted case is synchronous worker-launch resource
failure (`EAGAIN`) **only when the launcher proves no child was created, no
parent capability was transferred and no stage exists**. A worker exit,
timeout, interrupted spawn or transport failure is not that evidence.
Local target/base inspection and authority revalidation must still succeed
before retry; inability to inspect refuses continuation. The prototype must
prove this launcher classification, rather than infer it from an error string.

## Cleanup and crash leftovers

On successful replacement the stage name is normally consumed. Close retained
handles only after completion inspection. If an external actor repopulates the
old stage name, it is not ours and must not be deleted.

On pre-effect refusal/cancellation, cleanup would need to unlink only the
verified attempt-owned stage. **This is a second unresolved mechanism gate:**
an identity check followed by pathname `unlinkat` is not atomic conditional
deletion. A post-check substitution can delete someone else's entry. This
proposal does not claim that existing creation cleanup proves ADR-0010's
never-delete-a-substituted-stage rule, and does not silently extend the
replacement residual-race acceptance to cleanup.

Before a real editing prototype, review a concrete way to satisfy that rule
or an explicit cleanup-contract disposition. Until then, no automated named-
stage unlink is accepted by this proposal. Retained or unverified stages must
be reported as cleanup incomplete, with affected admission blocked; do not
report `not_executed` as if staging had no side effects. An anonymous-stage
alternative can be assessed separately; it is not automatically authorized.

Crashes can leave stage contents on disk. Do not persist receipts or create
an edit journal to recover them. No startup replay, orphan scan, guessed
deletion or automatic retry after process restart. Operator cleanup must be
explicit and must not enter a provider result with private filesystem data.

## Evidence plan and next review

No new probe was run for this proposal. Primary API semantics and existing
source support the candidate shape; they do not establish a working sandbox
profile, metadata completeness or race-safe cleanup.

Before accepting a bounded prototype, review the three gates above and the
receipt/profile/phase protocol. The next authorized work should first resolve
those gates with a focused decision packet, not enable `apply_patch` editing.
Once resolved, use the existing native harness and disposable synthetic files
for these independently checkpointed cases:

| Case group | Required observable result |
| --- | --- |
| Baseline replacement and retained old handle | Exact new bytes at approved entry; old handle still references the old object; ordinary mode/newline policy preserved. |
| Approved-read authority and budgets | No receipt from truncated/withheld/failed disclosure; receipt limits/expiry/reset close resources; no model-forged receipt or digest. |
| Target changes before final observation | Fresh read/proposal/approval; no replacement even when patch context still matches. |
| Target/base/ABA changes after final observation | Record actual approved-entry effect and accepted race without claiming atomic revision comparison; no evidence-free retry. |
| Late target removal, symlink, hard link or directory | Measure each primitive outcome; do not follow symlinks or pretend existence/type was guaranteed at effect time. |
| UID/GID/mode, ACL/xattr/default ACL and inode flags | Refuse unsupported/unobservable metadata; detect changes; verify stage and result, including hidden-attribute visibility limitations. |
| Stage substitution/content changes and cleanup | Never delete substituted entries; truthful leftover/blocking outcomes; no claims based solely on check-then-unlink. |
| Startup/parent movement and nested mount | Enforce observed scope/terminal root rules; no scope recapture or cross-mount fallback. |
| Owner rotation/protected aliases and absent reservations | Correct admission/release ordering and non-disclosure; reads/discovery/creation retain their separate gates. |
| Pre-release failure and retry | One receipt consumer, at most one internal retry, no overlap; count actual replacements and join first ordinal before continuation. |
| Release/completion loss, kill, sync error and equal-byte external actor | Confirmed identity/result or unknown; no repeated mutation after possible release, no rollback/durability claim. |
| Reset/revocation/cancellation/continuation failure | Block affected future effects/inspection, join and retain cleanup, suppress stale publication, never replay completed effects. |

Record exact source, binary/runner hashes, kernel/filesystem/backend/profile,
case inventory, actor checkpoints and joined resources. Unavailable and
inconclusive cases stay so classified. Then add behavioral evidence through
the runtime/UI/provider integration boundary for full diff/destination,
conflict rereads, limits, inspection privacy and truthful outcomes. Run the
repository-required Go checks for implementation changes and obtain a
separate maintainer enablement review. This document changes no runtime,
schema, release, privileged setup or current capability registration.

Related: [#322](https://github.com/ayeshLK/komposyn/issues/322),
[#315](https://github.com/ayeshLK/komposyn/issues/315),
[#267](https://github.com/ayeshLK/komposyn/issues/267),
[historical editing design](issue-320-editing-design.md) and
[mutation deferral](mutation-deferral.md).
