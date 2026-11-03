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

readonly BASELINE=5fb44dc7e0c161d6010628eb4017d5c88b33e097
readonly DEFAULT_BWRAP=/usr/bin/bwrap
readonly LOG_LIMIT=$((2 * 1024 * 1024))
readonly BUILD_TIMEOUT=300
readonly STUDY_TIMEOUT=180

usage() {
  cat <<'EOF'
Usage: scripts/run-read-broker-study.sh [--bwrap ABSOLUTE_PATH] [--revision COMMIT] [--output-dir NEW_DIRECTORY]

Runs the committed Linux read-broker feasibility study from an isolated Git
archive. It never edits or cleans the caller's checkout. Results are retained
in a private directory and a .tar.gz archive beside it.
EOF
}

bwrap=$DEFAULT_BWRAP
revision=$BASELINE
output_dir=
while (($#)); do
  case "$1" in
    --bwrap)
      (($# >= 2)) || { usage >&2; exit 2; }
      bwrap=$2; shift 2 ;;
    --revision)
      (($# >= 2)) || { usage >&2; exit 2; }
      revision=$2; shift 2 ;;
    --output-dir)
      (($# >= 2)) || { usage >&2; exit 2; }
      output_dir=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

for dependency in git python3 tar sha256sum stat date mktemp chmod id uname env awk tr head find sort xargs; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Required runner dependency is unavailable: %s\n' "$dependency" >&2
    exit 2
  fi
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)
if [[ -z $repo ]]; then
  printf 'Cannot locate the Git repository containing this runner.\n' >&2
  exit 2
fi

if [[ -z $output_dir ]]; then
  output_dir=$(mktemp -d "${TMPDIR:-/tmp}/komposyn-read-broker-results.XXXXXX")
else
  [[ $output_dir = /* ]] || { printf 'Output directory must be absolute.\n' >&2; exit 2; }
  [[ ! -e $output_dir && ! -L $output_dir ]] || { printf 'Output path already exists; refusing to overwrite it.\n' >&2; exit 2; }
  [[ ! -e ${output_dir}.tar.gz && ! -L ${output_dir}.tar.gz ]] || { printf 'Archive path already exists; refusing to overwrite it.\n' >&2; exit 2; }
  mkdir -m 700 -- "$output_dir"
fi
chmod 700 -- "$output_dir"
output_dir=$(cd -- "$output_dir" && pwd -P)
archive=${output_dir}.tar.gz
if [[ -e $archive || -L $archive ]]; then
  printf 'Archive path already exists; refusing to overwrite it.\n' >&2
  exit 2
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/komposyn-read-broker-work.XXXXXX")
source_dir=$work/source
build_dir=$work/build
mkdir -m 700 -- "$source_dir" "$build_dir"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

runner_start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
runner_hash=$(sha256sum -- "${BASH_SOURCE[0]}" | awk '{print $1}')
source_commit=
source_status=not_started
go_version=
go_bin=
go_status=not_checked
build_status=not_started
build_exit_code=
run_status=not_started
run_exit_code=
study_json_status=not_produced
backend_lstat_status=unavailable
backend_owner=
backend_mode=
backend_sha256=
executable_sha256=
build_seconds=
run_seconds=
build_timed_out=false
run_timed_out=false
preflight_status=not_run
study_availability=unknown
study_backend_trusted=unknown
failure=

capture_helper=$work/capture.py
cat >"$capture_helper" <<'PY'
import json, os, selectors, signal, subprocess, sys, time

timeout = float(sys.argv[1])
limit = int(sys.argv[2])
stdout_path, stderr_path, cwd = sys.argv[3], sys.argv[4], sys.argv[5]
argv = sys.argv[6:]
if argv[:1] == ["--"]: argv = argv[1:]
started = time.monotonic()
result = {"exit_code": None, "timed_out": False, "duration_seconds": 0.0,
          "stdout_bytes": 0, "stderr_bytes": 0, "stdout_truncated": False,
          "stderr_truncated": False, "termination": "none"}
try:
    child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, start_new_session=True,
                             close_fds=True, cwd=cwd)
except Exception as exc:
    result["launch_error"] = type(exc).__name__
else:
    selector = selectors.DefaultSelector()
    files = {"stdout": open(stdout_path, "wb"), "stderr": open(stderr_path, "wb")}
    counts = {"stdout": 0, "stderr": 0}
    streams = {child.stdout: "stdout", child.stderr: "stderr"}
    for stream, name in streams.items():
        os.set_blocking(stream.fileno(), False)
        selector.register(stream, selectors.EVENT_READ, name)
    deadline = started + timeout
    term_deadline = None
    interrupt_signal = None
    def handle_signal(signum, _frame):
        global _runner_signal
        _runner_signal = signum
        try: os.killpg(child.pid, signal.SIGTERM)
        except ProcessLookupError: pass
    _runner_signal = None
    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)
    while selector.get_map() or child.poll() is None:
        now = time.monotonic()
        if _runner_signal is not None and interrupt_signal is None:
            interrupt_signal = _runner_signal
            result["interrupted_by_signal"] = interrupt_signal
            result["termination"] = "received signal; SIGTERM process group, then SIGKILL after 3 seconds"
            term_deadline = now + 3.0
        if now >= deadline and not result["timed_out"] and interrupt_signal is None:
            result["timed_out"] = True
            result["termination"] = "SIGTERM process group, then SIGKILL after 3 seconds"
            try: os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError: pass
            term_deadline = now + 3.0
        if term_deadline is not None and now >= term_deadline:
            try: os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError: pass
            term_deadline = None
        events = selector.select(0.1)
        for key, _ in events:
            stream, name = key.fileobj, key.data
            try: data = os.read(stream.fileno(), 65536)
            except BlockingIOError: continue
            if not data:
                selector.unregister(stream); stream.close(); continue
            remaining = max(0, limit - counts[name])
            if remaining:
                files[name].write(data[:remaining])
                counts[name] += min(len(data), remaining)
            if len(data) > remaining:
                result[name + "_truncated"] = True
    result["exit_code"] = child.wait()
    for f in files.values():
        f.flush(); os.fsync(f.fileno()); f.close()
    result["stdout_bytes"] = counts["stdout"]
    result["stderr_bytes"] = counts["stderr"]
result["duration_seconds"] = round(time.monotonic() - started, 3)
json.dump(result, sys.stdout, sort_keys=True)
sys.stdout.write("\n")
PY

run_bounded() {
  local name=$1 timeout=$2 cwd=$3; shift 3
  python3 "$capture_helper" "$timeout" "$LOG_LIMIT" \
    "$output_dir/$name.stdout.log" "$output_dir/$name.stderr.log" "$cwd" -- "$@" \
    >"$output_dir/$name.capture.json"
}

read_capture() {
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    d=json.load(open(sys.argv[1], encoding="utf-8"))
    print(d.get(sys.argv[2], ""))
except Exception:
    print("")
PY
}

if [[ $(id -u) == 0 ]]; then
  failure='runner_refused_root'
  source_status=not_attempted
elif [[ $(uname -s) != Linux ]]; then
  failure='unsupported_host_os'
  source_status=not_attempted
elif [[ $(uname -m) != x86_64 && $(uname -m) != aarch64 ]]; then
  failure='unsupported_host_architecture'
  source_status=not_attempted
elif [[ -z ${HOME:-} ]]; then
  failure='home_environment_unavailable'
  source_status=not_attempted
elif [[ $bwrap != /* ]]; then
  failure='bubblewrap_path_not_absolute'
  source_status=not_attempted
else
  if ! git -C "$repo" cat-file -e "$revision^{commit}" 2>/dev/null; then
    failure='source_revision_not_available'
    source_status=not_available
  elif ! git -C "$repo" merge-base --is-ancestor "$BASELINE" "$revision" 2>/dev/null; then
    failure='source_revision_does_not_include_reviewed_baseline'
    source_status=refused
  else
    source_commit=$(git -C "$repo" rev-parse "$revision^{commit}")
    if git -C "$repo" archive --format=tar "$source_commit" | tar -xf - -C "$source_dir"; then
      source_status=clean_git_archive
    else
      failure='source_archive_failed'
      source_status=failed
    fi
  fi
fi

if [[ $source_status == clean_git_archive ]]; then
  go_bin=$(command -v go || true)
  if [[ -z $go_bin ]]; then
    failure=${failure:-go_not_found}
    go_status=unavailable
  else
    run_bounded go-version 10 "$source_dir" env -i HOME="${HOME:?HOME is required}" PATH="$PATH" GOENV=off GOTOOLCHAIN=auto "$go_bin" version
    go_code=$(read_capture "$output_dir/go-version.capture.json" exit_code)
    go_version=$(tr -d '\r\n' <"$output_dir/go-version.stdout.log" | head -c 128)
    if [[ $go_code != 0 ]] || ! python3 - "$go_version" <<'PY'
import re, sys
m=re.search(r'go([0-9]+)\.([0-9]+)(?:\.([0-9]+))?', sys.argv[1])
sys.exit(0 if m and tuple(map(int, (m.group(1),m.group(2),m.group(3) or 0))) >= (1,26,10) else 1)
PY
    then
      failure=${failure:-required_go_version_unavailable}
      go_status=refused
    else
      go_status=passed
      build_start=$(date +%s)
      if run_bounded build "$BUILD_TIMEOUT" "$source_dir" env -i HOME="${HOME:?HOME is required}" PATH="$PATH" GOENV=off GOTOOLCHAIN=auto CGO_ENABLED=0 GOCACHE="$build_dir/go-build-cache" "$go_bin" build -buildvcs=false -o "$build_dir/read-broker-study" ./cmd/read-broker-study; then
        build_code=$(read_capture "$output_dir/build.capture.json" exit_code)
        build_exit_code=$build_code
        build_timed_out=$(read_capture "$output_dir/build.capture.json" timed_out)
        build_seconds=$(read_capture "$output_dir/build.capture.json" duration_seconds)
        if [[ $build_timed_out == true ]]; then
          build_status=timed_out
          failure=${failure:-build_timeout}
        elif [[ $build_code == 0 && -x $build_dir/read-broker-study ]]; then
          build_status=passed
          executable_sha256=$(sha256sum -- "$build_dir/read-broker-study" | awk '{print $1}')
          run_start=$(date +%s)
          if run_bounded study "$STUDY_TIMEOUT" "$source_dir" env -i PATH="$PATH" TMPDIR="${TMPDIR:-/tmp}" "$build_dir/read-broker-study" --commit "$source_commit" --bwrap "$bwrap"; then
            run_code=$(read_capture "$output_dir/study.capture.json" exit_code)
            run_exit_code=$run_code
            run_timed_out=$(read_capture "$output_dir/study.capture.json" timed_out)
            run_seconds=$(read_capture "$output_dir/study.capture.json" duration_seconds)
            if [[ $run_timed_out == true ]]; then run_status=timed_out
            elif [[ $run_code == 0 ]]; then run_status=completed
            else run_status=failed; failure=${failure:-study_exit_nonzero}; fi
            if python3 - "$output_dir/study.stdout.log" "$output_dir/study-report.json" "$source_commit" "$bwrap" <<'PY'
import json,sys
try:
    with open(sys.argv[1], "rb") as f: data=f.read(2*1024*1024+1)
    if len(data)>2*1024*1024: raise ValueError("truncated")
    obj=json.loads(data)
    env=obj.get("environment")
    cases=obj.get("cases")
    if (obj.get("schema") != "komposyn.read-broker-study.v1" or obj.get("commit") != sys.argv[3]
        or obj.get("availability") not in ("available","unavailable") or not isinstance(env,dict)
        or env.get("bubblewrap_path") != sys.argv[4] or not isinstance(obj.get("launch_policy"),dict)
        or not isinstance(cases,list) or not all(isinstance(c,dict) and isinstance(c.get("status"),str) for c in cases)):
        raise ValueError("schema or provenance")
    with open(sys.argv[2],"wb") as f: f.write(data)
except Exception:
    sys.exit(1)
PY
            then
              study_json_status=valid
              study_availability=$(python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
print(json.load(open(sys.argv[1],encoding="utf-8")).get("availability","unknown"))
PY
)
              study_backend_trusted=$(python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
print(str(json.load(open(sys.argv[1],encoding="utf-8")).get("environment",{}).get("bubblewrap_trusted","unknown")).lower())
PY
)
              if [[ $study_availability == available ]]; then preflight_status=trusted_and_probed
              else preflight_status=refused_before_backend_probe_or_launch; fi
            else
              if [[ ! -s $output_dir/study.stdout.log ]]; then
                study_json_status=missing
                failure=${failure:-study_report_missing}
              else
                study_json_status=malformed
                failure=${failure:-study_report_malformed}
              fi
            fi
          else
            run_status=runner_error
            failure=${failure:-study_runner_failed}
          fi
        else
          build_status=failed
          failure=${failure:-build_failed}
        fi
      else
        build_status=runner_error
        failure=${failure:-build_runner_failed}
      fi
    fi
  fi
fi

# Collect safe backend metadata without executing or probing the candidate.
if [[ -e $bwrap || -L $bwrap ]]; then
  backend_lstat_status=present_metadata_only
  read -r backend_owner backend_mode < <(stat -c '%u %a' -- "$bwrap" 2>/dev/null || true)
fi
if [[ -f $output_dir/study-report.json ]]; then
  backend_sha256=$(python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
print(json.load(open(sys.argv[1],encoding="utf-8")).get("environment",{}).get("bubblewrap_sha256",""))
PY
)
fi

manifest_path=$output_dir/manifest.json
python3 - "$manifest_path" "$runner_start" "$runner_hash" "$BASELINE" "$source_commit" "$source_status" \
  "$bwrap" "$backend_lstat_status" "$backend_owner" "$backend_mode" "$backend_sha256" \
  "$go_version" "$go_bin" "$go_status" "$build_status" "$build_seconds" "$build_timed_out" \
  "$build_exit_code" "$executable_sha256" "$run_status" "$run_exit_code" "$run_seconds" "$run_timed_out" "$study_json_status" \
  "$study_availability" "$study_backend_trusted" "$preflight_status" "$failure" "$output_dir/study-report.json" <<'PY'
import json,os,platform,sys,time
import hashlib,stat
(path,started,runner_hash,baseline,commit,source_status,bwrap,bwrap_stat,bwrap_uid,bwrap_mode,bwrap_sha,
 go_version,go_bin,go_status,build_status,build_seconds,build_timeout,build_exit,exe_sha,run_status,run_exit,run_seconds,run_timeout,
 report_status,availability,trusted,preflight,failure,report_path)=sys.argv[1:]
def optional_number(v):
    try:return float(v)
    except:return None
report={}
try:
    with open(report_path,encoding="utf-8") as f: report=json.load(f)
except Exception: pass
observed=report.get("environment",{})
metadata_sha=None
try:
    if os.path.isabs(bwrap):
        fd=os.open(bwrap,os.O_RDONLY|os.O_CLOEXEC|os.O_NOFOLLOW|os.O_NONBLOCK)
        try:
            info=os.fstat(fd)
            if stat.S_ISREG(info.st_mode) and 0 <= info.st_size <= 512*1024*1024:
                digest=hashlib.sha256(); total=0
                while True:
                    chunk=os.read(fd,1024*1024)
                    if not chunk: break
                    total+=len(chunk)
                    if total>512*1024*1024: raise ValueError("backend exceeds hash bound")
                    digest.update(chunk)
                metadata_sha=digest.hexdigest()
        finally: os.close(fd)
except Exception: pass
data={
 "schema":"komposyn.read-broker-runner-manifest.v1",
 "started_utc":started,"finished_utc":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime()),
 "runner":{"sha256":runner_hash,"path":"scripts/run-read-broker-study.sh"},
 "source":{"repository":"github.com/ayeshLK/komposyn","baseline_commit":baseline,
          "selected_commit":commit or None,"status":source_status,
          "method":"git archive of selected immutable commit; caller worktree changes are excluded",
          "caller_worktree":"not inspected or used"},
 "host":{"os":platform.system(),"architecture":platform.machine(),"kernel":platform.release(),
         "uid":os.getuid()},
 "bubblewrap":{"path":bwrap,"metadata_only_status":bwrap_stat,"uid":bwrap_uid or None,
               "mode_octal":bwrap_mode or None,"sha256_metadata_only_bounded":metadata_sha,
               "sha256_from_authoritative_study_report":bwrap_sha or None,
               "trusted":trusted,"authoritative_inspection":{
                 k:observed.get(k) for k in ("bubblewrap_trusted","bubblewrap_version","bubblewrap_capabilities",
                   "bubblewrap_bind_mode","bubblewrap_error") if k in observed}},
 "study_environment":{k:observed.get(k) for k in (
   "os","architecture","kernel","go_version","uid","user_namespace_clone_sysctl",
   "namespace_functional_probe_status","seccomp_action_available","cgroup_v2","cgroup_controllers",
   "cgroup_delegation_status","apparmor_profile_status","host_restrictions") if k in observed},
 "effective_launch_policy":report.get("launch_policy") or None,
 "go":{"version_command":go_version or None,"binary":go_bin or None,"status":go_status},
 "build":{"command":"env -i HOME=<user-home> PATH=<inherited-path> GOENV=off GOTOOLCHAIN=auto CGO_ENABLED=0 GOCACHE=<private-temp>/go-build-cache <go-binary> build -buildvcs=false -o <private-temp>/read-broker-study ./cmd/read-broker-study",
          "status":build_status,"exit_code":int(build_exit) if build_exit.isdigit() else None,
          "duration_seconds":optional_number(build_seconds),"timed_out":build_timeout=="true",
          "executable_sha256":exe_sha or None},
 "study":{"command":"env -i PATH=<inherited-path> TMPDIR=<temporary-directory> <study-executable> --commit <selected-commit> --bwrap <absolute-backend-path>",
          "status":run_status,"exit_code":int(run_exit) if run_exit.isdigit() else None,
          "duration_seconds":optional_number(run_seconds),"timed_out":run_timeout=="true",
          "report_status":report_status,"availability":availability,"authoritative_preflight":preflight},
 "classification":{"runner_failure":failure or None,
   "enforcement_acceptance":"not established by this runner; inspect per-case evidence and documented native gaps"},
 "capture_limits":{"stdout_bytes_per_command":2097152,"stderr_bytes_per_command":2097152,
                   "build_timeout_seconds":300,"study_timeout_seconds":180,
                   "timeout_action":"SIGTERM process group; after 3 seconds SIGKILL; drain pipes and wait for direct child"},
 "subprocess_environment":{"go_version_and_build":"clean environment: HOME, PATH, GOENV=off, GOTOOLCHAIN=auto; build also sets CGO_ENABLED=0 and private GOCACHE",
                           "study":"clean environment: PATH and TMPDIR only; study controls the worker environment"}
}
with open(path,"w",encoding="utf-8") as f: json.dump(data,f,indent=2,sort_keys=True); f.write("\n")
PY

summary=$output_dir/summary.md
{
  printf '# Read-broker native validation\n\n'
  printf -- '- Source commit: `%s` (%s)\n' "${source_commit:-unavailable}" "$source_status"
  printf -- '- Bubblewrap: `%s`; trust: `%s`; preflight: `%s`\n' "$bwrap" "$study_backend_trusted" "$preflight_status"
  if [[ -n $backend_owner && $backend_owner != 0 && $study_backend_trusted == false ]]; then
    printf -- '- Backend owner UID `%s` is not root; the study refused trust and skipped backend probes, worker startup, and fixture operations.\n' "$backend_owner"
  fi
  printf -- '- Build: `%s`; study: `%s`; report: `%s`; availability: `%s`\n' "$build_status" "$run_status" "$study_json_status" "$study_availability"
  printf -- '- Overall: **native acceptance not established**. A clean process exit or a subset of passing fixture cases is not broad enforcement evidence.\n\n'
  if [[ -f $output_dir/study-report.json ]]; then
    printf '## Cases\n\n| Case | Status | Worker | FD sent | Read | Published | Fixture outcome | Authority A | Authority B |\n|---|---|---:|---:|---:|---:|---|---|---|\n'
    python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
for c in json.load(open(sys.argv[1],encoding="utf-8")).get("cases",[]):
    vals=[c.get("name",""),c.get("status",""),str(c.get("worker_started",False)).lower(),
          str(c.get("capability_sent",False)).lower(),str(c.get("read_started",False)).lower(),
          str(c.get("published",False)).lower(),c.get("fixture_effect",""),c.get("authority_a",""),c.get("authority_b","")]
    vals=[str(v).replace("|","\\|").replace("\n"," ") for v in vals]
    print("| "+" | ".join(vals)+" |")
PY
    printf '\n## Remaining native gaps\n\n'
    python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
for item in json.load(open(sys.argv[1],encoding="utf-8")).get("native_evidence_gaps",[]): print("- "+str(item).replace("\n"," "))
PY
  else
    printf '## Evidence status\n\nNo valid study JSON report was produced. See the bounded logs and manifest for the distinct setup/build/run failure status.\n'
  fi
  printf '\nThe existing study does not exercise the integrated independent-actor, protected-owner rotation, mount-boundary, parent-death, or cleanup scenarios. It makes no shell-safety or production-readiness claim.\n'
} >"$summary"

(
  cd -- "$output_dir"
  find . -maxdepth 1 -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 -r sha256sum >SHA256SUMS
)
tar -czf "$archive" -C "$(dirname -- "$output_dir")" "$(basename -- "$output_dir")"
archive_hash=$(sha256sum -- "$archive" | awk '{print $1}')
printf '%s  %s\n' "$archive_hash" "$(basename -- "$archive")" >"${archive}.sha256"

printf 'Results directory: %s\nArchive: %s\nArchive SHA-256: %s\n' "$output_dir" "$archive" "$archive_hash"
if [[ -n $failure ]]; then
  printf 'Runner classification: %s\n' "$failure"
  exit 1
fi
