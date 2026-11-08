# Issue #215 Stage 6 integrated runner

This runner is an experimental acceptance fixture. It does not register or
ship `read_file`, does not connect to a real provider, and uses only temporary
synthetic auth/settings stores and file contents.

## Reproduction

Run from a clean Linux checkout using a regular user, Go 1.26.10 or newer, and
the reviewed absolute Bubblewrap executable. The command requires an exact
clean `HEAD`; it archives that commit into a private temporary source snapshot
and refuses to label dirty source as a commit.

```sh
python3 scripts/run-issue215-stage6.py \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --output-root /tmp
```

The runner refuses root, non-Linux hosts, unsupported architectures, relative
backend paths, dirty checkouts, and a revision other than the current clean
`HEAD`. It does not edit the checkout. It builds with `CGO_ENABLED=0`, a
separate temporary Go build/module cache, then executes the test binary with a
finite deadline. The existing bounded process-group capture helper terminates
and reaps children on timeout; no unrestricted/elevated retry is attempted.

The native cases run the authoritative Bubblewrap trust and capability
inspection before any backend probe. If the backend is untrusted, missing, or
unsupported, those worker cases are reported unavailable and no worker is
launched. Separate synthetic-runtime controls verify that ordinary chat still
works without a root/backend and that a provider-requested read is refused
before approval, worker creation, or continuation disclosure. The report marks
those controls as `synthetic-runtime-composition`; they are not native
enforcement evidence. The runner records filesystem metadata for the backend
but does not use that as a substitute for the Go harness trust check.

## Results

The runner prints a private results directory and a `.tar.gz` archive path.
The archive contains:

- `manifest.json`: source revision/cleanliness, runner and overlay hashes,
  host/Go/backend metadata, bounded process status, and overall classification;
- `stage6-report.json`: per-case observations from the test binary when
  produced;
- bounded build, test and Go-version stdout/stderr logs;
- `summary.md` and `SHA256SUMS`.

The process exit code is not an enforcement result. The runner exits `3` when
native evidence is unavailable, incomplete, or not classified as a measured
pass. Review case statuses and the manifest. A backend ownership refusal is
`native_unavailable`, not a pass. Malformed or missing structured reports are
recorded distinctly. Archives and binaries belong in the temporary output,
not in source control. Share the reviewed archive with issue #215 only after
checking that the manifest contains the intended revision and no private
material.

The test binary includes no-root text chat and attempted-read refusal controls,
explicit once-approved exact read plus same-context replay, empty-file read,
synthetic provider-source registration refusal, synthetic auth/settings
refusals, captured-batch revocation, and pre-/post-handoff owner rotation
using the actual Service and runtime gate. The provider-source case confirms
that this composition refuses registration because it does not own a source
lifecycle; it does not exercise approved source disclosure or rotation. Its Stage 6
wrapper implements the full runtime read execution interface; a synthetic
forwarding test exercises that interface independently of Bubblewrap
availability. Reports distinguish these synthetic-runtime controls from
native-integrated cases. Other Stage 6 cases remain explicitly unavailable or
not implemented in this runner revision. A host trust refusal means none of the
worker-path cases ran. The runner does not establish provider-source owner
coverage, independent namespace actor cases, native cancellation/cleanup,
aggregate cgroup quotas, mutation, shell, non-Linux enforcement, or production
readiness.
