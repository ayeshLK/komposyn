$ErrorActionPreference = 'Stop'
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
$releaseDir = Join-Path $temp 'release'
$installDir = Join-Path $temp 'install'
New-Item -ItemType Directory -Path $releaseDir, $installDir -Force | Out-Null
try {
    $asset = 'komposyn-lg_1.2.3_windows_amd64.zip'
    $payloadDir = Join-Path $temp 'payload'
    New-Item -ItemType Directory -Path $payloadDir | Out-Null
    Set-Content -Path (Join-Path $payloadDir 'komposyn-lg.exe') -Value 'fixture-binary'
    Compress-Archive -Path (Join-Path $payloadDir 'komposyn-lg.exe') -DestinationPath (Join-Path $releaseDir $asset)

    # Exercise the same PowerShell extraction helper used by verify-release.sh
    # with caller-selected paths that contain spaces and apostrophes.
    $quotedPathRoot = Join-Path $temp "O'Neil release paths"
    $quotedArchiveDir = Join-Path $quotedPathRoot 'release assets'
    $quotedDestination = Join-Path $quotedPathRoot "O'Neil extraction"
    New-Item -ItemType Directory -Path $quotedArchiveDir, $quotedDestination -Force | Out-Null
    $quotedArchive = Join-Path $quotedArchiveDir 'fixture archive.zip'
    Compress-Archive -Path (Join-Path $payloadDir 'komposyn-lg.exe') -DestinationPath $quotedArchive
    & (Join-Path $PSScriptRoot 'expand-release-archive.ps1') `
        -ArchivePath $quotedArchive -DestinationPath $quotedDestination
    if ((Get-Content -Raw (Join-Path $quotedDestination 'komposyn-lg.exe')) -ne 'fixture-binary') {
        throw 'PowerShell archive extraction failed for a path containing spaces and apostrophes'
    }

    $hash = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $releaseDir $asset)).Hash.ToLowerInvariant()
    Set-Content -Path (Join-Path $releaseDir 'SHA256SUMS') -Value "# version=1.2.3`n$hash  $asset"
    Set-Content -Path (Join-Path $releaseDir "$asset.sig") -Value 'fixture-signature'
    Set-Content -Path (Join-Path $releaseDir "$asset.pem") -Value 'fixture-certificate'

    $mockCosign = Join-Path $temp 'cosign.ps1'
    @'
if ($args -notcontains '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$') { throw 'unexpected certificate identity' }
if ($args -notcontains 'https://token.actions.githubusercontent.com') { throw 'unexpected OIDC issuer' }
$global:LASTEXITCODE = 0
'@ | Set-Content -Path $mockCosign

    $env:KOMPOSYN_RELEASE_DIR = $releaseDir
    $env:KOMPOSYN_RELEASE_METADATA_URL = 'fixture://release-metadata'
    $env:KOMPOSYN_INSTALL_DIR = $installDir
    $env:KOMPOSYN_COSIGN_BIN = $mockCosign
    $env:KOMPOSYN_VERSION = ''
    $global:TestReleaseMetadata = [pscustomobject]@{ tag_name = 'v1.2.3'; draft = $false; prerelease = $false }
    function Invoke-RestMethod {
        if ($global:TestReleaseMetadata -eq '__unavailable__') { throw 'fixture API unavailable' }
        return $global:TestReleaseMetadata
    }

    # Keep this validation aligned with the copy/paste Windows bootstrap in issue #53.
    $resolveBootstrapTag = {
        param($Release)
        if ($null -eq $Release -or $Release -isnot [pscustomobject] -or
            $Release.draft -isnot [bool] -or $Release.prerelease -isnot [bool] -or
            $Release.draft -or $Release.prerelease -or
            $Release.tag_name -isnot [string] -or
            $Release.tag_name -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
            throw 'Latest release metadata is not a valid stable Komposyn release'
        }
        return $Release.tag_name
    }
    if ((& $resolveBootstrapTag ([pscustomobject]@{ tag_name = 'v1.2.3'; draft = $false; prerelease = $false })) -cne 'v1.2.3') {
        throw 'Windows bootstrap did not resolve a valid stable release'
    }
    $invalidBootstrapMetadata = @(
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = 'false'; prerelease = $false },
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = 0; prerelease = $false },
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = $false; prerelease = 0 },
        [pscustomobject]@{ tag_name = 123; draft = $false; prerelease = $false }
    )
    foreach ($metadata in $invalidBootstrapMetadata) {
        $rejected = $false
        try { $null = & $resolveBootstrapTag $metadata } catch { $rejected = $true }
        if (-not $rejected) { throw "Windows bootstrap accepted malformed metadata: $($metadata | ConvertTo-Json -Compress)" }
    }

    $installer = Join-Path $temp 'install.ps1'
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'install.ps1') -Destination $installer
    $output = (& $installer | Out-String)
    if ($output -notmatch 'resolved Komposyn release: v1\.2\.3') { throw 'default installer did not show the resolved stable tag' }
    $binary = Join-Path $installDir 'komposyn-lg.exe'
    if ((Get-Content -Raw $binary) -notmatch 'fixture-binary') { throw 'default installer did not select/install the fixture archive' }

    # An explicit version must bypass metadata resolution and select that exact local archive.
    $global:TestReleaseMetadata = '__unavailable__'
    $env:KOMPOSYN_VERSION = 'v1.2.3'
    $output = (& $installer | Out-String)
    if ($output -notmatch 'resolved Komposyn release: v1\.2\.3') { throw 'pinned installer did not show its version' }

    Set-Content -Path $binary -Value 'preserved-binary'
    $invalidMetadata = @(
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = $true; prerelease = $false },
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = $false; prerelease = $true },
        [pscustomobject]@{ tag_name = 'main'; draft = $false; prerelease = $false },
        [pscustomobject]@{ tag_name = 'v1.2'; draft = $false; prerelease = $false },
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = 'false'; prerelease = $false },
        [pscustomobject]@{ tag_name = 'v1.2.3'; draft = 0; prerelease = $false },
        [pscustomobject]@{ draft = $false; prerelease = $false },
        'malformed response'
    )
    $env:KOMPOSYN_VERSION = ''
    foreach ($metadata in $invalidMetadata) {
        $global:TestReleaseMetadata = $metadata
        $failed = $false
        try { & $installer | Out-Null } catch { $failed = $true }
        if (-not $failed) { throw "installer accepted invalid metadata: $($metadata | ConvertTo-Json -Compress)" }
        if ((Get-Content -Raw $binary) -notmatch 'preserved-binary') { throw 'metadata failure replaced the installed executable' }
    }
    $global:TestReleaseMetadata = '__unavailable__'
    $failed = $false
    try { & $installer | Out-Null } catch { $failed = $true }
    if (-not $failed) { throw 'installer accepted an unavailable release API' }
    if ((Get-Content -Raw $binary) -notmatch 'preserved-binary') { throw 'API failure replaced the installed executable' }
    Write-Output 'default and pinned Windows installer resolution checks passed'
} finally {
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
