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

"""Validate and classify the existing read-broker study's structured report."""

import json
import sys

MAX_REPORT_BYTES = 2 * 1024 * 1024


def validate_report_bytes(data, expected_commit, expected_bwrap):
    if len(data) > MAX_REPORT_BYTES:
        raise ValueError("report exceeds byte limit")
    report = json.loads(data)
    environment = report.get("environment")
    cases = report.get("cases")
    if (
        report.get("schema") != "komposyn.read-broker-study.v1"
        or report.get("commit") != expected_commit
        or report.get("availability") not in ("available", "unavailable")
        or not isinstance(environment, dict)
        or environment.get("bubblewrap_path") != expected_bwrap
        or not isinstance(report.get("launch_policy"), dict)
        or not isinstance(cases, list)
        or not all(isinstance(case, dict) and isinstance(case.get("status"), str) for case in cases)
    ):
        raise ValueError("report schema or provenance mismatch")
    return report


def classify_preflight(environment):
    trusted = environment.get("bubblewrap_trusted")
    error = environment.get("bubblewrap_error", "")
    if trusted is True:
        return "backend_trust_validated; capability inspection attempted"
    if trusted is False and "trust validation failed; harness skipped backend probes" in error:
        return "trust_refused_before_backend_probes"
    if trusted is False and "missing or not a regular file" in error:
        return "backend_missing_or_invalid; probe state unavailable"
    return "probe_state_unknown"


def main(argv):
    if len(argv) == 6 and argv[1] == "validate":
        stdout_path, report_path, commit, bwrap = argv[2:]
        with open(stdout_path, "rb") as stdout_file:
            data = stdout_file.read(MAX_REPORT_BYTES + 1)
        report = validate_report_bytes(data, commit, bwrap)
        with open(report_path, "wb") as report_file:
            report_file.write(data)
        return 0
    if len(argv) == 3 and argv[1] == "classify":
        with open(argv[2], encoding="utf-8") as report_file:
            report = json.load(report_file)
        print(classify_preflight(report.get("environment", {})))
        return 0
    print("usage: read-broker-study-report.py validate STDOUT REPORT COMMIT BWRAP | classify REPORT", file=sys.stderr)
    return 2


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv))
    except (OSError, ValueError, json.JSONDecodeError):
        raise SystemExit(1)
