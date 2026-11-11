# Experimental v0.14.0 release overview

[v0.14.0](https://github.com/ayeshLK/komposyn/releases/tag/v0.14.0) was published
from `46f84ea5923b7bb04b3b530f57cec46d7c291595` after the reviewed Release Please
lifecycle and all six signed installation gates. Prepared for
[issue #269](https://github.com/ayeshLK/komposyn/issues/269), against baseline
**v0.13.1**. Live provider validation remains explicitly unverified in
[follow-up #272](https://github.com/ayeshLK/komposyn/issues/272).
The [validation record](release-validation.md) separates local, CI, native,
artifact, and pending live evidence.

## What changes for chat users

The experimental `komposyn` chat adds temporary plans and clarification on
OpenAI API-key Responses and Anthropic API-key Messages routes. Approval
dialogs name action/resource scope, offer once or conversation grants where
applicable, and allow denial, cancellation, or an instruction to replan.
`/plan` shows the plan; `/permissions` inspects and revokes temporary grants.
Continue with ChatGPT remains a separate, text-only authorization route.
Discovered model names do not guarantee chat or tool compatibility.

Linux hosts that pass trusted Bubblewrap inspection can additionally offer
`read_file`. `/fs-root` selects a lookup root without granting access. Each
read requires explicit once approval for an exact retained file and the
displayed provider/model/profile destination. The worker is bounded to 64 KiB;
protected objects, incomplete owner coverage, unsupported sources, and unsafe
or unavailable backends refuse access. Truncated, invalid-text, canceled, and
stale captures are withheld. Unknown file contents may be sensitive; approved
contents go to the provider and may recur in the valid conversation. Concurrent
renames mean continuous live pathname containment is not guaranteed.

Terminal improvements include multiline draft paste, direct transcript
selection/copy, `/copy` and `/copy all`, clipboard fallback, and responsive
input/usage layouts. Streaming, follow-up context, request cancellation, and
confirmed model changes remain part of temporary chat. See the
[five-minute walkthrough and capability table](first-use.md).

## What changes for module consumers

This is also a Token Logger core release. Codex parsing now ignores token
counts without usage information and accepts supported optional reasoning
metadata. Explicit bounded Codex recovery adds preview, transactional apply,
and read-only committed-history inspection APIs. Recovery preserves base
events and appends allocation evidence; accounting readers use effective
allocations. It does not silently recover historical data during import.

SQLite advances **18 → 19** with an immutable transactional migration and a
verified consistent pre-migration online backup. Opening an older database
may migrate it; no automatic down-migration exists. Do not point an older core
at an upgraded database. Retain the verified backup and follow normal recovery
procedures rather than copying or merging data directories. See
[Codex recovery](../codex-historical-recovery-v1.md) and the
[core API](../core-api.md). The event schema and standalone protocol-v1
contract are unchanged; downstream compatibility is checked separately.

The production `komposyn-lg` executable remains independently versioned and
released from its own repository. This release does not distribute it.
`go.mod` promotes the already pinned `uniseg` dependency from indirect to
direct for terminal selection; it adds no dependency version upgrade relative
to v0.13.1.

## Experimental limits and data handling

Live OpenAI API-key, Anthropic API-key, and ChatGPT login/inference validation
remains **pending after publication**. Fixtures do not prove real account
eligibility, current service behavior, or model access. No live credentials or
billable requests were used for preparation or this publication verification.
The maintainer will coordinate authorized checks against the published binaries and record unavailable routes
explicitly in the validation record. This deferral does not waive automated,
native, security, compatibility, or signed installer gates.

Native read reports cover specific Linux amd64/Bubblewrap 0.9.0 profiles,
with shipped-worker snapshot and study-worker descriptor evidence kept
distinct. They do not establish non-Linux enforcement, arbitrary descendant
cleanup, aggregate cgroup quotas, universal opened-object correctness, or
strict live pathname containment. The local preparation attempt refused an
untrusted backend before launch. Review the exact provenance and remaining
limits in the [validation record](release-validation.md).

Chat state, roots, tool activity, grants, plans, and continuation remain in
memory. Auth profiles and UI settings persist; credentials and OAuth tokens
are plaintext under private file permissions/ACLs. Installation, upgrade, and
uninstall preserve configuration, Token Logger data/backups, and `komposyn-lg`.
Editing, rename/deletion, shell execution, web/image tools, persistent
conversations/tasks, MCP, and general plugin loading remain unavailable.
Research binaries and SQLite feasibility studies do not enable these features.

Future work will follow separately reviewed designs and feedback without
promised dates. Report ordinary problems through
[GitHub Issues](https://github.com/ayeshLK/komposyn/issues) with version/commit,
platform, auth method, sanitized outcomes, and synthetic reproduction steps.
Do not include credentials, private paths, transcripts, or raw databases.
Use [private security reporting](../../SECURITY.md#reporting-a-vulnerability)
for suspected vulnerabilities or sensitive disclosure.

## Completed publication gate

Preparation [#270](https://github.com/ayeshLK/komposyn/pull/270) and version/
changelog [#271](https://github.com/ayeshLK/komposyn/pull/271) merged through
normal review. Release Please created the immutable source tag and draft.
The maintainer dispatched binary publication for the exact tagged commit after
the final go/no-go. All six native builds and signed installation lifecycle
checks passed before automatic publication; the published assets were then
independently verified.

The completed workflow and artifact identities are recorded in the
[validation record](release-validation.md). The existing manual lifecycle and
same-tag recovery rules remain in [release verification](../release-verification.md).
Post-publication live checks are owned by @ayeshLK in
[#272](https://github.com/ayeshLK/komposyn/issues/272); no live verification is
implied by successful publication.
