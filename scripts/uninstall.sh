#!/usr/bin/env bash
set -euo pipefail

install_dir="${KOMPOSYN_INSTALL_DIR:-${HOME}/.local/bin}"
binary="$install_dir/komposyn-lg"
if [[ -e "$binary" ]]; then
	if [[ -x "$binary" ]]; then
		capability="$("$binary" _watch-lifecycle 2>/dev/null || true)"
		if [[ "$capability" == 1 ]]; then
			status="$("$binary" watch status --format json)" || {
				echo 'unable to inspect the background collector; executable was not removed' >&2
				exit 1
			}
			if [[ "$status" == *'"state":"running"'* || "$status" == *'"state":"degraded"'* || "$status" == *'"state":"starting"'* || "$status" == *'"state":"stopping"'* ]]; then
				"$binary" watch stop --timeout 30s || {
					echo 'unable to stop the background collector; executable was not removed' >&2
					exit 1
				}
			fi
		fi
	fi
	rm "$binary"
	echo "removed $binary"
else
	echo "komposyn-lg is not installed at $binary"
fi
echo "local database, configuration, and consent settings were preserved"
