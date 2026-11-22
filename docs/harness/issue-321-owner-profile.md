# Issue #321 production owner-profile foundation

**Status:** first gated implementation slice; production creation remains
unavailable and unregistered.

The trusted `cmd/harness` composition now passes the launch directory to the
host sandbox before initializing the runtime's navigable working directory.
The host records a per-run opaque binding and an immutable startup-scope
generation. This snapshot is separate from the runtime filesystem root, so
`/cd` and conversation reset do not replace it. The current snapshot reports
`identity-validation-deferred`: this slice retains no directory descriptor and
does not establish object identity, movement detection, or creation eligibility.
Unsupported host profiles keep this future capability unavailable without
changing ordinary text-chat startup.

The typed creation-owner profile follows the current trusted composition:

| Owner | Applicability | Coverage required |
|---|---|---|
| Auth store | Required | Effective default/override route and its registered object identities or reserved absent route, including rotation lifecycle. |
| Settings store | Required | Independent effective default/override route and its registered object identities or reserved absent route, including rotation lifecycle. |
| Token Logger store | Trusted composition declaration: not instantiated | No Token Logger store is opened or owned by this chat. This says nothing about arbitrary project files. |
| Provider-source ingestion | Trusted composition declaration: not instantiated | This chat does not ingest provider sources. |

An absent auth/settings file is still a required owner: its effective route is
registered as a reservation. Missing registration, pending/retired/stale
coverage, or an unknown sensitive owner makes the owner profile incomplete.
No empty path, empty registry, caller boolean, runtime argument, model request,
or UI selection can declare an owner uninstantiated. The declarations are
fixed in trusted composition code. A future sensitive component must be added
as required and integrated through its real lifecycle before this profile can
report complete coverage. The host snapshot carries the inventory's actual
required-owner set; a newly required inventory owner missing from the
production owner registry is surfaced as unknown, and declaring an inventory
required owner as not instantiated makes coverage incomplete.

`Service.CreationFoundationSnapshot` returns detached owner counts and states,
profile/coverage revisions, startup run binding and safe availability reasons.
It exposes no path, descriptor or filesystem capability. It is informational:
no runtime operation consumes it as permission, and it cannot enable a tool.
Creation registration remains absent even when auth and settings coverage is
complete. Existing read/discovery admission and its unknown-root gate are
unchanged; ordinary project entries are not inventoried by this profile.

## Established and deferred

This slice establishes the composition's owner applicability declarations,
reuse of existing auth/settings registration and rotation state, a fixed
startup input captured before navigation, and observable fail-closed snapshot
behavior. Existing tests exercise auth/settings default and override routes,
absent-route reservation, protected-object replacement, pending/stale
coverage, and cleanup through their existing lifecycle hooks.

Still deferred are retained startup-directory identity capture, movement and
replacement detection, nested existing-parent resolution, protected-target
checks for creation, scope/owner transition enforcement at creation, new
scope-specific native evidence, and separate explicit model-facing enablement.
Historical fixture/native creation evidence does not establish these claims.
The snapshot contract and tests here do not claim end-to-end startup identity
enforcement.
