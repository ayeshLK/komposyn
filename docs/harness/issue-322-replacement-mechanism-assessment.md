# Issue #322 replacement mechanism decision packet

**Recommendation: keep editing unavailable; no screened candidate advances to
prototype review on the ordinary Linux amd64/local-ext4/Bubblewrap profile.**
This is a scoped negative result, not a claim that every unprivileged editing
approach is impossible. No runtime, host setting or capability registration was
changed.

## Decision basis

Reviewed upstream `master` at
[`a80a8af297c7c5c978d6cb7a9ec98be1a35401de`](https://github.com/ayeshLK/komposyn/commit/a80a8af297c7c5c978d6cb7a9ec98be1a35401de),
which contains accepted [ADR-0010](https://github.com/ayeshLK/komposyn/blob/a80a8af297c7c5c978d6cb7a9ec98be1a35401de/docs/adr/0010-propose-approved-entry-replacement-for-file-editing.md)
and merged [PR #362](https://github.com/ayeshLK/komposyn/pull/362). Also reviewed
the [maintainer decision](https://github.com/ayeshLK/komposyn/issues/322#issuecomment-7162364764)
and [#320 closeout](https://github.com/ayeshLK/komposyn/blob/615e32c02874561acb2694b0ec6e7187b0e8a5ae/docs/harness/issue-320-closeout.md).
The local checkout was `2cfe8fdc10d550928b0929b4d8ed14ab99ee4f4e`; its
worktree already contained unrelated changes, which this packet leaves intact.
The upstream commit was obtained from GitHub's public commit API; local git
metadata is read-only, so no fetch ref was written.

ADR-0010 remains the behavioral contract: host-owned receipt from a complete
prior approved read; exact context, complete diff and exact target in once
approval; fixed startup scope; protected-owner checks; fresh read/proposal/
approval after detected conflict; truthful lifecycle outcomes; and the
accepted residual external race after final observation. It does not permit
pretending a missing target was replaced, replaying an uncertain effect, or
silently deleting a substituted stage. The editing proposal's fixed worker
would sit behind existing `sandbox.Service` admission/release barriers, with
runtime-owned receipt and approval, a typed worker operation, and UI-bound
diff. Those seams carry authority but do not repair the mechanism gaps below.

## Metadata gate

No concrete unprivileged profile was found that lets the worker establish
eligibility for real project files while preserving relevant metadata. An
empty `flistxattr` result is not proof of no xattrs: Linux documents that
inaccessible attribute names may be omitted, and ext4's `trusted.*` listing
requires `CAP_SYS_ADMIN`. The handler is registered independently of the
ordinary user/security handlers. This behavior is visible in the pinned
[Linux v7.0 ext4 trusted-xattr handler](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/xattr_trusted.c)
and [Linux `listxattr(2)` documentation](https://man7.org/linux/man-pages/man2/listxattr.2.html).
The ext4 superblock xattr feature bit cannot establish per-file xattr absence
or visibility; it may be set when the filesystem is formatted ([e2fsprogs
`mke2fs.conf`, v1.47.3](https://github.com/tytso/e2fsprogs/blob/v1.47.3/misc/mke2fs.conf.in#L1-L3), [pinned ext4
implementation](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/fs/ext4/xattr.c#L842-L861)).

For any future profile, checks must be bound to the retained target and repeated
at read capture, preparation, after approval, immediately before release, and
during authorized result inspection. UID/GID, ordinary mode, special bits,
link count/type, ACLs including inherited parent default ACLs, xattrs/security
labels, and filesystem flags/masks all need explicit preserve-or-refuse
semantics. Stat/statx, ACL and flag queries can check visible values, but an
unknown/unsupported query, changed value, enumeration overflow or instability
must refuse. Stage creation also has to account for inherited ACLs and security
labels; copying mode bits is not sufficient. On ordinary ext4, hidden
`trusted.*` means that even successful empty user-visible enumeration cannot
meet the contract. Ownership and a clean synthetic fixture do not change this.

A narrower filesystem profile would need an externally verifiable guarantee
that relevant metadata cannot exist or is completely observable and
preservable, plus trusted composition that verifies the actual target mount,
filesystem and kernel prerequisites on every admission. No such profile was
identified that covers normal project files. A synthetic tmpfs or specially
prepared fixture would test mechanics only, not establish eligibility for
real project files.

## Candidate comparison

| Candidate | Metadata eligibility | Destination exists at publication? | Namespace effects | Cleanup and outcome classification |
|---|---|---|---|---|
| `renameat2(..., 0)` (named same-directory stage) | Fails on ordinary ext4 for hidden/unhandled metadata; stage inheritance is also unproven. | **No.** A target removed after final observation is replaced by the stage, creating a name. Linux documents zero flags as ordinary rename; the pinned VFS follows its create path when destination lookup is absent ([`renameat2(2)`](https://man7.org/linux/man-pages/man2/rename.2.html), [Linux v6.12 `namei.c`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/fs/namei.c#L4908-L4926)). | Replaces the directory entry with the stage object; old open handles/hard links still refer to old object. Cannot satisfy required existing-at-effect condition under the accepted race. | Stage cleanup by name has no expected-inode operand; check-then-`unlinkat` can delete a substituted entry. Lost/released outcome is unknown absent positive evidence. Never offer creation or replay on uncertainty. **Set aside.** |
| `renameat2(..., RENAME_EXCHANGE)` | Same metadata failure. | **Yes**, both names must exist per Linux [`renameat2(2)`](https://man7.org/linux/man-pages/man2/rename.2.html). | Atomically swaps both entries; types may differ. A late directory, symlink or other entry, including a protected entry, can move to the stage name. The syscall has no expected-inode operands. Any exchange proposal must check protection for both operands and account for the displaced entry's disposition; the attempt-stage cleanup rule does not cover it ([Linux v6.12 locked VFS lookup](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/fs/namei.c#L5104-L5138), [ext4 exchange](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/fs/ext4/namei.c#L4115-L4153)). | Cleanup would need to distinguish the displaced target from the attempt-owned stage under races; no approved disposition exists for moving/deleting the displaced entry. `ENOENT` can refer to either operand and by itself does not establish destination-specific absence. Only a joined worker plus reliable no-publication evidence and local confirmation of the missing destination may offer separate creation review or cancel. Review creation uses existing complete-content once approval and no-replace publication; if the destination reappears, the creation attempt must fail on collision without overwriting it. Unknown means neither offer nor replay. **Blocked.** |
| `O_TMPFILE` + link then replacement | Same target metadata failure; anonymous inode does not prove metadata completeness. | Final replacement still has the plain-rename vacancy race; linking itself cannot overwrite an existing destination ([Linux `link(2)`](https://man7.org/linux/man-pages/man2/link.2.html)). | Needs a named stage for replacement; it therefore does not avoid the publication gap, and relabels the cleanup stage rather than eliminating cleanup after linking. | Anonymous pre-link cleanup is descriptor-based, but after linking named-stage substitution concerns return. Link failure / lost worker requires phase evidence; uncertain publication has no creation prompt or replay. **Does not resolve gates.** |
| In-place write / advisory lock or lease | Does not establish metadata preservation or protect aliases; may change bytes before failure. | Not an entry-replacement primitive; still acts on an object that can be concurrently changed. | Reintroduces partial writes and the exact-object/revision-at-effect assumptions already screened in #320. | Partial/interrupted write is not safely classifiable as no effect; no replay on uncertainty. **Do not revive under this contract.** |

The syscall documentation supports API-level flag and type semantics. The
source links show how Linux v6.12 VFS/ext4 implement the relevant paths; these
are source-based inferences, not native tests of the current host, Bubblewrap
composition, or filesystem. #320 remains a scoped negative result for its
examined in-place/lease candidates and stricter exact-object contract; it does
not prove the alternatives here impossible.

## Smallest next review

Keep #322 open. Another mechanism review is justified only after a proposal
names both (1) a real-project-filesystem profile that unprivileged trusted
composition can verify to exclude or preserve all relevant metadata, including
hidden attributes and inherited labels/ACLs, and (2) an existing-destination
publication primitive whose full namespace effects and displaced-entry
handling are acceptable, with safe attempt-stage cleanup or an explicit
contract disposition. It must also explain how a missing-destination failure
is distinguished from a missing source stage before offering separate creation
review. Do not treat native evidence for one gate as resolving the others.

If a candidate passes that design review, prototype verification should use
the existing Service/runtime/UI seams and fixed worker with disposable files
and independently checkpointed same-user actors. Record exact observables:

- target bytes, metadata, parent, startup root, owner coverage or policy changed
  and detected by final observation: no publication and fresh approved
  read/proposal/approval;
- in-process invalidation racing publication release: the Service barrier
  serializes it so invalidation observed first aborts publication, while a
  release recorded first proceeds under the accepted residual race;
- external target removal after final observation: publication must refuse
  because the destination is absent at effect time. If the worker is joined,
  non-publication is positively established, and the target is confirmed
  missing, offer only local review of separate creation or cancel;
- external target replacement after final observation: record the actual
  entry/object and report the accepted residual race without claiming
  revision-at-effect locking. A late replacement can be affected under that
  accepted race, so preserve/account for its namespace and owner consequences;
- if the target is confirmed missing and non-publication is positively
  established, offer only local review of separate creation or cancel.
  Review creation uses the existing complete-content once approval and
  no-replace publication; if the name reappears before creation publishes,
  report the collision and do not overwrite it;
- target replaced with regular file, symlink, directory, hard link, or
  protected owner: exact documented namespace outcome, no unintended
  following, protection checks on all affected entries, preservation of
  displaced entries, and fresh read/approval on detected conflict;
- hidden xattr, ACL/default ACL, security label, UID/GID, special bit, link
  count, inode flag, unknown mask, stage inheritance or unstable enumeration:
  refuse with no metadata stripped or silently lost;
- stage-name substitution during refusal/cancellation cleanup: substituted
  entry remains untouched; leftover is reported locally, and affected edit
  admission remains blocked;
- worker killed, response lost, sync error or cancellation around release:
  joined lifecycle and exact `not_executed`/failure/unknown classification;
  uncertain effect gets no creation offer or replay;
- revocation, reset or loss of authorization: end inspection and retry; drain
  work and report any unresolved outcome without replay;
- provider continuation failure: never replay an effect that may have
  completed;
- any permitted retry requires positively established no-publication,
  continued authority, fresh target/metadata/scope/owner revalidation, safe
  stage handling, and at most one internal continuation under the same
  consumed receipt; it is not a second dispatch;
- successful replacement: confirm the attempt-owned replacement object's
  identity at the target and its exact approved bytes, with truthful cleanup
  and sync status; old open handle remains on old object. Make no durability
  or rollback claim.

Until then there is no candidate to prototype. This packet is source-only and
does not authorize a prototype, native acceptance, or editing enablement.

## Primary references and revisions

- Linux man-pages [`rename(2)`](https://man7.org/linux/man-pages/man2/rename.2.html), [`listxattr(2)`](https://man7.org/linux/man-pages/man2/listxattr.2.html), [`acl(5)`](https://man7.org/linux/man-pages/man5/acl.5.html), [`xattr(7)`](https://man7.org/linux/man-pages/man7/xattr.7.html), [inode flags](https://man7.org/linux/man-pages/man2/ioctl_iflags.2.html), and [`link(2)`](https://man7.org/linux/man-pages/man2/link.2.html), current pages identified as man-pages 6.19 when checked on 2026-11-25. The merged proposal also cited the archived man-pages 6.11 manual; these live links are not asserted to be that revision.
- Linux source commit [`adc218676eef25575469234709c2d87185ca223a`](https://github.com/torvalds/linux/commit/adc218676eef25575469234709c2d87185ca223a) (v6.12 tree paths for VFS rename/unlink, ext4 rename/xattr handling); the ext4 trusted handler was additionally checked at v7.0 commit [`028ef9c96e96197026887c0f092424679298aae8`](https://github.com/torvalds/linux/commit/028ef9c96e96197026887c0f092424679298aae8), as cited in the maintainer decision.
- [ADR-0010 and merged mechanism proposal](https://github.com/ayeshLK/komposyn/commit/a80a8af297c7c5c978d6cb7a9ec98be1a35401de); [maintainer decision](https://github.com/ayeshLK/komposyn/issues/322#issuecomment-7162364764); [#320 closeout](https://github.com/ayeshLK/komposyn/blob/615e32c02874561acb2694b0ec6e7187b0e8a5ae/docs/harness/issue-320-closeout.md).
