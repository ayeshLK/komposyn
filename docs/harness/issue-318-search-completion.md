# Issue #318 search implementation and acceptance

Status: PRs #332 and #333 implemented and validated bounded search; merged
PR #334 enabled the reviewed ext4-scoped profile. A fresh runner 14 run on
clean merge commit `ed53e9e2e59a8d5a404ab284f83a2687e9420aff` passed all eight
search cases using the corrected descriptor-based filesystem observer.
All 51 filesystem observations were recorded and identified ext4. This
completes the native search validation for the supported profile; the
acceptance matrix below records the scoped #318 closeout. Issue closure
follows review and merge of this documentation reconciliation.

Availability requires the measured Linux amd64 descriptor-bind worker profile.
Admission requires complete protected-owner coverage and verifies the retained
selected directory's statfs magic and exact ext4 mount type before once approval
and again at execution revalidation. The overall Stage 6 profile remains
incomplete because mutation and shell are unavailable and outside this search scope.
This record does not complete umbrella issue #315 or establish additional
filesystems, architectures, launch modes, shipped-binary or live-provider
acceptance.

## Acceptance matrix

| #318 requirement | Implementation and behavioral coverage | Native coverage | Status |
| --- | --- | --- | --- |
| Advertise only on an eligible host route and require explicit once approval | Availability requires the measured launcher profile; runtime/provider integration tests check schema, denial, exact directory/query/options/destination approval and continuation. Admission rejects non-ext4 before approval | `positive_content_search` passed on the composed Service and provider fixture under once approval | Native case passed on the measured profile; the approved enablement is implemented in this follow-up for the measured profile |
| Bind target, query, options and destination through admission and publication | Search fields are parsed before admission and included in request digest and execution authority; changed binding is refused | Native positive case recorded exact approval text and continuation | Implemented and measured; no broader provider or shipped-binary claim |
| Reuse bounded traversal, ignores, globs and protection without following links or crossing mounts | Host filters traversal snapshot, classifies path/inode/ancestors before descriptor preparation, and caps traversal, candidates, bytes, aggregate matcher/filter work, lines, output and duration | Native cases covered recursion, protected aliases, `.git`, mount exclusion, candidate substitution and root movement | Implemented and measured for listed cases; aggregate cgroup quotas remain unprovisioned and unclaimed |
| Search only small UTF-8 regular files and report truthful limits | Literal case-sensitive line matching, safe binary/invalid-UTF-8/oversize skips, bounded snippets, aggregate skipped count and `incomplete` output | Native composed-service positive search passes; per-file filtering and result-boundary edge cases are covered by focused implementation tests, not claimed as aggregate host resource enforcement | Implemented; aggregate cgroup quotas remain unprovisioned and unclaimed |
| Revalidate identity, directory and ignore observations before disclosure | Host retains candidate and discovery observations through content scanning, revalidates them, and closes all candidate/snapshot descriptors before deciding publication | `search_candidate_protected_substitution` and `search_ignore_rules_changed_after_content` passed; the latter recorded both worker joins and both descriptor-closure checks | Measured on the candidate; final-policy changes withheld stale results |
| Separate directory traversal and content search enforcement | Search traversal and sealed-content workers have separate seccomp profiles; exact-file read profile stays unchanged | Positive and cancellation cases recorded the two profiles and handoff boundary on the candidate | Measured for this profile; exact-file read profile remains unchanged |
| Cancellation, revocation, stale publication and cleanup | Existing operation authority/generation gates are retained; each child worker has independent join state; search joins honor the invocation deadline and suppress disclosure when cleanup is unconfirmed | `search_content_worker_cancellation_cleanup`, `search_selected_root_movement`, `search_owner_rotation_withholds_capture`, `search_revocation_withholds_capture`, and `search_mount_boundary` passed on the candidate | Measured for the listed cases; no aggregate cgroup quota or arbitrary-descendant claim |
| Keep known stale-policy refusal separate from uncertain cleanup | A joined scan with changed ignore policy returns a sanitized failed tool result, with no staged search content. Descriptor-close errors or unconfirmed joins remain unknown and interrupt continuation; cleanup ownership keeps admission blocked | `search_ignore_rules_changed_after_content` passed for nested and applicable ancestor mutations; both worker joins, both descriptor closures, two once-grant consumptions, sanitized continuations, and no disclosure were recorded | Measured on the candidate; cleanup uncertainty remains fail-closed |
| Enable only the measured profile | Search availability requires trusted Linux amd64 descriptor-bind worker composition; admission matches retained-descriptor `fstatfs` and mount-ID type and fails closed unless the mount type is exactly ext4 | Exact candidate search cases passed on the measured profile; full Stage 6 remains incomplete because mutation and shell are outside this profile | Ext4-only enablement approved and implemented here. Other filesystems and launch profiles remain unavailable |

## Merged-source acceptance run (runner 14)

The accepted closeout run tested clean PR #334 merge commit
`ed53e9e2e59a8d5a404ab284f83a2687e9420aff`. It used Go 1.26.11, Linux
7.0.0-42-generic amd64, UID 1000, and trusted root-owned mode-0755 Bubblewrap
0.9.0 (binary SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`).
Requested and actual bind modes were `ro-bind-fd`. Both build and study exited
0 without timeout or incomplete output draining, with cleanup complete.

The report records 38 measured passes, including all eight search cases, and
two unavailable cases (`mutation_profile` and `shell_profile`). Consequently,
the overall classification remains `stage6_profile_incomplete`; those two
unimplemented profiles do not prevent scoped search acceptance. Search cases
covered composed-Service positive search and protected aliases, candidate
substitution/content changes, post-content nested and ancestor ignore changes,
content-worker cancellation, selected-root movement, owner rotation,
revocation, and mount-boundary exclusion. The ignore-change case independently
recorded traversal/content worker joins, snapshot/candidate descriptor closure,
two once-grant consumptions, sanitized failure continuations, and withheld
stale disclosure. Default false evidence fields in other case rows must not
be interpreted as measurements those rows did not record.

The corrected observer uses one opened fixture descriptor for `fstatfs` and
mount-ID lookup, avoiding the historical symlink-parent mismatch. All 51
observations were `recorded`, with no failed, unknown or partial observations:
29 fixture roots, 19 selected listing/search scopes, one nested bind alias and
two nested mount boundaries. Each reported ext4 mount type, filesystem magic
`0x000000000000ef53`, and 4096-byte blocks. These measurements establish only
the recorded fixture environment and mount arrangements.

Archive: `/tmp/komposyn-issue215-stage6-20261117T223956Z-1895310.tar.gz`.

| Artifact | SHA-256 |
| --- | --- |
| Archive | `304e8424d8fd208575634ee5cf371b229bf19e67f0a54aa3269b5e494c1b294e` |
| Report | `1081d664438c2a4baa48a58b51624944913fbb6eb8c76b491c79cc65070dcf62` |
| Manifest | `7520352c625485bfd499b071aa3a6c1d6455e24c74f85473a306362d7f7e7649` |
| Source archive | `2fc27bc7b1fed1d5336d05653adabd294b269f6c336d6a22dc5dd78379c917ab` |
| Runner script | `88fe08edd2db5707253f5efef9062ee0d210ec3185db366b8e99069c43373a51` |
| Capture helper | `fade10f56a5b762489375c8c58e7f033ea50c1b37c7cc57dde81938b2560b7da` |
| Stage 6 test | `68cd502ef2bc80c3f41f0c4c2de429ef49e17e26595b0d48b0a9870440c95a83` |
| Runtime overlay | `1fbb3f6cb53e419eaec3ce32a7043469b51c16f9d3dc505d750d5889a1107db5` |

All nine `SHA256SUMS` entries and the report hash referenced by the manifest
were independently verified. The source archive, runner, capture helper,
Stage 6 test and runtime overlay hashes match the recorded Git commit. The
report's per-case filesystem observations agree with its summary and the
manifest. This documentation is a later closeout change; it was not part of
the measured source revision.

Behavioral coverage remains at the existing runtime-to-UI/provider integration
boundary and host/tool tests. It covers approval/query/options/destination
binding, denial, route/profile gating, bounded parsing and matching, safe
skips/incomplete results, and provider continuation. The native run supplements
that coverage; it does not turn synthetic owner-refusal controls into native
provider-source lifecycle evidence. It uses the Stage 6 test-binary worker and
provider fixtures, without a live provider or interactive CLI acceptance run.
Aggregate cgroup quotas and non-Linux enforcement remain unestablished.

### Behavioral and repository validation

The acceptance matrix is supported by these existing test seams:

- [Runtime approval and publication tests](../../internal/harness/runtime/read_publication_test.go)
  cover query/options/destination approval, once consumption, denial, sanitized
  known failure, and provider continuation. [Admission tests](../../internal/harness/runtime/read_admission_test.go)
  check the exact approval binding.
- [Composed-Service tests](../../internal/harness/sandbox/service_linux_test.go)
  check host-profile registration, ext4 admission, non-ext4 refusal before
  approval, and rejection on a listing-only profile.
- [Search filtering/publication tests](../../internal/harness/readbrokerstudy/search_execute_linux_test.go)
  cover protected aliases/substitution, ignore changes, shared work allowance,
  safe skips and result limits. [Literal matcher tests](../../internal/harness/readbrokerstudy/search_match_linux_test.go)
  cover UTF-8, line handling, long snippets and matching budgets; [tool tests](../../plugins/tools/tools_test.go)
  cover strict query/options parsing and encoded input limits.

Repository validation for this closeout is recorded in the accompanying PR.
The native run above is separate from ordinary Go/race tests. No new executor
or enforcement change is introduced by this documentation reconciliation.

## Runner 13 trusted-host run (historical)

The historical search-case run tested clean source
`000c3e6fbcb453a4bad902de934311e93143b9be` and used Go 1.26.11, Linux 7.0.0-42-generic amd64,
UID 1000, root-owned mode-755 Bubblewrap 0.9.0 (binary SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`), and
measured `ro-bind-fd`. It built the fixed Stage 6 test-binary worker from the
source commit recorded in this section. The build and study both exited 0 with
cleanup complete. All 38 implemented cases passed; mutation and shell were the
two unavailable cases, so the overall Stage 6 classification is
`stage6_profile_incomplete`. This is not acceptance for those unavailable
profiles, a shipped binary, or a real provider.

Archive: `/tmp/komposyn-issue318-native-results/komposyn-issue215-stage6-20261117T182252Z-1799056.tar.gz`;
SHA-256 `add7bb898c7b244cd1debb1fcb45d59f7f5bf2627663c71070b3e854f83700fc`.
Report SHA-256:
`0e6b9b2100ac499e12c55c5cd582bee6f39a5088838b085f0c59be077cc8a4a6`.
Manifest source archive SHA-256:
`b88f7a7166439d590e24f87ebc750c6d5578a3ed8c9a624ab4dd722991fb2dc4`.
The manifest records `checkout_clean: true`, matching requested and actual
revision, runner version 13, Bubblewrap trust metadata, and the measured bind
mode. The report records `search_ignore_rules_changed_after_content` as
`measured_pass`, with failed safe outcomes, both search workers joined, both
descriptor groups closed, no staged fixture bytes, disclosure withheld, and
cleanup confirmed.

The archive, manifest and report do not record the filesystem containing the
synthetic fixtures. Do not infer it from the kernel, Bubblewrap mode or host OS.
Runner 14 now instruments `statfs` and the matched, allowlisted mount
characteristics at the actual fixture paths and binds those observations to
case roles in the hashed report/manifest. The later runs below and the
merged-source acceptance run above exercise that instrumentation; historical
archives remain unchanged.

### Earlier runner 14 availability attempt

Clean candidate `d097603542c5b5bbf5ba09bb5f1ddfc38b1f175e` was run with Go
1.26.11 on Linux 7.0.0-42-generic amd64 as UID 1000. The local Bubblewrap
binary was version 0.9.0, mode 0755, but owned by UID/GID 65534; the harness
requires a root-owned backend and refused before backend probes or worker
launch. The study process exited 0, but the report classified the profile as
`native_unavailable`: seven synthetic controls passed, 33 native cases were
unavailable, and there were zero fixture filesystem observations. This is
not fresh enforcement evidence and establishes no fixture filesystem type.

Archive: `/tmp/komposyn-issue215-stage6-20261117T194852Z-2.tar.gz`; SHA-256
`e75894e42e32eb2310aa1fa58c8e60d5bab6edb94222596522314d696bc1e902`. Report
SHA-256 `047d0ac989223262220176a661669ef5203a0aade8fb3a0482e1eb425642c707`;
manifest SHA-256
`fc2d0fada881a7798c6686fe2ec36544a80c56a68cca27a8414025221057b7cf`.
All archive `SHA256SUMS` entries were independently verified. The report
explicitly marks fixture provenance `unavailable`, with zero observations and
no disclosed paths. This was an earlier attempt in the assistant environment;
the later trusted-host run below supersedes its availability outcome only.

To obtain the required measurement on a trusted regular-user Linux host, check
out the exact clean candidate and run:

```sh
git checkout d097603542c5b5bbf5ba09bb5f1ddfc38b1f175e
go_root="$(go env GOROOT)"
"$go_root/bin/go" version
PATH="$go_root/bin:$PATH" python3 -B scripts/run-issue215-stage6.py \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --output-root /tmp
```

The host must already have Go 1.26.11 or newer and Bubblewrap satisfying the
repository's root-ownership trust check. Do not infer fixture filesystem
values from this availability attempt or fill its archive retroactively.

## Pre-enablement trusted-host run (runner 14, historical)

The fresh run tested clean source commit
`d097603542c5b5bbf5ba09bb5f1ddfc38b1f175e` on Linux
7.0.0-42-generic amd64, Go 1.26.11, and UID 1000. Bubblewrap was trusted,
root-owned mode 0755, version 0.9.0, SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`; the
requested and actual bind mode were both `ro-bind-fd`. The clean source build
and study completed with exit code 0 and cleanup confirmed. The report contains
38 measured passes and two unavailable cases (`mutation_profile` and
`shell_profile`), so the overall classification remains
`stage6_profile_incomplete`. Those two profiles are outside this search
follow-up. All eight search cases passed.

The test process recorded 51 path-free fixture observations: 29 fixture roots,
19 selected listing/search scopes, one nested bind-mounted alias, and two
nested mount boundaries. All 51 were `recorded`; there were no failed,
unknown, or partial mount observations. Every observation reported filesystem
magic `0x000000000000ef53`, mount type `ext4`, and 4096-byte blocks.
Twenty-seven fixture roots and 17 selected scopes were `filesystem_root`
mounts with read/write and relatime options. Two roots and two selected scopes
were `subtree` mounts with read/write, nodev, nosuid, and relatime options.
The nested bind alias had the same subtree options. The two nested mount
boundaries were `subtree` mounts with read-only, nodev, nosuid, and relatime
options; their superblock options remained read/write.
These observations establish the fixture filesystems and arrangements in this
run only. They do not establish support or portability on other filesystems,
mount configurations, architectures, or launch modes.

Archive: `/tmp/komposyn-issue215-stage6-20261117T200336Z-1852003.tar.gz`; SHA-256
`16eecff18b46fbc0d7c153448cb4decbe21cde6aa8ec89665cc23ab811fc53f2`. Report
SHA-256 `e16995c0db36ff102659950ef1eebd517c70fbaadf610c4ed2a9ca9c949f7621`;
manifest SHA-256
`ee0204532f76afafff5c4436636317efc2d796166069820f6428aaede7599d41`.
Manifest source archive SHA-256:
`73d08d9462aee4d5ebd2eedd94ec5039c02befb06d53022ee105a1c8f7d865d8`.
Runner script SHA-256:
`88fe08edd2db5707253f5efef9062ee0d210ec3185db366b8e99069c43373a51`;
capture helper SHA-256:
`fade10f56a5b762489375c8c58e7f033ea50c1b37c7cc57dde81938b2560b7da`;
Stage 6 test SHA-256:
`19504f08e9f1c253ebc35e4db557e63a051aa960a277cfeb4768ddf691581b01`;
runtime overlay SHA-256:
`1fbb3f6cb53e419eaec3ce32a7043469b51c16f9d3dc505d750d5889a1107db5`;
overlay mapping SHA-256:
`3ec13bf2dbe94d8aa8118dac291aef63e4291888174d37b6e15b36a6bc62c0d0`.
The archive was extracted and all `SHA256SUMS` entries were independently
verified. The report's fixture summary matches the per-case records and
manifest.

The runner 14 archive predates the descriptor-mount-ID correction in this
follow-up. Its filesystem magic values and recorded mount types are preserved
as historical observations. Because the archive intentionally omits fixture
paths, it cannot establish whether any path had a symlinked parent; the
corrected runner now reads `statfs` from an opened fixture descriptor and
matches mount information by that descriptor's mount ID. A synthetic
regression test covers a symlinked parent pointing to tmpfs. No supplied
artifact demonstrates that the earlier archive reported a wrong filesystem.

## Supported-profile evidence and approval status

The ext4-scoped profile is trusted Linux amd64, Bubblewrap 0.9.0 in actual
`ro-bind-fd` mode, complete auth/settings owner coverage, successful composition
of the fixed traversal and content workers, and a selected directory whose
retained descriptor has ext4 mount type and the measured statfs magic. The
runner 14 archive recorded 51 fixture observations with ext4 mount type and
`f_type=0xef53`; the path-matching limitation for that historical observer is
documented above. Other filesystems, bind modes, architectures, and non-Linux
platforms remain unsupported or unmeasured.

The current availability gate requires the measured Linux amd64 descriptor-
bind worker profile. Admission reads statfs from the retained directory FD,
obtains that FD's mount ID from procfs, and matches the ID to an exact `ext4`
record in mountinfo. A failed or ambiguous lookup refuses search before once
approval and is repeated during revalidation. The separate ext4-scoped
enablement decision is recorded for this change; it does not expand the native
evidence to another filesystem or launch profile.

The follow-up adds the host-side filesystem eligibility check and profile
availability gate; traversal/content workers, worker protocol, and seccomp
profiles are unchanged. Focused tests exercise exact ext4 mount-type matching,
shared ext-family magic refusal, ext4 admission, pre-approval refusal on
procfs, and descriptor-based fixture provenance through a symlinked parent.
These changes do not extend the native worker evidence to another filesystem
or launch profile. The ext4-only enablement decision is recorded for this
change. The merged-source run above completes native validation of that
profile; this documentation closeout reconciles #318 while #315 remains open
for creation/editing research and gated patch delivery.

The version 11 historical harness included composed-service positive search,
protected-alias filtering, protected candidate substitution, selected-root
movement/replacement, owner rotation, revocation, content-worker cancellation,
and search-specific mount-boundary cases. A trusted-host run against clean
commit `511665bca9516239aee2931f404ce5e5af6fd6e0` used Go 1.26.11, trusted
Bubblewrap 0.9.0 (SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`), and
`ro-bind-fd`. The general read and listing cases passed, but the study failed:
the public Service rejected `search_files` before admission, leaving six search
cases inconclusive and the mount-boundary case unavailable. The report-classification test also had a stale
expected count (22 instead of 29 native-integrated cases). This is a failed
candidate, not search enforcement evidence. Archive SHA-256:
`34c3717a7b948756cba919c2af2100e928ac62a335acf23d5e794c4d314af75c`; report
SHA-256: `891c13477cc25046db8067887fa06b21f9ef8c950d626d6937d270c58835a7d2`.
The archive is at `/tmp/komposyn-issue215-stage6-20261116T232444Z-1617473.tar.gz`
on the test host. A second trusted-host run against clean commit
`d9d73498908afc90cf1e61fd13982d0eb89e1a78` used the same Go, Bubblewrap and
`ro-bind-fd` profile. The Service fix worked: positive search, protected
candidate substitution, search mount exclusion, selected-root movement, owner
rotation and revocation all passed. The cancellation case joined its content worker before the separate content
snapshot handoff; the shared operation evidence correctly retained the earlier
traversal worker's capability transfer. The harness incorrectly expected a
completed turn and a provider continuation; runtime correctly returned an
interrupted turn. That case was inconclusive and the suite failed
on the assertion. Archive SHA-256:
`80f6c576842401aaf4ed935b1c7652a1b5d98c45ed619b769d09f385b28b0a9f`; report
SHA-256: `ec9b4440a22e6c0389fda9c267349f97de0f28ab103203bc2e334a8780c478bc`.
The archive is at `/tmp/komposyn-issue215-stage6-20261116T234207Z-1626259.tar.gz`
on the test host. The follow-up corrects the cancellation assertion to distinguish the completed
traversal handoff from the cancelled content snapshot handoff, and to verify
that cancellation stops the turn, joins the content worker, publishes no result
and makes no provider continuation. The exact updated commit still requires a
clean trusted-host run.


The prior trusted-host run reported all seven search-specific cases as passes on
clean commit `77785c29ae602d9d1dd3cd0a1a5e84e4413d28f1`, using Go 1.26.11,
Linux 7.0.0-42-generic amd64, regular UID 1000, and root-owned Bubblewrap 0.9.0
(SHA-256 `e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`),
with `ro-bind-fd` selected and measured. The report has 37 measured passes and
two unavailable cases (`mutation_profile` and `shell_profile`, both outside
the implemented Stage 6 profile), so its overall classification is
`stage6_profile_incomplete`; that does not mark any search case unavailable.
The run used the fixed Stage 6 test-binary worker, not the shipped executable or
a real provider. Archive SHA-256:
`d30b50936d8d83037e53d05e0380b41c32f88d1fac0bec7916fdfdff8799c2c6`; report
SHA-256: `1521d4d40ec0c151d0ddba053a81f636d7008efcbe8f1d29ab7ec5783fc63b1c`.
The archive is `/tmp/komposyn-issue215-stage6-20261116T234951Z-1630674.tar.gz`
on the test host. A subsequent PR review found that traversal and content
workers shared one per-operation `Wait` bookkeeping record. Consequently,
content-worker cleanup assertions could report the traversal worker's joined
state without calling `Wait` on the content worker. Preserve this archive as
historical provenance, but do not treat its search cleanup or publication
results as current acceptance evidence. The supplied follow-up run against
`ac1893a` has verified archive SHA-256
`a4dd62b75317eb4be3192aa8985a164c3e9dc878b7775b5d6e70b5c1375e59d4` and report
SHA-256 `e1a8cf7d770d18cde05b65ee503f2d56f82f25988c6874e364d1b95dd1dfb4d1`.
It recorded 37 measured passes, one inconclusive search case, and two unavailable
profiles; the study exited 1 and was classified `candidate_failed_or_incomplete`.
The report-classification assertion expected 29 native-integrated cases when
the registry contained 30. The ignore-change case reached only its nested
mutation and returned `interrupted` because the host conflated final policy
validation failure with descriptor cleanup uncertainty. It did not establish
the ancestor mutation or the intended nondisclosure assertions. The archive is
`/tmp/komposyn-issue215-stage6-20261117T061746Z-1711147.tar.gz`. This failed run
remains bound to `ac1893a`; it is not a pass for the follow-up.

The current code records known final-policy refusal as a failed tool result only
after both workers joined and all candidate/snapshot descriptors closed. The
provider receives a fixed sanitized error continuation; no empty result is
returned and no private capture is staged. An unconfirmed worker join or
descriptor close remains unknown, interrupts the turn, and preserves the
existing cleanup/admission barrier. Runner version 13 asserts the expected 30
native-integrated cases and reports traversal/content joins and descriptor
closure separately. Focused runtime, host and lifecycle tests plus the synthetic
Stage 6 overlay pass locally. The trusted-host run above verifies the new
final-policy refusal behavior on the exact candidate.

Earlier trusted-host candidates exposed and corrected the public Service search
admission allowlist and the cancellation-test expectation. Their archive hashes
and tested commits remain recorded above for provenance. The final run confirms
that the cancellation checkpoint joins the content worker before its separate
snapshot handoff, leaves no private result, and makes no provider continuation.

The historical native evidence supports only its recorded candidates; the
runner 13 evidence above applies only to its exact recorded candidate and
profile. Runner 14 supplies the filesystem provenance for its own clean
candidate; it does not retroactively change earlier archives. Merged PR #334
opened search availability only for the approved ext4-scoped profile. This
closeout does not close umbrella issue #315 or establish shipped-binary,
real-provider, file-snapshot, non-Linux, mutation, shell, or aggregate cgroup
quota support. Listing evidence remains historical supporting context only.
