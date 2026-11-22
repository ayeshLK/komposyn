# Native acceptance of sandbox composition and bounded creation

Run as your normal Ubuntu/Linux user, without sudo or host security changes. Requires Go 1.26.11+, Git, Python 3.12+ (Python 3.11.8+ with tar extraction filters also works), and a trusted system Bubblewrap with the required namespace/seccomp capabilities.

The hash-pinned runner and test overlay are kept together in
`testdata/native-acceptance/`:

- `run-native-acceptance.sh`
- `run-native-acceptance.py`
- `native_acceptance_linux_test.go`

The runner includes four #321 creation cases. It composes the real host service
with the real Bubblewrap worker. The earlier bounded-worker cases use the
synthetic complete owner manifest used by fixtures; the new startup-scope
cases use actual auth/settings owner registrations and a separate internal
test-only preparation gate. The suite publishes a synthetic file, checks exact
bytes and mode `0600`, verifies stage cleanup and worker join, and confirms an
existing target is not replaced. It also injects a host-side independent actor
after the service's final target observation and immediately before the
worker's publication request, then checks that `RENAME_NOREPLACE` preserves
the actor's file and removes the stage. This measures one deterministic residual-race
checkpoint, not every possible syscall interleaving. The second case starts a
separate same-UID fixed-purpose actor process that moves the synthetic parent
directory and creates a substitute at the old pathname immediately before
worker staging. It records actor PID/UID, the retained and substitute parent
identity hashes, the mount ID/filesystem, stage disposition and worker join.
The actor has no shell or arbitrary command interface and shares the host mount
namespace. Additional cases capture the service's fixed startup scope, publish
beneath an existing nested parent, and move/replace the startup path with a
fixed same-UID actor. They verify that movement is detected before worker
launch and remains terminal after the original object is restored. These are
new candidate cases and have not yet been run on a trusted host. The study does
not use a provider, enable the production tool, or authorize arbitrary roots.

From the candidate checkout, use the exact candidate commit and an existing
scratch directory on local ext4. The issue-specific procedure is documented
in [issue-321-native-acceptance.md](../../docs/harness/issue-321-native-acceptance.md).
The general invocation is:

```sh
scripts/native-acceptance/run-native-acceptance.sh \
  --repo "$PWD" \
  --revision "$(git rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap \
  --scratch-dir /path/to/writable/ext4/scratch
```

The command reads only the selected committed revision into its own temporary
source tree. It does not modify or clean the checkout. `--revision` is
required, so a run cannot silently fall back to an old runner baseline.

The script reads a Git archive of that exact commit. If it is absent locally, it fetches the commit from the public repository into a disposable scratch repository. It never checks out, fetches into, edits or cleans your actual worktree.

Builds and native tests use an isolated source directory with one **test-only overlay**. The archive identifies the source commit, source archive hash, overlay hash, runner hash, exact committed capture helper hash and native test binary hash separately. It does not falsely claim the overlay is committed code. A normal host run can take several minutes for the first build.

## Cases and boundaries

- The real shipped service still refuses all generic reads.
- A bounded fixture rebinds the internal deny-only coordinator to explicit fixture once approvals, using the same real service registry, composition inventory, protector and native Bubblewrap launcher. This exists only in the injected `_test.go` file; there is no shipped bypass flag or model-facing filesystem tool.
- Real ordinary-file bytes must be returned with readiness, capability hold/release, read, join and private coordinator publication evidence.
- Auth/settings routes and a hard-link alias must produce the exact protected-resource refusal before once consumption/worker launch. Unrelated failures cannot pass these checks.
- A real held worker is invalidated/joined while the real auth/settings stores rotate through the service protector. The first update must return cleanup_pending and preserve the destination while the test holds the coordinator at its checkpoint. After releasing that checkpoint and confirming join, a fresh update must register a distinct empty replacement before writing. The observer verifies actual worker join before the successful protector returns to the writer. The retired settings alias remains protected.
- Revocation after capture must discard private bytes before publication.
- Root change while a worker is held must first return cleanup_pending; after the old read settles, a fresh invocation under the new root must succeed. Cancellation while held must join without reading or publishing. Shutdown is forced to report incomplete cleanup at the held-operation boundary, then a fresh shutdown retry must drain it. This deterministic coordination case is not a natural kernel-hang test.
- Separately labelled existing unit tests cover independent path overrides, pending-coverage recovery, runtime preparation/grant ordering, data suppression and bounded shutdown/retry. They are not counted as native enforcement.

No provider network request is made. “Publication” here means the coordinator's private result sink, not a live selected LLM. The suite does not establish the unknown-file policy, filesystem tool enablement, all Bubblewrap modes, all platforms, every resource limit, arbitrary descendants, natural unjoinable processes, or all missing issue #215 gates. It does not simulate a kernel hang or claim native proof of incomplete-cleanup cases covered only by unit tests. The #321 cases cover stage, sync, no-replace publication, exact bytes and mode, incumbent preservation, one deterministic independent-actor mutation after final observation, retained-parent movement/substitution, cleanup, and join on the actual tested filesystem and selected Bubblewrap bind mode. Interrupted write/sync/close/publication syscalls and other independent-actor timings remain unestablished. Deterministic injected protocol failures in the behavioral suite are not native syscall interruption evidence.

## Results

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
