function Resolve-KomposynReleaseTag {
    param([Parameter(Mandatory = $true)]$Release)

    if ($null -eq $Release -or $Release -isnot [pscustomobject]) {
        throw 'latest release metadata is malformed'
    }
    if ($Release.draft -isnot [bool] -or $Release.prerelease -isnot [bool] -or
        $Release.draft -or $Release.prerelease -or
        $Release.tag_name -isnot [string] -or
        $Release.tag_name -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
        throw 'latest release metadata is malformed or not a stable Komposyn release'
    }
    return $Release.tag_name
}
