# Install the experimental `komposyn` CLI

`komposyn` is the experimental in-memory text chat in this repository. It is
separate from the production `komposyn-lg` executable. The README quick-start
commands fetch the installer scripts from the moving `master` branch. Fetching
a script does not verify or install a binary: when run, the installer resolves
a stable binary release and verifies its signed checksum manifest, archive
digest, contents, and executable metadata before replacement.

As checked on 2026-10-29, the latest GitHub release is v0.9.0 and has no binary
assets; v0.10.0 and its signed binaries are not published. The scripts can be
downloaded from `master`, but installation needs a suitable signed binary
release. The pinned examples below become usable after the v0.10.0 source tag
and signed release assets are available.

Before announcing availability, verify that
[`v0.10.0`](https://github.com/ayeshLK/komposyn/releases/tag/v0.10.0) is
published, that the installer and Windows architecture helper resolve from the
`v0.10.0` source tag, and that the release contains the six platform archives,
`checksums.txt`, and `checksums.txt.sigstore.json` described in the
[release verification reference](release-verification.md). A source tag alone
does not mean the signed binary release is ready to download.

Installers download one stable release tag, verify the signed `checksums.txt`
manifest and the selected archive digest, inspect archive contents, and check
the executable's version, commit, and target before replacing an installation.
They do not read or change provider credentials/settings, initialize chat,
create a database, or make a model request.

## Linux and macOS

Requirements: Bash, `curl`, `tar`, `sha256sum` or `shasum`, and Cosign v2.4.3
on `PATH`. See the [Cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/).
`jq` is also required when resolving the latest stable release. A version-pinned
install does not need `jq`.

For an immutable installer source, download the script from a version tag.
The script source version and selected binary version are separate: the tag in
the URL chooses the installer implementation; `--version` chooses the signed
binary. This example pins the script to v0.10.0 and leaves the binary version
unselected so it resolves latest stable when run:

```sh
installer_version=v0.10.0
installer_dir="$(mktemp -d)"
curl -fsSLo "$installer_dir/install.sh" \
  "https://raw.githubusercontent.com/ayeshLK/komposyn/${installer_version}/scripts/install.sh"
```

Review `$installer_dir/install.sh` with your preferred text editor before
execution if you want to inspect the downloaded script. In the same Bash
session, run it to resolve the latest stable binary:

```sh
bash "$installer_dir/install.sh"
```

To select a binary version explicitly:

```sh
bash "$installer_dir/install.sh" --version v0.10.0
```

To upgrade, rerun the installer. Omit `--version` for latest stable, or pass
`--version vX.Y.Z` to pin the upgrade. A downloaded, version-pinned script does
not update its own source; download the script from a newer immutable tag to
use a newer installer implementation.

The default install directory is `~/.local/bin`. To choose another directory:

```sh
bash "$installer_dir/install.sh" --install-dir "$HOME/bin/komposyn"
```

The installer cannot modify the parent shell's `PATH`. Add the selected
directory to your shell startup file, then open a new terminal. For example,
for the default directory in Bash or Zsh:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

For local fixture or release-candidate testing, provide an explicit version
and a directory containing that target archive, `checksums.txt`, and
`checksums.txt.sigstore.json`:

```sh
bash "$installer_dir/install.sh" --version v0.10.0 \
  --artifacts-dir "$HOME/komposyn-release-artifacts"
```

The verification identity and OIDC issuer are fixed in the installer. The
optional `KOMPOSYN_RELEASE_METADATA_URL` and `KOMPOSYN_COSIGN_BIN` environment
values are test seams for alternate metadata fixtures and Cosign executables;
they cannot change the accepted release signer identity.

Uninstall only the `komposyn` executable and its owned install artifacts:

```sh
uninstaller_version=v0.10.0
uninstaller_dir="$(mktemp -d)"
curl -fsSLo "$uninstaller_dir/uninstall.sh" \
  "https://raw.githubusercontent.com/ayeshLK/komposyn/${uninstaller_version}/scripts/uninstall.sh"
bash "$uninstaller_dir/uninstall.sh"
```

Pass `--install-dir` to uninstall from a custom directory. Repeating
uninstallation is safe. It preserves `~/.komposyn/auth.json`,
`~/.komposyn/settings.json`, `komposyn-lg`, and Token Logger data.

## Windows

Requirements: Windows PowerShell 5.1 or PowerShell 7 and Cosign v2.4.3 on
`PATH`. See the [Cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/)
for supported setup options. The installer detects native Windows ARM64 even
when PowerShell is running under x64 emulation.

Download both the installer and its architecture helper from the same
immutable source tag before running the installer:

```powershell
$installerVersion = 'v0.10.0'
$scriptDir = Join-Path $env:TEMP ('komposyn-installer-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $scriptDir -ErrorAction Stop | Out-Null
$source = "https://raw.githubusercontent.com/ayeshLK/komposyn/$installerVersion/scripts"
Invoke-WebRequest -ErrorAction Stop -Uri "$source/install.ps1" -OutFile (Join-Path $scriptDir 'install.ps1')
Invoke-WebRequest -ErrorAction Stop -Uri "$source/windows-architecture.ps1" -OutFile (Join-Path $scriptDir 'windows-architecture.ps1')
```

The two downloaded files are from the same immutable source tag. You can
inspect them before execution. In the same PowerShell session, run the
installer:

```powershell
try { & (Join-Path $scriptDir 'install.ps1') } finally { Remove-Item -LiteralPath $scriptDir -Recurse -Force -ErrorAction SilentlyContinue }
```

The source script is pinned to `v0.10.0`; `-Version` selects the binary
release. Omit `-Version` to resolve the latest stable binary, or pass
`-Version vX.Y.Z` to pin it. To upgrade, repeat the download and execution
steps with the installer source tag you want to use; a downloaded script does
not update itself. Close a running KompoSyn chat before upgrading; Windows may
not allow replacement of an executable that is still running. The default
install directory is `%LOCALAPPDATA%\Komposyn\bin`. Choose another directory
with `-InstallDir`.
The installer does not change `PATH`. To add the default directory to the
current user's persistent PATH:

```powershell
$bin = Join-Path $env:LOCALAPPDATA 'Komposyn\bin'
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $bin) {
    [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';') + ';' + $bin).TrimStart(';')), 'User')
}
```

Open a new terminal after changing `PATH`. To install local candidate
artifacts, pass both `-Version vX.Y.Z` and `-ArtifactsDir PATH`. The optional
`-MetadataUri` and `-CosignPath` parameters support fixture testing and
alternate Cosign executable locations; the accepted signer identity remains
fixed.

Uninstall with the PowerShell script from that same source tag:

```powershell
$uninstallerVersion = 'v0.10.0'
$uninstallerDir = Join-Path $env:TEMP ('komposyn-uninstaller-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $uninstallerDir -ErrorAction Stop | Out-Null
try {
    $uninstallerUri = "https://raw.githubusercontent.com/ayeshLK/komposyn/$uninstallerVersion/scripts/uninstall.ps1"
    $uninstallerPath = Join-Path $uninstallerDir 'uninstall.ps1'
    Invoke-WebRequest -ErrorAction Stop -Uri $uninstallerUri -OutFile $uninstallerPath
    & $uninstallerPath
} finally {
    Remove-Item -LiteralPath $uninstallerDir -Recurse -Force -ErrorAction SilentlyContinue
}
```

Pass `-InstallDir` if the executable is in a custom directory. If Windows
cannot remove a running executable, close the chat and retry. Uninstallation
preserves `%USERPROFILE%\.komposyn\auth.json`,
`%USERPROFILE%\.komposyn\settings.json`, `komposyn-lg`, and Token Logger data
and backups.

## Start the chat

Check the executable and start it:

```sh
komposyn --version
komposyn --help
komposyn
```

In the chat, open `/model`, add an OpenAI provider with an API key or ChatGPT
sign-in, or add an Anthropic provider with an API key. Then explicitly choose a
model. Login and model discovery do not send a chat inference request; submitted
messages go to the selected provider. The model selection and chat context stay
in memory. Validated auth profiles and UI settings persist locally; auth
credentials are stored as plaintext in a private configuration file. See the
[harness guide](harness/README.md) and [temporary chat security notes](harness/phase-one-security.md)
for setup, recovery, and security details.

## Verify an installation

The binary supports `--version` and `--help` without loading configuration.
Use isolated files when testing `--check`, which reads the selected config
paths:

```sh
komposyn --version
komposyn --help
tmp_dir="$(mktemp -d)"
komposyn --auth-file "$tmp_dir/auth.json" \
  --settings-file "$tmp_dir/settings.json" --check
rm -rf "$tmp_dir"
```

The `komposyn` and `komposyn-lg` executable names are distinct, so they can
share a bin directory. Installing or uninstalling `komposyn` does not alter
the production executable or either tool's data.

## Troubleshooting

- If latest-release resolution fails on Linux or macOS, confirm `jq` is on
  `PATH`; the installer uses it to resolve GitHub release metadata.
- If installation reports that Cosign is missing, install Cosign v2.4.3 and
  make sure `cosign` is on `PATH`. The installer stops before replacing the
  executable if signature, checksum, archive, or executable validation fails.
- If `komposyn` is not found after installation, add the install directory to
  `PATH` and open a new terminal. Installation does not edit `PATH`.
- On Windows, close a running chat before upgrading or uninstalling so the
  executable can be replaced or removed.

For release publication and signed asset checks, see
[release verification](release-verification.md). The production CLI's
installation guidance belongs to the
[standalone repository](https://github.com/ayeshLK/komposyn-lg#install).
