#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:?usage: verify-release.sh <release-dir>}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(sed -n 's/^# version=//p' "$dist_dir/SHA256SUMS" | head -1)"
if [[ -z "$version" ]]; then
  echo "release checksum manifest does not declare a version" >&2
  exit 1
fi
(cd "$dist_dir" && sha256sum -c SHA256SUMS)

case "$(uname -s):$(uname -m)" in
  Linux:x86_64) asset="komposyn-lg_${version}_linux_amd64.tar.gz" ;;
  Linux:aarch64|Linux:arm64) asset="komposyn-lg_${version}_linux_arm64.tar.gz" ;;
  Darwin:x86_64) asset="komposyn-lg_${version}_darwin_amd64.tar.gz" ;;
  Darwin:arm64) asset="komposyn-lg_${version}_darwin_arm64.tar.gz" ;;
  *) echo "unsupported verification host: $(uname -s):$(uname -m)" >&2; exit 1 ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
tar -xzf "$dist_dir/$asset" -C "$tmp_dir"
for required in komposyn-lg LICENSE NOTICE README.md THIRD_PARTY_LICENSES.md third_party_licenses; do
  [[ -e "$tmp_dir/$required" ]] || { echo "archive missing $required" >&2; exit 1; }
done

install_dir="$tmp_dir/bin"
data_dir="$tmp_dir/data"
mkdir -p "$install_dir" "$data_dir"
cp "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
chmod 0755 "$install_dir/komposyn-lg"
TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" settings --collector off --auto-start off >/dev/null
TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" usage --format json >/dev/null
[[ -e "$data_dir/settings.json" ]] || { echo "settings were not created" >&2; exit 1; }
[[ -e "$data_dir/token-logger.db" ]] || { echo "database was not created" >&2; exit 1; }

# Replacing the executable is the initial-release upgrade operation.
cp "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
[[ -e "$data_dir/settings.json" && -e "$data_dir/token-logger.db" ]] || {
  echo "upgrade did not preserve local data" >&2
  exit 1
}

KOMPOSYN_INSTALL_DIR="$install_dir" bash "$root_dir/scripts/uninstall.sh" >/dev/null
[[ ! -e "$install_dir/komposyn-lg" ]] || { echo "uninstall left executable behind" >&2; exit 1; }
[[ -e "$data_dir/settings.json" && -e "$data_dir/token-logger.db" ]] || {
  echo "uninstall removed local data" >&2
  exit 1
}
echo "release install, upgrade, and data-preserving uninstall checks passed"
