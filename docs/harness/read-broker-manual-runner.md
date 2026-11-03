# Manual read-broker native study runner

[`scripts/run-read-broker-study.sh`](../../scripts/run-read-broker-study.sh)
builds and runs the existing experimental broker-plus-sandbox study from an
immutable source revision. It does not edit, reset, stash, or clean the caller's
checkout. The default source is the merged #221 baseline
`5fb44dc7e0c161d6010628eb4017d5c88b33e097`; a selected newer commit must include
that baseline. The runner extracts the selected commit with `git archive`, so
local modifications and untracked files are excluded from the study source.
The runner's own SHA-256 is recorded separately.

## Prerequisites

- Linux amd64 or arm64, a regular (non-root) user, Git, Bash, Python 3, GNU
  coreutils/findutils (`tar`, `sha256sum`, `stat`, `date`, `find`, `sort`,
  `xargs`), and Go 1.26.10 or newer.
- A system-installed Bubblewrap executable at an explicit absolute path. The
  default is `/usr/bin/bwrap`. Bubblewrap 0.9.0 is supported by feature
  detection in the merged study; a newer version is not required.
- The installed Bubblewrap must satisfy the study's existing root-owned,
  non-writable executable trust check, capability probes, and required
  namespace setup. The runner does not weaken those checks, change host
  settings, install packages, or retry without isolation.
- Enough free temporary storage for a static study binary, its bounded worker
  snapshot, and the small synthetic fixture set. No real credentials,
  provider transcripts, databases, or backups are read.
- The runner preserves the installed Go command's normal toolchain selection
  and records `go version`; it does not force an older local toolchain when the
  repository's `toolchain` directive selects a newer installed toolchain.
- Build and study subprocesses receive clean, allowlisted environments; the
  runner does not serialize the caller's environment into the archive.

Run from a normal host terminal as the regular user:

```sh
/home/ayesh/projects/ayeshLK/komposyn/scripts/run-read-broker-study.sh
```

The script prints the absolute results directory and compressed archive path.
To select a trusted backend path explicitly:

```sh
/home/ayesh/projects/ayeshLK/komposyn/scripts/run-read-broker-study.sh --bwrap /usr/bin/bwrap
```

To test a newer source revision that contains the baseline:

```sh
/home/ayesh/projects/ayeshLK/komposyn/scripts/run-read-broker-study.sh --revision FULL_COMMIT_SHA
```

To choose a new output directory, pass an absolute path that does not already
exist. Existing result or archive paths are never overwritten. All result
directories are private to the invoking user. Temporary build files are
removed on exit; the evidence directory and archive are retained.

## What it records

The archive includes:

- `manifest.json`: source/baseline commit, clean archive method, runner hash,
  bounded host metadata, Go and Bubblewrap provenance, authoritative trust and
  capability observations from the study when available, effective launch
  policy, build/run exit statuses, durations, timeout state, and executable
  hash.
- `study-report.json` when the CLI produced valid study JSON.
- Bounded stdout/stderr logs and capture-status JSON for Go version, build, and
  study execution. Each stream is capped at 2 MiB; subprocesses have finite
  deadlines. The capture helper becomes a Linux child subreaper, tracks adopted
  descendants (including detached sessions), sends `SIGTERM`, escalates to
  `SIGKILL`, and bounds output draining. It reports incomplete cleanup or
  undrained output explicitly instead of waiting forever or claiming the child
  tree was joined.
- `summary.md`, per-file `SHA256SUMS`, and a separate archive SHA-256 sidecar.

The runner does not save the full environment, process command lines, shell
history, credentials, or unrelated host information. The study report may
include the selected Bubblewrap path and sanitized host restriction metadata.
Review the archive before sharing it.

## Reading outcomes

`manifest.json` distinguishes source/setup refusal, Go-version refusal, build
failure, timeout, study failure, missing report, malformed report, and the study's own
`availability`. Backend probe classification follows the study's explicit
trusted flag and error fields. An unavailable report after successful trust
validation is not labeled as a pre-probe refusal.

If the Bubblewrap owner is UID 65534 or another trust
restriction fails, the study records the refusal and skips backend probes,
worker startup, and fixture operations. A refusal in a restricted agent
launcher is not evidence that the host itself cannot run the study; rerun the
copyable command from a normal host terminal if that is the only blocker. Do
not bypass UID mapping or executable trust checks.

An exit status of zero is not an enforcement pass. Use each case's status,
worker startup, descriptor transfer, read, publication, and fixture effect.
`unavailable`, `not_executed`, setup refusal, timeout, missing report, and
malformed report are not passes. Even a successful baseline report does not
cover the integrated independent-actor, protected-owner rotation,
mount-boundary, parent-death, or cleanup scenarios listed as native gaps in the
report. It does not establish shell safety, production readiness, or
acceptance of any filesystem tool.

The runner finalizes the partial manifest, summary, checksums, and archive
after `SIGINT`, `SIGTERM`, or `SIGHUP`, then exits with the corresponding signal
status. Further signals are ignored while finalization is in progress. Archive
and checksum-sidecar creation uses exclusive links, and pre-existing files or
symlinks at either output path are preserved.

Run disposable regression checks for detached pipe holders, output bounds,
preflight classification, sidecar collisions, and interruption finalization:

```sh
python3 scripts/test-read-broker-study-runner.py
```

`SHA256SUMS` validates the collected files; validate the archive itself with
the adjacent `.sha256` file before extracting. Share the archive privately
with reviewers or attach a reviewed, sanitized summary to issue #215. Do not
publish host-specific raw diagnostics without reviewing them.
