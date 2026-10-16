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
