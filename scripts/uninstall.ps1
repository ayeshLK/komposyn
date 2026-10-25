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
param([string]$InstallDir)

$ErrorActionPreference = 'Stop'
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'Komposyn\bin' }
if (-not $InstallDir) { throw 'Unable to determine the per-user install directory; pass -InstallDir.' }
$binary = Join-Path ([System.IO.Path]::GetFullPath($InstallDir)) 'komposyn.exe'

if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    Write-Output "KompoSyn is not installed at $binary."
    exit 0
}

try {
    Remove-Item -LiteralPath $binary -Force
} catch {
    throw "Could not remove '$binary'. Close any running KompoSyn chat and retry. No configuration or other files were removed. Details: $($_.Exception.Message)"
}

Write-Output "Removed $binary."
Write-Output 'auth.json, settings.json, komposyn-lg, and Token Logger data were preserved.'
