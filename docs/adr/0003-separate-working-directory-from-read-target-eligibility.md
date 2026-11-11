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
coverage, regular-file, size, and exact-object checks remain in force. Native
outside-directory evidence is still required; the available host backend does
not meet the runner's trusted ownership preflight, so this implementation has
not claimed native acceptance.
