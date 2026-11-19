# Proposed research closeout: issue #319

Status: **proposal for maintainer review; issue remains open**. This document
does not close the ticket, enable creation, or complete #315. It recommends
closing #319 as a negative research result for the mechanisms actually tested,
while keeping creation unavailable and the dependent #321 gate unsatisfied.

## Disposition

Three mechanism investigations have produced counterexamples:

1. Direct named create-then-write exposed the new inode's payload through a
   same-user actor-created hard link before the write completed.
2. The tested `O_TMPFILE` worker did not protect its staged inode from a
   same-user host actor reopening the worker descriptor through host
   `/proc/<pid>/fd` and changing the bytes before publication.
3. The selected original-user-namespace broker investigation failed Gate A.
   A synthetic actor opened an inherited output descriptor before the target
   disabled dumpability and wrote through that retained descriptor afterward.

The result is a rejection of these candidates under their stated threat
contracts, not a proof that all unprivileged creation is impossible. No
positive creation mechanism, complete approval chain, or model-facing
capability has been established. A negative closeout would satisfy #319's
explicit negative-research completion clause; it would not deliver the
creation user story, close #267 or #315, or unblock #321's positive-evidence
gate.

## Evidence and mechanism comparison

| Mechanism | Contract and measured result | Revision, profile, evidence | Limits |
| --- | --- | --- | --- |
| Direct named create, then write | The worker creates the final name before writing. An independent same-user actor linked that inode into a synthetic protected directory at the checkpoint; the subsequent write exposed payload bytes through the alias. **Observed violation.** | Source `d9a7a7e28d6b340c91a3c3e537e5f650d3247510`; trusted run on Ubuntu 24.04.5, Linux `7.0.0-42-generic`, amd64, Go 1.26.11, regular UID 1000, root-owned Bubblewrap 0.9.0 mode 0755. Fixture ext4, magic `0xef53`, mount ID 33. Candidate profile `default-no-proc`. Archive SHA-256 `e711fd65d7d592f75b9796d3c6ed1591f93eff6768edba02d7f43dde4aedd958`; report SHA-256 `4137e276a2c043eac0b7068a068d2988d5ae412334a3b9c90088173d98e52db1`. Both archive and report hash were verified from `/tmp/komposyn-issue-319-native.0GvbQx.tar.gz`. | One host/filesystem and a synthetic protected alias. It rejects this create-then-write candidate; it does not establish a universal impossibility result. Successful collision, symlink, relocation, bounded-outcome and cleanup rows do not undo a disclosure counterexample. |
| `O_TMPFILE` staging and no-replace publication | The worker stages bytes in an unnamed inode and links it into the retained parent after the staged checkpoint. A same-user host actor reopened the staged FD via host procfs and changed the payload before publication. **Observed violation.** | Same source, host, fixture, archive, report and profile as the direct-create study; candidate profile `otmpfile-private-pid-proc`. The report states the worker published the modified content and both children were reaped. | The successful unnamed-stage, collision, symlink, parent/root relocation, cancellation-before-publication and stage-failure rows cover their synthetic cases only. They do not offset access to and mutation of the staged inode. Owner lifecycle, aggregate isolation, crash durability and production approval synchronization remain unproved. |
| Original-user-namespace broker, Gate A | Proposed contract protected every authority holder from startup onward, retained the approved parent/stage handles inside a bounded broker, and allowed no unrestricted host executor. Gate A stopped before a broker was built: a synthetic same-UID actor opened inherited fd 1 while the target reported dumpable 1, retained the descriptor through `PR_SET_DUMPABLE(0)`, then wrote the exact marker. A later proc-fd open was refused. **Observed violation of the startup ordering candidate.** | Probe source `2cb48322d99bcdc1feaa440d6701f54a1ccad3b7`; Go 1.26.11, Linux `7.0.0-42-generic`, amd64, ext4 mount ID 6339. Capture ran with Yama scope 1, a mapped noninitial user namespace, and original-namespace capability assumptions unverified. The host is ineligible for the selected scope-2/3 profile. Report SHA-256 `36a6290f55357cee527109de580c40135c2deace36353253ea79decf8390065a`; archive SHA-256 `0b0a86be6fd53978b3717f6f49f7d1d133f8fac2a744cf1a8d59bbc99f1ae51f`. Both were verified from `/tmp/komposyn-issue-319-gate-a-capture.tar.gz`. | This is a synthetic inherited-pipe control, not a complete terminal/UI spoofing journey or a mode-2/3 native run. The argument that Yama's ATTACH-class policy does not cover the READ-class proc-fd check, and that normal `execve` resets dumpability, is a documented-semantics deduction. Broker confinement, complete authority-chain protection and lifecycle supervision were unavailable because Gate A failed. |

Successful rows must remain attached to their actual candidate and scope.
For example, the `O_TMPFILE` publication success proves only that the
synthetic unnamed inode could be published on the recorded ext4 fixture; it
does not establish staged-FD isolation. The successful direct-create collision
and symlink refusals do not prevent a late hard link after creation. The Gate A
report's later-open refusal does not revoke a descriptor already opened before
protection.

The checkpoint-publication race found during review of PR #337 was fixed in
that merged PR by writing checkpoints to a same-directory temporary file and
renaming only after the write completes. The checked-in report is still the
historical capture from source `2cb48322...`; the fix does not retroactively
change its source revision or either capture hash. It removes a harness
checkpoint ambiguity and does not change the reported retained-descriptor
observation. The original source/report/artifact provenance remains intact.

## #319 acceptance-criterion reconciliation

“Negative research completed” does not mean every positive-mechanism test
passed.

| #319 criterion | Disposition | Evidence and remaining limit |
| --- | --- | --- |
| Threat model, exact parent/absent-leaf contract, generations, once receipt and namespace semantics | **Contract documented** | #267, ADR-0005 and `creation-isolation-design.md` specify mounted-object authority, exact target, trusted-start and generation/receipt requirements. No production creation approval chain exists. |
| Investigate unprivileged single-file creation | **Rejected by counterexample** | Direct named create and the tested `O_TMPFILE` worker each have the same-user violations above. Other mechanisms were not disproved. |
| Do not rely on paths, advisory locks, prechecks or postchecks as exact-effect proof | **Contract documented** | #267 and the creation design reject those as standalone enforcement. No successful effect-binding mechanism was delivered. |
| Use an operation-specific bounded worker and existing harness | **Demonstrated for the study only** | The fixed one-operation workers and independent actors ran in the existing native harness. This is not a production broker or a safe candidate. |
| Exercise collisions, substitution, aliases, protection transitions, cancellation, cleanup and unknown outcomes | **Partly demonstrated; intentionally unreached after failed gates** | The #336 archive records candidate-specific collision, symlink, relocation, late-alias, stage, cancellation and unknown rows. The #337 synthetic control adds startup descriptor retention. Protected-owner lifecycle, complete revocation ordering, delayed/incomplete joins and the complete authority chain remain unavailable. |
| Record source, host, backend, filesystem, outcome and limits | **Demonstrated for the accepted captures** | #336 source/report/archive and #337 source/report/archive provenance are recorded above and in the preserved manifests. The #337 host is explicitly ineligible for scope 2/3 acceptance. |
| Deliver a reviewed mechanism or precise negative result and alternatives | **Negative research result ready for review** | The current selected broker candidate fails Gate A. Alternatives below require fresh decisions; none is selected or authorized. This criterion supports a narrow closeout, not #321 enablement. |
| Relate to #267 without closing/redefining it or changing host security | **Contract documented / preserved** | #267 remains open. No privileged loader, policy setting, host-security change or creation tool was added. |
| Use the runtime/UI/provider seam and native harness | **Native study demonstrated; runtime/UI path intentionally unreached** | There is no production create operation to exercise at the runtime/UI/provider seam. The negative Gate A result required stopping before building that workflow. |
| Required repository checks for implementation changes | **Demonstrated for merged research changes** | PR #336 and PR #337 report module verification, vet, full tests, race tests and `make check` passing. These checks validate code quality, not native enforcement. |

## Proposed tracker reconciliation

Recommendation: after maintainer review, close #319 with the draft text in
`/tmp/komposyn-issue-319-tracker-drafts.md`, scoped explicitly to the rejected
candidate path. Do not mark unmeasured positive acceptance cases as passed.
The negative-result clause is the basis for closure; it does not imply the
entire checkbox list represents demonstrated positive behavior.

Keep #321 blocked by its existing dependency on #319 until a new creation
mechanism has positive operation-specific evidence and a separate explicit
enablement review. Keep #267 and umbrella #315 open. No tracker comments,
body edits, dependency changes or issue state changes have been made.

Possible future branches of work are decisions, not recommendations already
accepted: investigate a different protected launch/I/O boundary; request a
separate review of an independently qualified host-policy profile; propose a
separately reviewed trusted approval architecture; or consider a deployment
change requiring explicit review. Each would need its own threat model and
evidence. Do not treat any as authorization to prototype or change host policy.

## References

- [Issue #319](https://github.com/ayeshLK/komposyn/issues/319)
- [Parent issue #315](https://github.com/ayeshLK/komposyn/issues/315)
- [Creation enablement gate #321](https://github.com/ayeshLK/komposyn/issues/321)
- [Mutation contract #267](https://github.com/ayeshLK/komposyn/issues/267)
- [Merged PR #336](https://github.com/ayeshLK/komposyn/pull/336)
- [Merged PR #337](https://github.com/ayeshLK/komposyn/pull/337)
- [Creation feasibility study](create-one-study.md)
- [Gate A report](issue-319-gate-a.md)
- [Creation isolation design](creation-isolation-design.md)
- [ADR-0005](../adr/0005-investigate-creation-broker-with-protected-authority-chain.md)
- [Proposed ADR-0006](../adr/0006-proposed-negative-closeout-for-tested-file-creation-candidates.md)
