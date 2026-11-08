#!/usr/bin/env python3
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

"""Build and run the integrated, disposable Issue #215 Stage 6 study."""

import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time

# Loading the existing bounded capture helper must not dirty the source tree.
sys.dont_write_bytecode = True
from pathlib import Path, PurePosixPath

RUNNER_VERSION = "5"
DEFAULT_BWRAP = "/usr/bin/bwrap"
MAX_LOG_BYTES = 2 * 1024 * 1024
BUILD_TIMEOUT = 300
RUN_TIMEOUT = 180


def load_capture_helper(repo):
    path = repo / "scripts/read-broker-study-capture.py"
    spec = importlib.util.spec_from_file_location("read_broker_capture", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.run_capture


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as source:
        while True:
            chunk = source.read(1024 * 1024)
            if not chunk:
                return digest.hexdigest()
            digest.update(chunk)


def git(repo, *args):
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        timeout=30,
    ).stdout.strip()


def utc_now():
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")


def safe_extract_git_archive(archive, destination):
    with tarfile.open(archive, "r:") as source:
        for member in source.getmembers():
            name = PurePosixPath(member.name)
            if name.is_absolute() or ".." in name.parts or member.issym() or member.islnk():
                raise ValueError("Git archive contains an unsupported path entry")
        source.extractall(destination)


def bounded_capture(run_capture, argv, timeout, stdout, stderr, cwd):
    return run_capture(
        argv,
        timeout,
        MAX_LOG_BYTES,
        str(stdout),
        str(stderr),
        str(cwd),
    )


def isolated_environment_command(env, argv):
    return ["/usr/bin/env", "-i", *[f"{key}={value}" for key, value in env.items()], *argv]


def build_go_environment(work, cache_dir, module_cache):
    home = work / "home"
    home.mkdir(mode=0o700)
    os.chmod(home, 0o700)
    return {
        "HOME": str(home),
        "PATH": "/usr/bin:/bin",
        "GOCACHE": str(cache_dir),
        "GOMODCACHE": str(module_cache),
        "GOWORK": "off",
        "GOENV": "off",
        "GOTOOLCHAIN": "local",
        "GOPROXY": "https://proxy.golang.org",
        "GOAUTH": "off",
        "GOVCS": "*:off",
        "GOSUMDB": "sum.golang.org",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_TERMINAL_PROMPT": "0",
        "CGO_ENABLED": "0",
    }


def backend_metadata(path):
    metadata = {"path": path, "lstat_status": "unavailable"}
    try:
        info = os.lstat(path)
        metadata.update(
            {
                "lstat_status": "recorded",
                "regular_file": stat.S_ISREG(info.st_mode),
                "uid": info.st_uid,
                "gid": info.st_gid,
                "mode_octal": oct(stat.S_IMODE(info.st_mode)),
                "size_bytes": info.st_size,
                "sha256": sha256_file(path) if stat.S_ISREG(info.st_mode) else None,
            }
        )
    except OSError as exc:
        metadata["lstat_status"] = "unavailable"
        metadata["error_type"] = type(exc).__name__
    return metadata


def summarize(report, build, run):
    lines = [
        "# Issue #215 Stage 6 integrated native study",
        "",
        f"Overall classification: **{report.get('overall_status', 'report unavailable')}**",
        "",
        "This archive distinguishes runner/build correctness from sandbox enforcement. A successful Go test process or exit code is not an enforcement pass.",
        "",
        f"Build: `{build.get('exit_code')}`; timeout={build.get('timed_out', False)}; cleanup={build.get('cleanup_complete', False)}.",
        f"Study process: `{run.get('exit_code')}`; timeout={run.get('timed_out', False)}; cleanup={run.get('cleanup_complete', False)}.",
        "",
        "## Cases",
        "",
        "| Case | Status | Evidence kind | Worker started | Capability transferred | Capability held | Read started | Read outcome | Disclosure withheld | Cleanup confirmed | Reason |",
        "|---|---|---|---:|---:|---:|---:|---|---:|---:|---|",
    ]
    for case in report.get("cases", []):
        reason = str(case.get("reason", "")).replace("|", "\\|").replace("\n", " ")
        lines.append(
            "| {case} | {status} | {evidence} | {worker} | {cap} | {held} | {read} | {outcome} | {withheld} | {cleanup} | {reason} |".format(
                case=case.get("case_id", "unknown"),
                status=case.get("status", "unknown"),
                evidence=case.get("evidence_kind", "unclassified"),
                worker=case.get("worker_started", False),
                cap=case.get("capability_transferred", False),
                held=case.get("capability_held", False),
                read=case.get("read_started", False),
                outcome=case.get("read_outcome", ""),
                withheld=case.get("disclosure_withheld", False),
                cleanup=case.get("cleanup_confirmed", False),
                reason=reason,
            )
        )
    lines.extend(["", "## Limitations", ""])
    lines.extend(f"- {item}" for item in report.get("limitations", []))
    lines.extend(
        [
            "",
            "A backend trust refusal is unavailable evidence, not a pass. The report must be reviewed with the exact source/backend provenance in `manifest.json`; the test binary's structured report is `stage6-report.json`.",
            "",
        ]
    )
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--revision", help="exact clean HEAD revision to archive (default: HEAD)")
    parser.add_argument("--bwrap", default=DEFAULT_BWRAP, help="absolute trusted Bubblewrap path")
    parser.add_argument("--output-root", default="/tmp", help="existing parent for a new private results directory")
    args = parser.parse_args()

    script = Path(__file__).resolve()
    repo = Path(git(script.parent, "rev-parse", "--show-toplevel"))
    run_capture = load_capture_helper(repo)
    now = utc_now()
    manifest = {
        "schema_version": 1,
        "runner_version": RUNNER_VERSION,
        "started_utc": now,
        "finished_utc": None,
        "source": {},
        "host": {
            "os": platform.system().lower(),
            "architecture": platform.machine(),
            "kernel": platform.release(),
            "uid": os.getuid() if hasattr(os, "getuid") else None,
            "python_version": platform.python_version(),
        },
        "go": {"version": None, "status": "not_checked"},
        "backend": backend_metadata(args.bwrap),
        "build": {"status": "not_started"},
        "study": {"status": "not_started"},
        "report": {"status": "not_produced"},
        "classification": "incomplete",
        "limitations": [
            "This Stage 6 profile is research-only and does not register read_file in the shipped executable.",
            "Passing selected cases does not establish mutation, shell, provider-source-owner, or aggregate resource-limit acceptance.",
            "A successful process exit is not itself evidence that native enforcement passed.",
        ],
    }
    output_root = Path(args.output_root).expanduser().resolve()
    if not output_root.is_dir():
        raise SystemExit("output root must already exist and be a directory")
    if output_root == repo or repo in output_root.parents:
        raise SystemExit("output root must be outside the source repository")
    stamp = dt.datetime.now().strftime("%Y%m%dT%H%M%SZ")
    output = output_root / f"komposyn-issue215-stage6-{stamp}-{os.getpid()}"
    archive_path = output.with_suffix(".tar.gz")
    if output.exists() or archive_path.exists():
        raise SystemExit("results path already exists; refusing to overwrite")
    output.mkdir(mode=0o700)
    os.chmod(output, 0o700)

    work = Path(tempfile.mkdtemp(prefix="komposyn-stage6-work-", dir="/tmp"))
    source_dir = work / "source"
    build_dir = work / "build"
    cache_dir = work / "gocache"
    module_cache = work / "gomodcache"
    for directory in (source_dir, build_dir, cache_dir, module_cache):
        directory.mkdir(mode=0o700)
    manifest_path = output / "manifest.json"
    report_path = output / "stage6-report.json"
    try:
        status = git(repo, "status", "--porcelain", "--untracked-files=all")
        head = git(repo, "rev-parse", "HEAD")
        requested = git(repo, "rev-parse", (args.revision or "HEAD") + "^{commit}")
        manifest["source"] = {
            "revision_requested": args.revision or "HEAD",
            "commit": requested,
            "checkout_head": head,
            "checkout_clean": not bool(status),
            "archive_kind": "git archive of exact commit",
            "dirty_source_labeled_as_commit": False,
        }
        if os.getuid() == 0:
            manifest["classification"] = "refused_root"
            raise RuntimeError("runner refuses to run as root")
        if platform.system() != "Linux":
            manifest["classification"] = "unsupported_host"
            raise RuntimeError("Stage 6 native runner supports Linux only")
        if platform.machine() not in ("x86_64", "aarch64"):
            manifest["classification"] = "unsupported_architecture"
            raise RuntimeError("host architecture is not in the runner's reviewed candidate set")
        if not os.path.isabs(args.bwrap):
            manifest["classification"] = "refused_backend_path"
            raise RuntimeError("Bubblewrap path must be absolute")
        if requested != head or status:
            manifest["classification"] = "refused_dirty_or_non_head_source"
            raise RuntimeError("source must be the exact clean HEAD; commit current changes before running")
        if not args.bwrap.startswith("/"):
            raise RuntimeError("Bubblewrap path must be absolute")

        # The backend's trusted ownership/path checks and bounded feature probes
        # are performed by the compiled harness before any Bubblewrap execution.
        archive_file = work / "source.tar"
        with open(archive_file, "wb") as archived:
            subprocess.run(["git", "-C", str(repo), "archive", "--format=tar", requested], stdout=archived, stderr=subprocess.PIPE, check=True, timeout=60)
        safe_extract_git_archive(archive_file, source_dir)
        manifest["source"]["archive_sha256"] = sha256_file(archive_file)
        manifest["source"]["runner_script_sha256"] = sha256_file(source_dir / "scripts/run-issue215-stage6.py")
        manifest["source"]["capture_helper_sha256"] = sha256_file(source_dir / "scripts/read-broker-study-capture.py")
        manifest["source"]["study_test_sha256"] = sha256_file(source_dir / "testdata/native-stage6/sandbox_stage6_linux_test.go")
        manifest["source"]["runtime_overlay_sha256"] = sha256_file(source_dir / "testdata/native-stage6/runtime_study_linux.go")

        go = shutil.which("go")
        if not go:
            manifest["go"]["status"] = "unavailable"
            raise RuntimeError("Go is not available on PATH")
        env = build_go_environment(work, cache_dir, module_cache)
        version_stdout, version_stderr = output / "go-version.stdout.log", output / "go-version.stderr.log"
        version_result = bounded_capture(run_capture, isolated_environment_command(env, [go, "version"]), 10, version_stdout, version_stderr, source_dir)
        manifest["go"]["status"] = "recorded" if version_result.get("exit_code") == 0 else "unavailable"
        manifest["go"]["capture"] = version_result
        try:
            manifest["go"]["version"] = version_stdout.read_text(encoding="utf-8", errors="replace").strip()[:512]
        except OSError:
            pass
        if version_result.get("exit_code") != 0:
            raise RuntimeError("Go version query failed")
        manifest["go"]["required_minimum"] = "go1.26.10"
        version_match = re.search(r"\bgo(\d+)\.(\d+)(?:\.(\d+))?\b", manifest["go"]["version"])
        version_tuple = tuple(int(part or 0) for part in version_match.groups()) if version_match else (0, 0, 0)
        if version_tuple < (1, 26, 10):
            manifest["go"]["status"] = "unsupported_version"
            raise RuntimeError("Go 1.26.10 or newer is required")

        overlay = {
            "Replace": {
                str(source_dir / "internal/harness/runtime/native_read_study_linux.go"): str(source_dir / "testdata/native-stage6/runtime_study_linux.go"),
                str(source_dir / "internal/harness/sandbox/native_stage6_linux_test.go"): str(source_dir / "testdata/native-stage6/sandbox_stage6_linux_test.go"),
            }
        }
        overlay_path = work / "overlay.json"
        overlay_path.write_text(json.dumps(overlay, sort_keys=True), encoding="utf-8")
        os.chmod(overlay_path, 0o600)
        manifest["source"]["overlay_mapping_sha256"] = sha256_file(overlay_path)
        executable = build_dir / "stage6-worker.test"
        build_command = [go, "test", "-c", "-buildvcs=false", "-tags=komposyn_stage6_study", "-overlay=" + str(overlay_path), "-o", str(executable), "./internal/harness/sandbox"]
        manifest["build"] = {"status": "running", "command_template": ["go", "test", "-c", "-buildvcs=false", "-tags=komposyn_stage6_study", "-overlay=<temporary overlay>", "-o <temporary binary>", "./internal/harness/sandbox"], "cgo_enabled": "0", "timeout_seconds": BUILD_TIMEOUT, "output_limit_bytes_per_stream": MAX_LOG_BYTES}
        build = bounded_capture(run_capture, isolated_environment_command(env, build_command), BUILD_TIMEOUT, output / "build.stdout.log", output / "build.stderr.log", source_dir)
        manifest["build"].update(build)
        manifest["build"]["binary_sha256"] = sha256_file(executable) if executable.exists() else None
        manifest["build"]["status"] = "built" if build.get("exit_code") == 0 and executable.exists() else ("timeout" if build.get("timed_out") else "failed")
        if manifest["build"]["status"] != "built":
            manifest["classification"] = "build_failed_or_timed_out"
            raise RuntimeError("Stage 6 test binary did not build")

        test_env = dict(env)
        test_env.update({
            "KOMPOSYN_STAGE6_REPORT": str(report_path),
            "KOMPOSYN_STAGE6_SOURCE_COMMIT": requested,
            "KOMPOSYN_STAGE6_BWRAP": args.bwrap,
        })
        run_command = [str(executable), "-test.run=^TestStage6", "-test.timeout=120s", "-test.count=1"]
        manifest["study"] = {"status": "running", "command_template": ["<built test binary>", "-test.run=^TestStage6", "-test.timeout=120s", "-test.count=1"], "timeout_seconds": RUN_TIMEOUT, "output_limit_bytes_per_stream": MAX_LOG_BYTES}
        run = bounded_capture(run_capture, isolated_environment_command(test_env, run_command), RUN_TIMEOUT, output / "study.stdout.log", output / "study.stderr.log", source_dir)
        manifest["study"].update(run)
        manifest["study"]["status"] = "completed" if run.get("exit_code") == 0 and not run.get("timed_out") else ("timeout" if run.get("timed_out") else "failed")
        if report_path.exists():
            try:
                report_data = report_path.read_bytes()
                if len(report_data) > 2 * 1024 * 1024:
                    raise ValueError("structured report exceeded 2 MiB")
                report = json.loads(report_data)
                if report.get("source_commit") != requested or report.get("schema_version") != 1:
                    raise ValueError("structured report provenance or schema mismatch")
                manifest["report"] = {"status": "valid", "sha256": sha256_file(report_path), "case_count": len(report.get("cases", []))}
                manifest["classification"] = report.get("overall_status", "incomplete")
                (output / "summary.md").write_text(summarize(report, manifest["build"], manifest["study"]), encoding="utf-8")
            except (OSError, ValueError, json.JSONDecodeError) as exc:
                manifest["report"] = {"status": "malformed", "error_type": type(exc).__name__}
                manifest["classification"] = "malformed_report"
        else:
            manifest["report"] = {"status": "missing", "study_completed": manifest["study"]["status"]}
            manifest["classification"] = "report_missing"
    except Exception as exc:
        manifest.setdefault("failure", {"type": type(exc).__name__, "reason": str(exc)[:512]})
        if not any(path.name == "summary.md" for path in output.iterdir()):
            (output / "summary.md").write_text(
                "# Issue #215 Stage 6 integrated native study\n\n"
                f"Overall classification: **{manifest['classification']}**\n\n"
                f"Runner stopped: `{type(exc).__name__}`.\n\n"
                "No enforcement pass is claimed. See `manifest.json` and bounded logs for the completed phase.\n",
                encoding="utf-8",
            )
    finally:
        manifest["finished_utc"] = utc_now()
        manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        for path in output.iterdir():
            if path.is_file() and path.name != "SHA256SUMS":
                os.chmod(path, 0o600)
        sums = []
        for path in sorted(output.iterdir()):
            if path.is_file() and path.name != "SHA256SUMS":
                sums.append(f"{sha256_file(path)}  {path.name}")
        (output / "SHA256SUMS").write_text("\n".join(sums) + "\n", encoding="utf-8")
        os.chmod(output / "SHA256SUMS", 0o600)
        with tarfile.open(archive_path, "w:gz") as archive:
            archive.add(output, arcname=output.name, recursive=True)
        os.chmod(archive_path, 0o600)
        shutil.rmtree(work, ignore_errors=True)

    print(f"Results directory: {output}")
    print(f"Results archive: {archive_path}")
    print(f"Classification: {manifest['classification']}")
    return 0 if manifest["classification"] == "measured_pass" else 3


if __name__ == "__main__":
    raise SystemExit(main())
