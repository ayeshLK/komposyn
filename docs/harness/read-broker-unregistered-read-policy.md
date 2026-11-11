# Policy decision: unregistered exact-file reads

**Historical decision; revised by #278.** The initial policy allowed generic
reads only beneath a selected lookup root. The current policy uses the working
directory only for relative-path resolution: an individually approved exact
target may be outside it. The broker opens the target beneath its verified
mount root and retains no-symlink, no-magic-link, no-mount-crossing-within-that-
mount, regular-file, protected-route/identity, and directory-coverage checks.
This note keeps the original decision and evidence for historical context.

For the complete disclosure/replay decision and staged handoff, see the
[implementation plan](read-broker-unregistered-read-implementation-plan.md).
The maintainer explicitly accepted its six clauses in the [issue decision
comment](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6709361902).
That authorizes staged implementation; it does not enable reads. This note
retains its original proposal and evidence provenance.

## Current behavior and the question

The #243 host service registers the effective auth and settings locations and
their protected object identities. It does not open a Token Logger Store and
does not read provider-source records. That accurately describes this process
composition; it does not classify every file in a user-selected root. An
unregistered file can still be an unrelated credential, database, backup,
provider record, or independent copy of sensitive bytes. The service's
`unknown-root-data` gate therefore returns `ErrUnknownRootData` before grant
consumption, preparation, descriptor release, or worker launch.

The accepted decision permits a user to explicitly authorize disclosure of one
unregistered exact file despite that classification gap, while registered
protected routes and identities remain non-approvable. Root selection and a
warning alone do not answer this question.

## Choices

| Choice | Rule | Security and usability consequence |
| --- | --- | --- |
| **A. Keep the refusal** | Generic reads remain unavailable until all required protected data in the selected scope has authoritative coverage. | Preserves a conservative interpretation of the protected-data rule. Arbitrary roots may remain unusable because independent applications and copies cannot be exhaustively registered. This does not make a future inventory complete by scanning filenames or extensions. |
| **B. Explicit exact-file disclosure (accepted)** | Permit a once-approved read of one otherwise-unregistered exact regular-file object under the selected root, with a clear confirmation that its contents will be sent to the selected provider. Preserve mandatory protections for registered routes/identities and classified provider sources. | Makes narrow reads useful without claiming unknown files are safe. A user can deliberately disclose an unregistered credential, database, or provider record; the tool cannot identify it by content or origin. The approval authorizes disclosure of that exact opened object, not its safety. |

A broad root grant, warning-only notice, suffix/content heuristic, default path
list, or machine-wide scan is not a third safe answer. A manual per-file
declaration could help the user understand a file's origin, but cannot prove
its bytes contain no other sensitive data.

## Original #215 accepted semantics

Issue #278 supersedes the selected-root containment clauses below while
retaining the protected-object, exact-approval, lifecycle, and
unsupported-backend rules.

These policy details are accepted; enforcement remains staged and incomplete.

### Target and scope

- One request names one exact file beneath an explicitly selected root. The
  root is a lookup boundary, not a read grant. There is no directory listing,
  recursive read, write, rename, deletion, or shell operation.
- The initial profile accepts only a regular file opened by the supported
  constrained lookup. Symlinks, magic links, mount crossing, non-regular
  objects, unsupported lookup flags, and incomplete protected-directory
  coverage remain refused. A pinned directory is not treated as a complete
  descendant inventory.
- Approval binds the invocation to the requested route, selected root
  generation, opened object identity, current classification, and relevant
  policy, coverage, and grant revisions. A changed target or classification
  requires fresh preparation and approval.
- Authority follows the opened object under B. A later rename or alias does
  not by itself revoke an already approved read. This does not promise that
  the object remains beneath the live root pathname, freeze in-place bytes, or
  prevent a different sensitive object from being copied into it.

### Registered protections and classification

| Target classification | Proposed rule | Consequence and limit |
| --- | --- | --- |
| Registered auth/credential route or identity | Deny generic read regardless of root/file grant. Deny through aliases and after rename. Reserved absent routes and retired pins remain protected. | An approval dialog cannot override the trusted owner registration. Only the registered route/object is covered by this guarantee. |
| Registered settings route or identity | Same non-approvable denial as auth/settings policy. | A broad root grant cannot override the registration. |
| Registered provider-source route or identity | Require explicit per-invocation provider-source approval bound to route, opened identity, classification, invocation, and selected-provider disclosure. An ordinary-file approval is insufficient. Keep source objects immutable. | The requirement applies only when trusted owner integration actually classified the route/object. Root discovery or a filename pattern is not that integration. |
| Otherwise-unregistered exact file | Permit only after an explicit once approval that states the exact target and that the bytes will be provided to the selected model/provider. | The object might be an unrelated credential, database, backup, provider record, or independent sensitive copy. The approval deliberately authorizes that disclosure; KompoSyn does not certify the object as non-sensitive. |
| Incomplete protected-directory scope, unsupported class, or unavailable backend | Refuse. | Explicit approval cannot compensate for missing owner coverage, unavailable enforcement, or unsupported path semantics. |

**Credential/source policy consequence requiring an explicit answer:** under
Choice B, known registered auth/settings routes and identities remain
non-approvable, and registered provider sources remain separately
once-approvable. An unrelated credential or provider record that has no
authoritative registration is indistinguishable from another unregistered
file. If the intended invariant is “no credential or provider-source bytes
may ever be disclosed,” Choice B is not sufficient without a trustworthy way
to classify those unregistered objects; choose A or define and review a
separate bounded registration mechanism. Do not imply that user confirmation
detects their contents.

### Approval, root, backend, and publication

- **Recommend once-only approval** for the initial read profile. Do not offer a
  conversation-lifetime read grant until separately reviewed; do not reuse or
  broaden existing grant semantics silently.
- The approval must say in plain language that read permission includes
  returning the retrieved bytes to the currently selected model/provider.
  It must name the exact requested file and distinguish an unregistered
  object from a registered source. Avoid displaying credential values.
- Root selection, supported-platform/backend availability, composition
  coverage, classification, and permission approval are independent checks.
  Selecting a root grants no access. A warning or policy acknowledgement does
  not make the backend available or establish owner coverage.
- Root, policy, coverage, grant, invocation, or target revision changes before
  release require fresh authorization. Revocation prevents dispatch and
  publication; it must suppress captured stale bytes and confirm worker join
  before reporting cleanup complete. An incomplete cleanup remains a
  blocking obligation and is reported truthfully.
- Do not restore or replay a consumed once grant after an uncertain read.
  Distinguish refusal/not-executed before release from unknown/incomplete
  outcomes after capability release.

## Exact statements affected by Choice B

If the maintainer accepts B, update the policy/status statements that say
**all generic exact-file reads from arbitrary selected roots remain
unavailable solely because unregistered root data is unknown**, including
the unknown-root gate in `initial-sandbox-composition.md`, the corresponding
gate/status in `composition-admission-sandbox-service.md`, and the user-facing
Harness README. Replace them with the narrower rule: unknown files remain
unclassified; a one-time exact-file disclosure approval can authorize a
regular unregistered object, while registered protected routes/identities,
registered provider sources, incomplete directory scopes, and unsupported
enforcement retain their independent rules.

The following do **not** change: filesystem tools are unavailable until the
implementation and native acceptance are reviewed; auth/settings registered
objects cannot be overridden by generic grants; provider-source classification
requires explicit per-call approval; directory coverage remains fail-closed;
root selection is not authority; once consumption remains runtime-owned;
stale results are suppressed; the worker stays fixed-purpose and networkless;
Token Logger integration, filesystem mutation, shell, and persistent state
remain separate gates.

## Decision table for maintainers

| Question | Recommendation | Alternative and tradeoff |
| --- | --- | --- |
| Can a user read a file with no registered owner/class? | **Yes, after exact-file once approval that explicitly authorizes disclosure.** | Keep refusing until authoritative coverage exists; strongest default protection but prevents generic arbitrary-root reads. |
| Does approval imply that the file is non-sensitive? | **No.** State that an unregistered object may contain credentials, database contents, provider records, or copies. | A categorical no-secret guarantee requires an authoritative classifier/owner source or continued refusal. |
| Can a broad root or conversation grant override registered auth/settings? | **No.** | No proposed alternative. |
| Can an ordinary-file grant read a classified provider source? | **No; require the source-specific once approval.** | No proposed alternative. |
| Should first release offer “allow for this conversation” for generic reads? | **No; once only.** | Add conversation lifetime only after a separate risk/UX decision; it makes repeated disclosure easier. |
| Does selecting a root or seeing a warning enable reads? | **No.** | No proposed alternative. |

Concrete example: if `~/work/data.txt` is unregistered, a user choosing
“Read this file and share it with the selected model” authorizes that exact
opened object once, even if the file turns out to contain a token or copied
provider data. If `~/.komposyn/auth.json` or a registered alias is targeted,
the service refuses before asking for ordinary approval. If a trusted provider
owner classifies the target as a provider source, only the separate
provider-source approval can authorize that invocation.

## Staged implementation outline

The maintainer accepted the six-clause policy in the [issue decision
comment](https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6709361902).
Implementation remains gated by separate reviewed stages; policy acceptance
alone does not enable reads.

1. **Record the policy and threat-model choice.** Complete. Keep no
   implementation flag or default bypass.
2. **Design root UX separately from permission.** Stage 1 adds explicit absolute-root
   selection for the in-memory conversation; show canonical root and backend
   status; clear on reset/shutdown; invalidate pending approvals and prepared
   work on root change. Preserve chat operation without a root.
3. **Implement runtime-owned exact-file preparation.** Validate one supported
   regular file, classify registered routes and identities before ordinary
   eligibility, refuse incomplete directory coverage, bind all relevant
   generations, and keep runtime the sole once-grant consumer.
4. **Implement approval and disclosure UX.** For ordinary unregistered files,
   present exact path, once lifetime, and explicit model disclosure. Keep
   provider-source approval distinct; make registered auth/settings
   non-approvable.
5. **Dispatch through the fixed worker only.** Transfer no data descriptor
   before trusted bootstrap, authorization, and generation checks. Capture
   within the bounded private result area, confirm join, then revalidate
   before provider continuation. No shell, directory access, write, or delete.
6. **Preserve revocation and shutdown barriers.** Test active revoke/root/policy
   invalidation, cancellation, retained cleanup, and stale output suppression.
   Keep all cleanup duties bounded and observable.
7. **Run independent native acceptance.** Test mandatory registered denials
   overriding broad roots, once consumption exactly once, unregistered
   admission only under accepted policy, provider-source-specific approval,
   stale preparation, unsupported capability refusal, cancellation, no
   stale provider continuation, and safe cleanup. Use synthetic provider/data
   fixtures only; make no real inference call.
8. **Keep enablement as a separate review gate.** Review the changed exact
   mechanism/profile, remaining owner/platform evidence, user disclosure copy,
   and `read_file` registration separately. A green unit suite or this
   historical native result is not that acceptance.

### Copyable next-worker prompt outline

> Implement the explicitly accepted unregistered exact-file read policy in
> KompoSyn issue #215. Preserve and link the maintainer's accepted decision at
> https://github.com/ayeshLK/komposyn/issues/215#issuecomment-6709361902 and
> preserve every registered auth/settings/provider-source rule. Keep the
> current gate closed until the accepted policy, explicit root UX, once-only
> approval and runtime-owned preparation are all represented. Implement one
> regular exact file under the selected root through the existing fixed
> worker; no listing, recursion, mutation, shell, provider credentials, or
> real inference. Bind requested route, opened identity, classification and
> root/policy/coverage/grant revisions; preserve runtime-only once consumption,
> denial-before-handoff for registered auth/settings, separate per-call
> approval for classified provider sources, directory-coverage refusal,
> cancellation/revocation join, bounded cleanup, and stale-result suppression.
> Add synthetic end-to-end tests for the accepted unknown-file disclosure and
> all mandatory denials. Then produce a separate native acceptance report and
> request a distinct `read_file` enablement review. Do not alter the policy or
> register the tool based on the prompt alone.
