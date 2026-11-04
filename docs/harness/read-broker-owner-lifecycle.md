# Native owner registration and lifecycle study

**Status:** experimental acceptance plan for issue #215. It uses only disposable synthetic fixtures. It does not wire production owners, provider queues, runtime dispatch, or model-facing filesystem/shell tools.

The target contract is the approved object-authority profile (B) and trusted-owner exact-file registration in the [approved typed-read profile](read-broker-approved-typed-read.md). A directory pin is not a descendant inventory; `DirectoryCoverageIncomplete` remains fail closed. Historical archives are unchanged and are not evidence for this new protocol.

## Scenario and expected-evidence matrix

| Case | Object and owner action | Worker/authority transition | Required outcome and evidence |
|---|---|---|---|
| Registration before release | Prepare and authorize an ordinary file. The owner registers that same opened identity as credential data before the read descriptor is sent. After the worker joins, the owner may write synthetic sensitive bytes through a descriptor whose identity equals the pin. | One policy-generation advance invalidates the prepared receipt. A real worker may have started and passed readiness, but no target FD is handed off and no read starts. | Once approval remains consumed once; no publication; direct worker join confirmed. A fresh request through a registered identity alias is refused before worker/data handoff. Owner write is gated on read completion/join and pin identity. |
| Registration while access is held | Prepare and authorize an ordinary object. A fixed worker test mode receives and validates that same read-only FD, acknowledges `CAPABILITY_HELD`, and waits at a bounded protocol gate before beginning `pread`. The owner registers that exact identity. | Registration advances policy and requests worker termination. The registration call may report cleanup pending while the active reader unwinds. The read path confirms direct-child join; only then may the owner retry and write through the same registered object. | Evidence separates FD sent/received from read started. Expected: sent and received, no `READ_STARTED`, no read/publication; worker joined before owner write; write FD identity equals the retained protected pin. A direct-child join is not namespace-wide cleanup evidence. |
| Registration after capture, before publication | Read an ordinary fixture through the real worker until bytes are captured and the direct worker has exited. The owner authoritatively registers/pins that same identity at the post-capture checkpoint. | Policy generation changes while the coordinator still holds buffered bytes; the publication gate must reject the stale result. | Evidence retains the actual read/capture and registration generation change while showing `published=false` and no sink bytes. This complements, but does not replace, the held-capability worker case. It does not claim to recall bytes already read. |
| Rotation with old/new objects | Start with a pinned old protected object and aliases. Create an empty replacement/temporary file and open its writable descriptor. Register/pin both the temporary object and effective destination before sensitive bytes. After affected workers join, write through the descriptor matching the new pin, then publish by rename. | Registration/policy generations invalidate affected work. The old pin is retained through shutdown. The destination and temporary routes are reserved by policy; identity protection follows both old and new objects through aliases/renames. | Record path and object identities before/after publish, write start/completion, and generation. Old/new aliases and renamed old object refuse before worker handoff. No old authorization or invocation replay dispatches against the replacement. |
| Cleanup pending | Use the actual bounded worker lifecycle. Registration returns cleanup pending while termination/join is outstanding. | Owner write and affected admission remain blocked until the same direct worker is confirmed joined. | Do not simulate a native unjoined child with a fake flag. If a real incomplete-join condition cannot be produced safely, mark it unavailable; retain injected regression coverage and disclose that direct join does not prove descendant cleanup. |
| Provider missing approval | Provider-only registry plus a separate ordinary fixture. Request the registered provider source without an explicit once grant. | Real broker/registry/session; no worker is expected to start. | `refused`, zero once consumption, no handoff/read/publication. This is an authorization refusal, not a native positive read. |
| Provider authorization mismatch | With provider-only registration, supply a receipt bound to an ordinary identity/class/route or another invocation. | Runtime receipt correlation must reject before worker launch. | Record mismatch type, consumption outcome and zero worker/data activity. Do not let the credential/settings registrations mask this case. |
| Provider stale generations/replay | The fixture authorizer first creates the correctly bound receipt, then a deterministic test hook changes policy, root, or grant generation before returning that now-stale receipt; separately retry a consumed invocation. | Old receipts cannot dispatch after invalidation; once consumption is not restored or replayed. | Record before/after revisions and invocation identity. Stale work has no data FD handoff; replay adds no worker. The receipt mutation wrapper is coordinator test instrumentation, not provider or sandbox enforcement. |
| Provider explicit once | Provider-only registry and registered synthetic source. Approve the exact invocation, route, class, object and generations. | One actual Bubblewrap worker receives the approved object and returns the bounded bytes. | Bytes match the approved object; one consumption, one handoff/read/publication; a second dispatch is refused. |

## Bounded worker hold protocol

Only the feasibility harness may request the fixed hold mode. The normal worker launch and protocol remain unchanged. The hold mode adds one explicit bootstrap option and two protocol frames:

```text
worker validates the single received read-only FD against the approved identity
worker -> host: CAPABILITY_HELD(identity)
host owner registers/pins and advances policy; coordinator requests termination
worker remains at the bounded gate; it cannot start pread without RELEASE_READ
host confirms direct worker join; owner verifies writable FD identity equals pin
owner writes synthetic bytes through that FD
```

`RELEASE_READ` is only sent for a non-invalidated operation. The coordinator must serialize the final generation check and release send with the same session mutex used by registration: if registration wins, no release or read occurs; if release wins, registration invalidates and joins the worker before the owner writes. The worker already receives a single capability and validated identity; the hold does not add a path, descriptor, syscall, mount, network permission, or general command. A finite worker deadline and existing coordinator cancellation bound the wait. Reports distinguish `capability_sent`, `capability_received/held`, `read_started`, owner `write_authorized`, `write_started`, `write_completed`, join, and publication. A missing acknowledgement or cleanup is unavailable/unknown, never a pass.

The host-side fixture owner is a narrow test-only operation: register/pin exact paths, wait for the affected read to finish joining when registration reports pending, retry idempotently, verify the open write descriptor's `(device,inode,type)` equals the retained registry pin, then write and optionally rename the registered replacement. It does not receive or alter a model grant and cannot register paths from model arguments.

## Lifecycle ordering

```text
prepare target and capture object/route/class/generations
  -> consume exact once authorization once
  -> launch fixed Bubblewrap worker; verify readiness/probes
  -> (held case) send one read-only FD; worker validates identity and acks HELD
  -> owner registers/pins affected object; publish policy generation; invalidate active read
  -> request worker termination; wait/reap direct child
  -> owner confirms cleanup and pin identity; only then write sensitive bytes
  -> suppress stale result; never restore grant or replay invocation
  -> retain old/new pins and descriptors until their owned lifecycle ends
```

The operation generation and once-grant consumer remain coordinator-owned. Owner registration does not consume a grant. Registration can become authoritative before cleanup finishes; while cleanup is pending the owner must not write and the session must deny affected admissions. A completed read is not rolled back. Captured bytes are published only if root/policy/grant revisions remain current.

## Evidence boundaries and pending cases

A successful native case requires a trusted Bubblewrap backend, the actual fixed worker start, all startup probes, the described descriptor/control handshakes, explicit authorization outcome, fixture identity/effect inspection, and confirmed direct-child join. Injected worker tests validate coordinator behavior but are not native evidence. No test establishes arbitrary namespace-descendant cleanup, parent-death cleanup, sealed-copy mode unless that mode actually ran, or production-owner integration. A failure or trust refusal must retain its exact phase and mark later cases unavailable.
