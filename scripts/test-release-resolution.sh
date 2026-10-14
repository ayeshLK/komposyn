#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

resolver="$root_dir/scripts/resolve-latest-release.sh"
write_metadata() { printf '%s\n' "$1" > "$tmp_dir/metadata.json"; }
resolve() {
	KOMPOSYN_RELEASE_METADATA_URL="file://$tmp_dir/metadata.json" bash "$resolver" ayeshLK/komposyn
}

write_metadata '{"tag_name":"v1.2.3","draft":false,"prerelease":false}'
[[ "$(resolve)" == v1.2.3 ]] || { echo 'latest stable tag did not resolve' >&2; exit 1; }

for invalid in \
	'{"tag_name":"v1.2.3","draft":true,"prerelease":false}' \
	'{"tag_name":"v1.2.3","draft":false,"prerelease":true}' \
	'{"tag_name":"main","draft":false,"prerelease":false}' \
	'{"tag_name":"v1.2","draft":false,"prerelease":false}' \
	'{"tag_name":"v1.2.3","draft":"false","prerelease":false}' \
	'{broken json'; do
	write_metadata "$invalid"
	if resolve >/dev/null 2>&1; then
		echo "resolver accepted invalid release metadata: $invalid" >&2
		exit 1
	fi
done

if KOMPOSYN_RELEASE_METADATA_URL='file:///definitely-missing-komposyn-release.json' bash "$resolver" ayeshLK/komposyn >/dev/null 2>&1; then
	echo 'resolver accepted unavailable release metadata' >&2
	exit 1
fi

echo 'latest-release resolution checks passed'
