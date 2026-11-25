# Reproduce #243 service native acceptance

Run from a normal Linux terminal, as a regular user, from any directory:

```sh
/path/to/komposyn/scripts/native-acceptance/run-native-acceptance.sh \
  --repo /path/to/komposyn \
  --revision d43a7dd7005ae6ccc8102936d00bd2920bbd9fcf \
  --bwrap /usr/bin/bwrap
```

The wrapper runs the byte-preserved harness revision 2 under
[`testdata/native-acceptance/`](../../testdata/native-acceptance/README.md).
The Go overlay is kept there because Go intentionally excludes `testdata`
trees from `go test ./...`; the fixture is injected only into the runner's
temporary source archive. This keeps the native acceptance tests out of the
normal package test suite and shipped executable.

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
`57643610ed88716f205ecf9ac6d657d0b3fe19f732992c48730a35fa1ce57bc9`.
Current overlay SHA-256:
`bb7d9d3531ece3fdb2b4014330a10c1883a2f8ac6f367948e7f166350a1cd8e3`.
The test bundle also records these hashes from the exact reviewed candidate.
The tracked host-independent results are in
[`docs/harness/read-broker-composition-native-acceptance.md`](../../docs/harness/read-broker-composition-native-acceptance.md).
