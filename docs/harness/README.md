# Experimental temporary chat

This repository contains a small, experimental `komposyn` chat under
`plugins/model-provider/`, `plugins/ui/`, and `internal/harness/`. It proves
the OpenAI and Anthropic provider boundary and a replaceable UI client. It is
not a general plugin runtime, agent workflow engine, or stable public API.

The workflow is: start KompoSyn, open `/model`, add an OpenAI API key or
choose **Continue with ChatGPT**, or add an Anthropic API key; choose a
discovered model and exchange text messages. ChatGPT plan authorization is
separate from API-key billing. No project,
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
not the documented installation path. Release builds are prepared with
[`scripts/build-release.sh`](../../scripts/build-release.sh) and published only
through the manual workflow described in
[release verification](../release-verification.md); no binary is committed
to source control.

For a signed binary installation, use the
[experimental CLI installation guide](../installation.md).

For headless configuration inspection, run `komposyn --check`. `--auth-file`
and `--settings-file` select alternate paths for tests or isolated runs; they
do not accept credential values. Startup creates no chat database.

## Local configuration

The default paths are `~/.komposyn/auth.json` and
`~/.komposyn/settings.json`. Paths can be overridden with the flags above.
The auth file remains at schema version 1; this CLI format has not been
released, so OAuth extends the existing pre-release schema without a version
bump. It stores API keys and OpenAI OAuth tokens as plaintext JSON under the
same private-file boundary. On Unix the directory is
restricted to `0700` and files to `0600`; on Windows it applies a protected
ACL granting access to the current user. Writes use a private temporary file,
an interprocess lock, and atomic replacement. Symlinks/reparse points,
malformed files, duplicate profile IDs, invalid fields, and unsupported
versions fail without replacing the existing configuration. Existing
API-key/environment profiles load unchanged alongside OAuth profiles.

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
contain credentials. Removing an API-key profile removes only the local
reference and does not revoke the provider key.

## Continue with ChatGPT

In `/model`, choose **Add New Provider → OpenAI → Continue with ChatGPT**.
KompoSyn starts a short-lived listener on `127.0.0.1`, opens the system
browser, and uses Authorization Code with PKCE. It validates the returned
OpenID token before saving a profile. Initial registration uses the
`dynamic_agent_client` entrypoint; KompoSyn stores the issued client ID with
the verified account, along with a stable local host ID. Returning sign-in
reuses that registration and checks that the verified subject still matches.
The account is not added or changed until callback state, code exchange, and
identity validation succeed. Sign-in itself sends no chat prompt.

The requested `chatgpt.tokens.use.direct` permission authorizes eligible
Responses API usage through the selected user's ChatGPT plan. A valid account
login without that returned scope is shown as signed in with plan usage
disabled; no model discovery or inference is sent until the user reconnects
and enables plan usage. Eligibility and limits remain controlled by OpenAI.
Use **Manage Providers** to reconnect, disconnect while retaining the
registration, remove the registration, or open ChatGPT Settings → Usage.
Disconnect clears local tokens even if remote revocation cannot be confirmed;
KompoSyn reports that distinction and directs the user to ChatGPT Settings.

OAuth access, refresh, and retained ID tokens are stored in the protected
`auth.json` file. UI metadata never includes tokens. Refresh rotates access and
refresh tokens under the auth store's interprocess lock and does not change the
profile identity, selected model, or in-memory chat. Removing the local profile
does not delete the OpenAI registration.

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

- **Add New Provider** asks for a provider and one of its advertised auth
  methods. API-key entry remains masked and is checked with model discovery;
  ChatGPT sign-in returns to the picker after successful identity validation.
  Neither path selects a model automatically.
- **Refresh Models** fetches catalogs for each saved profile and shows partial
  failures alongside successful results.
- **Manage Providers** displays safe method/account/status metadata and lets
  users replace API keys or reconnect/disconnect OAuth profiles without
  revealing credentials.

Models are keyed by provider, credential profile, and provider model ID.
OpenAI's model-list endpoint reports identifiers and ownership, not text-chat
capabilities. Anthropic's catalog provides display metadata but not the
compatibility guarantee this chat needs. Catalog entries therefore have
**unknown** compatibility; the picker explains this and actual Responses or
Messages requests report unsupported access without automatic fallback.
Model names are not used to infer capability or context limits. Both adapters
send plain text, one system instruction, streaming, and no tools. OpenAI
requests set `store: false`; Anthropic requests use Messages. OpenAI API-key
requests retain the configured output-token bound and encrypted reasoning
continuation. The ChatGPT plan route follows the current SIWC preview subset:
it sends the required full input array with `store: false` and `stream: true`,
and omits unsupported `max_output_tokens` and API-key-only fields. KompoSyn
does not enforce its API-key output-token bound on this route. It never
silently switches between ChatGPT plan billing and an API key.

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
  scoped resolution. `internal/harness/auth/openai/` owns browser attempts,
  OIDC validation, refresh, and disconnect; provider plugins receive only the
  credential for the current operation and own their HTTP wire behavior.
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

Implementation follows the official [Sign in with ChatGPT open-source flow](https://developers.openai.com/siwc/token-sharing-open-source),
[preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations),
[account and session guidance](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions),
and [UI/UX guidance](https://developers.openai.com/siwc/ui-ux-guidelines).
