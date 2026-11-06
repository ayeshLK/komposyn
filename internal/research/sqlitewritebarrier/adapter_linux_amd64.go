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

// Package sqlitewritebarrier is an explicitly experimental adapter for the
// pinned modernc SQLite ABI. It must never be used by production stores.
package sqlitewritebarrier

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	sqlite "modernc.org/sqlite/lib"
)

// Decision is returned only by the host-owned registration gate.
type Decision uint8

const (
	Deny Decision = iota
	Allow
)

const maxPinnedObjects = 128

// Role identifies a SQLite file role. Only main database and rollback journal
// roles are admitted by this study.
type Role string

const (
	MainDB      Role = "main_db"
	MainJournal Role = "main_journal"
)

// Registration describes the exact object opened by SQLite. Paths are omitted
// from reports; Name is retained only for fixture correlation.
type Registration struct {
	Sequence uint64
	Role     Role
	Name     string
	Flags    int32
	Device   uint64
	Inode    uint64
}

// Gate blocks xOpen until the host explicitly decides. It must honor ctx.
type Gate func(context.Context, Registration) (Decision, error)

// Event is sanitized callback evidence. Object IDs are process-local labels.
type Event struct {
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"`
	Role     Role   `json:"role,omitempty"`
	Fixture  string `json:"fixture,omitempty"`
	Object   uint64 `json:"object,omitempty"`
	Matched  bool   `json:"identity_matched,omitempty"`
	Result   int32  `json:"sqlite_result"`
}

type Adapter struct {
	mu            sync.Mutex
	name          string
	tls           *libc.TLS
	vfs           uintptr
	methods       uintptr
	nameC         uintptr
	defaultV      uintptr
	gate          Gate
	ctx           context.Context
	cancel        context.CancelFunc
	files         map[uintptr]*fileState
	pins          []int
	events        []Event
	eventsDropped bool
	sequence      atomic.Uint64
	closed        bool
	closing       bool
	registry      uintptr
	inflight      int
}

type fileState struct {
	owner   *Adapter
	real    uintptr
	wrapper uintptr
	role    Role
	fixture string
	object  uint64
	dev     uint64
	ino     uint64
	pin     int
	allowed bool
}

// wrappedFile begins with sqlite3_file's pMethods field. The native file lives
// separately and has the exact size required by the default VFS.
type wrappedFile struct {
	methods uintptr
	state   uintptr
}

var (
	globalMu    sync.RWMutex
	global      = map[uintptr]*Adapter{}
	globalFiles = map[uintptr]*fileState{}
	nextID      atomic.Uint64
)

func New(parent context.Context, gate Gate) (*Adapter, error) {
	if parent == nil || gate == nil {
		return nil, errors.New("parent context and explicit registration gate are required")
	}
	if unsafe.Sizeof(sqlite.TunixFile{}) != 120 || unsafe.Offsetof(sqlite.TunixFile{}.Fh) != 24 {
		return nil, errors.New("pinned SQLite unix-file ABI layout mismatch")
	}
	if unsafe.Sizeof(sqlite.Tsqlite3_vfs{}) != 168 || unsafe.Sizeof(sqlite.Tsqlite3_io_methods{}) != 152 {
		return nil, errors.New("pinned SQLite VFS callback structure layout mismatch")
	}
	tls := libc.NewTLS()
	defaultV := sqlite.Xsqlite3_vfs_find(tls, 0)
	if defaultV == 0 {
		tls.Close()
		return nil, errors.New("default SQLite VFS unavailable")
	}
	v := (*sqlite.Tsqlite3_vfs)(ptrFromUintptr(defaultV))
	if v.FiVersion < 1 || v.FszOsFile != int32(unsafe.Sizeof(sqlite.TunixFile{})) || v.FxOpen == 0 || v.FxAccess == 0 || v.FxFullPathname == 0 {
		tls.Close()
		return nil, errors.New("native default VFS capabilities or file layout do not match reviewed linux/amd64 ABI")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	registry := nextID.Add(1)
	a := &Adapter{name: fmt.Sprintf("komposyn_write_barrier_%x", registry), tls: tls, defaultV: defaultV, gate: gate, ctx: ctx, cancel: cancel, files: make(map[uintptr]*fileState), registry: uintptr(registry)}
	var err error
	a.nameC, err = libc.CString(a.name)
	if err != nil {
		cancel()
		tls.Close()
		return nil, err
	}
	a.vfs = libc.Xmalloc(tls, libc.Tsize_t(unsafe.Sizeof(sqlite.Tsqlite3_vfs{})))
	a.methods = libc.Xmalloc(tls, libc.Tsize_t(unsafe.Sizeof(sqlite.Tsqlite3_io_methods{})))
	if a.vfs == 0 || a.methods == 0 {
		a.free()
		return nil, errors.New("allocate VFS structures")
	}
	*(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.vfs)) = *v
	custom := (*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.vfs))
	custom.FpNext, custom.FzName, custom.FpAppData = 0, a.nameC, a.registry
	custom.FszOsFile = v.FszOsFile + int32(unsafe.Sizeof(wrappedFile{}))
	custom.FxOpen = callback(vfsOpen)
	custom.FxDelete = callback(vfsDelete)
	custom.FxAccess = callback(vfsAccess)
	custom.FxFullPathname = callback(vfsFullPathname)
	custom.FxDlOpen, custom.FxDlError, custom.FxDlSym, custom.FxDlClose = 0, callback(vfsDlError), 0, 0
	custom.FxRandomness, custom.FxSleep = callback(vfsRandomness), callback(vfsSleep)
	custom.FxCurrentTime, custom.FxGetLastError, custom.FxCurrentTimeInt64 = callback(vfsCurrentTime), callback(vfsLastError), callback(vfsCurrentTimeInt64)
	*(*sqlite.Tsqlite3_io_methods)(ptrFromUintptr(a.methods)) = sqlite.Tsqlite3_io_methods{FiVersion: 2}
	io := (*sqlite.Tsqlite3_io_methods)(ptrFromUintptr(a.methods))
	io.FxClose, io.FxRead, io.FxWrite, io.FxTruncate = callback(fileClose), callback(fileRead), callback(fileWrite), callback(fileTruncate)
	io.FxSync, io.FxFileSize = callback(fileSync), callback(fileSize)
	io.FxLock, io.FxUnlock, io.FxCheckReservedLock = callback(fileLock), callback(fileUnlock), callback(fileCheckReserved)
	io.FxFileControl, io.FxSectorSize, io.FxDeviceCharacteristics = callback(fileControl), callback(fileSectorSize), callback(fileDeviceCharacteristics)
	io.FxShmMap, io.FxShmLock, io.FxShmBarrier, io.FxShmUnmap = callback(fileShmMap), callback(fileShmLock), callback(fileShmBarrier), callback(fileShmUnmap)
	globalMu.Lock()
	global[a.registry] = a
	globalMu.Unlock()
	if rc := sqlite.Xsqlite3_vfs_register(tls, a.vfs, 0); rc != sqlite.SQLITE_OK {
		globalMu.Lock()
		delete(global, custom.FpAppData)
		globalMu.Unlock()
		a.free()
		return nil, fmt.Errorf("register named VFS: %d", rc)
	}
	return a, nil
}

func callback(fn any) uintptr { type iface [2]uintptr; return (*iface)(unsafe.Pointer(&fn))[1] }

func (a *Adapter) Name() string { return a.name }
func (a *Adapter) Events() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event(nil), a.events...)
}
func (a *Adapter) EventsComplete() bool { a.mu.Lock(); defer a.mu.Unlock(); return !a.eventsDropped }
func (a *Adapter) record(e Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) < 4096 {
		e.Sequence = a.sequence.Add(1)
		a.events = append(a.events, e)
	} else {
		a.eventsDropped = true
	}
}

func adapter(p uintptr) *Adapter { globalMu.RLock(); a := global[p]; globalMu.RUnlock(); return a }
func file(p uintptr) (*fileState, bool) {
	if p == 0 {
		return nil, false
	}
	w := (*wrappedFile)(ptrFromUintptr(p))
	globalMu.RLock()
	f, ok := globalFiles[w.state]
	globalMu.RUnlock()
	return f, ok
}
func objectID() uint64 { return nextID.Add(1) }
func identity(fd int) (uint64, uint64, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Dev), st.Ino, nil
}
func (a *Adapter) free() {
	if a.tls != nil {
		if a.nameC != 0 {
			libc.Xfree(a.tls, a.nameC)
		}
		if a.vfs != 0 {
			libc.Xfree(a.tls, a.vfs)
		}
		if a.methods != 0 {
			libc.Xfree(a.tls, a.methods)
		}
		a.tls.Close()
		a.tls = nil
	}
}

var _ = runtime.KeepAlive

func vfsOpen(tls *libc.TLS, pVfs, zName, pFile uintptr, flags int32, pOut uintptr) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil || zName == 0 {
		return sqlite.SQLITE_CANTOPEN
	}
	a.mu.Lock()
	if a.closed || a.closing {
		a.mu.Unlock()
		return sqlite.SQLITE_CANTOPEN
	}
	a.inflight++
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.inflight--; a.mu.Unlock() }()
	if flags&sqlite.SQLITE_OPEN_DELETEONCLOSE != 0 {
		a.record(Event{Kind: "delete_on_close_refused", Result: sqlite.SQLITE_CANTOPEN})
		return sqlite.SQLITE_CANTOPEN
	}
	role := Role("")
	const roleMask = sqlite.SQLITE_OPEN_MAIN_DB | sqlite.SQLITE_OPEN_TEMP_DB | sqlite.SQLITE_OPEN_TRANSIENT_DB | sqlite.SQLITE_OPEN_MAIN_JOURNAL | sqlite.SQLITE_OPEN_TEMP_JOURNAL | sqlite.SQLITE_OPEN_SUBJOURNAL | sqlite.SQLITE_OPEN_SUPER_JOURNAL | sqlite.SQLITE_OPEN_WAL
	switch roleFlags := flags & roleMask; roleFlags {
	case sqlite.SQLITE_OPEN_WAL:
		a.record(Event{Kind: "wal_refused", Result: sqlite.SQLITE_CANTOPEN})
		return sqlite.SQLITE_CANTOPEN
	case sqlite.SQLITE_OPEN_MAIN_DB:
		role = MainDB
	case sqlite.SQLITE_OPEN_MAIN_JOURNAL:
		role = MainJournal
	default:
		a.record(Event{Kind: "unsupported_role_refused", Result: sqlite.SQLITE_CANTOPEN})
		return sqlite.SQLITE_CANTOPEN
	}
	name := libc.GoString(zName)
	nativeSize := (*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FszOsFile
	native := libc.Xmalloc(tls, libc.Tsize_t(nativeSize))
	if native == 0 {
		return sqlite.SQLITE_NOMEM
	}
	libc.Xmemset(tls, native, 0, uint64(nativeSize))
	open := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxOpen}))
	rc := open(tls, a.defaultV, zName, native, flags, pOut)
	if rc != sqlite.SQLITE_OK {
		closeRC := closeNative(tls, native)
		libc.Xfree(tls, native)
		a.record(Event{Kind: "native_open_failed", Role: role, Result: rc})
		if closeRC != sqlite.SQLITE_OK {
			a.record(Event{Kind: "native_open_cleanup_incomplete", Role: role, Result: closeRC})
		}
		return rc
	}
	if (*sqlite.Tsqlite3_file)(ptrFromUintptr(native)).FpMethods == 0 {
		libc.Xfree(tls, native)
		return sqlite.SQLITE_CANTOPEN
	}
	uf := (*sqlite.TunixFile)(ptrFromUintptr(native))
	fd := int(uf.Fh)
	dev, ino, err := identity(fd)
	if err != nil {
		closeRC := closeNative(tls, native)
		libc.Xfree(tls, native)
		a.record(Event{Kind: "identity_failed", Role: role, Result: sqlite.SQLITE_IOERR})
		if closeRC != sqlite.SQLITE_OK {
			a.record(Event{Kind: "identity_cleanup_incomplete", Role: role, Result: closeRC})
		}
		return sqlite.SQLITE_IOERR
	}
	pin, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		closeRC := closeNative(tls, native)
		libc.Xfree(tls, native)
		a.record(Event{Kind: "pin_failed", Role: role, Result: sqlite.SQLITE_IOERR})
		if closeRC != sqlite.SQLITE_OK {
			a.record(Event{Kind: "pin_cleanup_incomplete", Role: role, Result: closeRC})
		}
		return sqlite.SQLITE_IOERR
	}
	a.mu.Lock()
	if len(a.pins) >= maxPinnedObjects {
		a.mu.Unlock()
		_ = unix.Close(pin)
		closeNative(tls, native)
		libc.Xfree(tls, native)
		a.record(Event{Kind: "pin_budget_refused", Role: role, Object: objectID(), Result: sqlite.SQLITE_FULL})
		return sqlite.SQLITE_CANTOPEN
	}
	a.pins = append(a.pins, pin)
	a.mu.Unlock()
	obj := objectID()
	fixture := fixtureName(name)
	reg := Registration{Sequence: a.sequence.Add(1), Role: role, Name: fixture, Flags: flags, Device: dev, Inode: ino}
	a.record(Event{Kind: "registration_requested", Role: role, Fixture: fixture, Object: obj, Matched: true, Result: sqlite.SQLITE_OK})
	gateCtx, gateCancel := context.WithTimeout(a.ctx, 5*time.Second)
	d, err := a.gate(gateCtx, reg)
	gateErr := gateCtx.Err()
	gateCancel()
	if err == nil && gateErr != nil {
		err = gateErr
	}
	if err != nil || d != Allow {
		closeRC := closeNative(tls, native)
		libc.Xfree(tls, native)
		result := int32(sqlite.SQLITE_CANTOPEN)
		a.record(Event{Kind: "registration_refused", Role: role, Fixture: fixture, Object: obj, Result: result})
		if closeRC != sqlite.SQLITE_OK {
			a.record(Event{Kind: "registration_cleanup_incomplete", Role: role, Object: obj, Result: closeRC})
		}
		return result
	}
	devNow, inoNow, err := identity(fd)
	pinDev, pinIno, pinErr := identity(pin)
	if err != nil || pinErr != nil || devNow != dev || inoNow != ino || pinDev != dev || pinIno != ino {
		closeRC := closeNative(tls, native)
		libc.Xfree(tls, native)
		if closeRC != sqlite.SQLITE_OK {
			a.record(Event{Kind: "identity_revalidation_cleanup_incomplete", Role: role, Object: obj, Result: closeRC})
		}
		return sqlite.SQLITE_IOERR
	}
	stateID := uintptr(nextID.Add(1))
	fs := &fileState{owner: a, real: native, wrapper: pFile, role: role, fixture: fixture, object: obj, dev: dev, ino: ino, pin: pin, allowed: true}
	a.mu.Lock()
	a.files[stateID] = fs
	a.mu.Unlock()
	globalMu.Lock()
	globalFiles[stateID] = fs
	globalMu.Unlock()
	w := (*wrappedFile)(ptrFromUintptr(pFile))
	*w = wrappedFile{methods: a.methods, state: stateID}
	a.record(Event{Kind: "registered", Role: role, Fixture: fixture, Object: obj, Matched: true, Result: sqlite.SQLITE_OK})
	return sqlite.SQLITE_OK
}

func fixtureName(name string) string { // retain only a basename for synthetic fixtures
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}

func closeNative(tls *libc.TLS, native uintptr) int32 {
	f := (*sqlite.Tsqlite3_file)(ptrFromUintptr(native))
	if f.FpMethods == 0 {
		return sqlite.SQLITE_OK
	}
	m := (*sqlite.Tsqlite3_io_methods)(ptrFromUintptr(f.FpMethods))
	closeFn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxClose}))
	return closeFn(tls, native)
}

func nativeMethod(fs *fileState, offset uintptr) uintptr {
	f := (*sqlite.Tsqlite3_file)(ptrFromUintptr(fs.real))
	return *(*uintptr)(ptrFromUintptr(f.FpMethods + offset))
}
func fileClose(tls *libc.TLS, p uintptr) int32 {
	fs, ok := file(p)
	if !ok {
		return sqlite.SQLITE_IOERR_CLOSE
	}
	fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxClose))}))
	rc := fn(tls, fs.real)
	globalMu.Lock()
	delete(globalFiles, (*wrappedFile)(ptrFromUintptr(p)).state)
	globalMu.Unlock()
	fs.owner.mu.Lock()
	delete(fs.owner.files, (*wrappedFile)(ptrFromUintptr(p)).state)
	fs.owner.mu.Unlock()
	(*wrappedFile)(ptrFromUintptr(p)).methods = 0
	libc.Xfree(tls, fs.real)
	fs.owner.record(Event{Kind: "close", Role: fs.role, Fixture: fs.fixture, Object: fs.object, Matched: true, Result: rc})
	return rc
}
func fileRead(tls *libc.TLS, p, b uintptr, n int32, o sqlite.Tsqlite3_int64) int32 {
	return delegateRead(tls, p, b, n, o)
}
func delegateRead(tls *libc.TLS, p, b uintptr, n int32, o sqlite.Tsqlite3_int64) int32 {
	fs, ok := file(p)
	if !ok {
		return sqlite.SQLITE_IOERR_READ
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, sqlite.Tsqlite3_int64) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxRead))}))
	return fn(tls, fs.real, b, n, o)
}
func fileWrite(tls *libc.TLS, p, b uintptr, n int32, o sqlite.Tsqlite3_int64) int32 {
	fs, ok := file(p)
	if !ok {
		return sqlite.SQLITE_IOERR_WRITE
	}
	dev, ino, err := identity(int((*sqlite.TunixFile)(ptrFromUintptr(fs.real)).Fh))
	pinDev, pinIno, pinErr := identity(fs.pin)
	matched := err == nil && pinErr == nil && dev == fs.dev && ino == fs.ino && pinDev == fs.dev && pinIno == fs.ino && fs.allowed
	if !matched {
		fs.owner.record(Event{Kind: "write_identity_refused", Role: fs.role, Fixture: fs.fixture, Object: fs.object, Result: sqlite.SQLITE_IOERR_WRITE})
		return sqlite.SQLITE_IOERR_WRITE
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, sqlite.Tsqlite3_int64) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxWrite))}))
	rc := fn(tls, fs.real, b, n, o)
	fs.owner.record(Event{Kind: "write", Role: fs.role, Fixture: fs.fixture, Object: fs.object, Matched: matched, Result: rc})
	return rc
}
func fileTruncate(tls *libc.TLS, p uintptr, n sqlite.Tsqlite3_int64) int32 {
	fs, ok := file(p)
	if !ok {
		return sqlite.SQLITE_IOERR_TRUNCATE
	}
	dev, ino, err := identity(int((*sqlite.TunixFile)(ptrFromUintptr(fs.real)).Fh))
	pinDev, pinIno, pinErr := identity(fs.pin)
	matched := err == nil && pinErr == nil && dev == fs.dev && ino == fs.ino && pinDev == fs.dev && pinIno == fs.ino && fs.allowed
	if !matched {
		fs.owner.record(Event{Kind: "truncate_identity_refused", Role: fs.role, Fixture: fs.fixture, Object: fs.object, Result: sqlite.SQLITE_IOERR_TRUNCATE})
		return sqlite.SQLITE_IOERR_TRUNCATE
	}
	fn := *(*func(*libc.TLS, uintptr, sqlite.Tsqlite3_int64) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxTruncate))}))
	rc := fn(tls, fs.real, n)
	fs.owner.record(Event{Kind: "truncate", Role: fs.role, Fixture: fs.fixture, Object: fs.object, Matched: matched, Result: rc})
	return rc
}

func fileSync(tls *libc.TLS, p uintptr, flags int32) int32 {
	return callFileInt(tls, p, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxSync), flags, sqlite.SQLITE_IOERR_FSYNC)
}
func fileSize(tls *libc.TLS, p, out uintptr) int32 {
	fs, ok := file(p)
	if !ok {
		return sqlite.SQLITE_IOERR_FSTAT
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxFileSize))}))
	return fn(tls, fs.real, out)
}
func callFileInt(tls *libc.TLS, p, off uintptr, arg int32, failure int32) int32 {
	fs, ok := file(p)
	if !ok {
		return failure
	}
	fn := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, off)}))
	return fn(tls, fs.real, arg)
}
func fileLock(tls *libc.TLS, p uintptr, n int32) int32 {
	return callFileInt(tls, p, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxLock), n, sqlite.SQLITE_IOERR_LOCK)
}
func fileUnlock(tls *libc.TLS, p uintptr, n int32) int32 {
	return callFileInt(tls, p, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxUnlock), n, sqlite.SQLITE_IOERR_UNLOCK)
}
func fileCheckReserved(tls *libc.TLS, p, out uintptr) int32 {
	fs, ok := file(p)
	if !ok {
		return sqlite.SQLITE_IOERR_CHECKRESERVEDLOCK
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxCheckReservedLock))}))
	return fn(tls, fs.real, out)
}
func fileControl(tls *libc.TLS, p, op, arg uintptr) int32 { return sqlite.SQLITE_NOTFOUND }
func fileSectorSize(tls *libc.TLS, p uintptr) int32 {
	fs, ok := file(p)
	if !ok {
		return 0
	}
	fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxSectorSize))}))
	return fn(tls, fs.real)
}
func fileDeviceCharacteristics(tls *libc.TLS, p uintptr) int32 {
	fs, ok := file(p)
	if !ok {
		return 0
	}
	fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{nativeMethod(fs, unsafe.Offsetof(sqlite.Tsqlite3_io_methods{}.FxDeviceCharacteristics))}))
	return fn(tls, fs.real)
}
func fileShmMap(tls *libc.TLS, p uintptr, region, size int32, extend int32, out uintptr) int32 {
	if out != 0 {
		*(*uintptr)(ptrFromUintptr(out)) = 0
	}
	if fs, ok := file(p); ok {
		fs.owner.record(Event{Kind: "shm_map_refused", Role: fs.role, Object: fs.object, Result: sqlite.SQLITE_IOERR_SHMMAP})
	}
	return sqlite.SQLITE_IOERR_SHMMAP
}
func fileShmLock(tls *libc.TLS, p uintptr, offset, count, flags int32) int32 {
	return sqlite.SQLITE_IOERR_SHMLOCK
}
func fileShmBarrier(tls *libc.TLS, p uintptr) {}
func fileShmUnmap(tls *libc.TLS, p uintptr, deleteFlag int32) int32 {
	return sqlite.SQLITE_IOERR_SHMOPEN
}

func vfsDelete(tls *libc.TLS, pVfs, z uintptr, syncDir int32) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a != nil {
		a.record(Event{Kind: "delete_refused", Result: sqlite.SQLITE_IOERR_DELETE})
	}
	return sqlite.SQLITE_IOERR_DELETE
}
func vfsAccess(tls *libc.TLS, pVfs, z uintptr, flags int32, out uintptr) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil {
		return sqlite.SQLITE_IOERR_ACCESS
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxAccess}))
	return fn(tls, a.defaultV, z, flags, out)
}
func vfsFullPathname(tls *libc.TLS, pVfs, z uintptr, n int32, out uintptr) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil {
		return sqlite.SQLITE_CANTOPEN
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxFullPathname}))
	return fn(tls, a.defaultV, z, n, out)
}
func vfsDlError(tls *libc.TLS, pVfs uintptr, n int32, z uintptr) {}
func vfsRandomness(tls *libc.TLS, pVfs uintptr, n int32, out uintptr) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil {
		return 0
	}
	fn := *(*func(*libc.TLS, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxRandomness}))
	return fn(tls, a.defaultV, n, out)
}
func vfsSleep(tls *libc.TLS, pVfs uintptr, n int32) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil {
		return 0
	}
	fn := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxSleep}))
	return fn(tls, a.defaultV, n)
}
func vfsCurrentTime(tls *libc.TLS, pVfs uintptr, out uintptr) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil {
		return sqlite.SQLITE_ERROR
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{(*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxCurrentTime}))
	return fn(tls, a.defaultV, out)
}
func vfsCurrentTimeInt64(tls *libc.TLS, pVfs uintptr, out uintptr) int32 {
	a := adapter((*sqlite.Tsqlite3_vfs)(ptrFromUintptr(pVfs)).FpAppData)
	if a == nil {
		return sqlite.SQLITE_ERROR
	}
	f := (*sqlite.Tsqlite3_vfs)(ptrFromUintptr(a.defaultV)).FxCurrentTimeInt64
	if f == 0 {
		return sqlite.SQLITE_ERROR
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{f}))
	return fn(tls, a.defaultV, out)
}
func vfsLastError(tls *libc.TLS, pVfs uintptr, n int32, z uintptr) int32 { return 0 }

func (a *Adapter) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("close context is required")
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	if a.closing {
		a.mu.Unlock()
		return errors.New("VFS shutdown is already in progress")
	}
	if len(a.files) != 0 || a.inflight != 0 {
		n := len(a.files)
		inflight := a.inflight
		a.mu.Unlock()
		return fmt.Errorf("VFS still has %d open file(s) and %d opening callback(s)", n, inflight)
	}
	a.closing = true
	pins := append([]int(nil), a.pins...)
	a.mu.Unlock()
	select {
	case <-ctx.Done():
		a.mu.Lock()
		a.closing = false
		a.mu.Unlock()
		return ctx.Err()
	default:
	}
	a.cancel()
	if rc := sqlite.Xsqlite3_vfs_unregister(a.tls, a.vfs); rc != sqlite.SQLITE_OK {
		a.mu.Lock()
		a.closing = false
		a.mu.Unlock()
		return fmt.Errorf("unregister VFS: %d", rc)
	}
	globalMu.Lock()
	delete(global, a.registry)
	globalMu.Unlock()
	var closeErrs []error
	for _, fd := range pins {
		if err := unix.Close(fd); err != nil {
			closeErrs = append(closeErrs, err)
		}
	}
	a.mu.Lock()
	a.pins = nil
	a.closed = true
	a.closing = false
	a.mu.Unlock()
	a.free()
	runtime.KeepAlive(a)
	return errors.Join(closeErrs...)
}
