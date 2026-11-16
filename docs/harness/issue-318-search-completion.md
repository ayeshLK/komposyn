# Issue #318 search implementation and acceptance

Status: implementation is prepared behind a closed production gate. Search
specific native cases are present in the Stage 6 harness but have not been
run on a trusted host. This record does not approve enablement or complete
umbrella issue #315.

## Acceptance matrix

| #318 requirement | Implementation and behavioral coverage | Native coverage | Status |
| --- | --- | --- | --- |
| Advertise only on an eligible host route and require explicit once approval | Independent `SearchFilesAvailable` gate; runtime/provider integration test checks schema, exact directory/query/options/destination approval and continuation | `positive_content_search` drives the composed Service and provider fixture | Implemented; native case awaits trusted-host run |
| Bind target, query, options and destination through admission and publication | Search fields are parsed before admission and included in request digest and execution authority; changed binding is refused | Native positive case records exact approval text | Implemented; native case awaits trusted-host run |
| Reuse bounded traversal, ignores, globs and protection without following links or crossing mounts | Host filters traversal snapshot, classifies path/inode/ancestors before descriptor preparation, and caps traversal, candidates, bytes, matcher work, lines, output and duration | Positive case covers recursion, protected hard-link alias and `.git`; listing mount evidence is not counted as search evidence | Implementation tests pass; search mount/race evidence remains incomplete |
| Search only small UTF-8 regular files and report truthful limits | Literal case-sensitive line matching, safe binary/invalid-UTF-8/oversize skips, bounded snippets, aggregate skipped count and `incomplete` output | No trusted-host measurement yet | Implemented and unit tested |
| Revalidate identity and revision before disclosure | Host retains candidate descriptors through the content worker, reopens each candidate beneath the selected directory, reclassifies current path/identity/ancestors, and omits changed candidates before publication | `search_candidate_protected_substitution` uses independent same-user actors to replace a candidate with a protected auth hard link after content scan | Implementation test passes; native case awaits trusted-host run |
| Separate directory traversal and content search enforcement | Search traversal and sealed-content workers have separate seccomp profiles; exact-file read profile stays unchanged | Positive search case captures both worker probes | Profile implementation/tests pass; native case awaits trusted-host run |
| Cancellation, revocation, stale publication and cleanup | Existing operation authority/generation gates are retained; cancellation before content transfer joins the worker and suppresses disclosure | `search_content_worker_cancellation_cleanup` covers cancellation and joined cleanup; root movement, owner rotation and revocation cases remain to be measured | Partial; native coverage incomplete |
| Keep unavailable until measured evidence and independent review | `searchAccepted` defaults false; no listing gate or version string opens it | No search native archive/report exists | Gate closed; enablement approval not recorded |

## Proposed profile and evidence status

The proposed profile is trusted Linux amd64 with Bubblewrap selecting the
`ro-bind-fd` launch mode, complete owner inventory, and successful composition
of the fixed search traversal and content workers. This is a proposal only:
no native search result establishes that profile yet. File-snapshot mode,
other architectures, and non-Linux platforms remain unavailable.

The harness now includes composed-service positive search, protected-alias
filtering, protected candidate substitution, and content-worker cancellation
cases. It was compiled with the existing Stage 6 overlay. The current
environment's `/usr/bin/bwrap` is `nobody:nogroup`, so the Stage 6 test refused
before backend probes or worker launch. That run is unavailable evidence, not
a search pass. No archive or report hash is claimed for native search.

Before enablement, the trusted-host run still needs to measure the exact clean
candidate revision, actual backend and launch mode, both worker probes, and
search-specific root/directory movement, protected-owner rotation, revocation,
stale-publication, mount-boundary and cleanup behavior. Review the resulting
manifest and report against the code before changing the closed gate. Listing
evidence remains historical supporting context only.
