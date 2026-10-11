# Komposyn Token Logger harness protocol

This document is the normative wire contract for `komposyn-lg harness`.
The protocol name is `komposyn.token-logger`, and the current protocol version
is `1`.

The harness is a line-oriented sidecar. It reads one JSON request per line
from stdin and writes one JSON response per request to stdout. Protocol
responses are the only stdout output. Human-readable and sanitized diagnostics
are written to stderr.

## Transport and message limits

- Transport is UTF-8 newline-delimited JSON (JSONL).
- A blank or whitespace-only line is ignored and produces no response.
- A message is one input line, including its JSON object and terminating
  newline. The maximum scanner token is 8 MiB (`8 * 1024 * 1024` bytes).
- A line exceeding the 8 MiB scanner limit is a fatal transport error. The
  harness writes a sanitized diagnostic to stderr and exits with status `2`;
  it does not emit a protocol error response for that line.
- Requests may contain unknown JSON fields; the implementation ignores fields
  it does not use.

## Common envelope

Every request is an object with this shape:

| Field | JSON type | Required | Meaning |
| --- | --- | --- | --- |
| `protocol` | string | No | `komposyn.token-logger`. An omitted value is accepted. A non-empty different value returns `unsupported_protocol`. |
| `version` | integer | No | `1`. An omitted or zero value is accepted. A non-zero value other than `1` returns `unsupported_version`. |
| `id` | string | No | Caller correlation identifier. It is echoed in the response when non-empty. |
| `method` | string | Yes | One of `initialize`, `record`, `flush`, `health`, or `shutdown`. |
| `params` | object | Method-dependent | Parameters for the method. |

The canonical request form is:

```json
{"protocol":"komposyn.token-logger","version":1,"id":"1","method":"health"}
```

Every response has this shape:

```json
{"protocol":"komposyn.token-logger","version":1,"id":"1","ok":true,"result":{}}
```

| Field | JSON type | Meaning |
| --- | --- | --- |
| `protocol` | string | Always `komposyn.token-logger`. |
| `version` | integer | Always `1`. |
| `id` | string | Echoed when the request supplied a non-empty ID; omitted otherwise. |
| `ok` | boolean | `true` for success and `false` for a protocol/application error. |
| `result` | object | Method result on success. Omitted on failure. |
| `error` | object | Failure object on error; omitted on success. It contains `code` and `message`. |

Error messages are human-readable and may change. Error codes are the stable
part of the error contract.

## Lifecycle

The process starts uninitialized. A client should call `initialize` before
recording or flushing:

```text
start → initialize → record* / flush / health → shutdown → process exits
```

- `record` and `flush` before a successful `initialize` return
  `not_initialized`.
- `health` is valid before initialization and returns
  `{"status":"uninitialized"}`.
- `shutdown` is valid before initialization, returns `stopped: true`, and
  exits successfully.
- A successful `initialize` opens the selected database. Calling it again
  opens the new selection and closes the previous store.
- A successful `shutdown` closes the store, returns `stopped: true`, and ends
  the process with exit status `0`. No response is emitted for subsequent
  input because the process has exited.
- Normal stdin EOF ends the process with exit status `0` after processing all
  complete lines. EOF itself does not produce a response.
- A fatal input read or output encoding error writes a sanitized diagnostic to
  stderr and exits with status `2`.

## Methods

### `initialize`

Request parameters are optional:

```json
{"method":"initialize","id":"1","params":{"data_dir":"/var/lib/komposyn"}}
```

`params` may be omitted or `null`. `data_dir` is optional. Selection order is:

1. `params.data_dir`, when non-empty;
2. `TOKEN_LOGGER_DATA_DIR`;
3. the platform default data directory.

The database is `token-logger.db` within the selected directory. On success,
the result is:

```json
{
  "plugin_id":"token-logger",
  "display_name":"Komposyn Token Logger",
  "plugin_version":"0.1.0",
  "schema_versions":[1,2,3,4],
  "capabilities":["usage-events","local-reports"],
  "data_dir":"/var/lib/komposyn"
}
```

`plugin_version` is the running library version and can change between
releases. The current schema manifest reports every supported schema version,
1 through 4. Initialization errors return `invalid_params` for malformed
parameters or `initialize_failed` when the store cannot be opened.

### `record`

The normal parameters object is:

```json
{
  "event": {"event_id":"event-1", "schema_version":1, "event_type":"model.request", "occurred_at":"2026-10-11T12:00:00Z", "client":{"name":"example"}, "provider":"openai", "requested_model":"gpt-test", "usage":{"input_tokens":10,"output_tokens":5}, "provenance":{"adapter":"fixture","source_id":"source-1","source_record":"record-1"}, "ingestion_key":"source-1:record-1"},
  "project": {"id":"project-1","root":"/work/project","display_name":"Example"},
  "session": {"id":"session-1","project_id":"project-1","client":{"name":"example"}}
}
```

For compatibility, `params` may also be a `UsageEvent` object directly. The
normal object form is recommended for new clients because reference metadata
can only be supplied in that form.

On success, the result is:

```json
{"imported":1,"duplicates":0}
```

`imported` is the number of newly persisted events (zero or one for this
method), and `duplicates` is the number rejected by the stable ingestion key
(zero or one). Retrying the same event with the same `ingestion_key` is
therefore safe.

The event and any supplied project/session references are persisted in one
transaction. Reference rules are:

- If `event.project_id` is set, `project` is required and its `id` must match.
- If `event.session_id` is set, `session` is required and its `id` must match.
- If `session.project_id` is set, it must match `event.project_id` when the
  event has one, and matching `project` metadata is required.
- A referenced project must have a non-empty `id` and `display_name`.
- A referenced session must have a non-empty `id` and `client.name`.
- Project/session timestamps, when present, are RFC 3339 JSON timestamps.

If the request cannot be decoded, `invalid_params` is returned. A decoded
event that fails library validation, or reference metadata that fails storage
validation, returns `record_failed`; the append transaction is rolled back.
Missing or mismatched references return the stable codes listed below.

### `flush`

No parameters are required. After initialization, the result is:

```json
{"durable":true}
```

Record operations are synchronous, so a successful `flush` confirms that
prior appends have been committed. Before initialization it returns
`not_initialized`.

### `health`

No parameters are required. Before initialization:

```json
{"status":"uninitialized"}
```

After initialization:

```json
{"status":"healthy","event_count":1,"data_dir":"/var/lib/komposyn"}
```

`event_count` is the durable event row count, including previously persisted
events and excluding duplicate record attempts. A database query failure
returns `health_failed`.

### `shutdown`

No parameters are required. The result is:

```json
{"stopped":true}
```

This is also the successful result before initialization. A close failure
returns `shutdown_failed` and the process remains running so the caller can
inspect the error or retry shutdown.

## Data shapes and validation

The following fields are the JSON representation used by `record`. Optional
fields are omitted when empty or nil. JSON timestamps are RFC 3339 values;
event timestamps must carry UTC (`Z`), as required by the library validator.

### `UsageEvent`

| Field | Type | Constraint |
| --- | --- | --- |
| `event_id` | string | Required and non-blank. |
| `schema_version` | integer | Required and positive. |
| `event_type` | string | Required and non-blank. |
| `occurred_at` | timestamp | Required, non-zero, UTC. |
| `started_at` | timestamp | Optional; UTC. |
| `completed_at` | timestamp | Optional; UTC and not before `started_at`. |
| `project_id` | string | Optional reference ID. |
| `session_id` | string | Optional reference ID. |
| `request_id` | string | Optional. |
| `attempt` | integer | Optional; must not be negative. |
| `operation` | string | Optional. |
| `client` | `Client` | Required; `client.name` must be non-blank. |
| `provider` | string | Required and non-blank. |
| `requested_model` | string | Optional, but at least one of requested/resolved model is required. |
| `resolved_model` | string | Optional, but at least one of requested/resolved model is required. |
| `usage` | `TokenUsage` | All counts non-negative; cache counts must not exceed input; reasoning must not exceed output. |
| `cost` | `Cost` | Optional; see below. |
| `provenance` | `Provenance` | `adapter`, `source_id`, and `source_record` are required and non-blank. |
| `ingestion_key` | string | Required and non-blank; used for idempotency. |
| `attributes` | JSON value | Optional; when present, must contain valid JSON. |

`TokenUsage` contains `input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`, and `reasoning_tokens`, all integer counts.

`Cost` contains `status`, optional `amount_micros`, `currency`,
`pricing_version`, optional `provider_amount`, and `provider_currency`.
`status` is empty or one of `calculated`, `reported`, and `unknown`.
`unknown` must not include `amount_micros`; a supplied amount must not be
negative.

`Client` contains required `name` and optional `version` and `mode`.
`Provenance` contains required `adapter`, `source_id`, and `source_record`,
plus optional `source_offset` and `record_hash`.

### `Project`

```json
{"id":"project-1","root":"/work/project","display_name":"Example"}
```

`id` and `display_name` are required and non-blank when project metadata is
persisted. `root` is optional and may be empty.

### `Session`

```json
{"id":"session-1","project_id":"project-1","client":{"name":"example"},"started_at":"2026-10-11T12:00:00Z","ended_at":"2026-10-11T12:30:00Z","source_metadata":{"adapter":"fixture"}}
```

`id` and `client.name` are required and non-blank when session metadata is
persisted. `project_id`, `started_at`, `ended_at`, and `source_metadata` are
optional. `source_metadata` is an opaque JSON value supplied by the caller.

## Stable error catalog

All listed errors use `ok: false` and an `error` object. The response keeps
the request ID when one was supplied.

| Code | Returned when |
| --- | --- |
| `invalid_json` | A non-blank input line is not valid JSON. The server continues reading subsequent lines. |
| `unsupported_protocol` | A non-empty `protocol` differs from `komposyn.token-logger`. |
| `unsupported_version` | A non-zero `version` differs from `1`. |
| `unknown_method` | `method` is not one of the five supported methods. |
| `invalid_params` | Method parameters cannot be decoded. |
| `not_initialized` | `record` or `flush` is requested before successful initialization. |
| `initialize_failed` | The requested database cannot be opened or migrated. |
| `missing_project_metadata` | An event or session refers to a project without matching project metadata. |
| `invalid_project_metadata` | Project metadata ID does not match the event/session reference. |
| `missing_session_metadata` | An event refers to a session without matching session metadata. |
| `invalid_session_metadata` | Session metadata ID or project reference does not match the event/reference. |
| `record_failed` | The event/reference transaction cannot be persisted. |
| `health_failed` | The event count cannot be queried. |
| `shutdown_failed` | The initialized store cannot be closed. |

Exact human-readable messages are implementation details and are not stable.
Diagnostics for decode, persistence, close, read, and encode failures are
sanitized before being written to stderr.

## Malformed input, stderr, and process behavior

Malformed JSON receives a response with an empty/omitted ID, for example:

```json
{"protocol":"komposyn.token-logger","version":1,"ok":false,"error":{"code":"invalid_json","message":"request is not valid JSON"}}
```

The process continues after this recoverable request error. A blank line is
silently skipped. Clean EOF exits `0`; oversized messages, scanner/read
failures, and response-encoding failures exit `2` after a sanitized stderr
diagnostic. `shutdown` exits `0` after its success response.

stdout is machine-readable protocol output only. Clients must not parse stderr
as protocol data. The harness does not print logs, progress messages, or
terminal UI text to stdout.
