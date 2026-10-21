# Project inspection evidence

Store.InspectProject is a bounded, read-only library query for persisted
evidence associated with one KompoSyn project ID. It returns a versioned
ProjectInspection response; it does not change the existing JSON/CSV report
formats or the standalone CLI protocol-v1 messages.

## Calling the API

The request requires a project ID and a non-empty UTC half-open time range:

```go
inspection, err := store.InspectProject(tokenlogger.ProjectInspectionRequest{
    ProjectID: projectID,
    From:      from.UTC(),
    To:        to.UTC(),
    Limit:     20,
    Offset:    0,
})
```

From is inclusive and To is exclusive. Limit defaults to 20 and is restricted
to 1–50. Offset is restricted to 0–100,000. Locations, aliases, warnings, and
source evidence each carry their own total and has_more page metadata, with
deterministic ordering. Each call reads one SQLite transaction snapshot.
Separate page calls do not share a snapshot token, so a concurrent import
between calls can change the offsets represented by later pages.

The response is bounded, but SQLite work is not a fixed-cost operation:
selected/history aggregates scale with the project's stored events, and
evidence total counts scale with persisted references. Each returned page is
limited to at most 50 items. Offset has a hard ceiling; if a page reports
truncated=true, additional records exist beyond the accessible page range.

The query itself performs only SQL reads. It does not open provider files,
start or stop collection, refresh report ranges, call the network, write
diagnostics, or mutate project/session identities. It returns hashes instead
of raw filesystem locations, aliases, scopes, or source IDs. Warning kinds,
messages, adapters, and provenance labels are allowlisted before returning.

This does not make opening the database read-only. Store.Open retains its
existing safe-open behavior: it can create the database, apply required
numbered migrations after making and verifying a pre-migration backup, and
synchronize the embedded pricing catalog. Call InspectProject on an already
opened Store when the caller needs the query's no-write guarantee.

## Reading the response

- stored_session_count counts persisted session rows (or null-project session
  rows for the synthetic Unattributed bucket) independent of the selected
  range.
- selected_usage_session_count counts sessions represented by accepted usage
  events in the selected range. selected_usage uses the existing report
  calculation and preserves cost provenance. selected_activity separately
  reports event count, sessionless-event count, and activity bounds.
  has_matching_events=false means only that the selected range has no
  matching stored events.
- historical_activity aggregates all currently persisted usage_events.
  historical_coverage is always stored_events_only: it does not establish
  complete provider history, lifetime emptiness, source availability, or
  whether an import was ever attempted.
- warnings are all-time reconciliation warnings, not collector-run outcomes.
  Their page metadata reports the full warning count even when only one page
  is returned.
- aliases expose whether an alias belongs to a project or session and whether
  its scope is adapter-wide or adapter/client-specific. Alias, scope, and
  location values are represented by stable digests; raw values are not
  exposed. Provenance strings are normalized to a small allowlist.
- sources aggregate persisted event provenance. The adapter is a sanitized
  parser label only; it does not establish which provider directory supplied a
  file. The query joins a cursor only when the complete persisted event
  source_id exactly equals source_cursors.source_id. It does not infer
  prefix, generation, path, or source-directory relationships. source_id is
  returned only as a digest. The cursor fingerprint, when present, is a digest
  of source bytes, not source content.
- Missing projects return exists=false. The reserved
  komposyn:unattributed-project selector describes a synthetic bucket over
  rows whose project reference is null; it is not a persisted project row.
- name_origin is unavailable on schema 16 because that schema stores only the
  current display name. If display_name_source is present, the inspection
  exposes only its allowlisted persisted value; unknown maps to unavailable.
  In particular, a display name such as Unattributed and an adapter label
  cannot prove that a parser fallback created the project.
- classification_evidence is unavailable on schema 16 because the original
  source-classification decision is not stored. A parser label attached to an
  accepted event does not reconstruct that earlier decision.

## Schema evidence and gaps

Schema 16 can supply project/session rows, accepted usage events and their
stored adapter/source/record provenance, project locations, scoped aliases,
reconciliation warnings, and source cursors/checkpoints. It cannot recover a
classifier decision that was not stored, prove that a historical source file
was discovered, or show source content without reading provider-owned files.
No source file is read by this API.

The coordinated #148 change adds projects.display_name_source in schema 17
with controlled values unknown, fallback, cwd, provider, and explicit. It has
no historical backfill: rows migrated from older schemas remain unknown.
When the column is present, inspection exposes the exact allowlisted values
fallback, cwd, provider, and explicit. The explicit value means explicit core
API input; it does not prove that a person authored the name. Unknown maps to
unavailable; inspection must not
reconstruct old label origin from a literal name, alias, or adapter.

Issue #147 addresses positive source classification and unsupported fallback
identities. Its future persisted evidence may make later inspections more
specific, but this API does not infer that evidence for existing records.
Persisted source adapter labels describe which parser accepted an event, not
which provider-owned directory was read.

## Recovery classification

recovery_classification is advisory and never authorizes a mutation:

| Classification | Interpretation |
| --- | --- |
| not_found | No project row or synthetic-bucket evidence exists. This is not a deletion recommendation. |
| retain_metadata_only_session | Sessions exist but no accepted usage event is stored. Keep them; zero usage does not invalidate a session. |
| inactive_in_selected_range | Stored usage exists, but none falls in the selected range. This does not establish complete history. |
| review_reconciliation_evidence | A persisted warning exists. Review its bounded evidence; no merge or reassignment is performed. |
| candidate_name_review | Reserved for a persisted fallback label plus recorded location evidence after schema 17 is integrated. It remains a review candidate, not an automatic rename. |
| insufficient_evidence_leave_unchanged | The legacy label origin is unavailable. Leave uncertain identities untouched. |
| no_repair_indicated | Persisted evidence does not establish a repair need. |

No zero-count classification permits deletion, merge, or identity reset.
Rename, merge, or deletion remains a separate, explicitly reviewed operation
under #113, with preview, confirmation, transaction/concurrency protection,
and an audit trail.
