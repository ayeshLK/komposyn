$ErrorActionPreference = 'Stop'
$installDir = if ($env:KOMPOSYN_INSTALL_DIR) { $env:KOMPOSYN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
$binary = Join-Path $installDir 'komposyn-lg.exe'
if (Test-Path $binary) {
    Remove-Item -Force $binary
    Write-Output "removed $binary"
} else {
    Write-Output "komposyn-lg is not installed at $binary"
}
Write-Output 'local database, configuration, and consent settings were preserved'
