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

Refresh uses the stored token expiry as-is and applies a separate 90-second
refresh lead when deciding whether to renew. Cancellation can stop a caller
while it waits for the store lock. Once the token exchange starts, the auth
manager gives it an independent 15-second deadline and persists a successful
replacement before releasing the lock. A canceled chat remains canceled: it
does not start inference or append a completed turn. On normal CLI shutdown,
refreshes waiting to start are canceled and started exchanges are drained
within the CLI's 20-second shutdown window. Process termination or an
ambiguous network/timeout failure can still lose a replacement if the provider
rotated the token but the response was not received. When KompoSyn observes an
ambiguous exchange failure, it preserves the saved credentials, marks the
profile as requiring reconnection, and will not submit that refresh token
again automatically. If the process terminates before that state is saved, the
provider may have rotated a token that KompoSyn cannot recover; the user may
need to sign in again. Confirmed temporary OAuth rejections preserve the
credentials without marking the outcome unknown. Chat credential reads happen
outside the chat mutex and use cancellable, bounded lock waits so snapshots and
cancellation stay responsive while another process owns the auth-file lock.

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
When an OpenAI response is rejected as unsupported, the status may include a
fixed diagnostic code such as `openai.response_completed.status_in_progress`.
These codes identify a bounded rejection branch; they contain no response text,
provider IDs, credentials, URLs, or arbitrary provider values. They exist to
support safe bug reports and are not persisted.

Only one request runs at a time. While busy, Enter preserves the draft; Ctrl+C
or `/cancel` cancels the request. Changing provider, model, or profile after
messages requires confirmation and clears the in-memory chat. Canceling that
confirmation preserves the selection and chat. Restart begins with no selected
model and an empty conversation while saved settings/credentials remain.

The composer suggests `/model`, `/help`, `/cancel`, and `/exit` from the same
command definitions used for dispatch. Type `/` and narrow the prefix, use
Up/Down to highlight a suggestion, Tab to complete it, Enter to run the
highlighted command, or Esc to dismiss suggestions while keeping the draft.
Command input is dispatched as a command and is never sent as provider text.
Outside an active suggestion list, Tab and Shift+Tab move between the picker
filter, provider actions, and model choices. Provider actions and model rows
have separate headings in the same searchable picker; headings are not
selectable rows, and actions remain available while filtering.

The compact banner shows the KompoSyn name, build version, and selected model;
before selection it says **No model selected**. User turns retain a bold `You:`
label; assistant text is muted gray without a role label. Provider and user
text is escaped as literal text across streaming chunks before rendering.

Input, output, cached-read, and cache-write token counts accumulate in memory
for each provider/model pair for the lifetime of the process. Credential
profiles for the same provider/model are grouped; account and profile details
are not shown. Switching models clears conversation continuation after
confirmation but does not clear usage. Ctrl+L clears only the visible transcript
and its render buffer; an active reply continues from the point it was cleared,
and the draft, selected model, continuation context, and usage remain. Usage
from failed or interrupted provider calls is included when reported. Unknown
metrics remain unknown; if some calls report a metric and others omit it, the
shown count is marked partial. OpenAI input counts include cached-read tokens,
so cached-read is a subset of input. Anthropic reports uncached input separately
from cached-read and cache-write; those values remain separate. KompoSyn does
not add cache categories to input or count them twice. Unsupported cache metrics
stay unknown, not zero. The exact counter values are retained internally.

Counts use compact integer formatting (`1K`, `1.2K`, `1M`, `2.5M`) by default.
Ctrl+E toggles exact counts. On terminals at least 100 columns wide, the
28-column usage pane shows one provider/model row at a time; Ctrl+N and Ctrl+P
cycle through rows. PageUp and PageDown scroll within a row so long IDs and
exact or partial metric values remain reachable. Below 100 columns the pane
collapses; Ctrl+U reveals one model row above the composer while leaving input
available. Ctrl+N and Ctrl+P cycle rows, and PageUp/PageDown scroll its details.
Ctrl+U collapses the compact view again. On wide terminals, Ctrl+U toggles the
side pane. Resizing applies the automatic wide/narrow layout; the
wide-terminal manual expanded/collapsed choice is retained across resizes.
Usage is not persisted and disappears when the process exits.

## Implementation boundaries

- `plugins/model-provider/` holds the experimental provider contract and
  bundled OpenAI Responses / Anthropic Messages adapters.
- `plugins/ui/` holds the experimental UI lifecycle/client contract;
  `plugins/ui/tview/` owns the initial terminal presentation and picker.
- `internal/harness/auth/` owns credential validation, safe persistence, and
  scoped resolution. `internal/harness/auth/openai/` owns browser attempts,
  OIDC validation, refresh, and disconnect; provider plugins receive only the
  credential for the current operation and own their HTTP wire behavior.

Authentication is composed explicitly by provider and stored auth method.
Discovery and chat use the same profile-resolution registry; status inspection
returns sanitized connection and permission metadata without refreshing or
resolving secrets. Login progress is typed and UI-independent, and sensitive
authorization URLs are delivered only to an injected browser-opening
capability. See the [Phase 2 auth boundary](phase-two-auth-boundary.md).
- `internal/harness/model/` aggregates discovery and temporary selection.
- `internal/harness/runtime/` coordinates in-memory requests and replay-safe
  history without opening a database.
- `internal/harness/ui/` adapts that coordinator to the configured UI.
- `cmd/harness/` explicitly registers bundled implementations.

The JSON Schemas and runtime validation are versioned independently of Token
Logger's database schema. No chat or usage history is written to Token Logger.

See [phase-one architecture](phase-one-architecture.md),
[security notes](phase-one-security.md), and the
[phase-one acceptance status](phase-one-status.md). The final
[issue #164 acceptance matrix](issue-164-acceptance.md) maps the remaining
reliability, lifecycle, UI, and platform checks to their test evidence and
records unverified cases and explicit limits.

Implementation follows the official [Sign in with ChatGPT open-source flow](https://developers.openai.com/siwc/token-sharing-open-source),
[preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations),
[account and session guidance](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions),
and [UI/UX guidance](https://developers.openai.com/siwc/ui-ux-guidelines).
