# Abnormal lifecycle evidence for the read-broker study

This note defines the bounded process-lifecycle experiment for issue #215.
It is research infrastructure only: no production runtime, owner, credential,
provider, or model-facing tool is connected.

The report retains the existing 38 scenarios and adds three explicit rows:
coordinator death with a held capability, shutdown deadline with a retained
cleanup obligation, and owner-write blocking during cleanup pending. The
unavailable report therefore lists 41 rows; absent native execution, all 41
remain unavailable rather than passing.

## Process and capability ownership

```text
outer study supervisor (survives the coordinator in the death fixture)
  └─ fixture coordinator (owns session, grant fixture, registry and pins)
       └─ trusted Bubblewrap command / namespace init
            └─ fixed worker (same reviewed PID-namespace process; Go threads only)
                 └─ received read-only synthetic target FD
```

The coordinator owns the `FixtureSession`, active operation record, once-grant
fixture, registry pins, prepared target FD, Bubblewrap `exec.Cmd`, control
socket, and cleanup obligation. After SCM_RIGHTS handoff, the worker owns its
received copy of the read-only target FD. The fixture supervisor owns the
disposable root until every task-owned process is observed stopped and the
direct Bubblewrap child is reaped. The worker receives no supervisor channel,
host procfs mount, owner registry, grant state, or host path.

The normal shutdown order is:

```text
invalidate revisions and block admission/publication
  -> request worker termination
  -> wait for and observe direct Bubblewrap process exit/reap
  -> complete operation record
  -> release registry pins, executable snapshot and coordinator resources
```

If a shutdown deadline expires before join confirmation, the operation record,
registry pins, launcher resources, and cleanup obligation remain owned. Reads
and publication stay blocked. A later cleanup retry may complete that same
record; it does not replay a read or restore its consumed once approval.

## Failure and acceptance matrix

| Failure / checkpoint | Processes and capability | Cleanup owner and completion evidence | Acceptance interpretation |
| --- | --- | --- | --- |
| Coordinator is SIGKILLed while worker acknowledges the exact held target FD | Outer supervisor retains coordinator process handle and pidfds for both Bubblewrap and PID-namespace init; worker has the only transferred target FD | Immediately before killing the coordinator, the supervisor confirms both pidfds are still live. A fixed 60-second hold-mode watchdog and 45-second coordinator operation deadline leave a bounded observation window; after coordinator death, pidfds are observed before any supervisor cleanup | A stale held event or already-exited process is inconclusive and never a parent-death pass. Backend-driven direct process exit is distinct from later supervisor cleanup. Does not prove arbitrary namespace descendants. |
| Coordinator shutdown deadline expires while a real worker holds the capability | Coordinator session still owns the operation record, registry pins, and launcher; worker remains at its read-release gate | The fixture wrapper routes both operation-context cancellation and explicit `Terminate` through a labeled gate. Its fixed worker watchdog is 60 seconds. The supervisor observes the real worker alive with pidfd; it then releases the gate, sends the real kill, waits, and retries `Close` | Deterministic control-plane fault injection, not a natural kernel join failure. The normal non-held worker remains caller-cancelled with the existing five-second watchdog. Pass requires retained ownership and blocked admission/publication until actual wait/reap confirmation. |
| Owner rotation is requested while a real worker holds the capability | Same synthetic target object is held by worker and by fixture owner's write FD; registry commits the protected pin and policy revision | The fixture wrapper routes both operation-context cancellation and explicit `Terminate` through a labeled gate. Owner completion is attempted during the pending interval and must record no authorization, start, or bytes. After gate release, the real worker is killed and waited; retry verifies the write FD matches the retained pin | Pass only if no sensitive bytes are written before join and the retry writes through the exact registered object. Once consumption remains one; stale read publication remains suppressed. This is not a natural kernel wait failure. |
| Setup/handshake fails or observer is interrupted | Only already-created task-owned handles/resources may remain | Every started child remains tracked; abort sends a bounded coordinator command where possible, then exact `Process`/pidfd cleanup is attempted and observed | Missing readiness or failed observer cleanup is unavailable/inconclusive evidence, never a pass. Retain the fixture root if a process may still use it. |

## Process observation and trust assumptions

The supervisor receives a host PID plus `/proc/<pid>/stat` start-time token over
a private inherited socket only after the real worker has acknowledged the
capability identity. It opens a pidfd and rechecks the start-time token before
acting on that identity. Reports omit numeric PIDs and temporary paths. The
pidfd observes one exact process across PID reuse; a PID lookup, a successful
signal request, or `exec.Cmd.Wait` on the coordinator is not worker-exit
evidence.

`PR_GET_CHILD_SUBREAPER` writes its previous value through the pointer argument;
the supervisor uses that form, distinguishes GET from SET failures, and restores
the prior value on confirmed cleanup. A regression exercises GET, SET, and
restore on the native Linux process.

The outer process temporarily becomes a Linux child subreaper for the
coordinator-death fixture, so an orphaned task-owned Bubblewrap child can be
reaped by the surviving supervisor. It polls the worker pidfd and records its
state immediately before coordinator termination and again before sending any
cleanup signal. A missing/error/dead liveness precondition rejects the parent-
death classification. If `--die-with-parent` does not stop
the worker by the deadline, that observation is preserved as a backend cleanup
failure; only then may the supervisor signal that exact pidfd and attempt to
reap it. A fixture root is retained if cleanup cannot be confirmed.

Bubblewrap's `--die-with-parent` uses a parent-death signal for its command;
with `--unshare-pid`, the PID namespace init's exit causes the kernel to
terminate remaining namespace processes. The profile uses `--as-pid-1` and a
fixed worker whose seccomp filter denies process-like `clone` and `clone3` while
allowing only Go runtime thread creation. Native evidence therefore reports
the exact pidfd-targeted command/namespace-init process and its thread/process
topology. Direct wait/reap is not presented as a generic namespace-wide
descendant guarantee. Any namespace-wide inference is explicitly labeled as
based on the reviewed Bubblewrap/kernel contract, not a count of arbitrary
descendants observed through host procfs.

References for that inference are upstream [Bubblewrap 0.9.0 source](https://github.com/containers/bubblewrap/blob/v0.9.0/bubblewrap.c),
the Linux [`pid_namespaces(7)` documentation](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html),
and Bubblewrap maintainers' explanation of [`--die-with-parent` with a PID
namespace](https://github.com/containers/bubblewrap/issues/529). They describe
the mechanism; only the fixture run measures the installed backend.

The study cannot preserve in-memory permission or grant state after the
coordinator dies, and does not claim restart recovery. The held descriptor is
synthetic. No provider response, real credential, settings file, Token Logger
database, or user data is involved. No process/resource exhaustion is attempted.

## Evidence classifications

- **Measured pass:** the stated preconditions ran on the exact native profile,
  observed fixture effects match the expected ordering, and required cleanup
  was confirmed.
- **Observed cleanup violation:** the backend worker remained alive at the
  deadline before the supervisor acted, or another prohibited effect occurred.
  Later supervisor cleanup does not erase this result.
- **Unavailable:** a required platform/backend/pidfd/sandbox precondition did
  not run.
- **Inconclusive:** the experiment started but failed to establish a required
  identity, event, effect, or cleanup fact.
- **Fault-injection evidence:** the experiment deliberately delays a control
  action while a real Bubblewrap worker holds the real synthetic FD. In the
  shutdown and owner cases, it routes operation cancellation as well as the
  explicit termination call through the gate and uses a bounded 60-second
  worker watchdog. It tests retained obligations and barriers; it does not
  establish a natural process hang or kernel cleanup failure.

Direct child reaping, namespace cleanup, descriptor closure, owner write
eligibility, and stale-result suppression remain separate reported facts.
Filesystem and shell tools remain unavailable.
