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

[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$BinaryPath)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:OS -ne 'Windows_NT') { throw 'This configuration fixture test must run on Windows.' }
if (-not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) { throw "komposyn binary not found: $BinaryPath" }

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptDir 'windows-private-fixtures.ps1')
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('komposyn-private-config-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
$configDir = Join-Path $tmp '.komposyn'
New-Item -ItemType Directory -Path $configDir | Out-Null
$authFile = Join-Path $configDir 'auth.json'
$settingsFile = Join-Path $configDir 'settings.json'
$authContent = '{"schema_version":1,"credentials":[]}'
$settingsContent = '{"schema_version":1,"ui":{"plugin":"tview"}}'

try {
    Set-KomposynOwnerOnlyAcl -Path $configDir -Directory
    [System.IO.File]::WriteAllText($authFile, $authContent)
    [System.IO.File]::WriteAllText($settingsFile, $settingsContent)
    Set-KomposynOwnerOnlyAcl -Path $authFile
    Set-KomposynOwnerOnlyAcl -Path $settingsFile

    $authHash = (Get-FileHash -LiteralPath $authFile -Algorithm SHA256).Hash
    $settingsHash = (Get-FileHash -LiteralPath $settingsFile -Algorithm SHA256).Hash
    $authSddl = (Get-Acl -LiteralPath $authFile).Sddl
    $settingsSddl = (Get-Acl -LiteralPath $settingsFile).Sddl
    $checkOutput = (& $BinaryPath --check --auth-file $authFile --settings-file $settingsFile 2>&1 | Out-String)
    $checkExit = $LASTEXITCODE
    if ($checkExit -ne 0 -or $checkOutput -notmatch '(?m)^UI: tview\s*$' -or $checkOutput -notmatch '(?m)^Configured provider profiles: 0\s*$') {
        throw "Actual Windows komposyn --check rejected owner-only ACL fixtures: $checkOutput"
    }
    if ((Get-FileHash -LiteralPath $authFile -Algorithm SHA256).Hash -cne $authHash -or
        (Get-FileHash -LiteralPath $settingsFile -Algorithm SHA256).Hash -cne $settingsHash) {
        throw 'komposyn --check changed a configuration fixture.'
    }
    if ((Get-Acl -LiteralPath $authFile).Sddl -cne $authSddl -or
        (Get-Acl -LiteralPath $settingsFile).Sddl -cne $settingsSddl) {
        throw 'komposyn --check changed configuration access protections.'
    }
    Write-Output 'Native Windows komposyn --check accepted protected owner-only auth/settings fixtures.'
} finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
