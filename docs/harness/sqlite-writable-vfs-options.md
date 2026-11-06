# Writable SQLite VFS integration options for protected objects

Status: research only; refs [#215](https://github.com/ayeshLK/komposyn/issues/215). No production driver, owner API, registration or tool availability changes. Real Token Logger coverage remains unsupported. This refines the [lifecycle audit](read-broker-tokenlogger-lifecycle.md) without changing its evidence.

## Recommendation

A no-fork named writable VFS is **unsupported but prototypable**, not demonstrated and not proven to require a fork. The pinned driver has a named VFS selector and exposes translated SQLite structures and `sqlite3_vfs_register`, but no supported public Go writable-VFS constructor. A separate package could explore the generated ABI and callback conventions; using it as a production contract would create a version- and platform-specific dependency on unsafe implementation details. A wrapper around the native VFS cannot establish the required exact SHM registration merely by intercepting `xOpen` and `xWrite`: the Linux native implementation opens and initializes SHM inside `xShmMap`.

The smallest decisive research slice is a disposable, **non-default named** VFS probe against the pinned driver, first wrapping main/WAL opens and separately proving or falsifying exact SHM access *before* native initialization. Abort/refuse WAL if that cannot be done without unreviewed native internals. Test one independent backup destination and refusal/cleanup; do not call this Token Logger coverage. A supported upstream API could reduce Go callback maintenance, but it would still need a concrete SHM solution. An alternative driver is a separate option, not an implicit dependency decision. Until every required path is proved, retain the current fail-closed baseline.

### Scope and provenance

Baseline `432be6177142e23319412065088be80efc6d9c1c` (merged #236); Go module `modernc.org/sqlite v1.61.0`, upstream commit [`4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738`](https://gitlab.com/cznic/sqlite/-/tree/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738). The approved [object authority and exact-file owner contract](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377) applies; exact registrations and route checks remain mandatory and incomplete directory coverage refuses. Retired sensitive identities remain pinned until registry shutdown. No external host process is presumed to participate. The [earlier native broker results](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6574431158) are not VFS evidence.

The source findings below use the exact pinned module from the local Go module cache; generated source is platform-specific. Native execution is limited to the named read-only VFS/backup probe below on Linux/amd64, Go 1.26.10 and SQLite 3.54.1. No writable VFS, SHM interception, failure recovery, CLI compatibility, cross-compilation or OS sandbox test was run. Public symbols are evidence of technical access, not an upstream compatibility promise.

## Option matrix

| Option | Specific surface and assessed status | First write / SHM / backup | Cost, platforms and disposition |
| --- | --- | --- | --- |
| Separate adapter, pinned modernc | [`vfs.New`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/vfs/patches.go) is public but read-only. [`lib/sqlite.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite.go) exports `Tsqlite3_vfs`, `Tsqlite3_file`, `Tsqlite3_io_methods`, `Xsqlite3_vfs_find/register/unregister`; generated method fields are `uintptr`. Existing adapter makes callback pointers with `unsafe` and maintains Go object tokens. The `?vfs=` DSN selects a named registered VFS. | A custom implementation could own each opened file descriptor before exposing a usable `sqlite3_file`, but that ordering and refusal must be implemented and tested. It must implement SHM mapping/locking and optional mmap or refuse those modes; backend access is not supplied by the read-only API. Each connection, including independent backup/metadata/verify, must select it. | No driver fork technically required to try; unsupported ABI, generated struct layout, Go callback representation, lifetime, GC and per-architecture calling conventions are material risks. A real writable portable VFS is large; Linux-only feasibility is smaller. SQLite/libc and BSD-3-Clause notices/dependency review apply. Owner: KompoSyn if external; no platform is accepted without native evidence. |
| Shim over default VFS | [`sqlite3_vfs_find`](https://www.sqlite.org/c3ref/vfs_find.html) and [`sqlite3_io_methods`](https://www.sqlite.org/c3ref/io_methods.html) permit a C-style wrapper in principle. `SQLITE_FCNTL_FILE_POINTER` returns a `sqlite3_file *`, not a portable OS descriptor ([file controls](https://www.sqlite.org/c3ref/c_fcntl_begin_atomic_write.html)). The driver's public `FileControl` is not a pre-open descriptor API. | A wrapper could delegate locking and I/O and inspect native main/WAL objects only if it can obtain their *actual* native handle, then register before allowing sensitive effects. Default `xOpen` can itself create and mutate metadata. SHM is created/extended inside native `xShmMap`, may write and map before returning; wrapping only its entry/exit cannot reliably insert the necessary exact-handle barrier. `xDelete` receives a path, not an atomic identity condition. | Lowest code only for ordinary callbacks, not a complete solution; platform-specific native `unixFile`/Windows internals and ABI are unsupported. Replacing SHM backend means implementing its protocol, locks and interoperability, not a trivial shim. Must refuse unsupported platform/roles and carefully handle errors/close. |
| Upstream API / newer modernc | The [v1.61.1 package documentation](https://pkg.go.dev/modernc.org/sqlite@v1.61.1) documents DSN selection, while its [VFS package](https://pkg.go.dev/modernc.org/sqlite/vfs@v1.61.1) remains read-only. This is newer than the project pin, not a tested upgrade. No supported general writable Go registration API was found in the inspected sources; this is not an exhaustive survey of unmerged work. | A future supported interface would have to expose a pre-sensitive-write exact-file and SHM mechanism; a generic `xOpen` hook alone is insufficient. Backup connections still need explicit configuration. | Upstream maintenance/support agreement, API semantics, release/version and ownership are unknown; no proposal was sent. Upgrading requires dependency/CLI/release review and native acceptance. No fork needed if upstream actually provides the necessary API. |
| Maintained patch/fork | Potential patch points: stable Go registration/trampoline and registry lifecycle in driver/VFS package; native file open and SHM pre-initialize hook in SQLite OS layer or a fully owned VFS. Generated `lib` files are target-specific, so patches to generated output alone are overwritten by regeneration. | Can insert a registration barrier after exact open and before `_unixOpenSharedMemory`/`xShmMap` initializes/maps, but only with correct error, lock, lifetime and cleanup handling for each platform. All independent connections and Go-created snapshots still need routing. | Largest maintenance and security/update burden: maintain C source or regeneration patches, test Go/SQLite updates, cross-platform OS internals, reproducible builds, releases, notices and core/standalone compatibility. BSD-3-Clause and SQLite public-domain/third-party terms need review. No fork is approved. |
| Alternative: `ncruces/go-sqlite3` | [v0.36.0 commit `3994b44c1e91e7fb5681b2bd16c68ca448f660d5`](https://github.com/ncruces/go-sqlite3/tree/3994b44c1e91e7fb5681b2bd16c68ca448f660d5) has `database/sql`, a public [VFS `Open`/`File`](https://github.com/ncruces/go-sqlite3/blob/3994b44c1e91e7fb5681b2bd16c68ca448f660d5/vfs/api.go), online backup and a Go VFS; README says CGO-free and SQLite Wasm translated by wasm2go. | `FileSharedMemory.SharedMemory()` exists, but `SharedMemory` has unexported methods and [`NewSharedMemory(path, flags)`](https://github.com/ncruces/go-sqlite3/blob/3994b44c1e91e7fb5681b2bd16c68ca448f660d5/vfs/shm.go) supplies the implementation. Its exact SHM file open/registration order and handle access are **not** proved; a writable API is not sufficient evidence. Backup interface and destination VFS propagation must be adapted and tested. | Concrete API candidate, not a recommendation to migrate. Different driver name, SQLite build, VFS/locking, error/backup API, performance and Go/platform support need core and standalone compatibility tests, native WAL/backup tests, release and license review (MIT plus dependencies/SQLite). CGO-free is upstream documentation, not a local static-build result. |
| Current driver, no new boundary | Existing connection hook, path watcher or pre-open callback cannot provide exact-object barrier for SQLite's internal objects. | No SHM/backup/rotation guarantee. | **Current baseline:** refuse covered broker access; no new provisioning, licensing or maintenance burden. |

For every candidate, failure must return a typed SQLite/driver error and preserve registered pins, block publication and report incomplete cleanup. A SQLite open/create or lock may already create an empty file or change metadata before registration refusal; no claim of zero effects follows. Cancellation must unblock waits, close/join workers and SQLite handles without a callback re-entering Store while SQLite, registry or coordinator locks are held. No route should hold the coordinator gate across SQLite operations or call Store from the callback. More precise callback deadlines and lock order must be proven by a prototype rather than asserted here.

## Why SHM and backup are decisive

On Linux/amd64 at the pin, generated [`lib/sqlite_g_0000000000002880.go`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/lib/sqlite_g_0000000000002880.go) has `_unixShmMap` call `_unixOpenSharedMemory`; that code uses `_robust_open` on the SHM pathname and stores `FhShm` in `TunixShmNode`. The mapping path extends the file with `_seekAndWriteFd` and maps it `MAP_SHARED`. This is source inference about this specific target/build, not a native trace. A wrapper waiting for `xShmMap` to return is too late to register the exact object before those effects. An entry hook has no returned SHM descriptor yet. Accessing native private structures after the call also fails the ordering requirement; preopening a pathname does not prove SQLite uses that same object. Other platforms need separate inspection.

At the same pin, [`newConn` and `NewBackup`](https://gitlab.com/cznic/sqlite/-/blob/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738/conn.go) select the VFS from the *new connection's own* DSN and `NewBackup(dstUri)` calls `newConn(dstUri)`; the source connection does not pass its selected name. The [Token Logger backup](../../plugins/library/token-logger/backup.go) passes its destination pathname to `NewBackup`, then opens separate metadata and verification handles. Consequently every destination and reopen needs explicit enforcement routing. A trace of only the source DB is not evidence about backup output.

`xFetch`/`xUnfetch` and read mmap, `xTruncate`, `xSync`, size-hint/file-control operations, locks, close/checkpoint, WAL recovery, deletion, and unknown temp/journal roles all require a reviewed forwarding/denial policy. A pathname-only `xDelete` is not atomic exact-object deletion; classify cleanup and avoid deleting an externally substituted file. [SQLite VFS](https://www.sqlite.org/c3ref/vfs.html), [I/O methods](https://www.sqlite.org/c3ref/io_methods.html), [WAL](https://www.sqlite.org/wal.html), and [temporary files](https://www.sqlite.org/tempfiles.html) specify interfaces, not this integration's safety. The core's [`inspectSchemaSnapshot` and `copyRegularFile`](../../plugins/library/token-logger/schema.go) create copies via Go I/O before SQLite opens them; no SQLite VFS automatically registers those objects. The backup destination is also initially created by core Go code before SQLite opens it, so its exact object and any subsequent pathname reopen must be covered without a substitution window.

## Lifecycle and admission contract to prove

1. A trusted owner enumerates its bounded participating Store/connection/backup/snapshot routes and registers/pins existing exact sensitive objects before read admission. No discovery, inferred directory trust, snapshots as a substitute, or claim about unrelated processes. Unknown external writer/artifact participation refuses the affected capability.
2. For a new or replacement object, reserve its route; open/create the exact object SQLite or Go I/O will actually use, retain or duplicate a handle, register its identity and revision, invalidate affected approvals/workers and complete cancellation/join/publication barriers **before sensitive initialization, truncation, WAL-index mapping or writes**. Owner metadata effects from open/create are reported honestly. A reopened or independently selected object needs a new identity check; pins for prior objects remain until registry shutdown.
3. Perform SQLite work outside coordinator/registry locks. An operation waiting on owner registration cannot call back into the Store or wait on a worker that needs its SQLite lock. A refusal or timeout leaves the route unavailable, suppresses stale output and preserves recovery/backup guarantees. Runtime remains the sole consumer of once grants.
4. Trace fresh/open/reopen, independent Stores, WAL/SHM/recovery/checkpoint/last close, rollback journals and unknown temp roles; all backup source/destination/metadata/verify opens, failures and sidecars; direct Go schema snapshots. Prove SHM's exact handle before the underlying map or any initialization. Show failure, cleanup, no sensitive write and safe fresh retry, then test multiple participating handles and refuse unknown writers. Exact B authority and registered object/route rules remain unchanged.

## Maintainer choices and staged work

| Decision | Concrete choices and recommended next step |
| --- | --- |
| Research route | **Recommend:** named non-default generated-ABI feasibility probe with a strict stop at SHM boundary; alternatively investigate an upstream supported adapter first. Neither approves production use of unsafe ABI. |
| If the SHM barrier cannot be obtained | Continue refusing coverage (default); separately evaluate an upstream-supported pre-SHM hook, a narrowly maintained fork, or a driver migration. Do not choose for the maintainer. |
| If a supported driver/VFS candidate emerges | Decide whether to accept its dependency, platform and update responsibilities after exact-SHM, backup, failure/recovery and standalone compatibility evidence. No installation/provisioning is approved here. |

Stages: (1) run the bounded non-default ABI probe and record exact callback ordering and SHM refusal; (2) only after choosing a mechanism, add a disposable synthetic Store lifecycle integration covering all rows above, including Go-created snapshots and backup failures; (3) review a core-owned neutral lifecycle contract and owner coordination, while keeping production coverage unsupported until all acceptance gates pass; (4) perform per-platform native worker and standalone CLI compatibility studies before any tool enablement. Do not add a nested module, harness dependency to core, persistent state or new permissions.

### Copyable prompt for the first research slice

```text
In an isolated checkout of current master, read AGENTS.md, issue #215 and
this report. Create a disposable Linux-only, non-default named VFS feasibility
probe for the pinned modernc.org/sqlite. Do not change production APIs, driver,
module cache, default VFS, permissions, registration or tool availability.

Use synthetic temporary DBs and bounded synchronization. Record exact driver,
SQLite, Go and platform versions; source and callback/ABI assumptions. Show
whether a separate package can register/select a named adapter and obtain and
pin the exact main and WAL descriptors before sensitive writes. Determine how
the actual SHM object is obtained before native initialization/mapping; if it
cannot be proved, refuse WAL and explicitly report the missing evidence. Do not
assert xOpen/xWrite alone covers SHM. Route a separate NewBackup destination
explicitly; exercise refusal before write, cleanup, and safe fresh retry. Check
close/reopen, unknown roles and unchanged original/backup. Do not claim general
Token Logger, external process, other platform or shell coverage.

Use only temporary fixtures; never alter host security policy, the module cache,
or the process-global default VFS. Include minimal reproducible code, sanitized
ordered role/identity/state traces, failed/unavailable cases and exact sources.
Keep pins until registry shutdown and preserve once-grant single ownership.
Before production integration obtain the relevant dependency decision and cover
backup metadata/verification and Go-created schema snapshots independently.
Run repository-required checks and report native evidence separately.
```

## Measured named-VFS and backup probe

A standalone disposable Go program below created one synthetic source database using the default VFS, registered an instance of the **supported read-only** `vfs.New(os.DirFS(...))` under its generated non-default name, opened the source through that name, and called `NewBackup` with a plain destination and then with a destination selecting the read-only VFS. It never replaced the default VFS or touched real data. Observed output:

```text
sqlite=3.54.1 source_vfs_opens=[source.db source.db-journal source.db-wal source.db-journal source.db-wal refused.db] plain_destination_exists=true explicit_destination=explicit read-only VFS refused
```

The plain destination existing while the wrapper saw only the source and explicitly named `refused.db` is consistent with an independent default-VFS destination; the explicit read-only destination failed. This is a narrow native behavior check plus the source-level `newConn` explanation, not evidence of a writable VFS, exact handles, pre-write prevention or WAL/SHM coverage. The probe checks errors and closes connections; it does not assert an exact SQLite error code or fresh retry. `vfs.New` registration is process-global but named; `FS.Close` requires all connections closed and must not race with opens. This standalone process exits after cleanup.

Save the following code as `probe.go` inside a disposable checkout at the pinned module version and run `go run ./probe.go` there. Delete the disposable checkout afterward; do not place the file in the live repository root. The file uses synthetic paths and SQL only.

```go
// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	sqlite "modernc.org/sqlite"
	"modernc.org/sqlite/vfs"
	"os"
	"path/filepath"
	"sync"
)

type countFS struct {
	fs.FS
	mu    sync.Mutex
	opens []string
}

func (c *countFS) Open(name string) (fs.File, error) {
	c.mu.Lock()
	c.opens = append(c.opens, name)
	c.mu.Unlock()
	return c.FS.Open(name)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	dir, e := os.MkdirTemp("", "sqlite-vfs-study-")
	must(e)
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "source.db")
	db, e := sql.Open("sqlite", src)
	must(e)
	_, e = db.Exec("CREATE TABLE fixture (n INTEGER); INSERT INTO fixture VALUES (42)")
	must(e)
	must(db.Close())
	wrapped := &countFS{FS: os.DirFS(dir)}
	name, fsys, e := vfs.New(wrapped)
	must(e)
	defer fsys.Close()
	db, e = sql.Open("sqlite", "file:source.db?vfs="+name)
	must(e)
	defer db.Close()
	var ver string
	must(db.QueryRow("select sqlite_version()").Scan(&ver))
	conn, e := db.Conn(context.Background())
	must(e)
	defer conn.Close()
	destination := filepath.Join(dir, "backup.db")
	var outcome string
	e = conn.Raw(func(raw any) error {
		b, e := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(destination)
		if e != nil {
			return e
		}
		more, e := b.Step(-1)
		if e != nil {
			b.Finish()
			return e
		}
		if more {
			return fmt.Errorf("unexpected incomplete backup")
		}
		d, e := b.Commit()
		if e != nil {
			return e
		}
		return d.Close()
	})
	must(e)
	_, e = os.Stat(destination)
	must(e)
	e = conn.Raw(func(raw any) error {
		b, e := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup("file:refused.db?vfs=" + name)
		if e != nil {
			return e
		}
		defer b.Finish()
		_, e = b.Step(-1)
		return e
	})
	if e != nil {
		outcome = "explicit read-only VFS refused"
	} else {
		outcome = "unexpected success"
	}
	wrapped.mu.Lock()
	defer wrapped.mu.Unlock()
	fmt.Printf("sqlite=%s source_vfs_opens=%v plain_destination_exists=true explicit_destination=%s\n", ver, wrapped.opens, outcome)
}

```
