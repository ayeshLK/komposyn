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

old_tag="${1:-v0.6.0}"
new_tag="${2:-v0.7.0}"
repo="${KOMPOSYN_REPO:-ayeshLK/komposyn}"
identity='^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$'
issuer='https://token.actions.githubusercontent.com'
host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
host_arch="$(uname -m)"
case "$host_os:$host_arch" in
	linux:x86_64|linux:amd64) target="linux_amd64" ;;
	linux:aarch64|linux:arm64) target="linux_arm64" ;;
	darwin:x86_64) target="darwin_amd64" ;;
	darwin:arm64) target="darwin_arm64" ;;
	*) echo "unsupported native Unix verification host: $host_os:$host_arch" >&2; exit 1 ;;
esac

for tool in gh cosign python3 jq; do
	command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 1; }
done
if command -v sha256sum >/dev/null 2>&1; then
	sha_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
	sha_tool=shasum
else
	echo 'sha256sum or shasum is required' >&2
	exit 1
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-active-upgrade.XXXXXX")"
old_version="${old_tag#v}"
old_asset="komposyn-lg_${old_version}_${target}.tar.gz"
old_dir="$tmp_dir/old-release"
install_dir="$tmp_dir/bin"
data_dir="$tmp_dir/data"
source_dir="$tmp_dir/source"
mkdir -p "$old_dir" "$install_dir" "$data_dir" "$source_dir"
cleanup() {
	if [[ -x "$install_dir/komposyn-lg" && -f "$data_dir/token-logger.db" ]]; then
		TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch stop --timeout 10s >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp_dir"
}
trap cleanup EXIT

gh release download "$old_tag" --repo "$repo" --dir "$old_dir" \
	--pattern "$old_asset" --pattern SHA256SUMS --pattern SHA256SUMS.sig --pattern SHA256SUMS.pem \
	--pattern "$old_asset.sig" --pattern "$old_asset.pem"
if [[ "$sha_tool" == sha256sum ]]; then
	(cd "$old_dir" && grep "  ${old_asset}$" SHA256SUMS | sha256sum -c -)
else
	(cd "$old_dir" && grep "  ${old_asset}$" SHA256SUMS | shasum -a 256 -c -)
fi
cosign verify-blob --signature "$old_dir/$old_asset.sig" --certificate "$old_dir/$old_asset.pem" \
	--certificate-identity-regexp "$identity" --certificate-oidc-issuer "$issuer" "$old_dir/$old_asset"
cosign verify-blob --signature "$old_dir/SHA256SUMS.sig" --certificate "$old_dir/SHA256SUMS.pem" \
	--certificate-identity-regexp "$identity" --certificate-oidc-issuer "$issuer" "$old_dir/SHA256SUMS"
tar -xzf "$old_dir/$old_asset" -C "$old_dir"
cp "$old_dir/komposyn-lg" "$install_dir/komposyn-lg"
chmod 0755 "$install_dir/komposyn-lg"
binary="$install_dir/komposyn-lg"

run_old() { TOKEN_LOGGER_DATA_DIR="$data_dir" "$binary" "$@"; }
fixture="$(cd "$(dirname "${BASH_SOURCE[0]}")/../plugins/library/token-logger/testdata/codex" && pwd)/normal.jsonl"
cp "$fixture" "$source_dir/normal.jsonl"
run_old settings --collector off --auto-start off >/dev/null
run_old watch add "$source_dir" >/dev/null
run_old import "$source_dir/normal.jsonl" --format json >/dev/null
before_report="$(run_old usage --format json)"
before_usage="$(jq -c '.summary | {request_count,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_output_tokens,total_tokens}' <<<"$before_report")"
run_old watch start --interval 1s >/dev/null

old_status=''
for _ in {1..50}; do
	old_status="$(run_old watch status --format json)"
	if [[ "$(jq -r '.state' <<<"$old_status")" == running ]]; then
		break
	fi
	sleep 0.1
done
old_pid="$(jq -er 'select(.state == "running" and .mode == "background") | .pid | select(. > 0)' <<<"$old_status")" || {
	echo "historical collector did not become active: $old_status" >&2
	exit 1
}
old_instance="$(jq -er '.instance_id' <<<"$old_status")"
settings_digest="$("$sha_tool" "$data_dir/settings.json" | awk '{print $1}')"

export KOMPOSYN_REPO="$repo" KOMPOSYN_VERSION="$new_tag" KOMPOSYN_INSTALL_DIR="$install_dir"
export TOKEN_LOGGER_DATA_DIR="$data_dir"
install_output="$(bash "$(dirname "${BASH_SOURCE[0]}")/install.sh")"
[[ "$install_output" == *'restarted the background collector after upgrade'* ]] || {
	echo "signature-verifying installer did not report collector restart: $install_output" >&2
	exit 1
}
new_status="$("$binary" watch status --format json)"
new_pid="$(jq -er 'select(.state == "running" and .mode == "background" and .stale == false) | .pid | select(. > 0)' <<<"$new_status")" || {
	echo "upgraded collector is not running in background mode: $new_status" >&2
	exit 1
}
new_instance="$(jq -er '.instance_id' <<<"$new_status")"
[[ "$new_pid" != "$old_pid" && "$new_instance" != "$old_instance" ]] || {
	echo "collector did not stop and restart with a new process and lease identity" >&2
	exit 1
}
kill -0 "$new_pid" 2>/dev/null || { echo 'restarted collector process is not alive' >&2; exit 1; }
after_report="$("$binary" usage --format json)"
after_usage="$(jq -c '.summary | {request_count,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_output_tokens,total_tokens}' <<<"$after_report")"
[[ "$after_usage" == "$before_usage" ]] || {
	echo "usage totals changed during upgrade: before=$before_usage after=$after_usage" >&2
	exit 1
}
[[ "$("$sha_tool" "$data_dir/settings.json" | awk '{print $1}')" == "$settings_digest" ]] || {
	echo 'settings or consent changed during upgrade' >&2
	exit 1
}
python3 - "$data_dir/token-logger.db" "$new_pid" "$new_instance" <<'PY'
import sqlite3
import sys

connection = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
try:
    rows = connection.execute(
        "SELECT pid, instance_id, state, mode FROM collector_runtime"
    ).fetchall()
finally:
    connection.close()
expected = [(int(sys.argv[2]), sys.argv[3], "running", "background")]
if rows != expected:
    raise SystemExit(f"expected one healthy lease owner {expected!r}, found {rows!r}")
PY
"$binary" watch stop --timeout 10s >/dev/null
bash "$(dirname "${BASH_SOURCE[0]}")/uninstall.sh" >/dev/null
[[ ! -e "$binary" && -f "$data_dir/token-logger.db" && -f "$data_dir/settings.json" ]] || {
	echo 'uninstall failed to remove the binary or preserve local data' >&2
	exit 1
}
echo "signed $old_tag to $new_tag active-collector upgrade passed on $host_os/$host_arch"
