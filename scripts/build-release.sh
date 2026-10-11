#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: build-release.sh <version> <output-dir>}"
output_dir="${2:?usage: build-release.sh <version> <output-dir>}"
version="${version#v}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -z "$output_dir" || "$output_dir" == "/" || "$output_dir" == "." ]]; then
	echo "refusing unsafe release output directory: $output_dir" >&2
	exit 1
fi
mkdir -p "$output_dir"
find "$output_dir" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
bash "$root_dir/scripts/generate-third-party-licenses.sh" "$output_dir"

ldflags="-s -w -X github.com/ayeshLK/komposyn/plugins/library/token-logger.Version=${version}"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
	IFS=/ read -r goos goarch <<< "$target"
	asset="komposyn-lg_${version}_${goos}_${goarch}"
	stage_dir="$(mktemp -d)"
	trap 'rm -rf "$stage_dir"' EXIT
	if [[ "$goos" == windows ]]; then
		binary="$stage_dir/komposyn-lg.exe"
		archive="$output_dir/${asset}.zip"
	else
		binary="$stage_dir/komposyn-lg"
		archive="$output_dir/${asset}.tar.gz"
	fi
	GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$binary" "$root_dir/plugins/cli/token-logger"
	cp "$root_dir/LICENSE" "$root_dir/NOTICE" "$root_dir/README.md" "$output_dir/THIRD_PARTY_LICENSES.md" "$stage_dir/"
	cp -R "$output_dir/third_party_licenses" "$stage_dir/"
	if [[ "$goos" == windows ]]; then
		(
			cd "$stage_dir"
			zip -q -r "$archive" komposyn-lg.exe LICENSE NOTICE README.md THIRD_PARTY_LICENSES.md third_party_licenses
		)
	else
		tar -C "$stage_dir" -czf "$archive" "$(basename "$binary")" LICENSE NOTICE README.md THIRD_PARTY_LICENSES.md third_party_licenses
	fi
	rm -rf "$stage_dir"
	trap - EXIT
done

{
	echo "# version=${version}"
	(cd "$output_dir" && sha256sum komposyn-lg_*)
} > "$output_dir/SHA256SUMS"
