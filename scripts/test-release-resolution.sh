#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
release_dir="$tmp_dir/release"
install_dir="$tmp_dir/install"
mkdir -p "$release_dir" "$install_dir"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
asset="komposyn-lg_1.2.3_${os}_${arch}.tar.gz"
printf '#!/bin/sh\necho fixture-binary\n' > "$tmp_dir/komposyn-lg"
chmod 0755 "$tmp_dir/komposyn-lg"
tar -C "$tmp_dir" -czf "$release_dir/$asset" komposyn-lg
(
	cd "$release_dir"
	echo '# version=1.2.3'
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$asset"; else shasum -a 256 "$asset"; fi
) > "$release_dir/SHA256SUMS"
touch "$release_dir/$asset.sig" "$release_dir/$asset.pem"

mock_cosign="$tmp_dir/cosign"
cat > "$mock_cosign" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "${1:-}" == verify-blob ]]
shift
identity=''
issuer=''
while (($#)); do
	case "$1" in
		--certificate-identity-regexp) identity="$2"; shift 2 ;;
		--certificate-oidc-issuer) issuer="$2"; shift 2 ;;
		*) shift ;;
	esac
done
[[ "$identity" == '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$' ]]
[[ "$issuer" == 'https://token.actions.githubusercontent.com' ]]
EOF
chmod 0755 "$mock_cosign"
metadata="$tmp_dir/metadata.json"
write_metadata() { printf '%s\n' "$1" > "$metadata"; }
write_metadata '{"tag_name":"v1.2.3","draft":false,"prerelease":false}'
standalone_installer="$tmp_dir/install.sh"
cp "$root_dir/scripts/install.sh" "$standalone_installer"

latest_output="$(env -u KOMPOSYN_VERSION \
	KOMPOSYN_REPO=ayeshLK/komposyn \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_RELEASE_METADATA_URL="file://$metadata" \
	KOMPOSYN_INSTALL_DIR="$install_dir" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	bash < "$standalone_installer")"
[[ "$latest_output" == *'resolved Komposyn release: v1.2.3'* ]] || { echo 'default installer did not show resolved tag' >&2; exit 1; }
[[ -x "$install_dir/komposyn-lg" ]] || { echo 'default installer did not install fixture artifact' >&2; exit 1; }
"$install_dir/komposyn-lg" | grep -q fixture-binary

printf '#!/bin/sh\necho preserved-binary\n' > "$install_dir/komposyn-lg"
if KOMPOSYN_VERSION=v1.2.3 \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_RELEASE_METADATA_URL='file:///must-not-be-requested.json' \
	KOMPOSYN_INSTALL_DIR="$install_dir" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	bash "$standalone_installer" >/dev/null; then
	"$install_dir/komposyn-lg" | grep -q fixture-binary || { echo 'pinned release selected the wrong archive' >&2; exit 1; }
else
	echo 'explicit version pin unexpectedly required release metadata' >&2
	exit 1
fi

printf '#!/bin/sh\necho preserved-binary\n' > "$install_dir/komposyn-lg"
for invalid in \
	'{"tag_name":"v1.2.3","draft":true,"prerelease":false}' \
	'{"tag_name":"v1.2.3","draft":false,"prerelease":true}' \
	'{"tag_name":"main","draft":false,"prerelease":false}' \
	'{"tag_name":"v1.2","draft":false,"prerelease":false}' \
	'{"tag_name":"v1.2.3","draft":"false","prerelease":false}' \
	'{broken json'; do
	write_metadata "$invalid"
	if env -u KOMPOSYN_VERSION \
		KOMPOSYN_RELEASE_DIR="$release_dir" \
		KOMPOSYN_RELEASE_METADATA_URL="file://$metadata" \
		KOMPOSYN_INSTALL_DIR="$install_dir" \
		KOMPOSYN_COSIGN_BIN="$mock_cosign" \
		bash "$standalone_installer" >/dev/null 2>&1; then
		echo "installer accepted invalid latest metadata: $invalid" >&2
		exit 1
	fi
	"$install_dir/komposyn-lg" | grep -q preserved-binary || { echo 'resolution failure replaced installed binary' >&2; exit 1; }
done

if env -u KOMPOSYN_VERSION \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_RELEASE_METADATA_URL='file:///missing-release-metadata.json' \
	KOMPOSYN_INSTALL_DIR="$install_dir" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	bash "$standalone_installer" >/dev/null 2>&1; then
	echo 'installer accepted unavailable latest metadata' >&2
	exit 1
fi
"$install_dir/komposyn-lg" | grep -q preserved-binary || { echo 'API failure replaced installed binary' >&2; exit 1; }

echo 'default and pinned installer resolution checks passed'
