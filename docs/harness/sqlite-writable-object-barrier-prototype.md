# SQLite writable object-barrier prototype

**Experimental research only.** This study uses the pinned
`modernc.org/sqlite v1.61.0` generated ABI on Linux/amd64. It is not a
production VFS, Token Logger integration, or authorization implementation.
Filesystem tools remain unavailable. The generated callback representation
and structure layouts are implementation details with no upstream stability
guarantee.

## Boundary

```text
SQLite named-VFS xOpen
  -> default Linux Unix VFS opens its native file object
  -> inspect TunixFile.Fh from that actual object
  -> fstat + F_DUPFD_CLOEXEC pin, register identity
  -> explicit bounded gate decision while xOpen is blocked
  -> return wrapper file only on allow
  -> each xWrite/xTruncate verifies both live FD and pin identity
  -> delegate only after identity match
```

The adapter is registered under a generated non-default name and never
replaces SQLite's default VFS. A fixture-owned gate sees `{sequence, role,
fixture-relative name, device, inode}` and returns allow or deny. It receives
no database bytes, grants, credentials, or model arguments. The callback runs
without the adapter mutex. Each gate call has a five-second context within a
30-second adapter lifetime; the trusted test gates honor cancellation. At
most 128 pins and 4096 events are retained. Pin-budget exhaustion refuses
the open. Event overflow marks evidence incomplete and cannot count as a pass.

## ABI and ownership

The code is built only for Linux/amd64 and refuses startup unless the pinned
generated types match: `TunixFile` size 120 bytes, `Fh` offset 24,
`Tsqlite3_vfs` size 168, `Tsqlite3_io_methods` size 152, and the default
Unix VFS file size and required callbacks match. The implementation uses the
same raw callback-slot conversion pattern as the generated RBU wrapper in the
pinned dependency (`modernc.org/sqlite` source commit
`4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738`, generated RBU wrapper in
`lib/sqlite_g_0000000000003a00.go`). The isolated assembly helper contains the
single C-allocated `uintptr` to typed-pointer bridge needed by this ABI; it
does not provide general FFI or host access. No dependency source is copied or
modified, and module checksums remain unchanged.

The native descriptor is taken from the actual object SQLite opened, then
duplicated with `F_DUPFD_CLOEXEC`. Device/inode identity is checked from both
the live native FD and retained pin before each delegated write/truncate. The
pin keeps that inode from being reused while the adapter lives. It does not
freeze contents or prevent writes through another descriptor to the same
object. It also cannot discover independent copies of sensitive bytes.

Native `xOpen` runs first. It may create a zero-length main/journal entry and
metadata before the adapter can inspect the descriptor; this experiment
therefore gates subsequent `xWrite` and `xTruncate`, not all filesystem
effects. On gate refusal, timeout, identity mismatch, or unsupported role,
the native object is closed and the open is rejected. Failed-open cleanup
errors are reported in events. Denied identities remain pinned until shutdown
unless the bounded pin budget is exhausted before retention.

The caller must close all SQLite connections and quiesce every goroutine that
could begin an SQLite open before `Adapter.Close`. The adapter checks its
tracked open-file and xOpen counts, but those counters do not make a concurrent
SQLite VFS lookup safe against shutdown: SQLite can obtain the raw VFS pointer
before the callback increments the count. This precondition is part of the
fixture lifecycle. After quiescence, Close unregisters the named VFS and closes
retained pins.

## Supported subset and refusal rules

- Admit `SQLITE_OPEN_MAIN_DB` and `SQLITE_OPEN_MAIN_JOURNAL` only.
- Refuse `SQLITE_OPEN_DELETEONCLOSE` before calling the native backend, so a
  close-triggered pathname deletion cannot bypass the explicit `xDelete`
  refusal.
- Tests use rollback-journal `PERSIST` mode. The positive transaction showed
  writes to both exact objects and read back the committed synthetic value.
- `xDelete` always returns `SQLITE_IOERR_DELETE`: a pathname callback cannot
  identify and unlink an exact object. PERSIST avoids deletion in the tested
  transaction and online backup; deletion is not enabled.
- The I/O-method table is version 2 to expose an explicit `xShmMap` refusal
  that clears the output mapping and returns `SQLITE_IOERR_SHMMAP`. It rejects
  `SQLITE_OPEN_WAL` before calling the default VFS. The native fixture records
  its actual WAL negotiation reaching that xOpen refusal without creating a
  SHM file. A separate method-table invocation verifies the installed
  xShmMap slot clears its output and refuses; the code never calls native SHM
  initialization or mapping.
- Temporary, subjournal, super-journal, transient, WAL, unknown and missing-FD
  roles are refused, including combinations of multiple file-role bits.
  `xFileControl` returns `SQLITE_NOTFOUND`; I/O methods are version 2 to expose
  explicit SHM callbacks, while xFetch/xUnfetch remain zero. The xShmMap
  callback refuses before native SHM initialization/mapping. No mmap or
  path-only delete is delegated.

## Measured fixture results

Run the bounded synthetic study:

```sh
go run ./cmd/sqlite-write-barrier-study --commit "$(git rev-parse HEAD)"
```

It prints sanitized JSON to stdout and creates/removes only temporary fixture
databases. Callback events carry process-local object labels and no host paths,
SQL or database contents. Run regression tests with:

```sh
go test -count=1 -v ./internal/research/sqlitewritebarrier
```

The committed [sanitized native fixture report](sqlite-writable-object-barrier-report.json)
was generated from source commit `2a739dc831e9d912ddba5e1334987734ffd3ceaa`.
It records Linux/amd64, Go 1.26.10, modernc SQLite v1.61.0, SQLite 3.54.1,
ordered callback events and per-case classifications.

The study and tests demonstrate:

- Main DB and rollback journal each receive an explicit registration before
  the first observed `xWrite`; all positive writes match both the live FD and
  retained pin identity, and the committed value reads back.
- Denying journal registration stops the transaction before observed writes;
  the synthetic database value remains unchanged. Native open metadata or an
  empty entry may still have changed. A distinct fresh fixture and new
  decisions then complete successfully; the refused operation is not replayed.
- A deliberate identity mismatch, unknown file token and cancellation fail
  closed. Shutdown closes retained pins. Path-only deletion is refused.
- modernc's online-backup API routes both source and destination independently
  through explicit named-VFS URIs. Backup success requires a write/truncate
  callback correlated to the registered destination object and source-specific
  content replacing a distinct destination sentinel. A denied overwrite is
  measured only after the source is registered and the destination's matching
  registration request is refused; prior bytes must remain identical and no
  destination write callback may occur. A separate fresh destination then
  succeeds with the same content and callback requirements.
- WAL-role callback refusal occurs before delegation to native `xOpen`, and
  the native PRAGMA fixture reached that refusal without creating SHM. The
  separate xShmMap method-table callback also refuses and clears the mapping
  output; this does not claim a successful WAL workload.

The JSON report labels cases as measured passes/refusals or observed
violations, and includes ordered callback events. A report with incomplete
events cannot be a pass.

## Limitations and next decision

This is object binding for selected callbacks, not strict live host-path
containment. It does not cover independent namespace actors, external
processes, data mutation through aliases, hard-link discovery, path deletion,
all SQLite file roles, WAL/SHM, Token Logger backup verification/reopen paths,
Go-created schema snapshots, other architectures, or Bubblewrap worker
authorization. PERSIST `xTruncate` is guarded but was not observed as a
positive effect in these fixtures. Metadata effects before `xOpen` returns
are outside the byte-write barrier.

The evidence supports continuing this isolated ABI feasibility work, not
production adoption. Before expanding scope, review whether this unsupported
generated-ABI approach is acceptable for further experiments, and separately
decide the SHM/WAL and exact deletion mechanisms. Do not infer production
read/write authorization, owner inventory completeness, or Token Logger
coverage from this prototype.
