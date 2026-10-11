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
