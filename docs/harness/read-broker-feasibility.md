# Experimental brokered read feasibility harness

This package and command are isolated research infrastructure. They are not
used by production dispatch and register no filesystem tools. A narrowly
injected owner hook now exercises the actual harness auth and settings stores
against the study registry in disposable tests; the shipped chat does not wire
that hook to a sandbox session. Provider-source and Token Logger owner
inventories remain absent. See [protected owner integration](read-broker-owner-integration.md).

The follow-up native acceptance scenarios for actual auth/settings store calls
through the live Bubblewrap worker are defined in
[real-store native acceptance](read-broker-real-store-native-acceptance.md).
Their results are not inferred from the older synthetic `fixtureOwnerWriter`
cases or the injected-session store tests.

## Ownership and protocol

```text
fixture owner registrations
  -> host-owned pinned-object policy registry
  -> fixture runtime: explicit once approval and sole once-grant consumption
  -> trusted broker: scoped open, regular-file check, identity/classification
  -> fixed Bubblewrap worker: receives one read-only FD after readiness
  -> host-private bounded capture
  -> synchronized generation/revocation publication gate
  -> in-memory fixture result sink
```

The worker starts with only stdio and one local `AF_UNIX/SOCK_SEQPACKET`
protocol descriptor. The read capability is transferred with `SCM_RIGHTS`
only after startup probes, authorization, generation checks, broker
revalidation, and the coordinator's release gate. The worker accepts no path,
shell command, provider data, grant store, or owner registry. It performs one
bounded `pread` from the transferred regular-file descriptor and returns a
bounded result packet. There is no unrestricted or pathname-based fallback.

The trusted backend is first checked for path ownership and permissions; only
then do bounded `--version` and `--help` probes run. Parsed help options, not
the reported version, choose the launch mode. The version is provenance only.
The host opens `/proc/self/exe`, verifies the supplied `os.Executable` path
refers to that same device/inode, and validates the running image as a static
ELF. The worker path is only an identity hint; restrictive mode bits on a
user-owned pathname is not the trust anchor. The hint is inspected through a
non-following `O_PATH` handle, so a substituted FIFO or device is rejected
without opening it for data or blocking for a peer. A 7 MiB image ceiling
applies to both launch modes.

When `--ro-bind-fd` is available and the running executable has a resolvable
host path, the launcher passes independent read-only opens of the running
executable at FD 5 and FD 6. Each `Start` gets fresh open-file descriptions at
offset zero; `dup` is not used because it shares offsets. Bubblewrap 0.10
resolves `/proc/self/fd/5`, mounts the resolved source, then compares the
mounted object's device/inode with the still-open FD before it execs the
worker. A replacement that changes the mounted object fails that check before
untrusted bootstrap code can run. This ordering is visible in upstream
[Bubblewrap 0.10 source](https://github.com/containers/bubblewrap/blob/v0.10.0/bubblewrap.c).
The running executable's write exclusion prevents in-place writes while it is
executing (`ETXTBSY` in [`open(2)`](https://man7.org/linux/man-pages/man2/open.2.html)
and [`execve(2)`](https://man7.org/linux/man-pages/man2/execve.2.html)).
`--sync-fd 6 --as-pid-1` retains FD 6 through exec, and the bootstrap checks
`/worker` is a regular file with the same device/inode before closing FD 6.
If Bubblewrap's descriptor source cannot resolve to an existing path, the
launcher chooses descriptor-fed snapshot construction when supported; it
never runs the unresolved descriptor-bind path.

Bubblewrap 0.9 without a compatible `--ro-bind-fd` source uses
`--perms 0500 --ro-bind-data 5 /worker`. Upstream 0.9 source shows this option
creates a temporary file, applies the requested mode with `fchmod`, copies
bytes from FD 5, closes that FD, then read-only bind-mounts the file into the
new root and unlinks its staging name. This source is descriptor-fed, not a
resolved host source pathname. Before launch, the host copies the running
image into a sealed memfd and passes independent fresh read-only opens at FD 5
and FD 6. FD 6 is retained with `--sync-fd 6 --as-pid-1`. The already-running
parent image, sealed memfd, and trusted Bubblewrap copy make the first executed
bytes the same bounded snapshot; the copied bootstrap additionally hashes
`/worker` against FD 6 and closes it before installing seccomp. That post-exec
hash is an integrity check, not an independent authentication stage by itself.

Bubblewrap's copy loop has no intrinsic byte ceiling, so the launcher enforces
a 7 MiB source bound before passing a descriptor. The 0.9 path temporarily
stores another copy in Bubblewrap's private setup tmpfs while binding it into
the 8 MiB worker root; setup failure such as ENOSPC refuses before worker
readiness. The worker root contains only the executable and receives no host
procfs or other runtime filesystem.

Both modes close or consume FD 5, verify/close FD 6, validate/install the
host-prepared BPF from FD 3 with `SECCOMP_FILTER_FLAG_TSYNC`, and complete
isolation probes before readiness. Missing, standard, duplicate, symlinked,
mismatched, or unsealed descriptors fail startup. The startup descriptor
inventory requires that only protocol FD 4 remains after bootstrap. No
target-data descriptor is transferred until verification, probes,
authorization, and generation checks have succeeded.

The legacy pathname-based `--ro-bind /proc/self/fd/5 /worker` remains
prohibited. Bubblewrap 0.9 resolves path sources before mounting and has no
post-mount descriptor identity check for that operation, so replacement
between resolution and mount could execute an unverified image. The secure
compatibility mode never uses that path.

The compatibility design was compared with the Codex Linux sandbox at
checkout `970111f6cb62cc02fe36a82ea7d71b37a2889c97`
(`linux-sandbox/src/launcher.rs`, `fd_mount.rs`, and `linux_run_main.rs`); no
Codex source was copied. Its older path-bind fallback does not by itself solve
the pathname replacement race. See upstream
[Bubblewrap 0.9.0 source](https://github.com/containers/bubblewrap/blob/v0.9.0/bubblewrap.c)
for path resolution and [0.10.0 source](https://github.com/containers/bubblewrap/blob/v0.10.0/bubblewrap.c)
for descriptor bind setup and source-FD consumption.

This verification binds the worker mount to the separate pinned executable
object; it does not strengthen target-path containment or broker
protected-object scans.
The broker retains the opened target and parent directory descriptors. A
pathname change after open therefore does not redirect the bytes read by the
worker; it also does not prove strict live host-path containment. The worker
descriptor pins the inode against pathname replacement, but this prototype
does not prevent a same-UID process with write access to that inode from
modifying its contents in place. Native experiments do not mutate the worker
image.

## Operation state

```text
prepared
  -> launching (no read FD)
  -> ready (probes passed; no read FD)
  -> authorization consumed and target revalidated
  -> capability released
  -> read started
  -> captured privately
  -> final generation gate -> published

Any stale generation before release => terminate, not_executed
Cancellation/revocation after release => terminate and suppress publication;
  if completion/cleanup is uncertain, report unknown rather than rollback
```

The fixture runtime alone consumes a once approval. The authorizer binds its
receipt to invocation identity, prepared object identity, classification,
request digest, and root/policy/grant generations. Policy/root/revoke changes
advance the relevant generation and invalidate active operations. A global
grant-generation change stops every operation tied to the previous revision.
The final in-memory publication check is serialized with those generations;
namespace changes by independent processes are not serialized by this lock.
Revocation reports `ErrCleanupPending` while an active operation has not joined;
the operation reports `unknown` if it cannot confirm worker cleanup. A successful
signal request alone is not treated as completed cleanup.

For the Linux worker, `Joined()` means the direct Bubblewrap process was waited
and its process state was collected. `Wait()` can still return an exit error
after that join, such as when the direct process was intentionally killed before
target-data release. A known cancellation before the read descriptor is
released remains `cancelled` when the direct-process join is confirmed; the
exit error remains a diagnostic. After target-data release, a missing
acknowledgement, a stale generation before a complete result, or a worker
result/exit error remains `unknown` when the read outcome cannot be established.
Direct-process join does not prove that every namespace descendant has
terminated; descendant cleanup requires separate launcher guarantees and
evidence.

## Protected registrations and directory limits

The current experimental target follows the approved exact-file typed-read
contract in [`read-broker-approved-typed-read.md`](read-broker-approved-typed-read.md)
and the [issue #215 decision record](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377).
Earlier admission-gate descriptions and run results below are historical where
they conflict with that decision.

Trusted owners register protected exact-file locations and pin object
identities; retired pins remain until registry shutdown. Registration and pin
budgets are bounded and fail closed. No machine-wide discovery or
model-controlled registration occurs.

A registered directory pins that directory object, not every existing or
future child. Preserve `DirectoryCoverageIncomplete` refusals whenever the
broker cannot establish complete descendant coverage. A child moved out is
not thereby protected by the directory pin. Exact registered files remain
protected through hard-link aliases and renames while their pins remain live.
Independent copies of sensitive bytes remain outside identity-based
protection.

The earlier Python issue probe registered/pinned its initial authentication
file; it did not pin the directory. Its ancestor result demonstrated missing
descendant identity coverage. The fixture here registers a directory explicitly
and demonstrates both an in-tree descendant refusal and fail-closed refusal of
an unpinned same-device descendant moved out. Neither result means a directory
descriptor identifies every descendant.

The earlier conservative admission gate rejected all reads when a protected
location had mutable or unverifiable ancestry, and rejected ordinary reads
when a provider-source location had mutable ancestry. Those restrictions were
specific to the earlier policy and must not be presented as the approved
contract. Under the approved contract, a late alias of an already-approved
ordinary object into a protected exact-file location alone does not reclassify
that invocation. A fresh request through the protected route is denied;
registered sensitive identities remain denied through aliases and renames.
Provider-source reads remain explicitly approved per invocation and bound to
route and identity.

When a proposed scope overlaps a registered protected directory but the
registry cannot prove complete descendant identity coverage, the broker returns
`ErrDirectoryCoverage`. The coordinator reports this specific policy refusal
as `refused` before authorization: it consumes no once grant, starts no worker,
transfers no read descriptor, and publishes no data. Other preparation and
generation errors retain their separate outcomes.

| Registration owner | Required protected inputs | Generic read policy | Rotation rule |
| --- | --- | --- | --- |
| Authentication owner | Credential/auth files and temporary replacement objects | Denied | Register and pin temp object before writing sensitive bytes; wait for affected workers to terminate and join; keep old pins until shutdown |
| Settings owner | Application settings/state objects | Denied | Same register-before-sensitive-write and join ordering |
| Token Logger owner | DB, WAL, SHM, backup objects and reserved locations | Denied | Register active/rotating objects before sensitive writes; wait for affected workers and preserve old pins |
| Provider source owner | Provider-owned source objects/locations | Explicit once approval per invocation; no mutation capability here | Register/pin replacement before it can be read as a source; invalidate affected work |

The study has a narrow host adapter for the real harness auth and settings
stores, exercised with synthetic files. It registers their effective route
and replacement object before the stores write bytes, and refuses writes when
the session reports cleanup pending. This adapter is not installed in
`cmd/harness`; production dispatch still has no filesystem tools or sandbox
service. Token Logger and provider-source owners remain unwired. Participating
owners must register relevant exact-file locations and objects; missing
registrations make affected routes ineligible. Old pins remain through
registry shutdown. Inode identity alone cannot detect an independent copy of
protected bytes.

The fixture lock order is coordinator/session mutex before registry mutex;
registry staging opens and pins objects before taking its commit lock. There is
no owner callback while either lock is held, and this prototype has no
production owner mutex integration. Owners must serialize their own
write/rotation and follow register/pin, policy publication, worker
termination/join, then sensitive-write ordering. Do not drop old pins before
registry shutdown. The policy binds the requested route at preparation and
the opened object identity; it does not promise continuous checks of all
object names or atomic ordering with an independent namespace writer.

Credential/authentication material, settings, and registered Token Logger
database files are denied to generic reads. Provider-source files are only
eligible for explicit invocation approval and are never mutable through this
harness. Native sandbox scenarios use synthetic owner registrations. Owner integration tests use synthetic auth/settings files with the real store
save paths and in-memory registry adapter. The focused native acceptance cases
exercise those stores through the live study session and Bubblewrap worker; the
adapter is still not installed in the shipped chat. Token Logger and
provider-source owner registrations remain unavailable here. See the
[real-store native acceptance criteria](read-broker-real-store-native-acceptance.md).

## Effective worker profile

The launcher requests user, mount, PID, IPC, UTS, and network namespaces with
no optional `try` flags; a new session; dropped capabilities; disabled nested
user namespaces; an 8 MiB empty tmpfs root; one read-only worker executable
bound either by inode (`--ro-bind-fd`) or from the sealed byte snapshot
(`--ro-bind-data`); cleared environment; no host home, root, sockets, proc, or
device mounts; `--die-with-parent`; and `--as-pid-1`. It passes only seccomp FD
3, control socket FD 4, sealed snapshot source FD 5, and an independent
read-only verifier FD 6. Bubblewrap consumes FD 5 using the selected feature
and uses `--sync-fd 6` to retain the verifier across exec; the startup
bootstrap closes FD 6 after checking the mounted worker. The bootstrap also
closes seccomp FD 3 after installing the exact architecture-checked filter.
Before operation release, the worker probes `no_new_privs`, seccomp mode 2,
denied path opens/socket creation, absence of the synthetic inherited
environment marker, and absence of any non-protocol descriptor. The same
filter's process-creation denials are exercised separately by the real-filter
subprocess test described below. The host command receives only that synthetic marker; Bubblewrap applies
`--clearenv`. Standard input is `/dev/null`, stdout is discarded, and stderr
is captured with a 4 KiB cap.

The seccomp filter permits `clone` only when its flags exactly equal the Go
runtime's shared-thread flags, with or without `CLONE_SETTLS`; it also requires
the high 32 bits of the 64-bit flags argument to be zero. This matches the
supported Go runtime's [`cloneFlags` and amd64 TLS setup](https://github.com/golang/go/blob/go1.26.10/src/runtime/os_linux.go).
Process-like flag combinations are denied, and `clone3` remains denied. A subprocess test
installs the exact filter, drives Go runtime GC and scheduler work that needs
threads, then confirms process-like `clone` and `clone3` return `EPERM`. This
behavioral test ran on Linux/amd64; the filter is cross-compiled for arm64 but
has not been behaviorally exercised there. The fixed worker profile is not an
OS sandbox for arbitrary Go code. No shell, arbitrary executable,
fork/memory/CPU exhaustion, or aggregate cgroup quota is tested or claimed.
Worker result bytes are capped at 64 KiB, the per-worker deadline is five
seconds, and captured stderr is capped at 4 KiB.

## Running the study

For an isolated clean source snapshot, bounded build/run capture, and a
checksummed review archive, use the
[manual native-validation runner](read-broker-manual-runner.md). It defaults to
the merged Bubblewrap 0.9 compatibility baseline and delegates backend trust
and capability checks to this command; it does not run an independent version
probe or retry with weaker isolation.

On Linux amd64 or arm64 with a trusted absolute root-owned Bubblewrap binary
whose bounded `--help` output includes all required options and either
`--ro-bind-fd` or both `--perms` and `--ro-bind-data`, plus usable required
namespaces. Feature detection selects descriptor binding when available and
otherwise selects the bounded sealed-file snapshot mode. A backend without
either complete feature set is refused:

```sh
mkdir -m 700 .read-broker-study-tmp
trap 'rm -rf .read-broker-study-tmp' EXIT
CGO_ENABLED=0 go build -buildvcs=false -o "$PWD/.read-broker-study-tmp/worker" ./cmd/read-broker-study
"$PWD/.read-broker-study-tmp/worker" --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-read-broker-study-report.json
```

The JSON report records version and feature capabilities, the selected bind
mode, worker snapshot provenance and bound, policy, case statuses, and whether a worker started, received a
descriptor, read bytes, or published. It also records the exact planned launch
arguments and FD roles. An unavailable result is not a pass. The command
refuses an untrusted backend or worker and never tries an unrestricted
fallback. It uses disposable synthetic files and removes its fixture tree on
exit. Unit/race tests use injected workers or capability fixtures and are not
native sandbox evidence. Compatibility selection and mount-verification
ordering require native execution on each backend profile before being treated
as established behavior.

Native run records, host-specific diagnostics, and detailed per-case fixture
effects belong in the linked tracking issue rather than this repository.

## Independent same-user actor scenarios

The command includes a separate, same-user fixture process with a fixed
rename/hard-link/replacement allowlist. It starts with an empty environment,
waits for a bounded READY/RUN/DONE handshake, and never starts descendants. The
actor is released only at the coordinator checkpoint after the last broker
revalidation and before duplicating or transferring the read descriptor. That
ordering tests a real check-to-handoff namespace transition; a unit-test hook
with an injected worker is only coordinator regression coverage.

Cases record worker startup/readiness and join, actor readiness/transition and
direct-process join, descriptor transfer, read start, publication, once-grant
consumption, coordinator generations, fixture effect, and an evidence
classification. Under the approved contract, an alias introduced at a
protected exact-file location after an ordinary invocation was prepared does
not, by itself, reclassify that invocation. The existing invocation may finish
from its captured object; a fresh request through that protected route must be
refused. A registered sensitive identity remains refused through aliases and
renames. Root/parent relocation and target replacement test whether the
descriptor continues to refer to the approved object under B; they do not
establish A. An unregistered child moved out of a registered protected
directory remains subject to `DirectoryCoverageIncomplete` refusal.

The supplied older archive's scan-to-handoff event remains an observed
violation under the historical policy. The approved decision changes the
expected outcome only for the already-approved ordinary-object late-alias
case; it does not rewrite that archive or turn a setup refusal into positive
native evidence. Owner registration/rotation is a separate invalidation path:
the owner must register and pin before sensitive writes, then wait for affected
workers to terminate and join. Cleanup pending blocks the write. The
research-only owner lifecycle scenarios now include registration before FD
release, registration while the real worker holds the same FD, registration
after capture before publication, synthetic old/new object rotation, and
provider-only authorization controls. Their expectations and limits are
specified in [`read-broker-owner-lifecycle.md`](read-broker-owner-lifecycle.md);
source presence is not native evidence until the runner executes them.

The scenario/expectation matrix and exact pending cases are in
[`read-broker-native-actor.md`](read-broker-native-actor.md). In particular,
protected directory registrations are not descendant inventories, and
`DirectoryCoverageIncomplete` remains in force. Token Logger and
provider-source owner integration, mount-boundary behavior, and
recursive-child relocation remain separate evidence gaps. The actual harness
auth/settings store cases have their own evidence fields and are summarized by
the manual runner; their implementation remains research-only. The bounded abnormal-lifecycle fixtures are described in
[`read-broker-abnormal-lifecycle.md`](read-broker-abnormal-lifecycle.md): they
observe the exact Bubblewrap process and fixed PID-namespace init, and use
explicit termination-request gates for shutdown and owner barriers. Native
results are valid only for the exact selected commit and trusted backend
recorded by the runner; source presence is not native enforcement evidence.
These observations do not establish arbitrary namespace-descendant cleanup or
a naturally hung worker.

## Authority conclusions

The intended target is object authority (B), subject to independent protected
object rules. A read-only open descriptor identifies the object whose bytes
the worker can read; ordinary external renames do not retarget that descriptor.
This alone does not provide strict live host-path containment (A), prevent
protected bytes copied into an independent object, or make policy scans atomic
with an independent namespace writer. Typed-read success also says nothing
about shell execution. Production integration and tool enablement remain out
of scope until measured evidence and maintainer policy decisions support them.
