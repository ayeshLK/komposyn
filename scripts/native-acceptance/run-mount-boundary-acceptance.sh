#!/usr/bin/env bash
# Copyright 2026 Ayesh Almeida
# SPDX-License-Identifier: Apache-2.0
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage: run-mount-boundary-acceptance.sh --repo DIR --revision SHA --scratch-dir DIR [--bwrap PATH]

Creates a private disposable scope beneath an existing ext4 scratch directory,
bind-mounts an empty source directory beneath it, runs the native acceptance
suite as the current user, then unmounts only the bind mount created here.
The runner's results archive remains in --scratch-dir.

This requires sudo permission for `mount --bind` and `umount`. The script never
runs the native tests as root and never changes ownership, permissions, or
system mount configuration. If unmount fails, it preserves and reports the
fixture path rather than deleting anything beneath a mounted directory.
EOF
}

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

repo=''
revision=''
scratch=''
bwrap='/usr/bin/bwrap'
while (($#)); do
  case "$1" in
    --repo) (($# >= 2)) || die 'missing value for --repo'; repo=$2; shift 2 ;;
    --revision) (($# >= 2)) || die 'missing value for --revision'; revision=$2; shift 2 ;;
    --scratch-dir) (($# >= 2)) || die 'missing value for --scratch-dir'; scratch=$2; shift 2 ;;
    --bwrap) (($# >= 2)) || die 'missing value for --bwrap'; bwrap=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
  esac
done

[[ -n "$repo" && -n "$revision" && -n "$scratch" ]] || { usage >&2; exit 2; }
[[ $EUID -ne 0 ]] || die 'run as your regular user, not root or sudo'
[[ -t 0 ]] || die 'run from an interactive terminal so the mount confirmation can be read'
[[ $revision =~ ^[[:xdigit:]]{40}$ ]] || die '--revision must be a full 40-character commit hash'
command -v findmnt >/dev/null || die 'findmnt is required (install util-linux)'
command -v sudo >/dev/null || die 'sudo is required for the temporary bind mount'
[[ -x "$bwrap" ]] || die "Bubblewrap is not executable: $bwrap"
[[ "$bwrap" == /* ]] || die '--bwrap must be an absolute path'

repo=$(realpath -e -- "$repo") || die '--repo must exist'
scratch=$(realpath -e -- "$scratch") || die '--scratch-dir must exist'
[[ -d "$repo" && -d "$scratch" ]] || die '--repo and --scratch-dir must be directories'
[[ -w "$scratch" && -x "$scratch" ]] || die '--scratch-dir must be writable and searchable'
[[ $(findmnt -n -o FSTYPE -T "$scratch") == ext4 ]] || die '--scratch-dir must be on ext4'
[[ -f "$repo/testdata/native-acceptance/run-native-acceptance.sh" ]] || die 'native runner not found under --repo'
[[ -f "$repo/.git" || -d "$repo/.git" ]] || die '--repo must be a Git checkout'

fixture=$(mktemp -d -- "$scratch/komposyn-mount-boundary.XXXXXXXX") || die 'could not create fixture under scratch'
chmod 700 -- "$fixture"
source_dir="$fixture/source"
scope="$fixture/scope"
nested="$scope/nested-mount"
mkdir -m 700 -- "$source_dir" "$scope" "$nested"

mounted=0
created_mount_id=''
preserve=0
cleanup() {
  local exact_id=''
  if ((mounted)); then
    exact_id=$(findmnt -rn -M "$nested" -o ID 2>/dev/null || true)
    if [[ -n "$exact_id" ]]; then
      if [[ "$exact_id" != "$created_mount_id" ]]; then
        printf 'cleanup refused: mount at %s changed (expected ID %s, found %s); fixture preserved at %s\n' \
          "$nested" "$created_mount_id" "$exact_id" "$fixture" >&2
        preserve=1
      elif ! sudo umount -- "$nested"; then
        printf 'cleanup failed: could not unmount %s; fixture preserved at %s\n' "$nested" "$fixture" >&2
        preserve=1
      fi
    fi
    if [[ -n "$(findmnt -rn -M "$nested" -o ID 2>/dev/null || true)" ]]; then
      printf 'cleanup failed: mount remains at %s; fixture preserved at %s\n' "$nested" "$fixture" >&2
      preserve=1
    fi
  fi
  if ((!preserve)); then
    rmdir -- "$nested" "$scope" "$source_dir" "$fixture" 2>/dev/null || {
      printf 'fixture directories were not empty; preserving them at %s\n' "$fixture" >&2
      preserve=1
    }
  fi
  return 0
}
on_exit() {
  local exit_status=$?
  trap - EXIT
  cleanup
  if ((preserve)); then exit_status=1; fi
  exit "$exit_status"
}
trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Fixture: %s\nScratch filesystem: ext4\n' "$fixture"
printf 'The script will bind-mount this empty directory:\n  %s\nonto:\n  %s\n' "$source_dir" "$nested"
printf 'It will run tests as UID %s, then unmount only %s.\n' "$EUID" "$nested"
read -r -p 'Proceed with this temporary mount and native acceptance run? [y/N] ' answer
[[ $answer == [yY] || $answer == [yY][eE][sS] ]] || die 'cancelled before mounting'

sudo -v
[[ -z "$(findmnt -rn -M "$nested" -o ID 2>/dev/null || true)" ]] || die 'fixture target is unexpectedly already a mount point'
sudo mount --bind "$source_dir" "$nested"
mounted=1

nested_id=$(findmnt -n -M "$nested" -o ID) || die 'bind mount was not visible after creation'
created_mount_id=$(findmnt -n -M "$nested" -o ID) || die 'bind mount was not visible after creation'
scope_id=$(findmnt -n -o ID -T "$scope") || die 'could not inspect scope mount ID'
[[ $scope_id != "$created_mount_id" ]] || die 'nested bind mount did not receive a distinct mount ID'
[[ $(stat -c '%u:%a' -- "$scope") == "$EUID:700" ]] || die 'scope is not private and owned by the current user'
printf 'Verified scope mount ID %s; nested mount ID %s.\n' "$scope_id" "$created_mount_id"
printf 'Running the native runner as UID %s (not sudo).\n' "$EUID"

"$repo/scripts/native-acceptance/run-native-acceptance.sh" \
  --repo "$repo" --revision "$revision" --bwrap "$bwrap" \
  --scratch-dir "$scratch" --mount-scope "$scope"
