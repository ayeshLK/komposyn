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

//go:build linux && amd64

package sqlitewritebarrier

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	sqlite "modernc.org/sqlite"
	lib "modernc.org/sqlite/lib"
)

func TestExactObjectRegistrationAndWriteBarrier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.db")
	if err := preparePersistentDB(path); err != nil {
		t.Fatal(err)
	}

	type gateRecord struct {
		reg     Registration
		atWrite bool
	}
	var mu sync.Mutex
	var registrations []gateRecord
	var adapter *Adapter
	firstMutationStarted := false
	gate := func(ctx context.Context, reg Registration) (Decision, error) {
		select {
		case <-ctx.Done():
			return Deny, ctx.Err()
		default:
		}
		mu.Lock()
		atWrite := false
		if adapter != nil {
			for _, e := range adapter.Events() {
				if e.Kind == "write" {
					atWrite = true
					firstMutationStarted = true
				}
			}
			if !firstMutationStarted {
				registrations = append(registrations, gateRecord{reg, atWrite})
			}
		}
		mu.Unlock()
		return Allow, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := New(ctx, gate)
	if err != nil {
		t.Fatal(err)
	}
	adapter = a
	if err := updateViaVFS(ctx, path, a.Name(), "allowed-value"); err != nil {
		t.Fatalf("update through named VFS: %v; events=%+v", err, a.Events())
	}
	if got, err := readViaVFS(ctx, path, a.Name()); err != nil || got != "allowed-value" {
		t.Fatalf("same-adapter close/reopen read=%q err=%v", got, err)
	}
	a.mu.Lock()
	pins := append([]int(nil), a.pins...)
	a.mu.Unlock()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("close adapter: %v", err)
	}
	for _, fd := range pins {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); !errors.Is(err, unix.EBADF) {
			t.Fatalf("pin fd %d was not closed at shutdown: %v", fd, err)
		}
	}
	if len(registrations) < 2 {
		t.Fatalf("expected main and rollback-journal registration, got %#v", registrations)
	}
	for _, r := range registrations {
		if r.atWrite {
			t.Fatalf("write observed before registration decision for %s", r.reg.Role)
		}
	}
	var writes int
	for _, e := range a.Events() {
		if e.Kind == "write" {
			writes++
			if !e.Matched || e.Result != 0 {
				t.Fatalf("unmatched or failed write event: %+v", e)
			}
		}
	}
	mainWrite, journalWrite := false, false
	for _, e := range a.Events() {
		if e.Kind == "write" {
			mainWrite = mainWrite || e.Role == MainDB
			journalWrite = journalWrite || e.Role == MainJournal
		}
	}
	if writes == 0 || !mainWrite || !journalWrite {
		t.Fatalf("expected exact writes to main and rollback journal; events=%+v", a.Events())
	}
	got, err := readMarker(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "allowed-value" {
		t.Fatalf("readback=%q", got)
	}
}

func TestStudyReportContainsSanitizedMeasuredCases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := Run(ctx, "6e9404db8d897f5afae5c3b0eb9f1c1e7be8e663")
	if err != nil {
		t.Fatal(err)
	}
	if report.SQLiteVersion != "3.54.1" || report.DriverVersion != "v1.61.0" {
		t.Fatalf("unexpected provenance: %+v", report)
	}
	if !report.ExecutionComplete || !strings.Contains(report.Conclusion, "not production") {
		t.Fatalf("report omits completion/limitation summary: %+v", report)
	}
	want := map[string]string{"main_and_rollback_journal_write": "measured_pass", "journal_registration_refusal": "measured_refusal", "fresh_object_retry": "measured_pass", "sqlite_wal_negotiation": "measured_refusal", "installed_shm_map_callback_stop": "measured_pass", "wal_role_callback_stop": "measured_pass", "online_backup_named_destination": "measured_pass", "online_backup_denial_preserves_previous": "measured_refusal", "online_backup_fresh_retry": "measured_pass"}
	for _, c := range report.Cases {
		if expected, ok := want[c.Name]; ok {
			if c.Classification != expected {
				t.Errorf("%s: classification=%s want=%s", c.Name, c.Classification, expected)
			}
			if !c.EvidenceComplete {
				t.Errorf("%s: callback evidence incomplete", c.Name)
			}
			delete(want, c.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing report cases: %v", want)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("/tmp/")) || bytes.Contains(b, []byte("/home/")) {
		t.Fatal("report unexpectedly contains an absolute host path")
	}
}

func TestDeniedRegistrationThenFreshAllowedRetry(t *testing.T) {
	dir := t.TempDir()
	deniedPath := filepath.Join(dir, "denied.db")
	if err := preparePersistentDB(deniedPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var denyOnce sync.Once
	a, err := New(ctx, func(_ context.Context, reg Registration) (Decision, error) {
		denied := false
		if reg.Role == MainJournal {
			denyOnce.Do(func() { denied = true })
		}
		if denied {
			return Deny, nil
		}
		return Allow, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = updateViaVFS(ctx, deniedPath, a.Name(), "must-not-commit")
	if err == nil {
		t.Fatal("denied registration unexpectedly updated DB")
	}
	unchanged, e := readMarker(ctx, deniedPath)
	if e != nil {
		t.Fatal(e)
	}
	if unchanged != "before" {
		t.Fatalf("denied transaction changed content: %q", unchanged)
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// Retry is a separate fresh database object and a fresh adapter decision.
	retryPath := filepath.Join(dir, "retry.db")
	if err := preparePersistentDB(retryPath); err != nil {
		t.Fatal(err)
	}
	b, err := New(ctx, func(context.Context, Registration) (Decision, error) { return Allow, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := updateViaVFS(ctx, retryPath, b.Name(), "fresh-success"); err != nil {
		t.Fatalf("fresh retry: %v", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := readMarker(ctx, retryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "fresh-success" {
		t.Fatalf("fresh retry readback=%q", got)
	}
}

func TestCancellationAndMismatchedIdentityFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cancel.db")
	if err := preparePersistentDB(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	var enteredOnce sync.Once
	gate := func(ctx context.Context, _ Registration) (Decision, error) {
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		return Deny, ctx.Err()
	}
	a, err := New(ctx, gate)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(path), a.Name()))
	if err != nil {
		t.Fatal(err)
	}
	queryDone := make(chan error, 1)
	go func() { var v string; queryDone <- db.QueryRow("SELECT marker FROM fixture").Scan(&v) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("registration gate was not entered")
	}
	cancel()
	select {
	case err := <-queryDone:
		if err == nil {
			t.Fatal("cancelled open unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SQLite open did not stop after cancellation")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := readMarker(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Fatalf("cancelled registration changed fixture: %q", got)
	}

	// A malformed/missing object token and a deliberately mismatched registered
	// identity both fail at xWrite before the delegated native method is called.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	b, err := New(ctx2, func(context.Context, Registration) (Decision, error) { return Allow, nil })
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(path), b.Name()))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	var value string
	if err := conn.QueryRowContext(ctx2, "SELECT marker FROM fixture").Scan(&value); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	var mainFile *fileState
	for _, f := range b.files {
		if f.role == MainDB {
			mainFile = f
			break
		}
	}
	b.mu.Unlock()
	if mainFile == nil {
		t.Fatal("main database registration missing")
	}
	mainFile.dev++
	buf := libc.Xmalloc(b.tls, 1)
	defer libc.Xfree(b.tls, buf)
	if rc := fileWrite(b.tls, mainFile.wrapper, buf, 1, 0); rc != sqliteIOERRWrite {
		t.Fatalf("mismatched identity write rc=%d", rc)
	}
	var missing wrappedFile
	missing.state = ^uintptr(0)
	if rc := fileWrite(b.tls, uintptr(unsafe.Pointer(&missing)), buf, 1, 0); rc != sqliteIOERRWrite {
		t.Fatalf("missing token write rc=%d", rc)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(ctx2); err != nil {
		t.Fatal(err)
	}
	got, err = readMarker(ctx2, path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Fatalf("identity-refused callbacks changed fixture: %q", got)
	}
}

const sqliteIOERRWrite int32 = 778

func TestUnsupportedRolesAndPathDeleteFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.db")
	if err := prepareWALDB(path); err != nil {
		t.Fatal(err)
	}
	shmPath := filepath.Join(dir, "shm-callback.db")
	if err := preparePersistentDB(shmPath); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, func(context.Context, Registration) (Decision, error) { return Allow, nil })
	if err != nil {
		t.Fatal(err)
	}
	shmDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(shmPath), a.Name()))
	if err != nil {
		t.Fatal(err)
	}
	var fixtureMarker string
	if err := shmDB.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&fixtureMarker); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	var openMain uintptr
	for _, f := range a.files {
		if f.role == MainDB {
			openMain = f.wrapper
			break
		}
	}
	a.mu.Unlock()
	shmRC, shmCleared, shmErr := invokeShmMap(a, openMain)
	if shmErr != nil || shmRC != sqliteIOERRShmMap || !shmCleared {
		t.Fatalf("installed xShmMap refusal rc=%d cleared=%t err=%v", shmRC, shmCleared, shmErr)
	}
	if err := shmDB.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(path), a.Name()))
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	err = db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
	if err == nil && strings.EqualFold(mode, "wal") {
		t.Fatalf("WAL unexpectedly accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("WAL refusal left/created SHM file, stat err=%v", err)
	}
	actualBarrierReached := false
	for _, e := range a.Events() {
		if e.Kind == "wal_refused" || e.Kind == "shm_map_refused" {
			actualBarrierReached = true
		}
	}
	seenRefusal := false
	// Exercise SQLite's actual WAL negotiation, then separately invoke the WAL
	// role callback to retain a focused pre-native-xOpen regression.
	tls := libc.NewTLS()
	defer tls.Close()
	z, _ := libc.CString(path)
	defer libc.Xfree(tls, z)
	fileMem := libc.Xmalloc(tls, libc.Tsize_t((*lib.Tsqlite3_vfs)(ptrFromUintptr(a.vfs)).FszOsFile))
	defer libc.Xfree(tls, fileMem)
	rc := vfsOpen(tls, a.vfs, z, fileMem, lib.SQLITE_OPEN_WAL, 0)
	if rc != lib.SQLITE_CANTOPEN {
		t.Fatalf("WAL xOpen refusal rc=%d", rc)
	}
	if rc := vfsOpen(tls, a.vfs, z, fileMem, lib.SQLITE_OPEN_MAIN_DB|lib.SQLITE_OPEN_TEMP_DB, 0); rc != lib.SQLITE_CANTOPEN {
		t.Fatalf("mixed-role xOpen refusal rc=%d", rc)
	}
	deleteOnClosePath := filepath.Join(dir, "delete-on-close.db")
	zDeleteOnClose, _ := libc.CString(deleteOnClosePath)
	defer libc.Xfree(tls, zDeleteOnClose)
	if rc := vfsOpen(tls, a.vfs, zDeleteOnClose, fileMem, lib.SQLITE_OPEN_MAIN_DB|lib.SQLITE_OPEN_DELETEONCLOSE, 0); rc != lib.SQLITE_CANTOPEN {
		t.Fatalf("DELETEONCLOSE xOpen refusal rc=%d", rc)
	}
	if _, err := os.Stat(deleteOnClosePath); !os.IsNotExist(err) {
		t.Fatalf("DELETEONCLOSE target reached native open, stat err=%v", err)
	}
	for _, e := range a.Events() {
		if e.Kind == "wal_refused" || e.Kind == "delete_on_close_refused" {
			seenRefusal = true
		}
	}
	if !seenRefusal {
		t.Fatalf("did not observe explicit WAL stop path: %+v", a.Events())
	}
	if !actualBarrierReached {
		t.Fatalf("SQLite WAL negotiation did not reach the adapter refusal callback: %+v", a.Events())
	}
	// Also exercise the SHM callback directly to prove it clears the output mapping.
	var mapped uintptr
	if rc := fileShmMap(nil, 0, 0, 32768, 1, uintptr(unsafe.Pointer(&mapped))); rc != sqliteIOERRShmMap {
		t.Fatalf("SHM map rc=%d", rc)
	}
	if mapped != 0 {
		t.Fatal("refused SHM callback returned a mapping")
	}
	zDelete, _ := libc.CString(path)
	defer libc.Xfree(tls, zDelete)
	if rc := vfsDelete(tls, a.vfs, zDelete, 0); rc != lib.SQLITE_IOERR_DELETE {
		t.Fatalf("path delete rc=%d", rc)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("path-delete refusal removed DB: %v", err)
	}
	if !strings.Contains(a.Name(), "write_barrier") {
		t.Fatalf("unexpected named VFS %q", a.Name())
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOnlineBackupRoutesDestinationThroughNamedAdapter(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := preparePersistentDB(source); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "destination.db")
	if err := preparePersistentDB(destination); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := New(ctx, func(context.Context, Registration) (Decision, error) { return Allow, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runOnlineBackup(ctx, fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(source), a.Name()), fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(destination), a.Name())); err != nil {
		t.Fatalf("online backup: %v; events=%+v", err, a.Events())
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := readMarker(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Fatalf("backup content=%q", got)
	}
	mainWrites := 0
	sourceOpened, destinationOpened := false, false
	for _, e := range a.Events() {
		if e.Kind == "write" && e.Role == MainDB {
			mainWrites++
		}
		if e.Kind == "registered" && e.Role == MainDB && e.Fixture == "source.db" {
			sourceOpened = true
		}
		if e.Kind == "registered" && e.Role == MainDB && e.Fixture == "destination.db" {
			destinationOpened = true
		}
	}
	if mainWrites == 0 {
		t.Fatalf("backup destination was not written through adapter: %+v", a.Events())
	}
	if !sourceOpened || !destinationOpened {
		t.Fatalf("source and destination did not independently select named VFS: %+v", a.Events())
	}
}

func TestOnlineBackupDestinationRefusalAndFreshRetry(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := preparePersistentDB(source); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	knownGood := filepath.Join(dir, "known-good-backup.db")
	if err := preparePersistentDB(knownGood); err != nil {
		t.Fatal(err)
	}
	prior, err := New(ctx, func(context.Context, Registration) (Decision, error) { return Allow, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runOnlineBackup(ctx, fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(source), prior.Name()), fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(knownGood), prior.Name())); err != nil {
		t.Fatalf("initial known-good backup: %v", err)
	}
	if err := prior.Close(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(knownGood)
	if err != nil {
		t.Fatal(err)
	}
	denied := knownGood
	a, err := New(ctx, func(_ context.Context, r Registration) (Decision, error) {
		if r.Role == MainDB && r.Name == "known-good-backup.db" {
			return Deny, nil
		}
		return Allow, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = runOnlineBackup(ctx, fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(source), a.Name()), fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(denied), a.Name()))
	if err == nil {
		t.Fatal("denied backup destination unexpectedly succeeded")
	}
	after, readErr := os.ReadFile(knownGood)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("refused destination changed known-good backup: before=%d after=%d", len(before), len(after))
	}
	for _, e := range a.Events() {
		if e.Kind == "write" {
			t.Fatalf("write occurred after denied destination registration: %+v", e)
		}
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// Preserve the prior control target and retry using a separate fresh object.
	fresh := filepath.Join(dir, "fresh-backup.db")
	if err := preparePersistentDB(fresh); err != nil {
		t.Fatal(err)
	}
	b, err := New(ctx, func(context.Context, Registration) (Decision, error) { return Allow, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runOnlineBackup(ctx, fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(source), b.Name()), fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(fresh), b.Name())); err != nil {
		t.Fatalf("fresh backup retry: %v; events=%+v", err, b.Events())
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := readMarker(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Fatalf("fresh backup marker=%q", got)
	}
}

func runOnlineBackup(ctx context.Context, source, destinationURI string) error {
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("driver connection has no NewBackup")
		}
		b, err := backuper.NewBackup(destinationURI)
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = b.Finish()
			}
		}()
		for {
			done, stepErr := b.Step(-1)
			if stepErr != nil {
				return stepErr
			}
			if !done {
				break
			}
		}
		dst, err := b.Commit()
		finished = true
		if err != nil {
			return err
		}
		return dst.Close()
	})
}

const sqliteIOERRShmMap int32 = 5386

func preparePersistentDB(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Exec("PRAGMA journal_mode=PERSIST"); err != nil {
		return err
	}
	if _, err = db.Exec("CREATE TABLE fixture (marker TEXT NOT NULL)"); err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO fixture VALUES ('before')")
	return err
}
func prepareWALDB(path string) error {
	if err := preparePersistentDB(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	var mode string
	if err = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		_ = db.Close()
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		_ = db.Close()
		return fmt.Errorf("could not prepare WAL fixture: mode=%q", mode)
	}
	return db.Close()
}
func updateViaVFS(ctx context.Context, path, name, value string) error {
	dsn := fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(path), name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=PERSIST").Scan(&mode); err != nil {
		return err
	}
	if mode != "persist" {
		return fmt.Errorf("fixture journal mode is %q, want persist", mode)
	}
	_, err = db.ExecContext(ctx, "UPDATE fixture SET marker = ?", value)
	return err
}
func readMarker(ctx context.Context, path string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var v string
	err = db.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&v)
	return v, err
}
func readViaVFS(ctx context.Context, path, name string) (string, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(path), name))
	if err != nil {
		return "", err
	}
	defer db.Close()
	var got string
	err = db.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&got)
	return got, err
}
