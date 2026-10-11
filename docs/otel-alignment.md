# OpenTelemetry GenAI alignment audit

KompoSyn Token Logger stores normalized usage records from local AI-client
transcripts. It does **not** instrument model calls, emit OpenTelemetry spans or
metrics, or export OTLP. Its JSONL `harness` sidecar accepts KompoSyn
`UsageEvent` records, not OpenTelemetry telemetry. Similar field names alone
do not make these formats interoperable.

This audit compares the current [library event model](../plugins/library/token-logger/tokenlogger.go),
[Codex adapter](../plugins/library/token-logger/codex.go),
[Claude Code adapter](../plugins/library/token-logger/claude.go),
[SQLite schema](../plugins/library/token-logger/migrations.go), and
[sidecar contract](HARNESS_PROTOCOL.md) with the OpenTelemetry GenAI
[client-inference conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/6fd0d763a092db245a54104fc1124031eb3c51d2/docs/gen-ai/client-inference.md)
at revision `6fd0d763a092db245a54104fc1124031eb3c51d2` (2026-10-09).
Those conventions are marked *Development*; this is a point-in-time comparison,
not a conformance or compatibility claim.

## Field correspondences, not emitted OTel attributes

The names in the right column are OpenTelemetry **span attributes**. The local
fields in the left column are stored as `usage_events` columns and exposed in
`record.params.event`; the sidecar does not translate or emit them as OTel
attributes. Counts are integers. A zero in the KompoSyn record may mean either
zero usage or that the source did not provide a breakdown.

| KompoSyn `UsageEvent` field | Nearest OTel GenAI attribute | What is actually known |
| --- | --- | --- |
| `usage.input_tokens` | `gen_ai.usage.input_tokens` | Intended total input including cache subsets. Codex uses its reported input total; Claude Code adds ordinary, cache-creation, and cache-read input counts. A `record` caller supplies its own value. The billing-vs-consumption meaning is not independently verified. |
| `usage.output_tokens` | `gen_ai.usage.output_tokens` | Adapter-reported output count; the sidecar accepts caller-supplied counts. Billing-vs-consumption meaning is not independently verified. |
| `usage.cache_read_tokens` | `gen_ai.usage.cache_read.input_tokens` | Input subset. Claude Code uses `cache_read_input_tokens`; Codex adds `cached_input_tokens` and `cache_read_input_tokens` when present. The adapter does not independently prove that those Codex source fields are disjoint. |
| `usage.cache_write_tokens` | `gen_ai.usage.cache_write.input_tokens` | Input subset. Claude Code uses `cache_creation_input_tokens`; Codex uses `cache_write_tokens` when present. |
| `usage.reasoning_tokens` | `gen_ai.usage.reasoning.output_tokens` | Output subset populated from Codex `reasoning_output_tokens` when present; the Claude Code adapter does not populate it. |
| `requested_model` | `gen_ai.request.model` | A possible analogue only when the actual requested model is known. Both adapters currently copy the transcript's session/message model into this field. |
| `resolved_model` | `gen_ai.response.model` | **Not a verified response-model mapping.** Both adapters copy the same model value used for `requested_model`; they do not independently observe the serving model. |
| `provider` | `gen_ai.provider.name` | **Not a direct mapping.** The adapters set `openai` for Codex and `anthropic` for Claude Code; a sidecar caller may supply another value. OTel defines this attribute as the instrumentation's provider/telemetry-format discriminator, which may differ from the upstream provider. |
| `session_id` | `gen_ai.conversation.id` | A possible correlation analogue, but a local client session is not guaranteed to be one OTel conversation. |
| `operation` | `gen_ai.operation.name` | **Not mapped.** It is optional and unrestricted; the adapters leave it empty. `event_type = "model.request"` is a KompoSyn event type, not an OTel operation name. |

The first five count correspondences are closest to OTel's total/subset
relationships: cache reads and writes belong within input, and reasoning
belongs within output. The library validates those subset bounds. Codex
imports derive per-record deltas from cumulative counters; Claude Code imports
message usage. Neither adapter observes the duration of the model call.

## Deliberate gaps and local extensions

- `event_id`, `schema_version`, timestamps, project/session references,
  ingestion key, client details, cost and pricing, and source provenance are
  KompoSyn's local data model. They are not declared OTel attributes or an
  OTel extension namespace. The arbitrary `attributes` JSON field is also
  **not** an OTel attribute bag.
- There are no OTel span IDs, trace context, span kind/status, inference
  duration, or GenAI metric instruments. Stored `occurred_at` and optional
  start/completion times do not turn a usage record into an inference span.
- The adapters do not retain prompt or response content. This is an intentional
  privacy boundary, not an incomplete content-export feature.
- The versioned JSONL sidecar is named `komposyn.token-logger`; it is not OTLP
  or a general KompoSyn plugin protocol. See its [normative contract](HARNESS_PROTOCOL.md).

## Before claiming interoperability

Any future OTel export or convention claim needs a separate design decision:
choose a target convention revision and signal type; define per-adapter
provider/model and token-count semantics; decide which local-only fields, if
any, merit a namespaced extension; preserve content minimization; and test the
resulting payloads against that pinned contract. No such exporter or extension
is proposed by this audit.
