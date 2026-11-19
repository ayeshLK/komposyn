---
status: accepted
---

# Keep file creation unavailable after the tested candidates fail

The direct create-then-write candidate, tested `O_TMPFILE` worker, and selected
original-user-namespace broker each have a same-user counterexample or failed
startup gate. Accept #319's negative research closeout for those mechanisms,
keeping creation unavailable; this is not a claim
that all unprivileged creation is impossible and does not satisfy #321's
positive-evidence gate. Alternative launch, host-policy, trusted-approval, or
deployment boundaries require separate decisions. This disposition does not close
#267 or #315. See the [consolidated evidence and criterion matrix](../harness/issue-319-closeout.md).

Issue #319 closed on 2026-11-19 after PR #338 merged. Its native dependency from
#321 is resolved; #321's separate positive-evidence and explicit enablement
gate remains unsatisfied. Acceptance records the research disposition, not a
safe mechanism or delivered creation tool.
