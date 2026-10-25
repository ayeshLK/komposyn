# Historical combined releases

Combined KompoSyn releases through [v0.7.0](https://github.com/ayeshLK/komposyn/releases/tag/v0.7.0)
remain published for provenance and rollback investigations. They included
both the core module source and `komposyn-lg` binaries. The experimental
temporary-chat `komposyn` binaries are now prepared from this repository's
manual release workflow. The standalone
[komposyn-lg repository](https://github.com/ayeshLK/komposyn-lg/releases)
continues to own all production executable releases.

## Historical CLI artifact verification

Combined CLI archives and their `SHA256SUMS` manifests were signed by the
former core publication workflow. Verify their checksums and Cosign signatures
with this exact certificate identity and issuer:

```text
Identity: https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master
Issuer:   https://token.actions.githubusercontent.com
```

For example, after downloading a release archive, its `.sig`, `.pem`, the
manifest and its signature/certificate:

```sh
cosign verify-blob \
  --signature komposyn-lg_0.7.0_linux_amd64.tar.gz.sig \
  --certificate komposyn-lg_0.7.0_linux_amd64.tar.gz.pem \
  --certificate-identity-regexp '^https://github.com/ayeshLK/komposyn/.github/workflows/release.yml@refs/heads/master$' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  komposyn-lg_0.7.0_linux_amd64.tar.gz
```

Verify `SHA256SUMS` with its accompanying signature and certificate using the
same identity, then verify the selected archive checksum with `sha256sum -c`
(or `shasum -a 256 -c`). Check the archive name against the target OS and
architecture before extracting it.

This old archive layout applies to historical `komposyn-lg` artifacts only.
New experimental `komposyn` assets use the signed checksum-bundle contract and
identity documented in [release verification](release-verification.md).
Standalone `komposyn-lg` artifacts must match the exact release workflow
identity documented by that CLI repository. Historical signatures do not make
old binaries current or supported. To continue using `komposyn-lg`,
install the latest signed standalone release over the existing executable and
preserve its data directory; see the standalone
[upgrade instructions](https://github.com/ayeshLK/komposyn-lg/blob/master/docs/installation.md).
