# Copyright 2026 Ayesh Almeida
# SPDX-License-Identifier: Apache-2.0

import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

runner_path = Path(__file__).with_name("run-native-acceptance.py")
spec = importlib.util.spec_from_file_location("native_acceptance_runner", runner_path)
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ManifestTest(unittest.TestCase):
    def test_result_archive_redacts_private_host_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            private_root = Path(tmp).resolve() / "private-account"
            mountpoint = private_root / "private-mount"
            scratch = mountpoint / "scratch"
            scratch.mkdir(parents=True)
            backend = private_root / "private-bin" / "bwrap"
            backend.parent.mkdir()
            backend.touch(mode=0o755)

            read_text = Path.read_text

            def read_mountinfo(path, *args, **kwargs):
                if path == Path("/proc/self/mountinfo"):
                    return f"42 1 8:1 / {mountpoint} rw - ext4 /dev/fixture rw\n"
                return read_text(path, *args, **kwargs)

            argv = [
                str(runner_path), "--repo", str(private_root),
                "--revision", "0" * 40, "--scratch-dir", str(scratch),
                "--bwrap", str(backend),
            ]
            # Fail source extraction after constructing the manifest. The runner
            # still writes the shareable archive without building or launching a worker.
            with (
                patch.object(sys, "argv", argv),
                patch.object(runner.os, "geteuid", return_value=1000),
                patch.object(runner.shutil, "which", return_value="/usr/bin/fixture"),
                patch.object(Path, "read_text", read_mountinfo),
                patch.object(runner.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)),
                patch.object(runner.subprocess, "check_output", return_value=b""),
                contextlib.redirect_stdout(io.StringIO()),
                contextlib.redirect_stderr(io.StringIO()),
            ):
                previous_umask = runner.os.umask(0o077)
                try:
                    self.assertEqual(runner.main(), 1)
                finally:
                    runner.os.umask(previous_umask)

            archive_path, = scratch.glob("*.tar.gz")
            with tarfile.open(archive_path) as archive:
                manifest_member, = (m for m in archive.getmembers() if m.name.endswith("/manifest.json"))
                manifest_text = archive.extractfile(manifest_member).read().decode()
            self.assertNotIn(str(mountpoint), manifest_text)
            self.assertNotIn(str(backend), manifest_text)
            self.assertNotIn("private-account", manifest_text)
            manifest = json.loads(manifest_text)
            self.assertEqual(manifest["scratch_filesystem"], "ext4")
            self.assertEqual(manifest["scratch_mountpoint"], "<scratch-mount>")
            self.assertEqual(manifest["bubblewrap_path"], "<configured-backend>")
            self.assertEqual(manifest["bubblewrap_metadata"]["mode"], oct(backend.stat().st_mode & 0o7777))


if __name__ == "__main__":
    unittest.main()
