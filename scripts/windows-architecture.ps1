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

function Resolve-KomposynWindowsArchitecture {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RuntimeArchitecture,
        [string]$SystemArchitecture = ''
    )

    # Prefer the native system architecture. RuntimeArchitecture may describe
    # an emulated x64 process on Windows ARM64.
    $system = $SystemArchitecture.Trim().ToUpperInvariant()
    if ($system -in @('ARM64', 'AARCH64')) { return 'arm64' }
    if ($system -in @('AMD64', 'X64', 'X86_64')) { return 'amd64' }
    if ($system -ne '') {
        throw "Unsupported Windows system architecture '$SystemArchitecture'. Supported architectures are ARM64 and AMD64."
    }

    switch ($RuntimeArchitecture.Trim().ToUpperInvariant()) {
        { $_ -in @('ARM64', 'AARCH64') } { return 'arm64' }
        { $_ -in @('X64', 'AMD64', 'X86_64') } { return 'amd64' }
        default { throw "Unsupported Windows runtime architecture '$RuntimeArchitecture'. Supported architectures are ARM64 and AMD64." }
    }
}

function Get-KomposynWindowsArchitecture {
    $runtimeArchitecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    $systemArchitecture = ''

    # The registry describes the OS's native architecture even when this
    # PowerShell process is running under x64 emulation on Windows ARM64.
    try {
        $systemArchitecture = [string](Get-ItemPropertyValue -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment' -Name 'PROCESSOR_ARCHITECTURE' -ErrorAction Stop)
    } catch {
        try {
            $systemArchitecture = [string]$env:PROCESSOR_ARCHITEW6432
        } catch {
            $systemArchitecture = ''
        }
    }

    return Resolve-KomposynWindowsArchitecture -RuntimeArchitecture $runtimeArchitecture -SystemArchitecture $systemArchitecture
}
