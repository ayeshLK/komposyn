# Harness protocol

`komposyn-lg harness` implements protocol `komposyn.token-logger`, version
`1`, over newline-delimited JSON on stdin/stdout. Every response includes the
protocol name and numeric version. stdout contains protocol responses only;
sanitized diagnostics are written to stderr.

Requests use this envelope:

```json
{"protocol":"komposyn.token-logger","version":1,"id":"1","method":"health"}
```

Supported methods are:

- `initialize`: accepts `params.data_dir` and returns the plugin manifest and
  selected data directory.
- `record`: accepts `params.event`, a normalized `UsageEvent`, and returns
  imported/duplicate counts. When the event has a `project_id` or `session_id`,
  include matching `params.project` and/or `params.session` metadata in the
  same request. The metadata is upserted before the event append, so retries
  are safe. A referenced project or session without matching metadata returns
  `missing_project_metadata` or `missing_session_metadata`.
- `flush`: returns `durable: true` after prior synchronous appends have been
  committed.
- `health`: returns status, event count, and data directory after initialize.
- `shutdown`: closes the database and returns `stopped: true`.

Responses have the form:

```json
{"protocol":"komposyn.token-logger","version":1,"id":"1","ok":true,"result":{}}
```

Failures set `ok` to `false` and include an `error` object with stable `code`
and human-readable `message` fields. The sidecar does not emit logs or
terminal UI text on stdout.
