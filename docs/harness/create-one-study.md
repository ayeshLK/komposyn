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
That does not show that a same-user process outside the sandbox cannot reach
the worker descriptor through the host's `/proc/<pid>/fd`. Linux gates those
entries with ptrace-style access checks affected by credentials, user
namespaces, capabilities, and active LSMs; the study must measure the actual
cross-process link attempt rather than assume the private PID namespace blocks
it. See [`proc_pid_fd(5)`](https://man7.org/linux/man-pages/man5/proc_pid_fd.5.html)
and [`ptrace(2)`](https://man7.org/linux/man-pages/man2/ptrace.2.html). A
protected owner can also race release, and a post-publication alias transition
is untested. Direct `O_CREAT|O_EXCL` create-then-write remains a negative
control: its name exists before the write and a same-user actor can hard-link
it into the synthetic protected directory during that interval.

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
| Mounted-object movement and replacement | Native O_TMPFILE parent and root relocation rows on ext4 mount ID 33 | Both affected only the retained admitted object; former-path replacement stayed untouched |
| Competing destination and symlink at publication | Native no-replace collision and symlink rows | Both refused publication, preserved the existing object, discarded the unnamed stage, and reaped worker and actor |
| Direct-create late alias hazard | Native direct-create control exposed the payload through an actor-created hard link | Observed violation; create-then-write is unsuitable for protected-owner safety |
| Unnamed stage stays unpublished before `linkat` | Native staged checkpoint checked the leaf and protected alias before publication | Passed for the synthetic actor; does not cover host proc-fd inspection |
| Same-user outside actor reaches staged inode through host `/proc/<pid>/fd` | Follow-up native actor attempts no-replace publication and, if that is denied, reopens the staged descriptor and changes one payload byte | Added after the recorded packet; native result required before assessing this boundary |
| Protected-owner rotation, protected parent/alias transition and complete owner coverage | No production owner integration exists in this study | Missing; blocks eligibility |
| Cancellation before release and after staging but before publication | Native staged cancellation and cleanup rows | No destination was published and the unnamed stage was discarded; production revocation ordering remains missing |
| Cancellation after publication, worker/coordinator death, delayed join and resource retirement | Unknown-outcome classification and direct worker wait; no delegated cgroup/tree-kill boundary | Incomplete; never replay uncertain effects |
| Exact UTF-8 payload and 64 KiB limit | Worker has one fixed 25-byte UTF-8 fixture | Variable payload validation and limit boundary not exercised |
| Partial, zero-byte, unsupported filesystem, publication failure and cleanup | Native direct-create partial cases; O_TMPFILE stage-write failure and no-replace cleanup | Staging failure left no named leaf and discarded the unnamed inode; unsupported filesystems and durability were not tested |
| Filesystem provenance for the actual synthetic directory | Native report records `fstatfs` ext4 magic and `statx` mount ID 33 matched to `/proc/self/mountinfo` | Established for this fixture and host only |
| Model/provider/UI/runtime behavior | Deliberately not implemented or exposed | Remains unavailable by design |

Go tests only validate the fixed worker, report classification, and fixture
construction. They do not establish Linux filesystem enforcement. The study
does not verify production scope selection, once-grant consumption, owner
coverage or rotation, cancellation/revocation synchronization, provider
destination binding, stale publication suppression, process-tree cleanup,
crash durability, or behavior on other filesystems/platforms.

## Historical and candidate native evidence

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

An earlier local attempt used implementation commit
`8fdd9435870e6b90e4cb83c7169878bb0e3a2bb8`. Its unavailable report is
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

That earlier local attempt used a checkout under `/tmp`; Bubblewrap had the
same binary hash but was owned by `nobody:nogroup`. `ValidateBackend` correctly
refused it, so no native scenario ran. No ownership, privilege, or host setting
was changed. This unavailable attempt is distinct from the trusted-host run
below.

The trusted-host archive
`/tmp/komposyn-issue-319-native.tkioSL.tar.gz` has SHA-256
`b927545ffd59d9a2ff1640968a2c4625d1068e6732923d65ccfb30c669f69c59`. It
contains `report.json` with SHA-256
`b873241c17db76cf9d8bcfc1ad8bfbc671dfb73ae480fad41fa35584e6dae5f7` and
`manifest.txt` with SHA-256
`7e86c9397c842a161a16107c377df380ef4777767657ff4c9066ec3335e1668e`; the
manifest's report hash matches the extracted report. It records source commit `609f5343b0f8c449f0ade43506f6ceebf87e0da8`, which contains
the tested study implementation from `8fdd9435870e6b90e4cb83c7169878bb0e3a2bb8`;
subsequent changes were only capture-script/documentation changes. The run used
Go 1.26.11, Ubuntu 24.04.5, Linux `7.0.0-42-generic` amd64, uid 1000 without
root execution, and Bubblewrap 0.9.0 owned by root with mode 755 and SHA-256
`e318903862396f96de3df57264e0158682b952fd3fb53ac23d876413e7b30f71`. The
actual fixture directory was ext4 (`0xef53`), mount ID 33. The runner exited 0.

All 16 issue-319 rows are accounted for: 12 `measured_pass`, one successful
O_TMPFILE publication marked `measured`, two deliberate lost-completion rows
marked `unknown`, and one `observed_violation` in the direct-create
create-then-write control. All workers were reaped. The violation is a useful
negative result: the late hard link exposed payload bytes through a protected
alias. It rules out direct named creation followed by writing for this
candidate contract. O_TMPFILE publication, no-replace collision and symlink
refusal, parent/root relocation, pre-publication invisibility, cancellation,
and failed-stage cleanup worked in the tested synthetic cases. The two
`unknown` cases intentionally lost completion; the harness preserved the
unknown classification and did not retry.

This packet predates the host-proc-fd actor probe added in the current branch.
It does not cover that probe. The previous case results still describe the
unchanged test rows; recapture native evidence from the current clean branch
before using it to assess descriptor isolation.

The current probe code is commit `9087d12bfc775f7ea86985cb5aba8028919ad742`.
The wrapper built it successfully, but its local native attempt exited `3`
because this environment's Bubblewrap is owned by `nobody:nogroup`; the trust
check stopped the run before any native scenario. Its unavailable archive is
`/tmp/komposyn-issue-319-native.FR14mJ.tar.gz` (SHA-256
`4c47032d63d02f0869282731610cb9b855980a2fe9cef3afde82b65f798d36b3`), with
report SHA-256
`7c938983d79da1c1bf9a6bc72934e9c59aa59cb1a84a2c7676784fa574637cd4` and
manifest SHA-256
`13eecb76f4dfe7a6acd7aa869ce72bf8313e991afdab923d7841e0efe843ef56`. This is
an unavailable preflight capture, not evidence about host proc-fd access.

After correcting worker discovery, the local wrapper built source
`b125b58c4402c13d02bc73b6431ddb8169771936` but again stopped before native
scenarios because local Bubblewrap ownership is `nobody:nogroup`. Archive
`/tmp/komposyn-issue-319-native.ry6vrv.tar.gz` has SHA-256
`efdfaa495762871d4e7a104945d80f284365a7a4fa926c30d75360cbe4b86b72`; report
SHA-256 is `7e9fda88ba26480876d680f2a3d6b19a639ce04c18b8265c97b8519b40b37486`.
This is another unavailable preflight, not native evidence for the corrected
probe.

The first trusted-host run with the host-proc-fd probe used source commit
`2cd86bee281233fce36924975c51196121dc6ce5`. Archive
`/tmp/komposyn-issue-319-native.xFmmLa.tar.gz` has SHA-256
`d34051395e618752453320d2f5613187262b9f4e984f42a133496034b9dc76d5`, and its
report SHA-256 is
`0e309aad3e8ea98e5f35adb787930ea95d4e85468195894fe6d60b4bfcf6aab2`. The
manifest report hash matches the report. The new probe row is `unavailable`
(`staged-fd-not-found`), not a pass. The probe selected Bubblewrap's
intermediate PID-namespace reaper rather than the worker, so that row says
nothing about host access to the staged descriptor. The other study rows and
their host provenance remain recorded as captured; this archive must not be
relabeled as evidence for the corrected probe.

A second trusted-host run used commit
`029b337db2de1906fafe34462cacc39ba39d85d7`. Its archive,
`/tmp/komposyn-issue-319-native.vyFs95.tar.gz`, has SHA-256
`7d70adacccd096aebc373f1d5c3628af7175e777888dd0ee8b3f5a0f5cd9acb1`, and its
report SHA-256 is
`2b9e43436e42e8b34e1d87e732a1d2ce25f577b123bd65036330c7a54c5f0bb5`; the
manifest hash matches. The proc-fd row remains `unavailable` with
`staged-fd-not-found`. Since this version selected by argv token alone, it may
have selected a launcher/reaper carrying the worker argument. The next probe
revision additionally requires `/proc/<pid>/exe` to resolve to the mounted
`/worker` executable. Neither trusted-host run establishes descriptor access.

This is evidence for the candidate mechanism on one ext4 host, not production
acceptance. Unsupported filesystems, other mount configurations, access to the
worker descriptor through other process-inspection routes, protected-owner
rotation/coverage, production approval and revocation synchronization,
post-publication races, crash durability, and process-tree/cgroup guarantees
remain unproven. Other issue-215 report rows also remain unavailable; a runner
exit of 0 does not turn those into passes.

## Reproduction on a trusted Linux host

The candidate includes the host-proc-fd actor probe. Copy
`/tmp/komposyn-issue-319-creation-feasibility.bundle` to
the trusted host, then run these checkout commands once:

```sh
mkdir -p "$HOME/projects/samples/komposyn-issue-319"
git clone https://github.com/ayeshLK/komposyn.git "$HOME/projects/samples/komposyn-issue-319"
cd "$HOME/projects/samples/komposyn-issue-319"
git fetch /path/to/komposyn-issue-319-creation-feasibility.bundle refs/heads/work/issue-319-creation-feasibility
git checkout --detach FETCH_HEAD
git merge-base --is-ancestor 8fdd9435870e6b90e4cb83c7169878bb0e3a2bb8 HEAD
```

Then run the study and send back the archive path it prints:

```sh
./scripts/run-create-one-study.sh
```

The script verifies a clean Linux checkout, builds the study, runs it with the
required `/usr/bin/bwrap`, and packages the JSON report and host/build hashes
into one `.tar.gz` archive under `/tmp`. Exit status 3 means prerequisites
prevented native execution; the archive is still produced and should be sent
back so the refusal can be reviewed. It does not change system ownership or
install anything. The script records the commit it actually runs, so verify
that it matches the candidate revision before interpreting results.

The native runner may still refuse a host whose Bubblewrap fails its trust
checks. Do not run as root or change Bubblewrap ownership to satisfy the
preflight.

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
review, not a supported or positively accepted mechanism.** The trusted-host
run supports the narrow O_TMPFILE publication behavior on ext4 and confirms
the direct-create late-hardlink hazard. It does not establish production
authorization or protected-owner safety. Unsupported filesystems, production
owner coverage/rotation, approval and revocation synchronization, descriptor
inspection by other actors, post-publication races, payload-limit validation,
process-tree cleanup, and crash durability remain open.

The immediate next step is to capture the corrected host-proc-fd probe on the
same trusted Linux host using the refreshed bundle and one-command wrapper.
If the actor can link or change the staged inode, record that as a candidate
violation and stop treating O_TMPFILE as safe under this profile. If access is
denied, the result is measured only for that host and still does not prove
protected-owner lifecycle or production authorization. Then independently
review the packet and decide whether to continue the candidate with its gaps
explicitly scoped or evaluate another unprivileged mechanism. Do not close
#319, unblock #321, or expose creation/editing based on this study alone. Keep
#320 editing research separate, and leave #315, #319, #321, and #267 open for
review.

## Validation

Go `1.26.11` was used. After correcting worker discovery, the following checks
passed:

- `go mod verify`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...` (Token Logger package completed in 258.576 seconds)
- `make check`
- Focused `go test ./internal/harness/sandboxstudy ./cmd/sandbox-study`
- Focused `go test -race ./internal/harness/sandboxstudy`

The first trusted-host packet's proc-fd row is unavailable because the probe
selected Bubblewrap's intermediate reaper. The bounded descendant lookup is in
commit `91ccf2ef512a551dd9b7bb7b36d29dcca4e488f2`; exact executable
verification was added in `11880381d9c5aefb703bcf7ab5bbccb0538cd2fd`. Capture
this probe from the refreshed clean source bundle before drawing a conclusion
about descriptor isolation. Ordinary Go and race checks do not establish
filesystem enforcement.
