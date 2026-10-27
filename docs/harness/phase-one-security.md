# Temporary chat data handling

The experimental chat sends the user's submitted text and a short system
instruction to the selected OpenAI or Anthropic API using the selected
credential. OpenAI credentials can be an API key or an SIWC OAuth token for
ChatGPT plan usage; Anthropic remains API-key based. It does not read workspace
files, load `AGENTS.md` or skills, run tools, send MCP requests, or write
messages to Token Logger. OpenAI Responses requests set `store: false`.
Anthropic Messages requests do not create a KompoSyn conversation resource.

API keys and OpenAI OAuth access, refresh, and retained ID tokens are stored as
plaintext in `~/.komposyn/auth.json`; API-key environment-reference profiles
avoid copying the environment value. File-backed credentials are therefore
available to a process that can read the user's private configuration. Unix
directories/files are set to `0700`/`0600`; Windows directory and file DACLs
are protected and grant the current user access. Reads reject symlinks or
reparse points. Updates use a per-file lock, a private temporary file, sync,
and atomic replacement. Malformed, unsupported, or unsafe files fail closed;
failed validation does not replace an existing profile. The settings file
contains only UI configuration and no key, chat, or model selection.

Credentials do not appear in provider error messages, profile metadata, UI
snapshots, chat messages, or diagnostic output. Authorization URLs containing
the retained ID-token hint are never displayed or logged. The auth coordinator
passes only one resolved credential into a provider call. Refresh is serialized
under the auth-file interprocess lock. A canceled caller can stop waiting for
the lock; after a refresh exchange begins, it runs with an independent
15-second timeout and a successful replacement is persisted before unlocking.
The CLI drains in-flight exchanges during normal shutdown. Temporary refresh
errors preserve the stored credentials, while confirmed unusable-token errors
mark reconnect required. If the process terminates or a network failure makes
the exchange outcome ambiguous, a provider-rotated token may be unrecoverable;
KompoSyn does not blindly retry it.
Disconnect attempts the discovered remote refresh-token revocation endpoint,
then removes local tokens; the UI distinguishes confirmed remote revocation
from local-only removal. Removing an API-key profile does not revoke that key.

All chat state, selected model, provider-native continuation data, partial
output, discovery results, and request usage are process memory only. Restart
starts an empty chat with no selected model. On exit, active work is canceled
and joined, then memory is discarded. A failure or partial response is not
automatically retried or appended to follow-up provider context.

Provider credentials can authorize billable inference. OAuth login makes no
inference request. ChatGPT plan usage is enabled only when the token response
grants `chatgpt.tokens.use.direct`; a successful identity login alone does not
authorize model discovery or inference. OAuth inference uses the public
Responses API with `store: false`, `stream: true`, and the request subset
permitted by the SIWC preview. It omits `max_output_tokens`, so the chat's
API-key output bound is not enforced on this OAuth route. Provider failures
never cause a silent switch to another account or billing method. Live account
authorization and billable inference were not performed by the test suite;
fake HTTP tests do not claim real-provider connectivity.
