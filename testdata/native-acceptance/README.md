# Native acceptance of sandbox composition and bounded creation

Run as your normal Ubuntu/Linux user, without sudo or host security changes. Requires Go 1.26.11+, Git, Python 3.12+ (Python 3.11.8+ with tar extraction filters also works), and a trusted system Bubblewrap with the required namespace/seccomp capabilities.

The hash-pinned runner and test overlay are kept together in
`testdata/native-acceptance/`:

- `run-native-acceptance.sh`
- `run-native-acceptance.py`
- `native_acceptance_linux_test.go`

The startup-scope #321 cases compose the real host Service, actual auth/settings
registrations, the retained startup descriptor and the real Bubblewrap worker.
`TestNativeServiceNestedCreationUsesStartupScope` also calls the same
profile-gated runtime composition helper used by `cmd/harness` and asserts that
the eligible Service registers `apply_patch` before executing the native
creation path. Earlier bounded-worker cases retain their separate synthetic
complete owner manifest. All cases use only synthetic file content. They
verify exact bytes/mode, no-replace collision behavior, stage disposition and
joined workers.
Fixed same-UID actors test retained-parent movement, stale nested-parent
substitution before approval release, startup-root movement before worker
launch, and startup-root movement immediately before worker publication.
The latter records a known success through the retained object after the final
observation, followed by a terminal refusal for later proposals; it demonstrates
ADR-0008's accepted residual race rather than strict live-pathname containment.
The refusal matrix establishes a valid nested baseline before checking ordinary
content, direct/nested escapes, outside navigation, missing/non-directory
parents, symlinked parent/leaf paths, collisions, and an absent protected auth
route. It verifies unchanged sentinels, no worker launch for refusals, and no
leftover private stage. The overlay does not run the shipping CLI binary or
contact a provider; behavioral Service/runtime/UI tests cover the rest of the
approval path.

The #322 metadata-only case runs a fixed count-only FIEMAP query in the host
test overlay on a duplicate of the broker's retained regular-file descriptor.
It verifies that the descriptor matches the broker identity and the ext4
scratch mount. On a clean fixture the existing read worker finishes and joins.
A visible xattr, an inherited access ACL, an injected ioctl denial and a
mismatched mount refuse before worker launch or once-approval consumption. The
directory case refuses in the existing broker. An xattr added at the in-process
pre-handoff checkpoint makes final revalidation refuse and join the worker
before it receives the file descriptor. The direct probe also checks the
real ioctl error on an `O_PATH` descriptor. The report keeps injected errors
separate from filesystem observations. Hidden attributes, EA-inode storage,
security labels and independent actors are outside this overlay case. Editing
and publication of replacements remain unavailable outside the following
single, opt-in test-only acceptance case.

`TestNativeRewritePrototypeComposedReadAndApprovedRewrite` reads through Chat
and Service with a scripted provider, approves the complete replacement diff
once, and exercises the trusted native rewrite worker. The fixture checks its
exact new bytes and length, same device/inode, mode, ownership and user xattr.
It records one write attempt and joined workers. A host refusal is reported as
`unavailable` with the Service/backend prerequisite; it is not counted as
native success.

Mount crossing is measured only if the disposable scope already contains a
real nested mount. The runner does not create or alter mounts. Without such a
boundary the case is recorded `unavailable` with its exact prerequisite; an
unavailable case prevents an aggregate native pass. The report also preserves
the prior synthetic worker cases and their historical claims separately from
the current startup-scope acceptance.

From the candidate checkout, use the exact candidate commit and an existing
scratch directory on local ext4. The issue-specific procedure is documented
in [issue-321-native-acceptance.md](../../docs/harness/issue-321-native-acceptance.md).
The general invocation is:

```sh
scripts/native-acceptance/run-native-acceptance.sh \
  --repo "$PWD" \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --scratch-dir /path/to/writable/ext4/scratch \
  --case TestNativeRewritePrototypeComposedReadAndApprovedRewrite
```

Without `--case`, the runner retains its full expected-case inventory and
aggregate status rules. With a selector, its manifest and report inventory
must contain exactly the explicitly selected case.

If an already-existing private directory beneath that scratch directory
contains a nested mount which you are authorized to inspect without changing,
add `--mount-scope /absolute/path/to/that/directory`. The optional mount case
then tests the real boundary. The runner itself never creates or alters mounts.

If you have authorization to provision a disposable nested bind mount, the
companion helper performs the setup, runner invocation, and exact unmount in
one command:

```sh
scripts/native-acceptance/run-mount-boundary-acceptance.sh \
  --repo "$PWD" \
  --revision "$(git rev-parse HEAD)" \
  --scratch-dir /path/to/writable/ext4/scratch
```

It creates private fixture directories beneath scratch, prompts before
mounting, uses `sudo` only for the bind mount and its exact unmount, and runs
the runner as the current user. The results archive stays in scratch. If
unmount fails, the helper preserves the fixture and prints its path.

The command reads only the selected committed revision into its own temporary
source tree. It does not modify or clean the checkout. `--revision` is
required, so a run cannot silently fall back to an old runner baseline.

The script reads a Git archive of that exact commit. If it is absent locally, it fetches the commit from the public repository into a disposable scratch repository. It never checks out, fetches into, edits or cleans your actual worktree.

Builds and native tests use an isolated source directory with one **test-only overlay**. The archive identifies the source commit, source archive hash, overlay hash, runner hash, exact committed capture helper hash and native test binary hash separately. It does not falsely claim the overlay is committed code. `manifest.json` records the expected case names; `native-report.json` must contain exactly one record per expected case, no extras, and an explicit reason for every unavailable case. Missing, malformed, duplicate/mismatched, failed, unavailable or cleanup-incomplete records cannot produce `scoped_native_pass`. A normal host run can take several minutes for the first build.

## Cases and boundaries

- The real shipped service still refuses all generic reads.
- A bounded fixture rebinds the internal deny-only coordinator to explicit fixture once approvals, using the same real service registry, composition inventory, protector and native Bubblewrap launcher. This exists only in the injected `_test.go` file; there is no shipped bypass flag or model-facing filesystem tool.
- Real ordinary-file bytes must be returned with readiness, capability hold/release, read, join and private coordinator publication evidence.
- Auth/settings routes and a hard-link alias must produce the exact protected-resource refusal before once consumption/worker launch. Unrelated failures cannot pass these checks.
- A real held worker is invalidated/joined while the real auth/settings stores rotate through the service protector. The first update must return cleanup_pending and preserve the destination while the test holds the coordinator at its checkpoint. After releasing that checkpoint and confirming join, a fresh update must register a distinct empty replacement before writing. The observer verifies actual worker join before the successful protector returns to the writer. The retired settings alias remains protected.
- Revocation after capture must discard private bytes before publication.
- Root change while a worker is held must first return cleanup_pending; after the old read settles, a fresh invocation under the new root must succeed. Cancellation while held must join without reading or publishing. Shutdown is forced to report incomplete cleanup at the held-operation boundary, then a fresh shutdown retry must drain it. This deterministic coordination case is not a natural kernel-hang test.
- Separately labelled existing unit tests cover independent path overrides, pending-coverage recovery, runtime preparation/grant ordering, data suppression and bounded shutdown/retry. They are not counted as native enforcement.

No provider network request is made. “Publication” here means the coordinator's private result sink, not a live selected LLM. The suite does not establish the unknown-file policy, live provider behavior, all Bubblewrap modes, all platforms, every resource limit, arbitrary descendants, natural unjoinable processes, or all missing issue #215 gates. The nested creation case proves profile-gated schema registration and Service effect behavior on the measured host, but it does not replace maintainer review or establish support beyond that profile. It does not simulate a kernel hang or claim native proof of incomplete-cleanup cases covered only by unit tests. The #321 cases cover stage, sync, no-replace publication, exact bytes and mode, incumbent preservation, one deterministic independent-actor mutation after final observation, retained-parent movement/substitution, cleanup, and join on the actual tested filesystem and selected Bubblewrap bind mode. Interrupted write/sync/close/publication syscalls and other independent-actor timings remain unestablished. Deterministic injected protocol failures in the behavioral suite are not native syscall interruption evidence.

## Results

The exact PR #359 candidate (`2a576e5b38eb6e71caca1f56084b014212c8f16a`)
recorded 17 of 18 cases as `measured_pass`. The shipping
`TestNativeServiceNestedCreationUsesStartupScope` case confirmed
`creation_schema_registered: true`. The mount-boundary case was unavailable
because the designated scope had no pre-existing nested mount, so the aggregate
was `native_unavailable` (exit 2), not a pass. See the [native acceptance
record](../../docs/harness/issue-321-native-acceptance.md#shipping-composition-candidate-run-2026-11-24)
for the archive and hashes.

The runner prints a path such as:

```text
Results archive: /tmp/komposyn-composition-native-results.XXXXXXXX.tar.gz
```

Share that archive path for review. It includes:

- `manifest.json`: provenance, stage results, cleanup status and aggregate classification.
- `native-report.json` and `native-records/`: per-case status, backend mode/trust, worker/probe evidence and rotation observations.
- Bounded unit/build/native logs and capture metadata.
- The exact test overlay and capture helper as text.
- `SHA256SUMS`; an external archive SHA-256 sidecar is also written.

Exit statuses:

- `0`: this bounded native fixture suite and selected unit checks passed.
- `2`: unit/build stages completed but native cases were unavailable (not a pass).
- `1`: failed/incomplete runner or tests; inspect the archive.

If cleanup cannot be confirmed, the runner retains its synthetic scratch work and reports the path; it does not remove resources underneath potentially live descendants. No databases, binaries, raw user files or credential values are included in the shareable archive. Root-owned Bubblewrap trust is never bypassed; an unavailable run does not install, chmod, chown or elevate anything.
