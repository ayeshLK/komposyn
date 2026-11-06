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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestReadBrokerLifecycleProbe records the SQLite artifact lifecycle using
// synthetic temporary databases. Run with -v to print checkpointed evidence.
// This is not a broker test and does not prove a pre-write enforcement hook.
func TestReadBrokerLifecycleProbe(t *testing.T) {
	t.Logf("driver=modernc.org/sqlite v1.61.0 core=%s", Version)
	root := t.TempDir()

	freshDir := filepath.Join(root, "fresh")
	if err := os.Mkdir(freshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	freshPath := filepath.Join(freshDir, "fresh.sqlite")
	store, err := Open(freshPath)
	if err != nil {
		t.Fatalf("open fresh fixture: %v", err)
	}
	observeLifecycleFiles(t, "fresh.open", freshDir)
	if _, err := store.RecordDiagnostic("synthetic lifecycle probe"); err != nil {
		t.Fatalf("write synthetic fixture row: %v", err)
	}
	observeLifecycleFiles(t, "fresh.written", freshDir)
	if err := store.Close(); err != nil {
		t.Fatalf("close fresh fixture: %v", err)
	}
	observeLifecycleFiles(t, "fresh.closed", freshDir)

	store, err = Open(freshPath)
	if err != nil {
		t.Fatalf("reopen fresh fixture: %v", err)
	}
	observeLifecycleFiles(t, "fresh.reopened", freshDir)
	if err := store.Close(); err != nil {
		t.Fatalf("close reopened fixture: %v", err)
	}
	observeLifecycleFiles(t, "fresh.reopened_closed", freshDir)

	phaseDir := filepath.Join(root, "backup-phases")
	if err := os.Mkdir(phaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	phaseSource := filepath.Join(phaseDir, "source.sqlite")
	createLifecycleSchemaV1(t, phaseSource, false)
	var sqliteVersion string
	source, err := sql.Open("sqlite", sqliteFileURI(phaseSource, "mode=ro"))
	if err != nil {
		t.Fatalf("open source for provenance: %v", err)
	}
	if err := source.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatalf("read SQLite version: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close provenance connection: %v", err)
	}
	t.Logf("sqlite_version=%s", sqliteVersion)
	phaseBackup := filepath.Join(phaseDir, "backup.sqlite")
	if err := runOnlineBackup(phaseSource, phaseBackup); err != nil {
		t.Fatalf("run online backup: %v", err)
	}
	observeLifecycleFiles(t, "backup.online_complete", phaseDir)
	metadata := MigrationBackupMetadata{
		SourceSchema: 1, TargetSchema: CurrentSchemaVersion,
		CreatedAt: time.Now().UTC(), CoreVersion: Version,
	}
	if err := writeBackupMetadata(phaseBackup, metadata); err != nil {
		t.Fatalf("write backup metadata: %v", err)
	}
	observeLifecycleFiles(t, "backup.metadata_complete", phaseDir)
	if err := verifyMigrationBackup(phaseBackup, metadata); err != nil {
		t.Fatalf("verify backup: %v", err)
	}
	observeLifecycleFiles(t, "backup.verification_complete", phaseDir)

	failureDir := filepath.Join(root, "later-migration-failure")
	if err := os.Mkdir(failureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	failureSource := filepath.Join(failureDir, "legacy.sqlite")
	createLifecycleSchemaV1(t, failureSource, true)
	failureBackup := filepath.Join(failureDir, "explicit-backup.sqlite")
	failedStore, openErr := OpenWithOptions(failureSource, OpenOptions{BackupPath: failureBackup})
	if failedStore != nil {
		_ = failedStore.Close()
		t.Fatal("expected synthetic migration 2 failure")
	}
	if openErr == nil || !strings.Contains(openErr.Error(), "apply migration 2") {
		t.Fatalf("open error = %v, want migration 2 conflict", openErr)
	}
	t.Logf("expected_later_migration_error=%q", openErr.Error())
	observeLifecycleFiles(t, "migration.failed_after_verified_backup", failureDir)
	if _, err := os.Stat(failureBackup); err != nil {
		t.Fatalf("verified backup should remain after later migration failure: %v", err)
	}

	t.Logf("fixture_root=%s (removed by t.TempDir cleanup)", root)
}

func createLifecycleSchemaV1(t *testing.T, path string, forceMigrationConflict bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("create schema fixture: %v", err)
	}
	for _, statement := range []string{
		"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)",
		"INSERT INTO schema_migrations(version, applied_at) VALUES (1, 'synthetic')",
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("initialize schema fixture: %v", err)
		}
	}
	if forceMigrationConflict {
		if _, err := db.Exec("CREATE TABLE diagnostics (synthetic INTEGER)"); err != nil {
			_ = db.Close()
			t.Fatalf("prepare deterministic migration conflict: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close schema fixture: %v", err)
	}
}

func observeLifecycleFiles(t *testing.T, checkpoint, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixture directory at %s: %v", checkpoint, err)
	}
	observed := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat fixture entry at %s: %v", checkpoint, err)
		}
		observed = append(observed, map[string]any{
			"name": entry.Name(), "mode": info.Mode().String(), "size": info.Size(),
		})
	}
	sort.Slice(observed, func(i, j int) bool { return observed[i]["name"].(string) < observed[j]["name"].(string) })
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checkpoint=%s dir=%s entries=%s", checkpoint, dir, encoded)
}
