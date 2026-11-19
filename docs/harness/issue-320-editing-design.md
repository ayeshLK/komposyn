# Initial design investigation: issue #320 editing

Status: **design only; no editing prototype or model-facing capability**. The
existing-file edit gate remains unsatisfied. This proposal is intentionally
separate from the accepted negative closeout for creation in
[`issue-319-closeout.md`](issue-319-closeout.md). Issue #319's closure and its
creation counterexamples do not establish editing safety.

## Required contract

An edit may target exactly one existing UTF-8 regular file. The original and
result are each at most 64 KiB. A successful proposal must preserve the exact
existing line-ending convention and permission state. It must refuse binary or
unsupported encodings, symlinks, hard-linked targets, directories, multiple
files, renames, deletion and out-of-scope targets. Matching is exact; there is
no fuzzy search or automatic rebase.

The model's proposed text, path, digest, inode number and claims are untrusted
input. The authority must derive from a prior approved read in the same
conversation and host-owned data. A future host-side read receipt would need to
bind at least:

- the opaque conversation/read-capture identity and one-time lifecycle;
- host-observed mounted-object identity, including mount identity and the
  device/inode/type data used by the retained Linux service;
- a host-computed digest/revision of the exact bytes returned by that approved
  read, with an explicit content length and UTF-8 validation;
- the read's target, read authorization, provider destination, policy/owner
  generations and expiry.

The current runtime has short-lived in-memory `PrivateReadCapture` records with
the bytes, `dev:ino:type` identity, destination and authorization metadata.
They are consumed by the provider publication path and retired under bounded
lifecycle rules. They do not expose a stable host-recorded content-revision
receipt for a later edit; the identity carried into the current capture does
not include mount ID, and no edit capability consumes it. A design would need
to add a separately bounded in-memory receipt or equivalent host-owned record,
not persist it and not accept a model-supplied digest. A digest helps compare
the proposed base to the approved read; by itself it does not close a race at
the effect point.

The exact once-approval request must bind the complete target and resulting
bytes/diff, the prior read receipt and revision, the user-selected mutation
scope and its generation, host policy and protected-owner coverage generations,
the selected provider destination, the current conversation, and a unique
effect attempt. The UI must display the exact target and complete diff before
the explicit approval. Changing any bound field invalidates the approval.
Directory selection and working-directory navigation grant no mutation
authority.

The host must retain cleanup ownership until all effect-capable workers and
descriptors are joined or retired. Scope, policy, target-owner or protected
inventory transitions invalidate preparations and once grants, cancel affected
work, join it, and block new admission while cleanup is incomplete. Provider
source files remain read-only. Credentials/auth and settings remain protected
through aliases and rotation; owned Token Logger databases, journals, WAL/SHM
files and backups remain protected where that store is composed. Missing owner
coverage makes the operation unavailable.

Refusal before effect is `not_executed`. A known prefix write is `partial`. If
the worker may have written but completion or cleanup cannot establish the
result, report `unknown`. Cancellation after effect release does not promise
rollback. Never automatically retry a partial or unknown edit, and never
publish stale results after revocation, target/scope/policy changes or
conversation closure. No outcome string can turn an unproven effect boundary
into an enforced one.

This preserves #267's mounted-object/exact-object contract: ordinary external
renames do not themselves revoke authority, but any effect must still be on the
exact object whose identity and read revision were approved. Replacing a path
entry with a newly created inode would change that contract and requires a
separate maintainer decision.

## Existing lease evidence

The existing `F_SETLEASE(F_WRLCK)` study is kernel-mechanism evidence, not
runtime integration or editing acceptance. Its recorded source is
`e35ed70145dbab72974b4cd1c4ee3c7f95d57442`; the study document reports Linux
amd64, kernel `7.0.0-42-generic`, Go 1.26.11, `/tmp` on ext4, and a Bubblewrap
trust refusal that left the sandbox suite unavailable. The exact JSON cited
by the study (`18a8e264...`) is not present in the current `/tmp` capture set.
An additional report is available at `/tmp/komposyn-issue-319-report.bsyNOM.json`
(SHA-256 `56cde0cfffd4e6aa3ee62622e1812c144709218fd4e224af02b8b5a1ed8e6599`).
It records source `8fdd9435870e6b90e4cb83c7169878bb0e3a2bb8`, the same kernel,
filesystem and eight lease observations described below. The `lease_linux.go`
source is byte-identical at `8fdd943...` and the documented `e35ed701...`
revision (SHA-256 `e425fdb85671240c446289b77fb2d362cec91ef3485591081a9321fe0bb00350`);
the relevant diff in `cmd/sandbox-study/main_linux.go` is report metadata.
Treat the available JSON as a corroborating capture from its own revision,
not as the `e35ed701...` report or as a replacement for its documented hash.
The checked-in study and source are also available.

| Probe | What the study measured | What it does not prove |
| --- | --- | --- |
| Existing writable FD | With an independent `O_RDWR` FD held, write-lease acquisition returned `EAGAIN`; after closure, acquisition succeeded. | It does not reserve the target across the whole user conversation, establish the approved revision, or prove complete coverage of every writer path. |
| Retained `MAP_SHARED` writable mapping | On the tested ext4 setup, write-lease acquisition was refused while the independent actor retained the mapping after closing its FD. The study then changed the synthetic bytes through the unleased mapping. | The test did not edit under a lease with this mapping; it proves neither behavior on other filesystems nor coverage of all mapping/write paths. |
| Late hard link under write lease | A same-user actor created a hard-link alias while `F_WRLCK` remained active. A later nonblocking open through the alias got `EWOULDBLOCK` and moved the lease into break state. | The lease does not reject alias creation. It does not meet the explicit no-hard-linked-target rule or prove protected-owner alias coverage. |
| Lease break | The study exercised the tested nonblocking alias open and observed lease-break state. | Forced-break timeout, suspended/dead holder, exact timeout cleanup and an actual edit during/after break were not measured. Linux documents that the kernel can forcibly remove/downgrade a lease after `/proc/sys/fs/lease-break-time`; no workflow here proves safe effect completion across that transition. |
| In-place outcome | The study marked partial/unknown effect injection `not_executed` and prohibited replay in its contract. | No edit bytes were written by the lease study; no atomicity, rollback or truthful production outcome path was established. |

The file lease is not a revision lock. It may help detect/block some conflicting
opens and truncations, but is not a compare-and-write transaction. The native
study directly measured that the targeted late hard link itself completed
under a held lease. See the primary Linux [`F_SETLEASE` manual](https://man7.org/linux/man-pages/man2/F_SETLEASE.2const.html),
[`mmap(2)`](https://man7.org/linux/man-pages/man2/mmap.2.html) and
[`link(2)`](https://man7.org/linux/man-pages/man2/link.2.html). These API
semantics do not add atomic revision binding to user-space file writes.

## Candidate assessment

### Cross-cutting startup and approval-chain prerequisite

No candidate below solves the trusted startup and approval-input problem
identified by #337. Its native Gate A probe observed a same-UID actor opening
an inherited output pipe through host procfs before the target disabled
dumpability, retaining that descriptor, and writing through it afterward.
That is the measured pipe-descriptor result. The claim that Yama's ATTACH-class
policy does not cover the READ-class proc-fd open, and the related `execve`
dumpability discussion, are documented-semantics deductions. The report did
not measure terminal/UI spoofing, nor did it establish mode-2/3 acceptance.
The capture host was ineligible for that profile.

This finding concerns the trustworthy delivery and display of the approval
request and result; choosing an edit primitive does not repair it. A future
mechanism would still need to bind the exact diff, target, read receipt,
conversation, destination and effect attempt to host-owned preparation data,
and ensure the UI receives that data over an integrity-protected channel before
the once approval. The worker must not be able to alter the approval presented
to the user or substitute its release input.

Applied to each candidate:

- **In-place write:** a retained target FD and lease do not protect the
  approval display channel or its release input. Existing read/approval UI
  startup assumptions remain unresolved, independently of the content race.
- **Replacement rename:** retaining a parent FD does not establish trusted
  approval presentation or release. The candidate also remains rejected for
  changing the mounted object, as described below.
- **Kernel/filesystem conditional edit:** even if a concrete primitive could
  bind and apply a revision atomically, it would not authenticate the user's
  approval or protect the request/result channel on its own.

No editing-specific process-hardening or launch profile is accepted by this
design. Any future proposal must separately review whether it needs Yama scope
2/3, dumpability transitions, inherited-descriptor closure/retention rules,
procfs restrictions, ptrace/pidfd/proc-memory controls, terminal/UI integrity,
or other process isolation. The #337 capture does not prove those controls
sufficient, available, or correctly ordered for editing. Do not treat them as
current prerequisites satisfied by the recorded study; record and measure a
concrete profile before relying on one.

| Candidate | Effect and assumptions | Exact identity/revision and actor analysis | Assessment / decisive rejection |
| --- | --- | --- | --- |
| In-place write through a retained FD, optionally guarded by `F_SETLEASE` | Open and retain the exact regular-file object; validate UTF-8, permissions, bytes and context; write/truncate that same inode. A write lease requires filesystem support and suitable ownership/privilege. | A retained descriptor can continue to name the same object after a rename, which fits mounted-object authority. A host-recorded digest check followed by `pwrite` is still a check/write race unless a trusted mechanism excludes every concurrent writer atomically. Existing writable FD/mapping behavior can refuse lease acquisition; late hard-link creation succeeded while the lease was held. Root/parent moves and owner rotation were not tested for the edit operation. | **Not a viable supported candidate on current evidence.** The existing lease evidence does not satisfy hard-link refusal or revision-bound effects and leaves lease-break, unsupported-filesystem and partial/unknown handling unresolved. Minimal rejection control: after acquiring the lease on a disposable file, have an independent actor attempt late `linkat`; assert whether the target now has a second name before any write. The accepted study already observed that alias creation succeeds. A follow-up would also checkpoint a content update between read capture and lease acquisition, then race a second writer at the exact validation/write boundary. |
| Write a replacement file and atomically rename it over the approved pathname | Write a complete temporary inode, then atomically replace the directory entry. Requires a supported same-filesystem rename and explicit decisions for mode/ACL/xattr preservation and durability. | The result inode is different. Existing open descriptors and other hard links continue to refer to the old object; rename changes which inode the path names. This authorizes a directory entry at effect time, not the mounted object whose content was read. A retained parent FD narrows path lookup but does not make the replacement the original object. | **Rejected under #267's accepted contract.** Linux `rename(2)` documents atomic replacement of the name and that other hard links/open descriptors to the old file are unaffected. Minimal counterexample: hard-link the approved inode to an independent alias, perform replacement at the approved name, then compare object identities and bytes through both names. The authorized mounted object was not edited. Adopting directory-entry-at-effect authority would be a separate explicit contract decision, not an implementation detail. |
| New trusted kernel/filesystem-mediated conditional edit | Would need an effect boundary that atomically verifies the approved mounted object and expected content revision and applies the bounded bytes as one authorized operation. A Linux LSM/BPF hook or dedicated filesystem component is only a broad research category here; no specific hook/API is selected. | It must account for existing writable FDs/mappings, namespace replacement, late aliases, exact revision, owner/policy transitions and one-shot receipt at the actual write point, while returning truthful partial/unknown status and retaining cleanup ownership. Standard user-space prechecks and postchecks cannot supply this guarantee. | **Requires a new trust/deployment decision and a concrete design before a prototype.** #267 notes that its BPF LSM candidate requires privileged system-wide loading, hook/object validation, token correlation and filesystem-specific native evidence; this assignment authorizes none of those. Do not assume a hook provides atomic compare-and-write without a reviewed design and measurement. |

All examined paths either violate exact-object/revision semantics or require a
new kernel/filesystem/deployment trust boundary. There is no candidate to
prototype within this assignment. This is a bounded negative design result,
not a claim that no future mechanism can satisfy the contract.

## Required scenarios before any later prototype

No prototype plan is approved. If a new candidate is separately accepted, its
first decision packet must name a concrete effect primitive and cover at least
these independently checkpointed same-user counterexamples before model
integration:

1. Read and approve revision R; mutate the object in place before preparation,
   after validation, and immediately before the effect. No stale patch may
   apply to revision R+1.
2. Replace/move the pathname and parent while retaining the original object;
   distinguish exact-object authority from directory-entry authority.
3. Create a symlink or hard link before preparation and during the protected
   interval. Unsupported aliases must be refused without leaking protected
   state.
4. Retain writable FDs and mappings across read, lease acquisition, effect
   release, lease break, cancellation and owner rotation.
5. Rotate auth/settings/provider/Token Logger owners and protected aliases
   around preparation, approval, effect and publication; incomplete inventory
   must refuse before approval/release.
6. Cancel or revoke before effect release and after release; kill/suspend the
   worker, delay join, lose completion, inject a short write, and verify no
   automatic replay or stale publication.
7. Verify exact diff review and provider destination binding through the
   existing runtime-to-UI/provider boundary, with preparation/execution using
   the real composed host service and no model-controlled identity/digest.

The smallest immediate follow-up is a maintainer decision on whether to retain
#267's mounted-object semantics and whether a new privileged kernel/filesystem
or deployment boundary may even be researched. If no such boundary is
authorized, keep #320 open with editing unavailable and do not build an
executor, UI, or apply-patch prototype. If approved, first write a
mechanism-specific design and native rejection protocol; only a later
authorization may start implementation.

## Current tracker state

Issue #320 is open and has no issue comments. It is listed as the blocker for
#322. #267 and umbrella #315 remain open. No tracker state was modified.

## References

- [Issue #320](https://github.com/ayeshLK/komposyn/issues/320)
- [Edit enablement gate #322](https://github.com/ayeshLK/komposyn/issues/322)
- [Mutation contract #267](https://github.com/ayeshLK/komposyn/issues/267)
- [Parent issue #315](https://github.com/ayeshLK/komposyn/issues/315)
- [File-lease study](file-lease-revision-binding-study.md)
- [Runtime read admission and capture](../../internal/harness/runtime/read_admission.go)
- [Host read identity and admission metadata](../../internal/harness/readbrokerstudy/contracts.go)
- [Linux sandbox read execution](../../internal/harness/sandbox/service_linux.go)
- [Linux file-lease study implementation](../../internal/harness/sandboxstudy/lease_linux.go)
- [Proposed ADR-0007](../adr/0007-proposed-exact-revision-bound-file-editing-contract.md)
