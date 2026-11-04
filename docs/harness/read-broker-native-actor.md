# Read-broker native independent-actor study

This document defines the next native feasibility scenarios for the
experimental broker-plus-Bubblewrap read study. It is a test plan, not a
production authorization contract. The study remains disabled for
model-facing filesystem and shell tools.

## Scenario and expectation matrix

| Scenario | Independent actor transition | Expected result | Contract/evidence |
|---|---|---|---|
| Protected alias in the scan-to-handoff gap | Hard-link the prepared ordinary object at an already registered protected file location after the final broker scan and before FD transfer. | No capability should be released after the protected location changes. Any transfer/read is recorded as a protected-rule violation even if the later publication check suppresses bytes. | Mandatory protected-location rule; B does not override it. |
| Selected root moved after final scan | Rename the selected root object outside its original pathname while the sandbox worker is ready. | Read/publish only the object opened before the rename. This is consistent with B; strict live-path A would fail. | B follows exposed object authority. |
| Parent moved outside selected root | Rename the opened target's parent outside the selected root while the sandbox worker is ready. | Read/publish only the originally opened object. This is consistent with B; strict live-path A would fail. | B follows exposed object authority. |
| Target replaced after classification | Move the opened ordinary object aside and put a different synthetic file at its old pathname after final scan. | Return bytes from the opened object, never the replacement. This is consistent with B. | Object identity and descriptor authority. |
| Unregistered protected-directory child moved out | After registering a synthetic protected directory, use the separate actor to move an unregistered child outside it, then request a read. | Refuse with the precise incomplete-directory-coverage reason before authorization, worker launch, or FD handoff. | Mandatory protected descendant rule; conservative refusal is the existing policy. |
| Registered protected ancestor relocation/replacement | Requires a prepared ordinary target that remains eligible while the protected ancestor location is replaced or moved across its ancestry. | Pending unless a fixture can satisfy the broker's directory-coverage gate without weakening it. A setup refusal is not race evidence. | Mandatory protected-location rule. |
| Mount crossing / live owner rotation | Separate mount topology or coordinated owner rotation protocol. | Pending in this focused slice. | Not approximated by rename or same-process callbacks. |

## Synchronization and evidence fields

The actor is a separate same-user process with a fixed operation allowlist and
a bounded `READY` / `RUN` / `DONE` protocol. It is started before the fixture
read and waits without touching its paths. The coordinator invokes it at the
explicit post-final-revalidation, pre-capability-transfer boundary. Thus the
worker has completed readiness probes, while the actor transition is ordered
after the last broker scan and before SCM_RIGHTS handoff. For the moved-child
refusal case, the actor completes before the read attempt because the current
policy intentionally refuses that object during preparation.

Each native case reports actor readiness, transition completion, direct actor
exit confirmation, worker readiness/probe, descriptor release, read-start,
publication, once-grant consumption, policy/root/grant generations before and
after, fixture observations, outcome, and cleanup. A missing transition or
worker/backend precondition is `unavailable` or `inconclusive`, never a pass.
Observed protected-rule violations remain explicit in the JSON even when the
study process exits normally.

The study uses disposable synthetic paths only. It does not exercise real
credentials, databases, provider transcripts, production dispatch, shell, or
production tool registration. Native Bubblewrap evidence is reported only for
the selected launch mechanism and exact source revision.
