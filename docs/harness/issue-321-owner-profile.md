# Issue #321 production owner-profile foundation

**Status:** first gated implementation slice; production creation remains
unavailable and unregistered.

The trusted `cmd/harness` composition passes the launch directory to the host
sandbox before initializing the runtime's navigable working directory. The
host retains an opened startup directory and its identity and mount binding
only when the active trusted Linux creation profile and ext4 check succeed.
`/cd` and conversation reset do not replace this retained startup scope. If the
original path stops naming that object, the service latches a terminal moved
state, invalidates proposals, and drains affected creation work. Restoring the
old pathname does not restore eligibility. Unsupported profiles keep this
future capability unavailable without changing ordinary text-chat startup.

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
unchanged; ordinary project entries are not inventoried by this profile. This
slice permits fixture proposals to resolve existing nested parents beneath
the retained startup descriptor, checks their protected-owner ancestry, and
revalidates the exact scope and parent before execution. It does not register
the model-facing tool or enable production creation.

## Established and deferred

This slice establishes the composition's owner applicability declarations,
reuse of existing auth/settings registration and rotation state, retained
startup directory identity, fixed-scope nested parent resolution, protected
owner classification from retained descriptors, and terminal movement
detection. Runtime/service tests exercise replacement, restoration, nested
targets, stale parents, refusal before dispatch, and existing owner lifecycle
behavior.

Still deferred are new trusted-host native evidence for startup-scope creation
and movement, broader filesystems and launch profiles, production creation
registration, and separate explicit model-facing enablement. The fixture
tests establish control flow, not native filesystem enforcement. Historical
fixture/native evidence does not establish these new claims. Creation remains
unavailable in the normal CLI composition.
