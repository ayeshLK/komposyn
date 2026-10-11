$ErrorActionPreference = 'Stop'

$repo = if ($env:KOMPOSYN_REPO) { $env:KOMPOSYN_REPO } else { 'ayeshLK/komposyn' }
$version = if ($env:KOMPOSYN_VERSION) { $env:KOMPOSYN_VERSION.TrimStart('v') } else { 'latest' }
$base = "https://github.com/$repo/releases"
$cosign = if ($env:KOMPOSYN_COSIGN_BIN) { $env:KOMPOSYN_COSIGN_BIN } else { 'cosign' }
$cosignIdentity = if ($env:KOMPOSYN_COSIGN_IDENTITY_REGEXP) { $env:KOMPOSYN_COSIGN_IDENTITY_REGEXP } else { '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/tags/.*$' }
$cosignIssuer = 'https://token.actions.githubusercontent.com'
if ($version -eq 'latest') {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name.TrimStart('v')
}
$installDir = if ($env:KOMPOSYN_INSTALL_DIR) { $env:KOMPOSYN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
$tagPath = "download/v$version"
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
    Invoke-WebRequest -Uri $assetUrl -OutFile $archive
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksums
    Invoke-WebRequest -Uri "$assetUrl.sig" -OutFile $signature
    Invoke-WebRequest -Uri "$assetUrl.pem" -OutFile $certificate
    $expected = ((Get-Content $checksums | Where-Object { $_ -match "  $([regex]::Escape($asset))$" }) -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) { throw 'release checksum verification failed' }
    if (-not (Get-Command $cosign -ErrorAction SilentlyContinue)) { throw 'cosign is required to verify release signatures' }
    & $cosign verify-blob --signature $signature --certificate $certificate --certificate-identity-regexp $cosignIdentity --certificate-oidc-issuer $cosignIssuer $archive
    if ($LASTEXITCODE -ne 0) { throw 'release signature verification failed' }
    Expand-Archive -Path $archive -DestinationPath $temp -Force
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Move-Item -Path (Join-Path $temp 'komposyn-lg.exe') -Destination (Join-Path $installDir 'komposyn-lg.exe') -Force
    Write-Output "installed komposyn-lg $version at $(Join-Path $installDir 'komposyn-lg.exe')"
    Write-Output 'existing database, configuration, and consent settings were preserved'
} finally {
    Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}
