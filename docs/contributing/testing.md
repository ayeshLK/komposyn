# Testing guide

The required baseline for behavior changes is:

```sh
go test ./...
go vet ./...
go test -race ./...
make check
```

Run focused package tests first, then the smallest applicable matrix below. CI
remains the final cross-platform check.

| Change area | Required coverage |
| --- | --- |
| Domain model or validation | Focused library tests, full tests, and vet |
| SQLite storage | Idempotency, rollback, cursor, and race tests |
| Migrations | Fresh database, historical upgrade, complete migration history, and newer-schema rejection |
| Provider adapters | Detection, normalization, malformed input, immutable snapshot, repeated import, offsets, and replacement tests |
| Collector | Polling, cancellation, unavailable source, and health behavior |
| Reporting and time | Ordering, output formats, fractional offsets, DST folds, and local civil-hour buckets |
| Settings | Round-trip, permissions, replacement failure, and platform behavior |
| Diagnostics | Sanitization, deduplication, persistence, and disclosure boundaries |
| Harness protocol | Lifecycle, malformed input, unknown methods, stable errors, message limits, stdout/stderr, metadata atomicity, and duplicates |
| CLI | Focused command tests plus the full suite |
| Release or installer | Shell syntax, release build, signature/checksum verification, lifecycle verifier, and platform-specific behavior |
| Workflow or security | YAML review, least-privilege permissions, immutable action references, and relevant checks |
| Documentation | Local links, command accuracy, examples, and consistency with implementation |

## Evidence

PRs should list exact commands and whether each passed. If a check cannot be
run locally, say why and identify the GitHub Actions job that will cover it.
Never use real provider transcripts or include prompts, responses, credentials,
or unsanitized diagnostics in fixtures or evidence.
