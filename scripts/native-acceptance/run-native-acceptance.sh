#!/usr/bin/env bash
# Copyright 2026 Ayesh Almeida
# SPDX-License-Identifier: Apache-2.0
set -Eeuo pipefail
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../.." && pwd -P)
exec "$repo_root/testdata/native-acceptance/run-native-acceptance.sh" "$@"
