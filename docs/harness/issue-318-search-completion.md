# Issue #318 search implementation and acceptance

Status: runner 14 measured all eight implemented #318 search cases as passes on
clean commit `d097603542c5b5bbf5ba09bb5f1ddfc38b1f175e` with trusted Bubblewrap
0.9.0 and `ro-bind-fd`. All 51 fixture filesystem observations were recorded
and identified ext4. The overall Stage 6 profile remains incomplete because
mutation and shell profiles are unavailable and outside this search follow-up.
The approved ext4-scoped enablement is implemented here: availability requires
the measured Linux amd64 descriptor-bind worker profile, complete owner
coverage, and the selected directory's retained descriptor to report ext4.
Non-ext4 directories are refused before approval and again at execution
revalidation. The maintainer approved this scoped enablement in the task
conversation, and this implementation enables only the measured ext4-scoped
profile. This record does not complete umbrella issue #315.

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
| Enable only the measured profile | Search availability requires trusted Linux amd64 descriptor-bind worker composition; admission uses `fstatfs` on the retained directory and fails closed unless it is ext4 | Exact candidate search cases passed on the measured profile; full Stage 6 remains incomplete because mutation and shell are outside this profile | Ext4-only enablement approved and implemented here. Other filesystems and launch profiles remain unavailable |

## Runner 13 trusted-host run (historical)

The accepted search-case run used Go 1.26.11, Linux 7.0.0-42-generic amd64,
UID 1000, root-owned mode-755 Bubblewrap 0.9.0 (binary SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`), and
measured `ro-bind-fd`. It built the fixed Stage 6 test-binary worker from the
clean exact candidate commit above. The build and study both exited 0 with
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
case roles in the hashed report/manifest. This changes no historical archive;
a fresh trusted-host run must exercise the instrumentation before any
filesystem-specific support claim can be made.

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

## Latest trusted-host run (runner 14)

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

## Supported-profile evidence and approval status

The accepted runner 14 evidence records trusted Linux amd64, Bubblewrap 0.9.0
in actual `ro-bind-fd` mode, complete auth/settings owner coverage, successful
composition of the fixed traversal and content workers, and ext4 fixture
locations with the mount characteristics listed above. Other filesystems,
bind modes, architectures, and non-Linux platforms remain unmeasured. This is
evidence for review, not authorization to enable search.

For the next enablement review, propose Linux amd64 with trusted, root-owned
Bubblewrap 0.9.0 using actual `ro-bind-fd`, complete protected-owner inventory,
and an approved search directory on ext4 (`f_type=0xef53`). The current
`SearchFilesAvailable`/`SearchProfilesAvailable` gate checks the Linux amd64
descriptor-bind worker profile but does not inspect the selected directory's
filesystem type. The enablement change must therefore either check the
canonical selected directory's filesystem type during admission and refuse
non-ext4 targets, or add native evidence and a separate explicit decision for
additional filesystem types. Keep the public gate closed for unknown or
unmeasured filesystems and for partial provenance. That change must also
exercise the real composed Service/runtime availability, provider schema and
instructions, admission, approval, and unsupported-profile refusal paths.
This evidence applies to the worker/enforcement revision recorded above. The
follow-up adds only a host-side filesystem eligibility check and the profile
availability gate; traversal/content workers, worker protocol, seccomp
profiles, and native assertions are unchanged. Focused tests exercise ext4
admission and pre-approval refusal on procfs. These changes do not extend the
native evidence to another filesystem or launch profile. The ext4-only
enablement decision is recorded for this change; #318/#315 remain open until
their other acceptance and closure criteria are completed.

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
candidate; it does not retroactively change earlier archives. This follow-up
opens search availability only for the approved ext4-scoped profile. This work does not close umbrella issue #315 or establish shipped-binary,
real-provider, file-snapshot, non-Linux, mutation, shell, or aggregate cgroup
quota support. Listing evidence remains historical supporting context only.
