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

Runner SHA-256:
`c6b44530e473e1de6089deae402968391251196df10ed7f2dc1847b8bc7c27b0`.
Overlay SHA-256:
`e61b66665dde953113b3b5f0df17f6238bffa543494acf9471dba0e84b636f14`.
The tracked host-independent results are in
[`docs/harness/read-broker-composition-native-acceptance.md`](../../docs/harness/read-broker-composition-native-acceptance.md).
