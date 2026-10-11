#!/usr/bin/env bash
set -euo pipefail

output_dir="${1:?usage: generate-third-party-licenses.sh <output-dir>}"
license_dir="$output_dir/third_party_licenses"
manifest="$output_dir/THIRD_PARTY_LICENSES.md"
mkdir -p "$license_dir"

{
  echo "# Third-party licenses"
  echo
  echo "This bundle lists the resolved Go module dependencies included in the Komposyn Token Logger release."
  echo
} > "$manifest"

# `go list -m` can report module metadata without downloading the source tree
# for modules that are present only in the transitive module graph. Download
# the complete graph first so every module has a directory from which its
# license files can be collected in clean CI environments.
go mod download all

while read -r module version dir; do
  [[ -z "$module" ]] && continue
  [[ -n "$dir" && -d "$dir" ]] || {
    echo "module source directory unavailable for $module@$version" >&2
    exit 1
  }
  mapfile -t license_files < <(find "$dir" -maxdepth 1 -type f \( -iname 'license*' -o -iname 'copying*' -o -iname 'notice*' \) -exec basename {} \; | sort)
  if ((${#license_files[@]} == 0)); then
    echo "no license file found for $module@$version" >&2
    exit 1
  fi
  safe_module="${module//\//__}"
  module_dir="$license_dir/$safe_module"
  mkdir -p "$module_dir"
  {
    echo "## $module@$version"
    echo
    echo "License files:"
    for license_file in "${license_files[@]}"; do
      cp "$dir/$license_file" "$module_dir/$license_file"
      echo "- [${license_file}](third_party_licenses/${safe_module}/${license_file})"
    done
    echo
  } >> "$manifest"
done < <(go list -m -f '{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}' all | sort)
