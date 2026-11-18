# Issue #319 Gate A: creation broker startup and confinement

**Disposition: the selected original-user-namespace broker candidate fails
the startup-protection gate and remains unavailable.** The deterministic
synthetic control retained an inherited output descriptor opened before the
target became nondumpable, then wrote through it after the protection
transition. This run was on an ineligible host and is not native acceptance of
Yama scope 2 or 3. The independent source deduction is that those Yama modes
restrict ATTACH-class access, while proc-fd access is checked using READ mode.
No Gate B work or production mutation capability is authorized.

The accepted research basis is preserved in
[the creation isolation decision](creation-isolation-design.md) and
[ADR-0005](../adr/0005-investigate-creation-broker-with-protected-authority-chain.md).
This report records the Gate A findings without changing that decision.

## Authority and process inventory

The current executable starts in `cmd/harness/main.go`:

1. `main` handles only internal sandbox worker bootstrap, then starts Cobra.
   The normal path inherits file descriptors from its launcher across `exec`;
   the command does not close or replace standard descriptors before entering
   application code.
2. The command resolves the working directory, creates the sandbox service,
   registers auth/settings protection, loads settings, and builds providers,
   UI, approval and clarification bridges, and the in-memory runtime.
3. `runtime.NewChatWithToolRuntime` owns chat/tool state and the configured
   approval interaction. The `ApprovalBridge` stores pending approval records
   and response channels in memory. The tview frontend owns its visible
   `approvalRequest`; the runtime still validates the response and owns grant
   creation/consumption.
4. `Plugin.Run` calls `tview.Application.Run`. With no injected screen,
   tview creates a tcell screen; on Unix tcell opens `/dev/tty` for its
   terminal input/output. This is the visible approval channel. It is opened
   after runtime and bridge construction in the current composition.
5. The launcher, shell, terminal emulator, and their descriptors are outside
   the KompoSyn object graph. Their identities, descriptor flags, ancestry and
   lifetimes are not inspected or controlled by this executable.

| Holder | Authority or endpoint | Current lifecycle | Gate A consequence |
| --- | --- | --- | --- |
| `komposyn` main process | Chat/runtime state, approval bridge, auth/settings objects as loaded, later any prepared payload and capabilities | Same process from Cobra startup through UI shutdown | Every in-process authority needs protection before admission; memory-control probes were not run in this synthetic study. |
| `komposyn` inherited fd 0/1/2 | Launcher-provided terminal, pipe, file or closed endpoint; exact values vary by launch | Present after `exec` unless close-on-exec | Cannot assume these are closed, harmless or replaced before the actor's startup race. |
| tview/tcell screen | `/dev/tty` input/output on the Unix default path | Opened by `Application.Run`, after runtime construction | Approval presentation and input are part of the authority chain. A retained same-terminal output handle can spoof presentation. |
| launcher/shell/terminal | Process launch and terminal endpoint | External to this process; no protection handoff exists | Scope and inheritance provenance are incomplete. A sysctl value does not prove them safe. |
| proposed broker | Approved parent, stage and release authority | Does not exist | No bounded broker confinement or process supervision was prototyped. |

The worker and UI processes do not hold a provider transcript or creation
payload at startup. Those would be introduced only after a future request.
That ordering does not make the inherited display endpoint harmless: the user
must trust that same terminal while reviewing the later approval. The current
UI and authority state are composed in the same Go process, not in an
independent trusted approval service.

## Startup and protection finding

The considered ordering would set `PR_SET_DUMPABLE(0)` in the first
application code, before auth/settings loading, chat construction, approval
creation, or opening `/dev/tty`. It would then verify the active Yama mode,
credential/user-namespace assumptions, and all-thread state before admitting
creation. This is necessary but insufficient:

- Linux resets the dumpable attribute to 1 when a normal executable is
  launched with `execve`. The launcher cannot set the child attribute in
  advance and rely on it surviving exec.
- An independent same-user actor can race the executable between kernel exec
  and the first protection call. Scope 2 and 3 do not close this interval for
  proc-fd access: `/proc/<pid>/fd` uses `PTRACE_MODE_READ_FSCREDS`, while
  Yama's policy hook applies to `PTRACE_MODE_ATTACH`.
- A proc-fd handle opened in that interval continues to refer to the same
  open file description after the target changes dumpability. The native
  synthetic control observed this for an inherited output pipe: a later open
  was refused, while a descriptor opened before protection still wrote after
  protection.
- For a normal terminal launch, fd 1 may refer to the same controlling
  terminal that the later tcell screen opens as `/dev/tty`. A retained output handle to that same terminal
  can inject display bytes while the user reviews approval. The synthetic
  control establishes the descriptor-retention primitive; it is not a full
  UI spoofing test. The application has no trusted presentation channel to
  distinguish the injected bytes.
- Closing/replacing standard descriptors after startup does not revoke copies
  already acquired by an actor. Deferring `/dev/tty` open until after
  protection still leaves inherited terminal output as an alternate writer
  when fd 1 refers to that same terminal. Designing a separate trusted UI or
  assuming a trusted terminal is outside this task's accepted direction.

The accepted Yama scope-2/3 profile alone cannot supply the missing READ-class
startup protection. Adding and qualifying a separate LSM or procfs policy
profile would be a new design decision, not evidence for this candidate.

The first bullet and the distinction between Yama ATTACH and proc-fd READ are
documented-semantics deductions. The deterministic descriptor-retention
observation below confirms that a later dumpability transition does not revoke
an already opened proc-fd descriptor on the recorded host. It does not claim
that a native run was performed under either selected Yama profile.

The source basis is Linux v7.0's [Yama hook](https://github.com/torvalds/linux/blob/v7.0/security/yama/yama_lsm.c),
which applies its `ptrace_scope` policy to ATTACH-mode checks, and the
[proc-fd access check](https://man7.org/linux/man-pages/man5/proc_pid_fd.5.html),
which uses `PTRACE_MODE_READ_FSCREDS`. The kernel's [proc fd implementation](https://github.com/torvalds/linux/blob/v7.0/fs/proc/fd.c)
confirms that READ-class check. Linux [execve semantics](https://man7.org/linux/man-pages/man2/execve.2.html)
reset dumpability for a normal executable, and [PR_SET_DUMPABLE](https://man7.org/linux/man-pages/man2/pr_set_dumpable.2const.html)
documents the later process-local transition. By contrast,
[`/proc/<pid>/mem`](https://man7.org/linux/man-pages/man5/proc_pid_mem.5.html)
and [`pidfd_getfd`](https://man7.org/linux/man-pages/man2/pidfd_getfd.2.html)
use ATTACH-class checks; those operations were not part of this synthetic
probe. The [user-namespace capability rules](https://man7.org/linux/man-pages/man7/user_namespaces.7.html)
remain relevant to the original-namespace assumption and were not established
by the local capture.

## Confinement decision

The candidate stopped before broker implementation. No syscall filter,
namespace, launch, or descriptor-right profile was claimed as broker
confinement. The following are therefore **unavailable**, not passed:

- Proving the complete authority chain protected before every authority
  transition and every thread that can hold relevant state.
- A bounded rights set for parent/stage handles and the approval/release
  channel.
- Argument/effect confinement for `openat2`, create, write, and publication.
  A seccomp allowlist cannot inspect pointed-to pathname or `open_how`
  contents; it cannot establish exact target authorization by itself.
- Denial of unrelated filesystem effects, network, execution, process
  creation, and authority transfer for every OS thread.
- Worker death/suspension/timeout supervision, incomplete-join admission
  blocking, retained cleanup ownership, cancellation, and resource retirement.
- Binding approval, exact bytes, retained mounted parent plus absent leaf,
  generations, provider destination, and protected-owner rotation to the
  effect point. #267's mounted-object contract is unchanged.

No unrestricted host executor or fallback was prototyped. Because startup
protection fails first, attempting to solve these later gates would not make
the selected candidate acceptable.

## Evidence matrix

| Required case | Evidence class and result | Limitation |
| --- | --- | --- |
| Same-user actor races launch/protection | Synthetic native control: `observed_violation`; actor opened the target's inherited fd 1 while the exec'd target reported dumpable 1, retained it across `PR_SET_DUMPABLE(0)`, then wrote the exact marker. | Deterministic checkpoint demonstrates the startup ordering flaw, not a probabilistic race against the full chat binary. Host was ineligible for the selected profile. |
| Startup proc-fd and inherited stdio/control endpoint | Synthetic native control: `observed_violation` for output-channel integrity. | Does not reproduce a complete tview approval journey or prove keyboard-input forgery. Source confirms tcell opens `/dev/tty`; trust consequence is a design deduction. |
| Deliberately exposed pre-protection descriptor | Synthetic native control: positive open and post-protection write succeeded. | Output pipe is synthetic; same-terminal effect is inferred from the production UI's default terminal endpoint. |
| New proc-fd open after protection | Synthetic native control: refused with permission denied after dumpability became 0. | This is the local host's refusal path, not a mode-2/3 result. |
| ptrace, `/proc/<pid>/mem`, process_vm and pidfd_getfd after protection | Source deduction only for ATTACH-class denial under the selected Yama modes; not invoked by this fixture. | No native mode-2 or mode-3 run. A failed ptrace request would not substitute for the proc-fd positive control. |
| Dumpable positive control | Synthetic native control: target reported dumpable 1; actor opened `/proc/<pid>/fd/1`. | Current container uses Yama mode 1 and a noninitial user namespace; not eligible native acceptance evidence. |
| Broker confinement probes | `unavailable`; no broker was built after the first failed gate. | No claim for filesystem, network, execution, process, thread, or authority-transfer confinement. |
| Missing/unsupported policy or incomplete provenance | Current profile ineligible: Yama is mode 1; current process is in a mapped noninitial user namespace; absence of `CAP_SYS_PTRACE` in the original user namespace is unverified. The report hard-codes candidate eligibility false. | The production application has no Gate A admission gate because creation is not implemented or registered. |
| Death, suspension, cancellation, timeout and delayed join | Probe target and actor both reaped in the observed run. All other lifecycle cases are unavailable. | This fixed two-child probe does not establish broker tree supervision or cleanup ownership. |

The synthetic run had no production credentials, provider content, user files,
database, or live request. Its only write was a fixed marker into a temporary
pipe. The target and actor were both reaped. It is evidence of the kernel
descriptor behavior in this environment, not proof of the complete approval
chain, native acceptance, or creation safety.

## Recorded local capture

Capture details for the working-tree run:

- Go `1.26.11`; Linux `7.0.0-42-generic`; amd64; uid/gid `1000:1000`.
- The disposable fixture reported ext4 magic `0xef53`, mount ID `6339`.
- `ptrace_scope=1`, which is explicitly ineligible. `fs.suid_dumpable=2`.
- The process user namespace was `user:[4026533849]`, with uid map `1000 0 1`;
  `CapEff=0`. This does not prove the original-user-namespace capability
  assumption.
- `/usr/bin/bwrap` was `nobody:nogroup`, mode `755`; it fails the repository
  trust check. The synthetic descriptor probe did not execute Bubblewrap.
- Result: pre-protection proc-fd open succeeded; post-protection open was
  refused; the retained handle wrote `SYNTHETIC_APPROVAL_DISPLAY_CHANNEL`;
  target and actor reaped; classification `observed_violation` for the
  synthetic behavior and `candidate_profile_eligible=false` for this host.

The commit-bound capture used source `2cb48322d99bcdc1feaa440d6701f54a1ccad3b7`.
Its JSON report is `/tmp/komposyn-issue-319-gate-a-startup.json`, SHA-256
`36a6290f55357cee527109de580c40135c2deace36353253ea79decf8390065a`. The
static study binary is `/tmp/komposyn-sandbox-study-gate-a`, SHA-256
`29893394cab7175f326f5fc44cb671975c79586a01ebc3000fe2d4abd63f7d3d`. The
capture archive is `/tmp/komposyn-issue-319-gate-a-capture.tar.gz`, SHA-256
`0b0a86be6fd53978b3717f6f49f7d1d133f8fac2a744cf1a8d59bbc99f1ae51f`; it
contains the binary, JSON report and manifest. The source hashes are recorded
in that manifest. Do not reuse this host to claim mode-2 or mode-3 coverage.

## Reproduction and validation

On Linux amd64, build and run the bounded synthetic control:

```sh
git rev-parse HEAD
go version
CGO_ENABLED=0 go build -trimpath -o /tmp/komposyn-sandbox-study-gate-a ./cmd/sandbox-study
/tmp/komposyn-sandbox-study-gate-a --gate-a-startup-exposure --commit "$(git rev-parse HEAD)" > /tmp/komposyn-issue-319-gate-a-startup.json
sha256sum /tmp/komposyn-sandbox-study-gate-a /tmp/komposyn-issue-319-gate-a-startup.json
```

The output always reports `candidate_profile_eligible=false`; it is a narrow
mechanism probe and is not a substitute for a trusted Linux amd64/ext4 host,
an already-active Yama mode 2 or 3, original-user-namespace credential
verification, and complete confinement evidence. It never changes sysctls,
ownership, identities or privileges and never executes a fallback.

The Gate A code and documentation require the normal repository validation:

```sh
go mod verify
go vet ./...
go test ./...
go test -race ./...
make check
```

Ordinary Go and race tests do not establish native process or filesystem
enforcement. This report does not close #319, unblock #321, close #267, or
complete umbrella #315. Gate B and any creation enablement require a new
reviewed decision.
