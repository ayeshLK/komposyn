# Testing guide

The required baseline for core behavior changes is:

```sh
go mod verify
go vet ./...
go test ./...
go test -race ./...
make check
```

Core CI also runs the public standalone CLI test suite against the candidate
core source. It clones the public CLI without credentials or write permissions
and applies a temporary replacement in the runner's disposable checkout only.
It never edits or commits the core module's `go.mod`.

| Change area | Required coverage |
| --- | --- |
| Domain model or validation | Focused library tests, full tests, vet, and standalone CLI compatibility job |
| SQLite storage | Idempotency, rollback, cursor, and race tests |
| Migrations | Fresh database, historical upgrade, complete migration history, backup failure, migration failure, restoration, and newer-schema rejection |
| Provider adapters | Initial/re-import, append, replacement/path reuse, malformed input, retryable tail completion, unsupported usage, attribution, storage failure, source immutability, and normalization |
| Collector | Polling, cancellation, unavailable source, lease, registry, and health behavior |
| Reporting and time | Ordering, machine-output precision, provenance, fractional offsets, DST folds, and local civil-hour buckets |
| Diagnostics | Sanitization, deduplication, persistence, category/version metadata, and disclosure boundaries |
| Core compatibility | Candidate core CI runs the standalone CLI suite without cross-repository secrets or write permissions |
| Workflow or security | YAML review, least-privilege permissions, immutable Action references, and relevant checks |
| Documentation | Local links, command ownership, examples, and consistency with implementation |

PRs should list exact commands and whether each passed. If a check cannot be
run locally, identify the GitHub Actions job that will cover it. Use only
sanitized synthetic data in tests and evidence.
