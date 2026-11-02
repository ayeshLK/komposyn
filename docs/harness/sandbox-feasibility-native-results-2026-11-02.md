# Native sandbox feasibility results — 2026-11-02

Status: **native feasibility evidence only**. The run does not enable
filesystem or shell tools, adopt authority contract B, or change production
authorization/dispatch. Issue #215 remains open. The earlier blocked attempt
is preserved in [PR #217](https://github.com/ayeshLK/komposyn/pull/217); that
draft remains a record of a different execution attempt.

## Result

The merged #216 study started and completed in the supplied Linux environment.
Its study executable exited 0 because the fixture suite completed. The runner
reported **10 observed violations, 3 observed prevented cases, 5 narrow
measured passes, and 5 unavailable cases**. The runner verdict is “observed
violations; containment is not established.” Exit 0 is not an enforcement
pass.

The strongest finding is that the candidate did not satisfy **A, strict live
host-path containment**, in several deterministic same-user namespace races.
Effects remained possible through moved objects, outside hard-link aliases,
and a substituted deletion entry. A separate test read a protected fixture
through a hard-link alias after masking its direct pathname. That is a
protected-object confidentiality failure and remains relevant even if a
different authority contract is later considered.

Some effects are consistent with **B, mounted-tree/object authority**: the
worker continued operating on objects retained in its mount view after their
host names changed. This does not prove all of B, establish a product boundary,
or show that A is impossible for every mechanism. A remains the enablement
gate unless maintainers explicitly decide otherwise. No filesystem or shell
tool should be enabled from this evidence.

## Provenance and environment

| Item | Recorded value |
| --- | --- |
| Source commit | `cdb904b0d4353b4362a8b6508e7363b9d1d40e64` (merged PR #216) |
| Run interval | 2026-11-02 08:04:21.413212–08:04:21.973215 UTC |
| OS / architecture | Ubuntu 24.04.5 LTS / Linux amd64 (`x86_64`) |
| Kernel | `7.0.0-38-generic` |
| Go | `go version go1.26.10 linux/amd64` (version and build stages exited 0) |
| Bubblewrap | `/usr/bin/bwrap`, 0.9.0, uid/gid 0, mode `0755`, SHA-256 `e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71` |
| Study executable | SHA-256 `9bdbfd3a27e2bda556877cf02164414170eaf27108a2edd82851a4b36853e955` |
| Manual runner | SHA-256 `3801d5e2f18cef90bcfe86499f68c3fa748f75ffb19cd7d8217104c4204294a6` |
| Exact source archive | SHA-256 `30dcd194d9cc7c2c12368e85490618abc3d878a5faf620ea1bc0a982ce27f0b9` |
| Supplied evidence archive | SHA-256 `c48639ab24f081ca6c53f8cf17896dbc55ae3c897686f5fa103005dc9587721d` |

The archive checksum and every member in `SHA256SUMS` were verified without
executing or extracting archive content. `study.json` is byte-identical to
`study.stdout.log`. Source archive, Go version, build, and study subprocesses
all started, exited 0, were reaped, and neither timed out nor hit an output
cap. The JSON was produced and 18 implemented fixture cases ran; the five
unavailable rows did not run their requested experiments.

### Host restrictions and allowance

The snapshot records `apparmor_enabled=Y`,
`apparmor_restrict_unprivileged_userns=1`, user namespace limit `55603`,
`unprivileged_userns_clone=1`, and a full-range uid map. These are host
configuration observations, not proof of enforcement. The runner recorded no
host security setting changes and no privileged/unrestricted fallback.

The read-only check found `/etc/apparmor.d/komposyn-study-bwrap` containing:

```text
abi <abi/4.0>,
include <tunables/global>

/usr/bin/bwrap flags=(unconfined) {
    userns,
}
```

The file was mode `0644`, reported owner `nobody:nogroup` in this execution
context. It is a broad allowance for invocations of `/usr/bin/bwrap`, not a
per-study grant or a confinement profile. `aa-status --enabled` exited 0, but
`aa-status --profiled` could not read the loaded profile set (exit 4); direct
profile-set inspection was also denied. The snapshot labels the study process
`unconfined`. Thus the file's presence and AppArmor-enabled status are
verified; whether this exact profile was loaded at run time is **unverified**.
`host_security_settings_changed=false` means the study runner itself made no
such change; it says nothing about earlier administrator setup.

Cgroup v2 controller files listed `cpuset cpu io memory hugetlb pids rdma
misc dmem`; the snapshot also showed some controllers in `cgroup.subtree_control`.
No child cgroup was created or configured, no controller was written, and no
effective aggregate bound was demonstrated. Controller visibility is not
usable delegation evidence. No exhaustion experiment was run.

## Effective candidate policy

The CLI-reported `effective_candidate_policy` is a source-defined description,
not proof each property was enforced. The source at the pinned commit requests
these settings:

- Explicit user, PID, IPC, UTS, and network namespaces, all required; no
  `*-try` or weaker fallback.
- `--die-with-parent`, a new session, disabled nested user namespaces, and
  dropped capabilities.
- An empty 32 MiB tmpfs root; static worker read-only bound into it; only the
  synthetic fixture tree host-backed and writable. A protected fixture path
  may be hidden by a private tmpfs mask.
- Private 16 MiB `/tmp`; `HOME=/tmp/home`, `TMPDIR=/tmp`,
  `PATH=/nonexistent`, `LANG=C.UTF-8`; cleared inherited environment.
- No host root/home, `/proc`, `/sys`, `/dev`, host sockets, runtime sockets, or
  preserved descriptors. Standard streams and seccomp fd 3 form the control
  path.
- Architecture-checked seccomp filter denies network/socket, namespace/mount,
  process-inspection, and selected privilege-changing syscalls. Other
  syscalls are allowed; it is not a minimized general-purpose syscall allowlist.
- Five-second worker deadline and 64 KiB protocol-line/stderr bounds. No
  cgroup or process-count limit is configured; no exhaustion test is run.

See the [study guide](sandbox-feasibility-study.md) for the reproducible full
bubblewrap argument vector and source-level details. This was a fixed typed Go
fixture worker, not a shell experiment and not a claim about arbitrary Go code.

## Scenario evidence

| Scenario | Observed fixture result | A assessment | B interpretation / limit |
| --- | --- | --- | --- |
| Selected root relocated: create | 7-byte fixture created under moved root; worker succeeded | **Violation** | Consistent with retained mounted object access; one fixture only |
| Selected root relocated: read | Opened file remained readable after root move; worker succeeded | **Violation** | Consistent with object authority; does not prove all of B |
| Parent moved before create | 7-byte file created in moved parent; worker succeeded | **Violation** | Consistent with retained parent object |
| Parent moved before delete | Opened moved-parent target removed; replacement at authorized pathname remained | **Violation** | Also shows object identity does not preserve pathname authorization |
| Open target moved before write | Moved target changed; worker succeeded | **Violation** | Consistent with a retained target object |
| Existing hard-link alias | Outside alias reflected mutation; worker succeeded | **Violation** | Demonstrates inode aliasing crosses pathname boundary |
| Late hard-link alias | Alias created after open reflected mutation; worker succeeded | **Violation** | Demonstrates a prior path check cannot bind future aliases |
| Delete target substitution | Replacement link removed; displaced original entry remained (14 fixture bytes) | **Violation** | Exact prepared-entry identity is unresolved even under B |
| Recursive child moved | File in moved child removed; worker succeeded | **Violation** | Consistent with retained child object; no general recursive guarantee |
| Protected pathname with hard-link alias | Direct path masked, alias existed and was read; worker succeeded | **Protected-object failure**; separate from the A pathname race classification | Path masking alone does not protect object aliases; B does not solve disclosure |
| Rename into unmounted scope | Source remained; destination was unmounted; worker failed | Prevented in this fixture only | No general rename or mount-boundary claim |
| Symlink to unmounted host fixture | Worker failed; external synthetic fixture remained | Prevented in this fixture only | Only shows this absent path was not readable in this case |
| Nested protected direct path | Direct read failed while masked | Prevented in this fixture only | Alias row above is a counterexample to complete protected-object denial |
| Network/host/runtime probes | Worker reported cleared environment, absent host mounts, closed fd 3, denied socket probes, unavailable shell lookup, `no_new_privs=1`, seccomp mode 2, host PID unreachable | Narrow measured probes only | Does not prove all host isolation properties |
| Private scratch | Write succeeded in private `/tmp`; no host fixture entry appeared | Narrow measured pass | Only this scratch fixture and profile |
| Bounded output | 128 KiB line rejected at 64 KiB; worker command reaped; outcome `unknown` | Narrow measured pass | Descendant cleanup was not established |
| Partial write | Injected post-effect failure left a known partial fixture; outcome `partial` | Outcome classification measured | Does not establish containment |
| Cancel before release | Startup acknowledged, cancellation before release, no operation permitted; `not_executed` | Narrow measured pass | No cancellation after release |

The nine rows labelled **Violation** are concrete evidence against A for the
tested race/property instances. They do not prove that every possible
enforcement mechanism fails A. The protected alias row is an additional
confidentiality failure, not counted among those nine. The three `prevented`
rows support only their individual observations and do not establish general
containment.

### Unavailable rows and stale placeholder

- **Mount boundary:** no native nested-mount fixture was created. The study
  row says “the surrounding launcher denies namespace setup,” but that text is
  a hard-coded stale placeholder: this run started bubblewrap and workers.
  It is not a fresh launcher failure or mount test result.
- **Aggregate resource limits:** no cgroup was created or written; no
  exhaustion test was safe or attempted.
- **Parent death/descendants:** no live descendant termination test was run;
  `--die-with-parent` was requested but this behavior was not measured.
- **Shell:** not implemented. Typed-worker findings say nothing about shell
  safety.
- **Revocation after release:** only deterministic coordinator gate tests
  exist; no live worker mutation/revocation or stale-output test ran.

Protected aliases beyond the tested hard-link, ancestor movement/removal,
mount crossing, cleanup after abrupt parent death, resource enforcement, and
shell remain gaps. See the prioritized plan below. A skipped or unavailable
case is not a pass.

## Authority decision and recommendation

| Contract | User-visible meaning | Evidence here | Decision |
| --- | --- | --- | --- |
| **A — strict live host-path containment** | An operation remains beneath the approved host pathname at the moment of effect, despite relocation, aliases, recursive-child moves, or entry substitution. | The nine race rows above violate A. | **Keep as current enablement gate.** Do not enable tools. This candidate does not meet it. |
| **B — mounted-tree/object authority** | The worker may act on objects exposed in its private mount view even if external renames or hard links change their host names. | Several successful moved-object and alias effects are consistent with B. | Not proven as a complete contract and **not adopted**. Requires explicit maintainer decision and revised tests/guarantees. |

Choosing B would make a user-approved mounted object remain in scope after
being moved by another same-user process; it would not mean “the pathname I
approved is still protected.” It would also need clear UX for concurrent
renames and exact target identity. B alone does not prevent the protected
hard-link alias read, and does not decide whether deleting a replacement entry
is authorized. Narrow alternatives include excluding affected object classes,
operations, or scopes until protected-object identity and exact-entry rules
are designed. A snapshot-and-publish design could avoid some live namespace
races, but requires its own capture, conflict detection, atomic publication,
partial failure, and user-visible conflict semantics; it is not implemented
here.

**Recommendation:** preserve A as the gate; keep all filesystem and shell
tools unavailable. First review A versus B and decide the separate protected
object alias and exact prepared-entry requirements. This evidence does not
justify a production service or establish A impossible in general. System
bubblewrap plus Go remains adequate for this feasibility harness; no measured
requirement for a custom launcher emerged. Ubuntu 24.04.5/kernel 7.0.0/bwrap
0.9.0 are observations, not an approved product minimum. Delegated cgroup v2
also remains an undecided resource-control proposal.

## Prioritized follow-up plan

These are research tasks, not approved changes to production authorization.

| Priority / property | Proposed experiment | Prerequisites and safe limits | Acceptance |
| --- | --- | --- | --- |
| 1. Protected aliases and ancestor moves | Add disposable protected-file hard links, rename/remove protected ancestors, and alias creation at deterministic worker gates; inspect every host fixture afterward. | Decide which owners register effective protected objects (auth, settings, provider sources, DB/WAL/SHM/backups, runtime helpers); synthetic data only; no credentials or databases. | Direct names, aliases, and moved ancestors cannot disclose/mutate protected fixture content, or affected scopes/capabilities explicitly refuse. Record what inventory is incomplete. |
| 2. Independent mount crossing | Have a separately controlled host actor mount a disposable nested filesystem in the fixture tree; test read/write/delete and rename across it. | Disposable Linux host and mount permission already available; do not escalate or change security policy; no unrelated mounts touched. | Record actual device/mount identity and host effects; distinguish path containment from mount-view behavior. Unavailable if safe fixture setup cannot be established. |
| 3. Aggregate pids/memory/CPU | Verify delegated cgroup-v2 child creation, controller enablement, attachment before release, effective limits and cleanup without stressing host. | Explicit decision whether cgroup delegation is a product prerequisite; test in a dedicated disposable cgroup; prove limits before any bounded resource test. No exhaustion until independently effective controls are verified. | Evidence that the exact worker is attached before start and limits are effective; otherwise refuse arbitrary workers and report the capability unavailable. |
| 4. Parent death and descendant cleanup | Start a harmless synthetic descendant held at a handshake, terminate coordinator, inspect process/cgroup state and fixture cleanup. | Effective process containment and dedicated cgroup or other reviewed kill boundary; bounded helper; no shell payload. | No descendants survive; cleanup is joined or explicitly reported incomplete. Do not infer this from `--die-with-parent`. |
| 5. Revocation after release and stale output | Gate a harmless mutation and buffered output around release; revoke at deterministic points and inspect revision, fixture and displayed output. | Decide synchronization/linearization point; disposable target; no production dispatch change in this study. | Before release: no effect and `not_executed`; after release: truthful `partial`/`unknown` if uncertain; stale generation output suppressed; never replay. |
| 6. Shell-specific enforcement | Separate shell profile and adversarial shell fixtures only after its authority contract is reviewed. | Shell is a distinct capability; requires demonstrated process/resource limits, descendant cleanup, executable/helper policy, environment and network controls. No shell under the current typed-worker profile. | Shell cannot reach unapproved host data/resources under its declared contract; typed-worker tests cannot count toward acceptance. |

The smallest next slice is a maintainer contract review of A/B plus protected
alias and exact-entry semantics, followed by one isolated protected-object
alias/ancestor experiment. Do not start production integration until those
decisions and the required guarantees are explicit.

## Integration findings (not implemented)

- **Protected-resource inventory:** owners must register effective resolved
  locations, including auth lock/temp files, settings overrides, provider
  source roots, active Token Logger DB plus WAL/SHM and chosen backup paths,
  and runtime worker/helper/socket/fd resources. Missing required owner
  registration should make the relevant scope ineligible. Do not infer roots
  from cwd, `HOME`, repository discovery, model arguments, extension guesses,
  or broad machine scans. Aliases and ancestor changes need object-aware
  protection, not pathname masks alone.
- **Launching vs running:** report `launching` while creating/registering the
  sandbox. Startup acknowledgement is not operation execution. Only a
  permission release plus worker `STARTED` acknowledgement supports `running`.
- **Outcomes and revocation:** proven setup refusal before release is
  `not_executed`; after release, uncertain effects are `unknown`, and known
  incomplete mutation is `partial`. Suppress stale buffered output after a
  revision change. Do not replay uncertain mutation.
- **Once grants:** existing runtime consumes a once grant before
  `ToolEnforcer.Execute`; it remains the single consumption owner unless a
  separately reviewed protocol moves ownership atomically. The coordinator
  must serialize grant revision validation, admission, and release with
  revocation/reset/shutdown. It must not consume the same grant a second time.
- **Permission shape:** the agreed target is scoped write includes read,
  create, modify, rename, and delete; execute and network remain separate.
  The study changes no evaluator or grants. A writable bind mount is not an
  authorization decision.

## Reproduction and validation

The archive was collected by the reviewed manual runner from the exact
committed source archive. Its SHA-256 was checked against the supplied digest;
all archive entries were regular files with verified internal checksums. No
archive executable was run. To reproduce on a suitable disposable Linux
host, follow the [study guide](sandbox-feasibility-study.md) and preserve the
no-escalation/no-fallback rule. Do not assume the host setup above is a product
minimum.

For this documentation-only change, validation checks the machine-readable
JSON, archive/member provenance, scenario counts, local documentation links,
and `git diff --check`. No Go code or native sandbox test was run as part of
this report change. The native observations above come only from the supplied
run artifact and are not repository test results.
