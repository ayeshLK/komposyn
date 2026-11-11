---
status: accepted
---

# Separate working directory from read-target eligibility

The working directory supplies relative-path context and does not limit which
exact files may be requested. Absolute and relative targets outside that
directory may receive individual once approval without changing the working
directory; show the resolved target, provider destination and an outside-folder
notice in the same approval panel. Preserve protected-file refusals, read
limits and exact-object authorization, with no directory-wide grant. The broker
anchors lookup at the requested target's verified mount root and retains
component-constrained, no-symlink, no-magic-link and no-mount-crossing checks
within that mount; the working directory is not the eligibility boundary.

This replaces the selected-root eligibility restriction for the proposed UX.
The broker policy and native acceptance must be updated and reviewed before
enabling this wider target scope; the implementation is tracked in #278.
Changing the working directory does not load repository configuration or
redirect authentication/settings paths.

There is no separate file-reading toggle. Each read requires explicit approval,
with denial and turn cancellation available. `/cd <path>` changes relative-path
context; the obsolete `/fs-root` command and selection screen are removed.

## Implementation status

Implemented in #278: the target path is opened beneath its verified mount root
and no longer needs to be contained beneath the working directory. Registered
protected routes, pinned identities, provider-source classification, directory
coverage, regular-file, size, and exact-object checks remain in force. The
native runner initially built commit `418ac64` but refused backend trust before
probing because `/usr/bin/bwrap` was owned by UID 65534. A later run against
`89a8fa7fb93aa1bb0defea6bffa5dc9fcccd61e7` completed on Linux amd64 with Go
1.26.11 and trusted Bubblewrap 0.9.0 (`ro-bind-fd`). It recorded 55 measured
passes, 6 inconclusive cases, and 1 unavailable case across 62 rows. The
`outside_working_directory_positive` case returned bytes matching the approved
object after worker readiness, capability handoff, read, join, and publication;
direct protected reads, hard-link aliases, and post-capture revocation checks
also passed. The checksummed archive SHA-256 is
`bc785b5e566e541ddc4ed581e640c3654ffe1bd3ac48c24a5789a6e3c31df328`.

At that point this was partial native evidence, not full acceptance. The run
left 6 cases inconclusive and 1 unavailable; it did not provide native cases
for unsupported lookups or oversized/nonregular targets, and protected-ancestor
and mount-boundary coverage remained incomplete. #278 remained open pending
corrected native positive and negative evidence. The later scoped completion
run is recorded below and in the [#278 completion comment](https://github.com/ayeshLK/komposyn/issues/278#issuecomment-6798370362).
See the [manual read-broker runner guide](../harness/read-broker-manual-runner.md)
for reproduction and result interpretation.

The #278 follow-up added native report rows for unsupported symlink and FIFO
lookups, an oversized regular file whose bounded partial result must not be
published, and the existing post-capture revocation check. At the time of the
older reports these were implemented scenarios without conclusive evidence;
the corrected trusted-host report below now records the scoped rows as measured
passes. Nested mount transitions and protected-ancestor races remain unmeasured;
backend trust and lookup guards stay enabled.

Run `08076c58667acd1d73971ee8ca6d1eea46682e89` completed on Linux amd64 with
Go 1.26.11 and trusted Bubblewrap 0.9.0 (`ro-bind-fd`). Its validated report
contains 58 measured passes, 6 inconclusive cases and 1 unavailable case, with
no observed-violation rows. The outside-working-directory positive, symlink
and FIFO pre-worker refusals, oversized no-publication case, protected aliases,
stale/revoked publication, and target-replacement actor case passed. Archive
SHA-256: `363c210c4d7d44b3a3fbee9a967cc672176b0054d70f161e5d52d45d856b7d52`.

The inconclusive cases are `actor_protected_ancestor_relocation`,
`owner_registration_before_release`,
`owner_registration_while_capability_held`,
`real_auth_held_worker_cleanup_pending_blocks_write_then_retry`,
`real_settings_cleanup_pending_refuses_then_retries`, and
`owner_incomplete_cleanup_blocks_write_native`;
`owner_incomplete_cleanup_blocks_write` is unavailable. The protected-ancestor
case remains fail-closed because incomplete descendant coverage prevents any
eligible same-device read. The moved-descendant refusal passed separately.
Owner lifecycle cleanup rows still lack conclusive native observations.

The next run used source commit `ebd5dac166cd169cf80ad1a870ba9bbad033ec1c`
and completed on the same trusted Linux amd64/Bubblewrap 0.9.0 descriptor-bind
profile. It recorded 59 measured passes, 6 inconclusive cases and 1 unavailable
case. `separate_mount_target_positive` passed with worker readiness, exact
approved bytes, publication, and join. Archive SHA-256:
`113b96181a85ae37d63c9c847a6e9e1710147fb18e048a8bd65334c10f386828`.

A subsequent review found that the symlink and FIFO rows used generic
preparation-refusal details. Although they were reported as `measured_pass`,
that detail did not prove the broker lookup itself produced the refusal; an
unrelated cleanup or inventory refusal could have produced the same report.
The rows are not counted as conclusive until rerun with broker-specific
refusal reasons.

This confirms an exact approved target on a distinct mount is readable. The
broker anchors each target at its verified mount root and applies
`RESOLVE_NO_XDEV` beneath that root; a cross-mount refusal is not the policy for
a separately requested target on another mount. A nested mount transition was
not separately staged in the native fixture.

The protected-ancestor actor row remains inconclusive because
`DirectoryCoverageIncomplete` refuses the target before an eligible read can
start; the moved-descendant refusal passed separately. The owner registration
and cleanup rows remain inconclusive or unavailable as listed above. They
exercise owner write/cleanup lifecycle beyond #278's target-admission change;
the relevant revocation and actual auth/settings post-capture invalidation
rows passed. Keep these broader study limits visible, and do not weaken backend
trust or lookup guards.

## Scoped #278 native completion (2026-11-11)

The corrected trusted run used source commit
`434542a9d77f202c94182867974afe27bf0b86c6`, which is included in merged
master through PR #285. On Linux amd64 with Go 1.26.11 and trusted Bubblewrap
0.9.0 (`ro-bind-fd`), all 66 report rows were classified: 59 measured passes,
6 inconclusive and 1 unavailable. The archive SHA-256 is
`7190ef4e90f86716bc20f91e3bb7131a3dd75c8a5c6f86ea2f6e6876081b9b9b`.

The corrected symlink and FIFO rows each show the matching broker refusal
reason, zero once-grant consumption, no worker startup and no capability
handoff. Outside-directory and separate-mount positives, oversized
no-publication, protected direct/alias refusals, target replacement bound to
the approved object, and post-capture revocation suppression also passed. The
completion record is the [#278 issue comment](https://github.com/ayeshLK/komposyn/issues/278#issuecomment-6798370362); #278 is closed.

This closes the scoped #278 native acceptance requirement. It does not turn the
broader inconclusive owner-cleanup/protected-ancestor rows or unavailable
owner-cleanup case into passes, and it does not measure a nested-mount
transition or establish general sandbox or production readiness.
