# Issue #215 Linux sandbox study execution — 2026-11-02

## Result: native candidate study did not run

The merged study was built from the verified, clean `master` merge commit
`cdb904b0d4353b4362a8b6508e7363b9d1d40e64`. Its documented invocation was
attempted through the normal restricted command launcher. The launcher exited
with status 1 before starting the study executable:

```text
bwrap: setting up uid map: Permission denied
```

This message is from the surrounding agent command launcher. It is not output
from the candidate `/usr/bin/bwrap`, and does not show that the candidate
backend failed or succeeded. The study CLI did not start, did not invoke the
candidate, did not create disposable fixtures, and started **zero workers**.
No individual operation ran; no host fixture effects were observed. The study
process exit status and structured study report are unavailable because the
process did not start. The expected report file was absent after the attempt.
The launcher status is not the study's exit status. No elevated or unrestricted
retry was made.

Consequently there is no measured result for contract A or B. No A pass, A
violation, or B behavior can be inferred from this execution. The existing
PR #213 filesystem counterexamples remain evidence about the earlier
descriptor-based prototype only; they are not bubblewrap observations.

## Baseline and provenance

| Item | Recorded value |
| --- | --- |
| Repository | `ayeshLK/komposyn` |
| Study source commit | `cdb904b0d4353b4362a8b6508e7363b9d1d40e64` (merged PR #216; also the fetched `origin/master` at start) |
| Isolated checkout | Clean before this report; the user's original checkout contained unrelated changes and was left untouched |
| OS / architecture | Ubuntu 24.04.5 LTS / Linux amd64 (`x86_64`) |
| Kernel | `7.0.0-38-generic` |
| Go | `go1.26.10 linux/amd64` |
| Candidate path | `/usr/bin/bwrap`, regular file, uid 0, gid 0, mode `0755` |
| Package provenance | `/usr/bin/bwrap` belongs to Ubuntu package `bubblewrap`, version `0.9.0-1ubuntu0.3` (package metadata; the candidate's own `--version` was not reached) |
| Candidate binary SHA-256 | `e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71` |
| Study executable | Built from the source commit above with `CGO_ENABLED=0`, Go 1.26.10, Linux/amd64; SHA-256 `6c8ced131cb4dad832a69983e31e8284e389afe00c57391bd80d1c5c240105c8` |

The original checkout was not used for edits or execution. The isolated
worktree was based directly on `origin/master` at the merged commit. The binary
hash identifies the executable that was prepared for the run; it does not
identify a worker that ran, because none did.

### Namespace, seccomp and cgroup facts

These are read-only host observations collected separately from the blocked
study invocation. They are configuration and visibility evidence, not proof
that the candidate can establish or enforce its requested profile:

- `/proc/sys/user/max_user_namespaces` was `55603`; the inspected process had
  `kernel.unprivileged_userns_clone=1` and
  `kernel.apparmor_restrict_unprivileged_userns=1`. Its visible `uid_map` was
  `0 0 4294967295`. The surrounding launcher nevertheless failed its own uid
  map setup. A nested candidate user-namespace launch was not tested.
- The kernel config exposed `CONFIG_SECCOMP=y` and
  `CONFIG_SECCOMP_FILTER=y`; `kernel.seccomp.actions_avail` listed
  `kill_process kill_thread trap errno user_notif trace log allow`. The
  inspected host process reported `Seccomp: 0` and `NoNewPrivs: 0`. The study's
  filter was not installed in a candidate worker, so candidate seccomp behavior
  is unverified.
- The cgroup filesystem was v2. Root controllers visible were
  `cpuset cpu io memory hugetlb pids rdma misc dmem`. The app slice exposed
  `cpu memory pids`, with `memory pids` in its `cgroup.subtree_control`; the
  inspected process cgroup exposed `memory pids`, with an empty child
  `cgroup.subtree_control`. Read-only access checks reported the parent
  subtree-control file and current `cgroup.procs` as writable. No child cgroup
  was created, no cgroup control was written, and no resource limit was
  applied. Usable delegated child-cgroup provisioning and effective aggregate
  enforcement are **unverified**; apparent writability is not counted as a
  pass.

The environment facts above came from a separate authorized inspection
context because the normal command launcher could not start. They must not be
read as a study-process snapshot or as evidence that the candidate profile
would run in that context.

## Build and attempted command

The merged CLI's supported flags were inspected before invocation: `--bwrap`
selects a trusted absolute executable path (default `/usr/bin/bwrap`), and
`--commit` records the repository commit (default `unknown`). The documented
command was used without changing either option:

```sh
CGO_ENABLED=0 go build -o /tmp/komposyn-sandbox-study ./cmd/sandbox-study
/tmp/komposyn-sandbox-study \
  --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-sandbox-study-report.json
```

The build succeeded. The run attempt returned launcher exit status `1` and the
single diagnostic shown above. There is no study CLI exit status, JSON report,
candidate `bwrap --version` output, or worker output to capture. The machine
readable companion
[`sandbox-feasibility-execution-2026-11-02.json`](sandbox-feasibility-execution-2026-11-02.json)
records the attempt, provenance and unobserved cases. It is an execution
record, **not** output emitted by the study CLI.

## Requested policy versus effective policy

The source-defined profile is recorded in the merged
[study guide](sandbox-feasibility-study.md). It requests private user, PID,
IPC, UTS and network namespaces; dropped capabilities and disabled nested user
namespaces; a 32 MiB empty root tmpfs, 16 MiB private `/tmp`, a read-only static
worker bind, only a synthetic disposable fixture tree as a writable host bind,
an optional nested tmpfs mask, a cleared environment with four fixed values,
only standard streams and seccomp fd 3, and the architecture-checked seccomp
filter. Each worker has a five-second deadline and output is bounded to 64 KiB
per protocol line/stderr buffer. No cgroup is configured.

This is the **requested profile**, not an effective sandbox observed on this
host. Namespace, mount, seccomp, network, inherited-fd, environment, scratch,
worker startup and operation behavior were not exercised. The profile has no
weaker fallback. The study's typed Go worker does not invoke a shell; there is
no shell result or shell safety claim.

## Scenario-by-scenario result

For each implemented fixture below, the intended operation-start checkpoint is
the worker's `READY` acknowledgement after opening the relevant descriptor;
the host-side fixture actor then changes the disposable namespace before the
coordinator releases the worker. Since the coordinator never started, neither
the checkpoint nor the host actor ran. The “B relevance” column describes what
a future observation could be consistent with, not an observation from this
run.

| Implemented fixture | Intended operation and actor checkpoint | Actual worker / host effect | A result | B relevance |
| --- | --- | --- | --- | --- |
| Selected-root relocation | Open root, then move it and replace its original path before create | Not started; no fixture created | No conclusion | A create through retained root would be consistent with B, but unobserved |
| Selected-root relocation read | Open file, then move root before reading descriptor | Not started; no fixture created | No conclusion | Read of retained object could be consistent with B, but unobserved |
| Parent relocation before create | Open parent, move it outside scope and replace pathname before `openat` create | Not started; no fixture created | No conclusion | Create through retained parent could be consistent with B, but unobserved |
| Parent relocation before delete | Open parent and inspect entry; move parent and replace entry before `unlinkat` | Not started; no fixture created | No conclusion | Delete through retained parent could be consistent with B, but unobserved |
| Opened-target relocation | Open file, rename its name outside scope, then write through descriptor | Not started; no fixture created | No conclusion | Mutation of retained object could be consistent with B, but unobserved |
| Existing hard-link alias | Create an outside alias before worker opens the target; write target | Not started; no fixture created | No conclusion | Shared inode content change could be consistent with B, but unobserved |
| Late hard-link alias | Open target; create an outside hard link before write | Not started; no fixture created | No conclusion | Shared inode content change could be consistent with B, but unobserved |
| Deletion-target substitution | Open parent and inspect target; replace its name with a symlink before unlink | Not started; no fixture created | No conclusion | Removing the replacement entry may fit mounted-tree authority, but exact prepared-target integrity is unobserved |
| Recursive child relocation | Open child directory; move it outside root before walking/removing entries | Not started; no fixture created | No conclusion | Mutation through retained child directory could be consistent with B, but unobserved |
| Rename to unmounted scope | Attempt rename from `/scope` to absent `/outside` after release | Not started; no fixture created | No conclusion | B exposes only mounted paths; unobserved |
| Symlink to unmounted host fixture | Attempt path read through an absolute symlink whose host target is not mounted | Not started; no fixture created | No conclusion | Target absent from mount view; unobserved |
| Nested protected path | Mask a fixture subdirectory with tmpfs and attempt its direct read | Not started; no fixture created | No conclusion | Mask behavior and object hiding unobserved |
| Protected hard-link alias | Mask protected name but attempt read through a sibling hard link | Not started; no fixture created | No conclusion | A name mask may leave an alias exposed; unobserved |
| Network/host-path/process/runtime probes | Probe host root, `/proc`, `/home`, sockets, host PID, env, fd 3, shell lookup and seccomp state | Not started; no fixture created | No conclusion | Separate isolation properties; no B conclusion |
| Private scratch | Write `/tmp/scratch.bin`, then verify successful outcome and absence of host fixture | Not started; no fixture created | Not applicable | No observation |
| Bounded output | Emit a 128 KiB line; coordinator should reject over 64 KiB and reap command | Not started; no output generated | Not applicable | No observation |
| Partial mutation outcome | Create a fixture and write a known prefix before returning `partial` | Not started; no fixture created | Not applicable | No observation |
| Cancel before release | Cancel after `READY` but before release; require no operation effect | Not started; no fixture created | No conclusion | No observation |

Every implemented row is `not_run`; none is a measured pass or violation. No
host fixture state exists to inspect. No A or B behavior is established.

## Evidence gaps and test status

**Implemented but blocked before execution:** all 18 rows above. Their fixture
setups, workers, outside actors and result classifiers were not reached.

**Explicitly unavailable in the merged harness:**

- Mount-boundary test: no child mount was attempted. Do not claim
  `RESOLVE_NO_XDEV` or namespace mount behavior was measured here.
- Aggregate PID, memory and CPU limits: no cgroup was provisioned or changed;
  no exhaustion tests were run.
- Parent death, surviving descendants, and descendant escape: no live child
  tree or cgroup kill boundary was tested.
- Revocation immediately after release and stale output suppression: the gate's
  before/after and concurrent ordering is a Go synchronization model test; it
  is not an actual running-worker/revocation/output integration experiment.
- Shell: not implemented. Typed-worker results make no shell acceptance claim.

**Not a dedicated case in this profile:** movement/removal of a protected
resource's ancestor; a Unix control-socket connection by pathname; and stale
buffered output after a revision change. The protected direct-name and hard-link
fixtures are implemented but were not run. General root/parent relocation
fixtures, also not run, do not establish the protected-owner inventory or
alias policy.

macOS and Windows were not tested. Linux/arm64 builds are compile-only evidence,
not native enforcement evidence. Go tests/race tests are repository checks,
not candidate sandbox results. No process, memory or CPU exhaustion was
attempted.

## Recommendations and maintainer decisions

1. **Next action:** run the unchanged merged study on a disposable native
   Linux/amd64 host where the ordinary command launcher can create the required
   user namespace. Preserve the no-escalation rule. Verify the candidate
   executable path/package/hash there and inspect the full structured JSON and
   fixture state. If the launcher blocks again, stop and report unavailable;
   do not retry outside it.
2. Keep **A** as the filesystem enablement gate. This attempt says nothing
   about whether bubblewrap can satisfy A. Do not adopt **B** based on this
   report; there is no candidate observation to review.
3. Continue evaluating system bubblewrap plus Go orchestration first. This
   blocked run identifies no requirement for a custom launcher or extracted
   helper.
4. Review the minimum supported Linux environment and bubblewrap provisioning
   only after native capability results. The observed host versions are
   provenance, not an approved product minimum.
5. Decide separately whether delegated cgroup v2 is required for typed workers
   and for any later shell work. This study neither provisions nor tests such
   delegation; do not run exhaustion tests without effective controls.
6. Keep protected-path registry completeness, protected ancestor/alias rules,
   `/fs-root` UX, once-grant consumption ownership and worker revocation
   semantics as maintainer decisions. This report changes none of them.

Issue #215 remains open. No production service, permission semantics, tool
registration or root UX changed.

## Reproduction handoff

Use a disposable Linux/amd64 environment with the repository at merge commit
`cdb904b0d4353b4362a8b6508e7363b9d1d40e64`, Go 1.26.10 or newer, and a trusted
absolute system bubblewrap path meeting the merged profile's ownership and
mode checks. Its namespaces and seccomp requirements must be usable from the
normal process-launch path. This is a reproduction target, not a declared
minimum supported product environment.

From the repository root, run the merged instructions exactly:

```sh
CGO_ENABLED=0 go build -o /tmp/komposyn-sandbox-study ./cmd/sandbox-study
/tmp/komposyn-sandbox-study \
  --commit "$(git rev-parse HEAD)" \
  > /tmp/komposyn-sandbox-study-report.json
```

Capture the command's own exit status, bounded stderr and complete JSON. Exit
status 3 or any `unavailable` result is missing evidence, not a pass. Confirm
that each worker reached `READY`/`STARTED` and inspect the disposable host
fixture after each case. Never elevate or retry outside the candidate profile.

The merged run's existing records are in
[`sandbox-feasibility-study.md`](sandbox-feasibility-study.md), the
[#213 policy](filesystem-enablement-policy.md), and the
[enforcement foundation](filesystem-enforcement-foundation.md). See the
[bubblewrap 0.9 manual](https://github.com/containers/bubblewrap/blob/v0.9.0/bwrap.xml),
[Linux cgroup v2 documentation](https://docs.kernel.org/admin-guide/cgroup-v2.html),
and [upstream bubblewrap source](https://github.com/containers/bubblewrap/tree/v0.9.0)
for configuration semantics; those references are not native evidence.
