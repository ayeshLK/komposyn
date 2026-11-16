# Temporary chat capability availability

Evidence reviewed 2026-11-15. This matrix describes the bundled temporary
chat routes and its current four capabilities. “Supported” describes the
provider protocol implementation and fixture-backed contract; it does not
grant permission to execute a tool. Runtime availability also requires the
host authorization, approval, and enforcement composition. No capability
selection changes the provider, profile, authentication method, or billing
route.

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
| `search_files` | Unavailable: search-specific retained candidate filtering, worker protocol/seccomp, and native acceptance are not implemented | Unavailable for the same search-specific host requirements; listing evidence does not enable search | Unavailable for the same search-specific host requirements | Unimplemented route |

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

`search_files` remains unavailable on every route. The request schema,
approval binding, host candidate validation and pathless sealed-snapshot worker
protocol are implemented behind an independent `SearchFilesAvailable` gate
that defaults closed. Search traversal and content workers use separate
operation-specific seccomp profiles. No search native acceptance has been run
or reviewed, so these implementation checks do not establish filesystem
enforcement. Existing read and listing evidence is not search evidence.
Enablement still requires search-specific native acceptance on the exact
supported profile and independent review. The Stage 6 overlay now contains
search-specific mount-boundary and lifecycle cases, but this host refused
before worker launch because its Bubblewrap binary is not trusted; that is
unavailable evidence. See [the search design
note](search-files-design.md) and [#318 completion matrix](issue-318-search-completion.md)
for limits, current coverage and remaining evidence.

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
