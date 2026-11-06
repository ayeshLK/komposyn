# Owner-aware SQLite exact-object pre-use hook contract

**Design only.** This document proposes a reviewable contract for a future
SQLite integration. It does not implement a driver hook, select a driver/fork,
change Token Logger, enable filesystem tools, or establish production
coverage. The approved object-authority B contract remains in force: authority
follows registered objects, protected resources remain protected through
aliases and renames, unknown participation refuses, and sensitive-object pins
last until policy-registry shutdown.

## Recommendation

Investigate the smallest upstream-supported modernc VFS hook that can admit
the actual opened object before use and account for shared-memory node reuse.
The repository pin `modernc.org/sqlite v1.61.0` has no supported writable
pre-use hook. The latest inspected release was v1.61.1, commit
[`014351f110f66c8f485cff948fea608d7dcfafb1`](https://gitlab.com/cznic/sqlite/-/tree/014351f110f66c8f485cff948fea608d7dcfafb1),
and the inspected upstream snapshot was
[`49aabf7efd1ccfa8c49f92be257611265c590ebe`](https://gitlab.com/cznic/sqlite/-/tree/49aabf7efd1ccfa8c49f92be257611265c590ebe).
Neither exposed the required supported writable owner-aware hook. This is a
source review, not upstream agreement or a commitment. No upstream contact
was made.

Do not use `xSetSystemCall`, exported generated symbols, or a package-global
override as the production mechanism. #240 showed that the raw override
changes modernc's package-global syscall table, lacks owner and connection
attribution, uses private generated ABI details, is not atomically restored,
and can run while SQLite's Unix VFS mutex is held. It is a measured ordering
probe only. Keep Token Logger coverage unavailable until a supported contract
and native acceptance exist.

## Terms and ownership

| Term | Meaning |
| --- | --- |
| **Owner** | A trusted host component, such as one Token Logger Store lifecycle. It is not inferred from a path, DSN, model argument, or VFS name. |
| **Owner binding** | A durable opaque, host-minted capability attached out-of-band to a driver connection/connector. It identifies the owner and route for that connection's whole lifetime; it is not permission to perform an operation. |
| **Route** | A named purpose within an owner: live database, backup source, backup destination, metadata, verification, or schema snapshot. Each route has its own connection/open path. |
| **Operation group** | One atomic, generation-bound reservation acquired before entering SQLite or direct file I/O. It declares every route and role needed by one logical operation, reserves their pin capacity together, and establishes reader quiescence once. |
| **Route lease** | A group-owned, single-use sublease for one declared route and driver call/connection. It is installed on the live connection only for that operation and is removed when the complete operation scope ends. It cannot reserve routes or advance generations by itself. |
| **Object admission** | The driver's synchronous pre-use callback with the actual open handle and role. It pins/registers that exact object or refuses before content use. |
| **Pin** | A retained host-owned handle and stable native identity record. It prevents identity reuse while retained; it does not freeze bytes or enforce pathname containment. |
| **Owner generation** | Revision of one owner's route/connection inventory. Rotation, route replacement, and shutdown invalidate older reservations. |
| **Policy generation** | Revision of registered protected routes and object identities. New registrations invalidate prepared broker reads and stale publication. |

Keep three layers separate:

1. **Upstream driver/VFS API.** Owns native open, VFS role, connection and
   SHM-node lifecycle. It exposes a supported exact-handle callback and
   explicit capabilities. It does not depend on KompoSyn permissions, UI,
   provider, runtime, or token-grant types.
2. **Core-owned neutral lifecycle.** If later accepted, Token Logger can
   accept a neutral operation-group/lifecycle interface around each logical
   operation and its routes. The core describes the operation and calls
   begin/finish; it does not make policy decisions or import harness
   packages. No public API
   signature is selected by this design.
3. **Host policy and sandbox composition.** The host mints owner bindings,
   checks inventory and authorization, closes broker admission, cancels and
   joins workers, owns pins/revisions, suppresses stale output, and reports
   cleanup. The native callback invokes only a fast, pre-authorized
   registration operation.

### Conceptual API

The following pseudocode describes the division of work, not an existing API
or a proposed source-compatible implementation:

```go
type RoutePlan struct {
    Route RouteKind
    Roles []FileRole // closed allowlist; unknown roles are refused
}

type OperationPlan struct {
    Owner   OwnerBinding // opaque; minted by trusted host composition
    Routes  []RoutePlan  // complete set; all reserved atomically
    Purpose OperationKind
}

type OwnerLifecycle interface {
    ReserveGroup(ctx context.Context, plan OperationPlan) (OperationGroup, error)
    BeginShutdown(ctx context.Context) (ShutdownCleanup, error)
}

type OperationGroup interface {
    // Subleases only declared routes; it cannot nest or widen the group.
    Lease(route RouteKind, connection ConnectionID, call CallKind) (RouteLease, error)
    Complete(EffectOutcome) error
    Abort(EffectOutcome) error
}

type RouteLease interface {
    // Installed out-of-band on a connection for one complete call scope.
    CallToken() CallToken
    Generation() Generation
    End(EffectOutcome) error
}

type OperationToken interface {
    Generation() Generation
    Action() OperationAction // normal call or one enumerated cleanup action
}

type ShutdownCleanup interface {
    // Issues only cleanup tokens for already-owned connections and pins.
    LeaseExisting(connection ConnectionID, action CleanupAction) (CleanupToken, error)
    Complete(EffectOutcome) error
}

type DriverHooks interface {
    BeforeFileUse(binding DurableOwnerBinding, token OperationToken,
        connection ConnectionID,
        role FileRole, opened BorrowedFile, disposition OpenDisposition) (ObjectPermit, error)
    BeforeShmAttach(binding DurableOwnerBinding, token OperationToken,
        connection ConnectionID,
        shm SharedMemoryObject, region RegionRequest) (ObjectPermit, error)
    BeforeFileClose(binding DurableOwnerBinding, token OperationToken,
        connection ConnectionID, object ObjectID) error
    BeforeShmDetach(binding DurableOwnerBinding, token OperationToken,
        connection ConnectionID, shm SharedMemoryObject, unlink bool) error
    BeforeDelete(binding DurableOwnerBinding, token OperationToken,
        role FileRole, entry OwnedEntry) error
}
```

The production-quality form must be per connector/VFS instance and
connection-bound, strongly rooted by the driver, and capability-reporting.
The lifecycle adapter installs only an opaque normal `CallToken` or
shutdown-scoped `CleanupToken` in driver-owned connection state before
entering SQLite; the VFS callback cannot reach back into the lifecycle
coordinator to acquire a lease or wait for a group.
`BorrowedFile` must expose a narrow identity query and a safe duplicate/pin
operation; the native connection retains ownership of its live handle. The
host receives the pin, not a general filesystem handle. `ObjectPermit` is an
opaque driver token bound to the exact identity, durable binding, call token,
and generation. It is not a
grant and cannot be fabricated by a path or model result.

`OpenDisposition` must report whether the actual handle was created or
reused, based on the native open operation rather than a later path check.
The callback receives role, durable route binding, connection identity,
native file identity, read/write/create flags, and the active group/lease
generation. A display path
may be diagnostic metadata but never authorizes access. Capability queries
must say which roles and SHM callbacks are implemented on the selected
OS/architecture. Missing callbacks mean unavailable, not fallback.

The neutral core boundary should ultimately let a trusted host wrap each
logical SQLite or Go-file operation with `ReserveGroup → route subleases →
Complete/Abort`.
The driver binding is supplied through a connector/configuration object, never
through `?vfs=` or caller-controlled URI fields. A future API should be
additive (for example, a new lifecycle-aware constructor); do not add a field
to the existing public `OpenOptions` without a separate compatibility review.
The exact Go surface remains a maintainer decision after upstream feasibility
is known.

### Durable connection binding and operation leases

The owner binding and operation authority have different lifetimes. A
connection receives one immutable durable owner/route binding at creation;
that binding remains until the native connection is fully closed. It carries
identity only. It cannot authorize file callbacks by itself. Every operation
on a retained `sql.Conn` or native connection must enter under a fresh route
lease from an active operation group. Before calling into SQLite, the
lifecycle adapter serializes access to that connection, installs the lease in
connection-owned driver state, and captures its owner and policy generations.
Native file and SHM callbacks read that active lease and refuse if it is
missing, ended, stale, for another route, or lacks the role. The adapter
removes the lease only after the full call scope has ended; it never reuses a
completed lease for a later call. Connection serialization prevents two
goroutines from replacing the active lease while SQLite is using it.

The group scope follows the complete logical `database/sql` operation, not
just a single native callback. Within it, every driver call gets a fresh
route lease:

- A non-transactional Exec owns a group through return. A Query owns one
  through `Rows.Close` or EOF, with new route leases for Query, each `Next`,
  and Close.
- A transaction owns a group from before Begin through Commit/Rollback; every
  statement and each Begin/Commit/Rollback call use fresh subleases from that
  group. Keeping the group open is
  intentionally conservative and keeps broker admission closed for that
  transaction.
- An online backup owns one group across source and destination connection
  creation, all `Backup.Step` calls, `Finish`, and both connection closes.
- Driver reconnect/reopen and connection close are lifecycle operations too.
  A lifecycle-aware connector/connection wrapper must acquire a group before
  any such SQLite call and pass a declared route sublease. The `database/sql`
  pool may not perform an unobserved native open or close outside this gate.

The lifecycle integration therefore cannot expose a raw `*sql.DB` or
`driver.Conn` path that bypasses operation entry. It must own pool operations,
including idle/expired connection retirement and error-driven discard. If a
pool path cannot be intercepted before SQLite is entered, that pool behavior
is unsupported and the owner route remains unavailable; a stale last lease
is never a fallback. Calls that return live rows or a transaction retain the
group until their terminal close/commit/rollback. Cancellation, row close,
failed begin, failed reconnect, and failed close all end/abort the lease and
group with an explicit outcome. Incomplete close retains the callback root
and pins as `CleanupIncomplete`.

No normal generation-changing transition may begin while a group is active.
Shutdown is the explicit exception: it closes admission, advances the owner
and policy fence, cancels the active group, and invalidates its normal tokens.
After its current native calls return, only fresh cleanup-only tokens may
settle that group's resources. For ordinary operation groups, an object
discovered by a callback may consume only a role and pin slot declared in
that group. It advances the group's acknowledged generation; the group
records that expected transition while global admissions are already closed.
Any unrelated policy/owner revision makes the group stale and causes
immediate refusal. The durable binding survives group completion, but the
next operation must install a fresh group lease before any SQLite call.

Nested Store helpers never call `ReserveGroup` while a group is active. They
borrow its route subleases. The outermost lifecycle entry declares a
conservative superset of routes/roles needed by its possible branch (for
example, Store initialization plus migration backup, metadata, and
verification). If that set cannot be known or bounded up front, split the
work into sequential groups: finish and close all connections/resources in
the first group, then reserve the next group based on its result. No nested
SQLite call may implicitly reopen broker admission or change generations.
Unexpected dynamic routes refuse and make the operation partial/unknown as
appropriate; the helper cannot widen the active group.

### Shutdown and cleanup-only authority

Shutdown is a distinct coordinator transition, not an ordinary operation
group. Its ordering resolves the otherwise conflicting rules that normal
admission closes at shutdown while active rows, transactions, and retained
connections still need terminal cleanup:

```text
Running -> Closing
  reject new ordinary groups; advance shutdown fence; close broker publication
  cancel active operations; wait for currently executing native calls
  issue fresh CleanupToken for exact owned connection/object set
  close rows -> rollback transactions -> abort/close backups -> close idle conns
  quiesce callbacks -> unregister VFS -> release callback roots -> close pins
  -> Closed

Any timeout/refusal -> CleanupIncomplete
  keep admissions closed; retain callbacks, live resources, and pins
```

1. Atomically set the owner to `Closing`, reject new ordinary groups, advance
   the owner/policy admission generations, invalidate unstarted call tokens,
   and close broker publication/admission. Keep VFS callbacks, connection
   bindings, and all pins rooted. Do not reopen generic read admission during
   shutdown.
2. Cancel active logical operations and request worker termination. Wait
   outside Store, coordinator, registry, and SQLite locks for all currently
   executing driver calls and native callbacks to return. A deadline here
   yields `CleanupIncomplete`; retain all roots and pins.
3. Once each connection is at a driver-call boundary, the shutdown
   coordinator may mint a **fresh cleanup-only token** for that already-owned
   connection and its currently pinned object IDs. It is not an old route
   lease and is never accepted for normal Exec/Query/Next, Begin, backup
   stepping, or a new connection. It permits only terminal actions such as
   `Rows.Close`, transaction `Rollback` (never Commit), abort/finish cleanup
   of an active backup, and native connection close. It cannot open a new
   route or add an unregistered identity.
4. Settle retained resources in order: close live rows; roll back active
   transactions; stop/close backup handles while classifying any copied
   destination as partial or unknown; then close idle connections. Every
   native call receives a new cleanup token bound to its connection, cleanup
   action, owner shutdown generation, and the pre-existing pinned identity
   set. No old normal token is reused, and no normal operation group is
   allowed to start.
5. Cleanup may perform only SQLite rollback and VFS close/unlock/unmap work
   on already admitted identities. It does not request a checkpoint or run
   ordinary SQL. If SQLite close requests deletion of a SHM/sidecar entry,
   the driver needs a dedicated pre-delete hook. That hook must verify the
   directory entry still names the registered ephemeral object under the
   reviewed deletion primitive. If it cannot safely establish this, refuse
   unlink, preserve the entry, and report incomplete cleanup; do not unlink a
   replacement by pathname. Any close-time request for a new/unpinned file or
   an unplanned write is refused and likewise leaves cleanup incomplete.
6. After every connection and mapping closes, wait for callback quiescence;
   unregister the VFS only then, release callback roots next, and close
   registry pins last. Successful shutdown requires confirmed native handle,
   mapping, and callback cleanup. A timeout or refused required cleanup
   operation retains live state and pins and reports `CleanupIncomplete`.

The cleanup token is a host-issued teardown capability, not a grant and not a
way to keep the conversation alive. Its allowlist is limited to owned
connection IDs, pinned object identities, and terminal cleanup actions. It
does not authorize content disclosure, new objects, regular mutations,
provider work, or renewed worker dispatch. A future implementation must
demonstrate that the driver's cleanup callbacks can enforce this distinction;
otherwise shutdown remains unavailable rather than falling back to a stale
normal lease or unrestricted close.

`OperationToken.Action()` is checked for each VFS method, not merely when the
connection enters SQLite. A normal call token allows only the operation kind
declared in its route lease. A cleanup token can permit the narrowly necessary
rollback reads/writes/truncates on already pinned DB/journal/WAL objects, row
and connection close, and SHM detach. It cannot authorize an application
statement, commit, new backup step, new connection, or an unplanned file role.
This is the trusted owner's shutdown path; it does not create or reuse a
model-facing permission grant.

The driver needs pre-close and pre-detach callbacks as well as pre-use
callbacks so the cleanup token is checked before `xClose`, SHM detach/unmap,
or its optional unlink. `BeforeDelete` is required for path-entry deletion
because unlink does not provide an opened file descriptor to the hook. Its
`OwnedEntry` must be tied to a pinned ephemeral object and a reviewed
identity-safe deletion primitive. A string path or prior `stat` is
insufficient. If the driver cannot provide such a primitive, it refuses the
unlink and reports a leftover-entry/cleanup outcome; it does not claim
successful sidecar cleanup.

## Source-pinned integration points

The repository pin is modernc v1.61.0, upstream source commit
[`4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738`](https://gitlab.com/cznic/sqlite/-/tree/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738),
SQLite 3.54.1. Links below are to that immutable source revision and selected
Linux/amd64 generated files.

### Ordinary file, main database, rollback journal, and WAL

The Unix VFS `_unixOpen` is in
[`sqlite_g_0000000000000480.go:26983`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000000480.go#L26983).
It can use a cached descriptor (`_findReusableFd` at
[`sqlite_g_0000000000002880.go:171`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000002880.go#L171));
fresh and reused handles both need object admission. The open path may apply
ownership metadata before an outer wrapper sees `xOpen` return, so a wrapper
around the public xOpen call is not sufficient for a strict pre-use contract.
The supported insertion point must be inside the backing VFS after the actual
handle and identity exist but before fchown, initialization, or any content
operation. A cached descriptor must run the admission check again before
being attached to a new connection.

`_unixWrite` and `_unixTruncate` use the `TunixFile.Fh` handle in
[`sqlite_g_000000000001ffff.go:12943`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001ffff.go#L12943)
and
[`sqlite_g_0000000000003fc0.go:8105`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000003fc0.go#L8105).
The object permit must be attached to that exact open object before these
methods can run. Reads, sync, file size, lock, fetch/unfetch and close also
need an explicit forward/refuse policy for every supported VFS role; unknown
or temporary roles must fail closed.

WAL is opened through `_sqlite3WalOpen` at
[`sqlite_g_000000000001feab.go:20865`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001feab.go#L20865)
and then opens a VFS file with the WAL role. That route needs its own object
permit before WAL writes. Checkpoint, recovery, close and reopen are separate
operations; a successful main-file xWrite wrapper does not establish WAL or
SHM coverage.

### SHM creation, attach/reuse, mapping, and cleanup

The selected `_unixShmMap` is
[`sqlite_g_0000000000002880.go:445`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000002880.go#L445).
On a first connection, `_unixOpenSharedMemory` looks up
`TunixInodeInfo.FpShmNode` under the Unix VFS mutex. If absent, it derives the
`-shm` name and opens it through `_robust_open`; it then stores the fd, does
fchown, and locks the shared object. `_unixLockSharedMemory` can acquire the
DMS lock and truncate to three bytes in
[`sqlite_g_0000000000003f80.go:24`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000003f80.go#L24).
Back in `_unixShmMap`, SQLite can extend the file with
`_seekAndWriteFd` and maps `MAP_SHARED` before returning. A hook at xShmMap
return is too late. An xOpen-only wrapper has no SHM handle at the necessary
point.

There are two distinct required events:

1. **Physical SHM object admission:** immediately after the native open has
   produced the actual SHM handle and identity, before fchown, DMS truncation,
   extension, or mapping. The hook reports whether it created or reused the
   object. Open/create itself may already have left a zero-length entry or
   other metadata; report that possible effect separately.
2. **Per-connection SHM attach/map admission:** every connection must be
   associated with the protected object before an existing shared node or an
   already-mapped region is handed to it. Existing `FpShmNode` reuse increments
   a ref and skips physical open, fchown, and DMS setup. Repeated map calls can
   reuse region pointers. A physical-open callback alone therefore cannot
   account for all participating connections or mapping uses.

The attach callback may verify a prior object permit and add the connection
association; it must not wait. If no registered pin, current operation-group
lease, or valid owner binding exists, the pointer is not returned and the
route remains unavailable. `TunixShmNode.FhShm`, references, and regions are defined in
[`sqlite_g_000000000001ffff.go:312`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001ffff.go#L312).
`_unixShmUnmap` can unlink, unmap and close the native fd on final cleanup in
[`sqlite_g_000000000001feab.go:22448`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001feab.go#L22448).
None of those events retires the host pin.

### Why the current public hooks do not meet this contract

The driver's `ConnectionHookFn` runs after `newConn` has set up the SQLite
connection; see
[`sqlite.go:725`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/sqlite.go#L725)
and
[`conn.go:73`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/conn.go#L73).
It is too late to govern the opens themselves. `vfs.New(fs.FS)` in
[`vfs/patches.go:223`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/vfs/patches.go#L223)
is a read-only filesystem VFS, not a writable OS-file admission API.

`_unixSetSystemCall` ignores its VFS parameter and changes the package-global
`_aSyscall.FpCurrent` table without a per-owner lock or connection context:
[`sqlite_g_000000000001feab.go:22345`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001feab.go#L22345),
table at
[`sqlite_g_0000000000003fc0.go:2753`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000003fc0.go#L2753).
SQLite's optional `xSetSystemCall` family is documented as testing/fault
simulation, and available calls can vary between VFS versions; see the
[SQLite VFS documentation](https://sqlite.org/c3ref/vfs.html). Naming a VFS
does not scope modernc's generated global syscall table.

## Two-stage coordination and lock order

A nonblocking native callback cannot discover a new identity and then wait
for active workers to stop. SQLite may hold its Unix VFS mutex and proceed to
fchown, truncate, extend, write, or map as soon as the callback returns. The
join must happen before entering SQLite.

```text
Stable(owner generation O, policy generation P)
  -> ReserveGroup(operation G; all routes/roles/pin slots declared together)
       close broker read/dispatch admission for affected coverage
       invalidate prepared-but-not-dispatched reader leases
       reserve one owner transition and capacity for every declared route
       publish route-deny while identities are not yet known
  -> Drain [outside every Store, coordinator, registry, and SQLite lock]
       cancel affected broker workers
       join them and suppress their stale output
       if deadline expires: do not enter SQLite; keep route unavailable
  -> GroupReserved(O, P, G, route bindings, pin slots)
  -> Install a fresh route lease for each entered driver call
       durable connection binding supplies identity; lease supplies current authority
  -> Enter SQLite/direct-file operation [no coordination lock held]
  -> Object admission callback [may run under SQLite's Unix mutex]
       verify G/O/P/route/role and available pre-reserved capacity
       duplicate + identify the actual handle; register/pin it quickly
       advance policy revision and G's acknowledged revision
       return object permit, or refuse immediately
  -> Effects only after object permit (open/create metadata may precede it)
  -> End each call lease; finish all routes in G as one unit
       commit new generation; reopen admission only after group completion
  -> Stable(O', P')
```

**Lock rules:**

- A per-owner transition slot (or the initial conservative global transition
  slot) serializes operation groups. Acquire/update it briefly, then drop it
  before cancellation, joins, SQL, file I/O, or waiting. Same-owner concurrent
  group preparation gets busy/stale and cannot enter SQLite. Distinct owners may
  proceed concurrently only after disjointness and global-generation behavior
  are proven; the first prototype should serialize identity-changing
  transitions.
- The registry mutex covers only bounded map/generation/pin-list updates. It
  never calls a Store, provider, UI, VFS, SQLite, worker, or filesystem scan.
- The worker coordinator closes admission and marks generations while holding
  its short state lock, then drops it before cancel/join. Workers' result
  publication checks the captured generation and suppresses stale bytes.
- No Store lock is held while beginning a route reservation or joining
  workers. No coordinator/registry lock is held while SQLite runs. There is no
  lock path from the native callback back to a Store or worker join. This
  prevents the cycle `Store -> SQLite mutex -> callback -> registry -> Store`
  and `SQLite mutex -> callback -> join -> worker -> SQLite`.
- The hook must be attached to a connector/connection object minted by trusted
  composition. A DSN path, `?vfs=` name, or exported string owner identifier
  alone is not an owner capability.

The operation plan must reserve every expected route, role, and pin capacity
before SQLite entry. There is one global admission close/drain and one group
generation for the complete logical operation; routes are not acquired by
nested `Reserve` calls. Registering a discovered object bumps policy
generation while admission remains closed. The active group records each
expected revision change it causes; an unrelated generation change makes the
group stale. The group commits only if all route bindings, owner generation,
role coverage, callbacks, and final registry generation still match. Readers
are re-enabled only after every route and connection in the group has
finished. The once-grant remains consumed by
the runtime at its existing dispatch boundary; the VFS hook never consumes,
creates, or widens grants.

For a main DB/WAL/SHM/journal identity whose initial registration is missing,
the conservative first implementation closes **all broker reader admission**
until the lifecycle transition completes. A narrower target-only drain would
need evidence that every prepared/open worker handle can be mapped to the
same object; hard-link aliases make pathname matching alone insufficient.
Registration of the host-owned path route before entry provides an early
deny while object identity is learned; it is not itself permission to read or
write. External processes that do not participate remain outside coverage.

### Stale state, newly discovered roles, and refusal

- A new/replacement object is admitted only if the owner prepared that role
  and a bounded pin slot declared in the active group. A fresh main DB target
  may be precreated and pinned by its owner, but the callback must still
  compare the exact SQLite-opened handle with that pin; precreating a sidecar or predicting
  a path alone is not the contract.
- An unexpected temp, super-journal, subjournal, unknown role, stale owner/policy
  generation, canceled group/lease, path/route mismatch, pin-budget
  exhaustion, or callback-registration failure returns a SQLite error before
  content use. An open/create or permission metadata effect may already have
  happened. Do not remove by pathname or call this unchanged; keep the owner
  route unavailable, retain any acquired pins, mark coverage incomplete, and
  report partial/unknown effects.
- If the discovered object would require a new worker join, the group
  was insufficient. The callback cannot perform that join. It refuses and
  leaves the route unavailable until an outside-SQLite transition safely
  retries under a newly acquired group. There is no automatic replay.
- Cancel/deadline before SQLite entry returns not-executed, after the drain
  is safely unwound. Cancellation after entry requests operation/connection
  termination through supported driver mechanisms, then awaits bounded
  cleanup outside SQLite locks. SQLite rollback or an error does not prove
  that sidecars, WAL, SHM, or bytes were unchanged.
- Policy registration/rotation does not authorize a previously prepared model
  read. Its generation check fails before dispatch or publication. No new
  policy callback may silently approve or widen a grant.

## Outcome, callback, and pin lifecycle

| Outcome | Meaning |
| --- | --- |
| `NotExecuted` | Group reservation failed/canceled before SQLite or direct file I/O began. No file effect was attempted. |
| `Unavailable` | Required role hook, platform capability, owner registration, group route/role slot, or complete participation is absent. No weaker path is used. |
| `RefusedBeforeUse` | Native open may have created an empty entry or changed metadata, but the callback denied content initialization/use. Report the metadata possibility separately. |
| `Partial` | A known subset of SQLite or snapshot effects occurred before failure/cancellation. Preserve observed effects and pins. |
| `Unknown` | Possible effects cannot be attributed or cleanup/termination is unconfirmed. Do not claim rollback or unchanged state. |
| `Succeeded` | The route completed, registrations and close callbacks settled, and the operation result is known. This still does not imply OS sandbox enforcement. |
| `CleanupIncomplete` | A callback, connection, worker, or native resource did not quiesce before the bounded deadline. Keep admission closed and retain callbacks, VFS state, and pins. |

These can be carried as an operation outcome plus an independent cleanup
status. SQL transaction failure does not substitute for lifecycle evidence.
Possible open/create metadata is not the same as sensitive page/WAL/SHM byte
effects; the report must distinguish them. Never automatically replay a
mutation with partial or unknown outcome. Any retry uses a newly acquired
operation group and a new owner/runtime decision.

Descriptor and memory ownership:

1. The driver owns the live native handle and borrows it to the callback. The
   callback checks identity and obtains a close-on-exec duplicate/pin; the
   host registry owns that pin. Verify the duplicate identifies the same
   native object before registration.
2. The driver roots callback closures and VFS/native memory from registration
   until every connection, SHM attachment, mapped region, operation, and
   callback has drained. Failed partial installation unwinds in reverse order
   only after proving no native callback can still run. Do not free a Go
   callback target merely because `Close` was requested.
3. Bound owner route entries, operation groups, route leases, object pins,
   callback events, and native resources. Reserve capacity before entering
   SQLite.
   Never evict old pins to admit new ones. Any budget exhaustion refuses and
   leaves coverage unavailable.
4. Store every protected object pin until **policy-registry/conversation
   shutdown**, including objects renamed, unlinked, checkpointed, closed, or
   replaced. SQLite close, unmap, unlink, worker join, and owner rotation are
   not pin-retirement permissions. No early-retirement optimization is
   proposed.
5. Shutdown follows the ordered cleanup-only protocol below: close ordinary
   admission and invalidate generations, settle active resources with fresh
   cleanup-only tokens, close remaining connections and mappings while
   callbacks remain rooted, confirm callback quiescence, then release callback
   roots and finally close policy pins. On timeout, report
   `CleanupIncomplete`; retain live state and pins instead of
   unregistering/freeing underneath a callback.

The pin binds an object identity only. It does not freeze contents, enforce
live pathname containment, discover independent copies of sensitive bytes,
or control external writers. Complete coverage requires authoritative
participating owners; otherwise the affected route stays unavailable.

## Token Logger routes and compatibility

The SQLite hook is one layer, not complete Store coverage. Current source
paths require operation groups and route-specific durable connection bindings:

| Route | Current behavior | Hook/lifecycle requirement |
| --- | --- | --- |
| Store open/configuration/migration | `storage.go` resolves the caller path, runs schema preflight before `sql.Open`, sets one connection per Store, changes journal mode to WAL, migrates, and syncs pricing. | Each outer lifecycle operation reserves one group for all known routes/roles before its first connection/open. Bind the durable owner before `newConn`; register main DB and each journal/WAL/SHM actual object before content use. A retained Store connection gets a fresh operation lease for later calls. Same Store close does not retire pins. |
| Schema inspection | `schema.go` opens/hashes the source main/WAL/journal with Go file I/O, copies them into `MkdirTemp`, then opens the copy through SQLite; it intentionally does not copy SHM. | A SQLite VFS cannot protect the Go opens/copies. The enclosing group declares source handles, private output, and the snapshot SQLite route. An exact-handle host boundary pins/registers source and destination before copied bytes; SQLite opens only the registered snapshot. Retain pins through registry shutdown even after `RemoveAll`. |
| Migration backup creation | `backup.go` precreates a same-directory or explicit destination via Go (`CreateTemp` or `O_EXCL`), then `NewBackup` opens SQLite destination. | Register/pin the exact Go-created destination; driver callback must verify the SQLite-opened destination is the same object, not just the same path. Source and destination are declared together in one group, with distinct route subleases and pin slots. |
| Online backup source and destination | `NewBackup(dstUri)` constructs an independent destination `newConn(dstUri)`; source binding does not automatically propagate. | The driver API must accept explicit source and destination owner bindings/connectors. One group is reserved before either connection opens and remains active through backup stepping, Finish, and both closes. Missing destination identity or unsupported roles refuse before backup page writes. Preserve online backup's consistent copy semantics. |
| Backup metadata | `writeBackupMetadata` separately opens the backup path and writes SQL. | A separate operation group/route binding verifies the exact existing backup identity and covers any WAL/SHM/journal sidecars. |
| Backup verification | `verifyMigrationBackup` independently reopens the backup read-only. | A separate read operation group/route binding registers the reopened identity; read-only open may still touch WAL SHM state. |
| Backup error cleanup | Incomplete backup cleanup currently removes the main path; sidecars may remain. | Cleanup is a declared sub-operation in the active group or a new cleanup group after the failed group has settled. It must use an exact owned identity where possible; pathname removal is not a safe delete primitive. Preserve verified pre-migration backups and never delete a substituted or existing destination. Classify leftover sidecars/cleanup uncertainty. |
| Later reopen/checkpoint/recovery | SQLite can checkpoint, remove, recreate, or reopen WAL/SHM; different Store instances/processes may share the DB. | Each permitted connection binds an owner; reuse and reopened identities are admitted. Retain all earlier identities. Unknown external Store/process writer participation keeps coverage unavailable. |

Core should continue owning Token Logger schema and backup invariants. It must
not import harness permission/runtime/sandbox packages. A future neutral
optional lifecycle may wrap these operation groups; the host supplies
policy/registry callbacks. Existing `Open`, `OpenWithOptions`, migrations,
online backup behavior, schema version, and one-way dependency direction
remain unchanged in this design. New public constructor/connector signatures
require separate compatibility review. The standalone `komposyn` CLI and
`komposyn-lg` behavior are not changed here.

## Driver and mechanism options

| Mechanism | Exact hook and SHM behavior | Maintenance, compatibility, and status |
| --- | --- | --- |
| **Recommended: upstream-supported modernc API** | Add a per-VFS/per-connector binding to the Unix file-open path after actual handle/identity acquisition but before fchown/content effects; invoke for cached-handle reuse. At `_unixOpenSharedMemory`, admit actual SHM fd before fchown/DMS truncate/extension. Add per-connection SHM node attach and pre-map callbacks for new, reused, and already-mapped regions. Report exact supported role/OS/architecture capability. | Preserves current modernc `database/sql`, online backup and CLI dependency if upstream accepts/maintains it. Upstream owns generated platform implementations and security updates; no hook currently exists and no commitment is implied. Current modernc SQLite is BSD-3-Clause. |
| Maintained patch/fork | Can implement the same insertion points and connection/destination binding if upstream cannot. | KompoSyn owns rebases, security/CVE response, generated OS/architecture code, tests, releases, backup/CLI compatibility, and license notice preservation. Requires explicit maintainer decision before adoption. |
| ncruces/go-sqlite3 | Its Go VFS API is public, but the inspected [current source](https://github.com/ncruces/go-sqlite3/tree/d816be0acbb77033425bb2550ad4d7b1c767b83e) still has package-private OS SHM lifecycle methods; a driver swap alone cannot add host owner admission before its SHM open/map. Its VFS uses mmap on Unix and `MapViewOfFile` on Windows. | Separate Wasm-based driver/runtime and VFS locking/WAL semantics, higher memory, backup and CLI migration tests, and MIT rather than BSD-3-Clause licensing. An upstream extension or fork is still needed; not the first route. |
| Continue fail-closed | No object becomes readable by a generic tool through Token Logger registration. | Current safe state. Keep Token Logger broker coverage unsupported until exact routes and native evidence meet the acceptance matrix. |

The ncruces assessment is pinned to the current inspected source, not a
general claim about later commits. Its [VFS documentation](https://github.com/ncruces/go-sqlite3/blob/d816be0acbb77033425bb2550ad4d7b1c767b83e/vfs/README.md)
describes its OS shared-memory implementation; implementation details are in
[`api.go`](https://github.com/ncruces/go-sqlite3/blob/d816be0acbb77033425bb2550ad4d7b1c767b83e/vfs/api.go#L189-L215),
[`file.go`](https://github.com/ncruces/go-sqlite3/blob/d816be0acbb77033425bb2550ad4d7b1c767b83e/vfs/file.go#L84-L141),
and [`shm_ofd.go`](https://github.com/ncruces/go-sqlite3/blob/d816be0acbb77033425bb2550ad4d7b1c767b83e/vfs/shm_ofd.go#L28-L110).
No dependency was changed and no upstream contact occurred.

## Evidence status

| Evidence | Already measured | Does not establish |
| --- | --- | --- |
| #239 merged writable object-barrier study | Linux/amd64 generated-ABI prototype bound main DB and rollback-journal descriptors before observed xWrite; refusal/fresh success and separate backup routes were tested on synthetic fixtures. The report's implementation evidence revision is `809fe100e69902626cfb1009f2685d9b726b275d`; merge is #239 at `4bb13c6e98a956b068268adc9bbc78fb9832ecdf`. | Supported API, WAL/SHM, Token Logger owner lifecycle, cross-platform guarantee, live worker coordination. |
| #240 merged WAL/SHM ordering study | Linux/amd64 probe observed/pinned actual WAL and SHM descriptors before native use, correlated SHM to a shared map, exercised two connections/shared-node reuse, checkpoint and retained pins. Its measured implementation source was `ecc20a5b0d02fee104eba2f0565ed4a4488e9bdc`; merge is `64d8d0db9aa21a3cfbd705378b023e21901c868a`. | Supported owner-aware API, denial/cancellation/fresh-retry enforcement, owner rotation, authoritative Store inventory, worker joins or sandbox enforcement. The raw package-global hook is unsuitable for authorization. |
| Current source review | Exact modernc v1.61.0 Linux/amd64 insertion/reuse/cleanup paths and current public API were inspected; modernc v1.61.1 and ncruces current snapshots were checked read-only. | Runtime measurement of any future API or behavior on other OS/architectures. |

The existing core owner review in
[`read-broker-tokenlogger-lifecycle.md`](read-broker-tokenlogger-lifecycle.md)
records the independent Go snapshot, backup, metadata, verification, and
reopen paths. There is no worker archive or native Bubblewrap result for this
future contract. Source inspection, callback injection in #239/#240, actual
synthetic SQLite I/O, and OS sandbox enforcement remain separate evidence
categories.

## Proposed acceptance matrix for a later prototype

These are **proposed**, not run by this design PR. Every native case uses
disposable fixtures, exact object IDs, deterministic entered/release barriers,
and validates actual fixture bytes/sidecars rather than process exit alone.

| Area | Acceptance case and required observation |
| --- | --- |
| Main DB | Existing and newly created main DB; exact live/cached handle admitted before read/write; path replacement yields identity mismatch and refusal; create metadata distinguished from page writes. |
| Rollback journal | Existing/new journal and recovery; callback precedes journal bytes and main-page mutation; denied registration preserves source bytes but reports any open metadata. |
| WAL | Existing/new/reopened WAL; exact identity admitted before first frame write/truncate; checkpoint and recovery use same registered object; a failed SQLite transaction is not treated as proof of no WAL effect. |
| SHM create | Hold the actual-open callback before it returns; assert identity pin precedes fchown/DMS truncate, extension, and `MAP_SHARED`; correlate map fd and shared mapping with that pin. |
| SHM reuse | Two connections, repeated region mapping, existing shared node and already-mapped region; each connection attaches to the registered owner/object without requiring a second physical open. Stale/missing attachment refuses before returning a pointer. |
| Owner binding | DSN path/VFS name cannot impersonate another owner; source, destination, metadata, verification, and snapshot connections each require the correct opaque binding. Unknown owner/role refuses. |
| Worker coordination | Hold a broker read worker; group reservation closes admissions, invalidates prepared work, cancels and joins it before the SQLite callback's `entered` barrier. A prepared-but-not-dispatched stale lease cannot start after the generation change. |
| Retained connection lifetime | Open a connection under a group and complete it while retaining the connection. A later operation must acquire a new group and install a new lease on that same connection; the old lease and missing-lease callbacks refuse. Close the retained connection under a separately acquired close group. Cover Exec, Rows.Next/Close, Tx Commit/Rollback, reconnect, pool discard and shutdown. |
| Nested lifecycle helpers | Run initialization with schema inspection, snapshot creation, migration, backup, metadata, and verification helpers. Each helper borrows declared subleases from the outer group; there is no nested `ReserveGroup`. If a route was not declared, the helper refuses before entering that route. Verify sequential groups only begin after all connections from the previous group are closed and its admission transition completed. |
| Idle shutdown | With an idle retained connection, close ordinary admission and advance generations, then close it successfully using a fresh cleanup-only token scoped to that connection and its pinned objects. Verify normal tokens refuse, cleanup does not reopen readers, and callback roots/pins release only after confirmed close/quiescence. Substitute a SHM directory entry and verify cleanup refuses the unlink without deleting the replacement. |
| Active-resource shutdown | Hold a Rows stream and a transaction at deterministic barriers. Shutdown rejects new normal work, cancels the operation, waits for its current driver call, then uses fresh cleanup-only tokens for Rows.Close and Rollback before closing the connection. Confirm no Commit/query/backup-step is admitted. Repeat with a backup mid-copy and verify its destination is classified partial/unknown. Force timeout and verify `CleanupIncomplete` retains callbacks, connections, mappings, and pins. |
| Concurrent groups | Same-owner concurrent group preparation is serialized or refused before SQLite entry; stale generations cannot reuse old leases. Distinct-owner concurrency is tested only if disjoint route guarantees are defined. |
| Cancellation/refusal | Cancellation during group drain prevents entering SQLite (`NotExecuted`). Callback refusal after open reports possible empty entry/metadata as partial; no page effect is claimed without fixture inspection. Fresh attempt requires a new group and new decision; no automatic replay. |
| Rotation/aliases | Register/pin replacement before writing sensitive bytes; old pin remains until registry shutdown after path rename/unlink; aliases of old object remain protected. Registration failure keeps route unavailable. |
| Error cleanup | Partial VFS install, failed callback, SQL error, cancellation, backup failure, `xClose`, checkpoint, and shutdown. Exact observed bytes and sidecars classified; backup sentinel remains intact on refusal. |
| Callback/GC lifetime | Force GC during opens and calls; retained callback target does not crash. Shutdown while a callback is held returns bounded incomplete cleanup without unregistering/freeing live callback or pins; release then allows successful teardown. |
| Pin budget/inventory | Reserve enough slots before SQLite entry; budget exhaustion refuses early. Unexpected extra role after open yields partial/unknown and coverage remains unavailable; old pins are never evicted. Missing external-owner participation refuses. |
| Backup group success control | Atomically reserve source and destination routes and capacities before opening either connection. Under conservative global-drain mode, perform a real online backup with both route subleases from that one group, step to completion, finish and close both connections before reopening broker admission. No nested `ReserveGroup`, stale source lease, or premature admission reopen is allowed. Use distinct source bytes and a destination sentinel; verify actual replacement and metadata. |
| Snapshot group | Atomically declare source handles, private temporary output, and the snapshot SQLite route before copying. Use subleases from that group; no nested reservation or copy before source/output admission. SQLite opens only the registered snapshot; unlink does not retire its pin before registry shutdown. |
| Sandbox separation | Later native worker acceptance independently demonstrates descriptor delivery, revocation/join, stale-output suppression, and exact output publication. No SQLite callback or unit test substitutes for OS sandbox evidence. |

## Decisions and staged next slice

The approved object authority, protected routes, non-discovery inventory,
runtime once-grant ownership, and registry-shutdown pin lifetime are not
reopened here.

| Maintainer decision | Recommendation | Tradeoff |
| --- | --- | --- |
| Upstream API or fork | First request design/review of a supported modernc API; if unavailable, keep fail-closed until a separate explicit fork decision. | Upstream route minimizes KompoSyn security/platform maintenance; a fork offers control but makes KompoSyn responsible for rebases, generated builds, CVEs, and releases. |
| Operation-group breadth | Start with one serialized owner transition and global broker-worker quiescence. Atomically reserve all routes/roles for a logical operation (including source+destination backup routes); keep one group active through the operation's full lifetime. Narrow only with measured alias/worker coverage. | Conservative pauses can span a transaction, open rows, backup, snapshot, reconnect, or close. This can reduce read availability, but avoids nested reservation and stale-connection gaps. |
| Hook capability baseline | Linux/amd64 is the first prototype target only. Each other OS/architecture must report unavailable until its handle identity, SHM mapping/locking and cleanup behavior are implemented and natively exercised. | No compatibility claim from compilation alone. |
| Core neutral lifecycle | Review an additive, host-implemented lifecycle around every distinct file route after driver binding is concrete. Do not add it in this design PR. | Premature core API risks codifying a hook that cannot cover `NewBackup` or Go-created snapshots. |

1. **Contract review:** approve/revise the roles, two admission callbacks,
   durable binding versus operation lease, atomic multi-route groups,
   no-wait callback rules, errors, and pin lifetime.
2. **Mechanism decision:** confirm the upstream-supportable modernc hook is
   feasible to propose, or explicitly choose continued refusal/fork
   evaluation. No upstream outreach is authorized yet.
3. **Isolated contract-validation prototype:** only after separate approval,
   validate deterministic worker join, exact identity admission, SHM reuse,
   cancellation/refusal, and safe fresh retry on synthetic stores.
4. **Token Logger integration:** route every Store/backup/metadata/verify/
   snapshot path, preserve schema and verified-backup behavior, and keep
   coverage unsupported until all routes pass.
5. **Owner composition and runtime/sandbox:** authoritative owner registration,
   revocation, native worker evidence and separate later tool enablement gates.

The next code slice should be the separately authorized isolated
contract-validation prototype, scoped to one synthetic Store and one Linux
target after a supported API or prototype seam is chosen. It must not change
production Store behavior or enable tools.
