$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'resolve-release.ps1')

$valid = '{"tag_name":"v1.2.3","draft":false,"prerelease":false}' | ConvertFrom-Json
if ((Resolve-KomposynReleaseTag -Release $valid) -cne 'v1.2.3') { throw 'stable release did not resolve' }

$invalidMetadata = @(
    '{"tag_name":"v1.2.3","draft":true,"prerelease":false}',
    '{"tag_name":"v1.2.3","draft":false,"prerelease":true}',
    '{"tag_name":"main","draft":false,"prerelease":false}',
    '{"tag_name":"v1.2","draft":false,"prerelease":false}',
    '{"tag_name":"v1.2.3","draft":"false","prerelease":false}',
    '{broken json'
)
foreach ($json in $invalidMetadata) {
    $rejected = $false
    try {
        $release = $json | ConvertFrom-Json
        $null = Resolve-KomposynReleaseTag -Release $release
    } catch { $rejected = $true }
    if (-not $rejected) { throw "invalid release metadata was accepted: $json" }
}
Write-Output 'latest-release resolution checks passed'
