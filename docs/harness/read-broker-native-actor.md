# Read-broker native independent-actor study

This document defines the next native feasibility scenarios for the
experimental broker-plus-Bubblewrap read study. It is a test plan, not a
production authorization contract. The study remains disabled for
model-facing filesystem and shell tools.

## Scenario and expectation matrix

| Scenario | Independent actor transition | Expected result | Contract/evidence |
|---|---|---|---|
| Protected alias in the scan-to-handoff gap | Hard-link the prepared ordinary object at an already registered protected file location after the final broker scan and before FD transfer. | No capability should be released after the protected location changes. Any transfer/read is recorded as a protected-rule violation even if the later publication check suppresses bytes. | Mandatory protected-location rule; B does not override it. |
| Protected alias before read admission | A separate same-user actor creates a hard link at a registered absent credential path before a read request. | The broker refuses before once-grant consumption, worker startup or descriptor transfer when protected-location ancestry is mutable. | Measures the conservative admission boundary only; it does not re-run or erase the scan-to-handoff violation. |
| Selected root moved after final scan | Rename the selected root object outside its original pathname while the sandbox worker is ready. | Read/publish only the object opened before the rename. This is consistent with B; strict live-path A would fail. | B follows exposed object authority. |
| Parent moved outside selected root | Rename the opened target's parent outside the selected root while the sandbox worker is ready. | Read/publish only the originally opened object. This is consistent with B; strict live-path A would fail. | B follows exposed object authority. |
| Target replaced after classification | Move the opened ordinary object aside and put a different synthetic file at its old pathname after final scan. | Return bytes from the opened object, never the replacement. This is consistent with B. | Object identity and descriptor authority. |
| Registered-directory descendant checks | First select the original fixture root, then register a synthetic protected directory. Read a child while inside, move it out, and read again. | The in-directory read must return the registered-protected-resource refusal; the moved-out read must return the incomplete-directory-coverage refusal. A scope mismatch cannot count as either protection result. | Mandatory protected descendant rule; conservative refusal is the existing policy. |
| Registered protected ancestor relocation/replacement | Requires a prepared ordinary target that remains eligible while the protected ancestor location is replaced or moved across its ancestry. | Pending unless a fixture can satisfy the broker's directory-coverage gate without weakening it. A setup refusal is not race evidence. | Mandatory protected-location rule. |
| Mount crossing / live owner rotation | Separate mount topology or coordinated owner rotation protocol. | Pending in this focused slice. | Not approximated by rename or same-process callbacks. |
| Fixture session cleanup | Close the coordinator session under a fixed deadline after all cases. | Record whether every started direct worker child was joined; timeout/error is inconclusive. This does not establish namespace-wide descendant cleanup. | Bounded study-process lifecycle. |

## Synchronization and evidence fields

The actor is a separate same-user process with a fixed operation allowlist and
a bounded `READY` / `RUN` / `DONE` protocol. Before `READY`, it opens and pins
the private fixture-root directory but does not inspect or mutate the requested
source/target entries. The coordinator invokes it at the explicit
post-final-revalidation, pre-capability-transfer boundary. Thus the
worker has completed readiness probes, while the actor transition is ordered
after the last broker scan and before SCM_RIGHTS handoff. For the moved-child
refusal case, the actor completes before the read attempt because the current
policy intentionally refuses that object during preparation.

The conservative gate refuses worker reads when a credential, settings or
Token Logger database registration has a containing directory chain that is
mutable or unverifiable for the current same-user actor. Under user-owned
temporary fixture roots, this makes the former worker cases unavailable. The
pre-admission actor case must be reported separately and cannot be interpreted
as successful execution of the original scan-to-handoff race.

Ordinary-classified reads are also refused when a registered provider-source
location is mutable or unverifiable. Correctly classified provider-source reads
remain distinct and still require explicit once approval. This prevents an
ordinary approval from being reused after a late transition into provider
classification.

Each native case reports actor readiness, transition completion, direct actor
exit confirmation, worker readiness/probe, descriptor release, read-start,
publication, once-grant consumption, policy/root/grant generations before and
after, fixture observations, outcome, and cleanup. A final
`study_session_cleanup` row records the bounded session close and whether all
started direct worker children were joined. A missing transition or
worker/backend precondition is `unavailable` or `inconclusive`, never a pass.
Observed protected-rule violations remain explicit in the JSON even when the
study process exits normally.

The study uses disposable synthetic paths only. It does not exercise real
credentials, databases, provider transcripts, production dispatch, shell, or
production tool registration. Native Bubblewrap evidence is reported only for
the selected launch mechanism and exact source revision.
