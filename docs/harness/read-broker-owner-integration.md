# Read-broker protected owner integration

This note records the bounded owner hook for the experimental read-broker
study. It does not wire a production filesystem service or make any filesystem
or shell tool available.

## Composition

```text
auth.Store / settings.Store
  -> SensitiveFileProtector (host supplied; no policy authority in the store)
  -> read-broker study adapter
  -> FixtureSession.RegisterRotation
  -> pinned PolicyRegistry + generation invalidation + worker termination
  -> exact-object verification against the still-open owner descriptor
  -> owner writes through that same descriptor, then atomically publishes it
```

The credential and settings packages own their effective path rules, private
file validation, lock, JSON serialization, and replacement. They expose
`EffectivePath`, `RegisterProtection`, and a narrow optional
`SensitiveFileProtector`. A store with no protector retains its existing
behavior. The shipped `cmd/harness` has no filesystem enforcer, worker session,
or filesystem tools; it does not install a protector. The adapter exists only
in `cmd/read-broker-study` and its disposable tests. Thus the composition
proves owner-to-registry sequencing without claiming production enforcement.

## Existing-object registration

The host calls `RegisterProtection` for each effective store before it admits
reads to a future protected worker session. The store resolves its configured
override or existing default, opens an existing file with the package's private
file rules, and passes that descriptor and route to the host hook. A missing
leaf is registered as a route only. The study adapter registers the route and
compares the registry's pinned identity with the supplied descriptor identity;
a mismatch fails closed. Registration reads no file content.

`EffectivePath` reports the selected absolute path only. It does not scan the
machine, infer other owners, or establish read permission. No owner registration
grants a tool permission.

## Write and rotation ordering

The stores keep their existing per-path interprocess lock. They serialize the
new JSON value in memory, create a private empty temp file in the destination
directory, and apply restrictive permissions before invoking the protector.
The adapter registers both the effective destination route and the exact temp
path/object. `RegisterRotation` commits the pinned policy revision and
invalidates older prepared operations. If an affected worker has not been
confirmed terminated and joined, the hook returns cleanup-pending and the
store writes no content. The caller can retry after cleanup is confirmed.

After successful registration, the store checks cancellation and confirms the
temp pathname still names the same object as its open descriptor. It writes,
syncs, rechecks object identity, closes, and atomically renames the temp into
place. It never reopens the temp by pathname for content writing. The registry
retains the temp object's identity after rename, so the now-published object
and aliases remain protected. The prior destination identity remains pinned
until registry shutdown.

The lock order is owner store lock, then session/coordinator lock, then
registry lock. The study adapter and registry never call back into a store
while holding session or registry locks. The optional callback is invoked
while the store lock is held so another store writer cannot race the prepared
replacement.

If registration, context validation, identity verification, file writing,
sync, or publication fails, the store returns an error. Cleanup removes a temp
pathname only if it still identifies the originally created file; it will not
delete a replacement. This does not make general same-user namespace changes
atomic with final pathname publication. It protects the bytes written to the
registered object and fails closed on detected substitution, consistent with
the experimental object-authority profile. It is not strict live host-path
containment.

## Owner inventory and remaining gaps

| Owner/category | Status in this slice | Consequence |
| --- | --- | --- |
| Harness auth profile | Real store hook, effective override path, startup registration, empty-temp registration before auth JSON writes, rotation and retired-identity tests | Protected only when a host explicitly composes the hook; shipped chat remains without a filesystem service |
| Harness UI settings | Real store hook, effective override path, startup registration, empty-temp registration before settings JSON writes, save test | Same; settings reads by owner remain normal local storage operations |
| Token Logger DB/WAL/SHM/backups | Not registered here; owned by other core storage components | Any scope overlapping these resources remains ineligible until those owners provide authoritative lifecycle registrations |
| Provider source files | Not registered here; provider adapters remain read-only source owners | No claim of source inventory completeness; preserve the existing per-invocation source approval requirement |
| Environment-referenced credentials and independent copies of sensitive bytes | Not represented as auth-store file identities | Identity protection cannot discover independent copies or arbitrary external credential sources |

A registered directory is not a descendant inventory. Missing owner inventory,
`DirectoryCoverageIncomplete`, unsupported registration, cleanup-pending, or an
identity mismatch keeps affected operations unavailable. Ordinary-object late
alias behavior remains as approved in the issue decision; this integration
does not widen or redefine that contract.

## Evidence

The tests use temporary synthetic JSON files and injected store/session
capabilities. They exercise the actual auth `AddAPIKey` and settings `Save`
paths through the study adapter, including an override, replacement, old/new
identity retention, hard-link alias classification, and refusal/retry after a
cleanup-pending result. These are owner integration and registry tests, not
native Bubblewrap evidence. No credential, provider source, Token Logger
database, or user settings file is opened by the tests. The existing native
study and its environmental restrictions are documented separately in
`read-broker-feasibility.md`.
