---
status: accepted
---

# Keep the working directory for the current chat run

The working directory is run-level user context: retain it until changed or
exit, including across model/provider changes. Approvals and sensitive provider
context remain bound to their authorized conversation and destination and must
retire on model/provider changes and relevant security invalidations. A working-
directory change alone preserves still-valid conversation and file context.
This preserves the directory choice without
repeated selection while retaining exact-file approval; the choice stays in
memory and does not become remembered folder trust across launches. Its role
is relative-path resolution; target eligibility is governed separately by
[ADR-0003](0003-separate-working-directory-from-read-target-eligibility.md).

Start in the launch directory without a blocking folder-confirmation dialog;
show it clearly in chat. `/cd <path>` changes it directly. Remove `/fs-root`
and do not introduce a separate file-reading toggle. Each read requires approval.

Implemented by #276 launch-directory initialization and #277 `/cd` navigation.
Directory state remains run-local; changing it alone preserves still-valid
conversation and file-derived context while model/provider changes continue to
invalidate destination-bound authority.
