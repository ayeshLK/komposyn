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
    [string]$Version,
    [string]$InstallDir,
    [string]$ArtifactsDir,
    [string]$MetadataUri,
    [string]$CosignPath = 'cosign',
    [switch]$ResolveOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw 'install.ps1 is for Windows. Use scripts/install.sh on Linux or macOS.'
}

$repository = 'ayeshLK/komposyn'
$cosignIdentity = '^https://github\.com/ayeshLK/komposyn/\.github/workflows/release\.yml@refs/heads/master$'
$cosignIssuer = 'https://token.actions.githubusercontent.com'
$releaseFiles = @('checksums.txt', 'checksums.txt.sigstore.json')
$packageFiles = @('komposyn.exe', 'LICENSE', 'NOTICE', 'THIRD_PARTY_LICENSES.txt')

function Resolve-ReleaseVersion {
    param([string]$RequestedVersion, [string]$LocalArtifacts, [string]$MetadataEndpoint)

    if ($LocalArtifacts -and -not $RequestedVersion) {
        throw 'An explicit -Version is required when -ArtifactsDir is supplied.'
    }

    if ($RequestedVersion) {
        if ($RequestedVersion -notmatch '^v?[0-9]+\.[0-9]+\.[0-9]+$') {
            throw "Version must be a stable X.Y.Z or vX.Y.Z tag; got '$RequestedVersion'."
        }
        $tag = if ($RequestedVersion.StartsWith('v')) { $RequestedVersion } else { "v$RequestedVersion" }
    } else {
        $endpoint = if ($MetadataEndpoint) { $MetadataEndpoint } else { "https://api.github.com/repos/$repository/releases/latest" }
        try {
            $endpointUri = $null
            if ([System.Uri]::TryCreate($endpoint, [System.UriKind]::Absolute, [ref]$endpointUri) -and $endpointUri.IsFile) {
                # Offline metadata fixture support for installer tests.
                $release = Get-Content -LiteralPath $endpointUri.LocalPath -Raw | ConvertFrom-Json
            } else {
                $release = Invoke-RestMethod -Uri $endpoint -Headers @{ Accept = 'application/vnd.github+json' }
            }
        } catch {
            throw "Unable to resolve the latest stable KompoSyn release: $($_.Exception.Message)"
        }
        if ($null -eq $release -or $release -isnot [pscustomobject] -or
            $release.draft -isnot [bool] -or $release.prerelease -isnot [bool] -or
            $release.draft -or $release.prerelease -or
            $release.tag_name -isnot [string] -or
            $release.tag_name -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
            throw 'Latest release metadata is malformed or is not a stable KompoSyn release.'
        }
        $tag = $release.tag_name
    }

    return $tag
}

function Get-ReleaseDigest {
    param([string]$ManifestPath, [string]$AssetName)

    $foundDigests = @()
    foreach ($line in Get-Content -LiteralPath $ManifestPath) {
        if ($line -match '^([0-9a-fA-F]{64})\s+\*?([^\s]+)$' -and $Matches[2] -ceq $AssetName) {
            $foundDigests += $Matches[1].ToLowerInvariant()
        }
    }
    if ($foundDigests.Count -ne 1) { throw "checksums.txt must contain exactly one SHA-256 entry for $AssetName." }
    return $foundDigests[0]
}

if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
if (-not $InstallDir) { throw 'Unable to determine the per-user install directory; pass -InstallDir.' }
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$tag = Resolve-ReleaseVersion -RequestedVersion $Version -LocalArtifacts $ArtifactsDir -MetadataEndpoint $MetadataUri
if ($ResolveOnly) { return $tag }
$releaseVersion = $tag.Substring(1)
$architectureHelper = Join-Path $PSScriptRoot 'windows-architecture.ps1'
if (-not (Test-Path -LiteralPath $architectureHelper -PathType Leaf)) {
    throw "Missing Windows architecture helper '$architectureHelper'. Download install.ps1 and windows-architecture.ps1 from the same repository ref."
}
. $architectureHelper
$architecture = Get-KomposynWindowsArchitecture
$assetName = "komposyn_${releaseVersion}_windows_${architecture}.zip"
$expectedTarget = "windows/$architecture"

$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("komposyn-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempRoot | Out-Null
try {
    $manifest = Join-Path $tempRoot 'checksums.txt'
    $bundle = Join-Path $tempRoot 'checksums.txt.sigstore.json'
    $archive = Join-Path $tempRoot $assetName

    if ($ArtifactsDir) {
        $localManifest = Join-Path $ArtifactsDir 'checksums.txt'
        if (-not (Test-Path -LiteralPath $localManifest -PathType Leaf)) { throw "Missing local artifact: $localManifest" }
        foreach ($required in $releaseFiles) {
            $source = Join-Path $ArtifactsDir $required
            if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "Missing local artifact: $source" }
        }
        $localArchive = Join-Path $ArtifactsDir $assetName
        if (-not (Test-Path -LiteralPath $localArchive -PathType Leaf)) { throw "Missing local release asset: $localArchive" }
        Copy-Item -LiteralPath $localManifest -Destination $manifest
        Copy-Item -LiteralPath (Join-Path $ArtifactsDir $releaseFiles[1]) -Destination $bundle
        Copy-Item -LiteralPath $localArchive -Destination $archive
    } else {
        # Every URL uses the resolved tag. Nothing is downloaded from a moving
        # latest-release endpoint after this point.
        $releaseBase = "https://github.com/$repository/releases/download/$tag"
        Invoke-WebRequest -Uri "$releaseBase/checksums.txt" -OutFile $manifest
        Invoke-WebRequest -Uri "$releaseBase/checksums.txt.sigstore.json" -OutFile $bundle
        Invoke-WebRequest -Uri "$releaseBase/$assetName" -OutFile $archive
    }

    if (-not (Get-Command $CosignPath -ErrorAction SilentlyContinue)) {
        throw 'Cosign is required to verify release signatures. Install the documented Cosign version and retry.'
    }
    & $CosignPath verify-blob --bundle $bundle --certificate-identity-regexp $cosignIdentity --certificate-oidc-issuer $cosignIssuer $manifest
    if ($LASTEXITCODE -ne 0) { throw 'Cosign rejected the checksum manifest signature or signer identity.' }

    $expectedDigest = Get-ReleaseDigest -ManifestPath $manifest -AssetName $assetName
    $actualDigest = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualDigest -cne $expectedDigest) { throw 'Release archive SHA-256 verification failed.' }

    # Inspect the full archive before extracting the executable.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
    try {
        $names = @($zip.Entries | ForEach-Object { $_.FullName })
        foreach ($name in $names) {
            if ($name.Contains('/') -or $name.Contains('\') -or $name -eq '.' -or $name -eq '..') {
                throw "Release archive contains an unexpected nested or unsafe entry '$name'."
            }
        }
        foreach ($expected in $packageFiles) {
            if ($names -cnotcontains $expected) { throw "Release archive is missing $expected." }
        }
        if ($names.Count -ne $packageFiles.Count -or ($names | Select-Object -Unique).Count -ne $names.Count) {
            throw 'Release archive contains unexpected or duplicate entries.'
        }
        $binaryEntry = $zip.GetEntry('komposyn.exe')
        if ($null -eq $binaryEntry -or $binaryEntry.Length -eq 0) { throw 'Release archive contains an empty komposyn.exe.' }
        $candidate = Join-Path $tempRoot 'komposyn.exe'
        $input = $binaryEntry.Open()
        $output = [System.IO.File]::Open($candidate, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
        try { $input.CopyTo($output) } finally { $output.Dispose(); $input.Dispose() }
    } finally {
        $zip.Dispose()
    }

    # --version is deliberately config-free; no auth/settings are read or
    # rewritten by installation. Fail before touching the installed binary.
    $versionOutput = (& $candidate --version 2>&1 | Out-String).Trim()
    $candidateExit = $LASTEXITCODE
    if ($candidateExit -ne 0 -or $versionOutput -cnotmatch '^komposyn version=([^\s]+) commit=[0-9a-f]{40} target=([^\s]+)$') {
        throw "Candidate komposyn --version returned unexpected output: $versionOutput"
    }
    if ($Matches[1] -cne $releaseVersion -or $Matches[2] -cne $expectedTarget) {
        throw "Candidate metadata mismatch: expected version=$releaseVersion target=$expectedTarget; got $versionOutput"
    }

    # Stage beside the destination so replacement is on the same filesystem.
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $stageDir = Join-Path $InstallDir ('.komposyn-stage-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $stageDir | Out-Null
    try {
        $stagedBinary = Join-Path $stageDir 'komposyn.exe'
        [System.IO.File]::Copy($candidate, $stagedBinary, $false)
        $installedBinary = Join-Path $InstallDir 'komposyn.exe'
        if (Test-Path -LiteralPath $installedBinary -PathType Leaf) {
            # File.Replace is atomic and leaves the original intact when a
            # running executable denies replacement.
            try {
                [System.IO.File]::Replace($stagedBinary, $installedBinary, $null)
            } catch {
                throw "Could not replace '$installedBinary'. Close any running KompoSyn chat and retry. The existing installation was preserved. Details: $($_.Exception.Message)"
            }
        } else {
            [System.IO.File]::Move($stagedBinary, $installedBinary)
        }
    } finally {
        Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
    }

    Write-Output "Installed KompoSyn $releaseVersion ($expectedTarget) at $(Join-Path $InstallDir 'komposyn.exe')."
    Write-Output 'Existing auth.json and settings.json were not opened or modified.'
} finally {
    Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
}
