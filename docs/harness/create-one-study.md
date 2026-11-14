# One-file creation candidate study (#319)

This is a disposable native study only. It is not connected to the chat
runtime, does not register a tool, and does not change filesystem policy. The
study code is based on integration tip
`23d0b5877ede251b296c27d59cabe26622d6d253`. Build and run it on a trusted Linux
host with the repository's existing `cmd/sandbox-study` mechanism:

```sh
GOCACHE=/tmp/komposyn-study-gocache CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /tmp/komposyn-sandbox-study ./cmd/sandbox-study
/tmp/komposyn-sandbox-study --bwrap /usr/bin/bwrap --commit "$(git rev-parse HEAD)" > /tmp/komposyn-sandbox-study-report.json
findmnt -T /tmp -o TARGET,FSTYPE,OPTIONS
```

The runner requires a root-owned, non-group/world-writable absolute
`/usr/bin/bwrap` (or explicit `--bwrap` path), all requested user/PID/IPC/UTS
and network namespaces, and seccomp. Refusal is `unavailable`, never a pass or
fallback. The report records commit, OS, kernel, Go version, architecture,
backend identity/version where trusted, effective candidate profile and
scenario outcomes. Record the fixture filesystem from `findmnt` beside the
report when archiving evidence.

On the development host used for the initial attempt (Ubuntu 24.04.5, Linux
`7.0.0-42-generic`, Go 1.26.11, amd64, `/tmp` ext4), `/usr/bin/bwrap` was mode
0755 but UID 65534 owned; trusted backend validation stopped at `/usr` before
launch. This environment therefore produced no native scenario row. The
preflight report is `unavailable/not_executed`, not a positive or negative
enforcement result. A trusted host must record its actual kernel, filesystem,
architecture and backend owner from its own run.

The new typed worker cases use disposable synthetic bytes only. A separate
same-user actor process pauses at deterministic readiness checkpoints to move
the opened parent and install a replacement, insert a competing leaf or a
symlink, or create a late hard-link alias after direct `O_EXCL` creation but
before payload write. The direct candidate opens the parent beneath `/scope`
with `openat2` resolution guards, then uses one `openat` with
`O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC` for a single leaf. It never creates a
directory or overwrites an existing entry. Parent relocation is assessed under
mounted-object semantics: the retained descriptor continues to designate the
approved object, while a replacement at the old pathname is a different
object. Collision and symlink insertion must fail without following or
overwriting the actor's entry.

The late-alias row is intentionally expected to expose a limitation: after
direct creation returns and before content is written, an independent actor can
hard-link the new inode into a separate synthetic protected directory. The
subsequent payload write then changes the alias too. This makes
create-then-write unsuitable wherever protected aliases must remain excluded,
even though parent/leaf selection was bound for the create syscall.

The `O_TMPFILE` candidate keeps a new inode unnamed while writing all synthetic
bytes, then attempts no-replace publication with `linkat` from
`/proc/self/fd/<fd>` to the approved leaf. It uses a separate study-only
Bubblewrap profile that adds `--proc /proc` inside the already unshared PID
namespace. Every other row uses the original no-proc profile. This exposes only
the study worker's private PID namespace, not host `/proc`; it is not a
production profile or a fallback. If the private mount, `O_TMPFILE`, or proc-fd
publication is unsupported, the row remains unavailable. The runner does not
retry with a weaker path or call unavailability enforcement evidence.
Filesystem/kernel support and link permission requirements remain material;
only a trusted-host native run can evaluate them.
The corresponding report row labels this `otmpfile-private-pid-proc`; all
other rows label `default-no-proc`.

Injected partial content is reported as `partial`. A worker that creates and
writes a synthetic file but loses its result is reported as `unknown`; the
coordinator inspects the synthetic fixture only to describe the uncertainty
and issues no replay. Neither result claims rollback or atomic whole-file
publication. Native report rows remain unavailable until the trusted-backend
gate succeeds. Go tests validate harness code and classification only; they do
not establish native enforcement, protected-owner synchronization, or
model-facing safety.
