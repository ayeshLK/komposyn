# Contributing

Contributions to KompoSyn Token Logger CLI are provided under Apache-2.0; see
`LICENSE` and `NOTICE`. Read `AGENTS.md` and `SECURITY.md` before changing the
CLI, installer, protocol, or release process.

The CLI depends on the separately versioned core module
`github.com/ayeshLK/komposyn`. Keep CLI and core versions independent, avoid a
permanent `replace`, and keep domain behavior in core. Preserve the library/CLI
boundary and the protocol-v1 contract. Do not capture prompts, responses,
credentials, or provider source content, and do not add telemetry.

Use focused tests while iterating, then run `go mod verify`, `go vet ./...`,
`go test ./...`, `go test -race ./...`, and `make check`. For installer or
release changes, also run the platform lifecycle checks and verify signatures,
checksums, upgrade preservation, and data-preserving uninstall. Report exact
commands and results in pull requests. Never create or move release tags
manually; use the reviewed Release Please and protected publication workflow.
