---
status: proposed
---

# Use runtime file objects and conflict-aware approved saves

On 2026-11-19 the maintainer clarified the intended mutation boundary:
KompoSyn coordinates its own file objects, checks external changes and asks
for permission at save time. External components may mutate files; user
approval does not exclude a change during approval or between the final check
and the write. We propose this practical concurrency contract for the next
creation design rather than requiring isolation from hostile same-user actors
throughout startup and the effect. A detected conflict invalidates the old
save proposal and requires a fresh explicit decision.

This is a material proposed change to #267 and #315, recorded for PR review
before reconciling those issue contracts. It is not positive evidence for the
previous contract. The [design](../harness/runtime-file-object-saves.md)
defines the approval and conflict flow, limits and remaining decisions.
Creation is the next operation; editing, overwrite and append require separate
operation designs and acceptance. ADR-0007's existing editing contract remains
in force until explicitly revised. Historical negative findings remain valid
under their original contracts. No prototype, tool enablement, host change
or issue closure follows from this documentation proposal.
