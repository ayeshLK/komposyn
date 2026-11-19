---
status: accepted
---

# Preserve exact-object and read-revision authority for file editing

This accepted ADR records the retained contract and the bounded negative
research disposition from issue #320. Any future edit must apply to the exact
mounted object and content revision from a host-recorded, previously approved
read in the same conversation; a model-supplied digest or pathname replacement
is not authority. The examined unprivileged candidates do not establish atomic
revision-bound effects, so no mechanism, editing profile, prototype, privileged
setup or model-facing capability is accepted. Editing remains unavailable.

Issue #320 closed on 2026-11-19 after PR #340 merged; its
[native dependency from #322](https://github.com/ayeshLK/komposyn/issues/322)
is resolved, but #322's positive-evidence and explicit enablement gate remains
unsatisfied. The [#320 design investigation](../harness/issue-320-editing-design.md)
records the contract, lease evidence, rejected candidates and missing evidence;
the [closeout](../harness/issue-320-closeout.md) records the bounded result
and provenance. This ADR does not close #267 or #315 and does not authorize
implementation or tool enablement.
