# Reproduce #243 service native acceptance

Run from a normal Linux terminal, as a regular user, from any directory:

```sh
/path/to/komposyn/scripts/native-acceptance/run-native-acceptance.sh \
  --repo /path/to/komposyn \
  --revision "$(git -C /path/to/komposyn rev-parse HEAD)" \
  --bwrap /usr/bin/bwrap
```

The wrapper runs the native harness from the same checkout under
[`testdata/native-acceptance/`](../../testdata/native-acceptance/README.md).
The Go overlay is kept there because Go intentionally excludes `testdata`
trees from `go test ./...`; the fixture is injected only into the runner's
temporary source archive. This keeps the native acceptance tests out of the
normal package test suite and shipped executable. Use the runner from the
checkout whose native test bundle you intend to measure.

The runner includes `TestNativeActorModeDispatch`, a process-level check that
uses the compiled acceptance test binary as its fixed actor executable. This
case does not require Bubblewrap; it verifies early actor-mode dispatch,
protocol completion, the fixed directory transition, and child cleanup. The
remaining native service cases still require the trusted host prerequisites
documented in [the #321 acceptance guide](../../docs/harness/issue-321-native-acceptance.md).

If only the nested-mount case is unavailable, use
[`run-mount-boundary-acceptance.sh`](run-mount-boundary-acceptance.sh) to create
a disposable private fixture, run the suite unprivileged, and unmount only the
bind mount created by that helper. It asks before mounting; see its `--help`
output for the complete operations and prerequisites.

Current runner SHA-256:
`8bd84f33c014fff6f159fcfd4fb8605b09e91db26b6394228503ee6ac1c106c3`.
Current overlay SHA-256:
`09b895d2d67d281da95d00bea9944bc60d9743adf5887466345b06d96574783d`.
The test bundle also records these hashes from the exact reviewed candidate.
The tracked host-independent results are in
[`docs/harness/read-broker-composition-native-acceptance.md`](../../docs/harness/read-broker-composition-native-acceptance.md).
