# Issue #215 completion tracking and Stage 6 acceptance contract

Status: reviewed against master `f629cc87ce4bd9344299257b51c70877e7c29d03`
(2026-11-10), with Package F merged in PR #264 from reviewed head
`db4d78f7b5a0746c8e0fee75916c01aaad87d50a`. The maintainer authorized the
exact-file read capability and reviewed/merged its implementation. The
Linux-only tool is conditionally registered only after trusted backend
inspection, supported provider-route selection, root selection, and the full
retained-read lifecycle. Issue #215 remains open; mutation and shell gates
remain separate.

The plan was reconstructed from the current issue, its decision/research
comments, the merged implementation plan, source, and existing evidence. It is
not a claim that earlier native evidence covers the Stage 6 composition. The
Stage 5 implementation merged in #251; the Stage 6 runner, independent actor,
and held-worker/profile follow-up merged in #253, #254, and #255. The #255
tested head `f3665a7f4245820d184d92922f75dcc4b664e9a9` has the same source tree
as current master `9e58b466132d269746bcd275926f8a9bbb94d086`. The completion
checklist remains unfinished. Keep #215 open and preserve historical evidence.

## Current stage audit

| Stage | State | Evidence and boundary |
| --- | --- | --- |
| Policy decision | Accepted | The six clauses in [the maintainer decision](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6709361902) govern exact-file disclosure, same-context replay, logical dispatcher commitment, and retirement. |
| 1: explicit root lifecycle | Merged | [PR #246](https://github.com/ayeshLK/komposyn/pull/246). In-memory root selection/status/clear; selecting a root grants no authority. |
| 2: checked authority | Merged | [PR #247](https://github.com/ayeshLK/komposyn/pull/247). Checked grant transitions and runtime-owned once authority; unit evidence only. |
| 3: retained admission | Merged | [PR #248](https://github.com/ayeshLK/komposyn/pull/248). Exact retained-object admission and owner barriers; no reopen or second once consumer. |
| 4: worker and private capture | Merged | [PR #250](https://github.com/ayeshLK/komposyn/pull/250). One-attempt fixed worker, private bounded capture after join/descriptor retirement; native integrated acceptance remains open. |
| 5: publication and continuation | Merged | [PR #251](https://github.com/ayeshLK/komposyn/pull/251), merge `a61a3dfa4843f309bc533061f7be6e767f9b49b2`. Synchronized batch/provider handoff, replay provenance, owner-gated continuation retirement and stale-response tests. |
| 6: integrated native acceptance | Historical study-image profile measured; shipped-image profile prepared | Trusted-host reports on issue #215 exercised descriptor-bind and sealed-snapshot modes with the Stage 6 test image on one Linux amd64/Bubblewrap 0.9.0 environment. Runner v8 adds an optional source-built `cmd/harness` worker in sealed snapshot mode; that profile still needs a trusted-host run. Mutation and shell remain unavailable/not implemented. |
| Typed-read production integration | Merged in PR #264 | `read_file` is registered only with a trusted Linux host, complete read lifecycle, supported provider/auth route, and selected current root. Explicit once approval and Stage 5 publication/replay boundaries remain mandatory. Release-image construction tests pass; a native Bubblewrap launch of the larger image remains unverified. |

`cmd/harness` now composes `read_file` conditionally; this focused integration
does not complete issue #215 or enable filesystem mutation.

### Current launch profile inventory (not native acceptance)

The trusted host composition currently has one Linux worker profile and two
feature-selected Bubblewrap executable-construction modes:

| Profile part | Implemented behavior | Evidence boundary |
| --- | --- | --- |
| Platform | Linux only. The seccomp audit architecture is implemented for amd64 and arm64; other Linux architectures fail closed. Non-Linux builds retain text-only behavior. | Stage 6 native worker runs are recorded for Linux amd64 on the tested host. arm64 support is compile-only; no native enforcement claim is made for it. |
| Backend selection/trust | Trusted composition defaults to absolute `/usr/bin/bwrap`; it performs no PATH lookup or fallback. Before executing probes, every path component and the binary must pass the root-owned, non-group/world-writable trust check. | Current environment fails this precondition because the binary owner is UID/GID 65534; capability probes and worker startup therefore did not run. |
| Executable construction | Feature detection prefers `--ro-bind-fd`; otherwise it requires both `--perms` and `--ro-bind-data` and uses a bounded sealed executable snapshot. Version strings alone do not select the mode. The current code accepts images up to 14 MiB. | Both modes have integrated Stage 6 native evidence for the earlier study image on one trusted host. Runner v8 can launch the exact source-built `cmd/harness` worker with sealed snapshot mode; that larger-image launch has not yet been measured. |
| Namespace/mount policy | Requests user, mount (Bubblewrap-created), PID, IPC, UTS, and network namespaces; disables nested user namespaces, drops capabilities, clears worker environment, uses a 16 MiB empty tmpfs root, and mounts no host `/proc`, `/sys`, `/dev`, home, sockets, or libraries. | The earlier 8 MiB profile has trusted-host evidence. The changed 16 MiB root profile has not been exercised natively; the current environment refuses the installed backend at ownership validation. |
| Worker confinement | Fixed static Go worker; seccomp is installed with TSYNC and `no_new_privs` before readiness. It allows only the reviewed runtime thread-clone flags and denies process-like clone/exec, path opens, socket creation/use, mount and process-inspection operations. Post-bootstrap worker descriptor checks require only the control socket beyond stdio. | Real-filter Go runtime/GC tests and in-worker probes exist. These do not establish that Bubblewrap started or that host namespace policy was effective in this environment. |
| Host policy inventory | Registry bounds are 256 pinned objects and 256 policy locations; exhaustion refuses registration/admission rather than evicting protection. | Unit/fixture coverage exists. These bounds do not cap host memory used by all other chat or application state. |
| Per-worker bounds | Five-second normal worker watchdog, 64 KiB result cap, 14 MiB executable/snapshot cap, 16 MiB tmpfs size, bounded protocol/stderr, and bounded direct-child cleanup. | These are per-worker bounds, not aggregate CPU, memory, or process quotas. The changed image/root limits lack production-image native acceptance. Namespace-wide descendant cleanup is unverified. No cgroup quota is provisioned or claimed; exhaustion safety is untested. |

The existing Stage 6 reports record successful runs of the earlier study
worker profile; they do not establish native behavior of the larger production
`cmd/harness` image or the changed 16 MiB root. Runner v8 adds a reproducible
`--include-shipped-worker` profile for that image, but no trusted-host result is
recorded yet. The current agent environment still refuses its installed backend
at ownership validation, before probes or worker start. Do not promote
production-image execution based on image-construction tests, cross-compilation,
or Bubblewrap version alone.

## Outstanding issue checklist mapped to work packages

Use the package labels from the completion roadmap. A checked row below means
the deliverable is specifically tracked; it is not a claim that it is already
complete. A missing or untrusted native environment is `unavailable`, never a
pass.

| Outstanding #215 item | Package / dependency | Required result and closure evidence |
| --- | --- | --- |
| Stage 6 exact integrated profile | **B → C** | Runner, independent actor, and held-worker/profile follow-up are merged (#253–#255). Trusted-host descriptor and snapshot runs cover the study image. Runner v8 adds the `cmd/harness` worker-image profile; its trusted-host run is the next acceptance action for the larger image and current 14/16 MiB profile. |
| Complete accepted exact-file boundary through provider integration | **B → C → F** | PR #264 is merged and implements conditional production registration, approval, provider handoff, and replay boundary. Hosted tests and maintainer review pass; use runner v8's `--include-shipped-worker` on a trusted Linux host for native acceptance of the larger image. |
| Provider-source owner lifecycle and real-owner coverage | **D**, then C rerun if the profile changes | The memory-chat composition has no provider-source open/identity/rotation lifecycle; provider-source ownership remains unsupported and refused. Collector roots or discovered paths are not an exhaustive object inventory. Keep this unavailable unless a future composition selects a concrete source owner and supplies its route, opened identity/generation, and invalidation lifecycle; then prove negative admission and rotation with applicable synthetic and native evidence. |
| Negative inventory-admission native scenarios | **C/D** | Missing/incomplete registrations must refuse before once consumption, worker startup, read descriptor transfer, and provider publication. Coordinator-only tests are insufficient for a native claim. |
| Running-worker revocation, stale-result suppression and continuation retirement in complete composition | **B → C** | Exercise runtime once consumption, actual Service owner invalidation, fixed worker, fake provider, and history/delta sink together. Assert both before- and after-handoff orderings. |
| Remaining launch modes, platform, namespaces, packaging and resource limits | **E** | The source-based profile inventory is above. The README identifies fixed `/usr/bin/bwrap`, no installer provisioning, fail-closed behavior, and no trust bypass. Runner v8's command-image profile uses sealed snapshot only; descriptor mode for the larger image, parent-death/descendant cleanup of that image, effective namespace/FD/environment/IPC/network behavior, non-Linux enforcement, and aggregate quotas remain unverified or deferred. The current trust refusal ran no probe or worker. |
| Typed-read enablement | **F** | The maintainer authorized exact-file read and merged PR #264. Keep the tool's host/route/root gates and exact once-consent contract intact. Native Bubblewrap launch of the actual release-sized image remains unverified. |
| Write/rename/delete implementation and native acceptance | **G** | Accepted target: scoped write includes read/create/modify/rename/delete once proven; execute and network remain separate. The maintainer selected same-root regular-file rename, including between directories under that root, for the first proposed mutation slice; directory moves and cross-mount renames are excluded. Both root-relative paths and absolute host paths contained by the selected root are in scope ([path-form decision](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6721229688)). This selects interface scope only; exact traversal handling, bounds, and the effect mechanism remain to be decided. The BPF LSM operation-hook proposal is unproven and requires a separate decision before privileged prototyping; no such implementation or host loading is authorized. The independent-actor counterexample therefore keeps rename/delete unavailable until a reviewed mechanism protects object identity and registered protected objects under races. Proposed bounds in the [mutation decision packet](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6718986635) are not yet approved. Read evidence cannot prove mutations. |
| Shell profile and native acceptance | **H: unavailable by maintainer direction** | Shell remains unregistered and unavailable. Do not start shell implementation or imply process/descendant, quota, or native safety from typed-worker evidence. Reopen only through a separate reviewed design and enablement decision. |
| Token Logger / SQLite lifecycle and hook work | **Deferred until a composition opens or undertakes that Store** | Do not add a Store or SQLite hook solely to close this item. If composed later, follow the owner-aware hook contract and reopen the applicable D/C evidence gates. |

Historical #243 acceptance remains valid only for its service fixture and
private sink. It is neither Stage 6 evidence nor evidence for tool enablement.
The previous Stage 4 Bubblewrap trust refusal remains unavailable evidence.

## Stage 6 composition and acceptance

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
namespace availability. The [latest exact-head issue report](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6720531454)
records a trusted-backend refusal before probes or worker launch. This does not
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

## Completion sequence and decisions

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
Package I closes #215 only after every original checklist row is either
completed with applicable evidence, explicitly refused within accepted scope,
or tracked by an approved follow-up with reason and acceptance criteria.

The supported target remains Linux-first. Do not claim macOS/Windows
enforcement from compile checks. Do not require or claim aggregate cgroup
quotas without a reviewed and measured provisioning decision. Write
encompasses delete only after its dedicated profile proves that behavior;
shell remains separate from write and network authority. Production filesystem
mutation and shell remain unavailable until their separate gates pass.
