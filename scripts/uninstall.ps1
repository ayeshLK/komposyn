$ErrorActionPreference = 'Stop'
$installDir = if ($env:KOMPOSYN_INSTALL_DIR) { $env:KOMPOSYN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
$binary = Join-Path $installDir 'komposyn-lg.exe'
if (Test-Path $binary) {
    $capability = (& $binary _watch-lifecycle 2>$null)
    if ($LASTEXITCODE -eq 0 -and $capability -eq '1') {
        $collectorStatus = (& $binary watch status --format json 2>$null | ConvertFrom-Json)
        if ($LASTEXITCODE -ne 0 -or $null -eq $collectorStatus) { throw 'unable to inspect the background collector; executable was not removed' }
        if ($collectorStatus.state -in @('starting', 'running', 'degraded', 'stopping')) {
            & $binary watch stop --timeout 30s
            if ($LASTEXITCODE -ne 0) { throw 'background collector stop timed out' }
        }
    }
    Remove-Item -Force $binary
    Write-Output "removed $binary"
} else {
    Write-Output "komposyn-lg is not installed at $binary"
}
Write-Output 'local database, configuration, and consent settings were preserved'
