# Issue #321 native creation acceptance

This run tests a bounded, disposable fixture against one exact source commit.
It is not production enablement: the normal chat composition still does not
register creation, grant creation scope, or advertise its schema. The test uses
synthetic auth/settings/provider-source/Token Logger owner coverage and only
synthetic files. It makes no provider requests.

## Recorded native run

The accepted historical scoped run completed on 2026-11-20 against source commit
`39279e93080a2306f5ed1a702bd83ab852eb1055` with test overlay SHA-256
`560c845a55a0e52c4f76b8710407ce914113f168c41b14196432d47b0b66f5c4`.
The operator-retained archive is named
`komposyn-composition-native-results.e9sr9ic5.tar.gz` (archive SHA-256
`4a43e17e123e4fb6e2a0b55af42d276df7433092094ee0fb4b814e1b50047dfa`). Its
manifest SHA-256 is
`bdb9a10e800164cade52db33f9c3c73fe962511ba515c2ee64f7746b79159a0a`, and the
native report SHA-256 is
`d7547da9b89b3ad5bf56179d023bc7428e6ac05cabcc3858f5a2bd7dbcd920ec`.

Measured environment: Go 1.26.11, Linux 7.0.0-42-generic amd64, regular user
UID 1000, trusted root-owned Bubblewrap 0.9.0 mode `0755`, and the measured
`ro-bind-fd` launch mode. The creation fixture ran on ext4, mount ID 33.
All 10 recorded native cases passed, including exact created bytes and mode
`0600`, stage cleanup, incumbent and late-collision preservation, and worker
join. Unit checks, native build, native execution, and cleanup completed; no
work was retained. The per-case creation record SHA-256 is
`baf7151e28388d5296103784225096ae2432b55595c904365b98440102062645`.

This establishes only the fixed worker and test fixture on that host, ext4
mount, and measured Bubblewrap mode. It does not establish production owner
coverage, shipped tool availability, other launch modes or filesystems, other
platforms, or model-facing creation authorization. Separate explicit
maintainer review remains required before any production enablement.

## Native actor dispatch follow-up

The later baseline run used merged PR #347 source commit
`480f37a06b467455281f05c0f8d2ebeb55a63017`. Its retained archive was
`/tmp/komposyn-native-scratch.ZPKDWe/komposyn-composition-native-results.64hbmz2u.tar.gz`
(archive SHA-256
`d52b448a1e1e3248055516a849f757561e0bbedf64ae214785c531c313edb037`). The
manifest SHA-256 was
`d6521b6ecf4b2041bb668f04b978a505bfac032bf8a617dc1c3033fe879f0daa`, and the
native report SHA-256 was
`f8506ed6f54165634e414a35bed86db57712a313e9a426dfcfc0ccb95f095283`. The
overlay SHA-256 was
`cdf1712739ea9ea179550a6ee59ef53273afe9ad4e95cd2191d5d15948e65747`, and the
runner SHA-256 was
`088837dc7d2de35588a80959193b7a5d5b0f673a13f35154e6ef2aa855b4fa3d`. It
completed 10 of 11 cases; the retained-parent creation case was
`failed_or_incomplete`. The child process rejected
`-readbrokerstudy-actor` because the overlay's `init()` dispatched worker
bootstrap but not the fixed native actor mode. Cleanup was confirmed. This
report is a failure record, not evidence of the candidate fix.

The follow-up adds early dispatch for the exact fixed actor mode and a
Bubblewrap-independent `TestNativeActorModeDispatch`. That test starts the
compiled acceptance test binary with an empty environment, verifies READY,
releases one fixed directory move-and-replace action, and checks the retained
object identity, marker, transition acknowledgement, and joined child. It
also appears in the runner's expected case list. The test does not alter
production worker code or the existing creation contract. The focused test
passed in the development checkout. A full candidate native run is still
required on an eligible trusted-Bubblewrap host; this environment's
`/usr/bin/bwrap` is owned by `nobody:nogroup`, so it does not meet the host
trust requirement above. Do not change its ownership or permissions to obtain
a pass.

This correction establishes only that the exact acceptance test binary
dispatches its fixed actor mode before Go's testing argument parser. It does
not convert the failed baseline into a pass, prove the full native suite, or
change any production enablement decision.

The candidate runner completed on source commit
`8f0fde4cd580159e9433fe10c5711d2ae7101886`. Its archive is
`/tmp/komposyn-composition-native-results.23cx7fjs.tar.gz` (SHA-256
`fd1a4e269e9ce98a1eed3675832fae9e635298e1f29f0289a83cabe9e1711b37`), manifest
SHA-256 `2cdced99f2757edb33571c6607c4d10bd65cf69814e74e994ec91adf94ae5233`,
and native report SHA-256
`9b924d7e038b73d974df7d235d891e77efc52a39eeb9275eee1b8a3144e53615`. The
runner status is `native_unavailable`: the new actor-dispatch case and shipped
generic-read gate were measured passes, while the Bubblewrap-dependent cases
were unavailable. Unit checks, native build, process cleanup, and temporary
work cleanup completed. This validates the regression test on this candidate;
it does not replace the full trusted-host acceptance run.

## Trusted-host follow-up

The maintainer then ran the full candidate on the trusted host using source
commit `7f7807ce21365e5cf0bbe4fc26712c71573313e1`. Archive SHA-256:
`0d7169f618c98fa7ae6de89585011917bc2c3a3a1c348c3b0a5eb78e3e612deb`;
manifest SHA-256:
`a4280dd01e0ec863d9718ccd206759220a0941ec107a1cd070e7287f0264f6d1`;
native report SHA-256:
`ee63d3ee59c134c0f275a30208532bda583ee736fa7e24646e8ef0ade8898284`.
Environment: Go 1.26.11, Linux 7.0.0-42-generic amd64, regular UID 1000,
trusted root-owned Bubblewrap 0.9.0, `ro-bind-fd`, ext4 mount ID 33. Build and
unit stages passed; ten cases measured pass and two failed: the new actor
dispatch regression and the retained-parent move case. The runner reaped its
child, confirmed no remaining descendants, and removed temporary work.

This run exposed an additional constraint after the startup dispatch was
fixed. The study runner places fixtures under its configured ext4 scratch
directory, while `NewNativeActor` starts with an empty environment. The actor
therefore defaulted `TMPDIR` to `/tmp`; its existing private-temp-root check
refused the fixture under the separate scratch mount before READY. The
follow-up now supplies only `TMPDIR=<fixture-root-parent>` to the actor. It
continues to discard all inherited environment variables and credentials, and
retains the existing root validation and bounded actor protocol. Full native
acceptance must be rerun against the updated PR head; this failed archive is
preserved as evidence and is not a pass.

After that change, the candidate runner on commit
`8d872823fef76c11cc7fe229c3c6a85ef5eb60df` recorded
`TestNativeActorModeDispatch` and the shipped generic-read gate as measured
passes. The other ten worker cases were unavailable because this execution
environment's Bubblewrap is untrusted. Archive SHA-256:
`d58399b2fc7ebef0c681beb9f5e7bb30b5893727237e5f7ffbc3a48054a95c55`;
manifest SHA-256:
`2f9733a19298592583abf567d87b9dcd0e1263b51f18969cd6d02e45abc8b8ff`;
native report SHA-256:
`c8d76a261684c4605fe24b4d27a6389ae5e1b62dda4dc4a80af44c9ff45b6156`.
The runner reported `native_unavailable`, with process cleanup and temporary
work cleanup complete. This validates actor startup with the explicit fixture
`TMPDIR`; the trusted-host run remains necessary to recheck the retained-parent
case and full native matrix.

The maintainer reran the complete candidate on source commit
`dbfd90974f0979832a9abc60e32aeb3c87399840`. The archive
`komposyn-composition-native-results.ah0j_t7n.tar.gz` has SHA-256
`3d76449b95ba037db17d46b3404d36f398d993cd0af3c940904857be305236dc`.
Its manifest SHA-256 is
`1822336d698e55d533cc71a3a54738885e99fdaff64b81b0cb51e9c3173d9a02`, and the
native report SHA-256 is
`ac66529ef8a9a8e562505c5964d080d01aeb040122be804fabe86959b82f7ce6`.
Environment: Go 1.26.11, Linux 7.0.0-42-generic amd64, regular UID 1000,
trusted root-owned Bubblewrap 0.9.0, `ro-bind-fd`, ext4 mount ID 33. All 12
native cases, including actor dispatch and retained-parent creation, measured
pass; build, unit checks, process cleanup and temporary-work cleanup completed.
The runner status is `scoped_native_pass`. Some held-worker lifecycle records
report an unknown operation outcome after a deadline or wait error; the tests
pass by confirming result suppression, cleanup, and any required fresh retry.
This evidence remains limited to the fixed worker and tested host/profile; it
does not enable production creation or establish other filesystems, launch
modes, platforms, or arbitrary descendants.

## Fixed startup-scope candidate under review

The PR #354 baseline and accepted ADR-0009 require new evidence for the fixed
startup directory, owner-backed composition, nested-parent behavior, and
startup-root movement. The follow-up adds those cases to the isolated native
overlay and tightens result accounting; it does not change production worker
enforcement or register the creation tool. A trusted-host run of the exact
candidate is recorded below: 17 cases measured pass and the mount-boundary
case is unavailable because no existing nested mount fixture was supplied.
The runner correctly classified the aggregate as `native_unavailable` (exit
2), not a pass.

The added candidate cases are:

- `TestNativeServiceNestedParentSubstitutionRefusesPreparedProposal`: a fixed
  same-UID actor moves an existing nested parent and replaces its old route
  after preparation but before approved preparation. The report includes
  before/moved/substitute identity hashes, actor PID/UID, no-effect checks,
  stage disposition, and worker count.
- `TestNativeServiceScopeAndTargetRefusalsPreserveFixtures`: performs a valid
  nested creation first, then checks direct/nested escapes, navigation outside
  the fixed scope, absent and non-directory parents, symlinked parent/leaf,
  file/directory collisions, and an absent protected auth route. It checks the
  outside sentinel, absence of unintended effects, no worker launch for
  refusals, and no private stage.
- `TestNativeServiceMountBoundaryRefusal`: uses `--mount-scope` only when the
  operator already has a private directory beneath the chosen scratch root
  that contains a real nested mount. It records distinct observed mount IDs,
  establishes an eligible direct-child proposal without executing it, then
  checks refusal under the nested mount. No mount is created or changed. If no
  boundary is available, the record is `unavailable` with the exact missing
  prerequisite; that is not a pass.
- `TestNativeServiceStartupRootMovementAfterPublicationRelease`: an independent
  fixed actor moves/replaces the startup path immediately before the real
  worker's publish request, after final validation and publication release.
  The test records the known effect under the retained object, verifies it was
  not redirected to the replacement, joins worker and actor, restores the
  original object, and confirms future proposals remain terminally refused.
  This is a selected observation of ADR-0008's residual race, not strict
  pathname containment or rollback.

The existing nested-publication and pre-worker startup-movement cases are in the
same candidate inventory. These tests use the actual Service, retained
startup descriptor, actual auth/settings stores and real native worker. Source
presence and local Go compilation alone are not native enforcement evidence;
the measured outcomes below are from the exact candidate run. The committed
runner requires exactly one valid record per expected name and confirmed
cleanup. Any unavailable, missing, malformed, failed, extra, or
cleanup-incomplete record prevents `scoped_native_pass`.

## Recorded exact-candidate run

Operator-provided archive: `komposyn-composition-native-results.3xkcq4fd.tar.gz`

- Archive SHA-256: `de52a8fbbc250f354716ee7047978e289d792fea65c78b35cabd8f23f0b56416`
- Source commit: `d9d0dfc3230c0bb05e48f981b562ec66e133d1be`
- Source archive SHA-256: `6f767e5b5a913f4694e70ffc4e712bf1b6b366ef9bfe2e32eabdd6e9fb10b3df`
- Runner SHA-256: `57643610ed88716f205ecf9ac6d657d0b3fe19f732992c48730a35fa1ce57bc9`
- Overlay SHA-256: `bb7d9d3531ece3fdb2b4014330a10c1883a2f8ac6f367948e7f166350a1cd8e3`
- Native binary SHA-256: `cd30ada35e9aa976b7e8ba0fdb3c79c2dfd1ed4d9f5a0c450ead3f4516bddeae`
- Environment: Go 1.26.11, Linux `7.0.0-42-generic`, amd64, regular UID 1000,
  trusted root-owned Bubblewrap 0.9.0 mode `0755`, descriptor bind mode
  `ro-bind-fd`, ext4 startup scope mount ID 33.
- Inventory: all 18 expected records present exactly once; cleanup confirmed
  for every record and all captured processes were reaped.
- Outcomes: 17 `measured_pass`; `TestNativeServiceMountBoundaryRefusal` is
  `unavailable` because `--mount-scope` was not supplied and no pre-existing
  nested mount fixture was available. The report names this missing
  prerequisite. The runner exited `2` with `native_unavailable`.
- Manifest SHA-256: `a8ab652121c05ff2a7e32d71539297f599f55e12f1223210abe0d53d193e93c5`
- Native report SHA-256: `c8b18e0ad0704f7c5351af6fab81ee1d47a9463d6bf62a18b0e6a8c1c8fb4a15`

The measured cases include nested publication, stale nested-parent
substitution refusal, the scope/target refusal matrix, terminal startup-root
movement, and the selected after-release residual-race checkpoint. The latter
published successfully through the retained object and remained terminal for
future proposals after restoration. This is the ADR-0008 residual race, not
strict live-pathname containment or rollback. The archive's internal
`SHA256SUMS` entries were verified. The archive itself is retained at the
operator-provided location and is not committed to the repository.

At this historical run, the mount-boundary claim remained unmeasured. The
17 measured cases did not enable production creation. The later measured
mount-boundary result is recorded separately below.

## Follow-up attempt after PR #356

The first run with `--mount-scope` supplied used source
`c58bb7a0980c0e791a1523b1a8a082954a0a04e0` and stopped during fixture setup:
the mount test selected a different root at the same coordinator generation.
Its archive, `komposyn-composition-native-results.rsfqnqtk.tar.gz`, has SHA-256
`c52fc7db79d44c3e67f0b870c4494ca5fa526f0d67d6f6c4e516929fd525f2d1`. All 18
records were present and cleanup was confirmed, but the mount assertion was not
reached. The aggregate was `failed_or_incomplete` (exit 1). This failed attempt
is diagnostic only.

PR #356 corrected the test fixture generation; it changed no production
admission or enforcement. The exact corrected source
`34be8c54fa7ebb5a355ce8c8ccbc6fc89f818f7b` was then run with a scope supplied.
The runner reached mount discovery, found no pre-existing nested mount beneath
the designated scope, and recorded the case unavailable. This run confirms the
fixture setup correction but does not measure mount refusal.

- Archive: `komposyn-composition-native-results.9i8_bm_2.tar.gz`
- Archive SHA-256: `71413926f5ff0deec77548702aafe3d45337fd088aa7e8d1f475d4af8fea4fb9`
- Source archive SHA-256: `9e070aba92f9543abbd7232c654f23e001a03ab058bba8558c80046eb27742e2`
- Runner SHA-256: `57643610ed88716f205ecf9ac6d657d0b3fe19f732992c48730a35fa1ce57bc9`
- Overlay SHA-256: `9da0e55bb8421c2ca4b962cf88193dfbc82fa4a323a49177e14dee44ae6e1ef2`
- Native binary SHA-256: `2320be9b3bcd7b4e728a02f817f185e13a1242e14bd8fb41e236540356ff4bb1`
- Environment: Go 1.26.11, Linux `7.0.0-42-generic`, amd64, regular UID 1000,
  trusted root-owned Bubblewrap 0.9.0 mode `0755`, `ro-bind-fd`; the scope is
  ext4 mount ID 33. No nested mount ID was observed.
- Inventory and cleanup: all 18 expected records occurred exactly once;
  cleanup was confirmed for every case, captured processes were reaped, and
  temporary runner work was removed.
- Outcomes: 17 `measured_pass`; `TestNativeServiceMountBoundaryRefusal` is
  `unavailable` because the supplied scope had no pre-existing nested mount.
  Build, unit-check, and native stages completed, but the aggregate is
  `native_unavailable` (exit 2), not a pass.
- Manifest SHA-256:
  `4b7bdca4ae3ad4d82b2618cf544d965687936f38ef1a9b5261c56b926da66168`
- Native report SHA-256:
  `34e6f4a27b4fb6e3b42c772102e879358559be46783f131588d0c30eaabb7240`

The external archive sidecar and all 32 internal `SHA256SUMS` entries were
verified. The archive remains with the operator and is not committed. That run
did not measure the mount boundary; no mount was created or changed.
This documentation update follows measured source `34be8c54fa7ebb5a355ce8c8ccbc6fc89f818f7b`;
the runner, overlay, and enforcement paths are unchanged between that source
and this documentation revision.

## Nested mount-boundary follow-up

The exact current-master source `c0af7cda7bbbce3de289367af9e468ea8bea73df`
was run with an authorized private ext4 scope containing a real nested bind
mount. This resolves the previously unavailable mount-boundary measurement
for this candidate and measured host profile; it does not enable production
creation.

- Archive basename: `komposyn-composition-native-results.kgkmkzfx.tar.gz`
- Archive SHA-256: `00a4ffccd3c4acf156faae120a863e04ffc1a74f185702b4f1e0acafc6077f59`
- Manifest SHA-256: `53aad50556c103fb2e2bda2b0ea4dbc47d6f77061b3bc597e69f531cd7cd2f23`
- Native report SHA-256: `f378d34c3b87bad6691c3979e42e0c318a0b5e28a871c2dfd130694d867ff2f1`
- Source archive SHA-256: `a7b80eec9323236a475939fdcec3cc81a3af32757f7f530525f06f76ce331906`
- Runner SHA-256: `57643610ed88716f205ecf9ac6d657d0b3fe19f732992c48730a35fa1ce57bc9`
- Overlay SHA-256: `9da0e55bb8421c2ca4b962cf88193dfbc82fa4a323a49177e14dee44ae6e1ef2`
- Native binary SHA-256: `171db6baf066805c80bdef80317653fd724f6a48bb8ffad8f3dc111cb5a331d9`
- Environment: Go 1.26.11, Linux `7.0.0-42-generic`, amd64, regular UID 1000,
  trusted root-owned Bubblewrap 0.9.0 mode `0755`, measured `ro-bind-fd` mode.
- Filesystem evidence: startup scope on ext4 mount ID 33; nested mount ID 157.
  The IDs were distinct in the measured host mount namespace.
- Command: `scripts/native-acceptance/run-native-acceptance.sh --repo "$PWD"
  --revision c0af7cda7bbbce3de289367af9e468ea8bea73df --bwrap /usr/bin/bwrap
  --scratch-dir "$SCRATCH_DIR" --mount-scope "$MOUNT_SCOPE"`; exit status `0`.
- Inventory and outcome: all 18 expected cases appeared exactly once and were
  `measured_pass`; `case_inventory_exact` was true. Unit checks, native build,
  native execution, process joining and temporary-work cleanup completed.
  The aggregate status was `scoped_native_pass` and `work_retained` was false.
- Mount case: `TestNativeServiceMountBoundaryRefusal` was `measured_pass` with
  `creation_mount_boundary_refused=true`. Its direct-child eligibility proposal
  was prepared and discarded without execution; the nested-mount target was
  refused before approval/worker launch, no target appeared, and no private
  stage remained. The case cleanup was confirmed.
- Verification: the external archive SHA-256 sidecar matched, and all 32
  internal `SHA256SUMS` entries verified. The raw archive is retained by the
  operator and is not committed.

This records only the fixed worker and test fixture on the measured Linux
amd64/ext4/Bubblewrap profile. It does not establish other filesystems,
architectures, bind modes or platforms; exhaustive worker/actor schedules;
interrupted syscalls; production owner coverage; or model-facing enablement.
The runner, overlay, native worker and enforcement source in this run are the
same as at `c0af7cda7bbbce3de289367af9e468ea8bea73df`; this PR changes records
only. Creation remains unregistered and unavailable pending separate explicit
enablement review.

## Host requirements

- Linux amd64, running as your regular user (not root or `sudo`).
- Go 1.26.11 or newer, Git, Python 3.12 or newer.
- A trusted `/usr/bin/bwrap`: root-owned, not group/world-writable, and able to
  create its required namespaces and seccomp filter.
- A writable directory on a local ext4 filesystem for the scratch directory.
  The test verifies ext4 using the retained parent descriptor's mount ID and
  `/proc/self/mountinfo`; a filesystem label or `statfs` magic alone is not
  accepted as evidence.

Do not change Bubblewrap ownership or permissions, install privileged
components, or run as root to make a host pass. If a prerequisite fails, stop
and report it as unavailable.

## Exact-candidate operator steps

1. Clone the repository and enter it:

   ```sh
   git clone https://github.com/ayeshLK/komposyn.git
   cd komposyn
   ```

2. Fetch the PR branch and check out the exact full 40-character head SHA
   supplied in the PR handoff. Set the two values from that handoff:

   ```sh
   CANDIDATE_BRANCH='feat/issue321-acceptance-evidence-closeout'
   CANDIDATE_SHA='<full 40-character PR head SHA>'
   git fetch origin "$CANDIDATE_BRANCH"
   git checkout --detach "$CANDIDATE_SHA"
   test "$(git rev-parse HEAD)" = "$CANDIDATE_SHA"
   ```

   If that equality check fails, stop. Do not run a nearby branch tip or older
   commit.

3. Confirm the toolchain and host identity:

   ```sh
   go version
   uname -srm
   id
   stat -c '%U:%G %a %n' /usr/bin/bwrap
   /usr/bin/bwrap --version
   ```

   Bubblewrap must be owned by `root` and mode must not allow group or other
   writes. Stop if not.

4. Choose an existing writable directory that you control on local ext4. Set
   `SCRATCH_DIR` to that actual directory; do not copy an example host path:

   ```sh
   SCRATCH_DIR='/absolute/path/to/your/existing/ext4/scratch'
   test -d "$SCRATCH_DIR" && test -w "$SCRATCH_DIR" && test -x "$SCRATCH_DIR"
   findmnt -T "$SCRATCH_DIR" -o TARGET,FSTYPE,OPTIONS
   test "$(findmnt -n -o FSTYPE -T "$SCRATCH_DIR")" = ext4
   ```

   If any check fails, stop. Do not mount or reconfigure a filesystem for this
   test.

5. Run the acceptance runner from the checked-out candidate:

   ```sh
   scripts/native-acceptance/run-native-acceptance.sh \
     --repo "$PWD" \
     --revision "$CANDIDATE_SHA" \
     --bwrap /usr/bin/bwrap \
     --scratch-dir "$SCRATCH_DIR"
   ```

   Only add `--mount-scope "$MOUNT_SCOPE"` if you already have a private,
   current-user-owned directory beneath `SCRATCH_DIR` with a real nested mount
   that you are authorized to inspect without changing. Set `MOUNT_SCOPE` to
   that existing directory. The test will not create, move, or modify mounts.
   Without it, the mount-boundary case records unavailable and the runner
   exits `2` rather than reporting an aggregate pass.

   The runner uses a temporary source archive and test overlay, without
   modifying the checkout. It runs selected unit checks, builds the native
   test binary, and then executes the native cases. The #321 bounded-creation
   case verifies exact published bytes, mode `0600`, no-replace collision
   preservation, private-stage cleanup and worker join. The retained-parent
   case moves the parent with a separate same-UID actor and substitutes a new
   directory at its former path; it verifies creation is relative to the
   retained parent descriptor. Both reports include the actual filesystem
   type and mount ID observed from retained descriptors. The candidate adds
   the per-case records named in `manifest.json`, including
   `TestNativeServiceNestedParentSubstitutionRefusesPreparedProposal`,
   `TestNativeServiceScopeAndTargetRefusalsPreserveFixtures`,
   `TestNativeServiceMountBoundaryRefusal`, and
   `TestNativeServiceStartupRootMovementAfterPublicationRelease`. Expected
   case names come from the candidate manifest; do not assume a fixed case
   count.

6. Read the `Results archive: ...` path printed by the runner. Inspect
   `summary.txt`, `manifest.json`, `native-report.json`, the
   source/runner/overlay/binary hashes, every expected case record, the
   `case_inventory_exact` value, the aggregate status, cleanup fields, and
   `SHA256SUMS`. Check that the recorded kernel, architecture, UID, Go version,
   Bubblewrap trust/version/bind mode, and filesystem/mount evidence match the
   requested profile. Keep the archive unchanged and send back its path, the
   external archive SHA-256 sidecar value, and the command's exit status.

## Result interpretation

- Exit `0` means the selected unit/build stages and all recorded native cases
  passed for this host, mount and measured Bubblewrap mode.
- Exit `2` means the runner completed its earlier stages but native execution
  was unavailable or at least one expected case was explicitly unavailable.
  This is not a pass. An unavailable mount case names the missing pre-existing
  mount fixture needed for that evidence.
- Exit `1` means a test failed or evidence/cleanup was incomplete. Send the
  bundle without rerunning or deleting retained work.

This run does not establish production owner coverage, a live provider
continuation, all launch modes, interrupted syscalls, all independent-actor
timings, external isolation during approval, all Linux filesystems, or
non-Linux behavior. The design accepts residual external changes during
approval and between observation and effect. No native result authorizes
production enablement; a separate maintainer decision is required.
