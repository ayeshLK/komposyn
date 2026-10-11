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

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the library-owned SQLite persistence boundary.
type Store struct {
	db *sql.DB
}

// Open opens or creates a SQLite database and applies all migrations.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.configure(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure() error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure SQLite: %w", err)
		}
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies numbered schema migrations in order.
func (s *Store) Migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var applied int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&applied); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if applied > currentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", applied, currentSchemaVersion)
	}
	if applied < 1 {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration: %w", err)
		}
		if _, err := tx.Exec(schemaMigrationOne); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration 1: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration 1: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration 1: %w", err)
		}
	}
	if applied < 2 {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration 2: %w", err)
		}
		if _, err := tx.Exec(schemaMigrationTwo); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration 2: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 2, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration 2: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration 2: %w", err)
		}
	}
	return nil
}

// Receipt describes the result of an at-least-once append.
type Receipt struct {
	Imported   int
	Duplicates int
}

// UpsertProject creates or updates a project identity.
func (s *Store) UpsertProject(project Project) error {
	if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.DisplayName) == "" {
		return errors.New("project ID and display name are required")
	}
	_, err := s.db.Exec(`INSERT INTO projects(id, root, display_name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET root = excluded.root, display_name = excluded.display_name`,
		project.ID, project.Root, project.DisplayName, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// UpsertSession creates or updates a session identity.
func (s *Store) UpsertSession(session Session) error {
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.Client.Name) == "" {
		return errors.New("session ID and client name are required")
	}
	if session.SourceMetadata != nil && !json.Valid(session.SourceMetadata) {
		return errors.New("session source metadata must contain valid JSON")
	}
	_, err := s.db.Exec(`INSERT INTO sessions(
		id, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id = excluded.project_id,
		client_name = excluded.client_name, client_version = excluded.client_version,
		client_mode = excluded.client_mode, started_at = excluded.started_at,
		ended_at = excluded.ended_at, source_metadata = excluded.source_metadata`,
		session.ID, session.ProjectID, session.Client.Name, session.Client.Version,
		session.Client.Mode, nullableTime(session.StartedAt), nullableTime(session.EndedAt),
		nullableJSON(session.SourceMetadata))
	return err
}

// Append persists events, deduplicating by stable ingestion key.
func (s *Store) Append(events []UsageEvent) (Receipt, error) {
	return s.append(events, nil)
}

// AppendWithCursor persists events and advances the source cursor atomically.
// A failed event insert cannot leave the cursor advanced.
func (s *Store) AppendWithCursor(events []UsageEvent, cursor Cursor) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	return s.append(events, &cursor)
}

func (s *Store) append(events []UsageEvent, cursor *Cursor) (Receipt, error) {
	for _, event := range events {
		if err := event.Validate(); err != nil {
			return Receipt{}, fmt.Errorf("validate event %q: %w", event.EventID, err)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Receipt{}, fmt.Errorf("begin append: %w", err)
	}
	receipt := Receipt{}
	for _, event := range events {
		result, err := tx.Exec(eventInsertSQL,
			event.EventID, event.SchemaVersion, event.EventType, event.OccurredAt.Format(time.RFC3339Nano),
			nullableTime(event.StartedAt), nullableTime(event.CompletedAt), event.ProjectID, event.SessionID,
			event.RequestID, event.Attempt, event.Operation, event.Client.Name, event.Client.Version,
			event.Client.Mode, event.Provider, event.RequestedModel, event.ResolvedModel,
			event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.CacheReadTokens,
			event.Usage.CacheWriteTokens, event.Usage.ReasoningTokens, event.Cost.Status,
			nullableInt64(event.Cost.AmountMicros), event.Cost.Currency, event.Cost.PricingVersion,
			nullableInt64(event.Cost.ProviderAmount), event.Cost.ProviderCurrency, event.Provenance.Adapter,
			event.Provenance.SourceID, event.Provenance.SourceRecord, nullableInt64(event.Provenance.SourceOffset),
			event.Provenance.RecordHash, event.IngestionKey, nullableJSON(event.Attributes))
		if err != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("persist event %q: %w", event.EventID, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("inspect event %q insert: %w", event.EventID, err)
		}
		if count == 1 {
			receipt.Imported++
		} else {
			receipt.Duplicates++
		}
	}
	if cursor != nil {
		if _, err := tx.Exec(`INSERT INTO source_cursors(source_id, file_fingerprint, byte_offset, last_provider_record_id, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(source_id) DO UPDATE SET file_fingerprint = excluded.file_fingerprint,
			byte_offset = excluded.byte_offset, last_provider_record_id = excluded.last_provider_record_id,
			updated_at = excluded.updated_at`, cursor.SourceID, cursor.FileFingerprint, cursor.ByteOffset,
			cursor.LastProviderRecordID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("persist source cursor: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Receipt{}, fmt.Errorf("commit append: %w", err)
	}
	return receipt, nil
}

// EventCount returns the number of persisted usage events.
func (s *Store) EventCount() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&count)
	return count, err
}

// Cursor returns the committed cursor for a source.
func (s *Store) Cursor(sourceID string) (Cursor, error) {
	var cursor Cursor
	err := s.db.QueryRow(`SELECT source_id, file_fingerprint, byte_offset, last_provider_record_id
		FROM source_cursors WHERE source_id = ?`, sourceID).Scan(&cursor.SourceID, &cursor.FileFingerprint,
		&cursor.ByteOffset, &cursor.LastProviderRecordID)
	if err != nil {
		return Cursor{}, err
	}
	return cursor, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Format(time.RFC3339Nano)
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
