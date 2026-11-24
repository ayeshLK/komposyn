# Temporary chat capability availability

Evidence reviewed 2026-11-24. This matrix describes the bundled temporary
chat routes and its current capabilities. “Supported” describes the
provider protocol implementation and fixture-backed contract; it does not
grant permission to execute a tool. Runtime availability also requires the
host authorization, approval, and enforcement composition. No capability
selection changes the provider, profile, authentication method, or billing
route.

The `apply_patch` rows describe the unmerged creation-enablement change in
this source branch. Existing released builds still omit it until the change is
reviewed and merged. The branch registers only file creation on the supported
Linux amd64/local ext4/trusted Bubblewrap `ro-bind-fd` profile; it does not
enable editing or other mutations.

The parity policy is that KompoSyn capabilities are offered across provider
and supported subscription or authentication methods unless a provider imposes
an evidenced restriction. API-key billing and subscription billing alone do
not justify different KompoSyn capabilities. Verify tool definitions, tool
calls, and native continuation for each route before exposing client tools.
Check model IDs only when provider documentation or a supported API operation
shows model-specific differences. Unknown route support stays unavailable
until verified; it is work to resolve, not evidence of a provider restriction.
Image support requires its own content-format evidence.

## Authentication routes

| Provider route | Inventory | Capability evidence | Status and remaining work |
|---|---|---|---|
| OpenAI API key | Implemented; listed by `openai.Plugin.AuthMethods` | Responses tool declaration, tool-call parsing, native continuation, and protocol tests in `plugins/model-provider/openai/` | Supported for `update_plan` and `request_user_input`; `read_file` is conditional on host prerequisites below. Continue live provider validation. |
| OpenAI ChatGPT OAuth (SIWC) | Implemented; listed by `openai.Plugin.AuthMethods` | Route-level `RouteCapabilities(AuthOAuth)` enables client tools based on the documented SIWC protocol; the account-specific model catalog controls which model can be selected. Fixtures cover declaration, streamed calls, result correlation, and continuation. Operator-reported live plan/input, exact-file read, cancellation, and denial results for public model `gpt-6-sol` are recorded in the [protocol assessment](openai-chatgpt-oauth-tool-protocol.md). | Any model returned in the selected profile's catalog may use the documented OAuth tool protocol; the live evidence covers one account and model, so other account/model combinations are not live-verified. The reported exact-file allow and deny used trusted root-owned Bubblewrap 0.9.0; no fallback was observed. Evidence remains limited to one account, model, and host. |
| Anthropic API key | Implemented; listed by `anthropic.Plugin.AuthMethods` | Messages tool declaration, `tool_use` parsing, result continuation, and protocol tests in `plugins/model-provider/anthropic/` | Supported for `update_plan` and `request_user_input`; `read_file` is conditional on host prerequisites below. Continue live provider validation. |
| Anthropic subscription / OAuth | No authentication integration or selectable route exists in the bundled adapter; `AuthMethods` returns only `AuthAPIKey` | No adapter or route evidence | Unimplemented. Do not create or imply a selectable profile. No claim is made that Anthropic prohibits such integration. |

The app only offers authentication methods returned by the registered provider.
An unavailable integration is documented here rather than represented as a
profile a user can select. OpenAI OAuth has route-level protocol evidence;
Anthropic subscription remains an absent integration.

## Capability matrix

Older “two conversation tools” descriptions refer specifically to
`update_plan` and `request_user_input`. The later conditional Linux
`read_file` milestone adds a third capability under separate host and
approval prerequisites; it does not broaden those two tools or grant general
filesystem access.

| Capability | OpenAI API key | OpenAI ChatGPT OAuth | Anthropic API key | Anthropic subscription / OAuth |
|---|---|---|---|---|
| `update_plan` | Supported; runtime authorization and approval required | Supported on the documented OAuth route for catalog-listed models; runtime authorization and approval required. Live Allow once observed on one account/model. | Supported; runtime authorization and approval required | Unimplemented route |
| `request_user_input` | Supported; runtime authorization and approval required | Supported on the documented OAuth route for catalog-listed models; runtime authorization and approval required. Live Allow once observed on one account/model. | Supported; runtime authorization and approval required | Unimplemented route |
| `read_file` | Conditional: supported client-tool route plus trusted Linux backend and complete host enforcement | Conditional: catalog-listed OAuth model plus trusted Linux backend and complete host enforcement. Operator-reported exact-file live journey passed on one account/model and Linux host. | Conditional: supported client-tool route plus trusted Linux backend and complete host enforcement | Unimplemented route |
| `list_files` | Conditional: supported client-tool route plus trusted Linux backend and complete host enforcement | Conditional: catalog-listed OAuth model plus trusted Linux backend and complete host enforcement; live listing is not verified | Conditional: supported client-tool route plus trusted Linux backend and complete host enforcement | Unimplemented route |
| `search_files` | Conditional: trusted Linux amd64 Bubblewrap descriptor-bind profile, complete owner coverage, ext4 selected directory, and explicit once approval | Conditional under the same host and ext4 requirements for catalog-listed OAuth models; no live search-provider journey is claimed | Conditional under the same host and ext4 requirements; no live search-provider journey is claimed | Unimplemented route |
| `apply_patch` creation | Proposed by this unmerged change: supported Linux amd64/ext4/descriptor-bind profile, supported client-tool route, complete live owner coverage, and explicit once approval | Proposed under the same host profile for catalog-listed OAuth models; creation has no live provider journey evidence | Proposed under the same host profile and route requirements; creation has no live provider journey evidence | Unimplemented route |

`list_files` is registered only when the Linux host has inspected the trusted
Bubblewrap backend and composed the fixed worker. The runtime additionally
requires a selected root and a supported provider client-tool route; every
directory disclosure has a separate once approval naming the exact directory
and provider destination. Native synthetic evidence for filtering, movement
and replacement, protected aliases, owner rotation, mount boundaries, and
worker cleanup was recorded on Linux amd64 with Bubblewrap 0.9.0. This does not
establish live provider listing, production-provider integration, or
cross-platform enforcement.

Recursive listing and its `recursive`, `include_hidden`, `include_ignored`, and
`name_globs` options are supported for trusted Linux amd64 Bubblewrap using the
descriptor bind profile (`ro-bind-fd`), enabled in [PR #330](https://github.com/ayeshLK/komposyn/pull/330)
(merge `7938bbf998fd2a1a7db503417f7201ef5e32f1e0`). The launcher exposes this
profile only after trust and bounded capability checks succeed and the fixed
worker service is composed. Other architectures remain unavailable because
native evidence covers amd64 only. Stage 6 passed the recursive-listing cases
on exact clean revision
`eb56eef57c90b3c5a653b99e1d3a3dcec451cfc5` using trusted, root-owned Bubblewrap
0.9.0 on Linux amd64; the report records actual mode `ro-bind-fd`. It measured
30 cases as passes; mutation and
shell profile cases were unavailable because they are outside this Stage 6
profile. Provider-source lifecycle, aggregate cgroup quotas, shipped-binary
integration, non-Linux enforcement, and active-traversal cancellation were not
established. See [the issue #317 completion and evidence record](issue-317-completion-tracking.md)
for the acceptance matrix, report and archive hashes, earlier attempts, and
remaining limitations.

The file-snapshot bind profile remains unavailable for advanced options
because the recorded run did not measure it. The public parser rejects glob
sets whose worker options frame would exceed 2,048 bytes, before once approval
or worker launch. The initial agent host's untrusted
`/usr/bin/bwrap` refusal remains historical and does not describe the supplied
trusted-host result. The public service forwards the profile-derived options
gate.

The final integrated #316 directory-specific native run used clean commit
`85e06601040858678d7859a5a047b774808d8e96` on Linux amd64 with trusted,
root-owned Bubblewrap 0.9.0. Archive
`komposyn-issue215-stage6-20261115T144130Z-1174993.tar.gz` has SHA-256
`f2262d7492f172576b216475a61d665e0b7d3c1bad2f316e899a658032744bda`.
Its valid report measured `positive_directory_listing`,
`directory_listing_movement_replacement`,
`directory_listing_owner_rotation`, `directory_listing_mount_boundary`, and
`directory_listing_worker_cleanup` as passes. The mount case also asserted
that a protected auth object bind-mounted under an ordinary alias was absent
from the provider continuation. These cases use the actual sandbox service
and assert registration through the production runtime backend gate. The
study exited successfully; the overall Stage 6 profile remains incomplete
for unrelated mutation, shell, aggregate-quota and production-provider
coverage. This evidence does not establish live provider listing or
cross-platform enforcement.

The conditional read path requires inspected trusted Bubblewrap, the complete
retained-read lifecycle and host enforcement, an available working directory,
and a selected route that explicitly supports client tools. Its evidence is
limited to the documented Linux amd64/Bubblewrap 0.9.0 environment and the
scoped native acceptance in issues [#215](https://github.com/ayeshLK/komposyn/issues/215)
and [#278](https://github.com/ayeshLK/komposyn/issues/278). These facts do not
establish cross-platform or production-provider acceptance. The working
directory only resolves relative requests; individually approved exact targets
may be outside it when host eligibility checks permit. Every read requires a
separate explicit once approval naming the resolved target and provider
destination. Directory selection grants no read permission. See the [read
acceptance record](issue-215-completion-tracking.md) and
[#278 evidence record](https://github.com/ayeshLK/komposyn/issues/278#issuecomment-6798370362).

`search_files` is available conditionally for a narrow profile: trusted Linux
amd64 Bubblewrap using the measured `ro-bind-fd` mode, complete owner coverage,
and a selected directory whose retained descriptor maps to an ext4 mount and
reports the measured statfs magic (`f_type=0xef53`).
Admission checks that descriptor's statfs magic and exact mount-ID filesystem
type before the runtime can request once approval, and revalidates both before
execution. Other filesystems, architectures, bind modes, and non-Linux
platforms stay unavailable. The tool
description states the ext4 requirement. Availability opens only
when the inspected launcher matches the measured worker profile; it does not
enable search on listing evidence or version strings. The ext4-scoped
enablement decision was implemented by merged PR #334, and the implementation
enforces those profile limits. The merged-source trusted-host runner 14 archive
recorded 38 passes and two unrelated unavailable profiles, plus 51 ext4 fixture
observations; it used the fixed test-binary worker and did not establish live provider or
shipped-binary acceptance. See [the search design note](search-files-design.md)
and [#318 completion matrix](issue-318-search-completion.md) for exact
provenance and profile limits.

The unmerged enablement change in this branch connects the retained-startup
Service to normal CLI runtime composition. Registration requires Linux amd64,
trusted Bubblewrap using measured `ro-bind-fd`, and a retained local ext4
startup scope. A supported provider route is still required. Registration is
only schema availability: live auth/settings owner coverage, current scope,
policy revisions, and cleanup state are revalidated at preparation, dispatch,
and publication. Tool registration does not authorize a save. Each request
creates one UTF-8 regular text file (up to 64 KiB) under an existing in-scope
parent, shows the exact target, complete contents/diff, and provider
destination, and requires explicit once approval. Existing destinations are
refused; editing, overwrite, append, rename, deletion, and directory creation
remain unavailable.

Behavioral coverage composes the actual sandbox Service with the existing
runtime authorization and UI approval bridge, using real auth/settings
lifecycle registration and checking exact target, multiline diff,
provider/model/profile destination, and approved bytes. The newly wired
shipping activation path still requires a fresh native run on the supported
profile; the recorded 18/18 run below predates this composition change. A
detached scope status is descriptive only and never supplies authority.

The earlier native run recorded 12/12 scoped cases at source
`dbfd90974f0979832a9abc60e32aeb3c87399840` on Linux amd64/local ext4 with
root-owned BubbleWrap 0.9.0 and `ro-bind-fd`; it predates the fixed startup
scope and runtime/UI integration and does not establish those claims. A later
trusted-host run at `c0af7cda7bbbce3de289367af9e468ea8bea73df` measured 18/18
native cases, including a real nested-mount refusal, on Linux amd64/ext4 with
trusted BubbleWrap 0.9.0 and `ro-bind-fd`. This remains evidence for that recorded source/profile and does not exercise
this branch's shipping activation path. Other
filesystems/platforms, syscall interruption and general same-user isolation
remain unsupported or unestablished. See the
[lifecycle acceptance record](issue-321-lifecycle-acceptance.md),
[native acceptance procedure](issue-321-native-acceptance.md), and
[creation integration protocol](creation-integration-protocol.md).
The accepted scope/owner-coverage design is recorded in
[ADR-0009](../adr/0009-startup-directory-creation-scope.md): use the fixed CLI
startup directory and existing subdirectories, with required coverage for
components actually instantiated by the running chat. Ordinary existing
project content does not require a scope-wide inventory. The gated owner
profile now retains a startup directory descriptor and checks fixed-scope
identity, nested existing parents, protected-owner ancestry, and terminal
movement for creation proposals. The unmerged enablement change on this branch
uses those checks to gate schema registration to the trusted Linux amd64/ext4
profile with the operation-specific descriptor-bind worker. Owner coverage,
scope identity, and cleanup are still checked live; registration grants no
write authority. This branch is a review proposal, not a released capability.
The PR #355 candidate was run on a trusted Linux amd64/ext4 host with BubbleWrap
0.9.0 using `ro-bind-fd`: its original run and the corrected PR #356 follow-up
each measured 17 of 18 cases, with the mount-boundary case unavailable. A
later exact source `c0af7cda7bbbce3de289367af9e468ea8bea73df` run measured all
18 cases as passes, including refusal across a real nested mount (scope mount
ID 33, nested mount ID 157). The aggregate was `scoped_native_pass`. Source
presence and Go/race checks alone do not establish native enforcement. A fresh
native run must exercise the newly wired shipping activation path before this
change is ready to merge. See the
[owner-profile foundation](issue-321-owner-profile.md) and
[native acceptance record](issue-321-native-acceptance.md#nested-mount-boundary-follow-up).

## Runtime and UI reasons

The runtime snapshot carries availability for all three capabilities through
the UI client contract. Reasons distinguish an unknown or unsupported provider
route from missing host backend, missing authorization/enforcement composition,
and a capability not registered in the current build. A route that supports
tool protocol is not thereby authorized to execute tools. The compact header
shows a concise availability reason, and `/help` shows the full sanitized
reason and recovery guidance where one is known. Unknown provider support stays
unavailable; the runtime does not switch routes or billing methods.

Availability text is safe for display and excludes credential values, account
details, and private paths. Backend diagnostics can contain private paths, so
the UI reports generic setup guidance. Authorization, exact-target once
approval, and sandbox enforcement remain authoritative at dispatch time.

## Evidence and remaining work

- Provider route declarations: `plugins/model-provider/openai/provider.go` and
  `plugins/model-provider/anthropic/provider.go`.
- Tool protocol and continuation coverage: provider `tool_protocol_test.go`,
  `provider_test.go`, and `plugins/model-provider/anthropic/tool_protocol_test.go`.
- Runtime fail-closed dispatch and availability: `internal/harness/runtime/tool_loop.go`
  and `internal/harness/runtime/chat.go`.
- UI projection and display: `internal/harness/ui/adapter.go` and
  `plugins/ui/tview/ui.go`.
- Live ChatGPT OAuth plan and clarification tool behavior was verified on
  public model `gpt-6-sol` for one account on 2026-11-12 using synthetic
  content and a temporary development build. Model selection is limited to the
  selected OAuth profile's account-specific catalog; other accounts with that
  model ID have not been separately live-verified. The only live approvals were
  Allow once for `update_plan` and `request_user_input`. Follow-up operator
  reports record an Allow once exact-file read, a canceled turn, and a denied
  exact-file request on Linux amd64 with Bubblewrap 0.9.0. The denied read was
  not executed or disclosed, and no fallback was observed in allowed, canceled,
  or denied flows. Automated runtime fixtures are separate evidence. Live
  coverage remains limited to one account, model, and host. No Anthropic subscription integration
  is implemented. Any future route must retain explicit capability declarations
  and tests before it is selectable.
