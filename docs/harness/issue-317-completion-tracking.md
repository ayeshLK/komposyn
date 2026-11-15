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

The current host runs as a regular user and has the required Go binary at
`$(go env GOROOT)/bin/go` (Go 1.26.11). `/usr/bin/bwrap` is owned by
`nobody:nogroup` with mode 0755, so the repository trust check must refuse it.
Do not change its ownership or bypass that check. A runner refusal is an
unavailable-evidence record, not a pass. No advanced-options gate should be
opened from that result.

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
