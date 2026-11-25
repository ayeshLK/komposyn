---
status: accepted
---

# Keep file creation unavailable after the tested candidates fail

The direct create-then-write candidate, tested `O_TMPFILE` worker, and selected
original-user-namespace broker each have a same-user counterexample or failed
startup gate. Accept #319's negative research closeout for those mechanisms;
this is not a claim that all unprivileged creation is impossible and did not
satisfy #321's positive-evidence gate. Alternative launch, host-policy,
trusted-approval, or deployment boundaries required separate decisions. This
disposition does not close #267 or #315. See the [consolidated evidence and
criterion matrix](../harness/issue-319-closeout.md).

Issue #319 closed on 2026-11-19 after PR #338 merged. Its native dependency
from #321 was resolved then. **Later disposition:** PR #359 separately delivered
creation in source after the additional contract, mechanism, owner/scope,
native-evidence and enablement reviews. This ADR's negative result remains
limited to the candidates it examined; it is not the current creation
availability record. See [#321 native acceptance](../harness/issue-321-native-acceptance.md).
