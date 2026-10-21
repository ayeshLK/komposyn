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
	"context"
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
	db                  *sql.DB
	databasePath        string
	backupPath          string
	migrationBackupPath string
	beforeSnapshotRead  func()
	beforeSnapshotParse func()
	sourceReadHook      func(pass int, bytesRead int64)
	beforeAppend        func()
	benchmarkObserver   *collectorBenchmarkObserver
}

var errSourceCursorChanged = errors.New("source cursor changed while snapshot was being parsed")

const storedTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatStoredTime(value time.Time) string {
	return value.UTC().Format(storedTimeLayout)
}

// Open opens or creates a SQLite database and applies all migrations.
func Open(path string) (*Store, error) {
	return OpenWithOptions(path, OpenOptions{})
}

// OpenWithOptions opens or creates a store and applies all migrations. If the
// existing database needs migration, it first verifies a private online backup.
func OpenWithOptions(path string, options OpenOptions) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		resolved, err := resolveDatabasePath(path)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		path = resolved
	}
	version, exists, err := inspectSchemaSnapshot(path)
	if err != nil {
		return nil, err
	}
	if version > currentSchemaVersion {
		return nil, &UnsupportedSchemaError{DatabaseVersion: version, CurrentVersion: currentSchemaVersion}
	}
	if path != ":memory:" {
		if !exists {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
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
	store := &Store{db: db, databasePath: path, backupPath: options.BackupPath}
	if err := store.configure(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
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

// migrate applies numbered schema migrations in order after Open has completed
// the compatibility preflight on a private snapshot. The live schema version
// is checked again under the migration transaction's write lock.
func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback()
	var hasMigrations bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations')").Scan(&hasMigrations); err != nil {
		return fmt.Errorf("inspect migration metadata: %w", err)
	}
	var applied int
	if hasMigrations {
		if err := tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&applied); err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
	}
	if applied > currentSchemaVersion {
		return &UnsupportedSchemaError{DatabaseVersion: applied, CurrentVersion: currentSchemaVersion}
	}
	if applied > 0 && applied < currentSchemaVersion && s.databasePath != ":memory:" {
		backupPath, err := createMigrationBackup(s.databasePath, s.backupPath, applied, currentSchemaVersion)
		if err != nil {
			return fmt.Errorf("create verified pre-migration backup: %w", err)
		}
		s.migrationBackupPath = backupPath
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	if applied > currentSchemaVersion {
		return &UnsupportedSchemaError{DatabaseVersion: applied, CurrentVersion: currentSchemaVersion}
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
	if applied < 10 {
		if _, err := tx.Exec(schemaMigrationTen); err != nil {
			return fmt.Errorf("apply migration 10: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 10, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 10: %w", err)
		}
	}
	if applied < 11 {
		if _, err := tx.Exec(schemaMigrationEleven); err != nil {
			return fmt.Errorf("apply migration 11: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 11, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 11: %w", err)
		}
	}
	if applied < 12 {
		if err := applyBillingContextMigration(tx); err != nil {
			return fmt.Errorf("apply migration 12: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 12, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 12: %w", err)
		}
	}
	if applied < 13 {
		if err := applyMissingColumns(tx, []struct{ table, name, statement string }{
			{"usage_events", "cache_write_5m_tokens", schemaMigrationThirteenCacheWrite5m},
			{"usage_events", "cache_write_1h_tokens", schemaMigrationThirteenCacheWrite1h},
		}); err != nil {
			return fmt.Errorf("apply migration 13: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 13, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 13: %w", err)
		}
	}
	if applied < 14 {
		if err := applyMissingColumns(tx, []struct{ table, name, statement string }{
			{"pricing_rules", "speed_selector", schemaMigrationFourteenSpeedSelector},
		}); err != nil {
			return fmt.Errorf("apply migration 14: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 14, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 14: %w", err)
		}
	}
	if applied < 15 {
		if err := applyMissingColumns(tx, []struct{ table, name, statement string }{
			{"diagnostics", "category", "ALTER TABLE diagnostics ADD COLUMN category TEXT NOT NULL DEFAULT 'unknown'"},
			{"diagnostics", "core_version", "ALTER TABLE diagnostics ADD COLUMN core_version TEXT NOT NULL DEFAULT ''"},
			{"diagnostics", "cli_version", "ALTER TABLE diagnostics ADD COLUMN cli_version TEXT NOT NULL DEFAULT ''"},
			{"diagnostics", "protocol_version", "ALTER TABLE diagnostics ADD COLUMN protocol_version INTEGER NOT NULL DEFAULT 0"},
			{"diagnostics", "schema_version", "ALTER TABLE diagnostics ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 0"},
		}); err != nil {
			return fmt.Errorf("apply migration 15: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 15, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 15: %w", err)
		}
	}
	if applied < 16 {
		if _, err := tx.Exec(schemaMigrationSixteenParserCheckpoints); err != nil {
			return fmt.Errorf("apply migration 16: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 16, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 16: %w", err)
		}
	}
	if applied < 17 {
		if _, err := tx.Exec(schemaMigrationSeventeenProjectNameSource); err != nil {
			return fmt.Errorf("apply migration 17: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 17, formatStoredTime(time.Now())); err != nil {
			return fmt.Errorf("record migration 17: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func applyMissingColumns(tx *sql.Tx, columns []struct{ table, name, statement string }) error {
	for _, column := range columns {
		var present bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = ?)", column.table, column.name).Scan(&present); err != nil {
			return err
		}
		if !present {
			if _, err := tx.Exec(column.statement); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyBillingContextMigration(tx *sql.Tx) error {
	columns := []struct{ table, name, definition string }{
		{"usage_events", "billing_context_json", "TEXT"},
		{"pricing_rules", "service_tier_selector", "TEXT NOT NULL DEFAULT ''"},
		{"pricing_rules", "region_selector", "TEXT NOT NULL DEFAULT ''"},
	}
	present := make([]bool, len(columns))
	missing := 0
	for index, column := range columns {
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = ?)", column.table, column.name).Scan(&present[index]); err != nil {
			return err
		}
		if !present[index] {
			missing++
		}
	}
	if missing == len(columns) {
		_, err := tx.Exec(schemaMigrationTwelve)
		return err
	}
	for index, column := range columns {
		if !present[index] {
			if _, err := tx.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
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
	hasNameSource, err := projectNameSourceColumnExists(tx)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !hasNameSource {
		if _, err = tx.Exec(`INSERT INTO projects(id, root, display_name, created_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET root = excluded.root, display_name = excluded.display_name`,
			project.ID, project.Root, project.DisplayName, formatStoredTime(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
	} else if _, err = tx.Exec(`INSERT INTO projects(id, root, display_name, created_at, display_name_source)
		VALUES (?, ?, ?, ?, 'explicit')
		ON CONFLICT(id) DO UPDATE SET root = excluded.root, display_name = excluded.display_name,
		display_name_source = 'explicit'`,
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
	return s.append(events, nil, nil, nil, nil)
}

// AppendWithReferences persists reference metadata and events atomically.
func (s *Store) AppendWithReferences(events []UsageEvent, projects []Project, sessions []Session) (Receipt, error) {
	return s.append(events, nil, projects, sessions, nil)
}

// AppendWithReferencesAndCursor persists reference metadata, events, and a
// source cursor atomically. A failure in any part rolls back the complete
// ingestion unit.
func (s *Store) AppendWithReferencesAndCursor(events []UsageEvent, projects []Project, sessions []Session, cursor Cursor) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	return s.append(events, &cursor, projects, sessions, nil)
}

func (s *Store) appendWithReferencesAndCursorIfCurrent(events []UsageEvent, projects []Project, sessions []Session, cursor Cursor, expected sourceCursorExpectation) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	if cursor.SourceID != expected.sourceID {
		return Receipt{}, errors.New("cursor expectation source ID does not match committed cursor")
	}
	return s.append(events, &cursor, projects, sessions, &expected)
}

func (s *Store) appendWithReferencesAndCursorIfCurrentWithProjectNameSources(events []UsageEvent, projects []Project, sessions []Session,
	cursor Cursor, expected sourceCursorExpectation, projectNameSources map[string]projectNameSource) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	if cursor.SourceID != expected.sourceID {
		return Receipt{}, errors.New("cursor expectation source ID does not match committed cursor")
	}
	return s.appendInternalWithProjectNameSources(events, &cursor, projects, sessions, &expected, nil, projectNameSources)
}

func (s *Store) appendWithParserStateIfCurrent(events []UsageEvent, projects []Project, sessions []Session, cursor Cursor,
	expected sourceCursorExpectation, checkpointExpected sourceCheckpointExpectation, checkpoint *parserCheckpoint,
	identity collectorIdentityExpectation, ctx context.Context) (Receipt, error) {
	return s.appendWithParserStateAndProjectNameSourcesIfCurrent(events, projects, sessions, cursor, expected,
		checkpointExpected, checkpoint, identity, ctx, nil)
}

func (s *Store) appendWithParserStateAndProjectNameSourcesIfCurrent(events []UsageEvent, projects []Project, sessions []Session, cursor Cursor,
	expected sourceCursorExpectation, checkpointExpected sourceCheckpointExpectation, checkpoint *parserCheckpoint,
	identity collectorIdentityExpectation, ctx context.Context, projectNameSources map[string]projectNameSource) (Receipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	if cursor.SourceID != expected.sourceID {
		return Receipt{}, errors.New("cursor expectation source ID does not match committed cursor")
	}
	return s.appendInternalWithProjectNameSources(events, &cursor, projects, sessions, &expected, &parserStateCommit{
		checkpointExpected: checkpointExpected, checkpoint: checkpoint, identity: identity, ctx: ctx,
	}, projectNameSources)
}

// AppendWithCursor persists events and advances the source cursor atomically.
// A failed event insert cannot leave the cursor advanced.
func (s *Store) AppendWithCursor(events []UsageEvent, cursor Cursor) (Receipt, error) {
	if err := cursor.validate(); err != nil {
		return Receipt{}, err
	}
	return s.append(events, &cursor, nil, nil, nil)
}

func (s *Store) append(events []UsageEvent, cursor *Cursor, projects []Project, sessions []Session, expected *sourceCursorExpectation) (Receipt, error) {
	return s.appendInternal(events, cursor, projects, sessions, expected, nil)
}

type parserStateCommit struct {
	checkpointExpected sourceCheckpointExpectation
	checkpoint         *parserCheckpoint
	identity           collectorIdentityExpectation
	ctx                context.Context
}

func (s *Store) appendInternal(events []UsageEvent, cursor *Cursor, projects []Project, sessions []Session, expected *sourceCursorExpectation, parserState *parserStateCommit) (Receipt, error) {
	return s.appendInternalWithProjectNameSources(events, cursor, projects, sessions, expected, parserState, nil)
}

func (s *Store) appendInternalWithProjectNameSources(events []UsageEvent, cursor *Cursor, projects []Project, sessions []Session,
	expected *sourceCursorExpectation, parserState *parserStateCommit, projectNameSources map[string]projectNameSource) (Receipt, error) {
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
	if s.beforeAppend != nil {
		s.beforeAppend()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Receipt{}, fmt.Errorf("begin append: %w", err)
	}
	if parserState != nil {
		if err := parserState.ctx.Err(); err != nil {
			tx.Rollback()
			return Receipt{}, err
		}
	}
	var hasBillingContext bool
	var hasNameSource bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('projects') WHERE name = 'display_name_source')").Scan(&hasNameSource); err != nil {
		tx.Rollback()
		return Receipt{}, fmt.Errorf("inspect project name source schema: %w", err)
	}
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('usage_events') WHERE name = 'billing_context_json')").Scan(&hasBillingContext); err != nil {
		tx.Rollback()
		return Receipt{}, fmt.Errorf("inspect usage event schema: %w", err)
	}
	insertSQL := eventInsertSQL
	if !hasBillingContext {
		insertSQL = legacyEventInsertSQL
	}
	if expected != nil {
		current, currentErr := cursorInTransaction(tx, expected.sourceID)
		if errors.Is(currentErr, sql.ErrNoRows) {
			if expected.exists {
				tx.Rollback()
				return Receipt{}, errSourceCursorChanged
			}
		} else if currentErr != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("revalidate source cursor: %w", currentErr)
		} else if !expected.exists || current != expected.cursor {
			tx.Rollback()
			return Receipt{}, errSourceCursorChanged
		}
	}
	if parserState != nil {
		if expected == nil {
			tx.Rollback()
			return Receipt{}, errors.New("parser checkpoint requires a cursor expectation")
		}
		actual, actualErr := parserCheckpointExpectationInTransaction(tx, expected.sourceID)
		if errors.Is(actualErr, sql.ErrNoRows) {
			actualErr = nil
		} else if actualErr == nil && !checkpointEqual(actual, parserState.checkpointExpected) {
			actualErr = errParserCheckpointChanged
		}
		if actualErr != nil || !checkpointEqual(actual, parserState.checkpointExpected) {
			tx.Rollback()
			if actualErr != nil && !errors.Is(actualErr, errParserCheckpointChanged) {
				return Receipt{}, fmt.Errorf("revalidate parser checkpoint: %w", actualErr)
			}
			return Receipt{}, errSourceCursorChanged
		}
		if err := claimCollectorIdentityInTransaction(tx, parserState.identity, cursor.SourceID); err != nil {
			tx.Rollback()
			if errors.Is(err, errCollectorIdentityChanged) {
				return Receipt{}, errSourceCursorChanged
			}
			return Receipt{}, fmt.Errorf("claim collector source identity: %w", err)
		}
	}
	events, projects, sessions, err = reconcileImportedReferencesWithProjectNameSources(tx, events, projects, sessions, projectNameSources)
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
		nameSource := projectNameSourceUnknown
		updateName := strings.TrimSpace(project.SourceAdapter) == "" && strings.TrimSpace(project.DisplayName) != ""
		if updateName {
			nameSource = projectNameSourceExplicit
		}
		if !hasNameSource {
			if _, err := tx.Exec(`INSERT INTO projects(id, root, display_name, created_at)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
				root = CASE WHEN excluded.root = '' THEN projects.root ELSE excluded.root END,
				display_name = CASE WHEN ? = 1 AND excluded.display_name <> '' THEN excluded.display_name ELSE projects.display_name END`,
				project.ID, project.Root, project.DisplayName, time.Now().UTC().Format(time.RFC3339Nano), updateName); err != nil {
				tx.Rollback()
				return Receipt{}, fmt.Errorf("persist project %q: %w", project.ID, err)
			}
		} else if _, err := tx.Exec(`INSERT INTO projects(id, root, display_name, created_at, display_name_source)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			root = CASE WHEN excluded.root = '' THEN projects.root ELSE excluded.root END,
			display_name = CASE WHEN ? = 1 AND excluded.display_name <> '' THEN excluded.display_name ELSE projects.display_name END,
			display_name_source = CASE WHEN ? = 1 THEN excluded.display_name_source ELSE projects.display_name_source END`,
			project.ID, project.Root, project.DisplayName, time.Now().UTC().Format(time.RFC3339Nano), nameSource, updateName, updateName); err != nil {
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
		billingContextJSON, err := json.Marshal(event.BillingContext)
		if err != nil {
			tx.Rollback()
			return Receipt{}, fmt.Errorf("encode event %q billing context: %w", event.EventID, err)
		}
		args := []any{
			event.EventID, event.SchemaVersion, event.EventType, formatStoredTime(event.OccurredAt),
			nullableTime(event.StartedAt), nullableTime(event.CompletedAt), event.ProjectID, event.SessionID,
			event.RequestID, event.Attempt, event.Operation, event.Client.Name, event.Client.Version,
			event.Client.Mode, event.Provider, event.RequestedModel, event.ResolvedModel,
			event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.CacheReadTokens,
			event.Usage.CacheWriteTokens, event.Usage.ReasoningTokens, event.Cost.Status,
			nullableInt64(event.Cost.AmountMicros), event.Cost.Currency, event.Cost.PricingVersion,
			nullableInt64(event.Cost.ProviderAmount), event.Cost.ProviderCurrency, event.Provenance.Adapter,
			event.Provenance.SourceID, event.Provenance.SourceRecord, nullableInt64(event.Provenance.SourceOffset),
			event.Provenance.RecordHash, event.IngestionKey, nullableJSON(event.Attributes)}
		if hasBillingContext {
			args = append(args, nullableJSON(billingContextJSON))
			args = append(args, event.Usage.CacheWrite5mTokens, event.Usage.CacheWrite1hTokens)
		}
		if s.benchmarkObserver != nil {
			s.benchmarkObserver.eventInsertAttempts++
			if _, historical := s.benchmarkObserver.historicalEventIDs[event.EventID]; historical {
				s.benchmarkObserver.historicalInsertAttempts++
			}
		}
		result, err := tx.Exec(insertSQL, args...)
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
	if parserState != nil {
		if parserState.checkpoint == nil {
			if _, err := tx.Exec(`DELETE FROM parser_checkpoints WHERE source_id = ?`, cursor.SourceID); err != nil {
				tx.Rollback()
				return Receipt{}, fmt.Errorf("invalidate parser checkpoint: %w", err)
			}
		} else {
			checkpoint := parserState.checkpoint
			if checkpoint.SourceID != cursor.SourceID {
				tx.Rollback()
				return Receipt{}, errors.New("parser checkpoint source ID does not match cursor")
			}
			if _, err := tx.Exec(`INSERT INTO parser_checkpoints(source_id, adapter, parser_revision, format_version,
				source_generation, boundary_offset, prefix_fingerprint, state_json, state_digest, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(source_id) DO UPDATE SET adapter = excluded.adapter,
				parser_revision = excluded.parser_revision, format_version = excluded.format_version,
				source_generation = excluded.source_generation, boundary_offset = excluded.boundary_offset,
				prefix_fingerprint = excluded.prefix_fingerprint, state_json = excluded.state_json,
				state_digest = excluded.state_digest, updated_at = excluded.updated_at`,
				checkpoint.SourceID, checkpoint.Adapter, checkpoint.ParserRevision, checkpoint.FormatVersion,
				checkpoint.SourceGeneration, checkpoint.BoundaryOffset, checkpoint.PrefixFingerprint,
				checkpoint.StateJSON, checkpoint.StateDigest, formatStoredTime(time.Now())); err != nil {
				tx.Rollback()
				return Receipt{}, fmt.Errorf("persist parser checkpoint: %w", err)
			}
		}
	}
	if parserState != nil {
		if err := parserState.ctx.Err(); err != nil {
			tx.Rollback()
			return Receipt{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Receipt{}, fmt.Errorf("commit append: %w", err)
	}
	return receipt, nil
}

var errCollectorIdentityChanged = errors.New("collector source identity changed while snapshot was being imported")

func claimCollectorIdentityInTransaction(tx *sql.Tx, expected collectorIdentityExpectation, sourceID string) error {
	if expected.CanonicalKey == "" || expected.Adapter == "" || sourceID == "" {
		return errors.New("collector source identity claim is incomplete")
	}
	var actual string
	err := tx.QueryRow(`SELECT source_id FROM collector_source_identities WHERE canonical_key = ? AND adapter = ?`, expected.CanonicalKey, expected.Adapter).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		if expected.Exists {
			return errCollectorIdentityChanged
		}
		var owner string
		ownerErr := tx.QueryRow(`SELECT canonical_key FROM collector_source_identities WHERE adapter = ? AND source_id = ?`, expected.Adapter, sourceID).Scan(&owner)
		if ownerErr == nil || !errors.Is(ownerErr, sql.ErrNoRows) {
			if ownerErr != nil {
				return ownerErr
			}
			return errCollectorIdentityChanged
		}
		_, err = tx.Exec(`INSERT INTO collector_source_identities(canonical_key, adapter, source_id, created_at)
			VALUES (?, ?, ?, ?)`, expected.CanonicalKey, expected.Adapter, sourceID, formatStoredTime(time.Now()))
		return err
	}
	if err != nil {
		return err
	}
	if !expected.Exists || expected.SourceID != actual || actual != sourceID {
		return errCollectorIdentityChanged
	}
	return nil
}

func parserCheckpointExpectationInTransaction(tx *sql.Tx, sourceID string) (sourceCheckpointExpectation, error) {
	row, err := scanParserCheckpoint(tx.QueryRow(`SELECT source_id, adapter, parser_revision, format_version,
		source_generation, boundary_offset, prefix_fingerprint, state_json, state_digest
		FROM parser_checkpoints WHERE source_id = ?`, sourceID))
	if err != nil {
		return sourceCheckpointExpectation{}, err
	}
	return sourceCheckpointExpectation{exists: true, token: checkpointRowToken(row), row: row}, nil
}

func (s *Store) parserCheckpoint(sourceID string) (parserCheckpoint, error) {
	return scanParserCheckpoint(s.db.QueryRow(`SELECT source_id, adapter, parser_revision, format_version,
		source_generation, boundary_offset, prefix_fingerprint, state_json, state_digest
		FROM parser_checkpoints WHERE source_id = ?`, sourceID))
}

type checkpointRowScanner interface{ Scan(...any) error }

func scanParserCheckpoint(scanner checkpointRowScanner) (parserCheckpoint, error) {
	var row parserCheckpoint
	err := scanner.Scan(&row.SourceID, &row.Adapter, &row.ParserRevision, &row.FormatVersion,
		&row.SourceGeneration, &row.BoundaryOffset, &row.PrefixFingerprint, &row.StateJSON, &row.StateDigest)
	if err != nil {
		return parserCheckpoint{}, err
	}
	row.StateJSON = append([]byte(nil), row.StateJSON...)
	return row, nil
}

func cursorInTransaction(tx *sql.Tx, sourceID string) (Cursor, error) {
	var cursor Cursor
	err := tx.QueryRow(`SELECT source_id, file_fingerprint, byte_offset, last_provider_record_id, source_generation, pending_tail, snapshot_reusable
		FROM source_cursors WHERE source_id = ?`, sourceID).Scan(&cursor.SourceID, &cursor.FileFingerprint,
		&cursor.ByteOffset, &cursor.LastProviderRecordID, &cursor.SourceGeneration, &cursor.PendingTail, &cursor.SnapshotReusable)
	return cursor, err
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

func (s *Store) collectorSourceIdentity(canonicalKey, adapter string) (string, error) {
	var sourceID string
	err := s.db.QueryRow(`SELECT source_id FROM collector_source_identities
		WHERE canonical_key = ? AND adapter = ?`, canonicalKey, adapter).Scan(&sourceID)
	return sourceID, err
}

func (s *Store) rememberCollectorSourceIdentity(canonicalKey, adapter, sourceID string) (string, bool, error) {
	if _, err := s.db.Exec(`INSERT INTO collector_source_identities(canonical_key, adapter, source_id, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		canonicalKey, adapter, sourceID, formatStoredTime(time.Now())); err != nil {
		return "", false, err
	}
	storedSourceID, err := s.collectorSourceIdentity(canonicalKey, adapter)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return storedSourceID, true, nil
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
