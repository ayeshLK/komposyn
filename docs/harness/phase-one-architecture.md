# Experimental phase-one harness architecture

## Repository placement

The phase-one harness lives in the existing `github.com/ayeshLK/komposyn`
module, under `internal/harness/`, with its experimental terminal entrypoint in
`cmd/komposyn-harness/`. It is not a nested module and does not define an
independently versioned or supported public API. This placement follows the
maintainer direction for this PR; it does not establish a universal plugin
runtime or change the Core/CLI ownership contract for other executable
behavior.

The implementation is an experiment alongside the reusable Token Logger API.
It uses Core's public Token Logger package only for optional usage accounting.
Conversation history and configured models live in a separate harness-owned
SQLite file. No Token Logger schema or migration is changed.

## Package boundaries

```text
cmd/komposyn-harness/             tview entrypoint and local lifecycle
internal/harness/model/           provider-neutral request and turn types
internal/harness/runtime/         sequential turns, approvals, recovery
internal/harness/provider/        OpenAI and Anthropic SDK adapters
internal/harness/auth/            explicit environment-reference resolver
internal/harness/mcpworkspace/    bounded read-only workspace MCP server/client
internal/harness/instructions/    bounded workspace AGENTS.md loader
internal/harness/store/            separate local harness state database
internal/harness/observe/          token-only Token Logger adapter
internal/harness/ui/tview/         runtime client and inline approvals
```

Provider-native content remains opaque to the runtime and is replayed only by
its owning adapter. MCP is the model tool boundary. The terminal UI invokes
runtime APIs and does not schedule provider requests itself. Implementations
are statically bundled; there is no dynamic plugin loader or marketplace.

## Safety and persistence

The selected workspace is canonicalized and opened with Go `os.Root`. The MCP
server exposes only `list_files` and `read_file`, with relative-path checks,
size limits, deadlines, and per-operation approval. Approval is not a sandbox.
The implementation does not provide writes, shell execution, arbitrary MCP
processes, model routing, or automatic tool replay.

Configured models persist only `env:VARIABLE_NAME` references. The harness
resolves the value immediately before a request. Harness history can include
sensitive prompts and tool results, is stored in a separate owner-only local
directory, and is not encrypted by this implementation. Token Logger receives
usage, timing, provider/model, and stable identity references only.

Session turns are checkpointed locally. If a process stops while a turn or
operation is pending, the session is marked as needing reconciliation and the
runtime refuses to replay it automatically. This is a conservative recovery
limit; richer operation inspection and reconciliation remain future work.

## Scope

The current implementation is a bounded phase-one experiment. It is not a
compatibility promise for future harnesses. See [phase-one status](phase-one-status.md)
for working behavior, validation, and remaining acceptance gaps. The project
vision and architecture continue to distinguish this slice from a general
plugin API or distribution model.
