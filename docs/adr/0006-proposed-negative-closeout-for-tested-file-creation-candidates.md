---
status: proposed
---

# Keep file creation unavailable after the tested candidates fail

The direct create-then-write candidate, tested `O_TMPFILE` worker, and selected
original-user-namespace broker each have a same-user counterexample or failed
startup gate. The proposed disposition is to close #319 as a negative result
for those mechanisms while keeping creation unavailable; this is not a claim
that all unprivileged creation is impossible and does not satisfy #321's
positive-evidence gate. Alternative launch, host-policy, trusted-approval, or
deployment boundaries require separate decisions. This proposal does not close
#267 or #315. See the [consolidated evidence and criterion matrix](../harness/issue-319-closeout.md).
