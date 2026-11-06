# SQLite WAL/SHM feasibility for registered protected objects

**Research only; no Token Logger or production integration.** This study
evaluates whether the repository's pinned SQLite driver can expose and pin the
actual WAL and shared-memory objects before SQLite initializes, extends,
writes, or maps them. WAL/SHM remains unavailable to the read broker. The experiment does
not authorize access and does not change authority contract B: authority may
follow exposed objects, while mandatory protected-object rules continue to
deny protected objects through aliases and renames.

## Result

The positive ordering question is feasible in one narrow Linux/amd64
experiment: generated `xSetSystemCall` overrides observed SQLite opening the
synthetic `-wal` and `-shm` objects, duplicated and identity-checked those
already-open descriptors before returning them to SQLite, and held SHM open
at a deterministic checkpoint. A second connection reused SQLite's shared
node without another physical SHM open. A WAL transaction,
second-connection read, and truncating checkpoint completed, and retained
descriptors remained usable after SQLite closed.

This is **not a usable authorization hook**. In modernc v1.61.0 the override
table is package-global, uses private generated Go ABI details, and is not
scoped to the named VFS or database owner. It can intercept unrelated
modernc SQLite activity in the same process. SQLite documents these optional
system-call methods as testing/fault-injection interfaces whose behavior and
availability can vary; it does not provide the owner context or lifecycle
contract needed here. No implementation from this probe should be moved into
production. The next step requires a supported, owner-aware pre-SHM hook/API
or an explicitly maintained driver fork. Until that exists, fail closed.

## Provenance and reproduction

The probe source is commit
[`f592c9c627dea3518482265242ccdd3f4db05ab6`](https://github.com/ayeshLK/komposyn/tree/f592c9c627dea3518482265242ccdd3f4db05ab6)
in this repository. It used `modernc.org/sqlite v1.61.0`, generated source
commit
[`4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738`](https://gitlab.com/cznic/sqlite/-/tree/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738),
SQLite 3.54.1, Go 1.26.10, Linux/amd64, and kernel
`7.0.0-42-generic`. The machine-readable sanitized result is
[`sqlite-wal-shm-feasibility-report.json`](sqlite-wal-shm-feasibility-report.json).
It contains no host paths, fixture contents, credentials, or database data.

From a clean checkout of the exact probe commit:

```sh
git checkout --detach f592c9c627dea3518482265242ccdd3f4db05ab6
GOCACHE=/tmp/komposyn-go-cache go run ./cmd/sqlite-wal-shm-study \
  --commit f592c9c627dea3518482265242ccdd3f4db05ab6
```

The command verifies that `HEAD` is the supplied full commit and the checkout
is clean. It creates one synthetic database in a private temporary directory,
sets a 15-second overall context and bounded hook waits, closes the database,
then removes the fixture. It has no parameters for caller paths or SQL. It
does not access the repository's stores. The hook is installed only in this
standalone process and restored before exit.

The recorded run completed on 2026-11-06 at 10:36 UTC. The command runs as a
disposable standalone process (for example through `go run`); the raw
package-global hook is never installed in the application or test runner
process. It recorded both actual WAL and SHM descriptors before those open
calls returned to SQLite. At the pre-checkpoint gate, the pinned WAL object
contained 8,272 bytes; the matching SHM descriptor was mapped shared and grew
to 32,768 bytes. Both retained descriptors remained stat-able after SQLite
closed (the WAL had been truncated to zero by the measured checkpoint). The
synthetic transaction/readback/checkpoint and one physical SHM open across
two simultaneous connections passed. Denial/cancellation/fresh retry remain
`not_run`. This is measured driver behavior, not sandbox enforcement,
protected-resource policy, or a production read-broker result.

### Probe design

The probe uses a registered non-default VFS only to select the disposable
database, and overrides Linux `open` and `mmap` in the standalone process. At
each `fixture.db-wal` or `fixture.db-shm` open, it duplicates the actual
returned descriptor with `F_DUPFD_CLOEXEC`, checks `fstat` identity on both
descriptors, and stores the process-local object label. It pauses only the
SHM open return, then requires the later `MAP_SHARED` descriptor's identity
to match that retained SHM pin. A second bounded gate holds the worker after
the synthetic inserts and before checkpoint so the retained WAL pin can be
checked for actual bytes. Pins stay open until SQLite connections close,
then are checked and explicitly closed.

The only allowed operation is the fixture's SQL transaction/readback and
checkpoint. The probe aborts on unsupported hook calls, descriptor mismatch,
unexpected open size, missing mapping correlation, timeouts, failed readback,
or failed checkpoint. It does not implement an authorization denial. The
syscall callback is synchronous and can run while SQLite's Unix VFS mutex is
held; the test's main goroutine releases its fixture gates without re-entering
SQLite. This lock behavior is itself a reason the hook is not suitable for
owner policy or blocking authorization.

## Lifecycle reconstructed from pinned generated source

The Linux/amd64 build selects
[`sqlite_g_0000000000002880.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000002880.go#L445)
for `_unixShmMap`. On the first map for a `TunixFile`, it calls
`_unixOpenSharedMemory`. That function looks up the process-local
`TunixInodeInfo.FpShmNode` under the Unix VFS mutex. If no node exists, it
derives the `-shm` pathname, calls `_robust_open` (normally `O_RDWR|O_CREAT|
O_NOFOLLOW`, with an `O_RDONLY` fallback), stores the descriptor in the
`TunixShmNode`, then performs fchown and shared-memory locking before returning
to `_unixShmMap`. The `open` itself can create the sidecar entry before the
hook sees the descriptor; the experiment observed that zero-length metadata
state and reports it separately from later initialization and extension.

`_unixLockSharedMemory` can acquire the DMS lock and truncate SHM to three
bytes. Back in `_unixShmMap`, SQLite fstats the object, may extend it through
`_seekAndWriteFd`, and then maps it with `mmap(MAP_SHARED)`. Thus an xShmMap
wrapper that sees only the VFS API is too late to register the underlying
object before initialization/extension/mapping.

```text
SQLite WAL open (`_sqlite3WalOpen` -> xOpen `-wal`)
  -> open hook duplicates/fstats actual WAL descriptor before return
  -> SQLite WAL writes through that object
connection xShmMap
  -> process-local inode-info lookup under Unix VFS mutex
       -> existing shared node: increment ref; no physical open callback
       -> absent node: derive -shm; robust_open
            -> xSetSystemCall(open) experiment sees returned fd
            -> duplicate + fstat identity pin; pause before returning fd
            -> SQLite stores fd, fchown, DMS lock/truncate
  -> xShmMap fstat / optional extend-write
  -> xSetSystemCall(mmap) observes same dev+inode; MAP_SHARED
  -> WAL transaction / second connection may reuse node
  -> last unmap: optional unlink, munmap, close SQLite fd
  -> retained experiment pin remains usable until explicit close
```

WAL is separately created through `_sqlite3WalOpen` in
[`sqlite_g_000000000001feab.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001feab.go#L20865).
The exact selected implementation is linked above. Relevant locations include
`_unixOpenSharedMemory` around lines 322-421 and `_unixShmMap` around
445-574; `_unixLockSharedMemory` is in
[`sqlite_g_0000000000003f80.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000003f80.go#L24).
The SHM node fields (`FhShm`, refs and mappings) are in
[`sqlite_g_000000000001ffff.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001ffff.go#L312).
Unmap/purge and descriptor close are generated in
[`sqlite_g_000000000001feab.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001feab.go#L22406).
On final unmap, SQLite may unlink the sidecar before closing its descriptor.
The duplicate pin remains tied to the opened object, not to whether the
pathname still exists. The probe checks the duplicate after connection and
database close, then closes it. Native error/cancellation unwind paths were
not fault-injected; no denial or fresh-retry cleanup result is claimed.

These are source-derived orderings, not public API guarantees. They apply to
this exact generated Linux/amd64 driver build. WAL open and checkpoint paths
are separate source paths; a retained SHM fd does not by itself prove that
all related files are owner-registered.

## `xSetSystemCall` assessment

SQLite's [VFS interface documentation](https://sqlite.org/c3ref/vfs.html)
describes `xSetSystemCall`, `xGetSystemCall`, and `xNextSystemCall` as optional
methods used by some VFSes for testing/fault simulation. The available calls
can vary across VFS versions; clients must tolerate null methods and behavior
changes. This is not a stable authorization extension point.

In the pinned modernc source, the selected Unix implementation dispatches
through a package-global syscall table (`_aSyscall` and `FpCurrent`). Its
`_unixSetSystemCall` callback ignores the supplied VFS pointer and updates
that global table without a per-connection owner, role, or policy context.
The generated setter is at
[`sqlite_g_000000000001feab.go:22345`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_000000000001feab.go#L22345),
and the selected Linux/amd64 syscall table is in
[`sqlite_g_0000000000003fc0.go:2753`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000003fc0.go#L2753).
The experiment therefore used a non-default named VFS to select the fixture
database, but that name does **not** scope the syscall override. Other
modernc SQLite activity in the same process may be affected. Callback
signatures and generated structure layouts are private. The hook can execute
inside SQLite's Unix VFS mutex; policy callbacks must never wait on user/UI,
provider, store locks, or re-enter SQLite there.

The standalone process and synthetic fixture make the observed experiment
bounded; they do not fix these integration defects. A public hook should be
per-VFS and owner-aware, identify whether it is main DB/WAL/SHM and supply the
exact opened object descriptor before any mutation/map. Its callback must be
short, local, and non-reentrant. The API must define shared-node reuse and
provide a safe denial path. If it cannot run outside SQLite's global mutex,
it must explicitly constrain callbacks to nonblocking registration only.

## Driver and implementation options

| Option | Descriptor/order, ownership and semantics | Platform, backup/CLI, licensing and maintenance cost | Assessment |
| --- | --- | --- | --- |
| Pinned modernc v1.61.0 | A raw package-global hook observes synthetic WAL/SHM opens before descriptors return to SQLite. It does not identify connection/owner or safely authorize shared-node reuse. | Existing cgo-free `database/sql` driver and Token Logger backup behavior remain unchanged; upstream package is BSD-3-Clause. Generated ABI is platform-specific and unsupported. | Useful only for isolated measurement; reject for production. |
| Current modernc upstream | Latest inspected release [v1.61.1](https://gitlab.com/cznic/sqlite/-/tree/014351f110f66c8f485cff948fea608d7dcfafb1) (tag `014351f110f66c8f485cff948fea608d7dcfafb1`); inspected [repository snapshot](https://gitlab.com/cznic/sqlite/-/tree/49aabf7efd1ccfa8c49f92be257611265c590ebe). Supported Go VFS constructor remains read-only; no supported writable pre-SHM exact-object hook was found. | Lowest CLI/backup disruption if upstream adds the API. Upstream owns generated-platform support and security maintenance. | Request a supported callback/API; no claim upstream has committed to it. |
| ncruces/go-sqlite3 | Inspected [main snapshot](https://github.com/ncruces/go-sqlite3/tree/914b74b18bdf86d36eb03209665a31119f94ea53), release [v0.36.0](https://github.com/ncruces/go-sqlite3/tree/3994b44c1e91e7fb5681b2bd16c68ca448f660d5). Go VFS/shared-memory support exists, but the OS VFS SHM hook needed for external owner registration is package-private. | Its Wasm-based SQLite runtime uses more memory; changing drivers requires migration testing of SQL behavior, backup paths, VFS URIs and CLI builds. MIT license differs from current BSD-3-Clause dependency. It does not remove the need for an owner-aware OS-file boundary. | Driver migration alone does not satisfy the contract; any extension needs its own lifecycle and compatibility study. |
| Maintained fork | Could expose a per-VFS pre-use hook and define node reuse/close semantics. | KompoSyn would own patches, rebases, generated/platform changes, security updates, backup/CLI compatibility, tests and preserving upstream license notices. | Consider only after explicit maintenance/update policy approval. |
| Fail closed | Refuse Token Logger operations requiring writable WAL/SHM until supported owner-aware registration exists. | No driver or packaging change. | Current safe baseline; preserves protected-resource rules. |

The upstream snapshots above were inspected during this task; they are
research observations, not guarantees about later releases. No dependency or
production code was changed.

## Evidence matrix

| Question/scenario | Classification | Result and boundary |
| --- | --- | --- |
| Synthetic WAL transaction, two connections, second-connection read, TRUNCATE checkpoint | Measured pass | The fixture's initial row remained and two new rows were committed/read; checkpoint reported no busy writer. This is not policy enforcement. |
| Pin actual `-wal` fd before the open syscall returns | Measured pass | Fixture-relative `fixture.db-wal` opened at size 0; hook pinned the exact fd. It had 8,272 bytes at the pre-checkpoint gate, and the pin was stat-able after SQLite close (zero after TRUNCATE checkpoint). |
| Pin actual `-shm` fd before native use | Measured pass | Fixture-relative `fixture.db-shm` opened at size 0; hook duplicated/fstat-checked the descriptor while open was paused before return. |
| Same SHM object used by `MAP_SHARED` and grows | Measured pass | Dev/inode matched the retained pin at mmap; retained pin observed 32,768 bytes after SQLite close. |
| Shared SHM node reused by a second simultaneous connection | Measured pass, narrow | Two-connection workload succeeded with one physical `-shm` open observed. This does not invoke a per-connection callback or prove owner registration coverage. |
| Denial before return, cancellation, retry with fresh authorization | Not run | Only the allow path ran. No safe owner-aware authorization API exists; this test-only global hook is not treated as one. The original synthetic row remained and two inserts read back, but that is not a denial control. |
| WAL and SHM owner registration, protected-object rotation/alias rules | Not run | No actual auth/settings/Token Logger owner is connected; only synthetic DB used. |
| Concurrent namespace actor, revocation, stale result suppression, sandbox worker | Not run | Outside this focused driver-ordering experiment. |
| Other Go architectures, OSes, SQLite driver versions | Not run | No compatibility guarantee. |

The exact machine result is in the JSON report linked above. Its `measured_pass`
labels mean the named synthetic observation occurred. They do not mean
authorization, protected-resource policy, sandbox enforcement, or production
acceptance passed.

## Registration, pinning and unresolved ownership details

The measured descriptor is an object identity pin (device/inode plus an open
fd) obtained after the OS open and before the driver receives that fd. It is
not a pathname-containment guarantee, does not prevent writes through other
aliases, and does not freeze bytes. The probe has no production identity
registry, pin budget, generation or retirement protocol.

SQLite's process-local shared node is keyed by its Unix inode-info structure.
Subsequent connections can increment its reference count without reopening
SHM, so an owner protocol cannot equate “connection opened” with “SHM open
callback occurred.” It must register the owning store/policy before the first
connection can initialize the node and account for every connection that
reuses it. A pinned object must outlive SQLite's optional unlink and last-close
cleanup; retirement must wait until no active operation or stale publication
can use the identity. Pin exhaustion must fail closed.

The experiment does not determine safe lock ordering between Token Logger
owner locks, SQLite's Unix mutex, and a future policy registry. Owner rotation
would need to register/pin the new object before sensitive content is written,
keep the old object protected while any alias/connection remains reachable,
and retire pins only after SQLite and readers quiesce. The current callback
cannot safely perform that protocol: it has no owner identity and may run
under the native VFS mutex.

Identity pinning also cannot detect independent copies of sensitive bytes.
Protected-resource rules still need explicit owner registrations and a policy
for copies/exports; this probe neither discovers nor resolves such copies.

## Recommendation and maintainer decision

Keep WAL/SHM unavailable and continue failing closed. The measurement shows
that the *ordering point* exists in this exact modernc build, but the only
available hook is not a supported, owner-scoped authorization mechanism.
Request maintainer review of this narrow next step:

1. Should KompoSyn pursue a supported upstream modernc API, or explicitly own
   a maintained fork if upstream does not provide the needed hook?
2. Is the proposed API shape acceptable: owner/role-aware callback for the
   actual opened fd before fchown, DMS truncate, extension, or map; shared-node
   reuse notification; close/retirement semantics; and a documented
   nonblocking/no-reentry callback contract?
3. What update/compatibility burden is acceptable for such a fork, including
   architecture-specific generated implementations?

Do not proceed to Token Logger owner integration until the hook contract,
failure semantics, pin budget, rotation lock order, and lifecycle are reviewed.
No issue state or production availability is changed by this report.
