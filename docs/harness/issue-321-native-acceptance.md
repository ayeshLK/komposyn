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

## Steps

1. Clone the repository and enter it:

   ```sh
   git clone https://github.com/ayeshLK/komposyn.git
   cd komposyn
   ```

2. Fetch the candidate branch and check out the exact candidate commit supplied
   with this request. Replace `<candidate-commit>` with its full 40-character
   SHA:

   ```sh
   git fetch origin <candidate-branch>
   git checkout --detach <candidate-commit>
   git rev-parse HEAD
   ```

   Confirm that `git rev-parse HEAD` prints the same SHA. Do not run against a
   different commit.

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

4. Select an existing writable directory on local ext4. For example, if
   `/mnt/ext4` is already an ext4 mount and you own it:

   ```sh
   findmnt -T /mnt/ext4 -o TARGET,FSTYPE,OPTIONS
   mkdir -m 700 -p /mnt/ext4/komposyn-issue321
   ```

   `findmnt` must report `ext4`. Use a directory you are authorized to write;
   do not mount or reconfigure a filesystem for this test.

5. Run the acceptance runner from the checked-out candidate:

   ```sh
   scripts/native-acceptance/run-native-acceptance.sh \
     --repo "$PWD" \
     --revision "$(git rev-parse HEAD)" \
     --bwrap /usr/bin/bwrap \
     --scratch-dir /mnt/ext4/komposyn-issue321
   ```

   The runner uses a temporary source archive and test overlay, without
   modifying the checkout. It runs selected unit checks, builds the native
   test binary, and then executes the native cases. The #321 bounded-creation
   case verifies exact published bytes, mode `0600`, no-replace collision
   preservation, private-stage cleanup and worker join. The retained-parent
   case moves the parent with a separate same-UID actor and substitutes a new
   directory at its former path; it verifies creation is relative to the
   retained parent descriptor. Both reports include the actual filesystem
   type and mount ID observed from retained descriptors.

6. Read the `Results archive: ...` path printed by the runner. Inspect
   `summary.txt`, `manifest.json`, `native-report.json`, the
   `TestNativeServiceBoundedCreation` and
   `TestNativeServiceCreationUsesRetainedParentAfterIndependentMove` records,
   and `SHA256SUMS`. Send back the
   archive path and the command's exit status.

## Result interpretation

- Exit `0` means the selected unit/build stages and all recorded native cases
  passed for this host, mount and measured Bubblewrap mode.
- Exit `2` means the runner completed its earlier stages but native execution
  was unavailable. This is not a pass.
- Exit `1` means a test failed or evidence/cleanup was incomplete. Send the
  bundle without rerunning or deleting retained work.

This run does not establish production owner coverage, a live provider
continuation, all launch modes, interrupted syscalls, all independent-actor
timings, external isolation during approval, all Linux filesystems, or
non-Linux behavior. The design accepts residual external changes during
approval and between observation and effect. No native result authorizes
production enablement; a separate maintainer decision is required.
