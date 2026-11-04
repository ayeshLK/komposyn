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

for dependency in git python3 tar sha256sum stat date mktemp chmod id uname env awk tr head find sort xargs ln; do
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
  [[ ! -e ${output_dir}.tar.gz.sha256 && ! -L ${output_dir}.tar.gz.sha256 ]] || { printf 'Checksum sidecar already exists; refusing to overwrite it.\n' >&2; exit 2; }
  mkdir -m 700 -- "$output_dir"
fi
chmod 700 -- "$output_dir"
output_dir=$(cd -- "$output_dir" && pwd -P)
archive=${output_dir}.tar.gz
if [[ -e $archive || -L $archive ]]; then
  printf 'Archive path already exists; refusing to overwrite it.\n' >&2
  exit 2
fi
if [[ -e ${archive}.sha256 || -L ${archive}.sha256 ]]; then
  printf 'Checksum sidecar already exists; refusing to overwrite it.\n' >&2
  exit 2
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/komposyn-read-broker-work.XXXXXX")
source_dir=$work/source
build_dir=$work/build
mkdir -m 700 -- "$source_dir" "$build_dir"
cleanup() { rm -rf -- "$work"; }

capture_helper=$script_dir/read-broker-study-capture.py
report_helper=$script_dir/read-broker-study-report.py
runner_start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
runner_hash=$(sha256sum -- "${BASH_SOURCE[0]}" | awk '{print $1}')
capture_helper_hash=$(sha256sum -- "$capture_helper" | awk '{print $1}')
report_helper_hash=$(sha256sum -- "$report_helper" | awk '{print $1}')
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
capture_pid=
phase=setup
interrupted_signal=
interrupted_phase=
interruption_exit=0
finalizing=0

run_bounded() {
  local name=$1 timeout=$2 cwd=$3; shift 3
  python3 "$capture_helper" "$timeout" "$LOG_LIMIT" \
    "$output_dir/$name.stdout.log" "$output_dir/$name.stderr.log" "$cwd" -- "$@" \
    >"$output_dir/$name.capture.json" &
  capture_pid=$!
  local capture_status=0
  while :; do
    if wait "$capture_pid"; then
      capture_status=0
      break
    else
      capture_status=$?
    fi
    kill -0 "$capture_pid" 2>/dev/null || break
  done
  capture_pid=
  if [[ ! -s $output_dir/$name.capture.json ]]; then
    python3 - "$output_dir/$name.capture.json" "$capture_status" <<'PY'
import json,sys
with open(sys.argv[1],"w",encoding="utf-8") as f:
    json.dump({"exit_code":None,"launch_error":"capture_helper_failed","helper_exit_code":int(sys.argv[2])},f)
    f.write("\n")
PY
  fi
  return 0
}

handle_signal() {
  local sig=$1
  [[ $finalizing == 0 ]] || return 0
  if [[ -z $interrupted_signal ]]; then
    interrupted_signal=$sig
    interrupted_phase=$phase
    case "$sig" in
      INT) interruption_exit=130 ;;
      TERM) interruption_exit=143 ;;
      HUP) interruption_exit=129 ;;
    esac
    failure="runner_interrupted_SIG$sig"
    case "$phase" in
      build) build_status=interrupted ;;
      study) run_status=interrupted ;;
    esac
  fi
  if [[ -n $capture_pid ]]; then
    kill -s "$sig" "$capture_pid" 2>/dev/null || true
  fi
}

read_capture() {
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    d=json.load(open(sys.argv[1], encoding="utf-8"))
    value=d.get(sys.argv[2], "")
    print(str(value).lower() if isinstance(value,bool) else value)
except Exception:
    print("")
PY
}

run_study() {
phase=preflight
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

if [[ -n $interrupted_signal ]]; then
  return 0
fi

if [[ $source_status == clean_git_archive ]]; then
  phase=go_version
  go_bin=$(command -v go || true)
  if [[ -z $go_bin ]]; then
    failure=${failure:-go_not_found}
    go_status=unavailable
  else
    run_bounded go-version 10 "$source_dir" env -i HOME="${HOME:?HOME is required}" PATH="$PATH" GOENV=off GOTOOLCHAIN=auto "$go_bin" version
    go_code=$(read_capture "$output_dir/go-version.capture.json" exit_code)
    go_version=$(tr -d '\r\n' <"$output_dir/go-version.stdout.log" | head -c 128)
    if [[ -n $interrupted_signal ]]; then
      go_status=interrupted
      return 0
    fi
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
      phase=build
      build_start=$(date +%s)
      if run_bounded build "$BUILD_TIMEOUT" "$source_dir" env -i HOME="${HOME:?HOME is required}" PATH="$PATH" GOENV=off GOTOOLCHAIN=auto CGO_ENABLED=0 GOCACHE="$build_dir/go-build-cache" "$go_bin" build -buildvcs=false -o "$build_dir/read-broker-study" ./cmd/read-broker-study; then
        build_code=$(read_capture "$output_dir/build.capture.json" exit_code)
        build_exit_code=$build_code
        build_timed_out=$(read_capture "$output_dir/build.capture.json" timed_out)
        build_seconds=$(read_capture "$output_dir/build.capture.json" duration_seconds)
        if [[ -n $interrupted_signal ]]; then
          build_status=interrupted
          return 0
        elif [[ $build_timed_out == true ]]; then
          build_status=timed_out
          failure=${failure:-build_timeout}
        elif [[ $(read_capture "$output_dir/build.capture.json" cleanup_complete) != true ]]; then
          build_status=cleanup_incomplete
          failure=${failure:-build_descendant_cleanup_incomplete}
        elif [[ $build_code == 0 && -x $build_dir/read-broker-study ]]; then
          build_status=passed
          executable_sha256=$(sha256sum -- "$build_dir/read-broker-study" | awk '{print $1}')
          phase=study
          run_start=$(date +%s)
          if run_bounded study "$STUDY_TIMEOUT" "$source_dir" env -i PATH="$PATH" TMPDIR="${TMPDIR:-/tmp}" "$build_dir/read-broker-study" --commit "$source_commit" --bwrap "$bwrap"; then
            run_code=$(read_capture "$output_dir/study.capture.json" exit_code)
            run_exit_code=$run_code
            run_timed_out=$(read_capture "$output_dir/study.capture.json" timed_out)
            run_seconds=$(read_capture "$output_dir/study.capture.json" duration_seconds)
            if [[ $(read_capture "$output_dir/study.capture.json" cleanup_complete) != true ]]; then
              failure=${failure:-study_descendant_cleanup_incomplete}
            fi
            if [[ -n $interrupted_signal ]]; then run_status=interrupted
            elif [[ $run_timed_out == true ]]; then run_status=timed_out
            elif [[ $run_code == 0 ]]; then run_status=completed
            else run_status=failed; failure=${failure:-study_exit_nonzero}; fi
            if python3 "$report_helper" validate "$output_dir/study.stdout.log" "$output_dir/study-report.json" "$source_commit" "$bwrap"; then
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
              preflight_status=$(python3 "$report_helper" classify "$output_dir/study-report.json")
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
}

# Collect safe backend metadata without executing or probing the candidate.
finalize_results() {
  [[ $finalizing == 0 ]] || return 0
  finalizing=1
  phase=finalizing
  trap '' HUP INT TERM
  set +e
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
  "$go_version" "$go_bin" "$capture_helper_hash" "$report_helper_hash" "$go_status" "$build_status" "$build_seconds" "$build_timed_out" \
  "$build_exit_code" "$executable_sha256" "$run_status" "$run_exit_code" "$run_seconds" "$run_timed_out" "$study_json_status" \
  "$study_availability" "$study_backend_trusted" "$preflight_status" "$failure" "$interrupted_signal" "$interruption_exit" "$interrupted_phase" "$output_dir" "$output_dir/study-report.json" <<'PY'
import json,os,platform,sys,time
import hashlib,stat
(path,started,runner_hash,baseline,commit,source_status,bwrap,bwrap_stat,bwrap_uid,bwrap_mode,bwrap_sha,
 go_version,go_bin,capture_hash,report_hash,go_status,build_status,build_seconds,build_timeout,build_exit,exe_sha,run_status,run_exit,run_seconds,run_timeout,
 report_status,availability,trusted,preflight,failure,interrupted,interrupt_exit,interrupt_phase,results_dir,report_path)=sys.argv[1:]
def optional_number(v):
    try:return float(v)
    except:return None
report={}
try:
    with open(report_path,encoding="utf-8") as f: report=json.load(f)
except Exception: pass
observed=report.get("environment",{})
capture_observations={}
for stage in ("go-version","build","study"):
    try:
        with open(os.path.join(results_dir,stage+".capture.json"),encoding="utf-8") as f: capture_observations[stage]=json.load(f)
    except Exception: pass
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
 "runner":{"sha256":runner_hash,"path":"scripts/run-read-broker-study.sh",
           "capture_helper_sha256":capture_hash,"capture_helper_path":"scripts/read-broker-study-capture.py",
           "report_helper_sha256":report_hash,"report_helper_path":"scripts/read-broker-study-report.py"},
 "interruption":{"signal":interrupted or None,"exit_code":int(interrupt_exit),"phase":interrupt_phase or None},
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
 "classification":{"runner_failure":failure or None,"captured_processes":{
   stage:{k:record.get(k) for k in ("cleanup_complete","descendants_detected","descendants_remaining",
       "output_drain_incomplete","direct_child_reaped","timed_out","interrupted_by_signal") if k in record}
   for stage,record in capture_observations.items()},
   "enforcement_acceptance":"not established by this runner; inspect per-case evidence and documented native gaps"},
 "capture_limits":{"stdout_bytes_per_command":2097152,"stderr_bytes_per_command":2097152,
                   "build_timeout_seconds":300,"study_timeout_seconds":180,
                   "timeout_action":"subreaper tracks descendants; SIGTERM then SIGKILL; drain is bounded and cleanup completeness is reported"},
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
  if [[ -n $backend_owner && $backend_owner != 0 && $preflight_status == trust_refused_before_backend_probes ]]; then
    printf -- '- Backend owner UID `%s` is not root; the study refused trust and skipped backend probes, worker startup, and fixture operations.\n' "$backend_owner"
  fi
  printf -- '- Build: `%s`; study: `%s`; report: `%s`; availability: `%s`\n' "$build_status" "$run_status" "$study_json_status" "$study_availability"
  if [[ -n $interrupted_signal ]]; then
    printf -- '- Interrupted by `SIG%s` during `%s`; exit code `%s`. Partial evidence is retained.\n' "$interrupted_signal" "$interrupted_phase" "$interruption_exit"
  fi
  printf -- '- Backend probe classification: `%s` (derived from trusted flag and backend error fields).\n' "$preflight_status"
  printf -- '- Overall: **native acceptance not established**. A clean process exit or a subset of passing fixture cases is not broad enforcement evidence.\n\n'
  if [[ -f $output_dir/study-report.json ]]; then
    printf '## Cases\n\n| Case | Status | Classification | Actor started | Actor ready | Transition | Actor exited | Generations before → after | Worker started | Worker ready | Worker joined | FD sent | FD held | Read | Published | Receipt accepted | Owner write authorized / started / completed | Abnormal owner write: attempted / authorized / started | Abnormal lifecycle: coordinator / diagnostic / init live before / Bubblewrap live before / init exit before / after / Bubblewrap exit before / after / cleanup attempted / signal sent / join / pidfd error / pending / admission blocked | Bytes match approved object | Once grants | Fixture outcome | Authority A | Authority B |\n|---|---|---|---:|---:|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---|---|---:|---:|---|---|---|\n'
    python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
def cell(value):
    if value is None or value == "": return "—"
    if isinstance(value,bool): value=str(value).lower()
    if isinstance(value,(dict,list)): value=json.dumps(value,sort_keys=True,separators=(",",":"))
    return str(value).replace("|","\\|").replace("\n"," ")
def optional_bool(case,key):
    value=case.get(key)
    return "—" if value is None else str(bool(value)).lower()
def approved_object_match(case):
    value=case.get("bytes_match_approved_object")
    if value is not None: return str(bool(value)).lower()
    value=case.get("bytes_match_target")
    return "—" if value is None else "legacy target="+str(bool(value)).lower()
doc=json.load(open(sys.argv[1],encoding="utf-8"))
cases=doc.get("cases",[])
for c in cases:
    before=c.get("generations_before")
    after=c.get("generations_after")
    generations=(f'{cell(before)} → {cell(after)}' if before is not None or after is not None else "—")
    owner=c.get("owner_lifecycle") or {}
    owner_gate=" / ".join(str(bool(owner.get(key))).lower() for key in
                           ("write_authorized","write_started","write_completed")) if owner else "—"
    life=c.get("abnormal_lifecycle") or {}
    abnormal_owner=" / ".join(str(bool(life.get(key))).lower() for key in
                               ("owner_write_attempted","owner_write_authorized","owner_write_started")) if life else "—"
    lifecycle=(" / ".join(str(bool(life.get(key))).lower() for key in
                          ("coordinator_exit_observed","coordinator_diagnostic_present",
                           "namespace_init_live_before_coordinator_termination","bubblewrap_live_before_coordinator_termination",
                           "namespace_init_exited_before_supervisor_cleanup","namespace_init_exited_after_supervisor_cleanup","bubblewrap_exited_before_supervisor_cleanup",
                           "bubblewrap_exited_after_supervisor_cleanup","supervisor_cleanup_attempted",
                           "supervisor_sent_cleanup_signal","direct_worker_join_confirmed",
                           "pidfd_observation_error","cleanup_pending_observed","admission_blocked"))
               if life else "—")
    vals=[c.get("name",""),c.get("status",""),c.get("classification"),
          optional_bool(c,"actor_started"),optional_bool(c,"actor_ready"),
          optional_bool(c,"actor_transition_completed"),optional_bool(c,"actor_exit_confirmed"),generations,
          optional_bool(c,"worker_started"),optional_bool(c,"worker_ready"),optional_bool(c,"worker_join_confirmed"),
          optional_bool(c,"capability_sent"),optional_bool(c,"capability_received_held"),
          optional_bool(c,"read_started"),optional_bool(c,"published"),
          optional_bool(c,"authorization_receipt_accepted"),owner_gate,abnormal_owner,lifecycle,approved_object_match(c),
          c.get("once_grant_consumptions"),c.get("fixture_effect",""),
          c.get("authority_a",""),c.get("authority_b",""),]
    vals=[cell(v) for v in vals]
    print("| "+" | ".join(vals)+" |")
violations=[c for c in cases if c.get("classification")=="violation" or c.get("status")=="observed_violation"]
print(f'\nObserved protected-rule violations: **{len(violations)}**.')
if violations:
    for c in violations:
        print("- `"+cell(c.get("name","unnamed"))+"` — "+cell(c.get("observed","outcome unavailable")))
else:
    print("No violation was recorded in the produced case rows. This does not imply a pass for unavailable, inconclusive, or omitted scenarios.")
PY
    printf '\n## Remaining native gaps\n\n'
    python3 - "$output_dir/study-report.json" <<'PY'
import json,sys
for item in json.load(open(sys.argv[1],encoding="utf-8")).get("native_evidence_gaps",[]): print("- "+str(item).replace("\n"," "))
PY
  else
    printf '## Evidence status\n\nNo valid study JSON report was produced. See the bounded logs and manifest for the distinct setup/build/run failure status.\n'
  fi
  printf '\nReview actor readiness, completed transitions, direct actor exit confirmation, capability hold, authorization receipt, owner write-gate evidence, and abnormal-lifecycle observations for each case. Missing fields in older reports are shown as unavailable; a missing transition is not a pass. Production-owner integration, mount-boundary behavior, recursive-child relocation, arbitrary namespace-descendant cleanup, and natural worker hangs remain separate evidence gaps. The study makes no shell-safety or production-readiness claim.\n'
} >"$summary"

(
  cd -- "$output_dir"
  find . -maxdepth 1 -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 -r sha256sum >SHA256SUMS
)

archive_status=not_created
sidecar_status=not_created
archive_hash=
archive_stage=$(mktemp "${archive}.tmp.XXXXXX" 2>/dev/null || true)
if [[ -n $archive_stage ]] && tar -czf "$archive_stage" -C "$(dirname -- "$output_dir")" "$(basename -- "$output_dir")"; then
  if ln -- "$archive_stage" "$archive"; then
    archive_status=created
    archive_hash=$(sha256sum -- "$archive" | awk '{print $1}')
    sidecar_stage=$(mktemp "${archive}.sha256.tmp.XXXXXX" 2>/dev/null || true)
    if [[ -n $sidecar_stage ]] && printf '%s  %s\n' "$archive_hash" "$(basename -- "$archive")" >"$sidecar_stage" && ln -- "$sidecar_stage" "${archive}.sha256"; then
      sidecar_status=created
    else
      sidecar_status=refused_existing_or_unavailable
    fi
    [[ -z ${sidecar_stage:-} ]] || rm -f -- "$sidecar_stage"
  else
    archive_status=refused_existing
  fi
else
  archive_status=creation_failed
fi
[[ -z ${archive_stage:-} ]] || rm -f -- "$archive_stage"

printf 'Results directory: %s\n' "$output_dir"
if [[ $archive_status == created ]]; then
  printf 'Archive: %s\nArchive SHA-256: %s\nChecksum sidecar: %s.sha256 (%s)\n' "$archive" "$archive_hash" "$archive" "$sidecar_status"
else
  printf 'Archive not created: %s\n' "$archive_status"
  failure=${failure:-archive_$archive_status}
fi
if [[ -n $failure ]]; then
  printf 'Runner classification: %s\n' "$failure"
fi
}

on_exit() {
  local original_status=$?
  if [[ $finalizing == 0 ]]; then
    failure=${failure:-runner_exit_$original_status}
    finalize_results
  fi
  cleanup
  return "$original_status"
}

trap on_exit EXIT
trap 'handle_signal HUP' HUP
trap 'handle_signal INT' INT
trap 'handle_signal TERM' TERM

run_study
finalize_results
if [[ -n $interrupted_signal ]]; then
  exit "$interruption_exit"
elif [[ -n $failure ]]; then
  exit 1
fi
