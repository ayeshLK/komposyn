# Composition admission and non-exposing sandbox service

**Status:** Stage 3 retained-admission infrastructure only. The memory-only
chat composition injects the trusted host admission service, but does **not**
register or enable `read_file`. The internal admission path is reachable only
through the reserved runtime lifecycle and test seam; there is no worker start,
descriptor transfer, content read, result capture, or provider handoff. Generic
read dispatch remains unavailable.

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
        -> /fs-root selects or clears an in-memory lookup root; no authority
        -> optional retained-admission host is injected, but the tool is absent
           from the registry and ordinary dispatch remains refused
```

The host service composes the existing Linux `Registry`, broker, Bubblewrap
launcher and `HostCoordinator`. Its host authorizer remains deny-only. The
Stage 3 admission API can retain one exact regular-file object only after the
explicit root, required auth/settings owner registrations, backend, and
coverage checks succeed. This is an internal pre-approval operation, not a
general authorization or dispatch API. No model, tool or UI data can construct
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
inside the shared runtime/host authority commit gate. It immediately retires
the synthetic read authority without dispatching anything. The host service
does not consume grants. A preparation refusal consumes no once grant. This
bridge is exercised by trusted package tests; ordinary chat dispatch cannot
call it because `read_file` is not registered. Existing `update_plan` and
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
- This environment is Linux/amd64, Go 1.26.10, UID 1000. Its `/usr/bin/bwrap`
  is owned by UID 65534, so the required root-owner trust check rejects it
  before execution. Native worker/service positive and negative cases were not
  run. No elevated or unrestricted retry was attempted.
- Existing `read-broker-study` fixture and native evidence remains separate;
  it does not become evidence for this composed chat service. Do not carry
  forward the historical `6gGbUF` pass count.

Filesystem read execution remains disabled even on a host with a trusted
Bubblewrap backend. No target descriptor reaches the fixed worker through the
shipped chat. Provider-source owner integration remains unsupported and is
refused; Token Logger owner hooks, listing, recursion, writes, deletion, shell,
and non-Linux enforcement remain absent. This slice is not native worker
acceptance evidence. Stage 4 must implement startup/claim/release/read/capture,
and Stage 5 must separately review result publication/provider continuation.
Neither stage may reuse the synthetic authority retired here as execution
authority.

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
