# Standalone CLI repository bootstrap

This directory is a reviewable repository-root template for
`ayeshLK/komposyn-lg`. It contains repository policy and automation only; it
does not contain extracted CLI source. Apply these files at the destination
repository root only after issue #46's verification gates pass and access is
available. Keep the combined repository authoritative until a signed
standalone release has been published and verified.

The initial standalone module is `github.com/ayeshLK/komposyn-lg`, Go 1.26,
and requires `github.com/ayeshLK/komposyn v0.7.0`. Do not add a permanent
`replace` directive. The proposed first CLI release is v0.7.1; CLI and core
versions are independent. Transfer CLI implementation, tests, documentation,
installers and release machinery from the recorded extraction commit, then
apply the later verification fixes recorded in the readiness ledger, including
PR #130.

Before enabling publication, transfer and validate the complete signed release
pipeline from the combined repository. New standalone artifacts must be
accepted only when signed by
`https://github.com/ayeshLK/komposyn-lg/.github/workflows/release.yml@refs/heads/master`
with issuer `https://token.actions.githubusercontent.com`. Existing combined
artifacts retain their combined-repository identity; do not trust a broad
identity pattern.
