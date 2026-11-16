# Issue #318 search implementation and acceptance

Status: implementation is prepared behind a closed production gate. Search
specific native cases are present in the Stage 6 harness but have not been
run on a trusted host. This record does not approve enablement or complete
umbrella issue #315.

## Acceptance matrix

| #318 requirement | Implementation and behavioral coverage | Native coverage | Status |
| --- | --- | --- | --- |
| Advertise only on an eligible host route and require explicit once approval | Independent `SearchFilesAvailable` gate; runtime/provider integration tests check schema, denial, exact directory/query/options/destination approval and continuation | `positive_content_search` drives the composed Service and provider fixture | Implemented; native case awaits trusted-host run |
| Bind target, query, options and destination through admission and publication | Search fields are parsed before admission and included in request digest and execution authority; changed binding is refused | Native positive case records exact approval text | Implemented; native case awaits trusted-host run |
| Reuse bounded traversal, ignores, globs and protection without following links or crossing mounts | Host filters traversal snapshot, classifies path/inode/ancestors before descriptor preparation, and caps traversal, candidates, bytes, matcher work, lines, output and duration | Positive case covers recursion, protected hard-link alias and `.git`; `search_mount_boundary` independently checks matching content under an actual mount-ID-distinct directory | Implementation tests pass; search mount/race evidence remains incomplete |
| Search only small UTF-8 regular files and report truthful limits | Literal case-sensitive line matching, safe binary/invalid-UTF-8/oversize skips, bounded snippets, aggregate skipped count and `incomplete` output | No trusted-host measurement yet | Implemented and unit tested |
| Revalidate identity and revision before disclosure | Host retains candidate descriptors through the content worker, reopens each candidate beneath the selected directory, reclassifies current path/identity/ancestors, and omits changed candidates before publication | `search_candidate_protected_substitution` uses independent same-user actors to replace a candidate with a protected auth hard link after content scan | Implementation test passes; native case awaits trusted-host run |
| Separate directory traversal and content search enforcement | Search traversal and sealed-content workers have separate seccomp profiles; exact-file read profile stays unchanged | Positive search case captures both worker probes | Profile implementation/tests pass; native case awaits trusted-host run |
| Cancellation, revocation, stale publication and cleanup | Existing operation authority/generation gates are retained; cancellation before content transfer joins the worker and suppresses disclosure | `search_content_worker_cancellation_cleanup`, `search_selected_root_movement`, `search_owner_rotation_withholds_capture`, `search_revocation_withholds_capture`, and `search_mount_boundary` cover cleanup, selected-object authority, revocation, stale-capture suppression, and mount exclusion | Partial; all native cases await trusted-host measurement |
| Keep unavailable until measured evidence and independent review | `searchAccepted` defaults false; no listing gate or version string opens it | The first trusted-host candidate archive contains a failed run; no search worker launched and no search case passed | Gate closed; enablement approval not recorded |

## Proposed profile and evidence status

The proposed profile is trusted Linux amd64 with Bubblewrap selecting the
`ro-bind-fd` launch mode, complete owner inventory, and successful composition
of the fixed search traversal and content workers. This is a proposal only:
no native search result establishes that profile yet. File-snapshot mode,
other architectures, and non-Linux platforms remain unavailable.

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
rotation and revocation all passed. The cancellation case joined its worker
without transferring the content descriptor, but the harness incorrectly
expected a completed turn and a provider continuation; runtime correctly
returned an interrupted turn. That case was inconclusive and the suite failed
on the assertion. Archive SHA-256:
`80f6c576842401aaf4ed935b1c7652a1b5d98c45ed619b769d09f385b28b0a9f`; report
SHA-256: `ec9b4440a22e6c0389fda9c267349f97de0f28ab103203bc2e334a8780c478bc`.
The archive is at `/tmp/komposyn-issue215-stage6-20261116T234207Z-1626259.tar.gz`
on the test host. The follow-up corrects the cancellation assertion to verify
that cancellation stops the turn, joins the worker, publishes no result and
makes no provider continuation. The exact updated commit still requires a
clean trusted-host run.

Before enablement, a trusted-host run must measure the exact follow-up commit,
actual backend and launch mode, both worker probes, and every search-specific
case, including directory races, revocation, stale publication, mount-boundary
exclusion and cleanup. Review the resulting
manifest and report against the code before changing the closed gate. Listing
evidence remains historical supporting context only.
