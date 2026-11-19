---
status: accepted
---

# Investigate a creation broker with a protected authority chain

After #336 demonstrated same-user access to the staged creation inode, select
one bounded investigation of an operation-specific broker in the original user
namespace, protecting every authority holder including the experimental chat
process. Accept the resulting restrictions on ordinary debugger attachment and
OS core dumps while creation authority is held, and initially investigate only
hosts with already-active Yama mode 2 or 3; no host-policy changes, privileged
setup or separate trusted approval UI are part of this direction.

This preserves #267's authority contract rather than relying on a dumpability
flag inside the current child user namespace. Startup races and inherited
approval/control handles remain inside the proof obligation: if startup or
confinement fails, reject this candidate before building the broader workflow.
The accepted decision is the research direction, not mechanism safety or tool
enablement. See the [#319 design and rejection gates](../harness/creation-isolation-design.md).
