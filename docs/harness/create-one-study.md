# One-file creation feasibility study (#319)

**Status: research only; creation stays unavailable.** This study does not add
or register a chat tool, production authorization, mutation-scope UI, or an
`apply_patch` implementation. The worker is a fixed synthetic experiment, not
a creation executor.

## Operation contract

A future one-file create request would be eligible only when trusted host
preparation has retained and bound all of the following:

- The selected mutation-scope object and the approved parent directory object
  beneath it, opened without symlink, magic-link, or mount-crossing resolution.
- One absent destination leaf name relative to that retained parent. The
  target is not an existing object: creating a new inode is authorized under
  that exact parent and leaf pair.
- The exact supported UTF-8 payload, at most 64 KiB, plus action/resource,
  provider destination, scope and policy generations, and a once-approval
  receipt for those same values.
- Complete protected-owner coverage for credentials/auth, settings,
  provider-owned source files, and owned Token Logger database, journal,
  WAL/SHM, and backup objects. Unknown or incomplete coverage makes the
  candidate ineligible; this study must not read real provider data to build
  that coverage.

The model may suggest a relative path and text. It cannot supply authoritative
identities, digests, generations, owner coverage, or a receipt. Trusted host
preparation must validate the schema, resolve the path beneath the selected
scope, open and retain the parent, verify the leaf is absent, gather owner
protection, and bind all effective values to approval. The `operationGate` in
this package only models a small part of release ordering. It is not production
runtime integration and does not establish that the values above are retained
or consumed atomically.

The proposed effect point is the successful no-replace `linkat` that publishes
the fully written unnamed inode under the retained parent descriptor and
approved leaf. An ordinary rename of the parent or selected root does not
revoke mounted-object authority: publication should affect the retained
directory object, while a replacement at the former pathname must remain
untouched. A separate policy/scope generation change, owner registration or
rotation, cancellation, service closure, or failed worker cleanup must block
release or stale publication through a production synchronization protocol.
That protocol is not part of this study. Once publication succeeds, later
cancellation cannot undo it; a lost completion is `unknown` and must never be
replayed automatically.

Before publication, a failed or short write to an unnamed inode has no named
destination if the worker closes its final descriptor. Failure after direct
named creation may leave an empty or partial file and is `partial`. Failure at
publication must preserve an existing destination. A coordinator or worker
failure after publication may leave an effect whose completion is unknown.
The study does not claim fsync durability, rollback, or whole-operation
atomicity. `linkat` atomically adds a directory entry, but the current worker
does not fsync the file and parent for crash durability.

## Candidate mechanism

The fixed worker opens the approved parent beneath `/scope` using `openat2`
with `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|RESOLVE_NO_XDEV`.
It stages the exact synthetic bytes with `O_TMPFILE|O_WRONLY|O_CLOEXEC` and
mode `0600`, without `O_EXCL`: Linux documents that `O_EXCL` with `O_TMPFILE`
prevents later linking. It writes before publication, pauses at a deterministic
`STAGED` checkpoint, and then uses `linkat` from `/proc/self/fd/<fd>` with
`AT_SYMLINK_FOLLOW` to the relative leaf and retained parent descriptor. That
proc mount exists only in the study's explicitly named
`otmpfile-private-pid-proc` profile, inside the already unshared PID namespace.
The other rows do not mount proc. There is no fallback to direct create,
absolute-path publication, a host proc mount, or an unrestricted worker.

Linux documents that `O_TMPFILE` creates an unnamed inode that is discarded
when its last descriptor closes unless it is linked, that a proc-fd path with
`AT_SYMLINK_FOLLOW` can publish it without `CAP_DAC_READ_SEARCH`, and that only
some filesystems support the operation. The new destination is relative to the
directory FD; `linkat` does not overwrite an existing entry, so a collision or
symlink at the leaf must fail. These API semantics do not prove production
authorization or owner synchronization. See the Linux [`open(2)` manual](https://man7.org/linux/man-pages/man2/open.2.html)
and [`linkat(2)` manual](https://man7.org/linux/man-pages/man2/linkat.2.html).

The no-replace publication narrows the direct-create race: the new inode has
no ordinary name while bytes are written, and a same-user actor cannot create
the direct late-hardlink alias through the approved leaf before publication.
This observation does not prove an actor cannot reach the worker descriptor
through another process-inspection route, that a protected owner cannot race
the release, or that a post-publication alias transition is prevented. Those
remain explicit gaps. Direct `O_CREAT|O_EXCL` create-then-write remains a
negative control: its name exists before the write and a same-user actor can
hard-link it into the synthetic protected directory during that interval.

The candidate uses no shell, network, provider connection, credential or grant
store, unrestricted host filesystem handle, or general executor. Bubblewrap
setup refusal, unsupported `O_TMPFILE`, absent procfs, and `linkat` failure are
`unavailable` or typed pre-publication failure, never success and never a
weaker fallback. The study imposes a five-second worker deadline, 64 KiB
protocol-line ceiling, and 16 MiB private `/tmp`; it does not configure cgroups
or prove aggregate CPU, memory, or process limits.

## Acceptance coverage

| #319 requirement | Evidence in this branch | Disposition |
| --- | --- | --- |
| Bind parent, absent leaf, exact bytes, action/resource, provider destination, generations, once receipt | Contract specified above; existing study gate is only a model | Production preparation and approval binding missing; candidate ineligible |
| Mounted-object movement and replacement | Existing direct-create report covered parent movement; new native rows add O_TMPFILE parent and root movement after preparation | New rows not run in this environment |
| Competing destination, symlink and replacement at publication | New deterministic actor checkpoint before `linkat`; regular-file and symlink collision controls | Go behavior tests cover static collision; independent native race rows not run here |
| Direct-create late alias hazard | Historical native report observed the alias and payload exposure | Candidate-specific negative result for create-then-write |
| Unnamed stage stays unpublished before `linkat` | New staged checkpoint checks destination and synthetic protected alias absence | Not run natively here; does not cover `/proc` descriptor inspection by an actor |
| Protected-owner rotation, protected parent/alias transition and complete owner coverage | No production owner integration exists in this study | Missing; blocks eligibility |
| Cancellation before release and after staging but before publication | Existing pre-release model; new worker protocol has a staged cancellation case that joins the launcher and checks no published leaf | New staged native row not run; production revocation ordering missing |
| Cancellation after publication, worker/coordinator death, delayed join and resource retirement | Unknown-outcome classification and direct worker wait; no delegated cgroup/tree-kill boundary | Incomplete; never replay uncertain effects |
| Exact UTF-8 payload and 64 KiB limit | Worker has one fixed 25-byte UTF-8 fixture | Variable payload validation and limit boundary not exercised |
| Partial, zero-byte, unsupported filesystem, publication failure and cleanup | Direct-create partial cases; new injected partial-stage write failure, cancellation and no-replace cases | Historical candidate support was measured once; new rows await trusted native run |
| Filesystem provenance for the actual synthetic directory | New report records `fstatfs` magic and `statx` mount ID matched to `/proc/self/mountinfo` | New provenance instrumentation not yet captured in a native report |
| Model/provider/UI/runtime behavior | Deliberately not implemented or exposed | Remains unavailable by design |

Go tests only validate the fixed worker, report classification, and fixture
construction. They do not establish Linux filesystem enforcement. The study
does not verify production scope selection, once-grant consumption, owner
coverage or rotation, cancellation/revocation synchronization, provider
destination binding, stale publication suppression, process-tree cleanup,
crash durability, or behavior on other filesystems/platforms.

## Historical native evidence and current host

A retained report at `/tmp/komposyn-sandbox-study-report.json` has SHA-256
`fe5876710037fdbd78fd7058fba57098b50f10cdf8bd8d22da2f6561e3e7698a`. Its
reported source commit `80a1c4a99baab5095a8858d4b8efbf6292223341` is available
upstream and was fetched for source inspection. Its host fields report Go
1.26.11, Ubuntu 24.04.5, Linux `7.0.0-42-generic`, amd64, and root-owned
Bubblewrap 0.9.0 with binary SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`.
The report records direct create collision and symlink refusal, a direct-create
late-hardlink violation, and one successful unnamed write/publication using
the private-PID-proc profile. That successful O_TMPFILE case used only the
fixed 25-byte fixture. It does not include an archived capture packet or
manifest, does not report fixture mount-ID provenance, and does not cover the
new publication checkpoints, owner lifecycle, or cancellation cases. Treat it
as historical narrow evidence, not acceptance of this candidate revision.

The current candidate build is commit
`8fdd9435870e6b90e4cb83c7169878bb0e3a2bb8`. Its current-environment report is
`/tmp/komposyn-issue-319-native-unavailable-8fdd943.json` (SHA-256
`601825f7e093ec89409b25af37a771fbc2609a980b75a70e47979af9b7d99d76`), and
the packaged report plus manifest is
`/tmp/komposyn-issue-319-native-unavailable-8fdd943.tar.gz` (SHA-256
`1c8df713d26820526b965fac13747f91c6a591b666294bbecc0b985b5d31329c`). The
manifest SHA-256 is
`d800946119a6f88c8845a2558a23158c5ea571c103081858b594a22fdeebf51b`.
The report says `/usr` is not root-owned and marks the native suite
`unavailable/not_executed`; its only creation row is the preflight failure.
The study binary SHA-256 is
`a442cc4d3007ba2e6d7739d68668038c3f70310bef5b91911746e48349d987ef`.
This packet records a blocked attempt, not a native test pass or failure.

Earlier preflight-only reports remain distinct: for example,
`/tmp/komposyn-sandbox-study-319-followup.json` has SHA-256
`e71249fe47d812ad9953080d660c60a821ad0273651a826f33c2bac34bb503b0` and
records `unavailable/not_executed` because backend trust failed at `/usr`.
They are not negative filesystem results.

For this run, the clean checkout is under `/tmp`; Go is 1.26.11, and
`/usr/bin/bwrap` is version 0.9.0 with the same binary hash, but is owned by
`nobody:nogroup` rather than root. `ValidateBackend` correctly refuses it, so
no new native scenario ran. No ownership, privilege, or host security setting
was changed. Bubblewrap SHA-256 was
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`, but its
current owner is `nobody:nogroup`; a prior historical report's claim that the
same binary was root-owned does not describe this run. No fixture filesystem
or mount ID is available because no worker started.

## Reproduction on a trusted Linux host

After this branch is committed, export its commit as a bundle and copy that
bundle to the trusted host. For example, from this checkout run
`git bundle create /tmp/komposyn-issue-319-creation-feasibility.bundle work/issue-319-creation-feasibility`.
Replace the bundle path and `candidate_commit` below with the transferred
bundle and exact commit hash. The branch need not be pushed to GitHub.

```sh
set -eu
repo="$HOME/projects/samples/komposyn-issue-319"
candidate_bundle="/path/to/komposyn-issue-319-creation-feasibility.bundle"
candidate_commit="<candidate-commit>"
mkdir -p "$HOME/projects/samples"
if [ ! -d "$repo/.git" ]; then
  git clone https://github.com/ayeshLK/komposyn.git "$repo"
fi
cd "$repo"
git fetch "$candidate_bundle" refs/heads/work/issue-319-creation-feasibility:refs/remotes/study/issue-319-creation-feasibility
git checkout --detach "$candidate_commit"
test "$(git rev-parse HEAD)" = "$candidate_commit"
test -z "$(git status --porcelain)"
go version
id
stat -c '%U:%G %a %n' /usr/bin/bwrap
/usr/bin/bwrap --version
findmnt -T "$repo" -o TARGET,FSTYPE,OPTIONS
study_dir="$(mktemp -d "$repo/.creation-study.XXXXXX")"
report="$(mktemp /tmp/komposyn-issue-319-report.XXXXXX.json)"
trap 'rm -rf "$study_dir"' EXIT
GOCACHE=/tmp/komposyn-issue-319-gocache CGO_ENABLED=0 \
  go build -buildvcs=false -trimpath -o "$study_dir/sandbox-study" ./cmd/sandbox-study
"$study_dir/sandbox-study" --bwrap /usr/bin/bwrap --commit "$(git rev-parse HEAD)" > "$report"
sha256sum "$study_dir/sandbox-study" "$report"
jq -e --arg commit "$candidate_commit" '.host.repository_commit == $commit' "$report"
jq '.observations[] | select(.case | startswith("create-one"))' "$report"
```

The report records the filesystem magic and mount ID from the actual synthetic
parent descriptor. Review every creation row and its `worker_reaped`,
`actor_reaped`, and `unnamed_stage_discarded` fields. `unavailable`, missing
provenance, missing joins, or a mismatched fixture assertion is not a pass.
Build and report hashes alone do not authenticate the host; preserve the
operator's `id`, Go, backend ownership/version, kernel, and filesystem output
with the report. Do not run as root or change Bubblewrap ownership to satisfy
the preflight.

## Disposition

**Unresolved feasibility; O_TMPFILE remains a candidate for independent
review, not a supported or positively accepted mechanism.** Historical
evidence shows one narrow publication attempt succeeded and explains why it
avoids the direct-create late-write alias window. The new source adds targeted
native cases and path-free descriptor filesystem provenance, but this
environment cannot execute them. Even a successful trusted-host rerun would
not close protected-owner coverage, production authorization/revocation
synchronization, actor access through process descriptors, payload-limit
validation, worker-tree cleanup, or filesystem portability.

Next decision: obtain the checksummed native run from a trusted regular-user
host, independently review its new publication rows, then decide whether to
continue this candidate or record a candidate-specific limitation and evaluate
other unprivileged mechanisms. Do not close #319, unblock #321, or expose
creation/editing based on this study alone. Keep #320 editing research
separate, and leave #315, #319, #321, and #267 open for review.

## Validation

Go `1.26.11` was used. All repository checks passed on the committed source:

- `go mod verify`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...` (Token Logger package completed in 268.136 seconds)
- `make check`
- Focused `go test ./internal/harness/sandboxstudy ./cmd/sandbox-study`

The study binary built successfully. Its native command exited `3` after the
unchanged backend trust check refused `/usr`; Bubblewrap scenarios did not
execute. Ordinary Go and race checks do not establish filesystem enforcement.
