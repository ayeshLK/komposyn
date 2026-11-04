# Read-broker native independent-actor study

This document describes native scenarios for the experimental
broker-plus-Bubblewrap typed-read study. The current expected contract is
[`read-broker-approved-typed-read.md`](read-broker-approved-typed-read.md),
approved in the [issue #215 decision record](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377).
The study does not enable production filesystem or shell tools.

## Scenario and expectation matrix

| Scenario | Independent actor transition | Expected result | Evidence meaning |
|---|---|---|---|
| Late alias of approved ordinary object | After final revalidation and before descriptor transfer, create a hard link to the opened ordinary object at a registered protected exact-file location. | The existing invocation may read and publish bytes from its already-open object, provided authorization/root/policy/lifecycle generations remain valid. | Consistent with approved object authority B. This does not establish strict live-path containment A. |
| Fresh direct request through protected route | After a late alias, make a separate request through that registered protected route. | Refuse before once-grant consumption, worker start, or target descriptor handoff. | Checks requested-route protection independently from the existing invocation. |
| Registered sensitive identity via alias or rename | Create an alias or rename a registered synthetic sensitive object, then request it by the new route. | Refuse before target descriptor handoff. | Registered-object identity remains protected across these names while the owner pin remains live. |
| Selected root moved after final validation | Rename the selected root object while the worker is ready. | The read may return bytes from the object opened before the rename if generations remain valid. | Consistent with B; strict live-path containment A would fail. |
| Parent moved outside selected root | Rename the opened target's parent while the worker is ready. | The read may return bytes from the originally opened object. | Consistent with B; does not establish A. |
| Target replaced after classification | Move the opened ordinary object aside and place another synthetic file at the old route after final validation. | Return bytes only from the already-opened object. | Tests object binding, not route containment or content immutability. |
| Directory descendant coverage | Register a synthetic protected directory; read an in-tree child, then move an unregistered descendant out and read it. | In-tree protected-route access refuses; moved-out read remains refused with `DirectoryCoverageIncomplete` when complete descendant coverage cannot be established. | A directory pin is not a descendant inventory. Do not weaken this refusal to create a positive case. |
| Owner registration/rotation during active read | Register and pin a synthetic replacement while a controlled worker holds the current capability. | Affected work is invalidated; owner must wait for worker termination and join before writing sensitive bytes through the registered object. Cleanup pending blocks the write. | Tests the trusted-owner barrier, not protection from an independent actor. |
| Fixture session cleanup | Close the coordinator session under a fixed deadline after all cases. | Record whether every started direct worker child joined; timeout/error is inconclusive. | Does not establish namespace-wide descendant cleanup. |

## Synchronization and evidence fields

The actor is a separate same-user process with a fixed operation allowlist and
a bounded READY/RUN/DONE handshake. Before READY, it opens and pins only the
private synthetic fixture root; it does not inspect or mutate requested
entries. The coordinator releases it at an explicit checkpoint. For a late
alias case this checkpoint follows final authorization/generation
revalidation and precedes target descriptor transfer. This tests an
independent namespace transition, rather than a callback within broker code.

The revised expected behavior differs from the historical policy: an alias
created at a protected location does not retroactively change the class of an
already prepared ordinary object. The existing invocation may finish from
its captured object capability. A fresh request through the protected route
must independently refuse. A late alias of an already registered sensitive
object remains protected by its pinned identity. Authoritative registration,
rotation, revocation, root or policy-generation changes still invalidate
affected active operations.

For the owner-rotation scenario, a controlled worker must keep the read
capability live until the coordinator confirms termination and join. The
synthetic owner registers/pins the new empty file before sensitive fixture
content is written, waits for the registration barrier, verifies that its
writable descriptor names the registered object, and writes only after
cleanup is complete. On cleanup pending, no sensitive content is written.
This models the required owner protocol; it does not claim that a lock or
registration prevents independent namespace actors.

Each native case records actor readiness, transition completion and direct
actor exit; worker readiness/probes; descriptor release; read start;
publication; once-grant consumption; root/policy/grant generations; observed
fixture state; outcome; and cleanup. Missing transitions or failed worker
preconditions are unavailable/inconclusive, never passes. A process exit code
alone does not establish the expected fixture effect.

The study uses disposable synthetic paths only. It does not inspect real
credentials, databases, provider transcripts, or production data. It does not
exercise production dispatch, shell, or production tool registration. Native
evidence is scoped to the exact source revision, trusted backend and launch
mode recorded for that run.
