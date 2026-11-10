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

"""Regression tests for the Issue #215 Stage 6 manual runner."""

import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
SCRIPT = Path(__file__).with_name("run-issue215-stage6.py").resolve()
SPEC = importlib.util.spec_from_file_location("stage6_runner", SCRIPT)
RUNNER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNNER)


class Stage6RunnerTests(unittest.TestCase):
    def test_bind_mode_cli_rejects_unreviewed_values(self):
        process = subprocess.run(
            [sys.executable, "-B", str(SCRIPT), "--bind-mode", "unchecked"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=10,
            check=False,
        )
        self.assertEqual(process.returncode, 2)
        self.assertIn("invalid choice", process.stderr)

    def test_stage6_build_strips_non_runtime_sections(self):
        command = RUNNER.stage6_build_command("/trusted/go", "/tmp/overlay.json", "/tmp/study.test")

        self.assertIn("-ldflags=-s -w", command)
        self.assertEqual(command[-1], "./internal/harness/sandbox")

    def test_shipped_worker_build_is_release_shaped_and_commit_pinned(self):
        command = RUNNER.shipped_worker_build_command("/trusted/go", "/tmp/komposyn", "a" * 40)

        self.assertEqual(command[1], "build")
        self.assertIn("-trimpath", command)
        self.assertIn("-buildvcs=false", command)
        self.assertIn("-s -w -buildid= -X main.cliVersion=0.0.0-native-study -X main.cliCommit=" + "a" * 40, command)
        self.assertEqual(command[-1], "./cmd/harness")

    def test_shipped_worker_profile_refuses_descriptor_mode(self):
        process = subprocess.run(
            [sys.executable, "-B", str(SCRIPT), "--include-shipped-worker", "--bind-mode", "ro-bind-fd"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=10,
            check=False,
        )
        self.assertEqual(process.returncode, 2)
        self.assertIn("requires --bind-mode file-snapshot", process.stderr)

    def test_summary_keeps_synthetic_and_native_evidence_distinct(self):
        report = {
            "overall_status": "native_unavailable",
            "cases": [
                {
                    "case_id": "synthetic-refusal",
                    "status": "measured_pass",
                    "evidence_kind": "synthetic-runtime-composition",
                    "disclosure_withheld": True,
                },
                {
                    "case_id": "native-read",
                    "status": "unavailable",
                    "evidence_kind": "native-integrated",
                    "worker_started": False,
                    "failure_diagnostic": "worker startup failed: synthetic error",
                },
            ],
            "limitations": ["native worker did not run"],
        }
        summary = RUNNER.summarize(report, {"exit_code": 0}, {"exit_code": 0})

        self.assertIn(
            "| synthetic-refusal | measured_pass | synthetic-runtime-composition |",
            summary,
        )
        self.assertIn(
            "| native-read | unavailable | native-integrated |",
            summary,
        )
        self.assertIn("worker startup failed: synthetic error", summary)

    def test_summary_records_requested_shipped_worker_image_separately(self):
        summary = RUNNER.summarize(
            {"overall_status": "native_unavailable", "worker_image": {"profile": "cmd-harness-worker-image", "size_bytes": 12, "sha256": "abc"}, "cases": [], "limitations": []},
            {"exit_code": 0},
            {"exit_code": 3},
        )

        self.assertIn("Worker image requested", summary)
        self.assertIn("SHA-256: `abc`", summary)
        self.assertNotIn("was launched", summary)

    def test_backend_metadata_is_read_only_and_hashes_regular_file(self):
        with tempfile.TemporaryDirectory() as directory:
            backend = Path(directory) / "backend"
            backend.write_bytes(b"synthetic executable")
            metadata = RUNNER.backend_metadata(str(backend))
            self.assertEqual(metadata["lstat_status"], "recorded")
            self.assertTrue(metadata["regular_file"])
            self.assertEqual(metadata["sha256"], hashlib.sha256(b"synthetic executable").hexdigest())

    def test_build_environment_does_not_inherit_home_or_proxy_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            private_home = root / "host-home"
            private_home.mkdir()
            (private_home / ".netrc").write_text("machine proxy.example login secret password secret\n")
            work = root / "work"
            work.mkdir()
            cache = work / "cache"
            module_cache = work / "module-cache"
            cache.mkdir()
            module_cache.mkdir()
            old = {key: os.environ.get(key) for key in ("HOME", "GOPROXY", "GOAUTH")}
            try:
                os.environ["HOME"] = str(private_home)
                os.environ["GOPROXY"] = "https://user:password@private-proxy.invalid"
                os.environ["GOAUTH"] = "git /host/credential-helper"
                env = RUNNER.build_go_environment(work, cache, module_cache)
            finally:
                for key, value in old.items():
                    if value is None:
                        os.environ.pop(key, None)
                    else:
                        os.environ[key] = value

            self.assertEqual(env["HOME"], str(work / "home"))
            self.assertNotEqual(env["HOME"], str(private_home))
            self.assertFalse((Path(env["HOME"]) / ".netrc").exists())
            self.assertEqual(env["GOPROXY"], "https://proxy.golang.org")
            self.assertEqual(env["GOAUTH"], "off")
            self.assertEqual(env["GOVCS"], "*:off")
            self.assertEqual(env["GIT_CONFIG_NOSYSTEM"], "1")
            command = RUNNER.isolated_environment_command(env, ["go", "version"])
            self.assertNotIn("private-proxy", " ".join(command))
            self.assertNotIn("credential-helper", " ".join(command))

    def test_archive_extraction_rejects_parent_traversal(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "unsafe.tar"
            with tarfile.open(source, "w") as archive:
                import io

                entry = tarfile.TarInfo("../escaped")
                payload = b"not extracted"
                entry.size = len(payload)
                archive.addfile(entry, io.BytesIO(payload))
            with self.assertRaises(ValueError):
                RUNNER.safe_extract_git_archive(source, root / "extract")

    def test_dirty_checkout_refusal_retains_manifest_and_valid_archive(self):
        if os.getuid() == 0:
            self.skipTest("runner intentionally refuses root")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            checkout = root / "checkout"
            scripts = checkout / "scripts"
            scripts.mkdir(parents=True)
            fixture_script = scripts / SCRIPT.name
            fixture_helper = scripts / "read-broker-study-capture.py"
            fixture_script.write_bytes(SCRIPT.read_bytes())
            fixture_helper.write_bytes((SCRIPT.parent / fixture_helper.name).read_bytes())
            subprocess.run(["git", "init", "--quiet", str(checkout)], check=True)
            subprocess.run(["git", "-C", str(checkout), "add", "scripts"], check=True)
            env = os.environ.copy()
            env.update(
                {
                    "GIT_AUTHOR_NAME": "Stage 6 test",
                    "GIT_AUTHOR_EMAIL": "stage6@example.invalid",
                    "GIT_COMMITTER_NAME": "Stage 6 test",
                    "GIT_COMMITTER_EMAIL": "stage6@example.invalid",
                }
            )
            subprocess.run(["git", "-C", str(checkout), "commit", "--quiet", "-m", "fixture"], check=True, env=env)
            (checkout / "dirty.marker").write_text("deliberately dirty fixture\n")
            output_root = root / "output"
            output_root.mkdir()
            process = subprocess.run(
                [sys.executable, "-B", str(fixture_script), "--revision", "HEAD", "--bwrap", "/nonexistent/synthetic-bwrap", "--output-root", str(output_root)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=30,
                check=False,
            )
            self.assertEqual(process.returncode, 3, process.stderr)
            archive_path = next(Path(output_root).glob("*.tar.gz"))
            result_path = Path(str(archive_path)[:-7])
            manifest = json.loads((result_path / "manifest.json").read_text())
            self.assertEqual(manifest["classification"], "refused_dirty_or_non_head_source")
            self.assertTrue(manifest["source"]["checkout_clean"] is False)
            self.assertEqual(manifest["build"]["status"], "not_started")
            self.assertEqual(manifest["study"]["status"], "not_started")
            with tarfile.open(archive_path, "r:gz") as archive:
                archive.extractall(Path(output_root) / "unpacked", filter="data")
            unpacked = Path(output_root) / "unpacked" / result_path.name
            for line in (unpacked / "SHA256SUMS").read_text().splitlines():
                expected, name = line.split("  ", 1)
                self.assertEqual(hashlib.sha256((unpacked / name).read_bytes()).hexdigest(), expected)


if __name__ == "__main__":
    unittest.main()
