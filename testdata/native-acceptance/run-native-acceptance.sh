#!/usr/bin/env bash
# Copyright 2026 Ayesh Almeida
# SPDX-License-Identifier: Apache-2.0
set -Eeuo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
exec python3 "$script_dir/run-native-acceptance.py" "$@"
