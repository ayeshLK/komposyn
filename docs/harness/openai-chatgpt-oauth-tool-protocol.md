# OpenAI ChatGPT OAuth client tool protocol assessment

**Research checked:** 2026-11-12. The OpenAI documentation pages below do not
show a publication or last-updated date in the rendered content; this date is
the date they were checked. The operator authorized up to five live requests
with synthetic content. A later bounded verification used the existing
ChatGPT OAuth profile; see [live evidence](#live-verification-2026-11-12).
No credential material or auth-store contents were inspected or recorded.

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
| Model-specific tool support | Verified for `gpt-6-sol` on one tested account; other accounts and models remain unverified | Runtime can select only models returned in the selected OAuth profile's account-specific catalog. The live cycle below exercised `gpt-6-sol` on one account; the same model ID on other accounts has not been separately live-verified. SIWC documentation requires a model available to the signed-in account but does not provide a compatibility guarantee for every account/model. |
| Live ChatGPT OAuth round trip | Verified for `gpt-6-sol` on one tested account and route | A bounded synthetic live cycle exercised `update_plan` and `request_user_input`, each approved with Allow once, followed by correlated result submission and a follow-up. Live denial/cancellation and silent-fallback checks were not performed; the bounded request budget was consumed, so those checks remain open under #294. Automated runtime fixtures are separate evidence and do not establish these live outcomes. Exact-file reading was also not exercised; see the separate read gate below. |

The SIWC limitations separately list unsupported built-in tools including
image generation, file search, Code Interpreter, native computer use, hosted
MCP/connectors, and Responses `tool_search`; they also say
`programmatic_tool_calling` is not accepted in top-level `tools`. KompoSyn's
current client functions are ordinary host-executed function calls, not these
built-in or programmatic tools. No broader restriction is inferred from those
items.

The official docs do not state that all tools work for every eligible model or
account, nor do they document a specific restriction on KompoSyn's ordinary
client functions. The live result establishes compatibility only for the
tested route, account, and model; it does not justify a provider restriction
or a claim that every catalog model supports tools. The normal runtime
declaration remains **unknown** until implementation #293 incorporates the
verified route behavior without exposing unverified capabilities. Exact-file
live acceptance remains incomplete under #294.

## Fixture contract and adapter seam

The executable protocol fixture at `plugins/model-provider/openai/tool_protocol_test.go`
uses the existing OpenAI adapter HTTP/SSE seam. It covers OAuth bearer auth,
`additional_tools` placement, streaming function-call argument assembly,
call ID correlation, a `function_call_output`, and a second response. It also
checks OAuth request flags and confirms `max_output_tokens` and top-level
`tools` are absent. The negative fixture supplies a non-completed/incomplete
function call and asserts it yields no dispatchable call. These fixtures
exercise a protocol shape from official documentation; they do not assert
that OpenAI accepted it or that the public route is available. At the source
commit tested, public `Plugin.StreamChat` rejected OAuth tool-bearing requests
before network dispatch while `RouteCapabilities(AuthOAuth)` remained
`SupportUnknown`. Issue #293 adds model-aware capability checks: only
`gpt-6-sol` can advertise and send client tools on OAuth. Route-level OAuth
remains unknown, and unknown OAuth model IDs are rejected before network
dispatch. Runtime and adapter fixtures cover this model-scoped gate; the live
evidence below was gathered separately.

The adapter's protocol implementation encodes client function
definitions in a developer `additional_tools` input item, keeps them available
on the continuation request, and replays Responses-native assistant items
plus `function_call_output` values in full HTTP input history. Public OAuth
dispatch is allowed only for model ID `gpt-6-sol`; all other OAuth model IDs
remain fail-closed. Keep authorization, execution, and result production in the
existing host runtime. API-key request encoding remains unchanged.

## Live verification (2026-11-12)

The operator's existing ChatGPT OAuth profile was used without reading or
copying its credential data. The test ran on Linux amd64 with a temporary
development binary built from source commit
`27bbaac7cbb4d41773a053e5456a78bbe5ac0bc9`; its SHA-256 was
`6874018aa08b554bedfe057a678ba781dbe6fb74f080e3d489a515c7ee8e50a9`. For the
test only, a local uncommitted patch declared OAuth client tools supported and
removed the adapter's pre-network rejection. The normal installed binary and
source branch were not changed by that patch.

The selected OAuth profile's account-specific model catalog request succeeded.
Runtime can select only models returned in that catalog. On public model ID `gpt-6-sol`,
KompoSyn sent tool definitions and received `update_plan` and
`request_user_input` calls. Each action was approved once; a synthetic answer
was submitted, and the model returned a completed follow-up. The cycle stayed
within the authorized ceiling of five provider requests. The app's UI verified
the observed tool outcomes; no raw prompt, response, account identifier, or
credential was retained as evidence. This is live evidence for one account,
route, and model, in addition to the fixture evidence above. Other accounts
with the same model ID have not been separately live-verified, and this does
not establish support for every OpenAI OAuth model.

Both live tool approvals were Allow once: one each for `update_plan` and
`request_user_input`. Live denial/cancellation behavior and silent-fallback
behavior were not checked. The five-request budget was consumed by the bounded
cycle, so those live checks remain open under issue #294. Automated runtime
fixtures provide separate implementation evidence; they are not live checks.

The exact-file `read_file` journey was not attempted. The UI reported the
trusted file-read backend unavailable. Host inspection showed that the
installed Bubblewrap executable did not satisfy KompoSyn's root-owned,
non-writable trusted-executable prerequisite, so the worker was not admitted.
No file was opened or disclosed. To finish exact-file acceptance, run the live test on a
supported Linux setup with a trusted root-owned Bubblewrap executable and the
complete KompoSyn worker/enforcement composition, then repeat the once-approved
synthetic-file cycle. Until then `read_file` remains unavailable on this
route for this evidence record, and issue #294 stays open.

Native backend evidence is separate from this provider result. The earlier
reviewed #215/#278 Linux amd64/Bubblewrap 0.9.0 evidence covers the unchanged
read-enforcement path, but this run did not exercise that worker with OAuth.
It therefore does not satisfy the live `read_file` criterion.

## Sources

All sources are first-party OpenAI developer documentation, checked 2026-11-12:

1. [Sign in with ChatGPT: Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference) — OAuth bearer route, public endpoint, account model catalog, streaming and completion requirements.
2. [Sign in with ChatGPT: Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations) — SIWC route request fields, function/custom tool support surfaces, explicitly unsupported tool classes and field exclusions.
3. [Responses API: Function calling](https://developers.openai.com/api/docs/guides/function-calling) — function-call flow, output items, `call_id` correlation, result submission and continuation.
4. [Responses API: Streaming events reference](https://developers.openai.com/api/reference/resources/responses/streaming-events) — function-argument delta/done events and output item lifecycle.
5. [Responses API: Tool search](https://developers.openai.com/api/docs/guides/tools-tool-search) — `additional_tools` item shape, developer role and ordered input-history behavior.
