# Real-store native acceptance for the read broker

**Status:** research-only implementation in the read-broker study. Native
Bubblewrap execution is environment-dependent; neither the code nor this
document claims cases passed on a host until the study records that result.
No filesystem tool is enabled.

The older native owner lifecycle cases use `fixtureOwnerWriter`, which
registers synthetic paths and writes through a synthetic descriptor. Tests in
`cmd/read-broker-study/owner_integration_test.go` call the real auth and
settings stores with `ownerLifecycleTestSession`, not Bubblewrap. This study
adds separately named cases that use the public stores with the actual
`FixtureSession`, study adapter and Bubblewrap worker. The shipped chat now has
a separate host composition adapter and coordinator for auth/settings
protection, but its unknown-root gate prevents target reads. Older rows retain
their historical meaning and are not substituted for these cases.

## Composition exercised by the new cases

```text
auth.Store or settings.Store (explicit disposable override path)
  -> real public store operation
  -> studyOwnerProtection
  -> real FixtureSession and pinned registry
  -> trusted Bubblewrap worker and bounded read-capability protocol
  -> inspected temporary-file identity, content state, publication, cleanup
```

Only synthetic values and temporary files are used. The fixture does not open
user auth/settings paths, use provider credentials, connect to a provider, or
expose synthetic secret bytes in logs or reports. The store receives the
research-only observation adapter through its existing optional protection
hook. `cmd/harness` separately injects its host service through the same store
hooks before loading configuration. Its generic-read gate remains closed and
no model-facing filesystem tool is registered.

The report gives auth and settings distinct IDs under the `real_auth_` and
`real_settings_` prefixes. The additive `real_store` object identifies store,
operation, override/registration state, identity and cleanup correlations,
policy generations, once-grant counts, and stale-publication suppression. It
contains no paths, serialized JSON, credentials, or worker result bytes. Normal
environment preflight still applies: an unavailable trusted Bubblewrap backend
makes native rows unavailable, not passed. The study's observation adapter is
research-only; `cmd/harness` separately wires its own host service adapter to
the same store hooks before loading configuration. This does not enable a
model-facing filesystem tool.

When a case cannot confirm direct worker join, active-operation completion,
and pin release, it is downgraded to `inconclusive` with
`cleanup_pending: true`. The enclosing fixture runner retains its disposable
root for those rows instead of deleting data that a surviving worker may still
reference. The manual summary prints this retention state.

## Implemented cases

| Scenario | Cases and evidence |
|---|---|
| Startup registration and rotation | `real_auth_startup_registration_and_successive_rotations`, `real_settings_startup_absent_route_then_first_save`, and `real_settings_startup_existing_route_and_rotations` use public store operations. Separate rows test current routes, hard-link aliases, renamed retired auth identity, retained pins, and ordinary-read controls. |
| Active-worker barrier | `real_auth_held_worker_cleanup_pending_blocks_write_then_retry` and `real_settings_cleanup_pending_refuses_then_retries` correlate the held worker descriptor to the empty temp object, store refusal, cleanup request, direct wait, once-grant count, and fresh retry identity. |
| Registration failure | `real_auth_injected_registration_failure_preserves_destination` is a deterministic owner-hook failure fixture; `real_auth_failure_retry_after_registration` exercises a fresh real store operation with the study adapter. These rows are owner-lifecycle evidence, not independent namespace-race tests. |
| Publication invalidation | `real_auth_publication_invalidation_after_capture` and `real_settings_publication_invalidation_after_capture` mutate the actual store from the after-capture checkpoint, then check policy generation, worker join, once consumption and publication sink. |

Each native row records worker startup/read/join/publication fields where
applicable. Results use `measured_pass`, `observed_violation`, `unavailable`,
or `inconclusive`; unavailable setup and incomplete observations are not passes.
The per-case `real_store` fields distinguish route reservation, replacement
identity, empty-at-registration state, direct join, write ordering, once-grant
consumption, and stale-publication suppression.

## Reporting correction

The historical `SensitiveBytesPresentBeforeJoin` field was easy to misread. In
older synthetic pending cases it records that the fixture's original bytes
were unchanged before and after attempted owner completion; it does not mean
sensitive bytes were present. A new explicit
`FixtureContentUnchangedBeforeJoin` field preserves the old field for report
compatibility and is used by those case checks. Real-store cases report
`replacement_empty_at_registration`, `store_write_not_started_before_join`,
`store_write_started_after_join`, and
`published_object_matches_registered_temp` directly. The successful
post-capture case also clears its former stale “evidence incomplete” detail.

## Boundaries that remain

This acceptance slice covers auth and settings stores only. It does not
complete Token Logger DB/WAL/SHM/backup registration, provider-source owner
registration, sealed-copy native acceptance, protected ancestor or mount
coverage, arbitrary descendants, natural kernel hangs, or namespace-wide
cleanup. Direct Bubblewrap child join is not proof every namespace descendant
has terminated. Synthetic store values do not establish safety for real
credential or settings data. The study remains experimental and does not
enable a production sandbox service or model-facing filesystem/shell tools.
