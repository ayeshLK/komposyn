# Issue #318 search implementation and acceptance

Status: implementation and all search-specific Stage 6 native cases are
measured on the proposed trusted Linux amd64 `ro-bind-fd` profile. The
production gate remains closed pending independent review and a separate
enablement decision. This record does not approve enablement or complete
umbrella issue #315.

## Acceptance matrix

| #318 requirement | Implementation and behavioral coverage | Native coverage | Status |
| --- | --- | --- | --- |
| Advertise only on an eligible host route and require explicit once approval | Independent `SearchFilesAvailable` gate; runtime/provider integration tests check schema, denial, exact directory/query/options/destination approval and continuation | `positive_content_search` drives the composed Service and provider fixture under once approval | Implemented; native case measured pass; gate still awaits independent enablement review |
| Bind target, query, options and destination through admission and publication | Search fields are parsed before admission and included in request digest and execution authority; changed binding is refused | Native positive case records exact approval text and successful continuation | Implemented; native case measured pass |
| Reuse bounded traversal, ignores, globs and protection without following links or crossing mounts | Host filters traversal snapshot, classifies path/inode/ancestors before descriptor preparation, and caps traversal, candidates, bytes, matcher work, lines, output and duration | Positive case covers recursion, protected hard-link alias and `.git`; `search_mount_boundary` checks matching content under an actual mount-ID-distinct directory; candidate substitution and root movement cases pass | Search native cases measured pass on `ro-bind-fd` |
| Search only small UTF-8 regular files and report truthful limits | Literal case-sensitive line matching, safe binary/invalid-UTF-8/oversize skips, bounded snippets, aggregate skipped count and `incomplete` output | Native composed-service positive search passes; per-file filtering and result-boundary edge cases are covered by focused implementation tests, not claimed as aggregate host resource enforcement | Implemented; aggregate cgroup quotas remain unprovisioned and unclaimed |
| Revalidate identity and revision before disclosure | Host retains candidate descriptors through the content worker, reopens each candidate beneath the selected directory, reclassifies current path/identity/ancestors, and omits changed candidates before publication | `search_candidate_protected_substitution` uses independent same-user actors to replace a candidate with a protected auth hard link after content scan | Native case measured pass |
| Separate directory traversal and content search enforcement | Search traversal and sealed-content workers have separate seccomp profiles; exact-file read profile stays unchanged | Positive search and cancellation cases verify the separate worker probes and content handoff boundary | Native cases measured pass; exact-file read profile remains unchanged |
| Cancellation, revocation, stale publication and cleanup | Existing operation authority/generation gates are retained; cancellation before content transfer joins the worker and suppresses disclosure | `search_content_worker_cancellation_cleanup`, `search_selected_root_movement`, `search_owner_rotation_withholds_capture`, `search_revocation_withholds_capture`, and `search_mount_boundary` cover cleanup, selected-object authority, revocation, stale-capture suppression, and mount exclusion | All search-specific native cases measured pass |
| Keep unavailable until measured evidence and independent review | `searchAccepted` defaults false; no listing gate or version string opens it | Exact candidate search cases now pass on the proposed profile; full Stage 6 remains incomplete because mutation and shell are outside this profile | Gate closed; separate enablement approval not recorded |

## Supported-profile evidence and approval status

The search-specific evidence supports the proposed profile: trusted Linux amd64,
Bubblewrap's actual `ro-bind-fd` mode, complete auth/settings owner coverage,
and successful composition of the fixed traversal and content workers. Other
bind modes, architectures, and non-Linux platforms remain unavailable. This is
evidence for review, not authorization to enable search.

The version 11 harness includes composed-service positive search,
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


The final trusted-host run measured all seven search-specific cases as passes on
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
on the test host.

Earlier trusted-host candidates exposed and corrected the public Service search
admission allowlist and the cancellation-test expectation. Their archive hashes
and tested commits remain recorded above for provenance. The final run confirms
that the cancellation checkpoint joins the content worker before its separate
snapshot handoff, leaves no private result, and makes no provider continuation.

The native evidence now supports independent review of the proposed profile.
The production `searchAccepted` gate remains false. Enablement still requires
independent review of this evidence and an explicit separate enablement decision.
This work does not close umbrella issue #315 or establish shipped-binary,
real-provider, file-snapshot, non-Linux, mutation, shell, or aggregate cgroup
quota support. Listing evidence remains historical supporting context only.
