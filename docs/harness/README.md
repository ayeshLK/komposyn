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

For a short guided trial, start with the [five-minute walkthrough](first-use.md),
including the provider/auth/platform table and read-disclosure limits. It
describes experimental v0.14.0. Live provider validation remains pending;
fixture tests are not live tests.

## Build and run

From the repository root, build the executable with its intended name:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/komposyn ./cmd/harness
/tmp/komposyn
```

These flags match the static, stripped release profile. An unstripped
development binary can exceed the Linux worker's 14 MiB image bound and leave
`read_file` unavailable even on a trusted backend.

To install it in the Go bin directory:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$(go env GOPATH)/bin/komposyn" ./cmd/harness
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

The auth and settings stores expose a host-owned protection hook. The shipped
chat wires those stores to a host sandbox composition before loading their
contents. The six-clause bounded exact-file disclosure policy is accepted, and
Stages 1–5 plus the Stage 6 runner/lifecycle implementation are merged.
The read-focused #215 milestone is closed after reviewed shipped-worker snapshot
and study-worker descriptor evidence on the recorded Linux amd64/Bubblewrap
0.9.0 environment. See the [closure and evidence limits](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6771210816).
The focused `read_file` capability is now wired to the retained Linux worker
path on explicitly supported client-tool routes when the trusted Bubblewrap
backend and all lifecycle interfaces are available. It reads one exact regular
file beneath the selected root after a separate explicit once approval naming
the provider/model/profile. The bytes are sent to that provider and may recur
in the same valid conversation; approval does not permit a second read. Root
selection grants no access. Auth/settings protected identities remain denied;
provider-source reads, unavailable backends, unsupported routes, mutation, and
shell remain unavailable. Stage 6 native evidence is limited to the recorded
Linux amd64/Bubblewrap 0.9.0 environment; it is not production-provider or
cross-platform acceptance. `/fs-root` selects an in-memory lookup root. See the
[composition admission and sandbox service](composition-admission-sandbox-service.md),
[protected owner integration boundary](read-broker-owner-integration.md), and
[real-store native acceptance criteria](read-broker-real-store-native-acceptance.md).
The closure disposition, follow-up boundaries and Stage 6 evidence
contract are in the [#215 closure and acceptance record](issue-215-completion-tracking.md).
The integrated native feasibility runner and reproduction steps are described in the
[Stage 6 runner guide](issue-215-stage6-runner.md).

Mutation design is tracked separately in
[issue #267](https://github.com/ayeshLK/komposyn/issues/267).

The experimental named SQLite VFS read-only probe and its writable/SHM
limitations are documented in the [named VFS feasibility prototype](sqlite-named-vfs-prototype.md).
The separate Linux/amd64 generated-ABI write-barrier experiment is documented
in the [writable object-barrier prototype](sqlite-writable-object-barrier-prototype.md).
It is an isolated synthetic study and does not enable writes in KompoSyn stores.
The follow-up study of modernc SQLite's WAL shared-memory open, pin and mapping
order—and why its raw syscall hook is not a production authorization API—is in
the [WAL/SHM feasibility report](sqlite-wal-shm-feasibility.md).
The proposed owner-aware exact-object callback contract, coordination order,
Token Logger route coverage, and future acceptance matrix are in the
[SQLite owner-aware hook design](sqlite-owner-aware-hook-contract.md). This is
design-only; production SQLite owner integration and filesystem tools remain
unavailable.

The proposed first sandbox composition, trusted owner coverage states,
unknown-data refusal boundary and staged typed-read path are described in the
[initial sandbox composition design](initial-sandbox-composition.md). The
auth/settings lifecycle and pre-consumption admission seam are implemented in
the [composition admission and sandbox service](composition-admission-sandbox-service.md).
Generic filesystem access remains closed; the only exception is the bounded,
once-approved exact-file path described above.
The reviewed #243 native service-fixture result, its provenance, and the
checkout-safe reproduction runner are recorded in
[composition native acceptance](read-broker-composition-native-acceptance.md)
and [`scripts/native-acceptance/`](../../scripts/native-acceptance/README.md).
The accepted unregistered exact-file policy and implementation stages are
recorded in the [read implementation plan](read-broker-unregistered-read-implementation-plan.md).
Generic directory and broad filesystem reads remain unavailable. The
once-approved exact-file operation is the sole registered filesystem
capability; its changes still require ordinary review before merge.

## Local configuration

The default paths are `~/.komposyn/auth.json` and
`~/.komposyn/settings.json`. Paths can be overridden with the flags above.
The auth file remains at schema version 1. OAuth uses the existing schema
without a version bump; older API-key and environment-reference profiles remain
readable. It stores API keys and OpenAI OAuth tokens as plaintext JSON under the
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
send plain text, one system instruction, and streaming. On provider/auth routes
that explicitly support client tools, the executable registers
`update_plan` and `request_user_input` behind centralized approval. OpenAI
API-key Responses and Anthropic Messages support these tools. OpenAI OAuth and
unknown routes remain text-only. When the trusted Linux read backend is
available, supported routes may additionally advertise `read_file` as described
above. No filesystem mutation, process, or network tools are available. OpenAI
requests set `store: false`; Anthropic requests use Messages. OpenAI API-key
requests retain the configured output-token bound and encrypted reasoning
continuation. The ChatGPT plan route follows the current SIWC preview subset:
it sends the required full input array with `store: false` and `stream: true`,
and omits unsupported `max_output_tokens` and API-key-only fields. KompoSyn
does not enforce its API-key output-token bound on this route. It never
silently switches between ChatGPT plan billing and an API key.

For OpenAI streaming replies, completed `response.output_item.done` items are
retained in output order. When a completed terminal response contains an empty
output array, validated completed items provide native continuation; visible
text alone is never used to invent replay data. Incomplete, conflicting, or
gapped item sequences remain rejected.

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

The composer suggests `/model`, `/help`, `/cancel`, `/exit`, `/permissions`,
`/plan`, and `/copy` from the same
command definitions used for dispatch. Type `/` and narrow the prefix, use
Up/Down to highlight a suggestion, Tab to complete it, Enter to run the
highlighted command, or Esc to dismiss suggestions while keeping the draft.
Command input is dispatched as a command and is never sent as provider text.
`/permissions` shows temporary tool grants for this conversation, including
their opaque IDs, operation, scope, lifetime, and origin. Select a grant and
press Enter (or its `r` shortcut) to revoke it, or clear all grants. Revocation
prevents future dispatch and invalidates pending approvals; it does not undo a
completed effect or promise to stop running work.

### Conversation-local tools

The experimental executable always offers two conversation tools on routes that
explicitly support client tools: OpenAI API-key Responses and Anthropic
Messages. OpenAI OAuth and unknown routes remain text-only. Each tool requires
the normal action and resource authorization. The resource is the exact opaque
ID of the current conversation, with once or current-conversation approval;
these grants cannot authorize filesystem, process, or network access. The
approval dialog and `/permissions` label this scope as “current conversation.”

`update_plan` replaces the entire current plan atomically. Each of at most 32
steps has a `step` string (up to 160 Unicode characters) and one of `pending`,
`in_progress`, or `completed`; at most one step may be in progress. The plan
input is limited to 16 KiB and 8 KiB of combined step text. An empty plan clears
the current plan. `/plan` opens the full plan; the compact plan pane also shows
it in chat. Plans exist only in memory and are cleared on confirmed
conversation reset and process shutdown.

`request_user_input` asks one question (up to 500 Unicode characters) and may
offer up to three suggested answers. Suggestions never count as answers until
chosen. The user can choose a suggestion, enter free text, or explicitly
decline. A decline returns the typed `declined` outcome and the model may
continue without an answer. Cancel or Escape returns `cancelled` and stops the
turn without another provider request. No default, timeout, or silence is
treated as a response. Free-text answers are returned exactly as submitted,
up to 4 KiB, and are sent to the selected model. Clarification does not grant,
revoke, or widen permissions. Its wait follows caller cancellation and
deadlines, not the provider request timeout. `/permissions` remains reachable
while clarification is open; returning preserves any answer draft for that
request. From the answer editor, Shift+Tab focuses the question, arrow keys or
PageUp/PageDown/Home/End scroll it, and Tab returns to the editor.

Both tools share the runtime limits described below: eight invocations, four
provider requests, 256 KiB per serialized result, and 1 MiB of result envelopes
per turn. Tool activity distinguishes approval, authorization, execution, and
the final outcome. Updating a plan is an in-memory conversation effect, not a
persistent task, project, session, or goal.
Outside an active suggestion list, Up/Down in the composer recalls previously
submitted questions. Up moves toward older questions; Down moves toward newer
ones and restores the draft that was present before browsing. Recalled text can
be edited and is sent only when Enter is pressed. This history stays in memory
for the current CLI run, including across Ctrl+L and model switches; it is not
saved to disk. Blank submissions, slash commands, and unsent drafts are not
added to history. Up/Down still navigate slash suggestions while that list is
open. Provider forms and model lists keep their own arrow-key behavior.

Tab and Shift+Tab move between the picker
filter, provider actions, and model choices. Provider actions and model rows
have separate headings in the same searchable picker; headings are not
selectable rows, and actions remain available while filtering.

The compact banner shows the KompoSyn name, build version, and selected model;
before selection it says **No model selected**. User turns have a `You:` label
and a blank line separates each question from its answer. Transcript text uses
the terminal’s normal foreground and background rather than fixed gray, so it
follows the terminal’s light/dark palette without an assistant role label.
Provider and user text is rendered as literal text, including markup-looking
sequences.

### Copying replies and terminal text

Run **`/copy`** to copy the latest successfully completed assistant reply as
plain text. The target remains available after Ctrl+L and while a later reply
streams or fails; it changes only after another reply completes successfully.
Run **`/copy all`** to copy the retained visible conversation text, including
partial assistant output, without role labels, UI notices, visual wrapping, or
composer contents. Source whitespace, newlines, Unicode, and code indentation
are preserved. Empty copy requests leave the system clipboard unchanged.

Copy uses an available native clipboard helper on local macOS, Linux, Windows,
or WSL sessions. Helpers receive text through standard input and run outside the
UI event loop. If a local helper is unavailable or fails, the UI requests a
terminal clipboard write through OSC 52. A successful helper exit confirms its
write; OSC 52 status confirms only that the terminal request was sent, since
terminals, remote sessions, and multiplexers may block it. The UI never reads
the clipboard or copies credential-form contents. Ctrl+Y is not a copy
shortcut.

Bracketed terminal paste inserts text into the draft. Multiline content,
including slash-looking lines, is text only; paste never submits a request or
runs a command. The composer wraps and shows several draft lines; review the
draft and press Enter yourself to submit. Normal terminal paste remains
available without clipboard read access. Drag over transcript text to select
and copy on release; double-click selects a word and triple-click selects a
logical line. Escape clears the selection. Ctrl+C copies an active selection;
otherwise it keeps its request-cancel behavior. To use terminal-native
selection, press **Ctrl+T** to release mouse capture, select text, and use the
terminal's Copy action (often Ctrl+Shift+C on Linux). Press Ctrl+T again to
restore UI mouse controls.

When an injected tool runtime needs approval, KompoSyn shows every missing
action and resource requirement together. Choose **Allow once**, **Allow for
conversation** when offered, **Deny**, or **Do something else** with an
instruction for replanning. **Cancel turn** and Escape stop the turn without a
provider continuation. The dialog does not pre-approve; a keyboard choice must
be made after it appears. Read access warns that retrieved data is returned to
the selected model. Use `/permissions` to inspect or revoke temporary grants;
OAuth permission metadata is separate. Revocation blocks future dispatch but
does not roll back completed effects or claim to terminate running work.
`/permissions` remains available during a turn, and approval and clarification
dialogs link to it directly. Tool activity shows awaiting approval,
authorized, running, and finished states, with the typed outcome and
truncation status. A successful private capture is labeled as private until
continuation commitment; a later message-only handoff update identifies that
the contents were sent to the selected provider and may recur in this
conversation. Refusal, cancellation, and uncertain outcomes have separate
messages. Cancellation and revocation do not claim to recall data already sent.
Activity includes no raw file contents. The chat header reports file-reading
availability from the selected provider/authentication route, the inspected
backend, and the selected lookup directory. It explains when no model is
selected, route support is absent or unverified, the backend is unavailable,
or a lookup directory is still required. Availability does not promise that
every requested target is eligible; each read still passes host admission and
exact-target checks and requires a fresh once approval. Tool
prompts, plans, grants, and activity exist only in memory. The two bundled
conversation tools are always available on supported routes; `read_file` is
the sole conditional host-backed capability. Filesystem mutation, process,
web, and image tools remain unavailable.

Escape in the approval dialog explicitly cancels the entire active turn. The
first keyboard event after a dialog appears is consumed to prevent a composer
key already in flight from accepting an option.

### Token usage

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

Counts always use compact integer formatting (`1K`, `1.2K`, `1M`, `2.5M`).
Exact integer counters remain internal; there is no display-format toggle.
On terminals at least 100 columns wide, the
28-column usage pane shows one provider/model row at a time; Ctrl+N and Ctrl+P
cycle through rows. PageUp and PageDown scroll within a row so long IDs and
compact or partial metric values remain reachable. Below 100 columns the pane
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
# Filesystem enforcement foundation

KompoSyn registers one bounded model-facing filesystem capability: Linux
`read_file`, gated on trusted Bubblewrap inspection and a complete host-owned
read lifecycle. It follows the approved opened-object contract; concurrent
namespace changes mean it does not promise strict live pathname containment.
Filesystem mutation and shell remain unavailable.
See the [filesystem enforcement foundation](filesystem-enforcement-foundation.md)
and [enablement policy and evidence](filesystem-enablement-policy.md) for
guarantees, counterexamples, protected-path policy proposals, root-selection
UX proposals, and requirements for any later tool enablement.
The [Linux sandbox feasibility study](sandbox-feasibility-study.md) is
disposable research infrastructure only; its native tests are separate from Go
unit/race tests. Read evidence is limited to the specifically recorded native
environment; it does not establish mutation or shell enforcement. The
[sandbox direction and implementation gates](sandbox-design-proposal.md) record
the later object-authority target and distinguish it from the historical strict
pathname-containment gate in the earlier policy.

The [brokered read feasibility harness](read-broker-feasibility.md) provides an
experimental host-owned protected-object registry and fixed Bubblewrap worker.
The memory-chat host composition now uses that coordinator for auth/settings
registration and cleanup, but its independent unknown-data gate refuses generic
reads before descriptor release. The study command's bounded separate-process
namespace actors cover selected-root and parent relocation, target replacement,
a late protected alias, and a moved protected directory child. Those source
scenarios do not count as native evidence until the selected revision runs with
a trusted Bubblewrap backend. Protected-owner rotation, mount boundaries, a
protected-ancestor race, and descendant cleanup remain limited or pending in
that historical study. Current source separately enables only conditional
`read_file` under the reviewed Stage 6 boundaries above. See the [scenario matrix](read-broker-native-actor.md)
and follow the [manual read-broker runner guide](read-broker-manual-runner.md) to produce a
bounded, checksummed archive from a normal Linux host terminal.
The [owner inventory and admission note](read-broker-owner-inventory.md)
records the Token Logger/provider lifecycle gaps and explains why a complete
synthetic fixture profile does not establish production coverage.

## Native study prerequisite

The brokered-read study is experimental. Both its native worker and the
conditional chat read path require a trusted, system-installed Bubblewrap at the
absolute path `/usr/bin/bwrap`, usable user/mount/PID/IPC/UTS/network namespaces,
and the worker's seccomp support. The harness validates the executable and its
path before running probes; it does not search `PATH`, install or upgrade
Bubblewrap, change host permissions, or retry with weaker isolation. The
release installer does not include Bubblewrap. Missing, untrusted, incompatible,
or host-blocked prerequisites leave the worker unavailable; the study records
that as unavailable evidence, not a pass. Never run the study as root or change
ownership/host security settings to get past the trust check. See the
[manual native-validation runner](read-broker-manual-runner.md) for the exact
prerequisites and evidence handling.
