# Phase two auth boundary

The temporary chat composes authentication explicitly in `cmd/harness/`.
`internal/harness/auth.Registry` selects an implementation by the exact
provider and stored auth method. A registration checks that the implementation
identifies itself with the same pair. There is no alternate-account,
environment-key, or API-key fallback when a selected profile fails.

The auth registry resolves API-key, explicit environment-reference, and OAuth
profiles through one path for model discovery and chat. Implementations return
one secret only to the operation that requested it. Provider plugins receive
the typed credential for that operation and never receive a store or auth
implementation. The coordinator/store remains responsible for profile choice,
protected persistence, cross-process serialization, and stale-lifecycle guards.

Auth implementations own provider login, refresh, identity validation,
permission interpretation, and disconnect behavior. They expose public
connection state and named permission states without exposing token contents.
Status inspection reads local metadata only; it does not refresh tokens,
resolve a credential, or contact the provider. Auth resolution failures have
stable categories for missing configuration, cancellation, temporary service
failure, missing permission, invalid client configuration, and terminal
authentication failure. User guidance is sanitized while the typed cause
remains available to callers.

Login orchestration emits typed progress and completion data through the auth
contract. The provider implementation receives a `BrowserOpener` capability
for transient authorization URLs. A URL may contain account hints and is never
included in progress events, public status, chat snapshots, or diagnostics. UI
plugins render progress and submit decisions; they do not implement OAuth HTTP
or inspect credentials. The terminal UI is one implementation of that
interaction contract, not the owner of login orchestration.

The initial composition registers API-key and explicit environment resolution
for OpenAI and Anthropic, plus OpenAI OAuth. This records only the combinations
currently supported by the temporary chat. It does not introduce general
plugin loading or a universal plugin API.
