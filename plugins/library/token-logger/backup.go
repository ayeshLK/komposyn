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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sqlite3 "modernc.org/sqlite"
)

// OpenOptions selects a non-overwriting migration-backup destination. An
// empty BackupPath stores backups beside the database using private permissions.
type OpenOptions struct {
	BackupPath string
}

// MigrationBackupMetadata records the verified state immediately before a
// numbered migration. It is stored inside the backup database.
type MigrationBackupMetadata struct {
	SourceSchema int
	TargetSchema int
	CreatedAt    time.Time
	CoreVersion  string
}

func (s *Store) MigrationBackupPath() string { return s.migrationBackupPath }

func createMigrationBackup(sourcePath, requestedPath string, sourceSchema, targetSchema int) (path string, err error) {
	if sourcePath == ":memory:" {
		return "", nil
	}
	if requestedPath == "" {
		file, createErr := os.CreateTemp(filepath.Dir(sourcePath), ".komposyn-pre-migration-*.sqlite")
		if createErr != nil {
			return "", fmt.Errorf("create migration backup: %w", createErr)
		}
		path = file.Name()
		if chmodErr := file.Chmod(0o600); chmodErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("protect migration backup: %w", chmodErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("close migration backup destination: %w", closeErr)
		}
	} else {
		requestedPath, err = filepath.Abs(requestedPath)
		if err != nil {
			return "", fmt.Errorf("resolve migration backup path: %w", err)
		}
		sourceAbsolute, absErr := filepath.Abs(sourcePath)
		if absErr != nil {
			return "", fmt.Errorf("resolve database path: %w", absErr)
		}
		if filepath.Clean(requestedPath) == filepath.Clean(sourceAbsolute) {
			return "", errors.New("migration backup path must differ from database path")
		}
		file, createErr := os.OpenFile(requestedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return "", fmt.Errorf("create migration backup without overwriting: %w", createErr)
		}
		path = requestedPath
		if chmodErr := file.Chmod(0o600); chmodErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("protect migration backup: %w", chmodErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("close migration backup destination: %w", closeErr)
		}
	}
	ownedPath := path
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(ownedPath)
		}
	}()
	if err := runOnlineBackup(sourcePath, path); err != nil {
		return "", err
	}
	metadata := MigrationBackupMetadata{SourceSchema: sourceSchema, TargetSchema: targetSchema, CreatedAt: time.Now().UTC(), CoreVersion: Version}
	if err := writeBackupMetadata(path, metadata); err != nil {
		return "", err
	}
	if err := verifyMigrationBackup(path, metadata); err != nil {
		return "", err
	}
	complete = true
	return path, nil
}

func runOnlineBackup(sourcePath, destinationPath string) error {
	sourceAbsolute, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve backup source: %w", err)
	}
	uri := "file:" + filepath.ToSlash(sourceAbsolute) + "?mode=ro"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return fmt.Errorf("open migration backup source: %w", err)
	}
	defer db.Close()
	connection, err := db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("acquire migration backup source: %w", err)
	}
	defer connection.Close()
	var backupErr error
	err = connection.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite3.Backup, error)
		})
		if !ok {
			return errors.New("SQLite driver does not provide online backup")
		}
		backup, err := backuper.NewBackup(destinationPath)
		if err != nil {
			return fmt.Errorf("start online backup: %w", err)
		}
		defer func() {
			if backup != nil {
				backupErr = backup.Finish()
			}
		}()
		for {
			more, err := backup.Step(-1)
			if err != nil {
				return fmt.Errorf("copy online backup pages: %w", err)
			}
			if !more {
				connection, err := backup.Commit()
				backup = nil
				if err != nil {
					return fmt.Errorf("finish online backup: %w", err)
				}
				if err := connection.Close(); err != nil {
					return fmt.Errorf("close online backup destination: %w", err)
				}
				return nil
			}
		}
	})
	if err != nil {
		return err
	}
	if backupErr != nil {
		return fmt.Errorf("finish online backup: %w", backupErr)
	}
	return nil
}

func writeBackupMetadata(path string, metadata MigrationBackupMetadata) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open backup for metadata: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS komposyn_migration_backup (
		source_schema INTEGER NOT NULL,
		target_schema INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		core_version TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("write backup metadata table: %w", err)
	}
	if _, err := db.Exec("DELETE FROM komposyn_migration_backup"); err != nil {
		return fmt.Errorf("replace backup metadata: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO komposyn_migration_backup(source_schema, target_schema, created_at, core_version)
		VALUES (?, ?, ?, ?)`, metadata.SourceSchema, metadata.TargetSchema, metadata.CreatedAt.Format(time.RFC3339Nano), metadata.CoreVersion); err != nil {
		return fmt.Errorf("write backup metadata: %w", err)
	}
	return nil
}

func verifyMigrationBackup(path string, expected MigrationBackupMetadata) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open backup for verification: %w", err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("verify backup integrity: %w", err)
	} else if integrity != "ok" {
		return fmt.Errorf("verify backup integrity: result=%q", integrity)
	}
	var schema int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&schema); err != nil {
		return fmt.Errorf("verify backup source schema: %w", err)
	} else if schema != expected.SourceSchema {
		return fmt.Errorf("verify backup source schema: got=%d want=%d", schema, expected.SourceSchema)
	}
	var source, target int
	var created, version string
	if err := db.QueryRow(`SELECT source_schema, target_schema, created_at, core_version FROM komposyn_migration_backup`).Scan(&source, &target, &created, &version); err != nil {
		return fmt.Errorf("verify backup metadata: %w", err)
	}
	createdAt, parseErr := time.Parse(time.RFC3339Nano, created)
	if parseErr != nil || source != expected.SourceSchema || target != expected.TargetSchema || !createdAt.Equal(expected.CreatedAt) || version != expected.CoreVersion {
		return fmt.Errorf("verify backup metadata values: source=%d target=%d created=%q version=%q", source, target, created, version)
	}
	return nil
}
