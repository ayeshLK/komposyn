# Read-broker owner inventory and admission

This note audits owner boundaries and the bounded research admission gate.
The current implementation adds trusted auth/settings registration to the
memory-chat host composition, but does not establish complete inventory, admit
generic reads, or enable filesystem tools; see
[composition admission and sandbox service](composition-admission-sandbox-service.md).

## Owner and lifecycle audit

| Owner/category | Effective-path or identity source | Existing / absent route registration | Creation, replacement, rotation and retirement | Coverage today |
| --- | --- | --- | --- | --- |
| Harness auth store | `auth.Store.EffectivePath`; explicit `Store.Path` override or `~/.komposyn/auth.json` default | `RegisterProtection` registers the effective route; an existing regular private file is opened and its identity is pinned. An absent leaf reserves the route. `cmd/harness` now calls this before `ListMetadata`; the same protected store value is used by resolvers, OAuth and UI. | `UpdateJSONProtected` creates an empty private temp file, invokes the protector before JSON bytes are written, then writes and atomically publishes that same object. The host service retains replaced identities until shutdown. Environment variable values remain outside the file inventory. | Host composition wiring is implemented, with synthetic path override tests. This is not inventory of environment-referenced credentials or other applications. |
| Harness settings store | `settings.Store.EffectivePath`; explicit override or independent `~/.komposyn/settings.json` default. An auth-only override does not relocate settings. | Same route plus existing-object behavior as auth. `cmd/harness` registers this own effective path before `Load`. | `SaveContext` follows the protected atomic replacement path; the empty replacement is registered before settings bytes. The host service retains replaced identities until shutdown. | Host composition wiring is implemented, with independent override tests; this does not cover arbitrary other settings files. |
| Token Logger database and sidecars | Caller-selected path passed to `tokenlogger.Open` / `OpenWithOptions`; optional caller-selected `BackupPath` | No read-broker owner registration API exists. `Open` resolves the selected path, inspects schema, creates the parent/database as needed, configures SQLite WAL mode, then migrates. SQLite owns creation/use/removal of `-wal` and `-shm`; their timing depends on connection and journal lifecycle. | Migration backup is created before applying migrations: generated beside the DB when no backup path is supplied, or created exclusively at `BackupPath`. The online backup copies database state and then writes/verifies KompoSyn metadata. `Store.Close` closes SQLite but does not notify a read-broker owner. No demonstrated barrier registers DB, sidecars, backup destination and temporary/recovery objects before SQLite writes them. | Unsupported. A filename/path list cannot prove sidecar lifecycle coverage or registration-before-sensitive-write. |
| Provider source files | Caller-configured `CollectorRoot` values; collector discovery canonicalizes roots and walks for provider-shaped `.json` / `.jsonl` records. The stream reader later opens a discovered source and captures its open-file metadata/identity. | No registry hook connects configured roots, discoveries, opened source identities, cursor generations or reopens to the read-broker. Root registration alone is not an exhaustive identity inventory. | Provider source producers are external. Core adapters open/read sources and remain read-only; KompoSyn does not control external creation, replacement, append, rotation or retirement. A producer is not a cooperative KompoSyn owner merely because its file appears below a configured root. | Unsupported as a complete protected-owner inventory. Actual identity must be captured and correlated to a per-invocation provider approval; source content need not be retained for this inventory audit. |
| Standalone CLI instances and external credentials | Production `komposyn-lg` owns its data-directory/configuration choice. Auth profiles may also reference environment variables; arbitrary external credential files are outside core's store paths. | Core cannot enumerate independent CLI installations, process environments, user credential managers, or arbitrary files on the host. | Independent applications/producers have no shared registration and write barrier. | Out of scope for this bounded profile; no machine scan or default-path guess is treated as completeness evidence. |

### Required Token Logger consequences

Any later DB owner API must receive the effective caller-selected DB and backup
destinations, and cover SQLite-created WAL/SHM objects and migration/recovery
lifecycles. A before-open callback alone is insufficient unless the API can
demonstrate that every database and sidecar object is registered before
sensitive bytes are written, and that rotation/close waits for affected worker
cleanup. The current storage package has no such callback. This slice does not
change its API or SQLite behavior.

### Required provider-source consequences

A provider inventory must correlate configured roots with each effective
discovered/opened regular-file identity and its source generation, including
replacement, reopen and append/tail behavior. Configured roots are not proof
that all descendants or aliases are registered. External producers cannot be
given a KompoSyn write barrier by the current collector. This slice does not
change adapters or collector behavior.

## Research coverage contract

`readbrokerstudy.NewSyntheticStudyInventory` remains the only profile that
represents a complete four-owner **fixture universe**: credential/auth,
settings, Token Logger database, and provider source. The added
`NewChatCompositionInventory` is a separate trusted profile requiring only
the two actual auth/settings stores. It does not attest arbitrary-root data as
complete; `sandbox.Service` applies the independent unknown-root gate even
after those two owners register. Neither constructor accepts a completeness
boolean or permits a caller to mark a missing owner absent. The synthetic
profile's Token Logger/provider declarations remain synthetic evidence only.

An owner declaration does not count as a route. A route/object counts only
after it appears in the pinned policy registry. An absent leaf is represented
as a reserved route with zero pinned identities; existing and retired objects
are counted separately from route declarations. The synthetic profile admits
only the study's disposable fixture universe. Its Token Logger and provider
entries do not satisfy real production-owner coverage.

The fixture session checks complete coverage before opening a target, consuming
once authorization, launching a worker, or handing off a descriptor. Inventory
revision is recorded separately from root, policy and grant revisions. Owner
declaration/registration/retirement changes advance it. A required owner's
registration starts a pending update before registry mutation. Failed or
partially observed registration leaves that owner pending and blocks admission;
a successful retry with an effective route completes the update. Session-
mediated registration also advances policy. Active operations with stale
revisions are terminated, joined under the existing cleanup barrier, and
cannot publish buffered bytes. Explicit owner retirement invalidates its
attestation without dropping protected pins; it cannot be silently reactivated
in that session. Closing a session retires the inventory; a closed or missing
inventory refuses admission. Permission approval and coverage completeness
remain independent.

Tests distinguish the complete bounded synthetic profile from an unknown
inventory and from auth/settings-only partial coverage. In the research
profile, the latter refuses before once consumption and worker launch, then
admits only after remaining fixture owners/routes are registered. A successful
ordinary read uses the complete synthetic profile and normal worker contract;
that remains fixture evidence, not real-owner integration. The shipped host
composition registers actual auth/settings stores but still refuses generic
reads before preparation or grant consumption because root data is unknown.

## Token Logger lifecycle feasibility

The detailed audit of Store, SQLite sidecar, schema-inspection snapshot, and
migration-backup lifecycles—and the finding that the pinned driver lacks a
supported writable pre-write VFS hook—is recorded in
[Token Logger lifecycle feasibility](read-broker-tokenlogger-lifecycle.md).
Token Logger remains unsupported for real owner coverage.

## Remaining decision and next bounded task

No policy decision is needed to keep incomplete production coverage unavailable.
The next owner-integration work should be split by ownership boundary:

1. Propose a core-owned Token Logger lifecycle API that can reserve DB and
   backup routes, observe/register every SQLite-managed sidecar and temporary
   object before sensitive writes, correlate opened objects, and coordinate
   close/rotation with the host barrier. Demonstrate actual SQLite sidecar
   behavior in disposable fixtures before claiming coverage.
2. Propose a read-only provider-source registration seam that correlates
   configured roots, discovery, open descriptors and source generations without
   implying control over external producers. Keep provider approvals tied to
   exact opened identity and route.
3. Keep the trusted chat composition's unknown-root-data gate closed. If a
   later composition opens/undertakes Token Logger or provider sources, add
   their actual owner lifecycles and acceptance evidence; do not infer coverage
   from this chat profile. Production typed reads remain unavailable meanwhile.

Do not broaden this into machine-wide scanning, storage redesign, production
sandbox integration, or model-facing tool registration.
