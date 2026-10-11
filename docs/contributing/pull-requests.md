# Pull request guide

Pull requests should be easy to scan before they are easy to approve. Keep the
title action-oriented and keep the description focused on the change in the
PR, not the entire repository history.

## Recommended description structure

Use this order and keep a blank line between headings, paragraphs, and lists:

```markdown
## Summary

Explain what changed and why in two or three sentences.

## Scope and impact

- User impact:
- Maintainer or operational impact:
- Files or packages in scope:

## Design and invariants

Explain the relevant architecture, privacy, storage, protocol, or release
constraints.

## Validation

- `go test ./...` — passed
- `git diff --check` — passed
- Manual validation — describe the result or say not run

## Compatibility and risk

- Migration impact: none, or explain the migration
- Protocol impact: none, or explain compatibility
- Release/installer impact: none, or explain verification
- Security/privacy impact: none, or explain the review

## Follow-up

List deferred work, known limitations, or say “None”.
```

Use short paragraphs and bullets. Put commands and code in fenced blocks or
inline code. Do not use a single dense paragraph for the summary, validation,
and risk information.

## Evidence rules

- State the exact command and outcome; do not write only “tests passed”.
- Distinguish automated checks from manual checks.
- Explain checks that were not run and identify the corresponding CI job when
  possible.
- Mention documentation-only scope explicitly when applicable.
- Never include provider transcripts, prompts, responses, credentials,
  unsanitized diagnostics, or private signing material.

## Review handoff

Before requesting review, confirm the PR has a focused title, rendered headings,
blank lines around lists, a validation section, compatibility impact, and a
follow-up section. Update the description when review changes alter scope or
validation; do not leave the original description misleading.
