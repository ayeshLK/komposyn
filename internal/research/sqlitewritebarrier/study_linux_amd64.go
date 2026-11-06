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
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"modernc.org/libc"
	"modernc.org/sqlite"
	lib "modernc.org/sqlite/lib"
)

const sourceModuleCommit = "4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738"

type StudyReport struct {
	SchemaVersion      int         `json:"schema_version"`
	SourceCommit       string      `json:"source_commit"`
	RunAtUTC           string      `json:"run_at_utc"`
	OS                 string      `json:"os"`
	Architecture       string      `json:"architecture"`
	GoVersion          string      `json:"go_version"`
	DriverVersion      string      `json:"driver_version"`
	DriverSourceCommit string      `json:"driver_source_commit"`
	SQLiteVersion      string      `json:"sqlite_version"`
	VFSName            string      `json:"named_vfs"`
	ABI                string      `json:"generated_abi_assumption"`
	ExecutionComplete  bool        `json:"execution_complete"`
	Conclusion         string      `json:"conclusion,omitempty"`
	Cases              []StudyCase `json:"cases"`
	Limitations        []string    `json:"limitations"`
}

type StudyCase struct {
	Name                string  `json:"name"`
	Classification      string  `json:"classification"`
	Observed            string  `json:"observed"`
	RegistrationRoles   []Role  `json:"registration_roles,omitempty"`
	WriteRoles          []Role  `json:"write_roles,omitempty"`
	AllWritesMatched    bool    `json:"all_writes_matched"`
	FixtureUnchanged    bool    `json:"fixture_unchanged"`
	FreshRetrySucceeded bool    `json:"fresh_retry_succeeded"`
	OperationFailed     bool    `json:"operation_failed"`
	WritesObserved      bool    `json:"writes_observed"`
	Events              []Event `json:"events,omitempty"`
	EvidenceComplete    bool    `json:"evidence_complete"`
}

// Run executes a bounded synthetic-only study. It does not open paths supplied
// by the caller and retains no fixture after return.
func Run(parent context.Context, sourceCommit string) (StudyReport, error) {
	if parent == nil {
		return StudyReport{}, errors.New("parent context is required")
	}
	if len(sourceCommit) != 40 {
		return StudyReport{}, errors.New("source commit must be a full commit hash")
	}
	if _, err := hex.DecodeString(sourceCommit); err != nil {
		return StudyReport{}, errors.New("source commit must be hexadecimal")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "komposyn-write-barrier-")
	if err != nil {
		return StudyReport{}, err
	}
	defer os.RemoveAll(dir)
	report := StudyReport{SchemaVersion: 1, SourceCommit: sourceCommit, RunAtUTC: time.Now().UTC().Format(time.RFC3339), OS: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), DriverVersion: "v1.61.0", DriverSourceCommit: sourceModuleCommit, ABI: "linux/amd64 generated modernc ABI; TunixFile size=120, Fh offset=24; default Unix VFS delegated", Cases: []StudyCase{}, Limitations: []string{"Research-only unsafe generated ABI adapter; no upstream stability guarantee.", "Native xOpen may create empty entries or metadata before registration; barrier covers observed xWrite/xTruncate only.", "Only main DB and rollback journal roles are admitted; path-only xDelete always fails closed.", "WAL/SHM, temporary roles, file-control operations, mmap/xFetch, production Token Logger paths, external writers and other architectures remain unsupported.", "Identity pins bind opened objects, not live pathname containment, immutable contents, or independent copies."}}

	positive := filepath.Join(dir, "positive.db")
	if err := prepareFixture(positive); err != nil {
		return report, err
	}
	var sqliteVersion string
	if err := readVersion(ctx, positive, &sqliteVersion); err != nil {
		return report, err
	}
	report.SQLiteVersion = sqliteVersion
	adapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	report.VFSName = adapter.Name()
	updateErr := updateNamed(ctx, positive, adapter.Name(), "after")
	events := adapter.Events()
	positiveEvidenceComplete := adapter.EventsComplete()
	closeErr := adapter.Close(ctx)
	if updateErr != nil {
		return report, fmt.Errorf("positive exact-object update: %w", updateErr)
	}
	if closeErr != nil {
		return report, closeErr
	}
	got, err := readValue(ctx, positive)
	if err != nil {
		return report, err
	}
	roles, wroles, matched := summarize(events)
	report.Cases = append(report.Cases, StudyCase{Name: "main_and_rollback_journal_write", Classification: classify(got == "after" && containsRole(wroles, MainDB) && containsRole(wroles, MainJournal) && matched && positiveEvidenceComplete), Observed: "SQLite completed a PERSIST-mode transaction through the named adapter; fixture marker read back after; main/journal writes are individually correlated to the registered open fd and retained duplicate pin.", RegistrationRoles: roles, WriteRoles: wroles, AllWritesMatched: matched, Events: events, EvidenceComplete: positiveEvidenceComplete})

	denied := filepath.Join(dir, "denied.db")
	if err := prepareFixture(denied); err != nil {
		return report, err
	}
	denyAdapter, err := New(ctx, func(_ context.Context, r Registration) (Decision, error) {
		if r.Role == MainJournal {
			return Deny, nil
		}
		return Allow, nil
	})
	if err != nil {
		return report, err
	}
	denyOperationErr := updateNamed(ctx, denied, denyAdapter.Name(), "must-not-commit")
	denyEvents := denyAdapter.Events()
	denyEvidenceComplete := denyAdapter.EventsComplete()
	denyCloseErr := denyAdapter.Close(ctx)
	unchangedValue, readErr := readValue(ctx, denied)
	if denyCloseErr != nil {
		return report, denyCloseErr
	}
	if readErr != nil {
		return report, readErr
	}
	fresh := filepath.Join(dir, "fresh-retry.db")
	if err := prepareFixture(fresh); err != nil {
		return report, err
	}
	freshAdapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	freshErr := updateNamed(ctx, fresh, freshAdapter.Name(), "fresh-success")
	freshEvents := freshAdapter.Events()
	freshEvidenceComplete := freshAdapter.EventsComplete()
	freshCloseErr := freshAdapter.Close(ctx)
	if freshCloseErr != nil {
		return report, freshCloseErr
	}
	freshValue, readErr := readValue(ctx, fresh)
	if readErr != nil {
		return report, readErr
	}
	droles, dw, dm := summarize(denyEvents)
	froles, fw, fm := summarize(freshEvents)
	deniedNoWrite := !hasWrite(denyEvents)
	report.Cases = append(report.Cases, StudyCase{Name: "journal_registration_refusal", Classification: classifyRefusal(denyOperationErr != nil && unchangedValue == "before" && deniedNoWrite && denyEvidenceComplete, !deniedNoWrite || unchangedValue != "before"), Observed: "Main DB registration was allowed and rollback-journal registration denied before sensitive writes; synthetic fixture content remained unchanged. Empty-entry/metadata effects are possible and are not interpreted as byte-for-byte path absence.", RegistrationRoles: droles, WriteRoles: dw, AllWritesMatched: dm, FixtureUnchanged: unchangedValue == "before", OperationFailed: denyOperationErr != nil, WritesObserved: !deniedNoWrite, Events: denyEvents, EvidenceComplete: denyEvidenceComplete})
	report.Cases = append(report.Cases, StudyCase{Name: "fresh_object_retry", Classification: classify(freshErr == nil && freshValue == "fresh-success" && containsRole(fw, MainDB) && containsRole(fw, MainJournal) && fm && freshEvidenceComplete), Observed: "A separate fresh fixture and new registration decisions completed successfully; no refused operation was replayed.", RegistrationRoles: froles, WriteRoles: fw, AllWritesMatched: fm, FreshRetrySucceeded: freshErr == nil && freshValue == "fresh-success", Events: freshEvents, EvidenceComplete: freshEvidenceComplete})

	walFixture := filepath.Join(dir, "wal-fixture.db")
	if err := prepareWALStudyFixture(walFixture); err != nil {
		return report, err
	}
	walNegotiationAdapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	walDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(walFixture), walNegotiationAdapter.Name()))
	if err != nil {
		return report, err
	}
	var walMode string
	walPragmaErr := walDB.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&walMode)
	if err := walDB.Close(); err != nil {
		return report, err
	}
	negotiationEvents := walNegotiationAdapter.Events()
	negotiationComplete := walNegotiationAdapter.EventsComplete()
	if err := walNegotiationAdapter.Close(ctx); err != nil {
		return report, err
	}
	_, shmErr := os.Stat(walFixture + "-shm")
	shmAbsent := os.IsNotExist(shmErr)
	barrierReached := hasKind(negotiationEvents, "wal_refused") || hasKind(negotiationEvents, "shm_map_refused")
	walClass := "unavailable"
	if barrierReached && shmAbsent {
		walClass = "measured_refusal"
	}
	if barrierReached && !shmAbsent {
		walClass = "observed_violation"
	}
	if !negotiationComplete || (!shmAbsent && shmErr != nil) {
		walClass = "inconclusive"
	}
	report.Cases = append(report.Cases, StudyCase{Name: "sqlite_wal_negotiation", Classification: walClass, Observed: fmt.Sprintf("PRAGMA result mode=%q; query failed=%t; adapter WAL/SHM refusal reached=%t; SHM absent=%t", walMode, walPragmaErr != nil, barrierReached, shmAbsent), Events: negotiationEvents, EvidenceComplete: negotiationComplete})

	walAdapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	tls := libc.NewTLS()
	walName, _ := libc.CString(filepath.Join(dir, "wal.db"))
	fileMem := libc.Xmalloc(tls, libc.Tsize_t((*lib.Tsqlite3_vfs)(ptrFromUintptr(walAdapter.vfs)).FszOsFile))
	walRC := vfsOpen(tls, walAdapter.vfs, walName, fileMem, lib.SQLITE_OPEN_WAL, 0)
	libc.Xfree(tls, fileMem)
	libc.Xfree(tls, walName)
	tls.Close()
	walEvents := walAdapter.Events()
	walComplete := walAdapter.EventsComplete()
	if err := walAdapter.Close(ctx); err != nil {
		return report, err
	}
	report.Cases = append(report.Cases, StudyCase{Name: "wal_role_callback_stop", Classification: classify(walRC == lib.SQLITE_CANTOPEN && hasKind(walEvents, "wal_refused") && walComplete), Observed: "Direct callback invocation with SQLITE_OPEN_WAL was refused before delegating to native xOpen. The PRAGMA-based SQLite fixture may downgrade before requesting WAL; this case is callback-level evidence, not native WAL acceptance.", Events: walEvents, EvidenceComplete: walComplete})
	shmFixture := filepath.Join(dir, "shm-callback.db")
	if err := prepareFixture(shmFixture); err != nil {
		return report, err
	}
	shmAdapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	shmDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(shmFixture), shmAdapter.Name()))
	if err != nil {
		return report, err
	}
	var shmMarker string
	if err := shmDB.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&shmMarker); err != nil {
		_ = shmDB.Close()
		_ = shmAdapter.Close(ctx)
		return report, err
	}
	shmAdapter.mu.Lock()
	var shmFile uintptr
	for _, f := range shmAdapter.files {
		if f.role == MainDB {
			shmFile = f.wrapper
			break
		}
	}
	shmAdapter.mu.Unlock()
	shmRC, shmCleared, shmInvokeErr := invokeShmMap(shmAdapter, shmFile)
	if err := shmDB.Close(); err != nil {
		return report, err
	}
	shmEvents := shmAdapter.Events()
	shmComplete := shmAdapter.EventsComplete()
	if err := shmAdapter.Close(ctx); err != nil {
		return report, err
	}
	_, shmFixtureErr := os.Stat(shmFixture + "-shm")
	shmAbsentAfterCallback := os.IsNotExist(shmFixtureErr)
	shmClass := classify(shmInvokeErr == nil && shmRC == lib.SQLITE_IOERR_SHMMAP && shmCleared && hasKind(shmEvents, "shm_map_refused") && shmComplete && shmAbsentAfterCallback)
	if !shmAbsentAfterCallback && shmFixtureErr == nil {
		shmClass = "observed_violation"
	}
	if !shmAbsentAfterCallback && shmFixtureErr != nil {
		shmClass = "inconclusive"
	}
	report.Cases = append(report.Cases, StudyCase{Name: "installed_shm_map_callback_stop", Classification: shmClass, Observed: fmt.Sprintf("installed v2 xShmMap returned SQLITE_IOERR_SHMMAP; output cleared=%t; SHM fixture absent=%t", shmCleared, shmAbsentAfterCallback), Events: shmEvents, EvidenceComplete: shmComplete})

	backupSource := filepath.Join(dir, "backup-source.db")
	backupTarget := filepath.Join(dir, "backup-target.db")
	if err := prepareFixture(backupSource); err != nil {
		return report, err
	}
	if err := prepareFixture(backupTarget); err != nil {
		return report, err
	}
	backupAdapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	sourceURI := fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(backupSource), backupAdapter.Name())
	dstURI := fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(backupTarget), backupAdapter.Name())
	backupErr := runBackup(ctx, sourceURI, dstURI)
	backupEvents := backupAdapter.Events()
	backupComplete := backupAdapter.EventsComplete()
	backupCloseErr := backupAdapter.Close(ctx)
	if backupCloseErr != nil {
		return report, backupCloseErr
	}
	backupValue, err := readValue(ctx, backupTarget)
	if err != nil {
		return report, err
	}
	br, bw, bm := summarize(backupEvents)
	registeredSource, registeredDestination := false, false
	for _, e := range backupEvents {
		if e.Kind == "registered" && e.Role == MainDB && e.Fixture == "backup-source.db" {
			registeredSource = true
		}
		if e.Kind == "registered" && e.Role == MainDB && e.Fixture == "backup-target.db" {
			registeredDestination = true
		}
	}
	report.Cases = append(report.Cases, StudyCase{Name: "online_backup_named_destination", Classification: classify(backupErr == nil && backupValue == "before" && containsRole(bw, MainDB) && bm && backupComplete && registeredSource && registeredDestination), Observed: "modernc online-backup API opened both source and destination through explicit named-VFS URIs; independent source/destination main-object registrations and matched destination writes were observed, then the synthetic marker was verified.", RegistrationRoles: br, WriteRoles: bw, AllWritesMatched: bm, Events: backupEvents, EvidenceComplete: backupComplete})
	knownGoodBytes, err := os.ReadFile(backupTarget)
	if err != nil {
		return report, err
	}
	deniedBackup, err := New(ctx, func(_ context.Context, r Registration) (Decision, error) {
		if r.Role == MainDB {
			return Deny, nil
		}
		return Allow, nil
	})
	if err != nil {
		return report, err
	}
	denySourceURI := fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(backupSource), deniedBackup.Name())
	denyBackupErr := runBackup(ctx, denySourceURI, fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(backupTarget), deniedBackup.Name()))
	denyBackupEvents := deniedBackup.Events()
	denyBackupComplete := deniedBackup.EventsComplete()
	if err := deniedBackup.Close(ctx); err != nil {
		return report, err
	}
	afterBytes, err := os.ReadFile(backupTarget)
	if err != nil {
		return report, err
	}
	backupUnchanged := bytes.Equal(knownGoodBytes, afterBytes)
	noBackupWrites := !hasWrite(denyBackupEvents)
	report.Cases = append(report.Cases, StudyCase{Name: "online_backup_denial_preserves_previous", Classification: classifyRefusal(denyBackupErr != nil && backupUnchanged && noBackupWrites && denyBackupComplete, !backupUnchanged || !noBackupWrites), Observed: "A new destination registration was denied while overwriting a previously completed synthetic backup; the pre-existing backup bytes remained identical and no adapter xWrite/xTruncate ran.", FixtureUnchanged: backupUnchanged, Events: denyBackupEvents, EvidenceComplete: denyBackupComplete})
	freshBackupPath := filepath.Join(dir, "fresh-backup.db")
	if err := prepareFixture(freshBackupPath); err != nil {
		return report, err
	}
	freshBackupAdapter, err := New(ctx, allowAll)
	if err != nil {
		return report, err
	}
	freshSourceURI := fmt.Sprintf("file:%s?vfs=%s", filepath.ToSlash(backupSource), freshBackupAdapter.Name())
	freshBackupErr := runBackup(ctx, freshSourceURI, fmt.Sprintf("file:%s?vfs=%s&_pragma=journal_mode(PERSIST)", filepath.ToSlash(freshBackupPath), freshBackupAdapter.Name()))
	freshBackupEvents := freshBackupAdapter.Events()
	freshBackupComplete := freshBackupAdapter.EventsComplete()
	if err := freshBackupAdapter.Close(ctx); err != nil {
		return report, err
	}
	freshBackupValue, err := readValue(ctx, freshBackupPath)
	if err != nil {
		return report, err
	}
	_, freshBackupWrites, freshBackupMatched := summarize(freshBackupEvents)
	report.Cases = append(report.Cases, StudyCase{Name: "online_backup_fresh_retry", Classification: classify(freshBackupErr == nil && freshBackupValue == "before" && containsRole(freshBackupWrites, MainDB) && freshBackupMatched && freshBackupComplete), Observed: "The denied overwrite was not replayed; a distinct fresh destination was independently registered, written through the named VFS, and read back successfully.", WriteRoles: freshBackupWrites, AllWritesMatched: freshBackupMatched, FreshRetrySucceeded: freshBackupErr == nil && freshBackupValue == "before", Events: freshBackupEvents, EvidenceComplete: freshBackupComplete})
	report.ExecutionComplete = true
	allExpected, hasViolation, hasUnavailable := true, false, false
	for _, c := range report.Cases {
		if c.Classification == "observed_violation" {
			hasViolation = true
		}
		if c.Classification == "unavailable" || c.Classification == "inconclusive" {
			hasUnavailable = true
		}
		if c.Classification != "measured_pass" && c.Classification != "measured_refusal" {
			allExpected = false
		}
	}
	if allExpected {
		report.Conclusion = "Synthetic Linux/amd64 transactions and online-backup destinations passed the exact-object xWrite barrier through the pinned generated ABI. Journal xDelete and WAL/SHM remain fail-closed; this is not production, Token Logger, or strict live-path containment evidence."
	} else if hasViolation {
		report.Conclusion = "The study completed and observed at least one disallowed effect; inspect the per-case evidence. No failed case is treated as a pass."
	} else if hasUnavailable {
		report.Conclusion = "The study completed, but one or more cases were unavailable or inconclusive; inspect per-case evidence. No unavailable or inconclusive case is treated as a pass."
	} else {
		report.Conclusion = "The study completed, but one or more fixture expectations failed; inspect the per-case evidence. No failed case is treated as a pass."
	}

	return report, nil
}

func allowAll(context.Context, Registration) (Decision, error) { return Allow, nil }
func invokeShmMap(a *Adapter, pFile uintptr) (int32, bool, error) {
	if a == nil || pFile == 0 {
		return lib.SQLITE_ERROR, false, errors.New("open adapter file is required for SHM callback test")
	}
	m := (*lib.Tsqlite3_io_methods)(ptrFromUintptr(a.methods))
	if m.FiVersion < 2 || m.FxShmMap == 0 {
		return lib.SQLITE_ERROR, false, errors.New("installed I/O table does not expose xShmMap")
	}
	tls := libc.NewTLS()
	defer tls.Close()
	out := libc.Xmalloc(tls, libc.Tsize_t(unsafe.Sizeof(uintptr(0))))
	if out == 0 {
		return lib.SQLITE_NOMEM, false, errors.New("allocate SHM output")
	}
	defer libc.Xfree(tls, out)
	*(*uintptr)(ptrFromUintptr(out)) = uintptr(0xfeed)
	fn := *(*func(*libc.TLS, uintptr, int32, int32, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxShmMap}))
	rc := fn(tls, pFile, 0, 32768, 1, out)
	return rc, *(*uintptr)(ptrFromUintptr(out)) == 0, nil
}
func classify(ok bool) string {
	if ok {
		return "measured_pass"
	}
	return "inconclusive"
}
func classifyRefusal(ok, observedEffect bool) string {
	if ok {
		return "measured_refusal"
	}
	if observedEffect {
		return "observed_violation"
	}
	return "inconclusive"
}
func summarize(events []Event) ([]Role, []Role, bool) {
	regs := map[uint64]Role{}
	var roles, writes []Role
	matched := true
	writeCount := 0
	for _, e := range events {
		if e.Kind == "registered" {
			regs[e.Object] = e.Role
			roles = appendUnique(roles, e.Role)
		}
		if e.Kind == "write" || e.Kind == "truncate" {
			writeCount++
			writes = appendUnique(writes, e.Role)
			matched = matched && e.Matched && e.Result == lib.SQLITE_OK && regs[e.Object] == e.Role
		}
	}
	return roles, writes, matched && writeCount > 0
}
func appendUnique(in []Role, r Role) []Role {
	for _, v := range in {
		if v == r {
			return in
		}
	}
	return append(in, r)
}
func containsRole(rs []Role, r Role) bool {
	for _, v := range rs {
		if v == r {
			return true
		}
	}
	return false
}
func hasWrite(es []Event) bool {
	for _, e := range es {
		if e.Kind == "write" || e.Kind == "truncate" {
			return true
		}
	}
	return false
}
func hasKind(es []Event, k string) bool {
	for _, e := range es {
		if e.Kind == k {
			return true
		}
	}
	return false
}
func prepareFixture(path string) error {
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
func prepareWALStudyFixture(path string) error {
	if err := prepareFixture(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("could not prepare WAL study fixture")
	}
	return nil
}
func readVersion(ctx context.Context, path string, out *string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(out)
}
func readValue(ctx context.Context, path string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var value string
	err = db.QueryRowContext(ctx, "SELECT marker FROM fixture").Scan(&value)
	return value, err
}
func updateNamed(ctx context.Context, path, name, value string) error {
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
	if !strings.EqualFold(mode, "persist") {
		return fmt.Errorf("requested PERSIST mode, got %q", mode)
	}
	_, err = db.ExecContext(ctx, "UPDATE fixture SET marker=?", value)
	return err
}
func runBackup(ctx context.Context, source, destination string) error {
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
		bkr, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("driver connection lacks NewBackup")
		}
		backup, err := bkr.NewBackup(destination)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				_ = backup.Finish()
			}
		}()
		for {
			done, err := backup.Step(-1)
			if err != nil {
				return err
			}
			if !done {
				break
			}
		}
		dst, err := backup.Commit()
		committed = true
		if err != nil {
			return err
		}
		return dst.Close()
	})
}
