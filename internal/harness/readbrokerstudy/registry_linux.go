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

//go:build linux

package readbrokerstudy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

var (
	errInvalidRegistration = errors.New("protected resource registration is invalid")
	errRegistryClosed      = errors.New("protected resource registry is closed")
	errPolicyScanLimit     = errors.New("protected directory scan exceeded its entry bound")
)

const (
	maxProtectedDirectoryDepth   = 64
	maxProtectedDirectoryEntries = 4096
)

type registryEntry struct {
	registration   OwnerRegistration
	identity       FileIdentity
	locationDevice uint64
	pin            *os.File
}

// Registry retains pinned objects for the registry lifetime. Location rules
// are lexical, component-aware policy rules; they do not freeze a host
// namespace against a concurrent independent writer.
type Registry struct {
	mu         sync.RWMutex
	entries    []registryEntry
	byIdentity map[FileIdentity]ResourceClass
	generation uint64
	closed     bool
	pinLimit   int
	locLimit   int
}

var _ PolicyRegistry = (*Registry)(nil)

func NewRegistry() (*Registry, error) {
	return &Registry{
		byIdentity: make(map[FileIdentity]ResourceClass),
		pinLimit:   MaxPinnedObjects,
		locLimit:   MaxPolicyLocations,
	}, nil
}

func (r *Registry) Register(reg OwnerRegistration) error {
	return r.RegisterRotation(reg.Owner, []OwnerRegistration{reg})
}

// RegisterRotation atomically adds the next owner records. Existing records
// and pins are retained until Close so old objects remain protected across
// replacement/rotation. Callers must register a sensitive temporary path and
// object before writing sensitive bytes there.
func (r *Registry) RegisterRotation(owner string, registrations []OwnerRegistration) error {
	if owner == "" || len(registrations) == 0 {
		return errInvalidRegistration
	}
	staged := make([]registryEntry, 0, len(registrations))
	for _, reg := range registrations {
		if reg.Owner != owner {
			closeEntries(staged)
			return errInvalidRegistration
		}
		entry, err := stageRegistration(reg)
		if err != nil {
			closeEntries(staged)
			return err
		}
		staged = append(staged, entry)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		closeEntries(staged)
		return errRegistryClosed
	}
	newLocations, newPins := 0, 0
	for i, entry := range staged {
		if !r.hasLocationLocked(entry.registration) && !hasStagedLocation(staged[:i], entry.registration) {
			newLocations++
		}
		if entry.pin != nil {
			if _, exists := r.byIdentity[entry.identity]; !exists && !hasStagedIdentity(staged[:i], entry.identity) {
				newPins++
			}
		}
	}
	if r.locationCountLocked()+newLocations > r.locLimit || len(r.byIdentity)+newPins > r.pinLimit {
		closeEntries(staged)
		return ErrPinBudget
	}
	changed := false
	for _, entry := range staged {
		if r.hasExactEntryLocked(entry.registration, entry.identity) {
			if entry.pin != nil {
				_ = entry.pin.Close()
			}
			continue
		}
		changed = true
		if entry.pin != nil {
			if oldClass, exists := r.byIdentity[entry.identity]; exists {
				_ = entry.pin.Close()
				entry.pin = nil
				r.byIdentity[entry.identity] = stricterClass(oldClass, entry.registration.Class)
			} else {
				r.byIdentity[entry.identity] = entry.registration.Class
			}
		}
		r.entries = append(r.entries, entry)
	}
	if changed {
		r.generation++
	}
	return nil
}

func hasStagedIdentity(entries []registryEntry, identity FileIdentity) bool {
	for _, entry := range entries {
		if entry.pin != nil && entry.identity == identity {
			return true
		}
	}
	return false
}

func hasStagedLocation(entries []registryEntry, registration OwnerRegistration) bool {
	for _, entry := range entries {
		if entry.registration == registration {
			return true
		}
	}
	return false
}

func closeEntries(entries []registryEntry) {
	for _, entry := range entries {
		if entry.pin != nil {
			_ = entry.pin.Close()
		}
	}
}

func (r *Registry) hasLocationLocked(candidate OwnerRegistration) bool {
	for _, entry := range r.entries {
		if sameLocation(entry.registration, candidate) {
			return true
		}
	}
	return false
}

func (r *Registry) hasExactEntryLocked(candidate OwnerRegistration, identity FileIdentity) bool {
	for _, entry := range r.entries {
		if entry.registration == candidate && entry.identity == identity {
			return true
		}
	}
	return false
}

func (r *Registry) locationCountLocked() int {
	seen := make(map[OwnerRegistration]struct{}, len(r.entries))
	for _, entry := range r.entries {
		seen[entry.registration] = struct{}{}
	}
	return len(seen)
}

func sameLocation(a, b OwnerRegistration) bool {
	return a.Owner == b.Owner && a.Class == b.Class && a.Path == b.Path && a.Directory == b.Directory
}

func (r *Registry) View() PolicyView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	view := PolicyView{Generation: r.generation, Entries: make([]PolicyEntryView, 0, len(r.entries))}
	for _, entry := range r.entries {
		view.Entries = append(view.Entries, PolicyEntryView{
			Owner: entry.registration.Owner, Class: entry.registration.Class,
			Path: entry.registration.Path, Directory: entry.registration.Directory,
			Identity: entry.identity,
		})
	}
	return view
}

// Classify applies both object-identity and registered-location rules. A
// protected directory's pinned identity protects that directory object only;
// it does not enumerate current/future descendants or retain child identities
// after they move elsewhere.
func (r *Registry) Classify(path string, identity FileIdentity) (ResourceClass, bool) {
	class, protected := r.ClassifyWithAncestors(path, identity, nil)
	if protected {
		return class, true
	}
	parentFD, err := openAbsolute(filepath.Dir(path), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return class, false
	}
	defer unix.Close(parentFD)
	ancestors, err := directoryAncestors(parentFD)
	if err != nil {
		return class, false
	}
	return r.ClassifyWithAncestors(path, identity, ancestors)
}

// ClassifyWithAncestors also matches a target beneath a pinned protected
// directory object, including when that directory object has moved. The
// ancestry sample identifies only directories still on the target's parent
// chain; it is not a census or pin set for their descendants.
func (r *Registry) ClassifyWithAncestors(path string, identity FileIdentity, ancestors []FileIdentity) (ResourceClass, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var matched ResourceClass
	if class, ok := r.byIdentity[identity]; ok {
		matched = stricterClass(matched, class)
	}
	for _, entry := range r.entries {
		if pathWithin(entry.registration.Path, path, entry.registration.Directory) {
			matched = stricterClass(matched, entry.registration.Class)
		}
		if entry.registration.Directory {
			for _, ancestor := range ancestors {
				if ancestor == entry.identity {
					matched = stricterClass(matched, entry.registration.Class)
					break
				}
			}
		}
	}
	return matched, matched != ""
}

// InspectRegisteredLocations performs a bounded checkpoint scan of exact
// registered file names and registered directories. It can discover that a
// prepared object has since been moved/aliased into a protected location. The
// scan is not atomic with an independent namespace writer; callers must treat
// it only as a refusal checkpoint, never as proof against a later transition.
func (r *Registry) InspectRegisteredLocations(candidate FileIdentity) (ResourceClass, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return "", false, errRegistryClosed
	}
	var matched ResourceClass
	remaining := maxProtectedDirectoryEntries
	for _, entry := range r.entries {
		if !entry.registration.Directory {
			fd, err := openAbsolute(entry.registration.Path, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW)
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			if err != nil {
				return "", false, fmt.Errorf("inspect protected location %s: %w", describeLocation(entry.registration), err)
			}
			var stat unix.Stat_t
			statErr := unix.Fstat(fd, &stat)
			_ = unix.Close(fd)
			if statErr != nil {
				return "", false, statErr
			}
			if identityFromStat(&stat) == candidate {
				matched = stricterClass(matched, entry.registration.Class)
			}
			continue
		}

		// Scan both the pinned directory object and whatever currently occupies
		// its registered location. A directory identity is not a descendant
		// inventory; the bounded walk is only a snapshot at this checkpoint.
		if entry.pin != nil {
			found, consumed, err := scanDirectoryForIdentity(int(entry.pin.Fd()), candidate, remaining)
			remaining -= consumed
			if err != nil {
				return "", false, fmt.Errorf("scan pinned protected directory %s: %w", describeLocation(entry.registration), err)
			}
			if found {
				matched = stricterClass(matched, entry.registration.Class)
			}
		}
		locationFD, err := openAbsolute(entry.registration.Path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("open protected directory location %s: %w", describeLocation(entry.registration), err)
		}
		found, consumed, scanErr := scanDirectoryForIdentity(locationFD, candidate, remaining)
		_ = unix.Close(locationFD)
		remaining -= consumed
		if scanErr != nil {
			return "", false, fmt.Errorf("scan protected directory location %s: %w", describeLocation(entry.registration), scanErr)
		}
		if found {
			matched = stricterClass(matched, entry.registration.Class)
		}
	}
	return matched, matched != "", nil
}

// DirectoryCoverageIncomplete conservatively refuses unpinned file objects
// that share a device with any protected directory object or registered
// directory location. A directory pin is not a descendant inventory: on the
// same filesystem, an unregistered child could have been moved or hard-linked
// outside the directory before preparation. Exact pinned regular-file
// identities are exempt only when their registration is at least as strong
// as every potentially covering protected-directory registration.
func (r *Registry) DirectoryCoverageIncomplete(candidate FileIdentity) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return true
	}
	if candidate.Type == unix.S_IFREG {
		if candidateClass, pinned := r.byIdentity[candidate]; pinned {
			// A direct registration must not weaken a stronger directory rule.
			// In particular, a provider-source pin nested under a settings or
			// credential directory remains protected if it is moved elsewhere.
			for _, entry := range r.entries {
				if !entry.registration.Directory || classPriority(candidateClass) >= classPriority(entry.registration.Class) {
					continue
				}
				if candidate.Device == entry.identity.Device || (entry.locationDevice != 0 && candidate.Device == entry.locationDevice) {
					return true
				}
				fd, err := openAbsolute(entry.registration.Path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
				if errors.Is(err, unix.ENOENT) {
					continue
				}
				if err != nil {
					return true
				}
				var stat unix.Stat_t
				statErr := unix.Fstat(fd, &stat)
				_ = unix.Close(fd)
				if statErr != nil || candidate.Device == uint64(stat.Dev) {
					return true
				}
			}
			return false
		}
	}
	for _, entry := range r.entries {
		if !entry.registration.Directory {
			continue
		}
		if candidate.Device == entry.identity.Device || (entry.locationDevice != 0 && candidate.Device == entry.locationDevice) {
			return true
		}
		fd, err := openAbsolute(entry.registration.Path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return true
		}
		var stat unix.Stat_t
		statErr := unix.Fstat(fd, &stat)
		_ = unix.Close(fd)
		if statErr != nil || candidate.Device == uint64(stat.Dev) {
			return true
		}
	}
	return false
}

func classPriority(class ResourceClass) int {
	switch class {
	case ClassCredential:
		return 4
	case ClassSettings:
		return 3
	case ClassTokenDatabase:
		return 2
	case ClassProvider:
		return 1
	default:
		return 0
	}
}

func scanDirectoryForIdentity(rootFD int, candidate FileIdentity, remaining int) (bool, int, error) {
	if remaining <= 0 {
		return false, 0, errPolicyScanLimit
	}
	return scanDirectoryNode(rootFD, candidate, remaining, 0)
}

func scanDirectoryNode(directoryFD int, candidate FileIdentity, remaining, depth int) (bool, int, error) {
	if depth > maxProtectedDirectoryDepth {
		return false, 0, errors.New("protected directory scan exceeded its depth bound")
	}
	readFD, err := unix.Openat2(directoryFD, ".", &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return false, 0, fmt.Errorf("open directory for enumeration: %w", err)
	}
	directory := os.NewFile(uintptr(readFD), "protected-directory-scan")
	defer directory.Close()
	consumed := 0
	for {
		batch, readErr := directory.ReadDir(128)
		for _, child := range batch {
			consumed++
			if consumed > remaining {
				return false, consumed, errPolicyScanLimit
			}
			if child.Type()&os.ModeSymlink != 0 {
				return false, consumed, errors.New("symlink in protected directory makes the bounded scan ambiguous")
			}
			childFD, openErr := unix.Openat2(directoryFD, child.Name(), &unix.OpenHow{
				Flags:   unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW,
				Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
			})
			if openErr != nil {
				return false, consumed, fmt.Errorf("open child %q: %w", child.Name(), openErr)
			}
			var stat unix.Stat_t
			statErr := unix.Fstat(childFD, &stat)
			childID := identityFromStat(&stat)
			if statErr != nil {
				_ = unix.Close(childFD)
				return false, consumed, statErr
			}
			if childID == candidate {
				_ = unix.Close(childFD)
				return true, consumed, nil
			}
			if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
				_ = unix.Close(childFD)
				return false, consumed, errors.New("symlink in protected directory makes the bounded scan ambiguous")
			}
			if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
				found, nestedConsumed, nestedErr := scanDirectoryNode(childFD, candidate, remaining-consumed, depth+1)
				_ = unix.Close(childFD)
				consumed += nestedConsumed
				if nestedErr != nil {
					return false, consumed, fmt.Errorf("inspect child directory %q: %w", child.Name(), nestedErr)
				}
				if found {
					return true, consumed, nil
				}
				continue
			}
			_ = unix.Close(childFD)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return false, consumed, fmt.Errorf("read directory entries: %w", readErr)
		}
	}
	return false, consumed, nil
}

func stricterClass(a, b ResourceClass) ResourceClass {
	if classPriority(b) > classPriority(a) {
		return b
	}
	return a
}

func pathWithin(rule, candidate string, directory bool) bool {
	if rule == candidate {
		return true
	}
	if !directory {
		return false
	}
	rel, err := filepath.Rel(rule, candidate)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var errs []error
	for i := range r.entries {
		if r.entries[i].pin != nil {
			if err := r.entries[i].pin.Close(); err != nil {
				errs = append(errs, err)
			}
			r.entries[i].pin = nil
		}
	}
	return errors.Join(errs...)
}

func stageRegistration(reg OwnerRegistration) (registryEntry, error) {
	if reg.Owner == "" || !validClass(reg.Class) || !filepath.IsAbs(reg.Path) || filepath.Clean(reg.Path) != reg.Path || reg.Path == string(filepath.Separator) || strings.ContainsRune(reg.Path, '\x00') {
		return registryEntry{}, errInvalidRegistration
	}
	entry := registryEntry{registration: reg}
	fd, err := openAbsolute(reg.Path, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW)
	if err != nil {
		if !errors.Is(err, unix.ENOENT) {
			return registryEntry{}, fmt.Errorf("register protected location: %w", err)
		}
		parentDevice, parentErr := existingParentDevice(reg.Path)
		if parentErr != nil {
			return registryEntry{}, fmt.Errorf("register protected location parent: %w", parentErr)
		}
		if reg.Directory {
			entry.locationDevice = parentDevice
		}
		return entry, nil // A location rule protects a not-yet-created leaf.
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return registryEntry{}, err
	}
	wantType := uint32(unix.S_IFREG)
	if reg.Directory {
		wantType = unix.S_IFDIR
	}
	if stat.Mode&unix.S_IFMT != wantType {
		_ = unix.Close(fd)
		return registryEntry{}, fmt.Errorf("registered resource has unexpected type: %s", reg.Path)
	}
	entry.identity = identityFromStat(&stat)
	if reg.Directory {
		entry.locationDevice = entry.identity.Device
	}
	entry.pin = os.NewFile(uintptr(fd), "protected-identity-pin")
	return entry, nil
}

func validClass(class ResourceClass) bool {
	switch class {
	case ClassCredential, ClassSettings, ClassTokenDatabase, ClassProvider:
		return true
	default:
		return false
	}
}

func existingParentDevice(target string) (uint64, error) {
	parent := filepath.Dir(target)
	fd, err := openAbsolute(parent, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return 0, err
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(fd, &stat)
	closeErr := unix.Close(fd)
	if statErr != nil {
		return 0, statErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return uint64(stat.Dev), nil
}

func openAbsolute(name string, flags int) (int, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return -1, errInvalidRegistration
	}
	rootFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	relative := strings.TrimPrefix(name, "/")
	if relative == "" {
		relative = "."
	}
	fd, err := unix.Openat2(rootFD, relative, &unix.OpenHow{
		Flags:   uint64(flags),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	_ = unix.Close(rootFD)
	return fd, err
}

func identityFromStat(stat *unix.Stat_t) FileIdentity {
	return FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Type: stat.Mode & unix.S_IFMT}
}

func identityFromFile(file *os.File) (FileIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return FileIdentity{}, err
	}
	return identityFromStat(&stat), nil
}

func describeLocation(reg OwnerRegistration) string {
	return fmt.Sprintf("%s:%s:%s", reg.Owner, reg.Class, reg.Path)
}
