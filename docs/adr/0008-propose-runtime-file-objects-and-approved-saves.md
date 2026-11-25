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
proposal](../harness/creation-mechanism-proposal.md) records the separately
reviewed implementation mechanism. ADR-0007's editing contract remains in
force. Accepting this boundary alone did not enable a tool; separate mechanism,
evidence, owner/scope, and enablement reviews followed.

The later [proposed ADR-0010](0010-propose-approved-entry-replacement-for-file-editing.md)
records a distinct editing design review. It does not change this creation-only
decision or enable editing. Its proposed editing boundary supersedes the
relevant ADR-0007 clauses only if accepted; no concrete editing mechanism or
profile has passed acceptance.

## Post-merge implementation status — 2026-11-25

PR #359 merged the profile-gated creation composition at
`32ae327bd44e4581fbf65933f7f5b02afb9ad67c`. This decision's residual external
race boundary is used only for creation. The merge delivers source behavior
for Linux amd64/local ext4 with trusted Bubblewrap `ro-bind-fd`, subject to live
Service owner, scope, policy, and cleanup checks. It does not imply that a new
tagged binary release has been published, and it does not change ADR-0007 or
enable editing, rename, or delete.
