# Developer relations content revamp

Status: visitor-journey content prepared in this change for
[issue #301](https://github.com/ayeshLK/komposyn/issues/301). Publication,
GitHub metadata application (#304), and the first assigned recurring review
issue/date (#305) remain pending. Preparing this document does not publish the
content or schedule a review.

## Audience and outcome

Lead with the experimental `komposyn` terminal experience and its direction
toward an agent harness. Invite developers to try an early implementation,
give feedback, and contribute. Keep Token Logger discoverable as an
independently useful component, including its separate production
[`komposyn-lg` CLI](https://github.com/ayeshLK/komposyn-lg).

The immediate success criterion is a new visitor completing one guided trial
and knowing how to provide useful feedback. Stars are a secondary signal.
The positioning hypothesis is that a small, inspectable terminal experience,
explicit tool approvals, and a modular direction give early adopters a reason
to explore. Validate that hypothesis with user feedback.

## Content work

Items 1–5 are prepared for review in the linked files below. This records
editorial completion, not publication or a new live-provider trial:

- Claims, released version, and evidence limits: [first-use evidence](harness/first-use-evidence.md)
  and [capability matrix](harness/capability-availability.md).
- Audience, value, demonstration, and starting action: [README](../README.md).
- Guided trial, approval, optional disclosure, feedback: [first use](harness/first-use.md).
- Synthetic actual-UI illustration and text equivalent: [demo](harness/demo/first-use.png)
  and [capture method and limits](harness/first-use-evidence.md#staged-illustration-not-a-live-session).
- Aligned [index](README.md), [harness guide](harness/README.md),
  [vision](vision.md), and [contribution entry point](../CONTRIBUTING.md).

The agreed content scope remains:

1. Audit claims against the current release, source, capability matrix, and
   live validation evidence. Distinguish released behavior from source-only
   changes and proposed work.
2. Rewrite the README opening around the audience, present-day experience,
   and one clear invitation to try it. Follow with a short demonstration,
   quickstart, current capabilities and limits, direction, and contribution
   links. Keep detailed installation, removal, and architecture material in
   linked guides while retaining essential prerequisites and data-handling
   facts at the relevant decision points.
3. Refresh the guided trial: select a provider, exchange text, request a plan,
   and inspect a tool approval on a supported route. Make conditional Linux
   file reads an optional extension with their prerequisites and disclosure
   boundaries. Record unsupported or unverified combinations accurately.
4. Produce a short screenshot or terminal recording using synthetic content.
   Label the version, platform, and authentication route demonstrated; provide
   an equivalent text walkthrough.
5. Align the documentation index, harness guide, vision, and contribution
   entry points with the new reader journey. Preserve library documentation
   and the core/standalone CLI ownership boundary.
6. Prepared for rollout: the description and additive discovery topics below
   describe current capabilities and the experimental direction. Applying them
   remains pending publication; release tags follow the existing release process.
7. Pending publication: establish the recurring review process below and
   record the first due date when the revamped content ships.

General third-party plugin loading, a universal plugin API, file mutation,
shell execution, and web access must remain proposed until implemented and
validated. Bundled provider/UI contracts do not establish a general plugin
platform. This content plan does not change product or architecture scope.

## Acceptance criteria

- A visitor can identify who the project is for, what works today, and how
  to try it from the opening README sections.
- Trial evidence identifies the actual checked revision, binary, environment,
  and authentication route. The staged demo is separate from prior source-build
  live checks; a fresh v0.16.0 signed-binary live trial and broader combinations
  remain pending, as recorded in [verification](harness/first-use-evidence.md).
- Examples, internal links, installer instructions, and metadata agree.
- The demo contains no credentials, private paths, or real user content.
- Readers can find Token Logger, contribution guidance, ordinary feedback,
  and private security reporting without interpreting repository ownership.
- At publication, a review owner, assigned issue, and first due date are
  recorded using the checklist below. This rollout criterion remains pending.

## Manual metadata rollout (#304)

Prepared description:

> Experimental terminal chat with explicit tool approvals, evolving toward a
> modular agent harness. Includes a reusable Go Token Logger library.

Prepared topics: `go`, `cli`, `terminal`, `ai`, `llm`, `openai`, `anthropic`,
`token-usage`, `cost-tracking`. These describe the bundled chat and library;
the description frames the harness as direction. Neither implies general
plugin loading or future tools.

Publication and application remain pending. PR #306 was open when this
procedure was prepared; an integration-branch commit is not publication.
After #303's content is published, use the following read-only commands to
identify the actual publication revision and current default branch:

```sh
gh pr view 306 --repo ayeshLK/komposyn --json state,mergedAt,mergeCommit,url
gh repo view ayeshLK/komposyn --json defaultBranchRef,description,repositoryTopics
publication_revision=$(gh pr view 306 --repo ayeshLK/komposyn --json mergeCommit --jq '.mergeCommit.oid // empty')
default_branch=$(gh repo view ayeshLK/komposyn --json defaultBranchRef --jq '.defaultBranchRef.name')
test -n "$publication_revision"
checked_revision=$(gh api "repos/ayeshLK/komposyn/commits/${default_branch}" --jq .sha)
gh api "repos/ayeshLK/komposyn/commits/${checked_revision}" --jq '{sha, html_url}'
gh api "repos/ayeshLK/komposyn/compare/${publication_revision}...${checked_revision}" --jq '{status, publication: .base_commit.sha}'
```

Proceed only when the PR is `MERGED`, the publication SHA is nonempty, and
comparison status is `identical` or `ahead` (the default branch contains the
publication revision). If content ships through another PR, identify that
reviewed publication revision instead. Open the public default-branch README
and follow its links to installation, guided trial, demo/text equivalent,
capability evidence, Token Logger, contribution, ordinary feedback, and private
security reporting. Check that the published claims and evidence limits agree
with the proposed metadata. Record the checked revision if the default branch
has advanced. Stop and resolve missing content or contradictory claims before
applying metadata.

After those checks, apply the prepared description and add topics while
preserving any topics present at rollout:

```sh
gh repo edit ayeshLK/komposyn \
  --description 'Experimental terminal chat with explicit tool approvals, evolving toward a modular agent harness. Includes a reusable Go Token Logger library.' \
  --add-topic go,cli,terminal,ai,llm,openai,anthropic,token-usage,cost-tracking
gh repo view ayeshLK/komposyn --json description,repositoryTopics
```

Recheck the public repository landing page against the returned metadata and
repeat the landing-page-to-trial-to-feedback link journey. Reuse the recorded
installation, prior live-provider, and native evidence in
[first-use evidence](harness/first-use-evidence.md); do not repeat successful
live tests unless changed behavior or a gap requires it. A link/text check
cannot resolve the pending signed-binary live trial or broaden platform/auth
coverage. Record any new trial's version, environment, auth route, outcome,
and gaps separately.

Write the actual publication date (from the publication event, not this
preparation date), publication URL/SHA, checked revision, before/after metadata,
visitor-journey outcomes, reused evidence and its scope, unresolved gaps, and
rollout date to a local Markdown file. Post that reviewed record to #304:

```sh
gh issue comment 304 --repo ayeshLK/komposyn --body-file /tmp/komposyn-304-rollout.md
```

Pass the publication date and revision to #305 for its assigned review and
fourteen-day due date. Keep #304 open until publication, metadata application,
and the public checks are recorded. Metadata publication is no evidence of
adoption or demand. This procedure creates no release tags or automation.

## Ongoing documentation review

Review every two weeks, and update affected documentation whenever a release
changes user-facing behavior. The repository maintainer, ayeshLK, is the
default accountable owner and may delegate an individual review.

The content must first be published on the default branch. Then complete
[metadata rollout #304](https://github.com/ayeshLK/komposyn/issues/304) against
that published content and
[review setup #305](https://github.com/ayeshLK/komposyn/issues/305). Neither is
complete merely because this PR contains prepared content. Record the actual
publication date and URL, applied description/topics, and assigned review issue
in the rollout evidence. Do not invent a due date before publication.

Use the [manual review setup guide](contributing/documentation-review.md)
for the reusable issue body and date procedure. At rollout, create a
documentation review issue assigned to the owner with a due date fourteen
days after publication. At each completed review, record
the outcome and create or roll forward the next review for fourteen days
later. This is a manual recurring process until automation is separately
implemented; this plan alone does not schedule reminders.

Use this checklist in each review issue:

- [ ] Compare README positioning and capability claims with shipped behavior.
- [ ] Exercise quickstart commands and the guided trial in a recorded environment.
- [ ] Check provider, authentication, platform, and live-validation claims.
- [ ] Review installation prerequisites, links, demo accuracy, and release references.
- [ ] Update roadmap wording and remove resolved limitations.
- [ ] Check GitHub description, topics, and contribution entry points.
- [ ] Review new-user feedback and recurring questions; record concrete follow-ups.
- [ ] Record reviewer, date, release/commit, checks performed, and remaining gaps.
- [ ] Record the next owner and due date.

Only add a “last verified” date to material actually checked. Preserve explicit
evidence limits: reviewing text is not a successful live provider or platform
test. Release authors update affected documentation as part of their change;
the biweekly review catches remaining drift.

## Evaluation

Ask early testers whether they understood the project before installation,
completed the trial, and knew what to try or contribute next. Use voluntary
feedback and sanitized issue reports; this plan introduces no telemetry.
Use the first two biweekly reviews to identify onboarding failures and revise
the positioning hypothesis. Do not treat an absence of feedback as adoption.
