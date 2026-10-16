# Installation

Komposyn is distributed as signed release archives with platform installers.
Installers verify both the SHA-256 checksum and the Cosign blob signature before
installing the executable.

## Supported release targets

Release archives currently target Linux and macOS on amd64 and arm64, plus
Windows amd64 and arm64 artifacts. The current PowerShell installer selects the
Windows amd64 archive; Windows arm64 users should use the matching release
archive explicitly until installer architecture selection is added. The
standalone binary is named `komposyn-lg` (or `komposyn-lg.exe` on Windows).

## Unix: macOS and Linux

```text
bash scripts/install.sh
```

The Unix installer requires `curl`, `jq`, `tar`, and `cosign` when downloading
and verifying a published release. It resolves GitHub's latest stable release,
prints the concrete tag, then downloads every artifact from that immutable tag.
Drafts, prereleases, malformed metadata, and API failures are rejected. Set
`KOMPOSYN_VERSION=vX.Y.Z` to pin a specific stable release. For local
verification or air-gapped preparation, set `KOMPOSYN_RELEASE_DIR` to a
directory containing the archive, `SHA256SUMS`, signature, and certificate;
the manifest's version is validated against any explicit pin.

Useful overrides include `KOMPOSYN_VERSION`, `KOMPOSYN_REPO`,
`KOMPOSYN_INSTALL_DIR`, and `KOMPOSYN_RELEASE_DIR`.

## Windows

Run the PowerShell installer from a trusted checkout or release artifact
directory:

```powershell
.\scripts\install.ps1
```

PowerShell and `cosign` are required. By default, the installer resolves and
prints the latest stable release tag, rejecting malformed or unavailable
metadata, and pins all artifacts to that tag. Set `$env:KOMPOSYN_VERSION` to
`vX.Y.Z` to pin a specific stable version. Use `KOMPOSYN_RELEASE_DIR` and
`KOMPOSYN_INSTALL_DIR` to select local artifacts and an installation path. Add
the installation directory to `PATH` if the command is not found in a new
terminal.

## Verify an extracted release

The repository verifier checks checksums, archive contents, installation,
upgrade-by-replacement, and data-preserving uninstall:

```text
bash scripts/verify-release.sh /path/to/release-dir
```

## Upgrade and uninstall

For the initial release, upgrade by rerunning the latest standalone installer.
It replaces the executable and preserves the existing SQLite database,
settings, consent choices, diagnostics, and data directory. New installations
use `$HOME/.komposyn/token-logger/data` (or the equivalent under the Windows
user profile); an existing legacy data directory is reused in place when
detected. Schema migrations run when the new executable opens the database.
In-CLI self-updating is intentionally deferred.

If a managed collector is active, the installer requests graceful stop before
executable replacement. It automatically restarts only a collector that was
already running in detached `background` mode. Foreground and embedded REPL
owners remain stopped and receive mode-specific manual-resume instructions; the
installer never converts them into detached collection. A stop timeout aborts
replacement. A background restart failure leaves the verified new executable
installed but exits non-zero with an explicit `watch start` remediation. Set
`TOKEN_LOGGER_DATA_DIR` while running the installer when collection uses a
non-default data directory.

The uninstall scripts stop a managed collector before removing the
executable and preserve local data, registered sources, and runtime history. A
stop timeout prevents removal.
Back up the data directory before manually removing it; uninstall does not
delete the database or settings for you. See [Upgrades](upgrades.md).
