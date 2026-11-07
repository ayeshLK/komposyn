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

Runner SHA-256:
`fb844f048a23a1ad01c3fa36c972bdf645665ab0dc32d519a2e00b1b382acb63`.
Overlay SHA-256:
`3861d527558ce69722e9901a59fe0886a08faa67ed5e33026bc3cf2ac96dde11`.
The tracked host-independent results are in
[`docs/harness/read-broker-composition-native-acceptance.md`](../../docs/harness/read-broker-composition-native-acceptance.md).
