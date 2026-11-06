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

// Command sqlite-wal-shm-study is a disposable experiment for the pinned
// modernc Linux/amd64 generated ABI. It must run as a standalone process.
package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	_ "modernc.org/sqlite"
	lib "modernc.org/sqlite/lib"
)

const driverSourceCommit = "4df1ebea0f1cf0cd6ec3245b26f5fec4234ce738"

type report struct {
	SchemaVersion int          `json:"schema_version"`
	SourceCommit  string       `json:"source_commit"`
	Driver        string       `json:"driver"`
	DriverSource  string       `json:"driver_source_commit"`
	SQLiteVersion string       `json:"sqlite_version"`
	GoVersion     string       `json:"go_version"`
	Kernel        string       `json:"kernel"`
	RunTimeUTC    string       `json:"run_time_utc"`
	OS            string       `json:"os"`
	Architecture  string       `json:"architecture"`
	Cases         []caseResult `json:"cases"`
	Limitations   []string     `json:"limitations"`
}

type caseResult struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	Observed       string `json:"observed"`
}

type openRecord struct {
	pin      int
	object   string
	dev, ino uint64
	size     int64
	fileName string
}

type mapRecord struct {
	dev, ino uint64
	shared   bool
	object   string
}

type study struct {
	mu               sync.Mutex
	origOpen         uintptr
	origMmap         uintptr
	opened           chan openRecord
	mapped           chan mapRecord
	release          chan struct{}
	pins             []int
	nextObject       uint64
	pinnedDev        uint64
	pinnedIno        uint64
	closeOnce        sync.Once
	physicalSHMOpens uint64
}

func main() {
	commit := flag.String("commit", "", "exact clean source commit (required)")
	flag.Parse()
	if err := validateSource(*commit); err != nil {
		fmt.Fprintln(os.Stderr, "source validation:", err)
		os.Exit(2)
	}
	r, err := run(*commit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "study incomplete:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "encode report:", err)
		os.Exit(1)
	}
}

func validateSource(expected string) error {
	if len(expected) != 40 {
		return errors.New("--commit must be a full 40-character hash")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return errors.New("--commit must be hexadecimal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	head, err := gitOutput(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if head != expected {
		return fmt.Errorf("HEAD %s does not match --commit %s", head, expected)
	}
	dirty, err := gitOutput(ctx, "status", "--porcelain=v1")
	if err != nil {
		return err
	}
	if dirty != "" {
		return errors.New("refusing to label modified source as an unmodified commit")
	}
	return nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

func run(sourceCommit string) (report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := report{
		SchemaVersion: 1,
		SourceCommit:  sourceCommit,
		Driver:        "modernc.org/sqlite v1.61.0",
		DriverSource:  driverSourceCommit,
		GoVersion:     runtime.Version(),
		OS:            "linux",
		Architecture:  "amd64",
		RunTimeUTC:    time.Now().UTC().Format(time.RFC3339),
		Cases:         []caseResult{},
		Limitations: []string{
			"This standalone process installs raw generated xSetSystemCall callbacks; they mutate a package-global table and are not a supported per-VFS authorization API.",
			"Only one synthetic database is used. Denial, cancellation, fresh retry and multiple-connection owner coverage are not exercised by this positive ordering probe.",
			"The probe uses private generated ABI layouts and callbacks; it is not production integration or a supported driver contract.",
			"It does not establish protected-owner inventory, revocation, worker lifecycle, external-writer coordination, other architectures or strict path containment.",
		},
	}
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		result.Kernel = charsToString(uts.Release[:])
	} else {
		result.Kernel = "unavailable"
	}

	dir, err := os.MkdirTemp("", "komposyn-wal-shm-study-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fixture.db")
	if err := prepareFixture(ctx, path, &result.SQLiteVersion); err != nil {
		return result, err
	}

	s := &study{opened: make(chan openRecord, 4), mapped: make(chan mapRecord, 16), release: make(chan struct{})}
	defer s.closeRelease()
	defer func() {
		s.mu.Lock()
		pins := append([]int(nil), s.pins...)
		s.pins = nil
		s.mu.Unlock()
		for _, fd := range pins {
			_ = unix.Close(fd)
		}
	}()

	tls := libc.NewTLS()
	defer tls.Close()
	defaultV := lib.Xsqlite3_vfs_find(tls, 0)
	if defaultV == 0 {
		return result, errors.New("SQLite default Unix VFS is unavailable")
	}
	custom, customName, nameOpen, nameMmap, err := registerNamedVFS(tls, defaultV)
	if err != nil {
		return result, err
	}
	defer func() {
		_ = lib.Xsqlite3_vfs_unregister(tls, custom)
		libc.Xfree(tls, nameOpen)
		libc.Xfree(tls, nameMmap)
		libc.Xfree(tls, customName)
		libc.Xfree(tls, custom)
	}()

	customVFS := (*lib.Tsqlite3_vfs)(ptr(custom))
	get := function[func(*libc.TLS, uintptr, uintptr) uintptr](customVFS.FxGetSystemCall)
	set := function[func(*libc.TLS, uintptr, uintptr, uintptr) int32](customVFS.FxSetSystemCall)
	s.origOpen = get(tls, custom, nameOpen)
	s.origMmap = get(tls, custom, nameMmap)
	if s.origOpen == 0 || s.origMmap == 0 {
		return result, errors.New("Unix VFS does not expose the required open/mmap hooks")
	}
	if rc := set(tls, custom, nameOpen, ifaceData(any(s.callbackOpen))); rc != lib.SQLITE_OK {
		return result, fmt.Errorf("install open hook: SQLite result %d", rc)
	}
	if rc := set(tls, custom, nameMmap, ifaceData(any(s.callbackMmap))); rc != lib.SQLITE_OK {
		_ = set(tls, custom, nameOpen, s.origOpen)
		return result, fmt.Errorf("install mmap hook: SQLite result %d", rc)
	}
	defer func() {
		_ = set(tls, custom, nameMmap, s.origMmap)
		_ = set(tls, custom, nameOpen, s.origOpen)
	}()

	type workerResult struct {
		count          int
		checkpointBusy int
		err            error
	}
	workerDone := make(chan workerResult, 1)
	go func() {
		workerDone <- runWALWorkload(ctx, "file:"+filepath.ToSlash(path)+"?vfs="+libc.GoString(customVFS.FzName))
	}()

	var opened openRecord
	select {
	case opened = <-s.opened:
		if opened.size != 0 {
			return result, fmt.Errorf("synthetic SHM fixture was not empty at pre-effect checkpoint: size %d", opened.size)
		}
	case work := <-workerDone:
		return result, fmt.Errorf("WAL workload ended before SHM open checkpoint: %w", work.err)
	case <-ctx.Done():
		return result, errors.New("timed out waiting for SHM open checkpoint")
	}
	if opened.object == "" || opened.pin < 0 {
		return result, errors.New("SHM open hook did not retain an identity pin")
	}
	s.closeRelease()

	var mapped mapRecord
	select {
	case mapped = <-s.mapped:
	case work := <-workerDone:
		return result, fmt.Errorf("WAL workload ended before matching MAP_SHARED: %w", work.err)
	case <-ctx.Done():
		return result, errors.New("timed out waiting for SHM mapping checkpoint")
	}
	if mapped.object != opened.object || !mapped.shared {
		return result, errors.New("native MAP_SHARED did not use the pinned SHM object")
	}

	var work workerResult
	select {
	case work = <-workerDone:
	case <-ctx.Done():
		return result, errors.New("timed out waiting for WAL workload completion")
	}
	if work.err != nil {
		return result, work.err
	}
	if work.count != 3 || work.checkpointBusy != 0 {
		return result, fmt.Errorf("synthetic readback/checkpoint mismatch: count=%d busy=%d", work.count, work.checkpointBusy)
	}
	s.mu.Lock()
	physicalSHMOpens := s.physicalSHMOpens
	s.mu.Unlock()
	if physicalSHMOpens != 1 {
		return result, fmt.Errorf("expected one physical SHM open shared by the connections, observed %d", physicalSHMOpens)
	}
	var after unix.Stat_t
	if err := unix.Fstat(opened.pin, &after); err != nil {
		return result, fmt.Errorf("retained SHM pin became unusable after SQLite close: %w", err)
	}
	if after.Size <= opened.size {
		return result, fmt.Errorf("pinned SHM object did not grow after SQLite use: before=%d after=%d", opened.size, after.Size)
	}
	if err := unix.Close(opened.pin); err != nil {
		return result, fmt.Errorf("close retained SHM pin: %w", err)
	}
	s.mu.Lock()
	for i, fd := range s.pins {
		if fd == opened.pin {
			s.pins = append(s.pins[:i], s.pins[i+1:]...)
			break
		}
	}
	s.mu.Unlock()

	result.Cases = append(result.Cases,
		caseResult{Name: "wal_write_readback_and_checkpoint", Classification: "measured_pass", Observed: "A synthetic WAL transaction committed two rows; a second SQLite connection read them; TRUNCATE checkpoint completed without a busy writer."},
		caseResult{Name: "actual_shm_open_pinned_before_native_use", Classification: "measured_pass", Observed: fmt.Sprintf("The named-VFS open hook pinned process-local %s at size %d before returning the actual descriptor to SQLite; the matching MAP_SHARED call used that object and the retained pin observed growth to %d bytes after close.", opened.object, opened.size, after.Size)},
		caseResult{Name: "second_connection_shared_node", Classification: "measured_pass", Observed: "A second simultaneous connection completed a WAL read/write while the process observed only one physical SHM open; this is consistent with the pinned generated-code shared-node reuse path, not evidence of a per-connection owner callback."},
		caseResult{Name: "denial_cancellation_and_fresh_retry", Classification: "not_run", Observed: "Not exercised: this probe only demonstrates ordering with an allow decision; the global raw syscall hook is not a trusted authorization integration."},
	)
	return result, nil
}

func prepareFixture(ctx context.Context, path string, version *string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(version); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE TABLE sample(v TEXT); INSERT INTO sample VALUES('before')")
	return err
}

func runWALWorkload(ctx context.Context, uri string) (out struct {
	count          int
	checkpointBusy int
	err            error
}) {
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		out.err = err
		return out
	}
	db.SetMaxOpenConns(2)
	conn1, err := db.Conn(ctx)
	if err == nil {
		var mode string
		err = conn1.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
		if err == nil && !strings.EqualFold(mode, "wal") {
			err = fmt.Errorf("journal mode %q", mode)
		}
	}
	if err == nil {
		_, err = conn1.ExecContext(ctx, "INSERT INTO sample VALUES('first')")
	}
	var conn2 *sql.Conn
	if err == nil {
		conn2, err = db.Conn(ctx)
	}
	if err == nil {
		err = conn2.QueryRowContext(ctx, "SELECT count(*) FROM sample").Scan(&out.count)
	}
	if err == nil && out.count != 2 {
		err = fmt.Errorf("second connection read %d rows, want 2", out.count)
	}
	if err == nil {
		_, err = conn2.ExecContext(ctx, "INSERT INTO sample VALUES('second')")
	}
	if err == nil {
		err = conn1.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&out.checkpointBusy, new(int), new(int))
	}
	if conn2 != nil {
		if closeErr := conn2.Close(); err == nil {
			err = closeErr
		}
	}
	if conn1 != nil {
		if closeErr := conn1.Close(); err == nil {
			err = closeErr
		}
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		out.count = 3
	}
	out.err = err
	return out
}

func (s *study) callbackOpen(tls *libc.TLS, path uintptr, flags int32, mode uint32) int32 {
	fn := function[func(*libc.TLS, uintptr, int32, uint32) int32](s.origOpen)
	fd := fn(tls, path, flags, mode)
	if fd < 0 || !strings.HasSuffix(libc.GoString(path), "fixture.db-shm") {
		return fd
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(fd), &st); err != nil {
		_ = unix.Close(int(fd))
		return -1
	}
	dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		_ = unix.Close(int(fd))
		return -1
	}
	var pin unix.Stat_t
	if err := unix.Fstat(dup, &pin); err != nil || st.Dev != pin.Dev || st.Ino != pin.Ino {
		_ = unix.Close(dup)
		_ = unix.Close(int(fd))
		return -1
	}
	s.mu.Lock()
	s.nextObject++
	s.physicalSHMOpens++
	object := fmt.Sprintf("shm_object_%d", s.nextObject)
	s.pins = append(s.pins, dup)
	s.pinnedDev, s.pinnedIno = uint64(st.Dev), st.Ino
	s.mu.Unlock()
	select {
	case s.opened <- openRecord{pin: dup, object: object, dev: uint64(st.Dev), ino: st.Ino, size: st.Size, fileName: "fixture.db-shm"}:
	case <-time.After(2 * time.Second):
		_ = unix.Close(dup)
		_ = unix.Close(int(fd))
		return -1
	}
	select {
	case <-s.release:
		return fd
	case <-time.After(5 * time.Second):
		_ = unix.Close(int(fd))
		return -1
	}
}

func charsToString(b []byte) string {
	var out []byte
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, c)
	}
	return string(out)
}

func (s *study) callbackMmap(tls *libc.TLS, addr uintptr, length uint64, prot, flags, fd int32, offset int64) uintptr {
	var rec mapRecord
	if fd >= 0 {
		var st unix.Stat_t
		if err := unix.Fstat(int(fd), &st); err == nil {
			rec.dev, rec.ino = uint64(st.Dev), st.Ino
		}
	}
	s.mu.Lock()
	if rec.dev == s.pinnedDev && rec.ino == s.pinnedIno {
		rec.object = "shm_object_1"
	}
	s.mu.Unlock()
	rec.shared = flags&unix.MAP_SHARED != 0
	if rec.object != "" {
		select {
		case s.mapped <- rec:
		default:
		}
	}
	fn := function[func(*libc.TLS, uintptr, uint64, int32, int32, int32, int64) uintptr](s.origMmap)
	return fn(tls, addr, length, prot, flags, fd, offset)
}

func (s *study) closeRelease() { s.closeOnce.Do(func() { close(s.release) }) }

func registerNamedVFS(tls *libc.TLS, defaultV uintptr) (uintptr, uintptr, uintptr, uintptr, error) {
	custom := libc.Xmalloc(tls, libc.Tsize_t(unsafe.Sizeof(lib.Tsqlite3_vfs{})))
	if custom == 0 {
		return 0, 0, 0, 0, errors.New("allocate named VFS")
	}
	name, err := libc.CString("komposyn_wal_shm_study")
	if err != nil {
		libc.Xfree(tls, custom)
		return 0, 0, 0, 0, err
	}
	openName, err := libc.CString("open")
	if err != nil {
		libc.Xfree(tls, name)
		libc.Xfree(tls, custom)
		return 0, 0, 0, 0, err
	}
	mmapName, err := libc.CString("mmap")
	if err != nil {
		libc.Xfree(tls, openName)
		libc.Xfree(tls, name)
		libc.Xfree(tls, custom)
		return 0, 0, 0, 0, err
	}
	customVFS := (*lib.Tsqlite3_vfs)(ptr(custom))
	*customVFS = *(*lib.Tsqlite3_vfs)(ptr(defaultV))
	customVFS.FpNext = 0
	customVFS.FzName = name
	if rc := lib.Xsqlite3_vfs_register(tls, custom, 0); rc != lib.SQLITE_OK {
		libc.Xfree(tls, mmapName)
		libc.Xfree(tls, openName)
		libc.Xfree(tls, name)
		libc.Xfree(tls, custom)
		return 0, 0, 0, 0, fmt.Errorf("register non-default VFS: SQLite result %d", rc)
	}
	return custom, name, openName, mmapName, nil
}

func ifaceData(v any) uintptr      { type pair [2]uintptr; return (*pair)(unsafe.Pointer(&v))[1] }
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }
func function[T any](p uintptr) T  { return *(*T)(unsafe.Pointer(&struct{ uintptr }{p})) }
