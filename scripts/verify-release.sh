#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:?usage: verify-release.sh <release-dir> [verified-reference-binary]}"
reference_binary="${2:-}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(sed -n 's/^# version=//p' "$dist_dir/SHA256SUMS" | head -1)"
if [[ -z "$version" ]]; then
  echo "release checksum manifest does not declare a version" >&2
  exit 1
fi
(cd "$dist_dir" && sha256sum -c SHA256SUMS)

case "$(uname -s):$(uname -m)" in
  Linux:x86_64) asset="komposyn-lg_${version}_linux_amd64.tar.gz" ;;
  Linux:aarch64|Linux:arm64) asset="komposyn-lg_${version}_linux_arm64.tar.gz" ;;
  Darwin:x86_64) asset="komposyn-lg_${version}_darwin_amd64.tar.gz" ;;
  Darwin:arm64) asset="komposyn-lg_${version}_darwin_arm64.tar.gz" ;;
  *) echo "unsupported verification host: $(uname -s):$(uname -m)" >&2; exit 1 ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
tar -xzf "$dist_dir/$asset" -C "$tmp_dir"
for required in komposyn-lg LICENSE NOTICE README.md THIRD_PARTY_LICENSES.md third_party_licenses; do
  [[ -e "$tmp_dir/$required" ]] || { echo "archive missing $required" >&2; exit 1; }
done

install_dir="$tmp_dir/bin"
data_dir="$tmp_dir/data"
mkdir -p "$install_dir" "$data_dir"
source_dir="$tmp_dir/source"
mkdir -p "$source_dir"
cp "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
chmod 0755 "$install_dir/komposyn-lg"

# An optional released binary creates the database and settings for a real
# old-to-new migration. Protocol v1 is stable, so the fixture does not depend
# on private provider data or credentials.
if [[ -n "$reference_binary" ]]; then
  [[ -x "$reference_binary" ]] || { echo "reference binary is not executable: $reference_binary" >&2; exit 1; }
  cp "$reference_binary" "$install_dir/komposyn-lg"
  cat > "$source_dir/codex.jsonl" <<'EOF'
{"type":"event_msg","id":"issue-46-cursor-record","timestamp":"2026-10-11T12:00:00Z","payload":{"type":"token_count","session_id":"cursor-session","project_id":"cursor-project","model":"gpt-test","info":{"total_token_usage":{"input_tokens":100,"output_tokens":8}}}}
EOF
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" settings --collector off --auto-start off >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch add "$source_dir" >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" import "$source_dir/codex.jsonl" --format json >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch start --interval 1s >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch stop --timeout 10s >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" harness <<'EOF'
{"protocol":"komposyn.token-logger","version":1,"id":"init","method":"initialize","params":{}}
{"protocol":"komposyn.token-logger","version":1,"id":"record","method":"record","params":{"event":{"event_id":"issue-46-upgrade-fixture","schema_version":1,"event_type":"model.request","occurred_at":"2026-10-11T12:00:00Z","project_id":"issue-46-project","session_id":"issue-46-session","client":{"name":"issue-46-fixture"},"provider":"openai","requested_model":"gpt-test","usage":{"input_tokens":10,"output_tokens":5},"provenance":{"adapter":"fixture","source_id":"issue-46","source_record":"1"},"ingestion_key":"issue-46:upgrade-fixture"},"project":{"id":"issue-46-project","root":"/issue-46-fixture","display_name":"Issue 46 fixture"},"session":{"id":"issue-46-session","project_id":"issue-46-project","client":{"name":"issue-46-fixture"}}}}
{"protocol":"komposyn.token-logger","version":1,"id":"stop","method":"shutdown"}
EOF
  [[ -e "$data_dir/settings.json" && -e "$data_dir/token-logger.db" ]] || {
    echo "reference binary did not create settings and database" >&2
    exit 1
  }
else
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" settings --collector off --auto-start off >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" usage --format json >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch add "$source_dir" >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch start --interval 1s >/dev/null
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch status --format json | grep -Eq '"state":"(running|degraded)"' || {
    echo "background collector did not start during release verification" >&2
    exit 1
  }
fi
[[ -e "$data_dir/settings.json" ]] || { echo "settings were not created" >&2; exit 1; }
[[ -e "$data_dir/token-logger.db" ]] || { echo "database was not created" >&2; exit 1; }
settings_digest=""
if [[ -n "$reference_binary" ]]; then
  settings_digest="$(sha256sum "$data_dir/settings.json" | awk '{print $1}')"
fi

# Replacing the executable exercises a real released-binary upgrade when the
# optional fixture is supplied, and remains a basic lifecycle smoke test
# otherwise.
if [[ -z "$reference_binary" ]]; then
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch stop --timeout 10s >/dev/null
fi
cp "$tmp_dir/komposyn-lg" "$install_dir/komposyn-lg"
if [[ -n "$reference_binary" ]]; then
  upgraded_report="$(TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" usage --format json)"
  grep -Eq '"total_tokens"[[:space:]]*:[[:space:]]*123' <<<"$upgraded_report" || {
    echo "historical usage total was not preserved by upgrade: $upgraded_report" >&2
    exit 1
  }
  compgen -G "$data_dir/.komposyn-pre-migration-*.sqlite" >/dev/null || {
    echo "old-to-new upgrade did not retain a verified migration backup" >&2
    exit 1
  }
  [[ "$(sha256sum "$data_dir/settings.json" | awk '{print $1}')" == "$settings_digest" ]] || {
    echo "settings or consent values changed during upgrade" >&2
    exit 1
  }
  upgraded_sources="$(TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch list --format json)"
  [[ "$upgraded_sources" == *"$source_dir"* ]] || {
    echo "registered collector source was not preserved by upgrade" >&2
    exit 1
  }
  [[ "$upgraded_report" == *'"calculation_basis": "unavailable"'* ]] || {
    echo "unknown pricing evidence changed during upgrade" >&2
    exit 1
  }
  reimport_result="$(TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" import "$source_dir/codex.jsonl" --format json)"
  [[ "$reimport_result" == *'"imported_events":0'* && "$reimport_result" == *'"duplicate_events":0'* ]] || {
    echo "source cursor was not preserved for the unchanged fixture: $reimport_result" >&2
    exit 1
  }
  upgraded_projects="$(TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" projects list --format json)"
  [[ "$upgraded_projects" == *'issue-46-project'* ]] || {
    echo "project identity was not preserved by upgrade" >&2
    exit 1
  }
  upgraded_sessions="$(TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" sessions list --project issue-46-project --format json)"
  [[ "$upgraded_sessions" == *'issue-46-session'* ]] || {
    echo "session identity was not preserved by upgrade" >&2
    exit 1
  }
  upgraded_collector="$(TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch status --format json)"
  [[ "$upgraded_collector" != *'"state":"running"'* && "$upgraded_collector" != *'"state":"degraded"'* ]] || {
    echo "collector unexpectedly restarted after a stopped historical run" >&2
    exit 1
  }
  python3 - "$data_dir/token-logger.db" <<'PY'
import sqlite3
import sys

connection = sqlite3.connect(sys.argv[1])
try:
    row = connection.execute(
        "SELECT terminal_state FROM collector_runs ORDER BY started_at DESC LIMIT 1"
    ).fetchone()
finally:
    connection.close()
if row is None or row[0] != "stopped":
    raise SystemExit(f"collector run history was not preserved: {row!r}")
PY
  migration_backup="$(compgen -G "$data_dir/.komposyn-pre-migration-*.sqlite" | head -1)"
  recovery_dir="$tmp_dir/restored-data"
  mkdir -p "$recovery_dir"
  cp "$migration_backup" "$recovery_dir/token-logger.db"
  restored_report="$(TOKEN_LOGGER_DATA_DIR="$recovery_dir" "$install_dir/komposyn-lg" usage --format json)"
  grep -Eq '"total_tokens"[[:space:]]*:[[:space:]]*123' <<<"$restored_report" || {
    echo "restored pre-migration backup did not recover historical usage" >&2
    exit 1
  }
else
  TOKEN_LOGGER_DATA_DIR="$data_dir" "$install_dir/komposyn-lg" watch start >/dev/null
fi
[[ -e "$data_dir/settings.json" && -e "$data_dir/token-logger.db" ]] || {
  echo "upgrade did not preserve local data" >&2
  exit 1
}
if [[ -n "$reference_binary" && ! -f "$migration_backup" ]]; then
  echo "uninstall removed the migration backup" >&2
  exit 1
fi

TOKEN_LOGGER_DATA_DIR="$data_dir" KOMPOSYN_INSTALL_DIR="$install_dir" bash "$root_dir/scripts/uninstall.sh" >/dev/null
[[ ! -e "$install_dir/komposyn-lg" ]] || { echo "uninstall left executable behind" >&2; exit 1; }
[[ -e "$data_dir/settings.json" && -e "$data_dir/token-logger.db" ]] || {
  echo "uninstall removed local data" >&2
  exit 1
}
echo "release install, upgrade, and data-preserving uninstall checks passed"
