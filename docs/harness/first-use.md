# Five minutes with the experimental chat

This walkthrough describes the current experimental chat behavior. The
historical v0.14.0 release kept the ChatGPT OAuth route text-only, without
conversation tools or exact-file reads. API-key routes already supported
conditional Linux exact-file reads. These release limits are preserved as
history. Use the
[installation guide](../installation.md) for signed binaries, or build from
source below. Live ChatGPT OAuth planning, clarification, exact-file read,
cancellation, and denial have been operator-verified for one account/model on
one trusted Linux host. Other accounts/models and broader platform coverage
remain unverified.
Automated fixture tests do not establish account eligibility or real-provider
compatibility. See the [capability matrix](capability-availability.md) and
[protocol assessment](openai-chatgpt-oauth-tool-protocol.md) for scope and limits.

## 1. Start and choose a model

Run `komposyn`. For a source build on Linux/macOS, run:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/komposyn ./cmd/harness
/tmp/komposyn
```

The stripped build matches the release worker profile. An unstripped development
binary can exceed the 14 MiB worker image bound and leave reads unavailable.

Open `/model`, choose **Add New Provider**, and add an OpenAI API key,
**Continue with ChatGPT**, or an Anthropic API key. Saved profiles remain
available on restart, but you must explicitly choose a model each run.
**Refresh Models** updates discovery; **Manage Providers** manages profiles.
Discovery does not imply that a listed model supports chat or tools. Failed
requests do not switch your account or billing route automatically.

| Provider and auth | Chat route | Conversation tools | Exact-file reads |
| --- | --- | --- | --- |
| OpenAI API key or explicit key environment reference | Responses | Planning and clarification | Conditional Linux backend |
| Anthropic API key or explicit key environment reference | Messages | Planning and clarification | Conditional Linux backend |
| Continue with ChatGPT | Responses, separate plan authorization | `update_plan` and `request_user_input` for models in the selected profile's account catalog; live verified on one account/model | Conditional trusted Linux backend; live exact-file verified on one host |
| Unknown/unsupported route | No compatibility guarantee | Unavailable | Unavailable |

Ordinary chat and supported conversation tools work on Linux, macOS, and
Windows without a read backend. Native read evidence is limited to recorded
Linux amd64/Bubblewrap 0.9.0 profiles; arm64 builds and macOS/Windows tests do
not establish filesystem enforcement. The ChatGPT OAuth live results are
limited to one account/model and one trusted Linux host. API-key and
Anthropic live provider checks and broader platform checks remain pending.
ChatGPT login must grant plan-usage permission before discovery or inference;
a successful identity login alone is insufficient.

## 2. Exchange text and try a plan

Submit a small, non-sensitive question. Text streams into the transcript;
submit a follow-up after completion. Only one request runs at a time. Ctrl+C
or `/cancel` cancels it; Enter while busy preserves the draft. Failed or
canceled partial replies remain visible but are not automatically reused as
completed conversation context. Changing provider, profile, or model after
messages asks for confirmation and clears the conversation if confirmed.

On an API-key route, try: “Use update_plan to propose three steps for comparing
two sorting algorithms. Do not access files.” Tool use depends on the selected
model. Each plan update replaces the whole temporary plan; `/plan` opens it.
An empty plan clears it. Plans are not saved tasks.

The approval dialog lists action and resource requirements. Choose **Allow
once**, **Allow for conversation** when offered, **Deny**, **Do something
else** with an instruction, or **Cancel turn**. Escape cancels the turn.
Conversation grants authorize only the displayed conversation operation.
Open `/permissions` to inspect/revoke individual grants or clear all of them,
including while approval or clarification is pending. Revocation blocks future
dispatch and retires pending authority; it cannot undo completed effects or
recall data already sent.

## 3. Answer a clarification

Try: “Use request_user_input to ask whether I prefer an example or an
explanation.” Approve the tool if prompted. Choose a suggestion or submit free
text; suggestions and silence never count as answers. **Decline** lets the
model continue with a declined result. **Cancel** or Escape stops the turn
without another provider request. Your submitted answer goes to the selected
model. `/permissions` remains reachable without losing the answer draft.

## 4. Optionally disclose one synthetic Linux file

The release installer does not install Bubblewrap. The read path requires a
trusted system executable at `/usr/bin/bwrap`, usable namespaces and seccomp,
and successful host inspection and owner registration. KompoSyn does not
search PATH, weaken isolation, or repair host security settings. Missing or
refused prerequisites leave reads unavailable while chat remains usable.

Start KompoSyn from a disposable directory containing a UTF-8 file with only
synthetic text. The launch directory appears in the chat header and supplies
the base for relative file requests. Starting in that directory grants no read
permission. Ask the model to read the synthetic file by its relative path. If
a read is proposed, inspect the
exact target, operation, and provider/model/profile destination before choosing
**Allow once**. Each read needs its own approval; no directory-wide or
conversation-long read grant is offered.

Use `/cd <path>` to change the base for later relative requests. Paths can be
absolute or relative to the current working directory, may contain spaces or
Unicode, and accept `~` as home-directory shorthand. Invalid paths leave the
current directory and command draft intact. A directory change keeps the
conversation and does not add file permission. Relative paths use the current
working directory as context. An individually approved exact target may be
outside that directory when host eligibility checks permit it. See the
[capability matrix](capability-availability.md) for current route and backend
conditions.

The worker reads one retained regular file with a 64 KiB bound. Protected
auth/settings objects and aliases, unsupported provider-source access, unsafe
objects, incomplete protection coverage, and unavailable backends refuse the
operation. Oversized, truncated, invalid-text, stale, or canceled captures
are withheld from provider disclosure. It does not browse directories.

Unregistered files are not comprehensively classified: they may contain
secrets. Approved contents leave your machine for the displayed provider and
may recur in later requests in that valid conversation without another read.
Approval binds the opened object; concurrent renames mean it does not promise
continuous containment beneath the live root pathname. Do not approve
sensitive files. See [data handling](phase-one-security.md) and the
[native evidence limits](issue-215-completion-tracking.md).

## 5. Copy, inspect usage, and exit

Bracketed multiline paste inserts a draft without submitting or executing
slash-looking lines. Review it and press Enter to submit. Drag to select text
and copy on release; Ctrl+C copies an active selection, otherwise it cancels
the active request. `/copy` copies the latest completed reply; `/copy all`
copies retained visible conversation text. Ctrl+T toggles mouse capture for
terminal-native selection. Clipboard-helper success confirms a local write;
OSC 52 fallback confirms only that a terminal clipboard request was sent.

The usage display retains known, partial, and unknown counts. Narrow terminals
use a compact layout; Ctrl+U reveals usage and Ctrl+N/Ctrl+P page its rows. Ctrl+L clears the visible
transcript, not provider context or usage. `/exit` ends the process.

Messages, plans, permissions, roots, tool activity, model selection, discovery,
usage, and provider continuation exist only in memory. Restart starts an empty
chat. Validated auth profiles and UI settings persist in
`~/.komposyn/auth.json` and `~/.komposyn/settings.json`. Credentials and OAuth
tokens are **plaintext**, protected by private Unix permissions or the Windows
current-user ACL, not encryption. Upgrades/uninstall preserve configuration,
Token Logger data/backups, and the separate `komposyn-lg` executable.

Editing, rename/deletion, shell commands, web/image tools, multimodal input,
resumable conversations, persistent tasks, MCP, and general plugin loading are
unavailable. Future capabilities need separate design and validation; no
delivery dates are promised.

Report ordinary problems in [GitHub Issues](https://github.com/ayeshLK/komposyn/issues)
with version/commit, platform, auth method (no keys or tokens), sanitized
outcome, and synthetic reproduction steps. Omit prompts/responses, private
paths, raw diagnostics, and databases. Report suspected security or sensitive
data exposure through the [private vulnerability form](https://github.com/ayeshLK/komposyn/security/advisories/new),
following [SECURITY.md](../../SECURITY.md).
