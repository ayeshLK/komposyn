$ErrorActionPreference = 'Stop'
$installDir = if ($env:KOMPOSYN_INSTALL_DIR) { $env:KOMPOSYN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
$binary = Join-Path $installDir 'komposyn-lg.exe'
if (Test-Path $binary) {
    $capability = $null
    $capabilityExit = -1
    try {
        $capability = (& $binary _watch-lifecycle 2>$null)
        $capabilityExit = $LASTEXITCODE
    } catch {
        # Legacy, corrupt, and non-executable files have no managed lifecycle.
    }
    if ($capabilityExit -eq 0 -and $capability -eq '1') {
        try {
            $collectorStatusJson = (& $binary watch status --format json 2>$null)
            $collectorStatusExit = $LASTEXITCODE
            $collectorStatus = $collectorStatusJson | ConvertFrom-Json
        } catch {
            throw 'unable to inspect the background collector; executable was not removed'
        }
        if ($collectorStatusExit -ne 0 -or $null -eq $collectorStatus) { throw 'unable to inspect the background collector; executable was not removed' }
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
