# Composition admission and non-exposing sandbox service

**Status:** implementation infrastructure only. This change wires the
memory-only chat's auth/settings protection lifecycle and a pre-consumption
runtime admission seam to the fixed Linux worker coordinator. It does **not**
register or enable filesystem tools. Generic reads remain unconditionally
unavailable while unknown data in a selected root lacks a reviewed protection
boundary.

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
        -> read_file pre-consumption guard exists but is not registered
           and always refuses unknown-root data
```

The host service composes the existing Linux `Registry`, broker, Bubblewrap
launcher and `HostCoordinator`. The coordinator retains owner pins and its
worker cleanup barrier. Its host authorizer is deny-only; the service's
independent unknown-root gate rejects before broker preparation, grant use, or
worker launch. No model, tool or UI data can construct or complete a coverage
profile. The synthetic four-owner inventory remains in the study command and
is not used by `cmd/harness`.

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

The runtime now accepts an optional, internal `ToolPreparer`. For a tool ID
the trusted preparer owns, runtime invokes it only after validated invocation,
approval and fresh permission evaluation, but before the atomic once-grant
consumption. A preparation refusal therefore consumes no once grant. If
preparation returns a retained host capability, runtime owns its `Close` on
every path and executes that handle only after grant consumption. Cancellation
or a grant revision change closes the preparation before dispatch. A cleanup
failure is surfaced as an unknown outcome. The host service handles only the
reserved `read_file` ID as a guard; no such tool is registered. Existing
`update_plan` and `request_user_input` continue through their current enforcer
without requiring filesystem coverage or a Linux backend.

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

The future prepared capability must bind the invocation, validated arguments,
exact route/opened identity/classification, root/composition/coverage/policy
revisions and authorization binding. It must revalidate before descriptor
release and publication. This runtime seam does not make preparation a lock
against independent namespace writers. Runtime remains the sole once-grant
consumer; the service's deny-only authorizer cannot consume grants.

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

- Focused runtime, sandbox, broker-study and command package tests exercise the
  new preparation ordering, auth/settings path overrides, replacement
  registration, unknown-root refusal, owner refusal and text-only composition.
- `cmd/harness --check` was built and run against synthetic auth/settings
  overrides. It reported zero configured profiles and completed successfully.
- This environment is Linux/amd64, Go 1.26.10, UID 1000. Its `/usr/bin/bwrap`
  is owned by UID 65534, so the required root-owner trust check rejects it
  before execution. Native worker/service positive and negative cases were not
  run. No elevated or unrestricted retry was attempted.
- Existing `read-broker-study` fixture and native evidence remains separate;
  it does not become evidence for this composed chat service. Do not carry
  forward the historical `6gGbUF` pass count.

Generic reads remain disabled even on a host with a trusted Bubblewrap backend.
The fixed worker is composed for future reviewed use but receives no target
descriptor through the shipped chat. This slice does not implement the unknown
in-root-data policy, provider-source owner integration, Token Logger owner
hooks, a filesystem-root UI, read_file, listing, recursion, writes, deletion,
shell execution, or non-Linux enforcement. Real native positive-control
evidence and an explicit maintainer decision on unknown in-root objects remain
required before any typed-read enablement task.

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
