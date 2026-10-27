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
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// CurrentSchemaVersion is the SQLite schema written by this core version.
	CurrentSchemaVersion = 18
	// OldestMigratableSchemaVersion is the oldest numbered schema supported by
	// the forward migration history.
	OldestMigratableSchemaVersion = 1
)

var (
	ErrUnsupportedSchema      = errors.New("unsupported SQLite schema version")
	ErrInvalidSchema          = errors.New("invalid SQLite schema state")
	errSchemaSnapshotChanged  = errors.New("schema snapshot source changed during copy")
	errSchemaSnapshotUnstable = errors.New("schema snapshot remained unstable within the inspection retry budget")
)

type schemaSnapshotUnstableError struct{ cause error }

func (err *schemaSnapshotUnstableError) Error() string { return err.cause.Error() }
func (err *schemaSnapshotUnstableError) Unwrap() []error {
	return []error{errSchemaSnapshotUnstable, err.cause}
}

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

func inspectSchemaSnapshot(path string) (version int, exists bool, err error) {
	if path == ":memory:" {
		return 0, false, nil
	}
	absolute, err := resolveDatabasePath(path)
	if err != nil {
		return 0, false, fmt.Errorf("resolve database path: %w", err)
	}
	if _, err := os.Stat(absolute); errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, fmt.Errorf("inspect database: %w", err)
	}
	return inspectSchemaSnapshotWithCopier(absolute, copyRegularFile, 5*time.Second)
}

func inspectSchemaSnapshotWithCopier(path string, copyFile func(string, string) error, retryBudget time.Duration) (version int, exists bool, err error) {
	// Copying multiple files is not atomic. Discard changing snapshots and
	// allow collector bursts to settle within the same five-second wait budget
	// used by the live SQLite connection. Backoff avoids repeated full copies
	// in rapid succession; never open the source through SQLite as a fallback,
	// because even a read-only WAL connection can modify its shared-memory file.
	deadline := time.Now().Add(retryBudget)
	delay := 10 * time.Millisecond
	for {
		version, err = inspectSchemaSnapshotAttempt(path, copyFile)
		if err == nil {
			return version, true, nil
		}
		if errors.Is(err, ErrInvalidSchema) || errors.Is(err, ErrUnsupportedSchema) {
			return 0, true, err
		}
		remaining := time.Until(deadline)
		if remaining <= delay {
			if errors.Is(err, errSchemaSnapshotChanged) {
				return 0, true, fmt.Errorf("inspect private schema snapshot within %s retry budget: %w", retryBudget,
					&schemaSnapshotUnstableError{cause: err})
			}
			return 0, true, fmt.Errorf("inspect private schema snapshot within %s retry budget: %w", retryBudget, err)
		}
		time.Sleep(delay)
		delay = min(delay*2, 250*time.Millisecond)
	}
}

var schemaSnapshotSuffixes = []string{"", "-wal", "-journal"}

// schemaSnapshotDigests checks complete contents, including sidecar presence,
// rather than relying on timestamps or sizes that can miss concurrent writes.
func schemaSnapshotDigests(path string) (map[string][sha256.Size]byte, error) {
	digests := make(map[string][sha256.Size]byte)
	for _, suffix := range schemaSnapshotSuffixes {
		file, err := os.Open(path + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			return nil, fmt.Errorf("inspect snapshot source %s: not a regular file or stat failed: %v", filepath.Base(path+suffix), err)
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		digests[suffix] = [sha256.Size]byte(hash.Sum(nil))
	}
	return digests, nil
}

func inspectSchemaSnapshotAttempt(path string, copyFile func(string, string) error) (int, error) {
	before, err := schemaSnapshotDigests(path)
	if err != nil {
		return 0, fmt.Errorf("fingerprint schema snapshot source: %w", err)
	}
	// SQLite can update lock/read-mark bytes in an existing -shm file during a
	// read-only WAL open. Inspect a private temporary snapshot so even those
	// bookkeeping writes cannot touch the application's database or sidecars.
	inspectionDir, err := os.MkdirTemp("", "komposyn-schema-inspect-")
	if err != nil {
		return 0, fmt.Errorf("create private schema inspection snapshot: %w", err)
	}
	defer os.RemoveAll(inspectionDir)
	inspectionPath := filepath.Join(inspectionDir, "database.sqlite")
	for _, suffix := range schemaSnapshotSuffixes {
		if err := copyFile(path+suffix, inspectionPath+suffix); err != nil {
			if errors.Is(err, os.ErrNotExist) && suffix != "" {
				continue
			}
			return 0, fmt.Errorf("copy database for private schema inspection: %w", err)
		}
	}
	copied, err := schemaSnapshotDigests(inspectionPath)
	if err != nil {
		return 0, fmt.Errorf("fingerprint private schema snapshot: %w", err)
	}
	after, err := schemaSnapshotDigests(path)
	if err != nil {
		return 0, fmt.Errorf("verify schema snapshot source: %w", err)
	}
	if !maps.Equal(before, copied) || !maps.Equal(before, after) {
		return 0, errSchemaSnapshotChanged
	}
	// A journal copied during a writer's transaction can require rollback
	// recovery once separated from the writer's locks. Allow SQLite to recover
	// only this disposable private copy; the source remains untouched.
	uri := sqliteFileURI(inspectionPath, "mode=rw")
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return 0, fmt.Errorf("open private schema snapshot: %w", err)
	}
	defer db.Close()
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&tableCount); err != nil {
		return 0, fmt.Errorf("inspect private snapshot schema: %w", err)
	}
	var hasMigrations bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations')").Scan(&hasMigrations); err != nil {
		return 0, fmt.Errorf("inspect private snapshot migration metadata: %w", err)
	}
	if !hasMigrations {
		if tableCount != 0 {
			return 0, fmt.Errorf("%w: non-empty database has no migration history", ErrInvalidSchema)
		}
		return 0, nil
	}
	var version int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read private snapshot schema version: %w", err)
	}
	if version < 0 {
		return 0, fmt.Errorf("%w: negative migration version %d", ErrInvalidSchema, version)
	}
	if version == 0 && tableCount > 1 {
		return 0, fmt.Errorf("%w: non-empty database has no applied migration history", ErrInvalidSchema)
	}
	return version, nil
}

// resolveDatabasePath gives schema preflight and SQLite the same physical
// database identity, including when callers open the database through a file
// symlink. For a new database, retain the absolute requested path.
func resolveDatabasePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return absolute, nil
	}
	return "", err
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

func sqliteFileURI(path, query string) string {
	uriPath := filepath.ToSlash(path)
	isWindowsDrivePath := len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '/' || path[2] == '\\')
	if isWindowsDrivePath {
		uriPath = strings.ReplaceAll(path, "\\", "/")
	}
	if (filepath.VolumeName(path) != "" || isWindowsDrivePath) && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query}).String()
}
