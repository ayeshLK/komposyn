# Token Logger protected-resource lifecycle feasibility

**Status:** design and source audit; no production lifecycle API or owner
integration is implemented. Token Logger coverage in the read-broker study
remains `unsupported`.

This note addresses the Token Logger owner gap for the approved object-authority
contract in [issue #215](https://github.com/ayeshLK/komposyn/issues/215). It
does not enable filesystem tools or claim protection of production databases.
The current study documentation remains authoritative for the experimental
worker and its unrelated evidence gaps.

## Finding

The current storage boundary cannot establish the required registration-before-
write guarantee. `Open` and `OpenWithOptions` eventually call `sql.Open("sqlite",
dsn)`, then `configure` changes journal mode to WAL, migrations and pricing
initialization write through the connection, and `Store.Close` only calls
`db.Close` (`plugins/library/token-logger/storage.go`). The `database/sql`
handle is limited to one connection per Store, not one connection across Stores
or processes. Callers supply the database path; `OpenOptions.BackupPath` can
select a backup destination, while an empty value creates a generated backup
beside the database (`backup.go`).

The pinned driver is `modernc.org/sqlite v1.61.0` (go.mod/go.sum; module source
tag `v1.61.0`, upstream commit
[`4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738`](https://gitlab.com/cznic/sqlite/-/tree/4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738)). Its `Driver.Open` calls
`newConn` before invoking registered connection hooks (`driver.go`). The hook
documentation says the connection has been set up (`sqlite.go`,
`ConnectionHookFn`). It therefore cannot stop SQLite from opening a database or
creating a journal/WAL object before registration. The exported `FileControl`
does not provide a file descriptor or pre-write callback. The driver's
`modernc.org/sqlite/vfs.New(fs.FS)` API is explicitly read-only
(`vfs/patches.go`); it cannot wrap the live writable database lifecycle.
`NewBackup(dstUri)` creates a new connection to the destination URI
(`conn.go`), so backup output also goes through pathname-based SQLite opens.

SQLite's actual extension point can meet the logical need: a VFS sees object
open roles such as main database, rollback journal and WAL, and its I/O methods
include writes, truncation, synchronization and shared-memory operations. See
SQLite's [VFS interface](https://www.sqlite.org/c3ref/vfs.html),
[file I/O methods](https://www.sqlite.org/c3ref/io_methods.html),
[WAL behavior](https://www.sqlite.org/wal.html), and
[temporary-file behavior](https://www.sqlite.org/tempfiles.html). However, the
pinned Go driver exposes no supported writable VFS construction/registration
API. Building a private VFS into generated SQLite code, maintaining a fork, or
changing drivers is a material dependency and portability decision. It is not
safe to substitute a pre-open callback, path watcher, second `stat`, precreated
sidecar, or app-level mutex for the VFS write boundary.

**Recommendation:** keep the production Token Logger owner unsupported and
refuse admission for it. Do not add a speculative exported callback now. First
select and prototype a supported writable VFS path, or explicitly choose a
different reviewed storage/process boundary. Only then can a core-owned
lifecycle contract be designed against concrete exact-object semantics.

## Ownership and observed lifecycle

The bounded owner universe is one explicitly composed Token Logger Store and
the lifecycle it can prove. The core library receives the caller-selected DB
path and optional backup destination; it cannot enumerate every standalone
`komposyn-lg` process, separate Store, external SQLite connection, credential
manager or arbitrary database on a host. Collector writes use their Store, but
the collector lease only coordinates collector ownership; it is not a global
SQLite writer lock. Any future protected inventory must fail closed if another
writer or an uncovered artifact can exist.

`inspectSchemaSnapshot` makes a private temporary copy of the main DB and
present `-wal` and `-journal` files, verifies source stability, and inspects the
copy; it deliberately does not copy `-shm` (`schema.go`). This copy itself
temporarily contains DB bytes. It is not the live Store object and does not
provide a read-broker registration barrier. In this package's production Go
sources, the direct `sql.Open("sqlite", ...)` paths are the Store DB, online
backup source/destination, backup metadata writer, and backup verifier. No
`ATTACH`, explicit `CREATE TEMP`, `VACUUM`, or `temp_store` configuration was
found. SQLite may still use temporary files according to SQLite build/runtime
configuration; because the current code does not establish an exhaustive
temporary-artifact policy, any future enforcing VFS must classify or refuse
unrecognized VFS file roles.

A disposable Linux probe with synthetic DBs, pinned driver v1.61.0 and explicit
temporary paths observed a fresh Store's main DB plus WAL and SHM; `Close`
checkpointed/closed the connection and removed WAL/SHM. Reopening an existing
WAL DB recreated WAL/SHM, then close removed them. A synthetic legacy schema
with an explicit backup path produced a verified backup; an intentionally
incomplete schema then failed later in migration. The backup main file remained
alongside `-shm` and `-wal` entries after that later failure. This is a fixture
observation, not a universal SQLite naming or permission guarantee, and not a
failure injected during backup creation. The fixture directory was disposable
and removed. It demonstrated artifact lifecycle, not a hook capable of
blocking each artifact's first write.

### Lifecycle matrix

| Artifact / route | Owner and creation/open timing | First possible content or metadata write | Replacement, reopen, close and cleanup | Protected identity lifetime | Exact-object pre-write interception today |
|---|---|---|---|---|---|
| Main database | Caller supplies path to `Open`/`OpenWithOptions`; schema preflight reads/copies it before lazy `sql.Open`; SQLite may create/open it on first `Exec` in `configure` | Schema migration, catalog sync, or later Store operation; SQLite itself may initialize database metadata | Caller may replace/reopen path; one connection per Store; Store close can checkpoint; other Store/process handles are not coordinated | Protect exact identities until policy-registry shutdown, including after Store close and rename/rotation | No. Connection hook is after `newConn` opened SQLite objects |
| WAL (`-wal`) | SQLite chooses/opens it when WAL mode and writes require it | `PRAGMA journal_mode=WAL`, migration or first write; SQLite manages bytes | Checkpoint, reopen, truncation and last connection close can change/remove it; another Store/process can keep it live | Protect each identity until policy-registry shutdown; close/removal does not retire an alias of that object | No. Same missing writable VFS boundary |
| Shared memory (`-shm`) | SQLite creates/opens as needed for WAL coordination | SQLite's shared-memory methods initialize/update it; mode depends on parent directory/umask and runtime | May be removed/recreated as connections close/reopen; must not release a pin before SQLite has closed and completed cleanup | Protect each identity until policy-registry shutdown, even after unlink/rotation | No. Not even included in schema preflight snapshot |
| Rollback/other journals | SQLite chooses based on journal mode and operation (including recovery/locking behavior) | Journal records precede database mutation | Recovery and close can remove/change journal; code currently sets WAL but preflight observes `-journal`, and other modes/configurations remain possible | Protect each registered identity until policy-registry shutdown; close/delete is not identity retirement | No. Must classify VFS file role and fail closed if unsupported |
| Schema-inspection snapshot | Core creates a private `os.MkdirTemp` directory, copies main/WAL/journal, then opens the copied DB | Snapshot bytes are written by `copyRegularFile` before SQLite opens its copy | Temporary directory is removed after inspection; error paths also clean it; DB content exists there transiently | If included as protected, pin identity until policy-registry shutdown after unlink; otherwise this route must be proven inaccessible and excluded by reviewed policy | No owner hook. Host must include this temporary copy in lifecycle protection or change design before claiming complete coverage |
| Migration backup main | Core chooses same-directory generated `.komposyn-pre-migration-*.sqlite` or caller `BackupPath`; exclusive create, mode 0600 attempt, then online backup opens destination through driver | Online backup page writes; metadata table is written afterward; backup is integrity/schema verified before migration continues | Main backup is preserved after successful verified backup even if later migration fails; backup open/metadata/verify connections close independently | Protect the backup identity through registry shutdown, including retained or renamed backup objects | No. Destination is reopened by SQLite pathname; no descriptor-based VFS hook |
| Backup WAL/SHM/journal | SQLite creates as required when destination and metadata connections open | Online backup and metadata SQL may initialize or write sidecars | Sidecars can remain after subsequent migration failure; probe observed backup `-shm` and zero-length `-wal`; cleanup currently removes only the backup main path on incomplete backup construction | Protect each identity through registry shutdown; cleanup of the pathname does not establish identity retirement | No. Must cover sidecars and cleanup as part of backup lifecycle |
| Backup verification/source connections | `runOnlineBackup`, `writeBackupMetadata`, and `verifyMigrationBackup` each create independent `database/sql` handles/driver connections | Source read; backup destination/metadata writes; verification read | Deferred close occurs on function return; errors can leave sidecars; any failed backup construction removes only owned main pathname | Any protected object opened here remains protected until registry shutdown, regardless of connection close | No. Path-based driver opens and close effects are not coordinated with a broker |

Modes in the probe are reported only as observations under that fixture's
directory permissions and umask. They are not a policy guarantee. SQLite
sidecar behavior also varies with journal mode, build options, filesystem and
concurrent connections; the table describes paths reached or visible from
current code, not a closed list of all SQLite artifacts.

## Feasibility alternatives

| Option | Exact write barrier / identity | Ownership and compatibility | Disposition |
|---|---|---|---|
| Keep current driver hooks and register the DB path before `sql.Open` | Cannot identify or stop first WAL/SHM/journal/backup write; pathname resolution remains a substitution gap | Small change, but optional and incomplete | Reject as enforcement; remain unsupported |
| Current modernc read-only `vfs.New(fs.FS)` | No writable SQLite objects | Supported API, wrong capability | Reject for live Store |
| Add a writable VFS to modernc or maintain a reviewed fork | SQLite VFS can mediate typed opens, writes, deletes and shared-memory methods; a VFS could synchronously register its exact opened object before first write and hold the lifecycle lease through close | Requires upstream API, maintained fork, or driver migration; generated-code integration, SQLite versioning, cross-platform behavior and maintenance ownership need review | Feasible in principle, not currently available through pinned supported Go API; prototype only after dependency decision |
| Put DB in a protected directory/principal and expose that directory as an object-authority boundary | Could reduce independent path writers under a changed threat model, but does not itself coordinate SQLite sidecar registration, temporary snapshots, backup destinations, other processes, or same-principal writes | Requires provisioning, location policy, migration and CLI cooperation; would change approved exact-file/object inventory assumptions | Separate maintainer decision; not a substitute adopted here |

Because no currently supported hook meets the exact-object/pre-write
requirement, this report intentionally has no proposed exported API signature.
Inventing `OpenWithLifecycle` around a callback that runs before `sql.Open`
would imply a guarantee the callback cannot enforce. Once a concrete writable
VFS exists, prefer an additive constructor or options type over adding a field
to public `OpenOptions`: external users may use unkeyed composite literals.
That future constructor must preserve current `Open` defaults and make
uncovered callers explicitly ineligible; optional omission is not coverage.

## Required ordering if a VFS path is selected

This is an acceptance contract, not an implemented protocol:

```text
host owners register protected routes and existing exact objects
  -> Store open reserves the selected DB and backup routes
  -> VFS opens/creates a typed SQLite object
  -> host pins/classifies that exact object before first sensitive write
  -> operation admission checks owner, root, policy and grant generations
  -> worker read may proceed
  -> owner rotation/close blocks new admissions and cancels affected workers
  -> owner waits for confirmed worker join and publication suppression
  -> SQLite checkpoint/close/delete completes under VFS lifecycle control
  -> SQLite handle/route lease ends; protected object pins remain retired-but-protected
  -> pins are released only when the owning policy registry shuts down
```

The future lock order must be explicit and one-way, for example owner lifecycle
lock → coordinator admission/publication gate → registry/object pin lock. No
callback may enter a Store while holding coordinator or registry locks. Owner
close/rotation must prevent new reads, request cancellation, join workers,
then allow SQLite operations that can write or delete sidecars; if join or
cleanup is unconfirmed, initialization/mutation remains blocked and the result
is reported incomplete/unknown. The once grant remains consumed by the runtime
exactly once; lifecycle registration must neither consume nor restore it.
SQLite close completes the Store's write/sidecar lifecycle, but does **not**
make a retired database, WAL, SHM, journal or backup identity ordinary: a hard
link or renamed object may still expose the same bytes. Keep retired exact
object pins protected until registry/conversation shutdown. Any earlier pin
release or reclassification requires a separately reviewed and proven safe
retirement rule.

Per-Store `SetMaxOpenConns(1)` is not cross-Store/process serialization. A
supported integration must either coordinate every permitted writer through
the same host-owned boundary or refuse when participation is unknown. No
application mutex, SQLite busy timeout, watcher or database lease is evidence
that independent namespace actors cannot alter aliases. Identity pins also
cannot recognize independent copies of sensitive bytes.

## Authorized contract and maintainer decisions

| Topic | Already authorized | Still needs review |
|---|---|---|
| Generic tool policy | Token Logger DB/WAL/SHM/backup objects are protected from generic reads; no filesystem tool exposure in this task | None; keep coverage unsupported until evidenced |
| Exact-object lifecycle | Registration before sensitive writes, object identity across aliases/rotation, owner cleanup/join before release | Which supported writable VFS/driver implementation can deliver this on each OS, and who maintains it |
| Owner universe | Bounded, host-supplied registrations; no machine discovery or model registration | How standalone CLI and every Store/process participate, and how unknown writers make admission refuse |
| SQLite transient state | Preserve existing online backup and schema upgrade guarantees; do not overwrite backups | Whether all temporary SQLite roles can be mediated; how schema inspection's private DB snapshot is registered or redesigned |
| API shape | Core owns any eventual Token Logger lifecycle boundary; no harness/UI import | Additive constructor/options and compatibility behavior; no API should be committed before the VFS contract is proven |
| Platform availability | Unsupported means refusal; no weaker fallback | Native platform matrix and required driver/VFS support per platform |
| Backup failure cleanup | Existing verified backup must remain usable and later migration failure preserves it | Exact cleanup of incomplete backup sidecars without deleting an existing or externally replaced object |

## Staged implementation and acceptance plan

1. **Driver decision/prototype:** choose upstream writable-VFS support, a
   maintained driver fork, or a driver migration. In an isolated branch, prove
   an exact opened-object callback occurs before writes for main DB, WAL, SHM,
   rollback journal, and backup destination. Record driver and SQLite revisions.
   Unknown roles must refuse before bytes are written.
2. **Disposable Store integration:** with synthetic temporary DBs only, cover
   fresh creation, existing WAL reopen, committed/uncommitted activity,
   checkpoint and last-close/reopen; default and explicit migration backups;
   metadata/verification; open, backup and migration failures; and schema
   inspection snapshots. Confirm every effect maps to a registered object.
3. **Core-owned lifecycle contract:** add a compatible opt-in core constructor
   only after the prototype establishes call ordering. Specify registration,
   cancellation/join, close/rotation, cleanup-pending, generations and error
   classification. Retain retired exact-object pins through registry shutdown;
   SQLite close is not pin retirement. Test multiple Stores and refuse
   uncoordinated writers.
4. **Owner composition:** integrate each standalone host composition explicitly;
   register selected DB/backup routes and all current/replacement objects before
   sensitive writes. Keep provider-source integration separate. Missing owner
   registration or unsupported platform must keep typed reads unavailable.
5. **Native acceptance:** run exact-revision tests on every claimed OS and the
   broker worker under the reviewed profile. Exercise replacement/rotation and
   stale-output publication barriers. Tests, file observations, and OS
   enforcement evidence must be reported separately.

### Copyable prompt for the first implementation slice

```text
Implement an isolated driver/VFS feasibility prototype for Token Logger lifecycle registration.

Repository: ayeshLK/komposyn. Start from current master and read AGENTS.md and
its required documents. This is a research implementation only: do not change
production Token Logger APIs, enable filesystem tools, wire the harness, or
claim owner coverage. Use synthetic databases in disposable temporary dirs.

Before coding, identify the reviewed modernc.org/sqlite version and the
maintainer-selected writable VFS/driver path. Demonstrate, with source-level
references and deterministic tests, that the exact SQLite object is registered
before its first sensitive write. Cover main DB, WAL, SHM, rollback journal,
backup destination, metadata/verification, schema snapshot, close/checkpoint,
and unknown VFS roles. Preserve online backup semantics and failure
preservation. Never replace it with path copying. Refuse unknown roles or
uncoordinated writers. Bound and clean all fixtures.

If the selected driver has no supported writable VFS API, stop at a minimal
reproducible prototype/decision report; do not silently fork generated code or
add a callback before sql.Open and call it enforcement. Report exact driver and
SQLite revisions, platform evidence, descriptor/object ordering, cleanup,
limitations and required maintainer decisions. Add only focused tests needed
to prove the prototype. SQLite close must not release or reclassify retired
protected object identities; retain their pins until policy-registry shutdown
unless a separately reviewed safe retirement rule is demonstrated. Keep Token
Logger broker coverage marked unsupported.
```

## Reproduction and source references

Run the committed, synthetic-only lifecycle probe from the repository root:

```sh
go test -run '^TestReadBrokerLifecycleProbe$' -count=1 -v ./plugins/library/token-logger
```

`TestReadBrokerLifecycleProbe` creates all DBs under `t.TempDir`, prints each
checkpoint's explicit temporary directory and sorted file names/modes/sizes,
and removes the entire fixture root via Go test cleanup. It creates a fresh
Store, writes one synthetic diagnostic, observes close/reopen, and separately
runs online backup, metadata, and verification as named checkpoints. A second
fixture has only a version-1 `schema_migrations` table plus a pre-existing
`diagnostics` table; this makes migration 2 fail deterministically after
`OpenWithOptions` has created and verified the explicit online backup. No real
database or contents are read. The test does not use the broker worker and does
not establish a pre-write enforcement barrier.

Observed in this Linux amd64 run (Go `go1.26.10`, modernc driver `v1.61.0`,
SQLite `3.54.1`): the fresh Store had main/WAL/SHM while open; `Close` left only
the main database; reopen recreated a zero-length WAL and SHM, then close
removed them. The staged successful backup had only its main file after online
copy, metadata, and verification checkpoints. The full Store path then failed
as expected at migration 2 and left the explicit backup main plus `-shm` and
zero-length `-wal` at the final checkpoint. This output reproduces the final
states. It does **not** isolate which internal call in the integrated failing
Store path first created those backup sidecars; neither file appearance after
return nor syscall tracing alone proves a pre-write hook. Treat that exact
sidecar creation point as unresolved pending a VFS-level trace/prototype.

Checkpoint values from the run (fixture names shortened; sizes are bytes):

| Checkpoint | Observed fixture entries |
|---|---|
| `fresh.open` | `fresh.sqlite` 4096; `fresh.sqlite-shm` 32768; `fresh.sqlite-wal` 370832 |
| `fresh.written` | main 4096; SHM 32768; WAL 383192 |
| `fresh.closed` | main 352256 only |
| `fresh.reopened` | main 352256; SHM 32768; WAL 0 |
| `fresh.reopened_closed` | main 352256 only |
| `backup.online_complete` | backup main 8192; source main 8192 |
| `backup.metadata_complete` / `backup.verification_complete` | backup main 12288; source main 8192; no sidecars visible at either checkpoint |
| `migration.failed_after_verified_backup` | error `apply migration 2: ... table diagnostics already exists`; explicit backup main 16384, SHM 32768, WAL 0; source main 12288 |

The test emits the modes too: fresh files were `0644`; explicit backup and
sidecars were `0600`. These are fixture observations under the test process's
umask and directory modes, not a cross-platform permissions claim.

Repository commit for this audit: `c82305ce99d6c3aea8d6cc44c10161a505e79ff6`.
Relevant local symbols are `openWithOptionsAndObserver`, `Store.configure`,
`Store.Close`, `Store.migrate`, `inspectSchemaSnapshot`, `copyRegularFile`,
`createMigrationBackup`, `runOnlineBackup`, `writeBackupMetadata`, and
`verifyMigrationBackup`.

Primary SQLite references: [VFS interface](https://www.sqlite.org/c3ref/vfs.html),
[I/O methods](https://www.sqlite.org/c3ref/io_methods.html),
[WAL](https://www.sqlite.org/wal.html),
[temporary files](https://www.sqlite.org/tempfiles.html), and
[online backup API](https://www.sqlite.org/backup.html). Pinned driver sources:
[`driver.go`](https://gitlab.com/cznic/sqlite/-/blob/v1.61.0/driver.go),
[`conn.go`](https://gitlab.com/cznic/sqlite/-/blob/v1.61.0/conn.go),
[`sqlite.go`](https://gitlab.com/cznic/sqlite/-/blob/v1.61.0/sqlite.go), and
[`vfs/patches.go`](https://gitlab.com/cznic/sqlite/-/blob/v1.61.0/vfs/patches.go).

The earlier Python owner probe pinned only the initial auth file, not its
directory. It demonstrated missing descendant identity coverage. Neither that
probe nor the present SQLite lifecycle observations establish Token Logger
coverage. See the [owner inventory](read-broker-owner-inventory.md) for the
corrected registered-directory interpretation.
