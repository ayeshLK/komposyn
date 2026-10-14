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
