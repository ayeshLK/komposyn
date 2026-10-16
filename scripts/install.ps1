$ErrorActionPreference = 'Stop'

$repo = if ($env:KOMPOSYN_REPO) { $env:KOMPOSYN_REPO } else { 'ayeshLK/komposyn' }
$version = if ($env:KOMPOSYN_VERSION) { $env:KOMPOSYN_VERSION } else { $null }
$base = "https://github.com/$repo/releases"
$releaseDir = if ($env:KOMPOSYN_RELEASE_DIR) { $env:KOMPOSYN_RELEASE_DIR } else { $null }
$cosign = if ($env:KOMPOSYN_COSIGN_BIN) { $env:KOMPOSYN_COSIGN_BIN } else { 'cosign' }
$cosignIdentity = if ($env:KOMPOSYN_COSIGN_IDENTITY_REGEXP) { $env:KOMPOSYN_COSIGN_IDENTITY_REGEXP } else { '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$' }
$cosignIssuer = 'https://token.actions.githubusercontent.com'
if (-not $version) {
    if ($releaseDir -and -not $env:KOMPOSYN_RELEASE_METADATA_URL) {
        $version = ((Get-Content (Join-Path $releaseDir 'SHA256SUMS') | Where-Object { $_ -match '^# version=' } | Select-Object -First 1) -replace '^# version=', '').Trim()
        if (-not $version) { throw 'local SHA256SUMS is missing # version metadata' }
        if ($version -notmatch '^v') { $version = "v$version" }
    } else {
        try {
            $metadataUri = if ($env:KOMPOSYN_RELEASE_METADATA_URL) { $env:KOMPOSYN_RELEASE_METADATA_URL } else { "https://api.github.com/repos/$repo/releases/latest" }
            $release = Invoke-RestMethod -Uri $metadataUri -Headers @{ Accept = 'application/vnd.github+json' }
            if ($null -eq $release -or $release -isnot [pscustomobject] -or
                $release.draft -isnot [bool] -or $release.prerelease -isnot [bool] -or
                $release.draft -or $release.prerelease -or
                $release.tag_name -isnot [string] -or
                $release.tag_name -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
                throw 'latest release metadata is malformed or not a stable Komposyn release'
            }
            $version = $release.tag_name
        } catch {
            throw "unable to resolve a stable Komposyn release: $($_.Exception.Message)"
        }
    }
}
if ($version -notmatch '^v') { $version = "v$version" }
if ($version -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { throw "invalid release version/tag: $version" }
$tag = $version
$version = $tag.Substring(1)
Write-Output "resolved Komposyn release: $tag"
$installDir = if ($env:KOMPOSYN_INSTALL_DIR) { $env:KOMPOSYN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
$tagPath = "download/$tag"
$asset = "komposyn-lg_${version}_windows_amd64.zip"
$assetUrl = "$base/$tagPath/$asset"
$checksumsUrl = "$base/$tagPath/SHA256SUMS"
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    $archive = Join-Path $temp $asset
    $checksums = Join-Path $temp 'SHA256SUMS'
    $signature = Join-Path $temp "$asset.sig"
    $certificate = Join-Path $temp "$asset.pem"
    if ($releaseDir) {
        $manifestVersion = ((Get-Content (Join-Path $releaseDir 'SHA256SUMS') | Where-Object { $_ -match '^# version=' } | Select-Object -First 1) -replace '^# version=', '').Trim()
        if ($manifestVersion.TrimStart('v') -ne $version) { throw "local release metadata does not match requested tag $tag" }
        $localFiles = @(
            (Join-Path $releaseDir $asset),
            (Join-Path $releaseDir 'SHA256SUMS'),
            (Join-Path $releaseDir "$asset.sig"),
            (Join-Path $releaseDir "$asset.pem")
        )
        foreach ($localFile in $localFiles) {
            if (-not (Test-Path -LiteralPath $localFile -PathType Leaf)) { throw "missing local release artifact: $localFile" }
        }
        Copy-Item -LiteralPath $localFiles[0] -Destination $archive
        Copy-Item -LiteralPath $localFiles[1] -Destination $checksums
        Copy-Item -LiteralPath $localFiles[2] -Destination $signature
        Copy-Item -LiteralPath $localFiles[3] -Destination $certificate
    } else {
        Invoke-WebRequest -Uri $assetUrl -OutFile $archive
        Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksums
        Invoke-WebRequest -Uri "$assetUrl.sig" -OutFile $signature
        Invoke-WebRequest -Uri "$assetUrl.pem" -OutFile $certificate
    }
    $expected = ((Get-Content $checksums | Where-Object { $_ -match "  $([regex]::Escape($asset))$" }) -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) { throw 'release checksum verification failed' }
    if (-not (Get-Command $cosign -ErrorAction SilentlyContinue)) { throw 'cosign is required to verify release signatures' }
    & $cosign verify-blob --signature $signature --certificate $certificate --certificate-identity-regexp $cosignIdentity --certificate-oidc-issuer $cosignIssuer $archive
    if ($LASTEXITCODE -ne 0) { throw 'release signature verification failed' }
    Expand-Archive -Path $archive -DestinationPath $temp -Force
    $installedBinary = Join-Path $installDir 'komposyn-lg.exe'
    $restartBackgroundCollector = $false
    $stoppedCollectorMode = $null
    if (Test-Path -LiteralPath $installedBinary -PathType Leaf) {
        $capability = $null
        $capabilityExit = -1
        try {
            $capability = (& $installedBinary _watch-lifecycle 2>$null)
            $capabilityExit = $LASTEXITCODE
        } catch {
            # Legacy, corrupt, and non-executable files have no managed lifecycle.
        }
        if ($capabilityExit -eq 0 -and $capability -eq '1') {
            try {
                $collectorStatusJson = (& $installedBinary watch status --format json 2>$null)
                $collectorStatusExit = $LASTEXITCODE
                $collectorStatus = $collectorStatusJson | ConvertFrom-Json
            } catch {
                throw 'unable to inspect the existing collector; executable was not replaced'
            }
            if ($collectorStatusExit -ne 0 -or $null -eq $collectorStatus) { throw 'unable to inspect the existing collector; executable was not replaced' }
            if ($collectorStatus.state -in @('starting', 'running', 'degraded', 'stopping')) {
                $stoppedCollectorMode = if ($collectorStatus.mode) { [string]$collectorStatus.mode } else { 'unknown' }
                $restartBackgroundCollector = $stoppedCollectorMode -eq 'background'
                & $installedBinary watch stop --timeout 30s
                if ($LASTEXITCODE -ne 0) { throw 'collector stop timed out' }
            }
        }
    }
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Move-Item -Path (Join-Path $temp 'komposyn-lg.exe') -Destination (Join-Path $installDir 'komposyn-lg.exe') -Force
    Write-Output "installed komposyn-lg $version at $(Join-Path $installDir 'komposyn-lg.exe')"
    Write-Output 'existing database, configuration, and consent settings were preserved'
    if ($restartBackgroundCollector) {
        & (Join-Path $installDir 'komposyn-lg.exe') watch start
        if ($LASTEXITCODE -ne 0) { throw "upgrade succeeded, but the background collector could not be restarted; run 'komposyn-lg watch start' after reviewing its registered sources" }
        Write-Output 'restarted the background collector after upgrade'
    } elseif ($stoppedCollectorMode -eq 'foreground') {
        Write-Output 'the previous foreground collector was stopped for upgrade; rerun its original komposyn-lg watch foreground command to resume collection'
    } elseif ($stoppedCollectorMode -eq 'embedded') {
        Write-Output 'the previous embedded collector was stopped for upgrade; restart the Komposyn REPL to resume collection according to its saved settings'
    } elseif ($null -ne $stoppedCollectorMode) {
        Write-Output 'the previous collector was stopped for upgrade but its mode was not recognized; restart the intended collection mode manually'
    }
} finally {
    Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}
