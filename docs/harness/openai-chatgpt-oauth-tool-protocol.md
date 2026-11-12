# OpenAI ChatGPT OAuth client tool protocol assessment

**Research checked:** 2026-11-12. The OpenAI documentation pages below do not
show a publication or last-updated date in the rendered content; this date is
the date they were checked. For this verification attempt, an operator
authorized up to five live requests using synthetic content. No app session or
already-authorized test account/configuration was available in the execution
environment, so no account was accessed and zero live provider requests were
sent. No credential material or auth-store contents were inspected.

## Route and scope

KompoSyn's OpenAI OAuth adapter uses the public Responses endpoint at
`https://api.openai.com/v1/responses`, sends the OAuth access token as a bearer
credential, and separately uses the account model catalog. OpenAI's Sign in
with ChatGPT (SIWC) open-source plan-usage documentation specifies this same
endpoint and bearer-token route for eligible requests. It explicitly says not
to use ChatGPT `backend-api` endpoints. The documented scope is eligible
ChatGPT plan-usage requests through SIWC, not arbitrary OAuth tokens,
ChatGPT's private interface, or all OpenAI models/accounts.

OpenAI's current SIWC preview limitations say requests on this route must set
`store: false` and `stream: true`, include the needed context in `input`, and
omit `max_output_tokens` and `previous_response_id` over HTTP. The model must
be available to the signed-in account. The SIWC catalog's presence or a
successful text response alone does not establish tool support for a model.

## Capability findings

| Capability | Finding | Evidence and scope |
| --- | --- | --- |
| Outbound client function definitions | Documented protocol support, subject to account/model eligibility | SIWC preview limitations say function/custom tools are supported when grouped in namespaces or supplied as `additional_tools` input items. This adapter uses `additional_tools`; it does not infer support from model names. |
| Streamed argument assembly and completion | Documented Responses protocol; fixture covered | The Responses streaming reference defines `response.function_call_arguments.delta` and `.done`; completed output items carry function call identity and finalized arguments. SIWC supports streamed Responses on the public route. |
| Correlated result submission | Documented Responses protocol; fixture covered | Function calling documentation defines `function_call_output` correlated by `call_id`, followed by another Responses request. |
| Provider-native continuation | Documented HTTP behavior; fixture covered | SIWC says not to use `previous_response_id` over HTTP and to send required history in `input`; the function calling guide shows replaying the assistant response and tool output. KompoSyn retains/replays native assistant output items and sends the correlated result. |
| OAuth request differences | Documented | Set `store:false`, `stream:true`; omit `max_output_tokens`; use `additional_tools` for client function definitions on this route. Current API-key requests retain top-level `tools`, `max_output_tokens`, and `include: ["reasoning.encrypted_content"]`. The OAuth adapter does not send these API-key-only fields. |
| Model-specific tool support | Unverified by this assessment | SIWC instructs clients to use models available to the signed-in account, but the reviewed documentation does not establish per-model support for KompoSyn's exact function set. No model was tested live. |
| Live ChatGPT OAuth round trip | Unverified | No account access or live request was authorized or attempted. Fixtures validate adapter behavior against documented shapes only. |

The SIWC limitations separately list unsupported built-in tools including
image generation, file search, Code Interpreter, native computer use, hosted
MCP/connectors, and Responses `tool_search`; they also say
`programmatic_tool_calling` is not accepted in top-level `tools`. KompoSyn's
current client functions are ordinary host-executed function calls, not these
built-in or programmatic tools. No broader restriction is inferred from those
items.

The official docs do not state that all tools work for every eligible model or
account, nor do they document a specific restriction on KompoSyn's ordinary
client functions. Therefore provider capability remains **unverified** in the
runtime declaration, not provider-restricted. This assessment establishes the
wire protocol and a candidate adapter route only. It is not live provider
compatibility evidence.

## Fixture contract and adapter seam

The executable private protocol fixture at `plugins/model-provider/openai/tool_protocol_test.go`
uses the existing OpenAI adapter HTTP/SSE seam. It covers OAuth bearer auth,
`additional_tools` placement, streaming function-call argument assembly,
call ID correlation, a `function_call_output`, and a second response. It also
checks OAuth request flags and confirms `max_output_tokens` and top-level
`tools` are absent. The negative fixture supplies a non-completed/incomplete
function call and asserts it yields no dispatchable call. These fixtures
exercise a protocol shape from official documentation; they do not assert
that OpenAI accepted it or that the public route is available. Public
`Plugin.StreamChat` rejects OAuth tool-bearing requests before network dispatch
while `RouteCapabilities(AuthOAuth)` remains `SupportUnknown`. The private
protocol implementation exists to exercise the adapter fixture.

The adapter's private protocol implementation encodes client function
definitions in a developer `additional_tools` input item, keeps them available
on the continuation request, and replays Responses-native assistant items
plus `function_call_output` values in full HTTP input history. Public OAuth
dispatch remains text-only until route-specific live evidence verifies the
full tool cycle. Keep authorization, execution, and result production in the
existing host runtime. API-key request encoding remains unchanged.

## Live verification still required

Before changing OAuth `ClientTools` from unknown or exposing definitions in
the runtime, live validation must use the already-authorized app account/config
and stay within its agreed request budget under issue #272's process. The
authorization for this attempt allowed at most five requests with synthetic
content, but this environment exposed no authorized app session or test
account/config. No login flow was started, and zero live provider requests
were sent. The concrete prerequisite is an operator-provided app session with
an already-authorized eligible account and an account-available model.

When that prerequisite is available, validate a complete call/result/
continuation journey and record route, public model identifier, date, and
sanitized outcomes without credentials, private paths, or conversation
content. Record any provider rejection with its exact route/model scope and
sanitized error code. Until then, the verification gate stays unresolved and
the capability stays unknown.

## Sources

All sources are first-party OpenAI developer documentation, checked 2026-11-12:

1. [Sign in with ChatGPT: Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference) — OAuth bearer route, public endpoint, account model catalog, streaming and completion requirements.
2. [Sign in with ChatGPT: Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations) — SIWC route request fields, function/custom tool support surfaces, explicitly unsupported tool classes and field exclusions.
3. [Responses API: Function calling](https://developers.openai.com/api/docs/guides/function-calling) — function-call flow, output items, `call_id` correlation, result submission and continuation.
4. [Responses API: Streaming events reference](https://developers.openai.com/api/reference/resources/responses/streaming-events) — function-argument delta/done events and output item lifecycle.
5. [Responses API: Tool search](https://developers.openai.com/api/docs/guides/tools-tool-search) — `additional_tools` item shape, developer role and ordered input-history behavior.
