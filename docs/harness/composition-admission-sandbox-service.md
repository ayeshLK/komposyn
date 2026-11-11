# Composition admission and non-exposing sandbox service

**Status:** Stages 3–5 and the Stage 6 runner/lifecycle follow-ups are merged.
Trusted-host Stage 6 evidence is recorded in issue #215 for Linux amd64 with
Bubblewrap 0.9.0; it does not establish other platforms, mutation, shell, or
production-provider behavior. The current focused production composition adds
`read_file` only when the backend inspection and retained-read lifecycle are
available. It uses the existing once-approval, worker, capture, publication,
and replay path. Provider-source reads remain unsupported; mutation and shell
remain unavailable.

## Composition and ownership

```text
cmd/harness starts
  -> exact private worker bootstrap check (before Cobra/UI/auth/provider init)
  -> sandbox.NewHostService (trusted /usr/bin/bwrap inspection; no fallback)
  -> auth.Store and settings.Store each receive the same host protector
  -> each Store.RegisterProtection uses its own EffectivePath
  -> current routes/objects are pinned before Load/ListMetadata/UI exposure
  -> existing text-only + conversation-tool runtime starts
       \
        -> launch directory initializes an in-memory lookup boundary; no authority
       -> read_file is registered only for a complete trusted host capability;
          ordinary tool dispatch cannot execute it
       -> retained worker capture and provider handoff occur only after the
          synchronized publication gate, which takes
           Service root -> owner-update -> Chat -> runtime authorization locks
           and validates policy/coverage generations before handoff
```

The host service composes the existing Linux `Registry`, broker, Bubblewrap
launcher and `HostCoordinator`. Its host authorizer remains deny-only. The
Stage 3 admission API can retain one exact regular-file object only after the
explicit root, required auth/settings owner registrations, backend, and
coverage checks succeed. This is an internal pre-approval operation, not a
general authorization or dispatch API. Stage 5 sensitive handoff compares the
read's admitted policy and inventory revisions to current host state while
holding the service root lock and Linux owner-update gate, before entering Chat
and runtime authority locks. Owner rotation holds the same owner-update gate
from its pending latch through registry rotation, invalidation callbacks and
final coverage update. Handoff fails closed if a rotation is already active; a
later rotation is ordered after accepted handoff. The owner callback globally
retires captures and continuations, including those whose host admission has
already closed. No model, tool or UI data can construct
or complete a coverage profile. The synthetic four-owner inventory remains in
the study command and is not used by `cmd/harness`.

The host profile is fixed by trusted code:

| Owner or obligation | Current state | Evidence and effect |
|---|---|---|
| Harness auth store | Required, then registered | Effective route and existing/replacement identities are registered through the real `auth.Store` hook. |
| Harness settings store | Required, then registered | Independently resolved via `settings.Store.EffectivePath()` and registered through the real settings hook. |
| Token Logger Store | Absent from this composition | `cmd/harness` does not open or undertake a Store. This does not attest that databases in a selected root are safe. No late-owner activation API is exposed in this slice. |
| Provider sources | Unsupported / unknown | This composition has no source identity registration. A requested file is not inferred to be ordinary. |
| Other user-root contents | Unsupported / unknown | No machine scan, suffix/content guess, or root-selection completeness claim is used. |

Coverage revisions and routes are detached snapshots. Auth/settings replacements
register the effective route and the opened temporary object before writing;
the same registered object must match the owner's descriptor. Old identities
remain pinned until service shutdown. When the trusted worker session exists,
owner registration goes through its invalidation/cleanup barrier. If the
Bubblewrap backend is unavailable, no worker session is constructed; the
service still protects route/object registrations and never admits reads.

Auth and settings overrides remain independent. The service is injected into
the single `auth.Store` value used by profile resolution, OAuth login/refresh/
disconnect and UI actions, and into the single `settings.Store` value. Startup
registers both stores before settings load or auth metadata listing. The
existing private-file validation and atomic replacement behavior remains in
the stores.

## Runtime admission ordering

The runtime has a separate, internal retained-read approval bridge. It derives
an exact read request from the host-created admission metadata, obtains the
normal once-only permission decision, validates Stage 2's checked approval
transition, prepares the same retained object, and consumes the once grant
inside the shared runtime/host authority commit gate. Stage 4 retains that
runtime-issued reference through one execution attempt. The trusted service
sends only the retained object's descriptor to the fixed worker after bootstrap
and probes; it does not reopen the path or consume grants. A preparation
refusal consumes no once grant. Ordinary chat dispatch reaches this seam only
through the dedicated `read_file` branch; the generic tool enforcer cannot
invoke filesystem reads. Existing `update_plan` and
`request_user_input` continue through their current enforcer without requiring
filesystem coverage or a Linux backend.

The original caller context and a five-minute trusted maximum admission
lifetime remain attached from lookup through approval and preparation. Cancel or
expiry makes later preparation/commit stale and asynchronously retires the
host-owned object. If a constrained lookup does not acknowledge completion,
the caller returns within its own context budget with cleanup still pending;
the single bounded admission slot remains occupied until the late lookup closes
its result. Cleanup lock acquisition also observes the caller deadline.

If preparation cleanup fails, runtime retains the capability, returns an
unknown result without its content, stops the current turn before another
provider request, and retries cleanup before a later turn can make a provider
request. `Chat.CloseContext` also waits for the active turn within the caller's
deadline and drains retained preparations before the host closes the sandbox
service. Timeout or persistent cleanup failure is returned to the caller while
the runtime keeps the remaining handles for a later shutdown retry. The legacy
`Chat.Close` wrapper is bounded; callers that need to observe incomplete
cleanup use `CloseContext`. Preparation `Close` methods must honor their
context and be safe to retry. The Linux service also latches an owner as pending
before route/replacement registration; a registration error,
post-registration cancellation, or opened-object identity mismatch keeps that
owner pending in the detached coverage snapshot until a fresh registration and
identity check both succeed. This overlay is required because the coordinator
may complete its staged inventory update before the service's final descriptor
check.

The host-owned digest binds service and conversation/turn/call identity,
selected root and generation, exact target, opened object identity and class,
provider destination, and policy/inventory generations. Preparation revalidates
the same open target descriptor and current registry/coverage state; it never
reopens the path. The descriptor remains host-owned and no read method is
exposed. This does not freeze file contents, detect independent sensitive
copies, or promise live-path containment against namespace writers. Runtime
remains the sole once-grant consumer; the service's deny-only authorizer cannot
consume grants.

The lock order for the retained authorization commit is: Chat/runtime read
authority gate → service root gate → Linux service state gate → coordinator
state gate → registry/inventory snapshots → permission-store revision check.
Owner updates serialize through a separate owner-update mutex, latch pending
under the short service-state lock, invalidate runtime authority, then release
broad locks while admissions/lookups drain. Only after the coordinator has
registered and pinned the new route/replacement identity does the service
verify the owner's still-open descriptor and clear its pending latch. No store
callback runs while the coordinator or registry lock is held. Root changes
publish pending state before coordinator drain; stale older notifications are
ignored, and conflicting notifications with the same nonzero generation are
rejected.

## Worker entry and backend behavior

The executable checks exactly `--readbrokerstudy-bootstrap` before normal
command setup. It delegates only to the fixed worker bootstrap, which validates
its exact fd/mode arguments, authenticates the mounted executable and installs
seccomp before readiness. It provides no general command, shell, file, or SQL
interface. The host launcher uses the current executable as the fixed worker
and the absolute `/usr/bin/bwrap` path. Existing ownership/integrity checks,
capability probing, namespaces, no-network policy, cleared worker environment,
descriptor inventory, bounded protocol and no-fallback behavior are retained.
Descriptor-bind and sealed-copy modes remain distinct; detecting a supported
mode does not establish native acceptance for it.

If Bubblewrap is missing, untrusted, incompatible, or blocked by host policy,
the host service reports backend unavailability internally, keeps auth/settings
protection wiring, and the chat remains text-only. It does not alter host
settings, use a project PATH, retry with weaker restrictions, or run a worker
outside isolation. Non-Linux builds report the backend unsupported and retain
text-only behavior.

## Evidence and limits

- Focused runtime, sandbox, and broker-study tests exercise one retained
  lookup, same-object preparation after pathname replacement, checked approval
  and once consumption without dispatch, preparation refusal without
  consumption, pending owner drain, root/owner invalidation, late lookup
  cleanup, and existing text-only/auth/settings composition behavior.
- `cmd/harness --check` was built and run against synthetic auth/settings
  overrides. It reported zero configured profiles and completed successfully.
- The Stage 6 native study was later run on a trusted Linux/amd64 host with
  root-owned Bubblewrap 0.9.0 in both descriptor-bind and sealed-snapshot
  modes. Its exact provenance and per-case evidence are recorded in issue #215.
  This is feasibility evidence for the fixed worker and fake provider; it is
  not real inference, cross-platform evidence, mutation evidence, or shell
  evidence. Do not carry forward the historical `6gGbUF` pass count.

The focused production composition registers `read_file` only when trusted
backend inspection and the full retained-read lifecycle are available. Stage 4
captures bounded bytes in an opaque in-memory handle after worker join and
descriptor retirement; Stage 5 synchronizes result publication and same-context
provider continuation. Provider-source owner integration remains unsupported
and is refused. Token Logger is not opened by this composition. Listing,
recursion, mutation, shell, and non-Linux read enforcement remain unavailable.

## Validation commands

The focused package checks are:

```sh
GOCACHE=/tmp/komposyn-compose-gocache go test ./internal/harness/runtime ./internal/harness/sandbox ./internal/harness/readbrokerstudy ./cmd/harness
GOCACHE=/tmp/komposyn-compose-gocache go build -buildvcs=false -o /tmp/komposyn-compose-check ./cmd/harness
```

`-buildvcs=false` was needed for this isolated worktree because Go's VCS
metadata probe failed in the local environment. It changes build metadata
stamping only; it does not alter sandbox policy. The native run is unavailable
until a normal host terminal provides a trusted root-owned Bubblewrap binary
and the required user/mount/PID/network namespaces.
