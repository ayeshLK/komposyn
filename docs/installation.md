# Install the experimental `komposyn` CLI

`komposyn` is the experimental in-memory text chat in this repository. It is
separate from the production `komposyn-lg` executable. The first signed
`komposyn` binary release is not published until its native and cryptographic
release checks are complete; these instructions apply after a signed release
is available.

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
on `PATH`. `jq` is also required when resolving the latest stable release. A
version-pinned install does not need `jq`.

Download the installer from an immutable source tag, then run it. Update the
source tag below when you want to use a newer installer implementation:

```sh
installer_version=v0.10.0
curl -fsSLo /tmp/komposyn-install.sh \
  "https://raw.githubusercontent.com/ayeshLK/komposyn/${installer_version}/scripts/install.sh"
bash /tmp/komposyn-install.sh
```

The script source is pinned to `v0.10.0`. With no version argument, it
resolves the latest stable binary release when run, then downloads every
release file from that exact tag. To pin the binary version explicitly:

```sh
bash /tmp/komposyn-install.sh --version v0.10.0
```

To upgrade, download the installer again from the source tag you want to use
and rerun it. Leave off `--version` to select the latest stable binary, or
pass a version to pin the upgrade. A previously downloaded installer does not
update itself. For example, with the v0.10.0 script downloaded as above:

```sh
bash /tmp/komposyn-install.sh
bash /tmp/komposyn-install.sh --version v0.10.0
```

The default install directory is `~/.local/bin`. To choose another directory:

```sh
bash /tmp/komposyn-install.sh --install-dir "$HOME/bin/komposyn"
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
bash /tmp/komposyn-install.sh --version v0.10.0 \
  --artifacts-dir "$HOME/komposyn-release-artifacts"
```

The verification identity and OIDC issuer are fixed in the installer. The
optional `KOMPOSYN_RELEASE_METADATA_URL` and `KOMPOSYN_COSIGN_BIN` environment
values are test seams for alternate metadata fixtures and Cosign executables;
they cannot change the accepted release signer identity.

Uninstall only the `komposyn` executable and its owned install artifacts:

```sh
installer_version=v0.10.0
curl -fsSLo /tmp/komposyn-uninstall.sh \
  "https://raw.githubusercontent.com/ayeshLK/komposyn/${installer_version}/scripts/uninstall.sh"
bash /tmp/komposyn-uninstall.sh
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
$scriptDir = Join-Path $env:TEMP "komposyn-installer-$installerVersion"
New-Item -ItemType Directory -Path $scriptDir -Force | Out-Null
$source = "https://raw.githubusercontent.com/ayeshLK/komposyn/$installerVersion/scripts"
Invoke-WebRequest "$source/install.ps1" -OutFile (Join-Path $scriptDir 'install.ps1')
Invoke-WebRequest "$source/windows-architecture.ps1" -OutFile (Join-Path $scriptDir 'windows-architecture.ps1')
& (Join-Path $scriptDir 'install.ps1')
```

The source script is pinned to `v0.10.0`; `-Version` selects the binary
release. Omit `-Version` to resolve the latest stable binary, or use
`-Version v0.10.0` to pin it. To upgrade, download the installer and its
architecture helper again from the source tag you want to use, then rerun the
installer. A previously downloaded installer does not update itself. Close a
running KompoSyn chat before upgrading; Windows may not allow replacement of
an executable that is still running. The default install directory is
`%LOCALAPPDATA%\Komposyn\bin`. Choose another directory with `-InstallDir`.

With the scripts already downloaded in this session, either select the latest
stable binary or pin the upgrade explicitly:

```powershell
& (Join-Path $scriptDir 'install.ps1')
& (Join-Path $scriptDir 'install.ps1') -Version v0.10.0
```
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
Invoke-WebRequest "$source/uninstall.ps1" -OutFile (Join-Path $scriptDir 'uninstall.ps1')
& (Join-Path $scriptDir 'uninstall.ps1')
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
