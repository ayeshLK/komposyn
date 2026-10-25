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

install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME:-}/.local/bin}"
while (($#)); do
	case "$1" in
		--install-dir)
			(($# >= 2)) || { echo '--install-dir requires a value' >&2; exit 2; }
			install_dir="$2"
			shift 2
			;;
		-h|--help)
			echo 'Usage: uninstall.sh [--install-dir PATH]'
			exit 0
			;;
		*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

binary="$install_dir/komposyn"
if [[ -e "$binary" || -L "$binary" ]]; then
	[[ ! -d "$binary" ]] || { echo "refusing to remove directory: $binary" >&2; exit 1; }
	rm "$binary"
	echo "removed $binary"
else
	echo "komposyn is not installed at $binary"
fi
echo 'configuration files, komposyn-lg, and Token Logger data were preserved'
