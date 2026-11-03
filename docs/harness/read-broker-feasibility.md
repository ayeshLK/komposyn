# Experimental brokered read feasibility harness

This package and command are isolated research infrastructure. They are not
used by production dispatch, register no filesystem tools, and have no provider,
credential-store, UI, or persistent-state integration.

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
bounded result packet. There is no fallback launch path.

The worker executable is opened and pinned by the launcher, validated as a
static ELF, and passed to Bubblewrap through a feature-selected read-only bind.
The trusted backend is first checked for path ownership and permissions; only
then do bounded `--version` and `--help` probes run. The parsed help options,
not the reported version, determine whether `--ro-bind-fd` is available. The
version is retained only as provenance. Missing required launch options,
malformed probe output, timeout, and output overflow refuse the worker before
any target-data descriptor is sent.

When `--ro-bind-fd` is available, the launcher gives Bubblewrap FD 5 as the
mount source and a separate duplicate of the pinned worker at FD 6. Bubblewrap
consumes FD 5 after mounting; `--sync-fd 6 --as-pid-1` retains only the
independent verifier descriptor through exec. The trusted fixed executable
then checks that `/worker` is a regular file whose device and inode match FD 6,
and closes FD 6. Missing, standard, duplicate, symlinked, or mismatched
verification descriptors fail startup. The worker then reads the exact
host-prepared BPF program from FD 3, checks it against the compiled policy,
sets `no_new_privs`, and installs it with `SECCOMP_FILTER_FLAG_TSYNC`. Failed
thread synchronization or filter installation aborts startup. FD 3 is closed
before isolation probes and readiness. The startup descriptor inventory also
requires Bubblewrap's consumed FD 5 to be absent. No target-data descriptor is
transferred until verification, probes, authorization, and generation checks
have succeeded.

The older path-based compatibility proposal is deliberately refused. Bubblewrap
0.9 handles `--ro-bind /proc/self/fd/5 /worker` as a pathname source; its setup
resolves source pathnames before mounting. A same-user actor can replace the
mount candidate between path resolution and mount, causing an unverified
executable to run before its own verifier or seccomp code. Comparing that
already-running image to FD 6 cannot establish trust in the verifier. When
`--ro-bind-fd` is absent, capability inspection reports
`legacy-path-bind-refused` and the launcher returns unavailable without
producing launch arguments. No weaker fallback is attempted.

The compatibility proposal was compared with the Codex Linux sandbox at
checkout `970111f6cb62cc02fe36a82ea7d71b37a2889c97`
(`linux-sandbox/src/launcher.rs`, `fd_mount.rs`, and `linux_run_main.rs`); no
Codex source was copied. Its in-worker verification sequence does not by itself
solve the older Bubblewrap pathname replacement race. See upstream
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

## Protected registrations and directory limits

Owner registrations pin identities for protected files and directories and
retain retired pins until registry shutdown. Rotation stages and pins the next
set before adding it; owners must register a protected temporary object before
writing sensitive content there. Registration and pin/location budgets are
bounded and fail closed. No machine-wide discovery or model-controlled
registration occurs.

A registered directory pins the directory object and its location rule. The
broker checks descriptor ancestry and performs bounded current-tree scans; it
does not thereby pin all existing or future descendants. An unregistered
regular file sharing the protected directory's device is refused, which is
conservative and may make broad same-filesystem read scopes unusable. Exact
registered files remain protected through hard-link aliases and renames while
their pins remain live. Directory moves/replacements, scan races, independent
copies of sensitive bytes, and objects moved after the last scan remain limits
that require separate policy/mechanism review.

The earlier Python issue probe registered/pinned its initial authentication
file; it did not pin the directory. Its ancestor result demonstrated missing
descendant identity coverage. The fixture here registers a directory explicitly
and demonstrates both an in-tree descendant refusal and fail-closed refusal of
an unpinned same-device descendant moved out. Neither result means a directory
descriptor identifies every descendant.

| Registration owner | Required protected inputs | Generic read policy | Rotation rule |
| --- | --- | --- | --- |
| Authentication owner | Credential/auth files and temporary replacement objects | Denied | Register and pin temp object before writing sensitive bytes; keep old pins until shutdown |
| Settings owner | Application settings/state objects | Denied | Same register-before-sensitive-write ordering |
| Token Logger owner | DB, WAL, SHM, backup objects and reserved locations | Denied | Register active/rotating paths and objects; preserve old pins |
| Provider source owner | Provider-owned source objects/locations | Explicit once approval per invocation; no mutation capability here | Register replacement before it can become a source |

No production owner is wired into this experiment. Owners must register all
relevant paths and objects; missing registrations make the affected scope
ineligible. Inode identity alone cannot detect an independent copy of protected
bytes.

The fixture lock order is coordinator/session mutex before registry mutex;
registry staging opens and pins objects before taking its commit lock. There is
no owner callback while either lock is held, and this prototype has no owner
mutex integration. A production rotation protocol would need each owner to
serialize its own write/rotation, register and pin the next object before
sensitive bytes are written, atomically publish the registration revision,
then retain prior pins until no authorized read can refer to them. It must not
drop old policy entries before publishing the new revision. Location rules
apply at path preparation/revalidation; inode rules apply to the pinned object.
Neither makes an independent namespace transition atomic with a broker scan.

Credential/authentication material, settings, and registered Token Logger
database files are denied to generic reads. Provider-source files are only
eligible for explicit invocation approval and are never mutable through this
harness. These registrations are synthetic fixtures here; production owners
are not wired into this package.

## Effective worker profile

The launcher requests user, mount, PID, IPC, UTS, and network namespaces with
no optional `try` flags; a new session; dropped capabilities; disabled nested
user namespaces; an 8 MiB empty tmpfs root; one read-only, descriptor-pinned
worker executable; cleared environment; no host home, root, sockets, proc, or
device mounts; `--die-with-parent`; and `--as-pid-1`. It passes only seccomp FD
3, control socket FD 4, pinned worker mount-source FD 5, and its independent
duplicate verifier FD 6. Bubblewrap consumes FD 5 for `--ro-bind-fd` and uses
`--sync-fd 6` to retain the verifier across exec, and the startup
bootstrap closes it after checking `/worker` identity. The bootstrap also
closes seccomp FD 3 after installing the exact architecture-checked filter.
Before operation release, the worker probes `no_new_privs`, seccomp mode 2,
denied path opens/socket creation, absence of the synthetic inherited
environment marker, and absence of any non-protocol descriptor. The exact BPF
program includes process-creation denials; the study does not issue a process
creation syscall as a behavioral probe, so this part is established only by
the program construction and must not be reported as a measured syscall test. The
host command receives only that synthetic marker; Bubblewrap applies
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

On Linux amd64 or arm64 with a trusted absolute root-owned Bubblewrap binary
whose bounded `--help` output includes `--ro-bind-fd`, all other required
options, and usable required namespaces. A backend without `--ro-bind-fd` is
refused:

```sh
mkdir -m 700 .read-broker-study-tmp
trap 'rm -rf .read-broker-study-tmp' EXIT
CGO_ENABLED=0 go build -buildvcs=false -o "$PWD/.read-broker-study-tmp/worker" ./cmd/read-broker-study
"$PWD/.read-broker-study-tmp/worker" --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-read-broker-study-report.json
```

The JSON report records version and feature capabilities, the selected bind
path, policy, case statuses, and whether a worker started, received a
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

## Authority conclusions

The intended target is object authority (B), subject to independent protected
object rules. A read-only open descriptor identifies the object whose bytes
the worker can read; ordinary external renames do not retarget that descriptor.
This alone does not provide strict live host-path containment (A), prevent
protected bytes copied into an independent object, or make policy scans atomic
with an independent namespace writer. Typed-read success also says nothing
about shell execution. Production integration and tool enablement remain out
of scope until measured evidence and maintainer policy decisions support them.
