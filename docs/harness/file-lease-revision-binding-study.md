# Linux file-lease revision-binding study (#320)

Status: **study only**. This adds no chat capability and does not connect to
runtime authorization, tool dispatch, UI approvals, or provider publication.
The lease probes use disposable synthetic files and fixed same-user actor
operations. They are run by the existing `cmd/sandbox-study` executable, not a
parallel executor. The command runs these kernel-mechanism probes outside
Bubblewrap, then attempts the pre-existing sandbox suite separately. Lease
observations are not sandbox-enforcement evidence.

## Reproduce

Build and run on Linux with Go 1.26.11 or newer:

```sh
CGO_ENABLED=0 go build -buildvcs=false -o /tmp/komposyn-sandbox-study ./cmd/sandbox-study
/tmp/komposyn-sandbox-study --commit "$(git rev-parse HEAD)" > /tmp/komposyn-sandbox-study-report.json
```

The report includes kernel, architecture and backend metadata, separate lease
observations, and the Bubblewrap suite result. A nonzero exit caused by an
untrusted or unavailable Bubblewrap backend does not invalidate independent
lease rows; it leaves sandbox rows `unavailable` / `not_executed`. The study
does not retry outside Bubblewrap for sandbox scenarios. Each actor receives
only a fixed action and a generated mode-0700 fixture root. Marker files
provide bounded synchronization; actors are reaped or killed during cleanup.
The lease code reads no host content and makes no system-setting changes.

## Profile and observations

The reviewed run was built from source commit
`d03f588d694389d9b258a7c51eb827ece00bd0a4` plus the study-only changes in this
branch. It ran on Linux amd64, kernel `7.0.0-42-generic`, Go `go1.26.11`, with
`/tmp` on ext4. Bubblewrap was `/usr/bin/bwrap` 0.9.0, mode 0755, owned by UID
65534. Its trust preflight refused the executable path at `/usr`; the sandbox
suite recorded `unavailable`, `not_executed`, and skipped worker/fixture
operations. The direct lease observations below are not affected by that
preflight.

The report recorded:

- `preexisting-writable-fd`: an independent actor held an `O_RDWR` descriptor;
  `F_SETLEASE(F_WRLCK)` returned `EAGAIN`. After the actor closed it, lease
  acquisition succeeded. Both observations were `measured_pass` for the
  documented precondition.
- `retained-writable-mapping-blocks-lease`: after creating a writable
  `MAP_SHARED` mapping and closing its descriptor, the actor retained the
  mapping; `F_WRLCK` returned `EAGAIN` on ext4. The separate
  `mapping-write-under-lease` row is `unavailable` because a lease could not
  be established with that mapping present. After the refusal, the actor
  changed the synthetic file through the mapping (`mapping-write-without-lease`,
  `measured_pass`). This does not establish mapping behavior on other
  filesystems or a writable mapping created through another route.
- `late-hardlink-alias`: a same-user actor created a hard link while the
  lease remained `F_WRLCK`; this is an `observed_violation` for any proposal
  that relies on the lease to reject late aliases. A subsequent nonblocking
  open through the alias returned `EWOULDBLOCK` and put the lease into break
  state (`late-hardlink-open-break`, `measured_pass`).
- `in-place-partial-or-unknown`: `not_executed`. This bounded study did not
  inject a partial write or lost completion. File leases do not make an
  in-place write transactional; a known prefix effect remains partial, and a
  lost result after release remains unknown. Neither outcome permits automatic
  replay.

These results show a lease can reject acquisition while another descriptor or
the tested shared mapping is held, then block an open through a late alias
until a lease break is handled. They do not show revision-bound edit safety.
Late alias creation itself was not blocked. Lease break timeout, process
suspension/death, other filesystems, arbitrary mappings, parent/root changes,
protected-owner rotation, cancellation, and actual edit effects were not
accepted by this run. Do not infer exact-object editing, atomicity, rollback,
general sandbox enforcement, or production readiness.

The upstream semantics are documented in the Linux [`F_SETLEASE` manual](https://man7.org/linux/man-pages/man2/f_setlease.2const.html),
[`mmap(2)`](https://man7.org/linux/man-pages/man2/mmap.2.html), and
[`link(2)`](https://man7.org/linux/man-pages/man2/link.2.html). The lease
manual limits notification to conflicting open/truncate, requires no other
open descriptors for a write lease, and specifies forced lease break after
`/proc/sys/fs/lease-break-time`. The `MAP_SHARED` mapping test's lease refusal
is a measured filesystem-specific observation, not a claim derived from those
manual semantics.

**Disposition:** keep existing-file editing unavailable. Any future candidate
must bind the prior approved read revision and exact object at the effect
point, refuse hard-linked targets including concurrent alias changes, and
retain truthful partial/unknown outcomes. This study does not satisfy #267's
enablement gate.

## Validation record

`go mod verify`, `go vet ./...`, `go test ./...`, and `make check` passed on the
study branch. `go test -race ./...` produced package results through
`plugins/model-provider/openai` but then emitted no further output for more
than 5 minutes; the run was interrupted with status 130 as directed. Race
validation is therefore pending, not passed. The sandbox command exited 3
because its Bubblewrap trust check refused `/usr` (root ownership and
non-writable path-component requirement); its sandbox scenario remained
unavailable/not executed. The direct lease rows in that same report completed
independently.
