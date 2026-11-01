# Filesystem enforcement foundation

This document describes a host-side foundation for future reviewed filesystem
operations. It does not register filesystem tools or make filesystem access
available to a model. The executable remains text-only for filesystem access.

## Ownership and API

`internal/harness/filesystem` owns prepared targets and the native operation
implementation. Its narrow API prepares a read, text write, or explicit delete
against a configured root. It exposes operation methods and detached
authorization resource requests, not a raw filesystem handle or general
executor. Registration does not grant access. The runtime and permissions
coordinator continue to own policy, grant storage, approval, revocation, and
the atomic grant-revision check immediately before dispatch.

`Root.AuthorizationScope` describes a directory and descendants using
component-aware path containment. A write grant implies read in the same
scope, while delete is a distinct operation. Directory scopes do not include
the directory's parent or sibling-prefix names. Deletion has no conversation
approval shortcut; future callers must request per-invocation authorization.
Missing authorization, stale revision, denial, or unavailable enforcement
must prevent dispatch.

Prepared targets bind the canonical target, its parent/root descriptor
identity, operation, and exact resource request. Callers must pass that same
request through centralized authorization. Re-preparing after approval is
not a substitute for using the prepared target. Changes in target or
authorization context require fresh authorization.

## Linux enforcement

The Linux backend uses `openat2` relative to retained directory descriptors
with `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS`, `RESOLVE_NO_MAGICLINKS`, and
`RESOLVE_NO_XDEV`. It fails closed when the kernel or flags are unavailable.
Reads use the opened file descriptor. Prepared existing write and delete
targets retain a close-on-exec `O_PATH` descriptor, which pins the target
identity against inode reuse; writes also open the named target without
following links, compare it with the pinned identity, and mutate through that
opened descriptor. Duplicated directory descriptors are created atomically
with close-on-exec. Bounded reads return a complete UTF-8 prefix when the byte
limit cuts through a rune and reject malformed bytes in the returned range.
Recursive deletion walks directory descriptors without traversing symlinks;
an explicitly selected symlink may be removed as a link entry, never as its
destination. Nonexistent write targets use a securely opened parent and
exclusive, no-follow leaf creation. Hard-linked read/write targets are
rejected. Operations are bounded by size/depth/entry limits.

These claims are supported by native behavioral tests on Linux using
disposable fixtures, deterministic substitution checkpoints, and actual file
operations. They are not a claim of isolation from an arbitrary concurrent
same-user process. A retained descriptor remains bound to its directory
object if that directory is renamed after validation; operations therefore
do not follow a substituted pathname, but may act on the renamed object.
Strict pathname-tree semantics against a concurrent namespace writer need a
separately reviewed mechanism before model-facing use.

Non-Linux builds return `ErrUnsupported`; they do not use weaker path-based
I/O. Stub compilation is not enforcement evidence. macOS and Windows native
behavior has not been validated, including Windows junction/reparse-point
semantics. Cross-volume/mount traversal is refused on Linux through
`RESOLVE_NO_XDEV`; native mount behavior was not exercised in this environment.

## Authorization and protected data

The foundation is not connected to tool registration or dispatch. A future
integration must obtain the exact prepared target's action/resource request,
authorize centrally, and use the existing atomic revision validation directly
before invoking the operation. It must preserve one-time deletion approval,
revocation behavior, cancellation outcomes, and no-retry handling for
uncertain mutations. Registration alone must never authorize an operation.

No broad protected-path allow or deny list is invented here. Before enabling
tools, maintainers must define how provider-owned source files, credentials,
KompoSyn application data, and other protected locations are excluded or
explicitly handled, without silently inferring a root such as the current
directory or home directory.

## Acceptance conditions for later tools

An `apply_patch` PR must define a strict bounded patch schema, bind its
prepared target and write scope to the reviewed request, authorize before
dispatch, preserve source newlines/encoding rules, and test creation/update,
symlink and hard-link rejection, replacement races, partial/unknown outcomes,
revocation, cancellation, and protected-path policy. It must not add moves or
renames as a model capability.

A `delete_path` PR must define explicit file/directory and recursive intent,
require fresh per-invocation deletion approval, distinguish deleting a link
entry from deleting its destination, enforce bounded descriptor-based
traversal without following links, and test substitutions, partial/unknown
outcomes, cancellation, revocation, and protected-path policy.

Both follow-up PRs must keep this foundation's unsupported platforms
unavailable, demonstrate native enforcement on each claimed platform, use
only the existing in-memory conversation, and leave filesystem tools clearly
unavailable wherever enforcement cannot be demonstrated.

## Primary references

- Linux `openat2(2)`: <https://man7.org/linux/man-pages/man2/openat2.2.html>
- Linux `open(2)`: <https://man7.org/linux/man-pages/man2/open.2.html>
- Linux `rename(2)`: <https://man7.org/linux/man-pages/man2/rename.2.html>
- Linux `unlink(2)`: <https://man7.org/linux/man-pages/man2/unlink.2.html>
