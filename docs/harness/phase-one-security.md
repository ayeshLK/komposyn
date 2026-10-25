# Temporary chat data handling

The experimental chat sends the user's submitted text and a short system
instruction to the selected OpenAI or Anthropic API using the selected
credential. It does not read workspace files, load `AGENTS.md` or skills, run
tools, send MCP requests, or write messages to Token Logger. OpenAI Responses
requests set `store: false`. Anthropic Messages requests do not create a
KompoSyn conversation resource.

API keys are stored as plaintext in `~/.komposyn/auth.json` unless an explicit
environment-reference profile is used. File-backed credentials are therefore
available to a process that can read the user's private configuration. Unix
directories/files are set to `0700`/`0600`; Windows directory and file DACLs
are protected and grant the current user access. Reads reject symlinks or
reparse points. Updates use a per-file lock, a private temporary file, sync,
and atomic replacement. Malformed, unsupported, or unsafe files fail closed;
failed validation does not replace an existing profile. The settings file
contains only UI configuration and no key, chat, or model selection.

Credentials do not appear in provider error messages, profile metadata, UI
snapshots, chat messages, or diagnostic output. The auth resolver passes only
one credential value into a provider call. The store does not automatically
copy environment-variable values to disk. Removing a profile does not revoke
the upstream key.

All chat state, selected model, provider-native continuation data, partial
output, discovery results, and request usage are process memory only. Restart
starts an empty chat with no selected model. On exit, active work is canceled
and joined, then memory is discarded. A failure or partial response is not
automatically retried or appended to follow-up provider context.

Provider credentials can authorize billable inference. Setup validates with
the provider model-list operation, not a hidden chat prompt. Live provider
authentication/discovery and billable chat were not exercised as part of the
offline test suite; credential presence does not opt into such tests.
