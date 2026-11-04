# Protected-alias admission assessment

This note records historical evidence and the current approved contract for
the experimental broker study. The current contract is defined in
[`read-broker-approved-typed-read.md`](read-broker-approved-typed-read.md) and
the [issue #215 decision record](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377).
It does not enable production filesystem or shell tools.

## Historical evidence

The archive at
`f81ca0ad384c171e77ee30bfb1db544cb3bbc0c2ba18dc6c19073ea5ceb5affc`
reports source commit `181d6a7656cdb730424cffa3e780c5598e8c0205`, 17 measured
passes, one observed violation, and one inconclusive case. In the violating
scan-to-handoff case, an independent same-user process created a hard link to
an ordinary prepared object at a registered protected file location after the
last broker scan. The worker received the descriptor and began reading; the
later publication check suppressed the bytes. Under the policy then in force,
this was correctly reported as a protected-location violation. It was
synthetic fixture evidence, not a real credential disclosure.

PR #226 at `d2686a80d406c42fbb1e6e7a4d10e05e2c4005e1` later conservatively
refused before once-grant consumption or worker start. That report is also
historical; it did not measure a successful read and is not rewritten as one.

## Current approved contract

The approved policy distinguishes a request's captured route from the identity
of the opened object:

- A **new request** directly through a registered credential/auth, settings,
  or Token Logger protected file location is denied, including a reserved
  absent exact-file location that is later replaced.
- A registered sensitive **object identity** remains denied through hard-link
  aliases and after rename while its owner pin remains live.
- A late alias of an already-approved **ordinary** object into a protected
  location does not, by itself, reclassify or cancel that existing invocation.
  Its result remains bound to its previously opened object and captured route.
  A separate fresh request through the protected route is denied.
- Provider-source reads remain a separate class and require explicit
  per-invocation approval bound to the requested route and actual object. An
  ordinary approval cannot authorize a provider-source object.
- Authoritative owner registration, rotation, revocation, root changes, or
  policy/grant generation changes invalidate affected active work. This is
  distinct from an independent late alias and must still suppress stale
  publication.

This is the approved object-authority profile (B), not strict live host-path
containment (A). The policy does not claim continuous pathname containment,
immutable contents, or detection of independent copies of sensitive bytes.

## Owner registration and rotation

The initial profile accepts trusted-owner exact-file registrations. For a
credential or other sensitive file, an owner must:

1. Create and pin the replacement object before putting sensitive bytes in it.
2. Publish the registration/policy generation through the coordinator.
3. Wait until affected workers have terminated and joined.
4. Write sensitive bytes through the same object identity that was registered.
5. Retain old pins through registry shutdown.

If registration reports cleanup pending, the owner must not write sensitive
bytes or expose the replacement. It may retry after cleanup is confirmed.
Policy changes invalidate affected work; no consumed once grant is restored or
replayed. Owner rotation is a serialized protocol, not an application lock that
constrains an independent namespace actor.

Registered directories are **not** descendant inventories. Pinning a
directory does not pin or classify every existing or future child, nor does it
protect an object moved out. Preserve `DirectoryCoverageIncomplete` refusals;
do not treat directory registration as equivalent to complete exact-file
registration.

## Evidence still required

The historical alias violation remains useful regression evidence for the
old policy and race ordering. New tests should separately establish the
approved semantics: ordinary read + late alias may finish from the opened
object; a fresh direct protected-route request refuses before grant
consumption/handoff; a registered sensitive identity refuses through aliases
and rename; and an authoritative registration/rotation invalidates active
work and blocks sensitive writes until worker join. Directory coverage must
continue to fail closed where incomplete.

Native runs must report the exact source commit and Bubblewrap bind mode.
Descriptor-bound and sealed-snapshot runs are distinct evidence. A Go test,
cross-build, setup refusal, or historical report does not establish native
positive-read acceptance for the revised profile. The study remains
experimental, typed-read-only, and separate from production tool registration.
