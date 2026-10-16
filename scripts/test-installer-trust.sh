#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bash "$root_dir/scripts/test-release-resolution.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

release_dir="$tmp_dir/release"
install_dir="$tmp_dir/install"
mkdir -p "$release_dir"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
asset="komposyn-lg_0.1.0_${os}_${arch}.tar.gz"
printf '#!/bin/sh\nexit 0\n' > "$tmp_dir/komposyn-lg"
chmod 0755 "$tmp_dir/komposyn-lg"
tar -C "$tmp_dir" -czf "$release_dir/$asset" komposyn-lg
(
	cd "$release_dir"
	echo '# version=0.1.0'
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$asset"; else shasum -a 256 "$asset"; fi
) > "$release_dir/SHA256SUMS"

mock_cosign="$tmp_dir/cosign"
touch "$release_dir/$asset.sig" "$release_dir/$asset.pem"
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

install_output="$(KOMPOSYN_VERSION=v0.1.0 \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_INSTALL_DIR="$install_dir" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	KOMPOSYN_RELEASE_METADATA_URL='file:///must-not-be-requested.json' \
	bash "$root_dir/scripts/install.sh")"
[[ "$install_output" == *'resolved Komposyn release: v0.1.0'* ]] || {
	echo 'installer did not report its pinned release tag' >&2
	exit 1
}
[[ -x "$install_dir/komposyn-lg" ]] || {
	echo 'installer did not accept the configured release identity' >&2
	exit 1
}

if KOMPOSYN_VERSION=v0.2.0 \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_INSTALL_DIR="$tmp_dir/mismatched" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	bash "$root_dir/scripts/install.sh" >/dev/null 2>&1; then
	echo 'installer accepted a local manifest for a different pinned version' >&2
	exit 1
fi

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

# A lifecycle-capable installed binary must stop before replacement, and the
# verified replacement must restart only after installation succeeds.
lifecycle_install="$tmp_dir/lifecycle-install"
lifecycle_stop="$tmp_dir/lifecycle-stop"
lifecycle_restart="$tmp_dir/lifecycle-restart"
mkdir -p "$lifecycle_install"
cat > "$lifecycle_install/komposyn-lg" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
	_watch-lifecycle) echo 1 ;;
	watch)
		case "${2:-}" in
			status) echo '{"state":"running","mode":"background"}' ;;
			stop) : > "$KOMPOSYN_TEST_STOP_MARKER" ;;
		esac
		;;
esac
EOF
chmod 0755 "$lifecycle_install/komposyn-lg"
cat > "$tmp_dir/komposyn-lg" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
	_watch-lifecycle) echo 1 ;;
	watch) [[ "${2:-}" == start ]] && : > "$KOMPOSYN_TEST_RESTART_MARKER" ;;
esac
EOF
chmod 0755 "$tmp_dir/komposyn-lg"
tar -C "$tmp_dir" -czf "$release_dir/$asset" komposyn-lg
(
	cd "$release_dir"
	echo '# version=0.1.0'
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$asset"; else shasum -a 256 "$asset"; fi
) > "$release_dir/SHA256SUMS"
KOMPOSYN_VERSION=v0.1.0 \
	KOMPOSYN_RELEASE_DIR="$release_dir" \
	KOMPOSYN_INSTALL_DIR="$lifecycle_install" \
	KOMPOSYN_COSIGN_BIN="$mock_cosign" \
	KOMPOSYN_TEST_STOP_MARKER="$lifecycle_stop" \
	KOMPOSYN_TEST_RESTART_MARKER="$lifecycle_restart" \
	bash "$root_dir/scripts/install.sh" >/dev/null
[[ -f "$lifecycle_stop" && -f "$lifecycle_restart" ]] || {
	echo 'installer did not stop and restart a lifecycle-capable collector' >&2
	exit 1
}

# Foreground and embedded owners are stopped for safe replacement but must not
# be silently converted into detached background collection. Source counts
# cover both empty and populated registries; mode remains the deciding field.
for lifecycle_mode in foreground embedded; do
	for source_count in 0 1; do
		mode_install="$tmp_dir/lifecycle-${lifecycle_mode}-${source_count}"
		mode_stop="$tmp_dir/lifecycle-${lifecycle_mode}-${source_count}-stop"
		mode_restart="$tmp_dir/lifecycle-${lifecycle_mode}-${source_count}-restart"
		mkdir -p "$mode_install"
		cat > "$mode_install/komposyn-lg" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
	_watch-lifecycle) echo 1 ;;
	watch)
		case "${2:-}" in
			status) printf '{"state":"running","mode":"%s","source_count":%s}\n' "$KOMPOSYN_TEST_COLLECTOR_MODE" "$KOMPOSYN_TEST_SOURCE_COUNT" ;;
			stop) : > "$KOMPOSYN_TEST_STOP_MARKER" ;;
		esac
		;;
esac
EOF
		chmod 0755 "$mode_install/komposyn-lg"
		mode_output="$(KOMPOSYN_VERSION=v0.1.0 \
			KOMPOSYN_RELEASE_DIR="$release_dir" \
			KOMPOSYN_INSTALL_DIR="$mode_install" \
			KOMPOSYN_COSIGN_BIN="$mock_cosign" \
			KOMPOSYN_TEST_COLLECTOR_MODE="$lifecycle_mode" \
			KOMPOSYN_TEST_SOURCE_COUNT="$source_count" \
			KOMPOSYN_TEST_STOP_MARKER="$mode_stop" \
			KOMPOSYN_TEST_RESTART_MARKER="$mode_restart" \
			bash "$root_dir/scripts/install.sh")"
		[[ -f "$mode_stop" && ! -e "$mode_restart" && "$mode_output" == *"previous ${lifecycle_mode} collector was stopped"* ]] || {
			echo "installer mishandled ${lifecycle_mode} collector with source_count=${source_count}" >&2
			exit 1
		}
	done
done

! grep -Fq 'refs/tags/' "$root_dir/scripts/install.sh"
! grep -Fq 'refs/tags/' "$root_dir/scripts/install.ps1"
echo 'installer signing identity acceptance and rejection checks passed'
