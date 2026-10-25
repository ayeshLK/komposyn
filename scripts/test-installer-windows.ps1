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

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptDir 'windows-architecture.ps1')

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Assert-Throws {
    param([scriptblock]$Action, [string]$Message)
    try { & $Action } catch { return }
    throw $Message
}

function New-FixtureExecutable {
    param([string]$OutputPath, [string]$Version, [string]$Architecture)

    $source = Join-Path (Split-Path -Parent $OutputPath) 'fixture.go'
    @'
package main

import (
    "fmt"
    "os"
    "runtime"
)

var version = ""
var commit = ""

func main() {
    if len(os.Args) > 1 && os.Args[1] == "--version" {
        fmt.Printf("komposyn version=%s commit=%s target=%s/%s\n", version, commit, runtime.GOOS, runtime.GOARCH)
    }
}
'@ | Set-Content -LiteralPath $source -Encoding UTF8

    $oldGoarch = $env:GOARCH
    $oldGoos = $env:GOOS
    try {
        $env:GOOS = 'windows'
        $env:GOARCH = $Architecture
        $ldflags = "-X main.version=$Version -X main.commit=0123456789abcdef0123456789abcdef01234567"
        & go build -trimpath -ldflags $ldflags -o $OutputPath $source
        if ($LASTEXITCODE -ne 0) { throw 'Unable to build the Windows fixture executable.' }
    } finally {
        if ($null -eq $oldGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoarch }
        if ($null -eq $oldGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoos }
    }
}

function New-MockCosign {
    param([string]$OutputPath, [string]$Architecture)

    $source = Join-Path (Split-Path -Parent $OutputPath) 'cosign-mock.go'
    @'
package main

import (
    "os"
    "strings"
)

const expectedIdentity = `^https://github\.com/ayeshLK/komposyn/\.github/workflows/release\.yml@refs/heads/master$`
const expectedIssuer = "https://token.actions.githubusercontent.com"

func main() {
    args := os.Args[1:]
    _ = os.WriteFile(os.Getenv("KOMPOSYN_COSIGN_LOG"), []byte(strings.Join(args, "\n")), 0600)
    identityOK, issuerOK := false, false
    for i := 0; i+1 < len(args); i++ {
        if args[i] == "--certificate-identity-regexp" && args[i+1] == expectedIdentity { identityOK = true }
        if args[i] == "--certificate-oidc-issuer" && args[i+1] == expectedIssuer { issuerOK = true }
    }
    if !identityOK || !issuerOK || len(args) == 0 || args[0] != "verify-blob" { os.Exit(2) }
    if os.Getenv("KOMPOSYN_TEST_COSIGN_FAIL") == "1" { os.Exit(1) }
}
'@ | Set-Content -LiteralPath $source -Encoding UTF8

    $oldGoarch = $env:GOARCH
    $oldGoos = $env:GOOS
    try {
        $env:GOOS = 'windows'
        $env:GOARCH = $Architecture
        & go build -trimpath -o $OutputPath $source
        if ($LASTEXITCODE -ne 0) { throw 'Unable to build the Cosign fixture verifier.' }
    } finally {
        if ($null -eq $oldGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoarch }
        if ($null -eq $oldGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoos }
    }
}

function New-FixtureArtifacts {
    param([string]$Directory, [string]$Version, [string]$Architecture, [string]$BinaryVersion, [string]$OmitPackageFile)

    New-Item -ItemType Directory -Path $Directory -Force | Out-Null
    $releaseVersion = $Version.TrimStart('v')
    if (-not $BinaryVersion) { $BinaryVersion = $Version }
    $asset = "komposyn_${releaseVersion}_windows_${Architecture}.zip"
    $exe = Join-Path $Directory 'fixture.exe'
    New-FixtureExecutable -OutputPath $exe -Version $BinaryVersion.TrimStart('v') -Architecture $Architecture

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archivePath = Join-Path $Directory $asset
    $archive = [System.IO.Compression.ZipFile]::Open($archivePath, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in @('komposyn.exe', 'LICENSE', 'NOTICE', 'THIRD_PARTY_LICENSES.txt')) {
            if ($name -ceq $OmitPackageFile) { continue }
            $entry = $archive.CreateEntry($name)
            $stream = $entry.Open()
            try {
                if ($name -eq 'komposyn.exe') {
                    $bytes = [System.IO.File]::ReadAllBytes($exe)
                    $stream.Write($bytes, 0, $bytes.Length)
                } else {
                    $bytes = [System.Text.Encoding]::UTF8.GetBytes("fixture $name`n")
                    $stream.Write($bytes, 0, $bytes.Length)
                }
            } finally { $stream.Dispose() }
        }
    } finally { $archive.Dispose() }

    $digest = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    "$digest  $asset`n" | Set-Content -LiteralPath (Join-Path $Directory 'checksums.txt') -Encoding ASCII
    '{"fixture":true}' | Set-Content -LiteralPath (Join-Path $Directory 'checksums.txt.sigstore.json') -Encoding ASCII
    Remove-Item -LiteralPath $exe -Force
}

function Invoke-FixtureInstall {
    param([string]$Directory, [string]$Destination, [string]$Version = '0.10.0', [string]$Cosign = $script:fakeCosign)
    & (Join-Path $scriptDir 'install.ps1') -Version $Version -InstallDir $Destination -ArtifactsDir $Directory -CosignPath $Cosign
}

function Resolve-MetadataFixture {
    param([string]$Json)
    $metadataPath = Join-Path $testRoot ('release-metadata-' + [guid]::NewGuid().ToString('N') + '.json')
    Set-Content -LiteralPath $metadataPath -Value $Json -Encoding UTF8
    try {
        $metadataUri = ([System.Uri]$metadataPath).AbsoluteUri
        return & (Join-Path $scriptDir 'install.ps1') -MetadataUri $metadataUri -ResolveOnly
    } finally { Remove-Item -LiteralPath $metadataPath -Force -ErrorAction SilentlyContinue }
}

Assert-True ((Resolve-KomposynWindowsArchitecture -RuntimeArchitecture 'X64' -SystemArchitecture 'ARM64') -eq 'arm64') 'ARM64 system detection failed under x64 emulation.'
Assert-True ((Resolve-KomposynWindowsArchitecture -RuntimeArchitecture 'Arm64' -SystemArchitecture 'AMD64') -eq 'amd64') 'Native AMD64 system detection failed.'
Assert-True ((Resolve-KomposynWindowsArchitecture -RuntimeArchitecture 'X64') -eq 'amd64') 'Runtime AMD64 detection failed.'
Assert-Throws { Resolve-KomposynWindowsArchitecture -RuntimeArchitecture 'X86' -SystemArchitecture 'x86' } 'Unsupported x86 architecture was accepted.'
Assert-Throws { Resolve-KomposynWindowsArchitecture -RuntimeArchitecture 'Mips64' } 'Unknown architecture was accepted.'

if ($env:OS -ne 'Windows_NT') { throw 'test-installer-windows.ps1 must run on Windows.' }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go is required to build the disposable Windows fixture executable.' }

$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('komposyn-windows-installer-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
$oldFail = $env:KOMPOSYN_TEST_COSIGN_FAIL
$oldCosignLog = $env:KOMPOSYN_COSIGN_LOG
try {
    $script:targetArchitecture = Get-KomposynWindowsArchitecture
    $script:artifacts = Join-Path $testRoot 'release artifacts'
    $script:installDir = Join-Path $testRoot "Installer's bin"
    $script:fakeCosign = Join-Path $testRoot 'cosign-mock.exe'
    $env:KOMPOSYN_COSIGN_LOG = Join-Path $testRoot 'cosign-args.txt'
    New-MockCosign -OutputPath $script:fakeCosign -Architecture $script:targetArchitecture

    $latest = Resolve-MetadataFixture '{"draft":false,"prerelease":false,"tag_name":"v0.10.0"}'
    Assert-True ($latest -eq 'v0.10.0') "Latest stable metadata resolved unexpectedly: $latest"
    foreach ($invalidMetadata in @(
        '{"draft":false,"prerelease":false,"tag_name":"latest"}',
        '{"draft":true,"prerelease":false,"tag_name":"v0.10.0"}',
        '{"draft":false,"prerelease":true,"tag_name":"v0.10.0"}',
        '{"draft":false,"tag_name":"v0.10.0"}',
        '{not json'
    )) {
        Assert-Throws { Resolve-MetadataFixture $invalidMetadata } "Malformed or unsupported latest metadata was accepted: $invalidMetadata"
    }

    New-FixtureArtifacts -Directory $script:artifacts -Version 'v0.10.0' -Architecture $script:targetArchitecture
    Assert-Throws { & (Join-Path $scriptDir 'install.ps1') -InstallDir $script:installDir -ArtifactsDir $script:artifacts -CosignPath $script:fakeCosign } 'Local artifacts were accepted without an explicit version.'

    $fixtureHome = Join-Path $testRoot 'isolated home'
    New-Item -ItemType Directory -Path (Join-Path $fixtureHome '.komposyn') -Force | Out-Null
    $authPath = Join-Path $fixtureHome '.komposyn/auth.json'
    $settingsPath = Join-Path $fixtureHome '.komposyn/settings.json'
    [System.IO.File]::WriteAllText($authPath, '{"schema_version":1,"credentials":[]}')
    [System.IO.File]::WriteAllText($settingsPath, '{"schema_version":1,"ui":{"plugin":"tview"}}')
    $authHash = (Get-FileHash -LiteralPath $authPath -Algorithm SHA256).Hash
    $settingsHash = (Get-FileHash -LiteralPath $settingsPath -Algorithm SHA256).Hash
    $authAcl = (Get-Acl -LiteralPath $authPath).Sddl
    $settingsAcl = (Get-Acl -LiteralPath $settingsPath).Sddl

    New-Item -ItemType Directory -Path $script:installDir | Out-Null
    $installedBinary = Join-Path $script:installDir 'komposyn.exe'
    [System.IO.File]::WriteAllText($installedBinary, 'old controlled fixture')
    $loggerFixture = Join-Path $script:installDir 'komposyn-lg.exe'
    [System.IO.File]::WriteAllText($loggerFixture, 'leave this executable unchanged')

    # A verifier failure must happen before the existing binary is touched.
    $env:KOMPOSYN_TEST_COSIGN_FAIL = '1'
    Assert-Throws { Invoke-FixtureInstall -Directory $script:artifacts -Destination $script:installDir } 'Signature failure was accepted.'
    Assert-True ((Get-Content -LiteralPath $installedBinary -Raw) -eq 'old controlled fixture') 'Signature failure replaced the existing executable.'
    $env:KOMPOSYN_TEST_COSIGN_FAIL = '0'

    # A correct installation can replace and reinstall the controlled fixture.
    Invoke-FixtureInstall -Directory $script:artifacts -Destination $script:installDir | Out-Null
    $output = (& $installedBinary --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Installed fixture --version failed.' }
    Assert-True ($output -eq "komposyn version=0.10.0 commit=0123456789abcdef0123456789abcdef01234567 target=windows/$script:targetArchitecture") "Unexpected installed version metadata: $output"
    Invoke-FixtureInstall -Directory $script:artifacts -Destination $script:installDir | Out-Null

    # A valid signature/checksum cannot make a wrong-version binary install.
    $wrongArtifacts = Join-Path $testRoot 'wrong-version artifacts'
    New-FixtureArtifacts -Directory $wrongArtifacts -Version 'v0.10.0' -BinaryVersion 'v0.9.9' -Architecture $script:targetArchitecture
    $previousHash = (Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash
    Assert-Throws { Invoke-FixtureInstall -Directory $wrongArtifacts -Destination $script:installDir -Version '0.10.0' } 'Wrong-version executable was accepted.'
    Assert-True ((Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash -eq $previousHash) 'Wrong-version failure replaced the existing executable.'

    $badLayout = Join-Path $testRoot 'bad-layout artifacts'
    New-FixtureArtifacts -Directory $badLayout -Version 'v0.10.0' -Architecture $script:targetArchitecture -OmitPackageFile 'NOTICE'
    Assert-Throws { Invoke-FixtureInstall -Directory $badLayout -Destination $script:installDir } 'Archive without NOTICE was accepted.'
    Assert-True ((Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash -eq $previousHash) 'Archive-layout failure replaced the existing executable.'

    $missingAsset = Join-Path $testRoot 'missing-asset artifacts'
    New-Item -ItemType Directory -Path $missingAsset | Out-Null
    Copy-Item -LiteralPath (Join-Path $script:artifacts 'checksums.txt') -Destination $missingAsset
    Copy-Item -LiteralPath (Join-Path $script:artifacts 'checksums.txt.sigstore.json') -Destination $missingAsset
    Assert-Throws { Invoke-FixtureInstall -Directory $missingAsset -Destination $script:installDir } 'Missing release asset was accepted.'
    Assert-True ((Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash -eq $previousHash) 'Missing asset failure replaced the existing executable.'

    Assert-Throws { Invoke-FixtureInstall -Directory $script:artifacts -Destination $script:installDir -Version 'latest' } 'Unstable version selector was accepted.'
    Assert-True ((Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash -eq $previousHash) 'Unsupported version failure replaced the existing executable.'

    # Digest mismatch and missing signature material also preserve the install.
    $tampered = Join-Path $testRoot 'tampered artifacts'
    New-Item -ItemType Directory -Path $tampered | Out-Null
    Get-ChildItem -LiteralPath $script:artifacts | Copy-Item -Destination $tampered -Recurse
    $tamperedAsset = Get-ChildItem -LiteralPath $tampered -Filter '*.zip' | Select-Object -First 1
    [System.IO.File]::AppendAllText($tamperedAsset.FullName, 'tampered')
    Assert-Throws { Invoke-FixtureInstall -Directory $tampered -Destination $script:installDir } 'Checksum mismatch was accepted.'
    Assert-True ((Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash -eq $previousHash) 'Checksum failure replaced the existing executable.'
    Remove-Item -LiteralPath (Join-Path $script:artifacts 'checksums.txt.sigstore.json') -Force
    Assert-Throws { Invoke-FixtureInstall -Directory $script:artifacts -Destination $script:installDir } 'Missing signature bundle was accepted.'
    Assert-True ((Get-FileHash -LiteralPath $installedBinary -Algorithm SHA256).Hash -eq $previousHash) 'Missing signature failure replaced the existing executable.'

    $cosignArguments = Get-Content -LiteralPath $env:KOMPOSYN_COSIGN_LOG -Raw
    Assert-True ($cosignArguments.Contains('--certificate-identity-regexp') -and $cosignArguments.Contains('^https://github\.com/ayeshLK/komposyn/\.github/workflows/release\.yml@refs/heads/master$')) 'Cosign identity binding was not passed to the verifier.'
    Assert-True ($cosignArguments.Contains('--certificate-oidc-issuer') -and $cosignArguments.Contains('https://token.actions.githubusercontent.com')) 'Cosign issuer binding was not passed to the verifier.'

    Assert-True ((Get-FileHash -LiteralPath $authPath -Algorithm SHA256).Hash -eq $authHash) 'Installer changed auth.json.'
    Assert-True ((Get-FileHash -LiteralPath $settingsPath -Algorithm SHA256).Hash -eq $settingsHash) 'Installer changed settings.json.'
    Assert-True ((Get-Acl -LiteralPath $authPath).Sddl -eq $authAcl) 'Installer changed auth.json access protections.'
    Assert-True ((Get-Acl -LiteralPath $settingsPath).Sddl -eq $settingsAcl) 'Installer changed settings.json access protections.'
    Assert-True ((Get-Content -LiteralPath $loggerFixture -Raw) -eq 'leave this executable unchanged') 'Installer changed komposyn-lg.exe.'

    & (Join-Path $scriptDir 'uninstall.ps1') -InstallDir $script:installDir | Out-Null
    Assert-True (-not (Test-Path -LiteralPath $installedBinary)) 'Uninstall left komposyn.exe behind.'
    Assert-True (Test-Path -LiteralPath $loggerFixture) 'Uninstall removed komposyn-lg.exe.'
    & (Join-Path $scriptDir 'uninstall.ps1') -InstallDir $script:installDir | Out-Null
    Assert-True ((Get-FileHash -LiteralPath $authPath -Algorithm SHA256).Hash -eq $authHash) 'Uninstall changed auth.json.'
    Assert-True ((Get-FileHash -LiteralPath $settingsPath -Algorithm SHA256).Hash -eq $settingsHash) 'Uninstall changed settings.json.'

    Write-Output 'Windows installer fixture tests passed. Cosign was mocked; no cryptographic verification was performed.'
} finally {
    if ($null -eq $oldFail) { Remove-Item Env:KOMPOSYN_TEST_COSIGN_FAIL -ErrorAction SilentlyContinue } else { $env:KOMPOSYN_TEST_COSIGN_FAIL = $oldFail }
    if ($null -eq $oldCosignLog) { Remove-Item Env:KOMPOSYN_COSIGN_LOG -ErrorAction SilentlyContinue } else { $env:KOMPOSYN_COSIGN_LOG = $oldCosignLog }
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
