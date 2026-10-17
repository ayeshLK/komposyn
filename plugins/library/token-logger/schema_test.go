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
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenRefusesNewerSchemaWithoutChangingDatabaseOrWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	owner, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.db.Exec("UPDATE schema_migrations SET version = ? WHERE version = ?", CurrentSchemaVersion+1, CurrentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	beforeDB, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeDBInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{path + "-wal", path + "-shm"}
	beforeSidecars := make(map[string][]byte)
	for _, sidecar := range paths {
		if contents, err := os.ReadFile(sidecar); err == nil {
			beforeSidecars[sidecar] = contents
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}

	opened, err := Open(path)
	if opened != nil {
		_ = opened.Close()
		t.Fatal("Open accepted a database from a newer schema")
	}
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open error = %v, want ErrUnsupportedSchema", err)
	}
	var schemaErr *UnsupportedSchemaError
	if !errors.As(err, &schemaErr) || schemaErr.DatabaseVersion != CurrentSchemaVersion+1 {
		t.Fatalf("typed schema error = %#v", err)
	}
	afterDB, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(afterDB, beforeDB) {
		t.Fatalf("database bytes changed on refusal: read err=%v", err)
	}
	afterDBInfo, err := os.Stat(path)
	if err != nil || !beforeDBInfo.ModTime().Equal(afterDBInfo.ModTime()) {
		t.Fatalf("database timestamp changed on refusal: before=%v after=%v err=%v", beforeDBInfo.ModTime(), afterDBInfo.ModTime(), err)
	}
	for _, sidecar := range paths {
		contents, readErr := os.ReadFile(sidecar)
		before, existed := beforeSidecars[sidecar]
		if existed {
			if readErr != nil || !reflect.DeepEqual(contents, before) {
				firstDifference := -1
				for index := 0; index < len(before) && index < len(contents); index++ {
					if before[index] != contents[index] {
						firstDifference = index
						break
					}
				}
				t.Errorf("existing sidecar %s changed: lengths=%d/%d first difference=%d read err=%v", filepath.Base(sidecar), len(before), len(contents), firstDifference, readErr)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			t.Errorf("refusal created or changed sidecar %s: read err=%v", filepath.Base(sidecar), readErr)
		}
	}
	_ = owner.Close()
}

func TestOpenRefusesNewerSchemaWithHotJournalWithoutChangingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode = DELETE",
		"PRAGMA cache_size = 1",
		"CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)",
		"CREATE TABLE pending(value BLOB)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO schema_migrations VALUES (?)", CurrentSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Spill uncommitted pages to disk. The private inspection copy has no
	// writer lock, so SQLite must recover its copied rollback journal.
	if _, err := tx.Exec("INSERT INTO pending VALUES (zeroblob(65536))"); err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(path + "-journal")
	if err != nil {
		t.Fatalf("read spilled rollback journal required for recovery coverage: %v", err)
	}
	// SQLite's hot-journal header begins with this eight-byte magic. An
	// allocated but unflushed journal does not exercise rollback recovery.
	if len(journal) <= 512 || !bytes.Equal(journal[:8], []byte{0xd9, 0xd5, 0x05, 0xf9, 0x20, 0xa1, 0x63, 0xd7}) {
		t.Fatalf("expected a flushed hot rollback journal larger than 512 bytes; size=%d", len(journal))
	}
	paths := []string{path, path + "-journal"}
	before := make(map[string][]byte)
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = contents
	}
	store, err := Open(path)
	if store != nil {
		_ = store.Close()
		t.Fatal("Open accepted a database from a newer schema")
	}
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open error = %v, want ErrUnsupportedSchema", err)
	}
	for _, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(after, before[path]) {
			t.Errorf("source %s changed on refusal: %v", filepath.Base(path), err)
		}
	}
}

func TestSchemaSnapshotRetriesConcurrentSourceChanges(t *testing.T) {
	for _, action := range []string{"commit", "rollback", "checkpoint"} {
		t.Run(action, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "changing.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			mode := "DELETE"
			if action == "checkpoint" {
				mode = "WAL"
			}
			for _, statement := range []string{
				"PRAGMA journal_mode = " + mode,
				"PRAGMA cache_size = 1",
				"CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)",
				"CREATE TABLE pending(value BLOB)",
			} {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec("INSERT INTO schema_migrations VALUES (?)", CurrentSchemaVersion); err != nil {
				t.Fatal(err)
			}
			var tx *sql.Tx
			if action != "checkpoint" {
				tx, err = db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Exec("UPDATE schema_migrations SET version = ?", CurrentSchemaVersion+1); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec("INSERT INTO pending VALUES (zeroblob(65536))"); err != nil {
					t.Fatal(err)
				}
			}
			copies := 0
			version, exists, err := inspectSchemaSnapshotWithCopier(path, func(source, destination string) error {
				if err := copyRegularFile(source, destination); err != nil {
					return err
				}
				if source != path {
					return nil
				}
				copies++
				if copies != 1 {
					return nil
				}
				// Change the source between the main-file and sidecar copies.
				switch action {
				case "commit":
					return tx.Commit()
				case "rollback":
					return tx.Rollback()
				default:
					if _, err := db.Exec("UPDATE schema_migrations SET version = ?", CurrentSchemaVersion+1); err != nil {
						return err
					}
					_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
					return err
				}
			})
			want := CurrentSchemaVersion + 1
			if action == "rollback" {
				want = CurrentSchemaVersion
			}
			if err != nil || !exists || version != want || copies < 2 {
				t.Fatalf("snapshot version=%d exists=%t copies=%d err=%v; want version=%d after retry", version, exists, copies, err, want)
			}
		})
	}
}

func TestSchemaSnapshotBoundsRetriesForChangingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changing.db")
	owner, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	copies := 0
	_, _, err = inspectSchemaSnapshotWithCopier(path, func(source, destination string) error {
		if err := copyRegularFile(source, destination); err != nil {
			return err
		}
		if source == path {
			copies++
			_, err := owner.db.Exec("UPDATE schema_migrations SET version = version + 1 WHERE version = (SELECT MAX(version) FROM schema_migrations)")
			return err
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "source changed during copy") || copies != 5 {
		t.Fatalf("copies=%d err=%v; want bounded failure after 5 changed snapshots", copies, err)
	}
}

func TestMigrateRechecksLiveVersionAfterSnapshotPreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changed-version.db")
	owner, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	version, _, err := inspectSchemaSnapshot(path)
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("preflight version=%d err=%v", version, err)
	}
	if _, err := owner.db.Exec("UPDATE schema_migrations SET version = ? WHERE version = ?", CurrentSchemaVersion+1, CurrentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := owner.migrate(); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("live migration error=%v; want ErrUnsupportedSchema", err)
	}
}

func TestOpenRefusesNewerSchemaThroughSymlinkWithoutChangingTargetSidecars(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.db")
	link := filepath.Join(directory, "link.db")
	owner, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.db.Exec("PRAGMA wal_autocheckpoint = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.db.Exec("UPDATE schema_migrations SET version = ? WHERE version = ?", CurrentSchemaVersion+1, CurrentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		_ = owner.Close()
		t.Skipf("file symlinks unavailable: %v", err)
	}
	paths := []string{target, target + "-wal", target + "-shm"}
	before := make(map[string][]byte, len(paths))
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			_ = owner.Close()
			t.Fatalf("read %s: %v", filepath.Base(path), err)
		}
		before[path] = contents
	}

	opened, err := Open(link)
	if opened != nil {
		_ = opened.Close()
		_ = owner.Close()
		t.Fatal("Open accepted a symlink to a database from a newer schema")
	}
	if !errors.Is(err, ErrUnsupportedSchema) {
		_ = owner.Close()
		t.Fatalf("Open error = %v, want ErrUnsupportedSchema", err)
	}
	for _, path := range paths {
		after, readErr := os.ReadFile(path)
		if readErr != nil || !reflect.DeepEqual(after, before[path]) {
			t.Errorf("target %s changed on refusal: read err=%v", filepath.Base(path), readErr)
		}
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsNonemptyDatabaseWithoutMigrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-untracked.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE unknown_data(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := Open(path); store != nil || !errors.Is(err, ErrInvalidSchema) {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("Open = %v, %v; want ErrInvalidSchema", store, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid schema refusal changed database: %v", err)
	}
}

func TestOpenCreatesEmptyDatabaseAndReportsCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "fresh.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if SchemaVersion() != CurrentSchemaVersion || OldestMigratableSchemaVersion != 1 {
		t.Fatalf("schema compatibility = current %d, oldest %d", SchemaVersion(), OldestMigratableSchemaVersion)
	}
}

func TestSQLiteFileURIEncodesWindowsDrivePath(t *testing.T) {
	got := sqliteFileURI(`C:\Users\example\token-logger.db`, "mode=ro")
	if got != "file:///C:/Users/example/token-logger.db?mode=ro" {
		t.Fatalf("SQLite file URI = %q, want a triple-slash Windows drive URI", got)
	}
}

func TestMigrationBackupFailureLeavesSchemaUnmigratedAndPreservesDestination(t *testing.T) {
	tempDir := t.TempDir()
	databasePath := filepath.Join(tempDir, "legacy.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaMigrationOne); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (1, '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(tempDir, "existing-backup.sqlite")
	sentinel := []byte("caller-owned backup")
	if err := os.WriteFile(backupPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenWithOptions(databasePath, OpenOptions{BackupPath: backupPath})
	if store != nil {
		_ = store.Close()
		t.Fatal("OpenWithOptions succeeded although backup creation was blocked")
	}
	if err == nil {
		t.Fatal("OpenWithOptions did not report backup failure")
	}
	backupContents, err := os.ReadFile(backupPath)
	if err != nil || !reflect.DeepEqual(backupContents, sentinel) {
		t.Fatalf("existing backup destination changed: err=%v", err)
	}
	db, err = sql.Open("sqlite", sqliteFileURI(databasePath, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != 1 {
		t.Fatalf("schema version after backup failure = %d, %v; want 1", version, err)
	}
}
