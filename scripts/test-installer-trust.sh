#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

release_dir="$tmp_dir/release"
install_dir="$tmp_dir/install"
mkdir -p "$release_dir"
printf '#!/bin/sh\nexit 0\n' > "$tmp_dir/komposyn-lg"
chmod 0755 "$tmp_dir/komposyn-lg"
tar -C "$tmp_dir" -czf "$release_dir/komposyn-lg_0.1.0_linux_amd64.tar.gz" komposyn-lg
(
	cd "$release_dir"
	echo '# version=0.1.0'
	sha256sum komposyn-lg_0.1.0_linux_amd64.tar.gz
) > "$release_dir/SHA256SUMS"

mock_cosign="$tmp_dir/cosign"
touch "$release_dir/komposyn-lg_0.1.0_linux_amd64.tar.gz.sig" "$release_dir/komposyn-lg_0.1.0_linux_amd64.tar.gz.pem"
cat > "$mock_cosign" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "${1:-}" == verify-blob ]]
shift
identity=''
while (($#)); do
	case "$1" in
		--certificate-identity-regexp) identity="$2"; shift 2 ;;
		*) shift ;;
	esac
done
[[ "$identity" == '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$' ]]
EOF
chmod 0755 "$mock_cosign"

KOMPOSYN_VERSION=v0.1.0 \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_INSTALL_DIR="$install_dir" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	bash "$root_dir/scripts/install.sh" >/dev/null
[[ -x "$install_dir/komposyn-lg" ]] || {
	echo 'installer did not accept the configured release identity' >&2
	exit 1
}

for identity in \
	'^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/tags/.*$' \
	'^https://github.com/example/unrelated/.github/workflows/release.yml@refs/heads/master$'; do
	if KOMPOSYN_VERSION=v0.1.0 \
		KOMPOSYN_RELEASE_DIR="$release_dir" \
		KOMPOSYN_INSTALL_DIR="$tmp_dir/rejected" \
		KOMPOSYN_COSIGN_BIN="$mock_cosign" \
		KOMPOSYN_COSIGN_IDENTITY_REGEXP="$identity" \
		bash "$root_dir/scripts/install.sh" >/dev/null 2>&1; then
		echo "installer accepted an unexpected identity: $identity" >&2
		exit 1
	fi
done

grep -Fq 'refs/heads/master$' "$root_dir/scripts/install.ps1"
! grep -Fq 'refs/tags/' "$root_dir/scripts/install.sh"
! grep -Fq 'refs/tags/' "$root_dir/scripts/install.ps1"
echo 'installer signing identity acceptance and rejection checks passed'
