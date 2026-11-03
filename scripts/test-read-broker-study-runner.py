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

"""Disposable regression checks for the manual read-broker runner helpers."""

import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock


SCRIPTS = Path(__file__).resolve().parent
RUNNER = SCRIPTS / "run-read-broker-study.sh"


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


capture = load_module("read_broker_capture", SCRIPTS / "read-broker-study-capture.py")
report = load_module("read_broker_report", SCRIPTS / "read-broker-study-report.py")


class CaptureTests(unittest.TestCase):
    def test_output_cap_drains_both_streams(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-capture-test-") as directory:
            stdout = Path(directory) / "stdout"
            stderr = Path(directory) / "stderr"
            code = "import sys; print('x'*1000); print('y'*1000,file=sys.stderr)"
            result = capture.run_capture(
                [sys.executable, "-c", code], 5, 64, stdout, stderr, directory
            )
            self.assertEqual(result["exit_code"], 0)
            self.assertTrue(result["stdout_truncated"])
            self.assertTrue(result["stderr_truncated"])
            self.assertEqual(stdout.stat().st_size, 64)
            self.assertEqual(stderr.stat().st_size, 64)
            self.assertTrue(result["cleanup_complete"])

    def test_detached_pipe_holder_is_terminated_and_reaped(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-detached-test-") as directory:
            stdout = Path(directory) / "stdout"
            stderr = Path(directory) / "stderr"
            pid_file = Path(directory) / "pid"
            child_code = (
                "import os,signal,sys,time; "
                "signal.signal(signal.SIGTERM,signal.SIG_IGN); "
                "open(sys.argv[1],'w').write(str(os.getpid())); time.sleep(60)"
            )
            parent_code = (
                "import subprocess,sys; "
                "subprocess.Popen([sys.executable,'-c',sys.argv[2],sys.argv[1]],"
                "start_new_session=True); print('parent finished')"
            )
            started = time.monotonic()
            result = capture.run_capture(
                [sys.executable, "-c", parent_code, str(pid_file), child_code],
                5,
                4096,
                stdout,
                stderr,
                directory,
            )
            elapsed = time.monotonic() - started
            self.assertTrue(pid_file.exists())
            child_pid = int(pid_file.read_text())
            self.assertGreater(result["descendants_detected"], 0)
            self.assertTrue(result["cleanup_complete"], result)
            self.assertFalse(result["output_drain_incomplete"], result)
            self.assertTrue(result["direct_child_reaped"], result)
            self.assertLess(elapsed, 7.0)
            with self.assertRaises(ProcessLookupError):
                os.kill(child_pid, 0)

    def test_termination_deadline_is_not_reset_after_escalation(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-bounded-cleanup-test-") as directory:
            stdout = Path(directory) / "stdout"
            stderr = Path(directory) / "stderr"
            pid_file = Path(directory) / "pid"
            code = "import os,sys,time; open(sys.argv[1],'w').write(str(os.getpid())); time.sleep(60)"
            with mock.patch.object(capture, "TERMINATION_GRACE_SECONDS", 0.05), \
                    mock.patch.object(capture, "OUTPUT_DRAIN_SECONDS", 0.1), \
                    mock.patch.object(capture.os, "killpg", lambda *_args: None), \
                    mock.patch.object(capture, "signal_children", lambda *_args: None):
                started = time.monotonic()
                result = capture.run_capture(
                    [sys.executable, "-c", code, str(pid_file)], 0.05, 4096,
                    stdout, stderr, directory,
                )
            elapsed = time.monotonic() - started
            self.assertLess(elapsed, 1.0, result)
            self.assertFalse(result["cleanup_complete"], result)
            self.assertFalse(result["direct_child_reaped"], result)
            self.assertTrue(result["output_drain_incomplete"], result)
            child_pid = int(pid_file.read_text())
            try:
                os.kill(child_pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            else:
                os.waitpid(child_pid, 0)


class ReportClassificationTests(unittest.TestCase):
    def test_unavailable_after_trusted_probe_does_not_claim_no_probe(self):
        env = {
            "bubblewrap_trusted": True,
            "bubblewrap_version": "bubblewrap 0.9.0",
            "bubblewrap_capabilities": ["--unshare-user"],
            "bubblewrap_error": "required option missing",
        }
        result = report.classify_preflight(env)
        self.assertEqual(result, "backend_trust_validated; capability inspection attempted")
        self.assertNotIn("before_backend_probes", result)

    def test_untrusted_candidate_classification_requires_authoritative_error(self):
        trusted = report.classify_preflight(
            {
                "bubblewrap_trusted": False,
                "bubblewrap_error": "backend trust validation failed; harness skipped backend probes and sandbox launch",
            }
        )
        unknown = report.classify_preflight({"bubblewrap_trusted": False, "bubblewrap_error": "other"})
        self.assertEqual(trusted, "trust_refused_before_backend_probes")
        self.assertEqual(unknown, "probe_state_unknown")


class OutputReservationTests(unittest.TestCase):
    def test_existing_checksum_file_is_preserved(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-sidecar-test-") as directory:
            output = Path(directory) / "results"
            sidecar = Path(f"{output}.tar.gz.sha256")
            sidecar.write_text("keep this exact content\n")
            result = subprocess.run(
                [str(RUNNER), "--bwrap", "relative/path", "--output-dir", str(output)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=5,
                check=False,
            )
            self.assertEqual(result.returncode, 2)
            self.assertEqual(sidecar.read_text(), "keep this exact content\n")
            self.assertFalse(output.exists())

    def test_checksum_symlink_is_preserved(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-sidecar-link-test-") as directory:
            root = Path(directory)
            output = root / "results"
            target = root / "sentinel"
            target.write_text("symlink target stays unchanged\n")
            sidecar = Path(f"{output}.tar.gz.sha256")
            sidecar.symlink_to(target)
            result = subprocess.run(
                [str(RUNNER), "--bwrap", "relative/path", "--output-dir", str(output)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=5,
                check=False,
            )
            self.assertEqual(result.returncode, 2)
            self.assertEqual(target.read_text(), "symlink target stays unchanged\n")
            self.assertTrue(sidecar.is_symlink())
            self.assertFalse(output.exists())

    def test_interrupt_finalizes_partial_evidence_and_preserves_status(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-interrupt-test-") as directory:
            root = Path(directory)
            bindir = root / "bin"
            bindir.mkdir()
            marker = root / "build-started"
            fake_go = bindir / "go"
            fake_go.write_text(
                "#!/bin/sh\n"
                "if [ \"$1\" = version ]; then echo 'go version go1.26.10 linux/amd64'; exit 0; fi\n"
                f"printf started > {str(marker)!r}\n"
                "trap '' TERM\n"
                "while :; do sleep 1; done\n"
            )
            fake_go.chmod(0o755)
            output = root / "partial-results"
            environment = os.environ.copy()
            environment["PATH"] = f"{bindir}:{environment['PATH']}"
            process = subprocess.Popen(
                [str(RUNNER), "--output-dir", str(output)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                env=environment,
            )
            deadline = time.monotonic() + 20
            while not marker.exists() and process.poll() is None and time.monotonic() < deadline:
                time.sleep(0.05)
            self.assertTrue(marker.exists(), "fake build was not reached")
            process.send_signal(signal.SIGINT)
            stdout, stderr = process.communicate(timeout=12)
            self.assertEqual(process.returncode, 130, (stdout, stderr))
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(manifest["interruption"], {"signal": "INT", "exit_code": 130, "phase": "build"})
            self.assertEqual(manifest["build"]["status"], "interrupted")
            self.assertTrue(Path(f"{output}.tar.gz").is_file())
            self.assertTrue(Path(f"{output}.tar.gz.sha256").is_file())
            capture_record = json.loads((output / "build.capture.json").read_text())
            self.assertEqual(capture_record["interrupted_by_signal"], signal.SIGINT)
            self.assertTrue(capture_record["cleanup_complete"], capture_record)
            self.assertTrue(capture_record["direct_child_reaped"], capture_record)
            checksum = subprocess.run(
                ["sha256sum", "-c", str(Path(f"{output}.tar.gz.sha256"))],
                cwd=root,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=5,
                check=False,
            )
            self.assertEqual(checksum.returncode, 0, checksum.stderr)

    def test_sighup_terminates_and_reaps_active_build_child(self):
        with tempfile.TemporaryDirectory(prefix="komposyn-hup-test-") as directory:
            root = Path(directory)
            bindir = root / "bin"
            bindir.mkdir()
            pid_file = root / "build-child.pid"
            fake_go = bindir / "go"
            fake_go.write_text(
                f"#!{sys.executable}\n"
                "import os, signal, sys, time\n"
                "if sys.argv[1] == 'version':\n"
                "    print('go version go1.26.10 linux/amd64')\n"
                "    raise SystemExit(0)\n"
                f"open({str(pid_file)!r}, 'w').write(str(os.getpid()))\n"
                "signal.signal(signal.SIGTERM, signal.SIG_IGN)\n"
                "time.sleep(60)\n"
            )
            fake_go.chmod(0o755)
            output = root / "hup-results"
            environment = os.environ.copy()
            environment["PATH"] = f"{bindir}:{environment['PATH']}"
            process = subprocess.Popen(
                [str(RUNNER), "--output-dir", str(output)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                env=environment,
            )
            deadline = time.monotonic() + 20
            while not pid_file.exists() and process.poll() is None and time.monotonic() < deadline:
                time.sleep(0.05)
            self.assertTrue(pid_file.exists(), "fake build was not reached")
            child_pid = int(pid_file.read_text())
            process.send_signal(signal.SIGHUP)
            stdout, stderr = process.communicate(timeout=12)
            self.assertEqual(process.returncode, 129, (stdout, stderr))
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(manifest["interruption"]["signal"], "HUP")
            self.assertEqual(manifest["build"]["status"], "interrupted")
            capture_record = json.loads((output / "build.capture.json").read_text())
            self.assertEqual(capture_record["interrupted_by_signal"], signal.SIGHUP)
            self.assertTrue(capture_record["cleanup_complete"], capture_record)
            self.assertTrue(capture_record["direct_child_reaped"], capture_record)
            with self.assertRaises(ProcessLookupError):
                os.kill(child_pid, 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
