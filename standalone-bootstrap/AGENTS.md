# Agent instructions

This repository owns the `komposyn-lg` executable and its CLI policy,
interactive experience, settings, data-directory selection, process lifecycle,
and versioned JSONL harness transport. The reusable domain model, SQLite
storage and migrations, provider adapters, collector behavior, pricing, and
reports remain in `github.com/ayeshLK/komposyn`.

Preserve protocol version 1, stable error codes, stdout purity, exact JSON/CSV
values, data paths, consent, source-content privacy, and the meaning of
`plugin_version` (core version). `cli_version` identifies this executable.
Do not add a general plugin runtime/API, telemetry, prompt/response capture,
self-updater, automatic down-migrations, or a permanent module `replace`.

Read `CONTRIBUTING.md`, `SECURITY.md`, `LICENSE`, and `NOTICE` before changes.
Inspect the worktree and preserve unrelated changes. Keep generated binaries,
databases, release archives, and agent metadata out of commits. Pin GitHub
Actions to immutable commit SHAs and keep workflow permissions least-privilege.

Run `go mod verify`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
and the repository's `make check` before a release. Release readiness also
requires the native six-target and historical lifecycle evidence in the
issue #46 handoff; cross-compilation is not runtime evidence.
