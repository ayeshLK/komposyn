# Experimental KompoSyn harness

The phase-one harness is an experimental local workflow in this repository.
Its implementation is under `internal/harness/` and its terminal entrypoint is
`cmd/komposyn-harness/`. It uses the existing Go module; it has no nested
module, stable public API, release process, or general plugin marketplace.

Start it from the repository root:

```sh
go run ./cmd/komposyn-harness --workspace /path/to/project
```

By default, private local state is stored under
`~/.local/share/komposyn-harness`. Use `--data-dir` or
`KOMPOSYN_HARNESS_DATA_DIR` to change that location. The harness stores
conversations in `harness.db` and Token Logger usage separately in `usage.db`.
The local history is not encrypted; read [data handling](phase-one-security.md)
before using it with sensitive prompts or projects.

In the tview interface:

1. Use `/model add` to enter `openai` or `anthropic`, a model ID, a local alias,
   and a credential reference such as `env:OPENAI_API_KEY`. Adding a model does
   not contact the provider.
2. Set the referenced environment variable before submitting a prompt.
3. Use `/task NAME`, then enter a prompt. Each requested workspace read asks for
   approval. Available MCP tools are `list_files` and `read_file` only.
4. Use `/complete` when the task is finished. `/sessions` and `/resume ID` list
   and reopen saved sessions; `/usage` shows known token counts.

See [architecture](phase-one-architecture.md),
[security/data handling](phase-one-security.md), and
[implementation status](phase-one-status.md) for scope and limitations.
