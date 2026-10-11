#!/usr/bin/env bash
set -euo pipefail

repo="${KOMPOSYN_REPO:-ayeshLK/komposyn}"
version="${KOMPOSYN_VERSION:-latest}"
install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME}/.local/bin}"
cosign_bin="${KOMPOSYN_COSIGN_BIN:-cosign}"
cosign_identity="${KOMPOSYN_COSIGN_IDENTITY_REGEXP:-^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/tags/.*$}"
cosign_issuer="https://token.actions.githubusercontent.com"
base_url="https://github.com/${repo}/releases"
release_dir="${KOMPOSYN_RELEASE_DIR:-}"
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
	if [[ -n "$release_dir" ]]; then
		version="$(sed -n 's/^# version=//p' "$release_dir/SHA256SUMS" | head -1)"
	else
		version="$(curl -fsSL "${base_url}/latest/download/SHA256SUMS" | sed -n 's/^# version=//p' | head -1)"
	fi
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
if [[ -n "$release_dir" ]]; then
	for release_file in "$release_dir/$asset" "$release_dir/SHA256SUMS" "$release_dir/$asset.sig" "$release_dir/$asset.pem"; do
		[[ -f "$release_file" ]] || { echo "missing local release artifact: $release_file" >&2; exit 1; }
	done
	cp "$release_dir/$asset" "$tmp_dir/$asset"
	cp "$release_dir/SHA256SUMS" "$tmp_dir/SHA256SUMS"
	cp "$release_dir/$asset.sig" "$tmp_dir/$asset.sig"
	cp "$release_dir/$asset.pem" "$tmp_dir/$asset.pem"
else
	curl -fsSL "$asset_url" -o "$tmp_dir/$asset"
	curl -fsSL "$checksums_url" -o "$tmp_dir/SHA256SUMS"
	curl -fsSL "${asset_url}.sig" -o "$tmp_dir/$asset.sig"
	curl -fsSL "${asset_url}.pem" -o "$tmp_dir/$asset.pem"
fi
command -v "$cosign_bin" >/dev/null 2>&1 || { echo "cosign is required to verify release signatures" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
	(cd "$tmp_dir" && grep "  ${asset}$" SHA256SUMS | sha256sum -c -)
else
	(cd "$tmp_dir" && grep "  ${asset}$" SHA256SUMS | shasum -a 256 -c -)
fi
"$cosign_bin" verify-blob \
	--signature "$tmp_dir/$asset.sig" \
	--certificate "$tmp_dir/$asset.pem" \
	--certificate-identity-regexp "$cosign_identity" \
	--certificate-oidc-issuer "$cosign_issuer" \
	"$tmp_dir/$asset"
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
mkdir -p "$install_dir"
chmod 0755 "$tmp_dir/komposyn-lg"
mv "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
echo "installed komposyn-lg ${version} at ${install_dir}/komposyn-lg"
echo "existing database, configuration, and consent settings were preserved"
