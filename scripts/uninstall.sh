#!/usr/bin/env bash
set -euo pipefail

install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME}/.local/bin}"
binary="$install_dir/komposyn-lg"
if [[ -e "$binary" ]]; then
	rm "$binary"
	echo "removed $binary"
else
	echo "komposyn-lg is not installed at $binary"
fi
echo "local database, configuration, and consent settings were preserved"
