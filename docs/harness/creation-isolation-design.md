# Creation isolation design decision (#319)

Status: research design agreed by the maintainer on 2026-11-18. The mechanism
remains unproven. The next task is the Gate A feasibility study below;
implementation and model-facing enablement remain separate work.

The architectural tradeoff is recorded in
[ADR-0005](../adr/0005-investigate-creation-broker-with-protected-authority-chain.md).

**Subsequent disposition:** the selected candidate failed Gate A. Issue #319
closed on 2026-11-19 as negative research after PR #338 merged; see the
[accepted closeout](issue-319-closeout.md). The design and research gates below
record the earlier agreement, not a new instruction to restart that candidate.
Creation remains unavailable and #321's positive-evidence gate is unsatisfied.

## Agreed direction

On 2026-11-18, the maintainer selected one bounded further investigation of
unprivileged creation mechanisms after the negative findings merged in
[PR #336](https://github.com/ayeshLK/komposyn/pull/336). Compare candidate
mechanisms and define decisive rejection tests before choosing a prototype.
If no candidate survives, record a precise negative result and leave creation
unavailable. This is not a claim that all unprivileged mechanisms are impossible.

Existing constraints from [#319](https://github.com/ayeshLK/komposyn/issues/319)
and [#267](https://github.com/ayeshLK/komposyn/issues/267) remain binding:
mounted-object authority, the retained approved parent and absent destination
leaf, exact approved text, complete protected-owner coverage, and once approval
bound to the applicable generations. Ordinary parent/root renames do not alone
revoke that authority. No privileged installation or host-security changes are
authorized. Creation enablement remains a separate review under
[#321](https://github.com/ayeshLK/komposyn/issues/321).

## Established evidence

The [merged creation study](https://github.com/ayeshLK/komposyn/blob/0e4fd59320f3bb2a704845a62f34fdf87d5ecd13/docs/harness/create-one-study.md)
records direct named creation exposing
payload through a late hard link, and a same-user actor modifying an unnamed
O_TMPFILE stage through host procfs before publication. These reject the tested
candidates on the recorded host. Successful no-replace publication does not
override either violation.

## Agreed trusted-start boundary

The maintainer accepts an installed executable and launching process that have
not already been compromised. Independent same-user actors may race startup,
preparation and publication. The design must establish protection before
accepting authority and account for access obtained during startup; setting
protection after startup is not by itself a sufficient argument. If no such
sequence can withstand the permitted actors, reject the candidate. This
agreement does not exclude startup races or permit assuming they succeeded
harmlessly.

## Selected candidate for design assessment

The maintainer selected a tiny operation-specific creation broker in the
original user namespace as the single next candidate. It would retain the
approved parent and staged file without passing those handles to the current
child-user-namespace Bubblewrap worker. This selection authorizes design
assessment only.

Startup protection and confinement are the first rejection gates. The proposal
must establish bounded file access, no network or general execution, protected
approval/release state across all authority holders, and complete cleanup. If
startup protection or confinement fails, stop before building the broader
creation workflow. Disabling dumpability alone is not proof of those gates.

Editing research in #320 remains independent. This decision does not close
#319 or #315, or unblock #321.

## Candidate comparison

These are source-based design assessments, not new native results or accepted
mechanisms.

| Candidate | Current assessment |
| --- | --- |
| Direct named create followed by write | Rejected by the measured late-hardlink violation. |
| Current O_TMPFILE worker with dumpability disabled | Not a sufficient standalone fix: the host namespace owner can have capabilities in the worker's child user namespace. Additional nesting does not remove that ancestor authority. |
| Small non-dumpable operation broker in the original user namespace | Selected for design assessment, not accepted as safe. It must establish a bounded enforcement boundary without silently replacing Bubblewrap with unrestricted host execution. Every holder of staged handles, approved bytes or release authority needs protection. |
| Separate host identity or privileged enforcement | Outside the agreed unprivileged investigation if it requires account provisioning, privileged helpers or host-security changes. |

Linux documents [proc-fd access checks](https://man7.org/linux/man-pages/man5/proc_pid_fd.5.html),
the [ptrace capability exception to the dumpability check](https://man7.org/linux/man-pages/man2/ptrace.2.html),
and [ancestor namespace owner capabilities](https://man7.org/linux/man-pages/man7/user_namespaces.7.html).
Together these explain why disabling dumpability in the current child user
namespace cannot alone establish the desired boundary. Actual access still
depends on the full credentials, permissions and active security modules.

A broker proposal must also address startup and inherited access. Ordinary
[exec resets dumpability](https://man7.org/linux/man-pages/man2/execve.2.html);
setting it after exec does not itself prove an actor lacked earlier access.
Protection must cover the authority chain, including any parent retaining
relevant handles or release state, not merely the final writing process.
The agreed trusted-start boundary above does not settle that initialization
sequence; it remains a proof obligation.

## Agreed protection perimeter and deployment tradeoffs

The main experimental chat process currently holds authoritative approval and
runtime state. Protecting only a broker cannot establish approval integrity if
an actor can modify that main process. The maintainer accepts protection of
every authority-holding process, including the main experimental chat, and the
resulting restrictions on ordinary debugger attachment and OS core dumps while
creation authority is held. This is a design requirement, not an implemented
process setting. Independent trusted approval UI/process redesign is not the
selected direction.

On hosts permitting ordinary same-user tracing, process-local protection after
exec cannot establish integrity against an actor that attaches first. This is
a source-based startup counterexample, not a new native result. An explicitly
required existing host policy might close that opportunity, subject to proof
of actor ancestry, credentials, policy exceptions and all relevant access
routes. The maintainer accepts requiring a verified, already-active host
protection policy, with creation unavailable where it cannot be established.
[Yama's documented modes](https://docs.kernel.org/admin-guide/LSM/Yama.html)
illustrate policy-dependent tracing restrictions, not acceptance of a chosen
profile. The initial policy choice is below; its verification and sufficiency
remain proof obligations. No policy changes or privileged setup are authorized.

### Initial host policy decision

Yama restricted tracing (scope 1) still permits qualifying ancestors and
declared tracing relationships. Supporting it would require an explicit
trusted-ancestry assumption and control of exceptions. The maintainer chose to
investigate only already-active admin-only tracing (scope 2) and no-attach
tracing (scope 3) initially. Modes 0 and 1, absent policy and unverifiable policy
are ineligible. No mode is accepted as safe merely by this choice. Supporting
mode 1 would require a separate design and evidence decision.

These tracing policies do not establish denial of every procfs read-mode
access route. Before process protection, inherited descriptors and control
channels can still create exposure. The proposal must account for standard
I/O, payload, parent handles and approval/control endpoints from launch onward;
it cannot assume a fresh process begins without authority-bearing descriptors.
A policy observed only after startup does not prove it protected startup.

## Agreed research sequence

### Gate A: startup, authority chain and confinement

Use the existing native sandbox-study harness and disposable synthetic
fixtures. Start on Linux amd64/ext4, recording the actual kernel, filesystem,
namespace identities, policy and launch mechanism. Scope 2 and scope 3 are
separate profiles: evidence for one does not accept the other. Actors are
independent same-user processes without CAP_SYS_PTRACE in the original user
namespace; privileged attackers and an already compromised installed executable
or launching process are outside this design. Permitted startup races remain
inside it.

Account for the experimental chat, launcher, broker and any helper holding
approval, payload, parent/stage handles or release state. Specify initialization
ordering before implementing the fixture. Ordinary exec resets dumpability;
creating a protected child cannot cure prior access to its authoritative parent.
Do not send authority into the current child-user-namespace worker.

| Case | Required finding |
| --- | --- |
| Actor races launch and each protection transition | No actor gains execution/memory control or authority-bearing handles; policy must cover startup, not only later admission. |
| Startup proc-fd access, including inherited standard I/O and control endpoints | No stolen handle can alter approval, payload, target or release. Inventory actual descriptor holders; do not assume an empty initial descriptor set. |
| Actor retains a deliberately exposed synthetic handle before protection | Negative control demonstrates why later protection does not revoke existing access. Candidate ordering must exclude that exposure. |
| Proc-fd reopen, process-memory access, ptrace and pidfd_getfd after protection | Independent actor denied at every authority holder; identify the boundary causing refusal. |
| Deliberately dumpable disposable control process | Establish applicable proc-fd actor reachability without disabling host policy; do not require ptrace to succeed where the required host policy denies it. |
| Broker confinement | Enforced limits prevent unrestricted file access, network use, general execution and authority transfer; missing enforcement refuses the candidate. |
| Policy or startup provenance cannot be established | Unavailable, with no creation authority. A sysctl value alone is insufficient evidence. |
| Actor/worker death, suspension, timeout or incomplete join | Preserve cleanup ownership, block affected admission and report the actual outcome. |

Seccomp may be part of confinement but is not, by itself, evidence of path or
object authorization. The design must explain retained descriptor rights,
allowed syscall arguments and how each effect remains bound to the approved
parent/leaf. Confinement must cover every OS thread and remove inherited
network/filesystem capabilities; a filter installed on only one thread is
insufficient. No unrestricted host executor is an acceptable fallback.

If inherited UI or control channels were exposed before protection, prove they
cannot influence authority or retire and replace them before accepting it.
Do not silently add a trusted-terminal exception to bypass this test. If the
current approval UI cannot satisfy this requirement, report that as a failed
gate and return for a separate decision.

If Gate A fails, stop. Do not build mutation-scope UI, provider integration or
the broader creation workflow to work around the failed isolation boundary.

### Gate B: the complete creation contract

Only if Gate A survives review, extend operation-specific evidence for exact
approved bytes and the 64 KiB UTF-8 boundary, absent-leaf no-replace publication,
destination/symlink substitution, parent/root movement, aliases and protected
owner registration/rotation. Address transitions around staging, capability
release and publication, including retained handles after publication. Existing
successful O_TMPFILE rows do not substitute for evidence on the new mechanism.

Specify how host-recorded preparation, once approval, scope/policy generations,
owner lifecycle and revocation synchronize with the effect. Use the existing
runtime-to-UI/provider seam when assessing those behaviors. Keep cancellation,
stale publication, lost completion, partial effects and cleanup explicit; never
automatically replay an uncertain side effect or claim rollback.

An isolated primitive experiment is not proof of the complete authority chain.
If production composition remains missing, name that gap and keep enablement
blocked, even when the primitive passes.

## Agreed deliverable and stopping rule

Produce a reviewable design/evidence packet with exact source and artifact
hashes, host/profile provenance, deterministic actor checkpoints, observed bytes,
resource joins and per-case classifications. Distinguish native observations,
documented-semantics deductions and ordinary tests. Unavailable or inconclusive
cases cannot be counted as passes. Implementation work requires the repository
checks in AGENTS.md; this document-only discussion does not require Go tests.

A concrete counterexample rejects this candidate. Missing enforcement or
evidence leaves it unresolved and unavailable. Do not silently substitute a
different mechanism, weaken the threat model, change host policy or introduce
privileged setup. Return with the failed gate and alternatives for review.

Negative research can support closing #319 only after a separate reviewed
disposition; it does not establish that every unprivileged approach is
impossible. Positive findings require complete gate review and separate #321
enablement. This decision itself neither closes issues nor exposes tools.
