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

version="${KOMPOSYN_VERSION:-}"
install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME:-}/.local/bin}"
artifacts_dir="${KOMPOSYN_ARTIFACTS_DIR:-}"
cosign_bin="${KOMPOSYN_COSIGN_BIN:-cosign}"
metadata_url="${KOMPOSYN_RELEASE_METADATA_URL:-}"
release_base='https://github.com/ayeshLK/komposyn/releases'
cosign_identity='^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$'
cosign_issuer='https://token.actions.githubusercontent.com'

usage() {
	cat <<'EOF'
Usage: install.sh [--version vX.Y.Z] [--install-dir PATH] [--artifacts-dir PATH]

Install the signed KompoSyn CLI for the current Unix OS and architecture.
Options also have KOMPOSYN_VERSION, KOMPOSYN_INSTALL_DIR, and
KOMPOSYN_ARTIFACTS_DIR environment equivalents. Latest stable resolution uses
GitHub release metadata and requires jq. All installs require curl, tar, a
SHA-256 utility, and Cosign.
EOF
}

fail() {
	echo "komposyn installer: $*" >&2
	exit 1
}

while (($#)); do
	case "$1" in
		--version)
			(($# >= 2)) || fail '--version requires a value'
			version="$2"
			shift 2
			;;
		--install-dir)
			(($# >= 2)) || fail '--install-dir requires a value'
			install_dir="$2"
			shift 2
			;;
		--artifacts-dir)
			(($# >= 2)) || fail '--artifacts-dir requires a value'
			artifacts_dir="$2"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*) fail "unknown argument: $1" ;;
	esac
done

[[ -n "${HOME:-}" ]] || fail 'HOME is not set'
[[ -n "$install_dir" ]] || fail 'install directory is empty'
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
command -v "$cosign_bin" >/dev/null 2>&1 || fail 'cosign is required to verify signed checksums'

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
	linux|darwin) ;;
	*) fail "unsupported operating system: $os" ;;
esac

arch="$(uname -m)"
case "$arch" in
	x86_64|amd64) arch=amd64 ;;
	aarch64|arm64) arch=arm64 ;;
	*) fail "unsupported architecture: $arch" ;;
	esac
# macOS reports the translated process architecture under Rosetta. Prefer the
# machine capability so a native Apple Silicon install selects its arm64 build.
if [[ "$os" == darwin ]] && command -v sysctl >/dev/null 2>&1 &&
	[[ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" == 1 ]]; then
	arch=arm64
fi

if [[ -z "$version" ]]; then
	if [[ -n "$artifacts_dir" && -z "$metadata_url" ]]; then
		fail 'local artifact installs require an explicit --version'
	fi
	command -v jq >/dev/null 2>&1 || fail 'jq is required to resolve the latest stable release; use --version to pin a release'
	metadata_url="${metadata_url:-https://api.github.com/repos/ayeshLK/komposyn/releases/latest}"
	metadata="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$metadata_url")" || fail 'unable to resolve latest KompoSyn release'
	version="$(jq -er '
	  if type == "object" and .draft == false and .prerelease == false and
	     (.tag_name | type == "string" and test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))
	  then .tag_name else error("not a stable release") end
	' <<<"$metadata")" || fail 'latest release metadata is malformed or is not a stable vX.Y.Z release'
fi

[[ "$version" == v* ]] || version="v$version"
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "invalid stable release version: $version"
tag="$version"
numeric_version="${version#v}"
archive="komposyn_${numeric_version}_${os}_${arch}.tar.gz"
checksum_file='checksums.txt'
bundle_file='checksums.txt.sigstore.json'

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-install.XXXXXXXX")" || fail 'could not create temporary directory'
trap 'rm -rf "$tmp_dir"' EXIT
trap 'exit 1' HUP INT TERM

if [[ -n "$artifacts_dir" ]]; then
	[[ -d "$artifacts_dir" ]] || fail "artifact directory does not exist: $artifacts_dir"
	for name in "$archive" "$checksum_file" "$bundle_file"; do
		[[ -f "$artifacts_dir/$name" && ! -L "$artifacts_dir/$name" ]] || fail "missing or invalid local release artifact: $artifacts_dir/$name"
		cp "$artifacts_dir/$name" "$tmp_dir/$name"
	done
else
	# Resolve the version once, then use only URLs pinned to that immutable tag.
	asset_base="${release_base}/download/${tag}"
	for name in "$archive" "$checksum_file" "$bundle_file"; do
		curl -fsSL "${asset_base}/${name}" -o "$tmp_dir/$name" || fail "could not download $name for $tag"
	done
fi

[[ -s "$tmp_dir/$archive" ]] || fail "release archive is empty: $archive"
[[ -s "$tmp_dir/$checksum_file" ]] || fail 'signed checksum manifest is empty'
[[ -s "$tmp_dir/$bundle_file" ]] || fail 'Cosign bundle is empty'

"$cosign_bin" verify-blob \
	--bundle "$tmp_dir/$bundle_file" \
	--certificate-identity-regexp "$cosign_identity" \
	--certificate-oidc-issuer "$cosign_issuer" \
	"$tmp_dir/$checksum_file" >/dev/null || fail 'checksum signature or signer identity verification failed'

checksum_line="$(awk -v target="$archive" '$2 == target { if (found++) exit 2; print; } END { if (!found) exit 1 }' "$tmp_dir/$checksum_file")" || fail "checksum manifest has no unique entry for $archive"
expected_hash="${checksum_line%% *}"
[[ "$expected_hash" =~ ^[[:xdigit:]]{64}$ ]] || fail "malformed SHA-256 entry for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	actual_hash="$(sha256sum "$tmp_dir/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
	actual_hash="$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1}')"
else
	fail 'sha256sum or shasum is required'
fi
actual_hash="$(printf '%s' "$actual_hash" | tr '[:upper:]' '[:lower:]')"
expected_hash="$(printf '%s' "$expected_hash" | tr '[:upper:]' '[:lower:]')"
[[ "$actual_hash" == "$expected_hash" ]] || fail "SHA-256 mismatch for $archive"

# Accept only the flat release layout. This rejects path traversal, unexpected
# executable names, and nested archive members before extraction.
members="$tmp_dir/members.txt"
tar -tzf "$tmp_dir/$archive" >"$members" || fail 'release archive is malformed'
expected_members=$'komposyn\nLICENSE\nNOTICE\nTHIRD_PARTY_LICENSES.txt'
[[ "$(LC_ALL=C sort "$members")" == "$(printf '%s\n' "$expected_members" | LC_ALL=C sort)" ]] || fail 'release archive has unexpected or missing members'
tar_listing="$tmp_dir/member-details.txt"
tar -tvzf "$tmp_dir/$archive" >"$tar_listing" || fail 'could not inspect release archive members'
while IFS= read -r entry; do
	[[ "${entry:0:1}" == '-' ]] || fail 'release archive contains a non-regular file'
done <"$tar_listing"

extract_dir="$tmp_dir/extracted"
mkdir "$extract_dir"
tar -xzf "$tmp_dir/$archive" -C "$extract_dir" || fail 'could not extract release archive'
for name in komposyn LICENSE NOTICE THIRD_PARTY_LICENSES.txt; do
	[[ -f "$extract_dir/$name" && ! -L "$extract_dir/$name" ]] || fail "archive member is not a regular file: $name"
done
[[ -x "$extract_dir/komposyn" ]] || fail 'archive executable is not executable'
version_output="$("$extract_dir/komposyn" --version 2>&1)" || fail 'release executable failed its --version check'
escaped_version="$(printf '%s' "$numeric_version" | sed 's/\./\\./g')"
version_pattern="^komposyn version=${escaped_version} commit=[[:xdigit:]]{40} target=${os}/${arch}$"
[[ "$version_output" =~ $version_pattern ]] ||
	fail "release executable metadata does not match requested ${tag} ${os}/${arch}: $version_output"

mkdir -p "$install_dir" || fail "could not create install directory: $install_dir"
[[ ! -d "$install_dir/komposyn" ]] || fail "install target is a directory: $install_dir/komposyn"
staging_dir="$(mktemp -d "$install_dir/.komposyn-install.XXXXXXXX")" || fail 'could not create a staging directory on the destination filesystem'
stage="$staging_dir/komposyn.new"
backup="$staging_dir/komposyn.old"
cleanup_install() {
	local preserve_stage_dir=0
	if [[ -e "$backup" && ! -e "$install_dir/komposyn" && ! -L "$install_dir/komposyn" ]]; then
		if ! mv "$backup" "$install_dir/komposyn"; then
			preserve_stage_dir=1
			echo "previous executable is recoverable at $backup" >&2
		fi
	fi
	rm -f "$stage"
	if ((preserve_stage_dir == 0)); then rm -rf "$staging_dir"; fi
	rm -rf "$tmp_dir"
}
trap cleanup_install EXIT
trap 'exit 1' HUP INT TERM
cp "$extract_dir/komposyn" "$stage" || fail 'could not stage executable on the destination filesystem'
chmod 0755 "$stage" || fail 'could not set executable permissions'

if [[ -e "$install_dir/komposyn" || -L "$install_dir/komposyn" ]]; then
	# Keep the existing install recoverable if the final rename fails.
	mv "$install_dir/komposyn" "$backup" || fail 'could not stage the existing executable for replacement'
fi
if ! mv "$stage" "$install_dir/komposyn"; then
	if [[ -e "$backup" ]]; then
		mv "$backup" "$install_dir/komposyn" || fail "could not restore the previous executable; it remains recoverable at $backup"
	fi
	fail 'could not install the verified executable; the previous executable was restored'
fi
rm -f "$backup"
rm -rf "$staging_dir"
echo "installed KompoSyn ${tag} (${os}/${arch}) at ${install_dir}/komposyn"
echo 'configuration files and other programs in the install directory were preserved'
