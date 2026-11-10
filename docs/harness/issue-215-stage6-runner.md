# Issue #215 Stage 6 integrated runner


**Reviewed milestone status (2026-11-10):** [#215 is closed within read scope](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6771210816).
The [snapshot report](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6770069844)
and [descriptor report](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6771185536)
cover the merged read source tree with distinct worker-image profiles. See the
[closure record](issue-215-completion-tracking.md) for exact provenance and
remaining limitations. This update records reviewed issue evidence; it does
not rerun or newly verify the archives.

This runner is an experimental acceptance fixture and does not connect to a
real provider. By default, it uses a test-only `read_file` runtime composition
and the stripped Stage 6 test binary as the worker. The optional
`--include-shipped-worker` profile also builds the exact source revision's
`cmd/harness` executable and launches that executable as the native worker
through the production bootstrap. Both modes use only temporary synthetic
auth/settings stores and file contents; neither validates the interactive CLI
or a real provider.

## Reproduction

Run from a clean Linux checkout using a regular user, an already-installed Go
1.26.10 or newer toolchain, and the reviewed absolute Bubblewrap executable.
Put that toolchain first on `PATH`; the runner uses `GOTOOLCHAIN=local` and will
not download or switch toolchains. If the default `go` launcher selects a
toolchain from `GOTOOLCHAIN`, resolve its installed `GOROOT` once and verify
the binary before launching the runner. The command requires an exact clean
`HEAD`; it archives that commit into a private temporary source snapshot and
refuses to label dirty source as a commit.

```sh
go_root="$(go env GOROOT)"
"$go_root/bin/go" version
PATH="$go_root/bin:$PATH" python3 -B scripts/run-issue215-stage6.py \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --output-root /tmp
```

By default the study uses feature-detected automatic mode selection. To
measure a specific supported mode, request it explicitly:

```sh
PATH="$go_root/bin:$PATH" python3 -B scripts/run-issue215-stage6.py \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --bind-mode file-snapshot \
  --output-root /tmp
```

`--bind-mode` accepts `auto`, `ro-bind-fd`, or `file-snapshot`. An explicit
request is still gated by the trusted backend inspection and its bounded
capability probe; if the selected Bubblewrap lacks a requested feature, the
case is unavailable and no worker starts. There is no fallback to a different
mode. This selector is compiled only into the disposable Stage 6 test overlay;
normal launcher selection and the shipped executable are unchanged. The
manifest records both the requested mode and the mode actually selected.

To run the Stage 6 runtime/provider fixtures with the source-built shipped
executable as the sandbox worker, use the sealed snapshot profile:

```sh
PATH="$go_root/bin:$PATH" python3 -B scripts/run-issue215-stage6.py \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --include-shipped-worker \
  --bind-mode file-snapshot \
  --output-root /tmp
```

This option builds `./cmd/harness` from the same clean source archive, records
its size and SHA-256, and requires the launcher to verify that digest again on
the sealed snapshot before Bubblewrap receives it. The exact `cmd/harness`
binary runs its normal `sandbox.WorkerBootstrap` entrypoint inside Bubblewrap.
The independent Stage 6 coordinator and fake provider remain the test parent;
the native profile therefore validates the actual command executable as the
worker, not normal interactive CLI startup or real-provider integration. The
option forces `file-snapshot`; descriptor mode is not used for this external
worker-image test. The fixed parent-death helper uses the Stage 6 test binary,
so `parent_death_cleanup` is explicitly unavailable in this profile rather
than being attributed to the shipped image.

Confirm the printed version is Go 1.26.10 or newer before proceeding. If
`go env GOROOT` resolves to an older base installation, set `go_root` to the
`GOROOT` of an already-installed compatible toolchain. Do not rely on runner
toolchain auto-download; native execution must remain tied to the verified
local toolchain and exact source revision.

The runner refuses root, non-Linux hosts, unsupported architectures, relative
backend paths, dirty checkouts, and a revision other than the current clean
`HEAD`. It does not edit the checkout. It builds with `CGO_ENABLED=0`,
`-ldflags=-s -w`, and a separate temporary Go build/module cache, then executes
the test binary with a finite deadline. With `--include-shipped-worker`, it
also builds the release-shaped command image with `CGO_ENABLED=0`, `-trimpath`,
and stripped ELF sections before the test run. The linker flags omit debug and ELF
symbol sections from this disposable study binary; the test image remains
statically linked and is authenticated by the existing worker launch checks.
The Stage 6 runs recorded below predate PR #264's 14 MiB image ceiling and
16 MiB worker root. The existing bounded process-group capture helper
terminates and reaps children on timeout; no unrestricted/elevated retry is
attempted.

The 2026-11-09 trusted-host run built a 7,952,946-byte test executable, larger
than the then-current 7,340,032-byte snapshot bound, and setup correctly
refused it. Rebuilding the same study source with `-s -w` produced a 5,492,898-
byte static binary. The runner retained the stripped build. PR #264 later
raised the bound to 14 MiB and the root tmpfs to 16 MiB so the release-shaped
chat executable can fit. This historical test-image correction is not native
evidence for that larger production image or its changed profile.

The Go build uses a private temporary `HOME` and Go/module caches, the public
Go module proxy with Go authentication disabled, and no host `.netrc`, proxy
credentials, Go configuration, or Git credentials. Direct VCS fallback is
disabled for module downloads. A cold private module cache still requires the
public module proxy and checksum database; that is dependency retrieval, not
provider traffic or live inference.

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
  host/Go/backend metadata, study-binary hash and size, optional
  `cmd/harness` worker-image hash/size/build status, bounded process status,
  and overall classification;
- `stage6-report.json`: per-case observations from the test binary when
  produced, including bounded, path-redacted worker startup diagnostics and
  the parent-death helper's bounded stderr/exit status when a worker does not
  reach readiness;
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
refusals, incomplete required-owner inventory admission, captured-batch
revocation, and pre-/post-handoff owner rotation using the actual Service and
runtime gate. A synthetic once-authority case verifies one explicit approval,
one execution, the approval/consumption revision transitions, and no reusable
grant. The owner-inventory case verifies that missing auth/settings routes and
identities block admission before approval or worker state. The
provider-source case confirms that this composition refuses registration
because it does not own a source lifecycle; it does not exercise approved
source disclosure or rotation. Its Stage 6 wrapper implements the full runtime
read execution interface; a synthetic forwarding test exercises that interface
independently of Bubblewrap availability.

The runner separately tests the fixed actor re-exec helper with a disposable
fixture, then has one native-integrated case release independent same-user
actors after final target revalidation. The actors move the target parent and
selected root, then replace the final target pathname. The worker is expected
to return bytes from the retained opened object, which is consistent with
object-authority B; this test does not claim strict live-path containment A.

A second native-integrated lifecycle case uses the fixed capability-hold worker
profile. It cancels after the worker has acknowledged the read descriptor but
before read release. The returned operation outcome may be `cancelled` when the
join is already confirmed, or `unknown` when cleanup remains pending; a later
join must not rewrite an `unknown` operation outcome. The test uses a separate
bounded service shutdown to confirm eventual join, then checks that the read
never starts, no result is published, and the provider receives no continuation
request. A synthetic delayed-`Wait` regression runs without Bubblewrap so this
cleanup behavior remains covered when native prerequisites are unavailable.
That control is `TestStage4CancellationBeforeHeldReadReleasePreservesUnknownUntilDelayedJoin`
in the ordinary sandbox test suite; it is synthetic lifecycle evidence and is
not counted as a native Stage 6 case.
This does not cover cancellation after release, parent death, or every cleanup
failure mode.

A separate `native-worker-lifecycle` case exercises parent death for the fixed
held-read Bubblewrap profile. A short-lived coordinator starts the same test
binary as the fixed worker, waits for startup probes and the
`CAPABILITY_HELD` acknowledgement, then reports Bubblewrap and namespace-init
identities over a private pipe. The supervisor opens verified pidfds, kills
and joins that coordinator without sending read release, and requires both
observed processes to exit and disappear from host procfs within bounded
deadlines. The report contains only booleans, never the local PIDs. This
demonstrates cleanup for the observed processes in this exact profile; it does
not prove cleanup of arbitrary descendants, naturally hung workers, production
coordinator behavior, or every failure mode. If backend trust or pidfd
observation is unavailable, the case remains unavailable. The reviewed
descriptor-mode report measured this fixed study profile; the shipped-image
parent-death case remains unavailable. This profile-only fixture does not model runtime grants or approval; it transfers
only one synthetic descriptor after worker readiness and deliberately does not
release it for reading. Its report includes its own exact hold-mode launch
arguments, separate from the ordinary Stage 6 worker profile.

A third native-integrated case records the actual selected Bubblewrap argument
profile, probes the worker's seccomp/FD/environment restrictions, and reads a
synthetic file larger than the fixed 64 KiB cap. It requires a truthful partial
outcome with no published or provider-disclosed content. The report continues to
state that aggregate cgroup quotas are not provisioned or claimed; this case does
not run exhaustion tests. It only measures the selected bind mode in that run,
so each mode that is offered still needs its own trusted-host run.

The cancellation report's operation outcome records the result observed before
the independent cleanup drain. Its final `worker_joined` and `cleanup_confirmed`
fields record the later bounded shutdown result; they do not imply a successful
read or reclassify an unknown operation.

Reports classify the actor helper as `synthetic-actor-fixture`, runtime controls
as `synthetic-runtime-composition`, worker cases as `native-integrated`, the
fixed-profile parent-death case as `native-worker-lifecycle`, and unimplemented
cases as `not-implemented`. Additional namespace actor cases, post-release
cancellation, aggregate cgroup quotas, mutation, shell, provider-source
lifecycle/positive authorization, non-Linux enforcement, and production
readiness remain unproven. A host trust refusal means none of the worker-path
cases ran.

In `--include-shipped-worker` mode, each native-integrated read case uses the
exact-digest sealed `cmd/harness` image; the report identifies that worker
image and its digest without recording its temporary path. Parent-death
cleanup remains unavailable for that mode, and the runner does not claim that
the complete production CLI process or provider transport was exercised.

Every case is explicitly classified; unavailable native cases are not
conflated with cases that this runner revision does not implement. The report
currently contains five synthetic-runtime controls, one actor-helper fixture,
twelve native-integrated cases, one native-worker-lifecycle case, and two
unimplemented cases.
