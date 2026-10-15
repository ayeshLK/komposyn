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

	sqlite3 "modernc.org/sqlite"
)

// Store is the library-owned SQLite persistence boundary.
type Store struct {
	db *sql.DB
}

const storedTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatStoredTime(value time.Time) string {
	return value.UTC().Format(storedTimeLayout)
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
	// Install the wait policy as each driver connection is created. Setting it
	// only in configure is too late when another process is already changing
	// journal mode or applying the initial migrations.
	dsn := path + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	if strings.Contains(path, "?") {
		dsn = path + "&_pragma=busy_timeout(5000)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
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
	if err := store.syncBundledPricingCatalog(); err != nil {
		db.Close()
		return nil, fmt.Errorf("load bundled pricing catalog: %w", err)
	}
	return store, nil
}

func (s *Store) configure() error {
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
	} {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := s.db.Exec(statement); err == nil {
				break
			} else if !sqliteBusy(err) || !time.Now().Before(deadline) {
				return fmt.Errorf("configure SQLite: %w", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

func sqliteBusy(err error) bool {
	var sqliteErr *sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code() & 0xff
	return code == 5 || code == 6
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies numbered schema migrations in order.
func (s *Store) Migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var applied int
	if err := tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&applied); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if applied > currentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", applied, currentSchemaVersion)
	}
	if applied < 1 {
		if _, err := tx.Exec(schemaMigrationOne); err != nil {
			return fmt.Errorf("apply migration 1: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 1, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 1: %w", err)
		}
	}
	if applied < 2 {
		if _, err := tx.Exec(schemaMigrationTwo); err != nil {
			return fmt.Errorf("apply migration 2: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 2, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 2: %w", err)
		}
	}
	if applied < 3 {
		if err := normalizeStoredTimestamps(tx); err != nil {
			return fmt.Errorf("normalize timestamps: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 3, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 3: %w", err)
		}
	}
	if applied < 4 {
		if err := applySourceGenerationMigration(tx); err != nil {
			return fmt.Errorf("apply migration 4: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 4, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 4: %w", err)
		}
	}
	if applied < 5 {
		if err := applyPricingRationalMigration(tx); err != nil {
			return fmt.Errorf("apply migration 5: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 5, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 5: %w", err)
		}
	}
	if applied < 6 {
		if err := applyProjectIdentityMigration(tx); err != nil {
			return fmt.Errorf("apply migration 6: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 6, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 6: %w", err)
		}
	}
	if applied < 7 {
		if _, err := tx.Exec(schemaMigrationSeven); err != nil {
			return fmt.Errorf("apply migration 7: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 7, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 7: %w", err)
		}
	}
	if applied < 8 {
		if err := migrateReservedResourceIDs(tx); err != nil {
			return fmt.Errorf("apply migration 8: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 8, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 8: %w", err)
		}
	}
	if applied < 9 {
		if _, err := tx.Exec(schemaMigrationNine); err != nil {
			return fmt.Errorf("apply migration 9: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 9, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 9: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func migrateReservedResourceIDs(tx *sql.Tx) error {
	for _, id := range []string{UnattributedProjectID, UnattributedSessionID} {
		if err := migrateReservedProjectID(tx, id); err != nil {
			return err
		}
	}
	for _, id := range []string{UnattributedProjectID, UnattributedSessionID} {
		if err := migrateReservedSessionID(tx, id); err != nil {
			return err
		}
	}
	return nil
}

func migrateReservedProjectID(tx *sql.Tx, oldID string) error {
	var exists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", oldID).Scan(&exists); err != nil || !exists {
		return err
	}
	newID, err := unusedResourceID(tx, "projects", "project", oldID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO projects(id, root, display_name, created_at)
		SELECT ?, root, display_name, created_at FROM projects WHERE id = ?`, newID, oldID); err != nil {
		return err
	}
	for _, statement := range []string{
		"UPDATE sessions SET project_id = ? WHERE project_id = ?",
		"UPDATE usage_events SET project_id = ? WHERE project_id = ?",
		"UPDATE project_locations SET project_id = ? WHERE project_id = ?",
		"UPDATE project_aliases SET project_id = ? WHERE project_id = ?",
		"UPDATE session_aliases SET project_id = ? WHERE project_id = ?",
	} {
		if _, err := tx.Exec(statement, newID, oldID); err != nil {
			return err
		}
	}
	if err := migrateWarningResourceID(tx, "project_id", oldID, newID); err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM projects WHERE id = ?", oldID)
	return err
}

func migrateReservedSessionID(tx *sql.Tx, oldID string) error {
	var exists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", oldID).Scan(&exists); err != nil || !exists {
		return err
	}
	newID, err := unusedResourceID(tx, "sessions", "session", oldID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO sessions(id, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata)
		SELECT ?, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata
		FROM sessions WHERE id = ?`, newID, oldID); err != nil {
		return err
	}
	for _, statement := range []string{
		"UPDATE usage_events SET session_id = ? WHERE session_id = ?",
		"UPDATE session_aliases SET session_id = ? WHERE session_id = ?",
	} {
		if _, err := tx.Exec(statement, newID, oldID); err != nil {
			return err
		}
	}
	if err := migrateWarningResourceID(tx, "session_id", oldID, newID); err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM sessions WHERE id = ?", oldID)
	return err
}

func unusedResourceID(tx *sql.Tx, table, kind, oldID string) (string, error) {
	base := stableLocalID(kind, "migration-8", oldID)
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM "+table+" WHERE id = ?)", candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
}

func migrateWarningResourceID(tx *sql.Tx, column, oldID, newID string) error {
	otherColumn := "session_id"
	if column == "session_id" {
		otherColumn = "project_id"
	}
	query := `INSERT INTO reconciliation_warnings(
		kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at, occurrence_count)
		SELECT kind, scope, alias_digest, `
	if column == "project_id" {
		query += `?, session_id, message, first_seen_at, last_seen_at, occurrence_count `
	} else {
		query += `project_id, ?, message, first_seen_at, last_seen_at, occurrence_count `
	}
	query += `FROM reconciliation_warnings WHERE ` + column + ` = ?
		ON CONFLICT(kind, scope, alias_digest, project_id, session_id) DO UPDATE SET
		first_seen_at = MIN(first_seen_at, excluded.first_seen_at),
		last_seen_at = MAX(last_seen_at, excluded.last_seen_at),
		occurrence_count = occurrence_count + excluded.occurrence_count`
	if _, err := tx.Exec(query, newID, oldID); err != nil {
		return fmt.Errorf("migrate warning %s while preserving %s: %w", column, otherColumn, err)
	}
	_, err := tx.Exec("DELETE FROM reconciliation_warnings WHERE "+column+" = ?", oldID)
	return err
}

func applyProjectIdentityMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(schemaMigrationSix); err != nil {
		return err
	}
	now := formatStoredTime(time.Now())
	rows, err := tx.Query("SELECT id, root FROM projects WHERE root <> ''")
	if err != nil {
		return err
	}
	type legacyLocation struct{ id, root string }
	var locations []legacyLocation
	for rows.Next() {
		var item legacyLocation
		if err := rows.Scan(&item.id, &item.root); err != nil {
			rows.Close()
			return err
		}
		locations = append(locations, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range locations {
		canonical, err := canonicalProjectLocation(item.root)
		if err != nil || canonical == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO project_locations
			(project_id, canonical_path, display_path, is_preferred, first_seen_at, last_seen_at, provenance)
			VALUES (?, ?, ?, 1, ?, ?, 'legacy-project-root')`, item.id, canonical, item.root, now, now); err != nil {
			return err
		}
	}
	type legacySessionIdentity struct {
		id, projectID, client, adapter string
	}
	sessionRows, err := tx.Query("SELECT id, COALESCE(project_id, ''), client_name, COALESCE(source_metadata, '') FROM sessions")
	if err != nil {
		return err
	}
	var legacySessions []legacySessionIdentity
	for sessionRows.Next() {
		var item legacySessionIdentity
		var metadata string
		if err := sessionRows.Scan(&item.id, &item.projectID, &item.client, &metadata); err != nil {
			sessionRows.Close()
			return err
		}
		if metadata != "" {
			var source struct {
				Adapter string `json:"adapter"`
			}
			if json.Unmarshal([]byte(metadata), &source) == nil {
				item.adapter = strings.TrimSpace(source.Adapter)
			}
		}
		legacySessions = append(legacySessions, item)
	}
	if err := sessionRows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO project_aliases(scope, alias, project_id, provenance, first_seen_at, last_seen_at)
		SELECT 'legacy', id, id, 'migration-6', ?, ? FROM projects`, now, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_aliases(scope, alias, session_id, project_id, provenance, first_seen_at, last_seen_at)
		SELECT 'legacy:' || client_name, id, id, project_id, 'migration-6', ?, ? FROM sessions`, now, now); err != nil {
		return err
	}
	for _, item := range legacySessions {
		if item.adapter == "" {
			continue
		}
		if item.projectID != "" {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO project_aliases(scope, alias, project_id, provenance, first_seen_at, last_seen_at)
				VALUES (?, ?, ?, 'migration-6 source metadata', ?, ?)`, item.adapter, item.projectID, item.projectID, now, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO session_aliases(scope, alias, session_id, project_id, provenance, first_seen_at, last_seen_at)
			VALUES (?, ?, ?, NULLIF(?, ''), 'migration-6 source metadata', ?, ?)`, identityScope(item.adapter, item.client), item.id, item.id, item.projectID, now, now); err != nil {
			return err
		}
	}
	duplicateRows, err := tx.Query(`SELECT canonical_path FROM project_locations GROUP BY canonical_path HAVING COUNT(DISTINCT project_id) > 1`)
	if err != nil {
		return err
	}
	var duplicates []string
	for duplicateRows.Next() {
		var canonical string
		if err := duplicateRows.Scan(&canonical); err != nil {
			duplicateRows.Close()
			return err
		}
		duplicates = append(duplicates, canonical)
	}
	if err := duplicateRows.Close(); err != nil {
		return err
	}
	for _, canonical := range duplicates {
		projectRows, err := tx.Query("SELECT project_id FROM project_locations WHERE canonical_path = ? ORDER BY project_id", canonical)
		if err != nil {
			return err
		}
		var projectIDs []string
		for projectRows.Next() {
			var projectID string
			if err := projectRows.Scan(&projectID); err != nil {
				projectRows.Close()
				return err
			}
			projectIDs = append(projectIDs, projectID)
		}
		if err := projectRows.Close(); err != nil {
			return err
		}
		for _, projectID := range projectIDs {
			if err := recordReconciliationWarning(tx, "legacy-location-conflict", "migration-6", canonical, projectID, "", "multiple legacy projects share one canonical location; identities were retained for review", now); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyPricingRationalMigration(tx *sql.Tx) error {
	columns := []struct{ name, definition string }{
		{"rate_numerator_micros", "INTEGER NOT NULL DEFAULT 0"},
		{"rate_denominator_tokens", "INTEGER NOT NULL DEFAULT 1"},
		{"catalog_entry_id", "TEXT"},
		{"source_url", "TEXT NOT NULL DEFAULT ''"},
		{"source_reviewed_at", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		var present int
		err := tx.QueryRow("SELECT 1 FROM pragma_table_info('pricing_rules') WHERE name = ?", column.name).Scan(&present)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec("ALTER TABLE pricing_rules ADD COLUMN " + column.name + " " + column.definition); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE pricing_rules SET rate_numerator_micros = rate_micros WHERE rate_numerator_micros = 0 AND rate_micros != 0 AND catalog_entry_id IS NULL"); err != nil {
		return err
	}
	_, err := tx.Exec(schemaMigrationFive)
	return err
}

func applySourceGenerationMigration(tx *sql.Tx) error {
	var present int
	err := tx.QueryRow("SELECT 1 FROM pragma_table_info('source_cursors') WHERE name = 'source_generation'").Scan(&present)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(schemaMigrationFour)
	return err
}

func normalizeStoredTimestamps(tx *sql.Tx) error {
	columns := []struct {
		table  string
		column string
	}{
		{table: "schema_migrations", column: "applied_at"},
		{table: "projects", column: "created_at"},
		{table: "sessions", column: "started_at"},
		{table: "sessions", column: "ended_at"},
		{table: "usage_events", column: "occurred_at"},
		{table: "usage_events", column: "started_at"},
		{table: "usage_events", column: "completed_at"},
		{table: "source_cursors", column: "updated_at"},
		{table: "pricing_rules", column: "effective_from"},
		{table: "pricing_rules", column: "effective_to"},
		{table: "diagnostics", column: "first_seen_at"},
		{table: "diagnostics", column: "last_seen_at"},
	}
	for _, item := range columns {
		rows, err := tx.Query("SELECT rowid, " + item.column + " FROM " + item.table)
		if err != nil {
			return err
		}
		updates := make([]struct {
			rowID int64
			value string
		}, 0)
		for rows.Next() {
			var rowID int64
			var value sql.NullString
			if err := rows.Scan(&rowID, &value); err != nil {
				rows.Close()
				return err
			}
			if !value.Valid || value.String == "" {
				continue
			}
			parsed, err := time.Parse(time.RFC3339Nano, value.String)
			if err != nil {
				rows.Close()
				return fmt.Errorf("%s.%s value %q: %w", item.table, item.column, value.String, err)
			}
			updates = append(updates, struct {
				rowID int64
				value string
			}{rowID: rowID, value: formatStoredTime(parsed)})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, update := range updates {
			if _, err := tx.Exec("UPDATE "+item.table+" SET "+item.column+" = ? WHERE rowid = ?", update.value, update.rowID); err != nil {
				return err
			}
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
	if isReservedProjectID(project.ID) {
		return errors.New("project ID is reserved for synthetic navigation")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO projects(id, root, display_name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET root = excluded.root, display_name = excluded.display_name`,
		project.ID, project.Root, project.DisplayName, formatStoredTime(time.Now())); err != nil {
		tx.Rollback()
		return err
	}
	if err := persistExplicitProjectLocation(tx, project, formatStoredTime(time.Now())); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// UpsertSession creates or updates a session identity.
func (s *Store) UpsertSession(session Session) error {
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.Client.Name) == "" {
		return errors.New("session ID and client name are required")
	}
	if isReservedSessionID(session.ID) || isReservedProjectID(session.ProjectID) {
		return errors.New("session ID or project ID is reserved for synthetic navigation")
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
	return s.append(events, nil, nil, nil)
}

// AppendWithReferences persists reference metadata and events atomically.
func (s *Store) AppendWithReferences(events []UsageEvent, projects []Project, sessions []Session) (Receipt, error) {
	return s.append(events, nil, projects, sessions)
}

// AppendWithReferencesAndCursor persists reference metadata, events, and a
// source cursor atomically. A failure in any part rolls back the complete
// ingestion unit.
func (s *Store) AppendWithReferencesAndCursor(events []UsageEvent, projects []Project, sessions []Session, cursor Cursor) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	return s.append(events, &cursor, projects, sessions)
}

// AppendWithCursor persists events and advances the source cursor atomically.
// A failed event insert cannot leave the cursor advanced.
func (s *Store) AppendWithCursor(events []UsageEvent, cursor Cursor) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	return s.append(events, &cursor, nil, nil)
}

func (s *Store) append(events []UsageEvent, cursor *Cursor, projects []Project, sessions []Session) (Receipt, error) {
	events = append([]UsageEvent(nil), events...)
	projects = append([]Project(nil), projects...)
	sessions = append([]Session(nil), sessions...)
	for _, event := range events {
		if err := event.Validate(); err != nil {
			return Receipt{}, fmt.Errorf("validate event %q: %w", event.EventID, err)
		}
	}
	for _, project := range projects {
		if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.DisplayName) == "" {
			return Receipt{}, errors.New("project ID and display name are required")
		}
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.Client.Name) == "" {
			return Receipt{}, errors.New("session ID and client name are required")
		}
		if session.SourceMetadata != nil && !json.Valid(session.SourceMetadata) {
			return Receipt{}, errors.New("session source metadata must contain valid JSON")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Receipt{}, fmt.Errorf("begin append: %w", err)
	}
	events, projects, sessions, err = reconcileImportedReferences(tx, events, projects, sessions)
	if err != nil {
		tx.Rollback()
		return Receipt{}, fmt.Errorf("reconcile imported references: %w", err)
	}
	for _, event := range events {
		if isReservedProjectID(event.ProjectID) || isReservedSessionID(event.SessionID) {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("event %q uses a resource ID reserved for synthetic navigation", event.EventID)
		}
	}
	for _, project := range projects {
		if isReservedProjectID(project.ID) {
			tx.Rollback()
			return Receipt{}, errors.New("project ID is reserved for synthetic navigation")
		}
	}
	for _, session := range sessions {
		if isReservedSessionID(session.ID) || isReservedProjectID(session.ProjectID) {
			tx.Rollback()
			return Receipt{}, errors.New("session ID or project ID is reserved for synthetic navigation")
		}
	}
	receipt := Receipt{}
	for _, project := range projects {
		if _, err := tx.Exec(`INSERT INTO projects(id, root, display_name, created_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			root = CASE WHEN excluded.root = '' THEN projects.root ELSE excluded.root END,
			display_name = CASE WHEN excluded.display_name = '' THEN projects.display_name ELSE excluded.display_name END`,
			project.ID, project.Root, project.DisplayName, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("persist project %q: %w", project.ID, err)
		}
		if project.SourceAdapter == "" {
			if err := persistExplicitProjectLocation(tx, project, formatStoredTime(time.Now())); err != nil {
				tx.Rollback()
				return Receipt{}, fmt.Errorf("persist project location %q: %w", project.ID, err)
			}
		}
	}
	for _, session := range sessions {
		if _, err := tx.Exec(`INSERT INTO sessions(
			id, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata)
			VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET project_id = excluded.project_id,
			client_name = excluded.client_name, client_version = excluded.client_version,
			client_mode = excluded.client_mode, started_at = excluded.started_at,
			ended_at = excluded.ended_at, source_metadata = excluded.source_metadata`,
			session.ID, session.ProjectID, session.Client.Name, session.Client.Version,
			session.Client.Mode, nullableTime(session.StartedAt), nullableTime(session.EndedAt),
			nullableJSON(session.SourceMetadata)); err != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("persist session %q: %w", session.ID, err)
		}
	}
	for _, event := range events {
		result, err := tx.Exec(eventInsertSQL,
			event.EventID, event.SchemaVersion, event.EventType, formatStoredTime(event.OccurredAt),
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
		if _, err := tx.Exec(`INSERT INTO source_cursors(source_id, file_fingerprint, byte_offset, last_provider_record_id, source_generation, pending_tail, snapshot_reusable, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_id) DO UPDATE SET file_fingerprint = excluded.file_fingerprint,
			byte_offset = excluded.byte_offset, last_provider_record_id = excluded.last_provider_record_id,
			source_generation = excluded.source_generation, pending_tail = excluded.pending_tail,
			snapshot_reusable = excluded.snapshot_reusable, updated_at = excluded.updated_at`, cursor.SourceID, cursor.FileFingerprint, cursor.ByteOffset,
			cursor.LastProviderRecordID, cursor.SourceGeneration, cursor.PendingTail, cursor.SnapshotReusable, formatStoredTime(time.Now())); err != nil {
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
	err := s.db.QueryRow(`SELECT source_id, file_fingerprint, byte_offset, last_provider_record_id, source_generation, pending_tail, snapshot_reusable
		FROM source_cursors WHERE source_id = ?`, sourceID).Scan(&cursor.SourceID, &cursor.FileFingerprint,
		&cursor.ByteOffset, &cursor.LastProviderRecordID, &cursor.SourceGeneration, &cursor.PendingTail, &cursor.SnapshotReusable)
	if err != nil {
		return Cursor{}, err
	}
	return cursor, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatStoredTime(*value)
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
