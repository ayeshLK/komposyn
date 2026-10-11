#!/usr/bin/env bash
set -euo pipefail

repo="${KOMPOSYN_REPO:-ayeshLK/komposyn}"
version="${KOMPOSYN_VERSION:-latest}"
install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME}/.local/bin}"
base_url="https://github.com/${repo}/releases"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
	 darwin) os=darwin ;;
	 linux) os=linux ;;
	 *) echo "unsupported operating system: $os" >&2; exit 1 ;;
esac
case "$arch" in
	 x86_64|amd64) arch=amd64 ;;
	 aarch64|arm64) arch=arm64 ;;
	 *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

if [[ "$version" == latest ]]; then
	version="$(curl -fsSL "${base_url}/latest/download/SHA256SUMS" | sed -n 's/^# version=//p' | head -1)"
	if [[ -z "$version" ]]; then
		version="latest"
	fi
fi
version="${version#v}"
asset="komposyn-lg_${version}_${os}_${arch}.tar.gz"
if [[ "$version" == latest ]]; then
	asset_url="${base_url}/latest/download/${asset}"
	checksums_url="${base_url}/latest/download/SHA256SUMS"
else
	asset_url="${base_url}/download/v${version}/${asset}"
	checksums_url="${base_url}/download/v${version}/SHA256SUMS"
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
curl -fsSL "$asset_url" -o "$tmp_dir/$asset"
curl -fsSL "$checksums_url" -o "$tmp_dir/SHA256SUMS"
(cd "$tmp_dir" && grep "  ${asset}$" SHA256SUMS | sha256sum -c -)
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
mkdir -p "$install_dir"
chmod 0755 "$tmp_dir/komposyn-lg"
mv "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
echo "installed komposyn-lg ${version} at ${install_dir}/komposyn-lg"
echo "existing database, configuration, and consent settings were preserved"
