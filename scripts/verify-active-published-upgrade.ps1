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

param(
    [string]$OldTag = 'v0.6.0',
    [string]$NewTag = 'v0.7.0'
)

$ErrorActionPreference = 'Stop'
$repo = if ($env:KOMPOSYN_REPO) { $env:KOMPOSYN_REPO } else { 'ayeshLK/komposyn' }
$identity = '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$'
$issuer = 'https://token.actions.githubusercontent.com'
foreach ($tool in @('gh', 'cosign')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "$tool is required" }
}

$temp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
$oldDir = Join-Path $temp 'old-release'
$installDir = Join-Path $temp 'bin'
$dataDir = Join-Path $temp 'data'
$sourceDir = Join-Path $temp 'source'
$binary = Join-Path $installDir 'komposyn-lg.exe'
New-Item -ItemType Directory -Path $oldDir,$installDir,$dataDir,$sourceDir -Force | Out-Null
function Invoke-CLI([string[]]$Arguments) {
    $output = & $script:binary @Arguments
    if ($LASTEXITCODE -ne 0) { throw "komposyn-lg $($Arguments -join ' ') failed with exit $LASTEXITCODE`n$($output -join "`n")" }
    return $output
}
function Get-UsageSignature([string]$Json) {
    $summary = ($Json | ConvertFrom-Json).summary
    return ($summary | Select-Object request_count,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_output_tokens,total_tokens | ConvertTo-Json -Compress)
}
try {
    $oldAsset = "komposyn-lg_$($OldTag.TrimStart('v'))_windows_amd64.zip"
    & gh release download $OldTag --repo $repo --dir $oldDir --pattern $oldAsset --pattern SHA256SUMS --pattern SHA256SUMS.sig --pattern SHA256SUMS.pem --pattern "$oldAsset.sig" --pattern "$oldAsset.pem"
    if ($LASTEXITCODE -ne 0) { throw "could not download signed historical release $OldTag" }
    $expected = ((Get-Content (Join-Path $oldDir 'SHA256SUMS') | Where-Object { $_ -match "  $([regex]::Escape($oldAsset))$" }) -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $oldDir $oldAsset)).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) { throw "historical archive checksum mismatch: $OldTag" }
    & cosign verify-blob --signature (Join-Path $oldDir "$oldAsset.sig") --certificate (Join-Path $oldDir "$oldAsset.pem") --certificate-identity-regexp $identity --certificate-oidc-issuer $issuer (Join-Path $oldDir $oldAsset)
    if ($LASTEXITCODE -ne 0) { throw "historical archive signature rejected: $OldTag" }
    & cosign verify-blob --signature (Join-Path $oldDir 'SHA256SUMS.sig') --certificate (Join-Path $oldDir 'SHA256SUMS.pem') --certificate-identity-regexp $identity --certificate-oidc-issuer $issuer (Join-Path $oldDir 'SHA256SUMS')
    if ($LASTEXITCODE -ne 0) { throw "historical checksum manifest signature rejected: $OldTag" }
    Expand-Archive -LiteralPath (Join-Path $oldDir $oldAsset) -DestinationPath $oldDir
    Copy-Item -LiteralPath (Join-Path $oldDir 'komposyn-lg.exe') -Destination $binary

    $fixture = Join-Path $PSScriptRoot '..\plugins\library\token-logger\testdata\codex\normal.jsonl'
    Copy-Item -LiteralPath $fixture -Destination (Join-Path $sourceDir 'normal.jsonl')
    $env:TOKEN_LOGGER_DATA_DIR = $dataDir
    Invoke-CLI @('settings','--collector','off','--auto-start','off') | Out-Null
    Invoke-CLI @('watch','add',$sourceDir) | Out-Null
    Invoke-CLI @('import',(Join-Path $sourceDir 'normal.jsonl'),'--format','json') | Out-Null
    $before = Get-UsageSignature (Invoke-CLI @('usage','--format','json') | Out-String)
    Invoke-CLI @('watch','start','--interval','1s') | Out-Null
    $oldStatus = $null
    for ($attempt = 0; $attempt -lt 50; $attempt++) {
        $oldStatus = (Invoke-CLI @('watch','status','--format','json') | Out-String) | ConvertFrom-Json
        if ($oldStatus.state -eq 'running' -and $oldStatus.mode -eq 'background') { break }
        Start-Sleep -Milliseconds 100
    }
    if ($oldStatus.state -ne 'running' -or $oldStatus.mode -ne 'background' -or $oldStatus.pid -le 0) { throw "historical collector did not become active: $($oldStatus | ConvertTo-Json -Compress)" }
    $settingsDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $dataDir 'settings.json')).Hash

    $env:KOMPOSYN_REPO = $repo
    $env:KOMPOSYN_VERSION = $NewTag
    $env:KOMPOSYN_INSTALL_DIR = $installDir
    Remove-Item Env:KOMPOSYN_RELEASE_DIR -ErrorAction SilentlyContinue
    $installerOutput = & (Join-Path $PSScriptRoot 'install.ps1') 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "signature-verifying installer failed: $installerOutput" }
    if ($installerOutput -notmatch 'restarted the background collector after upgrade') { throw "installer did not report background restart: $installerOutput" }
    $newStatus = (Invoke-CLI @('watch','status','--format','json') | Out-String) | ConvertFrom-Json
    if ($newStatus.state -ne 'running' -or $newStatus.mode -ne 'background' -or $newStatus.stale -or $newStatus.pid -le 0) { throw "upgraded collector is unhealthy: $($newStatus | ConvertTo-Json -Compress)" }
    if ($newStatus.pid -eq $oldStatus.pid -or $newStatus.instance_id -eq $oldStatus.instance_id) { throw 'collector did not restart under a new process and lease identity' }
    Get-Process -Id $newStatus.pid -ErrorAction Stop | Out-Null
    $after = Get-UsageSignature (Invoke-CLI @('usage','--format','json') | Out-String)
    if ($after -ne $before) { throw "usage totals changed during upgrade: before=$before after=$after" }
    $afterSettings = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $dataDir 'settings.json')).Hash
    if ($afterSettings -ne $settingsDigest) { throw 'settings or consent changed during upgrade' }

    $env:TOKEN_LOGGER_DATA_DIR = $dataDir
    $env:KOMPOSYN_INSTALL_DIR = $installDir
    & (Join-Path $PSScriptRoot 'uninstall.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'uninstall failed' }
    if ((Test-Path -LiteralPath $binary) -or -not (Test-Path (Join-Path $dataDir 'token-logger.db')) -or -not (Test-Path (Join-Path $dataDir 'settings.json'))) { throw 'uninstall did not preserve local data or remove the binary' }
    Write-Output "signed $OldTag to $NewTag active-collector upgrade passed on Windows/amd64"
} finally {
    if ((Test-Path -LiteralPath $binary -PathType Leaf) -and (Test-Path -LiteralPath (Join-Path $dataDir 'token-logger.db'))) {
        $env:TOKEN_LOGGER_DATA_DIR = $dataDir
        try { & $binary watch stop --timeout 10s | Out-Null } catch { }
    }
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
