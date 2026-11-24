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
It exposes no path, descriptor or filesystem capability. The separate
`FixedCreationScopeStatus` runtime contract carries the trusted startup path
only to the local UI, together with lifecycle/readiness state; it is detached
and is not consumed as permission. No provider request or diagnostic receives
this display path.
Creation registration remains absent even when auth and settings coverage is
complete. Existing read/discovery admission and its unknown-root gate are
unchanged; ordinary project entries are not inventoried by this profile. This
slice permits fixture proposals to resolve existing nested parents beneath
the retained startup descriptor, checks their protected-owner ancestry, and
revalidates the exact scope and parent before execution. It does not register
the model-facing tool or enable production creation.

The preparation prototype has a separate package-private test gate that
normal composition never sets. Normal `cmd/harness` now captures the launch
directory through `NewHostServiceWithStartupDirectory` before navigation is
initialized. The UI reports the fixed startup scope separately from the
working directory, and reports creation unavailable while the production
registry omits the schema. For retained startup scopes, owner eligibility
comes from the live coordinator's complete auth/settings requirements,
service lifecycle state, and matching policy registrations, including
reserved absent routes. Token Logger and provider-source coverage is not
fabricated: those components remain trusted-not-instantiated in this chat.
The synthetic four-owner manifest remains confined to earlier fixture cases
and is not required to exercise the retained-startup owner profile.

## Established and deferred

This slice establishes the composition's owner applicability declarations,
reuse of existing auth/settings registration and rotation state, retained
startup directory identity, fixed-scope nested parent resolution, protected
owner classification from retained descriptors, and terminal movement
detection. Runtime/service tests exercise replacement, restoration, nested
targets, stale parents, refusal before dispatch, and existing owner lifecycle
behavior.

The test-only runtime composition connects the real Service proposal and
worker-supervision path through runtime authorization and the existing UI
approval bridge. Its behavioral test verifies the exact nested target,
complete multiline diff, provider/model/profile destination, explicit once
response and resulting bytes while using actual auth/settings registration,
including an absent reserved settings route. Existing tview approval handling
remains scrollable for long details. Those UI tests are behavioral; the
recorded native run below validates Service-level acceptance only, not UI
presentation or a live provider.

The PR #355 candidate has trusted-host native evidence for startup-scope
creation/movement, nested-parent substitution, and the target refusal matrix
on Linux amd64/ext4 with BubbleWrap `ro-bind-fd`. Its original run and the
corrected PR #356 run each recorded 17 measured passes with an unavailable
mount-boundary case; those outcomes remain historical. A later exact-current-
master run at `c0af7cda7bbbce3de289367af9e468ea8bea73df` measured 18/18 cases,
including mount-boundary refusal across real ext4 mount IDs 33 and 157. Its
archive SHA-256 is `00a4ffccd3c4acf156faae120a863e04ffc1a74f185702b4f1e0acafc6077f59`.
See the [native acceptance record](issue-321-native-acceptance.md#nested-mount-boundary-follow-up)
for complete provenance and limits. Broader filesystems and launch profiles,
production creation registration, integrated lifecycle review of every
stale/cancel/interruption case, and separate explicit model-facing enablement
remain deferred. Fixture tests establish control flow, not native filesystem
enforcement. Creation remains unavailable in the normal CLI composition.
