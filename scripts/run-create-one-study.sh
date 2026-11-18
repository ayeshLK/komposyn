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

set -Eeuo pipefail
umask 077

if [[ $(uname -s) != Linux ]]; then
  printf 'This native study requires Linux.\n' >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
cd "$repo"
if [[ -n $(git status --porcelain) ]]; then
  printf 'Checkout must be clean; refusing to study modified source.\n' >&2
  exit 2
fi
for dependency in go sha256sum tar mktemp id uname; do
  command -v "$dependency" >/dev/null 2>&1 || {
    printf 'Required command is unavailable: %s\n' "$dependency" >&2
    exit 2
  }
done

commit=$(git rev-parse HEAD)
out=$(mktemp -d "${TMPDIR:-/tmp}/komposyn-issue-319-native.XXXXXX")
build_dir=$(mktemp -d "$repo/.creation-study.XXXXXX")
cleanup() { rm -rf -- "$build_dir"; }
trap cleanup EXIT
report=$out/report.json
binary=$build_dir/sandbox-study
bwrap_path=${KOMPOSYN_STUDY_BWRAP:-/usr/bin/bwrap}

printf 'Source commit: %s\n' "$commit"
printf 'User: %s\n' "$(id)"
printf 'Go: %s\n' "$(go version)"
printf 'Kernel: %s\n' "$(uname -srmo)"

GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/komposyn-issue-319-gocache}" CGO_ENABLED=0 \
  go build -buildvcs=false -trimpath -o "$binary" ./cmd/sandbox-study

set +e
"$binary" --bwrap "$bwrap_path" --commit "$commit" >"$report"
run_status=$?
set -e

go_version=$(go version)
kernel=$(uname -srmo)
binary_sha=$(sha256sum "$binary" | awk '{print $1}')
report_sha=$(sha256sum "$report" | awk '{print $1}')
cat >"$out/manifest.txt" <<EOF
source_commit=$commit
runner_exit=$run_status
go_version=$go_version
kernel=$kernel
user=$(id)
study_binary_sha256=$binary_sha
report_sha256=$report_sha
EOF

archive=$out.tar.gz
tar -czf "$archive" -C "$out" report.json manifest.txt
printf 'Report: %s\n' "$report"
printf 'Evidence archive: %s\n' "$archive"
sha256sum "$archive"
printf 'Runner exit: %s (3 means native prerequisites were unavailable; inspect report.json)\n' "$run_status"
exit "$run_status"
