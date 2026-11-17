# Issue #318 search implementation and acceptance

Status: the latest trusted-host run measured all implemented #318 search cases
as passes on clean commit `000c3e6fbcb453a4bad902de934311e93143b9be` with runner
13 and `ro-bind-fd`. The overall Stage 6 profile remains incomplete because
mutation and shell profiles are unavailable and outside this search follow-up.
Production search remains disabled pending independent evidence review and a
separate enablement decision. This record does not complete umbrella issue
#315.

## Acceptance matrix

| #318 requirement | Implementation and behavioral coverage | Native coverage | Status |
| --- | --- | --- | --- |
| Advertise only on an eligible host route and require explicit once approval | Independent `SearchFilesAvailable` gate; runtime/provider integration tests check schema, denial, exact directory/query/options/destination approval and continuation | `positive_content_search` passed on the composed Service and provider fixture under once approval | Native case passed on the measured profile; production gate remains closed pending independent evidence review and enablement approval |
| Bind target, query, options and destination through admission and publication | Search fields are parsed before admission and included in request digest and execution authority; changed binding is refused | Native positive case recorded exact approval text and continuation | Implemented and measured; no broader provider or shipped-binary claim |
| Reuse bounded traversal, ignores, globs and protection without following links or crossing mounts | Host filters traversal snapshot, classifies path/inode/ancestors before descriptor preparation, and caps traversal, candidates, bytes, aggregate matcher/filter work, lines, output and duration | Native cases covered recursion, protected aliases, `.git`, mount exclusion, candidate substitution and root movement | Implemented and measured for listed cases; aggregate cgroup quotas remain unprovisioned and unclaimed |
| Search only small UTF-8 regular files and report truthful limits | Literal case-sensitive line matching, safe binary/invalid-UTF-8/oversize skips, bounded snippets, aggregate skipped count and `incomplete` output | Native composed-service positive search passes; per-file filtering and result-boundary edge cases are covered by focused implementation tests, not claimed as aggregate host resource enforcement | Implemented; aggregate cgroup quotas remain unprovisioned and unclaimed |
| Revalidate identity, directory and ignore observations before disclosure | Host retains candidate and discovery observations through content scanning, revalidates them, and closes all candidate/snapshot descriptors before deciding publication | `search_candidate_protected_substitution` and `search_ignore_rules_changed_after_content` passed; the latter recorded both worker joins and both descriptor-closure checks | Measured on the candidate; final-policy changes withheld stale results |
| Separate directory traversal and content search enforcement | Search traversal and sealed-content workers have separate seccomp profiles; exact-file read profile stays unchanged | Positive and cancellation cases recorded the two profiles and handoff boundary on the candidate | Measured for this profile; exact-file read profile remains unchanged |
| Cancellation, revocation, stale publication and cleanup | Existing operation authority/generation gates are retained; each child worker has independent join state; search joins honor the invocation deadline and suppress disclosure when cleanup is unconfirmed | `search_content_worker_cancellation_cleanup`, `search_selected_root_movement`, `search_owner_rotation_withholds_capture`, `search_revocation_withholds_capture`, and `search_mount_boundary` passed on the candidate | Measured for the listed cases; no aggregate cgroup quota or arbitrary-descendant claim |
| Keep known stale-policy refusal separate from uncertain cleanup | A joined scan with changed ignore policy returns a sanitized failed tool result, with no staged search content. Descriptor-close errors or unconfirmed joins remain unknown and interrupt continuation; cleanup ownership keeps admission blocked | `search_ignore_rules_changed_after_content` passed for nested and applicable ancestor mutations; both worker joins, both descriptor closures, two once-grant consumptions, sanitized continuations, and no disclosure were recorded | Measured on the candidate; cleanup uncertainty remains fail-closed |
| Keep unavailable until measured evidence and independent review | `searchAccepted` defaults false; no listing gate or version string opens it | Exact candidate search cases now pass on the proposed profile; full Stage 6 remains incomplete because mutation and shell are outside this profile | Gate closed; separate enablement approval not recorded |

## Latest trusted-host run

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

## Supported-profile evidence and approval status

The last accepted search-specific evidence records trusted Linux amd64,
Bubblewrap's actual `ro-bind-fd` mode, complete auth/settings owner coverage,
and successful composition of the fixed traversal and content workers. Its
fixture filesystem was not recorded. Runner 14 adds the missing capture, but
until a trusted-host run produces recorded fixture observations, no
filesystem-specific portability claim is established. Other bind modes,
architectures, and non-Linux platforms remain unavailable. This is evidence
for review, not authorization to enable search.

For the next enablement review, consider only Linux amd64 with a trusted,
root-owned Bubblewrap installation using the measured `ro-bind-fd` launch
profile and complete protected-owner inventory. Before opening the gate, use a
fresh runner 14 archive to identify the filesystem type and selected mount
characteristics at both the selected search scope and mount-boundary fixtures.
The runtime gate must be able to recognize the accepted filesystem scope from
the approved directory and refuse any unmeasured type; if that check is not
implemented or the run reports partial/unknown provenance, keep search closed
or gather narrower evidence. Do not treat this profile proposal as an
enablement decision. `searchAccepted` remains false.

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
profile. The production `searchAccepted` gate remains false. Independent review
of this evidence and a separate enablement decision are still required before
enablement.
This work does not close umbrella issue #315 or establish shipped-binary,
real-provider, file-snapshot, non-Linux, mutation, shell, or aggregate cgroup
quota support. Listing evidence remains historical supporting context only.
