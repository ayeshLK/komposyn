---
status: accepted
---

# Use runtime file objects and conflict-aware approved saves

On 2026-11-19, PR #343 merged the maintainer's accepted creation-boundary
decision: KompoSyn coordinates its own runtime file objects, checks external
changes and asks for permission at save time. External components may mutate
files; approval does not exclude a change during approval or between the final
check and the effect. This accepts that residual race instead of requiring
isolation from hostile same-user actors throughout startup and the effect. A
detected conflict invalidates the old save proposal and requires a fresh
explicit decision.

This decision applies to the creation boundary only; it does not revise the
editing, rename or delete requirements in #267 or #315. It is not positive
evidence for a mechanism and does not reinterpret earlier research under its
original contract. The [runtime-file-object design](../harness/runtime-file-object-saves.md)
records the accepted approval and conflict flow. The [creation mechanism
proposal](../harness/creation-mechanism-proposal.md) is a separate, pending
design review. ADR-0007's editing contract remains in force. No prototype,
tool enablement, host change or issue closure follows from accepting this
boundary decision.
