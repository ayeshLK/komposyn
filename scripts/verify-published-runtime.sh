#!/usr/bin/env bash
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

set -euo pipefail

tag="${1:-v0.7.0}"
target="${2:?usage: verify-published-runtime.sh <tag> <linux_amd64|linux_arm64|darwin_amd64|darwin_arm64>}"
repo="${KOMPOSYN_REPO:-ayeshLK/komposyn}"
identity='^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$'
issuer='https://token.actions.githubusercontent.com'
case "$(uname -s):$(uname -m)" in
	Linux:x86_64|Linux:amd64) native=linux_amd64 ;;
	Linux:aarch64|Linux:arm64) native=linux_arm64 ;;
	Darwin:x86_64) native=darwin_amd64 ;;
	Darwin:arm64) native=darwin_arm64 ;;
	*) echo "unsupported native runtime host: $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac
[[ "$native" == "$target" ]] || { echo "native host $native does not match requested target $target" >&2; exit 1; }
for tool in gh cosign jq; do
	command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 1; }
done
if command -v sha256sum >/dev/null 2>&1; then sha_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then sha_tool=shasum
else echo 'sha256sum or shasum is required' >&2; exit 1
fi

version="${tag#v}"
asset="komposyn-lg_${version}_${target}.tar.gz"
temp="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-runtime.XXXXXX")"
cleanup() { rm -rf "$temp"; }
trap cleanup EXIT
gh release download "$tag" --repo "$repo" --dir "$temp" \
	--pattern "$asset" --pattern "$asset.sig" --pattern "$asset.pem" \
	--pattern SHA256SUMS --pattern SHA256SUMS.sig --pattern SHA256SUMS.pem
cosign verify-blob --signature "$temp/SHA256SUMS.sig" --certificate "$temp/SHA256SUMS.pem" \
	--certificate-identity-regexp "$identity" --certificate-oidc-issuer "$issuer" "$temp/SHA256SUMS"
cosign verify-blob --signature "$temp/$asset.sig" --certificate "$temp/$asset.pem" \
	--certificate-identity-regexp "$identity" --certificate-oidc-issuer "$issuer" "$temp/$asset"
if [[ "$sha_tool" == sha256sum ]]; then
	(cd "$temp" && grep "  ${asset}$" SHA256SUMS | sha256sum -c -)
else
	(cd "$temp" && grep "  ${asset}$" SHA256SUMS | shasum -a 256 -c -)
fi
mkdir "$temp/extracted"
tar -xzf "$temp/$asset" -C "$temp/extracted"
binary="$temp/extracted/komposyn-lg"
version_output="$("$binary" version)"
[[ "$version_output" == *"komposyn-lg $version (core $version)"* ]] || {
	echo "unexpected version output on $target: $version_output" >&2
	exit 1
}
protocol_output="$(printf '%s\n' \
	'{"protocol":"komposyn.token-logger","version":1,"id":"runtime-init","method":"initialize","params":{}}' \
	'{"protocol":"komposyn.token-logger","version":1,"id":"runtime-shutdown","method":"shutdown"}' | "$binary" harness)"
jq -se 'length == 2 and .[0].id == "runtime-init" and .[0].result.plugin_version == "0.7.0" and .[0].result.cli_version == "0.7.0" and .[1].id == "runtime-shutdown"' <<<"$protocol_output" >/dev/null || {
	echo "protocol-v1 runtime exchange failed on $target: $protocol_output" >&2
	exit 1
}
echo "signed published $tag binary ran natively on $target; protocol v1 initialize/shutdown passed"
