# Research closeout: issue #320

Status: **accepted scoped negative research disposition; issue closed**. Issue
[#320](https://github.com/ayeshLK/komposyn/issues/320) closed on 2026-11-19 at
06:11:49 UTC after the review and merge of [PR #340](https://github.com/ayeshLK/komposyn/pull/340).
The [closeout comment](https://github.com/ayeshLK/komposyn/issues/320#issuecomment-7001344903)
records the disposition. This accepts research completion only for the
candidates examined under #267's existing mounted-object contract. It does not
claim that all unprivileged editing is impossible, enable mutation, close #267
or #315, or satisfy #322's positive-evidence gate.

The results below remain historical findings under the contract then recorded
in ADR-0007. [ADR-0010](../adr/0010-propose-approved-entry-replacement-for-file-editing.md)
records the later accepted editing design boundary; it does not reinterpret
the scoped negative result, supply positive mechanism evidence or enable
editing. It supersedes only the editing requirements concerning same-mounted-
object mutation and strict revision-at-effect.

## Accepted disposition

Issue #320 permits either a reviewed candidate or a concrete infeasibility
report and alternatives. The evidence below rejects the examined
lease-guarded in-place candidate and replacement-by-rename under the retained
contract. It does not experimentally disprove a general
kernel/filesystem-mediated conditional-edit category; no concrete primitive in
that category has been selected or studied. This is a bounded negative
research result, not a proof that every possible unprivileged mechanism is
infeasible.

Editing remains unavailable. The retained-FD study did not implement an edit
worker, exercise an actual edit, or pass the runtime-to-UI/provider approval
path. #322's independently stated entry gate for positive, reviewed editing
evidence and its separate explicit enablement review remain unsatisfied. Its
native GitHub dependency on #320 is resolved by the closure; that does not
satisfy the independent gate.

## Contract retained

Any later candidate remains subject to the existing #267 and #320 contract:

- Edit exactly one existing UTF-8 regular file; original and result are each
  at most 64 KiB. Preserve permissions and the existing line-ending
  convention. Refuse binary or unsupported encodings, symlinks, hard-linked
  targets, directories, multiple files, rename, deletion and out-of-scope
  targets.
- Authority comes from a prior approved read of that exact file in the current
  conversation. Host-owned state records the mounted-object identity and the
  exact content revision. A model-supplied path, digest, inode number or claim
  confers no authority. Match exact context; never fuzzy-match or silently
  rebase.
- Bind the exact proposed bytes and complete diff, read receipt/revision,
  independent user-selected mutation scope and generation, policy and
  protected-owner coverage generations, provider destination, conversation,
  once approval and unique effect attempt through the effect point.
- Preserve mounted-object/exact-object authority. An ordinary external rename
  alone does not revoke authority, but replacing a directory entry with a new
  inode is not an edit of the approved object by implication.
- Keep cleanup ownership until effect-capable workers and descriptors are
  joined or retired. Revocation and scope, policy, target-owner or protected
  inventory changes invalidate grants, cancel affected work, join it and block
  replacement admission until cleanup completes. Suppress stale publication.
- Protect auth/settings through aliases and rotation, provider-owned source
  files, and owned Token Logger databases, journals, WAL/SHM files and backups.
  Missing required owner coverage means unavailable.
- Report truthful `not_executed`, failed, partial, unknown and cancelled
  outcomes as applicable. Do not promise rollback or automatically replay a
  possible effect.

These are requirements, not behavior established by the lease observations.
ADR-0007 accepts them as retained contract constraints; it does not accept a
mechanism or enable editing. See [ADR-0007](../adr/0007-proposed-exact-revision-bound-file-editing-contract.md).

## Current implementation boundary

The current composition has no editing capability or edit approval path.
[`cmd/harness/main.go`](../../cmd/harness/main.go) wires the host sandbox into
the read-enabled runtime. Runtime admission and the opaque, bounded
`PrivateReadCapture` in
[`read_admission.go`](../../internal/harness/runtime/read_admission.go) and
[`read_authority.go`](../../internal/harness/runtime/read_authority.go) are
specific to approved reads and provider publication. The Linux service reports
read identity as device/inode/type; the read capture is not a revision-bound
receipt for a later edit and does not carry the mount ID into that identity.
[`service_linux.go`](../../internal/harness/sandbox/service_linux.go) provides
the retained read/list/search service, not mutation enforcement. The tview
approval UI displays read approvals, while the startup composition and
inherited-output finding are described in the [#337 Gate A record](issue-319-gate-a.md).
No edit-diff approval, release binding or provider publication flow has been
tested. These source paths are boundary evidence only; they do not prove
filesystem enforcement.

## Evidence and provenance

### File-lease observations

The original report cited by the study has SHA-256
`18a8e26432055c3ed80e586724ebb2c1fd4c301e024f3e68d005029f9a519153`. That
report is not present in the available capture set, so its contents and hash
could not be independently rechecked here. The checked-in study records its
source as `e35ed70145dbab72974b4cd1c4ee3c7f95d57442`, Linux amd64, kernel
`7.0.0-42-generic`, Go 1.26.11, `/tmp` on ext4, and a Bubblewrap trust refusal
that left sandbox rows unavailable/not executed. The direct lease probes run
outside Bubblewrap and are not sandbox-enforcement evidence.

A separate corroborating JSON report is available at
`/tmp/komposyn-issue-319-report.bsyNOM.json`. Its verified SHA-256 is
`56cde0cfffd4e6aa3ee62622e1812c144709218fd4e224af02b8b5a1ed8e6599`; it
records source `8fdd9435870e6b90e4cb83c7169878bb0e3a2bb8`, Ubuntu 24.04.5,
Linux `7.0.0-42-generic`, amd64 and Go 1.26.11. The `lease_linux.go` source at
that revision and at `e35ed701...` is byte-identical (SHA-256
`e425fdb85671240c446289b77fb2d362cec91ef3485591081a9321fe0bb00350`). This
corroborates the probe implementation, not the missing `e35ed...` report or
its capture. No archive containing this corroborating JSON was available. The
separate `/tmp/komposyn-issue-319-native-unavailable-8fdd943.tar.gz` was
inspected (archive SHA-256
`1c8df713d26820526b965fac13747f91c6a591b666294bbecc0b985b5d31329c`; embedded
report SHA-256
`601825f7e093ec89409b25af37a771fbc2609a980b75a70e47979af9b7d99d76`); its
manifest identifies a different unavailable capture with native scenarios
not executed, so it is not a bundle for the corroborating lease JSON.

The corroborating JSON does not record the lease fixture's resolved temp path,
filesystem type or mount ID. The source creates it with `os.MkdirTemp("", …)`;
the study notes report `/tmp` as ext4, but the report cannot independently
establish that this capture used that mount. Therefore this packet does not
claim an artifact-verified ext4 result for the lease probes. The creation
archives and #337 startup capture have their own filesystem metadata, but are
not editing evidence and must not fill this gap.

The available corroborating report records these fixed synthetic same-user
actor observations:

| Probe | Recorded outcome | What it establishes / does not establish |
| --- | --- | --- |
| Existing writable FD | `F_SETLEASE(F_WRLCK)` was refused with `EAGAIN` while an independent writable FD was open; after that FD closed, lease acquisition succeeded. | Acquisition can refuse a conflicting open state and later succeed. It does not reserve a revision across a conversation or exclude all writer paths. |
| Retained `MAP_SHARED` mapping | With a writable shared mapping retained after its FD closed, lease acquisition was refused. The actor then changed the synthetic bytes through the mapping without a lease. | This capture did not write under a lease with that mapping. It shows a path that can change content when the mapping prevents lease acquisition; general mapping behavior is not established. |
| Late hard link under `F_WRLCK` | An independent actor created the hard-link alias while the write lease remained held. | **Observed counterexample:** the lease does not prevent late alias creation and cannot enforce the contract's hard-linked-target refusal. |
| Nonblocking open through that alias | The actor's `O_NONBLOCK` open got `EWOULDBLOCK` and the lease entered break state; the coordinator removed the lease before timeout. | This is a tested conflict response, not prevention of the link or proof of safe effect handling during a break. |
| Partial/unknown edit outcome | `not_executed`. No in-place bytes were written for this case. | Partial/unknown result behavior and replay prevention are contract statements, not demonstrated edit integration. |

The study did not measure forced-break timing, holder suspension/death,
concurrent content races at an edit's effect point, actual edit effects,
parent/root replacement for editing, protected-owner rotation, cancellation,
delayed joins, or partial/unknown publication behavior. No native editing
worker or production approval chain was exercised.

The Linux [`F_SETLEASE(2)` manual](https://man7.org/linux/man-pages/man2/f_setlease.2const.html)
describes notification for conflicting opens/truncates, the no-other-open-FD
condition for a write lease, and forced lease removal/downgrade after
`/proc/sys/fs/lease-break-time`. It does not make a lease a compare-and-write
transaction. The observed hard-link result is from the synthetic probe; the
manual does not substitute for that observation.

### Startup and approval-chain evidence inherited from #337

The measured #337 primitive was a same-UID actor opening a synthetic inherited
output-pipe descriptor through host procfs before the target changed
dumpability, retaining it, then writing through it after the transition. A
fresh open after protection was refused. The checked-in report is source
`2cb48322d99bcdc1feaa440d6701f54a1ccad3b7`, SHA-256
`36a6290f55357cee527109de580c40135c2deace36353253ea79decf8390065a`; the
recorded fixture was ext4, mount ID 6339, on Linux `7.0.0-42-generic` amd64.
The host used Yama scope 1 and a noninitial user namespace, so it was not
eligible mode-2/3 acceptance evidence. PR #337's merge and CI do not change
that capture profile.

The connection from a retained output descriptor to terminal approval-display
integrity, and the distinction between Yama ATTACH policy and the READ-class
proc-fd check, are source/documentation deductions. The probe did not spoof a
complete UI approval, validate an editing diff, test Yama scope 2/3, or prove
that any process-hardening profile is sufficient. See the
[#337 Gate A record](issue-319-gate-a.md) and primary Linux
[Yama documentation](https://docs.kernel.org/admin-guide/LSM/Yama.html),
[`/proc/<pid>/fd` documentation](https://man7.org/linux/man-pages/man5/proc_pid_fd.5.html),
and [`execve(2)`](https://man7.org/linux/man-pages/man2/execve.2.html).

This startup/approval concern applies to every proposed edit primitive: an
effect API cannot authenticate what the user saw or ensure that the release
input matches the approved diff. It remains an unresolved prerequisite, not an
accepted editing profile. Do not transfer creation-specific Yama or
process-hardening decisions into editing. Any future editing profile must
separately review trusted approval presentation/release, startup ordering and
inherited handles, and identify and measure its own controls.

## Candidate assessment

| Candidate | Evidence class | Assessment under the retained contract |
| --- | --- | --- |
| In-place write through a retained FD, optionally guarded by `F_SETLEASE` | The lease rows above are direct host-kernel observations from fixed synthetic files and same-user actors, outside Bubblewrap. No edit operation was run. | **Reject this candidate on current evidence.** Leases can refuse acquisition for the tested pre-existing FD/mapping states, but do not provide a prior-read revision transaction; late link creation succeeded while the lease was held. A digest check followed by a write still leaves an unclosed concurrent-writer boundary. Break, forced removal, unknown outcomes and approval-chain synchronization are unresolved. |
| Write a replacement inode and rename it over the approved pathname | No editing-specific native run. The rejection follows Linux `rename(2)` and hard-link object semantics: rename changes the directory entry; other hard links and open descriptors to the old inode remain attached to that object. | **Reject under #267's mounted-object contract as it stood at closeout.** The pathname would name a different inode after replacement, while the approved object remains unchanged through its other name/descriptor. A directory-entry-at-effect contract was outside #320's scope; the later [ADR-0010](../adr/0010-propose-approved-entry-replacement-for-file-editing.md) records that separate design boundary. See [`rename(2)`](https://man7.org/linux/man-pages/man2/rename.2.html) and [`link(2)`](https://man7.org/linux/man-pages/man2/link.2.html). This is a documented-semantics rejection, not an observed race result. |
| Kernel/filesystem-mediated conditional edit | No concrete API or implementation was selected; no experiment tested this category. | **Unresolved, not experimentally disproved.** Under the contract at #320 closeout, a future proposal needed a concrete effect primitive and trust/deployment boundary, then evidence for exact mounted-object and revision binding, alias refusal, protected-owner/revocation behavior, truthful outcomes and trusted approval release. ADR-0010 later changes only the editing authority boundary; it accepts no privileged component, BPF/LSM load, host identity or host-policy change. Do not assume privileged enforcement is the only possible future solution. |

Other mechanisms were not exhaustively inventoried or disproved. This closeout
rejects only the candidate paths assessed above and records why no path then
reviewed met the contract. At closeout, alternatives required a separate
authority decision before implementation. ADR-0010 records the later selected
editing boundary; a concrete replacement mechanism, profile and prototype
still require their own design and evidence gates.

## #320 acceptance-criterion reconciliation

| #320 criterion | Disposition | Evidence and limit |
| --- | --- | --- |
| Specify prior-read host identity/revision and bind exact change at effect; model claims are not authority. | **Contract accepted; effect binding unproved.** | #267, ADR-0007 and `issue-320-editing-design.md` define the authority needed. There is no edit receipt or effect mechanism consuming it. |
| Investigate unprivileged editing of one bounded UTF-8 regular file, preserving metadata and refusing unsupported targets. | **One candidate investigated; editing behavior unmeasured.** | The F_SETLEASE study probes lease conditions on synthetic files. It writes no edit bytes and does not exercise encoding, permissions, line endings or target-type refusals. Replacement rename is rejected by the mounted-object contract. |
| Address concurrent content changes, namespace replacement, late aliases and protected rotation. | **Partly observed; major cases unmeasured.** | Lease acquisition was refused with the tested open FD/mapping; late hard-link creation under a lease was observed. No effect-point content race, edit namespace replacement, protected-owner rotation or complete alias coverage was tested. |
| Use a bounded operation-specific worker and independent actors through the native harness. | **Kernel probes demonstrated; edit worker not built.** | Existing `cmd/sandbox-study` lease actors are fixed synthetic same-user processes. The lease probes run outside Bubblewrap and are not an editing worker or enforcement acceptance. |
| Cover target/parent/root movement, substitution, aliases, protection, revocation/cancellation, cleanup and stale publication. | **Partly demonstrated for lease mechanics; editing scenarios unavailable.** | Late alias and the tested lease break were exercised. Editing movement/substitution, owner transitions, release races, cleanup ownership, stale publication, suspension, death and delayed/incomplete joins remain unmeasured. |
| State truthful failed/partial/unknown outcomes; no unsupported atomicity, rollback or replay claims. | **Contract documented; edit outcomes untested.** | Study row `in-place-partial-or-unknown` is `not_executed`. No actual edit or replay path exists to validate. |
| Record source, host, filesystem, backend, outcomes and limitations; produce candidate or infeasibility result. | **Scoped negative research accepted; provenance gap retained.** | The available raw report hash/source/host are verified; the original report is missing. The corroborating artifact omits fixture filesystem and mount ID, so the contemporaneous `/tmp` ext4 note cannot be independently tied to that run. The closeout rejects only named candidates and does not claim universal infeasibility. |
| Keep editing unexposed; relate to #267 without changing it or host security. | **Preserved.** | No tool, host setting, privileged component, identity or #267 contract change is included. #267 and #315 remain open. |
| Use existing runtime-to-UI/provider boundary and existing native harness; no parallel executor. | **Not exercised for editing.** | No edit operation exists to traverse this seam. #337's synthetic startup probe is not an editing approval journey. This remains a prerequisite for any later positive candidate. |
| Run repository checks for implementation changes; document behavior and limits. | **Implementation checks not applicable to this docs-only closeout.** | No Go implementation changed. The e35 study record says `go mod verify`, vet, tests and `make check` passed, while `go test -race ./...` was interrupted and remains pending for that revision. PR #336 CI passed on head `c2f549b58f85b0ba6b5fd1e238ae1544d86dcca6`; PR #337 CI passed on head `ac641cd1460615d48ac033519203a02723746225`. Those later results do not retroactively change the e35 race result or prove editing enforcement. |

“Negative research” here is the disposition of specified candidates, not a
claim that every positive acceptance scenario passed. Unexecuted cases remain
unmeasured and must not be checked off as successful enforcement.

The criterion table above records applicability and evidence at #320 closeout.
ADR-0010 supersedes only the editing contract's exact-object and strict
revision-at-effect clauses; it supplies no positive mechanism evidence and
does not revise the historical dispositions.

## Subsequent tracker disposition

On 2026-11-19, after PR #340 merged, #320 was closed as completed scoped
negative research. The closeout comment is at
[issuecomment-7001344903](https://github.com/ayeshLK/komposyn/issues/320#issuecomment-7001344903);
GitHub records the closure at 06:11:49 UTC. This dated note records the later
tracker action without changing what was known when the studies were run.

The closure resolves #322's native GitHub dependency on #320. #322 remains
open and its positive-evidence entry gate and separate explicit enablement
review remain unsatisfied. No replacement blocker was added. Keep #267 and
umbrella #315 open. This disposition neither delivers the editing user story
nor completes #315. No edit mechanism, profile, prototype, privileged setup,
or model-facing tool enablement is accepted. A future alternative requires a
concrete design and separate decision before implementation.

## References

- [Issue #320](https://github.com/ayeshLK/komposyn/issues/320)
- [Edit enablement gate #322](https://github.com/ayeshLK/komposyn/issues/322)
- [Mutation contract #267](https://github.com/ayeshLK/komposyn/issues/267)
- [Umbrella issue #315](https://github.com/ayeshLK/komposyn/issues/315)
- [Initial editing design](issue-320-editing-design.md)
- [File-lease revision-binding study](file-lease-revision-binding-study.md)
- [#337 startup Gate A evidence](issue-319-gate-a.md)
- [Creation isolation design](creation-isolation-design.md) (context only; not editing evidence)
- [Accepted ADR-0007 contract](../adr/0007-proposed-exact-revision-bound-file-editing-contract.md)
- [Merged PR #336](https://github.com/ayeshLK/komposyn/pull/336)
- [Merged PR #337](https://github.com/ayeshLK/komposyn/pull/337)
- [Merged PR #338](https://github.com/ayeshLK/komposyn/pull/338)
- [Merged PR #339](https://github.com/ayeshLK/komposyn/pull/339)
