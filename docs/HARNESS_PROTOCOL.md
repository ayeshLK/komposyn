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
- `record`: accepts `params.event`, a normalized `UsageEvent`, plus optional
  matching `params.project` and `params.session` reference metadata. Reference
  metadata and the event are committed atomically before the response. An
  event or session that names a project requires matching project metadata in
  the same request; a session that names a session requires matching session
  metadata.
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
