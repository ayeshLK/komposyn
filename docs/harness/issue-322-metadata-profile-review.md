# Issue #322 ext4 metadata profile review

`FS_IOC_FIEMAP` with `FIEMAP_FLAG_XATTR` is a plausible unprivileged
veto for stored extended attributes on a retained regular-file descriptor
on local ext4. A maintainer-run synthetic fixture confirmed a positive
count for a `trusted.*` attribute that was absent from the unprivileged
listing. A complete metadata profile remains unverified. Keep editing
unavailable under #322 while storage coverage, stage metadata and trusted
composition are reviewed. This assessment follows the merge of PR #363.
[Issue #322](https://github.com/ayeshLK/komposyn/issues/322) ·
[Merged assessment](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/docs/harness/issue-322-replacement-mechanism-assessment.md) ·
[ADR-0010](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/docs/adr/0010-propose-approved-entry-replacement-for-file-editing.md)

## What the pinned kernel reports

- The VFS resolves the ioctl against the supplied descriptor, runs the
  security hook, and dispatches to the file inode's FIEMAP operation. Ext4
  regular files register `ext4_fiemap`, which consumes the xattr flag before
  running the xattr mapping path. This path has no capability gate of its own.
  The separate ext4 `trusted.*` listing handler requires `CAP_SYS_ADMIN`.
  [VFS ioctl](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ioctl.c#L583-L599) ·
  [Regular-file operations](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/file.c#L981-L990) ·
  [Ext4 FIEMAP](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/extents.c#L5186-L5218) ·
  [Trusted handler](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/xattr_trusted.c#L16-L39)
- The mapping reports inode-body EA storage when `EXT4_STATE_XATTR` is set,
  or an external EA block when `i_file_acl` is nonzero. EA values stored in
  separate inodes still have a referencing entry in one of those places.
  The lookup considers storage without screening namespaces or values. Both
  stores may coexist, and the mapping can report just one. That is enough
  for a presence veto, including for ACLs, labels and hidden `trusted.*`
  entries stored in ext4. Files using inline data can also be vetoed because
  ext4 stores inline data using an internal EA entry.
  [Storage mapping](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/extents.c#L5111-L5167) ·
  [Inode EA state](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/inode.c#L5028-L5052) ·
  [EA-inode value references](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/xattr.c#L613-L626) ·
  [Inline data](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/inline.c#L267-L315)
- Use a zero-initialized FIEMAP header with start `0`, length `UINT64_MAX`,
  the xattr flag alone, and extent capacity `0`. The kernel counts mappings
  without copying extent addresses. Ext4 bounds the range independently of
  file length, so this includes empty files. The iomap path translates
  lookup `ENOENT` to successful completion. For a valid ext4 inode and this
  range, the pinned path starts at the beginning of the xattr mapping, and
  the inode-table lookup returns other errors on failure. Accept success
  with the expected response and a zero count only after verifying the
  profile. Any positive count, error, unsupported request or unexpected
  response vetoes eligibility. The result is a local boolean and needs no
  names, values or physical addresses.
  [Count-only and response handling](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ioctl.c#L111-L121) ·
  [VFS FIEMAP header](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ioctl.c#L199-L228) ·
  [Ext4 range handling](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/extents.c#L5153-L5183) ·
  [Iomap result handling](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/iomap/fiemap.c#L58-L88) ·
  [Inode location errors](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/inode.c#L4765-L4804)

## What the profile must establish

- Bind the retained, readable regular-file descriptor to an identified local
  ext4 mount and a reviewed kernel build. The `fstatfs` magic is shared by
  ext2, ext3 and ext4. Kernel version text alone does not verify vendor
  behavior. Check at read capture, preparation, after approval, immediately
  before release and during authorized outcome inspection as applicable.
  Normal VFS xattr updates take the exclusive inode lock. Ext4 FIEMAP takes
  its shared form, while ext4 separately documents `xattr_sem` as protecting
  `i_file_acl`. The behavior during internal EA relocation still needs review.
  The retained descriptor binds observations to the inode. ADR-0010 still
  accepts external changes after final observation.
  [Filesystem magic](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/include/uapi/linux/magic.h#L24-L27) ·
  [VFS xattr update](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/xattr.c#L304-L334) ·
  [Ext4 lock rules](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/xattr.c#L45-L53) ·
  [ADR-0010](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/docs/adr/0010-propose-approved-entry-replacement-for-file-editing.md)
- Establish separate preserve-or-refuse checks for type, link count, UID/GID,
  mode and special bits, flags and masks, project ID, and effective security
  labeling. For the verified parent, account for default ACLs, group and flag
  inheritance and label policy. Compare the actual staged inode against the
  approved baseline before release, including the EA veto. Ext4 initializes
  ACLs and security xattrs on creation through separate hooks. A regular-file
  FIEMAP check provides no evidence about the parent directory or security
  policy supplied outside stored EAs.
  [Ext4 ACL initialization](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/acl.c#L279-L304) ·
  [Ext4 security initialization](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/xattr_security.c#L37-L61) ·
  [Ext4 flags and project ID](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/ioctl.c#L989-L1001)
- Keep input construction in fixed trusted code, such as the host-side
  metadata observation on the retained descriptor. The merged worker filter
  has no edit operation. Seccomp can examine syscall argument words, such as
  the ioctl command, but cannot dereference the FIEMAP header to enforce the
  xattr flag or count-only form. A deployment denial from an LSM or outer
  sandbox must fail closed.
  [Merged worker filter](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/internal/harness/readbrokerstudy/profile_linux.go#L139-L179) ·
  [Seccomp filter reference](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/Documentation/userspace-api/seccomp_filter.rst#L20-L30)

## Evidence and disposition

An unprivileged smoke observation on Linux `7.0.0-44-generic` x86-64, UID
1000, and a disposable local ext4 `/tmp` directory returned zero for clean
regular files of 0, 1 and 65,536 bytes. Each returned one after adding a
one-byte `user.komposyn` value, one after replacing it with 2,500 bytes, and
zero after removal. The fixture was removed. The observation checked neither
physical placement nor `trusted.*`, ACLs, security labels or the shipped
worker. The upstream source review was pinned to Linux v7.0 commit
[`028ef9c96e96197026887c0f092424679298aae8`](https://github.com/torvalds/linux/commit/028ef9c96e96197026887c0f092424679298aae8).

### Follow-up observations

On the same kernel and local ext4 mount, an unprivileged Python probe used the
count-only header described above. It created disposable fixtures under
`/tmp` and joined the independent metadata actor before removing them. The
probe used `setfacl -m "d:u:$(id -u):r--"` and `getfacl -cn` to establish the
parent default ACL. It used exclusive creation with mode `0600` for both
stage fixtures.

| Case | Observed result |
| --- | --- |
| Stage under a plain parent | Mode `0600`, no access ACL, count zero. |
| Stage under the default-ACL parent | Mode `0600`, inherited `system.posix_acl_access`, count one. |
| Independent process adds, grows, shrinks and removes a `user.*` xattr on the opened regular file | Count zero at baseline, then one, one, one and zero at the respective checkpoints. |
| Independent process performs 4,000 xattr replacements alternating between 1 and 2,500 bytes while the observer queries the held descriptor | All 2,261 observations returned one. The attribute remained present, and its physical placement was not measured. |
| Clean ext4 directory and ext4 directory with a default ACL | Both calls succeeded, returning zero and one respectively. A zero result does not establish regular-file type. |
| Regular-file `O_PATH` descriptor, unsupported FIEMAP flag, and pipe descriptor | Returned `EBADF`, `EBADR`, and `EOPNOTSUPP` respectively. The probe refused eligibility in each case. |

These observations support checking type independently and checking the actual
stage for inherited metadata even when its ordinary mode bits match. The
concurrent run sampled one workload and does not establish serialization of
all ext4-internal transitions. The error cases exercise the probe's refusal,
not the behavior of a KompoSyn worker or an active LSM denial.

### Hidden trusted-attribute fixture

The maintainer supplied the following output from the disposable fixture
script. Its elevated preparer verified the attribute set on both files,
then the unprivileged observer checked the same inodes.

```text
observer uid=1000 kernel=7.0.0-44-generic mount=33 fstype=ext4 mount_options=rw,relatime super_options=rw
preparer verified clean: inode=4456579, xattrs=[]
preparer verified attributed: inode=4456581, xattrs=['trusted.komposyn_probe']
observer clean: inode=4456579 visible_xattrs=0 fiemap_count=0
observer attributed: inode=4456581 visible_xattrs=0 fiemap_count=1
PASS: hidden trusted attribute detected; disposable fixtures removed
```

This confirms that the unprivileged count-only probe detects stored xattrs
for this hidden-attribute fixture. The run did not characterize physical
storage placement or exercise the intended KompoSyn worker composition.

### Ext4 storage-layout observations

A separate unprivileged probe used disposable regular files on the local
ext4 `/tmp` mount, Linux `7.0.0-44-generic` x86-64, UID 1000 and a
4,096-byte filesystem block size. It used ordinary `user.*` attributes and
the count-only query. A separate one-slot diagnostic query classified the
reported mapping using `FIEMAP_EXTENT_DATA_INLINE`. The probe printed no
physical addresses and did not access the block device.

| Fixture state | Count-only result | Diagnostic observation |
| --- | --- | --- |
| Clean control | 0 | No mapping. |
| One 1-byte attribute | 1 | Inode-body mapping. |
| The same small attribute plus a separate 2,500-byte attribute | 1 | Both names were present. FIEMAP still reported the inode-body mapping. |
| Only the 2,500-byte attribute, after removing the small one | 1 | External-block mapping. The remaining value was still 2,500 bytes. |
| Independent 2,500-byte attribute control | 1 | External-block mapping. |
| Both attributes removed from the transition fixture | 0 | No mapping. |
| Attempted 8,192-byte attribute for EA-inode coverage | Unavailable | Fixture setup returned `ENOSPC`; the count stayed zero on the unchanged file. |

The transition supports mixed inode-body and external-block coverage on
this mount. The diagnostic query shows the store selected by the kernel
at each checkpoint, so it does not independently enumerate both stores at
the same instant. The initial 8,192-byte setup result applies to the local
`/tmp` mount and does not identify why that mount refused the value. The
separate hidden-attribute fixture verifies the namespace case, with its
storage layout unmeasured. All temporary files were removed.
[Ext4 mapping order](https://github.com/torvalds/linux/blob/028ef9c96e96197026887c0f092424679298aae8/fs/ext4/extents.c#L5111-L5151) ·
[FIEMAP extent flags](https://docs.kernel.org/filesystems/fiemap.html#extent-mapping)

### EA-inode fixture

The maintainer supplied the following result from an unprivileged run on a
separate ext4 mount. The probe read back the full 8,192-byte value before
querying FIEMAP.

```text
uid=1000 kernel=7.0.0-44-generic arch=x86_64 mount=5674 fstype=ext4 options=rw,nosuid,nodev,noexec,relatime block_size=4096 fixture_value_size=8192
clean: visible_xattrs=0 fiemap_count=0
small attribute control: visible_xattrs=1 fiemap_count=1
after small attribute removal: visible_xattrs=0 fiemap_count=0
large attribute verified: 8192 bytes, larger than one filesystem block
EA-inode candidate: visible_xattrs=1 fiemap_count=1
after large attribute removal: visible_xattrs=0 fiemap_count=0
PASS: larger-than-block xattr detected; disposable fixture removed
```

Ext4 stores values larger than the space in the inode or a single xattr
block in a separate EA inode. The verified 8,192-byte value on a mount with
4,096-byte blocks therefore exercises EA-inode value storage. This
classification follows the documented layout and observed value size. The
probe did not inspect the on-disk reference, and the supplied output did not
include a separate superblock feature listing.
[Linux EA-inode layout](https://docs.kernel.org/filesystems/ext4/eainode.html)

### Native Service baseline attempt

The maintainer supplied `komposyn-composition-native-results.lucowjp_.tar.gz`
for source `7696b27594c945351ad62f77ad6abfd40bd9e501`. The archive SHA-256
is `ee61ec19c30c05f4363e3bc267506a9a068a0084ad667bd56b7a2e9afd825b4e`;
all 23 internal checksums matched. It records Linux `7.0.0-44-generic`,
Go `1.26.11` Linux amd64, UID 1000, and trusted Bubblewrap `0.9.0` in
`ro-bind-fd` mode. Build and unit stages exited zero. The native stage
exited 1 with cleanup complete and zero descendants remaining.

Seven of nine native cases were `measured_pass`. Both settings and auth
rotation cases failed at the test overlay's assertion that the retry
registered a distinct newly opened replacement. Their records show that
the first updates preserved the destination, the workers joined before
the retries, and the retries succeeded. The assertion alone does not
distinguish a callback-count mismatch from equal recorded file identity.

The archived runner and overlay hashes match the older local checkout's
revision-2 bundle (`fb844f04…` and `3861d527…`). They differ from the
runner and overlay committed at the tested source revision (`30a84298…`
and `ceed4a05…`). The baseline result is `failed_or_incomplete` for that
combination. Rerun from an isolated checkout of the exact source revision
so its 18-case bundle runs. This archive contains no FIEMAP integration
case.
[Runner at the tested revision](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/testdata/native-acceptance/run-native-acceptance.py)

### Matched native Service baseline rerun

The maintainer supplied `komposyn-composition-native-results._rmb0w_b.tar.gz`.
Its source, revision-3 runner and overlay match commit
`7696b27594c945351ad62f77ad6abfd40bd9e501`. The archive SHA-256 is
`169bf2d8225336f7c1565877127e5d184c9b12b7bcd7f9053f32cc9e1c0a7e0d`.
All 32 internal checksums matched. The rerun used Linux
`7.0.0-44-generic` x86-64, Go `1.26.11`, UID 1000 and trusted
Bubblewrap `0.9.0` in `ro-bind-fd` mode.

Seventeen of 18 cases were `measured_pass`, including both rotation cases
that failed with the earlier overlay. The nested mount-boundary case was
`unavailable` because no nested mount scope was supplied. Build, unit and
native subprocesses all exited zero and confirmed cleanup with no descendants
remaining. The aggregate remains `native_unavailable` with exit code 2.
The native test binary SHA-256 is
`20a696859abc86f53230d6e0b5c85c8a0528373f8b275711645ad6468c0b0d8d`.

This establishes the measured Service baseline cases for the matched bundle.
The rerun did not include a FIEMAP integration case.
[Native runner guide](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/testdata/native-acceptance/README.md)

### Metadata-only Service integration

The revision-4 test overlay adds a fixed count-only host probe on the
broker's retained descriptor, with regular-file identity and mount checks.
The clean fixture returned zero at preparation and revalidation, then
completed the existing synthetic read. A visible xattr and inherited access
ACL returned one. Those fixtures, a mismatched mount and an injected ioctl
denial were refused before worker launch or once-approval consumption.
A metadata change at the in-process pre-handoff checkpoint changed the
count from zero to one. The worker joined without receiving the descriptor
or publishing synthetic bytes.

The exact overlay run reported the FIEMAP case as `measured_pass`. Its
source was `7696b27594c945351ad62f77ad6abfd40bd9e501`, overlay SHA-256
`bca92ac947cc01a8fa89fd504d2fa503d9f98a9025b9ac20712d1cf5041f08c4`.
The archive `komposyn-composition-native-results.f6168uq0.tar.gz` has
SHA-256 `422bf215bfd59b9d6904d201c3f203a81305ab82490ea8e149820e5e9eb7ba1a`.
All 33 internal checksums matched. All three subprocess stages completed
with exit zero and confirmed cleanup. The aggregate was 18 of 19 measured
passes and `native_unavailable` because the separate nested mount-boundary
case lacked a supplied mount. The run used trusted Bubblewrap `0.9.0` in
`ro-bind-fd` mode.

The late change was deterministic and in-process. The ioctl denial was
injected. The overlay exercised no edit operation or replacement effect.

### Remaining profile evidence

Security labeling, concurrent internal storage transitions, the intended
editing profile and native refusal under an active policy still require
verification. Hidden-attribute and EA-inode evidence comes from the
standalone fixtures above. Upstream has no editing worker operation to
exercise. This review changed no runtime code. Publication and cleanup
remain separate gates under the merged assessment.
[Merged assessment](https://github.com/ayeshLK/komposyn/blob/7696b27594c945351ad62f77ad6abfd40bd9e501/docs/harness/issue-322-replacement-mechanism-assessment.md)
