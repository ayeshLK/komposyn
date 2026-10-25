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

usage() {
	echo "usage: test-release-install-unix.sh --version X.Y.Z --commit FULL_SHA --artifacts-dir DIR" >&2
}

version=""
commit=""
artifacts_dir=""
while (($#)); do
	case "$1" in
		--version|--commit|--artifacts-dir)
			(($# >= 2)) || { usage; exit 2; }
			key="$1"
			value="$2"
			case "$key" in
				--version) version="$value" ;;
				--commit) commit="$value" ;;
				--artifacts-dir) artifacts_dir="$value" ;;
			esac
			shift 2
			;;
		*) usage; exit 2 ;;
	esac
done
if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ || ! "$commit" =~ ^[0-9a-f]{40}$ || -z "$artifacts_dir" ]]; then
	usage
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-release-install.XXXXXXXX")"
trap 'rm -rf "$tmp"' EXIT

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
	else shasum -a 256 "$1" | awk '{print $1}'; fi
}

install_dir="$tmp/bin with spaces"
mkdir -p "$install_dir"
printf 'production CLI fixture\n' >"$install_dir/komposyn-lg"
chmod 0755 "$install_dir/komposyn-lg"
fixture="$tmp/.komposyn"
mkdir -m 0700 "$fixture"
auth_file="$fixture/auth.json"
settings_file="$fixture/settings.json"
printf '{"schema_version":1,"credentials":[]}\n' >"$auth_file"
printf '{"schema_version":1,"ui":{"plugin":"tview"}}\n' >"$settings_file"
chmod 0600 "$auth_file" "$settings_file"
auth_hash="$(sha256_of "$auth_file")"
settings_hash="$(sha256_of "$settings_file")"
target_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
target_arch="$(uname -m)"
case "$target_arch" in
	x86_64|amd64) target_arch=amd64 ;;
	aarch64|arm64) target_arch=arm64 ;;
	*) echo "unsupported architecture $target_arch" >&2; exit 1 ;;
esac
if [[ "$target_os" == darwin ]] && command -v sysctl >/dev/null 2>&1 && [[ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" == 1 ]]; then
	target_arch=arm64
fi
asset="komposyn_${version}_${target_os}_${target_arch}.tar.gz"
[[ -s "$artifacts_dir/$asset" ]] || { echo "signed release archive not found: $asset" >&2; exit 1; }

installer=(bash "$root/scripts/install.sh" --version "$version" --artifacts-dir "$artifacts_dir" --install-dir "$install_dir")
verify_preserved_state() {
	[[ "$(sha256_of "$auth_file")" == "$auth_hash" ]] || { echo 'installer changed auth.json' >&2; exit 1; }
	[[ "$(sha256_of "$settings_file")" == "$settings_hash" ]] || { echo 'installer changed settings.json' >&2; exit 1; }
	[[ "$(stat -c '%a' "$fixture" 2>/dev/null || stat -f '%Lp' "$fixture")" == 700 ]] || { echo 'installer changed .komposyn permissions' >&2; exit 1; }
	[[ "$(stat -c '%a' "$auth_file" 2>/dev/null || stat -f '%Lp' "$auth_file")" == 600 ]] || { echo 'installer changed auth.json permissions' >&2; exit 1; }
	[[ "$(stat -c '%a' "$settings_file" 2>/dev/null || stat -f '%Lp' "$settings_file")" == 600 ]] || { echo 'installer changed settings.json permissions' >&2; exit 1; }
	grep -Fxq 'production CLI fixture' "$install_dir/komposyn-lg" || { echo 'installer changed komposyn-lg' >&2; exit 1; }
}
verify_installed_cli() {
	local output help check_output
	output="$("$install_dir/komposyn" --version)"
	[[ "$output" == "komposyn version=$version commit=$commit target=$target_os/$target_arch" ]] || {
		echo "unexpected installed version metadata: $output" >&2
		exit 1
	}
	help="$("$install_dir/komposyn" --help)"
	grep -q -- '--check' <<<"$help" || { echo 'installed help omitted --check' >&2; exit 1; }
	check_output="$("$install_dir/komposyn" --check --auth-file "$auth_file" --settings-file "$settings_file")"
	grep -Fxq 'UI: tview' <<<"$check_output" || { echo 'installed --check rejected fixture settings' >&2; exit 1; }
	grep -Fxq 'Configured provider profiles: 0' <<<"$check_output" || { echo 'installed --check returned unexpected profiles' >&2; exit 1; }
}

# Fresh install, same-version reinstall, and replacement of a pre-existing file.
"${installer[@]}"
verify_installed_cli
verify_preserved_state
"${installer[@]}"
verify_installed_cli
printf 'previous experimental executable\n' >"$install_dir/komposyn"
"${installer[@]}"
verify_installed_cli
verify_preserved_state

bash "$root/scripts/uninstall.sh" --install-dir "$install_dir"
[[ ! -e "$install_dir/komposyn" ]] || { echo 'uninstaller left komposyn behind' >&2; exit 1; }
verify_preserved_state
echo "Signed release install lifecycle passed for $asset using the configured Cosign verifier."
