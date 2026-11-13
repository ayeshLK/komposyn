// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tokenlogger

const schemaMigrationOne = `
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    root TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id),
    client_name TEXT NOT NULL,
    client_version TEXT NOT NULL DEFAULT '',
    client_mode TEXT NOT NULL DEFAULT '',
    started_at TEXT,
    ended_at TEXT,
    source_metadata TEXT
);

CREATE TABLE usage_events (
    event_id TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    project_id TEXT REFERENCES projects(id),
    session_id TEXT REFERENCES sessions(id),
    request_id TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    operation TEXT NOT NULL DEFAULT '',
    client_name TEXT NOT NULL,
    client_version TEXT NOT NULL DEFAULT '',
    client_mode TEXT NOT NULL DEFAULT '',
    provider_name TEXT NOT NULL,
    requested_model TEXT NOT NULL DEFAULT '',
    resolved_model TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    cache_read_tokens INTEGER NOT NULL,
    cache_write_tokens INTEGER NOT NULL,
    reasoning_tokens INTEGER NOT NULL,
    cost_status TEXT NOT NULL DEFAULT '',
    cost_amount_micros INTEGER,
    cost_currency TEXT NOT NULL DEFAULT '',
    pricing_version TEXT NOT NULL DEFAULT '',
    provider_cost_amount_micros INTEGER,
    provider_cost_currency TEXT NOT NULL DEFAULT '',
    source_adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_record_id TEXT NOT NULL,
    source_offset INTEGER,
    source_record_hash TEXT NOT NULL DEFAULT '',
    ingestion_key TEXT NOT NULL UNIQUE,
    attributes_json TEXT
);

CREATE TABLE source_cursors (
    source_id TEXT PRIMARY KEY,
    file_fingerprint TEXT NOT NULL DEFAULT '',
    byte_offset INTEGER NOT NULL DEFAULT 0,
    last_provider_record_id TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);

CREATE TABLE pricing_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_name TEXT NOT NULL,
    model_selector TEXT NOT NULL,
    token_category TEXT NOT NULL,
    rate_micros INTEGER NOT NULL,
    currency TEXT NOT NULL,
    effective_from TEXT NOT NULL,
    effective_to TEXT,
    pricing_version TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT ''
);

CREATE INDEX usage_events_occurred_at_idx ON usage_events(occurred_at);
CREATE INDEX usage_events_project_session_idx ON usage_events(project_id, session_id);
CREATE INDEX usage_events_provider_client_model_idx ON usage_events(provider_name, client_name, resolved_model);
CREATE INDEX usage_events_source_idx ON usage_events(source_id, source_record_id);
`

const eventInsertSQL = `INSERT INTO usage_events (
    event_id, schema_version, event_type, occurred_at, started_at, completed_at,
    project_id, session_id, request_id, attempt, operation, client_name,
    client_version, client_mode, provider_name, requested_model, resolved_model,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    reasoning_tokens, cost_status, cost_amount_micros, cost_currency,
    pricing_version, provider_cost_amount_micros, provider_cost_currency,
    source_adapter, source_id, source_record_id, source_offset,
    source_record_hash, ingestion_key, attributes_json, billing_context_json, cache_write_5m_tokens, cache_write_1h_tokens
) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ingestion_key) DO NOTHING`

const legacyEventInsertSQL = `INSERT INTO usage_events (
    event_id, schema_version, event_type, occurred_at, started_at, completed_at,
    project_id, session_id, request_id, attempt, operation, client_name,
    client_version, client_mode, provider_name, requested_model, resolved_model,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    reasoning_tokens, cost_status, cost_amount_micros, cost_currency,
    pricing_version, provider_cost_amount_micros, provider_cost_currency,
    source_adapter, source_id, source_record_id, source_offset,
    source_record_hash, ingestion_key, attributes_json
) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ingestion_key) DO NOTHING`

const schemaMigrationTwo = `
CREATE TABLE diagnostics (
    fingerprint TEXT PRIMARY KEY,
    message TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX diagnostics_status_idx ON diagnostics(status, last_seen_at);
`

const schemaMigrationFour = `
ALTER TABLE source_cursors ADD COLUMN source_generation INTEGER NOT NULL DEFAULT 0;
`

const schemaMigrationFive = `
CREATE UNIQUE INDEX IF NOT EXISTS pricing_rules_catalog_entry_id_idx ON pricing_rules(catalog_entry_id) WHERE catalog_entry_id IS NOT NULL;
`

const schemaMigrationSix = `
CREATE TABLE IF NOT EXISTS project_locations (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    canonical_path TEXT NOT NULL,
    display_path TEXT NOT NULL,
    is_preferred INTEGER NOT NULL DEFAULT 0,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    provenance TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(project_id, canonical_path)
);
CREATE INDEX IF NOT EXISTS project_locations_path_idx ON project_locations(canonical_path);

CREATE TABLE IF NOT EXISTS project_aliases (
    scope TEXT NOT NULL,
    alias TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provenance TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY(scope, alias)
);
CREATE INDEX IF NOT EXISTS project_aliases_project_idx ON project_aliases(project_id);

CREATE TABLE IF NOT EXISTS session_aliases (
    scope TEXT NOT NULL,
    alias TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id),
    provenance TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY(scope, alias)
);
CREATE INDEX IF NOT EXISTS session_aliases_session_idx ON session_aliases(session_id);

CREATE TABLE IF NOT EXISTS reconciliation_warnings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    alias_digest TEXT NOT NULL DEFAULT '',
    project_id TEXT,
    session_id TEXT,
    message TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    UNIQUE(kind, scope, alias_digest, project_id, session_id)
);
`

const schemaMigrationSeven = `
CREATE TABLE reconciliation_warnings_v7 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    alias_digest TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    UNIQUE(kind, scope, alias_digest, project_id, session_id)
);

INSERT INTO reconciliation_warnings_v7 (
    kind, scope, alias_digest, project_id, session_id, message,
    first_seen_at, last_seen_at, occurrence_count
)
SELECT kind, scope, alias_digest, COALESCE(project_id, ''), COALESCE(session_id, ''),
       MIN(message), MIN(first_seen_at), MAX(last_seen_at), SUM(occurrence_count)
FROM reconciliation_warnings
GROUP BY kind, scope, alias_digest, COALESCE(project_id, ''), COALESCE(session_id, '');

DROP TABLE reconciliation_warnings;
ALTER TABLE reconciliation_warnings_v7 RENAME TO reconciliation_warnings;
`

const schemaMigrationNine = `
ALTER TABLE source_cursors ADD COLUMN pending_tail INTEGER NOT NULL DEFAULT 0;
ALTER TABLE source_cursors ADD COLUMN snapshot_reusable INTEGER NOT NULL DEFAULT 0;
`

const schemaMigrationTen = `
CREATE TABLE collector_source_identities (
    canonical_key TEXT NOT NULL,
    adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(canonical_key, adapter),
    UNIQUE(adapter, source_id)
);
`

const schemaMigrationEleven = `
CREATE TABLE collector_sources (
    id TEXT PRIMARY KEY,
    canonical_path TEXT NOT NULL UNIQUE,
    display_path TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    latest_status TEXT NOT NULL DEFAULT 'unknown',
    latest_error TEXT NOT NULL DEFAULT '',
    last_checked_at TEXT
);
CREATE INDEX collector_sources_enabled_idx ON collector_sources(enabled, canonical_path);

CREATE TABLE collector_runtime (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    instance_id TEXT NOT NULL DEFAULT '',
    pid INTEGER NOT NULL DEFAULT 0,
    executable_version TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'stopped',
    interval_millis INTEGER NOT NULL DEFAULT 5000,
    started_at TEXT,
    heartbeat_at TEXT,
    last_poll_at TEXT,
    last_completed_at TEXT,
    stopped_at TEXT,
    stop_requested_at TEXT,
    imported INTEGER NOT NULL DEFAULT 0,
    duplicates INTEGER NOT NULL DEFAULT 0,
    ignored_non_usage INTEGER NOT NULL DEFAULT 0,
    unchanged_snapshot INTEGER NOT NULL DEFAULT 0,
    deferred_incomplete_tail INTEGER NOT NULL DEFAULT 0,
    unsupported_usage INTEGER NOT NULL DEFAULT 0,
    malformed_record INTEGER NOT NULL DEFAULT 0,
    failed_source INTEGER NOT NULL DEFAULT 0,
    pending INTEGER NOT NULL DEFAULT 0,
    source_count INTEGER NOT NULL DEFAULT 0,
    discovered_sources INTEGER NOT NULL DEFAULT 0,
    discovery_failures INTEGER NOT NULL DEFAULT 0,
    recovered_from_stale INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO collector_runtime(singleton) VALUES (1);

CREATE TABLE collector_runs (
    instance_id TEXT PRIMARY KEY,
    pid INTEGER NOT NULL DEFAULT 0,
    executable_version TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    interval_millis INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    stopped_at TEXT,
    terminal_state TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);
`

const schemaMigrationTwelve = `
ALTER TABLE usage_events ADD COLUMN billing_context_json TEXT;
ALTER TABLE pricing_rules ADD COLUMN service_tier_selector TEXT NOT NULL DEFAULT '';
ALTER TABLE pricing_rules ADD COLUMN region_selector TEXT NOT NULL DEFAULT '';
`

const schemaMigrationThirteenCacheWrite5m = `ALTER TABLE usage_events ADD COLUMN cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0`
const schemaMigrationThirteenCacheWrite1h = `ALTER TABLE usage_events ADD COLUMN cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0`
const schemaMigrationFourteenSpeedSelector = `ALTER TABLE pricing_rules ADD COLUMN speed_selector TEXT NOT NULL DEFAULT ''`

const schemaMigrationSixteenParserCheckpoints = `
CREATE TABLE parser_checkpoints (
    source_id TEXT PRIMARY KEY,
    adapter TEXT NOT NULL,
    parser_revision INTEGER NOT NULL,
    format_version INTEGER NOT NULL,
    source_generation INTEGER NOT NULL,
    boundary_offset INTEGER NOT NULL,
    prefix_fingerprint TEXT NOT NULL,
    state_json BLOB NOT NULL,
    state_digest TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
`

// Schema migration 17 records only name provenance demonstrated by the
// current import path. Historical rows deliberately remain unknown.
const schemaMigrationSeventeenProjectNameSource = `
ALTER TABLE projects ADD COLUMN display_name_source TEXT NOT NULL DEFAULT 'unknown'
    CHECK (display_name_source IN ('unknown', 'fallback', 'cwd', 'provider', 'explicit'));
`

// Schema migration 18 adds a separate bounded diagnostic evidence model. The
// random namespace key is created in the migration transaction and is
// preserved by the normal online-backup mechanism.
const schemaMigrationEighteenDiagnosticEvidence = `
CREATE TABLE diagnostic_detail_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    namespace_key BLOB NOT NULL CHECK (length(namespace_key) = 32),
    overflow_count TEXT NOT NULL DEFAULT '0',
    overflow_saturated INTEGER NOT NULL DEFAULT 0,
    next_evidence_order TEXT NOT NULL DEFAULT '0',
    problem_count INTEGER NOT NULL DEFAULT 0 CHECK (problem_count BETWEEN 0 AND 4096),
    persistence_available INTEGER NOT NULL DEFAULT 1
);
INSERT INTO diagnostic_detail_meta(singleton, namespace_key) VALUES (1, randomblob(32));

CREATE TABLE diagnostic_details (
    problem_id TEXT PRIMARY KEY CHECK (length(problem_id) = 64),
    created_at TEXT NOT NULL,
    component TEXT NOT NULL,
    operation TEXT NOT NULL,
    reason TEXT NOT NULL,
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    adapter TEXT NOT NULL DEFAULT '',
    subject_kind TEXT NOT NULL,
    subject_reference TEXT NOT NULL DEFAULT '',
    subject_availability TEXT NOT NULL,
    identity_scope TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    last_positive_at TEXT NOT NULL,
    latest_poll_id TEXT NOT NULL DEFAULT '',
    latest_poll_order TEXT NOT NULL DEFAULT '0',
    latest_poll_count TEXT NOT NULL DEFAULT '0',
    latest_poll_available TEXT NOT NULL DEFAULT 'unknown',
    latest_poll_saturated INTEGER NOT NULL DEFAULT 0,
    finding_slot_cursor INTEGER NOT NULL DEFAULT 0 CHECK (finding_slot_cursor BETWEEN 0 AND 63),
    observation_history_truncated INTEGER NOT NULL DEFAULT 0,
    latest_run_id TEXT NOT NULL DEFAULT '',
    latest_run_order TEXT NOT NULL DEFAULT '0',
    latest_run_count TEXT NOT NULL DEFAULT '0',
    latest_run_available TEXT NOT NULL DEFAULT 'unknown',
    latest_run_saturated INTEGER NOT NULL DEFAULT 0,
    lifetime_count TEXT NOT NULL DEFAULT '0',
    lifetime_saturated INTEGER NOT NULL DEFAULT 0,
    condition TEXT NOT NULL CHECK (condition IN ('active', 'resolved', 'unknown')),
    condition_as_of TEXT NOT NULL,
    condition_generation TEXT NOT NULL DEFAULT '',
    condition_offset TEXT NOT NULL DEFAULT '',
    condition_order TEXT NOT NULL DEFAULT '0',
    condition_coverage TEXT NOT NULL DEFAULT 'unknown',
    condition_reason TEXT NOT NULL,
    material_revision TEXT NOT NULL DEFAULT '1',
    review_state TEXT NOT NULL DEFAULT 'pending' CHECK (review_state IN ('pending', 'reviewed', 'skipped')),
    reviewed_revision TEXT NOT NULL DEFAULT '0',
    reviewed_at TEXT NOT NULL DEFAULT '',
    samples_truncated INTEGER NOT NULL DEFAULT 0,
    overflowed INTEGER NOT NULL DEFAULT 0,
    legacy_fingerprint TEXT
);
CREATE INDEX diagnostic_details_created_idx ON diagnostic_details(created_at, problem_id);
CREATE INDEX diagnostic_details_review_idx ON diagnostic_details(review_state, created_at, problem_id);
CREATE INDEX diagnostic_details_condition_idx ON diagnostic_details(condition, created_at, problem_id);
CREATE INDEX diagnostic_details_category_idx ON diagnostic_details(category, reason, created_at, problem_id);
CREATE INDEX diagnostic_details_subject_idx ON diagnostic_details(subject_reference, created_at, problem_id);

CREATE TABLE diagnostic_detail_samples (
    problem_id TEXT NOT NULL REFERENCES diagnostic_details(problem_id) ON DELETE CASCADE,
    slot INTEGER NOT NULL CHECK (slot BETWEEN 0 AND 3),
    byte_offset TEXT NOT NULL DEFAULT '',
    line_number TEXT NOT NULL DEFAULT '',
    generation TEXT NOT NULL DEFAULT '',
    coverage TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(problem_id, slot)
);

CREATE TABLE diagnostic_detail_findings (
    problem_id TEXT NOT NULL REFERENCES diagnostic_details(problem_id) ON DELETE CASCADE,
    slot INTEGER NOT NULL CHECK (slot BETWEEN 0 AND 63),
    finding_key TEXT NOT NULL,
    evidence_order TEXT NOT NULL,
    PRIMARY KEY(problem_id, slot),
    UNIQUE(problem_id, finding_key)
);
`

// Schema migration 19 adds the durable, append-only allocation history used
// by explicit historical recovery. Base usage_events remain untouched.
const schemaMigrationNineteenRecoveryAllocations = `
CREATE TABLE recovery_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    namespace_key BLOB NOT NULL CHECK (length(namespace_key) = 32),
    format_version INTEGER NOT NULL CHECK (format_version = 1)
);
INSERT INTO recovery_meta(singleton, namespace_key, format_version) VALUES (1, randomblob(32), 1);

CREATE TABLE recovery_runs (
    run_id TEXT PRIMARY KEY,
    plan_digest TEXT NOT NULL UNIQUE CHECK (length(plan_digest) = 64),
    format_version INTEGER NOT NULL CHECK (format_version = 1),
    source_id TEXT NOT NULL,
    source_generation INTEGER NOT NULL,
    snapshot_digest TEXT NOT NULL CHECK (length(snapshot_digest) = 64),
    snapshot_size INTEGER NOT NULL CHECK (snapshot_size >= 0),
    inserted_event_count INTEGER NOT NULL CHECK (inserted_event_count >= 0),
    overridden_event_count INTEGER NOT NULL CHECK (overridden_event_count >= 0),
    audit_json BLOB NOT NULL CHECK (length(audit_json) <= 16777216),
    committed_at TEXT NOT NULL
);

CREATE TABLE recovery_allocations (
    allocation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES recovery_runs(run_id),
    event_id TEXT NOT NULL REFERENCES usage_events(event_id),
    entry_kind TEXT NOT NULL CHECK (entry_kind IN ('override', 'inserted')),
    previous_allocation_id TEXT REFERENCES recovery_allocations(allocation_id),
    before_input_tokens INTEGER,
    before_output_tokens INTEGER,
    before_cache_read_tokens INTEGER,
    before_cache_write_tokens INTEGER,
    before_cache_write_5m_tokens INTEGER,
    before_cache_write_1h_tokens INTEGER,
    before_reasoning_tokens INTEGER,
    after_input_tokens INTEGER NOT NULL CHECK (after_input_tokens >= 0),
    after_output_tokens INTEGER NOT NULL CHECK (after_output_tokens >= 0),
    after_cache_read_tokens INTEGER NOT NULL CHECK (after_cache_read_tokens >= 0),
    after_cache_write_tokens INTEGER NOT NULL CHECK (after_cache_write_tokens >= 0),
    after_cache_write_5m_tokens INTEGER NOT NULL CHECK (after_cache_write_5m_tokens >= 0),
    after_cache_write_1h_tokens INTEGER NOT NULL CHECK (after_cache_write_1h_tokens >= 0),
    after_reasoning_tokens INTEGER NOT NULL CHECK (after_reasoning_tokens >= 0),
    created_at TEXT NOT NULL,
    UNIQUE(allocation_id, event_id),
    UNIQUE(run_id, event_id)
);
CREATE INDEX recovery_allocations_event_idx ON recovery_allocations(event_id, created_at, allocation_id);

CREATE TABLE current_event_allocations (
    event_id TEXT PRIMARY KEY REFERENCES usage_events(event_id),
    allocation_id TEXT NOT NULL UNIQUE,
    FOREIGN KEY (allocation_id, event_id) REFERENCES recovery_allocations(allocation_id, event_id)
);
CREATE TRIGGER current_event_allocations_override_only
BEFORE INSERT ON current_event_allocations
WHEN (SELECT entry_kind FROM recovery_allocations WHERE allocation_id = NEW.allocation_id) != 'override'
BEGIN SELECT RAISE(ABORT, 'only override allocations can become current heads'); END;
CREATE TRIGGER current_event_allocations_update_override_only
BEFORE UPDATE ON current_event_allocations
WHEN (SELECT entry_kind FROM recovery_allocations WHERE allocation_id = NEW.allocation_id) != 'override'
BEGIN SELECT RAISE(ABORT, 'only override allocations can become current heads'); END;

CREATE TABLE source_allocation_revisions (
    source_id TEXT PRIMARY KEY,
    source_generation INTEGER NOT NULL,
    revision INTEGER NOT NULL CHECK (revision >= 0)
);

CREATE TRIGGER recovery_runs_no_update BEFORE UPDATE ON recovery_runs BEGIN SELECT RAISE(ABORT, 'recovery runs are append-only'); END;
CREATE TRIGGER recovery_runs_no_delete BEFORE DELETE ON recovery_runs BEGIN SELECT RAISE(ABORT, 'recovery runs are append-only'); END;
CREATE TRIGGER recovery_allocations_no_update BEFORE UPDATE ON recovery_allocations BEGIN SELECT RAISE(ABORT, 'recovery allocations are append-only'); END;
CREATE TRIGGER recovery_allocations_no_delete BEFORE DELETE ON recovery_allocations BEGIN SELECT RAISE(ABORT, 'recovery allocations are append-only'); END;

CREATE VIEW effective_usage_events AS
SELECT e.event_id, e.schema_version, e.event_type, e.occurred_at, e.started_at, e.completed_at,
       e.project_id, e.session_id, e.request_id, e.attempt, e.operation, e.client_name,
       e.client_version, e.client_mode, e.provider_name, e.requested_model, e.resolved_model,
       COALESCE(a.after_input_tokens, e.input_tokens) AS input_tokens,
       COALESCE(a.after_output_tokens, e.output_tokens) AS output_tokens,
       COALESCE(a.after_cache_read_tokens, e.cache_read_tokens) AS cache_read_tokens,
       COALESCE(a.after_cache_write_tokens, e.cache_write_tokens) AS cache_write_tokens,
       COALESCE(a.after_reasoning_tokens, e.reasoning_tokens) AS reasoning_tokens,
       e.cost_status, e.cost_amount_micros, e.cost_currency, e.pricing_version,
       e.provider_cost_amount_micros, e.provider_cost_currency, e.source_adapter, e.source_id,
       e.source_record_id, e.source_offset, e.source_record_hash, e.ingestion_key,
       e.attributes_json, e.billing_context_json,
       COALESCE(a.after_cache_write_5m_tokens, e.cache_write_5m_tokens) AS cache_write_5m_tokens,
       COALESCE(a.after_cache_write_1h_tokens, e.cache_write_1h_tokens) AS cache_write_1h_tokens
FROM usage_events AS e
LEFT JOIN current_event_allocations AS h ON h.event_id = e.event_id
LEFT JOIN recovery_allocations AS a ON a.allocation_id = h.allocation_id AND a.event_id = e.event_id;
`
