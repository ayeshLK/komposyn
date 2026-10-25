# Copyright 2026 Ayesh Almeida
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Commit,
    [Parameter(Mandatory = $true)][string]$ArtifactsDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:OS -ne 'Windows_NT') { throw 'This signed-release lifecycle test must run on Windows.' }
if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'Version must be stable X.Y.Z.' }
if ($Commit -notmatch '^[0-9a-f]{40}$') { throw 'Commit must be a full lowercase SHA.' }

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('komposyn-signed-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
$installDir = Join-Path $tmp 'bin with spaces'
$configDir = Join-Path $tmp '.komposyn'
New-Item -ItemType Directory -Path $installDir, $configDir | Out-Null
$authFile = Join-Path $configDir 'auth.json'
$settingsFile = Join-Path $configDir 'settings.json'
$legacyBinary = Join-Path $installDir 'komposyn-lg.exe'
$architectureScript = Join-Path $scriptDir 'windows-architecture.ps1'
. $architectureScript
$targetArchitecture = Get-KomposynWindowsArchitecture
$authContent = '{"schema_version":1,"credentials":[]}'
$settingsContent = '{"schema_version":1,"ui":{"plugin":"tview"}}'
[System.IO.File]::WriteAllText($authFile, $authContent)
[System.IO.File]::WriteAllText($settingsFile, $settingsContent)
[System.IO.File]::WriteAllText($legacyBinary, 'production CLI fixture')
$authSddl = (Get-Acl -LiteralPath $authFile).Sddl
$settingsSddl = (Get-Acl -LiteralPath $settingsFile).Sddl
$archive = Join-Path $ArtifactsDir "komposyn_${Version}_windows_${targetArchitecture}.zip"
if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) { throw "Signed release archive is missing: $archive" }
$installedBinary = Join-Path $installDir 'komposyn.exe'

function Assert-PreservedState {
    if ([System.IO.File]::ReadAllText($authFile) -cne $authContent) { throw 'Installer changed auth.json contents.' }
    if ([System.IO.File]::ReadAllText($settingsFile) -cne $settingsContent) { throw 'Installer changed settings.json contents.' }
    if ((Get-Acl -LiteralPath $authFile).Sddl -cne $authSddl) { throw 'Installer changed auth.json access protections.' }
    if ((Get-Acl -LiteralPath $settingsFile).Sddl -cne $settingsSddl) { throw 'Installer changed settings.json access protections.' }
    if ([System.IO.File]::ReadAllText($legacyBinary) -cne 'production CLI fixture') { throw 'Installer changed komposyn-lg.exe.' }
}

function Invoke-RealInstaller {
    & (Join-Path $scriptDir 'install.ps1') -Version $Version -InstallDir $installDir -ArtifactsDir $ArtifactsDir
}

function Assert-InstalledCLI {
    $versionOutput = (& $installedBinary --version 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $versionOutput -cne "komposyn version=$Version commit=$Commit target=windows/$targetArchitecture") {
        throw "Installed version check failed: $versionOutput"
    }
    $helpOutput = (& $installedBinary --help 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $helpOutput -notmatch '(?m)--check') { throw 'Installed help omitted --check or failed.' }
    $checkOutput = (& $installedBinary --check --auth-file $authFile --settings-file $settingsFile 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $checkOutput -notmatch '(?m)^UI: tview\s*$' -or $checkOutput -notmatch '(?m)^Configured provider profiles: 0\s*$') {
        throw "Installed --check failed: $checkOutput"
    }
}

try {
    # Fresh install, same-version reinstall, and replacement of a pre-existing file.
    Invoke-RealInstaller
    Assert-InstalledCLI
    Assert-PreservedState
    Invoke-RealInstaller
    Assert-InstalledCLI
    [System.IO.File]::WriteAllText($installedBinary, 'previous experimental executable')
    Invoke-RealInstaller
    Assert-InstalledCLI
    Assert-PreservedState

    & (Join-Path $scriptDir 'uninstall.ps1') -InstallDir $installDir
    if (Test-Path -LiteralPath $installedBinary) { throw 'Uninstaller left komposyn.exe behind.' }
    Assert-PreservedState
    Write-Output "Signed release install lifecycle passed for $([System.IO.Path]::GetFileName($archive)) using the real Cosign verifier."
} finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
