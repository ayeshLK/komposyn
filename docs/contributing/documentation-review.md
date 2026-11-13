# Manual documentation review

Review documentation every fourteen days. **ayeshLK** is the default
accountable owner and may name a delegate for an individual review. Release
and change authors update affected documentation whenever user-facing behavior
changes; do not wait for the next review. This manual process schedules no
automated reminders.

This procedure and its issue body are **incomplete preparation until populated
with actual dates, publication evidence, and the returned assigned issue URL**.
Do not create the first review before the prerequisite content rollout is
published. In particular, [#305](https://github.com/ayeshLK/komposyn/issues/305)
remains open until its assigned review and publication evidence exist; a
committed template alone does not satisfy it. Track rollout status in the
[content plan](../devrel-content-plan.md#ongoing-documentation-review).

## First review after publication

Use the merged rollout PR as publication evidence, rather than the preparation
commit's date. For this rollout the prerequisite is
[PR #306](https://github.com/ayeshLK/komposyn/pull/306). Run the following from
a checkout with `gh` and Python 3 after that PR has merged. The guard stops
before date calculation if publication has not happened:

```sh
PUBLICATION_TIMESTAMP=$(gh pr view 306 --repo ayeshLK/komposyn --json mergedAt --jq '.mergedAt // empty')
[ -n "$PUBLICATION_TIMESTAMP" ] || { echo 'Rollout is not merged; stop here.'; exit 1; }
PUBLICATION_REVISION=$(gh pr view 306 --repo ayeshLK/komposyn --json mergeCommit --jq '.mergeCommit.oid')
PUBLICATION_URL="https://github.com/ayeshLK/komposyn/commit/$PUBLICATION_REVISION"
PUBLICATION_DATE=$(python3 -c 'import datetime,sys; print(datetime.datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00")).date())' "$PUBLICATION_TIMESTAMP")
DUE_DATE=$(python3 -c 'import datetime,sys; print(datetime.date.fromisoformat(sys.argv[1]) + datetime.timedelta(days=14))' "$PUBLICATION_DATE")
printf 'Published: %s\nRevision: %s\nURL: %s\nFirst due date (UTC): %s\n' "$PUBLICATION_TIMESTAMP" "$PUBLICATION_REVISION" "$PUBLICATION_URL" "$DUE_DATE"
```

The first due date is **publication date + 14 calendar days**, using UTC dates.
If it has already passed, retain that due date and explicitly mark the review
as overdue, with the actual creation date and reason. Do not silently restart
the clock. Later reviews use the actual completed review date + 14 days.

Copy the reusable body below to `/tmp/komposyn-documentation-review.md`. Replace
its scheduling placeholders with the real values above and select cycle 1.
Keep unperformed checks pending. Inspect the file for private information,
confirm the issue target, then create the assigned issue:

```sh
gh repo view ayeshLK/komposyn --json nameWithOwner
REVIEW_OWNER=ayeshLK
REVIEW_URL=$(gh issue create --repo ayeshLK/komposyn \
  --title "Documentation review due $DUE_DATE" \
  --assignee "$REVIEW_OWNER" \
  --body-file /tmp/komposyn-documentation-review.md)
printf '%s\n' "$REVIEW_URL"
```

Verify the returned issue's assignee, concrete due date, checklist, evidence
fields, and recurrence instructions with `gh issue view "$REVIEW_URL"`.
Record the actual publication timestamp, revision, publication URL, first due
date, owner, and returned review URL in #305 and the content plan. For the issue
record, prepare and inspect `/tmp/komposyn-review-setup-evidence.md` containing
those values and any overdue status, then run:

```sh
gh issue comment 305 --repo ayeshLK/komposyn \
  --body-file /tmp/komposyn-review-setup-evidence.md
```

Reconcile the content plan's pending review-setup status in a reviewed change.
Only then assess #305's acceptance criteria for closure. Future review cycles
remain ongoing maintenance.

## Reusable first and subsequent review body

Copy the following body. Replace `PENDING` scheduling fields before filing;
review findings remain pending until actually checked. A checked box must link
to its result or explain the scoped check, not imply that every provider or
platform passed.

```markdown
Part of https://github.com/ayeshLK/komposyn/issues/301.
Review setup: https://github.com/ayeshLK/komposyn/issues/305.

## Schedule and scope

- Cycle: PENDING (1, 2, or subsequent)
- Accountable owner / assigned reviewer: ayeshLK (or named delegate)
- Created on (UTC date): PENDING
- Original due date (UTC): PENDING
- Status: PENDING (scheduled / overdue, with reason and actual dates)
- Publication timestamp (UTC), revision, and URL: PENDING
- Previous review issue and completion date: PENDING (none for cycle 1)
- Reviewer and actual review date (UTC): PENDING until performed
- Release/binary version and source commit checked: PENDING
- Environment: PENDING (OS, architecture, terminal, native/fixture context)
- Provider, model, and authentication route checked: PENDING (no secrets)

## Checks

- [ ] README explains audience, experimental status, current value, and one
      starting action; Token Logger library and production komposyn-lg remain
      discoverable. Compare released, source-only, and proposed claims.
- [ ] Follow the public journey through installation, quickstart, and the
      guided trial: provider selection, text, plan, and tool approval. Record
      exact commands/actions and outcomes; optional Linux reads require the
      documented trusted backend and synthetic exact-file approval.
- [ ] Check provider/auth/platform claims against scoped evidence; record
      unsupported or unverified combinations without implying compatibility.
- [ ] Verify prerequisites, installer examples, release references, and links.
- [ ] Check demo freshness, version/platform/auth labels, synthetic content,
      and equivalent text walkthrough. Keep staged illustrations separate
      from live-provider or native-platform validation.
- [ ] Review roadmap wording and resolved limits against actual behavior.
- [ ] Check GitHub description/topics and ordinary contribution/feedback
      routes; keep private security reporting distinct.
- [ ] Review voluntary new-user feedback and recurring questions. In cycles
      1 and 2, explicitly record whether volunteers understood the project,
      completed the trial, and found contribution/feedback routes. Record
      concrete follow-ups; no telemetry and no adoption inference from silence.
- [ ] Add last-verified dates only for material actually checked, scoped to
      those checks; leave pending evidence pending.
- [ ] Record outcomes, gaps, follow-ups, and an assigned next review below.

## Evidence and outcomes

For each check record: material/link; exact check actually performed; evidence
kind (text review / fixture / live provider / native platform); checked date;
release/commit; environment and auth route; observed outcome; gaps; follow-up
issue/owner. Redact credentials, prompts, responses, private paths, and account
identifiers. Use private security reporting for sensitive findings.

PENDING — no review checks have been performed by creating this issue.

Voluntary feedback (required explicitly for cycles 1 and 2): PENDING.
Record “no voluntary feedback received” when applicable, not successful
onboarding or adoption. Link sanitized reports and actionable follow-ups.

## Completion and next review

- Actual completion date (UTC): PENDING
- Outcome and unresolved gaps / follow-up issues with owners: PENDING
- Next owner: ayeshLK (or named delegate)
- Next due date (actual review completion date + 14 days, UTC): PENDING
- Next assigned review issue URL: PENDING

Before closing, create or roll forward the next assigned review with that
concrete due date and link it here. Preserve the original due date and overdue
history. This is a manual process with no automated reminders. Update affected
documentation when behavior changes, including between reviews.
```

## Completing a review and continuing the cadence

Record the actual checks and gaps before checking boxes or dating material.
Use the same body for subsequent cycles, retaining the publication reference
and linking the previous review. Calculate the next date from the **actual
completion date**, replacing the placeholder below with the recorded UTC date:

```sh
REVIEW_DATE='YYYY-MM-DD'
DUE_DATE=$(python3 -c 'import datetime,sys; print(datetime.date.fromisoformat(sys.argv[1]) + datetime.timedelta(days=14))' "$REVIEW_DATE")
printf 'Next review due: %s\n' "$DUE_DATE"
```

Populate a new body file and use the same assigned `gh issue create` command,
or update an existing next-review issue from a body file with
`gh issue edit NEXT_ISSUE --repo ayeshLK/komposyn --add-assignee ayeshLK --body-file /tmp/komposyn-documentation-review.md`.
If delegating, assign the named delegate and record that owner in both issues.
Verify the assignment and due date, link the returned next-review URL in the
completed review, and record its outcome before closing. If overdue, record the
missed due date, actual completion date, and reason; do not rewrite history.
