# Reviewed native acceptance of the #243 composition

**Evidence status:** historical, reviewed native result for the merged #243
service. This records the exact scope and provenance of that run; it is not
evidence for a later revision or an enabled filesystem tool. The shipped
generic-read gate remains closed.

The reproducible runner and original test-only overlay are in
[`scripts/native-acceptance/`](../../scripts/native-acceptance/README.md). The
sanitized, machine-readable case summary is
[`evidence/read-broker-composition-native-acceptance.json`](evidence/read-broker-composition-native-acceptance.json).
The original result archive remains outside the repository; its digest is
recorded below. No raw archive, binary, temporary fixture, credential value,
or host path is committed.

## Provenance

| Item | Recorded value |
| --- | --- |
| Production source | `d43a7dd7005ae6ccc8102936d00bd2920bbd9fcf` (merged #243) |
| Native test harness revision | 2, test-only overlay |
| Overlay SHA-256 | `3861d527558ce69722e9901a59fe0886a08faa67ed5e33026bc3cf2ac96dde11` |
| Runner SHA-256 | `fb844f048a23a1ad01c3fa36c972bdf645665ab0dc32d519a2e00b1b382acb63` |
| Capture helper SHA-256 | `fade10f56a5b762489375c8c58e7f033ea50c1b37c7cc57dde81938b2560b7da` |
| Native test binary SHA-256 | `c25793178e7d95ad5129977d7f5ad4976a684c07c64e1844f6cba56b3455df11` |
| Original results archive SHA-256 | `4bb30f9ab696b9f039ca94cda57d1a0b1558a6ae61c51198e16eec9edb9c6876` |
| Environment | Linux/amd64, Go 1.26.10, kernel `7.0.0-42-generic`, regular UID 1000 |
| Bubblewrap | trusted root-owned 0.9.0, mode 0755, `ro-bind-fd`; SHA-256 `e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71` |

The archive's internal file checksums, captured helper/overlay hashes, and
source archive provenance were verified against the values above. The source
archive represents the exact committed source. The overlay is separately
identified test code; the run is not described as tests already present in
that commit.

## Measured result

All nine cases reported `measured_pass`, with cleanup confirmed. The suite
used the real #243 service registry, auth/settings protectors, coordinator,
Bubblewrap worker and bounded capture helper. A fixture-only test overlay
replaced the deny-only coordinator with explicit once-approved synthetic
fixture authorization. No provider request was sent; “published” means the
coordinator's private result sink.

| Case | Observed result |
| --- | --- |
| Shipped generic-read gate | Refused; the shipped service did not expose a read capability. |
| Positive exact-file read | Returned the expected synthetic bytes after worker readiness, held capability transfer, read, confirmed join, and private publication. Worker probes observed `no_new_privs`, seccomp mode 2, denied path opens/socket creation, cleared environment, and protocol-only initial descriptors. |
| Auth/settings routes and auth hard-link alias | Refused before once consumption or worker launch. |
| Settings rotation while worker held | First update returned cleanup-pending and preserved the destination. After release and confirmed join, a fresh update registered a distinct empty replacement before writing; the retired alias remained refused. |
| Auth rotation while worker held | Same cleanup-pending, unchanged-destination, join-before-write, distinct replacement, and retired-alias observations. |
| Revocation after capture | Read and join were confirmed; captured bytes were suppressed before publication. |
| Root change while worker held | First transition returned cleanup-pending; the old invocation did not read or publish. A fresh invocation succeeded under root generation 2. |
| Held cancellation | Worker joined without read/publication. The result remained unknown; the pass concerns withheld data and cleanup, not a known completed read. |
| Shutdown retry while held | Initial shutdown reported incomplete cleanup; a fresh bounded retry drained after the operation settled, with no read/publication and confirmed join. This was a deterministic coordination checkpoint, not a natural kernel-hang test. |

An earlier run named `ldozh8z1` remains failed/incomplete (6 passed, 3 failed).
Its revision-1 harness incorrectly expected immediate completion while the
coordinator was deliberately held. Revision 2 tests cleanup-pending refusal,
preserved data, confirmed join, and fresh recovery. It did not discard or
ignore the earlier failures, and production source did not change between
those runs.

The legacy `bytes_match_target` field was unpopulated. The positive overlay
independently asserted the exact returned fixture bytes and recorded
`verified_returned_bytes: true`; the legacy false field is not a mismatch.

## Limits and remaining work

This result does not establish generic reads from arbitrary roots, safe
classification of unregistered files, independent-copy detection, provider
source owner integration, Token Logger lifecycle coverage, enabled runtime or
provider publication, sealed-copy mode, all platforms, arbitrary descendant
cleanup, resource limits, or shell/write/delete safety. The service still
refuses generic reads before grant consumption or worker launch. No filesystem
tool is registered.

## Reproduction attempt for this PR

On 2026-11-07 the committed runner rebuilt and ran its checks against the
exact #243 source revision from a regular Linux/amd64 user environment (Go
1.26.10, Python 3.12.3, kernel `7.0.0-42-generic`). Unit checks and static
native-test-binary construction passed. The native test process ran, but
Bubblewrap 0.9.0 failed the existing trusted-backend ownership check because
`/usr/bin/bwrap` was owned by UID 65534 rather than root. Backend probes and
the candidate worker were not started. The runner returned status 2
(`native_unavailable`): one shipped-gate behavior case passed without a
worker; the eight backend-dependent cases were unavailable. There were no
timeouts, truncated output, incomplete capture cleanup, or captured remaining
descendants. This is an environment-specific trust refusal, not a claim that
Bubblewrap is unavailable on the host generally and not a native enforcement
pass. No trust bypass or elevated retry was attempted.

The new attempt's results archive SHA-256 is
`6ff96557f656744577b4b2435970006dd144e11379716cbe9a373f82e4072f18`; the
archive and binary are not committed. Its sanitized outcome is in the JSON
record. Use the runner guide's exact normal-host command to reproduce on a
host where the existing trust policy accepts the installed backend.
