# Experimental temporary chat

This repository contains a small, experimental `komposyn` chat under
`plugins/model-provider/`, `plugins/ui/`, and `internal/harness/`. It proves
the OpenAI and Anthropic provider boundary and a replaceable UI client. It is
not a general plugin runtime, agent workflow engine, or stable public API.

The workflow is: start KompoSyn, open `/model`, add an OpenAI or Anthropic API
key, choose a discovered model, and exchange text messages. No project,
workspace, session, task, SQLite database, MCP server, or Token Logger process
is required. The selected model, messages, provider continuation data,
discovery results, and per-request usage exist only in memory and are cleared
when the process exits.

## Build and run

From the repository root, build the executable with its intended name:

```sh
go build -o /tmp/komposyn ./cmd/harness
/tmp/komposyn
```

To install it in the Go bin directory:

```sh
go build -o "$(go env GOPATH)/bin/komposyn" ./cmd/harness
```

Bare `go install ./cmd/harness` produces a binary named `harness`, so it is
not the documented installation path. No binary is committed or published by
this repository.

For headless configuration inspection, run `komposyn --check`. `--auth-file`
and `--settings-file` select alternate paths for tests or isolated runs; they
do not accept credential values. Startup creates no chat database.

## Local configuration

The default paths are `~/.komposyn/auth.json` and
`~/.komposyn/settings.json`. Paths can be overridden with the flags above.
The auth file stores API keys as plaintext JSON. On Unix the directory is
restricted to `0700` and files to `0600`; on Windows it applies a protected
ACL granting access to the current user. Writes use a private temporary file,
an interprocess lock, and atomic replacement. Symlinks/reparse points,
malformed files, duplicate profile IDs, invalid fields, and unsupported
versions fail without replacing the existing configuration.

Example credentials file (use a real key only in your private home directory):

```json
{
  "schema_version": 1,
  "credentials": [
    {
      "id": "openai-personal",
      "provider": "openai",
      "label": "Personal OpenAI",
      "routing": { "organization": "<optional-org-id>" },
      "auth": { "type": "api_key", "api_key": "<OPENAI_API_KEY>" }
    }
  ]
}
```

OpenAI profiles may specify `organization` and `project` routing values.
Anthropic profiles do not accept routing metadata. An explicit environment
reference can use `"auth": { "type": "env", "env": "OPENAI_API_KEY" }`;
its value is resolved for the operation and is never copied into the file.
The API key is masked during entry. Profile metadata and UI snapshots never
contain the key. Removing a profile removes only the local reference and does
not revoke the provider key.

Settings contain only the schema version and UI plugin selection:

```json
{
  "schema_version": 1,
  "ui": { "plugin": "tview" }
}
```

An absent file, `ui` block, or `ui.plugin` defaults to `tview`. Explicit empty,
unknown, malformed, or unsupported values fail clearly. Extra top-level and
UI fields are preserved when settings are saved. Schemas are in
[`schema/harness-auth.schema.json`](../../schema/harness-auth.schema.json) and
[`schema/harness-settings.schema.json`](../../schema/harness-settings.schema.json).

## Using `/model`

`/model` opens a filterable picker. Its first three actions remain visible
while filtering:

- **Add New Provider** asks for provider, profile label, and a masked API key.
  The provider's model-list request validates the candidate. KompoSyn then
  saves the credential and returns to the picker; a model is never selected
  automatically.
- **Refresh Models** fetches catalogs for each saved profile and shows partial
  failures alongside successful results.
- **Manage Providers** displays safe metadata and lets users replace a key or
  remove a profile without revealing the current key.

Models are keyed by provider, credential profile, and provider model ID.
OpenAI's model-list endpoint reports identifiers and ownership, not text-chat
capabilities. Anthropic's catalog provides display metadata but not the
compatibility guarantee this chat needs. Catalog entries therefore have
**unknown** compatibility; the picker explains this and actual Responses or
Messages requests report unsupported access without automatic fallback.
Model names are not used to infer capability or context limits. Both adapters
send plain text, one system instruction, bounded output tokens, streaming, and
no tools. OpenAI requests set `store: false`; Anthropic requests use Messages.

After a completed request, a follow-up includes the current conversation's
completed user/assistant turns and provider-native continuation data. A failed,
canceled, refused-without-replay-data, truncated, or unsupported response is
not added to provider continuation history. Partial text remains visible in
the current UI, is marked interrupted/failed, and is never resent automatically.

Only one request runs at a time. While busy, Enter preserves the draft; Ctrl+C
or `/cancel` cancels the request. Changing provider, model, or profile after
messages requires confirmation and clears the in-memory chat. Canceling that
confirmation preserves the selection and chat. Restart begins with no selected
model and an empty conversation while saved settings/credentials remain.

## Implementation boundaries

- `plugins/model-provider/` holds the experimental provider contract and
  bundled OpenAI Responses / Anthropic Messages adapters.
- `plugins/ui/` holds the experimental UI lifecycle/client contract;
  `plugins/ui/tview/` owns the initial terminal presentation and picker.
- `internal/harness/auth/` owns credential validation, safe persistence, and
  scoped resolution; provider plugins receive only the credential for the
  current operation.
- `internal/harness/model/` aggregates discovery and temporary selection.
- `internal/harness/runtime/` coordinates in-memory requests and replay-safe
  history without opening a database.
- `internal/harness/ui/` adapts that coordinator to the configured UI.
- `cmd/harness/` explicitly registers bundled implementations.

The JSON Schemas and runtime validation are versioned independently of Token
Logger's database schema. No chat or usage history is written to Token Logger.

See [phase-one architecture](phase-one-architecture.md),
[security notes](phase-one-security.md), and the
[phase-one acceptance status](phase-one-status.md).
