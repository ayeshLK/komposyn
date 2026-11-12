# Temporary chat capability availability

Evidence reviewed 2026-11-12. This matrix describes the bundled temporary
chat routes and its current three capabilities. “Supported” describes the
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
| OpenAI ChatGPT OAuth (SIWC) | Implemented; listed by `openai.Plugin.AuthMethods` | `RouteCapabilities(AuthOAuth)` returns `SupportUnknown`. A private OAuth protocol implementation encodes `additional_tools`; its fixture covers declaration, streamed calls, result correlation, and continuation; see [the protocol assessment](openai-chatgpt-oauth-tool-protocol.md). Public `Plugin.StreamChat` rejects OAuth tool-bearing requests before network dispatch. | Protocol shape is established by official docs and fixture, while live account/model compatibility and public runtime exposure remain unverified. Keep text-only and fail closed until route-specific live evidence verifies the full tool cycle. Unknown support is not a provider restriction. |
| Anthropic API key | Implemented; listed by `anthropic.Plugin.AuthMethods` | Messages tool declaration, `tool_use` parsing, result continuation, and protocol tests in `plugins/model-provider/anthropic/` | Supported for `update_plan` and `request_user_input`; `read_file` is conditional on host prerequisites below. Continue live provider validation. |
| Anthropic subscription / OAuth | No authentication integration or selectable route exists in the bundled adapter; `AuthMethods` returns only `AuthAPIKey` | No adapter or route evidence | Unimplemented. Do not create or imply a selectable profile. No claim is made that Anthropic prohibits such integration. |

The app only offers authentication methods returned by the registered provider.
An unavailable integration is documented here rather than represented as a
profile a user can select. OpenAI OAuth's unknown capability is distinct from
Anthropic's absent subscription integration.

## Capability matrix

Older “two conversation tools” descriptions refer specifically to
`update_plan` and `request_user_input`. The later conditional Linux
`read_file` milestone adds a third capability under separate host and
approval prerequisites; it does not broaden those two tools or grant general
filesystem access.

| Capability | OpenAI API key | OpenAI ChatGPT OAuth | Anthropic API key | Anthropic subscription / OAuth |
|---|---|---|---|---|
| `update_plan` | Supported; runtime authorization and approval required | Unverified; unavailable | Supported; runtime authorization and approval required | Unimplemented route |
| `request_user_input` | Supported; runtime authorization and approval required | Unverified; unavailable | Supported; runtime authorization and approval required | Unimplemented route |
| `read_file` | Conditional: supported client-tool route plus trusted Linux backend and complete host enforcement | Unverified route; unavailable | Conditional: supported client-tool route plus trusted Linux backend and complete host enforcement | Unimplemented route |

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
- Live account/provider validation remains outstanding. ChatGPT tool support
  remains unverified until a route-specific tool cycle is evidenced. No
  Anthropic subscription integration is implemented. Any future route must
  retain explicit capability declarations and tests before it is selectable.
