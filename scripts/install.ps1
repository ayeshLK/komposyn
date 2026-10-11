$ErrorActionPreference = 'Stop'

$repo = if ($env:KOMPOSYN_REPO) { $env:KOMPOSYN_REPO } else { 'ayeshLK/komposyn' }
$version = if ($env:KOMPOSYN_VERSION) { $env:KOMPOSYN_VERSION.TrimStart('v') } else { 'latest' }
$base = "https://github.com/$repo/releases"
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
    Invoke-WebRequest -Uri $assetUrl -OutFile $archive
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksums
    $expected = ((Get-Content $checksums | Where-Object { $_ -match "  $([regex]::Escape($asset))$" }) -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) { throw 'release checksum verification failed' }
    Expand-Archive -Path $archive -DestinationPath $temp -Force
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Move-Item -Path (Join-Path $temp 'komposyn-lg.exe') -Destination (Join-Path $installDir 'komposyn-lg.exe') -Force
    Write-Output "installed komposyn-lg $version at $(Join-Path $installDir 'komposyn-lg.exe')"
    Write-Output 'existing database, configuration, and consent settings were preserved'
} finally {
    Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}
