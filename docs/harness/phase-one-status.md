# Phase-one acceptance status

Scope source: the latest phase-one decision on [issue #155](https://github.com/ayeshLK/komposyn/issues/155#issuecomment-6395039680).
Evidence below refers to deterministic tests and source paths in this branch.

| Acceptance area | Status | Evidence / limit |
| --- | --- | --- |
| Existing module and executable named `komposyn` | Complete | `cmd/harness/`; documented `go build -o /tmp/komposyn ./cmd/harness`; no nested module. |
| OpenAI and Anthropic share a provider contract | Complete | `plugins/model-provider/provider.go`; fake HTTP tests cover OpenAI Responses, Anthropic Messages, auth headers, pagination, native replay, and typed HTTP failures. |
| Provider profile credentials and schemas | Complete | `internal/harness/auth/`, `schema/harness-auth.schema.json`; API-key and explicit env-reference profiles, OpenAI OAuth registrations in the unreleased schema v1, secret-free metadata, serialized refresh, lifecycle generation checks, and private locked atomic persistence. |
| OpenAI ChatGPT OAuth sign-in and plan usage | Implemented; live verification pending | Loopback callback, PKCE, OIDC validation, cancellation, token refresh/revocation, visible-model discovery, and SIWC Responses subset are covered by fake HTTP and cryptographic tests. No live account or billable inference was used. |
| Settings schema and defaults | Complete | `internal/harness/settings/`, `schema/harness-settings.schema.json`; defaults, invalid values, version errors, and preservation of unrelated fields covered. |
| In-memory temporary chat and cancellation | Complete | `internal/harness/runtime/chat.go`; fake-provider tests cover multi-message history, reset confirmation, cancellation, late callbacks, safe follow-up, and sanitized errors. |
| UI plugin boundary and filterable `/model` picker | Partial | tview is under `plugins/ui/tview/`; a simulated-screen test opens the actual picker, checks actions/model rows, and selects a model. Masked onboarding/management flows are implemented but do not yet have a full input-driven end-to-end UI test. |
| No database/workspace/session dependency | Complete | `cmd/harness/` opens only auth/settings stores and the UI; source has no Store/SQLite/MCP/Token Logger imports. Headless `--check` is available. |
| Model compatibility and output/context limits | Partial | Provider catalogs are correctly marked unknown; no capability is inferred from model names. API-key output is bounded to 4096 tokens; the SIWC OAuth preview request omits `max_output_tokens`, so that bound does not apply on the OAuth route. Provider context limits are not available from list APIs. |
| OpenAI model-list pagination | Complete for current endpoint | Current OpenAI list endpoint returns a model list without a page cursor; the adapter decodes the complete result. |
| Discovery partial failures and profile identity | Complete | `internal/harness/model/`; provider/profile/model tuple avoids account collisions and partial failures are represented by profile. |
| Request options, tools, multimodal, automatic routing | Deferred by plan | Only text, minimal system instruction, API-key output limit, stream, and cancellation are sent. The SIWC OAuth preview route omits `max_output_tokens`. Tools are not advertised or executed. |
| Windows private credential ACL | Implemented; hosted validation pending | Windows-specific DACL, lock, no-reparse open, and replacement paths are present. Local test execution is Linux; CI must validate Windows. |
| Provider live verification | Not tested | No credentials were used. Fake HTTP tests do not claim real-provider connectivity. |
| Both-provider UI walkthrough through a real provider | Not tested | Offline adapter/runtime/UI components are tested separately. A live/billable walkthrough requires explicit opt-in. |

Remaining work is limited to broader onboarding/management UI automation and
hosted platform validation; do not treat the partial or untested rows as
complete. See [user/build instructions](README.md) and
[security notes](phase-one-security.md).
