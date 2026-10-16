# Security and privacy model

This page describes the boundary a reviewer should use when evaluating a
change. It complements [Privacy and data handling](privacy.md),
[SECURITY.md](../SECURITY.md), and the [harness protocol](HARNESS_PROTOCOL.md).

## Data-flow boundary

```text
Provider-owned files --read--> adapters --normalize--> local SQLite
                                                          |
                                      reports / harness --+--> local output
                                                          |
                                      reviewed issue draft --> GitHub URL
```

The default import, collection, reporting, and harness paths are local. Release
installation explicitly downloads and verifies release artifacts. The only
documented external reporting path is an explicitly reviewed GitHub issue
draft, which places sanitized diagnostic text in a URL. The application does
not silently submit an issue.

## Data inventory

The local database may contain usage counts, timestamps, provider/model/client
identifiers, project and session references, source provenance, source hashes,
source cursors, registered collector paths, local collector lease/health
history, pricing rules, and sanitized diagnostics. Registered paths are local
administrative metadata and are not copied into diagnostics or issue drafts.
Settings are stored separately and include onboarding, collector,
data-directory, and consent choices.

By design, Komposyn does not intentionally persist prompts, model responses,
full transcripts, API keys, or provider credentials. This is a design
constraint, not a promise that the host filesystem or a user-created fixture
cannot contain sensitive data.

## Disclosure review

Before sharing logs or opening an issue:

1. Inspect the command, paths, project/session names, model names, and
   diagnostic text.
2. Remove credentials, tokens, prompts, responses, transcript fragments, and
   private paths.
3. Prefer a reproduction using sanitized synthetic data.
4. For a vulnerability, use private reporting rather than a public issue.
5. Treat backups, release directories, and browser history containing issue
   draft URLs as sensitive.

The built-in sanitizer removes absolute paths and common `token=`,
`secret=`, `password=`, and `api_key=` shaped values. It cannot recognize every
secret format or infer that an arbitrary identifier is sensitive. Always review
the result yourself.

## Change review checklist

For changes affecting data, diagnostics, network behavior, installers, or CI:

- Does the change add a new data field, persistence path, or external request?
- Does it preserve provider source immutability and the library/CLI boundary?
- Could prompts, responses, credentials, or source content reach storage,
  stdout, stderr, issue URLs, artifacts, or logs?
- Are existing checksum, signature, certificate, least-privilege, and
  protocol-version checks preserved?
- Are backups, migrations, consent settings, and uninstall behavior preserved?
- Are tests and documentation updated with sanitized examples?
