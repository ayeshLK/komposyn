# Approved exact-file typed-read profile

**Status:** implementation target approved in [issue #215 decision record](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6623836377); experimental harness implementation and native acceptance remain pending until this change is validated. This document does not enable model-facing filesystem or shell tools or integrate production owners.

## Contract

Authority follows the object and the requested route captured for one
invocation. A prepared read binds the selected root generation, canonical
requested route, opened regular-file identity, resource classification,
policy/grant revisions, and one once-authorization receipt. It does not bind
continuous containment beneath a live host pathname or a stable content
snapshot.

| Invariant | Required behavior |
| --- | --- |
| Direct protected route | A new generic request whose requested route is a registered credential/auth, settings, or Token Logger file location is refused, including a reserved absent location that is later replaced. Directory routes remain subject to directory-coverage refusal. |
| Registered sensitive identity | A registered credential/auth, settings, or Token Logger object is refused through every alias and after rename. A late alias of an already registered sensitive object is never treated like a late alias of an ordinary object. |
| Ordinary route and late alias | If an independent process links or moves an already-approved ordinary object into a protected location after its route/classification and authorization were captured, that alias alone does not reclassify or cancel that invocation. It may finish with the same opened object if root, policy, grant, authorization, and lifecycle revisions remain valid. A fresh request uses fresh preparation and authorization. |
| Provider source | A registered provider-source identity or requested provider-source route is classified as provider data and requires explicit per-invocation approval bound to the actual identity and route. An ordinary receipt never authorizes it. |
| Owner registration/rotation | A new authoritative registration or rotation advances policy generation and invalidates affected prepared/active work. Before an owner writes sensitive bytes into a new/replacement object, it registers and pins that object, waits for affected workers to terminate and join, then writes through the same pinned object. Incomplete cleanup blocks the write and affected admission. Old pins remain through registry shutdown. |
| Directory registration | A directory pin is not a descendant inventory. Keep `DirectoryCoverageIncomplete` refusals. Do not widen the exact-file profile by removing this gate. |
| Other invalidation | Revocation, root changes, policy revisions, cancellation, and shutdown retain their existing release/publication barriers. A stale result is suppressed; an already completed read is not rolled back. No once grant is restored or replayed. |
| Content and disclosure | Identity/path enforcement cannot recognize independent copies of sensitive bytes or freeze in-place content. A successful read can be disclosed to the selected provider under its explicit approval. |

## Route and preparation semantics

The request route is the clean absolute target supplied to the fixture
coordinator under the explicitly selected root. `openat2` performs a
component-constrained, no-symlink, no-mount-crossing lookup and opens the
object. The requested route is retained as authorization context; the opened
descriptor and `(device, inode, type)` identify the prepared object.

At preparation:

1. Verify that root and route are canonical, absolute, and component-contained.
2. Refuse a requested route that matches a registered protected exact-file
   location or lies beneath a registered protected directory. A new request
   through the protected route is denied even if the location is absent at
   registration and later contains another object.
3. Open the route beneath the captured root and reject non-regular files.
4. Classify registered identities first. Protected credential/settings/database
   identities are denied through aliases and renames. Provider identities and
   provider routes retain the provider class and once-approval requirement.
5. Refuse targets for which registered protected-directory descendant coverage
   is incomplete. Exact-file registrations do not disable an otherwise
   eligible ordinary read merely because their parent ancestry is mutable.
6. Bind approval to the requested route, opened identity, class, invocation,
   and root/policy/grant revisions. Consume the once grant exactly once before
   worker release.

After preparation, revalidation checks the captured identity/capability is
still open, authoritative policy/root/grant generations are unchanged, and
directory coverage remains valid. It must not rescan current exact-file names
and use an alias-only location change to reclassify the prepared ordinary
object. Direct protected-route denial applies to each new request's requested
route; it is not an atomic guarantee about all names that may later refer to an
opened object.

Authoritative owner registration is different from an outside alias. It
advances policy generation and invalidates affected work even when the
requested route is ordinary. An owner preparing a sensitive write must use the
registration/join barrier, confirm that its writable descriptor identifies
the registered pin, and only then write. If `Register`/`RegisterRotation`
reports cleanup pending, the owner must not write sensitive bytes or expose
them. A retry after cleanup may confirm the same registration and proceed; it
must not consume a model grant again or dispatch an old invocation.

## Compact invariant and acceptance matrix

| Scenario | Expected result | Evidence that counts |
| --- | --- | --- |
| Ordinary file with mutable exact-file auth/settings/database registrations under the same user-owned tree | Once-approved read succeeds and returns bytes from the approved open object. | Real broker/registry/session path; worker readiness/probes; one once consumption; handoff/read/publication flags; fixture-byte comparison. |
| New direct request through protected exact path, including absent then replacement | Refused before once consumption and worker/data-FD handoff. | Route and fixture state, exact refusal reason, identity/handoff flags. A bare `ENOENT` is not a protected-policy pass. |
| Pinned sensitive object alias/rename | Refused before handoff. | Actor confirms transition and exit; registry pin still live; zero once consumption and no worker read. |
| Ordinary object receives a protected-location alias after final revalidation | Existing invocation may succeed under revised B; a fresh direct request through the protected route refuses. | Independent actor transition and exit; existing result matches approved opened object; no retroactive class change; separately run the fresh route check. |
| Registered provider source | Requires explicit once approval bound to provider class, route, and actual object. | Include provider-only registration; ordinary authorization fails; explicit once succeeds once. |
| Owner registration/rotation during active read | Generation change invalidates the operation; sensitive write cannot start until affected worker is confirmed joined. | Controlled worker that keeps the capability usable until join; registration outcome, write gate, old/new pins, stale publication. |
| Directory registration with incomplete descendant coverage | Affected read remains refused with `ErrDirectoryCoverage`. | No workaround through exact-file registration; test both nested target and moved descendant. |
| Cancellation, revocation, root/policy change, shutdown | No stale release/publication; incomplete cleanup retains a blocking obligation and is not reported as a pass. | Deterministic barriers, truthful outcomes, actor exit/direct worker join and explicit descendant-cleanup limitation. |

Native evidence must separately identify the exact commit, selected Bubblewrap
bind mechanism (`--ro-bind-fd` or sealed-copy), and which actors/workers actually
ran. A descriptor-bind run does not validate sealed-copy mode. A passing Go
test, cross-build, or conservative refusal is not a successful native positive
read.

## Status and limits

The historical alias violation at `181d6a7656cdb730424cffa3e780c5598e8c0205`
remains a violation under the policy then in force. PR #226 at
`d2686a80d406c42fbb1e6e7a4d10e05e2c4005e1` correctly refused under its earlier
conservative policy. The approved contract changes the expected outcome only
for the ordinary-object late-alias case; neither historical report is rewritten
as a pass. This implementation must still demonstrate useful positive reads
and negative sensitive-identity/route cases separately.

This is a Linux experimental exact-file profile. It does not claim live
pathname containment, immutable file contents, detection of independent copies
of sensitive bytes, complete directory-descendant coverage, namespace-wide
cleanup, production owner/runtime integration, or support for shell/write/delete
tools. Filesystem and shell tools remain unregistered.
