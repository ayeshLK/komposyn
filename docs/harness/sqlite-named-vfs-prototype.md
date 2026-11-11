# Named SQLite VFS feasibility prototype

**Status:** isolated Linux research only. No production Store integration,
driver decision, owner coverage, permission change, or filesystem-tool
availability change. Token Logger remains `unsupported` for broker coverage.

## What this prototype checks

This study runs a real SQLite connection through the pinned
`modernc.org/sqlite/vfs.New` adapter under a unique, non-default VFS name. It
creates only synthetic files inside a temporary directory and measures:

- A named-VFS read of a synthetic fixture.
- An attempted update refused by the adapter before the synthetic marker
  changes. The pinned adapter returns `SQLITE_NOMEM` when asked to open a
  main-journal role; that result is a refusal signal, not a useful writable
  policy API.
- A WAL-mode request that returns `delete` rather than selecting WAL; WAL and
  SHM fixture names are absent at pre- and post-operation inspections. This is
  an ordinary read-only refusal and does not show that an `xShmMap` hook ran or
  protected SHM.
- `NewBackup` with a plain destination, then with a destination independently
  selecting the named VFS. The plain control backup is produced and verified
  only inside the disposable fixture. The explicitly named destination and a
  fresh retry are refused, with their target paths absent at the before/after
  inspections.
- Closing all connections before unregistering the VFS.

The report records callback path names and fixture effects. The supported
adapter does not expose or register exact writable object descriptors, so no
write identity correlation or first-write barrier is claimed. Callback names
are observations, not file identities.

## Result and design boundary

Pinned dependency: `modernc.org/sqlite v1.61.0`, upstream source commit
[`4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738`](https://gitlab.com/cznic/sqlite/-/tree/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738), SQLite `3.54.1` in the
measured run. `vfs.New(fs.FS)` is documented and implemented as read-only. It
registers with `makeDflt=false`, leaving SQLite's default VFS selection
unchanged.

The generated `modernc.org/sqlite/lib` package exports translated SQLite
structs and VFS registration functions, but callbacks are represented as raw
`uintptr` values. The supported VFS package uses unsafe callback conversion,
object tokens, and target-generated layout details internally. It offers no
supported writable-wrapper constructor or safe generic trampoline for calling
an arbitrary native VFS callback. Depending on private `TunixFile` layout or
private generated callbacks in this research slice would bind correctness to
an unsupported ABI and platform. An adapter around native `xOpen` would also
need to establish which effects `xOpen` can perform before it returns. This
prototype therefore refuses to invent a positive writable result.

For SHM, generated Linux `_unixShmMap` reaches `_unixOpenSharedMemory` before
returning to SQLite. That code opens the path, initializes/locks it, can extend
it, and maps it `MAP_SHARED`. The adapter's public API offers no pre-native
access to that actual SHM object. This prototype does not call a custom
`xShmMap`, nor delegate to native `xShmMap` and inspect afterward. WAL stays
unavailable. A future implementation needs an owned SHM path or a separately
reviewed driver hook/fork before any WAL acceptance claim.

```text
synthetic fixture -> SQLite DSN ?vfs=<unique name> -> supported read-only VFS
                                                 |-> fixture read: succeeds
                                                 |-> write/journal request: refuses
                                                 `-> SHM exact-object hook: unavailable

named source connection -> NewBackup(destination URI)
                           |-> plain URI: independently uses default VFS
                           `-> named URI: read-only destination refusal
```

State/cleanup order exercised:

```text
create synthetic source with default VFS
  -> register unique non-default read-only VFS
  -> open/read/write-refusal/WAL-refusal probes
  -> backup plain control and named destination refusal + fresh retry
  -> close all connections and backup handles
  -> unregister VFS
  -> remove private fixture directory
```

Pins do not exist in this prototype because it performs no writable
registration. Retired-object lifetime and protected alias rules remain
unimplemented. The study proves neither support nor impossibility for a
maintained private adapter; it shows the supported public Go adapter is
insufficient for the required write contract.

## Run

From a clean checkout at the source commit being measured (the command checks
that `--commit` equals `HEAD` and refuses a dirty worktree):

```sh
go test -count=1 ./internal/research/sqlitevfsstudy ./cmd/sqlite-vfs-study
go run ./cmd/sqlite-vfs-study --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-sqlite-named-vfs-report.json
```

The command requires Linux and Go 1.26.11 or newer. It uses synthetic,
temporary database files and emits sanitized JSON to stdout. Context-aware
SQLite queries are bounded by a 30-second context. SQLite's online-backup call
is synchronous and has no context parameter; the probe limits it to a one-page
synthetic fixture. It is not an exhaustion or concurrency stress test. The
report contains no host fixture path or SQL payload.

The captured Linux/amd64 run is [recorded as sanitized JSON](sqlite-named-vfs-prototype-report.json).
Its `source_commit` is the exact clean source revision that the CLI verified
before running.

Classification meanings:

- `measured_pass`: the specific named-VFS read behavior ran successfully.
- `measured_refusal`: the operation was rejected and the reported fixture
  effect was checked.
- `measured_unprotected_control`: the plain backup destination succeeded
  outside named-VFS routing, by design as a synthetic control.
- `inconclusive`: the operation did not establish the expected refusal or the
  fixture evidence was not definitive.

For WAL/SHM and refused backup destinations, the classification requires
absence at both pre- and post-operation path inspections. Presence or an
inspection error is reported verbatim as `present` or `inspection_error` and
forces `inconclusive`. Before/after path observations do not rule out a
transient create/delete between inspections; the probe does not claim to detect
such effects or infer object identity from path state.

An error-free SQLite operation is not an enforcement pass. The command and
tests do not establish Token Logger compatibility, exact writable identities,
SHM interception, independent writer coordination, rotation protection, or
production safety.

## Next decision

Do not connect this adapter to Token Logger. Decide separately whether to
prototype a maintained platform-specific adapter against generated modernc
ABI internals, request/await a supported writable VFS and pre-SHM hook, or
evaluate a different driver. Any chosen route must first demonstrate exact
main/WAL object registration before sensitive writes and an actual SHM object
barrier before initialization/map; backup destination, metadata, verification
and Go-created schema snapshots remain separate routing obligations. A
positive main-file experiment cannot waive the SHM stop condition.
