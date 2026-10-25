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
	echo "usage: build-release.sh --version X.Y.Z --commit FULL_SHA --goos OS --goarch ARCH --output-dir DIR" >&2
}

version=""
commit=""
goos=""
goarch=""
output_dir=""
while (($#)); do
	case "$1" in
		--version|--commit|--goos|--goarch|--output-dir)
			(($# >= 2)) || { usage; exit 2; }
			key="$1"
			value="$2"
			case "$key" in
				--version) version="$value" ;;
				--commit) commit="$value" ;;
				--goos) goos="$value" ;;
				--goarch) goarch="$value" ;;
				--output-dir) output_dir="$value" ;;
			esac
			shift 2
			;;
		*) usage; exit 2 ;;
	esac
done
if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev[0-9A-Za-z.-]*)?$ || ! "$commit" =~ ^[0-9a-f]{40}$ || -z "$output_dir" ]]; then
	usage
	exit 2
fi
case "$goos/$goarch" in
	linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
	*) echo "unsupported release target: $goos/$goarch" >&2; exit 2 ;;
esac
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
invocation_dir="$(pwd)"
if [[ "$output_dir" != /* ]]; then
	output_dir="$invocation_dir/$output_dir"
fi
if [[ "$output_dir" == "/" || -L "$output_dir" ]]; then
	echo "refusing unsafe release output directory: $output_dir" >&2
	exit 1
fi
mkdir -p "$output_dir"
if [[ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
	echo "release output directory must be empty: $output_dir" >&2
	exit 1
fi
actual_commit="$(git -C "$root_dir" rev-parse HEAD)"
if [[ "$actual_commit" != "$commit" ]]; then
	echo "checkout commit $actual_commit does not match requested commit $commit" >&2
	exit 1
fi
if [[ -n "$(git -C "$root_dir" status --porcelain)" ]]; then
	echo "refusing to package a dirty source tree" >&2
	exit 1
fi
python_bin="$(command -v python3 || command -v python || true)"
[[ -n "$python_bin" ]] || { echo "Python 3 is required to create deterministic archives and checksums" >&2; exit 1; }

output_dir="$(cd "$output_dir" && pwd)"
stage_root="$(mktemp -d)"
trap 'rm -rf "$stage_root"' EXIT

# Include complete license text for every resolved module dependency. Fail if
# a module has no discoverable license/notice instead of silently omitting it.
manifest="$stage_root/THIRD_PARTY_LICENSES.txt"
{
	echo "Third-party dependency license texts for KompoSyn $version."
	echo "Dependency versions are resolved by the release go.mod and go.sum."
	echo
} > "$manifest"
while IFS=$'\t' read -r module module_version module_dir; do
	[[ -n "$module" ]] || continue
	[[ -n "$module_dir" && -d "$module_dir" ]] || {
		echo "module source unavailable for $module@$module_version" >&2
		exit 1
	}
	license_found=false
	while IFS= read -r license_file; do
		{
			echo "======================================================================"
			echo "$module@$module_version — $(basename "$license_file")"
			echo "======================================================================"
			cat "$license_file"
			echo
		} >> "$manifest"
		license_found=true
	done < <(find "$module_dir" -maxdepth 1 -type f \( -iname 'license*' -o -iname 'copying*' -o -iname 'notice*' \) -print | LC_ALL=C sort)
	if [[ "$license_found" != true ]]; then
		echo "no license or notice file found for $module@$module_version" >&2
		exit 1
	fi
done < <(GOWORK=off GOFLAGS=-mod=readonly go list -deps -f '{{if and .Module (not .Module.Main)}}{{printf "%s\t%s\t%s" .Module.Path .Module.Version .Module.Dir}}{{end}}' ./cmd/harness | LC_ALL=C sort -u)

stage_dir="$stage_root/${goos}_${goarch}"
mkdir -p "$stage_dir"
asset="komposyn_${version}_${goos}_${goarch}"
if [[ "$goos" == windows ]]; then
	binary="$stage_dir/komposyn.exe"
	archive="$output_dir/${asset}.zip"
else
	binary="$stage_dir/komposyn"
	archive="$output_dir/${asset}.tar.gz"
fi
GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 GOWORK=off GOFLAGS=-mod=readonly go build \
	-trimpath -buildvcs=false \
	-ldflags "-s -w -buildid= -X main.cliVersion=$version -X main.cliCommit=$commit" \
	-o "$binary" "$root_dir/cmd/harness"
cp "$root_dir/LICENSE" "$root_dir/NOTICE" "$manifest" "$stage_dir/"
"$python_bin" - "$stage_dir" "$archive" "$goos" <<'PY'
import gzip
import os
import pathlib
import stat
import sys
import tarfile
import zipfile

stage = pathlib.Path(sys.argv[1])
archive = pathlib.Path(sys.argv[2])
goos = sys.argv[3]
binary = "komposyn.exe" if goos == "windows" else "komposyn"
names = [binary, "LICENSE", "NOTICE", "THIRD_PARTY_LICENSES.txt"]

if goos == "windows":
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_STORED) as output:
        for name in names:
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            mode = 0o755 if name == binary else 0o644
            info.external_attr = (stat.S_IFREG | mode) << 16
            info.compress_type = zipfile.ZIP_STORED
            output.writestr(info, (stage / name).read_bytes(), compress_type=zipfile.ZIP_STORED)
else:
    with archive.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0, compresslevel=9) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as output:
                for name in names:
                    path = stage / name
                    info = output.gettarinfo(str(path), arcname=name)
                    info.uid = info.gid = 0
                    info.uname = info.gname = "root"
                    info.mtime = 0
                    info.mode = 0o755 if name == binary else 0o644
                    with path.open("rb") as source:
                        output.addfile(info, source)
PY
"$python_bin" - "$archive" "$output_dir/checksums.txt" <<'PY'
import hashlib
import pathlib
import sys

archive = pathlib.Path(sys.argv[1])
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
pathlib.Path(sys.argv[2]).write_text(f"{digest}  {archive.name}\n", encoding="ascii")
PY
echo "Built $goos/$goarch archive for version $version at $commit in $output_dir"
