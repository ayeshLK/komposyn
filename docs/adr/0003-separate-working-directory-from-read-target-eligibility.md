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

This is partial native evidence, not full acceptance. The run left 6 cases
inconclusive and 1 unavailable; it did not provide native cases for unsupported
lookups or oversized/nonregular targets, and protected-ancestor and
mount-boundary coverage remains incomplete. #278 remains open until its
required native positive and negative acceptance cases have conclusive
evidence. See the [manual read-broker runner guide](../harness/read-broker-manual-runner.md)
for reproduction and result interpretation.
