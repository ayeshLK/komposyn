# Issue #215 read milestone closure and Stage 6 acceptance contract

Status (2026-11-10): [issue #215 is closed](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6771210816)
within its reviewed read-focused scope. Conditional `read_file` integration
merged in PR #264; PR #265 supplied the source-pinned shipped-worker runner.
The tested read source tree is merged commit
`91efabdaeb43bda7c26324b22601cfc74352cbed`. This documentation update follows
setup merge `a3774cf`; it adds no new native measurement.

Mutation work moved to [issue #267](https://github.com/ayeshLK/komposyn/issues/267).
Its rename design packet is a separate proposal;
mutation and shell remain unavailable. The historical acceptance contract
below is retained to explain the evidence, rather than to reopen #215.

## Reviewed evidence and limits

| Evidence | Source and scope |
| --- | --- |
| Shipped-worker sealed snapshot | [Reviewed report](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6770069844); tested PR #265 head and merged read baseline have equal Git trees. Twelve native-integrated cases and six synthetic controls passed. The command image uses the sealed snapshot mode; its parent-death case remains unavailable. Archive SHA-256: `8ecf66f126f194f34a871b301a8705ac068c852e42eacb882c6a5a0281321f3d`. |
| Study-worker descriptor mode | [Merged-master report](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6771185536); exact clean `91efabd`, Go 1.26.10, Linux amd64, kernel 7.0.0-42-generic, trusted Bubblewrap 0.9.0. Twelve native-integrated cases, one fixed-profile parent-death case, and six synthetic controls passed. Archive SHA-256: `a66819dfb913658eab5026751e496d125bbe53451950f3c10ff1673b84835d09`. |
| Closure disposition | [Maintainer-directed closure](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6771210816) accepts the bounded read milestone and splits mutation to #267. Both report checksum sets were reviewed. |

These are linked reviewed reports, not archives re-executed or independently
revalidated by this documentation change. The aggregate report classification
remains `stage6_profile_incomplete` where unavailable cases remain. Issue closure
does not turn unavailable cases into measured passes.

The actor moved the root/parent and replaced the target pathname; the read
returned the retained object's bytes. That observation violates continuous
live-path containment A in the tested race and is consistent with opened-object
authority B. It proves neither A nor universal B. The command-image snapshot
and study-image descriptor results are different profiles, not a complete
cross-product of images and launch modes.

## Current capability and follow-up disposition

| Area | Disposition |
| --- | --- |
| Stages 1–5 and typed-read enablement | Merged (#246–#251, #264); explicit root, exact once approval, retained admission, private capture, synchronized disclosure and continuation retirement remain required. |
| Stage 6 read evidence | Read-focused gate closed on the recorded Linux amd64/Bubblewrap 0.9.0 environment. See profiles above and the [runner guide](issue-215-stage6-runner.md). |
| Launch policy | Trusted absolute `/usr/bin/bwrap`, feature-selected descriptor or sealed-snapshot construction, five-second normal worker watchdog, 64 KiB result cap, 14 MiB image cap and 16 MiB tmpfs. The reviewed shipped-image snapshot run covers the larger image/profile. |
| Protected owner coverage | Auth/settings identity, alias and rotation protections remain mandatory. Missing/incomplete required coverage refuses admission. Provider-source lifecycle integration remains unsupported; paths or collector roots do not supply an exhaustive owner inventory. |
| Additional acceptance | Actual command-image descriptor/parent-death coverage, arbitrary descendants, aggregate quotas/exhaustion, other kernels/filesystems/platforms and real-provider/interactive-UI validation remain unclaimed. arm64 compilation is not native evidence. |
| Mutation | #267 owns operation-specific effect binding, protected-owner proof, independent-actor native evidence and separate enablement review. No privileged BPF prototype is authorized by this document. |
| Shell | Unavailable by maintainer direction; requires a separate reviewed design and enablement decision. |
| Token Logger / SQLite | Deferred until a composition actually owns that Store. Do not introduce a Store or SQLite hook merely to complete an old checklist. |

Historical #243 service-fixture evidence stays limited to its source and private
sink. Earlier backend ownership refusals remain unavailable evidence for those
attempts; they do not invalidate later trusted-host runs or prove host-wide
unavailability. No trust bypass or elevated retry is permitted.

## Preserved Stage 6 composition and acceptance contract

The runner is internal research infrastructure in the existing Go module. Its
only provider is a capturing fake; its owners and target files are disposable
synthetic fixtures. It sends no live inference and uses no real credential.

```text
synthetic owner registrations
          │
          v
actual sandbox Service / registry ──> runtime approval + once consumption
          │                                      │
          └── protected-object policy             v
                                      retained exact-object admission
                                                   │
                                                   v
                                      fixed Bubblewrap worker
                                                   │
                                      bounded private capture after join
                                                   │
                                                   v
                          synchronized runtime publication / fake provider
                                                   │
                                                   v
                                  in-memory test sink and assertions
```

The separately compiled test binary uses a Go overlay to register the
dedicated `read_file` path in a test-only runtime composition; the overlay is
absent from the shipped executable. A build regression test compiles this
overlay. It must not use a fixture authorizer
in place of the runtime permission coordinator or a callback that stands in
for the host publication gate. Compose the runtime with the real
`sandbox.Service` APIs, substituting only explicit synthetic owner
registrations, a deterministic approval interaction, fake provider and fixture
sink. Capture bytes must be verified in the fake request while the first
request is verified not to contain them. A tool execution must occur once;
later context replay must not reopen or reread the target.

### Required native cases

Each row records actual worker launch/read/join, once consumption, bytes
captured/disclosed (length and synthetic digest only), provider requests,
fixture object effects, invalidation generation, and cleanup outcome. Use
deterministic handshakes, never timing-only sleeps for ordering races.

| Case | Expected evidence |
| --- | --- |
| No-root controls without a backend | Text request remains usable. A provider-requested read is refused before approval, worker creation, or continuation disclosure. These are synthetic runtime/composition controls, not native enforcement evidence. |
| Positive exact file, explicit once approval | One admission and one worker read; expected bytes reach only the capturing provider after the read; joined worker and retired descriptors before capture/publication. |
| Empty regular file and bounded partial/error behavior | Empty result is valid; over-limit, malformed, or interrupted reads preserve typed outcome and do not trigger automatic replay. |
| Auth/settings registered target, alias and replacement | Refusal precedes once consumption, worker start, transfer, read and provider disclosure; inspect fixture bytes/identity after each operation. |
| Synthetic provider-source registration | Exact source may proceed only after explicit per-invocation source consent; mutation is unavailable. Unregistered/unsupported source ownership refuses. |
| Owner inventory missing/incomplete and recovery | Missing route/identity/coverage refuses. A later authoritative registration permits only a fresh admission; recovery is tested separately. |
| Root/parent/target substitution with independent actor | Actor reports READY and deterministic transition; result is consistent with approved opened-object authority B or refusal. Never claim continuous live-path containment A or immutable unknown bytes. |
| Expected G0/G1/G2 and sibling A/B approvals | Exact checked once-grant transitions correlate to the invocation; legitimate sibling transitions do not self-invalidate; unrelated revisions/duplicate calls refuse without replay. |
| Capture A while B awaits approval, then revoke/clear | Entire undisclosed batch is withheld from provider requests and runtime-visible history/snapshots; completed effects remain truthfully reported and are not replayed. |
| Invalidate before dispatcher acceptance; reverse ordering | Before acceptance: no disclosure. If acceptance wins first: truthful commitment, best-effort cancellation, no recall claim; later context is retired. |
| Owner rotation after admission close/capture and provider commitment | Actual Service owner gate orders the mutation with handoff; stale generations cannot publish, replay or restore history. After prior handoff, no further request receives retired context. |
| Later turn, late response/delta and credential-resolution pause | Valid same-context replay includes committed content without a second read. Rejected/pending gate or invalidation after snapshot blocks raw, derived and native stale data from every provider request and UI delta. |
| Cancellation, budgets, redirect, shutdown, cleanup retry | Correlated payload-free outcomes preserve call/result pairing; all workers/FDs are retired or cleanup remains explicitly uncertain and blocks new dispatch. No automatic retry after uncertain effects. |
| Actual offered launch profile | Record effective backend mode, trust, namespaces, network/IPC, mounts, FD/environment inventory, probes, parent death, output/deadline and cleanup. Untested properties remain unproven. |

The worker must be the exact fixed read worker with the current trusted absolute
Bubblewrap path and existing fail-closed integrity checks. The run records the
source commit, dirty state, runner/overlay hashes, backend path/owner/mode/hash,
version/features, selected bind mode, host architecture/kernel and relevant
namespace availability. An [earlier exact-head issue report](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6720531454)
recorded a trusted-backend refusal before probes or worker launch; the completed trusted-host runs above supersede it as the current evidence summary. This does not
establish host-wide Bubblewrap unavailability. Per-run metadata and sanitized
results belong in the issue evidence, not duplicated in this tracking contract.
A probe failure or blocked outer agent launcher must retain its phase and
diagnostic; do not elevate, bypass trust, retry unrestricted, or treat the
environment as universally incapable.

### Result and archive contract

The archive manifest has a schema version and provenance for source, runner,
overlay, backend, launch mode, host metadata and timestamps. Each case carries:

- `case_id`, `status` (`measured_pass`, `observed_violation`, `unavailable`,
  `inconclusive`), and a short sanitized reason;
- `evidence_kind`, distinguishing native-integrated results from synthetic
  runtime/composition controls;
- expected assertions and separately observed assertions;
- whether worker start/read/join, descriptor transfer/retirement, once
  consumption, provider acceptance and output publication happened;
- operation result (`succeeded`, `refused`, `not_executed`, `failed`,
  `partial`, `unknown`, `cancelled`), read count, captured/disclosed byte
  lengths and fixture digest comparisons;
- provider request count and stale bytes found/not found; owner/root/policy
  generations; cleanup certainty and bounded duration.

No raw content, credential, host-private path, prompt/response, process command
line or unrestricted host log is archived. Keep only the reviewed sanitized
JSON report, concise Markdown summary, runner/script provenance and checksums;
the full temporary archive stays outside commits and is linked or attached to
the issue after review. Overall exit status zero is not an acceptance result.
`unavailable`, `inconclusive`, setup refusal, missing assertion, and skipped
cases never count as passes.

## Historical completion sequence and decisions

```text
A tracking/specification ──> B integrated runner ──> C trusted native run/fixes
                                                   │
D owner applicability/evidence ───────────────────┤
E launch/platform/resource audit ──────────────────┤
                                                   v
                                  F separate typed-read enablement review

G mutation design ──> reviewed mutation implementation ──> native gate
H shell design ─────> reviewed fixed shell profile ───────> native gate
all applicable work ──> I final checklist audit/issue closure
```

Package A updates tracking and acceptance only. Package B does not enable tools.
Package C cannot be replaced by CI, unit tests, cross-compilation, or the
historical #243 fixture. D/E may prepare source-grounded recommendations in
parallel but do not widen capabilities. F/G/H need their own explicit reviews.
The historical Package I gate required every original checklist row to be either
completed with applicable evidence, explicitly refused within accepted scope,
or tracked by an approved follow-up with reason and acceptance criteria.

The supported target remains Linux-first. Do not claim macOS/Windows
enforcement from compile checks. Do not require or claim aggregate cgroup
quotas without a reviewed and measured provisioning decision. Write
encompasses delete only after its dedicated profile proves that behavior;
shell remains separate from write and network authority. Production filesystem
mutation and shell remain unavailable until their separate gates pass.
