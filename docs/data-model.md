# Data model

The library stores normalized usage data in SQLite. Provider-specific fields
that do not fit the common model may be retained in `usage_events.attributes_json`.

## Core tables

| Table | Purpose |
| --- | --- |
| `projects` | A logical project root and display name. |
| `project_locations` | Canonical local paths associated with a project, including preferred and first/last-seen state. |
| `project_aliases` | Adapter-scoped provider project identifiers mapped to local project IDs. |
| `sessions` | A provider session, optionally associated with a project. |
| `session_aliases` | Adapter/client-scoped provider session identifiers mapped to local session IDs. |
| `reconciliation_warnings` | Path-free local records of ambiguous or conflicting identity evidence. |
| `usage_events` | One normalized usage record, including token counts, models, costs, and source provenance. |
| `source_cursors` | The committed position, fingerprint, and generation for an imported source. |
| `pricing_rules` | Time-bounded provider/model/token-category rates used for cost calculation. |
| `diagnostics` | Deduplicated operational failures and their occurrence state. |
| `collector_sources` | Stable enabled/disabled canonical roots and path-explicit local health. |
| `collector_runtime` | Singleton lease, heartbeat, stop request, latest poll, and cumulative outcomes. |
| `collector_runs` | Preserved start/stop state for background and embedded instances. |
| `schema_migrations` | Applied schema migration versions and timestamps. |

`usage_events` is the durable fact table. Its foreign keys to `projects` and
`sessions` are nullable because an event can be imported before a usable
reference is available. When references are known, the CLI and adapters pass
them to `AppendWithReferences`, which creates or updates the references and
inserts events in one transaction.

Resource browsing does not discard events whose project or session reference
is null. The library exposes synthetic `Unattributed` project and session
resources for those events so resource summaries reconcile with the unfiltered
usage total. These synthetic identities are query-layer sentinels and are not
inserted into `projects` or `sessions`; their identifiers are reserved at
persistence boundaries. Migration 8 remaps any older persisted collision and
its references to a collision-free local identifier.

`projects.id` and `sessions.id` are KompoSyn-owned identities. Provider IDs are
aliases rather than primary keys for new adapter imports. Reconciliation first
uses an existing scoped alias, then an absolute cleaned path (with symlinks
resolved when the target exists). A unique location match attaches a new alias
to the existing project. Display names never merge projects. An established
alias may add a new unclaimed location, but conflicting or ambiguous evidence
is retained as a local warning and never silently reassigns a session.
Project resource output includes the local warning count so conflicts remain
visible without exposing full paths in diagnostics or external issue drafts.

Migration 6 seeds every legacy project and session as a valid local identity,
adds compatibility aliases, and records each non-empty legacy root as a
location. It does not rewrite historical event references or merge legacy
projects by name.

## Event identity and provenance

Every event has a unique `event_id` and a unique `ingestion_key`. The ingestion
key is derived from stable source and record identity and is the idempotency
boundary: inserts use `ON CONFLICT(ingestion_key) DO NOTHING`.

The source fields have separate responsibilities:

- `source_adapter` identifies the parser, such as `codex` or `claude`.
- `source_id` identifies the logical source and generation.
- `source_record_id` identifies the provider record within that source.
- `source_offset` records the byte position when the format supports it.
- `source_record_hash` provides content-level provenance.

Token counts are stored as integers. Monetary values are stored as integer
microunits with an explicit currency and pricing version; floating-point money
is not used. Pricing rules retain the legacy `rate_micros` meaning (microunits
per token) and add an exact numerator/denominator representation for rates per
token range. The final event estimate rounds once to the nearest microunit,
with half values rounded away from zero. Migration 5 preserves legacy rules and
adds bundled-catalog identity and source provenance.

The bundled, offline catalog is `plugins/library/token-logger/pricing-catalog.json`.
Catalog entries carry a stable ID, model, token category, exact rate, currency,
effective interval, official source URL, review date, and assumptions. Startup
sync is idempotent and only inserts bundled IDs; it never replaces custom rules.

## Time representation

Persisted timestamps use a fixed-width UTC RFC3339 representation with
nanosecond precision. Migration 3 normalizes older timestamp strings to that
representation. Reporting derives local civil-hour buckets at read time and
retains the event's numeric offset.

## Transaction guarantees

An append transaction may include project aliases and locations, session
aliases, project/session rows, usage events, warnings, and the source cursor.
If any part fails, none of those changes are committed. This
prevents metadata from referring to an event that was not stored and prevents a
cursor from advancing past an unsuccessful import.

Diagnostics are intentionally separate from usage facts. Repeated failures are
folded by fingerprint and update occurrence counts and last-seen timestamps,
while sanitized text is exposed to CLI surfaces.

Collector paths are local administrative metadata. `watch list` may display
them, but diagnostics and issue drafts do not. Lease acquisition, stale
takeover, source changes, and status reads use bounded SQLite coordination and
do not weaken event/reference/cursor transaction atomicity.
