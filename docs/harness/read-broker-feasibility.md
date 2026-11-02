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
static ELF, and passed to Bubblewrap with `--ro-bind-fd`. This requires a
Bubblewrap release with that option (0.10 or newer). The broker similarly
retains the opened target and parent directory descriptors. A pathname change
after open therefore does not redirect the bytes read by the worker; it also
does not prove strict live host-path containment. The worker descriptor pins
the inode against pathname replacement, but this prototype does not prevent a
same-UID process with write access to that inode from modifying its contents
in place. Native experiments do not mutate the worker image.
Bubblewrap added descriptor-based bind options in [release 0.10.0](https://github.com/containers/bubblewrap/releases/tag/v0.10.0).

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
device mounts; and `--die-with-parent`. It supplies only seccomp FD 3, control
socket FD 4, and worker executable FD 5 to Bubblewrap. Before operation release,
the worker probes `no_new_privs`, seccomp mode 2, denied path opens and socket
creation, absence of the synthetic inherited environment marker, and absence
of any non-protocol descriptor. The host command receives only that synthetic
marker; Bubblewrap applies `--clearenv`. Standard input is `/dev/null`, stdout
is discarded, and stderr is captured with a 4 KiB cap.

The fixed worker profile is not an OS sandbox for arbitrary Go code. No shell,
arbitrary executable, fork/memory/CPU exhaustion, or aggregate cgroup quota is
tested or claimed. Worker result bytes are capped at 64 KiB, the per-worker
deadline is five seconds, and captured stderr is capped at 4 KiB.

## Running the study

On Linux amd64 or arm64 with a trusted absolute root-owned Bubblewrap binary
(version supporting `--ro-bind-fd`) and usable required namespaces:

```sh
mkdir -m 700 .read-broker-study-tmp
trap 'rm -rf .read-broker-study-tmp' EXIT
CGO_ENABLED=0 go build -buildvcs=false -o "$PWD/.read-broker-study-tmp/worker" ./cmd/read-broker-study
"$PWD/.read-broker-study-tmp/worker" --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-read-broker-study-report.json
```

The JSON report records provenance, policy, case statuses, and whether a worker
started, received a descriptor, read bytes, or published. An unavailable result
is not a pass. The command refuses an untrusted backend or worker and never
tries an unrestricted fallback. It uses disposable synthetic files and removes
its fixture tree on exit. Unit/race tests use injected workers and are not
native sandbox evidence.

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
