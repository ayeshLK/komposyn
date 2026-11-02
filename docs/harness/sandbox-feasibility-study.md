# Linux sandbox feasibility study (#215)

Status: **research harness only**. No production sandbox service or
model-facing filesystem/process tool is implemented or registered. This study
does not change authorization, grants, root selection, or runtime dispatch.
Authority contract A remains the existing enablement gate.

## Reproduce

Build on Linux/amd64 or Linux/arm64 with Go 1.26.10 or newer. Native study
execution additionally needs a root-owned, non-group/world-writable system
bubblewrap executable and functioning user namespaces/seccomp. The arm64
command and test package cross-compile; no arm64 host execution was performed.
The executable path is explicit and absolute; the harness does not search
`PATH`. No installation, privilege escalation, host security-setting change,
credential, or external service is required.

```sh
CGO_ENABLED=0 go build -o /tmp/komposyn-sandbox-study ./cmd/sandbox-study
/tmp/komposyn-sandbox-study \
  --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-sandbox-study-report.json
```

The study creates synthetic content under a fresh `os.MkdirTemp` directory,
launches one gated typed Go fixture worker at a time, and removes the fixture
tree on normal completion. Each worker has a five-second deadline, 64 KiB
protocol-line and stderr bounds, a 32 MiB empty root tmpfs, and a 16 MiB private
`/tmp`. No exhaustion test is run. A worker that does not start under the
complete requested policy is reported unavailable; the harness never retries
outside bubblewrap. Exit status 3 means native evidence was unavailable or a
candidate failed to launch. JSON rows marked unavailable are not passes.

The command itself must be a static ELF because only that executable is mounted
inside the empty root. The worker accepts only fixed fixture operation names;
it parses no shell text and starts no helper command. `PATH=/nonexistent`
exists only to test that helper lookup fails. This is typed-worker evidence;
there is no shell test or shell capability claim.

## Effective candidate policy

The current Linux profile constructs the following argument vector for
`/usr/bin/bwrap` (the runtime report records the selected path, version, hash,
owner and mode):

```text
--die-with-parent --new-session
--unshare-user --unshare-pid --unshare-ipc --unshare-uts --unshare-net
--disable-userns --cap-drop ALL --clearenv
--setenv HOME /tmp/home --setenv TMPDIR /tmp
--setenv PATH /nonexistent --setenv LANG C.UTF-8
--size 33554432 --tmpfs /
--dir /tmp --size 16777216 --perms 0700 --tmpfs /tmp
--dir /tmp/home --dir /scope
--ro-bind <static-study-executable> /worker
--bind <disposable-fixture-root> /scope
[--size 1048576 --perms 0700 --tmpfs /scope/<fixture-protected-path>]
--seccomp 3 --chdir /scope -- /worker --sandbox-study-worker <operation> <relative-path>
```

The empty root and `/tmp` are private, bounded writable tmpfs mounts; the only
host-backed writable mount is the disposable fixture tree. No `/proc`, `/sys`,
`/dev`, host root, home, runtime socket, credential file, or system library is
mounted. Bubblewrap receives only standard input/output/error plus the seccomp
program on fd 3; no `--preserve-fds` option is used. The Go parent passes only
the synthetic `KOMPOSYN_STUDY_SECRET=fixture-secret` marker to verify that
`--clearenv` removes inherited environment values; it reads and passes no host
environment variables. Bubblewrap clears the environment and sets the values
above. Its `PWD` behavior is determined by `--chdir` and bubblewrap's
documented `--clearenv` semantics.
The profile requires every requested namespace; it uses no `*-try` option and
has no weaker fallback. Nested user namespaces, all capabilities, network and
socket syscalls, mount/namespace changes, process inspection, and selected
privilege-changing syscalls are denied. The architecture-checked cBPF filter
returns `EPERM` for `socket`, `socketpair`, `connect`, `bind`, `listen`,
`accept`, `accept4`, `sendto`, `recvfrom`, `sendmsg`, `recvmsg`, `setsockopt`,
`getsockopt`, `shutdown`, `mount`, `umount2`, `pivot_root`, `chroot`, `unshare`,
`setns`, `ptrace`, `process_vm_readv`, `process_vm_writev`,
`process_madvise`, `process_mrelease`, `open_by_handle_at`, `pidfd_getfd`,
`pidfd_open`, `pidfd_send_signal`, `kcmp`, `init_module`, `finit_module`,
`delete_module`, `reboot`, `kexec_load`, `kexec_file_load`, `iopl`, `ioperm`,
`acct`, `swapon`, `swapoff`, `keyctl`, `perf_event_open`, and `bpf`. Audit
architecture mismatch kills the worker. Other syscalls are allowed; this is
not a production-minimized syscall allowlist. The fixed typed worker starts no
subprocess; no general process-count limit is configured.

Only the fixture tree is writable in the worker's mount view. A nested
protected fixture path can be hidden by a private tmpfs mount, but the alias
fixture deliberately tests whether an alternate name still exposes its object.
The worker has no cgroup membership management. Process count, memory outside
the bounded tmpfs, and CPU are not proven to have aggregate limits. The Go
coordinator allows one study worker at a time and the fixed worker has no
subprocess operation; this is not a bound against arbitrary worker code.

Bubblewrap documents that `--unshare-all` is not equivalent to requiring every
namespace: it tries user and cgroup namespaces, so this study names each
required namespace explicitly. Bubblewrap's documentation also specifies
`--size` as a limit for the following tmpfs and `--disable-userns` as requiring
user namespace setup. These are configuration semantics, not measured security
claims. See the [upstream bubblewrap manual](https://github.com/containers/bubblewrap/blob/v0.9.0/bwrap.xml).

## Authority contracts

**A — strict live host-path containment.** Every read, create, update, rename,
and deletion effect stays beneath the approved host pathname at the time of the
effect. Root, parent, target and recursive-child relocation, late hard links,
and deletion-entry substitution must be rejected or fail closed. A mount or
descriptor that retains access to an object after its approved host name moves
does not satisfy A.

**B — mounted-tree/object authority.** The worker may continue using objects
exposed in its private mount view after the host renames them or creates another
hard-link name. That behavior can be consistent with B even when the object is
no longer under the original host pathname. B is not adopted by this study and
cannot be called a pass for A.

The PR #213 tests distinguish object identity (a descriptor refers to the same
object), pathname containment (a path walk is within the selected root at that
instant), and aliasing (other names or mounts can refer to that object). The
bubblewrap fixtures adapt those races to an independent host-side actor and a
worker held at an explicit operation gate. A success after the actor relocates
an opened object would violate A and be consistent with B; it would not prove
all of B. The executable records the actual host fixture state rather than
inferring it from the worker's exit code.

## Operation-start and revocation protocol

The study's standalone coordinator has this ordering:

1. Create bubblewrap with the operation payload fixed to a validated fixture
   operation; register its child identity before accepting startup.
2. The worker opens the target object/directory required by that race case,
   emits `READY`, and waits with no effect permitted.
3. The coordinator checks its captured revision and cancellation state under
   the gate's mutex. The host-side actor can make a fixture namespace change
   while the child is waiting.
4. If still current, the coordinator releases the worker. The worker emits
`STARTED` immediately before its fixture syscall. A production UI should
   report `launching` before this acknowledgement and `running` only after it.
5. A revoke/cancel before release blocks the gate and terminates the child. A
   revoke after release requests termination; an effect may already have
   happened, so it cannot be reported as not executed or rolled back without
   evidence. Buffered output from a stale generation must be discarded.

The gate tests deterministic before-release, after-release and concurrent
release/revocation orderings. They do not establish kernel namespace safety.
Parent death, descendant termination and cleanup after an abrupt host crash are
not proven by this study. Bubblewrap's `--die-with-parent` is requested, but
the descendant behavior was not natively exercised here.

The existing runtime consumes a once-grant before calling
`ToolEnforcer.Execute`. The study gate receives a fact that consumption already
happened and never consumes a grant itself. A future integration must keep one
consumption owner: either retain runtime consumption and join revision checks
to launch admission, or explicitly move/delegate consumption to a single
coordinator at that admission point. It must not consume in both places. If
setup fails after an existing once grant was consumed, do not silently restore
it; require a fresh user authorization. A setup refusal before release is
`not_executed`; after release, a lost acknowledgement/result is `unknown`, and
a known incomplete mutation is `partial`. The study reports these separately
from A/B containment classification. It changes none of that production
behavior.

## Evidence matrix

The following are the intended candidate fixture cases. A native case is a
measured pass only when the JSON output includes its `measured` result and
fixture state. The execution environment used for this draft blocked the
outer command launcher before the study executable ran, so **none of the
candidate rows below has a native observation yet**.

| Fixture | Expected under A | What would be consistent with B | Current result |
| --- | --- | --- | --- |
| Selected-root relocation/replacement after open | No read or create through moved root; reject replacement | Read/write through retained root or file object | Unavailable: candidate not started |
| Parent relocation before create/delete | No create or deletion in moved parent; reject changed path | Create/delete relative to retained parent descriptor | Unavailable: candidate not started |
| Opened target relocation before update | No write through moved target | Write to retained target object | Unavailable: candidate not started |
| Existing hard-link alias | No mutation visible through outside alias | Same exposed inode changes through either name | Unavailable: candidate not started |
| Late hard-link creation after open | No mutation visible through new outside alias | Retained descriptor mutates same inode | Unavailable: candidate not started |
| Deletion target replaced by symlink | Refuse changed directory entry | Remove the replacement entry if it is in the mounted tree | Unavailable: candidate not started |
| Recursive child moved outside root | No removal from moved child | Retained child object remains mutable | Unavailable: candidate not started |
| Symlink traversal to unmounted host fixture | Refuse and leave outside fixture intact | B alone does not permit objects absent from the mounted view | Unavailable: candidate not started |
| Nested protected path and hard-link alias | Deny direct path and every alias | A path mask may hide one name while another name exposes the object | Unavailable: candidate not started |
| Rename into an unmounted scope | Refuse destination | B permits only objects actually exposed in the private view | Unavailable: candidate not started |
| Network, host path/process, environment, fd and helper probes | No host access/leakage | These are isolation properties separate from A/B | Unavailable: candidate not started |
| Private scratch, bounded output, cancellation before release | Stay inside tmpfs/output/time bounds; no effect before release | Not applicable | Unavailable: candidate not started |
| Partial mutation outcome | Report a known partial effect distinctly from failure or success | Not applicable | Unavailable: candidate not started |
| Mount crossing | Reject an independently mounted child | B is mount-view authority, not a promise of path-level mount refusal | Unavailable: not safely exercised |
| Parent death/descendants | No surviving worker tree | Not applicable | Unavailable: no cgroup kill test |
| Aggregate pids/memory/CPU | Effective configured limit must be demonstrated | Not applicable | Unavailable: no delegated cgroup was created or modified |
| Shell execution | Separate explicit shell policy | Typed worker result cannot establish shell behavior | Not implemented or run |

No namespace, network, protected-path, alias, output, or cleanup test is
classified as passed. The tool integration remains blocked by A and by missing
native evidence for the other isolation properties.

## Environment recorded for this draft

The repository base was merged PR #213, `84aeadc21ee137ab418d75b3edeb56a17a61927e`.
The study source itself was built statically with Go 1.26.10 on Ubuntu
24.04.5 LTS, Linux `7.0.0-38-generic`, `linux/amd64`. The trusted candidate
binary inspected read-only was `/usr/bin/bwrap`, root-owned mode `0755`,
bubblewrap 0.9.0, SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`.

`/proc/sys/user/max_user_namespaces` reported `55603`; `/proc/self/uid_map`
reported `0 0 4294967295`. These values indicate configured capacity/mapping,
not that the nested candidate launch is allowed. The normal native invocation
was attempted without escalation and failed before the Go study ran:

```text
bwrap: setting up uid map: Permission denied
```

This is the surrounding agent command launcher failing its own uid-map setup.
It is neither a result from `/usr/bin/bwrap` nor evidence that the candidate
backend failed. It also does not prove bubblewrap works. The candidate tests
were not retried with elevated or unrestricted execution.

The host cgroup v2 root exposed `cpuset cpu io memory hugetlb pids rdma misc
dmem`. At the observed user `app.slice`, `cpu memory pids` were visible and
`memory pids` were enabled in `cgroup.subtree_control`; the relevant user-owned
control file passed a read-only write-access check. No cgroup was created, and
no controller was written. Thus controller visibility and apparent delegation
are observations, but usable child-cgroup provisioning and actual aggregate
limits remain unverified. The host command context also reported
`CapEff=0`, `NoNewPrivs=0`, and `Seccomp=0`. No process/memory exhaustion was
attempted.

## Integration findings and decisions still open

**Recommendation for the next review:** preserve A as the enablement gate and
keep every filesystem/process tool unavailable. For the next native study run,
use a disposable Linux/amd64 host equivalent to Ubuntu 24.04.5, kernel
`7.0.0-38-generic`, with working unprivileged user namespaces, seccomp filter
support, and root-owned bubblewrap 0.9.0. These are the measured host's
versions, not proof that this profile runs there and not an approved product
minimum. No product minimum kernel or bubblewrap version is established; the
host must satisfy every requested namespace/filter feature at launch or refuse
without fallback. Continue with system bubblewrap plus Go orchestration; this
study has not demonstrated a requirement for a custom launcher or extracted
Codex helper. Revisit a narrow launcher only if native results identify a
specific missing setup/coordination capability. Treat delegated cgroup v2 as a
separate resource-control decision, not an assumed product prerequisite.

- **Authority:** keep A as the gate. The bwrap mount namespace and the fixture
  design are candidates for testing, not proof that A is satisfied. Any move to
  B needs an explicit maintainer decision and changed user-facing guarantees.
- **Permission meaning:** the target considered by this task is that scoped
  write includes read, create, modify, rename and delete. A writable bind mount
  is a mechanism, not an authorization grant. This study does not change the
  current central evaluator, which still treats deletion separately. Any
  permission change needs a separate reviewed implementation and must preserve
  action authorization and network separation.
- **Protected inventory:** owners would need to provide their *effective*
  protected paths, including overrides and ancillary files: auth owns the
  resolved auth file and auth lock/temp files; settings owns the effective
  settings path; provider adapters/collector configuration own the source roots
  they actually read; each Token Logger Store/backup caller owns its active
  database, WAL/SHM and selected backup paths. Runtime composition owns the
  worker/helper paths and socket/fd allowlist. Never infer these through cwd,
  `HOME`, repository discovery, model arguments, extension guessing or machine
  scans. A recommended eligibility rule is to refuse a requested capability
  when an owner required for its protected category has not registered its
  effective paths. This does not claim that every unrelated application file
  can be discovered. A path mask alone does not cover hard-link aliases,
  ancestor moves or mount aliases; the native alias experiment is still
  unavailable.
- **Launching and running:** creating/registering bubblewrap is `launching`;
  startup acknowledgement alone is still not execution; only a released
  operation gate plus the worker's `STARTED` acknowledgement is `running`.
  A proven failure before gate release can map to `not_executed`; after release,
  lost worker/protocol state is `unknown`, known incomplete mutation is
  `partial`, and no uncertain mutation is replayed. Stale buffered output is
  suppressed after revision change.
- **Once-grants:** the existing runtime remains the single consumer unless a
  later reviewed change atomically delegates that responsibility. The launch
  coordinator must serialize revision validation, admission and release with
  revoke/reset/shutdown; duplicate consumption is prohibited.
- **Resources/provisioning:** delegated cgroup v2 is only an evaluated proposal,
  not an approved product prerequisite. Here, process counts and aggregate
  memory/CPU were not enforced or tested. Before supporting arbitrary worker
  code or shell, demonstrate effective aggregate bounds or keep that profile
  unavailable. System bubblewrap plus Go orchestration is enough for this
  feasibility harness; native results are required before deciding whether a
  narrower launcher is needed. No Codex helper was extracted or added.
- **Platform:** the harness has Linux/amd64 and Linux/arm64 build paths; arm64
  was cross-compiled only and its native behavior is unverified. The host facts
  and attempted native launch in this report are amd64 only. macOS and Windows
  are outside this Linux study and remain unavailable. A version or compile
  check is not behavioral evidence.

No root-selection UX, protected-path policy, B contract, production sandbox
service, shell, filesystem tool, or new permission semantics are approved by
this study. The next decision is whether to provide a native disposable host
where the exact test can run, and then review measured A/B outcomes before
choosing any product contract or minimum supported environment.

## Validation for this change

Automated Go checks and race tests validate the harness's bounded readers,
trusted-path validation, BPF serialization shape, fixture construction, and
operation-gate ordering. Linux/arm64 command and test-package cross-builds
validate compilation only. They do not validate kernel enforcement. The native
suite must be run separately using the commands above on an eligible disposable
Linux host; this execution could not run it.

Primary references: [bubblewrap 0.9 manual](https://github.com/containers/bubblewrap/blob/v0.9.0/bwrap.xml),
[Linux cgroup v2 documentation](https://docs.kernel.org/admin-guide/cgroup-v2.html),
[bubblewrap source](https://github.com/containers/bubblewrap/tree/v0.9.0), and
the existing [#213 filesystem policy/evidence](filesystem-enablement-policy.md).
