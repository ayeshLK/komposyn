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
# Uses a fixture verifier; these tests do not prove Cosign cryptography.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/komposyn-installer-test.XXXXXXXX")"
trap 'rm -rf "$tmp"' EXIT
installer="$root/scripts/install.sh"
uninstaller="$root/scripts/uninstall.sh"
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
	else shasum -a 256 "$1" | awk '{print $1}'; fi
}
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in linux|darwin) ;; *) echo "unsupported test OS $os" >&2; exit 1 ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported test architecture $arch" >&2; exit 1 ;; esac
if [[ "$os" == darwin ]] && command -v sysctl >/dev/null 2>&1 && [[ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" == 1 ]]; then arch=arm64; fi

fakebin="$tmp/fake bin"
mkdir -p "$fakebin"
cat >"$fakebin/cosign" <<'COSIGN'
#!/usr/bin/env bash
set -euo pipefail
[[ "${1:-}" == verify-blob ]] || exit 2
shift
bundle='' identity='' issuer='' subject=''
while (($#)); do
  case "$1" in
    --bundle) bundle="$2"; shift 2 ;;
    --certificate-identity-regexp) identity="$2"; shift 2 ;;
    --certificate-oidc-issuer) issuer="$2"; shift 2 ;;
    *) subject="$1"; shift ;;
  esac
done
[[ "$identity" == '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$' ]]
[[ "$issuer" == 'https://token.actions.githubusercontent.com' ]]
[[ -s "$bundle" && -s "$subject" ]]
grep -Fq 'IDENTITY=https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master' "$bundle"
! grep -Fq 'FAIL' "$bundle"
COSIGN
chmod 0755 "$fakebin/cosign"

make_fixture() {
  local destination="$1" version="${2:-1.2.3}" target_os="${3:-$os}" target_arch="${4:-$arch}"
  local archive="komposyn_${version}_${target_os}_${target_arch}.tar.gz"
  local payload="$tmp/payload-$RANDOM"
  mkdir -p "$destination" "$payload"
	cat >"$payload/komposyn" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == --version ]]; then
		echo "komposyn version=${version} commit=0123456789012345678901234567890123456789 target=${target_os}/${target_arch}"
fi
EOF
  chmod 0755 "$payload/komposyn"
  printf 'Apache License fixture\n' >"$payload/LICENSE"
  printf 'KompoSyn notice fixture\n' >"$payload/NOTICE"
  printf 'Dependency notices fixture\n' >"$payload/THIRD_PARTY_LICENSES.txt"
  tar -czf "$destination/$archive" -C "$payload" komposyn LICENSE NOTICE THIRD_PARTY_LICENSES.txt
	(cd "$destination" && { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$archive"; else shasum -a 256 "$archive"; fi; } >checksums.txt)
  printf 'IDENTITY=https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master\n' >"$destination/checksums.txt.sigstore.json"
  printf '%s\n' "$archive"
}

release="$tmp/release"
archive="$(make_fixture "$release")"
install="$tmp/A path with apostrophe's bin"
mkdir -p "$install"
printf 'old executable\n' >"$install/komposyn"
printf 'komposyn-lg fixture\n' >"$install/komposyn-lg"
home="$tmp/isolated home"
mkdir -p "$home/.komposyn"
chmod 0700 "$home/.komposyn"
printf '{"fixture":"auth"}\n' >"$home/.komposyn/auth.json"
printf '{"fixture":"settings"}\n' >"$home/.komposyn/settings.json"
chmod 0600 "$home/.komposyn/auth.json" "$home/.komposyn/settings.json"
auth_hash="$(sha256_of "$home/.komposyn/auth.json")"
settings_hash="$(sha256_of "$home/.komposyn/settings.json")"
export HOME="$home"

KOMPOSYN_COSIGN_BIN="$fakebin/cosign" "$installer" --version 1.2.3 --artifacts-dir "$release" --install-dir "$install"
"$install/komposyn" --version | grep -Fq "target=$os/$arch"
[[ "$(stat -c '%a' "$home/.komposyn/auth.json" 2>/dev/null || stat -f '%Lp' "$home/.komposyn/auth.json")" == 600 ]]
[[ "$(stat -c '%a' "$home/.komposyn/settings.json" 2>/dev/null || stat -f '%Lp' "$home/.komposyn/settings.json")" == 600 ]]
[[ "$(sha256_of "$home/.komposyn/auth.json")" == "$auth_hash" ]]
[[ "$(sha256_of "$home/.komposyn/settings.json")" == "$settings_hash" ]]
grep -Fq 'komposyn-lg fixture' "$install/komposyn-lg"
# Upgrade/reinstall replaces KompoSyn only.
KOMPOSYN_COSIGN_BIN="$fakebin/cosign" "$installer" --version 1.2.3 --artifacts-dir "$release" --install-dir "$install"
grep -Fq 'komposyn-lg fixture' "$install/komposyn-lg"

expect_failed_install() {
  local artifacts="$1" expected='old executable' dir="$tmp/preserve-$RANDOM"
  mkdir -p "$dir"
  printf '%s\n' "$expected" >"$dir/komposyn"
  if KOMPOSYN_COSIGN_BIN="$fakebin/cosign" "$installer" --version 1.2.3 --artifacts-dir "$artifacts" --install-dir "$dir" >/dev/null 2>&1; then
    echo "invalid fixture unexpectedly installed: $artifacts" >&2
    exit 1
  fi
  grep -Fxq "$expected" "$dir/komposyn"
}

expect_failed_install "$tmp/missing"

# Latest metadata is validated, then all asset requests use the pinned tag.
metadata="$tmp/release-metadata.json"
printf '{"draft":false,"prerelease":false,"tag_name":"v1.2.3"}\n' >"$metadata"
KOMPOSYN_COSIGN_BIN="$fakebin/cosign" KOMPOSYN_RELEASE_METADATA_URL="file://$metadata" \
	"$installer" --artifacts-dir "$release" --install-dir "$install"
malformed="$tmp/malformed-metadata.json"
printf '{not-json}\n' >"$malformed"
metadata_install="$tmp/metadata-preservation"
mkdir -p "$metadata_install"
printf 'old executable\n' >"$metadata_install/komposyn"
if KOMPOSYN_COSIGN_BIN="$fakebin/cosign" KOMPOSYN_RELEASE_METADATA_URL="file://$malformed" \
	"$installer" --artifacts-dir "$release" --install-dir "$metadata_install" >/dev/null 2>&1; then
	echo 'malformed release metadata was accepted' >&2
	exit 1
fi
grep -Fxq 'old executable' "$metadata_install/komposyn"

# Explicitly reject unknown platforms and architectures instead of falling
# through to an arbitrary asset name.
fakeuname="$tmp/fake-uname"
mkdir -p "$fakeuname"
cat >"$fakeuname/uname" <<'UNAME'
#!/usr/bin/env bash
case "${1:-}" in
	-s) echo "${FAKE_UNAME_S:-Linux}" ;;
	-m) echo "${FAKE_UNAME_M:-x86_64}" ;;
	*) exit 2 ;;
esac
UNAME
chmod 0755 "$fakeuname/uname"
for unsupported in 'UnsupportedOS|x86_64' 'Linux|mips64'; do
	fake_os="${unsupported%%|*}"
	fake_arch="${unsupported#*|}"
	preserve_dir="$tmp/unsupported-$fake_os-$fake_arch"
	mkdir -p "$preserve_dir"
	printf 'old executable\n' >"$preserve_dir/komposyn"
	if FAKE_UNAME_S="$fake_os" FAKE_UNAME_M="$fake_arch" PATH="$fakeuname:$PATH" \
		KOMPOSYN_COSIGN_BIN="$fakebin/cosign" "$installer" --version 1.2.3 --artifacts-dir "$release" --install-dir "$preserve_dir" >/dev/null 2>&1; then
		echo "unsupported target unexpectedly accepted: $fake_os/$fake_arch" >&2
		exit 1
	fi
	grep -Fxq 'old executable' "$preserve_dir/komposyn"
done

wrong="$tmp/wrong-signer"
mkdir -p "$wrong"
cp "$release/$archive" "$wrong/$archive"
cp "$release/checksums.txt" "$wrong/checksums.txt"
printf 'IDENTITY=https://example.invalid/other\n' >"$wrong/checksums.txt.sigstore.json"
expect_failed_install "$wrong"
bad_signature="$tmp/bad-signature"
mkdir -p "$bad_signature"
cp "$release/$archive" "$bad_signature/$archive"
cp "$release/checksums.txt" "$bad_signature/checksums.txt"
printf 'FAIL\n' >"$bad_signature/checksums.txt.sigstore.json"
expect_failed_install "$bad_signature"
bad_hash="$tmp/bad-hash"
mkdir -p "$bad_hash"
cp "$release/$archive" "$bad_hash/$archive"
printf '%064d  %s\n' 0 "$archive" >"$bad_hash/checksums.txt"
cp "$release/checksums.txt.sigstore.json" "$bad_hash/checksums.txt.sigstore.json"
expect_failed_install "$bad_hash"
wrong_target="$tmp/wrong-target"
wrong_target_archive="$(make_fixture "$wrong_target" 1.2.3 "$os" otherarch)"
expect_failed_install "$wrong_target"

bad_members="$tmp/bad-members"
payload="$tmp/payload-extra"
mkdir -p "$bad_members" "$payload"
cp "$release/$archive" "$tmp/unused-archive"
cat >"$payload/komposyn" <<EOF
#!/usr/bin/env bash
echo "komposyn version=1.2.3 commit=0123456789012345678901234567890123456789 target=$os/$arch"
EOF
chmod 0755 "$payload/komposyn"
printf x >"$payload/LICENSE"
printf x >"$payload/NOTICE"
printf x >"$payload/THIRD_PARTY_LICENSES.txt"
printf x >"$payload/extra"
tar -czf "$bad_members/$archive" -C "$payload" komposyn LICENSE NOTICE THIRD_PARTY_LICENSES.txt extra
(cd "$bad_members" && { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$archive"; else shasum -a 256 "$archive"; fi; } >checksums.txt)
cp "$release/checksums.txt.sigstore.json" "$bad_members/checksums.txt.sigstore.json"
expect_failed_install "$bad_members"

KOMPOSYN_INSTALL_DIR="$install" "$uninstaller"
[[ ! -e "$install/komposyn" && -e "$install/komposyn-lg" ]]
KOMPOSYN_INSTALL_DIR="$install" "$uninstaller"
[[ "$(sha256_of "$home/.komposyn/auth.json")" == "$auth_hash" ]]
[[ "$(sha256_of "$home/.komposyn/settings.json")" == "$settings_hash" ]]
echo 'Unix installer fixture tests passed (Cosign verification is mocked).'
