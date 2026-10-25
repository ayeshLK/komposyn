# Phase-one harness data handling

This experimental harness sends user messages, root workspace instructions,
and approved read-only tool results to the selected provider. It stores model
configuration and conversation history locally in a dedicated `harness.db`.
Prompts and tool results may be sensitive. The harness database is not encrypted;
its directory and file are restricted to the current filesystem user.

Credentials use explicit `env:VARIABLE_NAME` references and are resolved only
when a provider request starts. The database stores the reference, not the
credential value. The harness never passes conversation content, instructions,
credentials, tool arguments, or tool results to the Token Logger observer.

The in-process MCP server has only `list_files` and `read_file`. It uses a
canonical workspace root and `os.Root` path confinement, and the runtime asks
for approval before every requested operation. Approval does not provide
sandboxing. There are no write or shell tools and no arbitrary third-party MCP
processes.

Provider services receive the content necessary to answer the user's request;
their own retention and processing terms apply. No network telemetry or live
provider request occurs on startup or while adding a model.
