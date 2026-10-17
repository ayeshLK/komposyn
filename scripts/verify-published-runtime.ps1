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
    [string]$Tag = 'v0.7.0',
    [Parameter(Mandatory = $true)]
    [ValidateSet('windows_amd64','windows_arm64')]
    [string]$Target
)

$ErrorActionPreference = 'Stop'
$repo = if ($env:KOMPOSYN_REPO) { $env:KOMPOSYN_REPO } else { 'ayeshLK/komposyn' }
$identity = '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$'
$issuer = 'https://token.actions.githubusercontent.com'
if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -eq 'Arm64') { $native = 'windows_arm64' } else { $native = 'windows_amd64' }
if ($native -ne $Target) { throw "native host $native does not match requested target $Target" }
foreach ($tool in @('gh', 'cosign')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "$tool is required" }
}

$version = $Tag.TrimStart('v')
$asset = "komposyn-lg_${version}_${Target}.zip"
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    & gh release download $Tag --repo $repo --dir $temp --pattern $asset --pattern "$asset.sig" --pattern "$asset.pem" --pattern SHA256SUMS --pattern SHA256SUMS.sig --pattern SHA256SUMS.pem
    if ($LASTEXITCODE -ne 0) { throw "could not download signed release $Tag assets" }
    & cosign verify-blob --signature (Join-Path $temp 'SHA256SUMS.sig') --certificate (Join-Path $temp 'SHA256SUMS.pem') --certificate-identity-regexp $identity --certificate-oidc-issuer $issuer (Join-Path $temp 'SHA256SUMS')
    if ($LASTEXITCODE -ne 0) { throw 'checksum manifest signature rejected' }
    & cosign verify-blob --signature (Join-Path $temp "$asset.sig") --certificate (Join-Path $temp "$asset.pem") --certificate-identity-regexp $identity --certificate-oidc-issuer $issuer (Join-Path $temp $asset)
    if ($LASTEXITCODE -ne 0) { throw "archive signature rejected: $asset" }
    $expectedLine = Get-Content (Join-Path $temp 'SHA256SUMS') | Where-Object { $_ -match "  $([regex]::Escape($asset))$" } | Select-Object -First 1
    if (-not $expectedLine) { throw "checksum manifest does not list $asset" }
    $expected = ($expectedLine -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $temp $asset)).Hash.ToLowerInvariant()
    if ($expected -ne $actual) { throw "archive checksum mismatch: $asset" }
    $extract = Join-Path $temp 'extracted'
    Expand-Archive -LiteralPath (Join-Path $temp $asset) -DestinationPath $extract
    $binary = Join-Path $extract 'komposyn-lg.exe'
    $versionOutput = & $binary version | Out-String
    if ($LASTEXITCODE -ne 0 -or $versionOutput -notmatch "komposyn-lg $version \(core $version\)") { throw "unexpected native version output: $versionOutput" }
    $callerDataDir = Join-Path $temp 'caller-data-probe'
    New-Item -ItemType Directory -Path $callerDataDir | Out-Null
    $sentinelPath = Join-Path $callerDataDir 'sentinel.txt'
    [System.IO.File]::WriteAllText($sentinelPath, 'preserve this caller-owned sentinel')
    $harnessDataDir = Join-Path $temp 'harness-data'
    $messages = @(
        (@{ protocol = 'komposyn.token-logger'; version = 1; id = 'runtime-init'; method = 'initialize'; params = @{ data_dir = $harnessDataDir } } | ConvertTo-Json -Compress),
        '{"protocol":"komposyn.token-logger","version":1,"id":"runtime-shutdown","method":"shutdown"}'
    )
    $previousDataDir = $env:TOKEN_LOGGER_DATA_DIR
    try {
        $env:TOKEN_LOGGER_DATA_DIR = $callerDataDir
        $protocolOutput = $messages | & $binary harness
    } finally {
        if ($null -eq $previousDataDir) {
            Remove-Item Env:TOKEN_LOGGER_DATA_DIR -ErrorAction SilentlyContinue
        } else {
            $env:TOKEN_LOGGER_DATA_DIR = $previousDataDir
        }
    }
    if ([System.IO.File]::ReadAllText($sentinelPath) -ne 'preserve this caller-owned sentinel') {
        throw 'published runtime changed the caller-selected data directory'
    }
    foreach ($callerFile in @('settings.json', 'token-logger.db', 'token-logger.db-wal', 'token-logger.db-shm')) {
        if (Test-Path (Join-Path $callerDataDir $callerFile)) {
            throw "published runtime wrote $callerFile to the caller-selected data directory"
        }
    }
    if ($LASTEXITCODE -ne 0) { throw 'protocol-v1 harness process failed' }
    $responses = @($protocolOutput | ForEach-Object { $_ | ConvertFrom-Json })
    if ($responses.Count -ne 2 -or $responses[0].id -ne 'runtime-init' -or $responses[0].result.plugin_version -ne '0.7.0' -or $responses[0].result.cli_version -ne '0.7.0' -or $responses[1].id -ne 'runtime-shutdown') {
        throw "protocol-v1 runtime exchange failed: $($protocolOutput -join "`n")"
    }
    Write-Output "signed published $Tag binary ran natively on $Target; protocol v1 initialize/shutdown passed"
} finally {
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
