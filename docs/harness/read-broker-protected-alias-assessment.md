# Protected-alias admission assessment

This note documents the conservative read-admission gate added to the
experimental broker study. It does not enable a production filesystem tool.

## Invariant and observed gap

Pinned identity and protected-location rules cover different facts. A retained
descriptor identifies the registered object across rename or hard-link aliases.
A registered pathname protects an object that appears at that location,
including an ordinary object newly linked there. The native
`actor_protected_alias_scan_to_handoff` run demonstrated the second case: an
independent same-user process created a hard link after the final broker scan;
the worker received the descriptor and began reading; only a later scan
suppressed publication. That remains an observed protected-rule violation.

The supplied sanitized archive (`f81ca0ad384c171e77ee30bfb1db544cb3bbc0c2ba18dc6c19073ea5ceb5affc`)
reports source commit `181d6a7656cdb730424cffa3e780c5598e8c0205`, 17 measured
passes, one observed violation and one inconclusive case. Its violating row
confirms actor readiness/transition/exit, worker readiness/join, one grant
consumption, descriptor transfer and read start, followed by suppressed
publication. This is synthetic fixture evidence, not a real credential
disclosure.

The coordinator mutex orders grant, root and policy generations. It does not
order a same-user namespace writer. A later scan, link count, path comparison or
generation check therefore cannot make the handoff safe.

## Admission mechanism

For credential, settings and Token Logger database registrations, the broker
now refuses *all* worker-read preparation if it cannot establish that every
directory capable of containing or replacing the registered location is
immutable to the current process identity. Registration checks the containing
directory chain using descriptor-relative, no-symlink traversal, effective
write-and-search permission checks (including ACLs), ownership, and Linux
effective, permitted and inheritable capability sets. Unknown checks fail
closed. A directory registration includes the registered directory itself
because it can contain new aliases.
For an exact-file registration, the containing directory is the final
location boundary.

This is an admission restriction, not a namespace lock. Its same-user claim is
limited to an actor with the same unprivileged effective identity and
credential set; setuid/capability elevation is outside the demonstrated
experiment. It is sound only for the stated actor and registered locations
whose checked ancestry cannot be changed by that actor. It assumes the trusted
owner registered the correct locations and that the kernel and permission
checks are sound. It does
not cover a privileged actor, independent copies of sensitive bytes, incomplete
owner inventory, or a future registration racing with an already released
capability. Live owner rotation remains a separate unsupported experiment.

The gate runs during preparation, before once-grant consumption, worker launch
or descriptor duplication, and is checked again during broker revalidation.
It leaves explicit provider-source reads alone because those already require
per-invocation approval; a generic protected registration still blocks the
entire worker-read admission path. The harness does not hold a global namespace
lock or claim that the check secures arbitrary mutable protected paths.

## Compatibility and evidence

The synthetic native fixtures are created under user-mutable temporary
directories. With the new gate, those worker-read scenarios are deliberately
unavailable. The added independent-actor case creates the late hard-link alias
before calling the broker; the broker must refuse with the specific admission
reason, consume no once grant, start no worker, transfer no descriptor, read no
bytes and publish nothing. This demonstrates the refusal boundary, not a pass
for the previous scan-to-handoff race. The earlier native violation remains
historical evidence and must not be relabeled as fixed by post-read suppression.

The gate also means provider-source reads are unavailable in a fixture registry
that contains a mutable generic-protected location. Typical user-owned home
configuration trees fail admission, so this policy is expected to disable
generic reads in common installations. No positive native case is claimed for
the new policy unless an eligible, same-user-immutable protected hierarchy is
actually exercised.

Pending evidence remains: sealed-copy native validation, protected ancestor
replacement, mount boundaries, live owner rotation, recursive-child movement,
parent death, descendant termination, and incomplete-shutdown cleanup. This
research change does not establish production readiness or shell safety.
