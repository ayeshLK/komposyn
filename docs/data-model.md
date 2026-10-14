# Data model

The library stores normalized usage data in SQLite. Provider-specific fields
that do not fit the common model may be retained in `usage_events.attributes_json`.

## Core tables

| Table | Purpose |
| --- | --- |
| `projects` | A logical project root and display name. |
| `sessions` | A provider session, optionally associated with a project. |
| `usage_events` | One normalized usage record, including token counts, models, costs, and source provenance. |
| `source_cursors` | The committed position, fingerprint, and generation for an imported source. |
| `pricing_rules` | Time-bounded provider/model/token-category rates used for cost calculation. |
| `diagnostics` | Deduplicated operational failures and their occurrence state. |
| `schema_migrations` | Applied schema migration versions and timestamps. |

`usage_events` is the durable fact table. Its foreign keys to `projects` and
`sessions` are nullable because an event can be imported before a usable
reference is available. When references are known, the CLI and adapters pass
them to `AppendWithReferences`, which creates or updates the references and
inserts events in one transaction.

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

An append transaction may include project rows, session rows, usage events, and
the source cursor. If any part fails, none of those changes are committed. This
prevents metadata from referring to an event that was not stored and prevents a
cursor from advancing past an unsuccessful import.

Diagnostics are intentionally separate from usage facts. Repeated failures are
folded by fingerprint and update occurrence counts and last-seen timestamps,
while sanitized text is exposed to CLI surfaces.
