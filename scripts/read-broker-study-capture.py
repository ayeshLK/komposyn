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

"""Bounded subprocess capture for the manual read-broker study runner."""

import ctypes
import json
import os
import selectors
import signal
import subprocess
import sys
import time

PR_SET_CHILD_SUBREAPER = 36
OUTPUT_DRAIN_SECONDS = 2.0
TERMINATION_GRACE_SECONDS = 3.0


def enable_child_subreaper():
    """Adopt orphaned grandchildren so timed-out detached children can be reaped."""
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != 0:
        err = ctypes.get_errno()
        raise OSError(err, os.strerror(err))
    children_path = f"/proc/self/task/{os.getpid()}/children"
    if not os.path.exists(children_path):
        raise OSError("Linux child list is unavailable")


def direct_children():
    path = f"/proc/self/task/{os.getpid()}/children"
    try:
        with open(path, encoding="ascii") as children_file:
            return [int(pid) for pid in children_file.read().split()]
    except (OSError, ValueError):
        return []


def signal_children(pids, sig):
    for pid in pids:
        try:
            os.kill(pid, sig)
        except ProcessLookupError:
            pass
        except PermissionError:
            continue


def reap_adopted(pids, leader_pid):
    for pid in pids:
        if pid == leader_pid:
            continue
        try:
            os.waitpid(pid, os.WNOHANG)
        except (ChildProcessError, ProcessLookupError):
            pass


def run_capture(argv, timeout, limit, stdout_path, stderr_path, cwd):
    started = time.monotonic()
    result = {
        "exit_code": None,
        "timed_out": False,
        "duration_seconds": 0.0,
        "stdout_bytes": 0,
        "stderr_bytes": 0,
        "stdout_truncated": False,
        "stderr_truncated": False,
        "termination": "none",
        "cleanup_complete": True,
        "descendants_detected": 0,
        "output_drain_incomplete": False,
        "direct_child_reaped": False,
    }

    files = {"stdout": open(stdout_path, "wb"), "stderr": open(stderr_path, "wb")}

    def close_outputs():
        for output_file in files.values():
            if not output_file.closed:
                output_file.flush()
                os.fsync(output_file.fileno())
                output_file.close()

    try:
        enable_child_subreaper()
    except OSError as exc:
        result["launch_error"] = "child_tracking_unavailable"
        result["launch_error_type"] = type(exc).__name__
        close_outputs()
        result["duration_seconds"] = round(time.monotonic() - started, 3)
        return result

    try:
        child = subprocess.Popen(
            argv,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            start_new_session=True,
            close_fds=True,
            cwd=cwd,
        )
    except Exception as exc:  # Report only the error type, never argv or paths.
        result["launch_error"] = "subprocess_start_failed"
        result["launch_error_type"] = type(exc).__name__
        close_outputs()
        result["duration_seconds"] = round(time.monotonic() - started, 3)
        return result

    selector = selectors.DefaultSelector()
    counts = {"stdout": 0, "stderr": 0}
    streams = {child.stdout: "stdout", child.stderr: "stderr"}
    for stream, name in streams.items():
        os.set_blocking(stream.fileno(), False)
        selector.register(stream, selectors.EVENT_READ, name)

    deadline = started + timeout
    terminate_at = None
    stop_draining_at = None
    interrupted_signal = None

    def begin_termination(reason, timed_out=False):
        nonlocal terminate_at, stop_draining_at
        if terminate_at is not None:
            return
        result["termination"] = reason
        result["timed_out"] = timed_out
        try:
            os.killpg(child.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        signal_children(direct_children(), signal.SIGTERM)
        terminate_at = time.monotonic() + TERMINATION_GRACE_SECONDS
        stop_draining_at = terminate_at + OUTPUT_DRAIN_SECONDS

    def handle_signal(signum, _frame):
        nonlocal interrupted_signal
        interrupted_signal = signum
        begin_termination("interrupted; sent SIGTERM then bounded SIGKILL", timed_out=False)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    while True:
        now = time.monotonic()
        leader_status = child.poll()
        children = direct_children()
        descendants = [pid for pid in children if pid != child.pid]
        result["descendants_detected"] = max(result["descendants_detected"], len(descendants))

        # A command that exits while leaving a detached descendant is not a
        # clean completion. Adopt, terminate, and reap that child before return.
        if leader_status is not None and descendants and terminate_at is None:
            result["detached_descendants_observed"] = True
            begin_termination("detached descendants remained after direct child exit")

        if now >= deadline and terminate_at is None:
            begin_termination("deadline; sent SIGTERM then bounded SIGKILL", timed_out=True)

        if terminate_at is not None and now >= terminate_at:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            signal_children(direct_children(), signal.SIGKILL)
            terminate_at = None

        # Reap adopted children that have exited. Newly orphaned descendants
        # appear in the child list and are handled on the next loop iteration.
        reap_adopted(direct_children(), child.pid)

        leader_status = child.poll()
        children = direct_children()
        descendants = [pid for pid in children if pid != child.pid]
        streams_open = bool(selector.get_map())
        if leader_status is not None and not descendants and not streams_open:
            result["exit_code"] = leader_status
            result["direct_child_reaped"] = True
            break

        if stop_draining_at is not None and now >= stop_draining_at:
            # Do not wait forever on a process that escaped the original
            # process group. Keep the result explicit if the kernel did not
            # confirm complete cleanup or close inherited pipes in time.
            result["cleanup_complete"] = False
            result["output_drain_incomplete"] = streams_open
            result["termination"] += "; bounded drain ended with cleanup still pending"
            if streams_open:
                for stream in list(selector.get_map().values()):
                    selector.unregister(stream.fileobj)
                    stream.fileobj.close()
            signal_children(direct_children(), signal.SIGKILL)
            if child.poll() is None:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            signal_children(direct_children(), signal.SIGKILL)
            result["exit_code"] = child.poll()
            result["direct_child_reaped"] = result["exit_code"] is not None
            break

        events = selector.select(0.05)
        for key, _ in events:
            stream, name = key.fileobj, key.data
            try:
                data = os.read(stream.fileno(), 65536)
            except BlockingIOError:
                continue
            if not data:
                selector.unregister(stream)
                stream.close()
                continue
            remaining = max(0, limit - counts[name])
            if remaining:
                files[name].write(data[:remaining])
                counts[name] += min(len(data), remaining)
            if len(data) > remaining:
                result[name + "_truncated"] = True

    if interrupted_signal is not None:
        result["interrupted_by_signal"] = interrupted_signal
    close_outputs()
    result["stdout_bytes"] = counts["stdout"]
    result["stderr_bytes"] = counts["stderr"]
    result["descendants_remaining"] = len([p for p in direct_children() if p != child.pid])
    if result["descendants_remaining"]:
        result["cleanup_complete"] = False
    result["duration_seconds"] = round(time.monotonic() - started, 3)
    return result


def main():
    if len(sys.argv) < 7:
        return 2
    timeout = float(sys.argv[1])
    limit = int(sys.argv[2])
    stdout_path, stderr_path, cwd = sys.argv[3:6]
    argv = sys.argv[6:]
    if argv[:1] == ["--"]:
        argv = argv[1:]
    result = run_capture(argv, timeout, limit, stdout_path, stderr_path, cwd)
    json.dump(result, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
