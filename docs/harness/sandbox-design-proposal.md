# Sandbox direction and implementation gates

Status as of merged PR #223 (`c5c629738371c969b0c575259dff4a1311988a68`).
This document consolidates the direction reviewed for
[issue #215](https://github.com/ayeshLK/komposyn/issues/215) and distinguishes
that target from the experimental code currently delivered. It does not enable
filesystem or shell tools. Native run records belong in the tracking issue.

This implementation wires the current chat's auth/settings owner lifecycle
and a pre-consumption runtime refusal seam around the fixed Linux worker. It does
not change the unknown-root-data gate or add a model-facing filesystem tool;
see [composition admission and sandbox service](composition-admission-sandbox-service.md).

## Authority and permission target

The agreed direction is **mounted-tree/object authority (B)**. An approved
worker acts on the objects exposed through its private view or explicitly
brokered descriptors. External renames need not revoke access to an already
opened ordinary object. This does not promise that every effect remains below
the original live host pathname, that the tree is a snapshot, or that its
objects have no aliases. The strict pathname-containment (A) counterexamples
remain valid evidence about that different contract.

Tool permission and resource permission are both required. Directory scope
includes the directory and descendants, never sibling prefixes or the parent.
The target write permission includes read, creation, modification, rename and
deletion within its authorized scope **only after the corresponding enforcement
is verified and reviewed**. It does not grant shell execution or network access.
The existing filesystem foundation still models deletion separately; this
record does not change its implementation.

Approvals are once or current-conversation, held in memory. Read approval also
authorizes returned content to reach the selected provider. The runtime remains
the sole consumer of once grants; a failed launch does not restore a consumed
grant or authorize an automatic retry.

The launch working directory is a candidate root. Explicit selection and a
future `/fs-root` command can replace it; selection itself grants no access.
Root changes invalidate affected grants and prepared work and require old
workers to be canceled and joined before affected new work starts. These are
target behaviors, not commands or lifecycle guarantees currently shipped.

## Ownership and execution boundary

```text
provider tool call
  -> runtime: prepare, authorize, consume once grant, coordinate lifecycle
  -> host-owned sandbox service: policy, capabilities, launch and supervision
  -> OS backend: constrained worker
  -> private bounded result capture
  -> runtime: revalidate publication, then return approved result to provider
```

| Component | Responsibility |
| --- | --- |
| `plugins/tools` | Tool definitions and adapters; no independent approval authority. |
| Permissions coordinator | Evaluate tool and resource requirements; hold and revoke in-memory grants. |
| Runtime | Bind invocation, root and policy generations; own dispatch and result publication. |
| Sandbox service | Compile enforceable policy, report availability, supervise workers and cleanup. |
| OS backend | Enforce the reviewed filesystem, process and network restrictions. |
| UI | Explain scope, approval choices, unavailable capabilities and required host preparation. |

The service belongs within the experimental core runtime boundary. Providers
never authorize or execute tools. The reusable Token Logger, its storage and
the separately owned production CLI retain their existing boundaries.

Linux is first, using Go orchestration and trusted system Bubblewrap with
seccomp. Detect required capabilities rather than demanding one version
number. A missing package, missing feature or blocked namespace must produce
an actionable unavailable result, with no unrestricted fallback or automatic
privileged host changes. Other platforms require separate backends and native
evidence before advertising support.

## Protected data and lifecycle requirements

Generic tools must not read or mutate registered credential/auth or settings
objects, or registered Token Logger databases, WAL/SHM files and backups.
Provider-owned sources remain immutable; a source read requires explicit
per-invocation approval and disclosure to the selected provider. Owners supply
the actual effective locations and object identities, including overrides.
Do not discover this inventory by scanning the machine or reading transcripts.
Unknown required coverage makes an affected capability unavailable.

Protection must cover reserved locations and protected objects, aliases,
replacement and rotation. A pinned directory alone is not an inventory of all
past, present and future descendants. Owner coordination must precede sensitive
writes. The memory-chat host service now wires the real auth/settings store
hooks through the existing registry and coordinator, but this does not cover
provider sources, Token Logger databases or arbitrary root contents.
Directory grants describe current entries, while an exact-file preparation
binds the approved identity and requires fresh preparation after replacement.

Launch admission, capability release, revocation and result publication must
share synchronized generation checks. Revocation blocks affected new work,
cancels workers and joins them, and suppresses stale buffered results. It does
not undo completed effects. Incomplete cleanup retains ownership of records
and resources and blocks affected admission; it must not be reported as joined.
Parent death and descendant cleanup need their own native evidence.

## Delivered research and remaining work

| Delivered | Limit |
| --- | --- |
| [Filesystem foundation](filesystem-enforcement-foundation.md), PR #212, and [policy/counterexamples](filesystem-enablement-policy.md), PR #213 | Host-side primitives demonstrate why retained descriptors do not establish A; no model-facing access. |
| [Linux sandbox study](sandbox-feasibility-study.md), PR #216 | Disposable experiments, not a reusable execution service. |
| [Brokered read harness](read-broker-feasibility.md), PR #219 | Fixed worker, fixture registry and publication coordinator; no auth/settings or provider-source integration at that point. |
| Worker startup and Bubblewrap compatibility, PRs #220–#221 | Descriptor-bind or bounded sealed-copy launch selected by features; each mode still needs native evidence. |
| Composition admission and non-exposing host service (this implementation) | Auth/settings registrations and the runtime preparation seam are wired to the fixed worker coordinator; the independent unknown-root gate still refuses every generic read. |
| [Manual evidence runner](read-broker-manual-runner.md), PR #222 | Reproducible source and bounded reports; its default remains the older #221 baseline. |
| Read handoff and outcome corrections, PR #223 | Fixes valid packet decoding, joined-worker classification and conservative directory-coverage refusals; merged fixes are not a verified native pass. |

Next, rerun the corrected baseline from a normal Linux host terminal, using a
checkout that contains the runner:

```sh
git fetch origin master
scripts/run-read-broker-study.sh \
  --revision c5c629738371c969b0c575259dff4a1311988a68 \
  --bwrap /usr/bin/bwrap
```

An explicit revision is necessary because the runner defaults to the earlier
#221 commit. Review case outcomes and worker/read/publication evidence, not
just exit status. A passing baseline alone does not close #215.

Remaining gates are:

1. Establish native evidence for both supported launch mechanisms. The reported
   version alone does not identify the mechanism, because distributions may
   backport features.
2. Exercise independent namespace actors at capability release, credential
   rotation and aliases, root/parent/target replacement, protected ancestors,
   mount boundaries and recursive-child movement. Distinguish injected-worker
   unit tests from actual sandbox observations.
3. Verify parent-death behavior, worker termination and descriptor/resource
   cleanup, including incomplete shutdown and stale-result suppression.
4. Extend the trusted composition beyond auth/settings only when the actual
   host graph owns those resources; provider-source identity/open lifecycle
   and the unknown-root-data boundary remain unresolved. The runtime has a
   pre-consumption preparation seam, but no read tool, root UI, or provider
   publication path is enabled.
5. Review typed-read enablement independently. Write, rename, deletion and shell
   need their own profiles, limits and acceptance evidence before registration.

The current fixed worker has bounded reads, output, scratch and execution
time. It does not establish aggregate process, memory or CPU quotas; aggregate
cgroup controls remain deferred and exhaustion safety is not claimed. Shell
safety cannot be inferred from typed-read results. MCP, subagents and persistent
conversation state remain outside this milestone.

## Source records

- [Issue #215](https://github.com/ayeshLK/komposyn/issues/215) and its reviewed
  [read-broker research](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6575572199).
- [PR #219](https://github.com/ayeshLK/komposyn/pull/219),
  [PR #220](https://github.com/ayeshLK/komposyn/pull/220),
  [PR #221](https://github.com/ayeshLK/komposyn/pull/221),
  [PR #222](https://github.com/ayeshLK/komposyn/pull/222), and
  [PR #223](https://github.com/ayeshLK/komposyn/pull/223).
- [Implemented phase-one tool architecture](tool-use-phase-one-architecture.md).
