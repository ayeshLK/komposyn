# First-use demonstration and verification

Reviewed 2026-11-13 for [issue #302](https://github.com/ayeshLK/komposyn/issues/302).
The acceptance boundary is the public journey from installation through the
[existing walkthrough](first-use.md) to ordinary feedback or private security
reporting. Editorial checks and a staged UI illustration are separate from
live-provider and native-backend evidence.

## Release and installation checks

The walkthrough targets **v0.16.0**, source commit
`82edf9621d2fb5768338e23af51e57dac7e6e47e`. GitHub's
[published release](https://github.com/ayeshLK/komposyn/releases/tag/v0.16.0)
contains all six platform archives, `checksums.txt`, and
`checksums.txt.sigstore.json`; the
[binary workflow](https://github.com/ayeshLK/komposyn/actions/runs/43362640195)
succeeded on 2026-11-13, including six build/smoke targets and six signed
native-install jobs. These workflow checks are release/install evidence; they
do not establish live-provider compatibility or read enforcement. This is a
binary release, not merely a source tag.

On Linux `7.0.0-42-generic`, x86_64, the repository installer ran with
`--version v0.16.0 --install-dir` pointing to a disposable directory. Signature
verification reported **Verified OK** and installation succeeded. Cosign
**v3.1.3** was used; this check does not establish the documented v2.4.3
prerequisite on this host. The initial attempt could not write the default
Sigstore cache; setting `TUF_ROOT` to a disposable writable cache allowed the
same verification to complete without changing verification policy.

The installed binary reported version `0.16.0`, commit
`82edf9621d2fb5768338e23af51e57dac7e6e47e`, target `linux/amd64`, and SHA-256:

```text
6abf5afe0d4703c0972bc2ccef2950cf904dddec42e71806f3598d1e50058a10
```

`--help` succeeded. `--check` with isolated absent auth/settings paths reported
`UI: tview` and zero configured provider profiles. These checks used no account
and sent no inference request. macOS, Windows, and arm64 native installation
were not repeated during this content review.

## Staged illustration, not a live session

[The demonstration](demo/first-use.png) contains three captures of the actual
bundled tview UI at the release source commit, run on Linux amd64 with Go
1.26.11 and a 120-column, 40-row tcell simulation screen. The displayed
ChatGPT OAuth profile **Demo ChatGPT**, text reply, approval event, and plan
are synthetic fixtures injected through the UI client contract. No credential
store, OAuth account, provider network request, or native read worker was used.
The public model label `gpt-6-sol` identifies the illustrated route; it is not
evidence that discovery or inference took place during capture.

The sequence shows a text exchange, the `update_plan` approval panel with its
current-conversation scope and provider destination, then `/plan` with three
synthetic steps. The fixture inserts the approval and resulting plan directly;
it does not validate model tool selection, execution, or provider continuation.
The real UI handled navigation to **Allow once** and opening `/plan`. The
numbered [walkthrough](first-use.md) gives equivalent actions in text, including
provider selection and the prompt needed to request the plan from a real model.

Capture method: run the real `tview.Plugin` with its existing `clientStub` and
`tcell.NewSimulationScreen`, send synthetic text through the composer, enqueue
an `ApprovalEvent`, select Allow once, supply a synthetic three-step plan, and
open `/plan`. Read the screen cells after each rendered state and rasterize
their glyphs and colors into PNG; add the visible staging/version/platform/auth
labels outside the UI. The disposable capture helper is not a new runtime
feature or a retained editorial test. The image contains no real account
identifier, credential, private path, or user content. Read capability and
working directory are deliberately unavailable in this fixture.

## Reused live and native evidence

These results were already recorded for
[issue #294](https://github.com/ayeshLK/komposyn/issues/294); this content change
reuses them rather than spending account requests to repeat unchanged behavior.
They are scoped prior evidence, not captures of the staged demonstration or
proof of a new signed-binary live run.

| Evidence | Recorded outcome and limit |
| --- | --- |
| ChatGPT OAuth discovery, planning, clarification, and completed follow-up, 2026-11-12 | Public model `gpt-6-sol`, one authorized account, Linux amd64; both tools explicitly approved once. Development source `27bbaac7cbb4d41773a053e5456a78bbe5ac0bc9` with the documented temporary tool-enablement patch. See the [initial live record](openai-chatgpt-oauth-tool-protocol.md#live-verification-2026-11-12) for binary identity and patch limits. |
| Exact synthetic file, Allow once, cancellation, 2026-11-13 | Operator-reported source build `837ad5f185ec997e8762b3e1c52f9d62ea5a7f4c`, same OAuth route/model, Linux x86_64, trusted root-owned `/usr/bin/bwrap` 0.9.0 mode 0755. Marker returned after inspecting the provider destination; selected profile retained, no fallback observed. See the [follow-up record](openai-chatgpt-oauth-tool-protocol.md#follow-up-live-verification-2026-11-13). |
| Denied synthetic file, 2026-11-13 | Operator-reported source `13b01e4f16912aeb3c05feb9f3b9f05cbfd15c18`; denied read was not executed or disclosed, selected OAuth profile retained, no fallback observed. See [denial evidence and binary identity](openai-chatgpt-oauth-tool-protocol.md#denial-follow-up-live-verification-2026-11-13). |
| Native exact-file enforcement | Existing scoped Linux amd64/Bubblewrap 0.9.0 acceptance under [#215](issue-215-completion-tracking.md) and [#278](https://github.com/ayeshLK/komposyn/issues/278#issuecomment-6798370362) is reused. This editorial change adds no enforcement code and performs no new native acceptance run. |

The source-build results establish the scoped provider/tool journey above.
They do not establish a fresh v0.16.0 signed-binary live trial, every catalog
model or account, OpenAI API-key or Anthropic live compatibility, or broader
native platforms. Those combinations remain unverified. No new live inference
or account access was performed for this content change.

## Content checks and next review

The walkthrough and installation examples were checked against the command
flags, provider/auth capability matrix, release assets, installer scripts, and
read-approval UI. Optional reads retain trusted backend inspection, exact-target
once approval, protected-object refusal, and the displayed provider destination;
no prerequisite was relaxed to make the demonstration work. Changed relative
links and image targets resolve. No prose-mirroring tests were added.

At the next documentation review, retain the separation between staged,
fixture, source-build live, signed-binary, and native evidence. Record any newly
performed provider checks with date, version/commit, binary identity, platform,
public model, authentication route, bounded outcome, and remaining gaps. Request
operator authorization and a request budget before any fresh live account trial;
never attach credentials, private paths, or real conversations to the record.
