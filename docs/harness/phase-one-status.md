# Phase-one harness status

## Implemented in this PR

- Internal packages in the existing KompoSyn Go module; no nested module.
- Shared provider contract with OpenAI Responses and Anthropic Messages SDK
  adapters, stream deltas, provider-native replay data, tool correlation, stop
  outcomes, and token/cache usage.
- Explicit environment credential references; credential values are never
  persisted.
- Persistent configured-model selection, sessions, conversation history, task
  state, and root `AGENTS.md` metadata in a harness-owned SQLite database.
- Sequential headless tool loop with explicit task completion and per-call
  approval.
- SDK MCP client/server protocol in-process, exposing only `list_files` and
  `read_file`; the canonical workspace root, traversal, output size, and call
  duration are enforced.
- Pending operation identity and completed read results are persisted.
  Interrupted turns require reconciliation and are never replayed automatically.
- tview model add/list/select, task and session commands, inline approval,
  instruction provenance, and session token summary.
- Optional Token Logger adapter using the public Core v0.9.0 API. It records
  usage and identity references without conversation content.
- Fake-HTTP provider tests and offline MCP/runtime/restart tests, including a
  write through the actual released Core store API. No live model request was
  made.

## Remaining before complete plan acceptance

- Queue and event snapshot/attach APIs; richer turn and operation
  history/inspection.
- Nested instruction refresh is not integrated before tool execution; only the
  root `AGENTS.md` is loaded at session creation.
- A broader provider/tool fault matrix, including malformed/unknown content and
  cancellation at each batch position, still needs coverage.
- OpenAI and Anthropic have independent fake-HTTP tests; a full dual-provider
  end-to-end MCP workflow is not automated for both.
- Add-model accepts explicit model IDs but does not query catalogs or verify
  capabilities without a generation request.
- Full-screen terminal behavior has not been validated with a PTY or on
  macOS/Windows.
- Live provider smoke tests were not run; they require credentials and can
  incur charges.

## Offline validation

Validation passed on 2026-10-25 with Go 1.26.9: `make check` (formatting,
module verification, vet, and full test suite) and `go test -race ./...`. Full
terminal behavior was not exercised.
