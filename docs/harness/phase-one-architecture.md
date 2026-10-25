# Temporary chat architecture

This experimental phase-one slice connects provider plugins to a temporary
text chat. It does not implement the broader project/session/task architecture
from the KompoSyn vision or issue #155. The latest implementation scope is
[comment 6395039680](https://github.com/ayeshLK/komposyn/issues/155#issuecomment-6395039680).

```text
cmd/harness/                    explicit bundled registration, binary komposyn
plugins/model-provider/         experimental provider contract and types
plugins/model-provider/openai/  OpenAI Responses adapter
plugins/model-provider/anthropic/ Anthropic Messages adapter
plugins/ui/                     experimental UI lifecycle/client contract
plugins/ui/tview/               initial terminal UI and /model picker
internal/harness/auth/          credential profiles, secure files, resolution
internal/harness/auth/openai/   SIWC browser login, OIDC validation, token lifecycle
internal/harness/model/         profile-scoped discovery and selection identity
internal/harness/runtime/       one in-memory request and replay-safe chat
internal/harness/ui/            configured UI adapter
```

The provider contract depends on neither tview nor the concrete runtime. A
provider receives one resolved, typed credential and routing context for each
operation. The auth coordinator owns login attempts, protected token storage,
refresh, and disconnect; the OpenAI plugin owns model/inference HTTP formats.
The UI sees only public profile status and commands. No plugin receives the
credential store.

OpenAI API-key profiles use the public `GET /v1/models` and streamed
`POST /v1/responses` APIs. OpenAI ChatGPT OAuth profiles use the SIWC account's
`models[].slug` catalog and the same public Responses endpoint with the
documented OAuth subset. API-key requests retain encrypted reasoning and the
configured output bound; OAuth requests omit `max_output_tokens` and the
API-key-only reasoning include option. Both send `store: false`, `stream: true`,
and complete in-memory context. No private ChatGPT backend is used.
Anthropic uses cursor-based `GET /v1/models` and streamed `POST /v1/messages`.
Both stream parsers bound event size and map HTTP/network/cancellation results
to sanitized typed outcomes. They never advertise or execute tools. No
automatic request retry occurs.

The adapters follow the providers' public API references:
[OpenAI Responses create](https://developers.openai.com/api/reference/resources/responses/methods/create),
[OpenAI models list](https://developers.openai.com/api/reference/resources/models/methods/list),
[Anthropic Messages create](https://platform.claude.com/docs/en/api/messages/create),
and [Anthropic models list](https://platform.claude.com/docs/en/api/models/list).

Discovery identity is provider ID + credential profile ID + model ID. Model
capability is marked unknown when the provider catalog does not establish
text-chat support. Selection and discovery cache are process-local. Active
requests hold the original provider/model/profile and credential snapshot;
profile changes for that request are rejected. Replacing an API key changes an
opaque profile-version hash and invalidates model selection. An OAuth profile
has a stable registration revision and verified subject; rotating access and
refresh tokens and expiry do not change the profile version, model selection,
or conversation.

The coordinator keeps provider history separately from the UI turn list. Only
completed, replayable turns enter provider continuation history. Partial,
interrupted, truncated, unsupported, and failed turns stay visible for the
current process but are excluded from the next provider request. This avoids
resending malformed or ambiguous native history after cancellation.

The auth store and settings file are separate from the Token Logger SQLite
database. They use versioned JSON Schemas and runtime validation. The auth
schema remains at version 1 because this CLI format has not shipped; OAuth
extends its pre-release profile format. Existing API-key/environment profiles
remain unchanged. Credential updates serialize through a file lock and replace
through a private temporary file. OAuth lifecycle generations guard reconnect
and disconnect against stale credential writes, independently of the stable
profile revision used for chat identity. There is no chat database,
workspace requirement, SQLite open, session ID, MCP connection, approval card,
Token Logger usage event, or persistent model selection in this workflow.

The UI contract accepts a minimal headless client. The tview implementation
owns input editing, filter state, focus, display, and scrolling. Provider
authentication/discovery and inference are sent to background goroutines so
network work does not block the terminal event loop. On exit, the UI cancels
and joins in-flight work before returning.
