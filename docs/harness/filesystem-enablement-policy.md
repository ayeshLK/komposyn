# Filesystem enablement policy and evidence

Status: filesystem tools are unavailable. This record describes the remaining
security gap after #212, presents proposed policy and root-selection behavior,
and records native test evidence. No filesystem tool is registered, and no
policy in this document is silently treated as approved.

## Threat model

The relevant adversary is an untrusted model invocation while another process
or thread able to modify the selected filesystem namespace concurrently
renames, replaces, links, or mounts entries. The model does not receive
filesystem descriptors. The kernel, filesystem driver, and process owner are
trusted; this is not a defense against a privileged kernel attacker. The
authorization boundary is a directory pathname and its descendants, not an
object capability that follows a directory or file wherever it is renamed.

Three properties must not be conflated:

| Property | Meaning | What #212 provides | Remaining gap |
| --- | --- | --- | --- |
| Object identity | A descriptor continues to refer to the same opened inode/object; a pinned `O_PATH` descriptor prevents inode reuse for prepared existing write/delete targets. | Linux descriptor identity checks and replacement tests. | The object can be moved outside the authorized pathname tree while the descriptor remains usable. |
| Pathname containment | At the instant of path resolution, every component is beneath the selected root. | Linux `openat2` `RESOLVE_BENEATH`, no symlink/magic-link following, and `RESOLVE_NO_XDEV` for descendants. | Containment is not held as a lock over the later read/write/delete. Parent/root renames after resolution remain effective. |
| Filesystem aliasing | Multiple names/mount paths refer to the same object or hierarchy. | Existing hard links are rejected for read/write when observed; mount crossings during resolution are rejected; symlink traversal is rejected. | A hard link created after the count check, later rename, or other namespace alias can race the effect. Delete cannot atomically say “unlink this name only if it still refers to inode X.” |

`openat2` constrains one pathname walk. It does not provide a transaction
covering the later data syscall. Retaining a descriptor fixes object identity,
not pathname containment. Linux VFS internally locks directories while each
individual lookup/unlink/rename runs, but userspace has no API to hold those
internal locks across this multi-step authorization and I/O sequence. The
kernel pathname-lookup documentation explicitly describes using an opened
file after its dentry is unlinked and the rename races handled during lookup;
it does not promise that a descriptor remains under a pathname root after the
lookup returns.

### Deterministic native counterexamples

`internal/harness/filesystem/filesystem_linux_test.go` uses disposable
temporary trees and injected checkpoints between checks and effects. The
counterexample tests intentionally pass by observing the unsafe effect; they
are evidence for unavailability, not acceptance tests for model-facing use.

- Moving/replacing the selected root after read validation still lets the
  retained file descriptor return its contents after the root pathname moved.
- Moving a prepared parent outside the selected root after write validation
  still lets the descriptor create a file in the moved directory.
- Moving the prepared delete parent outside the root after the final check
  still lets `unlinkat` remove the file from the moved directory.
- Moving an already-open target outside the root before `ftruncate` still lets
  the retained target descriptor mutate it.
- Replacing a delete target with a symlink after final validation lets
  `unlinkat` remove that new link entry. The link destination is not followed,
  but the approved entry changed and should require fresh authorization.
- Moving a recursive child directory outside the root after opening it still
  lets descriptor-based recursion delete its contents before it notices the
  name moved.
- Creating a hard link after the write alias check still lets a write through
  the open descriptor modify that new alias.
- Replacing a write target with a symlink or a new file before its final open
  is refused; target pinning also prevents the inode-reuse case reviewed in
  #212. These checks close specific windows but do not close the post-check
  windows above.

These findings mean the current Linux read, write, and delete primitives are
not safe to expose under the required pathname-scope contract. They remain
unregistered and unavailable. No final path-string recheck, advisory lock,
model instruction, or descriptor pin is presented as closing that gap.

### Candidate enforcement mechanisms

- **Descriptor-relative `openat2` only:** retained as useful defense in depth
  for component resolution and link/mount refusal. Rejected as the complete
  boundary because a later rename does not revoke an already-open descriptor.
- **Advisory locks or a second `stat`/canonicalization:** rejected because
  unrelated writers do not have to honor the lock and the namespace can
  change immediately after the check.
- **Landlock in this process:** not adopted. Landlock is an operating-system
  sandbox, which is outside the current authorization. Its rules are enforced
  on threads (with synchronization requiring a sufficiently new ABI); a Go
  process has many existing runtime threads. Filesystem rights are associated
  with opened descriptors and read/write are checked at open, so pre-opened
  descriptors remain a concern. It constrains the sandboxed process's own
  operations; it does not prevent a separate same-user process from moving a
  directory while KompoSyn holds a descriptor to it. Using it safely would
  require a separately reviewed worker/process and descriptor-lifecycle
  design, plus native security tests.
- **Private mount namespace or copied tree:** not adopted. This changes the
  filesystem authority model or introduces a sandbox/snapshot/commit system;
  it needs a separate design decision and does not by itself define safe
  commit semantics when the host tree changes.
- **Filesystem-specific transactions or immutable snapshots:** potentially
  useful only with explicit filesystem support, snapshot consistency, and
  effect-publication rules. No portable primitive has been established here.

The concrete next enforcement study should compare a dedicated short-lived
worker with kernel-enforced path policy against filesystem-native snapshot or
transaction support. It must start with read, write, delete, and recursive
delete adversarial rename tests. The worker must receive only the exact
prepared operation and no provider credentials, general filesystem handle,
network, or unrelated inherited descriptors. Adoption would be a separate
maintainer-reviewed OS sandbox decision; until then all model-facing
filesystem operations remain unavailable.

## Protected-path policy proposal

Existing requirements and new proposals are separated below. The proposal
does not add enforcement because the runtime has no authoritative inventory
for every protected path and several choices need maintainer approval.

| Category | Existing requirement | Recommended model-facing read | Create/update | Delete | Reason and unresolved coverage |
| --- | --- | --- | --- | --- | --- |
| Credential files and auth material: default `~/.komposyn/auth.json`, explicit `--auth-file`, OAuth access/refresh/ID tokens, API keys | Auth persistence is private and validated; credentials must not enter UI snapshots, provider metadata, diagnostics, or ordinary chat output. There is no filesystem-tool policy yet. | Deny, including one-time approval. | Deny. | Deny. | Prevent direct disclosure or destruction. The host must pass the actual resolved auth path (including overrides) as a protected object; do not infer it from `HOME` alone. Environment-backed secrets have no regular-file path and need separate exclusions for pseudo-filesystems. |
| KompoSyn settings: default `~/.komposyn/settings.json`, explicit `--settings-file` | UI configuration only; no auth/chat transcript. No tool protection is defined. | Deny by default; optional read-only export UX would need a separate decision. | Deny. | Deny. | Avoid indirect disclosure and application reconfiguration. The effective configured path must be registered by composition, including overrides. |
| Provider-owned source files (Codex/Claude logs and other imported source records) | Core adapters may read them; KompoSyn must not modify provider sources. | Recommend explicit per-invocation approval with a clear notice that retrieved text is returned to the selected model; do not allow a conversation grant for these sources by default. | Deny. | Deny. | Read can disclose private transcripts; write/delete violates source ownership. The chat currently has no complete registry of provider source roots. Do not discover or scan provider directories implicitly. |
| Token Logger database, SQLite `-wal`/`-shm`, migration backups, explicit backup paths | Preserve existing databases and backups. Backup/migration code owns consistency and restoration guarantees. | Deny. | Deny. | Deny. | A generic file tool can corrupt coupled DB/WAL state or delete a verified recovery copy. Core `Store` receives caller-selected database/backup paths; the temporary chat does not own or know those paths. Do not guess from extensions or names. |
| Other application state, caches, tokens, sockets, device and pseudo-filesystem entries | Existing guidance protects credentials and persistent state; no general allowlist exists. | Deny special files and unknown application-owned state; ordinary user-selected regular files follow explicit scope authorization only after protected-path registry is complete. | Same. | Same. | The current backend only supports regular-file reads/writes and file, directory, or link deletion, but path names can still address sensitive state. `/proc`, `/sys`, `/dev`, sockets, and device files require explicit rejection or reviewed support. |
| Ordinary files under a user-selected root | No implicit root is authorized; explicit action/resource approval is required. | Allow only after matching per-call or conversation resource authorization; recommended one-time approval for each newly read file. | Allow only after write authorization and target binding; write implies read only within identical scope. | Separate explicit per-invocation approval; no conversation lifetime. | User-selected scope does not override any protected-path rule. Unknown enforcement or protected-path inventory means deny. |

**Overlap and aliases (proposed):** protected rules take precedence over every
allow or conversation grant, including broad ancestor directory scopes.
Compare component-aware canonical paths and stable object identities for
existing protected files/directories. Reject symlink traversal, reparse
points, hard-link aliases, and mount aliases unless a later policy explicitly
supports one. A protected identity discovered through an alias protects all
names of that identity. Root/mount IDs and file IDs must be bound to the
prepared operation; path-only matching is not sufficient. These rules cannot
be considered implemented until the race-enforcement gap is solved.

**Inventory limitation:** auth/settings expose their default/override paths;
Token Logger accepts caller-owned database and backup paths; provider source
roots are owned by collector configuration, not by the temporary chat. The
chat currently has no shared protected-path registry for those external
owners. The recommended design is a detached, host-owned registry populated
by each owner at composition time, never by filesystem scanning or model
arguments. The production `komposyn-lg` remains unchanged and must not be
wired to this experimental tool surface.

## Explicit root-selection proposal

No workspace is required today, and this proposal adds no project, persistent
workspace, or session. Recommended smallest TUI surface:

1. `/fs-root` opens a modal asking the user to enter a full absolute path.
   The user confirms the exact displayed path. Do not default the input to
   cwd, home, repository discovery, or model arguments. A native directory
   picker can be considered later if supported consistently by terminals.
2. Display the selected canonical root in a compact status line and in the
   filesystem permission prompt. Label it **selected root; no access granted**.
   Platform enforcement validation is a separate status with its own
   unavailable reason. Permission approval is a third independent step.
3. The root exists only for the current in-memory conversation. Confirmed
   conversation reset clears it and closes all root/prepared descriptors.
   Process exit also closes it. It is never written to `settings.json`.
4. Selecting a different root increments a root generation, invalidates and
   closes every prepared capability, revokes filesystem grants for the old
   generation, and dismisses any pending filesystem approval as stale. The
   new root starts with no grants. Ordinary conversation grants for other
   tools are unchanged.
5. If the root pathname is replaced, moved, becomes unavailable, or native
   enforcement is unsupported, operations fail closed with a visible status;
   they do not follow the retained object at its new location. The current
   Linux backend cannot yet provide this behavior for all rename races, so
   selection alone must not expose a tool. Unsupported macOS/Windows should
   allow the user to see the selected path only if desired, while reporting
   enforcement unavailable; no operation is offered.

This UI/configuration behavior is a proposal only. It is not implemented
pending maintainer approval, including whether typed absolute-path selection
is acceptable for terminal users and whether conversation reset should clear
the root as proposed.

## Platform and operation evidence

| Platform / operation | Current mechanism | Native evidence | Status |
| --- | --- | --- | --- |
| Linux read | `openat2` descriptor-relative resolution, `RESOLVE_BENEATH`, no symlink/magic link/cross-mount; bounded `pread` from the opened file descriptor. | Linux/amd64, Ubuntu host kernel `7.0.0-38-generic`: temporary-fixture tests pass for containment and show a deterministic root/parent rename after validation still permits reading through the descriptor. | Primitive tested; pathname-scope enforcement under concurrent rename unavailable; not model-facing. |
| Linux create/update | Secure parent descriptor, `O_EXCL` creation, `O_NOFOLLOW`, pinned existing target identity, regular-file and link-count checks, mutation via opened file descriptor. | Temporary-fixture tests pass for normal create/update, pre-effect replacement and link rejection; counterexamples show parent/target relocation or a late hard link can receive the mutation. | Primitive tested; concurrent pathname/alias enforcement unavailable; not model-facing. |
| Linux file/directory/link delete | `unlinkat` relative to retained parent; recursive descriptor walk; no symlink traversal; bounded depth/count. | Temporary-fixture tests pass for link-entry deletion and safe symlink handling; counterexamples show late target substitution and recursive child relocation can affect changed/out-of-scope entries. | Primitive tested; exact-target/path-scope enforcement under concurrent changes unavailable; not model-facing. |
| Linux mount boundary | `RESOLVE_NO_XDEV` during descriptor-relative resolution. | The native test attempted a disposable tmpfs mount; this environment returned `EPERM`, so the test skipped. | Kernel flag is in use, but mount-boundary behavior was not exercised here. |
| macOS read/write/delete | `platform_unsupported.go` returns `ErrUnsupported`; no weaker I/O fallback. | No native execution evidence for filesystem enforcement. Prior CI macOS tests exercise only the unsupported stub. | Unavailable. |
| Windows read/write/delete | `platform_unsupported.go` returns `ErrUnsupported`; no weaker I/O fallback. | No native execution evidence for filesystem enforcement or junction/reparse points. Prior CI Windows tests exercise only the unsupported stub. | Unavailable. |

Linux `openat2` requires kernel support (introduced in Linux 5.6) and the
required resolve flags. The backend fails closed if the syscall/flags are
unsupported. The tests above are behavioral tests on Linux/amd64. The Go race
detector is supplementary and does not establish filesystem namespace-race
safety. No macOS/Windows enforcement compatibility is claimed.

## Decisions required before user-facing implementation

1. **Namespace enforcement:** approve a separate design phase for a narrowly
   scoped worker/process OS-enforcement boundary, or keep all filesystem
   tools unavailable. Recommendation: keep unavailable until a worker
   mechanism passes the deterministic counterexamples without relying on
   cooperative locks or a final path check. This may require choosing an
   OS-sandbox approach despite the current deferral; it is not implemented
   here.
2. **Protected paths:** approve or amend the policy matrix, especially
   provider-source reads (per-invocation approval recommended) and whether
   settings are always unreadable. Recommendation: deny credentials,
   settings, Token Logger DB/WAL/SHM/backups, and special files for all generic
   filesystem operations; allow provider-source reads only with explicit
   one-time approval and keep their writes/deletes denied.
3. **Protected-path registry ownership:** decide which components supply
   actual credential/settings/source/database/backup identities to a future
   host registry. Recommendation: each owning component registers exact paths
   and object identities; no scanning, extension-based guessing, or path
   inference. Until Token Logger/provider-source owners can supply paths,
   roots overlapping unknown owner data remain ineligible.
4. **Root selection UX:** approve `/fs-root` absolute-path modal selection,
   current-conversation lifetime, reset cleanup, and root-change invalidation
   as described above. No root-selection UI is implemented pending this
   decision.

## Follow-up acceptance criteria

Before an `apply_patch` PR, a supported backend must prevent or fail closed on
the root, parent, and target relocation tests, including changes at the final
validation/effect checkpoint; reject late hard-link and symlink substitutions;
bind the exact approved action/resource/root generation; enforce the approved
protected-path registry across aliases and ancestor scopes; and report partial
or unknown mutation outcomes without replay.

Before a `delete_path` PR, a supported backend must additionally prevent
recursive child relocation from causing outside-scope effects, atomically
bind deletion to the exact prepared entry or fail closed on any name change,
distinguish removal of a link entry from its destination, and require fresh
per-invocation deletion approval. Both tools require native behavioral tests
on every platform claimed as supported. Unsupported platforms remain
unavailable; no fallback to unrestricted path I/O is permitted.

## Primary technical references

- Linux kernel [pathname lookup and rename handling](https://docs.kernel.org/filesystems/path-lookup.html)
- Linux [`openat2(2)`](https://man7.org/linux/man-pages/man2/openat2.2.html)
- Linux kernel [Landlock documentation](https://docs.kernel.org/userspace-api/landlock.html)
- Linux kernel [Landlock ABI and filesystem rights](https://docs.kernel.org/userspace-api/landlock.html#filesystem-flags)
