#!/usr/bin/env bash
set -euo pipefail

repo="${KOMPOSYN_REPO:-ayeshLK/komposyn}"
version="${KOMPOSYN_VERSION:-}"
install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME}/.local/bin}"
cosign_bin="${KOMPOSYN_COSIGN_BIN:-cosign}"
cosign_identity="${KOMPOSYN_COSIGN_IDENTITY_REGEXP:-^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$}"
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

if [[ -z "$version" ]]; then
	if [[ -n "$release_dir" && -z "${KOMPOSYN_RELEASE_METADATA_URL:-}" ]]; then
		version="$(sed -n 's/^# version=//p' "$release_dir/SHA256SUMS" | head -1)"
		[[ -n "$version" ]] || { echo 'local SHA256SUMS is missing # version metadata' >&2; exit 1; }
		[[ "$version" == v* ]] || version="v$version"
	else
		command -v jq >/dev/null 2>&1 || { echo 'jq is required to resolve the latest release' >&2; exit 1; }
		metadata_url="${KOMPOSYN_RELEASE_METADATA_URL:-https://api.github.com/repos/${repo}/releases/latest}"
		metadata="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$metadata_url")" || {
			echo "unable to resolve latest release for $repo" >&2
			exit 1
		}
		version="$(jq -er '
		  if type == "object" and .draft == false and .prerelease == false and
		     (.tag_name | type == "string" and test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))
		  then .tag_name else error("not a stable Komposyn release") end
		' <<<"$metadata")" || {
			echo "latest release metadata for $repo is malformed or not a stable release" >&2
			exit 1
		}
	fi
fi
[[ "$version" == v* ]] || version="v$version"
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "invalid release version/tag: $version" >&2; exit 1; }
tag="$version"
version="${tag#v}"
echo "resolved Komposyn release: $tag"
asset="komposyn-lg_${version}_${os}_${arch}.tar.gz"
asset_url="${base_url}/download/${tag}/${asset}"
checksums_url="${base_url}/download/${tag}/SHA256SUMS"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
if [[ -n "$release_dir" ]]; then
	manifest_version="$(sed -n 's/^# version=//p' "$release_dir/SHA256SUMS" | head -1)"
	[[ "${manifest_version#v}" == "$version" ]] || { echo "local release metadata does not match requested tag $tag" >&2; exit 1; }
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
restart_collector=0
installed_binary="$install_dir/komposyn-lg"
if [[ -x "$installed_binary" ]]; then
	capability="$("$installed_binary" _watch-lifecycle 2>/dev/null || true)"
	if [[ "$capability" == 1 ]]; then
		collector_status="$("$installed_binary" watch status --format json)" || {
			echo 'unable to inspect the existing background collector; executable was not replaced' >&2
			exit 1
		}
		if [[ "$collector_status" == *'"state":"running"'* || "$collector_status" == *'"state":"degraded"'* || "$collector_status" == *'"state":"starting"'* || "$collector_status" == *'"state":"stopping"'* ]]; then
			restart_collector=1
			"$installed_binary" watch stop --timeout 30s || {
				echo 'unable to stop the existing background collector; executable was not replaced' >&2
				exit 1
			}
		fi
	fi
fi
mkdir -p "$install_dir"
chmod 0755 "$tmp_dir/komposyn-lg"
mv "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
echo "installed komposyn-lg ${version} at ${install_dir}/komposyn-lg"
echo "existing database, configuration, and consent settings were preserved"
if (( restart_collector )); then
	if "$install_dir/komposyn-lg" watch start; then
		echo 'restarted the background collector after upgrade'
	else
		echo "upgrade succeeded, but the background collector could not be restarted; run 'komposyn-lg watch start' after reviewing its registered sources" >&2
		exit 1
	fi
fi
