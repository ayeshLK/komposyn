// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package sqlitevfsstudy

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sqlite "modernc.org/sqlite"
	"modernc.org/sqlite/vfs"
)

const driverVersion = "v1.61.0"

// Report records only fixture-relative names and whether synthetic marker
// bytes were observed. It intentionally contains no SQL, database contents,
// or host fixture paths.
type Report struct {
	SchemaVersion      int      `json:"schema_version"`
	SourceCommit       string   `json:"source_commit"`
	RunAt              string   `json:"run_at_utc"`
	OS                 string   `json:"os"`
	Architecture       string   `json:"architecture"`
	GoVersion          string   `json:"go_version"`
	DriverVersion      string   `json:"driver_version"`
	DriverSourceCommit string   `json:"driver_source_commit"`
	SQLiteVersion      string   `json:"sqlite_version"`
	NamedVFS           string   `json:"named_vfs"`
	DefaultVFS         string   `json:"default_vfs_unchanged"`
	Cases              []Case   `json:"cases"`
	Conclusion         string   `json:"conclusion"`
	Limitations        []string `json:"limitations"`
}

type Case struct {
	Name           string   `json:"name"`
	Attempted      bool     `json:"attempted"`
	Classification string   `json:"classification"`
	Observed       string   `json:"observed"`
	Identity       string   `json:"exact_object_identity"`
	CallbackNames  []string `json:"callback_names,omitempty"`
	Effects        []string `json:"fixture_effects,omitempty"`
}

type observedFS struct {
	fs.FS
	mu    sync.Mutex
	opens []string
}

func (f *observedFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	if len(f.opens) < 64 {
		f.opens = append(f.opens, filepath.ToSlash(name))
	}
	f.mu.Unlock()
	return f.FS.Open(name)
}

func (f *observedFS) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opens...)
}

// Run exercises named-VFS selection, read-only refusal and independent
// SQLite online-backup routing against a private synthetic fixture.
func Run(ctx context.Context, commit string) (Report, error) {
	if len(commit) != 40 {
		return Report{}, errors.New("source commit must be a full 40-character commit hash")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return Report{}, errors.New("source commit must be hexadecimal")
	}
	if _, err := os.Stat("/proc/self"); err != nil {
		return Report{}, fmt.Errorf("Linux environment unavailable: %w", err)
	}
	dir, err := os.MkdirTemp("", "komposyn-sqlite-vfs-study-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(dir)

	marker := []byte("synthetic-marker-215")
	sourcePath := filepath.Join(dir, "source.db")
	if err := createFixture(ctx, sourcePath, marker); err != nil {
		return Report{}, fmt.Errorf("create synthetic source: %w", err)
	}

	observer := &observedFS{FS: os.DirFS(dir)}
	name, adapter, err := vfs.New(observer)
	if err != nil {
		return Report{}, fmt.Errorf("register named read-only VFS: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = adapter.Close()
		}
	}()

	report := Report{
		SchemaVersion:      1,
		SourceCommit:       commit,
		RunAt:              time.Now().UTC().Format(time.RFC3339),
		OS:                 runtime.GOOS,
		Architecture:       runtime.GOARCH,
		GoVersion:          runtime.Version(),
		DriverVersion:      driverVersion,
		DriverSourceCommit: "4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738",
		NamedVFS:           name,
		Cases:              []Case{},
		Limitations: []string{
			"The supported modernc.org/sqlite/vfs adapter is read-only; no writable callback adapter was implemented.",
			"No exact native descriptor was registered before SQLite writes; the experiment cannot establish a write barrier.",
			"WAL/SHM acceptance is unavailable. A named read-only VFS refusal is not proof of a pre-native xShmMap interception.",
			"The plain backup destination is an intentionally unprotected control and is confined to this disposable fixture.",
			"This prototype does not cover Token Logger, concurrent external actors, rotation, production integration, or non-Linux behavior.",
		},
	}

	// vfs.New registers with makeDflt=false. This is grounded in the pinned
	// dependency source; the default VFS name is not exposed by its public API.
	report.DefaultVFS = "preserved by non-default registration (source-inspected)"

	uri := "file:source.db?mode=ro&vfs=" + name
	readDB, err := sql.Open("sqlite", uri)
	if err != nil {
		return Report{}, err
	}
	defer readDB.Close()
	readDB.SetMaxOpenConns(1)
	var sqliteVersion string
	if err := readDB.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		_ = readDB.Close()
		return Report{}, fmt.Errorf("read synthetic fixture through named VFS: %w", err)
	}
	report.SQLiteVersion = sqliteVersion
	var got string
	if err := readDB.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&got); err != nil {
		_ = readDB.Close()
		return Report{}, fmt.Errorf("read marker through named VFS: %w", err)
	}
	report.Cases = append(report.Cases, Case{
		Name:           "named_vfs_read_control",
		Attempted:      true,
		Classification: "measured_pass",
		Observed:       "SQLite opened the synthetic source through the non-default named VFS and returned the fixture marker.",
		Identity:       "Not captured by the supported adapter; no inode/descriptor registration is claimed.",
		CallbackNames:  observer.names(),
		Effects:        []string{"source fixture unchanged"},
	})

	_, updateErr := readDB.ExecContext(ctx, "UPDATE fixture SET marker = 'must-not-write'")
	if updateErr == nil {
		_ = readDB.Close()
		return Report{}, errors.New("read-only named VFS unexpectedly accepted a write")
	}
	var markerAfter string
	if err := readDB.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&markerAfter); err != nil || markerAfter != string(marker) {
		_ = readDB.Close()
		return Report{}, errors.New("fixture marker could not be verified unchanged after rejected update")
	}
	report.Cases = append(report.Cases, Case{
		Name:           "named_vfs_write_refusal",
		Attempted:      true,
		Classification: "measured_refusal",
		Observed:       "An UPDATE through the supported named VFS failed because its xOpen refuses the main-journal role (SQLite reports SQLITE_NOMEM); the marker remained unchanged.",
		Identity:       "Not captured; refusal is an adapter capability limit, not an identity-bound authorization decision.",
		CallbackNames:  observer.names(),
		Effects:        []string{"synthetic marker unchanged"},
	})
	if err := readDB.Close(); err != nil {
		return Report{}, fmt.Errorf("close named VFS source: %w", err)
	}

	shmPath := sourcePath + "-shm"
	walPath := sourcePath + "-wal"
	shmBefore := inspectPath(shmPath)
	walBefore := inspectPath(walPath)
	walAttempted := shmBefore == pathAbsent && walBefore == pathAbsent
	walDB, err := sql.Open("sqlite", "file:source.db?mode=ro&vfs="+name)
	if err != nil {
		return Report{}, err
	}
	defer walDB.Close()
	walDB.SetMaxOpenConns(1)
	var walMode string
	var walErr error
	if walAttempted {
		walErr = walDB.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&walMode)
	} else {
		walErr = errors.New("WAL precondition paths were not absent")
	}
	closeErr := walDB.Close()
	if closeErr != nil {
		return Report{}, fmt.Errorf("close named VFS after WAL attempt: %w", closeErr)
	}
	shmAfter := inspectPath(shmPath)
	walAfter := inspectPath(walPath)
	walCase := classifyWALRefusal(walAttempted, walErr, walMode, walBefore, walAfter, shmBefore, shmAfter)
	walCase.CallbackNames = observer.names()
	report.Cases = append(report.Cases, walCase)

	backupSource, err := sql.Open("sqlite", uri)
	if err != nil {
		return Report{}, err
	}
	backupSource.SetMaxOpenConns(1)
	defer backupSource.Close()
	backupConn, err := backupSource.Conn(ctx)
	if err != nil {
		return Report{}, err
	}
	defer backupConn.Close()
	plainPath := filepath.Join(dir, "plain-control.db")
	if err := runBackup(ctx, backupConn, plainPath); err != nil {
		return Report{}, fmt.Errorf("plain destination control backup: %w", err)
	}
	plainExists := fileExists(plainPath)
	plainMarker, plainReadable := readFixtureMarker(ctx, plainPath)
	if !plainExists || !plainReadable || plainMarker != string(marker) {
		return Report{}, errors.New("plain default-VFS control backup was not verified against the synthetic source")
	}
	report.Cases = append(report.Cases, Case{
		Name:           "backup_plain_destination_control",
		Attempted:      true,
		Classification: "measured_unprotected_control",
		Observed:       "NewBackup's plain destination succeeded via SQLite's independently selected default VFS; source named-VFS selection did not protect it.",
		Identity:       "Destination object was not opened through the named adapter; identity was not registered.",
		CallbackNames:  observer.names(),
		Effects:        effectIf(plainExists, "plain-control.db created in disposable fixture", "plain-control.db absent"),
	})

	firstPath := filepath.Join(dir, "explicit-refused.db")
	secondPath := filepath.Join(dir, "retry-refused.db")
	firstBefore := inspectPath(firstPath)
	secondBefore := inspectPath(secondPath)
	firstAttempted := firstBefore == pathAbsent
	secondAttempted := secondBefore == pathAbsent
	var firstErr, secondErr error
	if firstAttempted {
		firstErr = runBackup(ctx, backupConn, "file:explicit-refused.db?vfs="+name)
	} else {
		firstErr = errors.New("destination precondition was not absent")
	}
	firstAfter := inspectPath(firstPath)
	if secondAttempted {
		secondErr = runBackup(ctx, backupConn, "file:retry-refused.db?vfs="+name)
	} else {
		secondErr = errors.New("fresh retry precondition was not absent")
	}
	secondAfter := inspectPath(secondPath)
	backupCase := classifyBackupRefusal(firstAttempted, firstErr, firstBefore, firstAfter, secondAttempted, secondErr, secondBefore, secondAfter)
	backupCase.CallbackNames = observer.names()
	report.Cases = append(report.Cases, backupCase)

	if err := backupConn.Close(); err != nil {
		return Report{}, err
	}
	if err := backupSource.Close(); err != nil {
		return Report{}, err
	}
	if err := adapter.Close(); err != nil {
		return Report{}, fmt.Errorf("unregister named VFS after closing connections: %w", err)
	}
	closed = true
	report.Conclusion = "The pinned driver supports non-default read-only VFS selection and SQLite demonstrates independent backup destination routing. A no-fork writable exact-object pre-write adapter and pre-native SHM protection were not demonstrated; writable Token Logger coverage remains unsupported."
	return report, nil
}

func createFixture(ctx context.Context, path string, marker []byte) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE TABLE fixture (marker TEXT)"); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "INSERT INTO fixture(marker) VALUES (?)", string(marker))
	if err != nil {
		return err
	}
	return db.Close()
}

func runBackup(ctx context.Context, conn *sql.Conn, destination string) error {
	// SQLite's driver Backup API is synchronous and has no context hook. The
	// study fixture is one tiny page; the caller bounds connection setup and
	// only uses disposable local files.
	_ = ctx
	err := conn.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("driver connection lacks NewBackup")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return err
		}
		more, err := backup.Step(-1)
		if err != nil {
			return finishBackup(backup, err)
		}
		if more {
			return finishBackup(backup, errors.New("bounded fixture backup was incomplete"))
		}
		destinationConn, err := backup.Commit()
		if err != nil {
			return err
		}
		return destinationConn.Close()
	})
	return err
}

func finishBackup(backup *sqlite.Backup, cause error) error {
	if err := backup.Finish(); err != nil {
		return fmt.Errorf("backup failed (%v), and finish cleanup failed: %w", cause, err)
	}
	return cause
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readFixtureMarker(ctx context.Context, path string) (string, bool) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return "", false
	}
	defer db.Close()
	var marker string
	if err := db.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&marker); err != nil {
		return "", false
	}
	return marker, true
}

func effectIf(ok bool, yes, no string) []string {
	if ok {
		return []string{yes}
	}
	return []string{no}
}

// Marshal returns the bounded, sanitized machine-readable record.
func (r Report) Marshal() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// MaxRunTime is used by the command so fixture work cannot wait indefinitely.
const MaxRunTime = 30 * time.Second

const (
	pathAbsent          = "absent"
	pathPresent         = "present"
	pathInspectionError = "inspection_error"
)

func inspectPath(path string) string {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return pathPresent
	case errors.Is(err, os.ErrNotExist):
		return pathAbsent
	default:
		return pathInspectionError
	}
}

func classifyWALRefusal(attempted bool, queryErr error, journalMode, walBefore, walAfter, shmBefore, shmAfter string) Case {
	allAbsent := walBefore == pathAbsent && walAfter == pathAbsent && shmBefore == pathAbsent && shmAfter == pathAbsent
	classification := "inconclusive"
	if attempted && queryErr == nil && !strings.EqualFold(journalMode, "wal") && allAbsent {
		classification = "measured_refusal"
	}
	observed := fmt.Sprintf("attempted=%t, journal_mode=%q, query_error=%t; WAL path before/after=%s/%s, SHM path before/after=%s/%s. This does not prove an xShmMap interception.", attempted, journalMode, queryErr != nil, walBefore, walAfter, shmBefore, shmAfter)
	return Case{
		Name:           "wal_shm_stop_condition",
		Attempted:      attempted,
		Classification: classification,
		Observed:       observed,
		Identity:       "SHM identity was not exposed before SQLite's native SHM path; no xShmMap protection is claimed.",
		Effects: []string{
			fmt.Sprintf("WAL path state: before=%s after=%s", walBefore, walAfter),
			fmt.Sprintf("SHM path state: before=%s after=%s", shmBefore, shmAfter),
		},
	}
}

func classifyBackupRefusal(firstAttempted bool, firstErr error, firstBefore, firstAfter string, secondAttempted bool, secondErr error, secondBefore, secondAfter string) Case {
	allAbsent := firstBefore == pathAbsent && firstAfter == pathAbsent && secondBefore == pathAbsent && secondAfter == pathAbsent
	classification := "inconclusive"
	if firstAttempted && secondAttempted && firstErr != nil && secondErr != nil && allAbsent {
		classification = "measured_refusal"
	}
	observed := fmt.Sprintf("attempted(first/retry)=%t/%t, returned_error(first/retry)=%t/%t; destination path states first=%s/%s, fresh retry=%s/%s. Final path absence cannot detect transient effects.", firstAttempted, secondAttempted, firstErr != nil, secondErr != nil, firstBefore, firstAfter, secondBefore, secondAfter)
	return Case{
		Name:           "backup_named_destination_refusal_and_fresh_retry",
		Attempted:      firstAttempted || secondAttempted,
		Classification: classification,
		Observed:       observed,
		Identity:       "No destination identity was captured; path observations are not object-identity proof.",
		Effects: []string{
			fmt.Sprintf("explicit destination path state: before=%s after=%s", firstBefore, firstAfter),
			fmt.Sprintf("fresh retry path state: before=%s after=%s", secondBefore, secondAfter),
		},
	}
}
