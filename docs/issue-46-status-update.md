# Issue #46 status update

**Draft for review; not posted to GitHub.**

The accepted extraction baseline is the published `komposyn` v0.7.0 release:
source commit `d66b4117d8b7771f9c3c22b3b251e19054ba9e88`, combined CLI/core
version `0.7.0`, protocol version 1, and SQLite schema 15.

PR #126 is merged and is part of the v0.7.0 ancestry. It completed the API,
schema refusal, migration backup, diagnostic routing, and separate CLI/core
version preparation. PR #130 is also merged; it expanded historical release
verification after the extraction commit. Its verifier and Windows archive
handling changes must transfer with the CLI.

The published v0.7.0 checksums and signatures were freshly verified for all
six archives and the signed `SHA256SUMS`. All six archives contain the expected
executable, license, notice, README, and third-party license files. The exact
signing identity is
`https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master`
with issuer `https://token.actions.githubusercontent.com`. Linux amd64 runtime
verification reported CLI/core `0.7.0`; protocol-v1 initialize and shutdown
also passed.

The signed manifest SHA-256 is
`82057898bf5906585087108ed0a29db32fe99226a23b913d671e300c0df482e2`. The
archive SHA-256 values independently matched that manifest:

| Target | SHA-256 |
| --- | --- |
| macOS amd64 | `b0af004ab8028d8ce1980801bf2ecde47e6c56acfaba4e14026abee9f11fa30b` |
| macOS arm64 | `99c563e939328adf6a8e4d5cc5d22edfaba9e751036c1d1e360a27447e77a76b` |
| Linux amd64 | `a1e1930d131a5f235beda80bbbb19ca956485ebaac819cbdeb1082bf665e79f9` |
| Linux arm64 | `0470314209d677ab6c5074507f3b199f58b88bef69dec70c4568e516759f1760` |
| Windows amd64 | `06edc6ea9756221791d95442cc5d5567c2a29439f34f26cf84edb53b7725cf38` |
| Windows arm64 | `c630e827c6adbd169f787f8783122050bf899e33c53bf6600fa4cbe120f8662b` |

On Linux amd64, the full v0.6.0-to-v0.7.0 historical migration fixture passed,
including identity, cursor, pricing, settings, collector history, reviewed
diagnostics, backup restoration, and data-preserving uninstall. Active
background collector upgrades from signed v0.5.0 and v0.6.0 binaries to the
signed v0.7.0 release also passed: the installer stopped the old collector,
replaced the executable, restarted under a new process and instance, preserved
usage and settings, and left one running background lease owner.

The native historical lifecycle gates are now complete. PR #131's workflow
passed all four v0.5.0/v0.6.0 active-collector upgrades on macOS arm64 and
Windows amd64, and the full v0.6.0 migration, backup restoration, and
uninstall fixture on both systems. See
[workflow run 39037544184](https://github.com/ayeshLK/komposyn/actions/runs/39037544184).

The following gates remain open:

- Execute published binaries natively on Linux arm64, macOS amd64, and Windows
  arm64. Current runtime evidence covers Linux amd64, macOS arm64, and Windows
  amd64. A six-target native workflow now performs signature/checksum,
  executable-version, and protocol-v1 checks; its new run is pending.
- Prepare the destination repository bootstrap and resolve access to
  `ayeshLK/komposyn-lg`. GitHub currently returns “Could not resolve to a
  Repository” for that name, so existence/access cannot yet be distinguished.
  A reviewable root-file bootstrap template is prepared locally; it includes
  Apache attribution, scoped policy, Go module pin, read-only CI, CodeQL,
  Dependabot, and Release Please config, without CLI source or publication
  workflow.

Proposed first standalone CLI version: `v0.7.1`, requiring the published core
tag `v0.7.0`; later CLI and core versions will advance independently. The
combined repository remains authoritative. No CLI extraction, standalone
publication, legacy removal, tag operation, or repository permission change
has happened.
