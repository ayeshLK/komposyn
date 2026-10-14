#!/usr/bin/env bash
set -euo pipefail

repo="${1:?usage: resolve-latest-release.sh OWNER/REPO}"
metadata_url="${KOMPOSYN_RELEASE_METADATA_URL:-https://api.github.com/repos/${repo}/releases/latest}"
command -v curl >/dev/null 2>&1 || { echo 'curl is required to resolve the latest release' >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo 'jq is required to resolve the latest release' >&2; exit 1; }

metadata="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$metadata_url")" || {
	echo "unable to resolve latest release for $repo" >&2
	exit 1
}
tag="$(jq -er '
  if type == "object" and .draft == false and .prerelease == false and
     (.tag_name | type == "string" and test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))
  then .tag_name else error("not a stable Komposyn release") end
' <<<"$metadata")" || {
	echo "latest release metadata for $repo is malformed or not a stable release" >&2
	exit 1
}
printf '%s\n' "$tag"
