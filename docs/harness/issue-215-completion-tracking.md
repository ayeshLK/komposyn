# Issue #215 completion tracking and Stage 6 acceptance contract

Status: current source audit at `a61a3dfa4843f309bc533061f7be6e767f9b49b2`
(2026-11-08). This is a tracking and evidence contract, not an enablement
decision. The six-clause exact-file disclosure policy is accepted, while
`read_file` remains unregistered and unavailable in shipped chat.

The plan was reconstructed from the current issue, its decision/research
comments, the merged implementation plan, source, and existing evidence. It is
not a claim that earlier native evidence covers the Stage 6 composition. PR
#251 merged at the source revision above, but GitHub currently reports issue
#215 closed while its completion checklist is unfinished. Reopen the issue and
use this mapping as the current audit; do not erase historical records.

## Current stage audit

| Stage | State | Evidence and boundary |
| --- | --- | --- |
| Policy decision | Accepted | The six clauses in [the maintainer decision](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6709361902) govern exact-file disclosure, same-context replay, logical dispatcher commitment, and retirement. This does not enable a tool. |
| 1: explicit root lifecycle | Merged | [PR #246](https://github.com/ayeshLK/komposyn/pull/246). In-memory root selection/status/clear; selecting a root grants no authority. |
| 2: checked authority | Merged | [PR #247](https://github.com/ayeshLK/komposyn/pull/247). Checked grant transitions and runtime-owned once authority; unit evidence only. |
| 3: retained admission | Merged | [PR #248](https://github.com/ayeshLK/komposyn/pull/248). Exact retained-object admission and owner barriers; no reopen or second once consumer. |
| 4: worker and private capture | Merged | [PR #250](https://github.com/ayeshLK/komposyn/pull/250). One-attempt fixed worker, private bounded capture after join/descriptor retirement; native integrated acceptance remains open. |
| 5: publication and continuation | Merged | [PR #251](https://github.com/ayeshLK/komposyn/pull/251), merge `a61a3dfa4843f309bc533061f7be6e767f9b49b2`. Synchronized batch/provider handoff, replay provenance, owner-gated continuation retirement and stale-response tests. |
| 6: integrated native acceptance | Not started | Requires one reviewed runner to exercise actual runtime, Service/registry, fixed worker, and a capturing fake provider under the exact candidate profile. No historical service-only run substitutes. |
| Typed-read enablement | Not approved | Requires Stage 6 acceptance, owner/launch gates, and a distinct reviewed enablement decision. |

Production `cmd/harness` still offers only `update_plan` and
`request_user_input`. The test-only read composition is not a provider route,
production enforcer, or tool registration.

## Outstanding issue checklist mapped to work packages

Use the package labels from the completion roadmap. A checked row below means
the deliverable is specifically tracked; it is not a claim that it is already
complete. A missing or untrusted native environment is `unavailable`, never a
pass.

| Outstanding #215 item | Package / dependency | Required result and closure evidence |
| --- | --- | --- |
| Stage 6 exact integrated profile | **B → C** | Build the runner, review it, execute it as a regular user on an eligible trusted Linux host, inspect fixture effects and archive, fix defects, then repeat affected cases and the full final profile. |
| Complete accepted exact-file boundary through provider integration | **B → C → F** | Prove capture-to-provider behavior and native boundaries; then a separate production adapter/registration PR and explicit enablement review. Until F, reads stay unavailable. |
| Provider-source owner lifecycle and real-owner coverage | **D**, then C rerun if the profile changes | Establish the actual participating owner contract and route applicability. Unsupported routes remain refused; prove negative admission and rotation with synthetic sources before claiming coverage. |
| Negative inventory-admission native scenarios | **C/D** | Missing/incomplete registrations must refuse before once consumption, worker startup, read descriptor transfer, and provider publication. Coordinator-only tests are insufficient for a native claim. |
| Running-worker revocation, stale-result suppression and continuation retirement in complete composition | **B → C** | Exercise runtime once consumption, actual Service owner invalidation, fixed worker, fake provider, and history/delta sink together. Assert both before- and after-handoff orderings. |
| Remaining launch modes, platform, namespaces, packaging and resource limits | **E** | Inventory every offered mode and platform. Keep any unproven mode unavailable or remove it from the supported claim by review. Record effective namespace/FD/environment/IPC/network policy, parent death and cleanup. Aggregate quotas are not implied by per-worker bounds. |
| Typed-read enablement | **F** | Separate review after C/D/E applicable gates. Review target-specific consent, exact routes, UI wording, complete tool-call/result pairing and later-turn retirement in the shipped composition. |
| Write/rename/delete implementation and native acceptance | **G** | First review operation semantics and protected-object behavior, then implement dedicated workers in small slices. Accepted target: scoped write includes read/create/modify/rename/delete once proven; execute and network remain separate. Read evidence cannot prove mutations. |
| Shell profile and native acceptance | **H** | Separate read-only shell design, then fixed profile implementation, descendant supervision and native tests. A write grant is not shell permission; typed-worker evidence is not shell evidence. |
| Token Logger / SQLite lifecycle and hook work | **Deferred until a composition opens or undertakes that Store** | Do not add a Store or SQLite hook solely to close this item. If composed later, follow the owner-aware hook contract and reopen the applicable D/C evidence gates. |

Historical #243 acceptance remains valid only for its service fixture and
private sink. It is neither Stage 6 evidence nor evidence for tool enablement.
The previous Stage 4 Bubblewrap trust refusal remains unavailable evidence.

## Stage 6 composition and acceptance

The runner is internal research infrastructure in the existing Go module. Its
only provider is a capturing fake; its owners and target files are disposable
synthetic fixtures. It sends no live inference, uses no real credential, and
does not register filesystem tools in `cmd/harness`.

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

The runner may use the unexported `readPublicationTestMode` switch solely to
exercise the reserved read path in a test-only runtime composition; this switch
must never reach the shipped executable. It must not use a fixture authorizer
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
| Ordinary chat without root/backend | Text request remains usable; no worker/read capability is created. Backend failure is a sanitized refusal for filesystem work, not a text-chat failure. |
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
namespace availability. A probe failure or blocked outer agent launcher must
retain its phase and diagnostic; do not elevate, bypass trust, retry
unrestricted, or treat the environment as universally incapable.

### Result and archive contract

The archive manifest has a schema version and provenance for source, runner,
overlay, backend, launch mode, host metadata and timestamps. Each case carries:

- `case_id`, `status` (`measured_pass`, `observed_violation`, `unavailable`,
  `inconclusive`), and a short sanitized reason;
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
shell remains separate from write and network authority. Production
filesystem/shell tools remain unavailable until their separate gates pass.
