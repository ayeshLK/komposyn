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
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

const (
	// CurrentSchemaVersion is the SQLite schema written by this core version.
	CurrentSchemaVersion = 15
	// OldestMigratableSchemaVersion is the oldest numbered schema supported by
	// the forward migration history.
	OldestMigratableSchemaVersion = 1
)

var (
	ErrUnsupportedSchema = errors.New("unsupported SQLite schema version")
	ErrInvalidSchema     = errors.New("invalid SQLite schema state")
)

// UnsupportedSchemaError reports that a database was created by a newer core.
type UnsupportedSchemaError struct {
	DatabaseVersion int
	CurrentVersion  int
}

func (err *UnsupportedSchemaError) Error() string {
	return fmt.Sprintf("database schema version %d is newer than supported version %d", err.DatabaseVersion, err.CurrentVersion)
}

func (err *UnsupportedSchemaError) Is(target error) bool { return target == ErrUnsupportedSchema }

// SchemaVersion returns the current numbered SQLite schema version.
func SchemaVersion() int { return CurrentSchemaVersion }

// SupportedEventSchemaVersions returns the stable event-format versions
// advertised by protocol initialization. It is not a SQLite schema range.
func SupportedEventSchemaVersions() []int { return []int{1, 2, 3, 4} }

func inspectSchemaReadOnly(path string) (version int, exists bool, err error) {
	if path == ":memory:" {
		return 0, false, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, false, fmt.Errorf("resolve database path: %w", err)
	}
	if _, err := os.Stat(absolute); errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, fmt.Errorf("inspect database: %w", err)
	}
	// SQLite can update lock/read-mark bytes in an existing -shm file during a
	// read-only WAL open. Inspect a private temporary snapshot so even those
	// bookkeeping writes cannot touch the application's database or sidecars.
	inspectionDir, err := os.MkdirTemp("", "komposyn-schema-inspect-")
	if err != nil {
		return 0, true, fmt.Errorf("create private schema inspection snapshot: %w", err)
	}
	defer os.RemoveAll(inspectionDir)
	inspectionPath := filepath.Join(inspectionDir, "database.sqlite")
	for _, suffix := range []string{"", "-wal", "-journal"} {
		if err := copyRegularFile(absolute+suffix, inspectionPath+suffix); err != nil {
			if errors.Is(err, os.ErrNotExist) && suffix != "" {
				continue
			}
			return 0, true, fmt.Errorf("copy database for read-only inspection: %w", err)
		}
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(inspectionPath), RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return 0, true, fmt.Errorf("open database read-only: %w", err)
	}
	defer db.Close()
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&tableCount); err != nil {
		return 0, true, fmt.Errorf("inspect database schema read-only: %w", err)
	}
	var hasMigrations bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations')").Scan(&hasMigrations); err != nil {
		return 0, true, fmt.Errorf("inspect migration metadata read-only: %w", err)
	}
	if !hasMigrations {
		if tableCount != 0 {
			return 0, true, fmt.Errorf("%w: non-empty database has no migration history", ErrInvalidSchema)
		}
		return 0, true, nil
	}
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, true, fmt.Errorf("read database schema version: %w", err)
	}
	if version < 0 {
		return 0, true, fmt.Errorf("%w: negative migration version %d", ErrInvalidSchema, version)
	}
	if version == 0 && tableCount > 1 {
		return 0, true, fmt.Errorf("%w: non-empty database has no applied migration history", ErrInvalidSchema)
	}
	return version, true, nil
}

func copyRegularFile(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", filepath.Base(sourcePath))
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		return err
	}
	return destination.Close()
}
