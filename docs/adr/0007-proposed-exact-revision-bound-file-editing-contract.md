---
status: proposed
---

# Preserve exact-object and read-revision authority for file editing

Any future edit must apply to the exact mounted object and content revision
from a host-recorded, previously approved read in the same conversation; a
model-supplied digest or pathname replacement is not authority. Current
unprivileged candidates do not establish atomic revision-bound effects, so no
editing mechanism or prototype is selected and editing remains unavailable
pending a separate trust/mechanism decision. The [#320 design investigation](../harness/issue-320-editing-design.md)
records the contract, lease evidence, rejected candidates and missing evidence.
This proposal does not close #267, #315 or #320, and does not unblock #322.
