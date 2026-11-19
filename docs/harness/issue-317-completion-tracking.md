# Issue #317 completion and evidence matrix

Status: issue #317's accepted scope is complete for the supported profile
below. PRs [#328](https://github.com/ayeshLK/komposyn/pull/328) and
[#329](https://github.com/ayeshLK/komposyn/pull/329) implemented recursive
listing and completed its native acceptance work. PR
[#330](https://github.com/ayeshLK/komposyn/pull/330), merged as
`7938bbf998fd2a1a7db503417f7201ef5e32f1e0`, enabled the reviewed profile.
This closure is limited to trusted Linux amd64 Bubblewrap selecting
`ro-bind-fd`; other architectures and bind modes remain unavailable for
advanced options. It does not complete umbrella issue #315 or enable search or
mutation.

## Acceptance matrix

| #317 criterion | Implementation and ordinary coverage | Native harness coverage | Current result |
|---|---|---|---|
| Explicit recursion, hidden/ignored inclusion, basename globs, and exact once-approval binding | `plugins/tools/conversation.go`; runtime admission and publication tests; provider-request schema/instruction tests | `positive_recursive_directory_listing` and `recursive_listing_nested_ignore_filters` exercise the real Service/provider fixture | Satisfied for the supported profile |
| Defaults, nested `.gitignore` precedence/negation, ignored-parent behavior, glob traversal, and hidden filtering | `TestRecursiveDirectoryListingFiltersAfterTraversalAndAppliesNestedIgnores`; worker and parser tests | `positive_directory_listing` plus `recursive_listing_nested_ignore_filters` | Satisfied; filters do not grant traversal authority |
| `.git` exclusion, symlink refusal, and mount boundaries under explicit options | Worker traversal tests; host-side mounted-object and protection checks | `positive_recursive_directory_listing` includes a directory symlink and `.git`; `directory_listing_mount_boundary` checks a mounted child and protected bind-mounted alias | Satisfied for the supported profile |
| Protected aliases, owner rotation, and missing owner coverage | Registry and admission tests; protected identities checked before visible counts | Recursive positive case creates a nested protected hard link; `directory_listing_owner_rotation` checks stale publication; `recursive_listing_incomplete_owner_inventory` checks refusal before approval/worker | Satisfied. Missing-owner refusal is synthetic runtime-composition evidence, not native worker enforcement |
| Descendant movement/replacement and object authority | Broker identity and mount revalidation tests | `recursive_descendant_movement_replacement` uses a separate actor after final revalidation | Satisfied for the tested Linux amd64 profile |
| Cancellation, revocation, stale publication, and cleanup | Runtime/session cancellation and lifecycle tests; descriptor-refusal regression tests | `directory_listing_worker_cleanup` cancels after the worker holds the capability; `recursive_listing_cancellation_after_traversal` cancels after host filtering; owner rotation suppresses capture after traversal | Satisfied for measured cancellation checkpoints. Cancellation during active traversal is not independently measured |
| Shared 10,000-entry budget, 1,000 visible-path limit, 64 KiB output, five-second deadline, bounded ignore input and matcher work | `TestRecursiveTraversalStopsAtSharedEntryBudget`, bounded encoder test, broker filter budgets, options-frame boundary validation and existing admission/session deadlines | `recursive_listing_entry_limit` creates a 10,020-file tree and requires a bounded `incomplete=true` provider result | Satisfied; options too large for the worker frame are rejected before approval |
| Provider-visible names/counts/errors and stale-result suppression | Runtime continuation tests; host filter removes protected entries before counts and strips all worker snapshots before serialization | Recursive provider fixture cases assert disclosed results, approval details, and absence of filtered names | Satisfied for the tested profile |
| Supported-profile enablement and route/schema composition | Linux amd64 gate is derived from trusted launcher inspection, selected `ro-bind-fd` mode, and successful fixed-worker composition; public `sandbox.Service` forwards it; runtime schema/instructions follow the exposed definition | Accepted Stage 6 report records actual `ro-bind-fd` mode; PR #330 added architecture and automatic-composition regressions | Satisfied for trusted Linux amd64 plus `ro-bind-fd`; other architectures and file-snapshot remain unavailable |

## Evidence provenance and host result

The staged attempts below preserve their original provenance. They culminate
in the accepted fifth run on `eb56eef`; later PR #330 enables only the profile
actually measured by that run. Prior nonrecursive reports do not prove
recursive enforcement.

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

A third trusted-host run exercised the exact clean source revision
`430d9c860c1f4a2c520025c60789c559815fa13c` (Go 1.26.11, Linux
`7.0.0-42-generic`, amd64, UID 1000, trusted root-owned Bubblewrap 0.9.0,
automatic bind mode). The executed #317 native-integrated listing cases
passed, including nested ignore/glob filtering, the 10,020-entry bounded
listing, descendant and selected-directory movement, mount boundary, owner
rotation, post-traversal cancellation, and worker cleanup. The two cases
previously inconclusive because of the fixture's envelope parsing passed their
expected names/counts assertions. Review then found that the protected
hard-link alias did not match the request glob and that the missing-owner
fixture sent a nonrecursive request; those findings were fixed in the next
revision.

A fourth trusted-host run exercised exact clean revision
`df570c0e1549fd5faee196c63a5450c40098610d` with the same Go, kernel,
architecture and Bubblewrap identity. The corrected `positive_recursive_directory_listing`
case passed: its protected hard-link alias matched the approved `*.go` glob,
the unprotected `nested/child.go` control remained visible, and the exact
provider-visible names/counts matched. The study then failed in the synthetic
`recursive_listing_incomplete_owner_inventory` case because its schema
assertion incorrectly expected `list_files` to be the provider's only tool;
the runtime also advertised its other bundled tools. The advanced schema was
present, but the test stopped before confirming the refusal assertions. The
assertion now selects the `list_files` definition specifically and checks the
recursive call carried in the provider continuation. This correction needs a
new trusted-host run.

The fourth archive SHA-256 is
`7e160a531089bdbd159e5c3f8b6c244d32c6d6b84fa2170db62a2dea1977b0bd`; the
report SHA-256 is
`7348a7543419608549dd128c5db697201bb405842078bc0684f0adc75219bd73`.
The fourth report measured 29 cases as passes, one as inconclusive, and two
out-of-profile cases as unavailable. Its failure was limited to the missing-
owner fixture assertion, subsequently corrected in `eb56eef`.

A fifth trusted-host run exercised exact clean revision
`eb56eef57c90b3c5a653b99e1d3a3dcec451cfc5` with Go 1.26.11 on Linux
`7.0.0-42-generic`, amd64, UID 1000, trusted root-owned Bubblewrap 0.9.0, and
automatic bind mode. The study completed with exit code 0. All #317 native
recursive-listing cases passed, including the protected alias with its matching
`*.go` glob and unprotected control, and recursive missing-owner refusal. The
report measured 30 cases as passes; two unrelated mutation and shell profile
cases were unavailable.

The fifth archive SHA-256 is
`7d29fa5c715c7353b99989381728fe34ced91208f55afae0d10295d764d83ba9`; the
report SHA-256 is
`f29d5ec5009383e2b71180fd31c49da9a25d9115fe433d996d4c67a8b0a9102b`.

The `stage6_profile_incomplete` classification describes the broader harness
profile: mutation and shell cases are outside the implemented Stage 6 profile,
and the study does not establish provider-source lifecycle, aggregate cgroup
quotas, shipped-binary integration, live-provider listing, or non-Linux
enforcement. Active-traversal cancellation also remains unmeasured. The
recursive missing-owner result is synthetic runtime-composition evidence, not
native worker enforcement. These limitations remain explicit and do not
invalidate the #317 listing cases accepted in the reviewed scope. PR #330
enables only the measured Linux amd64 `ro-bind-fd` profile.

To reproduce the trusted-host run from a clean committed candidate:

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

The accepted automatic selection resolved to `ro-bind-fd` on Linux amd64 and
passed the #317 recursive-listing cases on exact clean source revision
`eb56eef57c90b3c5a653b99e1d3a3dcec451cfc5`. PR #330 merged as
`7938bbf998fd2a1a7db503417f7201ef5e32f1e0` and restricts eligibility to that
measured architecture and bind mode. The launcher constructor enforces
backend trust and required capabilities; the service exposes options only
after fixed-worker composition succeeds. Exact-file reads retain their
stricter operation-specific seccomp profile; only directory workers receive
the listing profile. The native run used the Stage 6 test worker image, not
`cmd/harness` or a live provider.

The archive
`komposyn-issue215-stage6-20261116T054344Z-1406697.tar.gz` was inspected from
`/tmp/komposyn-issue215-stage6-20261116T054344Z-1406697.tar.gz`; its SHA-256 is
`7d29fa5c715c7353b99989381728fe34ced91208f55afae0d10295d764d83ba9`. Every
entry in its `SHA256SUMS` verified. The report SHA-256 is
`f29d5ec5009383e2b71180fd31c49da9a25d9115fe433d996d4c67a8b0a9102b`. It
records 30 passing cases and two unavailable out-of-profile cases
(`mutation_profile`, `shell_profile`). The archive does not establish the
broader limitations listed above.

Issue #317's accepted discovery scope is complete for this supported profile.
Issue #315 remains open. Search (#318), patch-tool enablement (#321/#322),
and the canonical mutation gate (#267) remain open. Creation (#319) and editing
(#320) research closed with scoped negative dispositions; this does not satisfy
either operation's positive-evidence or separate enablement gate.
