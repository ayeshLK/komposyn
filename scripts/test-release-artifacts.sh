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
	echo "usage: test-release-artifacts.sh --version VERSION --commit FULL_SHA --artifacts-dir DIR --goos OS --goarch ARCH" >&2
}
version=""
commit=""
artifacts_dir=""
goos=""
goarch=""
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python_bin="$(command -v python3 || command -v python || true)"
while (($#)); do
	case "$1" in
		--version|--commit|--artifacts-dir|--goos|--goarch)
			(($# >= 2)) || { usage; exit 2; }
			key="$1"
			value="$2"
			case "$key" in
				--version) version="$value" ;;
				--commit) commit="$value" ;;
				--artifacts-dir) artifacts_dir="$value" ;;
				--goos) goos="$value" ;;
				--goarch) goarch="$value" ;;
			esac
			shift 2
			;;
		*) usage; exit 2 ;;
		esac
done
if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev[0-9A-Za-z.-]*)?$ || ! "$commit" =~ ^[0-9a-f]{40}$ || -z "$artifacts_dir" || -z "$python_bin" ]]; then
	usage
	exit 2
fi
case "$goos/$goarch" in
	linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
	*) echo "unsupported release target: $goos/$goarch" >&2; exit 2 ;;
esac
asset="komposyn_${version}_${goos}_${goarch}"
if [[ "$goos" == windows ]]; then
	archive="$artifacts_dir/${asset}.zip"
else
	archive="$artifacts_dir/${asset}.tar.gz"
fi
[[ -f "$archive" && -f "$artifacts_dir/checksums.txt" ]] || {
	echo "archive or checksum manifest missing for $goos/$goarch" >&2
	exit 1
}
"$python_bin" - "$archive" "$artifacts_dir/checksums.txt" <<'PY'
import hashlib
import pathlib
import sys

archive = pathlib.Path(sys.argv[1])
manifest = pathlib.Path(sys.argv[2])
lines = manifest.read_text(encoding="ascii").splitlines()
if len(lines) != 1:
    raise SystemExit("expected one archive digest in this target's checksums.txt")
digest, name = lines[0].split(maxsplit=1)
if name.strip().lstrip("*") != archive.name:
    raise SystemExit(f"checksum manifest names unexpected asset: {name}")
actual = hashlib.sha256(archive.read_bytes()).hexdigest()
if digest != actual:
    raise SystemExit(f"checksum mismatch for {archive.name}")
PY
binary="komposyn"
if [[ "$goos" == windows ]]; then binary="komposyn.exe"; fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
"$python_bin" - "$goos" "$archive" "$tmp" <<'PY'
import pathlib
import sys
import tarfile
import zipfile

goos, archive_path, destination = sys.argv[1:]
archive = pathlib.Path(archive_path)
destination = pathlib.Path(destination)
binary = "komposyn.exe" if goos == "windows" else "komposyn"
expected = sorted([binary, "LICENSE", "NOTICE", "THIRD_PARTY_LICENSES.txt"])

if goos == "windows":
    with zipfile.ZipFile(archive) as package:
        entries = package.infolist()
        names = [entry.filename for entry in entries]
        if sorted(names) != expected or len(names) != len(set(names)):
            raise SystemExit(f"unexpected ZIP membership: {names}")
        if any("/" in name or "\\" in name for name in names):
            raise SystemExit("ZIP contains a nested path")
        for entry in entries:
            mode = entry.external_attr >> 16
            file_type = mode & 0o170000
            if file_type not in (0, 0o100000):
                raise SystemExit(f"ZIP member is not a regular file: {entry.filename}")
        package.extractall(destination)
else:
    with tarfile.open(archive, "r:gz") as package:
        entries = package.getmembers()
        names = [entry.name for entry in entries]
        if sorted(names) != expected or len(names) != len(set(names)):
            raise SystemExit(f"unexpected TAR membership: {names}")
        if any(not entry.isfile() for entry in entries):
            raise SystemExit("TAR contains a non-regular file")
        package.extractall(destination)
PY
[[ -s "$tmp/LICENSE" && -s "$tmp/NOTICE" && -s "$tmp/THIRD_PARTY_LICENSES.txt" ]] || {
	echo "archive legal materials must be non-empty" >&2
	exit 1
}
build_metadata="$(go version -m "$tmp/$binary")"
grep -Fq "GOOS=$goos" <<< "$build_metadata" || { echo "binary metadata does not identify GOOS=$goos" >&2; exit 1; }
grep -Fq "GOARCH=$goarch" <<< "$build_metadata" || { echo "binary metadata does not identify GOARCH=$goarch" >&2; exit 1; }
"$python_bin" - "$tmp/$binary" "$version" "$commit" <<'PY'
import pathlib
import sys

contents = pathlib.Path(sys.argv[1]).read_bytes()
for value in sys.argv[2:]:
    if value.encode() not in contents:
        raise SystemExit(f"binary does not contain build metadata {value}")
PY

host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
if [[ "$goos/$goarch" == "$host_os/$host_arch" ]]; then
	if [[ "$goos" != windows && ! -x "$tmp/$binary" ]]; then
		echo "Unix executable is not marked executable" >&2
		exit 1
	fi
	version_output="$("$tmp/$binary" --version)"
	expected="komposyn version=$version commit=$commit target=$goos/$goarch"
	[[ "$version_output" == "$expected" ]] || { echo "unexpected version output: $version_output" >&2; exit 1; }
	"$tmp/$binary" --help > "$tmp/help.txt"
	grep -q -- '--check' "$tmp/help.txt"
	"$tmp/$binary" --check --auth-file "$tmp/isolated-auth.json" --settings-file "$tmp/isolated-settings.json" > "$tmp/check.txt"
	grep -q '^UI: tview$' "$tmp/check.txt"
	[[ ! -e "$tmp/isolated-auth.json" && ! -e "$tmp/isolated-settings.json" ]] || {
		echo "read-only --check unexpectedly wrote configuration" >&2
		exit 1
	}
	if [[ "$goos" == windows ]]; then
		command -v cygpath >/dev/null 2>&1 && command -v pwsh >/dev/null 2>&1 || {
			echo 'cygpath and PowerShell are required for Windows ACL fixture validation' >&2
			exit 1
		}
		pwsh -NoProfile -File "$(cygpath -w "$root/scripts/test-harness-config-windows.ps1")" \
			-BinaryPath "$(cygpath -w "$tmp/$binary")"
	fi
	echo "native smoke checks passed for $goos/$goarch"
else
	echo "metadata and archive checks passed; native execution not run for $goos/$goarch on $host_os/$host_arch"
fi
