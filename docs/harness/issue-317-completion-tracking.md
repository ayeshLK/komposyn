# Issue #317 completion and evidence matrix

This record distinguishes implementation and ordinary tests from native
filesystem evidence. The recursive options remain unavailable until the
required native profile has been measured on the candidate revision and that
evidence has received independent maintainer review.

## Acceptance matrix

| #317 criterion | Implementation and ordinary coverage | Native harness coverage | Current result |
|---|---|---|---|
| Explicit recursion, hidden/ignored inclusion, basename globs, and exact once-approval binding | `plugins/tools/conversation.go`; runtime admission and publication tests; provider-request schema/instruction tests | `positive_recursive_directory_listing` and `recursive_listing_nested_ignore_filters` exercise the real Service/provider fixture | Implemented and fixture-compiled; native run unavailable |
| Defaults, nested `.gitignore` precedence/negation, ignored-parent behavior, glob traversal, and hidden filtering | `TestRecursiveDirectoryListingFiltersAfterTraversalAndAppliesNestedIgnores`; worker and parser tests | `positive_directory_listing` plus `recursive_listing_nested_ignore_filters` | Ordinary tests pass; native cases await trusted host |
| `.git` exclusion, symlink refusal, and mount boundaries under explicit options | Worker traversal tests; host-side mounted-object and protection checks | `positive_recursive_directory_listing` includes a directory symlink and `.git`; `directory_listing_mount_boundary` checks a mounted child and protected bind-mounted alias | Fixture assertions present; native cases unavailable here |
| Protected aliases, owner rotation, and missing owner coverage | Registry and admission tests; protected identities checked before visible counts | Recursive positive case creates a nested protected hard link; `directory_listing_owner_rotation` checks stale publication; `recursive_listing_incomplete_owner_inventory` checks refusal before approval/worker | Missing-owner case is synthetic runtime evidence only; native worker cases await trusted host |
| Descendant movement/replacement and object authority | Broker identity and mount revalidation tests | `recursive_descendant_movement_replacement` uses a separate actor after final revalidation | Fixture present; native case unavailable here |
| Cancellation, revocation, stale publication, and cleanup | Runtime/session cancellation and lifecycle tests; descriptor-refusal regression tests | `directory_listing_worker_cleanup` cancels after the worker holds the capability; `recursive_listing_cancellation_after_traversal` cancels after host filtering; owner rotation suppresses capture after traversal | Pre-release and post-traversal cancellation cases are in source; native run unavailable here. Cancellation during active traversal is still not independently measured |
| Shared 10,000-entry budget, 1,000 visible-path limit, 64 KiB output, five-second deadline, bounded ignore input and matcher work | `TestRecursiveTraversalStopsAtSharedEntryBudget`, bounded encoder test, broker filter budgets and existing admission/session deadlines | `recursive_listing_entry_limit` creates a 10,020-file tree and requires a bounded `incomplete=true` provider result | Ordinary tests pass; native limit case awaits trusted host. Stage 6 currently observes only provider-safe counts |
| Provider-visible names/counts/errors and stale-result suppression | Runtime continuation tests; host filter removes protected entries before counts and strips all worker snapshots before serialization | Recursive provider fixture cases assert disclosed results, approval details, and absence of filtered names | Fixture assertions present; no native result from this host |
| Supported-profile enablement and route/schema composition | Linux gate defaults false; public `sandbox.Service` forwards the options gate; runtime schema/instructions follow the exposed definition | Test-only Stage 6 composition sets the gate to exercise the path; this is not an enablement decision | Gate remains closed. No production profile can be accepted without native evidence and independent review |

## Evidence provenance and host result

The candidate starts from `origin/master` commit
`71bb16b82705755a3b52ebc4861b594bae453cda` (merged PR #328). Prior
nonrecursive reports belong to their recorded source and profile and do not
prove recursive enforcement.

The agent execution host used for the first attempt runs as a regular user and
has the required Go binary at `$(go env GOROOT)/bin/go` (Go 1.26.11).
`/usr/bin/bwrap` is owned by `nobody:nogroup` with mode 0755, so the repository
trust check must refuse it. Do not change its ownership or bypass that check.
A runner refusal is an unavailable-evidence record, not a pass. No
advanced-options gate should be opened from that result.

A subsequent trusted-host report was supplied for source revision
`a695916f1f56af69df244b07898b959cca43f6c6` (Go 1.26.11, Linux
`7.0.0-42-generic`, x86_64, UID 1000, `/usr/bin/bwrap` UID/GID 0:0 mode
0755, automatic bind mode). The runner built and started native workers: 13
native-integrated cases passed, while nine listing cases became inconclusive
and the test process exited 1. The listing workers started but returned a
failed outcome. A focused kernel regression reproduced the cause: the
read-worker seccomp profile denied `statx`, which the listing worker needs for
mount-ID validation; listing also requires `newfstatat` and `openat2` for
ignore metadata and constrained descendant traversal. This is a native
failure, not unavailable evidence. The candidate now uses an operation-specific
listing filter while retaining the stricter exact-file read filter. The
supplied report does not test that fix; rerun Stage 6 on the updated clean
revision before considering native acceptance.

The supplied archive SHA-256 is
`d08f9b0315d6de946b27b61602afb5ce5d7c2dfd8e5d2014467286e451ffc37b`; the
report SHA-256 is
`967b3ef5a9a122844fbf9bfc3cb0343b26da52fa863fc344e2724fd87deb9bdb`.

A second trusted-host run exercised commit
`fdbac5fb5bb35079aee66460cd02c3c73f3494c0` with the same Go, kernel,
architecture, Bubblewrap identity, and automatic bind mode. It measured 20 of
22 native-integrated cases as passes, including basic and recursive positive
listing, descendant replacement, mount boundary, owner rotation, and
cancellation. The nested-ignore and large-listing cases reached a succeeded
worker result and provider continuation but were left inconclusive because
their fixture assertions decoded the outer tool-result envelope instead of
its nested listing JSON. The archived report does not include private
provider-result contents, so those listing assertions are not accepted as
passes. The Stage 6 tests now decode both layers and require the expected
names/counts. The worker probe also now reports which operation profile was
installed, and readiness checks that it matches the host-selected operation.
Neither change is validated by this archive. Rerun on the same trusted host at
the updated clean revision before accepting the evidence.

The second archive SHA-256 is
`302d9a304de07c5a98d6ab92882e02ba69b0a60a975d9fde2ce2c1cd6207a349`; the
report SHA-256 is
`a9dcd5f99aa75d2017411e2d4cd16918ee25d8ab85ac604c9792e4421109ea7b`.

Once an eligible host is available, run from a clean committed candidate:

```sh
go_root="$(go env GOROOT)"
"$go_root/bin/go" version
PATH="$go_root/bin:$PATH" python3 -B scripts/run-issue215-stage6.py \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --output-root /tmp
```

Record the report and archive SHA-256, exact commit, Go version, kernel,
filesystem, architecture, trusted backend identity/version, selected bind
mode, worker image/profile, and each #317 case status. Run each backend or
worker-image profile that the final availability decision would expose. Keep
artifacts outside the repository and do not treat skipped cases as passes.

## Enablement decision

Do not set `linuxImplementation.listingOptionsAvailable` in production yet.
The new public forwarding method keeps the complete composition path
reviewable while preserving the false default. The eventual enablement must
select only a profile whose backend identity and launch-mode cases passed on
the exact tested source revision. Since this host cannot produce that evidence,
there is no safe production enablement diff to apply yet. The enforcement
revision and any later gate-only revision must remain separately identifiable.

Issue #317 remains open, and #318 remains blocked until the missing trusted
native run and independent evidence review are complete. This record does not
complete #315 or mutation work.
