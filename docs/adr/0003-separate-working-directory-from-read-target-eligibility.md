---
status: accepted
---

# Separate working directory from read-target eligibility

The working directory supplies relative-path context and does not limit which
exact files may be requested. Absolute and relative targets outside that
directory may receive individual once approval without changing the working
directory; show the resolved target, provider destination and an outside-folder
notice in the same approval panel. Preserve protected-file refusals, read
limits and exact-object authorization, with no directory-wide grant.

This replaces the selected-root eligibility restriction for the proposed UX.
The broker policy and native acceptance must be updated and reviewed before
enabling this wider target scope; current published enforcement is unchanged.
Changing the working directory does not load repository configuration or
redirect authentication/settings paths.

There is no separate file-reading toggle. Each read requires explicit approval,
with denial and turn cancellation available. `/cd <path>` changes relative-path
context; the obsolete `/fs-root` command and selection screen are removed.

Implementation is pending; this decision does not broaden current enforcement.

See [spec #275](https://github.com/ayeshLK/komposyn/issues/275) and implementation ticket [#278](https://github.com/ayeshLK/komposyn/issues/278).
